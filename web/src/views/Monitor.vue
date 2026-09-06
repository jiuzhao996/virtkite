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
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
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

let timer = null
onMounted(() => {
  loadAlerts()
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
