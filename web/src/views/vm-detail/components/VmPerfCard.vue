<template>
  <section v-show="active" class="panel">
    <div class="panel-head">
      <h3 class="panel-title">性能</h3>
    </div>
    <div v-if="!isRunning" class="panel-empty">
      <el-empty description="虚拟机未运行，无实时性能指标" :image-size="90" />
    </div>
    <template v-else>
      <div class="metric-grid">
        <el-card shadow="never" class="metric">
          <div class="metric-label">CPU 使用率</div>
          <el-progress :percentage="Math.round(cpuPct)" :color="usageColor(cpuPct)" :format="() => cpuPct.toFixed(1) + '%'" />
        </el-card>
        <el-card shadow="never" class="metric">
          <div class="metric-label">内存使用率{{ hasGuestMem ? '（客户机）' : '（分配）' }}</div>
          <el-progress :percentage="Math.round(memPct)" :color="usageColor(memPct)" :format="() => memText()" />
        </el-card>
      </div>
      <div class="metric-grid metric-grid-4">
        <el-card shadow="never" class="metric">
          <div class="metric-label">磁盘读取</div>
          <div class="metric-val mono">{{ fmtRateBytes(stats && stats.disk_read_bps) }}</div>
        </el-card>
        <el-card shadow="never" class="metric">
          <div class="metric-label">磁盘写入</div>
          <div class="metric-val mono">{{ fmtRateBytes(stats && stats.disk_write_bps) }}</div>
        </el-card>
        <el-card shadow="never" class="metric">
          <div class="metric-label">网络接收</div>
          <div class="metric-val mono">{{ fmtRateBytes(stats && stats.net_rx_bps) }}</div>
        </el-card>
        <el-card shadow="never" class="metric">
          <div class="metric-label">网络发送</div>
          <div class="metric-val mono">{{ fmtRateBytes(stats && stats.net_tx_bps) }}</div>
        </el-card>
      </div>
      <el-card shadow="never" class="chart-card">
        <template #header><span class="card-title">性能曲线（近 60 个采样点：历史来自 Prometheus，之后每 {{ intervalMs / 1000 }}s 实时追加）</span></template>
        <div ref="perfChartEl" class="perf-chart"></div>
      </el-card>
    </template>
  </section>
</template>

<script setup>
// 性能分区卡片：指标卡 + 实时曲线。echarts 的 init/resize/dispose 随本组件生命周期，
// 60 采样拼装（cpuHistory/memHistory）与 Prometheus 预填也自持在本组件。
//
// 数据流契约（与拆分前逐条对应）：
// - 轮询仍在壳：壳持 useAutoRefresh(pollStats, { intervalMs })，pollStats 自守卫
//   （VM 非 running 不拉），每次成功拉样经 ref 调 pushSample(freshStats) 追加采样点
//   （传实参避免 prop 未刷新读到旧值），再在 activeView==='perf' 时 ref 调 render() 重绘
//   ——「perf 才重绘」的守卫原样留在壳。
// - 挂载后壳按原顺序调 prefill()：从 Prometheus 预填近 60 点，拉不到静默降级。
// - 曲线重绘触发三条，等价原壳 watch：
//   1) 壳 pollStats 成功 && activeView==='perf' → render()
//   2) 本组件 watch(active) 翻 true（等价原 watch(activeView)）→ nextTick(init+render)
//   3) 本组件 watch(isRunning) 翻 true && active（等价原 watch(isRunning)）→ nextTick(init+render)
// v-show 常驻：分区切走再切回不丢历史采样、图表实例不重建。
import { ref, computed, watch, onUnmounted, nextTick } from 'vue'
import echarts from '../../../utils/echarts'
import { usageColor, fmtRateBytes, nowClock, cssVar } from '../../../utils/format'

const props = defineProps({
  vmId: { type: [String, Number], required: true },
  active: { type: Boolean, default: false }, // 是否处于性能分区（壳 activeView === 'perf'）
  isRunning: { type: Boolean, default: false },
  stats: { type: Object, default: null }, // 壳 pollStats 的最新采样（指标卡展示用）
  intervalMs: { type: Number, default: 2000 } // 轮询周期（标题文案用）
})

// echarts 不解析 var()，实时曲线需要真实色值：挂载时读一次 CSS 变量
const CHART_CPU_COLOR = cssVar('--el-color-primary', '#2a9da5')
const CHART_MEM_COLOR = cssVar('--color-warning', '#d97706')
const CHART_AXIS_COLOR = cssVar('--color-info', '#64748b')

/* ---------- 60 采样拼装（自持） ---------- */
const cpuHistory = ref([])
const memHistory = ref([])

/* ---------- 指标派生（公式与拆分前一致，纯函数化以便 pushSample 用最新采样实参） ---------- */
function cpuPctOf(s) {
  return Math.max(0, Math.min(100, Number((s && s.cpu_percent) || 0)))
}

function memPctOf(s) {
  const guestTotal = ((s && s.guest_total_kib) || 0) / 1024
  if (guestTotal > 0) return Math.min(100, (((s && s.guest_used_kib) || 0) / 1024 / guestTotal) * 100)
  const total = ((s && s.mem_total_kib) || 0) / 1024
  if (total > 0) return Math.min(100, (((s && s.mem_used_kib) || 0) / 1024 / total) * 100)
  return 0
}

const cpuPct = computed(() => cpuPctOf(props.stats))
const memPct = computed(() => memPctOf(props.stats))

