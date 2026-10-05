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

package bundle

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	APIVersion = "semantic.insightos.cn/v1alpha1"
	Kind       = "RobotRuntimeBundle"
)

// Manifest 描述一个可重复使用的 Robot 型号运行包。这里只保存运行所需文件与
// 进程边界；Robot ID、Endpoint 和凭据属于实例配置，不能写入共享 bundle。
type Manifest struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       Spec     `yaml:"spec" json:"spec"`
}

type Metadata struct {
	Name        string            `yaml:"name" json:"name"`
	Version     string            `yaml:"version" json:"version"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
}

type Spec struct {
	Robot     RobotSpec     `yaml:"robot" json:"robot"`
	Platform  PlatformSpec  `yaml:"platform,omitempty" json:"platform,omitempty"`
	Artifacts ArtifactSpec  `yaml:"artifacts" json:"artifacts"`
	Templates TemplateSpec  `yaml:"templates" json:"templates"`
	Runtime   RuntimePolicy `yaml:"runtime,omitempty" json:"runtime,omitempty"`
}

type RobotSpec struct {
	Model             string           `yaml:"model" json:"model"`
	SDKPackage        string           `yaml:"sdkPackage" json:"sdkPackage"`
	BackendProfiles   []BackendProfile `yaml:"backendProfiles" json:"backendProfiles"`
	DefaultSDKOptions map[string]any   `yaml:"defaultSDKOptions,omitempty" json:"defaultSDKOptions,omitempty"`
}

type BackendProfile struct {
	Backend string `yaml:"backend" json:"backend"`
	Profile string `yaml:"profile" json:"profile"`
}

type PlatformSpec struct {
	OS   string `yaml:"os,omitempty" json:"os,omitempty"`
	Arch string `yaml:"arch,omitempty" json:"arch,omitempty"`
}

type ArtifactSpec struct {
	InstanceLauncher string            `yaml:"instanceLauncher" json:"instanceLauncher"`
	AbilityFramework string            `yaml:"abilityFramework" json:"abilityFramework"`
	Pilot            string            `yaml:"pilot" json:"pilot"`
	RobotSkillSDK    string            `yaml:"robotSkillSDK" json:"robotSkillSDK"`
	PythonExecutable string            `yaml:"pythonExecutable" json:"pythonExecutable"`
	PythonWheels     []string          `yaml:"pythonWheels,omitempty" json:"pythonWheels,omitempty"`
	Abilities        []AbilityArtifact `yaml:"abilities" json:"abilities"`
}

type AbilityArtifact struct {
	Role        string `yaml:"role" json:"role"`
	Template    string `yaml:"template" json:"template"`
	AbilityName string `yaml:"abilityName" json:"abilityName"`
	File        string `yaml:"file" json:"file"`
}

type TemplateSpec struct {
	RobotDeployment  string `yaml:"robotDeployment" json:"robotDeployment"`
	AbilityFramework string `yaml:"abilityFramework" json:"abilityFramework"`
	ModelRegistry    string `yaml:"modelRegistry,omitempty" json:"modelRegistry,omitempty"`
}

type RuntimePolicy struct {
	ReadinessTimeout string `yaml:"readinessTimeout,omitempty" json:"readinessTimeout,omitempty"`
	ShutdownTimeout  string `yaml:"shutdownTimeout,omitempty" json:"shutdownTimeout,omitempty"`
}

func Load(path string) (Manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer file.Close()
	return Decode(file)
}

func Decode(reader io.Reader) (Manifest, error) {
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("解析 bundle.yaml 失败: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Manifest{}, errors.New("bundle.yaml 只能包含一个 YAML 文档")
		}
		return Manifest{}, fmt.Errorf("解析 bundle.yaml 结尾失败: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (manifest Manifest) Validate() error {
	if manifest.APIVersion != APIVersion || manifest.Kind != Kind {
		return fmt.Errorf("bundle 必须使用 apiVersion=%s、kind=%s", APIVersion, Kind)
	}
	if strings.TrimSpace(manifest.Metadata.Name) == "" || strings.TrimSpace(manifest.Metadata.Version) == "" {
		return errors.New("bundle metadata.name 和 metadata.version 必填")
	}
	if strings.TrimSpace(manifest.Spec.Robot.Model) == "" ||
		strings.TrimSpace(manifest.Spec.Robot.SDKPackage) == "" ||
		len(manifest.Spec.Robot.BackendProfiles) == 0 {
		return errors.New("bundle spec.robot.model、sdkPackage 和 backendProfiles 必填")
	}
	seenProfiles := map[string]bool{}
	for _, profile := range manifest.Spec.Robot.BackendProfiles {
		key := strings.TrimSpace(profile.Backend) + "\x00" + strings.TrimSpace(profile.Profile)
		if strings.TrimSpace(profile.Backend) == "" || strings.TrimSpace(profile.Profile) == "" || seenProfiles[key] {
			return errors.New("bundle robot.backendProfiles 不能包含空值或重复组合")
		}
		seenProfiles[key] = true
	}
	if manifest.Spec.Platform.OS != "" && manifest.Spec.Platform.OS != runtime.GOOS {
		return fmt.Errorf("bundle 只支持 %s，当前系统为 %s", manifest.Spec.Platform.OS, runtime.GOOS)
	}
	if manifest.Spec.Platform.Arch != "" && manifest.Spec.Platform.Arch != runtime.GOARCH {
		return fmt.Errorf("bundle 只支持 %s，当前架构为 %s", manifest.Spec.Platform.Arch, runtime.GOARCH)
	}
	for label, path := range map[string]string{
		"artifacts.instanceLauncher": manifest.Spec.Artifacts.InstanceLauncher,
		"artifacts.abilityFramework": manifest.Spec.Artifacts.AbilityFramework,
		"artifacts.pilot":            manifest.Spec.Artifacts.Pilot,
		"artifacts.robotSkillSDK":    manifest.Spec.Artifacts.RobotSkillSDK,
		"artifacts.pythonExecutable": manifest.Spec.Artifacts.PythonExecutable,
		"templates.robotDeployment":  manifest.Spec.Templates.RobotDeployment,
		"templates.abilityFramework": manifest.Spec.Templates.AbilityFramework,
	} {
		if err := validateRelativePath(label, path); err != nil {
			return err
		}
	}
	if manifest.Spec.Templates.ModelRegistry != "" {
		if err := validateRelativePath("templates.modelRegistry", manifest.Spec.Templates.ModelRegistry); err != nil {
			return err
		}
	}
	for index, path := range manifest.Spec.Artifacts.PythonWheels {
		if err := validateRelativePath(fmt.Sprintf("artifacts.pythonWheels[%d]", index), path); err != nil {
			return err
		}
	}
	if len(manifest.Spec.Artifacts.Abilities) == 0 {
		return errors.New("bundle 至少需要一个 Ability 包")
	}
	roles, templates, abilityNames := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for index, ability := range manifest.Spec.Artifacts.Abilities {
		if strings.TrimSpace(ability.Role) == "" || strings.TrimSpace(ability.Template) == "" || strings.TrimSpace(ability.AbilityName) == "" {
			return fmt.Errorf("artifacts.abilities[%d] 的 role、template、abilityName 必填", index)
		}
		if roles[ability.Role] || templates[ability.Template] || abilityNames[ability.AbilityName] {
			return fmt.Errorf("Ability role、template 和 abilityName 必须分别唯一: %s", ability.Role)
		}
		roles[ability.Role], templates[ability.Template], abilityNames[ability.AbilityName] = true, true, true
		if err := validateRelativePath(fmt.Sprintf("artifacts.abilities[%d].file", index), ability.File); err != nil {
			return err
		}
	}
	for label, value := range map[string]string{
		"runtime.readinessTimeout": manifest.Spec.Runtime.ReadinessTimeout,
		"runtime.shutdownTimeout":  manifest.Spec.Runtime.ShutdownTimeout,
	} {
		if value == "" {
			continue
		}
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return fmt.Errorf("%s 必须是正数时间长度", label)
		}
	}
	return nil
}

func validateRelativePath(label, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s 必填", label)
	}
	clean := filepath.Clean(value)
	if filepath.IsAbs(value) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s 必须是 bundle 内的相对路径", label)
	}
	return nil
}

func (manifest Manifest) ReferencedFiles() []string {
	files := []string{
		manifest.Spec.Artifacts.InstanceLauncher,
		manifest.Spec.Artifacts.AbilityFramework,
		manifest.Spec.Artifacts.Pilot,
		manifest.Spec.Artifacts.RobotSkillSDK,
		manifest.Spec.Artifacts.PythonExecutable,
		manifest.Spec.Templates.RobotDeployment,
		manifest.Spec.Templates.AbilityFramework,
	}
	files = append(files, manifest.Spec.Artifacts.PythonWheels...)
	for _, ability := range manifest.Spec.Artifacts.Abilities {
		files = append(files, ability.File)
	}
	if manifest.Spec.Templates.ModelRegistry != "" {
		files = append(files, manifest.Spec.Templates.ModelRegistry)
	}
	sort.Strings(files)
	return files
}

func (manifest Manifest) ReadinessTimeout() time.Duration {
	if manifest.Spec.Runtime.ReadinessTimeout == "" {
		return 30 * time.Second
	}
	value, _ := time.ParseDuration(manifest.Spec.Runtime.ReadinessTimeout)
	return value
}

func (manifest Manifest) ShutdownTimeout() time.Duration {
	if manifest.Spec.Runtime.ShutdownTimeout == "" {
		return 10 * time.Second
	}
	value, _ := time.ParseDuration(manifest.Spec.Runtime.ShutdownTimeout)
	return value
}

func (manifest Manifest) Supports(model, backend, backendProfile string) error {
	if manifest.Spec.Robot.Model != model {
		return fmt.Errorf("bundle Robot 型号为 %s，实例请求 %s", manifest.Spec.Robot.Model, model)
	}
	// SDK 包是类型包的内部组成，不是实例选择条件；实例重复声明只会产生两份事实
	// 和无意义的“不一致”拒绝。运行配置始终由已选择 bundle 写入精确 SDK 包名。

	for _, value := range manifest.Spec.Robot.BackendProfiles {
		if value.Backend == backend && value.Profile == backendProfile {
			return nil
		}
	}
	return fmt.Errorf("bundle %s 不支持 backend/profile %s/%s", manifest.Metadata.Name, backend, backendProfile)
}

type Bundle struct {
	Root     string   `json:"root"`
	Manifest Manifest `json:"manifest"`
}

func Open(root string) (Bundle, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Bundle{}, err
	}
	manifest, err := Load(filepath.Join(abs, "bundle.yaml"))
	if err != nil {
		return Bundle{}, err
	}
	for _, relative := range manifest.ReferencedFiles() {
		path := filepath.Join(abs, relative)
		info, err := os.Stat(path)
		if err != nil {
			return Bundle{}, fmt.Errorf("bundle 缺少 %s: %w", relative, err)
		}
		if !info.Mode().IsRegular() {
			return Bundle{}, fmt.Errorf("bundle 文件不是普通文件: %s", relative)
		}
	}
	return Bundle{Root: abs, Manifest: manifest}, nil
}

func (bundle Bundle) Path(relative string) string {
	return filepath.Join(bundle.Root, relative)
}
