package bundle

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportContainsOnlyDeclaredFilesAndNoVirtualEnvironment(t *testing.T) {
	root := t.TempDir()
	manifest := strings.Replace(validManifest, "    abilities:", "    pythonWheels: [wheels/skill-sdk.whl]\n    abilities:", 1)
	_ = os.WriteFile(filepath.Join(root, "bundle.yaml"), []byte(manifest), 0640)
	parsed, err := Load(filepath.Join(root, "bundle.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range parsed.ReferencedFiles() {
		path := filepath.Join(root, relative)
		_ = os.MkdirAll(filepath.Dir(path), 0750)
		_ = os.WriteFile(path, []byte("fixture"), 0750)
	}
	wheel, err := os.Create(filepath.Join(root, "wheels/skill-sdk.whl"))
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(wheel)
	f, _ := z.Create("sdk.dist-info/METADATA")
	_, _ = f.Write([]byte("Name: test-sdk\nVersion: 1.0.0\n\n"))
	_ = z.Close()
	_ = wheel.Close()
	_ = os.WriteFile(filepath.Join(root, "credentials.json"), []byte("must-not-export"), 0600)
	output := filepath.Join(t.TempDir(), "robot.zip")
	if err := ExportPackage(root, output, "3.12"); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	for _, entry := range archive.File {
		if entry.Name == "semantic-component.yaml" {
			reader, _ := entry.Open()
			body, _ := io.ReadAll(reader)
			reader.Close()
			if !strings.Contains(string(body), "kind: robot_base") || strings.Contains(string(body), "components:") {
				t.Fatal("底座必须是独立组件", string(body))
			}
		}
		if strings.Contains(entry.Name, "venv/") || strings.Contains(entry.Name, "credentials") {
			t.Fatal("不应导出运行环境或凭据", entry.Name)
		}
		if entry.Name == "requirements.lock" {
			reader, _ := entry.Open()
			body, _ := io.ReadAll(reader)
			reader.Close()
			if string(body) != "test-sdk==1.0.0\n" {
				t.Fatal(string(body))
			}
		}
	}
	// 显式替换 Wheel 后，依赖锁必须使用新 Wheel 的元数据。
	replacement := filepath.Join(t.TempDir(), "replacement.whl")
	file, _ := os.Create(replacement)
	z = zip.NewWriter(file)
	f, _ = z.Create("sdk.dist-info/METADATA")
	_, _ = f.Write([]byte("Name: test-sdk\nVersion: 1.1.0\n\n"))
	_ = z.Close()
	_ = file.Close()
	updated := filepath.Join(t.TempDir(), "updated.zip")
	if err := ExportPackageWithMappings(root, updated, "3.12", []FileMapping{{Target: "wheels/skill-sdk.whl", Source: replacement}}); err != nil {
		t.Fatal(err)
	}
	newArchive, err := zip.OpenReader(updated)
	if err != nil {
		t.Fatal(err)
	}
	defer newArchive.Close()
	for _, entry := range newArchive.File {
		if entry.Name == "requirements.lock" {
			reader, _ := entry.Open()
			body, _ := io.ReadAll(reader)
			reader.Close()
			if string(body) != "test-sdk==1.1.0\n" {
				t.Fatal(string(body))
			}
		}
	}
}
