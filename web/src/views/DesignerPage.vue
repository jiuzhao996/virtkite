<template>
  <div>
    <PageHead title="架构设计" subtitle="拖拽编排 → 拉线连线 → 一键落地（容器栈 compose 部署 + VM 建机装应用，口令仅落地时填写不随计划保存）" />

    <div class="ds-layout">
      <!-- 左：模板库 + 设备栏（拖进画布）+ 已保存计划 -->
      <el-card shadow="never" class="ds-left">
        <template #header><span class="ds-h">预置架构</span></template>
        <div v-for="t in templates" :key="t.id" class="ds-tpl" @click="loadTemplate(t)">
          <span class="ds-tpl-name">{{ t.name }}</span>
          <span class="ds-tpl-desc">{{ t.desc }}</span>
        </div>
        <el-divider />
        <div class="ds-h ds-hrow"><span>设备栏</span><span class="ds-hint">拖进画布添加</span></div>
        <template v-for="g in paletteGroups" :key="g.kind">
          <div class="ds-palette-title" :style="{ color: g.color }">{{ g.title }}</div>
          <div class="ds-palette">
            <div
              v-for="it in g.items" :key="it.ref"
              class="ds-palette-item" :style="{ borderLeftColor: g.color }"
              :title="it.label + '（按住拖入画布）'"
              @mousedown="startDrag($event, g.kind, it)"
            >{{ it.label }}</div>
          </div>
        </template>
        <el-divider />
        <span class="ds-h">已保存计划</span>
        <div v-for="p in plans" :key="p.id" class="ds-plan" @click="loadPlan(p)">
          <span class="ds-tpl-name mono">{{ p.id }}</span>
          <el-button text size="small" type="danger" :icon="Delete" @click.stop="removePlan(p.id)" />
        </div>
      </el-card>

      <!-- 中：X6 画布（拖拽/连线/对齐） -->
      <el-card shadow="never" class="ds-mid">
        <template #header>
          <div class="ds-bar">
            <el-input v-model="planName" size="small" placeholder="计划名（保存用）" style="width: 180px" />
            <el-button size="small" :icon="DocumentChecked" @click="savePlan">保存</el-button>
            <el-button size="small" :icon="Download" @click="exportYaml">导出 YAML</el-button>
            <el-button size="small" :icon="Aim" @click="zoomFit">适应画布</el-button>
            <el-button type="primary" size="small" :icon="VideoPlay" :loading="applying" @click="applyPlan">一键落地</el-button>
            <span class="ds-tip">拖节点编排 · 节点边缘拉线连线 · Delete 删除选中</span>
          </div>
        </template>
        <!-- 外层锁高（overflow:hidden 兜底），X6 用独立内层容器——autoResize 的
             SizeSensor 绑的是 X6 容器的父元素（=外层），若让 X6 直接用带 CSS 高度
             的元素，panning 后传感器会把撑大的高度内联回写、循环锁死（页面被拉到
             十几万 px，centerContent 失效＝点模板"没反应"） -->
        <div class="ds-canvas">
          <div ref="canvasRef" class="ds-canvas-inner"></div>
        </div>
        <div v-if="applyStatus" class="ds-apply" :class="applyStatus.status">
          <b>{{ applyStatusText }}</b>
          <div v-for="(s, i) in applyStatus.steps" :key="i" class="ds-step mono">{{ s }}</div>
          <div v-if="applyStatus.error" class="ds-err">{{ applyStatus.error }}</div>
        </div>
      </el-card>

      <!-- 右：选中节点属性 -->
      <el-card shadow="never" class="ds-right">
        <template #header><span class="ds-h">节点属性</span></template>
        <template v-if="selected">
          <el-form label-width="64px" size="small">
            <el-form-item label="名称"><el-input v-model="selected.name" /></el-form-item>
            <el-form-item label="类型"><el-tag size="small" effect="plain">{{ kindLabel[selected.kind] }}</el-tag></el-form-item>
            <!-- VM 节点落地参数：这些字段随计划保存，口令除外 -->
            <template v-if="selected.kind === 'vm'">
              <el-form-item label="云镜像">
                <el-select v-model="selected.ref" filterable placeholder="选择云镜像" style="width: 100%">
                  <el-option v-for="img in cloudImages" :key="img.id" :label="img.name" :value="String(img.id)">
                    <span>{{ img.name }}</span>
                    <span class="ds-opt-sub">{{ img.os_version }}</span>
                  </el-option>
                </el-select>
              </el-form-item>
              <el-form-item label="存储池">
                <el-select v-model="selected.pool" placeholder="默认池" style="width: 100%">
                  <el-option v-for="p in storagePools" :key="p.name" :label="p.name" :value="p.name" />
                </el-select>
              </el-form-item>
              <el-form-item label="规格">
                <div class="ds-spec">
                  <el-input-number v-model="selected.vcpu" :min="1" :max="16" size="small" controls-position="right" style="width: 88px" />
                  <span class="ds-spec-unit">vCPU</span>
                  <el-input-number v-model="selected.memory_mb" :min="512" :step="512" size="small" controls-position="right" style="width: 96px" />
                  <span class="ds-spec-unit">MB</span>
                </div>
              </el-form-item>
              <el-form-item label="SSH 用户"><el-input v-model="selected.ssh_user" placeholder="root" /></el-form-item>
              <el-form-item label="SSH 口令">
                <el-input v-model="selected._sshSecret" type="password" show-password autocomplete="new-password" placeholder="仅本次落地使用，不随计划保存" />
              </el-form-item>
              <el-form-item label="应用">
                <el-select v-model="selected.apps" multiple filterable placeholder="落地后自动安装" style="width: 100%">
                  <el-option v-for="a in apps" :key="a.id" :label="a.name" :value="a.id" />
                </el-select>
              </el-form-item>
            </template>
            <el-form-item v-else-if="selected.kind === 'container'" label="引用栈">
              <span class="mono ds-ref">{{ selected.ref }}</span>
            </el-form-item>
            <el-form-item v-else label="建议值">
              <el-input v-model="selected.ref" placeholder="网段/用途" />
            </el-form-item>
            <el-form-item label="备注"><el-input v-model="selected.note" type="textarea" :rows="2" /></el-form-item>
          </el-form>
          <el-divider>连线</el-divider>
          <div class="ds-links">
            <div v-for="l in edgesOfSelected" :key="l.id" class="ds-link-row">
              <span class="mono">{{ l.text }}</span>
              <el-button text size="small" type="danger" :icon="Delete" @click="removeEdge(l.id)" />
            </div>
            <div v-if="!edgesOfSelected.length" class="ds-link-empty">从节点边缘的连接点拉线到目标节点</div>
          </div>
          <el-divider />
          <el-button text type="danger" size="small" :icon="Delete" @click="removeSelected">删除节点</el-button>
        </template>
        <el-empty v-else description="点击画布节点编辑" :image-size="60" />
      </el-card>
    </div>
  </div>
