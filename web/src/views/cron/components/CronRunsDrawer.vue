<template>
  <!-- 执行历史抽屉（50% 宽）：GET /crons/:id/runs 分页倒序 -->
  <el-drawer v-model="visible" size="50%">
    <template #header>
      <div class="runs-head">
        <span class="runs-title">{{ title }}</span>
        <el-button
          size="small" type="primary" :icon="VideoPlay"
          :loading="rerunning"
          @click="emit('rerun')"
        >再跑一次</el-button>
      </div>
    </template>
    <el-table v-loading="runsLoading" :data="runs" stripe size="small">
      <template #empty>
        <EmptyState description="该任务还没有执行记录" />
      </template>
      <el-table-column label="开始时间" width="170">
        <template #default="{ row }">
          <span class="mono">{{ fmtDateTime(row.started_at) }}</span>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="90" align="center">
        <template #default="{ row }">
          <el-tag :type="runStatusTag(row.status)" effect="light" size="small">
            {{ runStatusText(row.status) }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="耗时" width="90" align="center">
        <template #default="{ row }"><span class="mono">{{ runDuration(row) }}</span></template>
      </el-table-column>
      <el-table-column label="尝试" width="60" align="center">
        <template #default="{ row }">{{ row.attempt || 1 }}</template>
      </el-table-column>
      <el-table-column label="结果摘要" min-width="220" show-overflow-tooltip>
        <template #default="{ row }">{{ row.output || '—' }}</template>
      </el-table-column>
    </el-table>
    <el-pagination
      class="runs-pager"
      layout="total, prev, pager, next"
      size="small"
      :total="runsTotal"
      :page-size="runsPageSize"
      v-model:current-page="page"
      @current-change="emit('page-change')"
    />
  </el-drawer>
</template>

<script setup>
// 计划任务「执行历史」抽屉：分页倒序展示某任务的执行记录（状态/耗时/尝试/结果摘要）。
// 数据与分页状态由页面（usePagination）持有；翻页 emit('page-change')、再跑一次 emit('rerun')。
// runStatusTag/runStatusText/runDuration 为纯函数搬入——runStatusTag/runStatusText 与页面
// 主表共用（各留一份），改一处须同步。
import { VideoPlay } from '@element-plus/icons-vue'
import { fmtDateTime } from '../../../utils/format'
import EmptyState from '../../../components/EmptyState.vue'

defineProps({
  // 抽屉标题（页面按当前任务名拼好传入）
  title: { type: String, default: '执行历史' },
  runs: { type: Array, default: () => [] },
  runsLoading: { type: Boolean, default: false },
  runsTotal: { type: Number, default: 0 },
  runsPageSize: { type: Number, default: 20 },
  // 「再跑一次」按钮 loading（页面按 runningId === 当前任务 id 计算）
  rerunning: { type: Boolean, default: false }
})
const emit = defineEmits(['rerun', 'page-change'])
const visible = defineModel({ type: Boolean, default: false }) // 父级 v-model="drawerVisible"
const page = defineModel('page', { type: Number, default: 1 })  // 父级 v-model:page="runsPage"

// 执行状态 → 中文 / tag 颜色（success 绿 / failed 红 / running 蓝）——⚠️ 与 CronList.vue 同步
function runStatusText(s) {
  return { success: '成功', failed: '失败', running: '执行中', skipped: '跳过' }[s] || s || '未知'
}
function runStatusTag(s) {
  return { success: 'success', failed: 'danger', running: 'primary', skipped: 'warning' }[s] || 'info'
}

// 执行耗时：由 started_at / finished_at 计算（进行中显示 —）
function runDuration(row) {
  if (!row.finished_at) return '—'
  const ms = new Date(row.finished_at) - new Date(row.started_at)
  if (!isFinite(ms) || ms < 0) return '—'
  if (ms < 1000) return ms + ' ms'
  const s = Math.round(ms / 1000)
  if (s < 60) return s + ' 秒'
  return Math.floor(s / 60) + ' 分' + (s % 60) + ' 秒'
}
</script>

<style scoped>
/* 以下规则自 CronList.vue 原样搬入（类名不变，DOM 不变） */
.runs-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.runs-title {
  font-weight: 600;
}
.runs-pager {
  margin-top: 12px;
  justify-content: flex-end;
}
</style>
