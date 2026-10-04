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
          <div class="filters">
            <el-select v-model="q.action" placeholder="操作类型" clearable filterable style="width: 180px" @change="search">
              <el-option v-for="a in actionOptions" :key="a.value" :label="a.label" :value="a.value" />
            </el-select>
            <el-select v-model="q.object_type" placeholder="对象类型" clearable style="width: 130px" @change="search">
              <el-option v-for="o in objectOptions" :key="o.value" :label="o.label" :value="o.value" />
            </el-select>
            <el-input v-model="q.username" placeholder="操作人" clearable style="width: 140px" @keyup.enter="search" @clear="search" />
            <el-select v-model="q.status" placeholder="状态" clearable style="width: 110px" @change="search">
              <el-option label="成功" value="success" />
              <el-option label="失败" value="failed" />
            </el-select>
            <el-date-picker
              v-model="range"
              type="daterange"
              range-separator="至"
              start-placeholder="开始日期"
              end-placeholder="结束日期"
              value-format="YYYY-MM-DD"
              :shortcuts="dateShortcuts"
              @change="onRange"
            />
            <el-button type="primary" :icon="Search" :loading="loading" @click="search">查询</el-button>
            <el-button :icon="RefreshLeft" @click="reset">重置</el-button>
            <el-button :icon="Refresh" :loading="loading" @click="refreshAll">刷新</el-button>
            <el-button :icon="Download" :loading="exporting" @click="exportCsv">导出</el-button>
          </div>

          <!-- 行点击钻取：整行可点开详情抽屉（与「详情」按钮同入口）；clickable-table 只作用于本表，
               不波及「控制台会话」tab（SessionList 为独立组件，行内另有虚拟机链接与断开按钮） -->
          <el-table :data="items" stripe border style="width: 100%" class="clickable-table" @row-click="onRowClick">
            <template #empty><el-empty description="暂无审计记录" :image-size="72" /></template>
            <el-table-column label="时间" min-width="172">
              <template #default="{ row }">
                <span class="mono">{{ fmtDateTime(row.created_at) }}</span>
              </template>
            </el-table-column>
            <el-table-column label="操作人" width="120">
              <template #default="{ row }">
                <span>{{ row.username || '—' }}</span>
              </template>
            </el-table-column>
            <el-table-column label="操作" width="160">
              <template #default="{ row }">
                <el-tag :type="actionTagType(row.action)" effect="plain">{{ actionLabel(row.action) }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="object_type" label="对象" width="100" />
            <el-table-column label="来源 IP" width="150">
              <template #default="{ row }">
                <span class="mono">{{ row.source_ip || '—' }}</span>
              </template>
            </el-table-column>
            <el-table-column label="状态" width="90">
              <template #default="{ row }">
                <el-tag :type="row.status === 'success' ? 'success' : 'danger'" effect="light">
                  {{ row.status === 'success' ? '成功' : '失败' }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="详情" min-width="220">
              <template #default="{ row }">
                <el-tooltip
                  v-if="row.detail"
                  :content="row.detail"
                  placement="top"
                  :show-after="300"
                  :enterable="false"
                  popper-class="audit-detail-popper"
                >
                  <span class="detail-cell mono" @click.stop="openDetail(row)">{{ row.detail }}</span>
                </el-tooltip>
                <span v-else class="muted">—</span>
              </template>
            </el-table-column>
            <el-table-column label="操作" width="90" fixed="right">
              <template #default="{ row }">
                <el-button text size="small" type="primary" :icon="View" @click.stop="openDetail(row)">详情</el-button>
              </template>
            </el-table-column>
          </el-table>

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

    <!-- 审计详情抽屉（替代原小弹窗）：行点击 / 「详情」按钮 / 详情单元格三处共用 openDetail 入口。
         形态对齐 TaskList 任务详情抽屉：顶部概要大字 + descriptions 结构化字段 + 关联跳转 + 详情原文 -->
    <el-drawer
      v-model="drawerOpen"
      :title="current ? '审计详情 #' + current.id : '审计详情'"
      size="480px"
      append-to-body
      destroy-on-close
    >
      <template v-if="current">
        <!-- 顶部概要：动作中文大字 + 状态大 tag + 原始动作串与时间（mono 等宽便于对齐追溯） -->
        <div class="dt-head">
          <div class="dt-title-row">
            <span class="dt-title">{{ actionLabel(current.action) }}</span>
            <el-tag :type="current.status === 'success' ? 'success' : 'danger'" effect="light" size="large">
              {{ current.status === 'success' ? '成功' : '失败' }}
            </el-tag>
          </div>
          <div class="dt-meta mono">{{ current.action }} · {{ fmtDateTime(current.created_at) }}</div>
        </div>

        <!-- 结构化字段：column=2 border small；耗时从 detail 文本里正则抽取（后端中间件固定写「请求处理时间: X」） -->
        <el-descriptions :column="2" border size="small" class="dt-desc">
          <el-descriptions-item label="操作人">{{ current.username || '—' }}</el-descriptions-item>
          <el-descriptions-item label="对象类型">{{ objectTypeLabel(current.object_type) }}</el-descriptions-item>
          <el-descriptions-item label="对象ID">
            <span class="mono">{{ current.object_id != null ? '#' + current.object_id : '—' }}</span>
          </el-descriptions-item>
          <el-descriptions-item label="来源 IP">
            <span class="mono">{{ current.source_ip || '—' }}</span>
          </el-descriptions-item>
          <el-descriptions-item label="请求处理耗时" :span="2">
            <span class="mono">{{ parseDuration(current.detail) }}</span>
          </el-descriptions-item>
        </el-descriptions>

        <!-- 关联跳转（包C）：vm 直达虚拟机详情、user 直达用户列表，其余对象无详情落点不渲染 -->
        <template v-if="relatedLink">
          <h4 class="detail-sec">关联跳转</h4>
          <el-link type="primary" :underline="false" @click="goRelated">{{ relatedLink.text }}</el-link>
        </template>

        <h4 class="detail-sec">详情原文</h4>
        <pre class="detail-text">{{ current.detail || '—' }}</pre>
      </template>
      <template #footer>
        <el-button @click="drawerOpen = false">关闭</el-button>
      </template>
    </el-drawer>
  </div>
</template>

<script setup>
import { ref, reactive, computed, watch, nextTick, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Search, RefreshLeft, Refresh, Download, View } from '@element-plus/icons-vue'
import { api, TOKEN_KEY } from '../api'
import echarts from '../utils/echarts'
import { BarChart } from 'echarts/charts'
import { useChart } from '../composables/useChart'
import { useAuth } from '../store/auth'
import { FALLBACK_ACTION_LABELS, fmtDateTime, errMsg, cssVar } from '../utils/format'
import { usePagination } from '../composables/usePagination'
import SessionList from './SessionList.vue'
import PageHead from '../components/PageHead.vue'

// utils/echarts.js 按需注册清单里没有 BarChart（该文件不在本批次可改范围）：
// 柱状图在本页局部补注册。echarts.use 幂等可叠加，且 useChart 持有的是同一模块单例，
// 注册全局生效；漏注册的后果是运行时静默白图（构建不报错），务必保留本行
echarts.use([BarChart])

const router = useRouter()
const { isAdmin } = useAuth()
// 默认 tab：管理员落在操作日志，普通用户只有会话流水可看
const activeTab = ref(isAdmin.value ? 'ops' : 'sessions')

const items = ref([])
const range = ref(null)
const drawerOpen = ref(false)
const current = ref(null)

// 对象类型中文映射：取值集合核实自 middleware/audit.go determineObjectType（vm/host/image/user/docker/app/cron/system）
// + 任务指定的 storage/network/task 预留位；未知类型显原文不臆造
const OBJECT_TYPE_LABELS = {
  vm: '虚拟机', user: '用户', image: '镜像', storage: '存储池', network: '网络',
  task: '任务', system: '系统', host: '宿主机', docker: 'Docker', app: '应用', cron: '计划任务'
}
function objectTypeLabel(t) {
  if (!t) return '—'
  return OBJECT_TYPE_LABELS[t] || t
}

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

function actionLabel(action) {
  if (!action) return '—'
  return actionMap.value[action] || action
}

// 动作大类着色（加在既有 actionLabel 调用处）：登录/登出 primary、删除 danger、
// 创建/克隆/导入/上传 success、电源类 warning、其余 info；集合核实自 middleware/audit.go
// determineAction/vmAction 与 service/jumpd/server.go（jumpd.cmd_blocked 落「其余」info）
function actionTagType(action) {
  if (!action) return 'info'
  if (action === 'login' || action === 'logout') return 'primary'
  if (action.startsWith('delete_')) return 'danger'
  if (
    action.startsWith('create_') ||
    action.startsWith('clone_') ||
    action.startsWith('import_') ||
    action.startsWith('upload_')
  ) {
    return 'success'
  }
  if (['start_vm', 'stop_vm', 'restart_vm', 'pause_vm', 'resume_vm'].includes(action)) return 'warning'
  return 'info'
}

// 请求处理耗时：中间件 detail 固定为「请求处理时间: 58.79ms」（Go time.Duration.String()，
// 可能是 ms/µs/s/m 复合形），抽取首个空白前的时长串；抽不到（如 jumpd 的 detail）显「—」
function parseDuration(detail) {
  if (!detail) return '—'
  const m = detail.match(/请求处理时间[:：]\s*([^\s，,]+)/)
  return m ? m[1] : '—'
}

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

// ===== 详情抽屉：行点击 / 详情按钮 / 详情单元格共用一个打开函数 =====
function openDetail(row) {
  current.value = row
  drawerOpen.value = true
}

// el-table row-click 首参即行数据，包一层免得 (row, column, event) 语义外泄
function onRowClick(row) {
  openDetail(row)
}

// 关联跳转（包C）：vm → 虚拟机详情、user → 用户列表；对象ID 为空或其余类型不给链接
const relatedLink = computed(() => {
  if (!current.value) return null
  const t = current.value.object_type
  const id = current.value.object_id
  if (t === 'vm' && id != null) return { text: '查看虚拟机 →', to: '/vms/' + id }
  if (t === 'user') return { text: '查看用户列表 →', to: '/users' }
  return null
})

function goRelated() {
  if (!relatedLink.value) return
  drawerOpen.value = false
  router.push(relatedLink.value.to)
}

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
/* .page-head / .page-title / .page-desc 已收进 global.css；.mono 的 font-family 亦然，此处只留字号 */
.filters {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-lg);
  margin-bottom: var(--space-xl);
}
.pager {
  margin-top: var(--space-xl);
  justify-content: flex-end;
}
.mono {
  font-size: 0.85rem;
}
.muted {
  color: var(--color-muted-foreground);
}
.detail-cell {
  display: block;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-primary);
  cursor: pointer;
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
/* ===== 行点击钻取 ===== */
/* el-table 内部渲染的 tr 拿不到本组件 scoped 属性，必须 :deep 穿透；
   限定 .clickable-table 前缀，只对操作日志表生效，不波及「控制台会话」tab 的行 */
.clickable-table :deep(.el-table__row) {
  cursor: pointer;
}
/* ===== 详情抽屉（形态对齐 TaskList 任务详情抽屉） ===== */
.dt-head {
  padding: 4px 0;
}
.dt-title-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}
.dt-title {
  font-size: 16px;
  font-weight: 600;
  word-break: break-all;
}
.dt-meta {
  font-size: 0.82rem;
  color: var(--color-muted-foreground);
}
.dt-desc {
  margin-bottom: 4px;
}
.detail-sec {
  margin: 16px 0 8px;
  font-size: 14px;
}
.detail-text {
  margin: 0;
  padding: var(--space-lg);
  background: var(--color-muted);
  border-radius: var(--radius-sm);
  font-family: var(--font-mono);
  font-size: 0.82rem;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 320px;
  overflow: auto;
}
/* tooltip 内容 teleport 到 body，scoped 下用 :deep 穿透；超长 detail 不把 tooltip 撑出视口 */
:deep(.audit-detail-popper) {
  max-width: 480px;
}
</style>
