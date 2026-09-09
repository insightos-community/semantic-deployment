> Historical technical reference / 历史技术参考。For current build and usage instructions, see [English](README.md) / [中文](README.zh-CN.md). Version-specific examples below are not a current release manifest.

# semantic-robot-deployment

本仓把确定版本的 Pilot、AbilityFramework、七类 Ability、Robot SDK 和 Robot
Skill Runtime SDK 组装为 Robot 型号运行包，再为每台 Robot 创建隔离的运行目录。

具体的 grasp-object、semantic-navigation 和 place-object 不进入 bundle。它们仍由
Semantic Server 的 Robot Skill Registry 单独发布、安装和启用。plugin-mujoco
也是独立进程；本仓只把它的 HTTP Endpoint 写入 Robot SDK 配置，不修改它。

### 阶段图像对应制品

当前类型包使用 `semantic-r1pro-abilities==0.4.0.dev1`，包含 Ability/Pilot
图片交换目录修复。MuJoCo 默认 Robot Skill 为 `grasp-object 0.4.23`、
`semantic-navigation 0.4.7`、`place-object 0.4.42`，包含阶段 RGB 采集。
Ability ZIP/CR 的 `0.4.0` 是能力包描述版本，不代表 Python 实现包版本。

使用 Framework 的 `scripts/refresh_v050_mujoco.py` 时，需同步更新本仓和
R1 Pro Ability、Robot Skill 仓。刷新脚本从当前源码构建到本次暂存目录，
检查 Ability 源码版本与本仓 Wheel 清单一致，并在激活前用 Bundle 内 Python
核对实际安装版本。结果写入 `.output/v050-mujoco-refresh/refresh-summary.json`
的 `ability_implementation`（版本、Wheel 文件名、SHA-256）。不复用 Ability
仓库 `dist` 中的旧 Wheel，也不能通过只升级宿主机 Python 包更新运行中的 Bundle。

`build --activate` 后仍需重启 Server 使 Catalog 生效，再运行脚本的 `publish`
发布/安装三个 Robot Skill，最后启动 Robot Runtime。仅执行 `build` 不会激活；
仅发布 Skill 不会更新 Bundle 内的 Ability。已有 Robot 的实际 Skill 版本以
Server desired/installed 对账为准，更新模板不会自动覆盖用户已选择的版本。

## 命令

- semantic-robot-bundle build/inspect：组装并检查共享只读 bundle。
- semantic-robot-instance start：使用 RobotDeployment 首次加入或直接启动一台 Robot。
- semantic-robot-instance render/run/status/stop：底层运维命令，正常部署不需要手工调用。

启动顺序固定为：

~~~text
AbilityFramework
→ 上传并启动七类 Ability
→ 按 instance ID + abilityName 确认 heartbeat
→ semantic-pilot
~~~

停止时先要求 Pilot 到达最近安全停止点。只有 Pilot 以 0 退出才记录
pilot_exited_cleanly=true；随后停止本实例的七个 Ability，最后关闭本实例管理的
AbilityFramework。Pilot 非零退出、停止超时或 Ability 停止失败会使实例进入
failed，不会仅因进程消失而报告 stopped。

## 从干净目录构建

先准备已经通过各自测试的 AbilityFramework、semantic-pilot、产品 Wheel、第三方
依赖 Wheel 和七个 Ability Zip。第三方依赖版本由类型包内的
`python-requirements.lock` 固定。Robot Skill SDK Wheel 名为
`semantic_robot_skill_sdk-0.1.0.dev0-py3-none-any.whl`，只含 SDK/Runtime，不含
三个具体 Robot Skill。

~~~bash
git clone <semantic-robot-deployment-url>
cd semantic-robot-deployment
make verify

export DEPLOY_ROOT="$PWD"
export ROBOT_ARTIFACTS=/srv/semantic-artifacts/v0.5.0

python -m pip download \
  --only-binary=:all: \
  --dest "$ROBOT_ARTIFACTS/wheels" \
  -r type-packages/r1pro-fake/python-requirements.lock

