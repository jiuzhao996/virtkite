<template>
  <div ref="termViewEl" class="term-view" :style="{ backgroundImage: 'url(' + consoleBg + ')' }">
    <!-- 星星划过背景：坐标来自一次性生成的 STARS 常量（内联 Math.random 会随每秒时钟重渲染而瞬移） -->
    <div class="stars-bg">
      <div v-for="(s, i) in STARS" :key="i" class="star" :style="{
        left: s.left + '%',
        top: s.top + '%',
        animationDelay: s.delay + 's',
        animationDuration: s.duration + 's',
      }" />
    </div>

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

    <!-- SSH 连接表单 -->
    <div v-if="mode === 'ssh' && !connected" class="ssh-form-wrap">
      <div class="ssh-form">
        <h3 class="form-title"><el-icon><Platform /></el-icon>SSH 连接</h3>
        <el-form label-width="70px">
          <el-form-item label="主机">
            <el-input v-model="sshForm.host" placeholder="VM IP 或域名，默认取虚拟机 IP" />
          </el-form-item>
          <el-form-item label="端口">
            <el-input-number v-model="sshForm.port" :min="1" :max="65535" controls-position="right" style="width: 100%" />
          </el-form-item>
          <el-form-item label="用户名">
            <el-input v-model="sshForm.user" placeholder="如 root" />
          </el-form-item>
          <el-form-item label="密码">
            <el-input v-model="sshForm.password" type="password" show-password @keyup.enter="connectSSH" />
          </el-form-item>
        </el-form>
        <el-button type="primary" class="form-btn" :loading="connecting" @click="connectSSH">连接终端</el-button>
      </div>
    </div>

    <!-- 串口连接面板：无表单，醒目入口 -->
    <div v-else-if="mode === 'serial' && !connected" class="serial-panel">
      <div class="serial-big-icon"><el-icon><Connection /></el-icon></div>
      <div class="serial-title">串口 Console · 免 IP 直连</div>
      <div class="serial-desc">等价 virsh console，直接读写 guest 串口 ttyS0。无需 IP / 账号，无网卡也能进系统，建议优先尝试。</div>
      <el-button type="primary" size="large" class="serial-btn" :loading="connecting" @click="connectSerial">
        <el-icon v-if="!connecting"><CaretRight /></el-icon>
        <span>{{ connecting ? '连接中…' : '连接串口 Console' }}</span>
      </el-button>
      <div class="serial-hint">
        <el-icon><InfoFilled /></el-icon>
        <span>连上却无输出、敲键无回显？通常是客户机没在 ttyS0 起终端：请在客户机内执行 <code>systemctl enable --now serial-getty@ttyS0</code>，并把 <code>console=ttyS0</code> 追加到内核 cmdline（写入 <code>/etc/default/grub</code> 后执行 <code>grub2-mkconfig -o /boot/grub2/grub.cfg</code> 并重启生效）。</span>
      </div>
    </div>

    <!-- 终端主体（SSH / 串口共用） -->
    <div v-else-if="connected" class="term-body">
      <div ref="termEl" class="terminal-container" />
    </div>

    <!-- 底部操作栏 -->
    <div class="term-footer">
      <div class="term-footer-left">
        <template v-if="connected">
          <el-button size="small" class="ft-btn" :loading="connecting" @click="reconnect">
            <el-icon v-if="!connecting"><Refresh /></el-icon><span>重新连接</span>
          </el-button>
          <el-button size="small" class="ft-btn" @click="disconnectFromTerminal">断开</el-button>
        </template>
        <template v-else>
          <el-button size="small" class="ft-btn" @click="emit('back')">
            <el-icon><ArrowLeft /></el-icon><span>返回选择</span>
          </el-button>
        </template>
      </div>
      <div class="term-footer-right">
        <template v-if="connected">
          <span class="term-shortcut" title="xterm 内置快捷键"><el-icon><InfoFilled /></el-icon>复制 Ctrl+Shift+C ｜ 粘贴 Ctrl+Shift+V / Ctrl+V</span>
          <el-button size="small" class="ft-btn" title="粘贴剪贴板内容到终端（需浏览器授权剪贴板）" @click="pasteFromClipboard">
            <el-icon><CopyDocument /></el-icon><span>粘贴</span>
          </el-button>
          <el-button size="small" class="ft-btn" title="缩小字号（11 ~ 24）" @click="changeTermFont(-1)">A-</el-button>
          <el-button size="small" class="ft-btn" title="放大字号（11 ~ 24）" @click="changeTermFont(1)">A+</el-button>
          <el-button size="small" class="ft-btn" :title="isTermFull ? '退出全屏' : '终端全屏'" @click="toggleTermFullscreen">
            <el-icon><FullScreen /></el-icon><span>{{ isTermFull ? '退出全屏' : '全屏' }}</span>
          </el-button>
        </template>
      </div>
    </div>
  </div>
