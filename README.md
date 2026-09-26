# <img src="branding/virtkite-mark-64.png" width="36" align="top" alt="鸢航"> 鸢航 VirtKite · 基于 KVM 的轻量级私有云管理平台

<p>
  <img src="branding/virtkite-horizontal.png" alt="鸢航 VirtKite" width="520" />
</p>

**鸢航 VirtKite** —— 基于 **KVM 虚拟化**的轻量级私有云管理平台的设计与实现。以 Go 构建后端 API，Vue3 构建管理前端，实现虚拟机全生命周期、镜像模板、硬件热管理、操作审计与监控的统一管理。

### 名字由来

**VirtKite = virt（虚拟化生态词根：KVM / libvirt / virt-manager 一脉相承）+ kite（风筝）**——最轻的飞行器，线在你手里：虚拟机飘在云上，管理权收在平台中。中文名「**鸢航**」：鸢即纸鸢（风筝古称），亦指猛禽鸢鹰；航取掌舵远行之意。

Logo 一笔三义：**波浪线既是终端的家目录符 `~`，也是海面**——纸鸢掠浪而行，正是「鸢航」；金色虚线自浪间牵向鸢身，"断而未断"，是平台与虚拟机之间的管理通道。品牌口号一句话：**把你的私有云放上天，线始终在手中。**

对标 virt-manager 核心功能（创建向导/硬件管理/控制台/存储池/网络/快照），辅以 PVE 式增量克隆（qcow2 backing chain）与 cloud-init 快速初始化。v3 起参考 1Panel（GPLv3，借鉴设计模式而非引入代码）吸收运维面板能力（容器管理 / 应用商店 / 计划任务 / 工具箱 / AI 助手），但保持 KVM 私有云定位，不做建站赛道。

## 技术栈

| 层 | 技术 |
|----|------|
| 后端 | Go 1.25 + gin + GORM + golang-jwt + bcrypt |
| 数据库 | MySQL 8（Docker 部署） |
| 虚拟化 | libvirt / KVM（`digitalocean/go-libvirt` 纯 Go RPC 直连，无 CGO） |
| 容器 | Docker CLI / Docker Engine API（`service/dockerx` 封装：结构化输出 + exec TTY 容器终端）+ docker compose |
| 前端 | Vue 3 + Vite + Element Plus + vue-router + ECharts |
| 监控 | 内建 Prometheus exporter + Prometheus + Grafana + Alertmanager |
| 日志 | Loki + Promtail（以应用商店 compose 包一键交付，LogQL 经后端代理查询） |
| AI | OpenAI 兼容 API 代理（SSE 流式 + 平台上下文注入，Key 只存服务端） |
| 部署 | 二进制直跑 / Docker / docker-compose |

## 功能清单

