package instance

import (
	"path/filepath"
	"testing"
)

func TestMujocoDeploymentTemplateUsesRuntimeDescriptor(t *testing.T) {
	var config Config
	config.Spec.Robot = RobotConfig{
		ID: "r1_pro_tote_gripper-1", DisplayName: "R1 Pro Tote", Model: "r1_pro_chassis",
		Backend:        "mujoco",
		BackendProfile: "r1pro-tote-mujoco-v1", FirmwareProfile: "r1pro-tote-mujoco-v1",
		SDKEndpoint: "http://127.0.0.1:8090", SceneInstanceID: "scene-1",
		URDFPath: "/assets/r1_pro_tote_gripper.urdf", PackageDirectories: []string{"/assets"},
		Options: map[string]any{"timeout_seconds": 10},
		Tools: []ToolConfig{
			{ToolRef: "component://tool/left", Side: "left", Kind: "tote_clamp",
				Frame: "left_tote_load_frame", Joint: "left_tote_clamp_joint",
				TravelM: 0.035, NormalForceN: 60, MaximumForceN: 120},
			{ToolRef: "component://tool/right", Side: "right", Kind: "tote_clamp",
				Frame: "right_tote_load_frame", Joint: "right_tote_clamp_joint",
				TravelM: 0.035, NormalForceN: 60, MaximumForceN: 120},
		},
	}
	config.Spec.AbilityFramework = AbilityFrameworkConfig{
		Endpoint: "http://127.0.0.1:18100", Managed: true,
	}
	config.Spec.Pilot.ID = "pilot-r1_pro_tote_gripper-1"

	root := filepath.Join("..", "..", "type-packages", "r1pro-mujoco")
	target := filepath.Join(t.TempDir(), "robot-deployment.yaml")
	err := renderTemplate(
		filepath.Join(root, "templates", "robot-deployment.yaml.tmpl"),
		target,
		renderData{
			Instance: config, ModelRegistryPath: "/runtime/model-registry.json",
			SkillDirectory: "/runtime/pilot/skills",
			SDKPackage:     "semantic-robot-sdk-r1pro",
			SDKOptions: mergeSDKOptions(
				map[string]any{
					"timeout_seconds": 15, "joint_position_tolerance_rad": 0.009,
					"fixed_end_effector_position_tolerance_m":      0.005,
					"fixed_end_effector_orientation_tolerance_rad": 0.02,
				},
				config.Spec.Robot.Options,
			),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := loadRobotDeployment(target)
	if err != nil {
		t.Fatalf("渲染结果不符合当前 RobotDeployment: %v", err)
	}
	if deployment.Robot.SDK.SceneInstanceID != "scene-1" ||
		deployment.Robot.Model != "r1_pro_chassis" ||
		len(deployment.Robot.Tools) != 2 {
		t.Fatalf("场景或工具描述在模板中丢失: %+v", deployment.Robot)
	}
	endEffectors, ok := deployment.Robot.Frames["end_effectors"].(map[string]any)
	if !ok || endEffectors["left"] != "left_tote_load_frame" ||
		endEffectors["right"] != "right_tote_load_frame" {
		t.Fatalf("双末端 frame 渲染错误: %#v", deployment.Robot.Frames)
	}
	if deployment.Pilot.ID == "" || len(deployment.RobotSkills) != 3 {
		t.Fatalf("Pilot 或 desired Robot Skill 未进入模板: %+v", deployment.Pilot)
	}
	if len(deployment.Robot.Kinematics.NamedPostures["travel"]) == 0 {
		t.Fatalf("travel 命名姿态未进入 RobotDeployment")
	}
	if deployment.Robot.SDK.Options["timeout_seconds"] != 10 ||
		deployment.Robot.SDK.Options["joint_position_tolerance_rad"] != 0.009 ||
		deployment.Robot.SDK.Options["fixed_end_effector_position_tolerance_m"] != 0.005 ||
		deployment.Robot.SDK.Options["fixed_end_effector_orientation_tolerance_rad"] != 0.02 {
		t.Fatalf("类型包默认 SDK 配置或实例覆盖丢失: %#v", deployment.Robot.SDK.Options)
	}
}
