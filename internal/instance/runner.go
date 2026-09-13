// Copyright 2026 InsightOS
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package instance

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"insightos.cn/semantic-robot-deployment/internal/abilityframework"
	"insightos.cn/semantic-robot-deployment/internal/bundle"
)

type managedProcess struct {
	name string
	cmd  *exec.Cmd
	done chan error
}

var errSafetyUnconfirmed = errors.New("Robot 安全停止证据未确认")

type pilotStopReport struct {
	ExecutionID       string    `json:"execution_id"`
	Safe              bool      `json:"safe"`
	HoldConfirmed     bool      `json:"hold_confirmed"`
	ActiveInvocations []string  `json:"active_invocations"`
	Reason            string    `json:"reason"`
	FinishedAt        time.Time `json:"finished_at"`
}

func pilotStopReportPath(instanceDirectory string) string {
	return filepath.Join(instanceDirectory, "pilot", "stop-result.json")
}

func startProcess(name, executable string, arguments []string, directory, logPath string, environment []string) (*managedProcess, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o750); err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, err
	}
	command := exec.Command(executable, arguments...)
	// supervisor 必须独占终端的信号边界。否则用户在调试终端按 Ctrl-C 时，
	// SIGINT 会同时到达 AbilityFramework、Pilot 和 supervisor，子进程会在
	// Pilot 提交 hold 证据之前退出，彻底破坏固定的安全停止顺序。每个直接
	// 受管进程使用独立进程组后，终端信号只唤醒 supervisor；后续仍由这里
	// 按 Pilot → Ability → AbilityFramework 的顺序发送显式停止请求。
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Dir = directory
	command.Env = environment
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("启动 %s 失败: %w", name, err)
	}
	process := &managedProcess{name: name, cmd: command, done: make(chan error, 1)}
	go func() {
		err := command.Wait()
		_ = logFile.Close()
		process.done <- err
	}()
	return process, nil
}

func (process *managedProcess) terminate(timeout time.Duration, requireCleanExit bool) (bool, error) {
	if process == nil || process.cmd.Process == nil {
		return false, nil
	}
	// startProcess creates a dedicated process group. After the caller has
	// completed Pilot hold and Ability stop, retire the whole owned group:
	// macOS has no Linux PR_SET_PDEATHSIG to reap Ability Python children.
	deadline := time.Now().Add(timeout)
	group := -process.cmd.Process.Pid
	if err := syscall.Kill(group, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return false, fmt.Errorf("终止 %s 进程组: %w", process.name, err)
	}
	select {
	case err := <-process.done:
		if requireCleanExit && err != nil {
			return false, fmt.Errorf("%s 安全停止返回非零状态: %w", process.name, err)
		}
		for {
			groupErr := syscall.Kill(group, 0)
			if errors.Is(groupErr, syscall.ESRCH) {
				return err == nil, nil
			}
			if groupErr != nil {
				return false, fmt.Errorf("检查 %s 子进程组: %w", process.name, groupErr)
			}
			if time.Now().After(deadline) {
				_ = syscall.Kill(group, syscall.SIGKILL)
				return false, fmt.Errorf("%s 子进程未在 %s 内退出", process.name, timeout)
			}
			time.Sleep(10 * time.Millisecond)
		}
	case <-time.After(timeout):
		_ = syscall.Kill(group, syscall.SIGKILL)
		<-process.done
		return false, fmt.Errorf("%s 未在 %s 内安全退出，已强制终止", process.name, timeout)
	}
}

// requestGracefulStop 只请求 Pilot 自行完成 Worker、Ability 与 SDK hold。
// 超时后不能强杀 Pilot：此时 Robot 状态仍未知，AbilityFramework 必须保留供对账。
func (process *managedProcess) requestGracefulStop(timeout time.Duration) error {
	if process == nil || process.cmd.Process == nil {
		return errors.New("semantic-pilot 进程不存在")
	}
	if err := process.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	select {
	case err := <-process.done:
		if err != nil {
			return fmt.Errorf("%s 安全停止返回非零状态: %w", process.name, err)
		}
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("%s 未在 %s 内返回安全停止证据", process.name, timeout)
	}
}

