# 鸢航 VirtKite v3.3 优化升级规划（1Panel 对标学习 + 全站 UI/UX 审计修复 + 论文对齐）

> **状态（2026-09-20）**：规划即施工依据，本批一次性实施完成。回退锚点 `0e28136`（v3.2 完成态）。
> **两条输入**：①全站 25 页面 UI/UX 审计（约 130 条发现，四个审计代理并行产出，全部有 文件:行号 依据）；
> ②1Panel v2 对标研究（demo 站实测 + GitHub 源码研究 + 官方文档，研究结论见 §2）。
> **用户口径**：功能对齐不抄样式——后端实现可参考 1Panel 源码，前端学交互模式、变着花样落地；答辩讲「参考 1Panel/Portainer 设计模式」是加分项。

## §1 许可证与借鉴纪律（先说清楚）

- 1Panel 是 **GPLv3**。借鉴代码片段时在注释注明来源（`// 参考实现: 1Panel agent/app/service/container.go (GPLv3)`）；整体架构与交互模式（计数 chips、跟随日志、三档确认）属通用设计模式，不受版权约束。
- 本项目不引入 1Panel 的 cosy UI 组件库、不改品牌视觉（青绿主色/金色点缀/鸢航 logo 不动）。

## §2 1Panel 研究结论（十条可借鉴要点）

| # | 1Panel 做法（源码证据） | 我们的对标动作 |
|---|---|---|
| 1 | 容器概览计数 chips：`el-tag plain + 零值隐藏`，数字可点击跳列表（`views/container/dashboard/index.vue`） | DockerList/TaskList 状态筛选带计数；Dashboard 统计卡已可点击（已做） |
| 2 | 日志查看器 = xterm 只读 + 跟随 checkbox + 行数下拉 + 时间戳开关 + 下载 Blob + 贴底判断（`components/log/container/index.vue`） | DockerList 日志抽屉补 跟随（2s 轮询版）/下载/tail 行数 三件（SSE 流式列为后续） |
| 3 | TableSearch/TableRefresh/TableSetting 三件套；自动刷新间隔下拉 + localStorage 记忆（`components/table/TableSetting.vue`） | 列表页自动刷新开关（间隔固定 10s 起步，localStorage 记忆开关态） |
| 4 | 危险确认三档：MessageBox → 影响列表 DelDialog → **输名称 ConfirmDialog**（`components/confirm-dialog/index.vue`） | 我们已有「删 VM 输名称」最高档；本批统一补红色确认钮（第二档轻量化） |
| 5 | 终端设置：字号/字体/主题即改即预览 + persisted store（`views/terminal/setting/index.vue`） | ConsolePage 加 A-/A+ 字号调整（持久化到 localStorage） |
| 6 | 计划任务列表列：cronSpec + 上次执行时间 + 记录主从布局；cron 解析用 `robfig/cron/v3` | 后端补 `GET /api/crons/preview` 下次执行预览；前端接入 |
| 7 | 监控图表时间范围：datetimerange + shortcuts，全局+单图双层 | VmDetail/Dashboard 曲线加 5m/30m/1h/6h 切换（后端 minutes 参数现成） |
| 8 | 任务中心 = badge 抽屉 + ComplexTable + 日志查看器 | 我们已有任务铃（本批修不可点击问题）；抽屉化列后续 |
| 9 | 概览 = CardWithHeader 计数 + 「已占用/可回收才亮清理按钮」磁盘治理 | StorageList 池卡补 mini 用量条（口径 caveat 保留） |
| 10 | 表单编辑统一 DrawerPro + 命令式 acceptParams | 记录为后续规范，本批只统一 AppStore 双形态安装入口（可选） |

**官方文档补充**（概览页手册）：首页 = 服务器状态 + 快捷入口（按权限过滤）+ 已安装应用。我们的 Dashboard 概览 tab 结构与此同构，已达标。

## §3 审计结论摘要（130 条 → 四类）

