# <img src="branding/virtkite-mark-64.png" width="36" align="top" alt="鸢航"> 鸢航 VirtKite · 基于 KVM 的轻量级私有云管理平台

<p>
  <img src="branding/virtkite-horizontal.png" alt="鸢航 VirtKite" width="520" />
</p>

**鸢航 VirtKite** —— 基于 **KVM 虚拟化**的轻量级私有云管理平台的设计与实现。以 Go 构建后端 API，Vue3 构建管理前端，实现虚拟机全生命周期、镜像模板、硬件热管理、操作审计与监控的统一管理。

### 名字由来

**VirtKite = virt（虚拟化生态词根：KVM / libvirt / virt-manager 一脉相承）+ kite（风筝）**——最轻的飞行器，线在你手里：虚拟机飘在云上，管理权收在平台中。中文名「**鸢航**」：鸢即纸鸢（风筝古称），亦指猛禽鸢鹰；航取掌舵远行之意。

Logo 一笔三义：**波浪线既是终端的家目录符 `~`，也是海面**——纸鸢掠浪而行，正是「鸢航」；金色虚线自浪间牵向鸢身，"断而未断"，是平台与虚拟机之间的管理通道。品牌口号一句话：**把你的私有云放上天，线始终在手中。**

对标 virt-manager 核心功能（创建向导/硬件管理/控制台/存储池/网络/快照），辅以 PVE 式增量克隆（qcow2 backing chain）与 cloud-init 快速初始化。

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
- [x] RBAC 第一阶段（**viewer 完全只读**：读接口放行 + 图形控制台只读观看；变更操作、SSH 终端、串口控制台一律 403，前端按钮级隐藏）
- [x] 宿主机管理（纳管 / 连通性测试回写状态 / /proc 实时状态 + 中文时长）
- [x] 虚拟机生命周期（卡片列表 / 详情 / 真实 KVM 建机 / 启停重启 / 暂停恢复 / 删除带存储清理）
- [x] **异步任务系统**（创建/删除/克隆/优雅关机走后台 worker，202 + 轮询，任务中心可见；入队有界 + 两层 panic 兜底）
- [x] **创建向导**（ISO / 导入磁盘 / 云镜像+cloud-init / 克隆四模式 + 汇总页）
- [x] **硬件热管理**（磁盘/网卡热插拔、调核/调内存、自启、引导顺序、XML 双通道编辑）
- [x] **增量克隆**（真 linked clone：子卷 XML 声明 `<backingStore>` 指向父盘，等价 `qemu-img create -f qcow2 -F qcow2 -b`，`qemu-img info` 可见 `backing file`；克隆机 UUID 与**每块网卡 MAC** 均重新生成）
- [x] **删除保护**（三重守卫：池外文件 / 镜像库登记的共享基镜像 / 仍被子卷依赖的增量克隆父盘一律不删，保留的卷经任务结果 `kept_volumes` 与服务端日志给出中文原因）
- [x] **cloud-init**（纯 Go 生成 seed ISO，用户/密码/SSH key/静态 IP）
- [x] 存储池管理（池/卷 CRUD + 卷列表刷新修复 + 建盘多池选择 + 池路径/卷格式校验）
- [x] 网络管理（CRUD / 启停 / XML 编辑 / DHCP 范围 / NAT 模板 + 网关 IPv4 校验）
- [x] 网页控制台（admin 三入口：VNC 图形 / SSH 终端 / 免 IP 串口；viewer 仅 VNC 只读；页内一键开机闭环；SSH 参数记忆）
- [x] **控制台会话跟踪**（谁连了哪台 VM，SSH/串口可服务端强制断开；WS 写入经 `console.Conn` 串行化）
- [x] 快照管理（名称+描述 / 列表含时间状态 / 删除 / 回滚）
- [x] 镜像管理（上传到池 / 模板标记 / 基于模板 linked clone 建机）
- [x] 审计日志（中间件自动写入 + 用户名回填 + 查询 / 详情 / 操作类型分布）
- [x] 仪表盘（总览计数 + 状态分布 + 宿主机实时大盘 + VM 实时表）
- [x] VM 列表实时化（卡片 + CPU/内存迷你折线 + 搜索筛选 + 批量操作）
- [x] 任务中心 / 会话管理 / 系统设置页（生效配置快照 + 轮询偏好）
- [x] **Prometheus 监控**（内建 `/metrics`：VM/宿主机/存储池/任务指标 + 5 告警规则 + Grafana 9 面板）
- [x] 存量 VM 导入 / 纳管
- [x] **安全加固**（路径参数主键统一解析防 SQL 注入 / libvirt XML 全部走 `encoding/xml` / JWT 锁定 HS256 / SSH 目标白名单 / release 密钥强校验）
- [x] E2E 回归脚本（`scripts/smoke.sh`，23 项断言）
- [x] 单元测试（140 个顶层测试函数 / 约 950 个子用例 / 7 个包，`go test -race ./...` 全通过；纯函数目标覆盖率基本 100%）
- [x] 前端工程化（路由懒加载 + manualChunks 分包：首屏下载量 −50%；`utils/format.js` 收敛 10 余处重复；图标全部换成 `@element-plus/icons-vue`）


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

