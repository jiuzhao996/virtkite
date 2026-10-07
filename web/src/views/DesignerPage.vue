<template>
  <div>
    <PageHead title="架构设计" subtitle="拖拽编排 → 拉线连线 → 一键落地（容器栈 compose 部署 + VM 建机装应用，口令仅落地时填写不随计划保存）" />

    <div class="ds-layout">
      <!-- 左：设备栏（拖进画布，置顶——最高频入口沉底要滚才能拖，用户实测反馈）
           + 预置架构 + 已保存计划；桌面端面板自身内部滚动 -->
      <el-card shadow="never" class="ds-left">
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
        <span class="ds-h">预置架构</span>
        <div v-for="t in templates" :key="t.id" class="ds-tpl" @click="loadTemplate(t)">
          <span class="ds-tpl-name">{{ t.name }}</span>
          <span class="ds-tpl-desc">{{ t.desc }}</span>
        </div>
        <el-divider />
        <span class="ds-h">已保存计划</span>
        <div v-for="p in plans" :key="p.id" class="ds-plan" @click="loadPlan(p)">
          <span class="ds-tpl-name mono">{{ p.id }}</span>
          <el-button text size="small" type="primary" :icon="CopyDocument" title="复制计划" @click.stop="duplicatePlan(p)" />
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
            <el-button size="small" :icon="Promotion" :loading="exportingAnsible" @click="exportAnsible" title="生成 inventory + site.yml（site 直接进入 Playbook 库）">导出 Ansible</el-button>
            <el-button size="small" :icon="Aim" @click="zoomFit">适应画布</el-button>
            <el-button size="small" :icon="Grid" @click="autoLayout" title="按 网络→虚拟机→容器栈 分层重排">一键整理</el-button>
            <el-button size="small" :icon="Connection" :loading="liveLoading" @click="loadLiveStatus">刷新状态</el-button>
            <el-button size="small" :icon="Import" :loading="importing" @click="importFromReality">从现状导入</el-button>
            <el-button size="small" :icon="Warning" :loading="driftLoading" @click="checkDrift" title="对比最近落地快照与平台现实">漂移检查</el-button>
            <el-button type="primary" size="small" :icon="VideoPlay" :loading="applying" @click="applyPlan">一键落地</el-button>
            <span class="ds-tip">拖节点编排 · 边缘拉线连线 · 框选 · Ctrl+Z 撤销 · Delete 删除</span>
          </div>
        </template>
        <!-- 外层锁高（overflow:hidden 兜底），X6 用独立内层容器——autoResize 的
             SizeSensor 绑的是 X6 容器的父元素（=外层），若让 X6 直接用带 CSS 高度
             的元素，panning 后传感器会把撑大的高度内联回写、循环锁死（页面被拉到
             十几万 px，centerContent 失效＝点模板"没反应"） -->
        <div class="ds-canvas">
          <div ref="canvasRef" class="ds-canvas-inner"></div>
          <div ref="minimapRef" class="ds-minimap"></div>
        </div>
        <div v-if="driftResult" class="ds-drift" :class="driftResult.drifted ? 'warn' : 'ok'">
          <b>漂移检查</b>
          <span v-if="!driftResult.has_snap">尚无快照——成功落地一次后才有对比基准</span>
          <span v-else-if="!driftResult.drifted">与现实一致（{{ driftResult.checked_at ? '刚刚' : '' }}）</span>
          <span v-else>发现 {{ driftResult.items.length }} 处漂移</span>
          <div v-for="(it, i) in driftResult.items" :key="i" class="ds-drift-item mono">
            [{{ it.kind }}] {{ it.node }} — {{ it.detail }}
          </div>
          <div v-if="driftResult.drifted" class="ds-drift-actions">
            <el-button size="small" type="primary" @click="applyPlan">收敛回计划（重新落地）</el-button>
            <el-button size="small" @click="importFromReality">接纳实况（按现状重画）</el-button>
          </div>
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
              <el-form-item label="Playbook">
                <el-select v-model="selected.playbooks" multiple filterable placeholder="落地后自动执行（初始化/加固/优化）" style="width: 100%">
                  <el-option v-for="p in playbooks" :key="p.id" :label="p.id + (p.desc ? '（' + p.desc + '）' : '')" :value="p.id" />
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
        <template v-else-if="selectedEdge">
          <el-form label-width="64px" size="small">
            <el-form-item label="类型">
              <el-radio-group v-model="edgeKindModel" size="small">
                <el-radio-button v-for="(v, k) in EDGE_KINDS" :key="k" :value="k">{{ v.label }}</el-radio-button>
              </el-radio-group>
            </el-form-item>
            <el-form-item label="连接">
              <span class="mono ds-ref">{{ selectedEdge.text }}</span>
            </el-form-item>
          </el-form>
          <el-divider />
          <el-button text type="danger" size="small" :icon="Delete" @click="removeEdge(selectedEdgeId)">删除连线</el-button>
        </template>
        <el-empty v-else description="点击画布节点或连线编辑" :image-size="60" />
      </el-card>
    </div>

    <!-- 落地预览对话框（D3）：将创建 / 已存在 对比 -->
    <el-dialog v-model="diffVisible" title="落地预览" width="640px">
      <p class="ds-diff-tip">
        对照平台现状：<b class="ds-new">将创建</b> 的节点会执行落地；<b class="ds-old">已存在</b> 的按类型跳过（VM 同名会失败、网络同名跳过、栈重新 up）。
      </p>
      <el-table :data="diffRows" size="small" max-height="360">
        <template #empty><el-empty description="画布为空" :image-size="60" /></template>
        <el-table-column label="名称" prop="name" min-width="180" />
        <el-table-column label="类型" prop="kind" width="100" />
        <el-table-column label="动作" width="120">
          <template #default="{ row }">
            <el-tag :type="row.exists ? 'info' : 'success'" effect="light" size="small">
              {{ row.exists ? '已存在·跳过' : '将创建' }}
            </el-tag>
          </template>
        </el-table-column>
      </el-table>
      <template #footer>
        <el-button @click="diffVisible = false">取消</el-button>
        <el-button type="primary" :loading="applying" @click="confirmApply">确认落地</el-button>
      </template>
    </el-dialog>
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
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Delete, Download, VideoPlay, DocumentChecked, Aim } from '@element-plus/icons-vue'
import { api } from '../api'
import { errMsg, isCancel, cssVar } from '../utils/format'
import PageHead from '../components/PageHead.vue'
import { Graph, Shape } from '@antv/x6'
import { Snapline } from '@antv/x6-plugin-snapline'
import { Selection } from '@antv/x6-plugin-selection'
import { Keyboard } from '@antv/x6-plugin-keyboard'
import { History } from '@antv/x6-plugin-history'
import { Clipboard } from '@antv/x6-plugin-clipboard'
import { MiniMap } from '@antv/x6-plugin-minimap'
import { Dnd } from '@antv/x6-plugin-dnd'

