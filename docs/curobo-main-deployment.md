# cuRobo 主部署迁移

2026-09-30 更新；适用于已有 Isaac / BEHAVIOR 环境和合法资产的新 Linux x86_64 机器。
当前源码基线以同目录 `curobo-release-lock.json` 为唯一入口。使用原部署分支：

| 仓库目录 | 发布分支 |
| --- | --- |
| semantic-framework | feature/behavior-test |
| semantic-web | feature/behavior-isaac |
| semantic-simulation/isaac-runtime | feature/behavior-test |
| semantic-robot-deployment | feature/behavior-test |
| semantic-robotsdk/robot-sdk | feature/behavior-test |
| semantic-ability/r1pro-behavior-ability | feature/behavior-test |
| semantic-ability/ability-runtime | develop |
| semantic-skill/robot-skill | feature/behavior-test |

检出时以清单中的完整 commit 为准，不使用分支最新提交代替锁定版本。
AbilityFramework 的 develop 已有更新的重编译制品；本清单仍锁定本机使用的 `258386e`，
不将未在本机采用的二进制更新混入此次基线。
本次对齐本机核心源码，不包含 Trace、录屏工作目录、测试运行资料或机器运行配置。
`semantic-20260928-lock.json` 保留为旧部署快照，不能作为本次新部署的源码锁。

## 运行结构

Server 管理场景生命周期，并启动 Runtime、Pilot、AbilityFramework。Ability 通过现有 SDK HTTP
接口提交目标；SDK 的 cuRobo 规划器运行在 Runtime 原生引擎进程内，由 Runtime 推进轨迹。
浏览器使用 Semantic Web 的场景几何与位姿渲染，传感器观测来自原生相机。

当前可重建版本：

| 组件 | 版本 |
| --- | --- |
| Robot 运行包 | 0.1.28，包含 Ability 0.5.24 和 Skill Worker 离线依赖修复 |
| Runtime 源码 | 0.1.27，原生启动，不含录屏分支 |
| SDK / Ability | 0.5.6 / 0.5.24 |
| behavior-init / behavior-nav | 0.1.6 / 0.1.9 |
| behavior-grasp | 0.1.47 |
| behavior-upright / behavior-place | 0.1.1 / 0.1.7 |
| behavior-radio-button | 0.1.37 |

包版本号不能代替源码提交：SDK 等组件在相同版本号下也可能存在代码差异。
本机原有 Robot 底座为 0.1.26，新构建使用部署分支已发布的 0.1.28 配方，修正旧 Ability
Wheel 文件名并补齐 python-fcl、cython；不改变本次锁定的机器人业务源码。
Ability 锁定发布提交 `80bec60`，与本机 `c123fe6` 的业务源码相同，仅清理两个临时测试数据库。

## 1. 准备相邻仓库

保持下面的目录关系，构建配方引用相邻仓库：

```text
semantic/
  semantic-framework/
  semantic-web/
  semantic-robot-deployment/       # 远端项目名 semantic-deployment
  semantic-robotsdk/robot-sdk/
  semantic-ability/r1pro-behavior-ability/
  semantic-ability/ability-runtime/ # AbilityFramework + ability_py Wheel
  semantic-skill/robot-skill/
  semantic-simulation/isaac-runtime/ # 远端项目名 issac-runtime
  third_party/curobo/
  third_party/sam3/
  third_party/behavior-curobo/       # BEHAVIOR-1K，含 OmniGibson / bddl3 / joylo
```

先从部署仓库的 `feature/behavior-test` 分支取得本说明和源码锁。
按 `curobo-release-lock.json` 中每项 path、URL 创建相邻仓库；Semantic 仓库 clone 对应发布
分支，third_party 从对应官方仓库获取。然后全部 checkout 清单指定 commit，保留完整历史或
确保精确提交已获取，不以默认分支最新代码替代。部署仓库自身使用包含本清单的提交。
例如，在新建的空工作区准备 Skill：

```bash
git clone --branch feature/behavior-test \
  https://github.com/insightos-community/semantic-skill/robot-skill.git \
  semantic-skill/robot-skill
git -C semantic-skill/robot-skill checkout --detach c379432d25a40cf1050665338c68392e5cb0eaee
```

在已有工作区切换提交前先检查未提交改动，不覆盖本地文件。
`ability-runtime` 的大文件需要 `git lfs pull`；检查 AbilityFramework 可执行，Wheel 可以正常解包。

## 2. 对齐原生环境和机器人资产

本机核心原生环境为 Python 3.11.16、Isaac Sim 5.1.0.0、锁定源码的 OmniGibson 3.9.2、
PyTorch 2.7.0+cu128、Warp 1.12.0、NumPy 1.26.0、SciPy 1.15.3、Trimesh 4.5.1。
核心版本清单见 `curobo-native-environment.json`，它不代替完整传递依赖锁。
cuRobo 必须使用源码锁中的 `78612f45...` 提交；安装包版本字符串受构建方式影响，不能单独作为依据。

