# 172.16.60.194 Semantic 部署

本文与 `semantic-20260928-lock.json` 是 2026-09-28 的历史部署快照。
新部署使用 [当前源码说明](curobo-main-deployment.md) 和
[当前源码锁](curobo-release-lock.json)，不要从下方历史版本表选择组件包。

- SSH：`ubuntu@172.16.60.194`
- 网页：<http://172.16.60.194:18385>
- 项目：Semantic BEHAVIOR
- 工作区：`/home/ubuntu/workspace/semantic`
- 管理员：`admin`；初始密码保存在目标机 `.runtime/admin.json`（0600）。
- LLM：沿用源机模型配置及服务端托管密钥，默认 `deepseek-v4-flash`。
- 服务：`semantic-server.service`、`semantic-web.service`，systemd 用户服务，已启用 linger 和开机启动。

登录后可选 picking_up_trash 或 turning_on_radio；每个场景包包含 instance-301 至 instance-320。Runtime、Robot、Pilot 和 Ability 生命周期由 Server 管理。

## 常用命令（在目标机 ubuntu 用户执行）

```bash
cd /home/ubuntu/workspace/semantic
systemctl --user status semantic-server semantic-web
python3 -c 'import json; print(json.load(open(".runtime/admin.json"))["password"])'
tail -f .runtime/server.log
```

查看 native 环境：

```bash
source /home/ubuntu/workspace/semantic/activate-native.sh
python -c 'import torch; print(torch.cuda.get_device_name(0))'
```

配置位置：`curobo-native.env`、`semantic-framework/.output/configs/semantic-server.yaml`。SAM 使用独立 `envs/sam31`，native 使用 `envs/native`。GPU Dynamics 关闭，无用视口关闭，GPU 0，场景步数预算 100000。

## 发布与验证记录

各仓库代码由 Git 的 `deploy/semantic-20260928` 分支拉取；准确提交、包哈希见 `install/remote-release-lock.json`。底座修正为 0.1.28，Ability 0.5.24、Runtime 0.1.27。六个 Skill：init 0.1.5、nav 0.1.8、grasp 0.1.46、upright 0.1.1、place 0.1.7、radio-button 0.1.37。

记录位于 `install/`：`deployment.json`、`scene-validation.json`、`sam-smoke-result.json`、`curobo-smoke-result.json`、`single-can-validation/result.json`。单罐是否完成以该 result 的 `passed` 和每步真实执行终态为准。

数据集复用 `/home/ubuntu/Dataset/Behavior/datasets`；R1Pro USD mimic 补丁前备份为 `install/r1pro.usda.before-mimic`。LLM 密钥、管理员密码和运行数据库不在 Git 中。

## 已完成的分层验收

- cuRobo 在 RTX 5090 上完成 GPU 轨迹规划，31 个轨迹点，目标误差约 1.49e-8 rad。
- SAM3.1 在 RTX 5090 上完成实际推理，独立验证图像中识别到一个 truck。
- radio（instance-304）和 trash（instance-301）均实际加载；robot_r1 就绪，8 类 Ability 为 0.5.24，6 个 Skill 已安装启用；head 相机 JPEG 已保存并检查。
- 单罐流程另见目标机 install/single-can-validation/result.json，不能将上述基础验收等同于抓放任务完成。

## 源码发布

八个主仓库发布在 deploy/semantic-20260928 分支，新机通过 Git 拉取。third_party 的 cuRobo、SAM、BEHAVIOR 从官方仓库检出清单中的精确提交。目标包哈希与原本机包哈希分别保留在 semantic-20260928-lock.json 的 target_packages 与 local_packages。

底座 0.1.28 修正了旧 Ability wheel 文件名，并补齐抓取和按钮 Skill 所需的 python-fcl 0.7.0.11、cython 3.3.0 离线依赖。

## 单罐端到端验收结果

2026-09-28，新机经网页 WebSocket 和 LLM 创建任务，Workflow `wf-38eccf7e-55a5-48ce-884d-04171751b292` 已 completed。trash instance-301 中 can_of_soda_115 的初始化、导航、抓取、直立、持物导航、投放、直立共7次 Skill 执行全部 completed。放置复用本次抓取的尺寸和抓持变换，浮点差异小于 1e-12。当前保留验收后的 trash 场景。

完整记录：目标机 install/single-can-validation/result.json 和 pilot-evidence.json。收音机场景已验证加载、相机、Robot/Ability/Skill 就绪，本轮未执行按键完整任务。
