#!/usr/bin/env bash
# 重启 = 先停再启
set -e
cd "$(dirname "$0")/.."
bash scripts/stop.sh || true
bash scripts/start.sh