native venv 从已安装 Isaac / BEHAVIOR 的 Python 3.11 环境继承系统包，再安装本次锁定的
OmniGibson、bddl3、joylo、SDK 和 Runtime。宿主环境的 OmniGibson 可能显示 3.9.3，
实际 native 环境必须加载 `third_party/behavior-curobo/OmniGibson` 中的 3.9.2 源码。
本机该目录来自锁定提交的干净归档，不需要旧部署记录中的 WebRTC 本地改动。
新机器应重建 venv，不直接复制已有环境。已准备匹配依赖后可安装源码：

```bash
uv pip install --python "$SEMANTIC_ROOT/envs/native/bin/python" --no-build-isolation --no-deps \
  -e "$SEMANTIC_ROOT/third_party/behavior-curobo/bddl3" \
  -e "$SEMANTIC_ROOT/third_party/behavior-curobo/OmniGibson" \
  -e "$SEMANTIC_ROOT/third_party/behavior-curobo/joylo" \
  -e "$SEMANTIC_ROOT/semantic-robotsdk/robot-sdk/packages/core" \
  -e "$SEMANTIC_ROOT/semantic-robotsdk/robot-sdk/packages/r1pro" \
  -e "$SEMANTIC_ROOT/semantic-simulation/isaac-runtime"
uv pip install --python "$SEMANTIC_ROOT/envs/native/bin/python" --no-build-isolation --no-deps \
  -e "$SEMANTIC_ROOT/third_party/curobo"
```

cuRobo CUDA 扩展需在目标机器使用与 PyTorch 对应的 CUDA 12.8 工具链构建，按目标 GPU
选择架构；不要跨机器直接复用编译缓存。`SEMANTIC_CUROBO=1` 的既有启动流程保留。
Robot / Ability 的独立环境使用 `type-packages/r1pro-behavior-atomic/requirements.lock`，
其 NumPy 等版本与 native 环境分别管理，不相互覆盖。

资产根目录应含 `2026-challenge-task-instances/metadata/available_tasks.yaml`。
核对 `curobo-assets/r1pro-assets.json` 中的机器人文件摘要。应保留 URDF、USD、cuRobo
配置及其引用网格；已安装场景资产可复用。固定碰撞球 JSON 已随 SDK 源码提交。

双指同步的 USD 改动在 `curobo-assets/r1pro-gripper-mimic.patch`；先备份，再在机器人
资产目录运行 `patch --dry-run -p1 < <补丁路径>`，检查通过后再应用。该补丁只覆盖双指同步，
其他文件摘要不一致时需比较或从合法资产持有方交付相同机器人目录。
完整资产、模型权重、CUDA/Shader 缓存均不随 Git 仓库交付。

主部署已移除强制打开 GPU Dynamics 的覆盖配置，迁移沿用原生引擎的物理配置。
cuRobo 的 GPU 规划与物理 GPU Dynamics 分别配置。

## 3. 配置原生 Runtime 启动

将 `examples/curobo-native.env.example` 复制到新工作区 `curobo-native.env`，填写环境、资产、
GPU 和端口。需要额外 Conda 动态库时设置 `SEMANTIC_BEHAVIOR_LIB_DIR`。
`SEMANTIC_BEHAVIOR_MAX_STEPS` 设置整轮场景的累计步数上限，必须为正整数；
未配置时默认 `10000`，示例设为 `100000`，适合包含多次规划的长流程。
空闲和规划等待期间的仿真步也计入预算，此值不会改变仿真更新频率或单个动作超时。
修改 `curobo-native.env` 后需重启 Runtime 并重新加载场景；当前场景不会即时更新。
创建工作区 `run-native-runtime.sh`：

```bash
#!/usr/bin/env bash
set -euo pipefail
HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$HERE/curobo-native.env"
exec "$HERE/semantic-robot-deployment/scripts/run-curobo-runtime.sh" "$@"
```

给予执行权限。启动器从同一工作区加载 Runtime 和 SDK，迁移后无需 `.integration` 实验副本。
Server 负责调用该脚本，通常无需另开终端重复启动 Runtime。

## 4. 构建与安装

在新工作区配置 Go、uv、Node；从对应仓库构建 Framework 和 Web。
Framework 首次使用 `semantic init -c <安装配置路径>` 生成独立配置和数据目录。
将 `examples/native-curobo-runtime.yaml` 放入配置的 `simulation.runtimes_dir`，替换其中所有
工作区和资产绝对路径。保持 `endpoint` 与 Runtime 脚本端口一致。

受管机器人配置示例：

```yaml
robot_runtime:
  enabled: true
  server_http_url: http://127.0.0.1:18200
  server_websocket_url: ws://127.0.0.1:18201/ws/pilot
  ability_port_first: 18300
  ability_port_last: 18399
```

保留初始化器生成的其他路径字段。Server HTTP / WS 设置为 18200 / 18201；端口可调整，
同步更新回连地址。新机器可以使用相同端口，前提是本机没有冲突。

