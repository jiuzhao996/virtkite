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
          <el-switch v-model="showVMs" size="small" />
          <span>挂载的虚拟机</span>
        </div>
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
        <span v-if="showVMs" class="st-legend"><i class="lg lg-vm" />虚拟机</span>
      </template>
    </Toolbar>

    <div class="st-canvas-wrap">
      <div ref="canvasRef" class="st-canvas" />
      <el-empty
        v-if="!loading && !gNodes.length"
        class="st-empty"
        description="暂无存储池或卷（libvirt 未返回数据）"
        :image-size="80"
      />
    </div>
    <p class="st-hint">
      图为「池 → 卷」的静态关系；池之间的金色弧线是跨池克隆血缘（标注条数），卷级克隆链
      的完整视图在「克隆家谱」与本池卷抽屉里。打开「挂载的虚拟机」可叠加挂载关系。点击池节点进入池管理。
    </p>
  </div>
</template>

<script setup>
// 存储拓扑（对标网络页首屏）：池 → 卷 两层为主，克隆血缘虚线为故事线。
// 挂载虚拟机层默认收起（26 条挂载边全画出来会盖掉主体），开关按需叠加。
// 数据复用既有端点 listStoragePools + volumeGraph；X6 用法与网络拓扑同套
// （shape 显式 / orth 路由 / fromJSON / 隐形端口 / 内层 absolute+inset:0 断回写环）。
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
const gNodes = ref([])         // volumeGraph.nodes（path 唯一）
const gEdges = ref([])         // volumeGraph.edges（parent/child 即 path）
const summary = ref({})
const showVMs = ref(false)     // 挂载边默认不画：全画出来会盖掉池→卷主体
const showLineage = ref(true)

let graph = null

const PW = 176, PH = 46        // 池节点（去掉容量条后收紧高度）
const VW = 136, VH = 44        // 卷节点
const MW = 132, MH = 42        // VM 节点
const GAPX = 14, GAPY = 14
const GAP_COL = 56
const MAX_COLS = 4
const ROW = { pool: 24, vol: 150, vm: 330 }

const stats = computed(() => ({
  pools: pools.value.length,
  vols: gNodes.value.filter((n) => !n.phantom).length
}))

// 跨池血缘聚合到池层级：卷级虚线全画出来会横穿所有池的卷区（实测太乱）。
// 池间一条弧线 + 条数标注；卷级细节由「克隆家谱 / 本池血缘」抽屉兜底。
const poolLineage = computed(() => {
  const poolOf = {}
  for (const v of gNodes.value) poolOf[v.path] = v.pool
  const m = {}
  for (const e of gEdges.value) {
    const fp = poolOf[e.parent], tp = poolOf[e.child]
    if (!fp || !tp || fp === tp) continue
    m[fp + '->' + tp] = (m[fp + '->' + tp] || 0) + 1
  }
  return Object.entries(m).map(([k, count]) => {
    const [from, to] = k.split('->')
    return { from, to, count }
  })
})

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
const panelId = (name) => 'panel:' + name

// 卷状态：模板金 / 在用青 / 零引用灰 / 池外淡虚线
function volStyle(n) {
  if (n.is_template) return { stroke: '#c9971c', fill: '#fdf6e3', text: '#7a5b00' }
  if (n.phantom) return { stroke: '#b9c0ca', fill: '#f4f4f5', text: '#6b7280', dash: '4 3' }
  if (n.in_use) return { stroke: '#0ea5b7', fill: '#e6f7fb', text: '#0e7490' }
  return { stroke: '#b3bac2', fill: '#f5f6f8', text: '#66707a' }
}

