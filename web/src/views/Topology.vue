<template>
  <div>
    <div class="page-head">
      <div>
        <h2 class="page-title">虚拟化拓扑</h2>
        <span class="page-desc">以宿主机为中心展示存储池与虚拟机的从属关系：池节点大小按容量、虚拟机节点颜色按运行状态、大小按内存；拖拽节点可整理布局，滚轮缩放，点击图例可按状态过滤</span>
      </div>
    </div>

    <el-card shadow="never">
      <div class="toolbar">
        <div class="toolbar-left">
          <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <span class="tp-hint">虚拟机 → 所属存储池 → 宿主机</span>
        </div>
        <span class="count">
          虚拟机 {{ vms.length }} 台 · 存储池 {{ pools.length }} 个 · 网络 {{ networks.length }} 个<span
            v-if="updatedAt"
          > · 更新于 {{ updatedAt }}</span>
        </span>
      </div>

      <div v-show="hasData" ref="chartRef" class="topo-chart" />
      <div v-if="!hasData" class="topo-empty">
        <el-empty :description="emptyText">
          <el-button type="primary" :loading="loading" @click="load">重新加载</el-button>
        </el-empty>
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import echarts from '../utils/echarts'
// utils/echarts 统一入口按需注册的图表清单里没有 graph 系列（此前只有折线图），
// 本页需要 graph；按其文件头约定「新增图表特性必须补注册」，在本页就地补注册，
// 不改动既有 utils/echarts.js（echarts.use 幂等，重复注册无副作用）
import { GraphChart } from 'echarts/charts'
echarts.use([GraphChart])

import http from '../api'
import { errMsg, cssVar, vmStatusText, vmStatusHex, fmtSizeBytes, nowClock } from '../utils/format'

const vms = ref([])
const pools = ref([])
const networks = ref([])
const loading = ref(false)
const loadFailed = ref(false)
// 最近一次成功拉取的时刻（HH:MM:SS），主数据（VM/池）任一路失败则不更新
const updatedAt = ref('')
const chartRef = ref(null)
let chart = null

const hasData = computed(() => vms.value.length > 0 || pools.value.length > 0)
const emptyText = computed(() =>
  loadFailed.value ? '拓扑数据加载失败，请点击重新加载' : '暂无存储池与虚拟机，先创建一台虚拟机再来看拓扑'
)

/* ---------- 节点分类与配色（echarts 不解析 var()，取真实色值） ---------- */
const COLOR_HOST = () => cssVar('--color-primary', '#2a9da5')
const COLOR_POOL = () => cssVar('--color-accent', '#217d83')
const COLOR_MUTED = () => cssVar('--color-muted-foreground', '#475569')

// 分类顺序即图例顺序：宿主机 / 存储池 / 四种 VM 运行状态（legend 点选即过滤）
const CATEGORIES = [
  { name: '宿主机' },
  { name: '存储池' },
  { name: '运行中' },
  { name: '已关机' },
  { name: '已暂停' },
  { name: '异常' }
]

function vmCategory(status) {
  if (status === 'running') return 2
  if (status === 'paused') return 4
  if (status === 'error') return 5
  return 3 // shut off / stopped / 未知一律按已关机灰
}

/* ---------- 尺寸映射：sqrt 比例缩放进 [lo, hi]，单值/零值有兜底 ---------- */
function scaleSize(values, lo, hi) {
  const nums = values.map((v) => Math.sqrt(Math.max(0, Number(v) || 0)))
  const min = Math.min(...nums)
  const max = Math.max(...nums)
  const span = max - min
  return (v) => {
    const n = Math.sqrt(Math.max(0, Number(v) || 0))
    if (!isFinite(n) || span === 0) return Math.round((lo + hi) / 2)
    return Math.round(lo + ((n - min) / span) * (hi - lo))
  }
}

