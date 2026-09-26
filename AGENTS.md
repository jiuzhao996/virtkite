# vmops 项目开发指南（AGENTS.md）

本文件供 AI 编码助手（opencode / Claude Code 等）在操作本项目时自动加载，作为开发规范与上下文。

## 项目概况

- **后端**：Go + Gin + GORM，通过 `digitalocean/go-libvirt`（unix socket 直连 `qemu:///system`，无 CGO）封装 KVM 能力，封装层在 `service/virt/`
- **前端**：Vue 3 + Vite 5 + Element Plus + vue-router，位于 `web/`，构建产物 `dist/` 由 Go 后端托管
- **通信**：统一响应 `{code, message, data}`；`handler/response.go` 提供 `Success`/`Fail`/`ErrorResponse` 等

## 可用 Skills（必须优先使用）

**项目级（`vmops/.opencode/skills/`）**
- `vmops-libvirt`：**操作 libvirt 相关代码必读必循**。固化 `service/virt` 封装约定（连接获取/错误包装/状态映射/flag 常量），含 go-libvirt API 速查与陷阱。

**全局 Go 技能（`~/.agents/skills/golang-*`，共 46 个，samber/cc-skills-golang）**
- 写/改任何 Go 代码时自动触发：`golang-code-style`、`golang-error-handling`、`golang-concurrency`、`golang-context`、`golang-performance`、`golang-observability`、`golang-testing` 等。涉及对应领域时应主动加载并遵循。

**前端 Vue 技能（`~/.agents/skills/vue-best-practices`、`vue-router-best-practices`、`vite`，antfu 出品）**
- 写任何 Vue 组件/页面/路由时加载 `vue-best-practices`：Composition API + script setup、组件拆分、props/emits 数据流、composable 抽取。本项目默认 Vue3 组合式 API。

**Element Plus 组件技能（`~/.agents/skills/element-plus-*`，74 个组件）**
- 用到 el-button/el-table/el-form 等组件时，加载对应 `element-plus-<组件名>` 技能查准确 API（props/events/slots）。禁止凭记忆瞎写组件属性。

**前端规范（`~/.agents/skills/ui-ux-pro-max`）**
- 美化/新增前端页面时使用：B 端设计规范（8px 栅格、统一配色、去 AI 味），适配 Vue3 + Element Plus。

## 后端开发标准（强制执行）

1. **所有 libvirt 调用必须走 `service/virt` 封装层**，禁止在 handler/service 直接 `new libvirt.Libvirt` 或 `ConnectToURI`。
2. **virt 层错误包装必须用 `%w`**（`fmt.Errorf("中文描述: %w", err)`），保留错误链，handler 才能用 `errors.Is/As` 判断。禁止 `%v` 丢弃链条。
3. **handler 边界禁止泄漏内部错误**：统一走 `ErrorResponse(c, status, err)` 或 `ErrorWithMessage(c, status, "中文", err)`；禁止 `gin.H{"error": err.Error()}` 或 `"detail": err.Error()`。完整错误进日志（`LogError`），前端只收到中文友好消息。例外：`terminal.go`/`serial.go` 交互式控制台 WS 消息保留细节。
4. **连接管理**：`getConn()` 每次探活（`ConnectGetVersion`），断线自动 `Reset()` 重建，无需手动调用 Reset。
5. **状态映射**：libvirt 状态必须经 `StateToPlatform` 转换，只允许 `running / shut off / paused / error`，与 `vms.status` 一致。**写状态字面量一律用常量**：`model.VMStatusRunning/VMStatusShutOff/VMStatusPaused/VMStatusError`（业务层）、`virt.StatusRunning/StatusShutOff/StatusPaused/StatusError`（封装层）。注意是 `shut off`（空格）不是 `shut_off`。virt 刻意不 import model（避免把 GORM 拖进最底层封装层），两处各自定义 + 交叉引用注释，改一处必须同步另一处。
6. **flag 用命名常量**、**XML 用标准库 encoding/xml**、**注释用中文并注明 virsh 等价命令**。
7. 新增 virt 方法前先读 `service/virt/` 对应文件 + `vmops-libvirt` skill，保持风格一致。
8. **路径参数中的数值主键必须经 `paramID`/`parseID`（`handler/param.go`）解析后再交给 GORM，禁止直传 `c.Param`**。
   - 原理：GORM 的 `BuildCondition` 对「非纯数字字符串且未带占位参数」的内联条件，会把该字符串当作**原始 SQL 片段**直接拼进 `WHERE`。因此 `db.First(&vm, c.Param("id"))` 是注入点，`db.First(&vm, id)`（`uint`）才走参数化。
   - 用法：普通 handler 用 `paramID(c, "id")`（失败已写好 400「ID 参数非法」并 Abort，调用方直接 `return`）；WebSocket 处理器用 `parseID(c.Param("id"))`（不写 HTTP 响应，错误经 WS 帧回送——WS 已升级无法再写 JSON）。
   - 中间件对 GET 的放行意味着**最低权限角色也能到达这些查询**，注入面不是理论风险。