func readPilotStopReport(path string) (pilotStopReport, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return pilotStopReport{}, err
	}
	var report pilotStopReport
	if err := json.Unmarshal(content, &report); err != nil {
		return pilotStopReport{}, fmt.Errorf("解析 Pilot 停止证据: %w", err)
	}
	if !report.Safe || !report.HoldConfirmed || report.FinishedAt.IsZero() {
		return report, errors.New("Pilot 未确认 Robot hold 或无活动物理动作")
	}
	return report, nil
}

func parseHTTPEndpoint(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("HTTP endpoint 无效: %s", value)
	}
	if parsed.Port() == "" {
		return nil, fmt.Errorf("AbilityFramework endpoint 必须包含端口: %s", value)
	}
	return parsed, nil
}

// Run 严格按 AbilityFramework、七类 Ability、Pilot 的顺序启动一个实例。
// bundle 始终只读并提供共享 Python；运行状态、数据库和日志只写 instanceDirectory。
func Run(ctx context.Context, instanceDirectory string) (runErr error) {
	instanceDirectory, err := filepath.Abs(instanceDirectory)
	if err != nil {
		return err
	}
	lockFile, err := os.OpenFile(filepath.Join(instanceDirectory, "run", "instance.lock"), os.O_CREATE|os.O_RDWR, 0o640)
	if err != nil {
		return err
	}
	defer lockFile.Close()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("实例已由另一个 semantic-robot-instance 进程管理")
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	config, err := LoadConfig(filepath.Join(instanceDirectory, "instance.yaml"))
	if err != nil {
		return err
	}
	opened, err := bundle.Open(config.Spec.Bundle)
	if err != nil {
		return err
	}
	if err := opened.Manifest.Supports(config.Spec.Robot.Model,
		config.Spec.Robot.Backend, config.Spec.Robot.BackendProfile); err != nil {
		return err
	}
	state, err := ReadState(instanceDirectory)
	if err != nil {
		return err
	}
	if state.Status == StatusStarting || state.Status == StatusRunning || state.Status == StatusStopping {
		return fmt.Errorf("实例当前状态为 %s，拒绝重复启动", state.Status)
	}
	state.Status = StatusStarting
	state.Error = ""
	state.SupervisorPID = os.Getpid()
	state.AbilityFrameworkPID, state.PilotPID = 0, 0
	state.StartedAt = time.Now().UTC()
	state.StoppedAt = time.Time{}
	state.AbilityInstanceIDs = nil
	state.StopEvidence = nil
	if err := writeState(instanceDirectory, &state); err != nil {
		return err
	}
	_ = writePID(filepath.Join(instanceDirectory, "run", "supervisor.pid"), os.Getpid())

	python := opened.Path(opened.Manifest.Spec.Artifacts.PythonExecutable)
	environment := runtimeEnvironment(instanceDirectory, config, opened, python)
	frameworkEnvironment, err := abilityFrameworkEnvironment(instanceDirectory, environment)
	if err != nil {
		return finishFailed(instanceDirectory, state, err)
	}
	var frameworkProcess, pilotProcess *managedProcess
	pilotStarted := false
	var activated []string
	client, err := abilityframework.NewClient(config.Spec.AbilityFramework.Endpoint, nil)
	if err != nil {
		return finishFailed(instanceDirectory, state, err)
	}
	shutdown := func() error {
		state.Status = StatusStopping
		_ = writeState(instanceDirectory, &state)
		var failures []string
		evidence := StopEvidence{
			AbilityStopRequested: len(activated),
			RecordedAt:           time.Now().UTC(),
		}
		if pilotStarted {
			var safetyFailure error
			if pilotProcess == nil {
				safetyFailure = errors.New("semantic-pilot 已异常退出，无法取得安全停止证据")
			} else if err := pilotProcess.requestGracefulStop(opened.Manifest.ShutdownTimeout()); err != nil {
				safetyFailure = err
			} else {
				report, reportErr := readPilotStopReport(pilotStopReportPath(instanceDirectory))
				if reportErr != nil {
					safetyFailure = reportErr
				} else {
					evidence.ExecutionID = report.ExecutionID
					evidence.Safe = report.Safe
					evidence.HoldConfirmed = report.HoldConfirmed
					evidence.ActiveInvocations = report.ActiveInvocations
					evidence.Reason = report.Reason
					evidence.FinishedAt = report.FinishedAt
					evidence.PilotExitedCleanly = true
				}
			}
			if safetyFailure != nil {
				evidence.Reason = safetyFailure.Error()
				state.StopEvidence = &evidence
				_ = writeState(instanceDirectory, &state)
				return fmt.Errorf("%w: %v", errSafetyUnconfirmed, safetyFailure)
			}
		}
		stopContext, cancel := context.WithTimeout(context.Background(), opened.Manifest.ShutdownTimeout())
		defer cancel()
		for index := len(activated) - 1; index >= 0; index-- {
			identifier := activated[index]
			if err := client.Stop(stopContext, identifier); err != nil {
				failures = append(failures, fmt.Sprintf("停止 Ability %s: %v", activated[index], err))
				continue
			}
			if err := client.WaitStopped(stopContext, identifier, opened.Manifest.ShutdownTimeout()); err != nil {
				failures = append(failures, fmt.Sprintf("确认 Ability %s 停止: %v", identifier, err))
			} else {
				evidence.AbilityStopConfirmed++
			}
		}
		if len(failures) > 0 {
			// Keep the framework available for reconciliation when Ability stop
			// has not been confirmed; group termination requires that evidence.
			state.StopEvidence = &evidence
			_ = writeState(instanceDirectory, &state)
			return fmt.Errorf("%w: %s", errSafetyUnconfirmed, strings.Join(failures, "; "))
		}
		if _, err := frameworkProcess.terminate(opened.Manifest.ShutdownTimeout(), false); err != nil {
			failures = append(failures, err.Error())
		}
		state.StopEvidence = &evidence
		_ = writeState(instanceDirectory, &state)
		if len(failures) > 0 {
			return errors.New(strings.Join(failures, "; "))
		}
		return nil
	}

	if config.Spec.AbilityFramework.Managed {
		frameworkProcess, err = startProcess(
			"AbilityFramework", opened.Path(opened.Manifest.Spec.Artifacts.AbilityFramework), nil,
			filepath.Join(instanceDirectory, "ability-framework"),
			filepath.Join(instanceDirectory, "ability-framework", "log", "process.log"), frameworkEnvironment,
		)
		if err != nil {
			return finishFailed(instanceDirectory, state, err)
		}
		state.AbilityFrameworkPID = frameworkProcess.cmd.Process.Pid
		_ = writePID(filepath.Join(instanceDirectory, "run", "ability-framework.pid"), state.AbilityFrameworkPID)
		_ = writeState(instanceDirectory, &state)
	}
	if err := client.WaitReady(ctx, opened.Manifest.ReadinessTimeout()); err != nil {
		_ = shutdown()
		return finishFailed(instanceDirectory, state, err)
	}
	for _, ability := range opened.Manifest.Spec.Artifacts.Abilities {
		if err := client.EnsurePackage(ctx, ability.Template, opened.Path(ability.File), opened.Manifest.ReadinessTimeout()); err != nil {
			_ = shutdown()
			return finishFailed(instanceDirectory, state, fmt.Errorf("准备 Ability %s: %w", ability.Role, err))
		}
	}

	for _, ability := range opened.Manifest.Spec.Artifacts.Abilities {
		// AbilityFramework 的激活接口返回异步 taskId，但当前实现不能可靠接收同一
		// Robot 的七个并发创建请求：请求虽然返回成功，偶发会有一个模板根本不产生
		// instance，最终白等完整 readiness timeout。这里顺序完成“创建并确认 Running”，
		// 不引入重试或懒加载；七类能力通常只增加数秒，却能保证 Pilot 上线前目录完整。
		instance, err := client.Activate(
			ctx, ability.Template, ability.AbilityName, opened.Manifest.ReadinessTimeout(),
		)
		if err != nil {
			_ = shutdown()
			return finishFailed(instanceDirectory, state, fmt.Errorf("启动 Ability %s: %w", ability.Role, err))
		}
		activated = append(activated, instance.InstanceID)
		if _, err := client.WaitHeartbeat(
			ctx, instance.InstanceID, ability.AbilityName, opened.Manifest.ReadinessTimeout(),
		); err != nil {
			_ = shutdown()
			return finishFailed(instanceDirectory, state,
				fmt.Errorf("确认 Ability %s heartbeat: %w", ability.Role, err))
		}
		state.AbilityInstanceIDs = append([]string(nil), activated...)
		_ = writeState(instanceDirectory, &state)
	}
	_ = os.Remove(pilotStopReportPath(instanceDirectory))
	pilotArguments := []string{
		"--profile", filepath.Join(instanceDirectory, "robot-deployment.yaml"),
		"--server-ws", config.Spec.SemanticServer.WebSocketURL,
		"--server-http", config.Spec.SemanticServer.HTTPURL,
		"--access-token", config.Spec.SemanticServer.AccessToken,
		"--pilot-id", config.Spec.Pilot.ID,
		"--data-dir", filepath.Join(instanceDirectory, "pilot"),
		"--skill-storage", filepath.Join(instanceDirectory, "pilot", "skills"),
		"--skill-sdk-source", opened.Path(opened.Manifest.Spec.Artifacts.RobotSkillSDK),
		"--python", python,
		"--pilot-version", opened.Manifest.Metadata.Version,
	}
	pilotProcess, err = startProcess(
		"semantic-pilot", opened.Path(opened.Manifest.Spec.Artifacts.Pilot), pilotArguments,
		instanceDirectory, filepath.Join(instanceDirectory, "pilot", "logs", "pilot.log"), environment,
	)
	if err != nil {
		_ = shutdown()
		return finishFailed(instanceDirectory, state, err)
	}
	pilotStarted = true
	state.PilotPID = pilotProcess.cmd.Process.Pid
	state.Status = StatusRunning
	if err := writeState(instanceDirectory, &state); err != nil {
		_ = shutdown()
		return err
	}
	_ = writePID(filepath.Join(instanceDirectory, "run", "pilot.pid"), state.PilotPID)

	var unexpected error
	select {
	case <-ctx.Done():
	case err := <-pilotProcess.done:
		pilotProcess = nil
		unexpected = fmt.Errorf("semantic-pilot 意外退出: %w", normalizeExitError(err))
	case err := <-frameworkDone(frameworkProcess):
		frameworkProcess = nil
		unexpected = fmt.Errorf("AbilityFramework 意外退出: %w", normalizeExitError(err))
	}
	shutdownErr := shutdown()
	if unexpected != nil {
		if shutdownErr != nil {
			unexpected = fmt.Errorf("%v; 安全关闭失败: %w", unexpected, shutdownErr)
		}
		if errors.Is(unexpected, errSafetyUnconfirmed) {
			return finishInterrupted(instanceDirectory, state, unexpected)
		}
		return finishFailed(instanceDirectory, state, unexpected)
	}
	if shutdownErr != nil {
		if errors.Is(shutdownErr, errSafetyUnconfirmed) {
			return finishInterrupted(instanceDirectory, state, shutdownErr)
		}
		return finishFailed(instanceDirectory, state, shutdownErr)
	}
	state.Status = StatusStopped
	state.SupervisorPID, state.AbilityFrameworkPID, state.PilotPID = 0, 0, 0
	state.StoppedAt = time.Now().UTC()
	state.Error = ""
	return writeState(instanceDirectory, &state)
}

