<template>
  <div>
    <!-- 独立路由使用时显示页头；被仪表盘以 tab 嵌入时隐藏（标题与刷新归属外层），
         刷新按钮移到「实时告警」卡头部，两种形态都可用 -->
    <PageHead v-if="!embedded" title="监控中心">
      <template #subtitle>
        <!-- 历史 p.page-desc（UA 外边距参与布局），经插槽原样保留 -->
        <p class="page-desc">Prometheus 原生看板 + Alertmanager 实时告警（指标 15s 采集）</p>
      </template>
      <el-button :icon="Refresh" :loading="alertsLoading" @click="loadAlerts">刷新告警</el-button>
    </PageHead>

    <!-- 原生看板（Grafana 退役批次 2026-10）：ECharts 直连平台代理的 Prometheus 历史，
         与详情页/仪表盘同一套图表语言，不再有 iframe 高度硬编码与 3MB 前端重载 -->
    <el-card shadow="never" class="alert-card">
      <template #header>
        <div class="alert-head">
          <el-radio-group v-model="board">
            <el-radio-button value="overview">宿主机</el-radio-button>
            <el-radio-button value="vms">虚拟机</el-radio-button>
          </el-radio-group>
          <div class="alert-head-actions">
            <el-select v-model="minutes" style="width: 120px" @change="reloadBoard">
              <el-option label="最近 30 分钟" :value="30" />
              <el-option label="最近 1 小时" :value="60" />
              <el-option label="最近 3 小时" :value="180" />
              <el-option label="最近 6 小时" :value="360" />
            </el-select>
            <el-button :icon="Refresh" :loading="boardLoading" @click="reloadBoard">刷新</el-button>
          </div>
        </div>
      </template>

      <!-- 概览：宿主机 CPU/内存 + 存储池使用率 -->
      <div v-show="board === 'overview'" class="chart-grid">
        <div class="chart-cell">
          <div class="chart-title">宿主机资源（CPU / 内存占用 %）</div>
          <div v-show="hostSource === 'unavailable'" class="chart-empty">
            <el-empty description="Prometheus 历史不可用（监控栈未启动或暂无采样）" :image-size="56" />
          </div>
          <div v-show="hostSource !== 'unavailable'" :ref="hostHandle.chartRef" class="chart-box" />
        </div>
        <div class="chart-cell">
          <div class="chart-title">存储池使用率（%）</div>
          <div v-if="poolSource === 'unavailable' || !poolNames.length" class="chart-empty">
            <el-empty :description="poolSource === 'unavailable' ? 'Prometheus 历史不可用' : '暂无存储池采样数据'" :image-size="56" />
          </div>
          <div v-show="poolSource !== 'unavailable' && poolNames.length" :ref="poolHandle.chartRef" class="chart-box" />
        </div>
      </div>

      <!-- 虚拟机：六指标小图（与原 vmops-vms 看板对齐：CPU/内存/磁盘读写/网络收发） -->
      <div v-show="board === 'vms'">
        <div class="vm-picker">
          <el-select v-model="selectedVM" filterable placeholder="选择虚拟机" style="width: 260px">
            <el-option v-for="name in vmNames" :key="name" :label="name" :value="name" />
          </el-select>
          <!-- 出口：监控看的是 VM，此前看完无法回到该 VM 详情（观测页 → 操作对象断链）。
               只有名字没有 ID，带 keyword 跳列表（VmList 读 query.keyword 预置筛选） -->
          <el-button v-if="selectedVM" size="small" @click="goVmList">查看虚拟机 →</el-button>
          <span v-if="vmSource === 'unavailable'" class="chart-hint">Prometheus 历史不可用（监控栈未启动或暂无采样）</span>
          <span v-else-if="!vmNames.length" class="chart-hint">暂无虚拟机采样数据（关机 VM 不产生指标）</span>
        </div>
        <div v-if="selectedVM && vmSource !== 'unavailable'" class="chart-grid six">
          <div v-for="m in VM_METRICS" :key="m.key" class="chart-cell">
            <div class="chart-title">{{ m.label }}</div>
            <div :ref="(el) => bindMetricRef(m.key, el)" class="chart-box small" />
          </div>
        </div>
      </div>
    </el-card>

    <!-- 扩展面板（panels.js 注册表驱动）：加一张监控面板 = 注册表加一行，零后端改动——
         这是 Grafana 模板位的自研答案（通用 prom-query 端点 + 声明式注册表） -->
    <div class="panel-grid">
      <el-card v-for="p in PANELS" :key="p.id" shadow="never" class="alert-card panel-card">
        <template #header>
          <div class="alert-head">
            <span>{{ p.title }}</span>
          </div>
        </template>
        <div :ref="(el) => bindPanelRef(p.id, el)" class="chart-box panel-box" />
        <div v-if="panelEmpty(p)" class="chart-empty panel-empty-hint">
          <span>{{ p.empty }}</span>
        </div>
      </el-card>
    </div>

    <!-- 告警列表（Alertmanager 实时 firing）→ monitor/components/RealtimeAlertsCard.vue -->
    <RealtimeAlertsCard
      :alerts="alerts"
      :alerts-loading="alertsLoading"
      :alerts-error="alertsError"
      :firing-count="firingCount"
      @refresh="loadAlerts"
    />

    <!-- 告警历史（webhook 入库追溯）→ monitor/components/AlertHistoryCard.vue -->
    <AlertHistoryCard
      v-model:status="historyStatus"
      v-model:page="historyPage"
      :history="history"
      :history-loading="historyLoading"
      :history-total="historyTotal"
      :history-page-size="historyPageSize"
      @refresh="loadHistory"
      @page-change="loadHistory"
      @filter-change="onHistoryFilter"
    />

    <!-- node_exporter 抓取目标预览（file_sd）→ monitor/components/FileSDCard.vue -->
    <FileSDCard
      :sd-targets="sdTargets"
      :sd-enabled="sdEnabled"
      :sd-loading="sdLoading"
      @refresh="loadFileSD"
    />

  </div>
