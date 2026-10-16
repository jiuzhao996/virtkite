<template>
  <!-- 告警历史卡：Alertmanager webhook 推回平台入库的追溯数据（状态筛选 + 分页） -->
  <el-card shadow="never" class="alert-card">
    <template #header>
      <div class="alert-head">
        <span><el-icon class="head-icon"><AlarmClock /></el-icon>告警历史</span>
        <div class="alert-head-actions">
          <el-select v-model="status" style="width: 130px" @change="emit('filter-change')">
            <el-option label="全部状态" value="" />
            <el-option label="触发中" value="firing" />
            <el-option label="已恢复" value="resolved" />
          </el-select>
          <el-button :icon="Refresh" @click="emit('refresh')">刷新</el-button>
        </div>
      </div>
    </template>

    <el-table :data="history" v-loading="historyLoading" stripe>
      <template #empty><el-empty description="暂无告警历史记录" :image-size="80" /></template>
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
            effect="light" size="small"
          >
            {{ (row.labels && row.labels.severity) === 'critical' ? '严重' : (row.labels && row.labels.severity) === 'warning' ? '警告' : (row.labels && row.labels.severity) || '—' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="虚拟机" width="140">
        <template #default="{ row }">
          <span class="mono">{{ (row.labels && row.labels.vm) || '—' }}</span>
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
        v-model:current-page="page"
        :page-size="historyPageSize"
        :total="historyTotal"
        layout="total, prev, pager, next"
        @current-change="emit('page-change')"
      />
    </div>
  </el-card>
</template>

<script setup>
// 监控中心「告警历史」卡：webhook 入库的历史告警，支持状态筛选 + 分页。
// 数据全由页面传入；筛选/翻页/刷新经 emit 回页面（页面调 loadHistory / onHistoryFilter）。
// status（筛选值）与 page（当前页）用 v-model 双向绑定页面状态，保持单一状态源在页面。
import { AlarmClock, Refresh } from '@element-plus/icons-vue'
import { fmtDateTime } from '../../../utils/format'

defineProps({
  history: { type: Array, default: () => [] },
  historyLoading: { type: Boolean, default: false },
  historyTotal: { type: Number, default: 0 },
  historyPageSize: { type: Number, default: 20 }
})
const emit = defineEmits(['refresh', 'page-change', 'filter-change'])
const status = defineModel('status', { type: String, default: '' }) // 父级 v-model:status="historyStatus"
const page = defineModel('page', { type: Number, default: 1 })      // 父级 v-model:page="historyPage"
</script>

<style scoped>
/* 与 Monitor.vue / RealtimeAlertsCard.vue / FileSDCard.vue 各留一份（scoped 不跨组件）——
   改一处须同步 */
.alert-card {
  margin-bottom: 16px;
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
.head-icon {
  vertical-align: -0.15em;
  margin-right: 4px;
  color: var(--el-color-primary);
}
.history-pager {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}
</style>
