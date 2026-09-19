<template>
  <div class="cterm">
    <!-- 状态条：连接状态 + 重连入口（●○ 为状态圆点，纯装饰指示符，非 emoji） -->
    <div class="cterm-bar">
      <span class="cterm-dot" :class="dotClass">●</span>
      <span class="cterm-status-text">{{ statusText }}</span>
      <span class="cterm-name mono">{{ containerId }}</span>
      <el-button class="cterm-reconnect" text size="small" @click="reconnect">重连</el-button>
    </div>
    <!-- xterm 挂载点：点击聚焦，尺寸变化经 ResizeObserver 自动 fit -->
    <div ref="termEl" class="cterm-body" />
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { TOKEN_KEY } from '../api'

// 容器终端（xterm.js + WebSocket ↔ docker exec TTY 桥）。
// 协议（handler/container_terminal.go）：
//   客户端 → 服务端：首帧必须是 {"type":"resize","cols":N,"rows":N} 控制帧声明尺寸；
//                    之后裸文本帧 = 键盘输入字节流（后端原样写入容器 stdin）；
//                    {"type":"ping"} 心跳。
//   服务端 → 客户端：二进制帧 = 容器 TTY 输出（xterm 直接 write）；
//                    JSON 帧 type=connected/pong/resized/error（error 为致命错误，随后服务端关闭）。
const props = defineProps({
  containerId: { type: String, required: true }
})

const termEl = ref(null)
// connecting | connected | closed | error
const status = ref('connecting')
const errorMsg = ref('')

let term = null
let fitAddon = null
let ws = null
let resizeObserver = null
let pingTimer = null
let fitTimer = null
// 主动关闭（组件卸载 / 重连清理）时不把「连接已断开」当异常展示
let manualClose = false

const dotClass = computed(
  () => ({ connected: 'ok', connecting: 'wait', closed: 'off', error: 'err' }[status.value] || 'off')
)
const statusText = computed(() => {
  if (status.value === 'connected') return '已连接'
  if (status.value === 'connecting') return '连接中…'
  if (status.value === 'error') return errorMsg.value || '连接错误'
  return '连接已断开'
})

function openSocket() {
  const token = localStorage.getItem(TOKEN_KEY) || ''
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  const ws = new WebSocket(
    `${proto}//${location.host}/api/docker/containers/${encodeURIComponent(props.containerId)}/terminal?token=${encodeURIComponent(token)}`
  )
  // TTY 输出走二进制帧，arraybuffer 便于 xterm 直接 write
  ws.binaryType = 'arraybuffer'
  return ws
}

// 建连超时兜底（对齐 ConsolePage 的 openWsWithTimeout：防代理黑洞下无限「连接中…」）
function openSocketWithTimeout(ms = 10000) {
  return new Promise((resolve, reject) => {
    const socket = openSocket()
    const timer = setTimeout(() => {
      try { socket.close() } catch (e) { /* 已关闭 */ }
      reject(new Error('WebSocket 连接超时'))
    }, ms)
    socket.onopen = () => { clearTimeout(timer); resolve(socket) }
    socket.onerror = () => { clearTimeout(timer); reject(new Error('WebSocket 连接失败')) }
  })
}

async function connect() {
  manualClose = false
  status.value = 'connecting'
  errorMsg.value = ''
  try {
    ws = await openSocketWithTimeout()
  } catch (e) {
    status.value = 'error'
    errorMsg.value = e.message || 'WebSocket 连接失败'
    return
  }
  ws.onmessage = handleMsg
  ws.onclose = () => {
    if (manualClose) return
    if (status.value !== 'error') {
      status.value = 'closed'
      appendNotice('\r\n\x1b[2m[连接已断开，可点击「重连」重新进入]\x1b[0m\r\n')
    }
  }
  ws.onerror = () => { /* close 事件已兜底展示，这里不再重复置错 */ }
  // 协议约定：首帧即 resize 控制帧，声明初始 TTY 尺寸（后端据此 exec resize）
  sendResize()
  startPing()
}

// 首帧/调窗统一走 resize 控制帧；xterm 尺寸经 FitAddon 实测得出
function sendResize() {
  if (!ws || ws.readyState !== WebSocket.OPEN || !term) return
  ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
}

function startPing() {
  stopPing()
  pingTimer = setInterval(() => {
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'ping' }))
  }, 30000)
}
function stopPing() {
  if (pingTimer) { clearInterval(pingTimer); pingTimer = null }
}