"$DEPLOY_ROOT/bin/semantic-robot-bundle" build \
  --source "$DEPLOY_ROOT/type-packages/r1pro-fake" \
  --output /opt/semantic/bundles/r1pro-fake-0.5.0 \
  --python python3 \
  --wheel-dir "$ROBOT_ARTIFACTS/wheels" \
  --file "bin/semantic-robot-instance=$DEPLOY_ROOT/bin/semantic-robot-instance" \
  --file "bin/AbilityFramework=$ROBOT_ARTIFACTS/bin/AbilityFramework" \
  --file "bin/semantic-pilot=$ROBOT_ARTIFACTS/bin/semantic-pilot" \
  --file "abilities/r1pro-navigation.zip=$ROBOT_ARTIFACTS/abilities/r1pro-navigation.zip" \
  --file "abilities/r1pro-manipulator-motion.zip=$ROBOT_ARTIFACTS/abilities/r1pro-manipulator-motion.zip" \
  --file "abilities/r1pro-end-effector.zip=$ROBOT_ARTIFACTS/abilities/r1pro-end-effector.zip" \
  --file "abilities/r1pro-robot-state.zip=$ROBOT_ARTIFACTS/abilities/r1pro-robot-state.zip" \
  --file "abilities/r1pro-sensor-capture.zip=$ROBOT_ARTIFACTS/abilities/r1pro-sensor-capture.zip" \
  --file "abilities/r1pro-object-perception.zip=$ROBOT_ARTIFACTS/abilities/r1pro-object-perception.zip" \
  --file "abilities/r1pro-grasp-planning.zip=$ROBOT_ARTIFACTS/abilities/r1pro-grasp-planning.zip"

"$DEPLOY_ROOT/bin/semantic-robot-bundle" inspect \
  --bundle /opt/semantic/bundles/r1pro-fake-0.5.0
~~~

`build`只消费已构建的 Wheel、Zip 和二进制。`--wheel-dir`只按`bundle.yaml`
声明的精确文件名补齐来源，不会把目录中的其他包带入类型包。构建器创建隔离的
`python/venv`，使用`--no-index --no-deps`安装清单 Wheel，然后把整个 bundle
改为只读。不同 Robot 共用这一 Python 环境，不在实例目录重复安装依赖；运行时
同时清空宿主`PYTHONPATH`并禁用用户 site-packages。

### MuJoCo 的离线规划与 WebSocket 依赖

Fake类型包不使用传感器WebSocket，因此不携带`websockets`。MuJoCo类型包还
声明 WebSocket、Pinocchio、Ruckig 及其传递依赖。构建 MuJoCo bundle 时，把
source、output和requirements lock改为`r1pro-mujoco`；其余命令不变。

所有 Wheel 必须预先放入离线制品目录。构建器在创建共享 Python 环境前检查全部
清单输入；缺少制品时立即失败，不生成到运行期才因`ModuleNotFoundError`退出的
bundle。

## 一条命令加入并启动

设备中心点击“添加 Pilot”取得一次性加入码后，使用类型包中的启动器。RobotDeployment 是用户唯一需要按设备维护的配置；完整 Fake 示例见 `examples/robot-deployment-r1pro-fake-02.yaml`。

~~~bash
export BUNDLE=/opt/semantic/bundles/r1pro-fake-0.5.0

"$BUNDLE/bin/semantic-robot-instance" start \
  --config examples/robot-deployment-r1pro-fake-02.yaml \
  --join-code ABCD12
~~~

启动器默认通过 `_semantic-server._tcp.local.` 扫描一次局域网 Server。mDNS 只发现地址；真正的设备身份由 join code 换取的专用 Pilot credential 建立。局域网无法使用 mDNS 时显式传入：

~~~bash
  --server-http http://127.0.0.1:8080 \
  --server-ws ws://127.0.0.1:8081/ws/pilot
~~~

首次成功后，`connection.yaml` 已保存专用 credential。以后无需 join code：

~~~bash
"$BUNDLE/bin/semantic-robot-instance" start \
  --config examples/robot-deployment-r1pro-fake-02.yaml
~~~

未指定 `--data-dir` 时，实例数据默认写入
`$XDG_STATE_HOME/semantic/robots/<robot-id>`；未设置 XDG 时使用用户的
`~/.local/state/semantic/robots/<robot-id>`。需要由 systemd 或容器指定持久卷时再
显式传入 `--data-dir`。R1 Pro Fake 示例已经在 RobotDeployment 中声明首次抓取
环境，启动后不再运行 Python 手工注入物体。

`start` 自动完成共享 bundle 定位、实例目录渲染、AbilityFramework、七类 Ability、Pilot 启动和 desired Robot Skill 对账。用户不再复制管理员 Token，也不逐个上传 Ability 或安装 Skill。

## 不连接 Server 的 Robot Skill 本地调试

开发者需要隔离观察 `Robot Skill → AbilityFramework → Ability → Robot SDK`
时，可以复用已经 render 的实例目录，只启动 AF和七类 Ability：

```bash
bin/semantic-robot-instance debug-stack \
  --instance /var/lib/semantic/robots/r1pro-mujoco-01
```

