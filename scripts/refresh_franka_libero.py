#!/usr/bin/env python3
"""Franka LIBERO 机器人运行支持包（robot_base）的一键重建脚本。

从克隆好的工作区出发，串起 deploy_franka_libero.py assemble 之前的全部准备
（Go 二进制、scaffold venv、产品 Wheel、uv.lock 依赖闭包收集）和之后的导出：

    编译 semantic-robot-bundle / semantic-robot-instance / semantic-pilot
    → 确保 franka-ability venv 与 ability-runtime scaffold venv
    → uv export 生成锁定 requirements.txt
    → pip wheel 收集全部依赖到 build/wheels（优先复用种子缓存，离线可用）
    → deploy_franka_libero.py assemble 组装 Bundle
    → semantic-robot-bundle export 导出 franka-libero-robot.zip

所有路径相对本脚本所在仓库推导，无绝对路径依赖。首次运行需要网络
（下载 Wheel）；之后 build/wheels 与种子缓存可离线复用。
"""
from __future__ import annotations

import argparse
import hashlib
import os
import re
import shutil
import subprocess
import sys
import zipfile
from pathlib import Path

SCRIPT = Path(__file__).resolve()
DEPLOYMENT = SCRIPT.parents[1]          # semantic-robot-deployment
WORKSPACE_DEFAULT = SCRIPT.parents[2]   # 工作区根（各仓库的父目录）


class RefreshError(RuntimeError):
    pass


def run(cmd: list[str], cwd: Path | None = None, env: dict | None = None) -> None:
    printable = " ".join(str(c) for c in cmd)
    where = f"  (cwd={cwd})" if cwd else ""
    print(f"$ {printable}{where}", flush=True)
    subprocess.run([str(c) for c in cmd], cwd=cwd, check=True, env=env)


def banner(step: str, total: int, title: str) -> None:
    print(f"\n===== [{step}/{total}] {title} =====", flush=True)


def require_dir(path: Path, label: str) -> Path:
    if not path.is_dir():
        raise RefreshError(f"缺少{label}: {path}（请先克隆对应仓库并切到 feature/libero-behavior-vla）")
    return path


def require_file(path: Path, label: str, min_bytes: int = 1024) -> Path:
    if not path.is_file() or path.stat().st_size < min_bytes:
        raise RefreshError(
            f"缺少{label}: {path}\n"
            f"  它由 Git LFS 提供——若文件只有几百字节说明是 LFS 指针，"
            f"请在 ability-runtime 仓库执行: git lfs pull"
        )
    return path


def require_tool(name: str) -> str:
    path = shutil.which(name)
    if not path:
        raise RefreshError(f"找不到 {name}，请先安装并加入 PATH")
    return path


def make_writable(root: Path) -> None:
    """bundle build 会把产物目录设为只读，删除前先恢复写权限。"""
    for path in sorted(root.rglob("*"), key=lambda p: len(p.parts), reverse=True):
        if path.is_dir():
            path.chmod(path.stat().st_mode | 0o700)
    if root.is_dir():
        root.chmod(root.stat().st_mode | 0o700)


def wheel_pin(path: Path) -> str:
    """从 wheel 文件名解析 name==version（文件名格式 name-version-pytag-abitag-platform.whl）。"""
    parts = path.name.split("-")
    return f"{parts[0].replace('_', '-')}=={parts[1]}"


def ensure_venv(target: Path, wheel: Path, offline: bool = False, seed_dirs: list[Path] | None = None) -> None:
    """确保 target venv 存在并装好 wheel（如 ability_scaffold）。

    就绪判据是 venv 内的可执行入口，不是 wheel 文件本身——wheel 随仓库存在，
    不能作为 venv 已建好的依据。
    """
    entry = target / "bin/ability-scaffold"
    if entry.exists():
        return
    uv = require_tool("uv")
    target.parent.mkdir(parents=True, exist_ok=True)
    if not (target / "bin/python").exists():
        # 用 uv 管理的 Python 3.12：wheelhouse 里的 Wheel 是 cp312 ABI，
        # 系统 python3 可能是 3.10，会导致 pip 找不到匹配 Wheel。
        run([uv, "venv", "--seed", "--python", "3.12", *(["--offline"] if offline else []), target])
    pip = [str(target / "bin/python"), "-m", "pip"]
    if not offline:
        # 升级 pip 需要联网；离线时用 venv 自带 pip 直接装本地 Wheel。
        run([*pip, "install", "-U", "pip"])
    install = [*pip, "install", str(wheel)]
    # scaffold 依赖 pyyaml 等传递依赖，离线时需从种子目录解析。
    for seed in seed_dirs or []:
        install += ["--find-links", str(seed)]
    if offline:
        install.append("--no-index")
    run(install)


