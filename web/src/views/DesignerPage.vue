<template>
  <div>
    <PageHead title="架构设计" subtitle="选架构 → 摆节点 → 连线 → 一键落地（容器栈 compose 部署 + VM 建机装应用，口令仅落地时填写不随计划保存）" />

    <div class="ds-layout">
      <!-- 左：模板库 + 节点面板 -->
      <el-card shadow="never" class="ds-left">
        <template #header><span class="ds-h">预置架构</span></template>
        <div v-for="t in templates" :key="t.id" class="ds-tpl" @click="loadTemplate(t)">
          <span class="ds-tpl-name">{{ t.name }}</span>
          <span class="ds-tpl-desc">{{ t.desc }}</span>
        </div>
        <el-divider />
        <span class="ds-h">添加节点</span>
        <div class="ds-add">
          <el-select v-model="addKind" size="small" style="width: 92px">
            <el-option label="容器栈" value="container" />
            <el-option label="VM 角色" value="vm" />
            <el-option label="网络" value="net" />
          </el-select>
          <el-select v-if="addKind === 'container'" v-model="addRef" size="small" filterable placeholder="选栈" style="flex: 1">
            <el-option v-for="s in stacks" :key="s.id" :label="s.id" :value="s.id" />
          </el-select>
          <el-select v-else-if="addKind === 'vm'" v-model="addRef" size="small" filterable placeholder="选云镜像" style="flex: 1">
            <el-option v-for="img in cloudImages" :key="img.id" :label="img.name" :value="String(img.id)" />
          </el-select>
          <el-input v-else v-model="addRef" size="small" placeholder="网段建议" style="flex: 1" />
          <el-button type="primary" size="small" :icon="Plus" @click="addNode" />
        </div>
        <el-divider />
        <span class="ds-h">已保存计划</span>
        <div v-for="p in plans" :key="p.id" class="ds-plan" @click="loadPlan(p)">
          <span class="ds-tpl-name mono">{{ p.id }}</span>
          <el-button text size="small" type="danger" :icon="Delete" @click.stop="removePlan(p.id)" />
        </div>
      </el-card>

      <!-- 中：画布（ECharts graph 实时预览，点选节点） -->
      <el-card shadow="never" class="ds-mid">
        <template #header>
          <div class="ds-bar">
            <el-input v-model="planName" size="small" placeholder="计划名（保存用）" style="width: 180px" />
            <el-button size="small" :icon="DocumentChecked" @click="savePlan">保存</el-button>
            <el-button size="small" :icon="Download" @click="exportYaml">导出 YAML</el-button>
            <el-button type="primary" size="small" :icon="VideoPlay" :loading="applying" @click="applyPlan">一键落地</el-button>
          </div>
        </template>
        <div ref="chartRef" class="ds-chart" />
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
            <el-form-item label="名称"><el-input v-model="selected.name" @change="renderChart" /></el-form-item>
            <el-form-item label="类型"><el-tag size="small" effect="plain">{{ kindLabel[selected.kind] }}</el-tag></el-form-item>
            <!-- VM 节点落地参数（v2）：这些字段随计划保存，口令除外 -->
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
            <el-form-item v-else :label="selected.kind === 'container' ? '引用栈' : '建议值'">
              <span class="mono ds-ref">{{ selected.ref }}</span>
            </el-form-item>
            <el-form-item label="备注"><el-input v-model="selected.note" type="textarea" :rows="2" /></el-form-item>
          </el-form>
          <el-divider>连线</el-divider>
          <div class="ds-links">
            <div v-for="(l, i) in linksOf(selected.id)" :key="i" class="ds-link-row">
              <span class="mono">{{ nodeName(l.from) }} → {{ nodeName(l.to) }}</span>
              <el-button text size="small" type="danger" :icon="Delete" @click="links.splice(links.indexOf(l), 1); renderChart()" />
            </div>
            <div class="ds-add">
              <el-select v-model="linkTo" size="small" placeholder="连接到…" style="flex: 1">
                <el-option v-for="n in nodes.filter((x) => x.id !== selected.id)" :key="n.id" :label="n.name" :value="n.id" />
              </el-select>
              <el-button size="small" :icon="Plus" @click="addLink" />
            </div>
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
// 架构设计器（P2B v2）：模板载入 + 节点/连线编辑 + ECharts 实时预览 +
// 计划保存(data/designer) + YAML 导出 + 一键落地（容器栈 compose up；VM 节点
// 建机→等 IP→装应用，进度 apply-status 轮询）。SSH 口令只存在内存（_sshSecret
// 下划线字段），保存/导出经 planPayload 剥离，落地时随 apply 请求体一次性携带。
// 画布交互刻意用「点选编辑」而非拖拽画布库——零新依赖，v3 可换 AntV X6。
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Delete, Download, VideoPlay, DocumentChecked } from '@element-plus/icons-vue'
import { api } from '../api'
import { errMsg, isCancel, cssVar } from '../utils/format'
import PageHead from '../components/PageHead.vue'
import echarts from '../utils/echarts'
import { GraphChart } from 'echarts/charts'
echarts.use([GraphChart])