</template>

<script setup>
// 架构设计器（P2B v3）：画布换 AntV X6——设备栏拖拽添加（Addon.Dnd）、节点自由
// 拖动、边缘连接点拉线连线、snapline 对齐、Delete 删除。业务链路（模板/计划存取/
// YAML 导出/一键落地/属性面板）与 v2 完全一致，仅 planPayload 的数据源从 echarts
// 数组换成 graphToPlan()（X6 → 计划 JSON 转换层），后端协议零改动。
// SSH 口令只存在内存（节点 data._sshSecret），graphToPlan 剥离，落地时随 apply
// 请求体一次性携带（后端写盘前也会强制剥离兜底）。
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Delete, Download, VideoPlay, DocumentChecked, Aim } from '@element-plus/icons-vue'
import { api } from '../api'
import { errMsg, isCancel, cssVar } from '../utils/format'
import PageHead from '../components/PageHead.vue'
import { Graph, Shape } from '@antv/x6'
import { Snapline } from '@antv/x6-plugin-snapline'
import { Dnd } from '@antv/x6-plugin-dnd'

const templates = ref([])
const stacks = ref([])
const plans = ref([])
const cloudImages = ref([])
const storagePools = ref([])
const apps = ref([])
const planName = ref('')
const selected = ref(null)
const selectedEdgeId = ref('')
const edgesOfSelected = ref([])
const applying = ref(false)
const applyStatus = ref(null)
const canvasRef = ref(null)
let graph = null
let dnd = null
let applyTimer = null
let seq = 1