function handleMsg(ev) {
  if (!term) return
  if (ev.data instanceof ArrayBuffer) {
    term.write(new Uint8Array(ev.data))
    return
  }
  if (ev.data instanceof Blob) {
    ev.data.arrayBuffer().then((buf) => { if (term) term.write(new Uint8Array(buf)) })
    return
  }
  let msg = null
  try { msg = JSON.parse(ev.data) } catch (e) { /* 非 JSON 视作终端输出 */ }
  if (msg && msg.type === 'connected') {
    // exec 就绪：回发一次实际尺寸（首帧发出时抽屉动画可能尚未结束）
    status.value = 'connected'
    sendResize()
    term.focus()
    return
  }
  if (msg && msg.type === 'error') {
    status.value = 'error'
    errorMsg.value = msg.msg || '连接失败'
    term.write(`\r\n\x1b[31m${msg.msg || '连接失败'}\x1b[0m\r\n`)
    return
  }
  if (msg && (msg.type === 'pong' || msg.type === 'resized')) return
  term.write(ev.data)
}

function appendNotice(text) {
  if (term) term.write(text)
}

function doFit() {
  if (!term || !fitAddon) return
  try { fitAddon.fit() } catch (e) { /* 容器尺寸为 0 时 fit 可能抛错，忽略等下次 */ }
}

// ResizeObserver 触发高频，轻去抖后 fit + 回发尺寸
function scheduleFit() {
  clearTimeout(fitTimer)
  fitTimer = setTimeout(() => {
    doFit()
    sendResize()
  }, 120)
}

function reconnect() {
  if (ws) {
    manualClose = true
    try { ws.close() } catch (e) { /* 已关闭 */ }
    ws = null
  }
  stopPing()
  if (term) term.reset()
  connect()
}

onMounted(async () => {
  await nextTick()
  if (!termEl.value) return
  term = new Terminal({
    cursorBlink: true,
    cursorStyle: 'bar',
    fontSize: 14,
    fontFamily: "'Cascadia Code', 'Fira Code', Consolas, 'Liberation Mono', Menlo, monospace",
    theme: {
      background: '#0d1b2a',
      foreground: '#cfe8ff',
      cursor: '#58a6ff',
      selectionBackground: 'rgba(31, 58, 95, 0.7)',
      black: '#1b2838', red: '#f85149', green: '#3fb950', yellow: '#d2991d',
      blue: '#58a6ff', magenta: '#bc8cff', cyan: '#39c5cf', white: '#b1bac4',
      brightBlack: '#30363d', brightRed: '#ff6e6a', brightGreen: '#56d364',
      brightYellow: '#e3b341', brightBlue: '#79c0ff', brightMagenta: '#d2a8ff',
      brightCyan: '#56d4dd', brightWhite: '#f0f6fc'
    }
  })
  fitAddon = new FitAddon()
  term.loadAddon(fitAddon)
  term.open(termEl.value)
  doFit()

  // 裸文本帧 = 键盘输入（后端把非控制帧原样写入容器 stdin）
  term.onData((data) => {
    if (!ws || ws.readyState !== WebSocket.OPEN) return
    ws.send(data)
  })
  termEl.value.addEventListener('click', () => term && term.focus())

  // 抽屉展开/窗口缩放/侧栏折叠都会改变容器尺寸，统一经 observer 自动 fit
  resizeObserver = new ResizeObserver(() => scheduleFit())
  resizeObserver.observe(termEl.value)

  connect()
})

onUnmounted(() => {
  manualClose = true
  stopPing()
  clearTimeout(fitTimer)
  if (resizeObserver) { resizeObserver.disconnect(); resizeObserver = null }
  if (ws) { try { ws.close() } catch (e) { /* 已关闭 */ } ws = null }
  if (term) { term.dispose(); term = null }
})
</script>

<style scoped>
.cterm {
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: #0d1b2a;
  border-radius: var(--radius-md, 8px);
  overflow: hidden;
}
.cterm-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  background: rgba(255, 255, 255, 0.04);
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
  color: #8aa5c0;
  font-size: 0.8rem;
  flex: none;
}
.cterm-dot {
  font-size: 0.7rem;
}
.cterm-dot.ok { color: #3fb950; }
.cterm-dot.wait { color: #d2991d; }
.cterm-dot.off { color: #6b7f94; }
.cterm-dot.err { color: #f85149; }
.cterm-name {
  margin-left: auto;
  color: #6b8aa8;
  font-size: 0.75rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cterm-reconnect {
  flex: none;
  color: #8ab8e8;
}
.cterm-body {
  flex: 1;
  min-height: 0;
  padding: 4px 8px 8px;
}
.cterm-body :deep(.xterm) {
  height: 100%;
}
</style>
