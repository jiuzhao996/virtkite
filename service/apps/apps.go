// Package apps 应用商店内置应用目录：定义每个应用的「已装检测命令」与「安装脚本」，
// 由 service/tasks 的 app_install 任务经 SSH 在虚拟机内以 sh 执行（对应手工 `ssh vm 'sh -c ...'`）。
//
// 脚本统一约定（新增应用必须遵守，稳字当头）：
//   - 目标系统以 apt 系（Ubuntu/Debian）优先；
//   - `set -e`：任一步失败立即退出（退出码非 0 → 任务失败）；
//   - DEBIAN_FRONTEND=noninteractive：避免 apt 交互提示卡死无终端的 SSH 会话；
//   - 幂等：executor 安装前先跑 Detect（已装即跳过），脚本自身重复执行也安全；
//   - Detect 必须是单行命令：executor 会包一层 `if <Detect>; then ...` 判别（见 app_tasks.go）；
//   - 脚本正文不能包含反引号（Go raw string 字面量的硬限制）。
package apps

// App 应用目录条目。Detect/Install 刻意不带 json tag（默认不序列化），
// 目录接口（List）天然不泄露脚本正文；详情接口（Get）需要脚本预览时由 handler 显式输出。
type App struct {
	ID       string `json:"id"`       // 唯一标识：nginx / mysql / redis / php / nodejs / python3 / docker-engine / wordpress / portainer / lnmp
	Name     string `json:"name"`     // 展示名
	Desc     string `json:"desc"`     // 一句话描述
	Category string `json:"category"` // web / database / cache / runtime / cms / ops
	Detect   string `json:"-"`        // 已装检测命令（VM 内 sh 执行，退出码 0=已装），单行
	Install  string `json:"-"`        // 安装脚本（VM 内 sh 执行）
}