const kindLabel = { container: '容器栈', vm: 'VM 角色', net: '网络' }
const KIND_COLOR = {
  container: cssVar('--el-color-primary', '#2a9da5'),
  vm: cssVar('--color-success', '#16a34a'),
  net: cssVar('--color-violet', '#7c3aed'),
}
const SIDES = ['top', 'right', 'bottom', 'left']

const applyStatusText = computed(() => ({ running: '应用中…', success: '✓ 应用完成', failed: '✗ 应用失败' }[applyStatus.value?.status] || applyStatus.value?.status || ''))

// 设备栏分组：容器栈逐栈、VM 逐云镜像（拖哪个进画布 ref 就带哪个值——
// 省掉 v1「下拉选类型→下拉选引用→点+」三步）、网络给一个标注模板
const paletteGroups = computed(() => [
  { kind: 'container', title: '容器栈', color: KIND_COLOR.container, items: stacks.value.map((s) => ({ ref: s.id, label: s.id })) },
  { kind: 'vm', title: '虚拟机（云镜像）', color: KIND_COLOR.vm, items: cloudImages.value.map((i) => ({ ref: String(i.id), label: i.name })) },
  { kind: 'net', title: '网络（标注）', color: KIND_COLOR.net, items: [{ ref: '10.0.0.0/24', label: '网段' }] },
])

// ── X6 节点外观：body/label/sub 三段自定义 markup，四个边缘连接点（hover 显现）──
const NODE_MARKUP = [
  { tagName: 'rect', selector: 'body' },
  { tagName: 'text', selector: 'label' },
  { tagName: 'text', selector: 'sub' },
]
function portsConfig() {
  return {
    groups: Object.fromEntries(SIDES.map((s) => [s, {
      position: s,
      attrs: { circle: { r: 4.5, magnet: true, stroke: KIND_COLOR.container, strokeWidth: 1.5, fill: 'var(--el-bg-color, #fff)', style: { transition: 'opacity .15s' } } },
    }])),
    items: SIDES.map((s) => ({ id: s, group: s })),
  }
}
function buildNodeConfig(n) {
  const data = { kind: n.kind, ref: n.ref || '', name: n.name || '', note: n.note || '' }
  if (n.kind === 'vm') {
    Object.assign(data, { pool: n.pool || '', vcpu: n.vcpu || 0, memory_mb: n.memory_mb || 0, ssh_user: n.ssh_user || '', apps: n.apps ? [...n.apps] : [], _sshSecret: '' })
  }
  const base = { id: n.id, x: n.x, y: n.y, data, ports: portsConfig() }
  if (n.kind === 'container') {
    return { ...base, shape: 'rect', width: 128, height: 46, markup: NODE_MARKUP,
      attrs: {
        body: { rx: 8, ry: 8, fill: KIND_COLOR.container, stroke: 'transparent', strokeWidth: 2, cursor: 'grab' },
        label: { text: data.name, fill: '#fff', fontSize: 12, fontWeight: 600, textAnchor: 'middle', textVerticalAnchor: 'middle', refX: '50%', refY: 16, textWrap: { width: -16, ellipsis: true } },
        sub: { text: data.ref, fill: 'rgba(255,255,255,.72)', fontSize: 10, textAnchor: 'middle', textVerticalAnchor: 'middle', refX: '50%', refY: 31 },
      } }
  }
  if (n.kind === 'vm') {
    return { ...base, shape: 'rect', width: 128, height: 46, markup: NODE_MARKUP,
      attrs: {
        body: { rx: 4, ry: 4, fill: KIND_COLOR.vm, stroke: 'transparent', strokeWidth: 2, cursor: 'grab' },
        label: { text: data.name, fill: '#fff', fontSize: 12, fontWeight: 600, textAnchor: 'middle', textVerticalAnchor: 'middle', refX: '50%', refY: 16, textWrap: { width: -16, ellipsis: true } },
        sub: { text: (n.vcpu ? n.vcpu + 'C/' + n.memory_mb + 'MB' : '未选规格'), fill: 'rgba(255,255,255,.72)', fontSize: 10, textAnchor: 'middle', textVerticalAnchor: 'middle', refX: '50%', refY: 31 },
      } }
  }
  return { ...base, shape: 'polygon', width: 92, height: 62,
    attrs: {
      body: { refPoints: '46,0 92,31 46,62 0,31', fill: KIND_COLOR.net, stroke: 'transparent', strokeWidth: 2, cursor: 'grab' },
      label: { text: data.name, fill: '#fff', fontSize: 11, fontWeight: 600, textAnchor: 'middle', textVerticalAnchor: 'middle', refX: '50%', refY: '42%' },
      sub: { text: data.ref, fill: 'rgba(255,255,255,.72)', fontSize: 9, textAnchor: 'middle', textVerticalAnchor: 'middle', refX: '50%', refY: '60%' },
    } }
}
// 属性面板改动回写节点：名称/规格摘要同步到节点文字
function syncNodeView(node, d) {
  node.attr('label/text', d.name)
  if (d.kind === 'vm') node.attr('sub/text', d.vcpu ? d.vcpu + 'C/' + d.memory_mb + 'MB' : '未选规格')
  else node.attr('sub/text', d.ref)
}
watch(selected, (v) => {
  if (!v || !graph) return
  const node = graph.getCellById(v.id)
  if (!node || !node.isNode()) return
  const data = { kind: v.kind, ref: v.ref, name: v.name, note: v.note }
  if (v.kind === 'vm') Object.assign(data, { pool: v.pool, vcpu: v.vcpu, memory_mb: v.memory_mb, ssh_user: v.ssh_user, apps: v.apps, _sshSecret: v._sshSecret })
  node.setData(data, { overwrite: true })
  syncNodeView(node, data)
  refreshEdges()
}, { deep: true })

