# vmops · 轻量级私有云管理平台

基于 **KVM 虚拟化**的轻量级私有云管理平台的设计与实现。以 Go 构建后端 API，Vue3 构建管理前端，实现对多宿主机、虚拟机全生命周期、镜像模板与操作审计的统一管理。

## 技术栈

| 层 | 技术 |
|----|------|
| 后端 | Go 1.21 + gin + GORM + golang-jwt + bcrypt |
| 数据库 | MySQL 8（Docker 部署） |
| 虚拟化 | libvirt / KVM（`virsh` + `qemu-img` 真正落地） |
| 前端 | Vue 3 + Vite + Element Plus + vue-router |
| 部署 | Docker / docker-compose |

## 当前进度

- [x] 基础框架（配置 / 数据库 / 中间件）
- [x] 认证与授权（JWT + bcrypt + admin/viewer 角色）
- [x] 宿主机管理（多节点纳管 / 连通性测试 / 状态）
- [x] 虚拟机生命周期（列表 / 详情 / **真实 KVM 建机** / 启停重启 / 删除）
- [x] 镜像管理（上传 / 列表 / 删除，目录穿越防护）
- [x] 审计日志（中间件自动写入 + 查询 / 详情 / 操作类型分布）
- [x] 仪表盘统计（总览计数 + 虚拟机状态分布）
- [x] 前端（Vite 标准工程，六大页面）
- [ ] Prometheus 监控集成（P2，规划中）

## 环境要求

- Go 1.21+
- Node.js 18+（仅前端开发 / 构建需要）
- MySQL 8.0+（或 Docker）
- libvirt + KVM（运行虚拟机的宿主机）

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
```

> 服务首次启动会自动 `AutoMigrate` 建表，并写入种子账号。

### 3. 前端（Vite 工程）

前端构建产物由后端直接托管，无需额外静态服务器。

```bash
cd ~/vmops/web
npm install
npm run build        # 产物输出到 web/dist，后端自动托管
```

开发模式（热更新，Vite 代理 `/api` 到 `:8080`）：

```bash
cd ~/vmops/web
npm run dev          # 访问 http://localhost:5173
```

### 4. 测试账号

| 账号 | 密码 | 角色 |
|------|------|------|
| `admin` | `password` | 管理员 |
| `user` | `123456` | 普通用户（viewer） |

## API 接口

统一响应格式：`{"code":200,"message":"success","data":{...}}`；除登录外均需在
`Authorization: Bearer <token>` 头携带 JWT。

### 认证

- `POST /api/auth/login` — 登录
- `GET  /api/auth/me` — 当前用户信息

### 用户管理（admin）

- `GET /api/users` `POST /api/users` `PUT /api/users/:id` `DELETE /api/users/:id`

### 宿主机管理（admin）

- `GET /api/hosts` `POST /api/hosts` `PUT /api/hosts/:id` `DELETE /api/hosts/:id`
- `POST /api/hosts/:id/test` — 连通性测试
- `GET  /api/hosts/:id/stats` — 宿主机状态

### 虚拟机管理（admin）

- `GET /api/vms` `GET /api/vms/:id` `POST /api/vms`
- `POST /api/vms/:id/start` `POST /api/vms/:id/stop` `POST /api/vms/:id/restart`
- `DELETE /api/vms/:id`

### 镜像管理（admin）

- `GET /api/images` `GET /api/images/:id` `POST /api/images/upload` `DELETE /api/images/:id`

### 仪表盘（admin）

- `GET /api/dashboard/overview` — 平台总览统计
- `GET /api/dashboard/vm-status` — 虚拟机状态分布

### 审计日志（admin）

- `GET /api/audit` — 列表（支持 action / object_type / username / status / 日期范围 / 分页）
- `GET /api/audit/:id` — 详情
- `GET /api/audit/summary` — 操作类型分布

### 其他

- `GET /api/health` — 健康检查

## 项目结构

```
vmops/
├── main.go              # 入口：路由注册、静态托管（web/dist）、种子数据
├── config/              # 环境变量配置
├── database/            # GORM 连接与自动迁移
├── handler/             # HTTP 处理器（auth/user/host/vm/image/audit/dashboard）
├── middleware/          # JWT / CORS / 审计中间件
├── model/               # GORM 模型（user/host/vm/image/audit）
├── scripts/             # init-db.sql 等
├── static/              # 旧版单文件前端（兜底）
├── web/                 # Vue3 + Vite 前端工程（详见 web/README.md）
│   └── dist/            # 构建产物，由后端托管
└── docs/                # 设计 / 开发文档
```

## 文档

详见 [`docs/`](docs/README.md) 目录：

- [00-选题依据与开题.md](docs/00-选题依据与开题.md) — 选题背景、研究内容、技术路线、进度安排（开题）
- [01-相关技术基础.md](docs/01-相关技术基础.md) — KVM/libvirt、Go/gin/GORM、Vue3、技术选型
- [02-系统需求分析.md](docs/02-系统需求分析.md) — 角色权限、功能/非功能需求
- [03-系统总体设计.md](docs/03-系统总体设计.md) — 架构、模块划分、请求流转
- [04-数据库设计.md](docs/04-数据库设计.md) — 五张核心表结构、ER 图
- [05-详细设计与实现.md](docs/05-详细设计与实现.md) — 逐模块实现 + 前端
- [06-系统测试与验证.md](docs/06-系统测试与验证.md) — 测试环境、功能测试、真实 KVM 演示
- [07-部署与运维.md](docs/07-部署与运维.md) — 部署与运维手册
- [08-总结与展望.md](docs/08-总结与展望.md) — 工作总结、监控/混合云等后续工作
- [09-答辩演示脚本.md](docs/09-答辩演示脚本.md) — 演示流程、功能清单、FAQ
- [10-参考项目研究.md](docs/10-参考项目研究.md) — virt-manager / Cockpit / vmdashboard / KvmDash 调研笔记

## License

MIT
