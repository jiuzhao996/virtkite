<template>
  <div class="st-wrap">
    <Toolbar>
      <template #left>
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        <el-button :icon="FullScreen" @click="fitView">适应画布</el-button>
        <el-tag v-if="stats.pools" effect="plain" size="small">池 {{ stats.pools }}</el-tag>
        <el-tag v-if="stats.vols" effect="plain" size="small" type="warning">卷 {{ stats.vols }}</el-tag>
        <el-tag v-if="summary.clone_count" effect="plain" size="small" type="primary">克隆链 {{ summary.clone_count }}</el-tag>
        <el-tag v-if="summary.orphan_actual_gb > 0" effect="plain" size="small" type="danger">
          孤儿 {{ fmtGB(summary.orphan_actual_gb) }}
        </el-tag>
        <div class="st-toggle">
          <el-switch v-model="showLineage" size="small" />
          <span>克隆血缘</span>
        </div>
      </template>
      <template #right>
        <span class="st-legend"><i class="lg lg-pool" />存储池</span>
        <span class="st-legend"><i class="lg lg-tpl" />模板基盘</span>
        <span class="st-legend"><i class="lg lg-vol" />在用卷</span>
        <span class="st-legend"><i class="lg lg-orph" />零引用卷</span>
        <span class="st-legend"><i class="lg lg-vm" />虚拟机</span>
      </template>
    </Toolbar>

    <div class="st-canvas-wrap">
      <div ref="canvasRef" class="st-canvas" />
      <el-empty
        v-if="!loading && !nodes.length"
        class="st-empty"
        description="暂无存储池或卷（libvirt 未返回数据）"
        :image-size="80"
      />
    </div>
    <p class="st-hint">
      图为「池 → 卷 → 挂载虚拟机」的静态关系；虚线金边是增量克隆血缘（基盘 → 子卷，跨池如实画出）。
      点击存储池节点进入池管理，点击虚拟机节点跳转虚拟机列表。
    </p>
  </div>
</template>

<script setup>
// 存储拓扑（对标网络页拓扑图）：池 → 卷 → VM 三层关系 + 可选克隆血缘虚线边。
// 数据复用两个既有端点：listStoragePools（池容量/卷数）与 volumeGraph（全库血缘，
// nodes 按 path 唯一、edges.parent/child 即 path）。布局为确定性手工分层，
// 与 NetworkTopology 同一套已验证的 X6 用法（shape 显式 / orth 路由 / fromJSON /
// 隐形端口 / 内层 absolute+inset:0 断开 autoResize 回写反馈环）。
import { ref, computed, onMounted, onBeforeUnmount, nextTick, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Graph } from '@antv/x6'
import { ElMessage } from 'element-plus'
import { Refresh, FullScreen } from '@element-plus/icons-vue'
import { api } from '../../../api'
import { errMsg } from '../../../utils/format'
import Toolbar from '../../../components/Toolbar.vue'

const emit = defineEmits(['open-pool'])
const router = useRouter()

const canvasRef = ref(null)
const loading = ref(false)
const pools = ref([])          // 池条目（listStoragePools）
const gNodes = ref([])         // volumeGraph.nodes
const gEdges = ref([])         // volumeGraph.edges（path → path）
const summary = ref({})
const showLineage = ref(true)

let graph = null

// 尺寸/间距常量（卷多时网格换行，避免单行撑爆画布）
const PW = 170, PH = 56        // 池节点
const VW = 132, VH = 44        // 卷节点
const MW = 132, MH = 42        // VM 节点
const GAPX = 16, GAPY = 16
const GAP_COL = 44
const MAX_COLS = 4
const ROW = { pool: 24, vol: 150, vm: 320 }

const stats = computed(() => ({
  pools: pools.value.length,
  vols: gNodes.value.filter((n) => !n.phantom).length
}))

function fmtGB(v) {
  const n = Number(v) || 0
  if (n >= 100) return n.toFixed(0) + ' GB'
  if (n >= 1) return n.toFixed(1).replace(/\.0$/, '') + ' GB'
  return (n * 1024).toFixed(0) + ' MB'
}
function short(s, n = 14) {
  s = String(s || '')
  return s.length > n ? s.slice(0, n - 1) + '…' : s
}

const volId = (path) => 'vol:' + path
const vmId = (name) => 'vm:' + name
const poolId = (name) => 'pool:' + name

// 卷状态分类：模板金 / 在用青 / 零引用灰 / 池外淡虚线
function volStyle(n) {
  if (n.is_template) return { stroke: '#c9971c', fill: '#fdf6e3', text: '#7a5b00', sub: '模板基盘' }
  if (n.in_use) return { stroke: '#0ea5b7', fill: '#e6f7fb', text: '#0e7490', sub: '' }
  if (n.phantom) return { stroke: '#b9c0ca', fill: '#f4f4f5', text: '#6b7280', sub: '池外父盘', dash: '4 3' }
  return { stroke: '#9aa3ad', fill: '#f2f4f6', text: '#5f6b76', sub: '零引用' }
}