</template>

<script setup>
// SSH / 串口共用终端视图（自 ConsolePage 拆出，2026-10 前端收敛批次）。
// WS 生命周期 / 探测 / 剪贴板 / 字号 / 全屏 / resize 校准等协议层逻辑原样保留，
// 仅按 mode prop 分流未连接面板与输入帧格式（SSH 走 JSON 帧、串口发原始字节）。
// 壳（ConsolePage）负责视图切换与「智能默认串口探测」的决策，探测结果经 probe-failed 上抛。
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { ElMessage } from 'element-plus'
// 图标一律用组件（禁止 emoji 当图标）。main.js 已全量全局注册，这里仍显式 import：
// 一是模板里能看出图标来源，二是将来改按需引入不用回头翻模板。
import {
  ArrowLeft,
  CaretRight,
  Clock,
  Connection,
  CopyDocument,
  FullScreen,
  InfoFilled,
  Monitor,
  Platform,
  Refresh,
  User,
  WarningFilled,
} from '@element-plus/icons-vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { TOKEN_KEY } from '../../../api'
import consoleBg from '../../../assets/console-bg.webp'
import { buildTermTheme, VM_TERM_SURFACE } from '../../../utils/term-theme'

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

// 星星背景坐标：一次性生成的模块级常量。此前在模板里内联 Math.random()，
// 时钟 ref 每秒更新触发重渲染 → 40 颗星每秒重新随机、肉眼可见地“瞬移”；
// 改为常量数组后 v-for 只读渲染，位置/节奏在页面生命周期内恒定。
const STARS = Array.from({ length: 40 }, () => ({
  left: +(Math.random() * 100).toFixed(2),
  top: +(Math.random() * 100).toFixed(2),
  delay: +(Math.random() * 6).toFixed(2),
  duration: +(2 + Math.random() * 4).toFixed(2),
}))

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

function openWs(path) {
  const token = localStorage.getItem(TOKEN_KEY) || ''
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return new WebSocket(`${proto}//${location.host}/api/vms/${props.vmId}/${path}?token=${encodeURIComponent(token)}`)
}

