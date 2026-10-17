# vmops 项目开发指南（AGENTS.md）

本文件供 AI 编码助手（opencode / Claude Code 等）在操作本项目时自动加载，作为开发规范与上下文。

## 项目概况

- **后端**：Go + Gin + GORM，通过 `digitalocean/go-libvirt`（unix socket 直连 `qemu:///system`，无 CGO）封装 KVM 能力，封装层在 `service/virt/`
- **前端**：Vue 3 + Vite 5 + Element Plus + vue-router，位于 `web/`，构建产物 `dist/` 由 Go 后端托管
- **通信**：统一响应 `{code, message, data}`；`handler/response.go` 提供 `Success`/`Fail`/`ErrorResponse` 等

## 可用 Skills（必须优先使用）

**项目级（`vmops/.opencode/skills/`）**
- `vmops-libvirt`：**操作 libvirt 相关代码必读必循**。固化 `service/virt` 封装约定（连接获取/错误包装/状态映射/flag 常量），含 go-libvirt API 速查与陷阱。

**全局 Go 技能（`~/.agents/skills/golang-*`，现 21 个，samber/cc-skills-golang）**
- 写/改任何 Go 代码时自动触发：`golang-code-style`、`golang-error-handling`、`golang-concurrency`、`golang-context`、`golang-performance`、`golang-observability`、`golang-testing` 等。涉及对应领域时应主动加载并遵循。
- 2026-10-01 按用户拍板删减 25 个与本项目无关的（grpc/graphql/swagger/wire/dig/fx/samber 工具族/cobra/viper/cli/gopls/benchmark/how-to 等，原 46 → 21）。

**前端 Vue 技能（`~/.agents/skills/vue-best-practices`、`vue-router-best-practices`、`vite`，antfu 出品）**
- 写任何 Vue 组件/页面/路由时加载 `vue-best-practices`：Composition API + script setup、组件拆分、props/emits 数据流、composable 抽取。本项目默认 Vue3 组合式 API。

**Element Plus 组件技能（`~/.agents/skills/element-plus-*`，现 47 个组件）**
- 用到 el-button/el-table/el-form 等组件时，加载对应 `element-plus-<组件名>` 技能查准确 API（props/events/slots）。禁止凭记忆瞎写组件属性。
- 2026-10-01 删减 27 个全仓库零使用的组件技能（cascader/tree/transfer/carousel/tour/watermark/slider/rate/color-picker 等，原 74 → 47）；timeline/skeleton/avatar/space/image/backtop/autocomplete 因告警中心与视觉改版可能用到而保留。

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
18. **砍 API 路由时必须连带清扫「只被该路由调用」的下层方法，并同步文档。**
    - 路由删掉后，`service/virt`、`service/*` 里只服务该路由的方法即成死代码，而 **golangci-lint 的 `unused` 只报非导出符号——导出方法无人调用它不报**（`service/virt.UpdateNetwork` 即实例：网络编辑路由 2026-09-26 砍除后方法留存数月，`docs/05` 还把它当功能描述）。
    - 删路由的同一个改动里：`grep -rw <方法名>` 全仓库（含 `docs/`）确认零引用 → 删方法 → 同步改掉文档里把它当功能写的句子（改述为「该功能随路由砍除」）。只删代码不改文档、或只改文档不删代码，都会留下下一轮要被重新发现的漂移。
    - 判定「独占」不用猜：方法名在全仓库除定义处外无第二处引用即可删；拿不准就先留着并在本文件登记，不要凭感觉删。
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
5. **前端静态检查**：`cd web && npm run lint`（ESLint 10 + eslint-plugin-vue，配置见 `web/eslint.config.js`）。只启用「真问题」规则（未定义变量、死分支、空块等），风格类关闭。2026-10-17 首次接入即扫出 4 个运行时缺陷（缺 `api`/`h` 导入、缺函数定义、`v-else-if` 死分支），**提交前端改动前必须 lint 通过**。`vue/no-mutating-props` 因建机向导整体下传 reactive 对象而关闭，属已论证的推迟项。

## 验收工具链（固定标准，2026-10-17 用户拍板，长期有效）