def extract_seed_from_zip(zip_path: Path, seed_dir: Path) -> None:
    """把已有交付包/构建缓存里的 Wheel 解出来当离线种子（按文件名匹配，不改变解析结果）。

    兼容两种包内布局：交付组件包用 `robot/wheels/`，产物 3 的 component-build
    缓存用 `wheels/`。只认前者会导致产物 3 已下载的 82 个依赖在产物 5 里重下一遍。
    """
    if seed_dir.is_dir() and any(seed_dir.glob("*.whl")):
        return
    if not zip_path.is_file():
        return
    print(f"从已有包提取 Wheel 种子: {zip_path}", flush=True)
    seed_dir.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(zip_path) as archive:
        for name in archive.namelist():
            if not name.endswith(".whl"):
                continue
            if not (name.startswith("robot/wheels/") or name.startswith("wheels/")):
                continue
            archive.extract(name, seed_dir)
            shutil.move(seed_dir / name, seed_dir / Path(name).name)
    for leftover in ("robot", "wheels"):
        shutil.rmtree(seed_dir / leftover, ignore_errors=True)


def discover_local_seeds(framework: Path) -> list[Path]:
    """探测本机可复用的 Wheel 来源，避免重复下载 GB 级依赖。

    覆盖三类：产物 3（semantic build）的 component-build 缓存、共享 wheelhouse
    （/data/wheelhouse 或 SEMANTIC_WHEELHOUSE 指定）、以及已解出的种子目录。
    """
    seeds: list[Path] = []
    # 产物 3 的构建缓存（zip，含完整依赖闭包）→ 解出到临时种子目录
    cache_dir = framework / ".output/cache/component-build"
    if cache_dir.is_dir():
        for archive in sorted(cache_dir.glob("*.zip")):
            extracted = framework / ".output/franka-bundle-build/seed-from-component-cache"
            extract_seed_from_zip(archive, extracted)
            if any(extracted.glob("*.whl")):
                seeds.append(extracted)
    # 共享 wheelhouse：SEMANTIC_WHEELHOUSE 显式指定时只用它（便于隔离测试与
    # 自定义部署）；未设置时回退到约定路径 /data/wheelhouse。
    env_wh = os.environ.get("SEMANTIC_WHEELHOUSE")
    candidates = [env_wh] if env_wh is not None else ["/data/wheelhouse"]
    for base in candidates:
        if not base:
            continue
        root = Path(base)
        if root.is_dir():
            if any(root.glob("*.whl")):
                seeds.append(root)
            for sub in sorted(root.iterdir()):
                if sub.is_dir() and any(sub.glob("*.whl")):
                    seeds.append(sub)
    return seeds


