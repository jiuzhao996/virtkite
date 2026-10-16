<template>
  <div ref="termViewEl" class="term-view" :style="{ backgroundImage: 'url(' + consoleBg + ')' }">
    <!-- 星星划过背景（坐标来自一次性生成的模块级常量）→ TermStars.vue -->
    <TermStars />

    <!-- JumpServer 风格顶部信息栏 -->
    <div class="term-header">
      <div class="term-header-left">
        <span v-if="connected" class="term-status online">● 已连接</span>
        <span v-else-if="connecting" class="term-status connecting">● 连接中</span>
        <span v-else class="term-status offline">○ 未连接</span>
      </div>
      <div class="term-header-center">
        <span class="term-user"><el-icon><User /></el-icon>当前用户：{{ currentUser }}</span>
        <span class="term-divider">|</span>
        <span class="term-host"><el-icon><Monitor /></el-icon>{{ hostLabel }}</span>
      </div>
      <div class="term-header-right">
        <span class="term-clock"><el-icon><Clock /></el-icon>{{ clock || '--' }}</span>
      </div>
    </div>

    <div v-if="termError" class="term-error">
      <el-icon><WarningFilled /></el-icon>{{ termError }}
      <el-button size="small" type="warning" plain class="term-error-retry" @click="reconnect">一键重连</el-button>
    </div>

    <!-- SSH 连接表单 → TermSshForm.vue（sshForm 直接改字段，connect 上抛） -->
    <TermSshForm
      v-if="mode === 'ssh' && !connected"
      :form="sshForm"
      :connecting="connecting"
      @connect="connectSSH"
    />

    <!-- 串口连接面板：无表单，醒目入口 → TermSerialPanel.vue -->
    <TermSerialPanel
      v-else-if="mode === 'serial' && !connected"
      :connecting="connecting"
      @connect="connectSerial"
    />

    <!-- 终端主体（SSH / 串口共用） -->
    <div v-else-if="connected" class="term-body">
      <div ref="termEl" class="terminal-container" />
    </div>

    <!-- 底部操作栏 → TermFooter.vue（各动作经 emit 回本组件执行） -->
    <TermFooter
      :connected="connected"
      :connecting="connecting"
      :is-term-full="isTermFull"
      @back="emit('back')"
      @reconnect="reconnect"
      @disconnect="disconnectFromTerminal"
      @paste="pasteFromClipboard"
      @font-change="changeTermFont"
      @fullscreen="toggleTermFullscreen"
    />
  </div>
</template>

<script setup>
// ============================================================================
// SSH / 串口共用终端视图（TermView）
// ----------------------------------------------------------------------------
// 职责：一套终端壳，按 mode 分流——未连接时展示 SSH 表单或串口入口面板，连接后用 xterm
//       读写 WS 数据流。WS 生命周期 / 串口探测 / 剪贴板 / 字号 / 全屏 / resize 校准等
//       协议层逻辑原样保留（SSH 走 JSON 帧、串口发原始字节）。
//   壳（ConsolePage）负责视图切换与「智能默认串口探测」决策，探测结果经 probe-failed 上抛。
//
// 数据流：connectSSH/connectSerial → openWsWithTimeout(一次性短时票据) → xterm ↔ ws；
//   输入 term.onData → ws.send；输出 handleMsg → term.write；resize 同步 SSH PTY 尺寸。
//
// 已拆出的子组件（同目录）：
//   TermStars.vue         星星划过装饰背景（自持 STARS 常量）
//   TermSshForm.vue       SSH 未连接表单（form 直连 sshForm，connect 上抛）
//   TermSerialPanel.vue   串口未连接入口面板（connect 上抛）
//   TermFooter.vue        底部操作栏（返回/重连/断开/粘贴/字号/全屏 全 emit 上抛）
//
// 刻意没拆的：顶部信息栏（.term-header，直读 connected/connecting/clock/hostLabel）、错误条、
//   终端主体——后者用 ref="termEl" 直连 xterm 实例（term.open），ref 无法外传，必须内联。
// ⚠️ .form-title .el-icon 的图标基线微调原与 header/error 共用一条规则，拆出后 TermSshForm
//   单独保留一份；≤640px 媒体查询按归属拆到 TermSshForm / TermFooter。
// ============================================================================
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { ElMessage } from 'element-plus'
// 图标一律用组件（禁止 emoji 当图标）。main.js 已全量全局注册，这里仍显式 import：
// 一是模板里能看出图标来源，二是将来改按需引入不用回头翻模板。
import { Clock, Monitor, User, WarningFilled } from '@element-plus/icons-vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { api } from '../../../api'
import consoleBg from '../../../assets/console-bg.webp'
import { buildTermTheme, VM_TERM_SURFACE } from '../../../utils/term-theme'
import TermStars from './TermStars.vue'
import TermSshForm from './TermSshForm.vue'
import TermSerialPanel from './TermSerialPanel.vue'
import TermFooter from './TermFooter.vue'

