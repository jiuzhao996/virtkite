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
          <el-button size="small" plain :disabled="row.State !== 'running'" @click="$emit('terminal', row)">终端</el-button>
          <el-button size="small" type="danger" plain @click="remove">删除</el-button>
        </div>
      </template>

      <el-tabs v-model="tab">
        <el-tab-pane label="概要" name="summary">
          <el-descriptions :column="2" border size="small">
            <el-descriptions-item label="名称"><span class="mono">{{ row.Names || '—' }}</span></el-descriptions-item>
            <el-descriptions-item label="状态"><el-tag :type="stateTag(row.State)" effect="light" size="small">{{ stateText(row.State) }}</el-tag></el-descriptions-item>
            <el-descriptions-item label="镜像" :span="2">
              <router-link v-if="row.Image" class="mono lk" to="/images">{{ row.Image }}</router-link>
              <span v-else>—</span>
            </el-descriptions-item>
            <el-descriptions-item label="容器 ID" :span="2"><span class="mono">{{ shortId(row.ID) }}</span></el-descriptions-item>
            <el-descriptions-item label="启动命令" :span="2"><span class="mono">{{ vm.summary.command || '—' }}</span></el-descriptions-item>
            <el-descriptions-item label="重启策略">{{ vm.summary.restartPolicy || 'no' }}</el-descriptions-item>
            <el-descriptions-item label="网络模式">{{ vm.summary.networkMode || '—' }}</el-descriptions-item>
            <el-descriptions-item label="创建时间" :span="2">{{ dockerTime(row.CreatedAt || row.Created) }}</el-descriptions-item>
          </el-descriptions>

          <div v-if="vm.ports.length" class="cd-sec">
            <div class="cd-sec-title">端口映射</div>
            <el-table :data="vm.ports" size="small" stripe>
              <el-table-column label="映射" min-width="200"><template #default="{ row: p }"><span class="mono">{{ portText(p) }}</span></template></el-table-column>
              <el-table-column label="" width="50"><template #default="{ row: p }"><CopyButton :text="portCopyText(p)" tip="复制 host:port" /></template></el-table-column>
            </el-table>
          </div>

          <div v-if="vm.mounts.length" class="cd-sec">
            <div class="cd-sec-title">挂载</div>
            <el-table :data="vm.mounts" size="small" stripe>
              <el-table-column label="源" min-width="180"><template #default="{ row: m }"><span class="mono">{{ m.source }}</span></template></el-table-column>
              <el-table-column label="目标" min-width="180"><template #default="{ row: m }"><span class="mono">{{ m.destination }}</span></template></el-table-column>
              <el-table-column label="读写" width="70"><template #default="{ row: m }"><el-tag size="small" :type="m.rw ? 'success' : 'warning'" effect="light">{{ m.mode }}</el-tag></template></el-table-column>
            </el-table>
          </div>
        </el-tab-pane>

        <el-tab-pane label="统计" name="stats" :disabled="row.State !== 'running'">
          <div v-if="row.State !== 'running'" class="cd-placeholder">容器未运行，无实时统计</div>
          <template v-else>
            <div class="cd-tiles">
              <div class="cd-tile"><span class="cd-tile-k">CPU</span><b class="mono">{{ tiles.cpu }}</b></div>
              <div class="cd-tile"><span class="cd-tile-k">内存</span><b class="mono">{{ tiles.mem }}</b></div>
              <div class="cd-tile"><span class="cd-tile-k">网络 I/O</span><b class="mono">{{ tiles.net }}</b></div>
              <div class="cd-tile"><span class="cd-tile-k">块 I/O</span><b class="mono">{{ tiles.block }}</b></div>
              <div class="cd-tile"><span class="cd-tile-k">PIDs</span><b class="mono">{{ tiles.pids }}</b></div>
            </div>
            <div class="cd-charts">
              <div class="cd-chart-box">
                <div class="cd-chart-title">CPU %</div>
                <div :ref="setChartRef('cpu')" class="cd-chart" />
              </div>
              <div class="cd-chart-box">
                <div class="cd-chart-title">内存 %</div>
                <div :ref="setChartRef('mem')" class="cd-chart" />
              </div>
            </div>
            <div class="cd-hint">滚动窗口约 3 分钟（每 3 秒采样一次）</div>
          </template>
        </el-tab-pane>

        <el-tab-pane label="日志" name="logs" lazy class="pane-logs">
          <div v-loading="logsLoading">
            <div v-if="logsMode === 'ws' || wsFallback" class="ls-bar">
              <span class="ls-dot" :class="dotClass">●</span>
              <span>{{ wsStatusText }}</span>
            </div>
            <LogViewer
              ref="logViewerRef"
              :text="logsMode === 'http' ? logsText : ''"
              :rows="logsMode === 'ws' ? streamLines : null"
              :loading="logsLoading"
              :follow="logsFollow"
              :timestamps="logsTimestamps"
              :filename="displayName"
              @update:follow="(v) => (logsFollow = v)"
              @update:timestamps="onTsChange"
              @refresh="reloadLogs"
            />
          </div>
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
</template>