也可直接 `go run main.go`（无需先 build 后端）。前端产物按
`<可执行文件目录>/web/dist` → `<可执行文件目录>/static` → `<当前工作目录>/web/dist` → `<当前工作目录>/static`
四候选依次探测，启动日志会打印命中的目录；全部未命中时降级为「仅 API」模式并在 `/` 返回中文提示页，
不会退出进程（后端 `go run` + 前端 `npm run dev` 是正常开发姿势）。

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
IMAGE_DIR=/var/lib/libvirt/images
SEED_DIR=/home/jiuzhao/vmops/data/seed
```

> ⚠️ `SERVER_MODE=release` 时**必须**把 `JWT_SECRET_KEY` 改成自定义值：仍为空或仍等于上面的内置默认值
> 时进程启动即被拒绝（默认值公开可见，任何人都能据此伪造 admin token）。

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
# docker-compose 一键栈（唯一方式；原生 ~/monitor 目录已废弃删除）
docker compose up -d prometheus grafana alertmanager
# 看板：http://127.0.0.1:3000/d/vmops-overview（admin/admin）
# Prometheus :9090，Grafana :3000，Alertmanager :9093
```

### 5. 测试账号

| 账号 | 密码 | 角色 | 权限 |
|------|------|------|------|
| `admin` | `password` | 管理员 | 全部权限，含 SSH 终端与串口控制台 |
| `user` | `123456` | 只读运维（viewer） | 只读接口 + 图形控制台**只读观看**；变更操作、SSH 终端、串口控制台一律 403 |

> 种子口令不再写入启动日志（只打用户名与角色），首次登录后请立即修改。

### 6. 回归验证

```bash
./scripts/smoke.sh          # E2E 23 项：只读接口 + metrics + 创建/删除 task 全链路 + 硬件管理
go test -race ./...         # 单元测试 140 个顶层函数 / 约 950 子用例 / 7 个包（必须带 -race）
go build ./... && go vet ./... && gofmt -l .
```

> `.golangci.yml` 已配置（govet/errcheck/staticcheck/unused/ineffassign/gofmt/revive，`go: "1.25"`），但本机**未安装** `golangci-lint`，
> 该项静态检查尚未执行，列为待补项。另注意该配置为 v1 schema，装 v2.x 会因字段改名（`linters-settings` → `linters.settings` 等）报错。

### 7. 容器构建（可选）

```bash
cd web && npm run build && cd ..     # 前端产物先出来（web/dist 被 gitignore，构建阶段从上下文带入）
docker build -t vmops:latest .       # 实测 74 秒，产物 51.5MB
```

Dockerfile 关键点（均为实测踩坑后固定下来的）：

| 项 | 取值 | 原因 |
|---|---|---|
| builder 基础镜像 | `golang:1.25-alpine` | `go.mod` 要求 `go 1.25.0`，`golang:1.21` 直接报版本不足 |
| 构建目标 | `go build -o vmops .` | 写 `./...` 匹配到 11 个包，报 `cannot write multiple packages to non-directory` |
| 模块代理 | `ARG GOPROXY=https://goproxy.cn,direct` | 容器内 `proxy.golang.org` 实测超时，不加则 `go mod download` 挂死；海外环境用 `--build-arg` 覆盖 |
| 时区 | `-tags timetzdata` + `ENV TZ=Asia/Shanghai` | alpine 无 `/usr/share/zoneinfo`，而 DSN 带 `loc=Local`，否则时间静默退化为 UTC（差 8 小时） |
| 运行镜像 | `alpine:3.24` | `alpine:3.19` 已停止维护 |
| 前端产物 | 从 builder 阶段 `COPY --from` + `mkdir -p` 兜底空目录 | `web/dist/` 被 gitignore，干净克隆里直接 `COPY web/dist` 会构建失败 |

## API 接口

统一响应格式：`{"code":200,"message":"success","data":{...}}`；除登录与 `/metrics`、`/health`、
websockify 回调 `/api/vnc/token/:token` 外均需在 `Authorization: Bearer <token>` 头携带 JWT
（浏览器 WebSocket 无法带 Header，改用 `?token=<JWT>` 查询参数）。耗时操作（创建/删除/克隆/停止）返回
`202 {"task_id"}`，轮询 `GET /api/tasks/:id` 至终态。

常见错误码：