</template>

<script setup>
// ============================================================================
// 监控中心（Monitor）
// ----------------------------------------------------------------------------
// 职责：Prometheus 原生看板（ECharts 直连平台代理的 Prometheus 历史）+ Alertmanager
//       实时告警 + 告警历史（webhook 入库）+ node_exporter file_sd 抓取目标预览。
//       被仪表盘以 tab 嵌入时隐藏页头（embedded），并随 tab 激活态启停轮询（active）。
//
// 数据流：
//   看板 ← api.hostHistory / poolHistory / vmMetricsHistory（按时间跨度 minutes）→
//          renderHostChart / renderPoolChart / renderMetricChart（各 useChart handle）
//   扩展面板 ← api.promQuery（PANELS 注册表声明式驱动，panels.js）
//   告警 ← api.listAlerts（3s 轮询）；历史 ← api.monitorAlertHistory（手动刷新）；file_sd
//          ← api.monitorFileSD（手动刷新）
//
// 已拆出的子组件（web/src/views/monitor/components/）：
//   RealtimeAlertsCard.vue   实时告警卡（refresh）
//   AlertHistoryCard.vue     告警历史卡（refresh / page-change / filter-change + v-model）
//   FileSDCard.vue           node_exporter 抓取目标预览卡（refresh）
//
// 刻意没拆的：原生看板卡与扩展面板网格——它们的 :ref 直连 setup 期建立的 useChart
//   句柄（hostHandle/poolHandle/metricHandles/panelHandles，bindMetricRef/bindPanelRef
//   把模板 ref 转发进去），句柄是模块级多实例闭包，下沉会牵动整套图表生命周期，收益低易错。
//   ⚠️ .alert-card / .alert-head / .head-icon 等样式在三张拆出的卡片里各留一份，改一处须同步。
// ============================================================================
// embedded：被仪表盘 tab 嵌入时隐藏独立页头
// active：嵌入场景随所在 tab 激活态启停轮询（独立路由页保持默认 true 一直轮询）
const props = defineProps({
  embedded: { type: Boolean, default: false },
  active: { type: Boolean, default: true }
})
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Refresh } from '@element-plus/icons-vue'
import { api } from '../api'
import { cssVar, fmtRateBytes } from '../utils/format'
import PageHead from '../components/PageHead.vue'
import { useAutoRefresh } from '../composables/useAutoRefresh'
import { useChart } from '../composables/useChart'
import { PANELS } from './monitor/panels'
import RealtimeAlertsCard from './monitor/components/RealtimeAlertsCard.vue'
import AlertHistoryCard from './monitor/components/AlertHistoryCard.vue'
import FileSDCard from './monitor/components/FileSDCard.vue'