// ── 画布初始化 ──
function initGraph() {
  graph = new Graph({
    container: canvasRef.value,
    autoResize: true,
    grid: { size: 16, visible: true, type: 'dot', args: { color: cssVar('--color-border', '#dcdfe6'), thickness: 1 } },
    // panning 只认左键拖空白：默认 eventTypes 含 mouseWheel，会与滚轮缩放叠加
    // 且拖动导致容器尺寸变化，触发 autoResize 把尺寸内联回写（画布爆炸根因之一）
    panning: { enabled: true, eventTypes: ['leftMouseDown'] },
    mousewheel: { enabled: true, modifiers: [], minScale: 0.4, maxScale: 2.5 },
    highlighting: { magnetAvailable: { name: 'stroke', args: { attrs: { 'stroke-width': 3 } } } },
    connecting: {
      anchor: 'center',
      connectionPoint: { name: 'boundary', args: { sticky: true } },
      allowBlank: false, allowLoop: false, allowNode: false, allowPort: true, allowMulti: false,
      highlight: true, snap: { radius: 28 },
      connector: { name: 'smooth', args: { radius: 12 } },
      createEdge: () => new Shape.Edge({ attrs: { line: { stroke: KIND_COLOR.net, strokeWidth: 2, targetMarker: null } } }),
    },
  })
  graph.use(new Snapline({ sharp: true }))
  bindGraphEvents()
  dnd = new Dnd({ target: graph, scaled: false, animation: true })
}
function bindGraphEvents() {
  graph.on('node:click', ({ node }) => {
    selectedEdgeId.value = ''
    const d = node.getData() || {}
    selected.value = reactive({ id: node.id, kind: d.kind, ref: d.ref || '', name: d.name || '', note: d.note || '',
      pool: d.pool || '', vcpu: d.vcpu || 1, memory_mb: d.memory_mb || 1024, ssh_user: d.ssh_user || 'root', apps: d.apps || [], _sshSecret: d._sshSecret || '' })
    normalizeVMNode(selected.value)
    refreshEdges()
  })
  graph.on('blank:click', () => { selected.value = null; selectedEdgeId.value = ''; refreshEdges() })
  graph.on('edge:click', ({ edge }) => { selectedEdgeId.value = edge.id; selected.value = null })
  graph.on('edge:connected', refreshEdges)
  graph.on('edge:removed', refreshEdges)
  graph.on('node:removed', () => { selected.value = null; refreshEdges() })
  // Delete/Backspace 删除选中节点或连线（原生监听即可；X6 的 bindKey 在 keyboard
  // 插件里，核心没有）。输入框聚焦时不拦截
  document.addEventListener('keydown', onKeydown)
}
function onKeydown(e) {
  if (e.key !== 'Delete' && e.key !== 'Backspace') return
  const ae = document.activeElement
  if (ae && (ae.tagName === 'INPUT' || ae.tagName === 'TEXTAREA' || ae.isContentEditable)) return
  if (!graph) return
  if (selectedEdgeId.value) { e.preventDefault(); removeEdge(selectedEdgeId.value); return }
  if (selected.value) { e.preventDefault(); removeSelected() }
}
function refreshEdges() {
  if (!graph) { edgesOfSelected.value = []; return }
  edgesOfSelected.value = graph.getEdges().map((e) => ({
    id: e.id,
    from: e.getSourceCellId(),
    to: e.getTargetCellId(),
    text: nodeName(e.getSourceCellId()) + ' → ' + nodeName(e.getTargetCellId()),
  })).filter((l) => !selected.value || l.from === selected.value.id || l.to === selected.value.id)
}
const nodeName = (id) => (graph && graph.getCellById(id)?.getData()?.name) || id
function removeEdge(edgeId) {
  graph?.removeEdge(edgeId)
  if (selectedEdgeId.value === edgeId) selectedEdgeId.value = ''
  refreshEdges()
}