// ── 分层布局：池一行；各池列内卷网格换行；VM 全局一行去重 ──
function buildLayout(poolList, vols) {
  const pos = {}
  const byPool = {}
  for (const v of vols) (byPool[v.pool] = byPool[v.pool] || []).push(v)

  const col = {}
  let totalW = 0
  poolList.forEach((p, i) => {
    const kids = byPool[p.name] || []
    const cols = Math.max(1, Math.min(MAX_COLS, kids.length || 1))
    const colW = Math.max(PW, cols * VW + (cols - 1) * GAPX)
    const rows = Math.max(1, Math.ceil((kids.length || 1) / MAX_COLS))
    col[p.name] = { colW, rows, kids }
    totalW += colW + (i ? GAP_COL : 0)
  })
  // VM 行也参与总宽
  const vmNames = [...new Set(vols.flatMap((v) => v.vms || []))]
  const vmRowW = vmNames.length ? vmNames.length * MW + (vmNames.length - 1) * GAPX : 0
  const canvasW = Math.max(760, totalW, vmRowW) + 120
  const cx = canvasW / 2
  let maxVolRows = 1

  // 池一行居中
  let px = cx - totalW / 2
  for (const p of poolList) {
    const { colW, rows, kids } = col[p.name]
    maxVolRows = Math.max(maxVolRows, rows)
    pos[poolId(p.name)] = { x: px + colW / 2 - PW / 2, y: ROW.pool }
    kids.forEach((k, idx) => {
      const r = Math.floor(idx / MAX_COLS)
      const c = idx % MAX_COLS
      pos[volId(k.path)] = { x: px + c * (VW + GAPX), y: ROW.vol + r * (VH + GAPY) }
    })
    px += colW + GAP_COL
  }
  // VM 全局一行居中
  let mx = cx - vmRowW / 2
  for (const name of vmNames) {
    pos[vmId(name)] = { x: mx, y: ROW.vm }
    mx += MW + GAPX
  }
  const canvasH = ROW.vm + MH + 60
  return { pos, canvasW, canvasH }
}

function poolAttrs(p) {
  const used = Number(p.capacity) - Number(p.available)
  const sub = p.vol_count != null ? `${p.vol_count} 卷 · ${fmtGB(used / (1024 ** 3))}/${fmtGB(p.capacity / (1024 ** 3))}` : ''
  return {
    body: { stroke: '#7c5cd6', strokeWidth: 1.8, fill: '#f0e6ff', rx: 10, ry: 10, cursor: 'pointer' },
    label: { text: short(p.name, 12), fill: '#5b21b6', fontSize: 13, fontWeight: 600, textAnchor: 'middle', textVerticalAnchor: 'middle', refX: '50%', refY: 16 },
    sub: { text: sub, fill: '#8b6fd8', fontSize: 10, textAnchor: 'middle', textVerticalAnchor: 'middle', refX: '50%', refY: 38 }
  }
}
function volAttrs(n) {
  const st = volStyle(n)
  const title = short(n.name, 14) + (n.is_template ? ' ⭐' : '')
  const sub = [fmtGB(n.capacity_gb), st.sub].filter(Boolean).join(' · ')
  return {
    body: { stroke: st.stroke, strokeWidth: 1.5, fill: st.fill, rx: 7, ry: 7, strokeDasharray: st.dash },
    label: { text: title, fill: st.text, fontSize: 11.5, fontWeight: 600, textAnchor: 'middle', textVerticalAnchor: 'middle', refX: '50%', refY: 15 },
    sub: { text: sub, fill: st.text, opacity: 0.75, fontSize: 9.5, textAnchor: 'middle', textVerticalAnchor: 'middle', refX: '50%', refY: 32 }
  }
}
function vmAttrs(name) {
  return {
    body: { stroke: '#3aa76d', strokeWidth: 1.5, fill: '#e8f7ee', rx: 8, ry: 8, cursor: 'pointer' },
    label: { text: short(name, 14), fill: '#1b6b3f', fontSize: 11.5, fontWeight: 600, textAnchor: 'middle', textVerticalAnchor: 'middle', refX: '50%', refY: '50%' }
  }
}

function renderGraph() {
  if (!graph) return
  const { pos } = buildLayout(pools.value, gNodes.value)
  graph.fromJSON({ cells: buildCells(pos) })
  fitView()
}