func frameworkDone(process *managedProcess) <-chan error {
	if process != nil {
		return process.done
	}
	return make(chan error)
}

func normalizeExitError(err error) error {
	if err == nil {
		return errors.New("进程正常退出，但实例仍处于运行期")
	}
	return err
}

func finishFailed(instanceDirectory string, state State, failure error) error {
	state.Status = StatusFailed
	state.SupervisorPID, state.AbilityFrameworkPID, state.PilotPID = 0, 0, 0
	state.Error = failure.Error()
	state.StoppedAt = time.Now().UTC()
	if err := writeState(instanceDirectory, &state); err != nil {
		return fmt.Errorf("%v; 写入失败状态: %w", failure, err)
	}
	return failure
}

func finishInterrupted(instanceDirectory string, state State, failure error) error {
	// Robot 状态未知时只结束 supervisor 自身；Pilot、Ability 和
	// AbilityFramework 的 PID 保留下来，供人工确认和后续对账。
	state.Status = StatusInterrupted
	state.SupervisorPID = 0
	state.Error = failure.Error()
	if err := writeState(instanceDirectory, &state); err != nil {
		return fmt.Errorf("%v; 写入 interrupted 状态: %w", failure, err)
	}
	return failure
}

func runtimeEnvironment(instanceDirectory string, config Config, opened bundle.Bundle, python string) []string {
	environment := append([]string(nil), os.Environ()...)
	environment = setEnvironment(environment, "SEMANTIC_ROBOT_CONFIG",
		filepath.Join(instanceDirectory, "robot-deployment.yaml"))
	environment = setEnvironment(environment, "SEMANTIC_ABILITY_EXECUTION_ROOT",
		filepath.Join(instanceDirectory, "executions"))
	environment = setEnvironment(environment, "SEMANTIC_ABILITY_ARTIFACT_ROOT",
		filepath.Join(instanceDirectory, "ability-framework", "artifact-exchange"))
	environment = setEnvironment(environment, "SEMANTIC_ABILITY_PYTHON", python)
	environment = setEnvironment(environment, "SEMANTIC_PILOT_STOP_REPORT",
		pilotStopReportPath(instanceDirectory))
	environment = setEnvironment(environment, "PYTHONDONTWRITEBYTECODE", "1")
	// Bundle 内的 Wheel 是 Robot 运行时唯一 Python 来源。清空宿主 PYTHONPATH 并
	// 禁用用户 site，避免开发机 Conda/ROS 包掩盖类型包漏装依赖。
	environment = setEnvironment(environment, "PYTHONPATH", "")
	environment = setEnvironment(environment, "PYTHONNOUSERSITE", "1")
	environment = setEnvironment(environment, "PYTHONPYCACHEPREFIX",
		filepath.Join(instanceDirectory, "pilot", "python-cache"))
	if config.Spec.Robot.SDKEndpoint != "" {
		environment = setEnvironment(environment, "SEMANTIC_ROBOT_SDK_ENDPOINT", config.Spec.Robot.SDKEndpoint)
	}
	if opened.Manifest.Spec.Templates.ModelRegistry != "" {
		environment = setEnvironment(environment, "MODEL_REGISTRY_PATH",
			filepath.Join(instanceDirectory, "model-registry.json"))
	}
	if filepath.IsAbs(python) {
		environment = setEnvironment(environment, "PATH", filepath.Dir(python)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return environment
}

func abilityFrameworkEnvironment(instanceDirectory string, environment []string) ([]string, error) {
	// AbilityFramework 安装能力包时会先在系统临时目录解包，再把目录原子移动到
	// packages。Linux 的 rename 不能跨文件系统；当 /tmp 是 tmpfs、实例目录位于
	// 磁盘时会直接返回 EXDEV。临时目录放在同一实例内即可保留原子移动语义，且
	// 不需要用户修改 shell 或 Server 的全局临时目录配置。
	temporaryDirectory := filepath.Join(instanceDirectory, "ability-framework", "tmp")
	if err := os.MkdirAll(temporaryDirectory, 0o750); err != nil {
		return nil, fmt.Errorf("创建 AbilityFramework 临时目录: %w", err)
	}
	return setEnvironment(environment, "TMPDIR", temporaryDirectory), nil
}

func setEnvironment(environment []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return append(result, prefix+value)
}

// Stop 向实例 supervisor 发出 SIGTERM。真正的安全关闭顺序由 Run 执行，
// 因而 stop 命令不会绕过 Ability stop 或直接杀死 Robot 进程。
func Stop(ctx context.Context, instanceDirectory string) error {
	state, err := ReadState(instanceDirectory)
	if err != nil {
		return err
	}
	if state.Status == StatusStopped || state.Status == StatusRendered {
		return nil
	}
	if state.SupervisorPID <= 0 || !processAlive(state.SupervisorPID) {
		return errors.New("实例 supervisor 不在线，无法确认安全停止；请检查 status 和设备状态")
	}
	process, err := os.FindProcess(state.SupervisorPID)
	if err != nil {
		return err
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, readErr := ReadState(instanceDirectory)
		if readErr == nil && (current.Status == StatusStopped || current.Status == StatusFailed || current.Status == StatusInterrupted) {
			if current.Status != StatusStopped {
				return fmt.Errorf("实例停止失败: %s", current.Error)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// FollowLog 是 CLI 后续可复用的最小日志读取器；当前 status 不会隐式输出大量日志。
func FollowLog(reader io.Reader, write func(string)) error {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		write(scanner.Text())
	}
	return scanner.Err()
}