- [x] 基础框架（配置 / 数据库 / 中间件 / 启动收敛）
- [x] 认证与授权（JWT + bcrypt + **三级角色 admin/operator/viewer** + 修改密码）
- [x] RBAC 第一阶段（**viewer 完全只读**：读接口放行 + 图形控制台只读观看；变更操作、SSH 终端、串口控制台一律 403，前端按钮级隐藏；**operator** = VM 全生命周期 + 三类控制台，`/api/vms` 之外写操作 403）
- [x] **资产授权（堡垒机 4A 轻量版）**（`vm_grants` 授权表：把 VM 分配给用户、`expires_at` 到期自动失效；**授权决定可见性**——非 admin 未授权访问一律 404 查无此项；VM 详情页「授权管理」面板（admin）；建机/克隆自动授权发起人，删 VM 回收授权；监控中心四端点对 viewer 收权 403）
- [x] 宿主机管理（纳管 / 连通性测试回写状态 / /proc 实时状态 + 中文时长）
- [x] 虚拟机生命周期（卡片列表 / 详情 / 真实 KVM 建机 / 启停重启 / 暂停恢复 / 删除带存储清理；IP 经 DHCP 租约 + qemu-guest-agent 双通道自动回填）
- [x] **异步任务系统**（创建/删除/克隆/优雅关机走后台 worker，202 + 轮询，任务中心可见；入队有界 + 两层 panic 兜底）
- [x] **创建向导**（四步：安装方式 → 计算资源 → 磁盘与网络 → 确认创建；三种安装方式：本地 ISO（介质平铺选择 + 按 ISO 文件名自动识别操作系统）/ 云镜像 + cloud-init / 克隆现有 VM）
- [x] **向导云镜像方式**（前置条件检查与直达链接、存储池可用空间显示与超额红字预警、增量盘标注（qcow2 backing 不复制镜像文件）、cloud-init 折叠配置（主机名/用户/密码/SSH 公钥/网络模式）、网络按 forward 类型分组）
- [x] **硬件热管理**（磁盘/网卡热插拔、一键建盘挂载与补齐标准设备（guest-agent 通道 + virtio-rng）、调核/调内存、自启、整域 XML 编辑）
- [x] **增量克隆**（真 linked clone：子卷 XML 声明 `<backingStore>` 指向父盘，等价 `qemu-img create -f qcow2 -F qcow2 -b`，`qemu-img info` 可见 `backing file`；克隆机 UUID 与**每块网卡 MAC** 均重新生成）
- [x] **删除保护**（三重守卫：池外文件 / 镜像库登记的共享基镜像 / 仍被子卷依赖的增量克隆父盘一律不删，保留的卷经任务结果 `kept_volumes` 与服务端日志给出中文原因）
- [x] **cloud-init**（纯 Go 生成 seed ISO，用户/密码/SSH key/静态 IP）
- [x] 存储池管理（池/卷 CRUD + 卷列表刷新修复 + 建盘多池选择 + 池路径/卷格式校验）
- [x] 网络管理（CRUD / 启停 / XML 编辑 / DHCP 范围 / NAT 模板 + 网关 IPv4 校验）
- [x] 网页控制台（admin/operator 三入口：VNC 图形 / SSH 终端 / 免 IP 串口；viewer 仅 VNC 只读；页内一键开机闭环；SSH 参数记忆）
- [x] **控制台会话跟踪**（谁连了哪台 VM，SSH/串口可服务端强制断开；WS 写入经 `console.Conn` 串行化）
- [x] 快照管理（名称+描述 / 列表含时间状态 / 删除 / 回滚）
- [x] 镜像管理（上传到池 / 既有池卷登记 / 模板标记 / 基于模板 linked clone 建机；页面双 tab：云镜像/模板盘 + ISO 安装镜像只读展示（管理在存储池页），带来源存储池标注）
- [x] 审计日志（中间件自动写入 + 用户名回填 + 多条件查询 / 操作类型分布；审计中心以双 tab 承载操作日志与控制台会话）
- [x] 仪表盘（概览/监控双 tab：总览计数、状态分布、宿主机实时大盘、资源容量/超分卡、历史性能曲线（Prometheus query_range 回放，刷新不清零）；监控 tab 复用监控中心）
- [x] VM 列表实时化（卡片 + CPU/内存迷你折线 + 搜索筛选 + 批量电源（状态不一致时禁用）与删除）
- [x] 任务中心（任务详情抽屉解析 `kept_volumes`）/ 审计中心（操作日志 + 会话双 tab）/ 系统设置（可写运行参数，保存即生效；只读快照移至仪表盘「平台信息」卡）/ 个人中心 / 用户管理
- [x] **Prometheus 监控**（内建 `/metrics`：VM/宿主机/存储池/任务指标 + 9 告警规则 + Grafana 双看板（宿主机 5 面板 / 虚拟机 6 面板）；监控中心含实时告警、webhook 告警历史、file_sd 抓取目标预览、Grafana 探活兜底）
- [x] 存量 VM 导入 / 纳管
- [x] **监控闭环**（Prometheus file_sd 服务发现自动下发 running 且已知 IP 的 VM 目标；Alertmanager webhook 告警网关按 fingerprint 去重入库 + 分页历史；**告警出站通知**（v3.4：设置页配置飞书/钉钉机器人地址，仅新增 firing 或 resolved→firing 推送、同 fingerprint 重复去重，best-effort 不影响 webhook 响应）；`vms.ip` DHCP 租约 + QGA 双通道回填）
- [x] **容器管理（v3：KVM 域 + Docker 容器「双运行时」统一面板）**（容器 / 镜像 / 网络 / 卷 / 编排（compose 项目级启停）五 tab + **容器创建**（v3.4：名称/镜像/端口映射/挂载卷/环境变量/重启策略/启动命令 → `docker run` 参数映射，纯函数校验，本地缺镜像自动拉取）+ 容器终端（WebSocket ↔ Docker Engine API exec TTY，支持运行中调窗）+ 日志查看（跟随/下载/tail 行数）+ 资源占用实时统计 + 批量启停删与悬空镜像/容器清理；viewer 403，容器终端与 SSH 终端共用 `console.Conn` 写锁 + recover 纪律与会话强断）
- [x] **应用商店（v3）**（参考 1Panel（GPLv3）声明式 compose 应用包设计模式：应用级+版本级 `data.yml`（动态表单）+ `docker-compose.yml` `${VAR}` 占位 → 写 `.env` → `docker compose up -d`，变量替换交给 compose 原生插值；内置 20 个应用（nginx / mysql / redis / minio / gitea / jenkins / n8n 等），安装走异步任务、安装前端口占用预检、卸载保留数据目录；Docker 不可用时页面级禁用安装）
- [x] **VM 应用（v3）**（不经容器往虚拟机里装软件：SSH 在客户机内幂等执行安装脚本，10 个内置应用（nginx/mysql/redis/php/nodejs/docker-engine 等），已装检测自动跳过）
- [x] **AI 运维助手（v3）**（OpenAI 兼容 `/chat/completions` 代理：API Key 只存服务端永不下发前端；`with_context` 注入平台环境摘要（VM/容器/告警统计），能答「我平台几台虚拟机在跑」；SSE 流式逐段转发；助手只读问答、不具备任何写操作能力；viewer 403）
- [x] **SSH 跳板入口（v3.6，借鉴堡垒机 4A）**（任意终端工具 `ssh <用户名>@<宿主机> -p 2222` 直达资产：平台密码认证 + per-IP 爆破限流 → 交互式资产菜单只列「有效授权 ∩ 运行中 ∩ 有 IP ∩ 已托管凭据」→ 连接前现查重验（授权/状态/IP 任何一项失效当场拒绝）→ vmssh 安全链桥接直达 VM shell；exit 回菜单、q 断开；会话落 console_sessions 审计；**命令黑名单**：rm -rf/mkfs/dd 等高危命令整行拦截并落审计（大小写/多空格归一，设置页可配）。`JUMPD_ENABLED=1` 开启，默认关闭）
- [x] **用户组授权 + 授权申请审批流（v3.6，借鉴堡垒机 4A）**（用户组：组 → 资产批量授权，组内成员即时获得/失去可见性；授权申请：学生在「资产申请」页自助申请（花名册只含名称/状态）→ 教师在审批队列批准（可调时长）或驳回 → 批准即写限时授权，到期自动失效。跳板会话天然禁用 sftp/scp/端口转发——只能敲键盘，传不了东西开不了隧道）
- [x] **SSH 凭据托管（v3）（AES-256-GCM 加密落库：主密钥运行时注入不落库 + 每条记录随机盐，数据库泄露后凭据不可直接可读；文件管理 / VM 应用安装「使用已保存凭据」后端自行解密消费，明文不出服务端）
- [x] **VM 文件管理（v3：双通道）**（在线通道：SSH/SFTP 浏览/上传/下载/删除/建目录，管开机机；离线通道：guestmount 只读挂载关机机系统盘，不依赖 VM 内 SSH；运行中 VM 一律拒绝离线挂载防磁盘锁，挂载只读——宁可浏览受限不可损坏磁盘）
- [x] **计划任务（v3）**（自研五字段 cron 解析（百行纯函数可单测，不引第三方）+ 整分 tick 调度器：定时快照 / mysqldump 数据库备份；启停 / 立即运行 / 执行记录（成功失败与耗时））
- [x] **Loki 日志栈（v3）**（以应用商店 compose 包一键交付（loki + promtail），监控中心提供 LogQL 查询与标签接口——指标 + 日志 + 告警完整可观测性）
- [x] **云镜像市场（v3）**（内置官方云镜像清单一键提交下载任务（异步、流式落盘、完成自动登记镜像库），镜像管理页第三 tab）
- [x] **VM 导出/导入（v3）**（tar.gz 全量包 = 域 XML + 系统盘卷；导出 gzip/tar 两级 writer 管道直写响应体，不落临时盘不整载内存；导入失败按副作用逆序清理）
- [x] **回收站（v3）**（删除是软删——回收站页可视化软删 VM：恢复 / 彻底清除（admin），删错机器的后悔药）
- [x] **工具箱（v3）**（宿主机快捷运维：进程列表 / 磁盘用量 / Docker 清理 / 任务记录清理，admin 专属）
- [x] **cloud-init 模板（v3）**（初始化配置存为可复用模板，创建向导一键套用 / 当前配置保存为模板）
- [x] **拓扑图（v3）**（宿主机-存储池-虚拟机从属关系可视化：池节点按容量、VM 节点按状态着色，拖拽布局/滚轮缩放/图例过滤）
- [x] **系统公告 + 安全入口（v3）**（公告板登录页与仪表盘公开展示（admin 经设置页编辑）；登录接口可设安全入口暗号，无暗号请求一律 404 伪装；建用户/改密密码复杂度校验）
- [x] **安全加固**（路径参数主键统一解析防 SQL 注入 / libvirt XML 全部走 `encoding/xml` / JWT 锁定 HS256 / SSH 目标白名单 / release 密钥强校验）
- [x] E2E 回归脚本（`scripts/smoke.sh`，23 项断言）
- [x] 单元测试（252 个顶层测试函数 / 207 个 `t.Run` 子测试分组 / 18 个测试包，`go test -race ./...` 全通过；纯函数目标覆盖率基本 100%）
- [x] 前端工程化（路由懒加载 + manualChunks 分包：首屏下载量 −50%；`utils/format.js` 收敛 10 余处重复；图标全部换成 `@element-plus/icons-vue`）


