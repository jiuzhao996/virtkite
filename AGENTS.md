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

## 品牌规范（鸢航 VirtKite，勿当装饰图误删）

- **正式名称**：鸢航 VirtKite（项目代号 vmops 仅存在于代码/目录/包名，UI 与文档一律用品牌名）。
- **logo 语义**：三道波浪线既是终端家目录符 `~` 也是海面，纸鸢掠浪而上；金色虚线"断而未断"=管理通道。virt 词根 + kite + 鸢航三关。
- **资产位置**：`web/public/brand/logo.svg`（主标青绿底）/ `web/public/brand/mark-white.svg`（侧栏透明白鸢版）/ `web/public/brand/logo-teal.svg`（登录页透明版）/ `web/public/favicon.svg|favicon-32.png|favicon-16.png` / `branding/`（PPT 素材：virtkite-logo.svg 与 512/256/128 PNG、白底 light 版、横版组合 logo-horizontal）。
- **品牌色**：深空蓝底 `#0d2444→#1b3a62`、鸢蓝 `#6db6ff→#2e6bd6`、金 `#ffd268`（点缀色，只用于牵线/鸢眼/飘带）。
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

## 近期修复记录（勿回退）

- virt 层全部错误 `%v` → `%w`（40 处）
- `getConn()` 断线自动恢复（原先 `Reset()` 从未被调用）
- 快照名 XML 转义（`xmlEscape`）
- handler 层 36 处 `err.Error()` 泄漏改为统一 `ErrorResponse`/`ErrorWithMessage`
- 连接错误补中文前缀（`virt.go` `Connect`）

### P0 稳定性批次

- **新增 `service/console/conn.go`：`Conn` 用写锁串行化所有 WS 写入**。SSH 桥有 stdout/stderr/主循环三路写、串口桥两路写、外加管理员强断的 `CloseMessage` 第四路，原先直接并发写裸 `*websocket.Conn`，触发 gorilla/websocket panic 即整进程退出。`Registry.conns` 类型随之由 `map[uint]*websocket.Conn` 改为 `map[uint]*Conn`，`Disconnect` 改用 `CloseWithReason("管理员已断开连接")`（关闭帧与底层关闭都在写锁内）。
- **全仓库原本 `recover()` 出现 0 次**；现 tasks worker（`runExecutor` + `run` 两层）、`Registry.sweepOnce`、3 个 WS 转发 goroutine（terminal 输出、serial 打开串口、serial 输出）均已兜底。
- **任务入队改有界等待**：`Submit` → `enqueue`，先非阻塞尝试，队列满用单个 `time.NewTimer` 最多等 `enqueueTimeout = 3s`，超时置 `failed` + `Error="任务队列繁忙，请稍后重试"` 并回写返回给 handler 的 task。原实现 `go func(){ m.queue <- id }()` 会堆积无上限且永不退出的阻塞 goroutine。
- `Manager` 删掉从未 `close` 的 `quit` 死字段，`loop()` 改 `for id := range m.queue`；生命周期与进程一致。
- **静态托管四候选探测**（见「运行与启停」），修复 `go run main.go` 必然启动失败。
- `Registry` 清扫器不再持锁做 DB IO：锁内取 `byToken` 快照 → 锁外逐条查库 → 回锁删除并二次确认映射未被重新签发覆盖。
- `middleware/audit.go` 删掉把整个请求体 `io.ReadAll` 进内存却从未使用的死代码（多 GB 镜像上传会 OOM）；`main.go` 补 `r.MaxMultipartMemory = 32 << 20`（超阈值部分落磁盘临时文件）。
- 审计白名单增加 `/assets`（Vite 产物）与 `/favicon.ico`，并跳过 SPA 路由回退（非 `/api` 路径且响应 `Content-Type` 为 `text/html`/`text/plain` 时不写审计）。
- 类型断言全部带 `ok`：`AdminMiddleware` 的 `role.(string)`、`AuditMiddleware` 的 `user_id.(uint)`/`username.(string)`，类型不符时降级处理而非 panic 掉请求链。
- `handler/terminal.go` 读循环 `break` 加 `readLoop` 标签（原裸 `break` 只跳出 `switch`，stdin 写失败时循环不退出）。
- `initSeedData` 补错误处理：`Count`/`HashPassword`/`Create` 失败即打日志中止，不再写入空哈希造成账号静默不可登录。

### P1 安全批次

- **39 处路径参数主键改为先解析再交 GORM**（新增 `handler/param.go` 的 `paramID`/`parseID`）：37 处普通 handler 用 `paramID`（`vm.go` 26、`host.go` 4、`image.go` 4、`audit.go` 1、`user.go` 1、`vnc.go` 1），2 处 WS 用 `parseID`（`terminal.go`、`serial.go`）。见后端标准第 8 条。
- **RBAC 语义收紧：viewer 现在真的只读**。`isGuestWriteChannel` 把 `GET /api/vms/:id/terminal` 与 `GET /api/vms/:id/serial` 对 viewer 判 403（两者虽是 GET，但建立的是对 guest 的双向写入通道，串口在多数云镜像上直接就是 root TTY）。图形控制台保留，`POST /api/vms/:id/vnc-token` 响应新增 `view_only` 布尔字段（非 admin 为 `true`），前端以 noVNC 的 `view_only=1` 打开禁用键鼠。
- **中间件响应格式统一**：`middleware/jwt.go` 原返回 `{"error":"..."}`，现走包内 `abortJSON` 输出 `{code,message,data}`。**不能复用 handler 的 `Fail`**——`handler` 已依赖 `middleware`，反向引用构成导入环。`handler/vnc.go` `RequestToken` 的 404 也改 `Fail`；`ResolveToken` 的 `{"host","port"}` 是 websockify 外部契约，**没变也不该变**。
- **`ParseToken` 加 `jwt.WithValidMethods(["HS256"])`** 防算法混淆（实测 `alg=none` 与 `alg=HS512` 伪造均 401）。
- **release 启动校验修死代码**：原判 `JWT_SECRET_KEY == ""` 永不成立（config 兜了硬编码默认值），现改为「空 **或** 等于内置默认值」即 `log.Fatalf` 拒绝启动。
- **libvirt XML 生成 5 处字符串拼接改 `encoding/xml`**：`CreateVolume`、`CreateVolumeCustom`、`CreateDirPool`、`CloneVolumeFromVol`、`NetworkXMLFromParams`（原来池 name/path、卷 name/format、网络 name/gateway 都可闭合标签注入任意 libvirt 定义）。
- **入参校验新增**：存储池 `path`（规范绝对路径 + 字符白名单 + 非根目录）、卷 `format`（仅 `qcow2`/`raw`）、网络 `gateway`（合法 IPv4）、宿主机 `ssh_ip`（IP 或保守主机名白名单——该值作为 argv 传给 `ping`，`-f` 会被当成 flood ping 选项）。
- **Web 终端 SSH 目标白名单**（`validateSSHTarget`）：VM 已记录 IP 则须精确匹配；未记录 IP 时只接受 RFC1918 私有网段 IP 字面量，排除环回/链路本地/组播/未指定，不接受主机名（防 DNS 解析到公网与 DNS rebinding）；端口须在 1-65535。原实现 host/port/user/password 全取自浏览器且零校验，等于把平台当跳板机 / 端口扫描器 / 口令爆破器。
- 种子账号的明文口令不再写进启动日志（只打用户名与角色）。
- **新增 `service/console/conn_test.go`（5 个测试，`go test -race` 全 PASS）**：并发写、关闭后写返回 `ErrConnClosed`、重复关闭幂等、`CloseWithReason` 投递中文原因关闭帧、关闭后阻塞中的 `ReadMessage` 能返回。此前全仓库 0 个 `*_test.go`。
- **仍未处理（勿在文档中宣称已解决）**：`POST /api/networks/xml` 与 `PUT /api/networks/:name`、`PUT /api/vms/:id/xml` 接受原始 XML 直定义；`/metrics` 公开无鉴权；登录无限流；CORS 默认 `*`；SSH `HostKeyCallback` 为 `InsecureIgnoreHostKey()`。

