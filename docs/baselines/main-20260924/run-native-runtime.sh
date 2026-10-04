#!/usr/bin/env bash
set -euo pipefail
ROOT=/home/amax/dyc/semantic-isaac
export PYTHONPATH="$ROOT/semantic-simulation/isaac-runtime/src"
export PYTHONNOUSERSITE=1
exec >> "$ROOT/.integration/managed-atomic/runtime.log" 2>&1
export CUDA_VISIBLE_DEVICES="${SEMANTIC_BEHAVIOR_GPU:-1}"
export OMNIGIBSON_HEADLESS=1
export OMNI_KIT_ACCEPT_EULA=YES
export OMNIGIBSON_DATA_PATH=/home/amax/dyc/BEHAVIOR-1K/datasets
export LD_LIBRARY_PATH="/home/amax/.conda/envs/behavior/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
exec "$ROOT/.integration/native-env/bin/python" -m semantic_isaac_runtime --data-root "$OMNIGIBSON_DATA_PATH" --host 127.0.0.1 --port 18100 --viewer --observation-mode development_full "$@"
