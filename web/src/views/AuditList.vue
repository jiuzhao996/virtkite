<template>
  <div v-loading="loading">
    <PageHead title="审计中心" subtitle="操作日志记录谁、何时、对哪个对象做了什么、成功还是失败；控制台会话记录谁连过哪台虚拟机。均为只读记录，用于安全追溯" />

    <el-tabs v-model="activeTab">
      <!-- 操作日志仅管理员可见（后端 /api/audit admin-only）；viewer 只能看会话流水 -->
      <el-tab-pane v-if="isAdmin" label="操作日志" name="ops">
        <!-- 近 7 天操作量（包B）：独立于下方列表筛选恒看近 7 天；柱条点击联动日期筛选，再点同一天取消。
             全部为空（7 天零操作）时不渲染整块，不留一张空图占位 -->
        <el-card v-if="chartVisible" shadow="never" class="chart-card">
          <div class="chart-head">
            <span class="chart-title">近 7 天操作量</span>
            <span class="chart-hint">共 {{ chartTotal }} 次操作 · 点击柱条按该日筛选，再点一次取消</span>
          </div>
          <div ref="chartRef" class="ops-chart" />
        </el-card>

        <el-card shadow="never">
          <!-- 筛选条 → audit/components/AuditFilterBar.vue（q/range 直接改页面状态，动作 emit 回页面） -->
          <AuditFilterBar
            :q="q"
            v-model:range="range"
            :action-options="actionOptions"
            :object-options="objectOptions"
            :date-shortcuts="dateShortcuts"
            :loading="loading"
            :exporting="exporting"
            @search="search"
            @reset="reset"
            @refresh="refreshAll"
            @export="exportCsv"
            @range-change="onRange"
          />

          <!-- 操作日志表 → audit/components/AuditOpsTable.vue（整行/详情单元格点击统一 emit row-click） -->
          <AuditOpsTable :items="items" :action-map="actionMap" @row-click="onRowClick" />

          <el-pagination
            class="pager"
            layout="total, sizes, prev, pager, next"
            :total="total"
            :current-page="page"
            v-model:page-size="pageSize"
            :page-sizes="[20, 50, 100]"
            @current-change="onPage"
            @size-change="onPageSizeChange"
          />
        </el-card>
      </el-tab-pane>
      <!-- 会话 tab 的行点击钻取未做：SessionList.vue 是独立组件且不在本批次可改范围，
           其未对外抛出行点击事件；待该组件补 @row-click emit 后复用下方抽屉形态即可 -->
      <el-tab-pane label="控制台会话" name="sessions">
        <SessionList />
      </el-tab-pane>
    </el-tabs>

    <!-- 审计详情抽屉（派生逻辑随之下沉）→ audit/components/AuditDetailDrawer.vue -->
    <AuditDetailDrawer v-model="drawerOpen" :current="current" :action-map="actionMap" />
  </div>
</template>

