<template>
  <div v-loading="loading">
    <div class="page-head">
      <div class="head-left">
        <h2 class="page-title">仪表盘</h2>
        <span class="live-tag"><span class="live-dot" />实时监控 · 3s</span>
      </div>
      <el-button :icon="Refresh" circle text @click="loadAll" />
    </div>

    <!-- Row 1: 统计卡片 -->
    <el-row :gutter="16">
      <el-col :xs="12" :sm="8" :md="3" v-for="s in stats" :key="s.label">
        <el-card shadow="hover" class="stat-card">
          <el-icon class="stat-icon" :style="{ color: s.color }">
            <component :is="s.icon" />
          </el-icon>
          <el-statistic :value="s.value" :value-style="{ color: 'var(--color-foreground)', fontWeight: 700 }" />
          <div class="stat-label">{{ s.label }}</div>
        </el-card>
      </el-col>
    </el-row>

    <!-- Row 2: 主机资源大盘 + 虚拟机状态 -->
    <el-row :gutter="16" class="mt">
      <el-col :md="14">
        <el-card shadow="hover" class="host-card">
          <template #header>
            <span class="card-title">主机资源实时大盘</span>
            <span v-if="lastUpdate" class="update-time">更新于 {{ lastUpdate }}</span>
          </template>

          <div class="host-summary">
            <div class="host-metric">
              <el-progress
                type="circle"
                :percentage="host.cpu"
                :width="92"
                :stroke-width="9"
                :color="primaryColor"
              />
              <div class="metric-text">
                <span class="metric-num">{{ host.cpu }}<small>%</small></span>
                <span class="metric-label">主机 CPU 使用率</span>
              </div>
            </div>
            <div class="host-metric mem">
              <div class="metric-text">
                <span class="metric-num">{{ host.memUsed }}<small> / {{ host.memTotal }} GB</small></span>
                <span class="metric-label">主机内存 · 已用 {{ host.memPct }}%</span>
                <el-progress
                  class="mem-bar"
                  :percentage="host.memPct"
                  :stroke-width="9"
                  :color="memChartColor"
                  :show-text="false"
                />
              </div>
            </div>
          </div>

          <div class="chart-wrap">
            <div ref="hostChartRef" class="host-chart" />
          </div>
        </el-card>
      </el-col>

      <el-col :md="10">
        <el-card shadow="hover">
          <template #header>
            <span class="card-title">虚拟机状态</span>
          </template>
          <div v-if="vmStatus.length === 0" class="empty">暂无数据</div>
          <div v-else class="donut-wrap">
            <div class="donut" :style="{ background: donutStyle }"><span class="donut-center">{{ totalVM }}<small>台</small></span></div>
            <div class="donut-legend">
              <div v-for="item in vmStatus" :key="item.status" class="legend-item">
                <span class="dot" :style="{ background: vmStatusHex(item.status) }" />
                <span>{{ vmStatusText(item.status) }}</span>
                <b>{{ item.count }}</b>
              </div>
            </div>
          </div>
          <div class="status-rows">
            <div v-for="item in vmStatus" :key="item.status" class="status-row">
              <span class="status-name">{{ vmStatusText(item.status) }}</span>
              <el-progress
                class="status-bar"
                :percentage="pct(item.count)"
                :color="vmStatusColor(item.status)"
                :format="() => item.count + ' 台'"
              />
            </div>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <!-- Row 3: VM 实时性能表 -->
    <el-row :gutter="16" class="mt">
      <el-col :span="24">
        <el-card shadow="hover">
          <template #header>
            <span class="card-title">VM 实时性能</span>
            <span class="update-time">运行中虚拟机展示 CPU / 内存占用</span>
          </template>
          <el-table :data="vmPerf" size="small" class="perf-table" empty-text="暂无运行中虚拟机">
            <el-table-column label="名称" min-width="220" show-overflow-tooltip>
              <template #default="{ row }">
                <span class="vm-name">
                  <el-icon class="vm-icon"><Monitor /></el-icon>
                  {{ row.name }}
                </span>
              </template>
            </el-table-column>
            <el-table-column label="状态" width="120">
              <template #default="{ row }">
                <el-tag :type="vmStatusTag(row.status)" effect="light" size="small" round>{{ vmStatusText(row.status) }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column label="CPU" min-width="220">
              <template #default="{ row }">
                <div v-if="row.status === 'running'" class="perf-cell">
                  <el-progress
                    :percentage="clampPct(row.cpu_percent)"
                    :color="usageColor(row.cpu_percent)"
                    :stroke-width="8"
                    :format="() => Math.round(row.cpu_percent || 0) + '%'"
                  />
                </div>
                <span v-else class="perf-na">—</span>
              </template>
            </el-table-column>
            <el-table-column label="内存" min-width="220">
              <template #default="{ row }">
                <div v-if="row.status === 'running'" class="perf-cell">
                  <el-progress
                    :percentage="clampPct(row.mem_pct)"
                    :color="usageColor(row.mem_pct)"
                    :stroke-width="8"
                    :format="() => Math.round(row.mem_pct || 0) + '%'"
                  />
                </div>
                <span v-else class="perf-na">—</span>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>
    </el-row>

    <!-- Row 4: 操作分布 + 平台信息 -->
    <el-row :gutter="16" class="mt">
      <el-col v-if="isAdmin" :md="12">
        <el-card shadow="hover">
          <template #header>
            <span class="card-title">操作类型分布</span>
          </template>
          <div v-if="topActions.length === 0" class="empty">暂无数据</div>
          <div v-for="a in topActions" :key="a.action" class="action-row">
            <span class="action-name">{{ actionLabel(a.action) }}</span>
            <div class="action-track">
              <div class="action-fill" :style="{ width: actionPct(a.count) + '%', background: actionColor(a.action) }" />
            </div>
            <span class="action-count">{{ a.count }}</span>
          </div>
        </el-card>
      </el-col>
      <el-col :md="isAdmin ? 12 : 24">
        <el-card shadow="hover">
          <template #header>
            <div class="alert-card-head">
              <span class="card-title">告警概览</span>
              <el-link type="primary" :underline="false" @click="$router.push('/monitor')">前往监控中心</el-link>
            </div>
          </template>
          <div v-if="alertsError" class="empty">监控栈未连接（docker compose up -d 启动 Prometheus / Alertmanager）</div>
          <template v-else>
            <div v-if="firingAlerts.length === 0" class="empty alert-ok">当前无告警，一切正常</div>
            <div v-for="a in firingAlerts.slice(0, 4)" :key="a.fingerprint" class="alert-row">
              <el-tag :type="(a.labels && a.labels.severity) === 'critical' ? 'danger' : 'warning'" effect="dark" size="small">
                {{ (a.labels && a.labels.severity) === 'critical' ? '严重' : '警告' }}
              </el-tag>
              <span class="alert-name">{{ a.labels && a.labels.alertname }}</span>
              <span class="alert-summary">{{ (a.annotations && a.annotations.summary) || '' }}</span>
            </div>
            <div v-if="firingAlerts.length > 4" class="empty">还有 {{ firingAlerts.length - 4 }} 条告警，见监控中心</div>
          </template>
        </el-card>
      </el-col>
    </el-row>

    <el-row :gutter="16" class="mt">
      <el-col :span="24">
        <el-card shadow="hover">
          <template #header>
            <span class="card-title">平台信息</span>
          </template>
          <div class="info-rows">
            <div><span>平台</span><strong>vmops · KVM 私有云</strong></div>
            <div><span>后端</span><strong>Go + Gin + GORM + Libvirt</strong></div>
            <div><span>前端</span><strong>Vue 3 + Element Plus + ECharts</strong></div>
            <div><span>当前用户</span><strong>{{ userText }}</strong></div>
            <template v-if="sysInfo">
              <div><span>libvirt URI</span><strong>{{ sysInfo.virt && sysInfo.virt.libvirt_uri }}</strong></div>
              <div><span>存储池</span><strong>{{ (sysInfo.storage && sysInfo.storage.pools || []).join('、') || '—' }}</strong></div>
              <div><span>虚拟网络</span><strong>{{ (sysInfo.network && sysInfo.network.networks || []).join('、') || '—' }}</strong></div>
              <div><span>运行模式</span><strong>{{ sysInfo.platform && sysInfo.platform.server_mode }} · :{{ sysInfo.platform && sysInfo.platform.server_port }}</strong></div>
            </template>
          </div>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onBeforeUnmount, nextTick } from 'vue'