// ── 节点增删 ──
function startDrag(evt, kind, it) {
  const id = 'n' + seq++
  const n = { id, kind, ref: it.ref, name: it.label, x: 0, y: 0 }
  if (kind === 'vm') { n.vcpu = 2; n.memory_mb = 2048; n.ssh_user = 'root' }
  dnd.start(graph.createNode(buildNodeConfig(n)), evt)
}
function removeSelected() {
  if (!selected.value) return
  graph.getCellById(selected.value.id)?.remove()
  selected.value = null
}
// VM 节点字段兜底（旧计划/模板载入时补齐，与后端 provisionVM 缺省一致）
function normalizeVMNode(n) {
  if (n.kind !== 'vm') return n
  if (!n.vcpu) n.vcpu = 1
  if (!n.memory_mb) n.memory_mb = 1024
  if (!n.ssh_user) n.ssh_user = 'root'
  if (!n.apps) n.apps = []
  if (n._sshSecret === undefined) n._sshSecret = ''
  return n
}

// ── X6 画布 ↔ 计划 JSON 转换层 ──
// Dnd 拖放会给节点重新生成 UUID id，统一重映射成 n1..nK 可读 id（连线引用与
// apply 凭据键都按此换算，也让重复保存的计划 id 稳定）
function nodeIdMap() {
  const m = new Map()
  graph.getNodes().forEach((n, i) => m.set(n.id, 'n' + (i + 1)))
  return m
}
function graphToPlan() {
  const idMap = nodeIdMap()
  const nodes = graph.getNodes().map((n) => {
    const d = n.getData() || {}
    const p = n.getPosition()
    const out = { id: idMap.get(n.id), kind: d.kind, ref: d.ref || '', name: d.name || '', x: Math.round(p.x), y: Math.round(p.y), note: d.note || '' }
    // _sshSecret 刻意不进计划载荷：口令只随 apply 请求体走
    if (d.kind === 'vm') Object.assign(out, { pool: d.pool || '', vcpu: d.vcpu || 0, memory_mb: d.memory_mb || 0, ssh_user: d.ssh_user || '', apps: d.apps || [] })
    return out
  })
  const links = graph.getEdges().map((e) => ({ from: idMap.get(e.getSourceCellId()), to: idMap.get(e.getTargetCellId()) }))
  return { nodes, links }
}
function loadIntoGraph(pNodes, pLinks) {
  graph.removeCells([...graph.getNodes(), ...graph.getEdges()])
  selected.value = null
  selectedEdgeId.value = ''
  for (const n of pNodes) graph.addNode(buildNodeConfig(normalizeVMNode({ ...n })))
  for (const l of pLinks || []) {
    if (graph.getCellById(l.from) && graph.getCellById(l.to)) graph.addEdge({ source: { cell: l.from }, target: { cell: l.to }, attrs: { line: { stroke: KIND_COLOR.net, strokeWidth: 2, targetMarker: null } } })
  }
  graph.centerContent()
}
function clearCanvas() {
  loadIntoGraph([], [])
  seq = 1
  planName.value = ''
}
function loadTemplate(t) {
  planName.value = t.name
  loadIntoGraph(t.nodes.map((n) => ({ ...n })), (t.links || []).map((l) => ({ ...l })))
  seq = t.nodes.length + 1
}
function loadPlan(p) {
  planName.value = p.name
  loadIntoGraph(p.nodes.map((n) => ({ ...n })), (p.links || []).map((l) => ({ ...l })))
  seq = p.nodes.length + 1
}

