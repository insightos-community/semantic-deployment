package instance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBehaviorTemplateKeepsNormalGrippersAndExternalModelBinding(t *testing.T) {
	var config Config
	config.Spec.Robot = RobotConfig{ID: "robot_r1", Model: "r1pro", Backend: "isaac",
		SDKEndpoint: "http://127.0.0.1:18100", SceneInstanceID: "behavior-instance"}
	config.Spec.AbilityFramework = AbilityFrameworkConfig{Endpoint: "http://127.0.0.1:18101", Managed: true}
	target := filepath.Join(t.TempDir(), "robot-deployment.yaml")
	err := renderTemplate(filepath.Join("..", "..", "type-packages", "r1pro-behavior", "templates", "robot-deployment.yaml.tmpl"), target, renderData{
		Instance: config, InstanceDirectory: "/instances/r1", ModelRegistryPath: "/models/selected/model.json",
		SkillDirectory: "/instances/r1/skills", SDKPackage: "semantic-robot-sdk-r1pro",
	})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := loadRobotDeployment(target)
	if err != nil {
		t.Fatal(err)
	}
	if deployment.Robot.Model != "r1pro" || deployment.Robot.Backend != "isaac" {
		t.Fatalf("错误后端: %+v", deployment.Robot)
	}
	if len(deployment.RobotSkills) != 1 || deployment.RobotSkills[0].Name != "vla-manipulation" {
		t.Fatalf("错误技能: %+v", deployment.RobotSkills)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"/models/selected/model.json", "behavior-omnigibson", "/instances/r1/executions/r1pro-vla.sqlite", "kinematics: none", "motion: none", "left_eef_link", "right_eef_link"} {
		if !strings.Contains(string(body), expected) {
			t.Fatalf("缺少绑定 %q", expected)
		}
	}
	for _, excluded := range []string{"tote", "urdf", "smolvla", "turning_on_radio", "semantic-navigation"} {
		if strings.Contains(string(body), excluded) {
			t.Fatalf("普通夹爪模板带入了专用配置 %q", excluded)
		}
	}
}