// ── 分层布局：池一行；各池卷网格（池底板视觉分组）；VM 行可选 ──
function buildLayout(poolList, vols) {
  const pos = {}
  const panels = []
  const byPool = {}
  for (const v of vols) (byPool[v.pool] = byPool[v.pool] || []).push(v)
  for (const k of Object.keys(byPool)) byPool[k].sort((a, b) => String(a.name).localeCompare(String(b.name)))

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
  const vmNames = showVMs.value ? [...new Set(vols.flatMap((v) => v.vms || []))] : []
  const vmRowW = vmNames.length ? vmNames.length * MW + (vmNames.length - 1) * GAPX : 0
  const canvasW = Math.max(760, totalW, vmRowW) + 120
  const cx = canvasW / 2
  let maxVolRows = 1

  let px = cx - totalW / 2
  for (const p of poolList) {
    const { colW, rows, kids } = col[p.name]
    maxVolRows = Math.max(maxVolRows, rows)
    pos[poolId(p.name)] = { x: px + colW / 2 - PW / 2, y: ROW.pool }
    if (kids.length) {
      // 池底板：把该池的卷网格圈进一块浅色区域（zIndex 0，纯视觉分组）
      panels.push({
        id: panelId(p.name),
        x: px - 8, y: ROW.vol - 12,
        width: colW + 16, height: rows * (VH + GAPY) - GAPY + 24,
        name: p.name
      })
    }
    kids.forEach((k, idx) => {
      const r = Math.floor(idx / MAX_COLS)
      const c = idx % MAX_COLS
      pos[volId(k.path)] = { x: px + c * (VW + GAPX), y: ROW.vol + r * (VH + GAPY) }
    })
    px += colW + GAP_COL
  }
  let mx = cx - vmRowW / 2
  for (const name of vmNames) {
    pos[vmId(name)] = { x: mx, y: ROW.vm }
    mx += MW + GAPX
  }
  const canvasH = (vmNames.length ? ROW.vm + MH : ROW.vol + maxVolRows * (VH + GAPY)) + 60
  return { pos, panels, canvasW, canvasH }
}

