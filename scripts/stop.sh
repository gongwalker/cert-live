#!/usr/bin/env bash
# 停止后台运行的 cert-live
cd "$(dirname "$0")/.."

APP="cert-live"
PID_FILE=".run/${APP}.pid"

if [ ! -f "$PID_FILE" ]; then
  echo "未运行(无 PID 文件)"
  # 兜底：按进程名清理
  pkill -f "./$APP" 2>/dev/null && echo "已按名称清理残留进程" || true
  exit 0
fi

pid="$(cat "$PID_FILE")"
if kill -0 "$pid" 2>/dev/null; then
  kill "$pid"
  echo "已停止, pid=$pid"
else
  echo "进程不存在($pid), 清理 PID 文件"
fi
rm -f "$PID_FILE"