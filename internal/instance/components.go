package instance

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"insightos.cn/semantic-robot-deployment/internal/bundle"
)

// 该结构是安装器发布的绑定协议。运行时读取实例内快照，不追随 current.json
// 的变化；因此新版本安装、回退均不会改变正在执行的 Python 环境或模型配置。
type componentBindings struct {
	RobotID    string                      `json:"robot_id"`
	RobotModel string                      `json:"robot_model"`
	Revision   string                      `json:"revision"`
	Abilities  map[string]componentAbility `json:"abilities"`
	Model      *struct {
		Name        string `json:"name,omitempty"`
		Version     string `json:"version,omitempty"`
		ComponentID string `json:"component_id"`
		Config      string `json:"config"`
	} `json:"model,omitempty"`
}
type componentAbility struct {
	Name           string `json:"name,omitempty"`
	Version        string `json:"version,omitempty"`
	SourceRevision string `json:"source_revision,omitempty"`
	ComponentID    string `json:"component_id"`
	Template       string `json:"template"`
	AbilityName    string `json:"ability_name"`
	Package        string `json:"package"`
	Python         string `json:"python"`
}

func readComponentBindings(path string, config Config) (componentBindings, error) {
	var binding componentBindings
	if path == "" {
		return binding, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return binding, err
	}
	if err := json.Unmarshal(body, &binding); err != nil {
		return binding, err
	}
	if binding.RobotID != config.Spec.Robot.ID || binding.RobotModel != config.Spec.Robot.Model {
		return binding, fmt.Errorf("组件绑定与 Robot 身份不一致")
	}
	return binding, nil
}

func snapshotComponentBindings(config *Config, directory string) (componentBindings, error) {
	binding, err := readComponentBindings(config.Spec.ComponentBindingsFile, *config)
	if err != nil {
		return binding, err
	}
	if config.Spec.ComponentBindingsFile == "" {
		return binding, nil
	}
	path := filepath.Join(directory, "run", "components.json")
	body, err := json.MarshalIndent(binding, "", "  ")
	if err != nil {
		return binding, err
	}
	if err := atomicWrite(path, body, 0600); err != nil {
		return binding, err
	}
	config.Spec.ComponentBindingsFile = "run/components.json"
	return binding, nil
}

func applyComponentAbilities(config Config, opened *bundle.Bundle, directory string) error {
	binding, err := readComponentBindings(config.Spec.ComponentBindingsFile, config)
	if err != nil {
		return err
	}
	roles := map[string]bool{}
	for index, original := range opened.Manifest.Spec.Artifacts.Abilities {
		roles[original.Role] = true
		replacement, ok := binding.Abilities[original.Role]
		if !ok {
			continue
		}
		// 角色的契约由 Robot 型号包声明。更新实现可换版本，接口名称仍与 Pilot 配置一致。
		if replacement.Template != original.Template || replacement.AbilityName != original.AbilityName {
			return fmt.Errorf("Ability %s 的接口与 Robot 型号包不匹配", original.Role)
		}
		output := filepath.Join(directory, "run", "component-packages", original.Role+".zip")
		if err := bindAbilityPython(replacement, output); err != nil {
			return err
		}
		opened.Manifest.Spec.Artifacts.Abilities[index].File = output
	}
	for role := range binding.Abilities {
		if !roles[role] {
			return fmt.Errorf("Robot 型号包未声明 Ability 角色 %s", role)
		}
	}
	return nil
}

func abilityPackagePath(opened bundle.Bundle, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return opened.Path(path)
}

// 每个 Ability 包只增加一层环境入口，保留原始启动脚本。AbilityFramework 的
// 激活、反馈、停止协议不变；Pilot 与未更新的 Ability 继续使用基础 Bundle 环境。
func bindAbilityPython(binding componentAbility, destination string) error {
	if !filepath.IsAbs(binding.Python) {
		return fmt.Errorf("Ability Python 必须为已安装环境的绝对路径")
	}
	if info, err := os.Stat(binding.Python); err != nil || info.IsDir() {
		return fmt.Errorf("Ability Python 不可用: %s", binding.Python)
	}
	source, err := zip.OpenReader(binding.Package)
	if err != nil {
		return err
	}
	defer source.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0750); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".ability-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	writer := zip.NewWriter(file)
	found := false
	copyAll := func() error {
		for _, entry := range source.File {
			header := entry.FileHeader
			if header.Name == "bin/ability.original" {
				return fmt.Errorf("Ability 包包含保留入口")
			}
			if header.Name == "bin/ability" {
				header.Name = "bin/ability.original"
				found = true
			}
			out, err := writer.CreateHeader(&header)
			if err != nil {
				return err
			}
			in, err := entry.Open()
			if err != nil {
				return err
			}
			_, err = io.Copy(out, in)
			_ = in.Close()
			if err != nil {
				return err
			}
		}
		if !found {
			return fmt.Errorf("Ability 包缺少 bin/ability 入口")
		}
		header := zip.FileHeader{Name: "bin/ability", Method: zip.Deflate}
		header.SetMode(0755)
		out, err := writer.CreateHeader(&header)
		if err != nil {
			return err
		}
		quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
		script := "#!/bin/sh\nexport SEMANTIC_ABILITY_PYTHON=" + quote(binding.Python) + "\nexport PATH=" + quote(filepath.Dir(binding.Python)) + ":\"$PATH\"\nexec \"$(dirname \"$0\")/ability.original\" \"$@\"\n"
		_, err = io.WriteString(out, script)
		return err
	}
	copyErr := copyAll()
	zipErr := writer.Close()
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if zipErr != nil {
		return zipErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), destination)
}