// 池节点不再画容量条/容量文本：dir 池容量是文件系统级的，同盘多池口径完全相同，
// 逐池重复同一条进度条纯属噪音；容量统一在「存储池」tab 的物理容量汇总条展示。
function poolAttrs(p) {
  const sub = p.vol_count != null ? `${p.vol_count} 卷` : ''
  return {
    body: { stroke: '#7c5cd6', strokeWidth: 1.8, fill: '#f0e6ff', rx: 10, ry: 10, cursor: 'pointer' },
    label: { text: short(p.name, 12), fill: '#5b21b6', fontSize: 13, fontWeight: 600, textAnchor: 'middle', textVerticalAnchor: 'middle', refX: '50%', refY: 18 },
    sub: { text: sub, fill: '#8b6fd8', fontSize: 10, textAnchor: 'middle', textVerticalAnchor: 'middle', refX: '50%', refY: 36 }
  }
}
function volAttrs(n) {
  const st = volStyle(n)
  const title = short(n.name, 15) + (n.is_template ? ' ⭐' : '')
  return {
    body: { stroke: st.stroke, strokeWidth: 1.4, fill: st.fill, rx: 7, ry: 7, strokeDasharray: st.dash },
    label: { text: title, fill: st.text, fontSize: 11.5, fontWeight: 600, textAnchor: 'middle', textVerticalAnchor: 'middle', refX: '50%', refY: 15 },
    sub: { text: fmtGB(n.capacity_gb), fill: st.text, opacity: 0.7, fontSize: 9.5, textAnchor: 'middle', textVerticalAnchor: 'middle', refX: '50%', refY: 32 }
  }
}
function vmAttrs(name) {
  return {
    body: { stroke: '#3aa76d', strokeWidth: 1.5, fill: '#e8f7ee', rx: 8, ry: 8 },
    label: { text: short(name, 14), fill: '#1b6b3f', fontSize: 11.5, fontWeight: 600, textAnchor: 'middle', textVerticalAnchor: 'middle', refX: '50%', refY: '50%' }
  }
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

function renderGraph() {
  if (!graph) return
  const { pos, panels } = buildLayout(pools.value, gNodes.value)
  graph.fromJSON({ cells: buildCells(pos, panels) })
  fitView()
}

function buildCells(pos, panels) {
  const at = (id) => pos[id] || { x: 0, y: 0 }
  const vols = gNodes.value
  const cells = []
  // 池底板垫底（纯视觉分组，无交互）
  for (const pn of panels) {
    cells.push({
      shape: 'rect', id: panelId(pn.name), x: pn.x, y: pn.y, width: pn.width, height: pn.height,
      attrs: { body: { stroke: '#e8edf3', strokeWidth: 1, fill: '#fafbfd', rx: 10, ry: 10 } },
      zIndex: 0, data: { kind: 'panel' }
    })
  }
  for (const p of pools.value) {
    cells.push({
      shape: 'rect', id: poolId(p.name), x: at(poolId(p.name)).x, y: at(poolId(p.name)).y, width: PW, height: PH,
      attrs: poolAttrs(p), data: { kind: 'pool', name: p.name }, zIndex: 2,
      markup: [
        { tagName: 'rect', selector: 'body' },
        { tagName: 'text', selector: 'label' },
        { tagName: 'text', selector: 'sub' }
      ],
      ports: portGroups()
    })
  }
  for (const v of vols) {
    cells.push({
      shape: 'rect', id: volId(v.path), x: at(volId(v.path)).x, y: at(volId(v.path)).y, width: VW, height: VH,
      attrs: volAttrs(v), data: { kind: 'vol' }, zIndex: 2,
      ports: portGroups()
    })
  }
  const vmNames = showVMs.value ? [...new Set(vols.flatMap((v) => v.vms || []))] : []
  for (const name of vmNames) {
    cells.push({
      shape: 'rect', id: vmId(name), x: at(vmId(name)).x, y: at(vmId(name)).y, width: MW, height: MH,
      attrs: vmAttrs(name), data: { kind: 'vm', name }, zIndex: 2,
      ports: portGroups()
    })
  }
  const known = new Set(cells.map((c) => c.id))
  const edge = (from, to, o = {}) => ({
    shape: 'edge',
    source: { cell: from, port: 'bottom' },
    target: { cell: to, port: 'top' },
    attrs: {
      line: {
        stroke: o.stroke || '#d5dbe3',
        strokeWidth: o.width || 1.2,
        strokeDasharray: o.dash,
        opacity: o.opacity != null ? o.opacity : 0.9,
        targetMarker: o.arrow ? { name: 'block', size: 6 } : null
      }
    },
    connector: { name: 'smooth' },
    zIndex: 1
  })
  // 池 → 卷（柔和竖线）
  for (const v of vols) {
    if (known.has(poolId(v.pool)) && known.has(volId(v.path))) {
      cells.push(edge(poolId(v.pool), volId(v.path), { stroke: '#dbe1e8' }))
    }
  }
  // 卷 → VM（开启时才画）
  if (showVMs.value) {
    for (const v of vols) {
      for (const vm of v.vms || []) {
        if (known.has(vmId(vm))) cells.push(edge(volId(v.path), vmId(vm), { stroke: '#bcd8ca', opacity: 0.75 }))
      }
    }
  }
  // 跨池血缘：池节点间的金色弧线（从池行上方绕行），标注条数
  if (showLineage.value) {
    for (const pl of poolLineage.value) {
      const f = poolId(pl.from), t = poolId(pl.to)
      if (!known.has(f) || !known.has(t)) continue
      cells.push({
        shape: 'edge',
        source: { cell: f, port: 'top' },
        target: { cell: t, port: 'top' },
        attrs: {
          line: { stroke: '#d9a62e', strokeWidth: 1.6, strokeDasharray: '5 4', opacity: 0.9, targetMarker: { name: 'block', size: 6 } }
        },
        connector: { name: 'smooth' },
        labels: [{
          // 用默认 label/body 选择器：自定义 markup 缺 label 选择器会抛 reference 错误
          attrs: {
            label: { text: pl.count + ' 条血缘', fill: '#8a6a10', fontSize: 10 },
            body: { fill: '#fff8e1', stroke: '#e5cf8a', rx: 4, ry: 4, strokeWidth: 1 }
          },
          position: { distance: 0.5 }
        }],
        zIndex: 1
      })
    }
  }
  return cells
}

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
}

watch([showVMs, showLineage], () => nextTick(renderGraph))

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
.lg-orph { background: #f5f6f8; border: 1px solid #b3bac2; }
.lg-vm { background: #e8f7ee; border: 1px solid #3aa76d; }
</style>