1. **演示翻车级 5 条**（P0 级）：ConsolePage CSS 选择器被注释焊死、DockerList 轮询清空勾选、全局搜索会话内不刷新、Element Plus 无中文 locale（确认框英文 OK/Cancel）、控制台背景星星每秒瞬移。
2. **全局一致性债**：刷新按钮 4 种形态、危险确认红钮仅 1 处有、页头 h2/h3 两派、硬编码颜色散落、3 处错误提示丢后端中文原因、4 处死代码。
3. **v3 新页面未对齐老页面惯例**：UserList/CloudInitTemplates 无刷新钮、TaskList 轮询整页闪 loading、AiChat 无 markdown/停止生成。
4. **运维惯例缺口**（1Panel 对标即上表）：日志查看器、自动刷新、计数 chips、IP 复制、时间范围、排序等。

## §4 批次总览（本批全部实施）

| 批次 | 优先级 | 内容 | 范围（文件所有权，防并行冲突） | DoD |
|---|---|---|---|---|
| **F1 核心线修复** | P1 | 翻车修复+核心页打磨 | `main.js` `MainLayout.vue` `Dashboard.vue` `VmList.vue` `utils/format.js` `global.css` | build 过 + 逐条修复清单落地 |
| **F2 向导/控制台/登录** | P1 | CSS 焊死/星星/剪贴板/全屏 + 向导状态机 | `ConsolePage.vue` `CreateVmWizard.vue` `Login.vue` | 同上 |
| **F3 容器线** | P1 | 勾选保留/日志查看器/自动刷新/商店兜底 | `DockerList.vue` `AppStore.vue` `ImageMarket.vue` | 同上 |
| **F4 运维页** | P1 | 任务闪屏/审计导出/AI 页归 F5a？否——F4=TaskList/Audit/Session/Cron/Monitor/Topology | `TaskList.vue` `AuditList.vue` `SessionList.vue` `CronList.vue` `Monitor.vue` `Topology.vue` | 同上 |
| **F5a AI+管理页** | P1 | markdown/停止生成/设置确认/用户页 | `AiChat.vue` `Settings.vue` `Profile.vue` `UserList.vue` `CloudInitTemplates.vue` `Toolbox.vue` `RecycleBin.vue` | 同上 |
| **F5b 基础设施页** | P1 | 确认框/刷新统一/卡片按钮统一 | `StorageList.vue` `ImageList.vue` `NetworkList.vue` `HostList.vue` | 同上 |
| **B1 后端安全+质量** | P2+P3 | SSH TOFU 主机密钥 / XML 收权 / 文案 / cron 预览 / lint / 死字段 | 全部后端 Go（前端代理不动 Go） | `go vet` + `go test -race` 全绿 |
| **D1 论文对齐·上** | P0 | README 功能清单/测试数 + 需求/总体设计补 v3 + 演示脚本 | `README.md` `docs/02` `docs/03` `docs/09` | 文档与代码事实一致 |
| **D2 论文对齐·下** | P0 | 数据库设计（v3 新表）+ 详细设计（v3 模块）+ 测试章节数据 | `docs/04` `docs/05` `docs/06` | 同上 |

## §5 P1 演示防翻车——修复清单（U1 全部 + U2/U3 择要）

### U1 翻车级（必修，全落）
1. ConsolePage CSS 选择器焊死：补 `{ vertical-align: -0.15em; margin-right: 4px; }` 让 `.topbar` 独立成规则，删 `.term-logo` 死选择器（`:697`）
2. DockerList 勾选保留：表格 `row-key="ID"` + 选择列 `reserve-selection`（`:48`）
3. 全局搜索改 `visible-change` 时拉取，删 `searchLoaded` 常驻缓存（MainLayout `:221`）
4. main.js 配 Element Plus zh-cn locale（全站英文按钮根治）
5. 星星坐标 setup 内一次性生成（ConsolePage `:122`）

