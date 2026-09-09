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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type FileMapping struct {
	Target string
	Source string
}

type BuildOptions struct {
	PythonExecutable string
	WheelDirectory   string
}

func ParseFileMapping(value string) (FileMapping, error) {
	target, source, ok := strings.Cut(value, "=")
	if !ok || strings.TrimSpace(target) == "" || strings.TrimSpace(source) == "" {
		return FileMapping{}, errors.New("--file 必须使用 bundle/relative/path=/source/path")
	}
	if err := validateRelativePath("--file target", target); err != nil {
		return FileMapping{}, err
	}
	return FileMapping{Target: filepath.Clean(target), Source: source}, nil
}

// Build 把类型包模板和显式提供的已构建产物组装成一个目录。它不会重新编译
// Wheel、Ability 或 Go 二进制；只把已构建 Wheel 安装到 bundle 共享环境。
func Build(source, output string, mappings []FileMapping) (Bundle, error) {
	return BuildWithOptions(source, output, mappings, BuildOptions{})
}

func BuildWithOptions(source, output string, mappings []FileMapping, options BuildOptions) (Bundle, error) {
	sourceAbs, err := filepath.Abs(source)
	if err != nil {
		return Bundle{}, err
	}
	outputAbs, err := filepath.Abs(output)
	if err != nil {
		return Bundle{}, err
	}
	if _, err := os.Stat(outputAbs); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return Bundle{}, fmt.Errorf("输出目录已存在，拒绝覆盖: %s", outputAbs)
		}
		return Bundle{}, err
	}
	manifest, err := Load(filepath.Join(sourceAbs, "bundle.yaml"))
	if err != nil {
		return Bundle{}, err
	}
	declared := map[string]bool{}
	for _, path := range manifest.ReferencedFiles() {
		declared[filepath.Clean(path)] = true
	}
	seen := map[string]bool{}
	for _, mapping := range mappings {
		if !declared[mapping.Target] {
			return Bundle{}, fmt.Errorf("--file 目标未在 bundle.yaml 声明: %s", mapping.Target)
		}
		if seen[mapping.Target] {
			return Bundle{}, fmt.Errorf("--file 目标重复: %s", mapping.Target)
		}
		seen[mapping.Target] = true
		info, err := os.Stat(mapping.Source)
		if err != nil {
			return Bundle{}, fmt.Errorf("读取构建产物 %s: %w", mapping.Source, err)
		}
		if !info.Mode().IsRegular() {
			return Bundle{}, fmt.Errorf("构建产物必须是普通文件: %s", mapping.Source)
		}
	}
	// 类型包清单仍然精确声明每个 Wheel；--wheel-dir 只是免去调用方重复书写
	// 二十余个 --file 映射。它按清单中的文件名补齐来源，不扫描或安装未声明
	// 的包，因此不会把构建机环境中的偶然依赖带进产品包。
	if options.WheelDirectory != "" {
		for _, target := range manifest.Spec.Artifacts.PythonWheels {
			cleanTarget := filepath.Clean(target)
			if seen[cleanTarget] {
				continue
			}
			source := filepath.Join(options.WheelDirectory, filepath.Base(cleanTarget))
			info, statErr := os.Stat(source)
			if statErr != nil {
				return Bundle{}, fmt.Errorf("Wheel 目录缺少清单制品 %s: %w", filepath.Base(cleanTarget), statErr)
			}
			if !info.Mode().IsRegular() {
				return Bundle{}, fmt.Errorf("Wheel 制品必须是普通文件: %s", source)
			}
			mappings = append(mappings, FileMapping{Target: cleanTarget, Source: source})
			seen[cleanTarget] = true
		}
	}
	parent := filepath.Dir(outputAbs)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Bundle{}, err
	}
	temporary, err := os.MkdirTemp(parent, ".semantic-robot-bundle-")
	if err != nil {
		return Bundle{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Chmod(temporary, 0o755)
			_ = os.RemoveAll(temporary)
		}
	}()
	if err := copyTemplateTree(sourceAbs, temporary); err != nil {
		return Bundle{}, err
	}
	for _, mapping := range mappings {
		target := filepath.Join(temporary, mapping.Target)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return Bundle{}, err
		}
		if err := copyRegularFile(mapping.Source, target, 0o644); err != nil {
			return Bundle{}, err
		}
	}
	// Python 环境可以由 build 在下一步生成，其他清单文件必须在安装前就齐全。
	// 这样 MuJoCo 的 WebSocket 等离线依赖不会拖到 Ability 启动时才暴露。
	if err := validateBuildInputs(temporary, manifest); err != nil {
		return Bundle{}, err
	}
	pythonTarget := filepath.Join(temporary, manifest.Spec.Artifacts.PythonExecutable)
	if _, err := os.Stat(pythonTarget); errors.Is(err, os.ErrNotExist) {
		if len(manifest.Spec.Artifacts.PythonWheels) == 0 {
			return Bundle{}, errors.New("bundle 缺少共享 Python，且没有声明可安装的 pythonWheels")
		}
		if options.PythonExecutable == "" {
			options.PythonExecutable = "python3"
		}
		venvRoot := filepath.Dir(filepath.Dir(pythonTarget))
		// Bundle 必须在没有宿主机 site-packages 的机器上仍可启动。过去继承
		// Conda/ROS 环境会掩盖未声明依赖，直到换机器后 Ability 才在 on_connect
		// 阶段失败；因此这里创建真正隔离的 venv，所有运行依赖都来自清单 Wheel。
		command := exec.CommandContext(context.Background(), options.PythonExecutable,
			"-m", "venv", venvRoot)
		if output, err := command.CombinedOutput(); err != nil {
			return Bundle{}, fmt.Errorf("创建 bundle 共享 Python 环境失败: %w: %s",
				err, strings.TrimSpace(string(output)))
		}
		arguments := []string{"-m", "pip", "install", "--no-index", "--no-deps", "--force-reinstall"}
		for _, wheel := range manifest.Spec.Artifacts.PythonWheels {
			arguments = append(arguments, filepath.Join(temporary, wheel))
		}
		command = exec.CommandContext(context.Background(), pythonTarget, arguments...)
		if output, err := command.CombinedOutput(); err != nil {
			return Bundle{}, fmt.Errorf("安装 bundle Python wheel 失败: %w: %s",
				err, strings.TrimSpace(string(output)))
		}
	} else if err != nil {
		return Bundle{}, err
	}
	built, err := Open(temporary)
	if err != nil {
		return Bundle{}, fmt.Errorf("组装后的 bundle 不完整: %w", err)
	}
	if err := makeReadOnly(temporary, built.Manifest); err != nil {
		return Bundle{}, err
	}
	if err := os.Rename(temporary, outputAbs); err != nil {
		return Bundle{}, err
	}
	committed = true
	return Open(outputAbs)
}

