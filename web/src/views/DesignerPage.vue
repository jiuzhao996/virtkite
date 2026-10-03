<template>
  <div>
    <PageHead title="架构设计" subtitle="选架构 → 摆节点 → 连线 → 一键落地（eNSP 式设计器 v1：容器栈自动部署，VM/网络节点标注导出）" />

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
          <el-input v-else v-model="addRef" size="small" placeholder="模板/网段建议" style="flex: 1" />
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
            <el-form-item :label="selected.kind === 'container' ? '引用栈' : '建议值'">
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
// 架构设计器 v1（P2B）：模板载入 + 节点/连线编辑 + ECharts 实时预览 +
// 计划保存(data/designer) + YAML 导出 + 容器栈一键落地（apply-status 轮询进度）。
// 画布交互刻意用「点选编辑」而非拖拽画布库——零新依赖，v2 可换 AntV X6。
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
  if (!addRef.value) return ElMessage.warning('请选择/填写引用')
  const id = 'n' + seq++
  nodes.push({ id, kind: addKind.value, ref: addRef.value, name: addRef.value, x: 160 + ((seq * 70) % 340), y: 140 + ((seq * 90) % 300) })
  renderChart()
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
  for (const n of t.nodes) nodes.push({ ...n })
  for (const l of t.links || []) links.push({ ...l })
  seq = t.nodes.length + 1
  renderChart()
}
function loadPlan(p) {
  clearAll()
  planName.value = p.name
  for (const n of p.nodes) nodes.push({ ...n })
  for (const l of p.links || []) links.push({ ...l })
  seq = p.nodes.length + 1
  renderChart()
}

async function savePlan() {
  const id = (planName.value || 'plan-' + Date.now()).trim().toLowerCase().replace(/[^a-z0-9_-]+/g, '-').slice(0, 60)
  await api.saveDesignerPlan({ id, name: planName.value || id, nodes: [...nodes], links: [...links] })
  ElMessage.success('计划已保存：' + id)
  loadPlans()
}
async function removePlan(id) {
  try { await ElMessageBox.confirm(`删除计划「${id}」？`, '删除', { type: 'warning' }) } catch (e) { if (!isCancel(e)) return }
  await api.deleteDesignerPlan(id)
  loadPlans()
}
async function exportYaml() {
  const id = (planName.value || 'plan').trim().toLowerCase().replace(/[^a-z0-9_-]+/g, '-').slice(0, 60)
  await api.saveDesignerPlan({ id, name: planName.value || id, nodes: [...nodes], links: [...links] })
  const yaml = await api.exportDesignerPlan(id)
  const blob = new Blob([yaml], { type: 'text/yaml;charset=utf-8' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = id + '-plan.yml'
  a.click()
  URL.revokeObjectURL(a.href)
}

async function applyPlan() {
  if (!nodes.length) return ElMessage.warning('画布为空')
  const containers = nodes.filter((n) => n.kind === 'container')
  if (!containers.length) return ElMessage.warning('计划中没有容器栈节点（VM/网络节点 v1 为标注性）')
  try {
    await ElMessageBox.confirm(`一键落地将顺序部署 ${containers.length} 个容器栈（${containers.map((n) => n.ref).join('、')}），镜像拉取可能需要数分钟。`, '落地确认', { type: 'info', confirmButtonText: '开始' })
  } catch (e) { if (!isCancel(e)) return }
  const id = (planName.value || 'plan').trim().toLowerCase().replace(/[^a-z0-9_-]+/g, '-').slice(0, 60)
  await api.saveDesignerPlan({ id, name: planName.value || id, nodes: [...nodes], links: [...links] })
  applying.value = true
  applyStatus.value = { status: 'running', steps: ['已提交…'] }
  try {
    await api.applyDesignerPlan(id)
    applyTimer = setInterval(async () => {
      const res = await api.designerApplyStatus(id)
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
  const [t, s] = await Promise.all([api.designerTemplates(), api.listStacks()])
  templates.value = (t.data && t.data.items) || []
  stacks.value = (s.data && s.data.items) || []
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
.ds-link-row {
  display: flex; align-items: center; justify-content: space-between;
  font-size: 0.8rem; padding: 4px 0; color: var(--color-muted-foreground);
}
</style>
