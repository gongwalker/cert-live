#!/usr/bin/env bash
# 后台启动 cert-live：编译 → 启动 → 记录 PID → 写日志
set -e

cd "$(dirname "$0")/.."

APP="cert-live"
PID_FILE=".run/${APP}.pid"
LOG_FILE="logs/app.log"

# 已在运行则跳过
if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
  echo "已在运行, pid=$(cat "$PID_FILE")"
  exit 0
fi

echo "编译中..."
go build -o "$APP" .

mkdir -p .run logs
nohup "./$APP" > "$LOG_FILE" 2>&1 &
echo $! > "$PID_FILE"

sleep 0.5
echo "已启动, pid=$(cat "$PID_FILE")"
echo "日志: $LOG_FILE"
grep -E "启动于" "$LOG_FILE" | tail -1 || true