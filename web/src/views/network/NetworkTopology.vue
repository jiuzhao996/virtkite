<template>
  <div class="topo-wrap">
    <Toolbar>
      <template #left>
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        <el-button :icon="FullScreen" @click="fitView">适应画布</el-button>
        <el-tag v-if="stats.vms" effect="plain" size="small">虚拟机 {{ stats.vms }}</el-tag>
        <el-tag v-if="stats.containers" effect="plain" size="small" type="success">容器 {{ stats.containers }}</el-tag>
        <el-tag v-if="stats.networks" effect="plain" size="small" type="warning">网络 {{ stats.networks }}</el-tag>
        <span v-if="sampledAt" class="count">采样 {{ sampledAt }}</span>
        <!-- 关机 VM 不产生网络通信；默认隐藏使图清爽，需要看全量挂接时打开 -->
        <div class="topo-toggle">
          <el-switch v-model="showStopped" size="small" />
          <span>含关机虚拟机</span>
        </div>
      </template>
      <template #right>
        <span class="topo-legend"><i class="lg lg-host" />宿主机</span>
        <span class="topo-legend"><i class="lg lg-nic" />物理网卡</span>
        <span class="topo-legend"><i class="lg lg-libvirt" />虚拟网络</span>
        <span class="topo-legend"><i class="lg lg-docker" />Docker 网络</span>
        <span class="topo-legend"><i class="lg lg-vm" />虚拟机</span>
        <span class="topo-legend"><i class="lg lg-ct" />容器</span>
      </template>
    </Toolbar>

    <div v-loading="loading" class="topo-canvas-wrap">
      <div ref="canvasRef" class="topo-canvas" />
      <el-empty
        v-if="!loading && !visibleNodes.length"
        class="topo-empty"
        description="暂无网络数据（libvirt / Docker 均未返回网络）"
        :image-size="80"
      />
    </div>
    <!-- 说明：同桥内 VM↔容器为二层直通、不经宿主协议栈，故本图展示挂接关系而非实时流量 -->
    <p class="topo-hint">
      图为「谁挂在哪个网络」的静态挂接关系（可滚动缩放）；<b class="ok-dot">●</b> 表示邻居表判定最近有通信。
      点击虚拟机/容器节点跳转详情，点击网络节点切换到对应管理列表。实时连接边见「通信流量」tab。
    </p>
  </div>
</template>

<script setup>
// 全局网络拓扑（N1）：宿主机 → 桥/虚拟网络 → VM/容器 三层关系图，X6 承载。
// 布局为确定性手工分层（网卡上、宿主中、网络中、资源下，资源网格换行），不引 @antv/layout（现网无该依赖）。
import { ref, computed, onMounted, onBeforeUnmount, nextTick, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Graph } from '@antv/x6'
import { ElMessage } from 'element-plus'
import { Refresh, FullScreen } from '@element-plus/icons-vue'
import { api } from '../../api'
import { errMsg } from '../../utils/format'
import Toolbar from '../../components/Toolbar.vue'

const emit = defineEmits(['open-network'])
const router = useRouter()

const canvasRef = ref(null)
const loading = ref(false)
const nodes = ref([])
const edges = ref([])
const stats = ref({})
const sampledAt = ref('')
const showStopped = ref(false)

let graph = null

// 节点尺寸/间距常量
const W = 148
const H = 46
const GAPX = 30
const GAPY = 22
const GAPX_COL = 46
const MAX_COLS = 3 // 每个网络下的资源每行最多几个（超出换行，避免单行撑爆画布）
const ROW = { nic: 24, host: 150, net: 288, res: 436 }

const PALETTE = {
  host: { fill: '#1f2d3d', stroke: '#1f2d3d', text: '#ffffff' },
  nic: { fill: '#f4f4f5', stroke: '#909399', text: '#303133' },
  libvirt_net: { fill: '#f0e6ff', stroke: '#8b5cf6', text: '#5b21b6' },
  docker_net: { fill: '#e6f0ff', stroke: '#2f7fe0', text: '#1d4ed8' },
  vm: { fill: '#e8f7ee', stroke: '#3aa76d', text: '#1b6b3f' },
  container: { fill: '#e6f7fb', stroke: '#0ea5b7', text: '#0e7490' }
}

// 默认隐藏关机虚拟机（不产生网络通信）；打开后展示全量挂接
const visibleNodes = computed(() => {
  if (showStopped.value) return nodes.value
  return nodes.value.filter((n) => !(n.kind === 'vm' && n.state === 'shut off'))
})
const visibleEdges = computed(() => {
  const ids = new Set(visibleNodes.value.map((n) => n.id))
  return edges.value.filter((e) => ids.has(e.from) && ids.has(e.to))
})