### P2 正确性批次

- **⚠️ 最重要：「增量克隆」原先根本没实现，README 与多篇 docs 都把它当核心亮点在讲。** 原 `CloneVolumeFromVol` 用 `StorageVolCreateXMLFrom`（等价 `virsh vol-clone`），注释还写着「libvirt 自动写 qcow2 backing file」；实测该 API 做的是**全量数据拷贝**，子卷 `qemu-img info` 无 `backing file` 行、`vol-dumpxml` 无 `<backingStore>`。现改为 `StorageVolCreateXML` + XML 声明 `<backingStore>`（见后端标准第 11 条），新增 `buildVolumeXMLWithBacking` 与 `volBackingXML`，`buildVolumeXML` 变成薄封装。实测子卷有 `backing file:`、有 `<backingStore>`，`qemu-img check` 报 `No errors were found on the image`。
- **克隆虚拟机 MAC 与源机相同（同网段冲突）**：`CloneVMFromSpec` 里 `spec := *source` 只复制切片头，`Disks` 深拷贝了但 **`Interfaces` 没有**，MAC 原样沿用源机；两台同时开机即 ARP 冲突、网络双双不可用。修复：新增 `randomMACAddr()`（前缀 `52:54:00`，与 `service/tasks` 的 `randomMAC` 一致），抽出**纯函数** `buildCloneSpec(source, newName, diskIdx, newDiskPath, srcDiskPath)`（深拷贝 Disks 与 Interfaces、重生成 UUID、**逐块网卡换 MAC**、清空 RawXML）与 `systemDiskIndex(source)`（定位首个 `device=='disk'`，跳过 cdrom）。抽纯函数的目的是可单测，不依赖 libvirt 连接。实测源机 `52:54:00:7a:ad:94` → 克隆机 `52:54:00:6d:f2:b1`，DB 与 libvirt 一致。
- **`execCloneVM` 的错误注释已修正**：原写「首个网卡 MAC 从 libvirt 查询（克隆后重新生成）」——libvirt 不会自动改 MAC，XML 里显式给了就照用；重新生成是 `CloneVMFromSpec` 做的，这里只是把结果同步进 DB。
- **删除虚拟机会误删共享卷 → `shouldKeepVol` 三重守卫**（见后端标准第 12 条）。真正实现 linked clone 后「删父盘 → backing chain 断裂 → 所有子机磁盘不可读且不可恢复」的风险成立；镜像库登记的共享基镜像被删则留下 `images` 表悬挂记录。配套新增 virt 层 `ListBackingRefs(poolName) (map[string][]string, error)`（枚举池内所有卷的 `<backingStore><path>`，返回「父卷路径 → 依赖它的子卷名列表」）。被保留的卷进任务结果 **`kept_volumes` 数组**（前端任务详情可见，`docs/task-contract.md` 已记录）。
- **新增 `service/virt/clone_test.go`（8 个测试，`go test -race` 全 PASS）**：MAC 格式与 500 次不重复、UUID v4 格式、系统盘定位跳过 cdrom、**两块网卡 MAC 全部重生成且不污染源 spec（核心用例）**、磁盘深拷贝只改系统盘、身份字段（名称/UUID/RawXML/硬件规格）、无网卡纯串口机、端到端 `BuildDomainXML` 不含源机 MAC。
- **Dockerfile 整文件重写**（`docker build --no-cache` 实测成功，74 秒，产物 51.5MB）：`golang:1.21` → `golang:1.25-alpine`（`go.mod` 要求 1.25.0）；`go build -o vmops ./...` → `-o vmops .`（前者必报 `cannot write multiple packages to non-directory`）；新增 `ARG GOPROXY=https://goproxy.cn,direct`（容器内 `proxy.golang.org` 实测超时，不加则 `go mod download` 挂死）；`-tags timetzdata` + `ENV TZ=Asia/Shanghai`（alpine 无 `/usr/share/zoneinfo`，DSN 带 `loc=Local`，否则时间静默退化 UTC 差 8 小时）；`alpine:3.19` → `alpine:3.24`（3.19 已 EOL）；`web/dist` 改从 builder 阶段 `COPY --from` + `mkdir -p` 兜底空目录（`web/dist/` 被 gitignore，干净克隆里原来直接构建失败）。
- **`.golangci.yml` 的 `go:` 从 `"1.21"` 改 `"1.25"`**（与 go.mod 对齐）。注意：本机**未安装 golangci-lint**，静态检查表里它必须标「未执行/待补」，不要混进「全绿」；该配置是 v1 schema，装 v2.x 会因字段改名（`linters-settings` → `linters.settings` 等）报错。
- **`handler/vm.go ImportVMs` 最后一处内部错误泄漏已修**：`fmt.Sprintf("%s: %v", name, err)` 会把 GORM 原始错误拼进响应 `errors` 数组，现改为完整错误进 `LogError`、响应只给「域名 + 写入数据库失败」。**更正一处认知**：这个 `errors` 数组前端 `VmList.vue` 其实没在用（只读 `imported`/`skipped`/`failed`），失败原因目前不会显示给用户 —— 待改进。
- **状态常量统一**：`model/vm.go` 的 gorm default 从 `shut_off`（下划线）改成 `shut off`（空格），与 `StateToPlatform` 及前端一致；新增 `model.VMStatus*` 与 `virt.Status*` 两套常量，`handler/vm.go` 8 处字面量改引用（见后端标准第 5 条）。实测数据库里**没有** `shut_off` 存量行（所有写入路径都显式给值，从没走到列默认值）；沙箱验证过 GORM AutoMigrate **能**把已有列的 default 改过来，后端下次重启即收敛。**`service/tasks/vm_tasks.go` 还有 5 处 `"shut off"` 字面量未换常量**，待改进。
- 顺带清理：删掉 `CloneVMFromSpec` 上一段过期开发期注释（「依赖 B1 产出的 spec.go…当前 B1 尚未落盘，本函数暂无法编译」——spec.go 早就在了，这段话只会让人困惑）；`service/tasks/vm_tasks.go` 新增 `defaultStoragePool` 常量替换硬编码池名 `"vmops"`。
- **P2 后仍未处理（勿在文档中宣称已解决）**：P1 遗留五项（原始 XML 直定义 / `/metrics` 公开 / 登录无限流 / CORS `*` / SSH `InsecureIgnoreHostKey`）继续有效，另加：golangci-lint 未安装故深度 lint 未执行；孤儿卷无自动清理；`vm_tasks.go` 5 处状态字面量；`ImportVMs` 的 `errors` 数组前端未消费；**多宿主机是空壳**（`hosts.libvirt_uri` 从未用于建立连接，`virt.New()` 固定 `qemu:///system`）。
- **✅ 已解决（原「已知未同步项」）**：本文件「前端开发标准 1（禁止 emoji 当图标）」与「控制台设计约定」里的 emoji 曾互相矛盾。**裁定：以「禁止 emoji 当图标」为准**——`ConsolePage.vue` 25 处、`Login.vue` 1 处图标已全部换成 `@element-plus/icons-vue` 组件（含 17 处 emoji 与 9 处被当图标用的几何字符 `▮ ▶ « » ←`），「控制台设计约定」的 emoji 描述改成图标组件名，规范新增「前端开发标准」第 3 条（emoji 仅允许出现在日志输出，`main.go` 启动日志的 emoji 保留）。业务逻辑、WS 处理、`isAdmin` 权限门控未动。

### P3 前端工程化 + P4 测试补齐批次

