#!/usr/bin/env bash
# noVNC + websockify 启动脚本
# websockify 通过 JSONTokenApi 向后端请求 token -> host:port 映射
# noVNC 页面由 websockify --web 托管（/usr/share/novnc）
set -e

BACKEND="${VMOPS_BACKEND:-http://127.0.0.1:8080}"
LISTEN_PORT="${NOVNC_PORT:-6080}"

echo "启动 websockify :$LISTEN_PORT (后端 $BACKEND)"
websockify --web /usr/share/novnc \
  --token-plugin JSONTokenApi \
  --token-source "$BACKEND/api/vnc/token/%s" \
  "$LISTEN_PORT"