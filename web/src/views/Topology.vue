<template>
  <div>
    <!-- 独立页头仅独立路由形态展示；嵌入仪表盘 tab（IA 精简批次）时由 tab 承担标题 -->
    <PageHead v-if="!embedded" title="虚拟化拓扑" subtitle="宿主机 → 存储池/网络 → 虚拟机四层从属关系：池节点按容量、VM 节点按状态着色并叠加实时 CPU、告警 VM 红色高亮、克隆 VM 金色虚线边；拖拽整理布局，滚轮缩放，点击图例过滤，点击节点直达详情" />

    <el-card shadow="never">
      <!-- 原左分组为 gap 8px 不换行（组件默认档）；计数为 .toolbar 直接子元素走默认插槽 -->
      <Toolbar>
        <template #left>
          <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <span class="tp-hint">虚拟机 → 存储池/网络 → 宿主机</span>
        </template>
        <span class="count">
          虚拟机 {{ vms.length }} 台 · 存储池 {{ pools.length }} 个 · 网络 {{ networks.length }} 个<template
            v-if="alertVMCount"
          > · <span class="tp-alert">告警 {{ alertVMCount }} 台</span></template><span
            v-if="updatedAt"
          > · 更新于 {{ updatedAt }}</span>
        </span>
      </Toolbar>

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
import { ref, computed, watch, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { ElMessage } from 'element-plus'
import { useRouter } from 'vue-router'
import { Refresh } from '@element-plus/icons-vue'
import echarts from '../utils/echarts'
import PageHead from '../components/PageHead.vue'
import Toolbar from '../components/Toolbar.vue'
// utils/echarts 统一入口已注册 GraphChart（克隆家谱批次补的），本页直接用
import { api } from '../api'
import { errMsg, cssVar, vmStatusText, vmStatusHex, fmtSizeBytes, nowClock } from '../utils/format'

// embedded：嵌入仪表盘 tab 形态（隐藏独立页头）。
// activeTick：宿主编排的重激活信号——tab 切走再切回时容器从 display:none 恢复，
// echarts 不会自动重算尺寸，宿主每次数值 +1 触发一次 resize（首次挂载前的自增无人监听，无副作用）
const props = defineProps({
  embedded: { type: Boolean, default: false },
  activeTick: { type: Number, default: 0 }
})
watch(
  () => props.activeTick,
  () => {
    if (props.embedded && chart) nextTick(() => chart && chart.resize())
  }
)

const router = useRouter()
const vms = ref([])
const pools = ref([])
const networks = ref([])
const loading = ref(false)
const loadFailed = ref(false)
// 最近一次成功拉取的时刻（HH:MM:SS），主数据（VM/池）任一路失败则不更新
const updatedAt = ref('')
const chartRef = ref(null)
let chart = null

// ── 叠加层（operator+ 才拉得到，viewer 全部降级为空 = 只看静态三层）──
const perfMap = ref({})   // vmName → {cpu, mem}（dashboard/vm-perf 实时口径）
const alertMap = ref({})  // vmName → [{alertname, summary, severity}]（AM active 告警）
const cloneMap = ref({})  // vmName → {base, siblings}（volume-graph 克隆家族）

const alertVMCount = computed(() => Object.keys(alertMap.value).length)

const hasData = computed(() => vms.value.length > 0 || pools.value.length > 0)
const emptyText = computed(() =>
  loadFailed.value ? '拓扑数据加载失败，请点击重新加载' : '暂无存储池与虚拟机，先创建一台虚拟机再来看拓扑'
)

/* ---------- 节点分类与配色（echarts 不解析 var()，取真实色值） ---------- */
const COLOR_HOST = () => cssVar('--color-primary', '#2a9da5')
const COLOR_POOL = () => cssVar('--color-kite', '#7cc6cb')
const COLOR_NET = () => cssVar('--color-violet', '#7c3aed')
const COLOR_GOLD = () => cssVar('--color-gold', '#ffd268')
const COLOR_MUTED = () => cssVar('--color-muted-foreground', '#475569')

// 分类顺序即图例顺序（legend 点选即过滤）；告警中独立成类——红色在图上直接可过滤
const CATEGORIES = [
  { name: '宿主机' },
  { name: '存储池' },
  { name: '网络' },
  { name: '运行中' },
  { name: '已关机' },
  { name: '已暂停' },
  { name: '异常' },
  { name: '告警中' }
]

function vmCategory(status, vmName) {
  if (alertMap.value[vmName]) return 7 // 告警中（红色，覆盖状态色——故障优先可见）
  if (status === 'running') return 3
  if (status === 'paused') return 5
  if (status === 'error') return 6
  return 4 // shut off / stopped / 未知一律按已关机灰
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

/* ---------- 子网匹配：VM IP 前三段 vs 网关/CIDR 前三段（/24 口径，够用且无歧义） ---------- */
function prefix3(s) {
  const m = String(s || '').match(/^(\d+\.\d+\.\d+)\./)
  return m ? m[1] : ''
}

/* ---------- 克隆家族：从 volume-graph 反推（VM 挂载的卷在 backing 链上是子卷 → 克隆） ---------- */
function buildCloneInfo(gNodes, gEdges) {
  const parentOf = {}
  for (const e of gEdges) parentOf[e.child] = e.parent
  const nodeByPath = {}
  for (const n of gNodes) nodeByPath[n.path] = n
  const rootOf = (p) => {
    const seen = new Set()
    let cur = p
    while (parentOf[cur] && !seen.has(cur)) {
      seen.add(cur)
      cur = parentOf[cur]
    }
    return cur
  }
  const familyVMs = {}
  const vmInfo = {}
  // 挂在「有父盘的卷」上的 VM = 克隆体；root 相同的互为兄弟
  for (const n of gNodes) {
    if (!parentOf[n.path] || !n.vms || !n.vms.length) continue
    const root = rootOf(n.path)
    for (const vm of n.vms) {
      vmInfo[vm] = { base: nodeByPath[root] ? nodeByPath[root].name : root, root }
      ;(familyVMs[root] = familyVMs[root] || new Set()).add(vm)
    }
  }
  for (const vm of Object.keys(vmInfo)) {
    vmInfo[vm].siblings = (familyVMs[vmInfo[vm].root] ? familyVMs[vmInfo[vm].root].size : 1) - 1
  }
  return vmInfo
}

/* ---------- 组装 graph 数据（宿主 → 池/网络 → VM 主链，无池 VM 才直连宿主） ---------- */
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

  // 一级：网络（只画「有 VM 挂靠」的网络——空网络只进计数不进图，避免纯装饰节点）
  const netByPrefix = {}
  networks.value.forEach((n) => {
    const g = prefix3(n.gateway || n.cidr)
    if (g) netByPrefix[g] = n
  })
  const attachedNets = new Map() // 网络名 → 挂靠 VM 数
  vms.value.forEach((v) => {
    const net = netByPrefix[prefix3(v.ip)]
    if (net) attachedNets.set(net.name, (attachedNets.get(net.name) || 0) + 1)
  })
  networks.value.forEach((n) => {
    if (!attachedNets.has(n.name)) return
    const id = 'net:' + n.name
    nodes.push({
      id,
      name: n.name,
      category: 2,
      symbolSize: 34 + attachedNets.get(n.name) * 6,
      itemStyle: { color: COLOR_NET() },
      meta: { kind: 'network', network: n, attached: attachedNets.get(n.name) }
    })
    addLink('host', id)
  })

  // 二级：虚拟机（颜色按状态/告警、size 按内存）；所属池未登记时补幽灵池节点避免悬空边
  const vmSize = scaleSize(vms.value.map((v) => v.memory_mb), 26, 62)
  vms.value.forEach((v) => {
    const id = 'vm:' + v.id
    const isClone = !!cloneMap.value[v.name]
    const perf = perfMap.value[v.name]
    const node = {
      id,
      name: v.name,
      category: vmCategory(v.status, v.name),
      symbolSize: vmSize(v.memory_mb),
      itemStyle: { color: vmStatusHex(v.status) },
      meta: { kind: 'vm', vm: v }
    }
    if (v.status === 'running' && perf) {
      // 运行中叠实时 CPU/内存（第二行小字随节点标签渲染）
      node.perfLine = `CPU ${Math.round(perf.cpu)}% · MEM ${Math.round(perf.mem)}%`
    }
    if (isClone) {
      // 克隆家族：金色虚线描边（不连边——11 个兄弟连边会炸力导向布局）
      node.itemStyle.borderType = 'dashed'
      node.itemStyle.borderColor = COLOR_GOLD()
      node.itemStyle.borderWidth = 2.5
    }
    nodes.push(node)
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
      addLink(pid, id)
    } else {
      addLink('host', id)
    }
    const net = netByPrefix[prefix3(v.ip)]
    if (net && attachedNets.has(net.name)) addLink(id, 'net:' + net.name)
  })

  return { nodes, links }
}

