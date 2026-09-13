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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
	"insightos.cn/semantic-robot-deployment/internal/abilityframework"
	"insightos.cn/semantic-robot-deployment/internal/bundle"
)

var testAbilities = []bundle.AbilityArtifact{
	{Role: "navigation", Template: "r1pro-navigation", AbilityName: "R1ProNavigation.V1"},
	{Role: "manipulator_motion", Template: "r1pro-manipulator-motion", AbilityName: "R1ProManipulatorMotion.V1"},
	{Role: "end_effector", Template: "r1pro-end-effector", AbilityName: "R1ProEndEffector.V1"},
	{Role: "robot_state", Template: "r1pro-robot-state", AbilityName: "R1ProRobotState.V1"},
	{Role: "sensor_capture", Template: "r1pro-sensor-capture", AbilityName: "R1ProSensorCapture.V1"},
	{Role: "object_perception", Template: "r1pro-object-perception", AbilityName: "R1ProObjectPerception.V1"},
	{Role: "grasp_planning", Template: "r1pro-grasp-planning", AbilityName: "R1ProGraspPlanning.V1"},
}

func TestAbilityFrameworkUsesInstanceLocalTemporaryDirectory(t *testing.T) {
	instanceDirectory := t.TempDir()
	environment, err := abilityFrameworkEnvironment(instanceDirectory, []string{
		"TMPDIR=/tmp",
		"PATH=/usr/bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "TMPDIR=" + filepath.Join(instanceDirectory, "ability-framework", "tmp")
	if !environmentContains(environment, want) {
		t.Fatalf("AbilityFramework 临时目录错误: %v", environment)
	}
	if environmentContains(environment, "TMPDIR=/tmp") {
		t.Fatal("不应继续继承可能位于其他文件系统的 /tmp")
	}
	if info, statErr := os.Stat(strings.TrimPrefix(want, "TMPDIR=")); statErr != nil || !info.IsDir() {
		t.Fatalf("实例临时目录未创建: info=%v err=%v", info, statErr)
	}
}

func TestRenderTwoFakeInstancesUseSharedBundle(t *testing.T) {
	bundleDirectory := createTestBundle(t)
	firstConfig := writeTestInstanceConfig(t, bundleDirectory, "robot-a", freePort(t))
	secondConfig := writeTestInstanceConfig(t, bundleDirectory, "robot-b", freePort(t))
	firstDirectory := filepath.Join(t.TempDir(), "robot-a")
	secondDirectory := filepath.Join(t.TempDir(), "robot-b")
	if _, err := Render(firstConfig, firstDirectory); err != nil {
		t.Fatal(err)
	}
	if _, err := Render(secondConfig, secondDirectory); err != nil {
		t.Fatal(err)
	}
	for directory, robotID := range map[string]string{firstDirectory: "robot-a", secondDirectory: "robot-b"} {
		content, err := os.ReadFile(filepath.Join(directory, "robot-deployment.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "id: "+strconv.Quote(robotID)) {
			t.Fatalf("%s 没有 render 独立 robot_id", directory)
		}
		if _, err := os.Stat(filepath.Join(directory, "python")); !os.IsNotExist(err) {
			t.Fatal("实例目录不应包含独立 venv")
		}
	}
	opened, err := bundle.Open(bundleDirectory)
	if err != nil {
		t.Fatal(err)
	}
	python := opened.Path(opened.Manifest.Spec.Artifacts.PythonExecutable)
	first, _ := LoadConfig(filepath.Join(firstDirectory, "instance.yaml"))
	second, _ := LoadConfig(filepath.Join(secondDirectory, "instance.yaml"))
	firstEnvironment := runtimeEnvironment(firstDirectory, first, opened, python)
	if !environmentContains(firstEnvironment, "SEMANTIC_ABILITY_PYTHON="+python) {
		t.Fatal("Robot A 没有使用 bundle 共享 Python")
	}
	if !environmentContains(firstEnvironment, "PYTHONPATH=") ||
		!environmentContains(firstEnvironment, "PYTHONNOUSERSITE=1") {
		t.Fatal("Robot 实例没有隔离宿主 Python 环境")
	}
	exchangeRoot := filepath.Join(firstDirectory, "ability-framework", "artifact-exchange")
	if !environmentContains(firstEnvironment, "SEMANTIC_ABILITY_ARTIFACT_ROOT="+exchangeRoot) {
		t.Fatal("Ability 与 Pilot 没有共享当前实例的 Artifact 交换目录")
	}
	if info, err := os.Stat(exchangeRoot); err != nil || !info.IsDir() {
		t.Fatalf("Artifact 交换目录未创建: info=%v err=%v", info, err)
	}
	if got := runtimeEnvironment(secondDirectory, second, opened, python); !environmentContains(got, "SEMANTIC_ABILITY_PYTHON="+python) {
		t.Fatal("Robot B 没有使用 bundle 共享 Python")
	}
}

func TestTwoFakeInstancesRunAndStopIndependently(t *testing.T) {
	bundleDirectory := createTestBundle(t)
	firstDirectory := filepath.Join(t.TempDir(), "robot-a")
	secondDirectory := filepath.Join(t.TempDir(), "robot-b")
	if _, err := Render(writeTestInstanceConfig(t, bundleDirectory, "robot-a", freePort(t)), firstDirectory); err != nil {
		t.Fatal(err)
	}
	if _, err := Render(writeTestInstanceConfig(t, bundleDirectory, "robot-b", freePort(t)), secondDirectory); err != nil {
		t.Fatal(err)
	}

	firstContext, cancelFirst := context.WithCancel(context.Background())
	secondContext, cancelSecond := context.WithCancel(context.Background())
	defer cancelFirst()
	defer cancelSecond()
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- Run(firstContext, firstDirectory) }()
	go func() { secondDone <- Run(secondContext, secondDirectory) }()
	waitForStatus(t, firstDirectory, StatusRunning)
	waitForStatus(t, secondDirectory, StatusRunning)

	firstState, _ := ReadState(firstDirectory)
	secondState, _ := ReadState(secondDirectory)
	if len(firstState.AbilityInstanceIDs) != 7 || len(secondState.AbilityInstanceIDs) != 7 {
		t.Fatalf("七类 Ability 未全部启动: A=%v B=%v", firstState.AbilityInstanceIDs, secondState.AbilityInstanceIDs)
	}
	cancelFirst()
	if err := waitRun(t, firstDone); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, firstDirectory, StatusStopped)
	firstStopped, _ := ReadState(firstDirectory)
	if firstStopped.StopEvidence == nil || !firstStopped.StopEvidence.Safe ||
		!firstStopped.StopEvidence.HoldConfirmed || !firstStopped.StopEvidence.PilotExitedCleanly ||
		firstStopped.StopEvidence.AbilityStopConfirmed != 7 {
		t.Fatalf("Robot A 缺少完整停止证据: %+v", firstStopped.StopEvidence)
	}
	if state, err := InspectStatus(secondDirectory); err != nil || state.Status != StatusRunning {
		t.Fatalf("停止 Robot A 不应影响 Robot B: state=%+v err=%v", state, err)
	}
	if countLines(filepath.Join(firstDirectory, "ability-framework", "stopped.log")) != 7 {
		t.Fatal("Robot A 应精确停止七个 Ability 实例")
	}
	if countLines(filepath.Join(secondDirectory, "ability-framework", "stopped.log")) != 0 {
		t.Fatal("Robot B 的 Ability 不应被 Robot A 的停止影响")
	}
	cancelSecond()
	if err := waitRun(t, secondDone); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, secondDirectory, StatusStopped)
}

