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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type StartOptions struct {
	DeploymentPath  string
	DataDirectory   string
	BundleDirectory string
	JoinCode        string
	ServerHTTPURL   string
	ServerWSURL     string
}

type connectionConfig struct {
	ServerHTTPURL string    `yaml:"server_http_url"`
	ServerWSURL   string    `yaml:"server_websocket_url"`
	PilotID       string    `yaml:"pilot_id"`
	Credential    string    `yaml:"credential"`
	EnrolledAt    time.Time `yaml:"enrolled_at"`
}

type deploymentToolConfig struct {
	ToolRef       string  `yaml:"tool_ref"`
	Side          string  `yaml:"side"`
	Kind          string  `yaml:"kind"`
	Frame         string  `yaml:"frame"`
	Joint         string  `yaml:"joint"`
	TravelM       float64 `yaml:"travel_m"`
	NormalForceN  float64 `yaml:"normal_force_n"`
	MaximumForceN float64 `yaml:"maximum_force_n"`
}

type robotDeployment struct {
	APIVersion int `yaml:"api_version"`
	Robot      struct {
		ID          string `yaml:"id"`
		DisplayName string `yaml:"display_name"`
		Model       string `yaml:"model"`
		Backend     string `yaml:"backend"`
		SDK         struct {
			Package         string            `yaml:"package"`
			Endpoint        string            `yaml:"endpoint,omitempty"`
			BackendProfile  string            `yaml:"backend_profile,omitempty"`
			FirmwareProfile string            `yaml:"firmware_profile"`
			SceneInstanceID string            `yaml:"scene_instance_id,omitempty"`
			Providers       map[string]string `yaml:"providers,omitempty"`
			Options         map[string]any    `yaml:"options,omitempty"`
		} `yaml:"sdk"`
		Frames     map[string]any         `yaml:"frames,omitempty"`
		Tools      []deploymentToolConfig `yaml:"tools,omitempty"`
		Kinematics struct {
			URDFPath               string                        `yaml:"urdf_path,omitempty"`
			PackageDirectories     []string                      `yaml:"package_directories,omitempty"`
			ControlledJointGroups  map[string][]string           `yaml:"controlled_joint_groups,omitempty"`
			DisabledCollisionPairs [][]string                    `yaml:"disabled_collision_pairs,omitempty"`
			NamedPostures          map[string]map[string]float64 `yaml:"named_postures,omitempty"`
		} `yaml:"kinematics,omitempty"`
		Safety map[string]any `yaml:"safety,omitempty"`
	} `yaml:"robot"`
	AbilityFramework struct {
		Endpoint          string `yaml:"endpoint"`
		ManagedByInstance bool   `yaml:"managed_by_instance"`
	} `yaml:"ability_framework"`
	Abilities map[string]map[string]any `yaml:"abilities"`
	Pilot     struct {
		ID                      string `yaml:"id"`
		RobotSkillDirectory     string `yaml:"robot_skill_directory,omitempty"`
		WorkerTimeoutSeconds    int    `yaml:"worker_timeout_seconds,omitempty"`
		HeartbeatIntervalSecond int    `yaml:"heartbeat_interval_seconds,omitempty"`
		AllowAbilityDebug       bool   `yaml:"allow_ability_debug"`
	} `yaml:"pilot"`
	RobotSkills []struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
		Enabled bool   `yaml:"enabled"`
	} `yaml:"robot_skills"`
}

func loadRobotDeployment(path string) (robotDeployment, error) {
	var deployment robotDeployment
	content, err := os.ReadFile(path)
	if err != nil {
		return deployment, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&deployment); err != nil {
		return deployment, fmt.Errorf("解析 RobotDeployment: %w", err)
	}
	if deployment.APIVersion != 1 || strings.TrimSpace(deployment.Robot.ID) == "" ||
		strings.TrimSpace(deployment.Robot.Model) == "" || strings.TrimSpace(deployment.Robot.Backend) == "" ||
		strings.TrimSpace(deployment.Robot.SDK.Package) == "" || strings.TrimSpace(deployment.AbilityFramework.Endpoint) == "" {
		return deployment, errors.New("RobotDeployment 缺少 api_version、Robot、SDK package 或 AbilityFramework endpoint")
	}
	if deployment.Pilot.ID == "" {
		deployment.Pilot.ID = "pilot-" + deployment.Robot.ID
	}
	if deployment.Robot.DisplayName == "" {
		deployment.Robot.DisplayName = deployment.Robot.ID
	}
	if deployment.Robot.SDK.BackendProfile == "" {
		deployment.Robot.SDK.BackendProfile = deployment.Robot.Backend + "-v1"
	}
	return deployment, nil
}