9. **WebSocket 写入必须经 `console.Conn`（`service/console/conn.go`），禁止直接对 `*websocket.Conn` 并发写。**
   - gorilla/websocket 明确禁止多 goroutine 同时写，违反直接 panic（`concurrent write to websocket connection`）；panic 发生在 goroutine 内部，**gin 的 Recovery 中间件拦不住**，整个进程被带走。
   - 本项目实际有多路写入方：SSH 桥（stdout / stderr / 主循环错误帧）、串口桥（guest 输出 / 错误帧）、会话注册表强制断开的 `CloseMessage`。
   - `Conn` 提供 `WriteMessage/WriteJSON/ReadMessage/Close/CloseWithReason`；写侧持写锁串行化，关闭后写返回 `console.ErrConnClosed`，`Close` 幂等。读侧由单一 goroutine 独占故不加锁。
10. **后台 goroutine 必须自带 `defer recover()`**（worker、定时清扫器、WS 转发协程等），**gin Recovery 只覆盖 HTTP 请求链，不覆盖任何自起的 goroutine**。recover 后：panic 值与 `debug.Stack()` 只进日志，用户可见字段只写中文友好文案。
11. **增量克隆必须靠卷 XML 的 `<backingStore>`，不是 `StorageVolCreateXMLFrom`。**
    - `StorageVolCreateXMLFrom`（等价 `virsh vol-clone`）做的是**全量数据拷贝**，产出的子卷**没有 backing file**（已实测：`qemu-img info` 无 `backing file` 行、`vol-dumpxml` 无 `<backingStore>` 节点）。用它实现「增量克隆」是名不副实。
    - 正确做法：`StorageVolCreateXML` + 卷 XML 里声明 `<backingStore><path>父盘</path><format type='qcow2'/></backingStore>`（等价 `qemu-img create -f qcow2 -F qcow2 -b 父盘 子盘`）。入口是 `storage.go` 的 `buildVolumeXMLWithBacking`，`buildVolumeXML` 是它 `backingPath=""` 的薄封装。
    - 验收方式固定为三条命令：子卷 `qemu-img info` 有 `backing file:`、`virsh vol-dumpxml` 有 `<backingStore>`、`qemu-img check` 报 `No errors were found on the image`。
12. **删卷前必须过 `shouldKeepVol` 三重守卫**（`service/tasks/vm_tasks.go` 的 `execDeleteVM`），禁止只按「是否在池路径下」判断。
    - 守卫一：池外文件（不在该池路径下）；守卫二：`images` 表登记的共享基镜像（「基于云镜像创建」走 `source_image_id` 是**直接引用不拷贝**）；守卫三：仍被子卷当 backing file 的增量克隆父盘（数据来自 `virt.ListBackingRefs(pool)`）。
    - 命中任一守卫即跳过并把中文原因写进日志 `[tasks] 保留卷（未删）` 与任务结果的 `kept_volumes` 数组。
    - 已知取舍：「先删父机、再删子机」顺序下父盘会残留为孤儿文件。**这是刻意选择**——宁可留一个垃圾文件，也不能损坏正在用的虚拟机磁盘。清理孤儿卷属后续工作，不要为了「删干净」把守卫拆掉。