import * as echarts from 'echarts'
import { Refresh, Cpu, Monitor, VideoPlay, FolderOpened, Connection, Picture, User, Document } from '@element-plus/icons-vue'
import { api } from '../api'
import { POLL_DEFAULTS, getPollInterval } from '../utils/settings'
import { useAuth } from '../store/auth'
import {
  FALLBACK_ACTION_LABELS,
  vmStatusText,
  vmStatusTag,
  vmStatusColor,
  vmStatusHex,
  usageColor,
  clampPct,
  nowClock,
  cssVar
} from '../utils/format'

const { state, isAdmin } = useAuth()
const loading = ref(false)
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

async function loadAlerts() {
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

// echarts 不解析 var()，主机曲线需要真实色值：cssVar 由 utils/format.js 提供
const primaryColor = cssVar('--el-color-primary', '#2a9da5')
const memChartColor = cssVar('--color-success', '#16a34a')

const host = ref({ cpu: 0, memPct: 0, memUsed: '0', memTotal: '0' })
const cpuSeries = ref([])
const memSeries = ref([])
const timeLabels = ref([])
const lastUpdate = ref('')

const hostChartRef = ref(null)
let chart = null
let hostTimer = null
let vmTimer = null
let alertTimer = null

const stats = computed(() => {
  const o = overview.value || {}
  return [
    { label: '宿主机', icon: Cpu, color: 'var(--color-primary)', value: o.host_count || 0 },
    { label: '虚拟机', icon: Monitor, color: 'var(--color-primary)', value: o.vm_count || 0 },
    { label: '运行中', icon: VideoPlay, color: 'var(--color-accent)', value: o.running_vm_count || 0 },
    { label: '存储池', icon: FolderOpened, color: 'var(--color-warning)', value: o.pool_count || 0 },
    { label: '网络', icon: Connection, color: '#2563eb', value: o.network_count || 0 },
    { label: '镜像', icon: Picture, color: '#7c3aed', value: o.image_count || 0 },
    { label: '用户', icon: User, color: '#0891b2', value: o.user_count || 0 },
    { label: '审计', icon: Document, color: 'var(--color-info)', value: o.audit_count || 0 }
  ]
})

const userText = computed(() => {
  const u = state.user
  if (!u) return '—'
  return u.username + '（' + (u.role === 'admin' ? '管理员' : '用户') + '）'
})

const totalVM = computed(() => vmStatus.value.reduce((a, b) => a + b.count, 0))

function pct(count) {
  if (!totalVM.value) return 0
  return Math.round((count / totalVM.value) * 100)
}
const donutStyle = computed(() => {
  if (!totalVM.value) return '#eef2f6'
  let acc = 0
  const segs = vmStatus.value.map((it) => {
    const from = Math.round((acc / totalVM.value) * 360)
    acc += it.count
    const to = Math.round((acc / totalVM.value) * 360)
    return `${vmStatusHex(it.status)} ${from}deg ${to}deg`
  })
  return `conic-gradient(${segs.join(', ')})`
})

// 审计动作分布（取前 8）
const topActions = computed(() => [...auditActions.value].sort((a, b) => b.count - a.count).slice(0, 8))
const maxAction = computed(() => (topActions.value.length ? Math.max(...topActions.value.map((a) => a.count)) : 1))
function actionPct(c) {
  return Math.max(3, Math.round((c / maxAction.value) * 100))
}
function actionLabel(a) {
  return actionLabelMap.value[a] || a
}
function actionColor(a) {
  if (a.includes('delete')) return 'var(--color-danger)'
  if (a.includes('create') || a.includes('upload') || a.includes('import')) return 'var(--color-primary)'
  if (a.includes('start') || a.includes('login')) return 'var(--color-success)'
  if (a.includes('stop') || a.includes('restart')) return 'var(--color-warning)'
  return 'var(--color-info)'
}

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
  await Promise.all([pollHost(), pollVms()])
}

