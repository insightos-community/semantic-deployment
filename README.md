# semantic-robot-deployment

[English](README.md) | [简体中文](README.zh-CN.md)

> The remote repository is named `semantic-deployment`; its local directory in quick-start is `semantic-robot-deployment/`.

Current BEHAVIOR / cuRobo deployment entry: [source deployment guide](docs/curobo-main-deployment.md),
[exact version lock](docs/curobo-release-lock.json) (2026-09-30, deployment repo branch `feature/behavior-test`).
Build from the locked commits; Trace, screen recording, and machine run configuration are not included.

This repo assembles pinned versions of Pilot, AbilityFramework, the seven Ability types, the Robot SDK, and the Robot
Skill Runtime SDK into a per-Robot-model runtime bundle, then creates an isolated runtime directory for each Robot.

The concrete grasp-object, semantic-navigation, and place-object skills do not ship in the bundle. They are still
published, installed, and enabled separately through the Semantic Server's Robot Skill Registry. plugin-mujoco
is also a separate process; this repo only writes its HTTP endpoint into the Robot SDK configuration and does not modify it.

### Stage-image artifacts

The current type package uses `semantic-r1pro-abilities==0.4.0.dev1`, which includes the Ability/Pilot
image exchange directory fix. The default MuJoCo Robot Skills are `grasp-object 0.4.22`,
`semantic-navigation 0.4.7`, and `place-object 0.4.42`, which include stage RGB capture.
The `0.4.0` of the Ability ZIP/CR is the capability-package descriptor version, not the Python implementation package version.

When using the Framework's `scripts/refresh_v050_mujoco.py`, update this repo and the
R1 Pro Ability and Robot Skill repos in lockstep. The refresh script builds from current source into the current staging
directory, checks that the Ability source version matches this repo's wheel manifest, and verifies the actually installed
version with the bundle's Python before activation. Results are written to
`ability_implementation` (version, wheel filename, SHA-256) in `.output/v050-mujoco-refresh/refresh-summary.json`.
It does not reuse old wheels from the Ability repo's `dist`, and a running bundle cannot be updated by only upgrading
host Python packages.

After `build --activate`, the Server still must be restarted for the Catalog to take effect; then run the script's
`publish` to publish/install the three Robot Skills, and finally start the Robot Runtime. Running only `build` does not
activate; only publishing Skills does not update the Abilities inside the bundle. For existing Robots, the actual Skill
versions are reconciled against the Server's desired/installed state; updating templates does not automatically override
versions the user has already selected.

## Commands

### Standard component delivery and development updates

New deployments use the Framework's `semantic install` or the Web project import entry. First delivery of the base platform:

```bash
semantic-robot-bundle export --bundle ./built/franka-libero \
  --python-version 3.12.14 --output ./franka-robot.zip
semantic install ./franka-robot.zip --project <项目ID>
```

The export only copies the binaries, templates, Ability ZIPs, and wheels declared in the manifest; the target installer creates the Python environment.
Source dependencies, the venv, instance credentials, and runtime data stay on the local machine. The R1 Pro native package uses Python 3.13.15, matching the wheel ABI.
When exporting an existing base platform, `--file bin/semantic-robot-instance=<current build artifact>` can be used to update the instance launcher.

Subsequent Abilities are built separately via each robot repo's `semantic-source.yaml`, and models are installed separately;
a running instance copies the component bindings into its own `run/components.json`, so updates or rollbacks do not touch the executing environment.
Applying new bindings follows the existing stop, heartbeat, and readiness checks. Scenes are started through the standard Server; normal operation does not need the deployment serve scripts.
See the Framework's `docs/project-import.md` for the full flow. The build instructions below are retained for base-platform builds and maintenance of existing environments.

- semantic-robot-bundle build/inspect: assemble and verify the shared read-only bundle.
- semantic-robot-instance start: use a RobotDeployment to join for the first time or directly start a Robot.
- semantic-robot-instance render/run/status/stop: low-level operations commands; normal deployments do not invoke them by hand.

The startup order is fixed:

~~~text
AbilityFramework
→ upload and start the seven Ability types
→ confirm heartbeat by instance ID + abilityName
→ semantic-pilot
~~~

On stop, Pilot is first asked to reach the nearest safe stop point. Only when Pilot exits with 0 is
pilot_exited_cleanly=true recorded; then the instance's seven Abilities are stopped, and finally the AbilityFramework
managed by this instance is shut down. A non-zero Pilot exit, a stop timeout, or an Ability stop failure moves the
instance into failed; it never reports stopped merely because a process disappeared.

## Building from a clean directory

First prepare AbilityFramework, semantic-pilot, product wheels, third-party dependency wheels, and the seven Ability
ZIPs that have each passed their own tests. Third-party dependency versions are pinned by
`python-requirements.lock` inside the type package. The Robot Skill SDK wheel is named
`semantic_robot_skill_sdk-0.1.0.dev0-py3-none-any.whl` and contains only the SDK/Runtime, not
the three concrete Robot Skills.

