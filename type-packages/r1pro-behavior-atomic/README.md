# BEHAVIOR 原子控制运行支持

该类型包连接 Semantic 管理的 Isaac Runtime，包含八类 Ability 和公共 Skill Worker SDK。六个 behavior Skill 经 Server Registry 安装。新机器部署入口见 [cuRobo 主部署迁移](../../docs/curobo-main-deployment.md)。

构建前，从 templates/model-registry.example.json 生成本目录 templates/model-registry.json，并填写新机器 SAM Python、源码和权重路径；实际配置不提交 Git。安装后的模型配置由启动器复制并通过 MODEL_REGISTRY_PATH 注入。

先构建 .output/bin/semantic-robot-instance 和 .output/bin/semantic-pilot，再用 semantic build type-packages/r1pro-behavior-atomic --output <包路径> 构建。SDK 与 Ability 来自相邻集成仓库。

场景 UUID、Runtime 地址、Robot ID、Pilot credential、能力端口均由 Server 注入；部署不指定固定仿真实例编号。

部署模板通过 ability_name 选择八个独立能力，同一语义角色下的动作仍保持原始 Action 类型。模板的 robot_skills 初始为空，待机器人上线后从 Registry 安装并启用 Skill。公共 Wheel 锁需覆盖 Skill Worker 的依赖；能力组件使用自己的独立 Python 环境。
