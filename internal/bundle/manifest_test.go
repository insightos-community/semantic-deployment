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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const validManifest = `apiVersion: semantic.insightos.cn/v1alpha1
kind: RobotRuntimeBundle
metadata:
  name: test-r1pro
  version: 0.5.0-test
spec:
  robot:
    model: r1pro
    sdkPackage: semantic-robot-sdk-r1pro
    backendProfiles:
      - backend: fake
        profile: fake-v1
  artifacts:
    instanceLauncher: bin/semantic-robot-instance
    abilityFramework: bin/AbilityFramework
    pilot: bin/semantic-pilot
    robotSkillSDK: wheels/skill-sdk.whl
    pythonExecutable: python/venv/bin/python
    abilities:
      - role: navigation
        template: r1pro-navigation
        abilityName: R1ProNavigation.V1
        file: abilities/navigation.zip
  templates:
    robotDeployment: templates/robot.yaml.tmpl
    abilityFramework: templates/af.yaml.tmpl
`

func TestDecodeStrictAndMatch(t *testing.T) {
	manifest, err := Decode(strings.NewReader(validManifest))
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Supports("r1pro", "fake", "fake-v1"); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Supports("franka", "fake", "fake-v1"); err == nil {
		t.Fatal("其他 Robot 型号不应匹配")
	}
	if err := manifest.Supports("r1pro", "fake", "wrong"); err == nil {
		t.Fatal("错误 backend profile 不应匹配")
	}
	if _, err := Decode(strings.NewReader(validManifest + "unknown: true\n")); err == nil {
		t.Fatal("未知字段必须拒绝")
	}
	broken := strings.Replace(validManifest, "abilities/navigation.zip", "../navigation.zip", 1)
	if _, err := Decode(strings.NewReader(broken)); err == nil {
		t.Fatal("bundle 外路径必须拒绝")
	}
}

func TestBuildProducesInspectableReadOnlyBundle(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "bundle.yaml"), []byte(validManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{
		"bin/semantic-robot-instance", "bin/AbilityFramework", "bin/semantic-pilot",
		"wheels/skill-sdk.whl", "abilities/navigation.zip", "python/venv/bin/python",
		"templates/robot.yaml.tmpl", "templates/af.yaml.tmpl",
	} {
		path := filepath.Join(source, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(relative), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(t.TempDir(), "bundle")
	result, err := Build(source, output, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(output, func(path string, entry os.DirEntry, err error) error {
			if err == nil && entry.IsDir() {
				_ = os.Chmod(path, 0o755)
			}
			return nil
		})
	})
	inspection, err := Inspect(result.Root)
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.AllFilesAvailable || inspection.RobotModel != "r1pro" {
		t.Fatalf("inspect 结果错误: %+v", inspection)
	}
	for relative, want := range map[string]os.FileMode{
		".":                           0o555,
		"bin/semantic-robot-instance": 0o555,
		"bin/AbilityFramework":        0o555,
		"bin/semantic-pilot":          0o555,
		"abilities/navigation.zip":    0o444,
	} {
		info, err := os.Stat(filepath.Join(output, relative))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s mode=%o want=%o", relative, info.Mode().Perm(), want)
		}
	}
}