SAM3.1 使用独立 Python 3.12 环境和清单锁定的 SAM 源码。本机版本为 Python 3.12.14、
PyTorch 2.10.0+cu128、torchvision 0.25.0+cu128、NumPy 1.26.4。
权重使用 `sam3.1_multiplex.pt`，下载来源、修订及 SHA-256 见源码锁的 `external_assets.model`；
下载后校验摘要，不把权重加入 Git。
从 `type-packages/r1pro-behavior-atomic/templates/model-registry.example.json` 生成同目录
`model-registry.json`，替换 Python / source / checkpoint 路径及 GPU 配置后再构建。
初始化和导航可以在感知服务准备前验证，抓取与放置需要可用感知环境。

构建命令（`SEMANTIC_ROOT` 已指向相邻仓库工作区）：

```bash
cd "$SEMANTIC_ROOT/semantic-framework"
make build
mkdir -p "$SEMANTIC_ROOT/semantic-robot-deployment/.output/bin"
go build -o "$SEMANTIC_ROOT/semantic-robot-deployment/.output/bin/semantic-pilot" ./cmd/semantic-pilot
cd "$SEMANTIC_ROOT/semantic-robot-deployment"
mkdir -p .output/bin
go build -o .output/bin/semantic-robot-instance ./cmd/semantic-robot-instance
export SEMANTIC_CLI="$SEMANTIC_ROOT/semantic-framework/.output/bin/semantic"
mkdir -p "$SEMANTIC_ROOT/packages/curobo"
"$SEMANTIC_CLI" build type-packages/r1pro-behavior-atomic \
  --output "$SEMANTIC_ROOT/packages/curobo/r1pro-behavior-atomic-0.1.28.zip"
"$SEMANTIC_CLI" build "$SEMANTIC_ROOT/semantic-ability/r1pro-behavior-ability" \
  --output "$SEMANTIC_ROOT/packages/curobo/r1pro-behavior-abilities-0.5.24.zip"
```

从源码锁读取六个 Skill 的版本并构建，避免沿用旧 ZIP 文件名：

```bash
python3 - <<'PY_BUILD'
import json, os, pathlib, subprocess
root = pathlib.Path(os.environ["SEMANTIC_ROOT"])
lock = json.loads((root / "semantic-robot-deployment/docs/curobo-release-lock.json").read_text())
for name, version in lock["skills"].items():
    source = root / "semantic-skill/robot-skill/semantic_robot_skills/skills" / name.replace("-", "_")
    output = root / "packages/curobo" / f"{name}-{version}.zip"
    subprocess.run([os.environ["SEMANTIC_CLI"], "build", str(source), "--output", str(output)], check=True)
PY_BUILD
cd "$SEMANTIC_ROOT/semantic-web"
npm ci
npm run build
```

导入 trash 与 radio 两个场景包：`behavior-picking_up_trash-scenes-3.9.3.zip` 和
`behavior-turning_on_radio-scenes-3.9.3.zip`。既有导出包的校验摘要见
`external_assets.scene_packages`，包本身和完整 BEHAVIOR 资产另行交付。
也可使用锁定 Runtime 的 `--data-root ... --export-scenes ... --scene ...` 重新导出，
再运行 `semantic build`；导出前用 `--catalog` 核对 `behavior-picking_up_trash-0`、
`behavior-turning_on_radio-0` 及所需 layout。重新导出的包另记摘要，不套用旧 ZIP 的哈希。

启动新 Server 和 Web，创建自己的管理员凭据、项目及 LLM 配置。从 Web 安装场景包、
Robot 运行包、Ability 包，将 Ability 绑定到项目 R1Pro 或具体机器人。选择场景初态并启动
Layout；Server 生成对应 Pilot 凭据并拉起受管 Robot。随后上传六个 Skill，在机器人上
安装并启用当前版本。模板不预填旧 Skill 引用。

Web 在其他机器浏览时，`VITE_SERVER_HTTP` / `VITE_SERVER_WS` 必须使用浏览器可访问的
新 Server 地址。例如 `http://<新机器IP>:18200` 和 `ws://<新机器IP>:18201`。
后端本机调用 Runtime、Pilot 回连可以继续使用 127.0.0.1。

## 5. 安装核对

部署完成后核对源码 commit、实际加载路径与安装启用的包版本。特别确认：

- init 0.1.6 接受 `arm_posture=down/raised`，默认 down。
- nav 0.1.9 默认 `arrival_radius_m=0.06`，显式输入仍按输入值执行。
- grasp 0.1.47 自动观察姿态包含 0.01 m 躯干自碰撞间距。
- place 0.1.7 接受抓取返回的 `object_size_m` 与 `eef_from_object`。跨步骤复用由调用方
  读取本轮同对象的成功抓取结果并原样传入；仅安装新版 Skill 不会自动补充任务输入。

依次检查场景加载、八类 Ability ready、Robot idle、原生相机、SAM 识别和 cuRobo plan-only，
再按需要执行任务。源码和包版本对齐不等同于目标机器已完成场景验收。
本清单不携带测试 prompt、运行记录、运行数据库、凭据或主机配置；历史部署快照只作归档，
新部署不要从中取旧版组件包。现有运行时配置按目标机器独立维护。