// ── 扩展面板（panels.js 注册表）：通用 prom-query 端点 + 声明式渲染 ──
const panelData = reactive({}) // panelId → [{name, points}]

function panelUnitFormatter(unit) {
  if (unit === 'bytes') return (v) => fmtRateBytes(v)
  return undefined
}

function panelEmpty(p) {
  const rows = panelData[p.id]
  return !rows || rows.every((s) => !s.points || !s.points.length)
}

async function loadPanels() {
  for (const p of PANELS) {
    // 面板内多序列并行，面板间串行（3 张面板各 1-2 序列，量小无所谓；串行便于复用 refs）
    const results = await Promise.allSettled(
      p.series.map((s) => api.promQuery(s.expr, minutes.value))
    )
    const rows = []
    results.forEach((r, i) => {
      if (r.status !== 'fulfilled') return
      const series = (r.value.data && r.value.data.series) || []
      // 单序列面板直接用；多序列按注册顺序对位（prom-query 每次只回该 expr 的序列）
      const points = series.length ? series[0].points : []
      rows.push({ name: p.series[i].name, points })
    })
    panelData[p.id] = rows
    await nextTick()
    renderPanel(p)
  }
}

function renderPanel(p) {
  const holder = panelHandles[p.id]
  if (!holder) return
  const rows = panelData[p.id] || []
  const colors = [primaryColor, successColor, '#d97706']
  const first = rows.find((r) => r.points && r.points.length)
  const opt = baseOption()
  opt.legend.data = rows.map((r) => r.name)
  opt.xAxis.data = first ? first.points.map((pt) => pt.t) : []
  opt.yAxis.axisLabel = { color: chartMutedColor, formatter: panelUnitFormatter(p.unit) }
  if (p.unit === 'percent') opt.yAxis.max = 100
  if (p.unit === 'bool') {
    opt.yAxis.max = 1.2
    opt.yAxis.min = -0.2
    opt.yAxis.interval = 1
  }
  opt.tooltip.valueFormatter = panelUnitFormatter(p.unit)
  opt.series = rows.map((r, i) => ({
    name: r.name,
    type: 'line',
    data: (r.points || []).map((pt) => pt.val),
    symbol: 'none',
    lineStyle: { width: 1.5, color: colors[i % colors.length] },
    itemStyle: { color: colors[i % colors.length] },
    areaStyle: { opacity: 0.08 },
    step: p.unit === 'bool' || p.unit === 'count' ? 'end' : undefined
  }))
  holder.setOption(opt)
}

// ── 原生看板（Grafana 退役批次）：ECharts 直连平台代理的 Prometheus 历史 ──
// echarts 不解析 var()，取令牌真实色值（与 HostResourceCard 同一套取色惯例）
const primaryColor = cssVar('--el-color-primary', '#2a9da5')
const successColor = cssVar('--color-success', '#16a34a')
const chartMutedColor = cssVar('--color-muted-foreground', '#64748b')
const chartAxisColor = cssVar('--color-border', '#e2e8f0')
const chartSplitColor = cssVar('--color-muted', '#eef2f6')

const board = ref('overview')
const minutes = ref(60)
const boardLoading = ref(false)

