<template>
  <!-- 实时告警卡：Alertmanager 当前 firing 列表（firingCount=0 时显示空态，接口失败显示告警条） -->
  <el-card shadow="never" class="alert-card" :class="{ 'alert-firing': firingCount }">
    <template #header>
      <div class="alert-head">
        <span>实时告警</span>
        <div class="alert-head-actions">
          <el-tag v-if="!alertsError" :type="firingCount ? 'danger' : 'success'" effect="light" size="small">
            {{ firingCount ? `${firingCount} 条待处理` : '当前无告警' }}
          </el-tag>
          <el-button :icon="Refresh" :loading="alertsLoading" @click="emit('refresh')">刷新</el-button>
        </div>
      </div>
    </template>

    <el-alert
      v-if="alertsError"
      type="warning"
      :closable="false"
      show-icon
      title="监控栈未连接"
      description="无法访问 Alertmanager（典型原因：docker compose 未启动监控栈）。执行 docker compose up -d 启动 Prometheus / Alertmanager 后刷新本页。"
    />

    <el-table v-else-if="firingCount" :data="alerts" v-loading="alertsLoading" stripe>
      <el-table-column label="级别" width="90">
        <template #default="{ row }">
          <el-tag :type="row.labels && row.labels.severity === 'critical' ? 'danger' : 'warning'" effect="light" size="small">
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
</template>

<script setup>
// 监控中心「实时告警」卡：展示 Alertmanager 返回的 firing 告警（含「监控栈未连接」错误态）。
// 纯展示；刷新由页面通过 @refresh 触发（loadAlerts）。
import { Refresh } from '@element-plus/icons-vue'
import { fmtDateTime } from '../../../utils/format'

defineProps({
  // Alertmanager 告警数组（原始结构：labels/annotations/status/startsAt）
  alerts: { type: Array, default: () => [] },
  alertsLoading: { type: Boolean, default: false },
  // 拉取失败（Alertmanager 不可达）→ 显示「监控栈未连接」提示条
  alertsError: { type: Boolean, default: false },
  // 需要处理的告警数（status.state === 'active'）
  firingCount: { type: Number, default: 0 }
})
const emit = defineEmits(['refresh'])
</script>

<style scoped>
/* 以下 .alert-card/.alert-firing/.alert-head/.alert-head-actions 与页面及其它卡片组件各留一份
   （scoped 样式不跨组件生效）——改一处须同步 Monitor.vue / AlertHistoryCard.vue / FileSDCard.vue */
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
.alert-head-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: normal;
  flex-wrap: wrap;
  justify-content: flex-end;
}
</style>