/* ---------- tooltip：按节点类型展示详情（叠加实时/告警/克隆层） ---------- */
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
  if (meta.kind === 'network') {
    const n = meta.network || {}
    const fwdText = { nat: 'NAT', bridge: '桥接', isolated: '隔离' }[n.forward] || n.forward || '隔离/其它'
    return [
      '<b>' + escapeHtml(n.name) + '</b>',
      '类型：' + escapeHtml(fwdText) + (n.active ? '' : '（未激活）'),
      '网关：' + (n.gateway || '—'),
      'CIDR：' + (n.cidr || '—'),
      '挂靠虚拟机：' + meta.attached + ' 台'
    ].join('<br/>')
  }
  // vm：静态规格 + 实时性能 + 克隆家族 + 告警
  const v = meta.vm || {}
  const lines = [
    '<b>' + escapeHtml(v.name) + '</b>',
    '状态：' + vmStatusText(v.status, '未知'),
    'IP：' + (v.ip || '未获取'),
    'vCPU：' + (v.vcpu ?? '—') + ' 核',
    '内存：' + (v.memory_mb ? v.memory_mb + ' MB' : '—'),
    '存储池：' + (v.storage_pool || '—')
  ]
  const perf = perfMap.value[v.name]
  if (v.status === 'running' && perf) {
    lines.push('实时：CPU ' + Math.round(perf.cpu) + '% · 内存 ' + Math.round(perf.mem) + '%')
  }
  const clone = cloneMap.value[v.name]
  if (clone) {
    lines.push('<span style="color:#d97706">⟲ 克隆自 ' + escapeHtml(clone.base) + '（兄弟 ' + clone.siblings + ' 台）</span>')
  }
  const alerts = alertMap.value[v.name]
  if (alerts && alerts.length) {
    for (const a of alerts) {
      lines.push('<span style="color:#dc2626">⚠ ' + escapeHtml(a.alertname) + '：' + escapeHtml(a.summary || '') + '</span>')
    }
  }
  lines.push('<span style="color:#94a3b8">点击节点进入虚拟机详情</span>')
  return lines.join('<br/>')
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
            COLOR_NET(),
            cssVar('--color-success', '#16a34a'),
            cssVar('--color-info', '#64748b'),
            cssVar('--color-warning', '#d97706'),
            cssVar('--color-danger', '#dc2626'),
            cssVar('--color-danger', '#dc2626')
          ],
          force: { repulsion: 320, edgeLength: [80, 210], gravity: 0.08 },
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
            formatter: (p) => {
              const d = p.data || {}
              if (d.meta && d.meta.kind === 'host') return '{bold|鸢航 VirtKite}'
              // 运行中 VM 第二行叠实时 CPU/内存（节点本体太挤，放标签行）
              return d.perfLine ? `${p.name}\n{perf|${d.perfLine}}` : p.name
            },
            rich: {
              bold: { fontWeight: 700, fontSize: 13, color: cssVar('--color-foreground', '#1e293b') },
              perf: { fontSize: 10, color: cssVar('--color-muted-foreground', '#64748b') }
            }
          },
          lineStyle: { color: cssVar('--color-border-strong', '#cbd5e1'), width: 1.2, opacity: 0.9 },
          emphasis: { focus: 'adjacency', lineStyle: { width: 3 } },
          scaleLimit: { min: 0.4, max: 4 }
        }
      ]
    },
    true
  )

  // 节点点击直达：VM→详情、池→存储页、网络→网络页（告警/克隆动线的最后一跳）
  chart.off('click')
  chart.on('click', (p) => {
    const meta = p.data && p.data.meta
    if (!meta) return
    if (meta.kind === 'vm') router.push('/vms/' + meta.vm.id)
    else if (meta.kind === 'pool' && !meta.pool.unregistered) router.push('/storage')
    else if (meta.kind === 'network') router.push('/networks')
  })
}