const memUsedMB = computed(() => ((props.stats && props.stats.mem_used_kib) || 0) / 1024)
const memTotalMB = computed(() => ((props.stats && props.stats.mem_total_kib) || 0) / 1024)
const guestUsedMB = computed(() => ((props.stats && props.stats.guest_used_kib) || 0) / 1024)
const guestTotalMB = computed(() => ((props.stats && props.stats.guest_total_kib) || 0) / 1024)
const hasGuestMem = computed(() => guestTotalMB.value > 0)

function memText() {
  if (hasGuestMem.value) return guestUsedMB.value.toFixed(0) + ' / ' + guestTotalMB.value.toFixed(0) + ' MB'
  return memUsedMB.value.toFixed(0) + ' / ' + memTotalMB.value.toFixed(0) + ' MB'
}

// 壳 pollStats 每次成功拉样后调用：追加一个采样点并维持 60 点窗口（原 pushHistory 逻辑）
function pushSample(s) {
  const t = nowClock()
  cpuHistory.value.push({ t, v: Number(cpuPctOf(s).toFixed(1)) })
  memHistory.value.push({ t, v: Number(memPctOf(s).toFixed(1)) })
  if (cpuHistory.value.length > 60) cpuHistory.value.shift()
  if (memHistory.value.length > 60) memHistory.value.shift()
}

// 进详情页时从 Prometheus 预填历史曲线（替代"从零攒点、刷新即失"）：
// 拉不到（监控栈未起/VM 从未运行）静默降级为原行为。之后轮询继续无缝追加。
async function prefill() {
  try {
    const res = await api.vmStatsHistory(props.vmId, 30)
    const pts = (res.data && res.data.points) || []
    if (!pts.length) return
    const recent = pts.slice(-60)
    cpuHistory.value = recent.map((p) => ({ t: p.t, v: p.cpu }))
    memHistory.value = recent.map((p) => ({ t: p.t, v: p.mem }))
    if (props.active) renderNow()
  } catch (e) {
    /* 静默降级 */
  }
}

/* ---------- echarts：init/resize/dispose 随组件 ---------- */
const perfChartEl = ref(null)
let perfChart = null

function initChart() {
  if (perfChart || !perfChartEl.value) return
  perfChart = echarts.init(perfChartEl.value)
  window.addEventListener('resize', onWinResize)
}
function onWinResize() {
  if (perfChart) perfChart.resize()
}
function renderNow() {
  if (!perfChart || !perfChartEl.value) return
  perfChart.resize()
  perfChart.setOption(
    {
      tooltip: { trigger: 'axis' },
      legend: { data: ['CPU %', '内存 %'], bottom: 0, itemWidth: 14, itemHeight: 8, textStyle: { fontSize: 11 } },
      grid: { left: 8, right: 12, top: 28, bottom: 30, containLabel: true },
      xAxis: {
        type: 'category',
        boundaryGap: false,
        data: cpuHistory.value.map((p) => p.t),
        axisLabel: { fontSize: 10, color: CHART_AXIS_COLOR }
      },
      yAxis: { type: 'value', min: 0, max: 100, axisLabel: { fontSize: 10, color: CHART_AXIS_COLOR } },
      series: [
        {
          name: 'CPU %',
          type: 'line',
          smooth: true,
          showSymbol: false,
          data: cpuHistory.value.map((p) => p.v),
          lineStyle: { width: 2, color: CHART_CPU_COLOR },
          itemStyle: { color: CHART_CPU_COLOR },
          areaStyle: { opacity: 0.06, color: CHART_CPU_COLOR }
        },
        {
          name: '内存 %',
          type: 'line',
          smooth: true,
          showSymbol: false,
          data: memHistory.value.map((p) => p.v),
          lineStyle: { width: 2, color: CHART_MEM_COLOR },
          itemStyle: { color: CHART_MEM_COLOR },
          areaStyle: { opacity: 0.06, color: CHART_MEM_COLOR }
        }
      ]
    },
    true
  )
}

// 供壳 pollStats 在 perf 分区激活时重绘：采样序列是本组件自持状态，调用即读到最新值
function render() {
  renderNow()
}

// 分区切到性能：容器此时才可见，等 DOM 更新再 init+渲染（等价原壳 watch(activeView) 的 nextTick 时机）
watch(
  () => props.active,
  (on) => {
    if (on) nextTick(() => {
      initChart()
      renderNow()
    })
  }
)
// VM 变为运行中且正停在性能分区：重建渲染（等价原壳 watch(isRunning)）
watch(
  () => props.isRunning,
  (r) => {
    if (r && props.active) nextTick(() => {
      initChart()
      renderNow()
    })
  }
)

onUnmounted(() => {
  window.removeEventListener('resize', onWinResize)
  if (perfChart) {
    perfChart.dispose()
    perfChart = null
  }
})

defineExpose({ pushSample, prefill, render })
</script>

<style scoped>
.panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
}
.panel-title {
  margin: 0;
  font-size: 1rem;
  font-weight: 600;
  color: var(--color-foreground);
}
.panel-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 32px 0;
}
.card-title {
  font-weight: 600;
  color: var(--color-foreground);
}

/* 性能 */
.metric-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;
  margin-bottom: 12px;
}
.metric-grid-4 {
  grid-template-columns: repeat(4, 1fr);
}
.metric-label {
  font-size: 0.85rem;
  color: var(--color-muted-foreground);
  margin-bottom: 8px;
}
.metric-val {
  font-size: 1.25rem;
  font-weight: 600;
  color: var(--color-foreground);
}
.chart-card {
  margin-bottom: 12px;
}
.perf-chart {
  height: 260px;
}

@media (max-width: 1200px) {
  .metric-grid-4 {
    grid-template-columns: repeat(2, 1fr);
  }
}
</style>