const templates = ref([])
const libvirtNets = ref([])   // 平台真实网络（net 物料动态化）
async function loadLibvirtNets() {
  try {
    const res = await api.listNetworks()
    libvirtNets.value = (res.data && res.data.items) || []
  } catch {
    // 物料是增强入口，失败静默（仍有自定义网段占位）
  }
}
const router = useRouter()
const stacks = ref([])
const plans = ref([])
const cloudImages = ref([])
const storagePools = ref([])
const apps = ref([])
const playbooks = ref([])
const planName = ref('')
const selected = ref(null)
const minimapRef = ref(null)
const selectedEdgeId = ref('')
// 选中连线的展示模型（依赖 graphTick 以响应改型）
const selectedEdge = computed(() => {
  void graphTick.value
  if (!graph || !selectedEdgeId.value) return null
  const e = graph.getCellById(selectedEdgeId.value)
  if (!e || e.shape !== 'edge') return null
  const nm = (c) => {
    const d = c && c.getData()
    return (d && (d.name || d.ref)) || (c && c.id) || '?'
  }
  const d = e.getData() || {}
  return { id: e.id, kind: d.kind || 'net', text: nm(graph.getCellById(e.getSourceCellId())) + ' → ' + nm(graph.getCellById(e.getTargetCellId())) }
})
const edgeKindModel = computed({
  get: () => (selectedEdge.value && selectedEdge.value.kind) || 'net',
  set: (v) => { const e = graph && graph.getCellById(selectedEdgeId.value); if (e) setEdgeKind(e, v) }
})
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
  {
    kind: 'net', title: '网络', color: KIND_COLOR.net,
    // 物料来自平台真实 libvirt 网络（拖入即真实网络名，落地时同名跳过/缺省按默认 NAT 建）；
    // 网段占位项保留——纯设计态草稿也要能画
    items: [
      ...libvirtNets.value.map((n) => ({ ref: n.name, label: n.name + (n.gateway ? '' : '') })),
      { ref: '10.0.0.0/24', label: '＋自定义网段' },
    ],
  },
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
    Object.assign(data, { pool: n.pool || '', vcpu: n.vcpu || 0, memory_mb: n.memory_mb || 0, ssh_user: n.ssh_user || '', apps: n.apps ? [...n.apps] : [], playbooks: n.playbooks ? [...n.playbooks] : [], _sshSecret: '' })
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
  node.attr('sub/text', nodeSubText(d))
}

