# 鸢航 VirtKite v3.1 施工级规划（✅ 全部批次已落地）

> **状态（2026-09-19 凌晨）**：批次 0 + M1 + A/B/C/D/E/G/I/K/L/N/O/P + M3/M4 全部完成，本地提交至 `17911f6`，**未推送**。
> 平台运行中；验收入口见各批次 DoD；回退锚点 `47276ad`（v3 前）或 `aced919`（v2 完成态）。
> 未做（刻意）：M2 PageShell、M5 页内导航组件化、F 概览改版（纯 UI 打磨，记录在案视后续）。

> 前置文档：ROADMAP-v3.md（功能批次 A-M 的功能级定义）。本文档回答四个问题：
> **先做什么（依赖与优先级）、做到什么程度算完成（DoD）、哪里会翻车（风险与预案）、每一批在答辩里怎么用（演示映射）**。
> 实施纪律：每批次开工前先在其卡片上补「批次设计小节」（接口/数据模型/交互稿），设计确认后写码；每批完成 = build + test -race 全绿 + 本地 commit。

## -1. 批次 0：架构微调（v3.0 前置，约 0.5h）

架构审视结论（v3 十五批次套入现有架构模拟）：分层/权限/任务框架/双通道设计全部经受住考验**不用动**；需要微调两处——

- **①main.go 路由拆分**：新增 `handler/routes.go`，每模块一个 `RegisterXxxRoutes` 函数；main.go 只留 `handler.RegisterAll(api, deps)` 一行（v3 落地后会突破 700 行，先拆先受益）
- **②依赖注入统一**：新增 `handler.Deps` 结构体（DB/Virt/Tasks/Sessions/SettingMgr 一次组装），handler 构造统一 `NewXxxHandler(deps)`
- 推迟项（v3 收尾一次性做）：前端 views 目录按域分组重组、api/index.js 按域拆分

## 0. 总览：15 个批次与两轮节奏

| # | 批次 | 类型 | 估时 | 依赖 |
|---|---|---|---|---|
| M1 | 设计 tokens 覆盖层 | UI | 1h | 无 |
| B | AI 运维助手 | 功能 | 3h | 无 |
| I | SSH 凭据托管 | 功能 | 2.5h | 无 |
| A | 应用商店 v2 | 功能 | 4h | I（体验依赖）· compose v2.29 已确认 ✓ |
| M3 | 商店卡片交互 | UI | 1h | A |
| C | 计划任务增强 | 功能 | 1.5h | 无 |
| G | 日志栈（Loki） | 功能 | 2h | A（以 compose 包形态落地） |
| D | 安全入口 + 密码策略 | 安全 | 1.5h | 无 |
| E | 工具箱 | 功能 | 1.5h | 无 |
| N | 回收站（新） | 功能 | 2h | 无 |
| O | VM 标签/分组（新） | 功能 | 2h | 无 |
| K | 拓扑可视化 | 功能 | 1.5h | O（分组节点更有看头，可选） |
| H | 云镜像市场 | 功能 | 2h | 无 |
| J | VM 导出/导入 | 功能 | 2h | 无 |
| L | cloud-init 模板 | 功能 | 1.5h | 无 |
| F/M2/M4/M5 | 概览改版/PageShell/统计卡/页内导航 | UI | 各 0.5-1h | M1 |

合计约 30h 有效工时 → 分三个里程碑（每里程碑一次本地 commit + 可验收）。

## 1. 依赖关系（文字版 DAG）

```
M1（tokens）──────────────→ 所有 UI 项受益（先做）
I（SSH 凭据托管）─────────→ A（商店安装免密）→ M3（商店卡片交互）
A（compose 包机制）──────→ G（Loki 以 compose 包交付）
O（VM 标签）─────────────→ K（拓扑图分组着色）
其余批次互相独立，可任意穿插
```

关键结论：**I 必须在 A 之前**（否则商店安装每次都要手填密码，体验断裂）；M1 全局先行。

## 2. 优先级矩阵（价值 × 成本）

- **高价值低成本（先做）**：M1、B、C、D
- **高价值高成本（核心投入）**：I、A+M3、G
- **中价值中成本（填充）**：N、O、E、K、H
- **低优先（有余力）**：J、L、F/M2/M4/M5

## 3. 里程碑切分

| 里程碑 | 内容 | 出口标准 |
|---|---|---|
| **v3.0 快速见效包** | **批次 0** + M1 + B + C + D | 架构微调完成；AI 助手可聊天、计划任务带历史与保留份数、安全入口生效；test -race 全绿 |
| **v3.1 三大件** | I + A + M3 + G | 声明式商店安装第一个容器应用；Loki 日志可查；SSH 免密体验 |
| **v3.2 打磨包** | N + O + E + K + H + J + L + F/M2/M4/M5 | 视余量裁剪，完成一项验一项 |