func TestBuildResolvesDeclaredWheelsFromDirectory(t *testing.T) {
	manifest := strings.Replace(
		validManifest,
		"    pythonExecutable: python/venv/bin/python\n",
		"    pythonExecutable: python/venv/bin/python\n"+
			"    pythonWheels:\n"+
			"      - wheels/runtime-dependency.whl\n",
		1,
	)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "bundle.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{
		"bin/semantic-robot-instance", "bin/AbilityFramework", "bin/semantic-pilot",
		"wheels/skill-sdk.whl", "abilities/navigation.zip", "python/venv/bin/python",
		"templates/robot.yaml.tmpl", "templates/af.yaml.tmpl",
	} {
		path := filepath.Join(source, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(relative), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wheelDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(wheelDirectory, "runtime-dependency.whl"), []byte("wheel"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "bundle")
	_, err := BuildWithOptions(source, output, nil, BuildOptions{WheelDirectory: wheelDirectory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(output, func(path string, entry os.DirEntry, err error) error {
			if err == nil {
				if entry.IsDir() {
					_ = os.Chmod(path, 0o755)
				} else {
					_ = os.Chmod(path, 0o644)
				}
			}
			return nil
		})
	})
	content, err := os.ReadFile(filepath.Join(output, "wheels", "runtime-dependency.whl"))
	if err != nil || string(content) != "wheel" {
		t.Fatalf("Wheel 目录制品未复制: %q, %v", content, err)
	}
}

func TestBuildRejectsMissingDeclaredOfflineWheel(t *testing.T) {
	manifest := strings.Replace(
		validManifest,
		"    pythonExecutable: python/venv/bin/python\n",
		"    pythonExecutable: python/venv/bin/python\n"+
			"    pythonWheels:\n"+
			"      - wheels/websockets-17.0.1-cp313-cp313-manylinux1_x86_64.manylinux_2_28_x86_64.manylinux_2_5_x86_64.whl\n",
		1,
	)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "bundle.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{
		"bin/semantic-robot-instance", "bin/AbilityFramework", "bin/semantic-pilot",
		"wheels/skill-sdk.whl", "abilities/navigation.zip", "python/venv/bin/python",
		"templates/robot.yaml.tmpl", "templates/af.yaml.tmpl",
	} {
		path := filepath.Join(source, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(relative), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, err := Build(source, filepath.Join(t.TempDir(), "bundle"), nil)
	if err == nil || !strings.Contains(err.Error(), "websockets-17.0.1-cp313-cp313-manylinux1_x86_64.manylinux_2_28_x86_64.manylinux_2_5_x86_64.whl") {
		t.Fatalf("缺少离线 Wheel 应在 build 阶段失败，得到: %v", err)
	}
}

func TestTypePackageManifests(t *testing.T) {
	fakePath := filepath.Join("..", "..", "type-packages", "r1pro-fake", "bundle.yaml")
	mujocoPath := filepath.Join("..", "..", "type-packages", "r1pro-mujoco", "bundle.yaml")
	manifests := map[string]Manifest{}
	for _, relative := range []string{fakePath, mujocoPath} {
		data, err := os.ReadFile(relative)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "linux" {
			if _, err := Load(relative); err == nil {
				t.Fatal("Linux example must reject other hosts")
			}
		}
		// These checked-in examples contain Linux wheel filenames. Validate
		// their metadata with a host-native fixture without changing the examples.
		manifest, err := Decode(strings.NewReader(strings.Replace(string(data), "os: linux", "os: "+runtime.GOOS, 1)))
		if err != nil {
			t.Fatalf("%s: %v", relative, err)
		}
		if len(manifest.Spec.Artifacts.Abilities) != 7 {
			t.Fatalf("%s 应声明七类 Ability", relative)
		}
		manifests[relative] = manifest
	}

	const websocketWheel = "wheels/websockets-17.0.1-cp313-cp313-manylinux1_x86_64.manylinux_2_28_x86_64.manylinux_2_5_x86_64.whl"
	contains := func(values []string, target string) bool {
		for _, value := range values {
			if value == target {
				return true
			}
		}
		return false
	}
	if contains(manifests[fakePath].Spec.Artifacts.PythonWheels, websocketWheel) {
		t.Fatal("Fake bundle 不应携带未使用的 WebSocket 依赖")
	}
	if !contains(manifests[mujocoPath].Spec.Artifacts.PythonWheels, websocketWheel) {
		t.Fatal("MuJoCo bundle 必须声明离线 WebSocket Wheel")
	}
	for _, wheel := range []string{
		"wheels/flask-3.1.3-py3-none-any.whl",
		"wheels/pydantic-2.13.4-py3-none-any.whl",
		"wheels/pin-3.9.0-0-cp313-cp313-manylinux_2_28_x86_64.whl",
		"wheels/ruckig-0.19.4-cp313-cp313-manylinux_2_27_x86_64.manylinux_2_28_x86_64.whl",
	} {
		if !contains(manifests[mujocoPath].Spec.Artifacts.PythonWheels, wheel) {
			t.Fatalf("MuJoCo bundle 缺少运行依赖 %s", wheel)
		}
	}
	mujoco := manifests[mujocoPath]
	if mujoco.Spec.Robot.Model != "r1_pro_chassis" ||
		len(mujoco.Spec.Robot.BackendProfiles) != 1 ||
		mujoco.Spec.Robot.BackendProfiles[0] != (BackendProfile{
			Backend: "mujoco", Profile: "r1pro-tote-mujoco-v1",
		}) {
		t.Fatalf("MuJoCo bundle 的 Robot 匹配契约错误: %+v", mujoco.Spec.Robot)
	}
	for _, ability := range mujoco.Spec.Artifacts.Abilities {
		if !strings.HasSuffix(ability.AbilityName, ".V2") {
			t.Fatalf("MuJoCo bundle 不能加载旧 schema Ability: %+v", ability)
		}
	}
	root := filepath.Dir(mujocoPath)
	deploymentTemplate, err := os.ReadFile(filepath.Join(root, mujoco.Spec.Templates.RobotDeployment))
	if err != nil {
		t.Fatal(err)
	}
	content := string(deploymentTemplate)
	for _, required := range []string{
		"managed_by_instance:", "scene_instance_id:", "end_effectors:",
		"component://tool/left", "component://tool/right", "robot_skills:",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("MuJoCo RobotDeployment 模板缺少 %q", required)
		}
	}
	for _, legacy := range []string{"managed_by_pilot:", "worker_timeout:", "end_effector: gripper_link"} {
		if strings.Contains(content, legacy) {
			t.Fatalf("MuJoCo RobotDeployment 模板仍包含旧字段 %q", legacy)
		}
	}
}