function planPayload() {
  const { nodes, links } = graphToPlan()
  return {
    id: (planName.value || 'plan-' + Date.now()).trim().toLowerCase().replace(/[^a-z0-9_-]+/g, '-').slice(0, 60),
    name: planName.value || '未命名计划',
    nodes, links,
  }
}

async function savePlan() {
  const p = planPayload()
  await api.saveDesignerPlan(p)
  ElMessage.success('计划已保存：' + p.id)
  loadPlans()
}
async function removePlan(id) {
  try { await ElMessageBox.confirm(`删除计划「${id}」？`, '删除', { type: 'warning' }) } catch (e) { if (!isCancel(e)) return }
  await api.deleteDesignerPlan(id)
  loadPlans()
}
async function exportYaml() {
  const p = planPayload()
  await api.saveDesignerPlan(p)
  const yaml = await api.exportDesignerPlan(p.id)
  const blob = new Blob([yaml], { type: 'text/yaml;charset=utf-8' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = p.id + '-plan.yml'
  a.click()
  URL.revokeObjectURL(a.href)
}
function zoomFit() {
  graph?.zoomToFit({ padding: 40, maxScale: 1 })
}

async function applyPlan() {
  const { nodes, links: _links } = graphToPlan()
  if (!nodes.length) return ElMessage.warning('画布为空')
  const idMap = nodeIdMap()
  const vmDatas = graph.getNodes().map((n) => ({ gid: n.id, d: n.getData() || {} })).filter((x) => x.d.kind === 'vm')
  if (!cts.length && !vmDatas.length) return ElMessage.warning('计划中没有可落地节点（容器栈 / VM）')
  const noImg = vmDatas.filter((x) => !x.d.ref)
  if (noImg.length) return ElMessage.warning('VM「' + noImg.map((x) => x.d.name).join('、') + '」还未选择云镜像')
  const needCred = vmDatas.filter((x) => (x.d.apps || []).length)
  const noPass = needCred.filter((x) => !x.d._sshSecret)
  if (noPass.length) return ElMessage.warning('VM「' + noPass.map((x) => x.d.name).join('、') + '」配了应用安装，需要填 SSH 口令')
  const parts = []
  const cts = nodes.filter((n) => n.kind === 'container')
  if (cts.length) parts.push(`部署 ${cts.length} 个容器栈（${cts.map((n) => n.ref).join('、')}）`)
  if (vmDatas.length) parts.push(`创建并初始化 ${vmDatas.length} 台 VM（${vmDatas.map((x) => x.d.name).join('、')}）`)
  try {
    await ElMessageBox.confirm(`一键落地将顺序执行：${parts.join('；')}。镜像拉取与 VM 初始化可能需要数分钟。`, '落地确认', { type: 'info', confirmButtonText: '开始' })
  } catch (e) { if (!isCancel(e)) return }
  const p = planPayload()
  // 口令只在这次请求里带上（后端 overlay 到对应节点，不写盘）；键用重映射后的计划 id
  const credentials = {}
  for (const x of vmDatas) {
    if (x.d._sshSecret) credentials[idMap.get(x.gid)] = { ssh_user: x.d.ssh_user || 'root', ssh_secret: x.d._sshSecret }
  }
  applying.value = true
  applyStatus.value = { status: 'running', steps: ['已提交…'] }
  try {
    await api.saveDesignerPlan(p)
    await api.applyDesignerPlan(p.id, { credentials })
    applyTimer = setInterval(async () => {
      const res = await api.designerApplyStatus(p.id)
      applyStatus.value = res.data
      if (res.data.status !== 'running') {
        clearInterval(applyTimer); applyTimer = null
        applying.value = false
        if (res.data.status === 'success') ElMessage.success('架构落地完成')
      }
    }, 2000)
  } catch (e) {
    applying.value = false
    ElMessage.error(errMsg(e, '应用启动失败'))
  }
}

async function loadPlans() {
  const res = await api.listDesignerPlans()
  plans.value = (res.data && res.data.items) || []
}

onMounted(async () => {
  // vmOptions 一次带回云镜像+存储池（与创建向导同源）；apps 为应用安装目录
  const [t, s, opt, appsRes] = await Promise.all([api.designerTemplates(), api.listStacks(), api.vmOptions(), api.listApps()])
  templates.value = (t.data && t.data.items) || []
  stacks.value = (s.data && s.data.items) || []
  const d = opt.data || {}
  // ISO 是安装介质（无 cloud-init，落地链路拿不到 IP），设计器只列非 ISO 镜像
  cloudImages.value = (d.cloud_images || []).filter((i) => (i.format || '').toLowerCase() !== 'iso')
  storagePools.value = d.storage_pools || []
  apps.value = Array.isArray(appsRes.data) ? appsRes.data : []
  initGraph()
  await loadPlans()
})
onUnmounted(() => {
  document.removeEventListener('keydown', onKeydown)
  if (applyTimer) clearInterval(applyTimer)
  if (graph) { graph.dispose(); graph = null }
})
</script>

<style scoped>
.ds-layout {
  display: grid;
  grid-template-columns: 250px 1fr 260px;
  gap: 16px;
}
@media (max-width: 1100px) {
  .ds-layout { grid-template-columns: 1fr; }
}
.ds-h { font-weight: 600; font-size: 0.9rem; }
.ds-hrow { display: flex; align-items: baseline; justify-content: space-between; }
.ds-hint { font-weight: 400; font-size: 0.72rem; color: var(--color-muted-foreground); }
.ds-tpl {
  display: flex; flex-direction: column; gap: 2px;
  padding: 8px 10px; margin-bottom: 6px;
  border: 1px solid var(--color-border); border-radius: var(--radius-sm);
  cursor: pointer; transition: all 0.15s ease;
}
.ds-tpl:hover { border-color: var(--el-color-primary); transform: translateY(-1px); }
.ds-tpl-name { font-weight: 600; font-size: 0.88rem; }
.ds-tpl-desc { font-size: 0.78rem; color: var(--color-muted-foreground); }
.ds-plan {
  display: flex; align-items: center; justify-content: space-between;
  padding: 6px 10px; margin-bottom: 4px;
  border-radius: var(--radius-sm); cursor: pointer;
}
.ds-plan:hover { background: var(--el-fill-color-light); }
.ds-palette-title { font-size: 0.75rem; font-weight: 600; margin: 8px 0 5px; }
.ds-palette { display: grid; grid-template-columns: 1fr 1fr; gap: 5px; }
.ds-palette-item {
  padding: 5px 8px; font-size: 0.78rem;
  border: 1px solid var(--color-border); border-left: 3px solid;
  border-radius: var(--radius-sm); cursor: grab; user-select: none;
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  transition: all 0.15s ease; background: var(--el-bg-color);
}
.ds-palette-item:hover { border-color: var(--el-color-primary); box-shadow: var(--shadow-sm); transform: translateY(-1px); }
.ds-canvas {
  position: relative;
  height: 520px; border: 1px solid var(--color-border); border-radius: var(--radius-md);
  background: var(--el-bg-color); overflow: hidden;
}
.ds-canvas-inner { position: absolute; inset: 0; }
/* 节点边缘连接点：hover 节点时显现，拖出即连线 */
.ds-canvas :deep(.x6-port-body) { opacity: 0; }
.ds-canvas :deep(.x6-node:hover .x6-port-body) { opacity: 1; }
.ds-bar { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
.ds-tip { font-size: 0.72rem; color: var(--color-muted-foreground); }
.ds-apply {
  margin-top: 12px; padding: 10px 14px; border-radius: var(--radius-sm);
  background: var(--el-fill-color-light); font-size: 0.85rem;
}
.ds-apply.success { background: var(--status-running-bg); }
.ds-apply.failed { background: var(--status-error-bg); }
.ds-step { color: var(--color-muted-foreground); font-size: 0.78rem; line-height: 1.7; }
.ds-err { color: var(--color-danger); font-size: 0.8rem; margin-top: 4px; word-break: break-all; }
.ds-ref { font-size: 0.8rem; word-break: break-all; }
.ds-spec { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.ds-spec-unit { font-size: 0.78rem; color: var(--color-muted-foreground); }
.ds-opt-sub { float: right; font-size: 0.75rem; color: var(--color-muted-foreground); }
.ds-links { display: flex; flex-direction: column; gap: 2px; }
.ds-link-row {
  display: flex; align-items: center; justify-content: space-between;
  font-size: 0.8rem; padding: 4px 0; color: var(--color-muted-foreground);
}
.ds-link-empty { font-size: 0.75rem; color: var(--color-muted-foreground); padding: 4px 0; }
</style>
