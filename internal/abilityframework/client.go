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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	endpoint string
	http     *http.Client
}

func NewClient(endpoint string, client *http.Client) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("AbilityFramework endpoint 无效: %s", endpoint)
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{endpoint: strings.TrimRight(parsed.String(), "/"), http: client}, nil
}

type Template struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		AbilityName string `json:"abilityName"`
	} `json:"spec"`
}

type Instance struct {
	InstanceID  string          `json:"instance_id"`
	CRName      string          `json:"cr_name"`
	AbilityName string          `json:"ability_name"`
	State       string          `json:"state"`
	Detail      json.RawMessage `json:"detail"`
	Snapshot    map[string]any  `json:"spec_snapshot"`
}

type Heartbeat struct {
	ID           string `json:"id"`
	InstanceName string `json:"instanceName"`
	AbilityName  string `json:"abilityName"`
	Version      string `json:"version"`
	State        string `json:"state"`
	IPCPort      int    `json:"IPCPort"`
	AbilityPort  int    `json:"abilityPort"`
}

type activationReceipt struct {
	TaskID   string `json:"taskId"`
	Template string `json:"template"`
}

// responseError 只承载 AbilityFramework HTTP 传输层信息。激活流程需要区分
// “实例仍在启动”与“异步创建出的 Standby 实例已经消失”这两种情况；后者由
// lifecycle API 的 404 明确表达，可以安全地重新创建一次，而不能无限轮询旧 UUID。
type responseError struct {
	method     string
	path       string
	statusCode int
	message    string
}

func (err *responseError) Error() string {
	return fmt.Sprintf(
		"AbilityFramework %s %s 返回 %d: %s",
		err.method, err.path, err.statusCode, err.message,
	)
}

func (client *Client) WaitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		_, last = client.ListInstances(ctx)
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("等待 AbilityFramework %s 就绪超时: %w", client.endpoint, last)
}

func (client *Client) UploadPackage(ctx context.Context, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint+"/api/package", file)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/zip")
	request.Header.Set("X-Semantic-Package-Filename", filepath.Base(path))
	return client.doJSON(request, nil)
}