13. **DB 写失败禁止静默吞掉**：凡是把状态/配置落库的地方，错误必须留痕，**禁止 `_ = db.Update(...)`、禁止连返回值都不接**（后者比 `_ =` 更隐蔽）。统一走 `service/dbx`：
    - `dbx.Persist(db, scene, fn)`：失败重试 3 次（退避 100/200ms）+ 每次留痕，三次全败打 `[persist] !!! 写入最终失败` 并返回 `%w` 错误，由调用方决定如何响应。
    - `dbx.PersistBestEffort(db, scene, fn)`：无返回值版，给「写失败无处可补」的点位（读接口的状态同步、审计时间等），**从 API 上杜绝 `_ =` 这种写法**。
    - 只接受**幂等等值写入**（`Update`/`Updates` 设固定值）。INSERT、累加、CAS、改外部系统不要传进去，helper 不保证幂等。
    - 语义上必须落库才成立的写入（如任务终态、控制台会话关闭）**不要**用 BestEffort，要显式暴露失败。
14. **手写进 `Update`/`Updates`/`Pluck` 的列名必须真实存在**：GORM 字段名与列名不总是一致——`VCPU` 字段的列名是 `v_cpu`。写错会报 1054，而调用处若只 `LogError` 就变成「libvirt 改了、DB 没改」的持久漂移，没人看得见（已在 `SetVcpu`/`vm_xml.go` 各发作过一次）。护栏测试 `model/columns_guard_test.go` 会扫全仓库并在写错时报错（已实证零误报），新增模型请同步该文件里的 `guardModels`。
15. **SSH 拨号必须经 `vmssh.NewOptions` + `vmssh.Dial`**，禁止自行组装 `ssh.ClientConfig` 或 `ssh.Dial`。
    - `vmssh.Options` 字段全部不可导出，**包外无法写 `vmssh.Options{Host: ...}` 字面量**；唯一构造入口 `NewOptions(recordedIP, host, port, user, password)` 内部强制过 `ValidateTarget` 白名单。
    - 这不是啰嗦：白名单校验原先分散在各个 handler「记得就调一次」，新增应用安装模块时漏了一行，平台直接变成免费跳板机（内网横扫/爆破/投递脚本，源 IP 全算在服务器上），而代码读起来完全正常、测试全绿。下沉进构造函数后，「绕过白名单外连任意地址」从「靠人记得」变成「编译期做不到」。
    - `NewOptions` 里**先补默认端口 22 再校验**，否则 `port=0` 会被 `ValidateTarget` 以「端口不合法」拒掉。
    - 明文口令不得落库：任务 payload 只存 `credential_id` 或边界加密后的密文；历史明文用 `scripts/purge-task-secrets`（默认 dry-run）清洗。
16. **虚拟机生命周期写操作必须先 `guardVMIdle` 再 `lockVM`**（启停/重启/暂停/恢复/删除/克隆/改规格/重 define）。
    - `h.guardVMIdle(c, vm.ID)`：查库确认该 VM 无 pending/running 任务，覆盖「HTTP 已返回但后台任务仍在跑」的时间窗；查询失败一律 fail-closed。
    - `h.lockVM(c, vm.ID)`：`service/vmlock` 的进程内非阻塞互斥，覆盖「同一瞬间并发进来的两个请求」（双开、误触、脚本重放）；冲突返回 409。必须 `defer release()`。
    - 两者**互补、不互相替代**：锁只在请求处理期间持有，后台任务窗口由 guardVMIdle 兜。
17. **禁止硬编码口令/密钥**：代码里不得出现真实口令字面量（含默认值）。配置一律从环境变量读，缺失即 fail-closed（如 compose 的 `${VAR:?}` 必填占位）。凭据加密主密钥用独立的 `CREDENTIAL_MASTER_KEY`，**不要复用 `JWT_SECRET_KEY`**——否则轮换 JWT 会让历史 VM 凭据集体解密失败，且报错伪装成「数据损坏」。种子管理员口令随机生成并一次性打印，不再硬编码。
## 品牌规范（鸢航 VirtKite，勿当装饰图误删）

