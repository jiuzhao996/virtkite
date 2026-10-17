<template>
  <!-- 安装任务日志抽屉：首屏 GET 全量 + WS 实时增量（复用任务中心的结构化日志端点）。
       打开、进度/状态更新由父级通过 defineExpose 调用；流生命周期完全自持。 -->
  <el-drawer v-model="visible" :title="title" size="55%" @closed="closeStream">
    <div class="log-head">
      <el-tag :type="statusTag" effect="light">{{ statusText }}</el-tag>
      <span v-if="live" class="log-live">● 实时</span>
      <el-progress v-if="running" :percentage="progress" :stroke-width="8" class="log-progress" />
    </div>
    <div ref="viewRef" class="log-view mono">
      <div v-if="!entries.length" class="log-empty">（等待日志输出…）</div>
      <div v-for="e in entries" :key="e.seq" class="log-line" :class="'log-' + e.kind">
        <span class="log-ts">{{ fmtLogTs(e.ts) }}</span>
        <span class="log-text">{{ e.text }}</span>
      </div>
    </div>
    <template #footer>
      <el-button @click="visible = false">关闭</el-button>
    </template>
  </el-drawer>
</template>

<script setup>
// 应用安装日志抽屉：任务结构化日志的「首屏全量 + WS 增量」展示。
// 数据流：AppStore.submitInstall 提交任务后调用 open(taskId, appName)，随后轮询把
// progress/status 推给 setProgress/setStatus；本组件只负责日志拉取、去重、贴底与 WS 重连。
import { ref, computed, nextTick } from 'vue'
import { api } from '../../../api'

const visible = ref(false)
const appName = ref('')
const entries = ref([])
const live = ref(false)
const status = ref('running') // running | success | failed
const progress = ref(0)
const viewRef = ref(null)
let taskId = 0
let lastSeq = 0
let ws = null
let retryTimer = null
let retry = 0
let manualClose = false

const title = computed(() => `安装日志${appName.value ? ' · ' + appName.value : ''}`)
const running = computed(() => status.value === 'running')
const statusText = computed(() => ({ running: '执行中', success: '安装完成', failed: '安装失败' }[status.value] || '执行中'))
const statusTag = computed(() => ({ running: 'warning', success: 'success', failed: 'danger' }[status.value] || 'info'))

/** 父级入口：打开抽屉并从头拉取指定任务的日志。 */
function open(id, name) {
  appName.value = name || ''
  entries.value = []
  live.value = false
  status.value = 'running'
  progress.value = 0
  visible.value = true
  openStream(id)
}

/** 父级轮询回调：更新进度条（0-100）。 */
function setProgress(v) {
  progress.value = v
}

/** 父级轮询终态：running | success | failed。 */
function setStatus(s) {
  status.value = s
}

function fmtLogTs(ts) {
  const d = ts ? new Date(ts) : new Date()
  if (isNaN(d.getTime())) return ''
  const p = (n) => String(n).padStart(2, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

/** 按 seq 去重追加：GET 首屏全量与 WS 增量可能重叠，seq 保证幂等。 */
function appendEntries(list) {
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
  if (added) scrollToBottom()
}

function scrollToBottom() {
  nextTick(() => {
    const el = viewRef.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

function closeStream() {
  manualClose = true
  if (retryTimer) {
    clearTimeout(retryTimer)
    retryTimer = null
  }
  if (ws) {
    try {
      ws.close()
    } catch (e) {
      /* 已关闭 */
    }
    ws = null
  }
  live.value = false
}

/** 打开任务日志：首屏 GET 全量 → 记下最大 seq → 非终态则连 WS 追平增量。 */
async function openStream(id) {
  closeStream()
  manualClose = false
  taskId = id
  lastSeq = 0
  retry = 0
  try {
    const res = await api.getTaskLogs(id)
    if (taskId !== id) return // 请求期间已切换任务
    const data = (res && res.data) || {}
    if (data.structured) appendEntries(data.entries)
  } catch (e) {
    // 日志端点不可用：静默降级（不打扰用户），仍可由轮询给出最终结果
    return
  }
  connectWS(id)
}

/** 连接日志增量 WS（票据一次性，重连会重新取票）。 */
async function connectWS(id) {
  let ticket = ''
  try {
    const res = await api.mintTaskLogTicket(id)
    ticket = (res.data && res.data.ticket) || ''
  } catch (e) {
    return // 取票失败：降级为「仅首屏全量」，不空转重连
  }
  if (manualClose || taskId !== id) return
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  const url = `${proto}//${location.host}/api/tasks/${id}/logs/ws?since=${lastSeq}&ticket=${encodeURIComponent(ticket)}`
  ws = new WebSocket(url)
  const openTimer = setTimeout(() => {
    try {
      ws.close()
    } catch (e) {
      /* 已关闭 */
    }
  }, 10000)
  ws.onopen = () => clearTimeout(openTimer)
  ws.onmessage = (ev) => {
    let msg
    try {
      msg = JSON.parse(ev.data)
    } catch (e) {
      return
    }
    if (msg.type === 'ready') {
      clearTimeout(openTimer)
      live.value = true
    } else if (msg.type === 'entries') {
      appendEntries(msg.entries)
    } else if (msg.type === 'done') {
      live.value = false
      closeStream()
    } else if (msg.type === 'error') {
      closeStream()
    }
  }
  ws.onclose = () => {
    clearTimeout(openTimer)
    live.value = false
    if (manualClose || taskId !== id) return
    if (retry >= 3) return
    retry++
    retryTimer = setTimeout(() => connectWS(id), 1000 * 2 ** (retry - 1))
  }
  ws.onerror = () => clearTimeout(openTimer)
}

defineExpose({ open, setProgress, setStatus })
</script>

<style scoped>
.log-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
}
.log-live {
  font-size: 12px;
  color: var(--color-success, #67c23a);
}
.log-progress {
  flex: 1;
}
.log-view {
  max-height: calc(100vh - 220px);
  overflow: auto;
  padding: 8px 10px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: var(--radius-sm, 6px);
  background: var(--el-fill-color-light);
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
}
.log-empty {
  color: var(--color-muted-foreground, #909399);
}
.log-line {
  display: flex;
  gap: 8px;
}
.log-ts {
  flex: none;
  color: var(--color-muted-foreground, #909399);
  user-select: none;
}
.log-text {
  flex: 1;
  min-width: 0;
}
.log-stderr .log-text {
  color: var(--el-color-danger);
}
.log-command .log-text {
  color: var(--el-color-primary);
}
.log-info .log-text {
  color: var(--color-success, #67c23a);
}
</style>