~~~bash
git clone https://github.com/insightos-community/semantic-deployment.git
cd semantic-deployment
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

`build` only consumes already-built wheels, ZIPs, and binaries. `--wheel-dir` only supplies sources by the exact
filenames declared in `bundle.yaml`; it does not pull other packages from the directory into the type package. The builder creates an isolated
`python/venv`, installs the manifest wheels with `--no-index --no-deps`, and then makes the whole bundle
read-only. Different Robots share this Python environment instead of reinstalling dependencies in each instance directory; at runtime
the host `PYTHONPATH` is also cleared and user site-packages are disabled.

### Offline planning and WebSocket dependencies for MuJoCo

The Fake type package does not use the sensor WebSocket and therefore does not carry `websockets`. The MuJoCo type package additionally
declares WebSocket, Pinocchio, Ruckig, and their transitive dependencies. When building the MuJoCo bundle, change the
source, output, and requirements lock to `r1pro-mujoco`; the rest of the commands are unchanged.

All wheels must be placed in the offline artifact directory in advance. The builder checks all
manifest inputs before creating the shared Python environment; if an artifact is missing it fails immediately, rather than
producing a bundle that only exits with `ModuleNotFoundError` at runtime.

## Join and start with one command

After clicking "Add Pilot" in the Device Center to obtain a one-time join code, use the launcher from the type package. RobotDeployment is the only per-device configuration the user needs to maintain; see `examples/robot-deployment-r1pro-fake-02.yaml` for a complete Fake example.

~~~bash
export BUNDLE=/opt/semantic/bundles/r1pro-fake-0.5.0

"$BUNDLE/bin/semantic-robot-instance" start \
  --config examples/robot-deployment-r1pro-fake-02.yaml \
  --join-code ABCD12
~~~

By default the launcher scans the LAN once for a Server via `_semantic-server._tcp.local.`. mDNS only discovers the address; the real device identity is established by the dedicated Pilot credential exchanged for the join code. When mDNS is not usable on the LAN, pass explicitly:

~~~bash
  --server-http http://127.0.0.1:8080 \
  --server-ws ws://127.0.0.1:8081/ws/pilot
~~~

After the first success, `connection.yaml` has saved the dedicated credential. No join code is needed later:

~~~bash
"$BUNDLE/bin/semantic-robot-instance" start \
  --config examples/robot-deployment-r1pro-fake-02.yaml
~~~

When `--data-dir` is not specified, instance data is written to
`$XDG_STATE_HOME/semantic/robots/<robot-id>` by default; when XDG is not set, the user's
`~/.local/state/semantic/robots/<robot-id>` is used. Pass `--data-dir` explicitly only when systemd or a container
needs to assign a persistent volume. The R1 Pro Fake example already declares the first-grasp
environment in the RobotDeployment, so no manual Python object injection is run after startup.

`start` automatically handles shared bundle location, instance directory rendering, startup of AbilityFramework, the seven Ability types, and Pilot, and reconciliation of desired Robot Skills. Users no longer copy admin tokens, upload Abilities one by one, or install Skills manually.

## Local Robot Skill debugging without a Server connection

When a developer needs to observe `Robot Skill → AbilityFramework → Ability → Robot SDK`
in isolation, they can reuse an already rendered instance directory and start only AF and the seven Ability types:

```bash
bin/semantic-robot-instance debug-stack \
  --instance /var/lib/semantic/robots/r1pro-mujoco-01
```

This command does not start the Pilot resident process, does not connect to the Semantic Server, and does not read the Pilot credential.
It shares `instance.lock` with the full `run`, so the same instance must be safely stopped first. After seeing
`status: ready`, use `semantic-pilot skill run` in another terminal to execute a specific Skill;
the Skill must be finished or safely stopped before exiting `debug-stack`.

This is a development debugging entry; it does not replace production `start/run`, and it does not create Projects, Workflows, Tasks, or
Server Robot Executions.

## Low-level render, run, status, stop

First edit the bundle, Server address, and token in examples/r1pro-fake-01.yaml:

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

`run` is a foreground supervisor; production environments should be managed by systemd or a container runner. `stop` sends a stop request to the
supervisor and waits for stop evidence; it does not bypass Pilot and kill Robot processes directly.

## Directory layout

The shared read-only bundle:

~~~text
r1pro-fake-0.5.0/
├── bundle.yaml
├── bin/
├── wheels/
├── abilities/
├── templates/
└── python/venv/
~~~

The writable per-Robot instance (`connection.yaml` is generated by the first join, with mode 0600):

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

The AbilityFramework databases, CRs, packages, logs, and Ability execution data, as well as Pilot's DB,
artifacts, Skills, and logs, all belong to the current Robot instance. The shared bundle holds no runtime state.

## Two Fake Robots

Two instances reuse the same bundle but use different robot.id, pilot.id, AbilityFramework
endpoints, and instance directories:

