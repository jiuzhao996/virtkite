<template>
  <div v-loading="loading && !firstLoading">
    <div class="page-head">
      <div class="head-left">
        <h2 class="page-title">仪表盘</h2>
        <span class="live-tag"><span class="live-dot" />实时监控 · {{ pollSeconds }}s</span>
      </div>
      <!-- icon-only 按钮必须带 tooltip（ui-ux-pro-max §1 aria-labels） -->
      <el-tooltip content="刷新" placement="top">
        <el-button :icon="Refresh" circle text aria-label="刷新" @click="loadAll" />
      </el-tooltip>
    </div>

    <!-- 系统公告（v3 批次 P）：公开接口拉取。localStorage 记录已关闭公告的内容 hash——
         同一内容点过关闭不再弹，公告内容变化（hash 不同）后重新提示 -->
    <el-alert
      v-if="announcementVisible"
      class="dash-announcement"
      type="info"
      show-icon
      :title="announcement"
      @close="markAnnouncementRead"
    />

    <!-- 概览 / 监控 两个 tab：概览是状态摘要（含即时时序快照），监控收敛全部深度分析
         （原生 ECharts 看板 + 实时告警 + 告警历史 + file_sd 服务发现）。监控 tab 用 lazy：
         首次激活才挂载，挂载后常驻不销毁（图表实例随组件生命周期）。
         概览 tab 已拆为 dashboard/components/ 下的卡片子组件：本壳只做
         数据拉取（loadAll / pollHost / pollVms / loadAlerts / ...）与 props 下发。 -->
    <el-tabs v-model="activeTab" class="dash-tabs" @tab-change="onTabChange">
      <el-tab-pane label="概览" name="overview">
        <!-- 首载骨架（批④）：形状对齐统计卡行 + 双图表卡；刷新走 v-loading 不闪骨架 -->
        <template v-if="firstLoading">
          <div class="dash-skel-cards">
            <el-card v-for="i in 5" :key="i" shadow="never" class="dash-skel-card">
              <div class="skeleton-text" style="width: 40%"></div>
              <div class="skeleton-text" style="width: 62%; height: 22px; margin-top: 10px"></div>
            </el-card>
          </div>
          <el-row :gutter="16" class="mt">
            <el-col :md="14"><el-card shadow="never"><div class="skeleton-block" style="height: 260px"></div></el-card></el-col>
            <el-col :md="10"><el-card shadow="never"><div class="skeleton-block" style="height: 260px"></div></el-card></el-col>
          </el-row>
        </template>
        <template v-else>
        <!-- Row 1: 统计卡片（adminOnly「用户」卡过滤、跳转在子组件内） -->
        <StatOverviewRow :overview="overview" />

        <!-- Row 2: 主机资源大盘 + 虚拟机状态/资源容量 -->
        <el-row :gutter="16" class="mt">
          <el-col :md="14">
            <!-- active-tick：切回概览 tab 时 +1，子组件据此补 resize（display:none 恢复后图不自动渲染） -->
            <HostResourceCard
              :host="host"
              :last-update="lastUpdate"
              :cpu-series="cpuSeries"
              :mem-series="memSeries"
              :time-labels="timeLabels"
              :active-tick="overviewTick"
            />
          </el-col>
          <el-col :md="10">
            <VmStatusCapacityCard :vm-status="vmStatus" :capacity="capacity" />
          </el-col>
        </el-row>

        <!-- Row 3: VM 实时性能表 -->
        <el-row :gutter="16" class="mt">
          <el-col :span="24">
            <VmPerfTable :items="vmPerf" />
          </el-col>
        </el-row>

        <!-- Row 4+5: 操作分布 / 告警概览 / 平台信息（可见性由子组件按 useAuth 判定） -->
        <AdminPanelCards
          :audit-actions="auditActions"
          :action-labels="actionLabelMap"
          :firing-alerts="firingAlerts"
          :alerts-error="alertsError"
          :sys-info="sysInfo"
          :user-text="userText"
          @go-monitor="activeTab = 'monitor'"
        />
        </template>
      </el-tab-pane>
      <el-tab-pane label="监控" name="monitor" lazy>
        <!-- ⚠️ 必须用 MonitorView：Monitor 已被 @element-plus/icons-vue 的显示器图标占用 -->
        <!-- active 随 tab：切走时 Monitor 内部停告警/看板轮询（组件不卸载，不停则后台空转） -->
        <MonitorView v-if="visitedTabs.has('monitor')" embedded :active="activeTab === 'monitor'" />
      </el-tab-pane>
      <el-tab-pane label="拓扑" name="topology" lazy>
        <!-- 拓扑图并入仪表盘第三 tab（IA 精简批次）；active-tick 用于 tab 切回时触发子图 resize -->
        <TopologyView v-if="visitedTabs.has('topology')" embedded :active-tick="topoTick" />
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onBeforeUnmount, reactive } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import MonitorView from './Monitor.vue'
import TopologyView from './Topology.vue'
import StatOverviewRow from './dashboard/components/StatOverviewRow.vue'
import HostResourceCard from './dashboard/components/HostResourceCard.vue'
import VmStatusCapacityCard from './dashboard/components/VmStatusCapacityCard.vue'
import VmPerfTable from './dashboard/components/VmPerfTable.vue'
import AdminPanelCards from './dashboard/components/AdminPanelCards.vue'
import { api } from '../api'
import { useRoute } from 'vue-router'
import { POLL_DEFAULTS, getPollInterval } from '../utils/settings'
import { useAuth } from '../store/auth'
import {
  FALLBACK_ACTION_LABELS,
  roleText,
  nowClock
} from '../utils/format'

