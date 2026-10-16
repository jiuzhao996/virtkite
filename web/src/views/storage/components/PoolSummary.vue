<template>
  <!-- 汇总条：物理容量 / cloud-init 种子目录 / 默认存储池 -->
  <!-- dir 池容量是文件系统级的，同盘多池口径相同，取 items 里最大 capacity 及其 available，不再每行重复 -->
  <el-card shadow="never" class="summary-card">
    <div class="summary-row">
      <div class="summary-item summary-cap">
        <div class="summary-label">物理容量</div>
        <template v-if="physCap.capacity">
          <div class="summary-value">
            {{ fmtSizeBytes(physCap.capacity) }}
            <span class="summary-sub">可用 {{ fmtSizeBytes(physCap.available) }}</span>
          </div>
          <el-progress
            :percentage="physPct"
            :color="usageColor(physPct)"
            :stroke-width="8"
            :show-text="false"
          />
        </template>
        <div v-else class="summary-sub">暂无存储池数据</div>
      </div>
      <div class="summary-item">
        <div class="summary-label">cloud-init 种子目录</div>
        <div class="summary-value mono summary-path" :title="seedDir.path">{{ seedDir.path || '—' }}</div>
        <div class="summary-sub">{{ seedDir.count }} 个 *-seed.iso 种子文件（每台 VM 一份）</div>
      </div>
      <div class="summary-item">
        <div class="summary-label">默认存储池</div>
        <div class="summary-value">{{ defaultPool || '—' }}</div>
        <div class="summary-sub">创建虚拟机未指定池时的落点</div>
      </div>
    </div>
  </el-card>
</template>

<script setup>
// 存储管理页顶部「汇总条」：物理容量条 + cloud-init 种子目录 + 默认存储池。
// 纯展示，数据全部由页面算好后传入（physCap/physPct 来自 pools 聚合，见 StorageList.vue）。
import { fmtSizeBytes, usageColor } from '../../../utils/format'

defineProps({
  // 物理容量 { capacity, available }（页面取最大池的口径）
  physCap: { type: Object, required: true },
  // 已用百分比 0~100（页面 clampPct 钳制后传入）
  physPct: { type: Number, default: 0 },
  // cloud-init 种子目录 { path, count }
  seedDir: { type: Object, default: () => ({ path: '', count: 0 }) },
  // 默认存储池名（空串→显示 —）
  defaultPool: { type: String, default: '' }
})
</script>

<style scoped>
/* 以下 .summary-* 规则自 StorageList.vue 原样搬入（类名不变，DOM 不变） */
.summary-card {
  margin-bottom: 16px;
}
.summary-row {
  display: flex;
  align-items: flex-start;
  flex-wrap: wrap;
  gap: 16px 48px;
}
.summary-item {
  min-width: 180px;
}
.summary-cap {
  flex: 1;
  min-width: 240px;
  max-width: 380px;
}
.summary-label {
  font-size: 0.8rem;
  color: var(--color-muted-foreground);
  margin-bottom: 4px;
}
.summary-value {
  font-size: 1.05rem;
  font-weight: 700;
  color: var(--color-foreground);
  margin-bottom: 6px;
}
.summary-path {
  font-size: 0.85rem;
  max-width: 340px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.summary-sub {
  font-size: 0.8rem;
  color: var(--color-muted-foreground);
}
</style>