function buildCells(pos) {
  const at = (id) => pos[id] || { x: 0, y: 0 }
  const vols = gNodes.value
  const cells = []
  for (const p of pools.value) {
    cells.push({
      shape: 'rect', id: poolId(p.name), x: at(poolId(p.name)).x, y: at(poolId(p.name)).y, width: PW, height: PH,
      attrs: poolAttrs(p), data: { kind: 'pool', name: p.name }, zIndex: 2,
      ports: portGroups()
    })
  }
  for (const v of vols) {
    cells.push({
      shape: 'rect', id: volId(v.path), x: at(volId(v.path)).x, y: at(volId(v.path)).y, width: VW, height: VH,
      attrs: volAttrs(v), data: { kind: 'vol', ...v }, zIndex: 2,
      ports: portGroups()
    })
  }
  const vmNames = [...new Set(vols.flatMap((v) => v.vms || []))]
  for (const name of vmNames) {
    cells.push({
      shape: 'rect', id: vmId(name), x: at(vmId(name)).x, y: at(vmId(name)).y, width: MW, height: MH,
      attrs: vmAttrs(name), data: { kind: 'vm', name }, zIndex: 2,
      ports: portGroups()
    })
  }
  const known = new Set(cells.map((c) => c.id))
  const edge = (from, to, opts = {}) => ({
    shape: 'edge',
    source: { cell: from, port: 'bottom' },
    target: { cell: to, port: 'top' },
    attrs: { line: { stroke: opts.stroke || '#c3cbd6', strokeWidth: opts.width || 1.4, strokeDasharray: opts.dash, targetMarker: opts.arrow ? { name: 'block', size: 6 } : null } },
    router: { name: 'orth' },
    connector: { name: 'rounded', args: { radius: 8 } },
    zIndex: 1
  })
  // 池 → 卷
  for (const v of vols) {
    if (known.has(poolId(v.pool)) && known.has(volId(v.path))) {
      cells.push(edge(poolId(v.pool), volId(v.path)))
    }
  }
  // 卷 → VM
  for (const v of vols) {
    for (const vm of v.vms || []) {
      if (known.has(vmId(vm))) cells.push(edge(volId(v.path), vmId(vm), { stroke: '#b9d6c6' }))
    }
  }
  // 克隆血缘（虚线金边，父盘 → 子卷；跨池如实画）
  if (showLineage.value) {
    for (const e of gEdges.value) {
      const f = volId(e.parent), t = volId(e.child)
      if (known.has(f) && known.has(t)) cells.push(edge(f, t, { stroke: '#d9a62e', dash: '5 4', arrow: true }))
    }
  }
  return cells
}

function portGroups() {
  return {
    groups: {
      top: { position: 'top', attrs: { circle: { r: 0, magnet: true, stroke: 'none', fill: 'none' } } },
      bottom: { position: 'bottom', attrs: { circle: { r: 0, magnet: true, stroke: 'none', fill: 'none' } } }
    },
    items: [{ id: 'top', group: 'top' }, { id: 'bottom', group: 'bottom' }]
  }
}

// 适应视口：按宽度缩放，下限 0.6 保文字可读（超出部分平移查看）
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
    const [poolRes, graphRes] = await Promise.all([api.listStoragePools(), api.volumeGraph()])
    pools.value = (poolRes.data && poolRes.data.items) || []
    gNodes.value = (graphRes.data && graphRes.data.nodes) || []
    gEdges.value = (graphRes.data && graphRes.data.edges) || []
    summary.value = (graphRes.data && graphRes.data.summary) || {}
    await nextTick()
    renderGraph()
  } catch (e) {
    ElMessage.error(errMsg(e, '获取存储拓扑失败'))
  } finally {
    loading.value = false
  }
}

function onNodeClick({ node }) {
  const d = node.getData() || {}
  if (d.kind === 'pool') emit('open-pool', d.name)
  else if (d.kind === 'vm') router.push({ path: '/vms', query: { keyword: d.name } })
}

watch(showLineage, () => nextTick(renderGraph))

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
.st-wrap {
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.st-toggle {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 0.82rem;
  color: var(--color-muted-foreground);
  margin-left: 6px;
}
.st-canvas-wrap {
  position: relative;
  /* 高度锁外层 + 内层 absolute 断开 X6 autoResize 的尺寸回写反馈环（同网络拓扑/设计器） */
  height: calc(100vh - 300px);
  min-height: 420px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md, 8px);
  background:
    radial-gradient(circle at 1px 1px, #e6e9ef 1px, transparent 0) 0 0 / 16px 16px,
    #fbfcfe;
  overflow: hidden;
}
.st-canvas {
  position: absolute;
  inset: 0;
}
.st-empty {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}
.st-hint {
  margin: 10px 2px 0;
  font-size: 0.8rem;
  color: var(--color-muted-foreground);
}
.st-legend {
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
.lg-pool { background: #f0e6ff; border: 1px solid #7c5cd6; }
.lg-tpl { background: #fdf6e3; border: 1px solid #c9971c; }
.lg-vol { background: #e6f7fb; border: 1px solid #0ea5b7; }
.lg-orph { background: #f2f4f6; border: 1px solid #9aa3ad; }
.lg-vm { background: #e8f7ee; border: 1px solid #3aa76d; }
</style>