- **`web/src/utils/format.js`（新建，312 行）**：收敛 10 余处重复——`statusText` 5 份拆成 `vmStatusText`/`hostStatusText`/`taskStatusTag` 三个域（键集不相交，硬合并会丢语义）；`fmtTime` 按输出格式拆成 `fmtDateTime`（补零）/`fmtDateTimeLocale`；`fmtSize` 按入参单位拆成 `fmtSizeGB`/`fmtSizeBytes`；`FALLBACK_ACTION_LABELS`（38 键）两份逐字重复收归一处；阈值配色统一走 CSS 变量（echarts 用 `cssVar()` 读真实值）。页面骨架 CSS（`.page-head`/`.page-title`/`.toolbar`/`.count`/`.mono` 字体族）搬进 `global.css`，同名不同物（VmDetail 顶栏）与差异规则留在原地。
- **401 拦截器与 store 脱钩已修**：`TOKEN_KEY` 从 `api/index.js` 挪到 `store/auth.js`（原来 store 反向 import api，api 直接 import store 会成环），拦截器调模块级 `logout()`；登录接口自身的 401 不清已有会话。上传 `uploadImage` 单独 `timeout: 0` + `onUploadProgress`（全局仍 15s），`ImageList` 接了进度条。
- **路由懒加载 + manualChunks**：13 个页面改 `() => import()`（Login/MainLayout 首屏必需，保持静态）；`vendor-echarts`/`vendor-xterm`/`vendor-element-plus(+icons)`/`vendor-vue` 独立 chunk。首屏下载量 **−50%**（gzip 964KB → 455KB）。`chunkSizeWarningLimit` 降回默认 500，echarts/element-plus 两个超限 chunk 刻意保留告警（再拆只能改 `.vue` 引入方式，见下）。验证走真实构建产物 + headless Chrome（dev server 不走 rollup，验不了分包）：真实登录、15 路由逐个渲染零白屏、echarts/xterm 按需加载、冷加载深链通过。
- **后续建议（未做）**：echarts 按需（`echarts/core` + `use()`，动 3 个 `.vue`，1127KB → 300-450KB，性价比最高）；Element Plus 按需（需 `unplugin-vue-components`，`MainLayout.vue` 的字符串图标映射必须改组件引用，漏改静默空白）；懒加载 chunk 加 prefetch。
- **P4 测试补齐**：`service/virt` 新增 `spec_test.go`(13)/`cloudinit_test.go`(6)/`storage_test.go`(7)/`network_test.go`(4)/`state_test.go`(4)/`snapshot_test.go`(4)；`handler` 新增 `param_test.go`(4)/`response_test.go`(7)/`terminal_test.go`(4)/`validate_test.go`(12)；`middleware/jwt_test.go`(16)；`service/tasks` 新增 `manager_test.go`(11)/`vm_tasks_test.go`(14)。**项目测试现状：122 个顶层函数 / 约 890 子用例 / 5 个包，`go test -race ./...` 全 PASS**；纯函数目标覆盖率基本 100%（`ParseDomainXML` 95.7%、`BuildDomainXML` 97.5%、`paramID`/`response.go`/`StateToPlatform`/`xmlEscape`/各校验函数 100%）。
- **测试中发现、已记录未修**：`diskSuffixIndex("aa")` 算出 0 与 `"a"` 冲突（无偏置 26 进制，第 28 块磁盘 target 会重复；26 块以上磁盘现实中几乎不存在，断言现状 + TODO）；`itoa(math.MinInt64)` 返回 `"-"`（唯一调用点是已校验 1-65535 的端口，不可达）；`friendlyMessage` 冒号在首位时不切分、600 字无冒号串不截断（与 `tasks.friendlyError` 截断 500 不一致）；`Fail` 无 `data` 键（与 `Success`/`abortJSON` 不一致）；`floatParam` 漏 `uint32/int8` 等类型（当前调用路径只经 JSON float64，不触发）；`execDeleteVM` 的 `shouldKeepVol` 是内部闭包无法单测（要测需提成包级纯函数，另排小重构）。
- **P4 后仍未处理（勿在文档中宣称已解决）**：P2 遗留清单继续有效，另加：`validateSSHTarget` 分支 1（VM 已记录 IP 则精确匹配）线上不可达——全仓库没有任何代码写 `vms.ip`，需接 DHCP lease 或 qemu-guest-agent 回填；`validateSSHTarget` 放行 IPv6 ULA（`fd00::/8`）但错误文案只写 IPv4 私有网段，文案与实现不对齐。

### UX 批次（价值密度改造：沉睡能力接线 + 监控融合 + 设置做实）