// 节点副标题：规格/引用 + （VM 已知运行态时）实时状态
function nodeSubText(d) {
  const base = d.kind === 'vm' ? (d.vcpu ? d.vcpu + 'C/' + d.memory_mb + 'MB' : '未选规格') : (d.ref || '')
  if (d.kind !== 'vm') return base
  const st = liveMap.value[d.name]
  return st ? base + ' · ' + (st === 'running' ? '运行中' : '已停止') : base
}

// ── 设计态↔运行态（D2）：按名称回填虚拟机运行状态，节点描边着色 ──
const liveMap = ref({})       // 虚拟机名 → 平台状态
const liveLoading = ref(false)
const importing = ref(false)
async function fetchLiveMap() {
  const res = await api.listVMs()
  const items = (res.data && res.data.items) || []
  const m = {}
  for (const vm of items) m[vm.name] = vm.status
  return m
}
// 把状态映射写到节点（silent：不进撤销栈，属只读回填）
function applyLiveStatus() {
  if (!graph) return
  const m = liveMap.value
  for (const node of graph.getNodes()) {
    const d = node.getData() || {}
    if (d.kind !== 'vm') continue
    const st = m[d.name]
    // 绿=运行、灰=已停止、橙=未找到同名虚拟机（设计态尚未落地）
    const color = st === 'running' ? '#3aa76d' : st ? '#c0c4cc' : '#e6a23c'
    node.attr('body/stroke', color, { silent: true })
    node.attr('sub/text', nodeSubText(d), { silent: true })
  }
}
async function loadLiveStatus() {
  liveLoading.value = true
  try {
    liveMap.value = await fetchLiveMap()
    applyLiveStatus()
    ElMessage.success('已回填运行状态')
  } catch (e) {
    ElMessage.error(errMsg(e, '获取虚拟机状态失败'))
  } finally {
    liveLoading.value = false
  }
}
// 从现状导入：扫描平台现有虚拟机生成设计草稿（教学「从现状改造」）
async function importFromReality() {
  importing.value = true
  try {
    const m = await fetchLiveMap()
    const names = Object.keys(m)
    if (!names.length) {
      ElMessage.warning('当前没有虚拟机可导入')
      return
    }
    const nodes = names.map((name, i) => ({
      id: 'n' + (i + 1), kind: 'vm', ref: '', name,
      x: 90 + (i % 4) * 190, y: 90 + Math.floor(i / 4) * 110,
      note: '导入自现状', pool: '', vcpu: 0, memory_mb: 0, ssh_user: 'root', apps: [], playbooks: []
    }))
    loadIntoGraph(nodes, [])
    seq = nodes.length + 1
    planName.value = '现状导入 ' + new Date().toLocaleDateString()
    liveMap.value = m
    applyLiveStatus()
    ElMessage.success(`已从现状导入 ${nodes.length} 台虚拟机`)
  } catch (e) {
    ElMessage.error(errMsg(e, '导入失败'))
  } finally {
    importing.value = false
  }
}
watch(selected, (v) => {
  if (!v || !graph) return
  const node = graph.getCellById(v.id)
  if (!node || !node.isNode()) return
  const data = { kind: v.kind, ref: v.ref, name: v.name, note: v.note }
  if (v.kind === 'vm') Object.assign(data, { pool: v.pool, vcpu: v.vcpu, memory_mb: v.memory_mb, ssh_user: v.ssh_user, apps: v.apps, playbooks: v.playbooks, _sshSecret: v._sshSecret })
  node.setData(data, { overwrite: true })
  syncNodeView(node, data)
  refreshEdges()
}, { deep: true })

