<template>
  <el-drawer
    :model-value="modelValue"
    title="全库克隆家谱与回收工作台"
    size="76%"
    @update:model-value="(v) => emit('update:modelValue', v)"
    @open="load"
  >
    <div v-loading="loading" class="lineage-body">
      <!-- 汇总条：虚拟 vs 实际双口径（增量克隆链收益一目了然） -->
      <div class="stat-strip" v-if="summary">
        <div class="stat">
          <span class="stat-num">{{ fmtGB(summary.total_virtual_gb) }}</span>
          <span class="stat-label">虚拟容量（读写上限）</span>
        </div>
        <div class="stat">
          <span class="stat-num">{{ fmtGB(summary.total_actual_gb) }}</span>
          <span class="stat-label">实际占用</span>
        </div>
        <div class="stat">
          <span class="stat-num highlight">{{ fmtGB(summary.savings_gb) }}</span>
          <span class="stat-label">链上节省（{{ summary.clone_count }} 个增量克隆）</span>
        </div>
        <div class="stat">
          <span class="stat-num" :class="{ warn: summary.orphans.length }">{{ fmtGB(summary.orphan_actual_gb) }}</span>
          <span class="stat-label">可回收（{{ summary.orphans.length }} 个孤儿卷）</span>
        </div>
      </div>

      <!-- 血缘图谱：模板/基盘 → 增量克隆链（跨池） → 挂载关系；点击节点看详情与级联影响 -->
      <div class="graph-title">血缘图谱<span class="graph-hint">跨池克隆链一图看全 · 金菱=模板 · 青圆=在用 · 灰圆=孤儿 · 点击节点看详情</span></div>
      <div v-show="nodes.length" ref="graphRef" class="lineage-graph" />
      <el-empty v-if="!loading && !nodes.length" description="全库暂无存储卷" :image-size="60" />

      <!-- 节点详情（点击图节点或表格行联动） -->
      <el-card v-if="selected" shadow="never" class="detail-card">
        <template #header>
          <div class="detail-head">
            <span class="mono">{{ selected.pool }} / {{ selected.name }}</span>
            <el-tag v-if="selected.is_template" type="warning" effect="plain" size="small">模板基盘</el-tag>
            <el-tag v-else-if="selected.children.length" type="primary" effect="plain" size="small">克隆父盘（{{ selected.children.length }} 子卷）</el-tag>
            <el-tag v-else-if="selected.phantom" type="danger" effect="plain" size="small">池外父盘</el-tag>
            <el-tag v-else-if="!selected.in_use" type="info" effect="plain" size="small">孤儿卷</el-tag>
          </div>
        </template>
        <el-descriptions :column="2" size="small">
          <el-descriptions-item label="路径"><span class="mono path-text">{{ selected.path }}</span></el-descriptions-item>
          <el-descriptions-item label="容量">
            {{ selected.phantom ? '（池外，容量未知）' : `${fmtGB(selected.capacity_gb)} 虚拟 / ${fmtGB(selected.allocation_gb)} 实际` }}
          </el-descriptions-item>
          <el-descriptions-item label="挂载虚拟机">{{ selected.vms.length ? selected.vms.join('、') : '—' }}</el-descriptions-item>
          <el-descriptions-item label="镜像库登记">{{ selected.image_names.length ? selected.image_names.join('、') : '—' }}</el-descriptions-item>
        </el-descriptions>
        <el-alert
          v-if="descendantVMs.length"
          type="warning"
          :closable="false"
          show-icon
          class="cascade-alert"
          :title="`级联影响：该卷是 ${descendantVMs.length} 台虚拟机磁盘链的祖先`"
          :description="`若该卷损坏或被删，以下虚拟机的磁盘将不可读：${descendantVMs.join('、')}（平台删卷守卫会拒绝此类删除）`"
        />
        <el-alert
          v-else-if="!selected.in_use && !selected.phantom"
          type="success"
          :closable="false"
          show-icon
          title="零引用：无虚拟机挂载、未登记镜像库、无克隆子卷——可安全回收"
        />
      </el-card>

      <!-- 回收工作台：零引用卷清单（跨池）+ 逐卷删除（删卷守卫兜底，409 会提示在用原因） -->
      <el-card v-if="summary && orphanRows.length" shadow="never" class="reclaim-card">
        <template #header>
          <div class="detail-head">
            <span>回收候选（零引用卷，共 {{ fmtGB(summary.orphan_actual_gb) }}）</span>
          </div>
        </template>
        <el-table :data="orphanRows" size="small" stripe>
          <el-table-column label="存储池" width="120">
            <template #default="{ row }">{{ row.pool }}</template>
          </el-table-column>
          <el-table-column label="卷名" min-width="200">
            <template #default="{ row }"><span class="mono">{{ row.name }}</span></template>
          </el-table-column>
          <el-table-column label="实际占用" width="110">
            <template #default="{ row }">{{ fmtGB(row.allocation_gb) }}</template>
          </el-table-column>
          <el-table-column label="虚拟容量" width="110">
            <template #default="{ row }">{{ fmtGB(row.capacity_gb) }}</template>
          </el-table-column>
          <el-table-column label="操作" width="130">
            <template #default="{ row }">
              <el-button text type="primary" size="small" @click="selected = row">详情</el-button>
              <el-button text type="danger" size="small" :loading="deleting === row.path" @click="removeVolume(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
        <p class="reclaim-note">
          删除前平台仍会过删卷守卫（挂载/镜像库/backing 父盘三重引用），引用竞争中出现的新引用会以 409 拒绝——宁可删不掉，不损在用磁盘。
        </p>
      </el-card>
    </div>
  </el-drawer>