<script setup>
// ============================================================================
// 审计中心（AuditList）
// ----------------------------------------------------------------------------
// 职责：两个 tab——「操作日志」（近 7 天操作量迷你图 + 可筛选的操作日志表 + 详情抽屉）
//       与「控制台会话」（独立组件 SessionList）。操作日志仅管理员可见（后端 /api/audit
//       admin-only）；viewer 只能看会话流水。均为只读记录，用于安全追溯。
//
// 数据流：
//   列表 ← usePagination(fetchAuditPage) → api.listAudit（筛选参数 q + 分页）
//   动作映射 ← api.auditActions（转数组供下拉；兜底 FALLBACK_ACTION_LABELS）
//   迷你图 ← loadChart（自行按 100/页翻近 7 天，聚合成本地日期柱状图，点柱联动日期筛选）
//
// 已拆出的子组件（web/src/views/audit/components/）：
//   AuditFilterBar.vue       筛选条（q/range 直连页面状态；search/reset/refresh/export 上抛）
//   AuditOpsTable.vue        操作日志表（行点击 emit row-click；动作着色纯函数搬入）
//   AuditDetailDrawer.vue    详情抽屉（变更 diff / 关联跳转 / 字段中文名等派生逻辑随之下沉）
//
// 刻意没拆的：近 7 天迷你图卡——chartRef 直接绑 useChart 句柄、点柱联动 q.start/end，
//   与页面筛选状态强耦合；翻页/筛选流转收在 usePagination，留在页面。
// ⚠️ 动作中文口径（actionMap）在三处使用：本页下拉、AuditOpsTable、AuditDetailDrawer
//   （后两者各自持一份 actionLabel）——改一处须同步。
// ============================================================================
import { ref, reactive, computed, watch, nextTick, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { api, TOKEN_KEY } from '../api'
import echarts from '../utils/echarts'
import { BarChart } from 'echarts/charts'
import { useChart } from '../composables/useChart'
import { useAuth } from '../store/auth'
import { FALLBACK_ACTION_LABELS, errMsg, cssVar } from '../utils/format'
import { usePagination } from '../composables/usePagination'
import SessionList from './SessionList.vue'
import PageHead from '../components/PageHead.vue'
import AuditFilterBar from './audit/components/AuditFilterBar.vue'
import AuditOpsTable from './audit/components/AuditOpsTable.vue'
import AuditDetailDrawer from './audit/components/AuditDetailDrawer.vue'

// utils/echarts.js 按需注册清单里没有 BarChart（该文件不在本批次可改范围）：
// 柱状图在本页局部补注册。echarts.use 幂等可叠加，且 useChart 持有的是同一模块单例，
// 注册全局生效；漏注册的后果是运行时静默白图（构建不报错），务必保留本行
echarts.use([BarChart])

const { isAdmin } = useAuth()
// 默认 tab：管理员落在操作日志，普通用户只有会话流水可看
const activeTab = ref(isAdmin.value ? 'ops' : 'sessions')

const items = ref([])
const range = ref(null)
const drawerOpen = ref(false)
const current = ref(null)

// DIFF_FIELD_LABELS / diffVal / changeRows / OBJECT_TYPE_LABELS / objectTypeLabel /
// parseDuration / actionLabel / actionTagType / relatedLink / goRelated 已随详情抽屉与
// 操作日志表下沉到 audit/components/ 下（本页不再用；utils 里 fmtDateTime 亦然）。

// 对象类型筛选下拉：与 determineObjectType 的真实产出对齐（原列表缺 docker/app/cron）
const objectOptions = [
  { value: 'vm', label: '虚拟机' },
  { value: 'host', label: '宿主机' },
  { value: 'image', label: '镜像' },
  { value: 'user', label: '用户' },
  { value: 'storage', label: '存储池' },
  { value: 'network', label: '网络' },
  { value: 'task', label: '任务' },
  { value: 'system', label: '系统' },
  { value: 'docker', label: 'Docker' },
  { value: 'app', label: '应用' },
  { value: 'cron', label: '计划任务' }
]

// 兜底映射（FALLBACK_ACTION_LABELS）与仪表盘共用，已收进 utils/format.js
const actionMap = ref({ ...FALLBACK_ACTION_LABELS })

const actionOptions = computed(() =>
  Object.keys(actionMap.value)
    .map((k) => ({ value: k, label: actionMap.value[k] || k }))
    .sort((a, b) => a.label.localeCompare(b.label, 'zh-CN'))
)

// actionLabel / actionTagType（AuditOpsTable、AuditDetailDrawer 各自持有）与 parseDuration
// （AuditDetailDrawer）已随渲染下沉——动作中文口径改一处须同步这三处。

const q = reactive({ action: '', object_type: '', username: '', status: '', start: '', end: '' })

// 日期范围快捷项（今天 / 近 7 天 / 近 30 天），返回 Date 由 value-format 统一转 YYYY-MM-DD
const dateShortcuts = [
  {
    text: '今天',
    value: () => {
      const d = new Date()
      return [d, d]
    }
  },
  {
    text: '近 7 天',
    value: () => {
      const end = new Date()
      const start = new Date()
      start.setDate(start.getDate() - 6)
      return [start, end]
    }
  },
  {
    text: '近 30 天',
    value: () => {
      const end = new Date()
      const start = new Date()
      start.setDate(start.getDate() - 29)
      return [start, end]
    }
  }
]

function onRange(val) {
  if (val && val.length === 2) {
    q.start = val[0]
    q.end = val[1]
  } else {
    q.start = ''
    q.end = ''
  }
  search()
}

// 分页状态与流转收进 usePagination：筛选变更/改页大回第 1 页、翻页保留筛选。
// search / onPage / onPageSizeChange / load 以解构别名保留原名，模板绑定零改动
const {
  page,
  pageSize,
  total,
  loading,
  handleCurrentChange: onPage,
  handleSizeChange: onPageSizeChange,
  reloadFromFirst: search,
  reload: load
} = usePagination(fetchAuditPage, { defaultPageSize: 20 })

// 单页获取：api 调用与响应解包留在页面内（composable 只管页码状态与流转）；
// 异常自行捕获提示（fetcher 契约），返回 total 由 composable 同步
async function fetchAuditPage({ page, pageSize }) {
  try {
    const res = await api.listAudit({ ...filterParams(), page, page_size: pageSize })
    items.value = (res.data && res.data.items) || []
    return (res.data && res.data.total) || 0
  } catch (e) {
    ElMessage.error(errMsg(e, '获取审计日志失败'))
  }
}

function reset() {
  Object.assign(q, { action: '', object_type: '', username: '', status: '', start: '', end: '' })
  range.value = null
  search()
}

// 刷新按钮：列表与迷你图一起刷新（翻页/筛选走 search，不重拉图——图恒看近 7 天不受筛选影响）
function refreshAll() {
  load()
  loadChart()
}

// 组装查询参数（不含分页，导出时也复用：按当前筛选条件拉全量）
function filterParams() {
  const params = {}
  for (const k of ['action', 'object_type', 'username', 'status', 'start', 'end']) {
    if (q[k]) params[k] = q[k]
  }
  return params
}

// ===== 导出 CSV（按当前筛选条件分页拉全量，上限 5000 条） =====
const exporting = ref(false)

// 导出改走后端流式（S1-3）：GET /api/audit/export 带当前筛选 + token，
// 后端 FindInBatches 逐千条 flush——万条级审计不再卡浏览器，也没有 5000 条上限。
// 下载仍走 blob（浏览器无法对 fetch 加 Authorization header，故取 blob 而非直链）。
async function exportCsv() {
  exporting.value = true
  try {
    const params = new URLSearchParams()
    const f = filterParams()
    for (const [k, v] of Object.entries(f)) {
      if (v !== undefined && v !== null && v !== '') params.set(k, v)
    }
    const resp = await fetch('/api/audit/export?' + params.toString(), {
      headers: { Authorization: 'Bearer ' + localStorage.getItem(TOKEN_KEY) }
    })
    if (!resp.ok) {
      let msg = '导出失败'
      try {
        const j = await resp.json()
        if (j && j.message) msg = j.message
      } catch { /* 非 JSON 响应用默认文案 */ }
      throw new Error(msg)
    }
    const blob = await resp.blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    const d = new Date()
    const p = (n) => String(n).padStart(2, '0')
    a.href = url
    a.download = `audit-${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}.csv`
    a.click()
    URL.revokeObjectURL(url)
    ElMessage.success('已按当前筛选导出（后端流式生成）')
  } catch (e) {
    ElMessage.error(e instanceof Error && e.message ? e.message : errMsg(e, '导出失败'))
  } finally {
    exporting.value = false
  }
}

// ===== 详情抽屉：行点击 / 详情单元格共用一个打开函数 =====
function openDetail(row) {
  current.value = row
  drawerOpen.value = true
}

// el-table row-click 首参即行数据，包一层免得 (row, column, event) 语义外泄
function onRowClick(row) {
  openDetail(row)
}

// relatedLink / goRelated（关联跳转）已随详情抽屉下沉到 AuditDetailDrawer.vue

// ===== 近 7 天操作量迷你图（包B） =====
// useChart 托管实例生命周期：init 惰性（首渲 setOption 才建）、ResizeObserver 自动跟随、
// unmount 自动 dispose；本页额外处理「区块 v-if 复活」：全空隐藏时显式 dispose，
// 下次有数据 ensureInit 会在新容器上重建实例（chartRef 已指向新元素）
const { chartRef, setOption, getChart, dispose: disposeChart } = useChart()

const chartVisible = ref(false)
const chartBuckets = ref([]) // [{ key: 'YYYY-MM-DD', label: 'MM-DD', count }]
const chartTotal = ref(0)
let clickBound = false // chart.on('click') 只绑一次；dispose 后随实例一起失效需重绑

// 迷你图配色走 CSS 变量（深浅色双模式同源）；cssVar 取当前主题真实色值，
// 主题热切换后色值不跟手——与 GuestMetricsCard 现行做法一致，刷新即恢复
const barColor = cssVar('--color-primary', '#2a9da5')
const barActiveColor = cssVar('--color-primary-dark-2', '#217d83')
const mutedColor = cssVar('--color-muted-foreground', '#475569')
const axisColor = cssVar('--color-border', '#e2e8f0')
const splitColor = cssVar('--color-muted', '#e9eff8')

// 本地日期键（聚合口径：created_at 按浏览器本地时区归日）
function localDateKey(d) {
  const p = (n) => String(n).padStart(2, '0')
  return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate())
}