**约定：以后说「验收 / 测一下 / 扫一扫」默认跑完下列全部适用项，不是只跑一条。** 只跑其中某条时必须在回复里写明「仅跑了 X，未跑 Y」。

### 后端（仓库根目录）

| 命令 | 作用 | 门槛 |
|---|---|---|
| `go build ./...` | 编译 | 必须通过 |
| `go test ./...` | 单元/集成测试 | 全绿（允许无测试的包，但不得有 FAIL） |
| `golangci-lint run` | 静态检查（v2 schema，`.golangci.yml`） | 0 发现 |
| `govulncheck ./...` | Go 依赖/标准库 CVE | `Your code is affected by 0 vulnerabilities` |
| `gosec -quiet -exclude-dir=web ./...` | 安全扫描（G304/G204/G301/G306/G115 等在本项目属既定设计，见下方「已知误报」） | 不新增 G703/G101/G110/G117 类发现；新增项须逐条定性 |

**gosec 已知误报（2026-10-17 全量定性，勿重复追）**：G304 变量路径（内部配置/数据目录）、G204 变量子进程（ansible/docker 按设计）、G301/G306/G302 权限（playbook 可读、磁盘镜像 qemu 可读、脚本可执行）、G115 整型转换（统计展示、上游已夹紧）、G104 错误路径的 Close/Remove。真实需关注的是 G703 路径穿越与 G101 硬编码凭据——新增时必须逐条核实。

**`go.mod` 已声明 `ignore ./web/node_modules`**（Go 1.26 ignore 指令）：前端依赖里夹带的 Go 包（`flatted/golang`）此前会被 `./...` 当成仓库包扫描，污染 build/test/vet 输出。

### 前端（`cd web`）

| 命令 | 作用 | 门槛 |
|---|---|---|
| `npm run lint` | ESLint（`eslint.config.js`） | 0 error / 0 warning |
| `npm run test` | Vitest 单测（`src/**/*.test.js`，jsdom） | 全绿；`npm run test:coverage` 看覆盖率 |
| `npm run build` | Vite 构建 | 必须通过（后端托管 `dist/`） |
| `ADMIN_PASS=<口令> npm run e2e` | Playwright 验收：登录 → 18 个页面遍历 → 打开类按钮逐个点击 → axe 可访问性 | 0 failed；无 pageerror；无 critical a11y |
| 无口令时 `npm run e2e` | 只跑登录页（渲染 + axe），登录后用例自动 skip | 同上 |
| `node scripts/ui-smoke.mjs`（仓库根，需 `ADMIN_PASS`） | CDP 深链/重定向/截图冒烟（Playwright 之外的补充，含全页截图） | 全绿 |

**Playwright 约定**：默认 `channel: 'chrome'` 复用系统 Chrome，不下载浏览器；串行 `workers: 1`（共享同一后端）。按钮点击只允许「打开类」（新建/刷新/查看/编辑…），**危险类（删除/关机/重启/保存/执行/部署…）一律跳过**，自动化误点删除的代价太大。新增页面时同步 `web/e2e/acceptance.spec.js` 的 `PAGES` 清单。

**业务链路验收（L5）以 `docs/testing/e2e-acceptance-plan.md` 为准**：该文档按链路列出用例编号、前置、步骤、断言与清理，并记录盘点发现的已知缺陷/风险。用例标签：

| 标签 | 内容 |
|---|---|
| `@smoke` | 登录页、18 页遍历、全按钮点击、axe |
| `@vm-create` | 建机三方式（ISO/云镜像/克隆）、安装源探测、真实建机、失败路径 |
| `@vm-life` | 生命周期、快照、设备、删除/回收站/守卫 |
| `@infra` | 存储池/卷、网络、镜像 |
| `@docker` | 容器、应用商店、部署栈 |
| `@auto` | Ansible、Playbook/Role、计划任务 |
| `@rbac` | 三角色权限负例、资产级授权 |

