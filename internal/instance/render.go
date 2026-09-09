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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"
	"insightos.cn/semantic-robot-deployment/internal/bundle"
)

type renderData struct {
	Instance          Config
	InstanceDirectory string
	BundleDirectory   string
	AFHost            string
	AFPort            string
	RobotStatePath    string
	ModelRegistryPath string
	SkillDirectory    string
	SDKPackage        string
	SDKOptions        map[string]any
}

// Render 建立一个 Robot 的独立可写目录。共享 bundle 中的任何文件都不会被修改。
func Render(configPath, outputDirectory string) (State, error) {
	config, err := LoadConfig(configPath)
	if err != nil {
		return State{}, err
	}
	opened, err := bundle.Open(config.Spec.Bundle)
	if err != nil {
		return State{}, err
	}
	if err := opened.Manifest.Supports(config.Spec.Robot.Model,
		config.Spec.Robot.Backend, config.Spec.Robot.BackendProfile); err != nil {
		return State{}, err
	}
	outputDirectory, err = filepath.Abs(outputDirectory)
	if err != nil {
		return State{}, err
	}
	if info, statErr := os.Stat(outputDirectory); statErr == nil {
		if !info.IsDir() {
			return State{}, fmt.Errorf("实例路径不是目录: %s", outputDirectory)
		}
		entries, readErr := os.ReadDir(outputDirectory)
		if readErr != nil {
			return State{}, readErr
		}
		if len(entries) != 0 {
			return State{}, fmt.Errorf("实例目录必须为空，拒绝覆盖: %s", outputDirectory)
		}
	} else if !os.IsNotExist(statErr) {
		return State{}, statErr
	}
	directories := []string{
		"run", "executions",
		"pilot/artifacts", "pilot/logs", "pilot/python-cache",
		"pilot/skills/active", "pilot/skills/packages", "pilot/skills/environments", "pilot/skills/staging",
		"ability-framework/packages", "ability-framework/crs", "ability-framework/databases",
		"ability-framework/data", "ability-framework/log", "ability-framework/artifact-exchange",
	}
	for _, relative := range directories {
		if err := os.MkdirAll(filepath.Join(outputDirectory, relative), 0o750); err != nil {
			return State{}, err
		}
	}
	endpoint, err := parseHTTPEndpoint(config.Spec.AbilityFramework.Endpoint)
	if err != nil {
		return State{}, err
	}
	data := renderData{
		Instance: config, InstanceDirectory: outputDirectory, BundleDirectory: opened.Root,
		AFHost: config.Spec.AbilityFramework.ListenAddress, AFPort: endpoint.Port(),
		RobotStatePath: filepath.Join(outputDirectory, "pilot", "robot-state.sqlite"),
		SkillDirectory: filepath.Join(outputDirectory, "pilot", "skills"),
		SDKPackage:     opened.Manifest.Spec.Robot.SDKPackage,
		SDKOptions:     mergeSDKOptions(opened.Manifest.Spec.Robot.DefaultSDKOptions, config.Spec.Robot.Options),
	}
	if opened.Manifest.Spec.Templates.ModelRegistry != "" {
		data.ModelRegistryPath = filepath.Join(outputDirectory, "model-registry.json")
		if err := copyFile(opened.Path(opened.Manifest.Spec.Templates.ModelRegistry), data.ModelRegistryPath, 0o640); err != nil {
			return State{}, err
		}
	}
	if err := renderTemplate(opened.Path(opened.Manifest.Spec.Templates.RobotDeployment),
		filepath.Join(outputDirectory, "robot-deployment.yaml"), data); err != nil {
		return State{}, err
	}
	if err := renderTemplate(opened.Path(opened.Manifest.Spec.Templates.AbilityFramework),
		filepath.Join(outputDirectory, "ability-framework", "config.yaml"), data); err != nil {
		return State{}, err
	}
	encoded, err := yaml.Marshal(config)
	if err != nil {
		return State{}, err
	}
	if err := atomicWrite(filepath.Join(outputDirectory, "instance.yaml"), encoded, 0o600); err != nil {
		return State{}, err
	}
	metadata := map[string]any{
		"bundle_root": opened.Root, "bundle_name": opened.Manifest.Metadata.Name,
		"bundle_version": opened.Manifest.Metadata.Version, "rendered_at": time.Now().UTC(),
	}
	metadataJSON, _ := json.MarshalIndent(metadata, "", "  ")
	if err := atomicWrite(filepath.Join(outputDirectory, "run", "bundle.json"), append(metadataJSON, '\n'), 0o640); err != nil {
		return State{}, err
	}
	state := State{InstanceName: config.Metadata.Name, RobotID: config.Spec.Robot.ID, Status: StatusRendered}
	if err := writeState(outputDirectory, &state); err != nil {
		return State{}, err
	}
	return ReadState(outputDirectory)
}