// 当前被柱条点击选中的日期（联动现有筛选状态：start=end=该日才算选中，不另起状态源）
const selectedDayKey = computed(() => (q.start && q.start === q.end ? q.start : ''))

function buildChartOption() {
  const selected = selectedDayKey.value
  return {
    grid: { left: 8, right: 8, top: 14, bottom: 0, containLabel: true },
    tooltip: {
      trigger: 'axis',
      formatter: (params) => {
        const p = Array.isArray(params) ? params[0] : params
        const b = chartBuckets.value.find((x) => x.label === p.name)
        const day = b ? b.key : p.name
        return `${day}${selected === day ? '（已筛选）' : ''}：${p.value} 次操作`
      }
    },
    xAxis: {
      type: 'category',
      data: chartBuckets.value.map((b) => b.label),
      axisLine: { lineStyle: { color: axisColor } },
      axisTick: { show: false },
      axisLabel: { color: mutedColor }
    },
    yAxis: {
      type: 'value',
      minInterval: 1, // 计数不出现小数刻度
      axisLabel: { color: mutedColor },
      splitLine: { lineStyle: { color: splitColor } }
    },
    series: [
      {
        name: '操作量',
        type: 'bar',
        barMaxWidth: 26,
        data: chartBuckets.value.map((b) => ({
          value: b.count,
          // 选中日加深（品牌青 → 深青），未选中的回落到 series 级 itemStyle
          itemStyle: b.key === selected ? { color: barActiveColor } : undefined
        })),
        itemStyle: { color: barColor, borderRadius: [3, 3, 0, 0] },
        emphasis: { itemStyle: { color: barActiveColor } }
      }
    ]
  }
}