// 虚拟机六指标面板定义（与后端 vmMetricSeries 字段一一对应）
const VM_METRICS = [
  { key: 'cpu', label: 'CPU 占用（%）', percent: true },
  { key: 'mem', label: '内存占用（%）', percent: true },
  { key: 'disk_read', label: '磁盘读取', rate: true },
  { key: 'disk_write', label: '磁盘写入', rate: true },
  { key: 'net_rx', label: '网络接收', rate: true },
  { key: 'net_tx', label: '网络发送', rate: true },
]

// 看板图表实例全部交 useChart 托管（init 惰性、ResizeObserver 自动 resize、卸载自动
// dispose，S1-2 收敛写法）：键在 setup 期静态可知，每键一个 handle（与 GuestMetricsCard
// 同一套惯例）。顺带修掉旧单例 ResizeObserver 只重排 host/pool/metric、漏掉面板图的缺陷。
const hostHandle = useChart()
const poolHandle = useChart()
const metricHandles = Object.fromEntries(VM_METRICS.map((m) => [m.key, useChart()]))
const panelHandles = Object.fromEntries(PANELS.map((p) => [p.id, useChart()]))

// 模板 :ref 转发到对应 handle 的 chartRef（ensureInit 在首次 setOption 时取）
function bindMetricRef(key, el) {
  metricHandles[key].chartRef.value = el
}
function bindPanelRef(id, el) {
  panelHandles[id].chartRef.value = el
}

const hostSource = ref('prometheus')
const poolSource = ref('prometheus')
const poolNames = ref([])
const vmSource = ref('prometheus')
const vmData = ref({})
const vmNames = computed(() => Object.keys(vmData.value).sort())
const selectedVM = ref('')
const router = useRouter()

// 观测页出口：带 VM 名跳到列表并预置关键词筛选（VmList 读 query.keyword）
function goVmList() {
  router.push({ path: '/vms', query: { keyword: selectedVM.value } })
}

function baseOption() {
  return {
    grid: { left: 8, right: 12, top: 28, bottom: 4, containLabel: true },
    tooltip: { trigger: 'axis' },
    legend: { top: 0, textStyle: { color: chartMutedColor }, itemWidth: 14, itemHeight: 2 },
    xAxis: {
      type: 'category',
      data: [],
      axisLine: { lineStyle: { color: chartAxisColor } },
      axisLabel: { color: chartMutedColor },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: chartMutedColor },
      splitLine: { lineStyle: { color: chartSplitColor } },
    },
  }
}

function lineSeries(name, points, color, { area = true, percent = false, rate = false } = {}) {
  return {
    name,
    type: 'line',
    data: points.map((p) => p.val),
    symbol: 'none',
    lineStyle: { width: 1.5, color },
    itemStyle: { color },
    areaStyle: area ? { opacity: 0.08 } : undefined,
    ...(percent ? { min: 0 } : {}),
    ...(rate
      ? {
          tooltip: { valueFormatter: (v) => fmtRateBytes(v) },
          yAxis: undefined,
        }
      : {}),
  }
}

function renderHostChart(points) {
  const opt = baseOption()
  opt.xAxis.data = points.map((p) => p.t)
  opt.series = [
    lineSeries('CPU %', points.map((p) => ({ t: p.t, val: p.cpu })), primaryColor),
    lineSeries('内存 %', points.map((p) => ({ t: p.t, val: p.mem })), successColor),
  ]
  hostHandle.setOption(opt)
}

function renderPoolChart(pools) {
  const names = Object.keys(pools).sort()
  const first = pools[names[0]] || []
  const opt = baseOption()
  opt.legend.data = names
  opt.xAxis.data = first.map((p) => p.t)
  opt.yAxis.max = 100
  opt.series = names.map((n) => lineSeries(n, pools[n]))
  poolHandle.setOption(opt)
}