</template>

<script setup>
// 全库克隆家谱与回收工作台（差异化功能，2026-10 批次 3）：数据与删卷守卫同源
// （/api/storage/volume-graph = 域磁盘挂载 + 镜像库登记 + 跨池 backing 依赖图，节点以路径为 id）。
// 「宁留垃圾文件、不损在用磁盘」的守卫立场不变——回收候选删除仍过 DeleteVolume 守卫。
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '../../../api'
import echarts from '../../../utils/echarts'
import { cssVar, errMsg, isCancel } from '../../../utils/format'

const props = defineProps({
  modelValue: { type: Boolean, default: false }
})
const emit = defineEmits(['update:modelValue'])

const loading = ref(false)
const nodes = ref([])
const edges = ref([])
const summary = ref(null)
const selected = ref(null)
const deleting = ref('')
const graphRef = ref(null)
let chart = null

const primaryColor = cssVar('--el-color-primary', '#2a9da5')
const goldColor = cssVar('--color-gold', '#ffd268')
const mutedColor = cssVar('--color-muted-foreground', '#64748b')

const orphanRows = computed(() =>
  summary.value ? nodes.value.filter((n) => summary.value.orphans.includes(n.pool + '/' + n.name)) : []
)

// 级联影响：沿 backing 边 BFS 收集全部后代卷上挂载的虚拟机（节点以路径连接，全库规模可忽略）
const descendantVMs = computed(() => {
  if (!selected.value) return []
  const childMap = {}
  for (const e of edges.value) (childMap[e.parent] = childMap[e.parent] || []).push(e.child)
  const seen = new Set([selected.value.path])
  const queue = [selected.value.path]
  const vms = new Set()
  while (queue.length) {
    const cur = queue.shift()
    for (const c of childMap[cur] || []) {
      if (seen.has(c)) continue
      seen.add(c)
      queue.push(c)
      const node = nodes.value.find((n) => n.path === c)
      for (const vm of node ? node.vms : []) vms.add(vm)
    }
  }
  return Array.from(vms).sort()
})

function fmtGB(v) {
  const n = Number(v) || 0
  if (n >= 100) return n.toFixed(0) + ' GB'
  if (n >= 1) return n.toFixed(1).replace(/\.0$/, '') + ' GB'
  return (n * 1024).toFixed(0) + ' MB'
}

async function load() {
  loading.value = true
  selected.value = null
  try {
    const res = await api.volumeGraph()
    const d = res.data || {}
    nodes.value = Array.isArray(d.nodes) ? d.nodes : []
    edges.value = Array.isArray(d.edges) ? d.edges : []
    summary.value = d.summary || null
    await nextTick()
    renderGraph()
  } finally {
    loading.value = false
  }
}