- **任务详情抽屉已兑现**（TaskList.vue）：解析 `task.result` JSON，展示 `vm_id`（跳详情）、`vm`、`kept_volumes`（删除保护保留原因列表）——原「文档承诺前端展示但任务详情根本不存在」的缺口已关闭；TaskList 同时补 `page/page_size` 真分页（后端 `Manager.ListPaged`，total 为 Count 真值，旧 `limit` 参数兼容）。
- **用户管理页落地**（UserList.vue + `/users` 路由，admin）：后端 CRUD 本来就有、此前前端零入口。顺带修两个后端缺口：CreateUser/UpdateUser 补角色白名单（原任意字符串入库）与密码 ≥6 位；DeleteUser 的 `fmt.Sscanf` 改 `paramID`（注入面）。
- **系统设置做实**：新增 `model.Setting`（`system_settings` KV 表）+ `service/setting`（白名单键 + 取值校验 + 内存缓存），`PUT /api/settings`（admin）写库即生效。三个消费点经**包级 resolver 钩子**接线（避免服务层互相 import）：`tasks.DefaultStoragePoolResolver`（原 `defaultStoragePool` 常量）、`vnc.TTLResolver`（原硬编码 5min）、`console.StaleAfterResolver`（原硬编码 60min）。**改这些逻辑必须经 resolver，勿回退成常量**；GET `/api/settings` 的 `tasks.workers/queue_buffer` 改读 `tasks.WorkerCount/QueueBufferSize` 真实常量（原是写死的占位）。
- **监控中心落地**（Monitor.vue + `/monitor`，登录即可看）：上半 Alertmanager 告警列表（`GET /api/monitor/alerts` 后端代理，env `ALERTMANAGER_URL`，AM 不可达 502 + 页内提示），下半 iframe 嵌 Grafana kiosk（`http://<host>:3000/d/vmops-overview/?kiosk`，uid 与 deploy/grafana-dashboard.json 一致）。**iframe 能嵌入依赖 docker-compose grafana 服务的三个 env**（`GF_AUTH_ANONYMOUS_ENABLED/ORG_ROLE`、`GF_SECURITY_ALLOW_EMBEDDING`），默认拒绝嵌入，勿删；Grafana 未启动时看板区空白属预期。镜像矩阵：**grafana 13.2.1（完整版，勿用 -slim）+ prometheus v3.14.0 + alertmanager v0.34.0**——slim 版缺内置数据源插件会报 `plugin not registered` 且后台补装不可靠（见 deploy/README.md）；Prometheus 2→3 的 prometheus.yml/规则文件语法兼容，实测零改动；「数据源代查」健康检查走 `/api/datasources/uid/{uid}/health`，老 `proxy/1` 路径已不可用。
- **删卷守卫**：`DELETE /api/storage/pools/:name/volumes/:vol` 从裸删改为先算引用（`virt.ListAllDomainDiskSources`（新方法，域磁盘 source 枚举）+ `images.path` 精确匹配 + `ListBackingRefs`），任一命中返回 **409** 中文原因；配套 `GET /api/storage/pools/:name/volume-refs` 池级一次算全，前端卷管理弹窗显示「在用」徽标、删卷确认带引用详情（双保险）。与 `shouldKeepVol` 同一立场：宁可删不掉，不可损坏在用磁盘。`StorageHandler` 因此新增 `DB` 字段（`NewStorageHandler(db)`）。
- **会话管理增厚**：后端 ListSessions 加 `type/vm_name/username` 过滤 + `page/page_size` 真分页；前端加 last_seen 列（VNC 僵死判定的唯一依据）、三个筛选控件、分页器。
- **MainLayout 改分组菜单**：navItems 加 `group` 字段，展开态两层 v-for + `el-menu-item-group`（展开/折叠两份清单收归一份），分组：概览/资源/基础设施/运维/管理（管理组 adminOnly）。新增菜单项：监控中心（运维组）、用户管理（管理组）；图标 `Odometer`/`User`/`Bell` 均已从 index.d.ts 查证存在。
- **顶栏任务铃**：`el-badge`+`el-popover`，并行拉 `status=running` 与 `status=pending` 两路合并；失败静默。`store/auth.js` 新增 `state.pageTitle` + `setPageTitle`——详情页顶栏显示 VM 名的响应式通道，MainLayout watch 路由（非 `/vms/:id` 即清除），VmDetail 拿到 spec 后写入。
- **其余**：HostList 编辑弹窗（复用添加弹窗 + `editingId`，接通从未被调用的 `updateHost`）；NetworkList 自启列改 el-switch（新端点 `PUT /api/networks/:name/autostart`，virt 层 `SetNetworkAutostart`）+ 启停二次确认；StorageList 池容量改进度条（`usageColor` 阈值色）。
- **验证状态**：`go vet` 干净、`go test -race ./...` 全绿（新增 `service/setting` 与 `handler/storage_refs_test.go`）、`npm run build` 成功、后端已重启实机冒烟通过（登录/用户列表/设置读写与校验/volume-refs/告警代理真返回一条 VMRunningDrop/会话与任务分页）；浏览器实测分组菜单、任务铃、用户管理、设置表单、卷管理徽标均正常。**本机 Grafana(3000) 未启动，监控页看板区为空属预期，`docker compose up -d` 后即出**。
- **IA 二次改造（对标 JumpServer 审计模块与云控制台顶栏分工）**：侧边栏 11 项重排为 总览(仪表盘/监控中心)、资源(虚拟机/镜像管理)、基础设施(宿主机/存储池/网络——宿主机从资源组下沉，实例优先原则)、运维(任务中心/审计中心)、管理(用户管理/系统设置)。**会话管理不再是独立菜单**：SessionList.vue 已改为无页面头的可嵌入组件，由 AuditList.vue 以 el-tabs 承载（操作日志 tab 仅管理员渲染——后端 /api/audit admin-only，viewer 只见会话 tab；/audit 路由因此去掉 requiresAdmin）。新增 **/profile 个人中心**（Profile.vue，全角色）：个人资料只读 + 修改密码（自 MainLayout 弹窗迁入，改完强制重登）+ 轮询偏好（自设置页迁入——本机偏好本就不该放系统设置）。顶栏右侧改为**头像/用户名下拉**（个人中心/退出登录）。Settings.vue 删除全部只读快照卡（只留运行参数表单），快照信息由 Dashboard「平台信息」卡承接（libvirt URI/存储池/网络/运行模式，getSettings 仅管理员拉取）。Dashboard 新增**告警概览卡**（firing 列表 + 跳监控中心，AM 不可达显示未连接提示）。侧栏分组折叠状态由本地 `closedGroups` 自管（default-openeds 在 isAdmin 异步到达后会失效，勿改回去）。
- **历史性能曲线（Prometheus 当历史库）**：仪表盘主机大盘、虚拟机列表迷你曲线、VmDetail 性能曲线原先靠浏览器内存攒点——进页面空白等 5s、刷新即失。现新增 `handler/history.go` 三个接口：`GET /api/dashboard/host-history`（仪表盘大盘）、`GET /api/dashboard/vm-history`（**批量**返回全部 VM 序列，供 VmList 迷你图按名字→id 预填）、`GET /api/vms/:id/stats-history`（VmDetail 大图），均经 `PROMETHEUS_URL`（config 新增，env，默认 127.0.0.1:9090；compose 内 http://prometheus:9090）调 `query_range`（step=15s 与抓取周期一致）返回 `{points:[{t:"HH:MM:SS",cpu,mem}]}`；前端进页面预填 60 点环形序列、轮询无缝追加，拉不到静默降级回旧行为。**关键坑**：内存占比表达式在 VM 关机瞬间产生 0/0=NaN，`c.JSON` 遇 NaN 直接渲染失败（200 + Content-Length:0 空响应体！），queryRange 解析时必须 `math.IsNaN/IsInf` 跳过非有限点。VM 关机时段无采样属正确行为（Prometheus 无该序列）。新增测试 history_test.go（escapePromLabel/clampMinutes）。
### 监控栈容器化（原生三件套已废弃，勿再回退原生运行）

- Prometheus/Alertmanager/Grafana 已从 `~/monitor` 原生安装**全部迁移为 docker compose 容器**（vmops-prometheus/vmops-alertmanager/vmops-grafana），原生目录已删除。混合形态：app+websockify 原生、mysql+监控栈容器。
- **坑一（已修）**：compose 里 `--alertmanager.url` 是 Prometheus 1.x 参数，2.53 直接启动失败（unknown long flag），2.x 用 prometheus.yml 的 `alerting:` 段对接——勿改回去。
- **坑二（已修）**：混合形态下 app 原生直跑，容器内 Prometheus 抓不到 `app:8080`，已改抓 `host.docker.internal:8080`（compose prometheus 服务加 `extra_hosts: host-gateway`），且**删掉了 prometheus 的 `depends_on: app`**（否则 compose up 会把 app 容器拉起来和原生进程抢 8080）。
- **坑三（国内网络）**：Docker Hub 直连超时，用 `docker.m.daocloud.io` 拉取后 `docker tag` 回官方名再 `docker rmi` 镜像源名。
- 形态切换与文件清单见 `deploy/README.md`（新增）。两种形态：混合（开发/演示推荐）与一键全容器（发布形态，websockify 尚未入 compose，控制台链路要补）。

### 多宿主机砍除 + 监控闭环 + XML 设备细节批次（2026-09）

