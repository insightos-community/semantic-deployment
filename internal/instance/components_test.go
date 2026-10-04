package instance

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"insightos.cn/semantic-robot-deployment/internal/bundle"
)

func TestComponentSnapshotDoesNotFollowLaterBinding(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "current.json")
	config := Config{Spec: Spec{Robot: RobotConfig{ID: "robot-1", Model: "franka"}, ComponentBindingsFile: source}}
	write := func(revision string) {
		data, _ := json.Marshal(componentBindings{RobotID: "robot-1", RobotModel: "franka", Revision: revision})
		if err := os.WriteFile(source, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("first")
	output := filepath.Join(root, "instance")
	if err := os.MkdirAll(filepath.Join(output, "run"), 0750); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotComponentBindings(&config, output); err != nil {
		t.Fatal(err)
	}
	write("second")
	binding, err := readComponentBindings(filepath.Join(output, config.Spec.ComponentBindingsFile), config)
	if err != nil || binding.Revision != "first" {
		t.Fatalf("%+v %v", binding, err)
	}
}

func TestAbilityBindingWrapsOnlyEnvironment(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "ability.zip")
	file, _ := os.Create(source)
	writer := zip.NewWriter(file)
	original := "#!/bin/sh\nexec \"$SEMANTIC_ABILITY_PYTHON\" main.py \"$@\"\n"
	header := zip.FileHeader{Name: "bin/ability"}
	header.SetMode(0755)
	out, _ := writer.CreateHeader(&header)
	_, _ = io.WriteString(out, original)
	_ = writer.Close()
	_ = file.Close()
	python := filepath.Join(root, "python")
	_ = os.WriteFile(python, []byte("python"), 0755)
	destination := filepath.Join(root, "bound.zip")
	if err := bindAbilityPython(componentAbility{Package: source, Python: python}, destination); err != nil {
		t.Fatal(err)
	}
	bound, err := zip.OpenReader(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer bound.Close()
	contents := map[string]string{}
	for _, entry := range bound.File {
		reader, _ := entry.Open()
		data, _ := io.ReadAll(reader)
		_ = reader.Close()
		contents[entry.Name] = string(data)
	}
	if contents["bin/ability.original"] != original || contents["bin/ability"] == "" {
		t.Fatal("原始入口应保留，仅注入组件 Python")
	}
	before, _ := os.ReadFile(source)
	if len(before) == 0 {
		t.Fatal("源包应保留")
	}
}

func TestAbilityBindingRejectsUndeclaredRole(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bindings.json")
	config := Config{Spec: Spec{Robot: RobotConfig{ID: "robot", Model: "franka"}, ComponentBindingsFile: path}}
	data, _ := json.Marshal(componentBindings{RobotID: "robot", RobotModel: "franka", Abilities: map[string]componentAbility{"unknown": {}}})
	_ = os.WriteFile(path, data, 0600)
	opened := bundle.Bundle{}
	if err := applyComponentAbilities(config, &opened, root); err == nil {
		t.Fatal("无法给未声明角色静默安装")
	}
}