// 连线语义（D1）：不同连线类型不同线型，一眼可辨接入/挂载/依赖
const EDGE_KINDS = {
  net: { label: '网络接入', stroke: '#2f7fe0', dash: '' },
  storage: { label: '存储挂载', stroke: '#8b8f96', dash: '6 4' },
  dep: { label: '启动依赖', stroke: '#8b8f96', dash: '2 3' },
}
function edgeLineAttrs(kind) {
  const k = EDGE_KINDS[kind] || EDGE_KINDS.net
  return {
    stroke: k.stroke,
    strokeWidth: 2,
    strokeDasharray: k.dash || undefined,
    targetMarker: kind === 'dep' ? { name: 'block', size: 6 } : null,
  }
}

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
      createEdge: () => new Shape.Edge({ attrs: { line: edgeLineAttrs('net') } }),
    },
  })
  graph.use(new Snapline({ sharp: true }))
  // D1：官方插件——框选多选、键盘快捷键、撤销重做、复制粘贴、缩略图
  graph.use(new Selection({ enabled: true, multiple: true, rubberband: true, movable: true, showNodeSelectionBox: true }))
  graph.use(new Clipboard({ enabled: true }))
  graph.use(new History({ enabled: true }))
  graph.use(new Keyboard({ enabled: true, global: true }))
  if (minimapRef.value) {
    graph.use(new MiniMap({ container: minimapRef.value, width: 180, height: 120, padding: 12 }))
  }
  bindKeys()
  bindGraphEvents()
  dnd = new Dnd({ target: graph, scaled: false, animation: true })
}
function bindGraphEvents() {
  graph.on('node:click', ({ node }) => {
    selectedEdgeId.value = ''
    const d = node.getData() || {}
    selected.value = reactive({ id: node.id, kind: d.kind, ref: d.ref || '', name: d.name || '', note: d.note || '',
      pool: d.pool || '', vcpu: d.vcpu || 1, memory_mb: d.memory_mb || 1024, ssh_user: d.ssh_user || 'root', apps: d.apps || [], playbooks: d.playbooks || [], _sshSecret: d._sshSecret || '' })
    normalizeVMNode(selected.value)
    refreshEdges()
  })
  graph.on('blank:click', () => { selected.value = null; selectedEdgeId.value = ''; refreshEdges() })
  graph.on('edge:click', ({ edge }) => { selectedEdgeId.value = edge.id; selected.value = null })
  graph.on('edge:connected', refreshEdges)
  graph.on('edge:removed', refreshEdges)
  graph.on('node:removed', () => { selected.value = null; refreshEdges() })
  // 双击节点直进对应管理页（设计态↔运行态闭环：D2）
  graph.on('node:dblclick', ({ node }) => {
    const d = node.getData() || {}
    if (d.kind === 'vm' && d.name) router.push({ path: '/vms', query: { keyword: d.name } })
    else if (d.kind === 'container') router.push({ path: '/containers', query: { tab: 'containers' } })
  })
}