## 环境要求

- Go 1.25+
- Node.js 18+（仅前端开发 / 构建需要）
- MySQL 8.0+（或 Docker）
- libvirt + KVM（运行虚拟机的宿主机）
- Docker（可选：容器管理与容器形态的应用商店需要，未安装时相关页面显示不可用提示）
- Prometheus / Grafana / Loki（可选，Docker compose 一键栈，见监控章节）

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
# SERVER_MODE 默认 release（安全默认）；本地开发用 ./start.sh（自动 export debug）或显式设为 debug
SERVER_MODE=debug
IMAGE_DIR=/var/lib/libvirt/images
SEED_DIR=/home/jiuzhao/vmops/data/seed
```

> ⚠️ `SERVER_MODE` 默认值即 **release**（安全默认）。release 模式下**必须**设置 `JWT_SECRET_KEY`：
> 为空、等于内置默认值或命中弱密钥黑名单（含 compose 示例弱值）时进程启动即被拒绝（默认值公开可见，
> 任何人都能据此伪造 admin token）；同时 release 下 `CORS_ORIGINS` 不能为 `*`。本地开发用
> `./start.sh` 启动（已自动 `export SERVER_MODE=debug`）；**手动重启后端必须带 `SERVER_MODE=debug`**，
> 否则按 release 校验拒绝启动。

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
| `admin` | `Password1` | 管理员（admin） | 全部权限，含 SSH 终端与串口控制台、用户/设置/授权管理 |
| `stu` | `123456` | 操作员（operator） | VM 全生命周期 + 三类控制台；`/api/vms` 之外写操作 403；**仅能看到被授权的 VM**（经「授权管理」面板分配） |
| `user` | `123456` | 只读运维（viewer） | 只读接口 + 图形控制台**只读观看**；变更操作、SSH 终端、串口控制台、监控中心四端点一律 403 |

> 登录页有演示账号提示条，构建时 `VITE_SHOW_DEMO_TIP=false` 可整体隐藏（公开演示/截图归档时不应暴露口令）。
> 种子口令不再写入启动日志（只打用户名与角色），首次登录后请立即修改。

### 6. 回归验证

```bash
./scripts/smoke.sh          # E2E 23 项：只读接口 + metrics + 创建/删除 task 全链路 + 硬件管理
go test -race ./...         # 单元测试 252 个顶层函数 / 18 个测试包（必须带 -race）
go build ./... && go vet ./... && gofmt -l .
```

> 静态检查：`go build` / `go vet` / `gofmt` 三件套全过；`.golangci.yml` 配置就绪
> （govet/errcheck/staticcheck/unused/ineffassign/gofmt/revive，v1 schema——装 v2.x 会因字段改名报错）。

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

如用 docker compose 一键部署：`JWT_SECRET_KEY` 已改为 `${JWT_SECRET_KEY:?请在 .env 中设置强随机 JWT_SECRET_KEY}` 强制外部注入——未在 `.env` 设置时 compose 直接报错退出。

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
| 403 | `账号已被禁用` / `需要管理员权限` | 账号停用；viewer/operator 发起越权变更或访问 admin 组 |
| 403 | `只读角色不能使用 SSH 终端与串口控制台，请使用图形控制台查看` | viewer 访问 `/terminal` 或 `/serial` |
| 403 | `监控数据仅操作员与管理员可见` | viewer 访问监控中心四端点（alerts / alerts/history / file-sd / grafana-status） |
| 404 | `虚拟机不存在` | **非 admin 访问未授权的 VM**（资产授权：授权决定可见性，未授权与不存在同响应） |


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

- `GET /api/vms` — 列表（含 perf 实时聚合，一次请求渲染指标；**非 admin 只返回其持有有效授权的 VM**）
- `GET /api/vms/:id/grants` `POST /api/vms/:id/grants`（`{user_id, expires_at?}`） `DELETE /api/vms/:id/grants/:gid` — 资产授权（admin；授权决定可见性，到期自动失效）
- `GET /api/vms/options` — 创建向导选项（池/网络/镜像/OS）
- `GET  /api/vms/:id` `POST /api/vms`（202 task） `DELETE /api/vms/:id`（202 task，任务结果可能带 `kept_volumes`：被守卫保留的共享卷及中文原因）
- `GET  /api/vms/import/scan` — 扫描未纳管存量 VM
- `POST /api/vms/import` — 勾选批量导入（body `{host_id?, names: []}`；响应 `{imported, skipped, failed, errors[]}`，`errors` 只给「域名 + 中文原因」，原始 SQL 错误只进日志）
- `POST /api/vms/:id/start` `POST /api/vms/:id/stop`（202 task） `POST /api/vms/:id/restart`
- `POST /api/vms/:id/pause` `POST /api/vms/:id/resume`
- `POST /api/vms/:id/clone`（202 task，增量克隆；新机 UUID 与全部网卡 MAC 重新生成）
- `GET  /api/vms/:id/spec` — 完整配置模型（含 raw_xml）
- `PUT  /api/vms/:id/spec` — 整体重定义（停机）
- `PUT  /api/vms/:id/cpu` `PUT /api/vms/:id/memory` `PUT /api/vms/:id/autostart`
- `POST /api/vms/:id/devices/disks` `POST /api/vms/:id/devices/disks/quick`（建 qcow2 卷 + 热挂载合一） `DELETE /api/vms/:id/devices/disks/:target?delete_volume=`（默认仅分离；`delete_volume=true` 分离并删除存储卷，受镜像库/backing 父盘/他机挂载/cdrom 四重守卫保护，响应含 `volume_deleted`/`keep_reason`）
- `POST /api/vms/:id/devices/interfaces` `DELETE /api/vms/:id/devices/interfaces/:mac` — 网卡热插拔
- `POST /api/vms/:id/devices/standard` — 幂等补齐标准设备（guest-agent 通道 + virtio-rng）
- `GET  /api/vms/:id/stats` — 实时性能（CPU/内存/磁盘/网络）
- `GET  /api/vms/:id/stats-history` — 历史性能曲线（Prometheus query_range，详情页大图）
- `GET  /api/vms/:id/xml` `PUT /api/vms/:id/xml` — XML 查看/编辑
- 快照：`GET /api/vms/:id/snapshots`（名称/描述/时间/状态） `POST /api/vms/:id/snapshots`（`{name, description}`） `DELETE /api/vms/:id/snapshots/:snap` `POST .../revert`
- `POST /api/vms/:id/vnc-token` — noVNC token（viewer 可用，响应含 `view_only`：非 admin 为 `true`，前端以 noVNC 只读模式打开）
- `GET  /api/vms/:id/terminal` — Web 终端 WS（SSH 桥，`?token=` 鉴权；**仅 admin**，目标须过私有网段白名单）
- `GET  /api/vms/:id/serial` — 串口 WS（libvirt console 桥；**仅 admin**）

### 镜像管理

- `GET /api/images`（`?is_template=true` 模板筛选） `GET /api/images/:id`
- `POST /api/images/upload` — 上传到指定池（form：name/os_version/pool/file）
- `POST /api/images/register` — 登记既有存储池卷为云镜像（同路径已登记返回 409，软删记录可恢复）
- `PUT /api/images/:id/template` — 标记模板
- `POST /api/images/:id/clone`（202 task，基于模板/云镜像增量克隆建机）
- `DELETE /api/images/:id`

### 存储 / 网络

- 存储池：`GET /api/storage/pools`（响应含 seed_dir/default_pool/pool_roles） `GET /api/storage/pools/:name`（含卷，卷含 `backing_file`） `POST /api/storage/pools` `DELETE /api/storage/pools/:name`
  - 池元数据：`PUT /api/storage/pools/:name/meta`（平台侧 role/description）
  - 卷引用与保护：`GET /api/storage/pools/:name/volume-refs`（池级一次算全）；`DELETE .../volumes/:vol` 删除前算引用，任一命中返回 409 中文原因；`POST /api/storage/pools/:name/orphan-cleanup`（202 转 `cleanup_volumes` 任务，零引用才删）
  - 卷：`POST /api/storage/pools/:name/volumes`
  - 建池 `path` 须为规范绝对路径（不含 `..`、无结尾斜杠、非根目录）；建卷 `format` 仅接受 `qcow2` / `raw`
- 网络：`GET /api/networks` `GET /api/networks/:name`（含 XML/autostart/DHCP） `POST /api/networks` `POST /api/networks/xml` `PUT /api/networks/:name` `PUT /api/networks/:name/autostart` `POST /api/networks/:name/start|stop` `DELETE /api/networks/:name`
  - `POST /api/networks` 的 `gateway` 须为合法 IPv4；`POST /api/networks/xml` 与 `PUT /api/networks/:name` 仍接受原始 XML 直定义（admin 专属，尚未做结构校验）

### 任务 / 会话

- `GET /api/tasks`（`page`/`page_size` 真分页，旧 `limit` 兼容；`?status=` 筛选） `GET /api/tasks/:id` `DELETE /api/tasks/:id`（仅 finished 可删）
- `GET /api/sessions`（`type`/`vm_name`/`username` 过滤 + `page`/`page_size` 真分页）
- `POST /api/sessions/:id/disconnect` — 强制断开（SSH/串口；VNC 中转无法强断）

### 仪表盘 / 审计 / 设置 / 监控

- `GET /api/dashboard/overview` `GET /api/dashboard/vm-status` `GET /api/dashboard/host-stats` `GET /api/dashboard/vm-perf`
- `GET /api/dashboard/capacity` — 资源容量/超分（分配 vCPU/内存 vs 物理核/内存，超分比）
- `GET /api/dashboard/host-history` `GET /api/dashboard/vm-history` — 历史曲线（Prometheus query_range，大盘与列表迷你图预填）
- `GET /api/audit`（action/object_type/username/status/日期/分页） `GET /api/audit/summary` `GET /api/audit/actions`
- `GET /api/settings`（admin，运行参数与快照） `PUT /api/settings`（admin，写库即生效）
- `GET  /api/monitor/alerts` — 实时告警（代理 Alertmanager，AM 不可达 502；**viewer 403**，下同）
- `GET  /api/monitor/alerts/history` — 告警历史（webhook 入库，status/fingerprint 过滤 + 分页；viewer 403）
- `GET  /api/monitor/file-sd` — file_sd 抓取目标预览（`{enabled, items}`；viewer 403）
- `GET  /api/monitor/grafana-status` — Grafana 探活（前端据此亮「未连接」兜底层；viewer 403）
- `POST /api/monitor/webhook` — Alertmanager 告警网关（公开路由，env `ALERT_WEBHOOK_TOKEN` 可选鉴权；按 fingerprint 去重入库）
- `GET /metrics` — Prometheus exposition（env `METRICS_TOKEN` 非空时要求 Bearer/`?token=`，未设置保持公开）
- `GET /api/health` — 健康检查

### v3 扩展（容器 / 应用商店 / AI / 运维面板）

- Docker（viewer 403）：`GET /api/docker/containers` `POST /api/docker/containers/:id/:action`（start/stop/restart/pause/unpause） `DELETE /api/docker/containers/:id` `GET .../logs`（`?follow/&tail/`） `GET .../inspect` `GET .../stats`、`GET /api/docker/stats`（全量统计）、`GET /api/docker/containers/:id/terminal`（容器终端 WS）
- Docker 镜像/网络/卷/编排：`GET|POST /api/docker/images` `DELETE /api/docker/images/:id` `POST /api/docker/images/pull` `POST /api/docker/prune`、`GET|POST|DELETE /api/docker/networks*`、`GET|POST /api/docker/volumes*` `POST /api/docker/volumes/prune`、`GET /api/docker/compose` `POST /api/docker/compose/:name/:action`
- 应用商店：`GET /api/appstore`（目录，登录可看） `GET /api/appstore/status` `POST /api/appstore/:key/install` `POST /api/appstore/:key/uninstall`（安装走异步任务）
- VM 应用：`GET /api/apps`（SSH 脚本应用目录） `POST /api/vms/apps/install`（挂 `/vms` 前缀，operator 可用——往自己 VM 装软件属操作语义）
- AI 助手（viewer 403）：`GET /api/ai/status` `POST /api/ai/chat`（SSE 流式；`with_context=true` 注入平台摘要）
- SSH 凭据：`POST|GET|DELETE /api/vms/:id/credentials`（AES-256-GCM 密文落库，响应永不含明文）
- VM 文件管理：`POST /api/vms/:id/files/{list,download,upload,delete,mkdir}`（SSH 在线通道）+ `POST /api/vms/:id/files/offline/{mount,list,unmount}` `GET .../offline/download` `GET .../offline-capability`（guestmount 离线只读通道）
- 计划任务（admin）：`GET|POST /api/crons` `PUT|DELETE /api/crons/:id` `POST /api/crons/:id/{toggle,run}` `GET /api/crons/:id/runs`（执行记录） `GET /api/crons/preview?expr=`（下次执行预览）
- SSH 主机指纹（TOFU，admin）：`GET /api/ssh-host-keys` `DELETE /api/ssh-host-keys/:id`
- 回收站（admin）：`GET /api/vms-recycle` `POST /api/vms-recycle/:id/restore` `DELETE /api/vms-recycle/:id/purge`
- 工具箱（admin）：`GET /api/toolbox/{processes,disk}` `POST /api/toolbox/{docker-prune,tasks-purge}`
- 镜像市场：`GET /api/images/market`（清单） `POST /api/images/market/download`（admin，202 任务）
- VM 导出/导入：`GET /api/vms/:id/export`（tar.gz 流式下载） `POST /api/vms/import-file`（admin）
- cloud-init 模板：`GET|POST /api/cloud-init-templates` `GET|PUT|DELETE /api/cloud-init-templates/:id`
- Loki 日志（viewer 403）：`GET /api/monitor/loki/query` `GET /api/monitor/loki/labels`
- 公告：`GET /api/announcement`（公开读；写入口收敛在 `PUT /api/settings`）

## 项目结构

```
vmops/
├── main.go              # 入口：静态托管（四候选探测）/Deps 组装/任务管理器/会话注册表/种子数据/启动收敛
├── config/              # 环境变量配置（含 SEED_DIR）
├── database/            # GORM 连接与自动迁移（16 张表）
├── handler/             # HTTP 处理器，routes.go 按域收口注册
│                        #   vm/存储/网络/镜像/任务/会话/设置/监控/历史 + v3：docker*/appstore/apps/
│                        #   ai/crons/vm_files*/vm_credentials/image_market/vm_export/vm_recycle/
│                        #   toolbox/cloud_init_templates/announcement/container_terminal/loki
│                        #   param.go：paramID/parseID，路径参数主键统一解析（禁止直传 GORM）
├── middleware/          # JWT / OperatorMiddleware(RBAC) / NonViewerMiddleware / CORS / 审计 / 安全入口
├── model/               # GORM 模型（user/host/vm/image/audit/task/session + vm_grant/vm_credential/
│                        #   scheduled_task/cron_run/setting/alert/pool_meta/cloud_init_template/host_key）
├── service/
│   ├── virt/            # libvirt 封装（domain/spec/device/storage/network/snapshot/console/stats/clone/cloudinit）
│   │                    #   clone.go：增量克隆（backingStore）+ buildCloneSpec 纯函数（UUID/MAC 重生成）
│   │                    #   storage.go：ListBackingRefs（父卷→子卷依赖表，删卷守卫用）
│   ├── tasks/           # 异步任务队列（4 worker，有界入队 + 两层 recover；VM 全生命周期 + 镜像下载 + 商店安装/卸载等 executor）
│   ├── console/         # 会话注册表（WS 持有/强制断开/VNC 映射/过期清扫）
│   │                    #   conn.go：写锁串行化的 WS 包装（VM 终端与容器终端共用）
│   ├── dockerx/         # Docker CLI / Engine API 封装（容器/镜像/网络/卷/compose/统计，数组参数 + 统一超时）
│   ├── appstore/        # 声明式 compose 应用包（conf/appstore 20 应用 → data/apps，参考 1Panel 设计模式）
│   ├── apps/            # VM 内 SSH 脚本应用目录（10 个内置，幂等安装）
│   ├── vmssh/           # SSH 短连接封装（文件管理/VM 应用安装，命令经 ShellQuote）
│   ├── secretbox/       # AES-256-GCM 凭据加解密（标准库实现，主密钥运行时注入）
│   ├── cron/            # 计划任务（五字段 cron 解析 + 整分 tick 调度器 + 执行记录）
│   ├── monitor/         # Prometheus file_sd 服务发现（GenerateFileSD 纯函数 + 原子落盘）
│   ├── metrics/         # Prometheus 内建采集
│   ├── setting/         # 系统设置 KV（白名单校验 + 内存缓存）
│   └── vnc/             # VNC token 存储
├── conf/appstore/       # 应用商店内置应用包（data.yml 表单定义 + docker-compose.yml，磁盘可增改无需重编译）
├── scripts/             # init-db.sql / smoke.sh（E2E 回归）/ start-novnc.sh
├── deploy/              # prometheus.yml / alerts.yml / alertmanager.yml / grafana 看板与 provisioning / docker-compose
├── web/                 # Vue3 + Vite 前端（26 个视图：Dashboard(概览/监控双 tab)/Topology/VmList/VmDetail/向导/Console/
│                        #   DockerList(容器五 tab+终端+日志)/AppStore/AiChat/CronList/RecycleBin/Toolbox/CloudInitTemplates/
│                        #   Host/Image(三 tab 含镜像市场)/Storage/Network/Task/Audit+SessionList/Settings/UserList/Profile/Login）
│   ├── src/utils/format.js  # 状态文案/时间/尺寸/错误提取统一实现（收敛 10 余处重复）
│   └── dist/            # 构建产物，由后端托管（路由懒加载 + manualChunks：首屏 −50%）
└── docs/                # 设计 / 开发文档（含 api-contract / task-contract / ROADMAP v2/v3）
```

## 文档

详见 [`docs/`](docs/README.md) 目录：

- [00-选题依据与开题.md](docs/00-选题依据与开题.md) — 选题背景、研究内容、技术路线、进度安排（开题）
- [01-相关技术基础.md](docs/01-相关技术基础.md) — KVM/libvirt、Go/gin/GORM、Vue3、技术选型
- [02-系统需求分析.md](docs/02-系统需求分析.md) — 角色权限、功能/非功能需求（含 v3 运维面板扩展需求）
- [03-系统总体设计.md](docs/03-系统总体设计.md) — 架构、模块划分、请求流转（含任务/会话/RBAC 与 v3 双运行时扩展）
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
| `POST /api/networks/xml`、`PUT /api/networks/:name` | 接受调用方原始 XML 直接定义，无结构校验（域的 `PUT /api/vms/:id/xml` 已收口仅 admin 可用） | 仅 admin 可达 |
| `GET /metrics` | 未设置 `METRICS_TOKEN` 时公开（启动日志有提示）；设置后要求 Bearer/`?token=` 认证 | 生产建议开启令牌或以防火墙限制来源网段 |
| `POST /api/auth/login` | ✅ 已限流（同 IP 1 分钟 5 次失败锁定）+ 可设安全入口暗号（无暗号一律 404 伪装） | 残余：无验证码，可换 IP 分布式爆破 |
| CORS | `CORS_ORIGINS` 默认 `*`（release 模式下为 `*` 拒绝启动） | 生产需收敛为具体来源 |
| Web 终端 SSH | ✅ 主机密钥已按 TOFU 语义校验（首次连接记录指纹，指纹变化拒绝连接并提示风险，admin 可管理指纹清单） | 残余：TOFU 首连本身无法识别「首次即中间人」；目标已限私有网段 |
| 静态检查 | `go build` / `go vet` / `gofmt` 全过，`.golangci.yml` 配置就绪 | 深度 lint 覆盖以 vet 为主 |
| 状态字面量 | 全仓库仍有 7 处状态字面量未换成常量 | 一致性隐患，行为正确 |
| deploy 明文密钥 | `deploy/prometheus.yml`（remote_write BasicAuth）与 `deploy/alertmanager.yml` 含明文凭据入库 | 仓库可见，需轮换并改环境变量注入 |
| Grafana iframe | 匿名只读 + allow_embedding 下，viewer 在监控 tab 仍能看到看板画面（告警/file-sd 四端点已对 viewer 收权 403，看板画面属部署层收权） | viewer 可见监控看板 |
| 批 B/C 审计修复 | 已完成（详见 AGENTS.md 同名批次）：null→[]、WS 帧固定文案、回写/守卫检查、路径边界、RegisterImage 限池内、启动清扫、日志降噪、N+1、webhook token 轮换、前端清理。残留小项见 AGENTS.md | 大部分闭环 |
| 导入失败原因 | `POST /api/vms/import` 响应的 `errors` 数组前端 `VmList.vue` 未消费（只读 `imported`/`skipped`/`failed`） | 单台导入失败时用户看不到具体原因 |
| 多宿主机 | 多宿主机纳管空壳已砍除（`hosts.libvirt_uri` 字段已删），宿主机模块定位为「登记与状态采集」，虚拟化连接固定本机 `qemu:///system` | 跨宿主机虚拟化操作（`qemu+ssh://` 等）列为后续工作 |

## License

MIT
