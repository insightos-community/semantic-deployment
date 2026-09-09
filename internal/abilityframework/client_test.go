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

package abilityframework

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClientUsesConfiguredEndpointAndLifecycle(t *testing.T) {
	var mutex sync.Mutex
	templates := map[string]bool{}
	instances := map[string]Instance{}
	stopped := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		switch request.Method + " " + request.URL.Path {
		case "GET /api/cr":
			values := []Template{}
			for name := range templates {
				var value Template
				value.Metadata.Name = name
				values = append(values, value)
			}
			_ = json.NewEncoder(writer).Encode(values)
		case "POST /api/package":
			name := strings.TrimSuffix(request.Header.Get("X-Semantic-Package-Filename"), filepath.Ext(request.Header.Get("X-Semantic-Package-Filename")))
			templates[name] = true
			writer.WriteHeader(http.StatusCreated)
		case "GET /api/instance":
			values := []Instance{}
			for _, value := range instances {
				values = append(values, value)
			}
			_ = json.NewEncoder(writer).Encode(values)
		case "POST /api/instance":
			var body map[string]any
			_ = json.NewDecoder(request.Body).Decode(&body)
			name := body["template"].(string)
			instances[name] = Instance{
				InstanceID: "instance-" + name, CRName: name,
				AbilityName: "R1ProNavigation.V1", State: "Running",
			}
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"taskId": "task-" + name, "template": name,
			})
		case "DELETE /api/instance/instance-r1pro-navigation":
			identifier := "instance-r1pro-navigation"
			stopped[identifier] = true
			for name, value := range instances {
				if value.InstanceID == identifier {
					value.State = "Terminated"
					instances[name] = value
				}
			}
			writer.WriteHeader(http.StatusNoContent)
		case "GET /api/ability-heartbeat":
			heartbeats := []Heartbeat{}
			if _, exists := instances["r1pro-navigation"]; exists && !stopped["instance-r1pro-navigation"] {
				heartbeats = append(heartbeats, Heartbeat{
					ID: "instance-r1pro-navigation", AbilityName: "R1ProNavigation.V1", State: "Running",
				})
			}
			_ = json.NewEncoder(writer).Encode(heartbeats)
		default:
			http.Error(writer, "unexpected "+request.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "r1pro-navigation.zip")
	if err := os.WriteFile(archive, []byte("zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := client.WaitReady(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := client.EnsurePackage(ctx, "r1pro-navigation", archive, time.Second); err != nil {
		t.Fatal(err)
	}
	instance, err := client.Activate(ctx, "r1pro-navigation", "R1ProNavigation.V1", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	heartbeats, err := client.Heartbeats(ctx)
	if err != nil || len(heartbeats) != 1 {
		t.Fatalf("heartbeat=%v err=%v", heartbeats, err)
	}
	if _, err := client.WaitHeartbeat(ctx, instance.InstanceID, "R1ProNavigation.V1", time.Second); err != nil {
		t.Fatal(err)
	}
	if err := client.Stop(ctx, instance.InstanceID); err != nil {
		t.Fatal(err)
	}
	if err := client.WaitStopped(ctx, instance.InstanceID, time.Second); err != nil {
		t.Fatal(err)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if !stopped[instance.InstanceID] {
		t.Fatal("Stop 没有到达配置的 AbilityFramework endpoint")
	}
	if strings.Contains(server.URL, ":8080") {
		t.Fatal(fmt.Errorf("测试 endpoint 不应依赖固定 8080"))
	}
}

func TestWaitStoppedDoesNotTreatAcceptedAsStopped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/ability-heartbeat" {
			_ = json.NewEncoder(writer).Encode([]Heartbeat{{
				ID: "still-running", AbilityName: "R1ProNavigation.V1", State: "Running",
			}})
			return
		}
		if request.URL.Path == "/api/instance" {
			_ = json.NewEncoder(writer).Encode([]Instance{{
				InstanceID: "still-running", CRName: "r1pro-navigation", State: "Running",
			}})
			return
		}
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Stop(context.Background(), "still-running"); err != nil {
		t.Fatal(err)
	}
	if err := client.WaitStopped(context.Background(), "still-running", 150*time.Millisecond); err == nil {
		t.Fatal("lifecycle API 仅受理时不能记为 Ability 已停止")
	}
}