// 输入框聚焦时不接管快捷键（否则在属性表单里打字会被 Delete 删节点）
function inInput() {
  const ae = document.activeElement
  return !!(ae && (ae.tagName === 'INPUT' || ae.tagName === 'TEXTAREA' || ae.isContentEditable))
}
// 快捷键：Delete 删除选中、Ctrl+Z/Y 撤销重做、Ctrl+A 全选、Ctrl+C/V 复制粘贴
function bindKeys() {
  const g = graph
  g.bindKey(['delete', 'backspace'], () => { if (inInput()) return true; deleteSelection(); return false })
  g.bindKey(['ctrl+z', 'meta+z'], () => { if (inInput()) return true; if (g.canUndo()) g.undo(); return false })
  g.bindKey(['ctrl+y', 'meta+y', 'ctrl+shift+z', 'meta+shift+z'], () => { if (inInput()) return true; if (g.canRedo()) g.redo(); return false })
  g.bindKey(['ctrl+a', 'meta+a'], () => { if (inInput()) return true; g.select(g.getNodes()); return false })
  g.bindKey(['ctrl+c', 'meta+c'], () => { if (inInput()) return true; const c = g.getSelectedCells(); if (c.length) g.copy(c); return false })
  g.bindKey(['ctrl+v', 'meta+v'], () => {
    if (inInput()) return true
    if (!g.isClipboardEmpty()) { const cells = g.paste({ offset: 32 }); g.cleanSelection(); g.select(cells) }
    return false
  })
}
function deleteSelection() {
  if (!graph) return
  const cells = graph.getSelectedCells()
  if (cells.length) { graph.removeCells(cells); selected.value = null; selectedEdgeId.value = ''; touchGraph(); return }
  if (selectedEdgeId.value) { removeEdge(selectedEdgeId.value); return }
  if (selected.value) removeSelected()
}
// graphTick：X6 图状态非响应式，选中连线/改线型后手动 +1 触发右栏重算
const graphTick = ref(0)
function touchGraph() { graphTick.value++ }
function setEdgeKind(edge, kind) {
  edge.setData({ ...(edge.getData() || {}), kind })
  edge.attr('line', edgeLineAttrs(kind))
  touchGraph()
}