const { state, isAdmin, canOperate } = useAuth()
const pollSeconds = computed(() => getPollInterval('dashboard', POLL_DEFAULTS.dashboard) / 1000)
const loading = ref(false)
// 首载标记（批④骨架屏）：首次 loadAll 后永久 false，之后刷新走 v-loading
const firstLoading = ref(true)
const overview = ref(null)
const vmStatus = ref([])
const auditActions = ref([])
const vmPerf = ref([])

// 告警概览（登录即可见，失败静默置未连接态）
const alerts = ref([])
const alertsError = ref(false)
const firingAlerts = computed(
  () => alerts.value.filter((a) => a.status && a.status.state === 'active')
)
// 系统信息（仅管理员拉取，补充平台信息卡；viewer 无 /settings 权限）
const sysInfo = ref(null)

// 系统公告（v3 批次 P）：登录即可见，公开接口。localStorage 记录已关闭公告的内容
// hash（djb2，仅本地「内容变没变」比对用）——同一内容点过关闭不再弹，内容变化重新提示
const ANN_READ_KEY = 'vmops_announcement_read'
const announcement = ref('')
const announcementRead = ref(true) // 初始视为已读，拉到未读内容再点亮

function hashStr(s) {
  let h = 5381
  for (let i = 0; i < s.length; i++) h = ((h << 5) + h + s.charCodeAt(i)) | 0
  return String(h >>> 0)
}

const announcementVisible = computed(() => announcement.value !== '' && !announcementRead.value)

async function loadAnnouncement() {
  try {
    const res = await api.getAnnouncement()
    const content = (res.data && res.data.content) || ''
    announcement.value = content
    announcementRead.value = content === '' || localStorage.getItem(ANN_READ_KEY) === hashStr(content)
  } catch (e) {
    /* 公告拉取失败静默，不打扰仪表盘主流程 */
  }
}

function markAnnouncementRead() {
  announcementRead.value = true
  try {
    localStorage.setItem(ANN_READ_KEY, hashStr(announcement.value))
  } catch (e) {
    /* localStorage 不可用（隐私模式等）时仅本次会话生效 */
  }
}

async function loadAlerts() {
  if (!canOperate.value) return // 监控数据仅操作员/管理员可见（viewer 不发请求）
  try {
    const res = await api.listAlerts()
    alerts.value = Array.isArray(res.data) ? res.data : []
    alertsError.value = false
  } catch (e) {
    alertsError.value = true
  }
}

async function loadSysInfo() {
  if (!isAdmin.value) return
  try {
    const res = await api.getSettings()
    sysInfo.value = res.data || null
  } catch (e) {
    sysInfo.value = null
  }
}

// 操作类型 → 中文兜底映射（后端 /audit/actions 优先覆盖）已收进 utils/format.js（与审计页共用）
const actionLabelMap = ref({ ...FALLBACK_ACTION_LABELS })

