<!--
  容器详情旗舰抽屉（Portainer 式多 tab）
  ────────────────────────────────────────────────────────────────
  职责：抽屉开关/头部快捷操作、inspect 拉取、统计轮询与曲线、日志 WS/HTTP 降级、网络挂接、
        重命名/删除/调整资源；对外暴露 open(row, initialTab)。
  数据流：
    open(row) → GET inspect（概要/原始 JSON 共用）
    统计 tab 激活且运行中 → GET stats 每 3s → 60 点滚动窗 → echarts
    日志 tab 激活 → WS 实时流（useLogStream）；重试用尽 → HTTP 每 2s 轮询
  子组件（./）：
    ContainerSummaryTab  概要 tab（描述项/端口/挂载/网络接入）
    ContainerStatsTab    统计 tab（指标块 + 图表挂载点）
    ContainerLogsTab     日志 tab（LogViewer 薄包装，暴露 scrollToEndOnce）
    ContainerUpdateDialog 调整资源/健康检查弹窗（自持表单）
    ContainerFilesTab / LogViewer 为既有组件
  刻意没拆：头部快捷操作组与 act() —— 与 row 状态、emit('changed')、统计轮询启停强耦合；
  原始 JSON tab 仅 10 行且与 inspectText 共享状态，拆出收益低。
-->
<template>
    <!-- 容器详情旗舰抽屉（Portainer 式多 tab）：概要 / 统计 / 日志 / 原始 JSON；
         头部为行内快捷操作组，终端仍走父级既有 termDrawer（xterm 生命周期不进 tab 体系） -->
    <el-drawer v-model="visible" :title="''" size="65%" class="cd-drawer" :close-on-click-modal="false" @closed="onClosed">
      <template #header>
        <div class="cd-head">
          <span class="cd-name mono" :title="row.Names">{{ displayName }}</span>
          <el-tag :type="stateTag(row.State)" effect="light" size="small">{{ stateText(row.State) }}</el-tag>
          <span class="cd-spacer" />
          <el-button v-if="row.State !== 'running' && row.State !== 'paused'" size="small" type="success" plain :loading="acting === 'start'" @click="act('start')">启动</el-button>
          <el-button v-else size="small" type="warning" plain :loading="acting === 'stop'" @click="act('stop')">停止</el-button>
          <el-button v-if="row.State === 'running'" size="small" plain :loading="acting === 'pause'" @click="act('pause')">暂停</el-button>
          <el-button v-else-if="row.State === 'paused'" size="small" type="success" plain :loading="acting === 'unpause'" @click="act('unpause')">恢复</el-button>
          <el-button size="small" plain :loading="acting === 'restart'" @click="act('restart')">重启</el-button>
          <el-button size="small" plain @click="openRename">重命名</el-button>
          <el-button size="small" plain @click="openUpdate">调整资源</el-button>
          <el-button size="small" plain :disabled="row.State !== 'running'" @click="$emit('terminal', row)">终端</el-button>
          <el-button size="small" type="danger" plain @click="remove">删除</el-button>
        </div>
      </template>

      <el-tabs v-model="tab">
        <el-tab-pane label="概要" name="summary">
          <ContainerSummaryTab
            :row="row"
            :vm="vm"
            :connected-networks="connectedNetworks"
            :connectable-networks="connectableNetworks"
            v-model:connect-target="connectTarget"
            @connect="connectNetwork"
            @disconnect="disconnectNetwork"
          />
        </el-tab-pane>

        <el-tab-pane label="统计" name="stats" :disabled="row.State !== 'running'">
          <ContainerStatsTab :running="row.State === 'running'" :tiles="tiles" :set-chart-ref="setChartRef" />
        </el-tab-pane>

        <el-tab-pane label="文件" name="files" lazy>
          <ContainerFilesTab v-if="row.ID" :container-id="row.ID" :running="row.State === 'running'" />
        </el-tab-pane>

        <el-tab-pane label="日志" name="logs" lazy class="pane-logs">
          <ContainerLogsTab
            ref="logsTabRef"
            :logs-loading="logsLoading"
            :logs-mode="logsMode"
            :ws-fallback="wsFallback"
            :dot-class="dotClass"
            :ws-status-text="wsStatusText"
            :logs-text="logsText"
            :stream-lines="streamLines"
            :logs-follow="logsFollow"
            :logs-timestamps="logsTimestamps"
            :display-name="displayName"
            @update:follow="(v) => (logsFollow = v)"
            @timestamps-change="onTsChange"
            @refresh="reloadLogs"
          />
        </el-tab-pane>

        <el-tab-pane label="原始 JSON" name="raw" lazy>
          <div class="logs-toolbar">
            <el-button size="small" :icon="Refresh" :loading="inspectLoading" @click="fetchInspect">刷新</el-button>
            <CopyButton :text="inspectText" tip="复制详情 JSON" success-msg="详情 JSON 已复制到剪贴板" />
          </div>
          <pre class="logs-pre">{{ inspectText || '（暂无数据）' }}</pre>
        </el-tab-pane>
      </el-tabs>
    </el-drawer>

    <!-- 调整资源 / 健康检查（docker update，运行中亦可；留空项不动原值） -->
    <ContainerUpdateDialog ref="updateDialogRef" :container-id="row.ID" @updated="onUpdateDone" />