function renderMetricChart(key, def, points) {
  const holder = metricHandles[key]
  if (!holder) return
  const opt = baseOption()
  opt.legend.show = false
  opt.tooltip.formatter = def.rate
    ? (params) => `${params[0].name}<br/>${fmtRateBytes(params[0].value)}`
    : undefined
  opt.yAxis.axisLabel = def.rate
    ? { color: chartMutedColor, formatter: (v) => fmtRateBytes(v) }
    : { color: chartMutedColor }
  opt.xAxis.data = points.map((p) => p.t)
  opt.series = [lineSeries(def.label, points, primaryColor, { area: true, percent: def.percent, rate: def.rate })]
  if (def.rate) delete opt.series[0].tooltip
  holder.setOption(opt)
}

async function loadBoard() {
  if (boardLoading.value) return
  boardLoading.value = true
  try {
    if (board.value === 'overview') {
      // 宿主机曲线复用仪表盘聚合端点；池使用率走新的原生看板端点
      const [hostRes, poolRes] = await Promise.allSettled([api.hostHistory(minutes.value), api.poolHistory(minutes.value)])
      if (hostRes.status === 'fulfilled') {
        const d = hostRes.value.data || {}
        hostSource.value = d.source || 'prometheus'
        renderHostChart(Array.isArray(d.points) ? d.points : [])
      } else {
        hostSource.value = 'unavailable'
      }
      if (poolRes.status === 'fulfilled') {
        const d = poolRes.value.data || {}
        poolSource.value = d.source || 'prometheus'
        const pools = d.pools || {}
        poolNames.value = Object.keys(pools)
        if (poolNames.value.length) renderPoolChart(pools)
      } else {
        poolSource.value = 'unavailable'
        poolNames.value = []
      }
    } else {
      const res = await api.vmMetricsHistory(minutes.value)
      const d = res.data || {}
      vmSource.value = d.source || 'prometheus'
      vmData.value = d.vms || {}
      // 上次选中的 VM 已无采样（关机/删除）时回落到第一台
      if (!selectedVM.value || !vmData.value[selectedVM.value]) {
        selectedVM.value = vmNames.value[0] || ''
      }
      await nextTick()
      if (selectedVM.value) {
        const row = vmData.value[selectedVM.value]
        for (const m of VM_METRICS) renderMetricChart(m.key, m, row[m.key] || [])
      }
    }
  } finally {
    boardLoading.value = false
  }
}

function reloadBoard() {
  loadBoard()
  loadPanels() // 面板与看板共用时间跨度；不进 3s 轮询（每次 3+ 个查询，手动/换档刷新足够）
}

// 切换看板：懒初始化对应图表（嵌入 Dashboard tab 的 v-show 场景由 ResizeObserver 兜底）
watch(board, () => {
  nextTick(loadBoard)
})
// 切换选中 VM：重画六张小图
watch(selectedVM, () => {
  const row = vmData.value[selectedVM.value]
  if (!row) return
  nextTick(() => {
    for (const m of VM_METRICS) renderMetricChart(m.key, m, row[m.key] || [])
  })
})

const alerts = ref([])
const alertsLoading = ref(false)
const alertsError = ref(false)

// 只统计需要处理的告警（active = 触发中）
const firingCount = computed(
  () => alerts.value.filter((a) => a.status && a.status.state === 'active').length
)

async function loadAlerts() {
  if (alertsLoading.value) return
  alertsLoading.value = true
  try {
    const res = await api.listAlerts()
    alerts.value = Array.isArray(res.data) ? res.data : []
    alertsError.value = false
  } catch (e) {
    // 静默置错误态：页面内提示如何启动监控栈，不弹全局 toast
    alertsError.value = true
  } finally {
    alertsLoading.value = false
  }
}

// 告警历史（webhook 入库数据）：筛选 + 分页，不随实时告警轮询（历史变化慢，手动刷新即可）
const history = ref([])
const historyLoading = ref(false)
const historyTotal = ref(0)
const historyPage = ref(1)
const historyPageSize = 20
const historyStatus = ref('')

async function loadHistory() {
  if (historyLoading.value) return
  historyLoading.value = true
  try {
    const res = await api.monitorAlertHistory({
      status: historyStatus.value || undefined,
      page: historyPage.value,
      page_size: historyPageSize
    })
    const data = res.data || {}
    history.value = Array.isArray(data.items) ? data.items : []
    historyTotal.value = data.total || 0
  } catch (e) {
    // 历史查询失败不打断页面（实时告警仍在上方正常展示）
    history.value = []
    historyTotal.value = 0
  } finally {
    historyLoading.value = false
  }
}

