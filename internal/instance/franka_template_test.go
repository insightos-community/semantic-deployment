package instance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFrankaDeploymentKeepsModelBindingAndInstanceStateSeparate(t *testing.T) {
	var config Config
	config.Spec.Robot = RobotConfig{
		ID: "franka-0", Model: "franka_panda", Backend: "mujoco",
		SDKEndpoint: "http://127.0.0.1:19090", SceneInstanceID: "libero-instance",
	}
	config.Spec.AbilityFramework = AbilityFrameworkConfig{
		Endpoint: "http://127.0.0.1:19100", Managed: true,
	}
	config.Spec.Pilot.ID = "pilot-franka-0"
	target := filepath.Join(t.TempDir(), "robot-deployment.yaml")
	err := renderTemplate(
		filepath.Join("..", "..", "type-packages", "franka-libero", "templates", "robot-deployment.yaml.tmpl"),
		target, renderData{
			Instance: config, InstanceDirectory: "/instance/franka-0",
			ModelRegistryPath: "/instance/franka-0/model-registry.json",
			SkillDirectory:    "/instance/franka-0/pilot/skills",
			SDKPackage:        "semantic-robot-sdk-franka",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := loadRobotDeployment(target)
	if err != nil {
		t.Fatal(err)
	}
	if deployment.Robot.Model != "franka_panda" || deployment.Robot.SDK.SceneInstanceID != "libero-instance" {
		t.Fatalf("Franka 场景绑定丢失: %+v", deployment.Robot)
	}
	if len(deployment.RobotSkills) != 1 || deployment.RobotSkills[0].Name != "vla-manipulation" {
		t.Fatalf("Franka 不应挂载拆码垛 Skill: %+v", deployment.RobotSkills)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"/instance/franka-0/model-registry.json", "/instance/franka-0/executions/franka-vla.sqlite",
		"hand: panda_hand", "version: 0.1.7", "kinematics: local", "motion: local",
	} {
		if !strings.Contains(string(body), expected) {
			t.Fatalf("缺少模型绑定或隔离状态路径 %q", expected)
		}
	}
}

func TestMujocoRequirementsAreScopedToRobotModel(t *testing.T) {
	configPath := writeTestInstanceConfig(t, createTestBundle(t), "franka-0", freePort(t))
	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	config.Spec.Robot.Model = "franka_panda"
	config.Spec.Robot.Backend = "mujoco"
	config.Spec.Robot.SDKEndpoint = "http://127.0.0.1:19090"
	config.Spec.Robot.SceneInstanceID = "libero-instance"
	if err := config.Validate(); err != nil {
		t.Fatalf("原生 Franka 控制器不依赖 R1 本地 IK 或周转箱工具: %v", err)
	}
	config.Spec.Robot.Model = "r1_pro_chassis"
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "工具描述") {
		t.Fatalf("R1 工具要求必须保留: %v", err)
	}
	config.Spec.Robot.Tools = []ToolConfig{{Side: "left"}}
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "urdfPath") {
		t.Fatalf("R1 本地 IK 要求必须保留: %v", err)
	}
}