function renderChart() {
  setOption(buildChartOption())
  const c = getChart()
  if (c && !clickBound) {
    clickBound = true
    c.on('click', onBarClick)
  }
}

// 柱条点击 → 联动页面日期区间筛选（写回 q.start/q.end + range，复用 search 回第 1 页）；
// 再点同一天 = 清空按日筛选。选中态高亮由 selectedDayKey 的 watch 统一重渲
function onBarClick(params) {
  const b = chartBuckets.value.find((x) => x.label === params.name)
  if (!b) return
  if (selectedDayKey.value === b.key) {
    range.value = null
    q.start = ''
    q.end = ''
    search()
    ElMessage.success('已取消按日筛选')
  } else {
    range.value = [b.key, b.key]
    q.start = b.key
    q.end = b.key
    search()
    ElMessage.success(`已按 ${b.key} 筛选操作日志`)
  }
}

// 手动改日期选择器/重置也要同步柱条选中态（单一状态源 q，图只是它的投影）
watch(selectedDayKey, () => {
  if (chartVisible.value) setOption(buildChartOption())
})

// 拉近 7 天数据并聚合。注意：后端 ListAuditLogs 对 page_size 上限 100（>100 重置为 20），
// 故按 100/页翻页；start=7 天前让后端只回窗口内记录，10 页（1000 条）封顶防失控——
// 审计只记写操作与登录（GET 不入审计），7 天量级远达不到上限
async function loadChart() {
  try {
    // 预填 7 个桶（含今天，旧 → 新）
    const buckets = []
    for (let i = 6; i >= 0; i--) {
      const d = new Date()
      d.setDate(d.getDate() - i)
      const key = localDateKey(d)
      buckets.push({ key, label: key.slice(5), count: 0 })
    }
    const byKey = new Map(buckets.map((b) => [b.key, b]))

    const startKey = buckets[0].key
    let page = 1
    let got = 0
    let total = Infinity
    while (page <= 10 && got < total) {
      const res = await api.listAudit({ page, page_size: 100, start: startKey })
      const d = (res && res.data) || {}
      total = d.total || 0
      const list = d.items || []
      if (!list.length) break
      for (const it of list) {
        const b = byKey.get(localDateKey(new Date(it.created_at)))
        if (b) b.count++
      }
      got += list.length
      page++
    }

    chartBuckets.value = buckets
    chartTotal.value = buckets.reduce((s, b) => s + b.count, 0)
    if (chartTotal.value === 0) {
      // 7 天零操作：整块不渲染；已渲染过则销毁实例，等复活时在新容器重建
      chartVisible.value = false
      disposeChart()
      clickBound = false
      return
    }
    chartVisible.value = true
    await nextTick() // v-if 首次渲染后 chartRef 才有元素，ensureInit 依赖它
    renderChart()
  } catch (e) {
    // 迷你图属辅助信息：失败静默保留现状（已有图保持、无图不出块），不打扰主列表
  }
}