</template>

<script setup>
// 容器详情旗舰抽屉（R3）：把行内「详情」从 JSON 查看器升级为容器域枢纽页。
// 概要/统计/日志/原始 JSON 四 tab + 头部快捷操作组；统计复用单容器 stats 端点（此前前端从未调用），
// 60 点 × 3s 滚动窗口；关闭抽屉或切离统计 tab 即停轮询。
import { ref, computed, watch, nextTick } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import CopyButton from '../../../components/CopyButton.vue'
import ContainerFilesTab from './ContainerFilesTab.vue'
import ContainerSummaryTab from './ContainerSummaryTab.vue'
import ContainerStatsTab from './ContainerStatsTab.vue'
import ContainerLogsTab from './ContainerLogsTab.vue'
import ContainerUpdateDialog from './ContainerUpdateDialog.vue'
import { api } from '../../../api'
import { errMsg, isCancel } from '../../../utils/format'
import { containerName, stateTag, stateText } from '../../../utils/docker-format'
import { parseInspect } from '../../../utils/docker-inspect'
import { useLogStream } from '../../../composables/useLogStream'
import { useChart } from '../../../composables/useChart'

const emit = defineEmits(['terminal', 'changed'])

const visible = ref(false)
const row = ref({})
const tab = ref('summary')
const acting = ref('')
const displayName = computed(() => containerName(row.value.Names))

// ── inspect（概要 + 原始 JSON 共用）──
const raw = ref({})
const inspectText = ref('')
const inspectLoading = ref(false)
const vm = computed(() => parseInspect(raw.value))

async function fetchInspect() {
  if (!row.value.ID) return
  inspectLoading.value = true
  try {
    const res = await api.dockerContainerInspect(row.value.ID)
    raw.value = res.data ?? {}
    inspectText.value = JSON.stringify(res.data ?? {}, null, 2)
  } catch (e) {
    ElMessage.error(errMsg(e, '获取容器详情失败'))
  } finally {
    inspectLoading.value = false
  }
}

// ── 统计（单容器 stats，3s 轮询，60 点滚动窗）──
const stats = ref(null)
const cpuSeries = ref([]) // [{t, val}]
const memSeries = ref([])
const MAX_POINTS = 60
const chartHandles = { cpu: useChart(), mem: useChart() }

function setChartRef(key) {
  return (el) => { chartHandles[key].chartRef.value = el }
}

function pct(s) {
  const n = parseFloat(String(s || '').replace('%', ''))
  return isNaN(n) ? null : n
}

async function fetchStats() {
  if (!row.value.ID || row.value.State !== 'running') return
  try {
    const res = await api.dockerContainerStats(row.value.ID)
    const d = res.data || {}
    stats.value = d
    const t = new Date().toLocaleTimeString('zh-CN', { hour12: false })
    pushPoint(cpuSeries, t, pct(d.CPUPerc))
    pushPoint(memSeries, t, memPct(d))
    renderCharts()
  } catch (e) {
    // 统计是增强信息，失败静默（不打扰概要与日志主流程）
  }
}

