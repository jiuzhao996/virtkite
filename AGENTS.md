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

## 部署/运行

- 后端：`go run main.go`（默认 `:8080`）；前端：`cd web && npm run dev`（`/api` 代理到 `:8080`）
- 生产：`cd web && npm run build`，Go 后端自动托管 `web/dist`（**index.html 启动时读入内存，改前端后必须 build + 重启后端才生效**）

## 运行与启停（易踩坑）

- 一键脚本：`./start.sh`（前台）/ `./start.sh --service`（systemd-run）/ `./start.sh --stop`；也可 `setsid bash -c './vmops > vmops.log 2>&1 & echo $! > vmops.pid'` 手动后台。
- **✅ 已修复：`go run main.go` 曾必然启动失败**。原静态托管只看 `<exeDir>/web/dist` 与 `<exeDir>/static`，而 `go run` 时二进制位于 `/tmp/go-build*/`，exeDir 下必然无产物，随即 `log.Fatalf("读取前端入口失败")` 退出——而 README 与本文件都把 `go run main.go` 写成标准运行方式。修复方式（`main.go` 的 `locateWebRoot`）：按 `<exeDir>/web/dist` → `<exeDir>/static` → `<cwd>/web/dist` → `<cwd>/static` 四候选依次探测，命中即打印 `前端产物目录: <路径>`（改前端没生效多半是命中了另一份产物）；全未命中时**降级为「仅 API」模式** + `/` 与 NoRoute 返回 503 中文提示页，不再 `log.Fatalf`（后端 `go run` + 前端 `npm run dev` 是正常开发姿势）。
- **⚠️ 旧进程占坑坑（重点）**：`vmops.pid` 可能陈旧，`kill $(cat vmops.pid)` 可能杀错对象，导致 8080 仍被旧进程占用、**新二进制从未生效**（症状：改了代码行为不变）。正确重启流程：`ss -tlnp | grep :8080` 看实际 PID → `kill -9 <实际PID>` → 删 `vmops.pid` → 启动 → 核对新 PID 与 `pgrep -af '\./vmops'`。
- **websockify 不保活**：控制台依赖 `websockify --web /usr/share/novnc --token-plugin JSONTokenApi --token-source http://127.0.0.1:8080/api/vnc/token/%s 6080`，掉线则 noVNC/控制台全不可用；排查控制台先查 `pgrep -af websockify`。
- 依赖：MySQL 在 Docker（容器 `vmops-mysql`，`:3306`）；VNC 端口由 libvirt autoport 分配（本机当前 5900）。
- 静态资源（图片等）放 `web/src/assets/` 走 import 打包；**禁止放 `dist/assets/`**（build 会清空）。
- **`SERVER_MODE=release` 时必须显式设置 `JWT_SECRET_KEY`**，且不能等于内置默认值 `vmops-jwt-secret-key-change-in-production`，否则启动即 `log.Fatalf` 拒绝（默认值公开可见，任何人都能伪造 admin token）。

## 控制台设计约定（ConsolePage.vue，勿回退）