## 4. 批次卡片（DoD / 风险 / 演示映射）

### M1 设计 tokens 覆盖层
- DoD：global.scss 新增品牌九档 primary 梯度 + 表格/卡片/对话框/帮助文字规范；全站无回归
- 风险：低。九档色阶需从 #2a9da5 算梯度（可用工具生成后微调）
- 演示：无需单独讲，整体质感提升

### B AI 运维助手
- 设计要点：settings 三键（ai_base_url/ai_api_key/ai_model，掩码）；`POST /api/ai/chat` SSE 流式代理；system prompt 注入平台摘要（VM 总数/运行数/容器数/最近告警，`with_context=true` 时附 VM 清单）
- DoD：配置 Key 后聊天可流式回复；带上下文模式能回答「几台 VM 在跑」；viewer 403；未配置 Key 400 + 引导
- 风险：SSE 经 gin 转发的断连处理；不同厂商兼容性（只承诺 OpenAI 兼容格式）
- 演示：答辩杀手锏——现场问「我平台几台虚拟机？哪台在跑？」

### I SSH 凭据托管
- 设计要点：新表 vm_credentials（vm_id 唯一 + AES-GCM 密文 + 盐）；密钥派生 = HKDF(JWT_SECRET, 随机盐)；文件管理/商店安装请求新增 `use_saved=true` 时后端自行取凭据（密码不出库）
- DoD：保存凭据 → 商店安装/文件管理免密可用；密文落库（库里看不到明文）；权限仅 admin/operator
- 风险：密钥派生若 JWT_SECRET 轮换则旧密文失效——设计上接受（重新保存即可），文档注明
- 演示：与 A 联动演示「点一下就装好了」

### A 应用商店 v2
- 设计要点（已细化，见 v3 文档批次 A）：conf/appstore embed + data.yml(formFields) + ${VAR} → .env → docker compose up -d；app_compose_install/uninstall executor；已装列表读 data/apps + compose ps
- DoD：内置 6 应用；nginx 从安装到浏览器可访问全流程演示；卸载保留数据目录；表单校验（端口范围/必填）
- 风险：compose 项目名冲突（用 <key>-<时间戳> 项目名）；端口占用（安装前 `ss -tln` 预检冲突端口）；**网络拉镜像慢**——脚本/文档注明可配镜像加速
- 演示：nginx 安装 → 自动跳转访问页 → 卸载。连贯 2 分钟

### G 日志栈（Loki）
- 设计要点：compose 包（loki + promtail 挂 /var/log 与 docker.sock 标签发现）；监控中心加「日志」tab（Grafana Explore iframe 或 /api/ai 类似的 LogQL 代理查询）
- DoD：商店一键装 Loki 栈；Grafana 能查到宿主机与容器日志；监控中心有日志入口
- 风险：promtail 权限（容器内读 /var/log 需挂载）；Grafana Explore 嵌入高度适配
- 演示：与监控中心连成「指标+日志」一体，收尾讲可观测性四件套

### D 安全入口 + 密码策略
- 设计要点：settings 加 `security_entrance`（随机后缀，可改）；登录路由挂校验中间件（后缀不符 404）；改密/建用户密码复杂度校验函数（长度≥8 + 三类字符，可配置开关）
- DoD：无后缀 /#/login 404；带后缀正常；弱密码被拒；SSH 应急说明文档（抄 1Panel 应急思路）
- 风险：前端 hash 路由与后缀的拼接方式（`/#/login/<suffix>` 或 query 参数，设计时定）；忘记后缀的兜底文档必须写
- 演示：现场展示扫描器打 /login 全 404

### N 回收站（新）
- 设计要点：删除 VM 已是软删——补「回收站」页（列出软删 VM + 恢复按钮 = 清 DeletedAt + 重新 define 域）+ N 天后物理清理（挂计划任务动作 `purge_deleted`）
- DoD：删 VM → 回收站可见 → 一键恢复（域重新 define、卷未删前提）；计划任务定期物理清理
- 风险：恢复时卷可能已被守卫保留/清理——恢复前校验卷存在，缺失则提示部分恢复
- 演示：删错机器的后悔药，教学场景强共鸣