const HOST_POINTS = 60

const host = ref({ cpu: 0, memPct: 0, memUsed: '0', memTotal: '0' })
const cpuSeries = ref([])
const memSeries = ref([])
const timeLabels = ref([])
const lastUpdate = ref('')

let hostTimer = null
let vmTimer = null
let alertTimer = null

// 概览/监控/拓扑 tab：visitedTabs 记录已激活过的 tab（配合 lazy，首次激活挂载后常驻）。
// 切回概览时 overviewTick +1 → HostResourceCard 补一次 resize（echarts 容器从
// display:none 恢复后不自动重绘）。
const activeTab = ref('overview')
const visitedTabs = reactive(new Set(['overview']))
// 拓扑 tab 重激活信号：拓扑子图已挂载后再切回时 +1，TopologyView watch 它做 chart.resize()
const topoTick = ref(0)
// 概览 tab 重激活信号：机制同 topoTick，方向反过来——图表逻辑在 HostResourceCard 内
const overviewTick = ref(0)
const route = useRoute()
function onTabChange(name) {
  if (name === 'monitor') visitedTabs.add('monitor')
  else if (name === 'topology') {
    const revisit = visitedTabs.has('topology')
    visitedTabs.add('topology')
    if (revisit) topoTick.value++
  } else overviewTick.value++
  // 概览轮询表随 tab 启停：切走全停（概览不可见不该打后端），切回重启
  setOverviewTimers(name === 'overview')
}

// 概览三张轮询表（宿主机 / VM / 告警）的统一启停。
// el-tabs 切走只是 display:none，组件与定时器都活着——不停表则监控/拓扑 tab 下
// 概览数据仍在后台空转轮询；且 tab=monitor 时内嵌 Monitor 也在轮询告警，不停会双路打同一接口。
// 周期每次启停现取（切回 overview 时若设置页改过轮询偏好，无需刷新即生效）。
function setOverviewTimers(on) {
  clearInterval(hostTimer)
  clearInterval(vmTimer)
  clearInterval(alertTimer)
  if (!on) return
  const ms = getPollInterval('dashboard', POLL_DEFAULTS.dashboard)
  hostTimer = setInterval(pollHost, ms)
  vmTimer = setInterval(pollVms, ms)
  alertTimer = setInterval(loadAlerts, ms)
}
const capacity = ref({ has_host: false, vm_count: 0, allocated_vcpu: 0, allocated_mem_mb: 0, physical_cores: 0, physical_mem_mb: 0, cpu_ratio: 0, mem_ratio: 0 })
async function loadCapacity() {
  try {
    const res = await api.dashboardCapacity()
    capacity.value = res.data || capacity.value
  } catch (e) { /* 静默：容量卡降级为空态 */ }
}

const userText = computed(() => {
  const u = state.user
  if (!u) return '—'
  return u.username + '（' + roleText(u.role) + '）'
})

async function loadAll() {
  loading.value = true
  try {
    // 分开请求：审计接口 viewer 无权限（403），不能拖死概览（Promise.all 一挂全挂）
    const [ov, vs] = await Promise.all([
      api.dashboardOverview(),
      api.vmStatus()
    ])
    overview.value = ov.data
    vmStatus.value = vs.data || []
  } catch (e) {
    // 静默降级，卡片保持 0
  }
  // 审计分布仅管理员可见，失败静默（viewer 直接跳过请求）
  if (isAdmin.value) {
    try {
      const [au, al] = await Promise.all([api.auditSummary(), api.auditActions()])
      auditActions.value = au.data || []
      actionLabelMap.value = { ...FALLBACK_ACTION_LABELS, ...(al.data || {}) }
    } catch (e) {
      // 忽略
    }
  }
  loading.value = false
  firstLoading.value = false
  await Promise.all([pollHost(), pollVms()])
}

