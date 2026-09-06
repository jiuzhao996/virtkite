<template>
  <div>
    <div class="page-head">
      <div>
        <h3 class="page-title">监控中心</h3>
        <p class="page-desc">Alertmanager 实时告警 + Grafana 可视化看板（Prometheus 指标 15s 刷新）</p>
      </div>
      <el-button :icon="Refresh" @click="loadAlerts">刷新告警</el-button>
    </div>

    <!-- Grafana 看板（kiosk 模式嵌入；地址跟随当前访问主机，兼容宿主机直跑与 docker compose 两种形态）。
         看板是本页视觉主体放首屏，告警作为状态摘要放下方（有告警时卡片标红提示）。 -->
    <el-card shadow="never" class="grafana-card">
      <template #header>
        <div class="alert-head">
          <span>资源监控看板</span>
          <el-link type="primary" :href="grafanaFull" target="_blank">在新窗口打开 Grafana</el-link>
        </div>
      </template>
      <div v-if="grafanaError" class="grafana-fallback">
        <el-empty description="Grafana 看板加载失败（需在 docker compose 中启动 grafana 服务并映射 3000 端口）" :image-size="72" />
      </div>
      <iframe
        v-else
        :src="grafanaEmbed"
        class="grafana-frame"
        frameborder="0"
        @error="grafanaError = true"
      ></iframe>
    </el-card>

    <!-- 告警列表 -->
    <el-card shadow="never" class="alert-card" :class="{ 'alert-firing': firingCount }">
      <template #header>
        <div class="alert-head">
          <span>实时告警</span>
          <el-tag v-if="!alertsError" :type="firingCount ? 'danger' : 'success'" effect="light" size="small">
            {{ firingCount ? `${firingCount} 条待处理` : '当前无告警' }}
          </el-tag>
        </div>
      </template>

      <el-alert
        v-if="alertsError"
        type="warning"
        :closable="false"
        show-icon
        title="监控栈未连接"
        description="无法访问 Alertmanager（典型原因：docker compose 未启动监控栈）。执行 docker compose up -d 启动 Prometheus / Alertmanager / Grafana 后刷新本页。"
      />

      <el-table v-else-if="firingCount" :data="alerts" v-loading="alertsLoading" stripe>
        <el-table-column label="级别" width="90">
          <template #default="{ row }">
            <el-tag :type="row.labels && row.labels.severity === 'critical' ? 'danger' : 'warning'" effect="dark" size="small">
              {{ (row.labels && row.labels.severity) === 'critical' ? '严重' : '警告' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="告警名称" width="170">
          <template #default="{ row }">
            <span class="mono">{{ row.labels && row.labels.alertname }}</span>
          </template>
        </el-table-column>
        <el-table-column label="摘要" min-width="240">
          <template #default="{ row }">{{ (row.annotations && row.annotations.summary) || '—' }}</template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.status && row.status.state === 'active' ? 'danger' : 'info'" effect="plain" size="small">
              {{ row.status && row.status.state === 'active' ? '触发中' : '已抑制' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="触发时间" width="180">
          <template #default="{ row }">
            <span class="mono">{{ row.startsAt ? fmtDateTime(row.startsAt) : '—' }}</span>
          </template>
        </el-table-column>
      </el-table>

      <el-empty v-else description="当前无告警，一切正常" :image-size="60" />
    </el-card>

    <!-- 告警历史（Alertmanager webhook 推回平台入库的追溯数据，与上方实时列表互补） -->
    <el-card shadow="never" class="alert-card">
      <template #header>
        <div class="alert-head">
          <span><el-icon class="head-icon"><AlarmClock /></el-icon>告警历史</span>
          <div class="alert-head-actions">
            <el-select v-model="historyStatus" style="width: 120px" size="small" @change="onHistoryFilter">
              <el-option label="全部状态" value="" />
              <el-option label="触发中" value="firing" />
              <el-option label="已恢复" value="resolved" />
            </el-select>
            <el-button size="small" :icon="Refresh" @click="loadHistory">刷新</el-button>
          </div>
        </div>
      </template>

      <el-table :data="history" v-loading="historyLoading" stripe>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.status === 'firing' ? 'danger' : 'success'" effect="light" size="small">
              {{ row.status === 'firing' ? '触发中' : '已恢复' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="告警名称" width="180">
          <template #default="{ row }">
            <span class="mono">{{ row.labels && row.labels.alertname || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="级别" width="90">
          <template #default="{ row }">
            <el-tag
              :type="(row.labels && row.labels.severity) === 'critical' ? 'danger' : (row.labels && row.labels.severity) === 'warning' ? 'warning' : 'info'"
              effect="dark" size="small"
            >
              {{ (row.labels && row.labels.severity) === 'critical' ? '严重' : (row.labels && row.labels.severity) === 'warning' ? '警告' : (row.labels && row.labels.severity) || '—' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="实例" width="150">
          <template #default="{ row }">
            <span class="mono">{{ (row.labels && row.labels.instance) || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="开始时间" width="170">
          <template #default="{ row }">
            <span class="mono">{{ row.starts_at ? fmtDateTime(row.starts_at) : '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="最近更新" width="170">
          <template #default="{ row }">
            <span class="mono">{{ row.updated_at ? fmtDateTime(row.updated_at) : '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="摘要" min-width="220" show-overflow-tooltip>
          <template #default="{ row }">
            {{ (row.annotations && (row.annotations.summary || row.annotations.description)) || '—' }}
          </template>
        </el-table-column>
      </el-table>

      <div class="history-pager">
        <el-pagination
          v-model:current-page="historyPage"
          :page-size="historyPageSize"
          :total="historyTotal"
          layout="total, prev, pager, next"
          small
          background
          @current-change="loadHistory"
        />
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { AlarmClock, Refresh } from '@element-plus/icons-vue'
import { api } from '../api'
import { fmtDateTime } from '../utils/format'
import { getPollInterval, POLL_DEFAULTS } from '../utils/settings'

const alerts = ref([])
const alertsLoading = ref(false)
const alertsError = ref(false)
const grafanaError = ref(false)

// 只统计需要处理的告警（active = 触发中）
const firingCount = computed(
  () => alerts.value.filter((a) => a.status && a.status.state === 'active').length
)

// Grafana 看板 uid 与 deploy/grafana-dashboard.json 一致；kiosk 模式隐藏侧栏只留面板
const grafanaEmbed = `http://${window.location.hostname}:3000/d/vmops-overview/?kiosk&refresh=15s`
const grafanaFull = `http://${window.location.hostname}:3000/d/vmops-overview/`

async function loadAlerts() {
  if (alertsLoading.value) return
  alertsLoading.value = true
  try {
    const res = await api.listAlerts()
    alerts.value = Array.isArray(res.data) ? res.data : []
    alertsError.value = false
  } catch (e) {
    // 静默置错误态：页面内提示如何启动监控栈，不弹全局 toast
    alertsError.value = true
  } finally {
    alertsLoading.value = false
  }
}

// 告警历史（webhook 入库数据）：筛选 + 分页，不随实时告警轮询（历史变化慢，手动刷新即可）
const history = ref([])
const historyLoading = ref(false)
const historyTotal = ref(0)
const historyPage = ref(1)
const historyPageSize = 20
const historyStatus = ref('')

async function loadHistory() {
  if (historyLoading.value) return
  historyLoading.value = true
  try {
    const res = await api.monitorAlertHistory({
      status: historyStatus.value || undefined,
      page: historyPage.value,
      page_size: historyPageSize
    })
    const data = res.data || {}
    history.value = Array.isArray(data.items) ? data.items : []
    historyTotal.value = data.total || 0
  } catch (e) {
    // 历史查询失败不打断页面（实时告警仍在上方正常展示）
    history.value = []
    historyTotal.value = 0
  } finally {
    historyLoading.value = false
  }
}

// 切换状态筛选后回到第一页再查
function onHistoryFilter() {
  historyPage.value = 1
  loadHistory()
}

let timer = null
onMounted(() => {
  loadAlerts()
  loadHistory()
  timer = setInterval(loadAlerts, getPollInterval('dashboard', POLL_DEFAULTS.dashboard))
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<style scoped>
.alert-card {
  margin-bottom: 16px;
}
/* 有待处理告警时卡片标红边，滚到看板下方也不会漏看 */
.alert-firing :deep(.el-card__header) {
  border-top: 2px solid var(--el-color-danger);
}
.alert-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-weight: 600;
}
.head-icon {
  vertical-align: -0.15em;
  margin-right: 4px;
  color: var(--el-color-primary);
}
.alert-head-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: normal;
}
.history-pager {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}
.grafana-frame {
  width: 100%;
  height: 720px;
  border: none;
  border-radius: 8px;
  background: #fff;
}
.grafana-fallback {
  padding: 24px 0;
}
</style>