// 建连超时兜底：代理/网络黑洞导致 WS open 挂起时，避免“连接中…”无限转圈
function openWsWithTimeout(path, ms = 10000) {
  return new Promise((resolve, reject) => {
    const socket = openWs(path)
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
.term-error .el-icon,
.form-title .el-icon {
  vertical-align: -0.15em;
  margin-right: 4px;
}

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

.stars-bg { position: absolute; inset: 0; pointer-events: none; overflow: hidden; z-index: 0; }
.star {
  position: absolute;
  width: 2px;
  height: 2px;
  background: #fff; /* 星点恒白（装饰，与主题无关） */
  border-radius: 50%;
  opacity: 0;
  animation: shootingStar linear infinite;
  box-shadow: 0 0 4px 1px var(--term-glow);
}
@keyframes shootingStar {
  0% { opacity: 0; transform: translateX(0) translateY(0); }
  5% { opacity: 1; }
  20% { opacity: 0; transform: translateX(-120px) translateY(80px); }
  100% { opacity: 0; transform: translateX(-120px) translateY(80px); }
}

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

/* ---------- SSH 连接表单 ---------- */
.ssh-form-wrap {
  position: relative;
  z-index: 2;
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  min-height: 0;
}
.ssh-form {
  width: 440px;
  max-width: 100%;
  padding: 26px 30px;
  border-radius: var(--radius-lg);
  background: var(--term-card-bg);
  border: 1px solid rgba(88, 166, 255, 0.2);
  box-shadow: var(--elev-pop);
}
.form-title { margin: 0 0 18px; color: var(--term-text); font-size: 1.1rem; }
.ssh-form :deep(.el-form-item__label) { color: var(--term-text-sub); }
.ssh-form :deep(.el-input__wrapper),
.ssh-form :deep(.el-input-number .el-input__wrapper) {
  background: rgba(255, 255, 255, 0.06);
  box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.15) inset;
}
.ssh-form :deep(.el-input__inner) { color: var(--term-text); }
.ssh-form :deep(.el-input-number__decrease),
.ssh-form :deep(.el-input-number__increase) {
  color: var(--term-text-sub);
  background: rgba(255, 255, 255, 0.08);
  border-color: rgba(255, 255, 255, 0.12) !important;
  box-shadow: none !important;
}
.form-btn { width: 100%; margin-top: 4px; }

/* ---------- 串口连接面板 ---------- */
.serial-panel {
  position: relative;
  z-index: 2;
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  text-align: center;
  padding: 24px;
}
.serial-big-icon {
  width: 84px;
  height: 84px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-serial-gold);
  border: 2px solid rgba(240, 185, 11, 0.5);
  border-radius: 50%;
  background: rgba(8, 14, 24, 0.6);
}
/* 圆盘内的大图标：svg 不吃 text-shadow，原来的金色发光改用 drop-shadow 保留 */
.serial-big-icon .el-icon {
  font-size: 2.8rem;
  filter: drop-shadow(0 0 12px rgba(240, 185, 11, 0.55));
}
.serial-title { color: var(--term-text); font-size: 1.25rem; font-weight: 600; }
.serial-desc { max-width: 460px; color: var(--term-text-sub); font-size: 0.88rem; line-height: 1.6; }
.serial-btn { margin-top: 8px; }
/* 客户机侧排障提示：串口连上但无输出/无回显多半是 guest 没起 getty（见诊断结论），一句话提示即可 */
.serial-hint {
  display: flex;
  align-items: flex-start;
  gap: 4px;
  max-width: 500px;
  margin-top: 10px;
  color: var(--term-text-dim);
  font-size: 0.78rem;
  line-height: 1.7;
  text-align: left;
}
.serial-hint .el-icon { margin-top: 0.3em; flex: none; }
.serial-hint code {
  padding: 0 4px;
  border-radius: var(--radius-sm);
  background: rgba(240, 185, 11, 0.12);
  color: var(--color-serial-gold);
  font-family: 'SF Mono', 'Cascadia Code', Consolas, monospace;
  font-size: 0.75rem;
}

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


/* ---------- 底部操作栏 ---------- */
.term-footer {
  position: relative;
  z-index: 3;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  background: var(--term-overlay);
  backdrop-filter: blur(4px);
  border-top: 1px solid var(--term-line);
  font-size: 0.85rem;
  color: var(--term-text-sub);
}
.term-footer-left { display: flex; gap: 8px; }
.term-footer-right { display: flex; align-items: center; gap: 14px; }
.ft-btn {
  color: var(--term-accent-bright);
  border-color: rgba(88, 166, 255, 0.3);
}
.ft-btn:hover {
  color: var(--term-accent-bright);
  border-color: rgba(88, 166, 255, 0.5);
  background: rgba(88, 166, 255, 0.08);
}

/* ---------- 窄屏适配（≤640px）：header 三段换行、footer 换行保操作区 ---------- */
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
  .term-footer {
    flex-wrap: wrap;
    row-gap: 6px;
    padding: 8px 12px;
  }
  .term-footer-right { flex-wrap: wrap; row-gap: 4px; }
  .ssh-form { padding: 20px 18px; }
}
</style>