function nodeCategory(n) {
  if (n.is_template) return 0 // 模板基盘
  if (!n.in_use) return 2 // 孤儿
  return 1 // 在用
}

function renderGraph() {
  if (!graphRef.value || !nodes.value.length) return
  if (!chart) {
    chart = echarts.init(graphRef.value)
  }
  chart.resize()
  // 节点体量 = 实际占用（对数缩放，避免大基盘把孤儿卷挤成针尖）
  const maxSize = Math.max(...nodes.value.map((n) => n.allocation_gb), 1)
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
            const s = nodes.value.find((x) => x.path === p.data.source)
            const t = nodes.value.find((x) => x.path === p.data.target)
            return `${s ? s.pool + '/' + s.name : '?'} → ${t ? t.pool + '/' + t.name : '?'}（backing）`
          }
          const n = nodes.value.find((x) => x.path === p.data.id)
          if (!n) return p.name
          return [
            `<b>${n.pool} / ${n.name}</b>${n.is_template ? '（模板）' : ''}${n.phantom ? '（池外）' : ''}`,
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
          data: nodes.value.map((n) => ({
            id: n.path,
            name: n.name,
            category: nodeCategory(n),
            symbolSize: n.is_template || n.children.length ? 44 : 14 + 30 * Math.sqrt(n.allocation_gb / maxSize),
            itemStyle: n.in_use || n.phantom ? undefined : { opacity: 0.55 },
            label: { show: n.is_template || n.children.length > 0 || n.allocation_gb / maxSize > 0.08 },
          })),
          links: edges.value.map((e) => ({
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
  // 点击节点联动详情卡（空白处点击取消选中）
  chart.off('click')
  chart.on('click', (p) => {
    selected.value = p.dataType === 'node' ? nodes.value.find((n) => n.path === p.data.id) : null
  })
}

// 回收候选逐卷删除：确认 → DeleteVolume（后端仍过三重守卫）→ 刷新图与汇总
async function removeVolume(row) {
  try {
    await ElMessageBox.confirm(
      `确定删除「${row.pool} / ${row.name}」（实际占用 ${fmtGB(row.allocation_gb)}）？删除不可恢复。`,
      '删除卷',
      { type: 'warning', confirmButtonText: '删除', confirmButtonClass: 'el-button--danger' }
    )
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  deleting.value = row.path
  try {
    await api.deleteVolume(row.pool, row.name)
    ElMessage.success(`已删除 ${row.name}`)
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  } finally {
    deleting.value = ''
  }
}

watch(
  () => props.modelValue,
  (open) => {
    if (open) nextTick(() => chart && chart.resize())
  }
)

// 抽屉关闭即销毁实例：显式释放最稳
function disposeChart() {
  if (chart) {
    chart.dispose()
    chart = null
  }
}
defineExpose({ disposeChart })
onUnmounted(disposeChart)
</script>

<style scoped>
.lineage-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* 汇总条：四格指标（对齐仪表盘统计卡的口径与质感） */
.stat-strip {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}
@media (max-width: 900px) {
  .stat-strip {
    grid-template-columns: repeat(2, 1fr);
  }
}
.stat {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 12px 16px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-card);
}
.stat-num {
  font-size: 1.3rem;
  font-weight: 700;
  font-family: var(--font-display);
  color: var(--color-foreground);
}
.stat-num.highlight {
  color: var(--color-success);
}
.stat-num.warn {
  color: var(--color-warning);
}
.stat-label {
  font-size: 0.78rem;
  color: var(--color-muted-foreground);
}

.graph-title {
  font-weight: 600;
  color: var(--color-foreground);
}
.graph-hint {
  margin-left: 12px;
  font-weight: normal;
  font-size: 0.78rem;
  color: var(--color-muted-foreground);
}
.lineage-graph {
  height: 400px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
}

.detail-card .detail-head {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
  flex-wrap: wrap;
}
.path-text {
  font-size: 0.78rem;
  word-break: break-all;
}
.cascade-alert {
  margin-top: 12px;
}

.reclaim-card .detail-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-weight: 600;
}
.reclaim-note {
  margin: 12px 0 0;
  font-size: 0.8rem;
  color: var(--el-text-color-secondary);
  line-height: 1.6;
}
</style>