- **多宿主机空壳已砍除（勿回退）**：`model/host.go` 的 `libvirt_uri` 字段与 `handler/host.go`、`HostList.vue` 的对应入口已删除，宿主机模块重新定位为「宿主机登记与状态采集」（SSH 连通性 ping + 状态采集）；跨宿主机虚拟化操作仍列后续工作。注意 GORM AutoMigrate 不会 drop 列，库里残留 `libvirt_uri` 列无害。`config.LibvirtURI`（LIBVIRT_URI env）已删除——从未接入连接逻辑（virt.New 固定 qemu:///system），连接地址现由 service/virt.URI 常量表示。
- **`vms.ip` DHCP 回填已落地**：virt 层 `ListDHCPLeases()`（`service/virt/network.go`，对应 `virsh net-dhcp-leases`，多网络归并 + 过期过滤），`handler/vm_ip_sync.go` 的 `syncVMIPs()` 在 VM 列表/详情请求时惰性触发（全局 30s 节流、失败静默），按 MAC 匹配回填。**`validateSSHTarget` 分支 1（已记录 IP 精确匹配）由不可达变为可用**。qemu-guest-agent 途径仍为后续工作（XML 已具备通道）。
- **`/metrics` 抓取令牌**：env `METRICS_TOKEN` 非空时要求 `Authorization: Bearer` 或 `?token=`；`deploy/prometheus.yml` 注释里有配对的 bearer_token 配置。未设置保持公开。
- **监控服务发现闭环已落地**：`service/monitor`（`GenerateFileSD` 纯函数 + `StartFileSDWriter` 后台原子写），env `FILE_SD_PATH` 启用（compose 全容器形态开箱即用，宿主机直跑需在 .env 手工加），输出 running 且已知 IP 的 VM 为 `ip:9100` node_exporter 目标（同 IP 去重）；`GET /api/monitor/file-sd` 预览。**file_sd 只含 VM 目标，平台自身 exporter 走 prometheus.yml 静态抓取，勿混入**。guest 内需装 node_exporter。
- **Alertmanager 告警网关已落地**：`model.Alert`（alerts 表，fingerprint 唯一）+ `POST /api/monitor/webhook`（env `ALERT_WEBHOOK_TOKEN` 可选鉴权；**除 401/400 外恒 200，防 AM 重试轰炸**）+ `GET /api/monitor/alerts/history`（分页，status/fingerprint 过滤）+ Monitor.vue「告警历史」卡片。`deploy/alertmanager.yml` 已并挂 webhook receiver（host.docker.internal:8080）。
- **域 XML 设备细节已对齐手工模板机（`BuildDomainXML`）**：显式输出 host-passthrough CPU（`CPUMode` 空值按直通；**"default" 特殊值 = 不输出 cpu 节点**）、clock 定时器（rtc catchup/pit delay/hpet off）、guest-agent 通道（org.qemu.guest_agent.0）、virtio-rng（/dev/urandom）、memballoon、串口/控制台 pty；`ParseDomainXML` 回读 cpu mode。建机链路（execCreateVM）支持 `machine`（白名单 q35/pc/空）与 `cpu_mode`（白名单 host-passthrough/default）参数，向导「机器类型」下拉透传。**已实测 `virt-xml-validate` 通过 + virsh define/dumpxml 确认设备全部就位**。新增 `TestBuildDomainXMLDeviceDetails`。
- **一键添加硬件（勿回退成"先建卷再填路径"的两步流程）**：`POST /api/vms/:id/devices/disks/quick`（建 qcow2 卷+热挂载合一，卷名 `<vm名>-dN` 顺延跳重名，失败回滚建卷）、`POST /api/vms/:id/devices/standard`（补齐存量 VM 的 guest-agent 通道 + virtio-rng，幂等；存在性解析在 `domainDevicePresence`，**通道/rng 挂在 <devices> 下，解析结构必须含该层级**）；VmDetail 硬件面板三按钮。
- **存储板块重设计（池角色/描述/镜像登记）**：`model.PoolMeta`（pool_meta 表：平台侧 role/description，libvirt 池 XML 无此语义）+ `inferPoolRole` 按名/路径推断（base→模板基盘等，测试覆盖）+ `PUT /api/storage/pools/:name/meta`；`VolInfo` 新增 `backing_file`（卷自身 backing 父盘，前端「增量系统盘」徽标依据）；`POST /api/images/register` 登记既有池卷为云镜像（同路径 409，软删记录可恢复）；`GET /api/storage/pools` 响应扩展 seed_dir/default_pool/pool_roles。镜像库已清理测试垃圾并登记 base 池三件（Ubuntu.img/rocky10.qcow2/Rocky.img）。
- **早前遗留清单核实结果（勿再列为待办）**：登录限流已实现（`handler/auth.go` loginLimiter，同 IP 1 分钟 5 次失败锁定）；孤儿卷清理已闭环（`POST /api/storage/pools/:name/orphan-cleanup` + `cleanup_volumes` 任务）；CORS release 模式禁 `*`（main.go 启动校验）。仍有效遗留：原始 XML 直定义端点、SSH `InsecureIgnoreHostKey`、golangci-lint 未装、`ImportVMs` errors 数组前端未消费、IPv6 ULA 文案不对齐。

### 冗余清理批次（2026-09，毕设精简体量）

