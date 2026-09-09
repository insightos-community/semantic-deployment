# Semantic Deployment

[English](README.md) | [简体中文](README.zh-CN.md)

📦 Build versioned Robot Bundles and manage individual Robot instances. This repository is named `semantic-deployment` remotely and lives at `semantic-robot-deployment/` in quick-start.

## Structure

- `cmd/` — `semantic-robot-bundle` and `semantic-robot-instance` commands.
- `internal/bundle/` — packaging and validation.
- `internal/instance/` · `internal/abilityframework/` — instance lifecycle and Ability hosting.
- `type-packages/` — Fake and MuJoCo Robot type definitions.
- `examples/` — example inputs.

## 🛠 Build and test

Requires Go **1.23+** and Make.

```bash
make build
make verify
```

Outputs: `bin/semantic-robot-bundle` and `bin/semantic-robot-instance`. Verification runs static checks, tests, and builds; it does not provision a working Robot environment.

## Use the tools

The bundle builder consumes matched AbilityFramework/Pilot binaries, Ability ZIPs, Python Wheels, and a Robot type manifest. Quick-start's Framework refresh workflow prepares these inputs and invokes this tool; use that workflow for the complete R1 Pro MuJoCo stack.

```bash
bin/semantic-robot-bundle inspect --bundle /absolute/path/to/robot-bundle
```

The instance tool handles joining and running a Robot against Server. It supervises the Robot's processes; connection credentials and mutable state belong in a per-instance data directory, not in the source tree or a public bundle.

## Troubleshooting

- A Bundle is not a Server installation and does not contain published Robot Skills.
- An instance cannot become executable until its requested Skill versions exist in the Server registry.
- Keep bundle inputs from the same quick-start manifest; arbitrary Wheel upgrades can break native ABI compatibility.
- Stop affected Robots before replacing active bundles. Use Fake/simulation before connecting hardware.
- Protect pairing tokens and connection files; do not include them in bug reports.

[Detailed CLI and bundle reference](README.reference.md) · [Robot type definitions](type-packages/)

## License

Copyright 2026 InsightOS. First-party code: [Apache-2.0](LICENSE). See [NOTICE](NOTICE) and [license scope](LICENSE_SCOPE.md) for third-party components and assets.