// 主机资源轮询：3s 推入 60 点环形数组
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
    updateChart()
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

function updateChart() {
  if (!chart) return
  chart.setOption({
    xAxis: { data: timeLabels.value },
    series: [
      { data: cpuSeries.value },
      { data: memSeries.value }
    ]
  })
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
    updateChart()
  } catch (e) {
    /* 静默降级 */
  }
}

function initChart() {
  if (!hostChartRef.value) return
  chart = echarts.init(hostChartRef.value)
  chart.setOption({
    animationDuration: 300,
    grid: { left: 8, right: 12, top: 36, bottom: 4, containLabel: true },
    tooltip: {
      trigger: 'axis',
      valueFormatter: (v) => v + '%'
    },
    legend: {
      top: 4,
      right: 8,
      itemWidth: 14,
      itemHeight: 8,
      textStyle: { color: 'var(--color-muted-foreground)', fontSize: 12 }
    },
    xAxis: {
      type: 'category',
      boundaryGap: false,
      data: timeLabels.value,
      axisLine: { lineStyle: { color: '#e2e8f0' } },
      axisLabel: { color: '#94a3b8', fontSize: 11, interval: 14 }
    },
    yAxis: {
      type: 'value',
      max: 100,
      axisLabel: { formatter: '{value}%', color: '#94a3b8' },
      splitLine: { lineStyle: { color: '#eef2f6' } }
    },
    series: [
      {
        name: 'CPU',
        type: 'line',
        smooth: true,
        showSymbol: false,
        lineStyle: { width: 2, color: primaryColor },
        itemStyle: { color: primaryColor },
        areaStyle: { opacity: 0.08, color: primaryColor },
        data: cpuSeries.value
      },
      {
        name: '内存',
        type: 'line',
        smooth: true,
        showSymbol: false,
        lineStyle: { width: 2, color: memChartColor },
        itemStyle: { color: memChartColor },
        areaStyle: { opacity: 0.08, color: memChartColor },
        data: memSeries.value
      }
    ]
  })
}