const props = defineProps({
  mode: { type: String, required: true }, // 'ssh' | 'serial'
  vmId: { type: [String, Number], required: true },
  vmName: { type: String, default: '' },
  currentUser: { type: String, default: '—' },
  // SSH 表单预填（壳加载 VM 后回填 IP）
  initialHost: { type: String, default: '' },
  // 壳的「智能默认」探测：true 时串口连不上要上抛 probe-failed（回选择页并标注原因）
  probe: { type: Boolean, default: false }
})
const emit = defineEmits(['back', 'probe-failed', 'serial-ok'])

// 星星背景坐标常量（STARS）已随装饰层下沉到 TermStars.vue（同目录）

// 终端字号偏好：A-/A+ 调节，localStorage 持久化（11~24，越界截断）
const TERM_FONT_KEY = 'vmops-term-font'
const TERM_FONT_MIN = 11
const TERM_FONT_MAX = 24
function loadTermFontSize() {
  const saved = parseInt(localStorage.getItem(TERM_FONT_KEY) || '', 10)
  if (!Number.isFinite(saved)) return 15
  return Math.min(TERM_FONT_MAX, Math.max(TERM_FONT_MIN, saved))
}

// SSH 表单
const sshForm = ref({ host: props.initialHost || '', port: 22, user: 'root', password: '' })

// 终端共用状态
const connected = ref(false)
const connecting = ref(false)
const termError = ref('')
const termEl = ref(null)
const clock = ref('')
const isTermFull = ref(false)
const termViewEl = ref(null)

let term = null
let fitAddon = null
let ws = null
let timeTimer = null
let resizeHandler = null
let probeMode = false
let probeTimer = null

const hostLabel = computed(() => {
  if (props.mode === 'ssh') {
    if (!sshForm.value.host) return '-'
    return `${sshForm.value.user}@${sshForm.value.host}:${sshForm.value.port}`
  }
  return props.vmName || '-'
})

