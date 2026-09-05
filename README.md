# vmops · 轻量级私有云管理平台

基于 **KVM 虚拟化**的轻量级私有云管理平台的设计与实现。以 Go 构建后端 API，Vue3 构建管理前端，实现虚拟机全生命周期、镜像模板、硬件热管理、操作审计与监控的统一管理。

对标 virt-manager 核心功能（创建向导/硬件管理/控制台/存储池/网络/快照），辅以 PVE 式增量克隆与 cloud-init 快速初始化。

## 技术栈

| 层 | 技术 |
|----|------|
| 后端 | Go 1.25 + gin + GORM + golang-jwt + bcrypt |
| 数据库 | MySQL 8（Docker 部署） |
| 虚拟化 | libvirt / KVM（`digitalocean/go-libvirt` 纯 Go RPC 直连，无 CGO） |
| 前端 | Vue 3 + Vite + Element Plus + vue-router + ECharts |
| 监控 | 内建 Prometheus exporter + Prometheus + Grafana + Alertmanager |
| 部署 | 二进制直跑 / Docker / docker-compose |

## 功能清单

- [x] 基础框架（配置 / 数据库 / 中间件 / 启动收敛）
- [x] 认证与授权（JWT + bcrypt + admin/viewer 角色 + 修改密码）
- [x] RBAC 第一阶段（viewer 只读运维：读全放 + 控制台，变更一律 403，前端按钮级隐藏）
- [x] 宿主机管理（纳管 / 连通性测试回写状态 / /proc 实时状态 + 中文时长）
- [x] 虚拟机生命周期（卡片列表 / 详情 / 真实 KVM 建机 / 启停重启 / 暂停恢复 / 删除带存储清理）
- [x] **异步任务系统**（创建/删除/克隆/优雅关机走后台 worker，202 + 轮询，任务中心可见）
- [x] **创建向导**（ISO / 导入磁盘 / 云镜像+cloud-init / 克隆四模式 + 汇总页）
- [x] **硬件热管理**（磁盘/网卡热插拔、调核/调内存、自启、引导顺序、XML 双通道编辑）
- [x] **增量克隆**（PVE 式 linked clone，子卷带 backing file，保护基镜像）
- [x] **cloud-init**（纯 Go 生成 seed ISO，用户/密码/SSH key/静态 IP）
- [x] 存储池管理（池/卷 CRUD + 卷列表刷新修复 + 建盘多池选择）
- [x] 网络管理（CRUD / 启停 / XML 编辑 / DHCP 范围 / NAT 模板）
- [x] 网页控制台（三入口：VNC 图形 / SSH 终端 / 免 IP 串口；页内一键开机闭环；SSH 参数记忆）
- [x] **控制台会话跟踪**（谁连了哪台 VM，SSH/串口可服务端强制断开）
- [x] 快照管理（名称+描述 / 列表含时间状态 / 删除 / 回滚）
- [x] 镜像管理（上传到池 / 模板标记 / 基于模板 linked clone 建机）
- [x] 审计日志（中间件自动写入 + 用户名回填 + 查询 / 详情 / 操作类型分布）
- [x] 仪表盘（总览计数 + 状态分布 + 宿主机实时大盘 + VM 实时表）
- [x] VM 列表实时化（卡片 + CPU/内存迷你折线 + 搜索筛选 + 批量操作）
- [x] 任务中心 / 会话管理 / 系统设置页（生效配置快照 + 轮询偏好）
- [x] **Prometheus 监控**（内建 `/metrics`：VM/宿主机/存储池/任务指标 + 5 告警规则 + Grafana 9 面板）
- [x] 存量 VM 导入 / 纳管
- [x] E2E 回归脚本（`scripts/smoke.sh`，23 项断言）

## 环境要求

- Go 1.25+
- Node.js 18+（仅前端开发 / 构建需要）
- MySQL 8.0+（或 Docker）
- libvirt + KVM（运行虚拟机的宿主机）
- Prometheus / Grafana（可选，二进制或 Docker，见监控章节）

## 快速开始

### 1. 准备数据库

```bash
# 方式 A：Docker（推荐）
docker run -d --name vmops-mysql -p 3306:3306 --restart unless-stopped \
  -e MYSQL_ROOT_PASSWORD=root123 \
  -e MYSQL_DATABASE=vmops \
  -e MYSQL_USER=vmops \
  -e MYSQL_PASSWORD=vmops123 \
  mysql:8.0.36

# 方式 B：本地 MySQL，执行初始化脚本（建库 + 建用户）
mysql -u root -p < scripts/init-db.sql
```

### 2. 后端

```bash
cd ~/vmops
go mod tidy
# 编辑 .env 调整配置（仓库已含示例 .env，见下）
go build -o vmops .
./vmops                # 监听 http://localhost:8080
```

`.env` 关键配置：