function short(s, n = 18) {
  s = String(s || '')
  return s.length > n ? s.slice(0, n - 1) + '…' : s
}

function subLine(n) {
  if (n.kind === 'host') return '宿主机'
  if (n.kind === 'nic') return [n.state, (n.ips && n.ips[0]) || ''].filter(Boolean).join(' · ')
  if (n.kind === 'libvirt_net' || n.kind === 'docker_net') {
    const seg = (n.meta && n.meta['网段']) || ''
    return [n.state, seg].filter(Boolean).join(' · ')
  }
  const ip = (n.ips && n.ips[0]) || ''
  const st = n.state || ''
  return [st, ip].filter(Boolean).join(' · ') || '—'
}

// 按挂接边把资源节点归到其网络列下，资源网格换行
function buildLayout(ns, es) {
  const pos = {}
  const nics = ns.filter((n) => n.kind === 'nic')
  const nets = ns.filter((n) => n.kind === 'libvirt_net' || n.kind === 'docker_net')
  const host = ns.find((n) => n.kind === 'host')
  const resOf = {}
  for (const e of es) {
    if (e.from.startsWith('libvirt:') || e.from.startsWith('docker:')) {
      ;(resOf[e.from] = resOf[e.from] || []).push(e.to)
    }
  }
  const col = {}
  let totalNetW = 0
  nets.forEach((net, i) => {
    const kids = resOf[net.id] || []
    const cols = Math.max(1, Math.min(MAX_COLS, kids.length || 1))
    const colW = cols * W + (cols - 1) * GAPX
    const rows = Math.max(1, Math.ceil((kids.length || 1) / MAX_COLS))
    col[net.id] = { colW, rows, kids }
    totalNetW += colW + (i ? GAPX_COL : 0)
  })
  const nicRowW = nics.length ? nics.length * W + (nics.length - 1) * GAPX : 0
  const canvasW = Math.max(760, totalNetW, nicRowW) + 120
  const cx = canvasW / 2
  let maxRows = 1

  let nx = cx - nicRowW / 2
  for (const n of nics) {
    pos[n.id] = { x: nx, y: ROW.nic }
    nx += W + GAPX
  }
  if (host) pos[host.id] = { x: cx - W / 2, y: ROW.host }

  let sx = cx - totalNetW / 2
  for (const net of nets) {
    const { colW, rows, kids } = col[net.id]
    maxRows = Math.max(maxRows, rows)
    pos[net.id] = { x: sx + colW / 2 - W / 2, y: ROW.net }
    kids.forEach((kid, idx) => {
      const r = Math.floor(idx / MAX_COLS)
      const c = idx % MAX_COLS
      pos[kid] = { x: sx + c * (W + GAPX), y: ROW.res + r * (H + GAPY) }
    })
    sx += colW + GAPX_COL
  }
  const canvasH = ROW.res + maxRows * (H + GAPY) - GAPY + 60
  return { pos, canvasW, canvasH }
}

function nodeAttrs(n) {
  const c = PALETTE[n.kind] || PALETTE.nic
  return {
    body: { stroke: c.stroke, strokeWidth: 1.6, fill: c.fill, rx: 8, ry: 8 },
    label: {
      text: [short(n.name), subLine(n)].join('\n'),
      fill: c.text,
      fontSize: 12,
      lineHeight: 15,
      textAnchor: 'middle',
      textVerticalAnchor: 'middle',
      refX: 0.5,
      refY: 0.5
    }
  }
}

function renderGraph() {
  if (!graph) return
  graph.clearCells()
  const ns = visibleNodes.value
  const es = visibleEdges.value
  const { pos } = buildLayout(ns, es)
  const cells = []
  for (const n of ns) {
    const p = pos[n.id]
    if (!p) continue
    cells.push({
      shape: 'rect',
      id: n.id,
      x: p.x,
      y: p.y,
      width: W,
      height: H,
      attrs: nodeAttrs(n),
      data: n,
      zIndex: 2,
      ports: {
        // 端口仅作连线锚点，r:0 隐形（否则节点上下露出白圈圆点）
        groups: {
          top: { position: 'top', attrs: { circle: { r: 0, magnet: true, stroke: 'none', fill: 'none' } } },
          bottom: { position: 'bottom', attrs: { circle: { r: 0, magnet: true, stroke: 'none', fill: 'none' } } }
        },
        items: [
          { id: 'top', group: 'top' },
          { id: 'bottom', group: 'bottom' }
        ]
      }
    })
  }
  const known = new Set(ns.map((n) => n.id))
  for (const e of es) {
    if (!known.has(e.from) || !known.has(e.to)) continue
    cells.push({
      shape: 'edge',
      source: { cell: e.from, port: 'bottom' },
      target: { cell: e.to, port: 'top' },
      attrs: { line: { stroke: '#c3cbd6', strokeWidth: 1.4, targetMarker: { name: 'block', size: 6 } } },
      router: { name: 'orth' },
      connector: { name: 'rounded', args: { radius: 8 } },
      zIndex: 1
    })
  }
  graph.fromJSON({ cells })
  fitView()
}