function updateTime() {
  clock.value = new Date().toLocaleString('zh-CN', {
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}
function startClock() {
  if (timeTimer) clearInterval(timeTimer)
  updateTime()
  timeTimer = setInterval(updateTime, 1000)
}
function stopClock() {
  if (timeTimer) { clearInterval(timeTimer); timeTimer = null }
}

function sshMemoryKey() {
  return `vmops-ssh-${props.vmId}`
}
function restoreSshForm() {
  try {
    const raw = localStorage.getItem(sshMemoryKey())
    if (!raw) return
    const saved = JSON.parse(raw)
    if (saved.host) sshForm.value.host = saved.host
    if (saved.port) sshForm.value.port = saved.port
    if (saved.user) sshForm.value.user = saved.user
  } catch (e) {
    // 忽略损坏的缓存
  }
}
// 连接成功后记忆参数（密码永不落盘）
function rememberSshForm() {
  try {
    localStorage.setItem(sshMemoryKey(), JSON.stringify({
      host: sshForm.value.host,
      port: sshForm.value.port,
      user: sshForm.value.user
    }))
  } catch (e) {
    // 配额不足等忽略
  }
}

function clearProbe() {
  probeMode = false
  if (probeTimer) { clearTimeout(probeTimer); probeTimer = null }
}

function failProbe(reason) {
  clearProbe()
  cleanupConnection()
  ElMessage.warning('串口不可用：' + reason + '，已回到选择页')
  emit('probe-failed', reason)
}

// 切走/卸载时彻底清理连接
function cleanupConnection() {
  stopClock()
  clearProbe()
  if (ws) {
    ws.onopen = null; ws.onmessage = null; ws.onerror = null; ws.onclose = null
    try { ws.close() } catch (e) {}
    ws = null
  }
  if (term) {
    if (resizeHandler) window.removeEventListener('resize', resizeHandler)
    resizeHandler = null
    try { term.dispose() } catch (e) {}
    term = null
    fitAddon = null
  }
  connected.value = false
  connecting.value = false
  if (!(arguments[0] && arguments[0].keepError)) termError.value = ''
}

function disconnectFromTerminal() {
  cleanupConnection()
  ElMessage.info('已断开连接')
}

// ---------- 全屏（Fullscreen API，只管本视图根元素） ----------
function exitFullscreenIfAny() {
  if (document.fullscreenElement) {
    document.exitFullscreen().catch(() => { /* 用户已退出等场景忽略 */ })
    return true
  }
  return false
}
function requestFullscreen(el) {
  if (!el || !el.requestFullscreen) {
    ElMessage.warning('当前浏览器不支持全屏')
    return
  }
  el.requestFullscreen().catch(() => ElMessage.warning('进入全屏失败'))
}
function toggleTermFullscreen() {
  if (!exitFullscreenIfAny()) requestFullscreen(termViewEl.value)
}
// 进入/退出全屏后容器尺寸突变：终端重新 fit（SSH 同时同步 PTY 尺寸）
function onFullscreenChange() {
  const fs = document.fullscreenElement
  isTermFull.value = !!fs && fs === termViewEl.value
  nextTick(() => onResize())
}

// ---------- 剪贴板 ----------
async function pasteFromClipboard() {
  if (!ws || ws.readyState !== WebSocket.OPEN) return
  try {
    const text = await navigator.clipboard.readText()
    if (!text) return
    if (props.mode === 'ssh') ws.send(JSON.stringify({ type: 'input', data: text }))
    else ws.send(text) // 串口：直接发原始字节
  } catch (e) {
    ElMessage.warning('浏览器未授权剪贴板')
  }
}
// xterm 自定义按键：Ctrl+Shift+C 复制选区 / Ctrl+V、Ctrl+Shift+V 粘贴，其余按键原样放行
function termClipboardKeyHandler(ev) {
  if (ev.type !== 'keydown') return true
  if (ev.ctrlKey && ev.shiftKey && (ev.key === 'C' || ev.key === 'c')) {
    if (term && term.hasSelection()) {
      navigator.clipboard.writeText(term.getSelection()).catch(() => { /* 剪贴板不可用静默 */ })
      return false
    }
    return true // 无选区不拦截
  }
  if (ev.ctrlKey && (ev.key === 'v' || ev.key === 'V')) {
    pasteFromClipboard()
    return false
  }
  return true
}

// ---------- 终端字号 ----------
function changeTermFont(delta) {
  if (!term) return
  const cur = term.options.fontSize || 15
  const next = Math.min(TERM_FONT_MAX, Math.max(TERM_FONT_MIN, cur + delta))
  if (next === cur) return
  term.options.fontSize = next
  try { localStorage.setItem(TERM_FONT_KEY, String(next)) } catch (e) { /* 配额不足忽略 */ }
  onResize() // 字号变化行列数随之变化：fit + 同步 SSH PTY 尺寸
}

// 取一次性短时票据建连：浏览器 WS 无法带 Authorization 头，票据一次性 + 30s，
// 即便进访问日志也只是废票（不再像 ?token= 那样泄漏小时级全权限 JWT）。
async function openWs(path) {
  const res = await api.wsTicket(props.vmId)
  const ticket = (res.data && res.data.ticket) || ''
  if (!ticket) throw new Error('连接凭证获取失败')
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return new WebSocket(`${proto}//${location.host}/api/vms/${props.vmId}/${path}?ticket=${encodeURIComponent(ticket)}`)
}

// 建连超时兜底：代理/网络黑洞导致 WS open 挂起时，避免“连接中…”无限转圈
async function openWsWithTimeout(path, ms = 10000) {
  const socket = await openWs(path) // 取票失败直接抛出，由调用方按连接失败处理
  return new Promise((resolve, reject) => {
    socket.binaryType = 'arraybuffer'
    const timer = setTimeout(() => {
      try { socket.close() } catch (e) {}
      reject(new Error('WebSocket 连接超时'))
    }, ms)
    socket.onopen = () => { clearTimeout(timer); resolve(socket) }
    socket.onerror = () => { clearTimeout(timer); reject(new Error('WebSocket 连接失败')) }
  })
}

async function connectSSH() {
  if (!sshForm.value.host || !sshForm.value.user || !sshForm.value.password) {
    ElMessage.warning('请填写主机、用户名和密码')
    return
  }
  cleanupConnection()
  connecting.value = true
  startClock()
  try {
    ws = await openWsWithTimeout('terminal')
    connected.value = true
    await nextTick()
    initTerminal()
    ws.onmessage = handleMsg
    ws.onclose = onWsClose
    ws.onerror = () => { if (ws) termError.value = 'WebSocket 错误' }
    ws.send(JSON.stringify({
      type: 'auth',
      host: sshForm.value.host,
      port: sshForm.value.port,
      user: sshForm.value.user,
      password: sshForm.value.password,
    }))
  } catch (e) {
    termError.value = e.message || '连接失败'
    connected.value = false
    if (ws) { ws.close(); ws = null }
  } finally {
    connecting.value = false
  }
}

async function connectSerial() {
  cleanupConnection()
  connecting.value = true
  startClock()
  try {
    ws = await openWsWithTimeout('serial')
    connected.value = true
    await nextTick()
    initTerminal()
    ws.onmessage = handleMsg
    ws.onclose = onWsClose
    ws.onerror = () => { if (ws) termError.value = 'WebSocket 错误' }
  } catch (e) {
    termError.value = e.message || '连接失败'
    connected.value = false
    if (ws) { ws.close(); ws = null }
    if (probeMode) failProbe(e.message || 'WebSocket 连接失败')
  } finally {
    connecting.value = false
  }
}

function reconnect() {
  if (props.mode === 'ssh') connectSSH()
  else if (props.mode === 'serial') connectSerial()
}

function onWsClose() {
  const wasConnecting = connecting.value
  connected.value = false
  connecting.value = false
  // 探测期静默断开也算失败：回到选择页并标注原因（勿回退智能默认约定）
  if (probeMode) {
    failProbe('连接已断开')
    return
  }
  // 建连中途断开（非主动清理）：给出提示，避免静默停留在未连接态
  if (wasConnecting) termError.value = '连接已断开，请重试'
}

async function initTerminal() {
  await nextTick()
  if (!termEl.value) return
  term = new Terminal({
    cursorBlink: true,
    cursorStyle: 'bar',
    fontSize: loadTermFontSize(), // A-/A+ 可调（11~24），localStorage 持久化
    fontFamily: "'Cascadia Code', 'Fira Code', Consolas, monospace",
    theme: buildTermTheme(VM_TERM_SURFACE),
  })
  fitAddon = new FitAddon()
  term.loadAddon(fitAddon)
  term.attachCustomKeyEventHandler(termClipboardKeyHandler) // Ctrl+Shift+C 复制 / Ctrl+V 粘贴
  term.open(termEl.value)
  fitAddon.fit()

  term.onData((data) => {
    if (!ws || ws.readyState !== WebSocket.OPEN) return
    if (props.mode === 'ssh') ws.send(JSON.stringify({ type: 'input', data }))
    else ws.send(data) // 串口：直接发原始字节
  })

  resizeHandler = () => onResize()
  window.addEventListener('resize', resizeHandler)
  termEl.value.addEventListener('click', () => term && term.focus())
  term.focus()
}

function onResize() {
  if (fitAddon) fitAddon.fit()
  if (props.mode === 'ssh' && ws && ws.readyState === WebSocket.OPEN && term) {
    ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
  }
}

// 暴露给壳：侧栏折叠/展开导致容器宽度变化时，壳 watch 后调用补一次 fit
defineExpose({ refit: onResize })

function handleMsg(ev) {
  if (!term) return
  if (ev.data instanceof ArrayBuffer) {
    clearProbe()
    term.write(new Uint8Array(ev.data))
    return
  }
  if (ev.data instanceof Blob) {
    clearProbe()
    ev.data.arrayBuffer().then((buf) => { if (term) term.write(new Uint8Array(buf)) })
    return
  }
  try {
    const msg = JSON.parse(ev.data)
    if (msg.type === 'error') {
      if (probeMode) {
        failProbe(msg.msg || '不可用')
        return
      }
      ElMessage.error(msg.msg || '连接失败')
      termError.value = msg.msg || '连接失败'
      // ⚠️ 不能走 disconnectFromTerminal——其 cleanupConnection 会清空 termError，
      // 错误条瞬间消失（用户实测「点了连接啥也没发生」）。此处静默清理连接、保留错误显示
      cleanupConnection({ keepError: true })
    } else if (msg.type === 'connected') {
      clearProbe()
      if (props.mode === 'serial') emit('serial-ok')
      // SSH 连通成功后记忆参数（下次自动填，密码不记）
      if (props.mode === 'ssh') rememberSshForm()
      onResize()
    }
  } catch {
    clearProbe()
    term.write(ev.data)
  }
}

onMounted(() => {
  document.addEventListener('fullscreenchange', onFullscreenChange)
  startClock()
  if (props.mode === 'ssh') restoreSshForm()
  // 串口：进入即连（侧栏点选与「智能默认」探测都是这个行为）
  if (props.mode === 'serial') {
    if (props.probe) probeMode = true
    connectSerial()
    // 兜底：若 2.5s 内既无 error 也无 connected，视为已连上
    if (props.probe) probeTimer = setTimeout(() => { clearProbe() }, 2500)
  }
})
onUnmounted(() => {
  document.removeEventListener('fullscreenchange', onFullscreenChange)
  cleanupConnection()
})
</script>

<style scoped>
/* 行内图标统一微调（图标一律用组件，禁 emoji）：el-icon 按基线对齐与中文混排偏高，下压 0.15em */
.term-user .el-icon,
.term-host .el-icon,
.term-clock .el-icon,
.term-error .el-icon {
  vertical-align: -0.15em;
  margin-right: 4px;
}
/* .form-title .el-icon 已随 SSH 表单下沉到 TermSshForm.vue（该组件单独保留一份） */

.term-view {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background-size: cover;
  background-position: center;
  overflow: hidden;
}

/* .stars-bg / .star / @keyframes shootingStar 已下沉到 TermStars.vue */

/* ---------- JumpServer 风格顶部信息栏 ---------- */
.term-header {
  position: relative;
  z-index: 3;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 16px;
  background: var(--term-overlay);
  backdrop-filter: blur(4px);
  border-bottom: 1px solid var(--term-line);
  color: var(--term-text-dim);
  font-size: 0.95rem;
}
.term-header-left, .term-header-center, .term-header-right {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: 1;
  min-width: 0;
}
.term-header-center { justify-content: center; }
.term-header-right { justify-content: flex-end; flex-shrink: 0; }
.term-status { white-space: nowrap; }
.term-divider { color: rgba(88, 166, 255, 0.2); }
.term-status.online { color: var(--term-ok); font-weight: 600; }
.term-status.connecting { color: var(--term-warn); font-weight: 600; animation: term-breathe 1.2s ease-in-out infinite; }
.term-status.offline { color: var(--term-off); }
@keyframes term-breathe { 0%, 100% { opacity: 1; } 50% { opacity: 0.35; } }
.term-error-retry { margin-left: 12px; }
.term-shortcut { display: inline-flex; align-items: center; gap: 4px; color: var(--term-off); font-size: 12px; margin-right: 8px; }
.term-user { color: var(--term-text); }
.term-host { color: var(--term-text-dim); }
.term-user, .term-host {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
.term-clock {
  font-family: 'SF Mono', 'Cascadia Code', Consolas, monospace;
  color: var(--term-accent-bright);
  font-weight: 500;
}

.term-error {
  position: relative;
  z-index: 3;
  margin: 8px 16px 0;
  padding: 8px 14px;
  border-radius: var(--radius-sm);
  background: rgba(248, 81, 73, 0.1);
  border: 1px solid rgba(248, 81, 73, 0.2);
  color: var(--term-err);
  font-size: 0.85rem;
}

/* ---------- SSH 连接表单 / 串口连接面板 ----------
   .ssh-form-wrap / .ssh-form / .form-title / .form-btn 已下沉到 TermSshForm.vue；
   .serial-* 已下沉到 TermSerialPanel.vue（各自保留一份，改一处须同步）。 */

/* ---------- 终端主体（含水印） ---------- */
.term-body {
  position: relative;
  z-index: 1;
  flex: 1;
  min-height: 0;
  margin: 6px 12px 0;
  border-radius: var(--radius-md);
  overflow: hidden;
  background: transparent;
}
.terminal-container { width: 100%; height: 100%; position: relative; z-index: 1; }
.terminal-container :deep(.xterm) {
  height: 100% !important;
  padding: 8px 12px;
  background: transparent !important;
}
.terminal-container :deep(.xterm-viewport) {
  overflow-y: auto !important;
  background: transparent !important;
}


/* ---------- 底部操作栏 ----------
   .term-footer / .ft-btn / .term-shortcut 已下沉到 TermFooter.vue（含其 ≤640px 媒体查询）。 */

/* ---------- 窄屏适配（≤640px）：header 三段换行 ----------
   footer / ssh-form 的窄屏规则已随各自组件下沉（TermFooter / TermSshForm）。 */
@media (max-width: 640px) {
  .term-header {
    flex-wrap: wrap;
    row-gap: 4px;
    font-size: 0.8rem;
    padding: 8px 12px;
  }
  .term-header-left { flex: 1 1 auto; }
  .term-header-right { flex: 0 0 auto; }
  .term-header-center {
    order: 3;
    flex: 1 1 100%;
    justify-content: flex-start;
  }
}
</style>