func TestStopUsesInstanceDeleteAndTreatsMissingInstanceAsStopped(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		methods = append(methods, request.Method+" "+request.URL.Path)
		if request.Method != http.MethodDelete || request.URL.Path != "/api/instance/gone-instance" {
			http.Error(writer, "unexpected request", http.StatusBadRequest)
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Stop(context.Background(), "gone-instance"); err != nil {
		t.Fatalf("重复停止已消失实例应幂等成功: %v", err)
	}
	if len(methods) != 1 || methods[0] != "DELETE /api/instance/gone-instance" {
		t.Fatalf("停止必须使用正式实例删除接口: %v", methods)
	}
}

func TestWaitStoppedFallsBackToInstanceListWithoutHeartbeatAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/ability-heartbeat":
			http.NotFound(writer, request)
		case "/api/instance":
			_ = json.NewEncoder(writer).Encode([]Instance{{
				InstanceID: "legacy-instance", CRName: "r1pro-navigation", State: "Inactive",
			}})
		default:
			writer.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.WaitStopped(context.Background(), "legacy-instance", time.Second); err != nil {
		t.Fatalf("无 heartbeat API 时应兼容 instance list 终态: %v", err)
	}
}

func TestActivateUsesRealHeartbeatUUIDWhenInstanceListStillInactive(t *testing.T) {
	const (
		instanceID  = "d02c8c89-04c1-4460-a9ab-db9d507a36eb"
		abilityName = "R1ProNavigation.V1"
	)
	activated := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "GET /api/instance":
			if !activated {
				_ = json.NewEncoder(writer).Encode([]any{})
				return
			}
			// 真实 AF 的 detail 是对象；heartbeat 已 Running 时数据库列表可能仍短暂为 Inactive。
			_ = json.NewEncoder(writer).Encode([]map[string]any{{
				"instance_id":     instanceID,
				"cr_id":           "32e5a5b6-4a8b-4d11-bb6d-0c862ba75bb1",
				"cr_name":         "r1pro-navigation",
				"instance_name":   "r1pro-navigation-d02c8c89",
				"ability_name":    abilityName,
				"ability_version": "0.1.0",
				"state":           "Inactive",
				"start_time":      1786417024,
				"stop_time":       0,
				"detail":          map[string]any{"abilityPort": 46503, "IPCPort": 52887},
				"spec_snapshot":   map[string]any{"spec": map[string]any{"abilityName": abilityName}},
			}})
		case "POST /api/instance":
			activated = true
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"taskId":   "c10a4672-80ae-4e89-9c03-0ac0d0d4633f",
				"template": "r1pro-navigation",
			})
		case "GET /api/ability-heartbeat":
			if !activated {
				_ = json.NewEncoder(writer).Encode([]any{})
				return
			}
			_ = json.NewEncoder(writer).Encode([]map[string]any{
				{"id": "wrong-instance", "abilityName": abilityName, "state": "Running"},
				{"id": instanceID, "abilityName": "WrongAbility.V1", "state": "Running"},
				{
					"id": instanceID, "instanceName": "r1pro-navigation-d02c8c89",
					"abilityName": abilityName, "version": "0.1.0", "state": "Running",
					"IPCPort": 52887, "abilityPort": 46503,
				},
			})
		default:
			http.Error(writer, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	instance, err := client.Activate(
		context.Background(), "r1pro-navigation", abilityName, time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if instance.InstanceID != instanceID || instance.AbilityName != abilityName || instance.State != "Running" {
		t.Fatalf("未按真实 heartbeat 选择精确实例: %+v", instance)
	}
	if !strings.Contains(string(instance.Detail), "abilityPort") {
		t.Fatalf("真实 object detail 未保留: %s", instance.Detail)
	}
}

func TestActivateRecoversNewInstanceStuckInStandby(t *testing.T) {
	const (
		instanceID  = "38ef033f-6834-4b2b-b4c7-7614f5dfb19c"
		template    = "r1pro-sensor-capture"
		abilityName = "R1ProSensorCapture.V1"
	)
	activated := false
	connected := false
	connectCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "GET /api/instance":
			if !activated {
				_ = json.NewEncoder(writer).Encode([]any{})
				return
			}
			_ = json.NewEncoder(writer).Encode([]Instance{{
				InstanceID: instanceID, CRName: template, AbilityName: abilityName,
				State: "Standby", Detail: json.RawMessage(`{"IPCPort":57159,"abilityPort":0}`),
			}})
		case "GET /api/ability-heartbeat":
			if !activated {
				_ = json.NewEncoder(writer).Encode([]any{})
				return
			}
			state := "Standby"
			abilityPort := 0
			if connected {
				state = "Running"
				abilityPort = 42001
			}
			_ = json.NewEncoder(writer).Encode([]Heartbeat{{
				ID: instanceID, AbilityName: abilityName, State: state,
				IPCPort: 57159, AbilityPort: abilityPort,
			}})
		case "POST /api/instance":
			activated = true
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"taskId": "task-sensor-capture", "template": template,
			})
		case "POST /api/lifecycle-request":
			var body map[string]string
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body["abilityInstanceId"] != instanceID || body["command"] != "connect" {
				http.Error(writer, "unexpected lifecycle request", http.StatusBadRequest)
				return
			}
			connectCalls++
			connected = true
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.Error(writer, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	instance, err := client.Activate(context.Background(), template, abilityName, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if instance.InstanceID != instanceID || instance.State != "Running" {
		t.Fatalf("Standby 实例没有恢复为 Running: %+v", instance)
	}
	if connectCalls != 1 {
		t.Fatalf("持续 Standby 的新实例应且只应补发一次 connect，得到 %d", connectCalls)
	}
}

func TestActivateRecreatesVanishedStandbyInstanceOnce(t *testing.T) {
	const (
		staleID     = "stale-standby-instance"
		runningID   = "replacement-running-instance"
		template    = "r1pro-manipulator-motion"
		abilityName = "R1ProManipulatorMotion.V1"
	)
	activationCalls := 0
	connectCalls := 0
	deleteCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "GET /api/instance":
			switch activationCalls {
			case 0:
				_ = json.NewEncoder(writer).Encode([]any{})
			case 1:
				_ = json.NewEncoder(writer).Encode([]Instance{{
					InstanceID: staleID, CRName: template, AbilityName: abilityName,
					State: "Standby", Detail: json.RawMessage(`{"IPCPort":39963,"abilityPort":0}`),
				}})
			default:
				_ = json.NewEncoder(writer).Encode([]Instance{
					{
						InstanceID: staleID, CRName: template, AbilityName: abilityName,
						State: "Standby", Detail: json.RawMessage(`{"IPCPort":39963,"abilityPort":0}`),
					},
					{
						InstanceID: runningID, CRName: template, AbilityName: abilityName,
						State: "Running", Detail: json.RawMessage(`{"IPCPort":39964,"abilityPort":42002}`),
					},
				})
			}
		case "GET /api/ability-heartbeat":
			heartbeats := []Heartbeat{}
			if activationCalls >= 2 {
				heartbeats = append(heartbeats, Heartbeat{
					ID: runningID, AbilityName: abilityName, State: "Running",
					IPCPort: 39964, AbilityPort: 42002,
				})
			}
			_ = json.NewEncoder(writer).Encode(heartbeats)
		case "POST /api/instance":
			activationCalls++
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"taskId": fmt.Sprintf("task-%d", activationCalls), "template": template,
			})
		case "POST /api/lifecycle-request":
			connectCalls++
			http.Error(writer, "no such ability", http.StatusNotFound)
		case "DELETE /api/instance/" + staleID:
			deleteCalls++
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.Error(writer, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	instance, err := client.Activate(context.Background(), template, abilityName, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if instance.InstanceID != runningID {
		t.Fatalf("应选择一次性重建后的 Running 实例，得到 %+v", instance)
	}
	if activationCalls != 2 {
		t.Fatalf("已消失的 Standby 实例只应触发一次重建，activation=%d", activationCalls)
	}
	if connectCalls != 1 {
		t.Fatalf("旧 Standby UUID 只应尝试一次 connect，connect=%d", connectCalls)
	}
	if deleteCalls != 1 {
		t.Fatalf("重建前必须删除旧 Standby UUID，delete=%d", deleteCalls)
	}
}

func TestActivateRejectsReceiptWithoutTaskID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "GET /api/instance", "GET /api/ability-heartbeat":
			_ = json.NewEncoder(writer).Encode([]any{})
		case "POST /api/instance":
			_ = json.NewEncoder(writer).Encode(map[string]string{"template": "r1pro-navigation"})
		default:
			http.Error(writer, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Activate(
		context.Background(), "r1pro-navigation", "R1ProNavigation.V1", time.Second,
	)
	if err == nil || !strings.Contains(err.Error(), "taskId") {
		t.Fatalf("无效 activation 回执应被拒绝，得到: %v", err)
	}
}

func TestActivateReportsLastInstanceDecodeError(t *testing.T) {
	activated := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "GET /api/instance":
			if activated {
				_, _ = writer.Write([]byte(`[{"instance_id":`))
				return
			}
			_ = json.NewEncoder(writer).Encode([]any{})
		case "GET /api/ability-heartbeat":
			_ = json.NewEncoder(writer).Encode([]any{})
		case "POST /api/instance":
			activated = true
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"taskId": "task-navigation", "template": "r1pro-navigation",
			})
		default:
			http.Error(writer, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Activate(
		context.Background(), "r1pro-navigation", "R1ProNavigation.V1", 150*time.Millisecond,
	)
	if err == nil || !strings.Contains(err.Error(), "最近轮询错误") ||
		!strings.Contains(err.Error(), "解析 AbilityFramework /api/instance 响应失败") {
		t.Fatalf("Activate 应回报最近的列表解码错误，得到: %v", err)
	}
}
