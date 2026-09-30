#!/bin/bash
# ============================================================================
# 鸢航 VirtKite 一键安装脚本（小白向：从零到首次登录，一条命令）
# 用法: bash install.sh
# 做什么:
#   1) 预检（Docker / libvirtd / 端口占用）
#   2) 生成 .env（强随机口令；已存在则复用，绝不覆盖）
#   3) docker compose 拉起基础设施（MySQL / Prometheus / Grafana / Alertmanager）
#   4) 编译并启动 vmops 后端（原生直跑，含 SSH 跳板 :2222）
#   5) 打印访问地址与首次登录口令
# ============================================================================

set -euo pipefail
cd "$(dirname "$0")"

RED='\033[31m'; GREEN='\033[32m'; YELLOW='\033[33m'; CYAN='\033[36m'; OFF='\033[0m'
info()  { echo -e "${CYAN}▶${OFF} $1"; }
ok()    { echo -e "${GREEN}✔${OFF} $1"; }
warn()  { echo -e "${YELLOW}⚠${OFF} $1"; }
fatal() { echo -e "${RED}✗ $1${OFF}"; exit 1; }

# ── 1. 预检 ────────────────────────────────────────────────────────────────
info "预检环境…"
command -v docker >/dev/null || fatal "未安装 Docker。请先安装：curl -fsSL https://get.docker.com | bash"
docker info >/dev/null 2>&1 || fatal "Docker 未运行。请先启动：sudo systemctl start docker"
command -v go >/dev/null || fatal "未安装 Go（>=1.25）。请先安装：https://go.dev/dl/"
ok "Docker / Go 就绪"

for port in 8080 2222; do
  if ss -ltn 2>/dev/null | grep -q ":$port "; then
    fatal "端口 $port 已被占用（可能是 vmops 已在运行）。如需重装请先停掉占用进程。"
  fi
done
ok "端口 8080 / 2222 空闲"

# libvirtd 检查（KVM 虚拟化的前提；缺失只警告不阻断——纯容器/Docker 功能仍可用）
systemctl is-active --quiet libvirtd 2>/dev/null && ok "libvirtd 运行中" \
  || warn "libvirtd 未运行：虚拟机功能不可用（Docker/监控功能不受影响）。安装：sudo apt install qemu-kvm libvirt-daemon-system && sudo systemctl start libvirtd"

# ── 2. 生成 .env（已存在则复用，绝不覆盖）────────────────────────────────
if [ -f .env ]; then
  ok ".env 已存在，复用现有配置（不覆盖任何口令）"
else
  info "生成 .env（全部强随机口令，权限 600）…"
  GEN=$(openssl rand -hex 24)
  cat > .env <<EOF
# 由 install.sh 自动生成于 $(date '+%F %T')——每个口令独立随机，切勿复用/入库
DB_PASSWORD=$(openssl rand -hex 24)
MYSQL_ROOT_PASSWORD=$(openssl rand -hex 24)
JWT_SECRET_KEY=$(openssl rand -hex 32)
CREDENTIAL_MASTER_KEY=$(openssl rand -hex 32)
GRAFANA_ADMIN_PASSWORD=$(openssl rand -base64 18)
METRICS_TOKEN=$(openssl rand -hex 24)
ADMIN_INITIAL_PASSWORD=$(openssl rand -base64 15 | tr -d '/+=' | cut -c1-14)
VIEWER_INITIAL_PASSWORD=$(openssl rand -base64 15 | tr -d '/+=' | cut -c1-14)
SERVER_MODE=release
JUMPD_ENABLED=1
JUMPD_PORT=2222
EOF
  chmod 600 .env
  ok ".env 已生成"
fi

# ── 3. 基础设施容器（compose 按 .env 注入口令；已存在则 start 免重建）──────
info "拉起基础设施容器（MySQL / Prometheus / Grafana / Alertmanager）…"
for c in vmops-mysql vmops-prometheus vmops-grafana vmops-alertmanager; do
  if docker ps -a --format '{{.Names}}' | grep -qx "$c"; then
    docker start "$c" >/dev/null
  fi
done
if ! docker ps --format '{{.Names}}' | grep -q '^vmops-mysql$'; then
  docker compose up -d mysql prometheus alertmanager grafana
fi
ok "容器已就绪"

# 等 MySQL 可接受连接（compose 侧健康检查 + 兜底轮询）
info "等待 MySQL 就绪…"
for i in $(seq 1 30); do
  docker exec vmops-mysql mysqladmin ping -uvmops -p"$(grep '^DB_PASSWORD=' .env | cut -d= -f2)" --silent >/dev/null 2>&1 && break
  [ "$i" = 30 ] && fatal "MySQL 30 秒未就绪，请 docker logs vmops-mysql 排查"
  sleep 1
done
ok "MySQL 就绪"

# ── 4. 编译并启动后端（release 模式：强口令校验已由 .env 满足）─────────────
info "编译后端（首次约 1 分钟）…"
go build -o vmops . || fatal "编译失败"
pkill -f '^\./vmops$' 2>/dev/null || true; sleep 1
info "启动 vmops（跳板 :2222 已随 JUMPD_ENABLED=1 开启）…"
set -a; . ./.env; set +a
nohup ./vmops > vmops.log 2>&1 &
echo $! > vmops.pid
sleep 3
ps -p "$(cat vmops.pid)" >/dev/null || fatal "后端启动失败，请查看 vmops.log"

# ── 5. 收尾输出 ────────────────────────────────────────────────────────────
ADMIN_PASS=$(grep '^ADMIN_INITIAL_PASSWORD=' .env | cut -d= -f2)
HOST_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
echo
echo -e "${GREEN}════════════════════════════════════════════════${OFF}"
echo -e "${GREEN}  🪁 鸢航 VirtKite 安装完成！${OFF}"
echo -e "${GREEN}════════════════════════════════════════════════${OFF}"
echo "  访问地址   : http://${HOST_IP:-127.0.0.1}:8080"
echo "  管理员账号 : admin"
echo "  初始口令   : ${ADMIN_PASS}（首次登录后请立即修改）"
echo "  SSH 跳板   : ssh admin@${HOST_IP:-127.0.0.1} -p 2222"
echo
echo "  下一步     : 浏览器登录 → 右上角头像改密 → 创建第一台虚拟机"
echo "  排障       : tail -f vmops.log ｜ docker logs vmops-mysql"
echo -e "${GREEN}════════════════════════════════════════════════${OFF}"