```ini
DB_HOST=127.0.0.1
DB_PORT=3306
DB_USER=vmops
DB_PASSWORD=vmops123
DB_NAME=vmops
JWT_SECRET_KEY=vmops-jwt-secret-key-change-in-production
JWT_EXPIRE_MINUTES=1440
SERVER_PORT=8080
SERVER_MODE=debug
LIBVIRT_URI=qemu:///system
IMAGE_DIR=/var/lib/libvirt/images
SEED_DIR=/home/jiuzhao/vmops/data/seed
```

> 服务首次启动会自动 `AutoMigrate` 建表（含 tasks / console_sessions），写入种子账号，并收敛上次残留的任务与会话。

### 3. 前端（Vite 工程）

前端构建产物由后端直接托管，无需额外静态服务器。

```bash
cd ~/vmops/web
npm install
npm run build        # 产物输出到 web/dist，后端自动托管
```

开发模式（热更新，Vite 代理 `/api` 到 `:8080`，含 WebSocket 透传）：

```bash
cd ~/vmops/web
npm run dev          # 访问 http://localhost:5173
```

### 4. 监控栈（可选）

```bash
# 方式 A：二进制直跑（推荐本机）
# Prometheus + Alertmanager + Grafana 二进制见 ~/monitor/（README 有启停命令）
# 看板：http://127.0.0.1:3000/d/vmops-overview（admin/admin）

# 方式 B：docker-compose 一键栈
docker compose up -d prometheus grafana alertmanager
# Prometheus :9090，Grafana :3000，Alertmanager :9093
```

### 5. 测试账号

| 账号 | 密码 | 角色 |
|------|------|------|
| `admin` | `password` | 管理员（全部权限） |
| `user` | `123456` | 只读运维（查看 + 控制台，变更 403） |

### 6. 回归验证

```bash
./scripts/smoke.sh   # 23 项：只读接口 + metrics + 创建/删除 task 全链路 + 硬件管理
```

## API 接口

统一响应格式：`{"code":200,"message":"success","data":{...}}`；除登录与 `/metrics`、`/health` 外均需在
`Authorization: Bearer <token>` 头携带 JWT。耗时操作（创建/删除/克隆/停止）返回 `202 {"task_id"}`，轮询 `GET /api/tasks/:id` 至终态。

### 认证

- `POST /api/auth/login` — 登录
- `GET  /api/auth/me` — 当前用户信息
- `PUT  /api/users/me/password` — 修改密码（所有角色，需旧密码）

### 用户管理（admin）

- `GET /api/users` `POST /api/users` `PUT /api/users/:id` `DELETE /api/users/:id`

### 宿主机管理

- `GET /api/hosts` `POST /api/hosts` `PUT /api/hosts/:id` `DELETE /api/hosts/:id`
- `POST /api/hosts/:id/test` — 连通性测试（回写 reachable 状态）
- `GET  /api/hosts/:id/stats` — 宿主机状态（/proc 直读 + 中文时长）

### 虚拟机管理

- `GET /api/vms` — 列表（含 perf 实时聚合，一次请求渲染指标）
- `GET /api/vms/options` — 创建向导选项（池/网络/镜像/OS）
- `GET  /api/vms/:id` `POST /api/vms`（202 task） `DELETE /api/vms/:id`（202 task）
- `GET  /api/vms/import/scan` — 扫描未纳管存量 VM
- `POST /api/vms/import` — 勾选批量导入
- `POST /api/vms/:id/start` `POST /api/vms/:id/stop`（202 task） `POST /api/vms/:id/restart`
- `POST /api/vms/:id/pause` `POST /api/vms/:id/resume`
- `POST /api/vms/:id/clone`（202 task，linked clone）
- `GET  /api/vms/:id/spec` — 完整配置模型（含 raw_xml）
- `PUT  /api/vms/:id/spec` — 整体重定义（停机）
- `PUT  /api/vms/:id/cpu` `PUT /api/vms/:id/memory` `PUT /api/vms/:id/autostart` `PUT /api/vms/:id/boot`
- `POST /api/vms/:id/devices/disks` `DELETE /api/vms/:id/devices/disks/:target` — 磁盘热插拔
- `POST /api/vms/:id/devices/interfaces` `DELETE /api/vms/:id/devices/interfaces/:mac` — 网卡热插拔
- `GET  /api/vms/:id/stats` — 实时性能（CPU/内存/磁盘/网络）
- `GET  /api/vms/:id/xml` `PUT /api/vms/:id/xml` — XML 查看/编辑
- 快照：`GET /api/vms/:id/snapshots`（名称/描述/时间/状态） `POST /api/vms/:id/snapshots`（`{name, description}`） `DELETE /api/vms/:id/snapshots/:snap` `POST .../revert`
- `POST /api/vms/:id/vnc-token` — noVNC token（viewer 可用）
- `GET  /api/vms/:id/terminal` — Web 终端 WS（SSH 桥，`?token=` 鉴权）
- `GET  /api/vms/:id/serial` — 串口 WS（libvirt console 桥）