// Start 是普通用户启动一台 Robot 的唯一入口。首次启动通过 join code 换取
// 专用 Pilot credential；后续只读 connection.yaml，不再要求管理员 token。
func Start(ctx context.Context, options StartOptions) error {
	if options.DeploymentPath == "" {
		return errors.New("start 需要 --config")
	}
	deployment, err := loadRobotDeployment(options.DeploymentPath)
	if err != nil {
		return err
	}
	if options.DataDirectory == "" {
		options.DataDirectory, err = defaultDataDirectory(deployment.Robot.ID)
		if err != nil {
			return err
		}
	}
	dataDirectory, err := filepath.Abs(options.DataDirectory)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dataDirectory), 0o750); err != nil {
		return err
	}
	connectionPath := filepath.Join(dataDirectory, "connection.yaml")
	if _, statErr := os.Stat(connectionPath); os.IsNotExist(statErr) {
		if err := resolveServerByDiscovery(ctx, &options); err != nil {
			return err
		}
	}
	connection, err := loadOrClaimConnection(ctx, connectionPath, deployment.Pilot.ID, options)
	if err != nil {
		return err
	}
	bundleRoot, err := resolveBundleRoot(options.BundleDirectory)
	if err != nil {
		return err
	}
	deployment.Pilot.RobotSkillDirectory = filepath.Join(dataDirectory, "pilot", "skills")
	if deployment.Pilot.WorkerTimeoutSeconds == 0 {
		deployment.Pilot.WorkerTimeoutSeconds = 30
	}
	if deployment.Pilot.HeartbeatIntervalSecond == 0 {
		deployment.Pilot.HeartbeatIntervalSecond = 2
	}
	if deployment.Robot.Backend == "fake" {
		if deployment.Robot.SDK.Options == nil {
			deployment.Robot.SDK.Options = map[string]any{}
		}
		deployment.Robot.SDK.Options["state_path"] = filepath.Join(dataDirectory, "pilot", "robot-state.sqlite")
	}
	config := instanceConfigFromDeployment(deployment, connection, bundleRoot)
	instancePath := filepath.Join(dataDirectory, "instance.yaml")
	if _, statErr := os.Stat(instancePath); os.IsNotExist(statErr) {
		if err := renderInitialInstance(config, dataDirectory); err != nil {
			return err
		}
	} else if statErr != nil {
		return statErr
	} else if err := refreshRenderedConfiguration(config, dataDirectory); err != nil {
		return err
	}
	deploymentContent, err := yaml.Marshal(deployment)
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(dataDirectory, "robot-deployment.yaml"), deploymentContent, 0o640); err != nil {
		return err
	}
	if err := saveConnection(connectionPath, connection); err != nil {
		return err
	}
	return Run(ctx, dataDirectory)
}