const onResize = () => chart && chart.resize()

onMounted(async () => {
  await loadAll()
  await nextTick()
  initChart()
  const ms = getPollInterval('dashboard', POLL_DEFAULTS.dashboard)
  hostTimer = setInterval(pollHost, ms)
  vmTimer = setInterval(pollVms, ms)
  // 告警概览与平台信息：进页拉一次，告警随仪表盘节奏轮询
  loadAlerts()
  loadSysInfo()
  alertTimer = setInterval(loadAlerts, ms)
  // 历史曲线预填：先画满过去一小时，再由轮询无缝追加
  prefillHostHistory()
  window.addEventListener('resize', onResize)
})

onBeforeUnmount(() => {
  clearInterval(hostTimer)
  clearInterval(vmTimer)
  clearInterval(alertTimer)
  window.removeEventListener('resize', onResize)
  if (chart) {
    chart.dispose()
    chart = null
  }
})
</script>

<style scoped>
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
.card-title {
  font-weight: 600;
  color: var(--color-foreground);
}
.update-time {
  float: right;
  font-size: 0.75rem;
  color: var(--color-muted-foreground);
  font-weight: 400;
}
.stat-card {
  text-align: center;
  margin-bottom: 4px;
}
.stat-icon {
  font-size: 1.5rem;
  margin-bottom: 6px;
}
.stat-label {
  font-size: 0.82rem;
  color: var(--color-muted-foreground);
  margin-top: 4px;
}
.mt {
  margin-top: 16px;
}

