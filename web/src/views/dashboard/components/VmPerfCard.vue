<template>
  <el-card shadow="hover">
    <template #header>
      <span class="card-title">VM 实时性能</span>
      <span class="update-time">运行中虚拟机展示 CPU / 内存占用</span>
    </template>
    <el-table :data="items" size="small" class="perf-table" empty-text="暂无运行中虚拟机">
      <el-table-column label="名称" min-width="220" show-overflow-tooltip>
        <template #default="{ row }">
          <!-- 名称可点直达 VM 详情（列表页卡片「详情」同目标） -->
          <el-link type="primary" :underline="false" @click="$router.push({ name: 'vm-detail', params: { id: row.id } })">
            <span class="vm-name">
              <el-icon class="vm-icon"><Monitor /></el-icon>
              {{ row.name }}
            </span>
          </el-link>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="120">
        <template #default="{ row }">
          <el-tag :type="vmStatusTag(row.status)" effect="light" size="small" round>{{ vmStatusText(row.status) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="CPU" min-width="220" prop="cpu_percent" sortable>
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
      <el-table-column label="内存" min-width="220" prop="mem_pct" sortable>
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
</template>

<script setup>
// VM 实时性能表卡（自 Dashboard.vue 拆出，渲染输出不变）。
// 纯展示组件：vmPerf 数组由 shell pollVms 轮询后经 props 下发。
import { Monitor } from '@element-plus/icons-vue'
import { vmStatusText, vmStatusTag, usageColor, clampPct } from '../../../utils/format'

defineProps({
  items: { type: Array, required: true } // 运行中虚拟机性能行（含非 running 展示「—」）
})
</script>

<style scoped>
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
</style>