function refreshEdges() {
  touchGraph()
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
  if (kind === 'vm') { n.vcpu = 2; n.memory_mb = 2048; n.ssh_user = 'root'; n.apps = []; n.playbooks = [] }
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
  if (!n.playbooks) n.playbooks = []
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
    if (d.kind === 'vm') Object.assign(out, { pool: d.pool || '', vcpu: d.vcpu || 0, memory_mb: d.memory_mb || 0, ssh_user: d.ssh_user || '', apps: d.apps || [], playbooks: d.playbooks || [] })
    return out
  })
  const links = graph.getEdges().map((e) => ({ from: idMap.get(e.getSourceCellId()), to: idMap.get(e.getTargetCellId()), kind: (e.getData() || {}).kind || 'net' }))
  return { nodes, links }
}
function loadIntoGraph(pNodes, pLinks) {
  graph.removeCells([...graph.getNodes(), ...graph.getEdges()])
  selected.value = null
  selectedEdgeId.value = ''
  for (const n of pNodes) graph.addNode(buildNodeConfig(normalizeVMNode({ ...n })))
  for (const l of pLinks || []) {
    if (graph.getCellById(l.from) && graph.getCellById(l.to)) {
      const kind = l.kind || 'net'
      graph.addEdge({ source: { cell: l.from }, target: { cell: l.to }, attrs: { line: edgeLineAttrs(kind) }, data: { kind } })
    }
  }
  touchGraph()
  if (graph.canUndo && graph.cleanHistory) graph.cleanHistory()
  applyLiveStatus()
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
// 复制计划为一个新计划（id 加时间后缀避免撞名）
async function duplicatePlan(p) {
  const name = (p.name || p.id) + ' 副本'
  const payload = {
    id: name.trim().toLowerCase().replace(/[^a-z0-9_-]+/g, '-').slice(0, 54) + '-' + Date.now().toString(36).slice(-4),
    name,
    nodes: (p.nodes || []).map((n) => ({ ...n })),
    links: (p.links || []).map((l) => ({ ...l }))
  }
  await api.saveDesignerPlan(payload)
  ElMessage.success('已复制为「' + name + '」')
  loadPlans()
}

async function removePlan(id) {
  try { await ElMessageBox.confirm(`删除计划「${id}」？`, '删除', { type: 'warning', confirmButtonClass: 'el-button--danger' }) } catch (e) { if (!isCancel(e)) return }
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
// ── 落地预览（D3）：节点 vs 平台现状 ──
const diffVisible = ref(false)
const diffRows = ref([])
const kindTextMap = { vm: '虚拟机', container: '容器栈', net: '网络' }
async function buildDiff() {
  const cells = graph.getNodes().map((n) => ({ cellId: n.id, d: n.getData() || {} }))
  const [vmRes, netRes, stackRes] = await Promise.all([
    api.listVMs(), api.listNetworks(), api.listStacks().catch(() => ({ data: {} }))
  ])
  const vmNames = new Set(((vmRes.data && vmRes.data.items) || []).map((v) => v.name))
  const netNames = new Set(((netRes.data && netRes.data.items) || []).map((n) => n.name))
  const deployedStacks = new Set(
    (((stackRes.data && stackRes.data.items) || []).filter((s) => s.deployed).map((s) => s.id))
  )
  return cells.map(({ cellId, d }) => ({
    cellId,
    name: d.name || d.ref || '未命名',
    kind: kindTextMap[d.kind] || d.kind,
    exists: d.kind === 'vm' ? vmNames.has(d.name)
      : d.kind === 'net' ? netNames.has(d.name)
        : d.kind === 'container' ? deployedStacks.has(d.ref)
          : false
  }))
}
// 画布上标色：将创建=绿、已存在=灰（silent 不进撤销栈）
function highlightDiff() {
  if (!graph) return
  for (const r of diffRows.value) {
    const node = graph.getCellById(r.cellId)
    if (node) node.attr('body/stroke', r.exists ? '#c0c4cc' : '#3aa76d', { silent: true })
  }
}

// 一键整理（AD2）：按 net → vm → container 分层网格重排（手写分层，不引布局库）。
// 只动位置不动数据/连线；同层每行 4 个，层间距留出连线走廊。
function autoLayout() {
  if (!graph) return
  const nodes = graph.getNodes()
  if (!nodes.length) return
  const layerOf = { net: 0, vm: 1, container: 2 }
  const sizeOf = { net: [92, 62], vm: [128, 46], container: [128, 46] }
  const byLayer = [[], [], []]
  for (const n of nodes) {
    const d = n.getData() || {}
    byLayer[layerOf[d.kind] ?? 1].push(n)
  }
  let y = 40
  for (const layer of byLayer) {
    if (!layer.length) continue
    const kind = (layer[0].getData() || {}).kind
    const [w, h] = sizeOf[kind] || [128, 46]
    const cols = Math.min(4, layer.length)
    layer.forEach((n, i) => {
      const col = i % cols
      const row = Math.floor(i / cols)
      n.position(60 + col * (w + 56), y + row * (h + 48))
    })
    y += Math.ceil(layer.length / cols) * (h + 48) + 72
  }
  graph.centerContent()
  touchGraph()
  ElMessage.success('已按 网络 → 虚拟机 → 容器栈 分层整理')
}

function zoomFit() {
  graph?.zoomToFit({ padding: 40, maxScale: 1 })
}

// 漂移检查（DE2）：对比最近落地快照与平台现实，缺资源的节点头部描橙
const driftLoading = ref(false)
const driftResult = ref(null)
async function checkDrift() {
  const p = planPayload()
  driftLoading.value = true
  try {
    await api.saveDesignerPlan(p)
    const res = await api.designerDrift(p.id)
    driftResult.value = res.data || {}
    applyDriftPaint()
  } catch (e) {
    ElMessage.error(errMsg(e, '漂移检查失败'))
  } finally {
    driftLoading.value = false
  }
}
// 橙色：快照里有、现实已无（画布节点描边）；extra 类现实有快照无——画布上不存在该节点，只进清单
function applyDriftPaint() {
  if (!graph) return
  const items = (driftResult.value && driftResult.value.items) || []
  const missing = new Set(items.filter((i) => i.kind === 'missing').map((i) => i.node))
  for (const node of graph.getNodes()) {
    const d = node.getData() || {}
    const name = d.kind === 'container' ? d.ref : d.name
    if (missing.has(name)) {
      node.attr('body/stroke', '#e6a23c', { silent: true })
    }
  }
}

// 画布 → Ansible（DE1）：后端生成 inventory/site.yml 并把 site 落进 playbook 库
const exportingAnsible = ref(false)
async function exportAnsible() {
  const p = planPayload()
  if (!p.nodes.length) return ElMessage.warning('画布为空')
  exportingAnsible.value = true
  try {
    await api.saveDesignerPlan(p)
    const res = await api.exportDesignerAnsible(p.id)
    const d = res.data || {}
    ElMessage.success(`已导出：${d.playbook_id} 已进入 Playbook 库，inventory 已存档`)
    // 弹窗展示生成物（可复制），并给「去执行」出口
    ElMessageBox.alert(
      `<pre class="ds-export-pre">${(d.inventory || '') + '\n──\n' + (d.site || '')}</pre>`,
      '生成的 Ansible 文件',
      { dangerouslyUseHTMLString: true, confirmButtonText: '去 Playbook 库', cancelButtonText: '关闭' }
    ).then(() => {
      router.push({ path: '/automation', query: { tab: 'playbooks' } })
    }).catch(() => {})
  } catch (e) {
    ElMessage.error(errMsg(e, '导出失败'))
  } finally {
    exportingAnsible.value = false
  }
}

async function applyPlan() {
  const { nodes } = graphToPlan()
  if (!nodes.length) return ElMessage.warning('画布为空')
  const vmDatas = graph.getNodes().map((n) => ({ gid: n.id, d: n.getData() || {} })).filter((x) => x.d.kind === 'vm')
  // cts 必须在引用前声明（此前写成先引用后声明，点「一键落地」直接 TDZ 崩溃）
  const cts = nodes.filter((n) => n.kind === 'container')
  if (!cts.length && !vmDatas.length) return ElMessage.warning('计划中没有可落地节点（容器栈 / VM）')
  const noImg = vmDatas.filter((x) => !x.d.ref)
  if (noImg.length) return ElMessage.warning('VM「' + noImg.map((x) => x.d.name).join('、') + '」还未选择云镜像')
  const needCred = vmDatas.filter((x) => (x.d.apps || []).length || (x.d.playbooks || []).length)
  const noPass = needCred.filter((x) => !x.d._sshSecret)
  if (noPass.length) return ElMessage.warning('VM「' + noPass.map((x) => x.d.name).join('、') + '」配了应用安装，需要填 SSH 口令')
  // 落地预览（D3）：比对平台现状标注「将创建/已存在」，确认后再执行
  try {
    diffRows.value = await buildDiff()
  } catch (e) {
    diffRows.value = [] // 现状拉取失败不阻断落地，预览表留空
  }
  highlightDiff()
  diffVisible.value = true
}

// 预览确认 → 真正落地
async function confirmApply() {
  diffVisible.value = false
  const idMap = nodeIdMap()
  const vmDatas = graph.getNodes().map((n) => ({ gid: n.id, d: n.getData() || {} })).filter((x) => x.d.kind === 'vm')
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
        if (res.data.status === 'success') {
          // 流程出口：架构落地完成给「查看结果」入口（此前成功只弹一句提示，无去向）
          try {
            await ElMessageBox.confirm('架构已落地为实际虚拟机，是否前往查看？', '落地完成', {
              type: 'success', confirmButtonText: '查看虚拟机', cancelButtonText: '留在本页'
            })
            router.push('/vms')
          } catch (e) {
            // 取消 = 留在本页
          }
        }
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
  const [t, s, opt, appsRes] = await Promise.all([api.designerTemplates(), api.listStacks(), api.vmOptions(), api.listApps(), loadLibvirtNets()])
  templates.value = (t.data && t.data.items) || []
  stacks.value = (s.data && s.data.items) || []
  const d = opt.data || {}
  // ISO 是安装介质（无 cloud-init，落地链路拿不到 IP），设计器只列非 ISO 镜像
  cloudImages.value = (d.cloud_images || []).filter((i) => (i.format || '').toLowerCase() !== 'iso')
  storagePools.value = d.storage_pools || []
  apps.value = Array.isArray(appsRes.data) ? appsRes.data : []
  const [t2] = await Promise.all([api.ansiblePlaybooks()])
  playbooks.value = (t2.data && t2.data.items) || []
  initGraph()
  await loadPlans()
})
onUnmounted(() => {
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
/* 工作台布局（>1100px）：整页不滚动——左右面板各自内部滚动、画布吃满剩余高度、
   工具栏常驻可视。此前整页随左卡（模板+设备栏+计划，约 820px）滚动：设备栏沉底
   要滚才见；滚轮悬在画布上又被 X6 缩放劫持，"滚动→拖拽→点工具栏"循环体感割裂
   （用户实测：设备在左下要往下划，滚完上面的保存/落地按钮点不了） */
@media (min-width: 1101px) {
  .ds-layout {
    /* 顶栏 60 + PageHead 区 86 + el-main 底垫 24 ≈ 170 */
    height: calc(100vh - 170px);
    min-height: 460px;
  }
  .ds-left,
  .ds-right {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
  .ds-left :deep(.el-card__body),
  .ds-right :deep(.el-card__body) {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    scrollbar-width: thin;
    scrollbar-color: var(--color-border) transparent;
  }
  .ds-mid {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
  .ds-mid :deep(.el-card__body) {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
  .ds-canvas {
    flex: 1;
    min-height: 0;
  }
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
  height: 520px; /* 窄屏堆叠布局兜底高；桌面端由上方 media 覆盖为 flex 撑满 */
  border: 1px solid var(--color-border); border-radius: var(--radius-md);
  background: var(--el-bg-color); overflow: hidden;
}
.ds-drift {
  margin-top: 12px;
  padding: 10px 12px;
  border-radius: 8px;
  font-size: 0.85rem;
  border: 1px solid var(--color-border);
}
.ds-drift.ok {
  background: #f0f9f2;
  border-color: #b7e0c4;
}
.ds-drift.warn {
  background: #fff8e6;
  border-color: #f0d9a0;
}
.ds-drift-item {
  margin-top: 4px;
  font-size: 0.78rem;
  color: var(--color-muted-foreground);
}
.ds-drift-actions {
  margin-top: 10px;
  display: flex;
  gap: 8px;
}
.ds-export-pre {
  max-height: 420px;
  overflow: auto;
  font-family: var(--font-mono);
  font-size: 0.78rem;
  line-height: 1.5;
  background: #0d1b2a;
  color: #cfe8ff;
  padding: 12px;
  border-radius: 8px;
  margin: 0;
}
.ds-diff-tip {
  margin: 0 0 12px;
  font-size: 0.85rem;
  color: var(--color-muted-foreground);
  line-height: 1.6;
}
.ds-diff-tip .ds-new {
  color: #3aa76d;
}
.ds-diff-tip .ds-old {
  color: #909399;
}
.ds-minimap {
  position: absolute;
  right: 12px;
  bottom: 12px;
  z-index: 5;
  border: 1px solid var(--color-border);
  border-radius: 6px;
  overflow: hidden;
  background: #fff;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.08);
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