/* 主机资源大盘 */
.host-summary {
  display: flex;
  align-items: center;
  gap: 32px;
  padding: 4px 0 8px;
}
.host-metric {
  display: flex;
  align-items: center;
  gap: 16px;
  flex: 1;
}
.host-metric.mem {
  border-left: 1px solid var(--color-border);
  padding-left: 32px;
}
.metric-text {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}
.metric-num {
  font-size: 1.7rem;
  font-weight: 700;
  color: var(--color-foreground);
  font-family: var(--font-mono);
  line-height: 1;
}
.metric-num small {
  font-size: 0.8rem;
  font-weight: 500;
  color: var(--color-muted-foreground);
  margin-left: 2px;
}
.metric-label {
  font-size: 0.82rem;
  color: var(--color-muted-foreground);
}
.mem-bar {
  width: 100%;
  max-width: 300px;
}
.chart-wrap {
  margin-top: 8px;
}
.host-chart {
  width: 100%;
  height: 220px;
}

/* 虚拟机状态 */
.donut-wrap {
  display: flex;
  align-items: center;
  gap: 24px;
  padding: 6px 0;
}
.donut {
  position: relative;
  width: 120px;
  height: 120px;
  border-radius: 50%;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}
.donut::before {
  content: '';
  position: absolute;
  width: 72px;
  height: 72px;
  border-radius: 50%;
  background: #fff;
}
.donut-center {
  position: relative;
  font-size: 1.3rem;
  font-weight: 700;
}
.donut-center small {
  font-size: 0.7rem;
  color: var(--color-muted-foreground);
  margin-left: 2px;
}
.donut-legend {
  display: flex;
  flex-direction: column;
  gap: 8px;
  flex: 1;
}
.legend-item {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 0.88rem;
}
.legend-item .dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
}
.legend-item b {
  margin-left: auto;
  font-family: var(--font-mono);
}
.status-rows {
  margin-top: 16px;
  border-top: 1px solid var(--color-border);
  padding-top: 12px;
}
.status-row {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 10px;
}
.status-name {
  width: 64px;
  font-size: 0.85rem;
  color: var(--color-muted-foreground);
}
.status-bar {
  flex: 1;
}

/* VM 实时性能表 */
.perf-table .vm-name {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-weight: 500;
}
.vm-icon {
  color: var(--color-primary);
}
.perf-cell {
  display: flex;
  align-items: center;
  gap: 10px;
  padding-right: 8px;
}
.perf-na {
  color: var(--color-muted-foreground);
  padding-left: 8px;
}

.empty {
  color: var(--color-muted-foreground);
  text-align: center;
  padding: 20px 0;
}
.info-rows {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px 24px;
}
.alert-card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.alert-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 0;
  border-bottom: 1px solid var(--color-border);
}
.alert-row:last-of-type {
  border-bottom: none;
}
.alert-name {
  font-weight: 600;
  font-size: 13px;
  white-space: nowrap;
}
.alert-summary {
  color: var(--color-muted-foreground);
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.alert-ok {
  color: var(--color-success);
}
.info-rows > div {
  display: flex;
  gap: 10px;
  font-size: 0.95rem;
}
.info-rows span {
  color: var(--color-muted-foreground);
  min-width: 56px;
}
/* 操作类型分布条形图 */
.action-row {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}
.action-name {
  width: 84px;
  font-size: 0.85rem;
  color: var(--color-muted-foreground);
  white-space: nowrap;
}
.action-track {
  flex: 1;
  height: 10px;
  border-radius: 5px;
  background: #eef2f6;
  overflow: hidden;
}
.action-fill {
  height: 100%;
  border-radius: 5px;
  transition: width 0.3s ease;
}
.action-count {
  min-width: 44px;
  text-align: right;
  font-family: var(--font-mono);
  font-size: 0.85rem;
  color: var(--color-foreground);
}
</style>