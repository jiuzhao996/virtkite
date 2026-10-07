// useLogStream：容器日志 WS 实时流（R4，对标 Dozzle 观感）。
// 连接 /api/docker/containers/:id/logs/ws，按行接收 {stream, data}；
// 连接失败或首帧未就绪则退避重连 3 次，仍失败置 fallback=true 由调用方降级为 HTTP 轮询
// （保底永不比改造前差）。服务端做行重组，前端只管累积与渲染。
import { ref, onUnmounted } from 'vue'
import { TOKEN_KEY } from '../api'

// 环形缓冲：长会话只保留末尾 N 行，防内存与 DOM 膨胀
const MAX_LINES = 5000

export function useLogStream() {
  // 日志行：[{stream: 'stdout'|'stderr', data: '…'}]
  const lines = ref([])
  // idle | connecting | live | closed | error
  const status = ref('idle')
  const errorMsg = ref('')
  // true = WS 不可用，调用方应切回 HTTP 轮询
  const fallback = ref(false)

  let ws = null
  let retries = 0
  let retryTimer = null
  let pingTimer = null
  let manualClose = false

  function url(id, tail, timestamps) {
    const token = localStorage.getItem(TOKEN_KEY) || ''
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const q = `tail=${tail || 500}${timestamps ? '&timestamps=1' : ''}`
    return `${proto}//${location.host}/api/docker/containers/${encodeURIComponent(id)}/logs/ws?${q}&token=${encodeURIComponent(token)}`
  }

  function push(stream, data) {
    const arr = lines.value
    arr.push({ stream, data })
    if (arr.length > MAX_LINES) lines.value = arr.slice(arr.length - MAX_LINES)
  }

  function scheduleRetry(connect) {
    if (retries >= 3) {
      fallback.value = true
      status.value = 'error'
      errorMsg.value = '实时日志流不可用'
      return
    }
    retries++
    const delay = 1000 * 2 ** (retries - 1) // 1s / 2s / 4s
    retryTimer = setTimeout(connect, delay)
  }

  function start(id, opts) {
    const tail = (opts && opts.tail) || 500
    const timestamps = !!(opts && opts.timestamps)
    stop()
    manualClose = false
    lines.value = [] // 新会话不残留上一次的行（换容器/重连时旧内容必须清掉）
    retries = 0
    fallback.value = false
    errorMsg.value = ''
    status.value = 'connecting'

    const connect = () => {
      ws = new WebSocket(url(id, tail, timestamps))
      // 建连超时兜底（对齐 ContainerTerminal：防代理黑洞下无限「连接中…」）
      const openTimer = setTimeout(() => {
        try { ws.close() } catch (e) { /* 已关闭 */ }
      }, 10000)

      ws.onopen = () => { clearTimeout(openTimer) }
      ws.onmessage = (ev) => {
        let msg
        try {
          msg = JSON.parse(ev.data)
        } catch (e) {
          return
        }
        if (msg.type === 'ready') {
          clearTimeout(openTimer)
          status.value = 'live'
          pingTimer = setInterval(() => {
            if (ws && ws.readyState === 1) ws.send(JSON.stringify({ type: 'ping' }))
          }, 30000)
        } else if (msg.type === 'log') {
          push(msg.stream === 'stderr' ? 'stderr' : 'stdout', String(msg.data || ''))
        } else if (msg.type === 'eof') {
          status.value = 'closed'
        } else if (msg.type === 'error') {
          status.value = 'error'
          errorMsg.value = msg.msg || '日志流错误'
          try { ws.close() } catch (e) { /* 已关闭 */ }
        }
      }
      ws.onclose = () => {
        clearTimeout(openTimer)
        if (pingTimer) { clearInterval(pingTimer); pingTimer = null }
        if (manualClose) return
        // 未就绪即关闭 = 流不可用，退避重连；重试用尽降级 HTTP
        if (status.value !== 'live') {
          status.value = 'connecting'
          scheduleRetry(connect)
        } else {
          status.value = 'closed'
        }
      }
      ws.onerror = () => { clearTimeout(openTimer) }
    }
    connect()
  }

  function stop() {
    manualClose = true
    if (retryTimer) { clearTimeout(retryTimer); retryTimer = null }
    if (pingTimer) { clearInterval(pingTimer); pingTimer = null }
    if (ws) {
      try { ws.close() } catch (e) { /* 已关闭 */ }
      ws = null
    }
    status.value = 'idle'
  }

  function reset() {
    stop()
    lines.value = []
    fallback.value = false
    errorMsg.value = ''
  }

  onUnmounted(stop)

  return { lines, status, errorMsg, fallback, start, stop, reset }
}
