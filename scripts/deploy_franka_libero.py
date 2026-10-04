#!/usr/bin/env python3
"""本地 LIBERO/Franka 独立部署，不读写正在使用的 R1 安装目录。

模型依赖从 Franka 的 uv.lock 导出、制成 wheel 后交给现有 Bundle 构建器；
本脚本只组装型号配置及部署资产，不在 Framework 中引入模型或任务逻辑。
"""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import shutil
import subprocess
import zipfile

import yaml


def write_yaml(path: Path, value: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(yaml.safe_dump(value, allow_unicode=True, sort_keys=False))


def assemble(workspace: Path, root: Path) -> None:
    build = root / "build"
    wheels = build / "wheels"
    vendor = workspace / "semantic-ability/ability-runtime"
    shutil.copy2(vendor / "ability_py-0.4.0-py3-none-any.whl", wheels)
    source = workspace / "semantic-robot-deployment/type-packages/franka-libero"
    staging = build / "franka-libero"
    shutil.copytree(source, staging, dirs_exist_ok=True)
    shutil.copy2(
        workspace / "semantic-ability/franka-ability/configs/smolvla-libero.json",
        staging / "templates/smolvla-libero.json",
    )
    manifest = yaml.safe_load((staging / "bundle.yaml").read_text())
    manifest["spec"]["artifacts"]["pythonWheels"] = [
        "wheels/" + wheel.name for wheel in sorted(wheels.glob("*.whl"))
    ]
    write_yaml(staging / "bundle.yaml", manifest)
    # 把依赖锁随产物留存，后续安装不能重新解析出另一个 Torch / LeRobot 组合。
    shutil.copy2(
        workspace / "semantic-ability/franka-ability/uv.lock", staging / "uv.lock"
    )
    shutil.copy2(build / "requirements.txt", staging / "requirements.txt")
    ability_zip = build / "franka-vla.zip"
    # 打包器会生成 requirements.txt；仅允许它写构建副本，避免污染源码。
    ability_source = build / "franka-vla-source"
    shutil.copytree(
        workspace / "semantic-ability/franka-ability/abilities/franka-vla",
        ability_source,
        dirs_exist_ok=True,
    )
    subprocess.run(
        [
            str(vendor / ".venv/bin/ability-scaffold"),
            "pack",
            str(ability_source),
            "-o",
            str(ability_zip),
        ],
        check=True,
    )
    subprocess.run(
        [
            str(root / "bin/semantic-robot-bundle"),
            "build",
            "--source",
            str(staging),
            "--output",
            str(root / "robot-bundles/franka-libero-0.1.0"),
            "--python",
            str(workspace / "semantic-ability/franka-ability/.venv/bin/python"),
            "--wheel-dir",
            str(wheels),
            "--file",
            "bin/semantic-robot-instance=" + str(root / "bin/semantic-robot-instance"),
            "--file",
            "bin/semantic-pilot=" + str(root / "bin/semantic-pilot"),
            "--file",
            "bin/AbilityFramework=" + str(vendor / "AbilityFramework"),
            "--file",
            "abilities/franka-vla.zip=" + str(ability_zip),
        ],
        check=True,
    )
    installed_python = root / "robot-bundles/franka-libero-0.1.0/python/venv/bin/python"
    subprocess.run([str(installed_python), "-m", "pip", "check"], check=True)
    skill_root = (
        workspace
        / "semantic-skill/robot-skill/semantic_robot_skills/skills/vla_manipulation"
    )
    # Skill 有独立虚拟环境，必须能仅用本 Bundle 的 wheel 离线安装其锁定依赖。
    # 不能以 Ability 主环境的 pip check 代替 Pilot 的安装契约。
    subprocess.run(
        [
            str(installed_python),
            "-m",
            "pip",
            "install",
            "--dry-run",
            "--ignore-installed",
            "--no-index",
            "--find-links",
            str(wheels),
            "-r",
            str(skill_root / "requirements.lock"),
        ],
        check=True,
    )
    skill_metadata = yaml.safe_load(
        (skill_root / "SKILL.md").read_text().split("---", 2)[1]
    )
    with zipfile.ZipFile(
        build / f"vla-manipulation-{skill_metadata['version']}.zip",
        "w",
        zipfile.ZIP_DEFLATED,
    ) as archive:
        for path in sorted(skill_root.rglob("*")):
            if (
                path.is_file()
                and "__pycache__" not in path.parts
                and path.suffix != ".pyc"
            ):
                archive.write(path, path.relative_to(skill_root))


def configure(workspace: Path, root: Path) -> None:
    (root / "robot-bundles").mkdir(parents=True, exist_ok=True)
    config_path = root / "configs/semantic-server.yaml"
    subprocess.run(
        [str(root / "bin/semantic"), "init", "-c", str(config_path)], check=True
    )
    config = yaml.safe_load(config_path.read_text())
    config["server"].update(http_addr=":19080", ws_addr=":19081", write_timeout="180s")
    config["robot_runtime"].update(
        enabled=True,
        bundles_dir=str(root / "robot-bundles"),
        data_root=str(root),
        server_http_url="http://127.0.0.1:19080",
        server_websocket_url="ws://127.0.0.1:19081/ws/pilot",
        ability_port_first=19100,
        ability_port_last=19149,
    )
    write_yaml(config_path, config)
    runtime = yaml.safe_load(
        (workspace / "semantic-framework/configs/runtimes.d/libero.yaml").read_text()
    )
    runtime.update(
        installation_id="isolated-libero",
        endpoint="http://127.0.0.1:19090",
        launch_mode="process",
        workdir=str(workspace / "semantic-simulation/mujoco-runtime"),
        command=[
            str(
                workspace
                / "semantic-simulation/mujoco-runtime/profiles/libero/.venv/bin/python"
            ),
            "-m",
            "semantic_sim_profiles.runtime_api",
        ],
    )
    runtime["environment_refs"] = {
        key: key
        for key in (
            "SEMANTIC_LIBERO_ROOT",
            "SEMANTIC_LIBERO_CONFIG_ROOT",
            "SEMANTIC_SIM_PROFILE",
            "SEMANTIC_LIBERO_CONTROLLER",
            "SEMANTIC_LIBERO_CAMERA_WIDTH",
            "SEMANTIC_LIBERO_CAMERA_HEIGHT",
        )
    }
    runtime["environment_refs"].update(
        {
            "PLUGIN_MUJOCO_PORT": "SEMANTIC_LIBERO_PORT",
            "MUJOCO_GL": "SEMANTIC_LIBERO_GL",
            "PYOPENGL_PLATFORM": "SEMANTIC_LIBERO_GL",
        }
    )
    write_yaml(root / "runtimes.d/libero.yaml", runtime)
    # LIBERO 的对外场景目录取自 Runtime Pack 的权威来源；Framework 的
    # configs/scenes.d 只保留原生 MuJoCo/robosuite 条目，不再放单任务 LIBERO
    # 占位（会与场景包全部 130 个任务里的同名 scene_id 冲突）。
    catalog = yaml.safe_load(
        (
            workspace
            / "semantic-simulation/mujoco-runtime/runtime-packs/libero-robosuite-1.4/catalog/catalog.yaml"
        ).read_text()
    )
    entry = catalog["entries"][0]
    entry["description"] = (
        "原生 Franka + SmolVLA；持续仿真，支持 Agent 调用 vla-manipulation"
    )
    version = entry["versions"][0]
    version["variants"] = [
        {
            "variant_id": f"init-{i}",
            "name": f"原生初态 {i}",
            "kind": "init_state",
            "parameters": {"init_state_id": i},
        }
        for i in range(3)
    ]
    version["evaluation"].update(supports_comparison=False, available_variants=["base"])
    write_yaml(root / "content/scene-catalogs/libero.yaml", catalog)


def serve(workspace: Path, root: Path) -> None:
    # 密钥由 Framework 在源码目录加载 .env；端口、数据路径显式固定到独立安装。
    # Runtime 控制配置必须与权重绑定匹配，不能沿用关节位置控制器。
    os.environ.update(
        {
            "SEMANTIC_SERVER_HTTP_ADDR": ":19080",
            "SEMANTIC_SERVER_WS_ADDR": ":19081",
            "SEMANTIC_STORE_SQLITE_PATH": str(root / "data/semantic.db"),
            "SEMANTIC_ROBOT_RUNTIME_DATA_ROOT": str(root),
            "SEMANTIC_ROBOT_RUNTIME_BUNDLES_DIR": str(root / "robot-bundles"),
            "SEMANTIC_SIMULATION_RUNTIMES_DIR": str(root / "runtimes.d"),
            "SEMANTIC_SIMULATION_CATALOG_DIR": str(root / "content/scene-catalogs"),
            "SEMANTIC_LIBERO_ROOT": str(
                workspace / ".cache/libero-behavior-vla/LIBERO"
            ),
            "SEMANTIC_LIBERO_CONFIG_ROOT": str(root / "runtime-libero-config"),
            "SEMANTIC_SIM_PROFILE": "libero-robosuite-1.4",
            "SEMANTIC_LIBERO_CONTROLLER": "OSC_POSE",
            "SEMANTIC_LIBERO_CAMERA_WIDTH": "256",
            "SEMANTIC_LIBERO_CAMERA_HEIGHT": "256",
            "PLUGIN_MUJOCO_PORT": "19090",
            "MUJOCO_GL": "egl",
            "PYOPENGL_PLATFORM": "egl",
            "SEMANTIC_LIBERO_PORT": "19090",
            "SEMANTIC_LIBERO_GL": "egl",
            "HF_HOME": str(workspace / ".cache/libero-behavior-vla/huggingface"),
            "HF_HUB_OFFLINE": "1",
            "TRANSFORMERS_OFFLINE": "1",
        }
    )
    os.chdir(workspace / "semantic-framework")
    os.execv(
        str(root / "bin/semantic-server"),
        ["semantic-server", "-c", str(root / "configs/semantic-server.yaml")],
    )


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=["assemble", "configure", "serve"])
    parser.add_argument(
        "--workspace", type=Path, default=Path(__file__).resolve().parents[2]
    )
    parser.add_argument("--root", required=True, type=Path)
    args = parser.parse_args()
    globals()[args.action](args.workspace.resolve(), args.root.resolve())