### U2 一致性（全落）
- 刷新按钮统一 default 描边 + `:loading`（13 处三形态收敛）；页内主操作唯一实底 primary
- 危险确认统一 `confirmButtonClass: 'el-button--danger'`（删卷/删池/清理/断开/卸载/清空/彻底删除）
- 页头标题统一 h2（嵌入组件除外）；错误提示统一 `errMsg(e, 兜底)`（HostList/TaskList/SessionList 三处漏网）
- CronList「立即运行」success→primary plain（规范回潮）；Monitor severity tag dark→light
- 硬编码颜色走 token（VmList 状态胶囊/live-tag、Dashboard 轴色图例、ConsolePage 浅色区择要、AppStore 错误气泡）
- 死代码清理：Toolbox `loadError` 接线、`diskLoading` 消费、Dashboard `host-card` 死类、global.css 重复规则

### U3 交互补齐（全落）
- **cloud-init 模板接进侧栏**（运维组，requiresOperate 档）——整条功能当前 UI 不可达
- viewer 不可勾选 VM 卡片（checkbox + selected 高亮整体 `v-if="canOperate"`）
- VmList 筛选空态改「无匹配虚拟机 + 清除筛选」；`bulkAction` 目标 = 勾选 ∩ 当前筛选
- TaskList 轮询走 silentRefresh（照抄 SessionList 范式）；「清理已完成」文案改「清理本页已完成 (N)」
- Dashboard 非 admin 隐藏「用户」统计卡；echarts legend/轴色走 `cssVar()`；性能表 CPU/内存列 sortable + 名称列跳详情
- VmDetail busy 互斥补全（重启/删除/控制台 `:disabled="!!busy"`）；曲线卡加 5m/30m/1h/6h 切换（后端 minutes 现成）
- 向导：提交中禁上一步、失败改常驻 alert（可复制）、必填项 rules + 名称正则/查重、安装卡 3 列
- ConsolePage：xterm 剪贴板（Ctrl+Shift+C/V + 粘贴按钮）、全屏按钮、折叠侧栏后 fit、暂停态文案、VNC 重连直连、字号 A-/A+
- AiChat：markdown 渲染（marked + DOMPurify，已装依赖）、停止生成按钮、代码块复制、贴底阈值

### 1Panel 对标增补（本批做的部分）
- DockerList 日志抽屉：跟随开关（2s 轮询+贴底判断）/下载（Blob .log）/tail 行数下拉（100/200/500/1000）
- DockerList：自动刷新开关（localStorage 记忆）、镜像 tab 搜索、状态 chips 带计数、状态列汉化、「清理未使用」改名「清理悬空镜像」、镜像创建时间汉化
- AppStore：Docker 不可用页顶 alert + 禁用安装、选 VM 回填 IP、分类控件统一 radio-button、应用搜索框
- VmList 卡片 IP 点击复制；AuditList 导出 CSV（当前筛选拉全量 Blob）+ 日期快捷项；UserList/CloudInitTemplates 补刷新钮
- CronList：下次执行预览（消费 B1 的 `GET /api/crons/preview`）；TaskList/AuditList 表格补筛选/搜索
- ImageMarket：onMounted 扫 running 任务恢复 downloading 态（防重复发起下载）

## §6 P2 安全收口（B1，口径已核实）

1. **SSH 主机密钥 TOFU**：新增 `host_keys` 表（host+port 唯一，存 fingerprint/key_type）；`service/vmssh` 抽出统一拨号助手，`handler/terminal.go` 与 VM 应用安装共用；首连记录指纹（TOFU），指纹变化拒绝连接并给中文风险提示；admin 清单/删除端点 `GET|DELETE /api/vms/ssh-host-keys`（`requireAdminRole` 口径）。**注意**：两处 `InsecureIgnoreHostKey`（`terminal.go:113`、`vmssh.go:68`）全部收编，勿留旁路。
2. **XML 直定义收权**：核实结论——`OperatorMiddleware` 已限定 operator 只写 `/api/vms` 前缀，networks 的写操作天然 admin-only；**真实缺口只有 `PUT /api/vms/:id/xml` 对 operator 开放**。修复：该 handler 加 `requireAdminRole` 二次收口（与 vm_grant 同模式），middleware 测试补 operator 403 用例。
3. **IPv6 ULA 文案**：`validateSSHTarget` 错误文案补「或 IPv6 ULA（fd00::/8）」，与实现对齐。
4. **cron 下次执行预览端点**：`GET /api/crons/preview?expr=`，优先复用 `service/cron` 既有解析；无则引 `robfig/cron/v3`（1Panel 同款），返回 `{next:["2006-01-02 15:04:05"×5]}`；带测试。