| 状态码 | message | 场景 |
|---|---|---|
| 400 | `ID 参数非法` | 路径参数 `:id` 非正整数（如 `/api/vms/abc`）。**主键先解析再入库查询，不再落到 404** |
| 401 | `未提供认证信息` / `认证格式错误` / `Token 无效或已过期` / `用户不存在` | 鉴权失败；签名算法非 HS256 也归入「Token 无效或已过期」 |
| 403 | `账号已被禁用` / `需要管理员权限` | 账号停用；viewer 发起变更或访问 admin 组 |
| 403 | `只读角色不能使用 SSH 终端与串口控制台，请使用图形控制台查看` | viewer 访问 `/terminal` 或 `/serial` |


### 认证

- `POST /api/auth/login` — 登录
- `GET  /api/auth/me` — 当前用户信息
- `PUT  /api/users/me/password` — 修改密码（所有角色，需旧密码）

### 用户管理（admin）

- `GET /api/users` `POST /api/users` `PUT /api/users/:id` `DELETE /api/users/:id`

### 宿主机管理

- `GET /api/hosts` `POST /api/hosts` `PUT /api/hosts/:id` `DELETE /api/hosts/:id`（`ssh_ip` 须为 IP 或主机名，该值会作为 argv 传给 `ping`）
- `POST /api/hosts/:id/test` — 连通性测试（回写 reachable 状态）
- `GET  /api/hosts/:id/stats` — 宿主机状态（/proc 直读 + 中文时长）

### 虚拟机管理

- `GET /api/vms` — 列表（含 perf 实时聚合，一次请求渲染指标）
- `GET /api/vms/options` — 创建向导选项（池/网络/镜像/OS）
- `GET  /api/vms/:id` `POST /api/vms`（202 task） `DELETE /api/vms/:id`（202 task，任务结果可能带 `kept_volumes`：被守卫保留的共享卷及中文原因）
- `GET  /api/vms/import/scan` — 扫描未纳管存量 VM
- `POST /api/vms/import` — 勾选批量导入（body `{host_id?, names: []}`；响应 `{imported, skipped, failed, errors[]}`，`errors` 只给「域名 + 中文原因」，原始 SQL 错误只进日志）
- `POST /api/vms/:id/start` `POST /api/vms/:id/stop`（202 task） `POST /api/vms/:id/restart`
- `POST /api/vms/:id/pause` `POST /api/vms/:id/resume`
- `POST /api/vms/:id/clone`（202 task，增量克隆；新机 UUID 与全部网卡 MAC 重新生成）
- `GET  /api/vms/:id/spec` — 完整配置模型（含 raw_xml）
- `PUT  /api/vms/:id/spec` — 整体重定义（停机）
- `PUT  /api/vms/:id/cpu` `PUT /api/vms/:id/memory` `PUT /api/vms/:id/autostart` `PUT /api/vms/:id/boot`
- `POST /api/vms/:id/devices/disks` `DELETE /api/vms/:id/devices/disks/:target` — 磁盘热插拔
- `POST /api/vms/:id/devices/interfaces` `DELETE /api/vms/:id/devices/interfaces/:mac` — 网卡热插拔
- `GET  /api/vms/:id/stats` — 实时性能（CPU/内存/磁盘/网络）
- `GET  /api/vms/:id/xml` `PUT /api/vms/:id/xml` — XML 查看/编辑
- 快照：`GET /api/vms/:id/snapshots`（名称/描述/时间/状态） `POST /api/vms/:id/snapshots`（`{name, description}`） `DELETE /api/vms/:id/snapshots/:snap` `POST .../revert`
- `POST /api/vms/:id/vnc-token` — noVNC token（viewer 可用，响应含 `view_only`：非 admin 为 `true`，前端以 noVNC 只读模式打开）
- `GET  /api/vms/:id/terminal` — Web 终端 WS（SSH 桥，`?token=` 鉴权；**仅 admin**，目标须过私有网段白名单）
- `GET  /api/vms/:id/serial` — 串口 WS（libvirt console 桥；**仅 admin**）

### 镜像管理

- `GET /api/images`（`?is_template=true` 模板筛选） `GET /api/images/:id`
- `POST /api/images/upload` — 上传到指定池（form：name/os_version/pool/file）
- `PUT /api/images/:id/template` — 标记模板
- `POST /api/images/:id/clone`（202 task，基于模板/云镜像增量克隆建机）
- `DELETE /api/images/:id`

### 存储 / 网络

- 存储池：`GET /api/storage/pools` `GET /api/storage/pools/:name`（含卷） `POST /api/storage/pools` `DELETE /api/storage/pools/:name`；卷：`POST /api/storage/pools/:name/volumes` `DELETE .../volumes/:vol`
  - 建池 `path` 须为规范绝对路径（不含 `..`、无结尾斜杠、非根目录）；建卷 `format` 仅接受 `qcow2` / `raw`