// 拉取后端操作类型→中文映射（{action: label}），转数组供下拉展示
async function loadActions() {
  try {
    const res = await api.auditActions()
    const map = (res && res.data) || {}
    if (map && typeof map === 'object' && Object.keys(map).length) {
      actionMap.value = { ...FALLBACK_ACTION_LABELS, ...map }
    }
  } catch (e) {
    // 接口失败时保留前端兜底映射
  }
}

onMounted(() => {
  // 操作日志接口仅管理员可用（/api/audit admin-only），viewer 不发起请求
  if (isAdmin.value) {
    load()
    loadActions()
    loadChart()
  }
})
</script>

<style scoped>
/* .page-head / .page-title / .page-desc 已收进 global.css；.mono 已随表格/抽屉下沉。
   .filters → AuditFilterBar.vue；.mono/.muted/.detail-cell/.clickable-table → AuditOpsTable.vue；
   .dt- 前缀 / .detail-sec / .detail-text / .audit-detail-popper → AuditDetailDrawer.vue */
.pager {
  margin-top: var(--space-xl);
  justify-content: flex-end;
}
/* ===== 近 7 天操作量迷你图（包B） ===== */
.chart-card {
  margin-bottom: var(--space-lg);
}
.chart-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-sm);
  margin-bottom: var(--space-sm);
}
.chart-title {
  font-size: 14px;
  font-weight: 600;
}
.chart-hint {
  font-size: 0.8rem;
  color: var(--color-muted-foreground);
}
.ops-chart {
  height: 120px;
  width: 100%;
}
/* ===== 行点击钻取 / 详情抽屉的样式 =====
   已随 AuditOpsTable.vue（.clickable-table）与 AuditDetailDrawer.vue（.dt-* / .detail-*）
   下沉，此处不再重复。 */
</style>
