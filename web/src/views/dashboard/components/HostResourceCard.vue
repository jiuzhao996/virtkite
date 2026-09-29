<template>
  <el-card shadow="hover">
    <template #header>
      <span class="card-title">主机资源实时大盘</span>
      <span v-if="lastUpdate" class="update-time">更新于 {{ lastUpdate }}</span>
    </template>

    <div class="host-summary">
      <div class="host-metric">
        <el-progress
          type="circle"
          :percentage="host.cpu"
          :width="92"
          :stroke-width="9"
          :color="primaryColor"
        />
        <div class="metric-text">
          <span class="metric-num">{{ host.cpu }}<small>%</small></span>
          <span class="metric-label">主机 CPU 使用率</span>
        </div>
      </div>
      <div class="host-metric mem">
        <div class="metric-text">
          <span class="metric-num">{{ host.memUsed }}<small> / {{ host.memTotal }} GB</small></span>
          <span class="metric-label">主机内存 · 已用 {{ host.memPct }}%</span>
          <el-progress
            class="mem-bar"
            :percentage="host.memPct"
            :stroke-width="9"
            :color="memChartColor"
            :show-text="false"
          />
        </div>
      </div>
    </div>

    <div class="chart-wrap">
      <div ref="hostChartRef" class="host-chart" />
    </div>
  </el-card>
</template>

<script setup>
// 主机资源实时大盘卡（自 Dashboard.vue 拆出，渲染输出不变）：
// - shell 只负责 pollHost/prefillHostHistory 拉数，序列经 props 下发；
// - echarts 初始化 / setOption / resize / dispose 全部随组件生命周期走，
//   组件卸载即 dispose + 摘除 window resize 监听，无实例泄漏。
import { ref, watch, onMounted, onBeforeUnmount, nextTick } from 'vue'
import echarts from '../../../utils/echarts'
import { cssVar } from '../../../utils/format'

const props = defineProps({
  host: { type: Object, required: true }, // { cpu, memPct, memUsed, memTotal }
  lastUpdate: { type: String, default: '' },
  cpuSeries: { type: Array, required: true }, // 60 点环形数组（shell 原地 push/shift）
  memSeries: { type: Array, required: true },
  timeLabels: { type: Array, required: true },
  // shell 在「切回概览 tab」时 +1（对齐 TopologyView 的 active-tick 模式）：
  // echarts 容器从 display:none 恢复后需要手动 resize 一次否则图不渲染
  activeTick: { type: Number, default: 0 }
})

// echarts 不解析 var()，主机曲线需要真实色值：cssVar 由 utils/format.js 提供
const primaryColor = cssVar('--el-color-primary', '#2a9da5')
const memChartColor = cssVar('--color-success', '#16a34a')
// 图例 / 轴标签等文字与线条颜色同样必须取真实值（原处传 var() 会静默失效回退黑色）
const chartMutedColor = cssVar('--color-muted-foreground', '#64748b')
const chartAxisColor = cssVar('--color-border', '#e2e8f0')
const chartSplitColor = cssVar('--color-muted', '#eef2f6')

const hostChartRef = ref(null)
let chart = null

function updateChart() {
  if (!chart) return
  chart.setOption({
    xAxis: { data: props.timeLabels },
    series: [
      { data: props.cpuSeries },
      { data: props.memSeries }
    ]
  })
}

// 序列由 shell 原地 push/shift（3s 轮询）或整体替换（历史预填），deep watch 两种都接得住
watch(
  () => [props.cpuSeries, props.memSeries, props.timeLabels],
  updateChart,
  { deep: true }
)

// 切回概览 tab：容器从 display:none 恢复，等 DOM 更新后补一次 resize
watch(
  () => props.activeTick,
  () => nextTick(() => chart && chart.resize())
)

function initChart() {
  if (!hostChartRef.value) return
  chart = echarts.init(hostChartRef.value)
  chart.setOption({
    animationDuration: 300,
    grid: { left: 8, right: 12, top: 36, bottom: 4, containLabel: true },
    tooltip: {
      trigger: 'axis',
      valueFormatter: (v) => v + '%'
    },
    legend: {
      top: 4,
      right: 8,
      itemWidth: 14,
      itemHeight: 8,
      textStyle: { color: chartMutedColor, fontSize: 12 }
    },
    xAxis: {
      type: 'category',
      boundaryGap: false,
      data: props.timeLabels,
      axisLine: { lineStyle: { color: chartAxisColor } },
      axisLabel: { color: chartMutedColor, fontSize: 12, interval: 14 }
    },
    yAxis: {
      type: 'value',
      max: 100,
      axisLabel: { formatter: '{value}%', color: chartMutedColor },
      splitLine: { lineStyle: { color: chartSplitColor } }
    },
    series: [
      {
        name: 'CPU',
        type: 'line',
        smooth: true,
        showSymbol: false,
        lineStyle: { width: 2, color: primaryColor },
        itemStyle: { color: primaryColor },
        areaStyle: { opacity: 0.08, color: primaryColor },
        data: props.cpuSeries
      },
      {
        name: '内存',
        type: 'line',
        smooth: true,
        showSymbol: false,
        lineStyle: { width: 2, color: memChartColor },
        itemStyle: { color: memChartColor },
        areaStyle: { opacity: 0.08, color: memChartColor },
        data: props.memSeries
      }
    ]
  })
}

const onResize = () => chart && chart.resize()

onMounted(() => {
  initChart()
  window.addEventListener('resize', onResize)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', onResize)
  if (chart) {
    chart.dispose()
    chart = null
  }
})
</script>

<style scoped>
.card-title {
  font-weight: 600;
  color: var(--color-foreground);
}
.update-time {
  float: right;
  font-size: 0.75rem;
  color: var(--color-muted-foreground);
  font-weight: 400;
}

/* 主机资源大盘 */
.host-summary {
  display: flex;
  align-items: center;
  gap: 32px;
  padding: 4px 0 8px;
}
.host-metric {
  display: flex;
  align-items: center;
  gap: 16px;
  flex: 1;
}
.host-metric.mem {
  border-left: 1px solid var(--color-border);
  padding-left: 32px;
}
.metric-text {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}
.metric-num {
  font-size: 1.7rem;
  font-weight: 700;
  color: var(--color-foreground);
  font-family: var(--font-mono);
  line-height: 1;
}
.metric-num small {
  font-size: 0.8rem;
  font-weight: 500;
  color: var(--color-muted-foreground);
  margin-left: 2px;
}
.metric-label {
  font-size: 0.82rem;
  color: var(--color-muted-foreground);
}
.mem-bar {
  width: 100%;
  max-width: 300px;
}
.chart-wrap {
  margin-top: 8px;
}
.host-chart {
  width: 100%;
  height: 220px;
}
</style>