/* ---------- 组装 graph 数据 ---------- */
function buildGraphData() {
  const nodes = []
  const links = []
  const seenLinks = new Set()
  const addLink = (source, target) => {
    const key = source + '→' + target
    if (source !== target && !seenLinks.has(key)) {
      seenLinks.add(key)
      links.push({ source, target })
    }
  }

  // 中心节点：宿主机
  nodes.push({
    id: 'host',
    name: '鸢航 VirtKite',
    category: 0,
    symbolSize: 76,
    itemStyle: { color: COLOR_HOST() },
    meta: { kind: 'host' }
  })

  // 一级：存储池（size 按容量；池可能不活跃，tooltip 里如实标注）
  const poolSize = scaleSize(pools.value.map((p) => p.capacity), 46, 88)
  const poolNames = new Set()
  pools.value.forEach((p) => {
    const id = 'pool:' + p.name
    poolNames.add(p.name)
    nodes.push({
      id,
      name: p.name,
      category: 1,
      symbolSize: poolSize(p.capacity),
      itemStyle: { color: COLOR_POOL() },
      meta: { kind: 'pool', pool: p }
    })
    addLink('host', id)
  })

  // 二级：虚拟机（颜色按状态、size 按内存）；所属池未登记时补一个幽灵池节点，避免悬空边
  const vmSize = scaleSize(vms.value.map((v) => v.memory_mb), 26, 62)
  vms.value.forEach((v) => {
    const id = 'vm:' + v.id
    nodes.push({
      id,
      name: v.name,
      category: vmCategory(v.status),
      symbolSize: vmSize(v.memory_mb),
      itemStyle: { color: vmStatusHex(v.status) },
      meta: { kind: 'vm', vm: v }
    })
    addLink('host', id)
    if (v.storage_pool) {
      const pid = 'pool:' + v.storage_pool
      if (!poolNames.has(v.storage_pool)) {
        poolNames.add(v.storage_pool)
        nodes.push({
          id: pid,
          name: v.storage_pool,
          category: 1,
          symbolSize: 40,
          itemStyle: { color: COLOR_POOL(), opacity: 0.55 },
          meta: { kind: 'pool', pool: { name: v.storage_pool, unregistered: true } }
        })
        addLink('host', pid)
      }
      addLink(id, pid)
    }
  })

  return { nodes, links }
}

/* ---------- tooltip：按节点类型展示详情 ---------- */
function tooltipFormatter(params) {
  if (params.dataType !== 'node' || !params.data || !params.data.meta) return ''
  const meta = params.data.meta
  if (meta.kind === 'host') {
    return (
      '<b>鸢航 VirtKite 管理节点</b><br/>KVM 宿主机（qemu:///system）<br/>' +
      '存储池 ' + pools.value.length + ' 个 · 虚拟机 ' + vms.value.length + ' 台 · 网络 ' + networks.value.length + ' 个'
    )
  }
  if (meta.kind === 'pool') {
    const p = meta.pool || {}
    if (p.unregistered) {
      return '<b>' + escapeHtml(p.name) + '</b><br/><span style="color:#d97706">未在平台登记的存储池（虚拟机引用）</span>'
    }
    const lines = [
      '<b>' + escapeHtml(p.name) + '</b>',
      '状态：' + (p.active ? '激活' : '未激活'),
      '容量：' + fmtSizeBytes(p.capacity),
      '已用：' + fmtSizeBytes(p.allocation),
      '可用：' + fmtSizeBytes(p.available),
      '卷数：' + (p.vol_count ?? '—')
    ]
    if (p.role) lines.splice(1, 0, '角色：' + escapeHtml(p.role))
    return lines.join('<br/>')
  }
  // vm
  const v = meta.vm || {}
  return [
    '<b>' + escapeHtml(v.name) + '</b>',
    '状态：' + vmStatusText(v.status, '未知'),
    'IP：' + (v.ip || '未获取'),
    'vCPU：' + (v.vcpu ?? '—') + ' 核',
    '内存：' + (v.memory_mb ? v.memory_mb + ' MB' : '—'),
    '存储池：' + (v.storage_pool || '—')
  ].join('<br/>')
}

function escapeHtml(s) {
  return String(s ?? '').replace(/[&<>"']/g, (ch) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
  }[ch]))
}