该命令不启动 Pilot常驻进程、不连接Semantic Server，也不读取 Pilot credential。
它与完整 `run` 共用 `instance.lock`，因此启动前必须先安全停止同一实例。看到
`status: ready` 后，在另一个终端使用 `semantic-pilot skill run` 执行具体 Skill；
必须先结束或安全停止 Skill，再退出 `debug-stack`。

这是开发调试入口，不替代生产 `start/run`，也不创建 Project、Workflow、Task或
Server Robot Execution。

## 底层 Render、Run、Status、Stop

先修改 examples/r1pro-fake-01.yaml 中的 bundle、Server 地址和 token：

~~~bash
export BUNDLE=/opt/semantic/bundles/r1pro-fake-0.5.0

"$BUNDLE/bin/semantic-robot-instance" render \
  --config examples/r1pro-fake-01.yaml \
  --output /var/lib/semantic/robots/r1pro-fake-01

"$BUNDLE/bin/semantic-robot-instance" run \
  --instance /var/lib/semantic/robots/r1pro-fake-01

"$BUNDLE/bin/semantic-robot-instance" status \
  --instance /var/lib/semantic/robots/r1pro-fake-01

"$BUNDLE/bin/semantic-robot-instance" stop \
  --instance /var/lib/semantic/robots/r1pro-fake-01 \
  --timeout 30s
~~~

run 是前台 supervisor，生产环境应由 systemd 或容器运行器托管。stop 给
supervisor 发停止请求并等待停止证据，不会绕过 Pilot 直接杀 Robot 进程。

## 目录

共享只读 bundle：

~~~text
r1pro-fake-0.5.0/
├── bundle.yaml
├── bin/
├── wheels/
├── abilities/
├── templates/
└── python/venv/
~~~

每台 Robot 的可写实例（`connection.yaml` 由首次加入生成，权限为 0600）：

~~~text
r1pro-fake-01/
├── instance.yaml
├── connection.yaml
├── robot-deployment.yaml
├── model-registry.json
├── ability-framework/
│   ├── config.yaml
│   ├── packages/
│   ├── crs/
│   ├── databases/
│   ├── data/
│   └── log/
├── pilot/
│   ├── pilot.db
│   ├── artifacts/
│   ├── skills/
│   └── logs/
├── executions/
└── run/
~~~

AbilityFramework 的数据库、CR、包、日志和 Ability 执行数据，以及 Pilot 的 DB、
Artifact、Skill 和日志都属于当前 Robot 实例。共享 bundle 不保存运行状态。

## 两台 Fake Robot

两个实例复用同一个 bundle，但使用不同 robot.id、pilot.id、AbilityFramework
Endpoint 和实例目录：

~~~bash
"$BUNDLE/bin/semantic-robot-instance" render \
  --config examples/r1pro-fake-01.yaml \
  --output /var/lib/semantic/robots/r1pro-fake-01

"$BUNDLE/bin/semantic-robot-instance" render \
  --config examples/r1pro-fake-02.yaml \
  --output /var/lib/semantic/robots/r1pro-fake-02
~~~

在两个终端分别执行 run。停止 Robot A 不会停止或删除 Robot B 的 Ability、DB、
Artifact 或日志。

## 两台 MuJoCo Robot 使用同一 SDK Endpoint

MuJoCo Runtime 可以在同一 Endpoint 暴露多个 Robot。两个 RobotInstance 可以使用
相同 sdkEndpoint，但 robot.id 必须不同；Robot SDK 会把 ID 带到底层请求。每台
Robot 仍使用不同的 Pilot ID、AbilityFramework Endpoint 和实例目录：

~~~text
Robot A: robot.id=r1pro-001, AF=http://127.0.0.1:18081
Robot B: robot.id=r1pro-002, AF=http://127.0.0.1:18082
共同 SDK Endpoint: http://127.0.0.1:18090
~~~

原生 MuJoCo 周转箱场景使用：

~~~text
model: r1_pro_chassis
backend: mujoco
backendProfile: r1pro-tote-mujoco-v1
~~~

每个实例还必须携带 Runtime 返回的 `scene_instance_id`、左右
`component://tool/left` / `component://tool/right` 工具描述以及 R1 Pro
URDF。完整手工诊断示例见 `examples/r1pro-mujoco-01.yaml`。正常产品流程由
Semantic Framework 在场景启动后渲染这些字段、内部签发 Pilot credential 并调用
bundle 启动器；用户不需要加入码，也不需要手工启动 Ability 或安装 Robot Skill。

backendProfile 表示 bundle 运行组合（如 r1pro-tote-mujoco-v1），用于匹配 bundle；
firmwareProfile 只交给 Robot SDK 处理固件差异，两者不能混用。