**测试数据纪律（强制）**：所有资源名必须 `e2e-<runId>-` 前缀；删除/修改前过 `isE2EName()`；批量删除/清空/改系统设置需 `E2E_ALLOW_DESTRUCTIVE=1` 且目标全为 e2e 资源。接口只用于「摆前置数据 + 收尾」，被验收的行为本身必须走 UI 点击。

### 性能（Lighthouse，可选）

Lighthouse **不进 devDependencies**（它体积大、只在需要基线数据时用一次，常驻只会让 `npm i` 变慢且被 knip 判为未使用依赖）。需要跑时临时调用：

```bash
cd web && CHROME_PATH=/usr/bin/google-chrome npx --yes lighthouse \
  "http://127.0.0.1:8080/#/login" \
  --chrome-flags="--headless=new --no-sandbox --disable-gpu" --quiet \
  --only-categories=performance,accessibility,best-practices
```

2026-10-17 基线（登录页）：performance **74** / accessibility **96** / best-practices **100**。
门槛：performance ≥ 70、best-practices = 100。**SEO 不设门槛**——`robots.txt` 刻意 `Disallow: /`（内部平台不收录），Lighthouse 会因此扣 `is-crawlable` 分，属预期。

可访问性**日常以 axe-core 为准**（已随 Playwright `@smoke` 每次都跑，门槛：无 critical），不必每次跑 Lighthouse。

**已知可访问性例外**：登录页主按钮白字压在品牌青 `#2a9da5` 上，对比度约 3.3:1（axe 报 serious）。这是品牌色决策，改它等于全站换主色；副标题 `.en` 已单独压暗到 `#1f7e84` 达标。除此之外新增 serious 必须修。

### 一次跑完

```bash
# 后端
go build ./... && go test ./... && golangci-lint run && govulncheck ./... && gosec -quiet -exclude-dir=web ./...
# 前端
cd web && npm run lint && npm run test && npm run build && ADMIN_PASS=<口令> npm run e2e
```

## Skills 适配清单（先读这里再加载 skill，豁免条款具有项目级效力）

**用法**：写代码前按本清单加载对应 skill；标记「豁免」的条款以本清单为准，不视为违规。本清单是项目级决策记录。原「skills 本体不修改不删除」条款已于 2026-10-01 按用户拍板作废：52 个无关 skill 已从 `~/.agents/skills/` 物理删除（Golang 不适用 25 个 + element-plus 零使用组件 27 个），目录从 131 收敛到 79；「不适用」清单随之消失，选型论证见 git 历史。

**豁免条款（已论证的项目决策，覆盖 skill 原文）**：
1. `golang-database` 的「用 sqlx/pgx、禁用 ORM」→ **vmops 钉死 GORM**。理由：管理平台 80% 数据操作是标准 CRUD，GORM 开发效率是主要矛盾；其"SQL 不可见"短板用 paramID 参数化收口 + 代码审查补偿。换库=全量重写零收益。
2. `golang-database` 的「禁止 AutoMigrate、用版本化迁移」→ **保留 AutoMigrate**。理由：单机部署、最大表 <1000 行、无多实例竞争，其反对的三个风险（锁表/竞争/无历史）均不成立；生产化演进（golang-migrate）列展望。
3. `golang-database` 的「禁用软删除类的魔法」→ **资产表（vms/hosts/images/users）保留 gorm.DeletedAt 软删**，过程表（tasks/audit_logs/console_sessions）不软删——边界已按"资产可恢复、过程只增不改"划清。

**适用（写/审对应领域代码前加载）**：golang-code-style、golang-error-handling、golang-naming、golang-concurrency（worker/WS goroutine）、golang-context、golang-security（注入面）、golang-safety、golang-observability、golang-testing、golang-refactoring、golang-structs-interfaces、golang-design-patterns、golang-performance、golang-database（除上述 3 条豁免，索引/NULL/注入/事务条款照用）、vue-best-practices、vue-router-best-practices、vite、element-plus-<组件名>（用到即查，禁凭记忆）、ui-ux-pro-max（UI 批次后审计）。


## 当前有效遗留清单（2026-09-26 逐项核实代码后收敛；勿在文档中宣称已解决）

