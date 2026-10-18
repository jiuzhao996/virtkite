// useTaskLogStream：任务结构化日志流（GET 首屏全量 + WS 增量追平）。
//
// 从 views/TaskList.vue 抽出。原实现把 WS 连接、一次性票据、退避重连、
// seq 去重全混在 1101 行的页面里；这套逻辑独立且多处复用：
// 任务中心详情抽屉、应用安装日志抽屉（apps/components/AppInstallLogDrawer.vue
// 里有一份近似实现），收敛到一处避免两边行为漂移。
//
// 用法：
//   const { entries, structured, live, debug, open, close } = useTaskLogStream()
//   open(task)   // 打开某任务日志：首屏 GET → 活动任务连 WS 追平
//   close()      // 手动关闭（抽屉关闭/切换任务时）
//
// 语义约定：
//   - GET /api/tasks/:id/logs 首屏全量；activity 判定由调用方给的 task.status 决定
//   - WS 需一次性票据：POST /api/tasks/:id/ws-ticket，30s 有效、单次消费
//   - since=<最大 seq>：服务端只发 seq > since 的条目；前端按 seq Set 去重（首屏与增量重叠时幂等）
//   - 退避重连：1s/2s/4s 最多 3 次，每次重新取票；手动 close 后不再重连
//   - 过期响应保护：请求期间任务已切换，响应直接丢弃（不写进当前抽屉）
//   - pending 任务首屏可能 structured=false；WS 首批 entries 到达时自动切到结构化面板
//   - onAppend 回调：每次追加条目后触发（调用方用来滚到底；不传则不做额外动作）
import { ref } from 'vue'
import { api } from '../api'

const MAX_RETRY = 3
// WS 10s 未 open 视为异常（后端不可达时 onerror 不一定触发，靠定时器兜底）
const OPEN_TIMEOUT = 10000

export function useTaskLogStream(onAppend) {
  const entries = ref([])
  const structured = ref(false)
  const live = ref(false)
  const debug = ref(false)

  let ws = null
  let retryTimer = null
  let retry = 0
  let manualClose = false
  let lastSeq = 0
  let currentId = null

  /** 按 seq 去重追加（GET 全量与 WS 增量可能重叠，seq 保证幂等） */
  function append(list) {
    if (!Array.isArray(list) || !list.length) return
    const have = new Set(entries.value.map((e) => e.seq))
    let added = false
    for (const e of list) {
      if (!e || have.has(e.seq)) continue
      have.add(e.seq)
      entries.value.push(e)
      if (e.seq > lastSeq) lastSeq = e.seq
      added = true
    }
    if (added && onAppend) onAppend()
  }

  function close() {
    manualClose = true
    if (retryTimer) {
      clearTimeout(retryTimer)
      retryTimer = null
    }
    if (ws) {
      try {
        ws.close()
      } catch (_e) {
        /* 已关闭 */
      }
      ws = null
    }
    live.value = false
  }

  async function connect(id) {
    // 手动关闭或已切换任务：不再重连
    if (manualClose || currentId !== id) return
    let ticket = ''
    try {
      const res = await api.mintTaskLogTicket(id)
      ticket = (res.data && res.data.ticket) || ''
    } catch (_e) {
      // 取票失败（权限/网络）：直接放弃，不给用户反复报错
      return
    }
    // 取票期间抽屉可能已关闭
    if (manualClose || currentId !== id) return

    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const url = `${proto}//${location.host}/api/tasks/${id}/logs/ws?since=${lastSeq}&ticket=${encodeURIComponent(ticket)}`
    let openTimer = setTimeout(() => {
      try {
        ws && ws.close()
      } catch (_e) {
        /* 已关闭 */
      }
    }, OPEN_TIMEOUT)

    ws = new WebSocket(url)
    ws.onopen = () => clearTimeout(openTimer)
    ws.onmessage = (ev) => {
      let msg
      try {
        msg = JSON.parse(ev.data)
      } catch (_e) {
        return
      }
      if (msg.type === 'ready') {
        clearTimeout(openTimer)
        live.value = true
      } else if (msg.type === 'entries') {
        // pending 任务首屏没有结构化日志；首批到达时切到结构化面板
        if (Array.isArray(msg.entries) && msg.entries.length) structured.value = true
        append(msg.entries)
      } else if (msg.type === 'done' || msg.type === 'error') {
        live.value = false
        close()
      }
    }
    ws.onclose = () => {
      clearTimeout(openTimer)
      live.value = false
      if (manualClose || currentId !== id) return
      if (retry >= MAX_RETRY) return
      retry++
      retryTimer = setTimeout(() => connect(id), 1000 * 2 ** (retry - 1))
    }
    ws.onerror = () => clearTimeout(openTimer)
  }

  /**
   * 打开某任务日志。
   * @param {{id:number,status?:string}} task status 缺省视为活动任务（一定连 WS）；
   *        传 'running'/'pending' 之外的终态则只拉首屏、不连 WS。
   * @param {{alwaysConnect?:boolean}} [opts] 传 {alwaysConnect:true} 时无视终态一律连 WS
   *        （安装日志等「轮询已结束但日志可能仍在写」的场景）
   */
  async function open(task, opts = {}) {
    close()
    manualClose = false
    entries.value = []
    structured.value = false
    debug.value = false
    lastSeq = 0
    retry = 0
    currentId = task.id

    try {
      const res = await api.getTaskLogs(task.id)
      if (currentId !== task.id) return // 请求期间已切换任务
      const data = (res && res.data) || {}
      structured.value = !!data.structured
      if (structured.value) append(data.entries)
    } catch (_e) {
      // 日志端点不可用（如后端未上线）：静默回退，不打扰用户
      structured.value = false
      if (!opts.alwaysConnect) return
    }
    const active =
      opts.alwaysConnect || task.status === 'running' || task.status === 'pending' || task.status === undefined
    if (!active) return // 终态任务：首屏全量已足够
    connect(task.id)
  }

  return { entries, structured, live, debug, open, close }
}