const templates = ref([])
const stacks = ref([])
const plans = ref([])
const cloudImages = ref([])
const storagePools = ref([])
const apps = ref([])
const planName = ref('')
const nodes = reactive([])
const links = reactive([])
const selected = ref(null)
const addKind = ref('container')
const addRef = ref('')
const linkTo = ref('')
const applying = ref(false)
const applyStatus = ref(null)
const chartRef = ref(null)
let chart = null
let applyTimer = null
let seq = 1

const kindLabel = { container: '容器栈', vm: 'VM 角色', net: '网络' }
const KIND_COLOR = {
  container: cssVar('--el-color-primary', '#2a9da5'),
  vm: cssVar('--color-success', '#16a34a'),
  net: cssVar('--color-violet', '#7c3aed'),
}
const KIND_SHAPE = { container: 'circle', vm: 'rect', net: 'diamond' }

const nodeName = (id) => nodes.find((n) => n.id === id)?.name || id
const applyStatusText = computed(() => ({ running: '应用中…', success: '✓ 应用完成', failed: '✗ 应用失败' }[applyStatus.value?.status] || applyStatus.value?.status || ''))
const linksOf = (id) => links.filter((l) => l.from === id || l.to === id)

function renderChart() {
  if (!chartRef.value) return
  if (!chart) chart = echarts.init(chartRef.value)
  chart.setOption({
    animationDurationUpdate: 300,
    tooltip: {
      trigger: 'item',
      formatter: (p) =>
        p.dataType === 'node'
          ? '<b>' + p.data.name + '</b><br/>' + (kindLabel[p.data.kind] || '') + ' · ' + p.data.ref
          : nodeName(p.data.source) + ' → ' + nodeName(p.data.target)
    },
    series: [{
      type: 'graph', layout: 'none', roam: true,
      data: nodes.map((n) => ({
        id: n.id, name: n.name, kind: n.kind, ref: n.ref,
        x: n.x, y: n.y,
        symbol: KIND_SHAPE[n.kind] || 'circle',
        symbolSize: n.kind === 'container' ? [110, 44] : 56,
        itemStyle: { color: KIND_COLOR[n.kind], borderRadius: n.kind === 'container' ? 8 : 4 },
        label: { show: true, color: '#fff', fontSize: 12, formatter: (p) => p.data.name.length > 8 ? p.data.name.slice(0, 7) + '…' : p.data.name },
      })),
      links: links.map((l) => ({ source: l.from, target: l.to, lineStyle: { color: KIND_COLOR.net, width: 2, curveness: 0.1 } })),
      emphasis: { focus: 'adjacency' },
    }],
  }, true)
  chart.off('click')
  chart.on('click', (p) => { selected.value = p.dataType === 'node' ? nodes.find((n) => n.id === p.data.id) || null : null })
}

function addNode() {
  if (!addRef.value) {
    return ElMessage.warning(addKind.value === 'vm' ? '请选择云镜像' : '请选择/填写引用')
  }
  const id = 'n' + seq++
  const n = { id, kind: addKind.value, ref: addRef.value, name: addRef.value, x: 160 + ((seq * 70) % 340), y: 140 + ((seq * 90) % 300) }
  if (addKind.value === 'vm') {
    // 新 VM 节点名默认取镜像名（可改）；规格/SSH 给可用初值，口令只进内存字段
    n.name = cloudImages.value.find((i) => String(i.id) === addRef.value)?.name || 'vm-' + seq
    n.vcpu = 2
    n.memory_mb = 2048
    n.ssh_user = 'root'
    n.pool = ''
    n.apps = []
    n._sshSecret = ''
  }
  nodes.push(n)
  renderChart()
}