- 网络：`GET /api/networks` `GET /api/networks/:name`（含 XML/autostart/DHCP） `POST /api/networks` `POST /api/networks/xml` `PUT /api/networks/:name` `POST /api/networks/:name/start|stop` `DELETE /api/networks/:name`
  - `POST /api/networks` 的 `gateway` 须为合法 IPv4；`POST /api/networks/xml` 与 `PUT /api/networks/:name` 仍接受原始 XML 直定义（admin 专属，尚未做结构校验）

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
├── main.go              # 入口：路由/静态托管（四候选探测）/任务管理器/会话注册表/种子数据/启动收敛
├── config/              # 环境变量配置（含 SEED_DIR）
├── database/            # GORM 连接与自动迁移
├── handler/             # HTTP 处理器（vm/spec/device/clone/stats/task/session/metrics/settings/…）
│                        #   param.go：paramID/parseID，路径参数主键统一解析（禁止直传 GORM）
├── middleware/          # JWT / OperatorMiddleware(RBAC) / CORS / 审计中间件
├── model/               # GORM 模型（user/host/vm/image/audit/task/session）
├── service/
│   ├── virt/            # libvirt 封装（domain/spec/device/storage/network/snapshot/console/stats/clone/cloudinit）
│   │                    #   clone.go：增量克隆（backingStore）+ buildCloneSpec 纯函数（UUID/MAC 重生成）
│   │                    #   storage.go：ListBackingRefs（父卷→子卷依赖表，删卷守卫用）
│   │                    #   *_test.go：spec(13)/clone(8)/cloudinit(6)/storage(7)/network(4)/state(4)/snapshot(4)
│   ├── tasks/           # 异步任务队列（4 worker + 5 executors，有界入队 + 两层 recover）
│   │                    #   manager_test.go(11)/vm_tasks_test.go(14)：panic 兜底/有界入队/payload 解析
│   ├── console/         # 会话注册表（WS 持有/强制断开/VNC 映射/过期清扫）
│   │                    #   conn.go：写锁串行化的 WS 包装 + conn_test.go（5 个 -race 用例）
│   ├── metrics/         # Prometheus 内建采集
│   └── vnc/             # VNC token 存储
├── scripts/             # init-db.sql / smoke.sh（E2E 回归）/ start-novnc.sh
├── deploy/              # prometheus.yml / alerts.yml / grafana 看板与 provisioning
├── web/                 # Vue3 + Vite 前端（17 页面：Dashboard/Monitor/VmList/VmDetail/向导/Host/Image/Storage/Network/Task/Audit(含Session)/Settings/UserList/Profile/Console/Login）
│   ├── src/utils/format.js  # 状态文案/时间/尺寸/错误提取统一实现（收敛 10 余处重复）
│   └── dist/            # 构建产物，由后端托管（路由懒加载 + manualChunks：首屏 −50%）
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

## 已知未处理项（如实记录，勿视为已解决）

| 项 | 现状 | 影响面 |
|---|---|---|
| `POST /api/networks/xml`、`PUT /api/networks/:name`、`PUT /api/vms/:id/xml` | 接受调用方原始 XML 直接定义，无结构校验 | 仅 admin 可达 |
| `GET /metrics` | 公开无鉴权（Prometheus 抓取需要） | 泄漏 VM 名与资源指标，需防火墙限制来源 |
| `POST /api/auth/login` | ✅ 已限流（同 IP 1 分钟 5 次失败锁定）+ 无验证码 | 残余：无验证码，可换 IP 分布式爆破 |
| CORS | `CORS_ORIGINS` 默认 `*` | 生产需收敛为具体来源 |
| Web 终端 SSH | `HostKeyCallback` 为 `InsecureIgnoreHostKey()` | 目标已限私有网段，残余中间人风险 |
| `golangci-lint` | 本机未安装，深度 lint 未执行 | 静态检查覆盖不完整（`go build`/`go vet`/`gofmt` 已过） |
| 孤儿卷 | 「先删父机、再删子机」顺序下，被守卫保留的父盘会残留为无人引用的孤儿文件，无自动清理 | 占存储空间；刻意取舍——宁留垃圾文件也不能损坏在用磁盘 |
| 状态字面量 | `service/tasks/vm_tasks.go` 仍有 5 处 `"shut off"` 字面量未换成常量 | 一致性隐患，行为正确 |
| 导入失败原因 | `POST /api/vms/import` 响应的 `errors` 数组前端 `VmList.vue` 未消费（只读 `imported`/`skipped`/`failed`） | 单台导入失败时用户看不到具体原因 |
| 多宿主机 | `hosts.libvirt_uri` 从未用于建立连接，`virt.New()` 固定 `qemu:///system` | 多宿主机纳管目前是空壳，仅本机真实可管 |

## License

MIT