### O VM 标签/分组（新）
- 设计要点：vms 加 `tags` JSON 数组列；列表页标签筛选 + 颜色可配；拓扑图按标签着色
- DoD：打标/筛选/按组统计（仪表盘加分组分布）
- 风险：低
- 演示：15 台 VM 按用途分组（大数据组/云计算组/监控组），一眼看清

### 其余批次
- E 工具箱 / K 拓扑 / H 镜像市场 / J 导出导入 / L 模板：按 v3 文档既有定义，开工前补设计小节
- 风险共通：J 大文件下载/上传的流式处理；H 依赖外网镜像源可达性（内置多镜像源备选）

## 5. 答辩演示映射（每个批次在演示脚本里的位置）

| 功能 | 放进 docs/09 的位置 | 时长 |
|---|---|---|
| 仪表盘（容器数/分组分布） | 开场概览 | 30s |
| AI 助手（环境感知问答） | 放在演示高潮位（倒数第二项） | 2min |
| 应用商店 v2（nginx 装到可访问） | 与 AI 并列高潮 | 2min |
| VM 文件管理（离线挂载） | 讲 guestmount 技术点时 | 1.5min |
| 回收站 | 讲「保留优先」设计立场时 | 1min |
| 计划任务+安全入口 | 运维与安全章节 | 1.5min |
| 日志栈/可观测性四件套 | 监控中心章节收尾 | 1min |

## 6. v3.2 增补批次（第二轮：对标 1Panel 容器模块逐页拆解 + 应用商店 263 应用全量调研）

用户结论：容器管理做浅了（1Panel 是 8 tab：容器/编排/镜像/网络/卷/仓库/模板/设置）、应用太少（1Panel 263 个 vs 我们 6 个）、镜像体系需理顺（市场=获取渠道，镜像库=资产仓库，并入镜像管理页做双 tab）。

### 批次 R：容器管理强化（八 tab 化）
后端 dockerx 扩展 + handler/docker.go 扩展 + 前端 DockerList 改 tab 页（容器/编排/镜像/网络/卷）：
- R1 容器详情：docker inspect JSON 只读抽屉
- R2 容器终端：WebSocket + PTY + docker exec -it（复用 console.Conn 写锁与 terminal 桥模式）
- R3 日志增强：--since/--tail/-f follow 流式
- R4 资源占用列：docker stats --no-stream 轮询（CPU%/内存% 实时列 + 悬浮详情）
- R5 批量启停删 + 7 状态筛选 + 名称搜索
- R6 镜像拉取：docker pull（异步任务，支持加速地址）
- R7 清理：image/container prune（返回删除数与回收空间）
- R8 创建容器表单：端口/挂载/环境变量/资源限制/重启策略 → docker run 参数映射
- R9 网络管理：network ls/create/rm
- R10 卷管理：volume ls/create/rm/prune
- 编排列表：compose ls + 逐项目启停（与应用商店联动）
- 不做：仓库凭据管理、镜像构建、daemon.json 配置页、IPv6 细项

### 批次 S：应用扩充（6 → 20）
调研筛选的 14 个新增 compose 包（已剔除 GitLab/HA 等资源怪兽，清单含真实镜像 tag/端口/env）：
PostgreSQL、MinIO、Gitea（官方镜像）、MongoDB、RabbitMQ（management）、Open WebUI、n8n、code-server、it-tools、Jenkins（LTS）、Halo、Memos、Jellyfin、qBittorrent——参照 1Panel 真实 data.yml 风格（密码 random、端口 paramPort）

### 批次 T：镜像体系理顺
- 云镜像市场并入镜像管理页第二 tab（「我的镜像」/「镜像市场」），下载完成原地刷新
- ImageMarket 独立页与路由撤除；两处引导文案（市场=获取渠道，镜像库=资产仓库）

### 批次 I+：凭据打通消费端
- 文件管理/应用商店 SSH 安装表单加「使用已保存凭据」开关（后端 ResolveFor 取用，明文不出服务端）

## 7. 技术风险与预案汇总

| 风险 | 预案 |
|---|---|
| compose 拉镜像慢/失败 | 应用包注明可配镜像加速；安装任务错误透出镜像名 |
| guestmount 磁盘锁/权限（已踩） | 拒绝运行中 VM；读取一律 sudo -n（已解决模式复用） |
| AI SSE 断连 | 前端 EventSource 异常兜底提示；后端 context 透传超时 |
| AES 密钥轮换致凭据失效 | 接受 + 重新保存；文档注明 |
| 端口占用 | compose 应用安装前 `ss -tln` 预检 |
| 全站 token 改造回归 | M1 纯覆盖层不改 DOM 结构；改后全页面截图回归 |
