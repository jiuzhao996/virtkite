<template>
  <div v-show="nodes.length" ref="graphRef" class="lineage-graph" :style="{ height }" />
</template>

<script setup>
// 血缘图谱渲染组件（P4 后抽取复用）：模板/基盘 → 增量克隆链 → 挂载 VM 的 ECharts
// force 图。原为 VolumeLineageDrawer 内联实现；存储池详情「本池血缘」复用同一渲染
// （数据由调用方过滤——本池节点 + 跨池克隆链的对端边界节点，boundary 节点置灰）。
// 布局刻意用原生 force + draggable（与拓扑页同一教训：勿动 layoutAnimation/坐标固化）。
import { nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import echarts from '../../../utils/echarts'
import { cssVar } from '../../../utils/format'

const props = defineProps({
  nodes: { type: Array, default: () => [] },
  edges: { type: Array, default: () => [] },
  height: { type: String, default: '420px' },
})
const emit = defineEmits(['select'])

const graphRef = ref(null)
let chart = null

const primaryColor = cssVar('--el-color-primary', '#2a9da5')
const goldColor = cssVar('--color-gold', '#ffd268')
const mutedColor = cssVar('--color-muted-foreground', '#64748b')

function fmtGB(v) {
  const n = Number(v) || 0
  if (n >= 100) return n.toFixed(0) + ' GB'
  if (n >= 1) return n.toFixed(1).replace(/\.0$/, '') + ' GB'
  return (n * 1024).toFixed(0) + ' MB'
}

function nodeCategory(n) {
  if (n.is_template) return 0 // 模板基盘
  if (!n.in_use) return 2 // 孤儿
  return 1 // 在用
}

function renderGraph() {
  if (!graphRef.value || !props.nodes.length) return
  if (!chart) chart = echarts.init(graphRef.value)
  chart.resize()
  const nodes = props.nodes
  const edges = props.edges
  // 节点体量 = 实际占用（对数缩放，避免大基盘把孤儿卷挤成针尖）
  const maxSize = Math.max(...nodes.map((n) => n.allocation_gb), 1)
  const categories = [
    { name: '模板基盘', itemStyle: { color: goldColor } },
    { name: '在用卷', itemStyle: { color: primaryColor } },
    { name: '孤儿卷', itemStyle: { color: mutedColor } },
  ]
  chart.setOption(
    {
      tooltip: {
        formatter: (p) => {
          if (p.dataType === 'edge') {
            const s = nodes.find((x) => x.path === p.data.source)
            const t = nodes.find((x) => x.path === p.data.target)
            return `${s ? s.pool + '/' + s.name : '?'} → ${t ? t.pool + '/' + t.name : '?'}（backing）`
          }
          const n = nodes.find((x) => x.path === p.data.id)
          if (!n) return p.name
          return [
            `<b>${n.pool} / ${n.name}</b>${n.is_template ? '（模板）' : ''}${n.phantom ? '（池外）' : ''}${n._boundary ? '（跨池链对端）' : ''}`,
            n.phantom ? '容量未知（卷不在任何激活池）' : `虚拟 ${fmtGB(n.capacity_gb)} / 实际 ${fmtGB(n.allocation_gb)}`,
            n.vms.length ? `挂载：${n.vms.join('、')}` : null,
            n.children.length ? `子卷：${n.children.length} 个` : null,
            !n.in_use && !n.phantom ? '零引用（可回收）' : null,
          ].filter(Boolean).join('<br/>')
        },
      },
      legend: [{ data: categories.map((c) => c.name), textStyle: { color: mutedColor } }],
      series: [
        {
          type: 'graph',
          layout: 'force',
          roam: true,
          draggable: true,
          force: { repulsion: 260, edgeLength: [90, 220], gravity: 0.08 },
          categories,
          color: categories.map((c) => c.itemStyle.color),
          data: nodes.map((n) => ({
            id: n.path,
            name: n.name,
            category: nodeCategory(n),
            symbolSize: n.is_template || n.children.length ? 44 : 14 + 30 * Math.sqrt(n.allocation_gb / maxSize),
            // 孤儿半透明；跨池链对端（边界节点）更淡——视觉上退后，链路不断
            itemStyle: n._boundary
              ? { opacity: 0.35 }
              : n.in_use || n.phantom
                ? undefined
                : { opacity: 0.55 },
            label: { show: n.is_template || n.children.length > 0 || n.allocation_gb / maxSize > 0.08 },
          })),
          links: edges.map((e) => ({
            source: e.parent,
            target: e.child,
            lineStyle: { color: primaryColor, width: 1.5, curveness: 0.15 },
          })),
          edgeSymbol: ['none', 'arrow'],
          edgeSymbolSize: 8,
          label: { show: true, position: 'bottom', color: mutedColor, fontSize: 11 },
          emphasis: { focus: 'adjacency', lineStyle: { width: 3 } },
        },
      ],
    },
    true
  )
  chart.off('click')
  chart.on('click', (p) => {
    emit('select', p.dataType === 'node' ? nodes.find((n) => n.path === p.data.id) || null : null)
  })
}

watch(
  () => [props.nodes, props.edges],
  () => nextTick(renderGraph),
  { deep: false }
)

// 抽屉/折叠容器初次展开时 echarts.init 可能撞上零尺寸（SVG 不产出）——
// ResizeObserver 等容器有宽度后再渲染/重排，初始化与后续变化都覆盖
let ro = null
onMounted(() => {
  ro = new ResizeObserver(() => {
    if (graphRef.value && graphRef.value.clientWidth > 0) renderGraph()
  })
  if (graphRef.value) ro.observe(graphRef.value)
})

// 容器尺寸变化（抽屉开合/转屏）重排
function resize() {
  chart && chart.resize()
}
defineExpose({ resize, dispose: disposeChart })

function disposeChart() {
  if (chart) {
    chart.dispose()
    chart = null
  }
  ro && ro.disconnect()
  ro = null
}
onUnmounted(disposeChart)
</script>

<style scoped>
.lineage-graph {
  width: 100%;
  border: 1px dashed var(--color-border);
  border-radius: var(--radius-md);
}
</style>