function pushPoint(series, t, val) {
  if (val === null) return
  series.value = [...series.value, { t, val }].slice(-MAX_POINTS)
}

// MemPerc 缺失时按 MemUsage「used / total」换算（与容器列表页同口径）
const SIZE_UNITS = { B: 1, kB: 1e3, KB: 1e3, KiB: 1024, MB: 1e6, MiB: 1024 ** 2, GB: 1e9, GiB: 1024 ** 3, TB: 1e12, TiB: 1024 ** 4 }
function parseBytes(s) {
  const m = String(s || '').trim().match(/^([\d.]+)\s*(B|kB|KB|KiB|MB|MiB|GB|GiB|TB|TiB)$/)
  return m ? parseFloat(m[1]) * (SIZE_UNITS[m[2]] || 1) : NaN
}
function memPct(d) {
  const direct = pct(d.MemPerc)
  if (direct !== null) return direct
  const parts = String(d.MemUsage || '').split('/')
  if (parts.length !== 2) return null
  const used = parseBytes(parts[0]); const total = parseBytes(parts[1])
  if (!isFinite(used) || !isFinite(total) || total <= 0) return null
  return Math.min(100, (used / total) * 100)
}

const tiles = computed(() => {
  const d = stats.value || {}
  return {
    cpu: d.CPUPerc || '—',
    mem: d.MemUsage || '—',
    net: d.NetIO || '—',
    block: d.BlockIO || '—',
    pids: d.PIDs || '—'
  }
})

const axisColor = '#d0d7de'
const mutedColor = '#8b949e'
const splitColor = '#eaeef2'
function renderCharts() {
  renderLine('cpu', cpuSeries.value, '#409eff')
  renderLine('mem', memSeries.value, '#67c23a')
}
function renderLine(key, points, color) {
  const h = chartHandles[key]
  if (!h.chartRef.value) return
  h.setOption({
    grid: { left: 6, right: 8, top: 8, bottom: 2, containLabel: true },
    tooltip: { trigger: 'axis' },
    xAxis: {
      type: 'category', data: points.map((p) => p.t),
      axisLine: { lineStyle: { color: axisColor } }, axisLabel: { color: mutedColor }, axisTick: { show: false }
    },
    yAxis: {
      type: 'value', max: 100, axisLabel: { color: mutedColor, formatter: '{value}%' },
      splitLine: { lineStyle: { color: splitColor } }
    },
    series: [{ type: 'line', data: points.map((p) => p.val), symbol: 'none', lineStyle: { width: 1.5, color }, itemStyle: { color }, areaStyle: { opacity: 0.08 } }]
  })
}

// ── 日志（WS 实时流，失败降级 HTTP；渲染复用 LogViewer）──
const logsText = ref('')
const logsLoading = ref(false)
const logsFollow = ref(true) // 进日志就是要看最新,默认跟随贴底
const logsTimestamps = ref(false)
const logsTail = 200
const logsMode = ref('ws')
const logsTabRef = ref(null)
const { lines: streamLines, status, errorMsg, fallback: wsFallback, start: startStream, stop: stopStream, reset: resetStream } = useLogStream()

const dotClass = computed(() => ({ live: 'ok', connecting: 'wait', closed: 'off', error: 'err' }[status.value] || 'off'))
const wsStatusText = computed(() => {
  if (wsFallback.value) return '实时流不可用，已退回轮询刷新'
  if (status.value === 'live') return '实时流已连接'
  if (status.value === 'connecting') return '实时流连接中…'
  if (status.value === 'error') return errorMsg.value || '实时流错误'
  if (status.value === 'closed') return '日志流已结束（容器停止）'
  return '实时流未连接'
})

// 打开/刷新时兜底贴底:多次幂等滚动,覆盖 WS 首批行晚于 DOM 稳定到达的各种时序
function stickLogsBottom() {
  const v = logsTabRef.value
  if (!v) return
  v.scrollToEndOnce()
  setTimeout(() => v.scrollToEndOnce(), 250)
  setTimeout(() => v.scrollToEndOnce(), 700)
}