func TestMissingPilotStopEvidenceKeepsAbilityFrameworkForReconciliation(t *testing.T) {
	t.Setenv("SEMANTIC_TEST_NO_STOP_REPORT", "1")
	bundleDirectory := createTestBundle(t)
	instanceDirectory := filepath.Join(t.TempDir(), "robot-unknown")
	if _, err := Render(writeTestInstanceConfig(t, bundleDirectory, "robot-unknown", freePort(t)), instanceDirectory); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, instanceDirectory) }()
	waitForStatus(t, instanceDirectory, StatusRunning)
	cancel()
	if err := waitRun(t, done); !errors.Is(err, errSafetyUnconfirmed) {
		t.Fatalf("缺少安全证据应进入 interrupted: %v", err)
	}
	state, err := ReadState(instanceDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusInterrupted || state.StopEvidence == nil || state.StopEvidence.Safe {
		t.Fatalf("状态未知不能报告 stopped: %+v", state)
	}
	if countLines(filepath.Join(instanceDirectory, "ability-framework", "stopped.log")) != 0 {
		t.Fatal("缺少 Robot 安全证据时不得停止 Ability 实例")
	}
	if state.AbilityFrameworkPID <= 0 || !processAlive(state.AbilityFrameworkPID) {
		t.Fatal("AbilityFramework 必须保留供状态确认")
	}
	process, _ := os.FindProcess(state.AbilityFrameworkPID)
	_ = process.Kill()
}