// VM 节点字段兜底：旧计划/模板载入时补齐（与后端 provisionVM 的缺省一致）
function normalizeVMNode(n) {
  if (n.kind !== 'vm') return n
  if (!n.vcpu) n.vcpu = 1
  if (!n.memory_mb) n.memory_mb = 1024
  if (!n.ssh_user) n.ssh_user = 'root'
  if (!n.apps) n.apps = []
  if (n._sshSecret === undefined) n._sshSecret = ''
  return n
}
function addLink() {
  if (!selected.value || !linkTo.value) return
  links.push({ from: selected.value.id, to: linkTo.value })
  linkTo.value = ''
  renderChart()
}
function removeSelected() {
  const i = nodes.indexOf(selected.value)
  if (i > -1) nodes.splice(i, 1)
  for (let j = links.length - 1; j >= 0; j--) if (links[j].from === selected.value.id || links[j].to === selected.value.id) links.splice(j, 1)
  selected.value = null
  renderChart()
}

function clearAll() { nodes.splice(0); links.splice(0); selected.value = null; seq = 1 }
function loadTemplate(t) {
  clearAll()
  planName.value = t.name
  for (const n of t.nodes) nodes.push(normalizeVMNode({ ...n }))
  for (const l of t.links || []) links.push({ ...l })
  seq = t.nodes.length + 1
  renderChart()
}
function loadPlan(p) {
  clearAll()
  planName.value = p.name
  for (const n of p.nodes) nodes.push(normalizeVMNode({ ...n }))
  for (const l of p.links || []) links.push({ ...l })
  seq = p.nodes.length + 1
  renderChart()
}

// 保存/导出/落地共用的计划载荷：剥离 _sshSecret（口令只随 apply 请求体走）
function planPayload() {
  return {
    id: (planName.value || 'plan-' + Date.now()).trim().toLowerCase().replace(/[^a-z0-9_-]+/g, '-').slice(0, 60),
    name: planName.value || '未命名计划',
    nodes: nodes.map((n) => { const { _sshSecret, ...rest } = n; return rest }),
    links: [...links],
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

async function applyPlan() {
  if (!nodes.length) return ElMessage.warning('画布为空')
  // 可落地节点 = 容器栈 + VM（v2）；net 仍为标注性
  const cts = nodes.filter((n) => n.kind === 'container')
  const vms = nodes.filter((n) => n.kind === 'vm')
  if (!cts.length && !vms.length) return ElMessage.warning('计划中没有可落地节点（容器栈 / VM）')
  const noImg = vms.filter((n) => !n.ref)
  if (noImg.length) return ElMessage.warning('VM「' + noImg.map((n) => n.name).join('、') + '」还未选择云镜像')
  const needCred = vms.filter((n) => (n.apps || []).length)
  const noPass = needCred.filter((n) => !n._sshSecret)
  if (noPass.length) return ElMessage.warning('VM「' + noPass.map((n) => n.name).join('、') + '」配了应用安装，需要填 SSH 口令')
  const parts = []
  if (cts.length) parts.push(`部署 ${cts.length} 个容器栈（${cts.map((n) => n.ref).join('、')}）`)
  if (vms.length) parts.push(`创建并初始化 ${vms.length} 台 VM（${vms.map((n) => n.name).join('、')}）`)
  try {
    await ElMessageBox.confirm(`一键落地将顺序执行：${parts.join('；')}。镜像拉取与 VM 初始化可能需要数分钟。`, '落地确认', { type: 'info', confirmButtonText: '开始' })
  } catch (e) { if (!isCancel(e)) return }
  const p = planPayload()
  // 口令只在这次请求里带上（后端 overlay 到对应节点，不写盘）
  const credentials = {}
  for (const n of vms) {
    if (n._sshSecret) credentials[n.id] = { ssh_user: n.ssh_user || 'root', ssh_secret: n._sshSecret }
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
  await loadPlans()
  renderChart()
  window.addEventListener('resize', onResize)
})
const onResize = () => chart && chart.resize()
onUnmounted(() => {
  window.removeEventListener('resize', onResize)
  if (applyTimer) clearInterval(applyTimer)
  if (chart) chart.dispose()
})
</script>

<style scoped>
.ds-layout {
  display: grid;
  grid-template-columns: 240px 1fr 260px;
  gap: 16px;
}
@media (max-width: 1100px) {
  .ds-layout { grid-template-columns: 1fr; }
}
.ds-h { font-weight: 600; font-size: 0.9rem; }
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
.ds-add { display: flex; gap: 6px; margin-top: 8px; align-items: center; }
.ds-chart { height: 520px; border: 1px dashed var(--color-border); border-radius: var(--radius-md); }
.ds-bar { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
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
.ds-link-row {
  display: flex; align-items: center; justify-content: space-between;
  font-size: 0.8rem; padding: 4px 0; color: var(--color-muted-foreground);
}
</style>