// catalog 内置应用目录（只读：All/Get 均不暴露可变引用给调用方修改）。
var catalog = []App{
	{
		ID:       "nginx",
		Name:     "Nginx",
		Desc:     "高性能 Web 服务器与反向代理，安装后自启并监听 80 端口",
		Category: "web",
		Detect:   `command -v nginx >/dev/null 2>&1`,
		Install: `# 安装 Nginx（Ubuntu/Debian apt 源），装后自启
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y nginx
systemctl enable --now nginx
nginx -v
`,
	},
	{
		ID:       "mysql",
		Name:     "MySQL Server",
		Desc:     "MySQL 数据库服务端，root 默认走 auth_socket 本地认证",
		Category: "database",
		Detect:   `command -v mysqld >/dev/null 2>&1 || command -v mysql >/dev/null 2>&1`,
		Install: `# 安装 MySQL Server（Ubuntu/Debian apt 源），装后自启
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y mysql-server
systemctl enable --now mysql
mysql --version
`,
	},
	{
		ID:       "redis",
		Name:     "Redis",
		Desc:     "内存键值数据库/缓存，默认监听 127.0.0.1:6379",
		Category: "cache",
		Detect:   `command -v redis-server >/dev/null 2>&1`,
		Install: `# 安装 Redis 服务端（Ubuntu/Debian apt 源），装后自启
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y redis-server
systemctl enable --now redis-server
redis-server --version
`,
	},
	{
		ID:       "php",
		Name:     "PHP (FPM)",
		Desc:     "PHP-FPM 运行环境（含 MySQL 扩展），配合 Nginx/Apache 的 FastCGI 使用",
		Category: "runtime",
		Detect:   `command -v php >/dev/null 2>&1`,
		Install: `# 安装 PHP-FPM + MySQL 扩展；顺带装 php-cli 提供 php 命令（便于检测与调试）
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y php-fpm php-mysql php-cli
# FPM 服务名带版本号（如 php8.3-fpm），动态探测逐个自启
for conf in /etc/init.d/php*-fpm; do
  [ -e "$conf" ] || continue
  systemctl enable --now "$(basename "$conf")"
done
php -v
`,
	},
	{
		ID:       "nodejs",
		Name:     "Node.js + npm",
		Desc:     "Node.js 运行时与 npm 包管理器（发行版仓库版本，稳定优先）",
		Category: "runtime",
		Detect:   `command -v node >/dev/null 2>&1`,
		Install: `# 安装 Node.js 与 npm（Ubuntu/Debian apt 源）
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y nodejs npm
node -v && npm -v
`,
	},
	{
		ID:       "python3",
		Name:     "Python 3",
		Desc:     "Python3 解释器与 pip 包管理器",
		Category: "runtime",
		Detect:   `command -v python3 >/dev/null 2>&1`,
		Install: `# 安装 Python3 与 pip（Ubuntu/Debian apt 源）
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y python3 python3-pip
python3 --version
`,
	},
	{
		ID:       "docker-engine",
		Name:     "Docker Engine",
		Desc:     "Docker 容器引擎（官方 get.docker.com 脚本安装，装后自启）",
		Category: "ops",
		Detect:   `command -v docker >/dev/null 2>&1`,
		Install: `# 安装 Docker Engine：官方脚本自动识别发行版与版本（官方脚本自身幂等）
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y curl ca-certificates
curl -fsSL https://get.docker.com | sh
systemctl enable --now docker
docker --version
`,
	},
	{
		ID:       "wordpress",
		Name:     "WordPress",
		Desc:     "博客/CMS（Docker Compose 版：WordPress + MySQL 8，宿主端口 8088；数据库口令安装时随机生成并回显一次）",
		Category: "cms",
		Detect:   `docker ps --format '{{.Names}}' | grep -q wordpress`,
		// 安全约定（新增应用请照此办理）：脚本里不得出现任何固定口令字面量——
		// 固定口令等于所有装了该应用的虚拟机共用一把众所周知的钥匙。
		// 这里在虚拟机内运行时随机生成，并回显到 stdout：
		// 任务结果只保留 stdout 尾部 1000 字（service/tasks/app_tasks.go: appOutTail），
		// 故回显必须放在脚本末尾；前端「安装输出」直接展示该字段（web/src/views/AppStore.vue）。
		Install: `# 安装 WordPress（Docker Compose 版）：wordpress + mysql:8 双服务，宿主端口 8088 映射容器 80
set -e
command -v docker >/dev/null 2>&1 || { echo "未检测到 docker，请先安装 docker-engine 应用"; exit 1; }
docker ps --format '{{.Names}}' | grep -q '^wordpress$' && exit 0
# 运行时随机生成数据库口令：优先 openssl，缺失时回落到 /dev/urandom。
# 只取字母数字，避免口令里的特殊字符破坏 YAML 与 URL 解析。
rand_pass() {
  openssl rand -hex 16 2>/dev/null || tr -dc 'A-Za-z0-9' < /dev/urandom 2>/dev/null | head -c 24
}
WP_DB_PASSWORD=$(rand_pass)
WP_ROOT_PASSWORD=$(rand_pass)
# 生成失败即中止：空口令会让数据库裸奔，宁可安装失败也不静默降级
if [ -z "$WP_DB_PASSWORD" ] || [ -z "$WP_ROOT_PASSWORD" ]; then
  echo "随机口令生成失败，已中止安装（请确认虚拟机内 openssl 或 /dev/urandom 可用）"
  exit 1
fi
mkdir -p /opt/wordpress
cat > /opt/wordpress/docker-compose.yml <<EOF
services:
  wordpress:
    image: wordpress:latest
    container_name: wordpress
    ports:
      - "8088:80"
    environment:
      WORDPRESS_DB_HOST: db
      WORDPRESS_DB_NAME: wordpress
      WORDPRESS_DB_USER: wordpress
      WORDPRESS_DB_PASSWORD: $WP_DB_PASSWORD
    restart: unless-stopped
  db:
    image: mysql:8
    container_name: wordpress-db
    environment:
      MYSQL_DATABASE: wordpress
      MYSQL_USER: wordpress
      MYSQL_PASSWORD: $WP_DB_PASSWORD
      MYSQL_ROOT_PASSWORD: $WP_ROOT_PASSWORD
    volumes:
      - db_data:/var/lib/mysql
    restart: unless-stopped
volumes:
  db_data:
EOF
cd /opt/wordpress
docker compose up -d 2>/dev/null || docker-compose up -d
# 凭据回显（必须留在 stdout 末尾：任务结果只保留尾部，前端安装输出展示该内容）
echo "WordPress 安装完成：http://<虚拟机IP>:8088"
echo "⚠️ 以下口令为本次随机生成，仅显示这一次，请立即保存（后续可查 /opt/wordpress/docker-compose.yml）："
echo "  数据库用户 wordpress 口令: $WP_DB_PASSWORD"
echo "  数据库 root 口令: $WP_ROOT_PASSWORD"
`,
	},
	{
		ID:       "portainer",
		Name:     "Portainer CE",
		Desc:     "Docker 图形化管理面板（Docker 版，HTTPS 端口 9443）",
		Category: "ops",
		Detect:   `docker ps -a --format '{{.Names}}' | grep -q '^portainer$'`,
		Install: `# 安装 Portainer CE（Docker 版）：挂载 docker.sock 管理本机容器，HTTPS 端口 9443
set -e
command -v docker >/dev/null 2>&1 || { echo "未检测到 docker，请先安装 docker-engine 应用"; exit 1; }
# 幂等：容器已存在（含停止态）时直接拉起
if docker ps -a --format '{{.Names}}' | grep -q '^portainer$'; then
  docker start portainer >/dev/null 2>&1 || true
  exit 0
fi
docker volume create portainer_data >/dev/null
docker run -d --name portainer --restart=unless-stopped \
  -p 9443:9443 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v portainer_data:/data \
  portainer/portainer-ce:latest
`,
	},
	{
		ID:       "lnmp",
		Name:     "LNMP 合集",
		Desc:     "Nginx + MySQL + PHP-FPM 三件套一键装齐（与单独安装等效）",
		Category: "web",
		Detect:   `command -v nginx >/dev/null 2>&1 && command -v mysql >/dev/null 2>&1 && command -v php >/dev/null 2>&1`,
		Install: `# 安装 LNMP 合集：nginx + MySQL + PHP-FPM 一次装齐（组件与单独安装一致）
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y nginx mysql-server php-fpm php-mysql php-cli
systemctl enable --now nginx mysql
# FPM 服务名带版本号（如 php8.3-fpm），动态探测逐个自启
for conf in /etc/init.d/php*-fpm; do
  [ -e "$conf" ] || continue
  systemctl enable --now "$(basename "$conf")"
done
nginx -v && mysql --version && php -v | head -n1
`,
	},
}

// All 返回完整应用目录（不含脚本正文：Detect/Install 无 json tag，JSON 序列化自动略去）。
// 返回浅拷贝，避免调用方改动包内目录。
func All() []App {
	out := make([]App, len(catalog))
	copy(out, catalog)
	return out
}

// Get 按 ID 查应用；不存在时 ok=false。
func Get(id string) (App, bool) {
	for _, app := range catalog {
		if app.ID == id {
			return app, true
		}
	}
	return App{}, false
}
