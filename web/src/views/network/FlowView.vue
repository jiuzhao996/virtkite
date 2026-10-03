<template>
  <div class="flow-wrap">
    <Toolbar>
      <template #left>
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        <span class="tp-hint">网络间实时通信（TCP 连接按网段归类聚合 · 3s 轮询 · 接口速率为 1s 窗口均值）</span>
      </template>
      <span class="count">
        {{ nets.length }} 个网络 · {{ edges.length }} 条通信路径<span v-if="sampledAt"> · 采样于 {{ sampledAt }}</span>
      </span>
    </Toolbar>

    <div class="flow-layout">
      <div ref="chartRef" class="flow-chart" />

      <div class="flow-side">
        <div class="fs-title">接口实时速率</div>
        <div class="fs-list">
          <div v-for="f in topIfaces" :key="f.name" class="fs-row">
            <span class="mono fs-name" :title="f.name">{{ f.name }}</span>
            <span class="fs-bar">
              <i :style="{ width: barPct(f.rx_bps), background: primaryColor }" />
            </span>
            <span class="mono fs-val">↓{{ fmtRate(f.rx_bps) }}</span>
            <span class="mono fs-val">↑{{ fmtRate(f.tx_bps) }}</span>
          </div>
        </div>
        <div class="fs-title fs-edges">通信路径 TOP</div>
        <div class="fs-list">
          <div v-for="(e, i) in topEdges" :key="i" class="fs-row">
            <span class="fs-edge">{{ netName(e.from) }} → {{ netName(e.to) }}</span>
            <span class="mono fs-val">{{ e.conns }} 连接</span>
          </div>
          <div v-if="!topEdges.length" class="fs-empty">暂无活跃连接</div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
// 网络通信流量视图（P1，2026-10-04）：宿主机/libvirt/Docker 三域网络间的实时通信。
// 数据 GET /api/networks/flows = 连接边（TCP 表按网段归类聚合）+ 接口速率（1s 差分）。
// 图：网络节点 + 流动箭头连线（line effect，按连接数定粗细/粒子大小）；右侧接口速率条。
// 3s 轮询经 useAutoRefresh 托管；采样含 1s 差分窗口，接口请求本身 ≈1s。
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { api } from '../../api'
import { cssVar, fmtRateBytes } from '../../utils/format'
import { useAutoRefresh } from '../../composables/useAutoRefresh'
import Toolbar from '../../components/Toolbar.vue'
import echarts from '../../utils/echarts'
import { GraphChart } from 'echarts/charts'
echarts.use([GraphChart]) // utils/echarts 已注册过则幂等

const nets = ref([])
const edges = ref([])
const ifaces = ref([])
const sampledAt = ref('')
const loading = ref(false)
const chartRef = ref(null)
let chart = null

const primaryColor = cssVar('--el-color-primary', '#2a9da5')
const netColor = cssVar('--color-violet', '#7c3aed')
const hostColor = cssVar('--color-success', '#16a34a')
const extColor = cssVar('--color-muted-foreground', '#64748b')
const muted = cssVar('--color-muted-foreground', '#64748b')

const KIND_COLOR = { libvirt: primaryColor, docker: netColor, host: hostColor, external: extColor }
const KIND_LABEL = { libvirt: '虚拟网络', docker: 'Docker', host: '宿主机', external: '外部' }

const netName = (id) => {
  const n = nets.value.find((x) => x.id === id)
  return n ? n.name : id
}
const topEdges = computed(() => [...edges.value].sort((a, b) => b.conns - a.conns).slice(0, 8))
const topIfaces = computed(() =>
  [...ifaces.value].sort((a, b) => b.rx_bps + b.tx_bps - (a.rx_bps + a.tx_bps)).slice(0, 10)
)
const maxRate = computed(() => Math.max(...topIfaces.value.map((f) => f.rx_bps + f.tx_bps), 1))
const barPct = (v) => Math.max(2, Math.round((v / maxRate.value) * 100)) + '%'
const fmtRate = (v) => fmtRateBytes(v || 0)

