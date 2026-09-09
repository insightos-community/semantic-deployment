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
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	APIVersion = "semantic.insightos.cn/v1alpha1"
	Kind       = "RobotInstance"
)

// Config 只保存一个 Robot 实例的差异项。Ability 不再逐个保存 Robot Endpoint，
// 它们统一读取 render 后的 SEMANTIC_ROBOT_CONFIG。
type Config struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       Spec     `yaml:"spec" json:"spec"`
}

type Metadata struct {
	Name string `yaml:"name" json:"name"`
}

type Spec struct {
	Bundle           string                 `yaml:"bundle" json:"bundle"`
	Robot            RobotConfig            `yaml:"robot" json:"robot"`
	AbilityFramework AbilityFrameworkConfig `yaml:"abilityFramework" json:"abilityFramework"`
	SemanticServer   SemanticServerConfig   `yaml:"semanticServer" json:"semanticServer"`
	Pilot            PilotConfig            `yaml:"pilot" json:"pilot"`
}

type RobotConfig struct {
	ID                 string         `yaml:"id" json:"id"`
	DisplayName        string         `yaml:"displayName" json:"displayName"`
	Model              string         `yaml:"model" json:"model"`
	Backend            string         `yaml:"backend" json:"backend"`
	BackendProfile     string         `yaml:"backendProfile" json:"backendProfile"`
	SDKEndpoint        string         `yaml:"sdkEndpoint,omitempty" json:"sdkEndpoint,omitempty"`
	FirmwareProfile    string         `yaml:"firmwareProfile,omitempty" json:"firmwareProfile,omitempty"`
	URDFPath           string         `yaml:"urdfPath,omitempty" json:"urdfPath,omitempty"`
	PackageDirectories []string       `yaml:"packageDirectories,omitempty" json:"packageDirectories,omitempty"`
	Options            map[string]any `yaml:"options,omitempty" json:"options,omitempty"`
	SceneInstanceID    string         `yaml:"sceneInstanceId,omitempty" json:"sceneInstanceId,omitempty"`
	Tools              []ToolConfig   `yaml:"tools,omitempty" json:"tools,omitempty"`
}

// ToolConfig 保留 Runtime Robot Profile 已公开的机械边界。Framework 只负责
// 搬运这份描述，具体的 frame、joint 和力限制仍由 Robot 类型包与 SDK 解释。
type ToolConfig struct {
	ToolRef       string  `yaml:"toolRef" json:"toolRef"`
	Side          string  `yaml:"side" json:"side"`
	Kind          string  `yaml:"kind" json:"kind"`
	Frame         string  `yaml:"frame" json:"frame"`
	Joint         string  `yaml:"joint" json:"joint"`
	TravelM       float64 `yaml:"travelM" json:"travelM"`
	NormalForceN  float64 `yaml:"normalForceN" json:"normalForceN"`
	MaximumForceN float64 `yaml:"maximumForceN" json:"maximumForceN"`
}

type AbilityFrameworkConfig struct {
	Endpoint      string `yaml:"endpoint" json:"endpoint"`
	Managed       bool   `yaml:"managed" json:"managed"`
	ListenAddress string `yaml:"listenAddress,omitempty" json:"listenAddress,omitempty"`
}

type SemanticServerConfig struct {
	WebSocketURL string `yaml:"websocketURL" json:"websocketURL"`
	HTTPURL      string `yaml:"httpURL" json:"httpURL"`
	AccessToken  string `yaml:"accessToken" json:"accessToken"`
}

type PilotConfig struct {
	ID string `yaml:"id" json:"id"`
}

func LoadConfig(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()
	config, err := DecodeConfig(file)
	if err != nil {
		return Config{}, err
	}
	if !filepath.IsAbs(config.Spec.Bundle) {
		config.Spec.Bundle = filepath.Join(filepath.Dir(path), config.Spec.Bundle)
	}
	config.Spec.Bundle, err = filepath.Abs(config.Spec.Bundle)
	return config, err
}