func instanceConfigFromDeployment(deployment robotDeployment, connection connectionConfig, bundleRoot string) Config {
	config := Config{APIVersion: APIVersion, Kind: Kind,
		Metadata: Metadata{Name: deployment.Robot.ID}}
	config.Spec.Bundle = bundleRoot
	config.Spec.Robot = RobotConfig{ID: deployment.Robot.ID, DisplayName: deployment.Robot.DisplayName,
		Model: deployment.Robot.Model, Backend: deployment.Robot.Backend,
		BackendProfile: deployment.Robot.SDK.BackendProfile,
		SDKEndpoint:    deployment.Robot.SDK.Endpoint, FirmwareProfile: deployment.Robot.SDK.FirmwareProfile,
		SceneInstanceID: deployment.Robot.SDK.SceneInstanceID, Options: deployment.Robot.SDK.Options,
		URDFPath:           deployment.Robot.Kinematics.URDFPath,
		PackageDirectories: append([]string(nil), deployment.Robot.Kinematics.PackageDirectories...)}
	for _, tool := range deployment.Robot.Tools {
		config.Spec.Robot.Tools = append(config.Spec.Robot.Tools, ToolConfig{
			ToolRef: tool.ToolRef, Side: tool.Side, Kind: tool.Kind, Frame: tool.Frame, Joint: tool.Joint,
			TravelM: tool.TravelM, NormalForceN: tool.NormalForceN,
			MaximumForceN: tool.MaximumForceN,
		})
	}
	config.Spec.AbilityFramework = AbilityFrameworkConfig{Endpoint: deployment.AbilityFramework.Endpoint,
		Managed: deployment.AbilityFramework.ManagedByInstance, ListenAddress: "127.0.0.1"}
	config.Spec.SemanticServer = SemanticServerConfig{WebSocketURL: connection.ServerWSURL,
		HTTPURL: connection.ServerHTTPURL, AccessToken: connection.Credential}
	config.Spec.Pilot.ID = deployment.Pilot.ID
	return config
}

func renderInitialInstance(config Config, dataDirectory string) error {
	temporary, err := os.CreateTemp(filepath.Dir(dataDirectory), ".robot-instance-*.yaml")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	encoded, err := yaml.Marshal(config)
	if err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	_, err = Render(temporaryPath, dataDirectory)
	return err
}

func defaultDataDirectory(robotID string) (string, error) {
	base := strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("确定 Robot 默认数据目录: %w", err)
		}
		base = filepath.Join(home, ".local", "state")
	}
	// Robot ID 会进入本地目录名。这里只处理路径分隔和父目录标记，不引入另一套
	// 命名规则；Server/Pilot 仍使用 RobotDeployment 中的原始 ID 作为身份。
	safeRobotID := strings.ReplaceAll(strings.TrimSpace(robotID), "..", "_")
	safeRobotID = strings.ReplaceAll(safeRobotID, "/", "_")
	safeRobotID = strings.ReplaceAll(safeRobotID, string(filepath.Separator), "_")
	if safeRobotID == "" {
		return "", errors.New("Robot ID 不能为空")
	}
	return filepath.Join(base, "semantic", "robots", safeRobotID), nil
}

func loadOrClaimConnection(ctx context.Context, path, pilotID string, options StartOptions) (connectionConfig, error) {
	if content, err := os.ReadFile(path); err == nil {
		var result connectionConfig
		if yaml.Unmarshal(content, &result) != nil || result.PilotID != pilotID || result.Credential == "" {
			return result, errors.New("connection.yaml 无效或不属于当前 Pilot")
		}
		return result, nil
	}
	if options.JoinCode == "" || options.ServerHTTPURL == "" || options.ServerWSURL == "" {
		return connectionConfig{}, errors.New("首次启动需要 --join-code、--server-http 和 --server-ws")
	}
	requestBody, _ := json.Marshal(map[string]string{"join_code": options.JoinCode, "pilot_id": pilotID})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(options.ServerHTTPURL, "/")+"/api/v1/pilot-enrollments/claim", bytes.NewReader(requestBody))
	if err != nil {
		return connectionConfig{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return connectionConfig{}, fmt.Errorf("claim Pilot 加入码: %w", err)
	}
	defer response.Body.Close()
	var payload struct {
		Credential string `json:"credential"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&payload) != nil || payload.Credential == "" {
		return connectionConfig{}, fmt.Errorf("claim Pilot 加入码失败: HTTP %d", response.StatusCode)
	}
	return connectionConfig{ServerHTTPURL: strings.TrimRight(options.ServerHTTPURL, "/"),
		ServerWSURL: options.ServerWSURL, PilotID: pilotID, Credential: payload.Credential,
		EnrolledAt: time.Now().UTC()}, nil
}

func saveConnection(path string, connection connectionConfig) error {
	content, err := yaml.Marshal(connection)
	if err != nil {
		return err
	}
	return atomicWrite(path, content, 0o600)
}

func resolveBundleRoot(configured string) (string, error) {
	if configured != "" {
		return filepath.Abs(configured)
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(filepath.Dir(executable), ".."))
}
