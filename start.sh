#!/bin/bash
# vmops 一键启动脚本：后端 + noVNC/websockify
# 用法: ./start.sh            # 前台启动（Ctrl+C 停止）
#       ./start.sh --service  # 用 systemd-run 后台启动（推荐无人值守）
set -e

cd "$(dirname "$0")"

NOVNC_PORT="${NOVNC_PORT:-6080}"
BACKEND="${VMOPS_BACKEND:-http://127.0.0.1:8080}"

start_backend() {
  echo "▶ 启动 vmops 后端 (:8080)..."
  ./vmops > vmops.log 2>&1 &
  echo $! > vmops.pid
  echo "   PID: $(cat vmops.pid)"
}

start_novnc() {
  echo "▶ 启动 noVNC/websockify (:$NOVNC_PORT)..."
  websockify --web /usr/share/novnc \
    --token-plugin JSONTokenApi \
    --token-source "$BACKEND/api/vnc/token/%s" \
    "$NOVNC_PORT" > novnc.log 2>&1 &
  echo $! > novnc.pid
  echo "   PID: $(cat novnc.pid)"
}

stop_all() {
  echo "■ 停止服务..."
  [ -f vmops.pid ] && kill "$(cat vmops.pid)" 2>/dev/null || true
  [ -f novnc.pid ] && kill "$(cat novnc.pid)" 2>/dev/null || true
  rm -f vmops.pid novnc.pid
  echo "   已停止"
}

if [ "$1" = "--stop" ]; then
  stop_all
  exit 0
fi

if [ "$1" = "--service" ]; then
  echo "▶ 使用 systemd-run 启动（无人值守）..."
  sudo systemctl stop vmops 2>/dev/null || true
  sudo systemctl reset-failed vmops 2>/dev/null || true
  sudo systemd-run --unit=vmops --working-directory="$(pwd)" "$(pwd)/vmops"
  sudo systemctl stop novnc 2>/dev/null || true
  sudo systemctl reset-failed novnc 2>/dev/null || true
  sudo systemd-run --unit=novnc websockify --web /usr/share/novnc \
    --token-plugin JSONTokenApi \
    --token-source "$BACKEND/api/vnc/token/%s" \
    "$NOVNC_PORT"
  sleep 2
  echo "▶ 服务状态:"
  ss -tln | grep -E ":8080|:$NOVNC_PORT" || true
  exit 0
fi

# 前台模式：清理旧进程
[ -f vmops.pid ] && kill "$(cat vmops.pid)" 2>/dev/null || true
[ -f novnc.pid ] && kill "$(cat novnc.pid)" 2>/dev/null || true
start_backend
sleep 1
start_novnc

trap stop_all EXIT
echo ""
echo "✅ vmops 已就绪："
echo "   平台   http://localhost:8080"
echo "   noVNC  http://localhost:$NOVNC_PORT/vnc.html"
echo "   Ctrl+C 停止服务"
wait