### 镜像管理

- `GET /api/images`（`?is_template=true` 模板筛选） `GET /api/images/:id`
- `POST /api/images/upload` — 上传到指定池（form：name/os_version/pool/file）
- `PUT /api/images/:id/template` — 标记模板
- `POST /api/images/:id/clone`（202 task，linked clone 建机）
- `DELETE /api/images/:id`

### 存储 / 网络

- 存储池：`GET /api/storage/pools` `GET /api/storage/pools/:name`（含卷） `POST /api/storage/pools` `DELETE /api/storage/pools/:name`；卷：`POST /api/storage/pools/:name/volumes` `DELETE .../volumes/:vol`
- 网络：`GET /api/networks` `GET /api/networks/:name`（含 XML/autostart/DHCP） `POST /api/networks` `POST /api/networks/xml` `PUT /api/networks/:name` `POST /api/networks/:name/start|stop` `DELETE /api/networks/:name`

### 任务 / 会话

- `GET /api/tasks` `GET /api/tasks/:id` `DELETE /api/tasks/:id`（仅 finished 可删）
- `GET /api/sessions` — 控制台会话（VNC/SSH/串口）
- `POST /api/sessions/:id/disconnect` — 强制断开（SSH/串口；VNC 中转无法强断）

### 仪表盘 / 审计 / 设置 / 监控

- `GET /api/dashboard/overview` `GET /api/dashboard/vm-status` `GET /api/dashboard/host-stats` `GET /api/dashboard/vm-perf`
- `GET /api/audit`（action/object_type/username/status/日期/分页） `GET /api/audit/:id` `GET /api/audit/summary` `GET /api/audit/actions`
- `GET /api/settings`（admin，生效配置快照）
- `GET /metrics` — Prometheus exposition（公开，生产请防火墙限制）
- `GET /api/health` — 健康检查

## 项目结构

```
vmops/
├── main.go              # 入口：路由/静态托管/任务管理器/会话注册表/种子数据/启动收敛
├── config/              # 环境变量配置（含 SEED_DIR）
├── database/            # GORM 连接与自动迁移
├── handler/             # HTTP 处理器（vm/spec/device/clone/stats/task/session/metrics/settings/…）
├── middleware/          # JWT / OperatorMiddleware(RBAC) / CORS / 审计中间件
├── model/               # GORM 模型（user/host/vm/image/audit/task/session）
├── service/
│   ├── virt/            # libvirt 封装（domain/spec/device/storage/network/snapshot/console/stats/clone/cloudinit）
│   ├── tasks/           # 异步任务队列（4 worker + 5 executors）
│   ├── console/         # 会话注册表（WS 持有/强制断开/VNC 映射/过期清扫）
│   ├── metrics/         # Prometheus 内建采集
│   └── vnc/             # VNC token 存储
├── scripts/             # init-db.sql / smoke.sh（E2E 回归）/ start-novnc.sh
├── deploy/              # prometheus.yml / alerts.yml / grafana 看板与 provisioning
├── web/                 # Vue3 + Vite 前端（14 页面：Dashboard/VmList/VmDetail/向导/Host/Image/Storage/Network/Task/Session/Settings/Audit/Console/Login）
│   └── dist/            # 构建产物，由后端托管
└── docs/                # 设计 / 开发文档（含 api-contract / task-contract）
```

## 文档

详见 [`docs/`](docs/README.md) 目录：

- [00-选题依据与开题.md](docs/00-选题依据与开题.md) — 选题背景、研究内容、技术路线、进度安排（开题）
- [01-相关技术基础.md](docs/01-相关技术基础.md) — KVM/libvirt、Go/gin/GORM、Vue3、技术选型
- [02-系统需求分析.md](docs/02-系统需求分析.md) — 角色权限、功能/非功能需求
- [03-系统总体设计.md](docs/03-系统总体设计.md) — 架构、模块划分、请求流转（含任务/会话/RBAC）
- [04-数据库设计.md](docs/04-数据库设计.md) — 七张核心表结构、ER 图
- [05-详细设计与实现.md](docs/05-详细设计与实现.md) — 逐模块实现 + 前端 + 监控
- [06-系统测试与验证.md](docs/06-系统测试与验证.md) — 测试环境、功能测试、真实 KVM 演示
- [07-部署与运维.md](docs/07-部署与运维.md) — 部署、监控栈、二进制直跑、E2E 回归
- [08-总结与展望.md](docs/08-总结与展望.md) — 工作总结、Alertmanager/混合云等后续工作
- [09-答辩演示脚本.md](docs/09-答辩演示脚本.md) — 演示流程、功能清单、FAQ
- [10-参考项目研究.md](docs/10-参考项目研究.md) — virt-manager / Cockpit / vmdashboard / KvmDash / JumpServer 调研笔记
- [api-contract.md](docs/api-contract.md) / [task-contract.md](docs/task-contract.md) — 后端契约（事实源）

## License

MIT
