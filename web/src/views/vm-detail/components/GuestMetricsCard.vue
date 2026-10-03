<template>
  <el-card v-if="visible" shadow="never" class="guest-card">
    <template #header>
      <div class="guest-head">
        <span><el-icon class="head-icon"><Cpu /></el-icon>Guest 内部（node_exporter）</span>
        <el-tag size="small" :type="available ? 'success' : 'info'" effect="plain">
          {{ available ? '已接入 file_sd 服务发现' : '未安装 node_exporter' }}
        </el-tag>
      </div>
    </template>

    <el-alert
      v-if="!available && loaded"
      type="info"
      :closable="false"
      show-icon
      title="该虚拟机尚未安装 node_exporter（端口 9100）"
      description="安装并启动后，平台会在 1 分钟内经 file_sd 服务发现自动纳管其内部指标（根分区使用率 / 内存占用 / 负载），本卡片无需刷新即可出图。"
    />

    <template v-else>
      <div class="guest-grid">
        <div v-for="m in GUEST_METRICS" :key="m.key" class="guest-cell">
          <div class="guest-title">{{ m.label }}</div>
          <div :ref="(el) => bindChartRef(m.key, el)" class="guest-chart" />
        </div>
      </div>
      <p class="guest-note">数据来自 VM 内 node_exporter，经平台 file_sd 自动下发抓取目标；最新值 {{ updatedAtText }}</p>
    </template>
  </el-card>
</template>

<script setup>
// Guest 内部指标卡（原生看板扩展层，批次 4b）：消费 file_sd 抓取的 node_exporter 指标
// （file_sd 目标带 vm_name 标签，附着到该目标全部序列）。数据源 GET /api/vms/:id/guest-metrics
// （后端聚合根分区/内存/负载三条 query_range）；未安装 exporter 时 available=false 显示安装引导。
// 懒加载：仅性能 tab 激活时拉取（active 驱动），切走不轮询（guest 指标变化慢）。
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { Cpu } from '@element-plus/icons-vue'
import { api } from '../../../api'
import { cssVar, fmtDateTime } from '../../../utils/format'
import { useChart } from '../../../composables/useChart'

const props = defineProps({
  vmId: { type: [String, Number], required: true },
  // 壳的「性能 tab 是否激活」：激活才拉取（嵌在 VmPerfCard 之下，随分区显隐）
  active: { type: Boolean, default: false },
  // VM 未运行时 node_exporter 也不可达，直接隐藏整卡（省一个必 404 的请求）
  isRunning: { type: Boolean, default: false }
})

const GUEST_METRICS = [
  { key: 'fs', label: '根分区使用率（%）', percent: true },
  { key: 'mem', label: '内存占用（%）', percent: true },
  { key: 'load1', label: '负载（load1）' }
]

const visible = computed(() => props.isRunning && props.active)
const available = ref(false)
const loaded = ref(false)
const loading = ref(false)
const updatedAtText = ref('')
// useChart 每 key 一个实例：init 惰性（首渲才建）、ResizeObserver 自动 resize、
// unmount 自动 dispose（S1-2 收敛，替代此前手写 init/dispose 生命周期）
const chartHandles = {
  fs: useChart(),
  mem: useChart(),
  load1: useChart()
}
// 模板 :ref 转发到对应 handle 的 chartRef（ensureInit 在首渲 setOption 时取）
function bindChartRef(key, el) {
  chartHandles[key].chartRef.value = el
}

const primaryColor = cssVar('--el-color-primary', '#2a9da5')
const successColor = cssVar('--color-success', '#16a34a')
const goldColor = cssVar('--color-gold', '#ffd268')
const mutedColor = cssVar('--color-muted-foreground', '#64748b')
const axisColor = cssVar('--color-border', '#e2e8f0')
const splitColor = cssVar('--color-muted', '#eef2f6')
const colors = [primaryColor, successColor, goldColor]

async function load() {
  if (loading.value || loaded.value) return
  loading.value = true
  try {
    const res = await api.guestMetrics(props.vmId, 60)
    const d = res.data || {}
    available.value = d.available === true
    loaded.value = true
    if (!available.value) return
    await nextTick()
    GUEST_METRICS.forEach((m, i) => renderChart(m, d[m.key] || [], colors[i]))
    const last = (d.fs || []).slice(-1)[0]
    updatedAtText.value = last ? fmtDateTime(last.t) : ''
  } finally {
    loading.value = false
  }
}

function renderChart(def, points, color) {
  const handle = chartHandles[def.key]
  if (!handle.chartRef.value) return
  handle.setOption({
      grid: { left: 6, right: 8, top: 8, bottom: 2, containLabel: true },
      tooltip: { trigger: 'axis' },
      xAxis: {
        type: 'category',
        data: points.map((p) => p.t),
        axisLine: { lineStyle: { color: axisColor } },
        axisLabel: { color: mutedColor },
        axisTick: { show: false }
      },
      yAxis: {
        type: 'value',
        axisLabel: { color: mutedColor },
        splitLine: { lineStyle: { color: splitColor } },
        ...(def.percent ? { max: 100 } : {})
      },
      series: [
        {
          type: 'line',
          data: points.map((p) => p.val),
          symbol: 'none',
          lineStyle: { width: 1.5, color },
          itemStyle: { color },
          areaStyle: { opacity: 0.08 }
        }
      ]
  })
}

watch(
  () => props.active,
  (on) => {
    if (on && props.isRunning && !loaded.value) load()
  },
  { immediate: true }
)

</script>

<style scoped>
.guest-card {
  margin-bottom: 16px;
}
.guest-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-weight: 600;
}
.head-icon {
  vertical-align: -0.15em;
  margin-right: 4px;
  color: var(--el-color-primary);
}
.guest-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
}
@media (max-width: 1100px) {
  .guest-grid {
    grid-template-columns: 1fr;
  }
}
.guest-cell {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 10px 12px;
}
.guest-title {
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--color-foreground);
  margin-bottom: 6px;
}
.guest-chart {
  height: 150px;
}
.guest-note {
  margin: 12px 0 0;
  font-size: 0.8rem;
  color: var(--el-text-color-secondary);
}
</style>