<script setup>
// 容器详情旗舰抽屉（R3）：把行内「详情」从 JSON 查看器升级为容器域枢纽页。
// 概要/统计/日志/原始 JSON 四 tab + 头部快捷操作组；统计复用单容器 stats 端点（此前前端从未调用），
// 60 点 × 3s 滚动窗口；关闭抽屉或切离统计 tab 即停轮询。
import { ref, reactive, computed, watch, nextTick } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import CopyButton from '../../../components/CopyButton.vue'
import LogViewer from './LogViewer.vue'
import { api } from '../../../api'
import { errMsg, isCancel } from '../../../utils/format'
import { containerName, stateTag, stateText, dockerTime, shortId } from '../../../utils/docker-format'
import { parseInspect, portText, portCopyText } from '../../../utils/docker-inspect'
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
const logViewerRef = ref(null)
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
  const v = logViewerRef.value
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
    if (logsFollow.value && logViewerRef.value) logViewerRef.value.scrollToEndOnce()
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

function open(r) {
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
  visible.value = true
  fetchInspect()
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
.cd-drawer .el-drawer__body {
  display: flex;
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
.cd-sec {
  margin-top: 14px;
}
.cd-sec-title {
  font-weight: 600;
  font-size: 0.9rem;
  margin-bottom: 6px;
}
.cd-tiles {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-bottom: 12px;
}
.cd-tile {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 8px 14px;
  border: 1px solid var(--color-border, #e4e7ed);
  border-radius: var(--radius-md, 8px);
  min-width: 110px;
}
.cd-tile-k {
  font-size: 0.75rem;
  color: var(--color-muted-foreground, #909399);
}
.cd-charts {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}
.cd-chart-box {
  border: 1px solid var(--color-border, #e4e7ed);
  border-radius: var(--radius-md, 8px);
  padding: 8px;
}
.cd-chart-title {
  font-size: 0.82rem;
  color: var(--color-muted-foreground, #909399);
  margin-bottom: 4px;
}
.cd-chart {
  height: 180px;
}
.cd-hint {
  margin-top: 8px;
  font-size: 0.78rem;
  color: var(--color-muted-foreground, #909399);
}
.cd-placeholder {
  padding: 40px 0;
  text-align: center;
  color: var(--color-muted-foreground, #909399);
}
/* 日志流状态条（WS 实时/降级提示） */
.ls-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 6px;
  font-size: 0.82rem;
  color: var(--el-text-color-secondary, #909399);
}
.ls-dot {
  font-size: 0.7rem;
}
.ls-dot.ok { color: #67c23a; }
.ls-dot.wait { color: #e6a23c; }
.ls-dot.off { color: #909399; }
.ls-dot.err { color: #f56c6c; }
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
.lk {
  color: var(--el-color-primary);
  text-decoration: none;
}
.lk:hover {
  text-decoration: underline;
}
</style>
