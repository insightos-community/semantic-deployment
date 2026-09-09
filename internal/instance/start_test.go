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
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLoadRobotDeploymentDefaultsRuntimeIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "robot-deployment.yaml")
	content := "api_version: 1\nrobot:\n  id: r1pro-test\n  model: r1pro\n  backend: fake\n  sdk:\n    package: semantic-robot-sdk-r1pro\n    firmware_profile: fake-v1\nability_framework:\n  endpoint: http://127.0.0.1:18082\n  managed_by_instance: true\nabilities: {}\npilot:\n  allow_ability_debug: true\nrobot_skills: []\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	deployment, err := loadRobotDeployment(path)
	if err != nil {
		t.Fatal(err)
	}
	if deployment.Pilot.ID != "pilot-r1pro-test" || deployment.Robot.SDK.BackendProfile != "fake-v1" {
		t.Fatalf("没有生成稳定运行身份: pilot=%s profile=%s", deployment.Pilot.ID, deployment.Robot.SDK.BackendProfile)
	}
}

func TestDefaultDataDirectoryUsesRobotIdentity(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateRoot)
	path, err := defaultDataDirectory("r1pro/fake-02")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(stateRoot, "semantic", "robots", "r1pro_fake-02")
	if path != want {
		t.Fatalf("默认实例目录没有按 Robot 隔离: got=%s want=%s", path, want)
	}
}

func TestConnectionClaimsOnceThenUsesLocalCredential(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path != "/api/v1/pilot-enrollments/claim" {
			http.NotFound(writer, request)
			return
		}
		var payload map[string]string
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["join_code"] != "ABC123" || payload["pilot_id"] != "pilot-r1pro-test" {
			t.Fatalf("claim 参数错误: %#v", payload)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]string{"credential": "pilot_credential"})
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "connection.yaml")
	options := StartOptions{JoinCode: "ABC123", ServerHTTPURL: server.URL, ServerWSURL: "ws://server/ws/pilot"}
	connection, err := loadOrClaimConnection(context.Background(), path, "pilot-r1pro-test", options)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveConnection(path, connection); err != nil {
		t.Fatal(err)
	}
	second, err := loadOrClaimConnection(context.Background(), path, "pilot-r1pro-test", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Credential != "pilot_credential" || calls.Load() != 1 {
		t.Fatalf("后续启动不应再次 claim: credential=%s calls=%d", second.Credential, calls.Load())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("connection.yaml 权限应为 0600，实际 %o", info.Mode().Perm())
	}
}

func TestStartClaimsRendersAndRunsInstance(t *testing.T) {
	bundleDirectory := createTestBundle(t)
	var claimCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		claimCalls.Add(1)
		_ = json.NewEncoder(writer).Encode(map[string]string{"credential": "pilot_runtime_credential"})
	}))
	defer server.Close()

	deploymentPath := filepath.Join(t.TempDir(), "robot-deployment.yaml")
	deployment := "api_version: 1\nrobot:\n  id: robot-start\n  display_name: Robot Start\n  model: r1pro\n  backend: fake\n  sdk:\n    package: semantic-robot-sdk-r1pro\n    firmware_profile: fake-v1\nability_framework:\n  endpoint: http://127.0.0.1:" + fmt.Sprint(freePort(t)) + "\n  managed_by_instance: true\nabilities: {}\npilot:\n  id: pilot-robot-start\n  heartbeat_interval_seconds: 1\n  worker_timeout_seconds: 2\nrobot_skills:\n  - name: grasp-object\n    version: 0.1.0\n    enabled: true\n"
	if err := os.WriteFile(deploymentPath, []byte(deployment), 0o600); err != nil {
		t.Fatal(err)
	}
	instanceDirectory := filepath.Join(t.TempDir(), "nested", "robot-start")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Start(ctx, StartOptions{DeploymentPath: deploymentPath, DataDirectory: instanceDirectory,
			BundleDirectory: bundleDirectory, JoinCode: "ABC123", ServerHTTPURL: server.URL,
			ServerWSURL: "ws://127.0.0.1:18081/ws/pilot"})
	}()
	waitForStatus(t, instanceDirectory, StatusRunning)
	if claimCalls.Load() != 1 {
		t.Fatalf("首次启动应只 claim 一次: %d", claimCalls.Load())
	}
	connection, err := os.ReadFile(filepath.Join(instanceDirectory, "connection.yaml"))
	if err != nil || !strings.Contains(string(connection), "pilot_runtime_credential") {
		t.Fatalf("没有保存专用 Pilot credential: %v", err)
	}
	profile, err := os.ReadFile(filepath.Join(instanceDirectory, "robot-deployment.yaml"))
	if err != nil || !strings.Contains(string(profile), "name: grasp-object") {
		t.Fatalf("RobotDeployment desired Skill 未进入运行实例: %v", err)
	}
	cancel()
	if err := waitRun(t, done); err != nil {
		t.Fatal(err)
	}

	// 同一个 Robot 实例升级类型包时应继续复用已签发的 Pilot credential，
	// 但 model registry 等类型包派生配置必须来自新包，不能悄悄沿用旧副本。
	replacementBundle := createTestBundleWithRegistry(t, "{\"revision\":2}\n")
	restartContext, stopRestart := context.WithCancel(context.Background())
	restartDone := make(chan error, 1)
	go func() {
		restartDone <- Start(restartContext, StartOptions{
			DeploymentPath:  deploymentPath,
			DataDirectory:   instanceDirectory,
			BundleDirectory: replacementBundle,
		})
	}()
	waitForStatus(t, instanceDirectory, StatusRunning)
	registry, err := os.ReadFile(filepath.Join(instanceDirectory, "model-registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(registry)) != `{"revision":2}` {
		t.Fatalf("实例重启没有刷新类型包派生的 model registry: %s", registry)
	}
	if claimCalls.Load() != 1 {
		t.Fatalf("刷新类型包不应重新 claim Pilot: %d", claimCalls.Load())
	}
	stopRestart()
	if err := waitRun(t, restartDone); err != nil {
		t.Fatal(err)
	}
}