/* ---------- 渲染（initDom → setOption → resize 监听 → dispose，同 Dashboard 模式） ---------- */
function renderChart() {
  if (!chartRef.value) return
  if (!chart) chart = echarts.init(chartRef.value)
  const { nodes, links } = buildGraphData()
  chart.setOption(
    {
      animationDuration: 400,
      tooltip: {
        trigger: 'item',
        formatter: tooltipFormatter,
        backgroundColor: cssVar('--color-card', '#ffffff'),
        borderColor: cssVar('--color-border', '#e2e8f0'),
        textStyle: { color: cssVar('--color-foreground', '#1e293b'), fontSize: 12 },
        extraCssText: 'box-shadow: 0 4px 16px rgba(13,36,68,.12); line-height: 1.8;'
      },
      legend: {
        top: 6,
        right: 12,
        itemWidth: 14,
        itemHeight: 10,
        data: CATEGORIES.map((c) => c.name),
        textStyle: { color: COLOR_MUTED(), fontSize: 12 }
      },
      series: [
        {
          type: 'graph',
          name: '拓扑',
          layout: 'force',
          // 分类调色板：与 CATEGORIES 顺序一一对应。不设的话图例色块落 echarts 默认色，
          // 和节点实际配色（itemStyle）对不上，用户按图例过滤会被误导
          color: [
            COLOR_HOST(),
            COLOR_POOL(),
            cssVar('--color-success', '#16a34a'),
            cssVar('--color-info', '#64748b'),
            cssVar('--color-warning', '#d97706'),
            cssVar('--color-danger', '#dc2626')
          ],
          force: { repulsion: 300, edgeLength: [80, 200], gravity: 0.08 },
          roam: true,
          draggable: true,
          categories: CATEGORIES,
          data: nodes,
          links,
          label: {
            show: true,
            position: 'bottom',
            color: COLOR_MUTED(),
            fontSize: 12,
            formatter: (p) => (p.data && p.data.meta && p.data.meta.kind === 'host' ? '{bold|鸢航 VirtKite}' : p.name),
            rich: { bold: { fontWeight: 700, fontSize: 13, color: cssVar('--color-foreground', '#1e293b') } }
          },
          lineStyle: { color: cssVar('--color-border-strong', '#cbd5e1'), width: 1.2, opacity: 0.9 },
          emphasis: { focus: 'adjacency', lineStyle: { width: 3 } },
          scaleLimit: { min: 0.4, max: 4 }
        }
      ]
    },
    true
  )
}

const onResize = () => chart && chart.resize()

async function load() {
  loading.value = true
  loadFailed.value = false
  try {
    // 三个数据源互不依赖，并行拉取；单边失败不拖垮另两边（各自降级为空数组）
    const [vmsRes, poolsRes, netsRes] = await Promise.allSettled([
      http.get('/vms'),
      http.get('/storage/pools'),
      http.get('/networks')
    ])
    const itemsOf = (r) => (r.status === 'fulfilled' && r.value.data.data && r.value.data.data.items) || []
    vms.value = itemsOf(vmsRes)
    pools.value = itemsOf(poolsRes)
    networks.value = itemsOf(netsRes)
    // 只有承载图的 VM/池两路失败才算页面级失败（网络只影响计数）
    loadFailed.value = vmsRes.status === 'rejected' || poolsRes.status === 'rejected'
    if (loadFailed.value) {
      const reason = vmsRes.status === 'rejected' ? vmsRes.reason : poolsRes.reason
      ElMessage.error(errMsg(reason, '拓扑数据加载失败'))
    } else {
      updatedAt.value = nowClock()
    }
    await nextTick()
    renderChart()
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  load()
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
.toolbar-left {
  display: flex;
  align-items: center;
  gap: 8px;
}
.topo-chart {
  width: 100%;
  height: 560px;
}
.topo-empty {
  padding: 48px 0;
}
.tp-hint {
  color: var(--color-muted-foreground);
  font-size: 0.85rem;
}
</style>