## §7 P3 工程质量（B1，时间盒 1h，有余力再做）

- golangci-lint v1.x 二进制经代理安装到 /tmp 跑一轮：只修 errcheck/clear 级别真问题，其余记录在案不重构
- `Host.DiskGB` 死字段删除；`StartVM/RestartVM` 响应 data 键与前端核实（前端不消费则维持现状并记录）
- **不做**：Element Plus 按需引入（大改，另批）、大组件拆分（毕设体量决策）

## §8 P0 论文与演示材料对齐（D1+D2）

| 文件 | 缺口 | 动作 |
|---|---|---|
| README.md | v3 功能 0 提及；测试数写 142/7 包（实际 197/17） | 功能清单补 v3 全模块；测试数据重数重写；技术栈表补 Docker 管理/Loki |
| docs/02-系统需求分析 | 容器/商店/AI/计划任务/凭据零覆盖 | 追加「v3 扩展需求」章（对照 1Panel 差距分析→需求推导，正好用 10-参考项目研究 呼应） |
| docs/03-系统总体设计 | 同上 | 补 v3 模块架构（dockerx/appstore/cron/secretbox/ai）与分层图文字版 |
| docs/04-数据库设计 | 缺 v3 新表 | 从 `model/*.go` 全量核对：补 vm_grants/vm_credentials/scheduled_tasks/cron_runs/system_settings/alerts/pool_meta/cloud_init_templates/host_keys 等 |
| docs/05-详细设计与实现 | v3 模块零覆盖 | 按模块追加实现要点（应用商店声明式包/容器管理/凭据加密/计划任务/回收站/文件管理双通道） |
| docs/06-系统测试与验证 | 测试数据过期 | 重数更新 + 补 v3 模块测试描述 + UI 审计/修复一轮（本批素材） |
| docs/09-答辩演示脚本 | v3 亮点未编入 | 演示动线重排（建机→商店装应用→容器八 tab→AI 助手→审计）；补 stu 账号提示行；补故障预案（Grafana 未启动/AM 不可达话术） |

纪律：以代码为准绳重数重写；**勿宣称未做的事**（AGENTS.md 在案遗留照抄遗留清单）；GPLv3 借鉴在 10-参考项目研究 章注明。

## §9 记录在案、本批不做（防 scope 失控）

应用 logo 图标映射、应用详情抽屉、安装实时日志流（SSE）、容器创建表单（R8）、compose 创建器、卷大小列、告警静默/认领、任务抽屉化、通知中心、面包屑、暗色主题、Element Plus 按需、骨架屏、ImageMarket 下载取消。全部列论文「展望」或后续批次。

## §10 验收与提交

1. 每个前端代理私有 `dist-check-*` 目录自验构建；主线程最终跑真实 `npm run build`
2. `go vet ./...` + `go test -race ./...` 全绿；后端 `go build -o vmops .` 重启（SERVER_MODE=debug，source .env）
3. 浏览器冒烟：登录→仪表盘→建机向导→控制台（CSS/星星/剪贴板）→容器八 tab（勾选保留/日志跟随）→AI（markdown/停止）→审计导出
4. 三笔本地提交：`feat(v3.3): P1 演示防翻车——全站 UI 审计修复（U1-U3+1Panel 对标增补）`、`feat(v3.3): P2 安全收口 + P3 工程质量`、`docs(v3.3): P0 论文对齐 + v3.3 规划文档`；AGENTS.md 补批次记录