def _curl_wheel_dir() -> Path:
    """定位共享的 curl Wheel 下载器（semantic-framework/scripts）。

    与产物 1/3 使用同一份实现，避免各仓各维护一套下载逻辑。
    """
    semantic = os.environ.get("SEMANTIC", "").strip()
    candidates = []
    if semantic:
        candidates.append(Path(semantic) / "semantic-framework/scripts")
    for parent in [WORKSPACE_DEFAULT, *WORKSPACE_DEFAULT.parents]:
        candidates.append(parent / "semantic-framework/scripts")
    for candidate in candidates:
        if (candidate / "curl_wheel.py").is_file():
            return candidate
    raise FileNotFoundError(
        "找不到 semantic-framework/scripts/curl_wheel.py；"
        "请把各仓库放在同一工作区下，或 export SEMANTIC=<工作区根目录>"
    )


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--workspace", type=Path, default=WORKSPACE_DEFAULT,
                        help="工作区根目录（默认：本脚本所在仓库的上级目录）")
    parser.add_argument("--output", type=Path, default=None,
                        help="输出组件 ZIP（默认：<framework>/.output/packages/libero-current/franka-libero-robot.zip）")
    parser.add_argument("--wheel-seed", type=Path, action="append", default=[],
                        help="离线 Wheel 种子目录，可重复；缺省时自动尝试从旧交付包提取")
    parser.add_argument("--offline", action="store_true",
                        help="pip wheel 加 --no-index，仅使用种子/本地缓存（首次无缓存时会失败）")
    parser.add_argument("--skip-go-build", action="store_true", help="跳过 Go 二进制编译（增量调试用）")
    parser.add_argument("--clean", action="store_true", help="先清空构建根目录再构建")
    args = parser.parse_args()

    workspace = args.workspace.resolve()
    framework = require_dir(workspace / "semantic-framework", "semantic-framework 仓库")
    franka = require_dir(workspace / "semantic-ability/franka-ability", "franka-ability 仓库")
    vendor = require_dir(workspace / "semantic-ability/ability-runtime", "ability-runtime 仓库")
    skills = require_dir(workspace / "semantic-skill/robot-skill", "robot-skill 仓库")
    sdk = require_dir(workspace / "semantic-robotsdk/robot-sdk", "robot-sdk 仓库")
    require_dir(DEPLOYMENT / "type-packages/franka-libero", "franka-libero 类型包")

    output = (args.output or framework / ".output/packages/libero-current/franka-libero-robot.zip").resolve()
    build_root = framework / ".output/franka-bundle-build"
    wheels = build_root / "build/wheels"
    bundle_dir = build_root / "robot-bundles/franka-libero-0.1.0"

    if args.clean and build_root.is_dir():
        print(f"清空构建根: {build_root}", flush=True)
        make_writable(build_root)
        shutil.rmtree(build_root)

    go = require_tool("go")
    uv = require_tool("uv")
    ability_framework = require_file(vendor / "AbilityFramework", "AbilityFramework 二进制", min_bytes=1 << 20)
    ability_py = require_file(vendor / "ability_py-0.4.0-py3-none-any.whl", "ability_py Wheel")
    scaffold_wheel = require_file(vendor / "ability_scaffold-1.2.0-py3-none-any.whl", "ability_scaffold Wheel")

    print(f"workspace = {workspace}")
    print(f"build     = {build_root}")
    print(f"output    = {output}")

    total_steps = 7
    try:
        banner(1, total_steps, "编译 Go 二进制")
        if args.skip_go_build:
            print("已按参数跳过", flush=True)
        else:
            (build_root / "bin").mkdir(parents=True, exist_ok=True)
            for name in ("semantic-robot-bundle", "semantic-robot-instance"):
                run([go, "build", "-trimpath", "-o", build_root / "bin" / name, f"./cmd/{name}"], cwd=DEPLOYMENT)
            run([go, "build", "-trimpath", "-o", build_root / "bin/semantic-pilot", "./cmd/semantic-pilot"], cwd=framework)

        banner(2, total_steps, "准备 scaffold venv")
        uv_offline = ["--offline"] if args.offline else []
        # 探测本地 Wheel 种子：依赖闭包（torch/nvidia 等 GB 级）优先从共享
        # wheelhouse 解析，避免重复下载。
        seed_dirs = [p.resolve() for p in args.wheel_seed]
        auto_seed = build_root / "seed-cache"
        extract_seed_from_zip(output, auto_seed)
        if any(auto_seed.glob("*.whl")):
            seed_dirs.append(auto_seed)
        for extra in discover_local_seeds(framework):
            if extra not in seed_dirs:
                seed_dirs.append(extra)
                print(f"自动复用本地缓存: {extra}", flush=True)
        # deploy_franka_libero.py assemble 需要 franka-ability/.venv/bin/python 作为
        # Bundle 的解释器来源；但不需要装 torch 等运行依赖（uv build/export 只读
        # pyproject 与 uv.lock），因此建一个空 venv 即可，避免 GB 级重复下载。
        franka_python = franka / ".venv/bin/python"
        if not franka_python.exists():
            run([uv, "venv", "--seed", "--python", "3.12", *uv_offline, franka / ".venv"])
        ensure_venv(vendor / ".venv", scaffold_wheel, offline=args.offline, seed_dirs=seed_dirs)

        banner(3, total_steps, "构建产品 Wheel（SDK 与 Skill SDK）")
        wheels.mkdir(parents=True, exist_ok=True)
        product_sources = [
            franka,                      # semantic_franka_abilities（Ability 实现，装入 Bundle 共享 venv）
            sdk / "packages/core",       # semantic_robot_sdk_core
            sdk / "packages/franka",     # semantic_robot_sdk_franka
            skills,                      # semantic_robot_skill_sdk
        ]
        for source in product_sources:
            run([uv, "build", "--wheel", "--python", "3.12", "--out-dir", wheels, *uv_offline, source])

        banner(4, total_steps, "生成锁定 requirements.txt（franka uv.lock 闭包）")
        export = subprocess.run(
            [uv, "export", "--project", str(franka), "--frozen", "--no-dev",
             "--no-emit-project", "--no-hashes", "--extra", "smolvla"],
            capture_output=True, text=True, check=True,
        )
        pins = {line.strip() for line in export.stdout.splitlines()
                if re.match(r"^[A-Za-z0-9][A-Za-z0-9._-]*==", line.strip())}
        # 本地路径依赖（sdk/ability_py）在 export 里是路径行，补成显式版本钉，
        # pip 会从 find-links（产品 Wheel / vendor）解析它们。
        pins.add(wheel_pin(ability_py))
        for wheel in wheels.glob("semantic_robot_sdk_*.whl"):
            pins.add(wheel_pin(wheel))
        requirements = build_root / "build/requirements.txt"
        requirements.parent.mkdir(parents=True, exist_ok=True)
        requirements.write_text("\n".join(sorted(pins)) + "\n", encoding="utf-8")
        print(f"共 {len(pins)} 条锁定依赖 → {requirements}", flush=True)

        banner(5, total_steps, "收集依赖 Wheel（种子优先，未命中走镜像下载）")
        builder = build_root / "build/builder"
        if not (builder / "bin/python").exists():
            run([uv, "venv", "--seed", "--python", "3.12", *uv_offline, builder])
        pip_env = None
        index = os.environ.get("UV_DEFAULT_INDEX") or os.environ.get("PIP_INDEX_URL")
        if index:
            pip_env = {**os.environ, "PIP_INDEX_URL": index}
        # 先用 curl 下载 PyPI 上有 wheel 的依赖：实测 pip 在 GB 级文件上会连接僵死
        # （317 MB 的 cublas 卡 25 分钟），curl 同文件 28 秒完成。剩余少数（源码包、
        # 本地产品 wheel）交给 pip 兜底。
        if not args.offline:
            try:
                sys.path.insert(0, str(_curl_wheel_dir()))
                from curl_wheel import collect as curl_collect
                curl_collect(requirements, wheels, index or "https://mirrors.aliyun.com/pypi/simple",
                             seed_dirs, python="3.12")
            except Exception as error:  # 下载器不可用时退回纯 pip
                print(f"curl 下载器不可用（{error}），改用 pip 收集", flush=True)
        pip = [str(builder / "bin/python"), "-m", "pip", "wheel"]
        for seed in seed_dirs:
            pip += ["--find-links", str(seed)]
        pip += ["--find-links", str(wheels), "--find-links", str(vendor),
                "-r", str(requirements), "--wheel-dir", str(wheels)]
        if args.offline:
            pip.append("--no-index")
        else:
            # 抗僵死：大 Wheel（torch/nvidia）在镜像上偶发连接挂起，pip 默认无限等。
            pip += ["--timeout", "60", "--retries", "5"]
        run(pip, env=pip_env)
        # pip 会从 --find-links 把另一个平台标签的同版本 Wheel 也拷进 --wheel-dir，
        # 导致 Bundle 组装时 pip 报 ResolutionImpossible；收集后统一去重。
        try:
            sys.path.insert(0, str(_curl_wheel_dir()))
            from curl_wheel import deduplicate
            deduplicate(wheels, "3.12", progress=lambda m: print(m, flush=True))
        except Exception as error:
            print(f"[warn] Wheel 去重跳过（{error}）", flush=True)
        collected = len(list(wheels.glob("*.whl")))
        print(f"build/wheels 共 {collected} 个 Wheel", flush=True)

        banner(6, total_steps, "组装 Bundle（deploy_franka_libero.py assemble）")
        if bundle_dir.exists():
            make_writable(bundle_dir)
            shutil.rmtree(bundle_dir)
        run([sys.executable, DEPLOYMENT / "scripts/deploy_franka_libero.py",
             "assemble", "--workspace", workspace, "--root", build_root])

        banner(7, total_steps, "导出组件包并校验")
        output.parent.mkdir(parents=True, exist_ok=True)
        run([build_root / "bin/semantic-robot-bundle", "export",
             "--bundle", bundle_dir, "--python-version", "3.12", "--output", output])
        with zipfile.ZipFile(output) as archive:
            names = archive.namelist()
            manifest = archive.read("semantic-component.yaml").decode("utf-8")
            wheel_count = sum(1 for n in names if re.match(r"robot/wheels/.*\.whl$", n))
        if "kind: robot_base" not in manifest or "name: franka-libero" not in manifest:
            raise RefreshError(f"导出包清单异常:\n{manifest}")
        digest = hashlib.sha256(output.read_bytes()).hexdigest()
        size_gb = output.stat().st_size / (1 << 30)
        print(f"\n✅ 完成: {output}")
        print(f"   {size_gb:.2f} GiB | {wheel_count} 个 Wheel | sha256={digest}")
        print(f"   安装: semantic install {output} --project <项目ID>")
        return 0
    except subprocess.CalledProcessError as error:
        print(f"\n✗ 步骤失败（exit {error.returncode}），修复后重跑本脚本即可续跑。", file=sys.stderr)
        return 1
    except RefreshError as error:
        print(f"\n✗ {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