async function fetchLogs() {
  if (!row.value.ID) return
  logsLoading.value = true
  try {
    const res = await api.dockerContainerLogs(row.value.ID, logsTail, logsTimestamps.value)
    logsText.value = (res.data || {}).logs || ''
    if (logsFollow.value && logsTabRef.value) logsTabRef.value.scrollToEndOnce()
  } catch (e) {
    ElMessage.error(errMsg(e, '获取日志失败'))
  } finally {
    logsLoading.value = false
  }
}

function reloadLogs() {
  if (logsMode.value === 'ws') startStream(row.value.ID, { tail: logsTail, timestamps: logsTimestamps.value })
  else fetchLogs()
  stickLogsBottom()
}

function onTsChange(v) {
  logsTimestamps.value = v
  reloadLogs()
}

// WS 重试用尽 → 切 HTTP 轮询（本抽屉内 2s 定时，切走 tab 即停）。
// 轮询与跟随解耦：只要在日志页就持续拉新，「跟随」仅是 LogViewer 内部滚动状态。
watch(wsFallback, (on) => {
  if (!on) return
  logsMode.value = 'http'
  fetchLogs()
  if (tab.value === 'logs') startLogTimer()
})

// 日志轮询定时（仅 HTTP 降级模式用；抽屉关闭/切走即停）
let logTimer = null
function startLogTimer() {
  stopLogTimer()
  logTimer = setInterval(() => { if (visible.value && tab.value === 'logs') fetchLogs() }, 2000)
}
function stopLogTimer() {
  if (logTimer) { clearInterval(logTimer); logTimer = null }
}

// ── 统计轮询：仅统计 tab 且容器运行时跑 ──
let statsTimer = null
function startStatsTimer() {
  stopStatsTimer()
  fetchStats()
  statsTimer = setInterval(fetchStats, 3000)
}
function stopStatsTimer() {
  if (statsTimer) { clearInterval(statsTimer); statsTimer = null }
}

// tab 切换：进入统计起轮询；进入日志起实时流（WS 模式）或首拉（HTTP 降级），离开即停
watch(tab, (t) => {
  if (t === 'stats') { startStatsTimer(); nextTick(renderCharts) } else stopStatsTimer()
  if (t === 'logs') {
    if (logsMode.value === 'ws') startStream(row.value.ID, { tail: logsTail, timestamps: logsTimestamps.value })
    else { fetchLogs(); startLogTimer() }
    stickLogsBottom()
  } else {
    stopStream()
    stopLogTimer()
  }
})

// ── 头部快捷操作 ──
async function act(action) {
  const label = { start: '启动', stop: '停止', restart: '重启', pause: '暂停', unpause: '恢复' }[action]
  acting.value = action
  try {
    await api.dockerContainerAction(row.value.ID, action)
    ElMessage.success(`已${label} ${displayName.value}`)
    // 状态变了：刷新行状态与统计可用性
    const list = await api.listContainers()
    const fresh = ((list.data || {}).items || []).find((c) => c.ID === row.value.ID)
    if (fresh) row.value = fresh
    emit('changed')
    if (action === 'stop' || action === 'pause') stopStatsTimer()
    if (action === 'start' || action === 'unpause') { if (tab.value === 'stats') startStatsTimer() }
  } catch (e) {
    ElMessage.error(errMsg(e, `${label}失败`))
  } finally {
    acting.value = ''
  }
}

async function openRename() {
  try {
    const { value } = await ElMessageBox.prompt('输入新的容器名称（字母数字开头，可含 _ . -）', '重命名容器', {
      confirmButtonText: '重命名',
      inputValue: displayName.value,
      inputPattern: /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$/,
      inputErrorMessage: '名称需以字母或数字开头，仅可含字母、数字与 _ . -'
    })
    await api.dockerContainerRename(row.value.ID, value)
    ElMessage.success('已重命名')
    emit('changed')
    visible.value = false
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '重命名失败'))
  }
}