~~~bash
"$BUNDLE/bin/semantic-robot-instance" render \
  --config examples/r1pro-fake-01.yaml \
  --output /var/lib/semantic/robots/r1pro-fake-01

"$BUNDLE/bin/semantic-robot-instance" render \
  --config examples/r1pro-fake-02.yaml \
  --output /var/lib/semantic/robots/r1pro-fake-02
~~~

Run `run` in two separate terminals. Stopping Robot A does not stop or delete Robot B's Abilities, DB,
artifacts, or logs.

## Two MuJoCo Robots sharing one SDK endpoint

The MuJoCo Runtime can expose multiple Robots on the same endpoint. Two RobotInstances may use the
same sdkEndpoint, but robot.id must differ; the Robot SDK carries the ID into the underlying requests. Each
Robot still uses a different Pilot ID, AbilityFramework endpoint, and instance directory:

~~~text
Robot A: robot.id=r1pro-001, AF=http://127.0.0.1:18081
Robot B: robot.id=r1pro-002, AF=http://127.0.0.1:18082
Shared SDK endpoint: http://127.0.0.1:18090
~~~

The native MuJoCo tote scene uses:

~~~text
model: r1_pro_chassis
backend: mujoco
backendProfile: r1pro-tote-mujoco-v1
~~~

Each instance must also carry the `scene_instance_id` returned by the Runtime, the left/right
`component://tool/left` / `component://tool/right` tool descriptors, and the R1 Pro
URDF. See `examples/r1pro-mujoco-01.yaml` for a complete manual diagnostics example. In the normal product flow, the
Semantic Framework renders these fields after the scene starts, issues the Pilot credential internally, and invokes the
bundle launcher; the user needs no join code and does not start Abilities or install Robot Skills by hand.

backendProfile denotes the bundle runtime combination (e.g. r1pro-tote-mujoco-v1) and is used to match the bundle;
firmwareProfile is only passed to the Robot SDK to handle firmware differences. The two must not be mixed.

## Project layout

- `cmd/`: the `semantic-robot-bundle` and `semantic-robot-instance` commands.
- `internal/bundle/`: packaging and verification.
- `internal/instance/` · `internal/abilityframework/`: instance lifecycle and Ability hosting.
- `type-packages/`: Fake / MuJoCo Robot type definitions.
- `examples/`: sample inputs.

## 🛠 Build and test

Requires Go **1.23+** and Make.

```bash
make build
make verify
```

The artifacts are `bin/semantic-robot-bundle` and `bin/semantic-robot-instance`. Verification covers static checks, tests, and build; it does not automatically configure a complete Robot environment.

## Using the artifacts

The bundle builder accepts matching versions of the AbilityFramework / Pilot binaries, Ability ZIPs, Python wheels, and a Robot type manifest. In quick-start, the Framework's refresh workflow prepares the inputs and invokes this tool; it is the recommended path for a full R1 Pro MuJoCo deployment.

```bash
bin/semantic-robot-bundle inspect --bundle /absolute/path/to/robot-bundle
```

The instance tool connects to the Server and runs the Robot, hosting related processes. Connection credentials and mutable state belong in a separate instance data directory and must not be written into the source tree or a public bundle.

## FAQ

- A bundle is not a Server installer, and it does not contain published Robot Skills.
- When the Server registry lacks a required Skill version, the instance cannot reach the executable state.
- Bundle inputs should come from the same quick-start manifest; upgrading wheels arbitrarily can break native ABI compatibility.
- Stop the Robot before replacing an in-use bundle; validate with Fake / simulation before connecting hardware.
- Pairing tokens and connection files are confidential; do not include them in issue reports.

[Detailed CLI and bundle reference](README.reference.md) · [Robot type definitions](type-packages/)

## License

Copyright 2026 InsightOS. First-party code is under [Apache-2.0](LICENSE); see [NOTICE](NOTICE) and [license scope](LICENSE_SCOPE.md) for third-party components and assets.

## Reproducing builds on three platforms

See the [glibc, musl, and macOS build guide](README.build.md): pinned source versions, actual script entries, tool requirements, local and CI commands, artifact locations, and per-platform verification scope.

## Windows ports (in progress)

The instance locks of the supervisor and debug stack now go through `internal/ports/filelock`:
Linux/macOS use `flock`, and Windows uses non-blocking `LockFileEx`.
The [native ports CI](.github/workflows/platform-ports.yml) tests mutual exclusion between different handles and
different processes on all three platforms, plus recovery after unlock, close, and forced process termination, including paths with Chinese characters and spaces.

With Go 1.25.8 installed, on Linux, macOS, or Windows:

```text
go test ./internal/ports/... -count=1 -timeout=2m
```

The current verification scope is only the instance-lock adapter. A full Windows supervisor still needs process-tree ownership,
graceful-stop IPC, and process-identity adaptation; there is no Windows executable release yet.
The existing Linux/macOS start and stop-evidence flows continue to be regression-tested by `go test ./...`.
A normal exit explicitly unlocks before closing the file; system lock cleanup after an abnormal exit may lag briefly.