func (client *Client) ListTemplates(ctx context.Context) ([]Template, error) {
	var result []Template
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.endpoint+"/api/cr", nil)
	if err != nil {
		return nil, err
	}
	if err := client.doJSON(request, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (client *Client) ListInstances(ctx context.Context) ([]Instance, error) {
	var result []Instance
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.endpoint+"/api/instance", nil)
	if err != nil {
		return nil, err
	}
	if err := client.doJSON(request, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (client *Client) Heartbeats(ctx context.Context) ([]Heartbeat, error) {
	var result []Heartbeat
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.endpoint+"/api/ability-heartbeat", nil)
	if err != nil {
		return nil, err
	}
	if err := client.doJSON(request, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// WaitHeartbeat 确认当前模板实际启动了期望 Ability，避免仅凭 CR Running
// 把同名模板、错误包或另一实例误认为可用。
func (client *Client) WaitHeartbeat(
	ctx context.Context, instanceID, abilityName string, timeout time.Duration,
) (Heartbeat, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		heartbeats, err := client.Heartbeats(ctx)
		if err == nil {
			for _, heartbeat := range heartbeats {
				if heartbeat.ID == instanceID && heartbeat.AbilityName == abilityName &&
					strings.EqualFold(heartbeat.State, "running") {
					return heartbeat, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return Heartbeat{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return Heartbeat{}, fmt.Errorf("Ability 实例 %s 没有上报期望 heartbeat %s", instanceID, abilityName)
}

func (client *Client) EnsurePackage(ctx context.Context, template, path string, timeout time.Duration) error {
	templates, err := client.ListTemplates(ctx)
	if err != nil {
		return err
	}
	for _, item := range templates {
		if item.Metadata.Name == template {
			return nil
		}
	}
	if err := client.UploadPackage(ctx, path); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		templates, err = client.ListTemplates(ctx)
		if err == nil {
			for _, item := range templates {
				if item.Metadata.Name == template {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("Ability 包上传后没有出现模板 %s", template)
}

func (client *Client) Activate(
	ctx context.Context, template, abilityName string, timeout time.Duration,
) (Instance, error) {
	instances, err := client.ListInstances(ctx)
	if err != nil {
		return Instance{}, err
	}
	heartbeats, err := client.Heartbeats(ctx)
	if err != nil {
		return Instance{}, err
	}
	if instance, ok := matchRunningInstance(instances, heartbeats, template, abilityName, nil); ok {
		return instance, nil
	}
	baselineInstances := make(map[string]bool, len(instances))
	for _, instance := range instances {
		baselineInstances[instance.InstanceID] = true
	}
	// POST /api/instance 返回的是异步 taskId，不是 Ability instance UUID。只记录
	// 调用前已经 Running 的 heartbeat；调用后首次变为 Running 的精确 heartbeat
	// 才能作为本次启动的实例标识。
	baselineRunning := map[string]bool{}
	for _, heartbeat := range heartbeats {
		if heartbeat.AbilityName == abilityName && strings.EqualFold(heartbeat.State, "running") {
			baselineRunning[heartbeat.ID] = true
		}
	}
	if err := client.requestActivation(ctx, template); err != nil {
		return Instance{}, err
	}
	deadline := time.Now().Add(timeout)
	lastState := ""
	var lastPollError error
	standbySince := map[string]time.Time{}
	connectRequested := map[string]bool{}
	activationRetried := false
	for time.Now().Before(deadline) {
		instances, err = client.ListInstances(ctx)
		if err == nil {
			for _, instance := range instances {
				if instance.CRName == template && instance.AbilityName == abilityName {
					lastState = formatInstanceState(instance)
					if !baselineInstances[instance.InstanceID] && strings.EqualFold(instance.State, "standby") {
						if standbySince[instance.InstanceID].IsZero() {
							standbySince[instance.InstanceID] = time.Now()
						}
						// AbilityFramework 的 create→start→connect 是异步流水线。真实部署中偶发
						// connect 先于实例注册完成，进程已经上报 Standby，却不会再自动进入
						// Running。这里只对本次 POST 新建且持续 Standby 的精确实例补发一次
						// 官方 lifecycle connect；既不碰历史实例，也不循环重启 Ability。
						if !connectRequested[instance.InstanceID] &&
							time.Since(standbySince[instance.InstanceID]) >= time.Second {
							connectRequested[instance.InstanceID] = true
							if connectErr := client.requestLifecycle(
								ctx, instance.InstanceID, "connect",
							); connectErr != nil {
								lastPollError = fmt.Errorf(
									"恢复 Standby Ability %s: %w", instance.InstanceID, connectErr,
								)
								var responseErr *responseError
								if !activationRetried && errors.As(connectErr, &responseErr) &&
									responseErr.statusCode == http.StatusNotFound {
									// 真实 AbilityFramework 偶发保留一个已经退出进程的 Standby
									// 数据库快照；lifecycle 404 证明这个 UUID 已无法恢复。重新
									// 创建前必须先删除这个精确的幽灵实例，否则 Pilot 会同时看见
									// 同一 Ability 的两个 UUID，并把整个 AbilityFramework 判为
									// degraded。这里仍只恢复一次，避免把短暂启动变成重试循环。
									activationRetried = true
									baselineRunning[instance.InstanceID] = true
									if removeErr := client.Stop(ctx, instance.InstanceID); removeErr != nil {
										lastPollError = fmt.Errorf(
											"删除已消失的 Ability %s: %w", instance.InstanceID, removeErr,
										)
										continue
									}
									if retryErr := client.requestActivation(ctx, template); retryErr != nil {
										lastPollError = fmt.Errorf("重新创建已消失的 Ability: %w", retryErr)
									}
								}
							}
						}
					}
				}
			}
		} else {
			lastPollError = fmt.Errorf("读取 Ability instance 列表: %w", err)
		}
		heartbeats, heartbeatErr := client.Heartbeats(ctx)
		if err == nil && heartbeatErr == nil {
			if instance, ok := matchRunningInstance(
				instances, heartbeats, template, abilityName, baselineRunning,
			); ok {
				return instance, nil
			}
		} else if heartbeatErr != nil {
			lastPollError = fmt.Errorf("读取 Ability heartbeat: %w", heartbeatErr)
		}
		select {
		case <-ctx.Done():
			return Instance{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	message := fmt.Sprintf(
		"等待 Ability %s 进入 Running 超时，最近状态: %s", template, strings.TrimSpace(lastState),
	)
	if lastPollError != nil {
		return Instance{}, fmt.Errorf("%s；最近轮询错误: %w", message, lastPollError)
	}
	return Instance{}, errors.New(message)
}

func (client *Client) requestActivation(ctx context.Context, template string) error {
	body, _ := json.Marshal(map[string]any{"template": template, "start": true, "connect": true})
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, client.endpoint+"/api/instance", bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	var receipt activationReceipt
	if err := client.doJSON(request, &receipt); err != nil {
		return err
	}
	if strings.TrimSpace(receipt.TaskID) == "" {
		return errors.New("AbilityFramework activation 回执缺少 taskId")
	}
	if receipt.Template != "" && receipt.Template != template {
		return fmt.Errorf("AbilityFramework activation 回执模板不匹配: %s", receipt.Template)
	}
	return nil
}

func matchRunningInstance(
	instances []Instance,
	heartbeats []Heartbeat,
	template string,
	abilityName string,
	excludedRunning map[string]bool,
) (Instance, bool) {
	byID := make(map[string]Instance, len(instances))
	for _, instance := range instances {
		byID[instance.InstanceID] = instance
	}
	for _, heartbeat := range heartbeats {
		if excludedRunning[heartbeat.ID] || heartbeat.AbilityName != abilityName ||
			!strings.EqualFold(heartbeat.State, "running") {
			continue
		}
		instance, ok := byID[heartbeat.ID]
		if !ok || instance.CRName != template || instance.AbilityName != abilityName {
			continue
		}
		// AbilityInstance.state 的数据库更新可能略晚于 Lifecycle heartbeat；UUID、
		// template 和 abilityName 已交叉确认后，以 heartbeat 的实时状态为准。
		instance.State = heartbeat.State
		return instance, true
	}
	return Instance{}, false
}

func formatInstanceState(instance Instance) string {
	detail := strings.TrimSpace(string(instance.Detail))
	if detail == "" || detail == "null" {
		return instance.State
	}
	return strings.TrimSpace(instance.State + " " + detail)
}

func (client *Client) Stop(ctx context.Context, instanceID string) error {
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return errors.New("Ability instance ID 不能为空")
	}
	// AbilityFramework 的 lifecycle-request 用于 start/connect/disconnect 等
	// 运行态切换；实例销毁的正式契约是 DELETE /api/instance/{id}。此前把
	// terminate 发给 lifecycle-request，真实 Framework 会继续向已经退出的
	// TaskMgr 投递消息并返回 404，随后 supervisor 又提前关闭 Framework，导致
	// 其余 Ability 全部失去停止确认。这里按实例删除契约发起异步停止，终态仍由
	// WaitStopped 根据 heartbeat/instance 视图确认，不能把 HTTP 2xx 当成已停止。
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodDelete,
		client.endpoint+"/api/instance/"+url.PathEscape(instanceID),
		nil,
	)
	if err != nil {
		return err
	}
	if err := client.doJSON(request, nil); err != nil {
		var responseErr *responseError
		if errors.As(err, &responseErr) && responseErr.statusCode == http.StatusNotFound {
			// 重复 stop 时实例已经消失等价于停止完成；其它 404（例如旧的
			// TaskMgr lifecycle 错误）不会走到本接口，不能被误吞。
			return nil
		}
		return err
	}
	return nil
}

func (client *Client) requestLifecycle(ctx context.Context, instanceID, command string) error {
	body, _ := json.Marshal(map[string]string{
		"abilityInstanceId": instanceID,
		"command":           command,
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint+"/api/lifecycle-request", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	return client.doJSON(request, nil)
}

// WaitStopped 将 lifecycle API 的受理与物理进程终态分开。只有实例消失或明确
// 进入 Terminated/Inactive，调用方才能把停止记为已确认。
func (client *Client) WaitStopped(ctx context.Context, instanceID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		heartbeats, heartbeatErr := client.Heartbeats(ctx)
		if heartbeatErr == nil {
			found := false
			for _, heartbeat := range heartbeats {
				if heartbeat.ID != instanceID {
					continue
				}
				found = true
				if !strings.EqualFold(heartbeat.State, "running") {
					return nil
				}
			}
			if !found {
				return nil
			}
		} else if instances, err := client.ListInstances(ctx); err == nil {
			// 兼容没有 heartbeat 查询接口的 AbilityFramework。新版本优先使用
			// heartbeat，因为 lifecycle-request 的 HTTP 2xx 仅表示异步受理。
			found := false
			for _, instance := range instances {
				if instance.InstanceID != instanceID {
					continue
				}
				found = true
				if instance.State == "Terminated" || instance.State == "Inactive" {
					return nil
				}
			}
			if !found {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("Ability 实例 %s 未在 %s 内确认停止", instanceID, timeout)
}

func (client *Client) doJSON(request *http.Request, target any) error {
	response, err := client.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return &responseError{
			method: request.Method, path: request.URL.Path,
			statusCode: response.StatusCode, message: strings.TrimSpace(string(body)),
		}
	}
	if target == nil {
		_, err = io.Copy(io.Discard, response.Body)
		return err
	}
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("解析 AbilityFramework %s 响应失败: %w", request.URL.Path, err)
	}
	return nil
}
