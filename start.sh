#!/bin/bash
# vmops 一键启动脚本：后端 + noVNC/websockify
# 用法: ./start.sh                      # 前台启动（Ctrl+C 停止），只起 MySQL
#       ./start.sh --service            # 用 systemd-run 后台启动（推荐无人值守）
#       ./start.sh --with-monitoring    # 同时启动 Prometheus/Alertmanager/VictoriaLogs/Vector
#       ./start.sh --stop               # 停止后端与 noVNC（容器保持当前状态）
set -e

cd "$(dirname "$0")"

WITH_MONITORING=0
for arg in "$@"; do
  [ "$arg" = "--with-monitoring" ] && WITH_MONITORING=1
done

NOVNC_PORT="${NOVNC_PORT:-6080}"
BACKEND="${VMOPS_BACKEND:-http://127.0.0.1:8080}"

start_infra() {
  # 依赖容器按需启动：compose 里 restart=unless-stopped，一旦被手动 stop，
  # 开机/Docker 重启都不会自动拉起（这正是「只在启动项目时才起」的语义），
  # 这里在启动项目时恢复。已运行的容器 docker start 是幂等的。
  #
  # 默认只起 MySQL（平台硬依赖）；监控/日志栈较吃内存，用 --with-monitoring 显式开启。
  local containers="vmops-mysql"
  if [ "$WITH_MONITORING" = "1" ]; then
    containers="$containers vmops-prometheus vmops-alertmanager vmops-victoria-logs vmops-vector"
  fi
  local missing=""
  for c in $containers; do
    if docker inspect "$c" >/dev/null 2>&1; then
      docker start "$c" >/dev/null 2>&1 || true
    else
      missing="$missing $c"
    fi
  done
  [ -n "$missing" ] && echo "   未创建的容器:$missing（首次部署请先执行 docker compose up -d）"
  return 0
}

start_backend() {
  echo "▶ 启动 vmops 后端 (:8080)..."
  start_infra
  # 原生直跑也加载 .env（METRICS_TOKEN/ALERT_WEBHOOK_TOKEN 等此前只在 compose 形态生效）
  if [ -f .env ]; then set -a; . ./.env; set +a; fi
  # 本地开发默认 debug 模式（config 默认已改为 release；release 要求强随机 JWT_SECRET_KEY 才能启动）
  export SERVER_MODE="${SERVER_MODE:-debug}"
  # 等 MySQL 就绪（docker start 后 InnoDB 初始化需数秒，抢跑会导致 vmops 连库失败退出、8080 无响应）
  if docker ps --format '{{.Names}}' 2>/dev/null | grep -q '^vmops-mysql$'; then
    # 口令只从 .env 读取，不再内置弱口令兜底（AGENTS.md 禁止硬编码口令）；
    # 未配置时用容器内免密 ping，失败也只是等满 15s 后继续。
    PING=(docker exec vmops-mysql mysqladmin ping --silent)
    [ -n "${DB_PASSWORD:-}" ] && PING=(docker exec vmops-mysql mysqladmin ping -uvmops -p"$DB_PASSWORD" --silent)
    for i in $(seq 1 15); do
      "${PING[@]}" 2>/dev/null && break
      [ "$i" = 1 ] && echo "   等待 MySQL 就绪…"
      sleep 1
    done
  fi
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