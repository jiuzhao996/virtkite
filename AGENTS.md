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
5. **状态映射**：libvirt 状态必须经 `StateToPlatform` 转换，只允许 `running / shut off / paused / error`，与 `vms.status` 一致。
6. **flag 用命名常量**、**XML 用标准库 encoding/xml**、**注释用中文并注明 virsh 等价命令**。
7. 新增 virt 方法前先读 `service/virt/` 对应文件 + `vmops-libvirt` skill，保持风格一致。

## 前端开发标准

1. 用 `ui-ux-pro-max` 规范：统一间距（8px 栅格）、配色（覆盖 `--el-color-primary`）、组件质感，禁止 emoji 当图标、禁止硬编码散落颜色。
2. 图标统一用 `@element-plus/icons-vue`（已在依赖中）。
3. 新增页面遵循现有目录结构（`views/`、`api/index.js`、`store/auth.js`）。

## 近期修复记录（勿回退）

- virt 层全部错误 `%v` → `%w`（40 处）
- `getConn()` 断线自动恢复（原先 `Reset()` 从未被调用）
- 快照名 XML 转义（`xmlEscape`）
- handler 层 36 处 `err.Error()` 泄漏改为统一 `ErrorResponse`/`ErrorWithMessage`
- 连接错误补中文前缀（`virt.go` `Connect`）

## 部署/运行

- 后端：`go run main.go`（默认 `:8080`）；前端：`cd web && npm run dev`（`/api` 代理到 `:8080`）
- 生产：`cd web && npm run build`，Go 后端自动托管 `web/dist`（**index.html 启动时读入内存，改前端后必须 build + 重启后端才生效**）

## 运行与启停（易踩坑）

- 一键脚本：`./start.sh`（前台）/ `./start.sh --service`（systemd-run）/ `./start.sh --stop`；也可 `setsid bash -c './vmops > vmops.log 2>&1 & echo $! > vmops.pid'` 手动后台。
- **⚠️ 旧进程占坑坑（重点）**：`vmops.pid` 可能陈旧，`kill $(cat vmops.pid)` 可能杀错对象，导致 8080 仍被旧进程占用、**新二进制从未生效**（症状：改了代码行为不变）。正确重启流程：`ss -tlnp | grep :8080` 看实际 PID → `kill -9 <实际PID>` → 删 `vmops.pid` → 启动 → 核对新 PID 与 `pgrep -af '\./vmops'`。
- **websockify 不保活**：控制台依赖 `websockify --web /usr/share/novnc --token-plugin JSONTokenApi --token-source http://127.0.0.1:8080/api/vnc/token/%s 6080`，掉线则 noVNC/控制台全不可用；排查控制台先查 `pgrep -af websockify`。
- 依赖：MySQL 在 Docker（容器 `vmops-mysql`，`:3306`）；VNC 端口由 libvirt autoport 分配（本机当前 5900）。
- 静态资源（图片等）放 `web/src/assets/` 走 import 打包；**禁止放 `dist/assets/`**（build 会清空）。

## 控制台设计约定（ConsolePage.vue，勿回退）

- 入口：VM 列表「控制台」→ 站内路由 `/console/:id`；**白底选择页 + 左侧可折叠侧边栏**，三种连接：🖥️ 图形控制台(VNC) / ⌨️ Web终端(SSH) / ▮ 串口 Console。
- **智能默认**：VM 运行中进入页面自动尝试串口 Console（免 IP 最轻），连上直接进；失败自动回选择页并把串口卡标注「不可用：原因」。
- 视觉：VNC 浅色干净（**无背景图**）；SSH/串口深色 + `console-bg.jpg` 背景、opspilot 风格顶栏（🕐 实时时钟/用户/hostname/●状态）、倾斜水印、底部栏（重新连接/断开 + 复制提示 + 终端尺寸）。
- 后端接口：`POST /api/vms/:id/vnc-token`；`GET /api/vms/:id/terminal`（WS→SSH 桥，首消息 `{"type":"auth",host,port,user,password}`，输入 JSON `input`）；`GET /api/vms/:id/serial`（WS→串口，首消息回 `{"type":"connected"}`，浏览器直接发原始字节）。
- **串口实现走 libvirt `DomainOpenConsoleBidirectional`**（`service/virt/console.go`），**禁止直接打开 /dev/pts/* 路径**（属主 root，权限不足）。
- WS 鉴权走 `?token=<JWT>` 查询参数（`middleware/jwt.go` 已支持，浏览器 WS 无法带 Header）。

## 并发协作注意

- 本目录可能被**多个 opencode 终端同时编辑**（共享同一工作树，改动均未提交）。动手前/提交前先 `git status`、`git diff` 确认，避免覆盖他人未提交的改动；不要在别人重构中途大改同一批文件。