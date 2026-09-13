# Semantic Deployment

[English](README.md) | [简体中文](README.zh-CN.md)

📦 构建版本化 Robot Bundle，并管理单个 Robot 实例。远端仓库名为 `semantic-deployment`，在 quick-start 中的本地目录为 `semantic-robot-deployment/`。

## 工程结构

- `cmd/`：`semantic-robot-bundle` 与 `semantic-robot-instance` 命令。
- `internal/bundle/`：打包与验证。
- `internal/instance/` · `internal/abilityframework/`：实例生命周期与 Ability 托管。
- `type-packages/`：Fake / MuJoCo Robot 类型定义。
- `examples/`：示例输入。

## 🛠 构建与测试

需要 Go **1.23+** 与 Make。

```bash
make build
make verify
```

产物为 `bin/semantic-robot-bundle` 和 `bin/semantic-robot-instance`。验证包括静态检查、测试和构建，不会自动配置完整 Robot 环境。

## 产物使用

Bundle 构建器接收匹配版本的 AbilityFramework / Pilot 二进制、Ability ZIP、Python Wheel 与 Robot 类型清单。quick-start 中 Framework 的刷新工作流会准备输入并调用本工具；完整 R1 Pro MuJoCo 部署建议使用该工作流。

```bash
bin/semantic-robot-bundle inspect --bundle /absolute/path/to/robot-bundle
```

实例工具负责接入 Server 并运行 Robot、托管相关进程。连接凭据与可变状态应放在独立实例数据目录，不应写入源码或公开 Bundle。

## 常见问题

- Bundle 不是 Server 安装包，也不包含已发布的 Robot Skill。
- Server 注册表缺少要求的 Skill 版本时，实例无法进入可执行状态。
- Bundle 输入应来自同一 quick-start 清单；任意升级 Wheel 可能破坏原生 ABI 兼容性。
- 替换使用中的 Bundle 前先停止 Robot；连接硬件前先验证 Fake / 仿真。
- 配对令牌与连接文件需要保密，不要放进问题报告。

[详细 CLI 与 Bundle 参考](README.reference.md) · [Robot 类型定义](type-packages/)

## 许可证

Copyright 2026 InsightOS。自有代码采用 [Apache-2.0](LICENSE)；第三方组件与资产请查看 [NOTICE](NOTICE) 和[许可范围](LICENSE_SCOPE.md)。

## 三个平台的构建复现

参见 [glibc、musl 与 macOS 构建说明](README.build.md)：包含已锁定的源码版本、实际脚本入口、工具要求、本地与 CI 指令、产物位置和平台验证范围。