- **正式名称**：鸢航 VirtKite（项目代号 vmops 仅存在于代码/目录/包名，UI 与文档一律用品牌名）。
- **logo 语义**：三道波浪线既是终端家目录符 `~` 也是海面，纸鸢掠浪而上；金色虚线"断而未断"=管理通道。virt 词根 + kite + 鸢航三关。
- **资产位置**：`web/public/brand/logo.svg`（主标青绿底：品牌青 `#2a9da5` → 深青 `#1f7e84` 渐变，白鸢 + 浅青风线 + 金牵线）/ `web/public/brand/mark-white.svg`（侧栏透明白鸢版）/ `web/public/brand/logo-teal.svg`（登录页透明版）/ `web/public/favicon.svg|favicon-32.png|favicon-16.png` / `branding/`（PPT 素材：virtkite-logo.svg 与 512/256/128 PNG、白底 light 版、横版组合 logo-horizontal）。
- **品牌色**：青绿 `#2a9da5→#217d83`（主色，交互/侧栏/登录背景同源）、金 `#ffd268`（点缀色，只用于牵线/鸢眼/飘带）。2026-09-25 用户拍板整体回归青绿，勿再改蓝。
- **已换标位置**：index.html（favicon 三件套+标题「鸢航 VirtKite · 基于 KVM 的轻量级私有云管理平台」）、Login.vue 品牌块、MainLayout 侧栏 brand 区（mark.svg + 「鸢航 VirtKite」）、Dashboard 平台信息卡、ConsolePage 水印（VirtKite console）、README 头部。

## 前端开发标准

1. 用 `ui-ux-pro-max` 规范：统一间距（8px 栅格）、配色（覆盖 `--el-color-primary`）、组件质感，禁止 emoji 当图标、禁止硬编码散落颜色。
2. 图标统一用 `@element-plus/icons-vue`（已在依赖中）。
3. **图标/emoji 裁定（本条为最终结论，与任何章节冲突时以本条为准）**：
   - **UI 里的图标一律用 `@element-plus/icons-vue` 组件**，写法 `<el-icon><Monitor /></el-icon>`，并在 `<script setup>` 里显式 `import`。`main.js` 虽已全量全局注册，但显式 import 让模板能看出图标来源，也为将来改按需引入留路。
   - **图标名必须查证后再写**：从 `web/node_modules/@element-plus/icons-vue/dist/types/components/index.d.ts` 确认导出存在。写错图标名不会导致 build 失败，只会静默渲染成空白。
   - **emoji 只允许出现在日志输出里**。`main.go` 启动日志的 ✅🚀👑👤🔑🧹⚠️ 是终端输出而非 UI 图标，**保留，不要清理**。
   - **例外（不是图标，保留字符）**：`●`／`○` 作状态圆点（`.term-status`、`.card-badge`）属纯装饰指示符，非 Unicode emoji 区段，无需换成图标组件——换成图标反而会破坏与文字的紧凑排版。
   - 落地情况：`ConsolePage.vue` 25 处、`Login.vue` 1 处已全部换成图标组件（其中 17 处原本是 emoji，另 9 处是被当图标用的几何字符 `▮ ▶ « » ←`），详见「控制台设计约定」。
4. 新增页面遵循现有目录结构（`views/`、`api/index.js`、`store/auth.js`）。

## Skills 适配清单（先读这里再加载 skill，豁免条款具有项目级效力）

**用法**：写代码前按本清单加载对应 skill；标记「豁免」的条款以本清单为准，不视为违规。本清单是项目级决策记录，skills 本体（全局共享）不修改不删除——保留反方条款作为选型论证的依据。

**豁免条款（已论证的项目决策，覆盖 skill 原文）**：
1. `golang-database` 的「用 sqlx/pgx、禁用 ORM」→ **vmops 钉死 GORM**。理由：管理平台 80% 数据操作是标准 CRUD，GORM 开发效率是主要矛盾；其"SQL 不可见"短板用 paramID 参数化收口 + 代码审查补偿。换库=全量重写零收益。
2. `golang-database` 的「禁止 AutoMigrate、用版本化迁移」→ **保留 AutoMigrate**。理由：单机部署、最大表 <1000 行、无多实例竞争，其反对的三个风险（锁表/竞争/无历史）均不成立；生产化演进（golang-migrate）列展望。
3. `golang-database` 的「禁用软删除类的魔法」→ **资产表（vms/hosts/images/users）保留 gorm.DeletedAt 软删**，过程表（tasks/audit_logs/console_sessions）不软删——边界已按"资产可恢复、过程只增不改"划清。