// 适应视口：优先按宽度缩放，但下限 0.6 保文字可读（超出部分滚动/平移查看）
function fitView() {
  if (!graph) return
  graph.zoomToFit({ padding: 24, maxScale: 1.1 })
  if (graph.zoom() < 0.6) {
    graph.zoom(0.6)
    graph.centerContent()
  }
}

async function load() {
  loading.value = true
  try {
    const res = await api.networkTopology()
    const d = res.data || {}
    nodes.value = d.nodes || []
    edges.value = d.edges || []
    stats.value = d.stats || {}
    sampledAt.value = d.sampled_at || ''
    await nextTick()
    renderGraph()
  } catch (e) {
    ElMessage.error(errMsg(e, '获取网络拓扑失败'))
  } finally {
    loading.value = false
  }
}

function onNodeClick({ node }) {
  const d = node.getData() || {}
  if (d.link_type === 'vm') {
    router.push({ path: '/vms', query: { keyword: d.link_id } })
  } else if (d.link_type === 'container') {
    router.push({ path: '/containers', query: { tab: 'containers', id: d.link_id } })
  } else if (d.link_type === 'network') {
    emit('open-network', { kind: d.kind, name: d.link_id })
  }
}

watch(showStopped, () => nextTick(renderGraph))

onMounted(async () => {
  graph = new Graph({
    container: canvasRef.value,
    autoResize: true,
    background: { color: 'transparent' },
    grid: { visible: true, type: 'dot', size: 16, args: { color: '#e6e9ef', thickness: 1 } },
    panning: true,
    mousewheel: { enabled: true, modifiers: ['ctrl', 'meta'], minScale: 0.3, maxScale: 2 },
    interacting: { nodeMovable: false, edgeMovable: false, vertexMovable: false, arrowheadMovable: false }
  })
  graph.on('node:click', onNodeClick)
  await load()
})

onBeforeUnmount(() => {
  if (graph) { graph.dispose(); graph = null }
})
</script>

<style scoped>
.topo-wrap {
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.topo-toggle {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 0.82rem;
  color: var(--color-muted-foreground);
  margin-left: 6px;
}
.topo-canvas-wrap {
  position: relative;
  /* 高度锁在外层，overflow:hidden 兜底裁切（与设计器 .ds-canvas 同款）：
     X6 autoResize 会把「撑大的尺寸」内联回写到自己容器上，若容器的尺寸参与父布局，
     父元素跟着长高、传感器再读到更大的尺寸，循环把页面拉到十几万 px。内层用
     absolute + inset:0 彻底脱离父布局即断开该反馈环。 */
  height: calc(100vh - 260px);
  min-height: 420px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md, 8px);
  background:
    radial-gradient(circle at 1px 1px, #e6e9ef 1px, transparent 0) 0 0 / 16px 16px,
    #fbfcfe;
  overflow: hidden;
}
.topo-canvas {
  position: absolute;
  inset: 0;
}
.topo-empty {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}
.topo-hint {
  margin: 10px 2px 0;
  font-size: 0.8rem;
  color: var(--color-muted-foreground);
}
.topo-hint .ok-dot {
  color: var(--color-success, #3aa76d);
}
.topo-legend {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 0.78rem;
  color: var(--color-muted-foreground);
  margin-left: 10px;
}
.lg {
  width: 10px;
  height: 10px;
  border-radius: 3px;
  display: inline-block;
}
.lg-host { background: #1f2d3d; }
.lg-nic { background: #f4f4f5; border: 1px solid #909399; }
.lg-libvirt { background: #f0e6ff; border: 1px solid #8b5cf6; }
.lg-docker { background: #e6f0ff; border: 1px solid #2f7fe0; }
.lg-vm { background: #e8f7ee; border: 1px solid #3aa76d; }
.lg-ct { background: #e6f7fb; border: 1px solid #0ea5b7; }
</style>
