# LIBERO / Franka 独立部署

此型号包仅启动 `FrankaVLA.V2`，模型由 Franka Ability 的
`configs/smolvla-libero.json` 绑定。Robot Skill `vla-manipulation` 单独发布到
Server，再通过 desired/installed 对账安装；不复用 R1 的工具、URDF 或拆码垛 Skill。

## 隔离与前置制品

从 Semantic 主目录运行，示例安装根目录为
`.cache/libero-behavior-vla/deployment`。原 `.output`、R1 数据库及其服务不动。
新 Server 使用 19080/19081，Web 使用 3001，LIBERO Runtime 使用 19090，
受管 AbilityFramework 使用 19100–19149。

以下是源码开发部署工具，不是已发布的自包含安装器。执行 `assemble` 前准备：

- `ROOT/bin/semantic-server`、`semantic`、`semantic-pilot`：当前 Framework 源码构建。
- `ROOT/bin/semantic-robot-bundle`、`semantic-robot-instance`：当前本仓源码构建。
- `ROOT/build/wheels`：Franka Ability `uv.lock` 锁定的 SmolVLA 完整依赖，以及
  当前源码的 Robot SDK Core、Franka SDK、Franka Ability、Robot Skill SDK Wheel。
- `ROOT/build/requirements.txt`：用
  `uv export --project semantic-ability/franka-ability --frozen --no-dev --extra smolvla --no-emit-local --no-hashes`
  导出的依赖清单；再用 `pip wheel --no-deps -r ...` 准备离线 Wheel。
- `semantic-ability/ability-runtime` 中已有的 AbilityFramework、ability-py Wheel
  和 `.venv/bin/ability-scaffold`。
- LIBERO 原生资产、`profiles/libero/.venv`、Franka Ability `.venv` 以及本机模型缓存。
  当前脚本使用 `.cache/libero-behavior-vla/LIBERO` 和该目录下的 `huggingface` 缓存，
  模型运行时开启离线模式，不在启动中临时下载或改变模型 revision。

## 组装与启动

```bash
export LIBERO_DEPLOY_ROOT="$PWD/.cache/libero-behavior-vla/deployment"
semantic-ability/franka-ability/.venv/bin/python \
  semantic-robot-deployment/scripts/deploy_franka_libero.py assemble --root "$LIBERO_DEPLOY_ROOT"
semantic-ability/franka-ability/.venv/bin/python \
  semantic-robot-deployment/scripts/deploy_franka_libero.py configure --root "$LIBERO_DEPLOY_ROOT"
semantic-ability/franka-ability/.venv/bin/python \
  semantic-robot-deployment/scripts/deploy_franka_libero.py serve --root "$LIBERO_DEPLOY_ROOT"
```

`assemble` 在构建副本中打包 Ability，保留依赖锁，并分别检查 Ability 环境和
Skill 的离线依赖可安装性。已有同路径 Bundle 时先安全停止使用它的 Robot，
将旧包移入备份，再构建；不覆盖正在运行的只读包。

另一终端在 `semantic-web` 中启动：

```bash
VITE_SERVER_HTTP=http://127.0.0.1:19080 \
VITE_SERVER_WS=ws://127.0.0.1:19081 npm run dev -- --host 0.0.0.0 --port 3001
```

在新 Web 中配置模型服务与密钥，创建 LIBERO 项目，选择 `LIBERO Spatial · Task 0`
及原生初态，再启动场景。发布 `ROOT/build/vla-manipulation-<源码版本>.zip`，
确认 Robot 的安装版本与源码一致。已有 Robot 的 desired 版本不会因模板更新而
被强制覆盖，须通过设备页或正式 Skill 安装接口选择新版本。

## 验收边界

1. 场景运行且位姿流持续推进，不代表 Robot 已就绪；还须检查 Ability Running、
   Pilot 在线，以及 `vla-manipulation` 安装成功且已启用。
2. 设备页 Skill 调试可验证执行、阶段图像和原生评测，但不能代替 Agent 验收。
3. Agent 验收须在真实对话中调用该 Skill。抓取指定物体使用 `objective=grasp`，
   原生完整任务使用 `objective=native_task`，不能混淆两种成功标准。
4. 模型密钥不随 Bundle、源码或构建证据复制。新 Server 未配置有效密钥时，
   对话闭环属于未完成，不以 Mock 或独立推理结果代替。
5. 失败执行和图像保留在独立 Server 数据目录，未通过原生目标判断不能报告任务成功。

本型号包不包含 BEHAVIOR/R1Pro π0.5；不能据此宣称 BEHAVIOR 已部署可用。