// 主机资源轮询：3s 推入 60 点环形数组（序列经 props 下发，HostResourceCard deep watch 后重画）
async function pollHost() {
  try {
    const res = await api.dashboardHostStats()
    const d = res.data || {}
    const cpu = Math.round(d.cpu_percent ?? 0)
    const totalKib = d.mem_total_kib || 0
    const usedKib = d.mem_used_kib || 0
    const memPct = totalKib ? Math.round((usedKib / totalKib) * 100) : 0
    host.value = {
      cpu,
      memPct,
      memUsed: (usedKib / 1048576).toFixed(1),
      memTotal: (totalKib / 1048576).toFixed(1)
    }
    cpuSeries.value.push(cpu)
    memSeries.value.push(memPct)
    timeLabels.value.push(nowClock())
    if (cpuSeries.value.length > HOST_POINTS) {
      cpuSeries.value.shift()
      memSeries.value.shift()
      timeLabels.value.shift()
    }
    lastUpdate.value = nowClock()
  } catch (e) {
    // 静默降级
  }
}

async function pollVms() {
  try {
    const res = await api.vmPerf()
    vmPerf.value = res.data || []
  } catch (e) {
    // 静默降级
  }
}

// 进页面时从 Prometheus 预填历史曲线（替代"从零攒点等 5s"）：
// 拉不到（监控栈未起）静默降级为原行为。之后 3s 轮询继续追加，衔接处时间连续。
async function prefillHostHistory() {
  try {
    const res = await api.hostHistory(60)
    const pts = (res.data && res.data.points) || []
    if (!pts.length) return
    const recent = pts.slice(-HOST_POINTS)
    timeLabels.value = recent.map((p) => p.t)
    cpuSeries.value = recent.map((p) => p.cpu)
    memSeries.value = recent.map((p) => p.mem)
  } catch (e) {
    /* 静默降级 */
  }
}

onMounted(async () => {
  // 兼容旧书签：/monitor、/topology 重定向到 /dashboard?tab=xx 时直达对应 tab
  if (route.query.tab === 'monitor' || route.query.tab === 'topology') {
    activeTab.value = route.query.tab
    visitedTabs.add(route.query.tab)
  }
  await loadAll()
  // 概览轮询表（宿主机/VM/告警）统一由 setOverviewTimers 托管：
  // 切到监控/拓扑 tab 时概览不可见，停表不空转（监控 tab 由内嵌 Monitor 自己轮询，
  // Dashboard 侧告警表同步停掉，否则同一接口双路轮询）
  setOverviewTimers(true)
  // 告警概览与平台信息：进页拉一次（告警后续走轮询表）
  loadAlerts()
  loadSysInfo()
  loadCapacity()
  loadAnnouncement()
  // 历史曲线预填：先画满过去一小时，再由轮询无缝追加
  prefillHostHistory()
})

onBeforeUnmount(() => {
  clearInterval(hostTimer)
  clearInterval(vmTimer)
  clearInterval(alertTimer)
})
</script>

<style scoped>
/* 首载骨架（批④）：统计卡行五等分，与 StatOverviewRow 栅格一致 */
.dash-skel-cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 16px;
}
/* 系统公告条：页头与 tabs 之间留 8px 栅格间距 */
.dash-announcement {
  margin-bottom: 16px;
}
.dash-tabs {
  margin-bottom: var(--space-lg);
}
/* 概览/监控 tab 做大：16px 加粗、加高加间距，避免藏在页首不被发现 */
.dash-tabs :deep(.el-tabs__item) {
  font-size: 1.05rem;
  font-weight: 600;
  height: 46px;
  line-height: 46px;
  padding: 0 26px;
  color: var(--el-text-color-secondary);
}
.dash-tabs :deep(.el-tabs__item.is-active) {
  font-weight: 700;
  color: var(--el-color-primary);
}
.dash-tabs :deep(.el-tabs__nav-wrap::after) {
  height: 1px;
}
/* .page-head / .page-title 已收进 global.css（原本页 margin-bottom: 16px 与 var(--space-xl) 等值） */
.head-left {
  display: flex;
  align-items: center;
  gap: 12px;
}
.live-tag {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 0.75rem;
  color: var(--color-muted-foreground);
  background: var(--color-muted);
  border-radius: 999px;
  padding: 2px 10px;
}
.live-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--color-success);
  animation: live-pulse 1.6s ease-in-out infinite;
}
@keyframes live-pulse {
  0%,
  100% {
    box-shadow: 0 0 0 0 rgba(22, 163, 74, 0.4);
  }
  50% {
    box-shadow: 0 0 0 5px rgba(22, 163, 74, 0);
  }
}
.mt {
  margin-top: 16px;
}
</style>