function renderChart() {
  if (!chartRef.value) return
  if (!chart) chart = echarts.init(chartRef.value)
  const maxConns = Math.max(...edges.value.map((e) => e.conns), 1)
  chart.setOption(
    {
      animationDurationUpdate: 300,
      tooltip: {
        trigger: 'item',
        backgroundColor: cssVar('--color-card', '#fff'),
        borderColor: cssVar('--color-border', '#e2e8f0'),
        textStyle: { color: cssVar('--color-foreground', '#1e293b'), fontSize: 12 },
        formatter: (p) => {
          if (p.dataType === 'edge') {
            return `${netName(p.data.source)} ↔ ${netName(p.data.target)}<br/>${p.data.conns} 条 TCP 连接`
          }
          const n = nets.value.find((x) => x.id === p.data.id)
          const ifs = ifaces.value.filter((f) => f.net_id === p.data.id)
          const rate = ifs.reduce((s, f) => s + f.rx_bps + f.tx_bps, 0)
          return [
            `<b>${p.data.name}</b>（${KIND_LABEL[n?.kind] || ''}）`,
            n?.subnet ? `网段：${n.subnet}` : null,
            ifs.length ? `接口速率：↓↑ ${fmtRate(rate)}` : null,
          ].filter(Boolean).join('<br/>')
        },
      },
      series: [
        {
          type: 'graph',
          layout: 'force',
          force: { repulsion: 400, edgeLength: [120, 240], gravity: 0.1 },
          roam: true,
          draggable: true,
          data: nets.value.map((n) => ({
            id: n.id,
            name: n.name,
            symbolSize: n.kind === 'external' || n.kind === 'host' ? 64 : 52,
            itemStyle: { color: KIND_COLOR[n.kind] || primaryColor },
            label: { show: true, position: 'bottom', color: muted, fontSize: 12 },
          })),
          links: edges.value.map((e) => ({
            source: e.from,
            target: e.to,
            value: e.conns,
            conns: e.conns,
            lineStyle: {
              color: primaryColor,
              width: 1 + (e.conns / maxConns) * 5,
              curveness: 0.15,
              opacity: 0.85,
            },
            // 流动感：粒子沿边流动，大小随连接数（视觉核心）
            effect: { show: true, period: 3, trailLength: 0.3, symbolSize: 4 + (e.conns / maxConns) * 10 },
          })),
          emphasis: { focus: 'adjacency', lineStyle: { width: 5 } },
          lineStyle: { color: primaryColor },
          scaleLimit: { min: 0.4, max: 4 },
        },
      ],
    },
    true
  )
}

async function load() {
  if (loading.value) return
  loading.value = true
  try {
    const res = await api.networkFlows()
    const d = res.data || {}
    nets.value = Array.isArray(d.networks) ? d.networks : []
    edges.value = Array.isArray(d.edges) ? d.edges : []
    ifaces.value = Array.isArray(d.interfaces) ? d.interfaces : []
    sampledAt.value = d.sampled_at || ''
    renderChart()
  } finally {
    loading.value = false
  }
}

const { start } = useAutoRefresh(load, { interval: 3000 })
const onResize = () => chart && chart.resize()
onMounted(() => {
  load()
  start()
  window.addEventListener('resize', onResize)
})
onUnmounted(() => {
  window.removeEventListener('resize', onResize)
  if (chart) {
    chart.dispose()
    chart = null
  }
})
</script>

<style scoped>
.flow-wrap {
  min-height: 480px;
}
.flow-layout {
  display: grid;
  grid-template-columns: 1fr 300px;
  gap: 16px;
  margin-top: 12px;
}
@media (max-width: 1100px) {
  .flow-layout {
    grid-template-columns: 1fr;
  }
}
.flow-chart {
  height: 460px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
}
.flow-side {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 12px 14px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}
.fs-title {
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--color-foreground);
  margin-bottom: 4px;
}
.fs-edges {
  margin-top: 10px;
}
.fs-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.fs-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 0.8rem;
  min-width: 0;
}
.fs-name {
  width: 84px;
  flex: none;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-muted-foreground);
}
.fs-bar {
  flex: 1;
  height: 6px;
  border-radius: 3px;
  background: var(--color-muted);
  overflow: hidden;
  min-width: 30px;
}
.fs-bar i {
  display: block;
  height: 100%;
  border-radius: 3px;
  transition: width 0.4s ease;
}
.fs-val {
  flex: none;
  color: var(--color-foreground);
  font-size: 0.78rem;
}
.fs-edge {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-muted-foreground);
}
.fs-empty {
  color: var(--color-muted-foreground);
  font-size: 0.8rem;
}
.tp-hint {
  color: var(--color-muted-foreground);
  font-size: 0.85rem;
}
</style>