- 入口：VM 列表「控制台」→ 站内路由 `/console/:id`；**白底选择页 + 左侧可折叠侧边栏**，三种连接：`Monitor` 图形控制台(VNC) / `Platform` Web终端(SSH) / `Connection` 串口 Console。
- **图标一律用 `@element-plus/icons-vue` 组件，不用 emoji**（见「前端开发标准」第 3 条）。本页固定映射，**改动请沿用，勿回退成 emoji**：
  | 语义 | 图标组件 | 位置 |
  |---|---|---|
  | 图形控制台 / VNC / hostname | `Monitor` | 顶栏提示、侧栏、选择卡、VNC 已连接条、终端顶栏 host |
  | Web 终端 (SSH) | `Platform` | 顶栏提示、侧栏、选择卡、SSH 表单标题 |
  | 串口 Console | `Connection` | 顶栏提示、侧栏、选择卡、串口面板大图标 |
  | 实时时钟 | `Clock` | 终端顶栏右侧 |
  | 当前用户 | `User` | 终端顶栏中部 |
  | VMOps 标识（安全/堡垒机语义） | `Lock` | 终端顶栏左侧，与 `Login.vue` 品牌图标一致 |
  | 提示 | `InfoFilled` | 终端底栏复制提示、`Login.vue` 演示账号条 |
  | 重新连接 | `Refresh` | 终端底栏 |
  | 错误/警告 | `WarningFilled` | `.term-error` |
  | 推荐标记 | `StarFilled` | 串口卡「先尝试它」徽标 |
  | 返回 / 侧栏折叠 | `ArrowLeft` / `ArrowRight` | 顶栏返回、底栏返回选择、`.collapse-btn` |
  | 连接串口（启动动作） | `CaretRight` | 串口面板主按钮 |

  - **`●`／`○` 状态圆点保留字符**（`.term-status` 已连接/未连接、`.card-badge` 可用/需运行中/不可用），它们是装饰性状态指示符不是图标。
  - 视觉尺寸靠 CSS 兜住，勿删：`.card .card-icon` 用 `font-size: 2.4rem` + `height: 2.6rem` + 显式配色（`#58a6ff`，串口卡 `#f0b90b`）保持与原 emoji 等大且卡片总高不变；`.nav-item .icon` 用 `inline-flex` + `1.15rem` 保证折叠态居中不塌陷；行内图标统一 `vertical-align: -0.15em` + `margin-right: 4px`（`el-icon` 是 `inline-flex`、按基线对齐，与中文混排会偏高）；`.serial-big-icon` 的金色发光从 `text-shadow` 改成 `.el-icon` 上的 `filter: drop-shadow(...)`（svg 不吃 `text-shadow`）。
- **按角色分入口（P1）**：admin 三入口齐全；**viewer 只有图形控制台且为只读**——侧栏与选择卡都不渲染 SSH / 串口入口（`v-if="isAdmin"`），选择页多一条黄色说明条（`.pick-note`），`select()` 内另有兜底提示；VNC 连上后顶栏显示「只读观看（键鼠已禁用）」标签。智能默认（自动尝试串口）仅对 admin 生效，viewer 直接留在选择页，串口卡标注「仅管理员可用」。
- **智能默认**：VM 运行中进入页面自动尝试串口 Console（免 IP 最轻），连上直接进；失败自动回选择页并把串口卡标注「不可用：原因」。
- 视觉：VNC 浅色干净（**无背景图**）；SSH/串口深色 + `console-bg.jpg` 背景、opspilot 风格顶栏（`Clock` 实时时钟 / `User` 用户 / `Monitor` hostname / `●` 状态）、倾斜水印、底部栏（重新连接/断开 + 复制提示 + 终端尺寸）。
- 后端接口：`POST /api/vms/:id/vnc-token`（响应含 `view_only`，非 admin 为 `true`，前端据此拼 `&view_only=1`）；`GET /api/vms/:id/terminal`（WS→SSH 桥，首消息 `{"type":"auth",host,port,user,password}`，输入 JSON `input`；**目标须过 `validateSSHTarget` 白名单**）；`GET /api/vms/:id/serial`（WS→串口，首消息回 `{"type":"connected"}`，浏览器直接发原始字节）。后两者对 viewer 返回 403。
- **串口实现走 libvirt `DomainOpenConsoleBidirectional`**（`service/virt/console.go`），**禁止直接打开 /dev/pts/* 路径**（属主 root，权限不足）。
- WS 鉴权走 `?token=<JWT>` 查询参数（`middleware/jwt.go` 已支持，浏览器 WS 无法带 Header）。
- **WS 写入一律经 `console.NewConn(rawConn)` 包装**（见后端标准第 9 条），禁止回退成裸 `*websocket.Conn`。


## 并发协作注意

- 本目录可能被**多个 opencode 终端同时编辑**（共享同一工作树，改动均未提交）。动手前/提交前先 `git status`、`git diff` 确认，避免覆盖他人未提交的改动；不要在别人重构中途大改同一批文件。