const onResize = () => chart && chart.resize()

async function load() {
  loading.value = true
  loadFailed.value = false
  try {
    // 主数据 + 三个叠加层并行拉取；叠加层是 operator+ 端点（vm-perf/alerts/volume-graph），
    // viewer 403 属常态——allSettled 各自降级为空，viewer 看到纯静态三层拓扑
    const [vmsRes, poolsRes, netsRes, perfR, alertsR, graphR] = await Promise.allSettled([
      api.listVMs(),
      api.listStoragePools(),
      api.listNetworks(),
      api.vmPerf(),
      api.listAlerts(),
      api.volumeGraph()
    ])
    const itemsOf = (r) => (r.status === 'fulfilled' && r.value.data && r.value.data.items) || []
    vms.value = itemsOf(vmsRes)
    pools.value = itemsOf(poolsRes)
    networks.value = itemsOf(netsRes)

    // 实时性能：vmPerf 返回 [{name, cpu_percent, mem_pct}]（仅运行中 VM）
    if (perfR.status === 'fulfilled' && Array.isArray(perfR.value.data)) {
      const m = {}
      for (const row of perfR.value.data) {
        m[row.name] = { cpu: Number(row.cpu_percent) || 0, mem: Number(row.mem_pct) || 0 }
      }
      perfMap.value = m
    } else {
      perfMap.value = {}
    }

    // 告警叠加：AM active 告警按 labels.vm 归组（无 vm 标签的平台级告警不上图）
    if (alertsR.status === 'fulfilled' && Array.isArray(alertsR.value.data)) {
      const m = {}
      for (const a of alertsR.value.data) {
        if (!a.status || a.status.state !== 'active') continue
        const vmName = a.labels && a.labels.vm
        if (!vmName) continue
        ;(m[vmName] = m[vmName] || []).push({
          alertname: (a.labels && a.labels.alertname) || '未知告警',
          summary: (a.annotations && a.annotations.summary) || '',
          severity: (a.labels && a.labels.severity) || 'warning'
        })
      }
      alertMap.value = m
    } else {
      alertMap.value = {}
    }

    // 克隆家族：volume-graph 的卷级依赖图反推 VM 级克隆关系
    if (graphR.status === 'fulfilled' && graphR.value.data) {
      const d = graphR.value.data
      cloneMap.value = buildCloneInfo(d.nodes || [], d.edges || [])
    } else {
      cloneMap.value = {}
    }

    // 只有承载图的 VM/池两路失败才算页面级失败（网络/叠加层只影响局部）
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
/* .toolbar-left 骨架与 gap 由 Toolbar 组件承担 */
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
.tp-alert {
  color: var(--color-danger);
  font-weight: 600;
}
</style>