func validateBuildInputs(root string, manifest Manifest) error {
	generatedPython := filepath.Clean(manifest.Spec.Artifacts.PythonExecutable)
	for _, relative := range manifest.ReferencedFiles() {
		if filepath.Clean(relative) == generatedPython {
			continue
		}
		info, err := os.Stat(filepath.Join(root, relative))
		if err != nil {
			return fmt.Errorf("bundle 构建缺少清单文件 %s: %w", relative, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("bundle 构建输入不是普通文件: %s", relative)
		}
	}
	return nil
}

func copyTemplateTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." {
			return err
		}
		destination := filepath.Join(target, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("类型包模板不能包含符号链接: %s", path)
		}
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("类型包模板只能包含目录和普通文件: %s", path)
		}
		return copyRegularFile(path, destination, 0o644)
	})
}

func copyRegularFile(source, target string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
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

func makeReadOnly(root string, manifest Manifest) error {
	executables := map[string]bool{
		filepath.Clean(manifest.Spec.Artifacts.InstanceLauncher): true,
		filepath.Clean(manifest.Spec.Artifacts.AbilityFramework): true,
		filepath.Clean(manifest.Spec.Artifacts.Pilot):            true,
		filepath.Clean(manifest.Spec.Artifacts.PythonExecutable): true,
	}
	var directories []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			directories = append(directories, path)
			return nil
		}
		// venv 中的 python 常为系统解释器的符号链接，禁止跟随链接修改系统文件。
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o444)
		if executables[filepath.Clean(relative)] {
			mode = 0o555
		}
		return os.Chmod(path, mode)
	})
	if err != nil {
		return err
	}
	sort.Slice(directories, func(i, j int) bool { return len(directories[i]) > len(directories[j]) })
	for _, directory := range directories {
		if err := os.Chmod(directory, 0o555); err != nil {
			return err
		}
	}
	return nil
}

type Inspection struct {
	Root              string            `json:"root"`
	Name              string            `json:"name"`
	Version           string            `json:"version"`
	RobotModel        string            `json:"robot_model"`
	SDKPackage        string            `json:"sdk_package"`
	BackendProfiles   []BackendProfile  `json:"backend_profiles"`
	InstanceLauncher  string            `json:"instance_launcher"`
	AbilityFramework  string            `json:"ability_framework"`
	Pilot             string            `json:"pilot"`
	PythonWheels      []string          `json:"python_wheels"`
	Abilities         []AbilityArtifact `json:"abilities"`
	AllFilesAvailable bool              `json:"all_files_available"`
}

func Inspect(root string) (Inspection, error) {
	opened, err := Open(root)
	if err != nil {
		return Inspection{}, err
	}
	manifest := opened.Manifest
	return Inspection{
		Root: opened.Root, Name: manifest.Metadata.Name, Version: manifest.Metadata.Version,
		RobotModel: manifest.Spec.Robot.Model, SDKPackage: manifest.Spec.Robot.SDKPackage,
		BackendProfiles:   append([]BackendProfile(nil), manifest.Spec.Robot.BackendProfiles...),
		InstanceLauncher:  opened.Path(manifest.Spec.Artifacts.InstanceLauncher),
		AbilityFramework:  opened.Path(manifest.Spec.Artifacts.AbilityFramework),
		Pilot:             opened.Path(manifest.Spec.Artifacts.Pilot),
		PythonWheels:      append([]string(nil), manifest.Spec.Artifacts.PythonWheels...),
		Abilities:         append([]AbilityArtifact(nil), manifest.Spec.Artifacts.Abilities...),
		AllFilesAvailable: true,
	}, nil
}

func InspectJSON(root string) ([]byte, error) {
	inspection, err := Inspect(root)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(inspection, "", "  ")
}
