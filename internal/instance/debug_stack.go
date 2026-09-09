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
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"insightos.cn/semantic-robot-deployment/internal/abilityframework"
	"insightos.cn/semantic-robot-deployment/internal/bundle"
)

// RunDebugStack 只启动本地 Robot Skill 调试所需的 AbilityFramework 和七类
// Ability。它不启动 Pilot 常驻进程，也不读取 Semantic Server credential。
//
// 本模式与完整实例共用 instance.lock，防止常驻 Pilot 和本地 semantic-pilot
// 同时控制同一 Robot。调试 Skill 结束或安全停止后，调用者再退出本进程；如果
// 反过来先关闭组件栈，Ability 将无法形成 Robot hold 证据。
func RunDebugStack(ctx context.Context, instanceDirectory string, readyOutput io.Writer) (runErr error) {
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
	python := opened.Path(opened.Manifest.Spec.Artifacts.PythonExecutable)
	environment := runtimeEnvironment(instanceDirectory, config, opened, python)
	client, err := abilityframework.NewClient(config.Spec.AbilityFramework.Endpoint, nil)
	if err != nil {
		return err
	}

	var frameworkProcess *managedProcess
	activated := make([]string, 0, len(opened.Manifest.Spec.Artifacts.Abilities))
	cleanup := func() error {
		stopContext, cancel := context.WithTimeout(context.Background(), opened.Manifest.ShutdownTimeout())
		defer cancel()
		var failures []string
		for index := len(activated) - 1; index >= 0; index-- {
			identifier := activated[index]
			if err := client.Stop(stopContext, identifier); err != nil {
				failures = append(failures, fmt.Sprintf("停止 Ability %s: %v", identifier, err))
				continue
			}
			if err := client.WaitStopped(stopContext, identifier, opened.Manifest.ShutdownTimeout()); err != nil {
				failures = append(failures, fmt.Sprintf("确认 Ability %s 停止: %v", identifier, err))
			}
		}
		if frameworkProcess != nil {
			if _, err := frameworkProcess.terminate(opened.Manifest.ShutdownTimeout(), false); err != nil {
				failures = append(failures, err.Error())
			}
		}
		if len(failures) != 0 {
			return errors.New(strings.Join(failures, "; "))
		}
		return nil
	}
	defer func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			runErr = errors.Join(runErr, cleanupErr)
		}
	}()

	if config.Spec.AbilityFramework.Managed {
		frameworkProcess, err = startProcess(
			"AbilityFramework", opened.Path(opened.Manifest.Spec.Artifacts.AbilityFramework), nil,
			filepath.Join(instanceDirectory, "ability-framework"),
			filepath.Join(instanceDirectory, "ability-framework", "log", "debug-stack.log"), environment,
		)
		if err != nil {
			return err
		}
	}
	if err := client.WaitReady(ctx, opened.Manifest.ReadinessTimeout()); err != nil {
		return err
	}
	for _, ability := range opened.Manifest.Spec.Artifacts.Abilities {
		if err := client.EnsurePackage(ctx, ability.Template, opened.Path(ability.File), opened.Manifest.ReadinessTimeout()); err != nil {
			return fmt.Errorf("准备 Ability %s: %w", ability.Role, err)
		}
		instance, err := client.Activate(ctx, ability.Template, ability.AbilityName, opened.Manifest.ReadinessTimeout())
		if err != nil {
			return fmt.Errorf("启动 Ability %s: %w", ability.Role, err)
		}
		activated = append(activated, instance.InstanceID)
		if _, err := client.WaitHeartbeat(ctx, instance.InstanceID, ability.AbilityName,
			opened.Manifest.ReadinessTimeout()); err != nil {
			return fmt.Errorf("确认 Ability %s heartbeat: %w", ability.Role, err)
		}
	}
	if readyOutput != nil {
		encoder := json.NewEncoder(readyOutput)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(map[string]any{
			"status": "ready", "robot_id": config.Spec.Robot.ID,
			"robot_deployment":     filepath.Join(instanceDirectory, "robot-deployment.yaml"),
			"ability_framework":    config.Spec.AbilityFramework.Endpoint,
			"ability_instance_ids": append([]string(nil), activated...),
		}); err != nil {
			return err
		}
	}

	select {
	case <-ctx.Done():
		return nil
	case err := <-frameworkDone(frameworkProcess):
		frameworkProcess = nil
		return fmt.Errorf("AbilityFramework 意外退出: %w", normalizeExitError(err))
	}
}