**适用（写/审对应领域代码前加载）**：golang-code-style、golang-error-handling、golang-naming、golang-concurrency（worker/WS goroutine）、golang-context、golang-security（注入面）、golang-safety、golang-observability、golang-testing、golang-refactoring、golang-structs-interfaces、golang-design-patterns、golang-performance、golang-database（除上述 3 条豁免，索引/NULL/注入/事务条款照用）、vue-best-practices、vue-router-best-practices、vite、element-plus-<组件名>（用到即查，禁凭记忆）、ui-ux-pro-max（UI 批次后审计）。

**不适用（本场景用不上，勿加载）**：golang-grpc/grpcio/graphql/swagger（无此类接口）、golang-google-wire/uber-dig/uber-fx/samber-do（依赖注入框架——项目手动构造注入）、golang-spf13-cobra/viper（CLI/配置框架——项目 gin + env）、golang-samber-lo/mo/ro/hot（工具库——项目未引入）、golang-samber-oops（项目错误约定是 fmt.Errorf+%w+中文文案，非结构化错误库）、golang-continuous-integration（无 CI）、golang-cli/gopls/pkg-go-dev/stay-updated（工具/资讯类，非代码规范）、golang-benchmark（性能优化批次才用）、golang-how-to/popular-libraries（入门/选型资讯）、golang-troubleshooting（排障时按需）、golang-modernize（大版本升级时用）、golang-documentation（docs/ 体系已自成约定）。


## 当前有效遗留清单（2026-09-26 逐项核实代码后收敛；勿在文档中宣称已解决）

- **原始 XML 直定义端点**：`POST /api/networks/xml`、`PUT /api/networks/:name` 接受原始 libvirt XML；`PUT /api/vms/:id/xml` 已收权 admin（2026-09-26 routes.go 核实在位）
- ~~`/metrics` 公开~~ **已核实为陈旧信息（2026-09-26）**：.env 自 v3.3 起即配置 METRICS_TOKEN，实测无 token 401 / 带 token 200（prometheus.yml 的 vmops job 一直在发配对凭证）
- **JWT 仍支持 `?token=` 查询参数传递**（`middleware/jwt.go`，WS 无法设 Header 的设计取舍；会进代理日志/Referer/审计）
- **历史 `tasks.payload` 明文待人工清洗**：`scripts/purge-task-secrets` 未执行，执行前须先 mysqldump 单表备份
- **多宿主机是空壳**：`virt.New` 固定 `qemu:///system`，宿主机模块仅登记与状态采集，跨宿主机操作列论文展望
- **`validateSSHTarget` 放行 IPv6 ULA 但错误文案只写 IPv4 私有网段**（文案与实现不对齐）
- **`.golangci.yml` 为 v1 schema**：升级 v2.x 会因字段改名报错，装 v1.64.8（`~/go/bin`，源码自建）

已核实**解决**（勿再列为待办）：登录限流（`handler/auth.go` loginLimiter）、CORS release 禁 `*`（main.go 启动校验）、SSH 主机密钥 TOFU（全仓库 InsecureIgnoreHostKey 归零）、孤儿卷清理闭环、golangci-lint 已装、ImportVMs errors 数组前端已消费。

## 历史批次记录（已外迁）

P0 稳定性 → P1 安全 → P2 正确性 → P3/P4 → UX → 监控栈 → 多宿主机砍除 → 冗余清理 → RBAC → 资产授权 → Skills 审计 → 批 B/C → v3.3 → v3.4 → v3.5 → 胰腺癌级审计 → IA 精简，全部批次记录见 **`docs/devlog-批次记录.md`**。排障/考古时按需查阅；本文件只保留现行规范，勿再往这里追加批次流水。