- **后端死代码删除**：`virt.GetDomainInfo`+`DomainInfo`（被 `GetDomainStats` 取代）、`virt.DefineNetworkXML`（handler 实际只调 `DefineNetwork`）、`virt.GetAutostart`、`tasks.Manager.List`（被 `ListPaged` 取代）、`handler.firstImageHost`（`firstHost` 的死副本）、`GET /api/audit/:id`（`GetAuditLog`，前后端零消费者）。
- **死字段/死配置删除**：`Host.OSVersion`（纯死列）、`VM.Template`（只写不读，含 execCreateVM 的 `template` payload 参数——handler 从未传过）、`config.LibvirtURI`（LIBVIRT_URI env 从未接入连接逻辑）。设置页 `libvirt_uri` 展示改读 `virt.URI` 常量（`service/virt/virt.go`），README/docs 的 LIBVIRT_URI 表述已同步。
- **前端清理**：MainLayout 未用 `watch` import、CreateVmWizard/VmList 死 CSS、`format.js` 5 个仅内部使用常量去 export（`FALLBACK_ACTION_LABELS` 有外部消费，export 保留）。
- **file-sd 补前端入口**：Monitor.vue 新增「node_exporter 抓取目标」卡片（`api.monitorFileSD`，原前后端半成品），展示 file_sd 将下发的 ip:9100 目标，空态/说明文案含 FILE_SD_PATH 启用提示。
- **⚠️ 监控看板 iframe 三坑 + 看板分组重构（勿回退）**：① iframe 高度小于看板实际渲染高度会把最后一块面板的图例切在 iframe 底边——最终方案是**看板按「宿主机/虚拟机」分组去重**（用户拍板）：宿主机看板（vmops-overview）=3 统计卡+「宿主机 CPU / 内存」「存储池使用率 %」两张全宽图（w=24，总 17u）；虚拟机看板（vmops-vms）=6 图三行两栏（CPU/内存/磁盘读/磁盘写/网络收/网络发，总 21u）；原 overview 里 4 张 VM 图已删（与明细看板重复）。iframe 高度按 1310px 宽 + kiosk=1 实测内容底边定 750/875（`boardHeight()`），**改 JSON 布局后须重新实测并同步**（跨端口读不到 iframe 内文档高度）。② 本地直连 3000 的 iframe src **必须带 /grafana 子路径前缀**（compose 设了 `GF_SERVER_ROOT_URL=https://kpyun.fun/grafana/`，不带前缀会被 301 到公网域名；serve_from_sub_path 开启时带前缀原地 200）。③ `grafanaBase` HTTPS 分支已含 `/grafana`，boardPath 里**只有本地分支再补前缀**——重复拼接 = /grafana/grafana 404（曾把公网弄挂）。另：Grafana 13 的 kiosk=1 藏不掉顶栏与「技术支持」页脚；每格实际 ≈33px 非文档写的 30px。切换标签文案为「宿主机/虚拟机」（Monitor.vue）。
- **监控中心增厚批次（2026-09 晚）**：①告警规则补到 9 条（deploy/alerts.yml：原有 5 条 + 新增 VmopsDown(up==0)/HostMemHigh/VMMemHigh/VMDiskIOHigh；改规则后 `docker restart vmops-prometheus` 生效——Prometheus 镜像未开 Lifecycle API，/-/reload 不可用）；②`GET /api/monitor/file-sd` 响应改 `{enabled, items}`（enabled=FILE_SD_PATH 是否配置），卡片头部显示「服务发现已启用/FILE_SD_PATH 未配置」，空态文案区分两种情况；③**Grafana 白屏必须后端代探**：容器未启动时浏览器错误页同样触发 iframe 的 load 事件，前端 onerror/load/超时全都判不了——新增 `GET /api/monitor/grafana-status`（探活 {GRAFANA_URL}/grafana/api/health 再退化 /api/health，禁跟随重定向防绕公网，GRAFANA_URL env 默认 http://127.0.0.1:3000、compose 内 http://grafana:3000），前端进入页面即探活，失败直接亮「Grafana 未连接 + 重试」提示层（重试=重挂载 iframe），iframe 15s 未 load 超时仅作挂起兜底；④窗口 <1100px 时看板区顶部显示「窗口过窄，建议在新窗口打开 Grafana」提示条（resize 监听，Grafana 网格窄屏重排会变高、静态 iframe 高度会复发截断）。
- **⚠️ 仪表盘与监控中心已合并（用户拍板，勿回退成两页）**：侧栏「总览」组只剩「仪表盘」一项，页面内 el-tabs 分「概览/监控」两个 tab。监控 tab 复用 Monitor.vue（加 `embedded` prop 隐藏独立页头，刷新按钮移进实时告警卡头部），pane 用 `lazy` 首次激活才挂载（Grafana iframe 首载约 3MB），挂载后常驻；切回概览 tab 需 `chart.resize()`（display:none 恢复后 echarts 不重绘）。**两个坑**：① Dashboard.vue 里 `Monitor` 名字被 @element-plus/icons-vue 的显示器图标占用，模板里嵌监控组件必须写 `MonitorView`（写成 `<Monitor embedded>` 会把图标组件撑满全屏）；② el-tabs 必须绑 `@tab-change` 维护 visitedTabs，漏绑则 lazy pane 永不挂载（白屏）。旧地址 `/monitor` 重定向 `/dashboard?tab=monitor`，Dashboard onMounted 读 query.tab 直达监控 tab。告警概览卡的「前往监控中心」改为切 tab。
- **仪表盘腾讯云风格化（2026-09 晚，参照腾讯云控制台主页设计语言）**：①侧栏单项目组不渲染分组标题——`menuGroups` 组内仅 1 项时直接平铺 el-menu-item（总览组只剩仪表盘后「总览」折叠头消失，菜单顶格从仪表盘开始；未来加回第二个总览级页面时分组标题会自动恢复）；②概览/监控 tab 做大（16px 加粗、46px 高、26px 内距，`.dash-tabs :deep(.el-tabs__item)`）；③统计卡腾讯云化：数字 1.7rem 加大、整卡可点跳转对应页面（stats 数据加 `to` 字段 + `@click="$router.push(s.to)"`）、hover 浮现右上角箭头 + 卡片上浮。
- **全平台控件尺寸统一（用户反馈"太小了"，对照腾讯云 14px 基准）**：页面级控件升级为默认尺寸——Monitor.vue 看板切换 radio-group、卡头刷新/重试按钮、告警历史筛选 select；Settings/Profile 的 `size="small"` 表单整体去掉 small（表单主体 12px 太小）。**保留 small 的惯例场景**：表格行内操作按钮、状态 tag、紧凑表格（`<el-table size="small">`）、顶栏徽标——列表页 grep 出的 small 绝大多数属此类，勿全量替换。全局新增 `.card-title { font-size: 0.95rem }`（各页卡片标题原本继承 14px 层级偏弱；局部页 scoped 自定义的不受影响）。改完实测：radio 14px/30px 高、按钮 32px 高、卡片标题 15.2px。
- **P1/P2 视觉一致性批次（对照腾讯云详情页/主页）**：①VmDetail 顶栏 7 个操作按钮（控制台/开机/暂停/恢复/关机/重启/删除）与卡头/配置区按钮、input-number、select 全部从 small 升为默认尺寸（顶栏 32px 高，高频主操作区）；表格内 tag/行内按钮仍保持 small 惯例；②主操作按钮配色统一：新建虚拟机/上传镜像 `type="success"` 绿 → `primary` 主题青绿，导入存量 VM 橙实底 → `warning plain` 白底描边（主操作品牌色实底、次操作描边的腾讯云层次）；③VmDetail 概览从带框 el-descriptions 表格改为**无框信息行**（`.ov-desc`：label 灰色 78px 固定宽、值区 16px 行距），对齐腾讯云详情页信息行；④4 个列表空态从 table empty-text 纯文本升级 el-empty + 引导文案（宿主机→添加宿主机、镜像→上传/登记、网络→新建 NAT、审计）；⑤仪表盘告警概览卡有待处理告警时标题区红边 +「N 条待处理」徽标（对齐监控中心 alert-firing）；⑥虚拟机状态卡「查看全部→」、操作类型分布卡「进审计中心→」右上角导航链接（腾讯云卡片操作位模式）。
- **VmDetail 顶栏电源/挂起合并为状态切换按钮（用户要求）**：开机+关机 → 一个电源钮（运行中显「关机」/其余显「开机」，`act(isRunning ? 'stop' : 'start')`），暂停+恢复 → 一个挂起钮（暂停中显「恢复」/否则显「暂停」）。语义与拆分版严格一致：暂停态电源钮禁用（须先恢复）、关机态挂起钮禁用、busy 期间锁定防动作闪烁。顶栏从 7 钮减到 5 钮（控制台/电源/挂起/重启/删除）。
- **VmList 卡片网格等高 + 去掉「更多」下拉（用户指出两处不合理）**：①运行中卡片因实时指标+曲线比未运行卡片高出一截，同行底部空洞——修法三件套：`.vm-grid` 加 `align-items: stretch`、`.vm-card`/`:deep(.el-card__body)` 改 flex column、`.vm-actions` 加 `margin-top: auto` 贴底，同时 `.vm-perf-idle` 占位固定 92px 高（= 值行+64px 曲线）消除高差来源；②「更多」下拉里只有重启/删除两项且悬浮突兀——**重启按钮按用户要求移除**（重启去详情页顶栏操作，卡片行内不做），删除改为常驻红色 icon 钮（`.vm-delete` margin-left:auto 右对齐独立，危险操作与常规操作分离；有输入名称确认弹窗兜底），ArrowDown 导入随之清理。
- **批量操作条改造（用户反馈勾选后三个实底大彩钮很怪）**：原「批量开机(N)/批量关机(N)/批量删除(N)」三个 default 实底彩钮常驻工具栏且**全状态可用**（对已关机机器亮着批量关机毫无意义）——改为「已选 N 台」紧凑操作条：`bulk-count` 主色计数 + 三个 plain 弱化按钮 + 「取消选择」。**智能禁用**：`bulkStartable`（选中存在非 running 才可开机）/`bulkStoppable`（存在 running 才可关机），禁用时 title 提示原因；`bulkAction` 内同步按状态过滤目标机器（start 只对非 running、stop 只对 running），即使绕过禁用也不会白跑接口。
- **批量电源合一（用户要求合并开机/关机两钮）**：两钮合成单个「批量电源」——`bulkPower` computed（选中含 running → stop/批量关机，否则 → start/批量开机）、`bulkPowerMixed`（选中状态集合 >1 即混合，按钮禁用 + title「选中虚拟机电源状态不一致，请分开勾选后操作」——混合时批量开关机没有单一语义，硬执行会既开机又关机，引导分开勾选是正确交互）。bulkAction 内按状态过滤目标的逻辑保留作兜底。
- **⚠️ 设备级 XML 编辑器 + GetDomainXML 改 INACTIVE（2026-09 晚，扣「基于 KVM」题眼）**：①新增 `web/src/utils/deviceXml.js`（浏览器 DOMParser/XMLSerializer：`extractDeviceXml` 按磁盘 target/网卡 MAC 从整域 XML 提取设备子树并 pretty print、`applyDeviceXml` 三层校验后替换回整域）——设备编辑=前端组合既有 getVMXML/updateVMXML，零后端新增；VmDetail 磁盘/网卡每张设备卡加「详情/XML」切换（reactive 视图态 + watch 自动加载草稿），保存提示"运行中域更新持久配置，重启后生效"；②**`virt.GetDomainXML` 从 flag 0 改 `DomainXMLInactive`（virsh dumpxml --inactive）**：XML 编辑（整域/设备级）必须以持久配置为底稿——define 写入的修改只体现在持久配置，用运行时 XML 做底稿会"保存后读不回"（实测踩中）；**⚠️ 坑：domain.go 里有两处 DomainGetXMLDesc，改 flag 时锚点匹配错函数**——一处属控制台函数（解析运行时 VNC 端口），误改 INACTIVE 会让控制台拿不到端口，已恢复并在两处都留了 flag 选择原因注释；spec.go/import.go 的两处调用暂保持 flag 0，如需统一另行评估。③后端重启必须 `go build -o vmops .`（`go build ./...` 只编译检查不产出到 ./vmops，本轮又踩一次）。- **资源容量/超分卡（仪表盘独有指标）**：`GET /api/dashboard/capacity`（dashboard 组，登录即可）返回 {vm_count, allocated_vcpu, allocated_mem_mb, physical_cores, physical_mem_mb, cpu_ratio, mem_ratio, has_host}。物理量**读本机**（runtime.NumCPU + /proc/meminfo MemTotal；hosts 表的 cpu_cores/memory_gb 从未回写全是 0，仅作兜底）——单管理节点宿主机=本机。聚合 SQL 别名必须与 GORM 命名策略一致：`SUM(v_cpu) AS v_cpu`（字段 VCPU 的列名是 v_cpu 不是 vcpu，别名错了 Scan 静默得 0）。前端卡在虚拟机状态卡下方：分配/物理横条（4× 超分封顶，ratio>1 变橙）+ 超分比数字 + tooltip 解释「KVM 只分配不预留」。
- **详情页可用性三修（用户反馈"乱/看不懂"）**：①「详情/XML」切换初始 undefined 两项都不亮——radio-group 改 `:model-value="devXmlView[k] || 'info'"` + `@update:model-value` 回写，默认点亮「详情」；②「补齐标准设备（guest-agent/rng）」术语劝退——文案简化为「补齐标准设备」+ el-tooltip 白话解释（guest-agent 通道=虚拟机上报 IP 用、virtio-rng=熵池加速开机，缺什么补什么）；③引导顺序 multi-select 下拉无法表达/调整顺序且无解释——重做为「优先级排序列表」（序号圆标 + ↑↓ 移动 + 移除 + 添加下拉，bootChoices 选项自带中文说明），field-tip 写清典型场景：装系统时 cdrom 排最前、装完把 hd 移到第一，多块硬盘共享 hd 类按磁盘先后尝试，PXE 用 network。
- **⚠️ 设备级 XML 编辑器与引导顺序面板已撤销删除（用户实际体验后觉得乱，2026-09 晚）**：VmDetail 磁盘/网卡卡恢复纯详情面板（devXmlView/devXmlDraft/devXmlSaving/loadDeviceXml/saveDeviceXml/watch 全清），`utils/deviceXml.js` 已删（git 历史可找回，思路见本文件历史记录）；「引导顺序」菜单项与面板整块删除（bootInput/bootChoices/moveBoot/applyBoot、Sort/ArrowUp/ArrowDown 图标清理；后端 setBoot API 保留，需要时可从 XML 定义或恢复面板接回）。**GetDomainXML 的 INACTIVE 改动保留**（语义正确：编辑底稿/详情展示都应以持久配置为准）。撤销时的大坑：多次正则脚本叠加把 vue import 行改成了三行垃圾导致 babel 构建失败——连环脚本编辑后必须 build 验证，坏了先查 import 区。
- **宿主机/网络页卡片化（此前讨论过、本轮落地）**：①宿主机页一行表格太空 → **健康大卡**（网格 minmax(420px,1fr)；卡头=名称+状态 tag，信息行=SSH 连接 user@ip:port/描述/登记时间，底部操作组；实时资源仍走「查看状态」SSH 采集弹窗）；②网络页表格 → **网络卡片**（minmax(400px,1fr)；卡头=名称+运行 tag+自启 switch，信息行两列=网桥/转发/网关/DHCP 范围，nc-label 64px nowrap 防换行；未激活卡 opacity 0.75）。网络与 VM 的关联计数列为后续工作（需 libvirt 域网络反查）。
- **setBoot 后端链路删除 + 卡片栅格对齐存储池（用户要求）**：①引导顺序彻底移除——main.go 路由 `PUT /vms/:id/boot`、handler.SetBoot、api.setBoot 全删（virt 层复用的 GetDomainSpec/BuildDomainXML/UpdateDomainXML 是通用方法不删）；②网络卡与宿主机卡从 auto-fill grid（单卡时 1fr 拉满整行 1200px 太宽）改为**存储池同款 el-row/el-col 栅格**（网络 xs24/sm12/md8 三列、宿主机 xs24/sm12 两列，1440 下单卡 382px）；③窄卡适配：nc-actions 加 flex-wrap（四按钮换行）、DHCP 范围行 `.nc-row.wide` 独占一行避免值换行；死样式（.net-cards/.host-cards）清理。
- **宿主机/网络页卡片化（此前讨论过、本轮落地）**：①宿主机页一行表格太空 → **健康大卡**（网格 minmax(420px,1fr)；卡头=名称+状态 tag，信息行=SSH 连接 user@ip:port/描述/登记时间，底部操作组；实时资源仍走「查看状态」SSH 采集弹窗）；②网络页表格 → **网络卡片**（minmax(400px,1fr)；卡头=名称+运行 tag+自启 switch，信息行两列=网桥/转发/网关/DHCP 范围，nc-label 64px nowrap 防换行；未激活卡 opacity 0.75）。网络与 VM 的关联计数列为后续工作（需 libvirt 域网络反查）。
- **创建向导四项改造（2026-09 晚，纯前端）**：①**前置条件检查**——进入向导基于 vmOptions 数据判定网络/安装源（云镜像∪ISO卷∪存量VM）/激活池，缺项在步骤条上方出 warning alert + 直达链接（precheckIssues/hasInstallSource/usablePools computed）；②**存储池下拉带可用空间**（poolLabel=「name（可用 x GB）」，storage_pools.available 字节换算）+ 新系统盘超出池剩余空间红字预警（diskOverPool computed，thin provisioning 未必失败但必须可见）；③**云镜像方式增量盘标注**（"基于「xx」的增量盘，qcow2 backing 不复制镜像文件，初始仅元数据级占用；容量是读写上限"）——扣 KVM 增量链；④**网络下拉按 libvirt forward 类型分组**（el-option-group：NAT 网络/桥接网络/隔离网络，项附网关；networkGroups computed 用 network_info.forward，空时回退平铺）；⑤**cloud-init 双入口收敛**：第 1 步的开关+折叠配置块整体移除（曾与第 4 步绑同一数据令用户困惑），识别系统后留一句"可在第 4 步配置"提示；cloudInitSupported 自动启用逻辑保留。验证状态：B 池空间/D 收敛已实测渲染，A/C 代码就位（浏览器与用户操作冲突未能截图，待用户刷新确认）。
- **IP 获取双通道 + 手动登记（用户问"手动改 IP 的机器没法获取吗"）**：①DHCP 租约匹配只认"从 DHCP 拿地址"的机器，客户机内静态 IP 是盲区——新增 `virt.ListGuestIPs`（libvirt `DomainInterfaceAddresses` SrcAgent，即 qemu-guest-agent 上报，对应 virsh domifaddr --source agent），只取 IPv4、跳过环回；②`syncVMIPs` 增强：DHCP 租约未命中且「运行中 + IP 为空」的 VM 降级走 QGA 探测回填（逐域 RPC 成本高故限定此条件；agent 未装属常态静默跳过，前端 IP 行可手动补登）；③手动登记接口 `PUT /vms/:id/ip` + 前端编辑按钮**已整体撤销**（用户确认所有镜像都会装 guest-agent，QGA 自动路径足够，手动入口纯冗余）；syncVMIPs 只填空/变更不清空。**撤销事故教训**：正则删模板按钮时把 ov-desc 中段 8 行信息 + 处理器面板连带删除且无闭合标签（构建失败才暴露）——正则删模板块后必须检查配对完整性，修复=按原始结构重建 IP 行之后全部信息行与处理器 section。
- **镜像管理页 tab 化（用户拍板"云镜像/base 盘与 ISO 分开放"，双代理并行）**：①后端 `ListImages` 每个 item 新增 `pool` 字段（`h.Virt.ListPools`+`GetPoolPath`，按 image.path 目录前缀匹配池名，**最长路径优先**且要求目录分隔符边界——防 `/a` 池吞 `/abc` 文件；匹配不到置空）；②前端双 tab：「云镜像 / 模板盘」（原登记表全功能+来源存储池列）/「ISO 安装镜像」（只读：名称/大小/来源池/路径；数据对每个 active 池并发调 getStoragePool 聚合 .iso 卷，`Promise.allSettled` 单池失败不拖垮；**首次切 tab 才加载**+手动刷新）；cdrom/ISO 的删除入口统一留在存储池页。两 tab 均已浏览器实测。
- **file-sd 卡片文案澄清（用户困惑"未配置怎么还有 1 个目标"）**：有目标+未配置时表格上方插 info alert——「以下 N 台虚拟机满足自动监控条件（运行中且已获取 IP）／当前 FILE_SD_PATH 未配置，此列表仅为预览，Prometheus 暂不会抓取」；sd-note 双态：启用时保留完整说明，未启用时精简为"需 VM 内安装并运行 node_exporter 才产生数据"。
- **分离磁盘可选删卷（用户反馈"详情页删了磁盘存储池还在"，双代理并行实现）**：①后端 `DetachDisk` 加 `?delete_volume=true`（默认仅分离行为不变）——分离前先取 spec 定位 source/device（spec 拿不到即失败，杜绝"分离了但没删"半完成态）；删卷走四重守卫（cdrom 共享介质不删 / images 库登记不删 / 任何池的 backing 父盘不删 / **仍被其它 VM 挂载不删**——第四条是代理主动补的，与"宁可删不掉不可损坏在用盘"立场一致），守卫命中返回 `volume_deleted:false + keep_reason` 中文原因；纯函数 `detachVolKeepReason` 可单测（`vm_detach_test.go` 6 子用例）。②前端移除确认弹窗改双选项：「仅分离（保留存储卷）」默认 /「分离并删除存储卷」（cdrom 时禁用+说明 ISO 为共享介质）；api.detachDisk 加第三参（query delete_volume）。③E2E 实测：100MB 测试卷挂 Docker → 分离+删卷 → 池与文件系统均消失 ✓；挂共享基镜像 Ubuntu.img → 守卫保留 + Ubuntu.img 完好 ✓。④用户报的两个"bug"核实结论：删 VM 后盘保留=shouldKeepVol 守卫按设计工作（任务 65 kept_volumes 有原因记录）；分离后卷还在=仅分离设计行为（现已补删卷选项）。
- **⚠️ cloud-init 定位终版（用户拍板：只有云镜像才适合它）**：第 1 步移除的 cloud-init 开关+折叠配置**回归第 1 步云镜像方式下**（镜像选中后显示开关[云镜像默认启用] + 配置折叠面板），**第 4 步整个删除**（向导 5 步 → 4 步：安装方式/计算资源/磁盘与网络/确认创建；`step < 4` 改 `step < 3`、确认 pane 条件 step===4→3）。ISO/导入/克隆方式不再显示任何 cloud-init UI（ISO+Ubuntu Server 虽技术上可配，为简洁统一交给云镜像方式）。注意 `next()` 的 per-step 校验与 `step` 索引已同步。
- **父盘链可视化 + 概念澄清（用户困惑"详情看不到 base/ISO 和模板好乱"）**：①`ParseDomainXML` 此前**未解析 backingStore**（DiskSpec.BackingFile 字段定义了但从未填充，详情页"父卷"行永远不显示）——已补：diskXML 与 ParseDomainXML 内联 struct 均加 `BackingStore *backingStoreXML`（可递归多层），chain() 输出"子 ← 父"链；实测 node1 spec 返回 `父卷链: /home/jiuzhao/storage/base/Rocky.img`，磁盘卡"父卷"行（v-if backing_file）随之生效；②镜像管理页加 info 提示条区分两种"镜像"：本页=qcow2 磁盘模板（增量克隆父盘，云镜像方式用），ISO=安装镜像（存 img 池，本地介质方式直接从存储池选，不经本页）。
- **导入磁盘与 ISO cloud-init 语义澄清（用户概念困惑）**：①「导入现有磁盘」=迁移工具（外部系统盘 qcow2 直接挂为系统盘引导，不装系统无增量链，会直接写入该盘）——导入表单顶部加 info 说明；②ISO 方式的 cloud-init **能否生效取决于 ISO 装出的系统是否自带 cloud-init**（Ubuntu Server 自带；Rocky 最小安装/Windows 不带，seed ISO 会挂但没人读）——第 4 步开关旁在 ISO 方式下加黄色警示，引导用户想自动化初始化就改用云镜像方式。
- **⚠️ 删除 import 方式引发向导白屏事故（已修复，教训重要）**：正则删 import 分支时把 `usablePools/hasInstallSource/precheckIssues` 三个 computed **连带误删**（它们与 import 相关代码相邻），模板仍引用 `precheckIssues.length` → 渲染即 TypeError → Vue 生产模式静默中断（**不触发 window error、console.error 只有一行无堆栈的 "Cannot read properties of undefined"，页面整页空白**）。排查路径：sourcemap 构建 + 劫持 console.error 拿到压缩位置 1:11798 → 手写 VLQ 解码 sourcemap 映射回源码（mappings 全在第一"行"且 gen_col 与压缩列不对齐，需直接看压缩产物 11798 处文本定位）→ 发现 `l.precheckIssues.length`。修复=恢复三个 computed（注意 poolLabel/gbText/networkGroups 等其余定义还在 527 行起，勿重复声明）。**两条铁律**：①正则批量删代码后必须立即 `npm run build` + 打开页面验证渲染（本轮 build 过了但运行时崩，构建通过≠页面正常）；②Vue3 生产模式下模板引用未定义变量是静默白屏，排查必须用 sourcemap。
- **⚠️ 「导入现有磁盘」安装方式已删除（用户拍板，勿回退）**：实测暴露三个体验问题（池登记错：默认 base 但盘在 exten；容量登记用表单默认 20GB 与实际盘 2.7G/15G 不符；语义与「导入存量 VM」按钮高度重叠令作者都困惑）——CreateVmWizard 移除 import 方式（方式卡片/importDisk/diskVolumeTree/onImportPick/next 校验分支/activeOsName 与 disks 分支/迁移工具 alert），向导 4 方式变 3 种（ISO/云镜像/克隆）。**保留**：工具栏「导入存量 VM」按钮（ImportVMs 批量纳管 virsh 已有域，另一个功能）；后端 execCreateVM 的 import 分支保留（前端无入口后不可达，删除风险大于收益可后续清理）。import-test 测试 VM（exten/import-test-disk.qcow2）为演示产物，导入后开机引导成功（Rocky 系统正常启动），不需要时可删 VM+盘。
- **ISO 安装流程改造（用户反馈"先选 OS 很乱/只有 img 池"）**：①**表单顺序反转**——安装介质在前、操作系统在后（对齐 virt-manager 介质优先）；②介质选择从"池→卷"两级级联改为**平铺下拉**（扫全部激活存储池的 .iso 卷，项内标注「img 池 · 1.9 GB」，不限 img 池也无歧义）；③**OS 自动识别**：onIsoPick 按 ISO 文件名关键词（ubuntu/rocky/centos/win11/kylin/uos…）到 osList **模糊匹配**（osList 是带版本号的名字如 "Rocky Linux 9"，无裸名，须 includes 匹配而非等值），命中自动填入并显示绿色"已根据 ISO 文件名自动识别，识别错误可手动更改"，识别不出保持空由用户手选；手动路径输入同样触发（watch iso.isoPath）。**坑**：级联换平铺后 onIsoPick 参数从数组变字符串，旧代码 `val[val.length-1]` 取到单字符导致识别失效——换控件类型时回调参数形状必须同步。