- ~~`/metrics` 公开~~ **已核实为陈旧信息（2026-09-26）**：.env 自 v3.3 起即配置 METRICS_TOKEN，实测无 token 401 / 带 token 200（prometheus.yml 的 vmops job 一直在发配对凭证）
- ~~**JWT 仍支持 `?token=` 查询参数传递**~~ **已成为历史**：WS 改用一次性短时票据（`service/wsticket`，`POST /api/vms/:id/ws-ticket` 与 `POST /api/docker/containers/:id/ws-ticket` 签发，30s + 单次使用 + 绑定资源），`?token=` 与 `PromoteQueryJWT` 已移除
- **多宿主机未实现**：宿主机模块仅登记与状态采集，虚拟化连接固定 `qemu:///system`（`hosts.libvirt_uri` 字段已删）

已核实**解决**（勿再列为待办）：网络 XML 直定义两路由（2026-09-26 砍除）、登录限流（`handler/auth.go` loginLimiter）、CORS release 禁 `*`（main.go 启动校验）、SSH 主机密钥 TOFU（全仓库 InsecureIgnoreHostKey 归零）、孤儿卷清理闭环、golangci-lint 已装并升级 v2.5.0（`.golangci.yml` 已迁移 v2 schema：`version:"2"`/`go:"1.26"`，字段改名与 `formatters` 段落地，全仓库 0 发现）、ImportVMs errors 数组前端已消费、`tasks.payload` 历史明文已清洗（`scripts/purge-task-secrets --apply`，共 7 条；含一条漏网遗留键 `cloud_init_password`，脚本键清单已补齐；清洗前已备份 `tasks` 单表）。

## Git 远端与分支约定（2026-10-08 用户拍板，长期有效）

- **两个远端的默认分支均为 `main`，推送一律推 `main`**，不再使用 master：
  - gitee：`git push origin main`（origin = gitee.com/jiuzhao996/virtkite.git；默认分支已改为 main，旧 `master` 分支由用户手动删除）
  - github：`git push github main`（github = github.com/jiuzhao996/virtkite.git）
- **github 直连不可达**（443 超时），推送/拉取 github 须走本机 Clash 代理（仅命令级环境变量，不改 git config）：
  `https_proxy=http://127.0.0.1:7890 http_proxy=http://127.0.0.1:7890 git push github main`；gitee 可直连。
- 沙箱内 git 推送会因无法写 `~/.git-credentials.lock` 失败（credential.helper=store），需在沙箱外执行。
- 本地 `main` 上游已于 2026-10-08 修正为 `origin/main`（此前误配 `origin/master`，曾致 `git status` 显示"领先 92"；gitee 旧 `master` 分支同日已删除）。
- 仓库内 `stacks/es-cluster.yml` 的 `cluster.initial_master_nodes` 与 `mysql-master-slave` 栈是 ES/MySQL 主从语义，与 git 分支无关，**不得当作分支引用改动**。
- `.trae/` 是 Trae 工具产物（规划文档等），**不提交**。

## 协作约定（AI 编码助手，2026-10-13 用户拍板）

- **大任务并行化**：一次任务里若有多个**相互独立**的子任务（如「后端 + 前端 + 文档一起改」「并行排查 N 个模块」），优先派多个子 agent 并行处理，缩短墙钟时间；有前后依赖的子任务仍串行，不要为了并行拆坏依赖。
- **收尾期以删减与优化为主**：项目已进入成熟期，原则上不再新增功能；优先做「删冗余、收敛、提准、前端体验优化」。确需新增时先说明必要性再动手。

## 历史批次记录（已清理）

P0 稳定性 → P1 安全 → P2 正确性 → P3/P4 → UX → 监控栈 → 多宿主机砍除 → 冗余清理 → RBAC → 资产授权 → Skills 审计 → 批 B/C → v3.3 → v3.4 → v3.5 → 胰腺癌级审计 → IA 精简 → 日志栈（VictoriaLogs + Vector）。批次流水与踩坑记录（devlog、archive 规划与契约存档、docs-site 文档站）已于 2026-09-30 按用户拍板清理；仍在生效的教训均已固化为本文件的「后端开发标准」「前端开发标准」，考古请查 git 历史。