func TestPilotNonzeroSafeStopIsFailure(t *testing.T) {
	directory := t.TempDir()
	script := filepath.Join(directory, "pilot")
	writeTestFile(t, directory, "pilot",
		"#!/bin/sh\ntrap 'exit 7' TERM INT\nprintf '%s\\n' \"$$\" > \"$PWD/pilot.ready\"\nwhile :; do sleep 1; done\n", 0o755)
	process, err := startProcess("semantic-pilot", script, nil, directory,
		filepath.Join(directory, "pilot.log"), os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	clean, err := process.terminate(2*time.Second, true)
	if err == nil || clean {
		t.Fatalf("Pilot 非零退出不能作为安全停止: clean=%v err=%v", clean, err)
	}
}

func TestManagedChildUsesSeparateProcessGroup(t *testing.T) {
	directory := t.TempDir()
	process, err := startProcess(
		"test-child", "/bin/sh", []string{"-c", "trap 'exit 0' TERM; while :; do sleep 1; done"},
		directory, filepath.Join(directory, "child.log"), os.Environ(),
	)
	if err != nil {
		t.Fatal(err)
	}
	childGroup, err := syscall.Getpgid(process.cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	parentGroup, err := syscall.Getpgid(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if childGroup == parentGroup {
		t.Fatalf("受管子进程仍与 supervisor 共用进程组: %d", childGroup)
	}
	if _, err := process.terminate(2*time.Second, false); err != nil {
		t.Fatal(err)
	}
}

func createTestBundle(t *testing.T) string {
	return createTestBundleWithRegistry(t, "{\"revision\":1}\n")
}

func createTestBundleWithRegistry(t *testing.T, registry string) string {
	t.Helper()
	source := t.TempDir()
	manifest := bundle.Manifest{
		APIVersion: bundle.APIVersion, Kind: bundle.Kind,
		Metadata: bundle.Metadata{Name: "r1pro-fake-test", Version: "0.5.0-test"},
		Spec: bundle.Spec{
			Robot: bundle.RobotSpec{
				Model: "r1pro", SDKPackage: "semantic-robot-sdk-r1pro",
				BackendProfiles: []bundle.BackendProfile{{Backend: "fake", Profile: "fake-v1"}},
			},
			Artifacts: bundle.ArtifactSpec{
				InstanceLauncher: "bin/semantic-robot-instance",
				AbilityFramework: "bin/AbilityFramework",
				Pilot:            "bin/semantic-pilot", RobotSkillSDK: "wheels/skill-sdk.whl",
				PythonExecutable: "python/venv/bin/python",
			},
			Templates: bundle.TemplateSpec{
				RobotDeployment:  "templates/robot.yaml.tmpl",
				AbilityFramework: "templates/af.yaml.tmpl",
				ModelRegistry:    "templates/model-registry.json",
			},
			Runtime: bundle.RuntimePolicy{ReadinessTimeout: "3s", ShutdownTimeout: "2s"},
		},
	}
	for index := range testAbilities {
		testAbilities[index].File = "abilities/" + testAbilities[index].Template + ".zip"
	}
	manifest.Spec.Artifacts.Abilities = append([]bundle.AbilityArtifact(nil), testAbilities...)
	encoded, err := yaml.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, source, "bundle.yaml", string(encoded), 0o644)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	frameworkScript := fmt.Sprintf("#!/bin/sh\nSEMANTIC_TEST_HELPER_AF=1 exec %s -test.run '^TestAbilityFrameworkHelperProcess$' --\n", strconv.Quote(executable))
	pilotScript := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$PWD/pilot.args\"\n" +
		"trap 'if [ \"$SEMANTIC_TEST_NO_STOP_REPORT\" != \"1\" ]; then printf \"{\\\"safe\\\":true,\\\"hold_confirmed\\\":true,\\\"active_invocations\\\":[],\\\"reason\\\":\\\"idle\\\",\\\"finished_at\\\":\\\"2026-08-12T00:00:00Z\\\"}\\n\" > \"$SEMANTIC_PILOT_STOP_REPORT\"; fi; exit 0' TERM INT\nprintf '%s\\n' \"$$\" > \"$PWD/pilot.ready\"\nwhile :; do sleep 1; done\n"
	writeTestFile(t, source, "bin/semantic-robot-instance", "#!/bin/sh\nexit 0\n", 0o755)
	writeTestFile(t, source, "bin/AbilityFramework", frameworkScript, 0o755)
	writeTestFile(t, source, "bin/semantic-pilot", pilotScript, 0o755)
	writeTestFile(t, source, "wheels/skill-sdk.whl", "sdk", 0o644)
	writeTestFile(t, source, "python/venv/bin/python", "#!/bin/sh\nexit 0\n", 0o755)
	for _, ability := range testAbilities {
		writeTestFile(t, source, ability.File, "zip", 0o644)
	}
	writeTestFile(t, source, "templates/af.yaml.tmpl",
		"framework_name: {{ quote .Instance.Metadata.Name }}\nhttp_ip: 127.0.0.1\nhttp_port: {{ .AFPort }}\n", 0o644)
	writeTestFile(t, source, "templates/robot.yaml.tmpl",
		"api_version: 1\nrobot:\n  id: {{ quote .Instance.Spec.Robot.ID }}\n  display_name: {{ quote .Instance.Spec.Robot.DisplayName }}\n  model: r1pro\n  backend: fake\nability_framework:\n  endpoint: {{ quote .Instance.Spec.AbilityFramework.Endpoint }}\n  managed_by_pilot: true\nabilities: {}\npilot:\n  robot_skill_directory: {{ quote .SkillDirectory }}\n  heartbeat_interval_seconds: 1\n", 0o644)
	writeTestFile(t, source, "templates/model-registry.json", registry, 0o644)
	output := filepath.Join(t.TempDir(), "bundle")
	if _, err := bundle.Build(source, output, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { makeTestTreeWritable(output) })
	return output
}

func writeTestInstanceConfig(t *testing.T, bundleDirectory, robotID string, port int) string {
	t.Helper()
	config := fmt.Sprintf(`apiVersion: semantic.insightos.cn/v1alpha1
kind: RobotInstance
metadata:
  name: %s
spec:
  bundle: %s
  robot:
    id: %s
    displayName: %s
    model: r1pro
    backend: fake
    backendProfile: fake-v1
    firmwareProfile: fake-v1
  abilityFramework:
    endpoint: http://127.0.0.1:%d
    managed: true
    listenAddress: 127.0.0.1
  semanticServer:
    websocketURL: ws://127.0.0.1:19090/ws/pilot
    httpURL: http://127.0.0.1:19090
    accessToken: test-token
  pilot:
    id: pilot-%s
`, robotID, bundleDirectory, robotID, robotID, port, robotID)
	path := filepath.Join(t.TempDir(), robotID+".yaml")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAbilityFrameworkHelperProcess(t *testing.T) {
	if os.Getenv("SEMANTIC_TEST_HELPER_AF") != "1" {
		return
	}
	runAbilityFrameworkHelper(t)
}

func runAbilityFrameworkHelper(t *testing.T) {
	data, err := os.ReadFile("config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Name string `yaml:"framework_name"`
		IP   string `yaml:"http_ip"`
		Port int    `yaml:"http_port"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	abilityNames := map[string]string{}
	for _, ability := range testAbilities {
		abilityNames[ability.Template] = ability.AbilityName
	}
	var mutex sync.Mutex
	templates := map[string]bool{}
	instances := map[string]abilityframework.Instance{}
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		if request.Method == http.MethodDelete && strings.HasPrefix(request.URL.Path, "/api/instance/") {
			if os.Getenv("SEMANTIC_TEST_ABILITY_STOP_FAIL") == "1" {
				http.Error(writer, "stop unconfirmed", http.StatusServiceUnavailable)
				return
			}
			identifier := strings.TrimPrefix(request.URL.Path, "/api/instance/")
			for name, value := range instances {
				if value.InstanceID == identifier {
					delete(instances, name)
				}
			}
			file, _ := os.OpenFile("stopped.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
			_, _ = fmt.Fprintln(file, identifier)
			_ = file.Close()
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		switch request.Method + " " + request.URL.Path {
		case "GET /api/cr":
			values := []abilityframework.Template{}
			for name := range templates {
				var value abilityframework.Template
				value.Metadata.Name = name
				value.Spec.AbilityName = abilityNames[name]
				values = append(values, value)
			}
			_ = json.NewEncoder(writer).Encode(values)
		case "POST /api/package":
			filename := request.Header.Get("X-Semantic-Package-Filename")
			name := strings.TrimSuffix(filename, filepath.Ext(filename))
			templates[name] = true
			writer.WriteHeader(http.StatusCreated)
		case "GET /api/instance":
			values := []abilityframework.Instance{}
			for _, value := range instances {
				values = append(values, value)
			}
			_ = json.NewEncoder(writer).Encode(values)
		case "POST /api/instance":
			var body map[string]any
			_ = json.NewDecoder(request.Body).Decode(&body)
			name := body["template"].(string)
			instances[name] = abilityframework.Instance{
				InstanceID: config.Name + "-" + name, CRName: name,
				AbilityName: abilityNames[name], State: "Running",
			}
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"taskId": "task-" + name, "template": name,
			})
		case "GET /api/ability-heartbeat":
			heartbeats := []abilityframework.Heartbeat{}
			for _, value := range instances {
				heartbeats = append(heartbeats, abilityframework.Heartbeat{
					ID: value.InstanceID, AbilityName: value.AbilityName, State: value.State,
				})
			}
			_ = json.NewEncoder(writer).Encode(heartbeats)
		default:
			http.Error(writer, "not found", http.StatusNotFound)
		}
	})
	server := &http.Server{Addr: net.JoinHostPort(config.IP, strconv.Itoa(config.Port)), Handler: handler}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		t.Fatal(err)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func writeTestFile(t *testing.T, root, relative, content string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func makeTestTreeWritable(root string) {
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && entry.IsDir() {
			_ = os.Chmod(path, 0o755)
		}
		return nil
	})
}

func waitForStatus(t *testing.T, directory string, expected Status) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		state, err := ReadState(directory)
		if err == nil && state.Status == expected {
			// StatusRunning records process creation, not the shell helper's
			// signal-handler readiness. Match the current PID so a previous
			// run's marker cannot make restart tests stop the helper too early.
			if expected == StatusRunning && state.PilotPID > 0 {
				ready, readErr := os.ReadFile(filepath.Join(directory, "pilot.ready"))
				if readErr != nil || strings.TrimSpace(string(ready)) != strconv.Itoa(state.PilotPID) {
					time.Sleep(10 * time.Millisecond)
					continue
				}
			}
			return
		}
		if err == nil && state.Status == StatusFailed {
			t.Fatalf("%s 运行失败: %s", directory, state.Error)
		}
		time.Sleep(50 * time.Millisecond)
	}
	state, _ := ReadState(directory)
	t.Fatalf("%s 未进入 %s，当前 %+v", directory, expected, state)
}

func waitRun(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(8 * time.Second):
		t.Fatal("等待实例退出超时")
		return nil
	}
}

func countLines(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(data)))
}

func environmentContains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestTerminateRetiresOwnedDescendantsWithoutSignalingOtherGroups(t *testing.T) {
	directory := t.TempDir()
	unrelated, err := startProcess("unrelated", "/bin/sleep", []string{"60"}, directory,
		filepath.Join(directory, "unrelated.log"), os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	defer unrelated.terminate(2*time.Second, false)
	script := `sleep 60 &
child=$!
echo "$child" > child.pid
trap 'wait "$child"; exit 0' TERM
wait "$child"
`
	process, err := startProcess("framework", "/bin/sh", []string{"-c", script}, directory,
		filepath.Join(directory, "framework.log"), os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(-process.cmd.Process.Pid, syscall.SIGKILL)
	deadline := time.Now().Add(2 * time.Second)
	var child int
	for time.Now().Before(deadline) {
		content, err := os.ReadFile(filepath.Join(directory, "child.pid"))
		if err == nil {
			child, _ = strconv.Atoi(strings.TrimSpace(string(content)))
			if child > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if child == 0 {
		t.Fatal("child did not start")
	}
	if _, err := process.terminate(2*time.Second, false); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(child, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("child remains after termination: %v", err)
	}
	if err := syscall.Kill(unrelated.cmd.Process.Pid, 0); err != nil {
		t.Fatalf("unrelated group was signaled: %v", err)
	}
}

func TestUnconfirmedAbilityStopKeepsFrameworkGroupForReconciliation(t *testing.T) {
	t.Setenv("SEMANTIC_TEST_ABILITY_STOP_FAIL", "1")
	bundleDirectory := createTestBundle(t)
	directory := filepath.Join(t.TempDir(), "robot-unconfirmed")
	if _, err := Render(writeTestInstanceConfig(t, bundleDirectory, "robot-unconfirmed", freePort(t)), directory); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, directory) }()
	waitForStatus(t, directory, StatusRunning)
	running, _ := ReadState(directory)
	defer syscall.Kill(-running.AbilityFrameworkPID, syscall.SIGKILL)
	cancel()
	if err := waitRun(t, done); !errors.Is(err, errSafetyUnconfirmed) {
		t.Fatalf("expected unconfirmed safety: %v", err)
	}
	state, err := ReadState(directory)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusInterrupted || state.StopEvidence.AbilityStopConfirmed != 0 {
		t.Fatalf("unexpected state: %+v", state)
	}
	if !processAlive(running.AbilityFrameworkPID) {
		t.Fatal("framework killed before Ability stop was confirmed")
	}
}
