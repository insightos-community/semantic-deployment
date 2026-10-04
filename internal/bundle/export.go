package bundle

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ExportPackage 从已有类型包导出标准安装组件。交付内容由清单精确决定，
// Python 环境在目标机器从 Wheel 重建，因此不携带构建机的 venv、凭据或实例数据。
// Ability 更新可以单独交付；这个包只承担首次安装 Robot 运行底座。
func ExportPackage(root, destination, pythonVersion string) error {
	return ExportPackageWithMappings(root, destination, pythonVersion, nil)
}

func ExportPackageWithMappings(root, destination, pythonVersion string, mappings []FileMapping) error {
	if pythonVersion == "" {
		return fmt.Errorf("导出需要明确 --python-version，与 Wheel 的 ABI 一致")
	}
	manifest, err := Load(filepath.Join(root, "bundle.yaml"))
	if err != nil {
		return err
	}
	if err := validateBuildInputs(root, manifest); err != nil {
		return err
	}
	overrides := map[string]string{}
	for _, mapping := range mappings {
		declared := false
		for _, file := range manifest.ReferencedFiles() {
			if file == mapping.Target {
				declared = true
				break
			}
		}
		if !declared || mapping.Target == manifest.Spec.Artifacts.PythonExecutable {
			return fmt.Errorf("导出覆盖目标未声明或为生成入口: %s", mapping.Target)
		}
		overrides[mapping.Target] = mapping.Source
	}
	wheels := make([]string, 0, len(manifest.Spec.Artifacts.PythonWheels))
	locked := map[string]string{}
	for _, relative := range manifest.Spec.Artifacts.PythonWheels {
		// 锁文件必须描述真正交付的 Wheel，包括显式更新后的源码构建产物。
		source := filepath.Join(root, relative)
		if replacement, ok := overrides[relative]; ok {
			source = replacement
		}
		name, version, err := wheelIdentity(source)
		if err != nil {
			return err
		}
		if previous, ok := locked[name]; ok && previous != version {
			return fmt.Errorf("Wheel 版本冲突: %s", name)
		}
		locked[name] = version
		wheels = append(wheels, "robot/"+filepath.ToSlash(relative))
	}
	if len(wheels) == 0 {
		return fmt.Errorf("离线导出需要清单中的 pythonWheels")
	}
	lines := make([]string, 0, len(locked))
	for name, version := range locked {
		lines = append(lines, name+"=="+version)
	}
	sort.Strings(lines)
	component := map[string]any{
		"schema_version": 1, "kind": "robot_base", "name": manifest.Metadata.Name, "version": manifest.Metadata.Version,
		"robot_models": []string{manifest.Spec.Robot.Model}, "bundle_manifest": "robot/bundle.yaml",
		"python": map[string]any{"version": pythonVersion, "requirements": "requirements.lock", "wheels": wheels,
			"executable": "robot/" + filepath.ToSlash(manifest.Spec.Artifacts.PythonExecutable)},
	}
	body, err := yaml.Marshal(component)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0750); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".robot-export-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	archive := zip.NewWriter(temp)
	write := func(name string, data []byte) error {
		entry, err := archive.Create(name)
		if err != nil {
			return err
		}
		_, err = entry.Write(data)
		return err
	}
	if err := write("semantic-component.yaml", body); err != nil {
		return err
	}
	if err := write("requirements.lock", []byte(strings.Join(lines, "\n")+"\n")); err != nil {
		return err
	}
	files := append([]string{"bundle.yaml"}, manifest.ReferencedFiles()...)
	sort.Strings(files)
	seen := map[string]bool{}
	for _, relative := range files {
		if relative == manifest.Spec.Artifacts.PythonExecutable || seen[relative] {
			continue
		}
		seen[relative] = true
		source := filepath.Join(root, relative)
		if replacement, ok := overrides[relative]; ok {
			source = replacement
		}
		file, err := os.Open(source)
		if err != nil {
			return err
		}
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return err
		}
		header := &zip.FileHeader{Name: "robot/" + filepath.ToSlash(relative), Method: zip.Store}
		header.SetMode(info.Mode())
		entry, err := archive.CreateHeader(header)
		if err == nil {
			_, err = io.Copy(entry, file)
		}
		file.Close()
		if err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), destination)
}

func wheelIdentity(path string) (string, string, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return "", "", err
	}
	defer archive.Close()
	for _, entry := range archive.File {
		if !strings.HasSuffix(entry.Name, ".dist-info/METADATA") {
			continue
		}
		file, err := entry.Open()
		if err != nil {
			return "", "", err
		}
		scanner := bufio.NewScanner(io.LimitReader(file, 1<<20))
		name, version := "", ""
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				break
			}
			if strings.HasPrefix(line, "Name: ") {
				name = strings.TrimPrefix(line, "Name: ")
			}
			if strings.HasPrefix(line, "Version: ") {
				version = strings.TrimPrefix(line, "Version: ")
			}
		}
		file.Close()
		if name != "" && version != "" {
			return name, version, nil
		}
	}
	return "", "", fmt.Errorf("Wheel 缺少名称版本: %s", path)
}