func DecodeConfig(reader io.Reader) (Config, error) {
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("解析 RobotInstance 失败: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Config{}, errors.New("RobotInstance 只能包含一个 YAML 文档")
		}
		return Config{}, err
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config *Config) Validate() error {
	if config.APIVersion != APIVersion || config.Kind != Kind {
		return fmt.Errorf("RobotInstance 必须使用 apiVersion=%s、kind=%s", APIVersion, Kind)
	}
	values := map[string]string{
		"metadata.name":                    config.Metadata.Name,
		"spec.bundle":                      config.Spec.Bundle,
		"spec.robot.id":                    config.Spec.Robot.ID,
		"spec.robot.displayName":           config.Spec.Robot.DisplayName,
		"spec.robot.model":                 config.Spec.Robot.Model,
		"spec.robot.backend":               config.Spec.Robot.Backend,
		"spec.robot.backendProfile":        config.Spec.Robot.BackendProfile,
		"spec.robot.firmwareProfile":       config.Spec.Robot.FirmwareProfile,
		"spec.abilityFramework.endpoint":   config.Spec.AbilityFramework.Endpoint,
		"spec.semanticServer.websocketURL": config.Spec.SemanticServer.WebSocketURL,
		"spec.semanticServer.httpURL":      config.Spec.SemanticServer.HTTPURL,
		"spec.semanticServer.accessToken":  config.Spec.SemanticServer.AccessToken,
		"spec.pilot.id":                    config.Spec.Pilot.ID,
	}
	for label, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s 必填", label)
		}
	}
	if err := validateURL("spec.abilityFramework.endpoint", config.Spec.AbilityFramework.Endpoint, "http", "https"); err != nil {
		return err
	}
	if _, err := parseHTTPEndpoint(config.Spec.AbilityFramework.Endpoint); err != nil {
		return err
	}
	if err := validateURL("spec.semanticServer.websocketURL", config.Spec.SemanticServer.WebSocketURL, "ws", "wss"); err != nil {
		return err
	}
	if err := validateURL("spec.semanticServer.httpURL", config.Spec.SemanticServer.HTTPURL, "http", "https"); err != nil {
		return err
	}
	if config.Spec.Robot.SDKEndpoint != "" {
		if err := validateURL("spec.robot.sdkEndpoint", config.Spec.Robot.SDKEndpoint, "http", "https"); err != nil {
			return err
		}
	}
	if config.Spec.Robot.Backend == "mujoco" {
		if config.Spec.Robot.SDKEndpoint == "" {
			return errors.New("MuJoCo 实例必须配置 spec.robot.sdkEndpoint")
		}
		if strings.TrimSpace(config.Spec.Robot.SceneInstanceID) == "" {
			return errors.New("MuJoCo 实例必须配置 spec.robot.sceneInstanceId")
		}
		if len(config.Spec.Robot.Tools) == 0 {
			return errors.New("MuJoCo 实例必须携带 Runtime Robot Profile 的工具描述")
		}
		if strings.TrimSpace(config.Spec.Robot.URDFPath) == "" {
			return errors.New("R1 Pro MuJoCo 本地 IK 必须配置 spec.robot.urdfPath")
		}
	}
	if config.Spec.Robot.Options == nil {
		config.Spec.Robot.Options = make(map[string]any)
	}
	if config.Spec.AbilityFramework.ListenAddress == "" {
		config.Spec.AbilityFramework.ListenAddress = "127.0.0.1"
	}
	return nil
}

func validateURL(label, value string, schemes ...string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%s 不是有效 URL", label)
	}
	for _, scheme := range schemes {
		if parsed.Scheme == scheme {
			return nil
		}
	}
	return fmt.Errorf("%s 必须使用 %s", label, strings.Join(schemes, " 或 "))
}