async function remove() {
  const running = row.value.State === 'running'
  try {
    await ElMessageBox.confirm(
      running
        ? `容器 ${displayName.value} 正在运行，将强制删除（force），未持久化的数据会丢失。确定删除？`
        : `确定删除容器 ${displayName.value}？`,
      '删除容器',
      { type: 'warning', confirmButtonText: '删除', confirmButtonClass: 'el-button--danger' }
    )
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  try {
    await api.dockerContainerDelete(row.value.ID, running)
    ElMessage.success(`已删除 ${displayName.value}`)
    emit('changed')
    visible.value = false
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

/* ── 网络挂接（docker network connect / disconnect）── */
const allNetworks = ref([])
const connectTarget = ref('')
const connectedNetworks = computed(() => Object.keys((raw.value.NetworkSettings && raw.value.NetworkSettings.Networks) || {}))
const connectableNetworks = computed(() => allNetworks.value.filter((n) => !connectedNetworks.value.includes(n)))

async function loadNetworks() {
  try {
    const res = await api.dockerNetworks()
    allNetworks.value = ((res.data && res.data.items) || []).map((n) => n.Name)
  } catch (e) {
    allNetworks.value = []
  }
}

async function connectNetwork(name) {
  if (!name) return
  try {
    await api.dockerContainerConnectNetwork(row.value.ID, name)
    ElMessage.success('已接入网络 ' + name)
    connectTarget.value = ''
    await fetchInspect()
    emit('changed')
  } catch (e) {
    ElMessage.error(errMsg(e, '接入网络失败'))
  }
}

async function disconnectNetwork(name) {
  try {
    await api.dockerContainerDisconnectNetwork(row.value.ID, name)
    ElMessage.success('已断开网络 ' + name)
    await fetchInspect()
    emit('changed')
  } catch (e) {
    ElMessage.error(errMsg(e, '断开网络失败'))
  }
}

/* ── 调整资源 / 健康检查（docker update，运行中亦可）── */
const updateDialogRef = ref(null)

function openUpdate() {
  updateDialogRef.value?.open()
}

// 更新成功：先刷新 inspect，再通知父列表（保持原来的刷新时序）
async function onUpdateDone() {
  await fetchInspect()
  emit('changed')
}

function open(r, initialTab) {
  row.value = r
  tab.value = 'summary'
  raw.value = {}
  inspectText.value = ''
  logsText.value = ''
  logsMode.value = 'ws'
  logsFollow.value = true
  logsTimestamps.value = false
  cpuSeries.value = []
  memSeries.value = []
  stats.value = null
  resetStream()
  loadNetworks()
  visible.value = true
  fetchInspect()
  // 跳转直达场景（栈详情→容器日志）：先归位 summary 再切目标 tab，
  // 两次赋值同 tick 合并、watcher 以最终值触发一次，避免残留态
  if (initialTab) tab.value = initialTab
}

function onClosed() {
  stopStatsTimer()
  stopLogTimer()
  stopStream()
}

defineExpose({ open })
</script>

<style>
/* 详情抽屉布局:外层 body 锁死不滚(双滚动容器会让「贴底」滚错对象),
   tabs 拉伸填满,日志 pane 内 .lv-pre 成为唯一滚动容器(flex 吃剩余高度)。 */
.cd-drawer .el-drawer__body {  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.cd-drawer .el-tabs {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.cd-drawer .el-tabs__content {
  flex: 1;
  min-height: 0;
}
.cd-drawer .el-tab-pane {
  height: 100%;
  overflow: auto;
}
.cd-drawer .el-tab-pane.pane-logs {
  overflow: hidden;
  display: flex;
  flex-direction: column;
}
.cd-drawer .pane-logs > div {
  /* v-loading 包装层也必须进拉伸链,否则链条在此断裂:pre 按内容撑高、底部被 pane 裁掉 */
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.cd-drawer .pane-logs .lv {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.cd-drawer .pane-logs .lv-pre {
  flex: 1;
  min-height: 0;
  max-height: none;
}
</style>

<style scoped>
.cd-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.cd-name {
  font-weight: 600;
  font-size: 1rem;
}
.cd-spacer {
  flex: 1;
}
.logs-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.logs-pre {
  margin: 0;
  padding: 12px;
  min-height: 300px;
  max-height: calc(100vh - 280px);
  overflow: auto;
  background: #0d1b2a;
  color: #cfe8ff;
  border-radius: var(--radius-md, 8px);
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
  font-size: 0.8rem;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
  user-select: text;
}
</style>