// mergeSDKOptions 将型号类型包的稳定默认值与单台 Robot 的差异配置合并。
// 默认值属于类型包，避免通用启动器认识 R1 Pro 的关节公差等型号细节；实例值
// 只覆盖明确声明的键，因而托管仿真和真机部署仍共用同一套渲染流程。
func mergeSDKOptions(defaults, instance map[string]any) map[string]any {
	merged := make(map[string]any, len(defaults)+len(instance))
	for key, value := range defaults {
		merged[key] = value
	}
	for key, value := range instance {
		merged[key] = value
	}
	return merged
}

// refreshRenderedConfiguration 在实例停止后刷新类型包派生文件，保留实例数据。
//
// RobotDeployment、AbilityFramework 配置和 model registry 都来自当前 bundle。
// 它们不能在升级后继续使用实例目录中的旧副本；Pilot 数据库、Skill、Artifact
// 与 Execution 则是运行状态，不能为了刷新配置而删除。这里先在相邻临时目录
// 完整执行既有 Render，全部成功后再逐文件原子替换，避免维护第二套模板逻辑。
func refreshRenderedConfiguration(config Config, outputDirectory string) error {
	state, err := ReadState(outputDirectory)
	if err != nil {
		return err
	}
	if state.Status == StatusStarting || state.Status == StatusRunning || state.Status == StatusStopping {
		return fmt.Errorf("实例当前状态为 %s，拒绝刷新运行中的配置", state.Status)
	}
	parent := filepath.Dir(outputDirectory)
	configFile, err := os.CreateTemp(parent, ".robot-instance-refresh-*.yaml")
	if err != nil {
		return err
	}
	configPath := configFile.Name()
	defer os.Remove(configPath)
	encoded, err := yaml.Marshal(config)
	if err != nil {
		configFile.Close()
		return err
	}
	if _, err := configFile.Write(encoded); err != nil {
		configFile.Close()
		return err
	}
	if err := configFile.Close(); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".robot-instance-refresh-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if _, err := Render(configPath, staging); err != nil {
		return err
	}
	type renderedFile struct {
		relative string
		mode     os.FileMode
	}
	files := []renderedFile{
		{relative: "instance.yaml", mode: 0o600},
		{relative: "robot-deployment.yaml", mode: 0o640},
		{relative: "ability-framework/config.yaml", mode: 0o640},
		{relative: "run/bundle.json", mode: 0o640},
	}
	if _, err := os.Stat(filepath.Join(staging, "model-registry.json")); err == nil {
		files = append(files, renderedFile{relative: "model-registry.json", mode: 0o640})
	} else if !os.IsNotExist(err) {
		return err
	} else if err := os.Remove(filepath.Join(outputDirectory, "model-registry.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, file := range files {
		content, err := os.ReadFile(filepath.Join(staging, file.relative))
		if err != nil {
			return err
		}
		if err := atomicWrite(filepath.Join(outputDirectory, file.relative), content, file.mode); err != nil {
			return err
		}
	}
	return nil
}

func renderTemplate(source, target string, data renderData) error {
	raw, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	tmpl, err := template.New(filepath.Base(source)).Option("missingkey=error").Funcs(template.FuncMap{
		"quote": strconv.Quote,
		"json": func(value any) (string, error) {
			encoded, err := json.Marshal(value)
			return string(encoded), err
		},
	}).Parse(string(raw))
	if err != nil {
		return fmt.Errorf("解析模板 %s: %w", source, err)
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, data); err != nil {
		return fmt.Errorf("render 模板 %s: %w", source, err)
	}
	return atomicWrite(target, output.Bytes(), 0o640)
}

func copyFile(source, target string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, path)
}