// 切换状态筛选后回到第一页再查
function onHistoryFilter() {
  historyPage.value = 1
  loadHistory()
}

// node_exporter 抓取目标预览（file_sd）：实时查库计算，与落盘文件同源；
// 变化慢不进轮询，手动刷新即可。失败静默（监控栈未起/无权限时不打断页面其它区块）
const sdTargets = ref([])
const sdEnabled = ref(false)
const sdLoading = ref(false)

async function loadFileSD() {
  if (sdLoading.value) return
  sdLoading.value = true
  try {
    const res = await api.monitorFileSD()
    const data = res.data || {}
    // 兼容旧响应（纯数组）：无 enabled 字段时按「状态未知」处理不误导
    sdTargets.value = Array.isArray(data) ? data : (Array.isArray(data.items) ? data.items : [])
    sdEnabled.value = data.enabled === true
  } catch (e) {
    sdTargets.value = []
    sdEnabled.value = false
  } finally {
    sdLoading.value = false
  }
}

// 看板与实时告警轮询：周期取系统设置的 dashboard 偏好；卸载自动停表（useAutoRefresh 托管）。
// start() 只起表不触发 fn——首拉仍由 onMounted 里的显式 load 负责，与原行为一致。
const { start: startPolling, stop: stopPolling } = useAutoRefresh(() => {
  loadAlerts()
  loadBoard()
}, { intervalKey: 'dashboard' })

// 嵌入仪表盘时随 tab 激活态启停：el-tabs 切走只是 display:none、组件不卸载，
// 不停表则告警/看板在后台空转，且与 Dashboard 概览侧告警表双路打同一接口
watch(
  () => props.active,
  (on) => (on ? startPolling() : stopPolling())
)

onMounted(() => {
  loadAlerts()
  loadHistory()
  loadFileSD()
  loadBoard()
  loadPanels()
  startPolling()
})
// 卸载清理全部自带：轮询定时器在 useAutoRefresh、图表 dispose/观察断开在各 useChart handle
</script>

<style scoped>
.alert-card {
  margin-bottom: 16px;
}
/* .alert-firing / .head-icon / .history-pager / .sd-alert / .sd-note 已随对应卡片下沉到
   monitor/components/{RealtimeAlertsCard,AlertHistoryCard,FileSDCard}.vue（各留一份，改一处须同步） */
.alert-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-weight: 600;
}
.alert-head-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: normal;
  flex-wrap: wrap;
  justify-content: flex-end;
}

/* ── 原生看板：双列图表网格（窄屏回落单列）；图表固定高，容器宽度自适应 ── */
.chart-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}
.chart-grid.six {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}
@media (max-width: 1100px) {
  .chart-grid,
  .chart-grid.six {
    grid-template-columns: 1fr;
  }
}
.chart-cell {
  min-width: 0;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 12px;
}
.chart-title {
  font-size: 0.88rem;
  font-weight: 600;
  color: var(--color-foreground);
  margin-bottom: 8px;
}
.chart-box {
  height: 260px;
}
.chart-box.small {
  height: 180px;
}
.chart-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 260px;
}
.vm-picker {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
}
.chart-hint {
  font-size: 0.85rem;
  color: var(--el-text-color-secondary);
}

/* ── 扩展面板网格（panels.js 注册表驱动）：自适应列数，窄屏单列 ── */
.panel-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 16px;
  margin-bottom: 16px;
}
@media (max-width: 768px) {
  .panel-grid {
    grid-template-columns: 1fr;
  }
}
.panel-card {
  margin-bottom: 0;
}
.panel-box {
  height: 190px;
}
.panel-empty-hint {
  margin-top: 8px;
  font-size: 0.8rem;
  color: var(--el-text-color-secondary);
  line-height: 1.6;
}

</style>
