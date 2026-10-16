<template>
  <!-- 存储容量区：dir 池 capacity 是文件系统级的，同盘多池口径完全相同，
       故只画一条总容量条（取容量最大的池），不再逐池重复同一条进度条 -->
  <div v-if="poolCap" class="pool-caps">
    <div class="pool-caps-head">
      <span class="pool-caps-title">存储容量</span>
      <span class="pool-caps-sub">全部 {{ poolCaps.length }} 个存储池共用同一文件系统，合并显示</span>
    </div>
    <div class="pool-cap-row">
      <span class="pool-cap-name">总计</span>
      <div class="pool-cap-track">
        <div class="pool-cap-fill" :style="{ width: poolCap.pct + '%', background: capColor(poolCap.pct) }" />
      </div>
      <span class="pool-cap-text mono">已用 {{ fmtSizeBytes(poolCap.used) }} / {{ fmtSizeBytes(poolCap.total) }}</span>
      <span class="pool-cap-pct mono" :style="{ color: capColor(poolCap.pct) }">{{ poolCap.pct }}%</span>
    </div>
  </div>
</template>

<script setup>
// 镜像页「存储容量」条：把各激活池的已用比例合并成一条总容量条（capColor 按占比变色）。
// 纯展示；poolCap 为空（无激活池 / 拉取失败）时不渲染（根 v-if，与拆前一致）。
import { fmtSizeBytes } from '../../../utils/format'

defineProps({
  // 代表池的容量 { used, total, pct }（页面取 total 最大的那条），null 时不渲染
  poolCap: { type: Object, default: null },
  // 全部激活池的容量列表（仅用于「全部 N 个存储池」文案）
  poolCaps: { type: Array, default: () => [] }
})

// 比例条配色：>90% 红 / >75% 橙 / 其余品牌青绿（与 VmList 状态条同源语义色，两主题各有限定值）
function capColor(pct) {
  if (pct > 90) return 'var(--color-danger)'
  if (pct > 75) return 'var(--color-warning)'
  return 'var(--color-primary)'
}
</script>

<style scoped>
/* 以下规则自 ImageList.vue 原样搬入（类名不变，DOM 不变） */
/* ── 存储池容量区（质感专项）：轻量区块（非 el-card），底色用 muted 令牌与下方列表卡区分层级 ── */
.pool-caps {
  background: var(--color-muted);
  border-radius: var(--radius-md);
  padding: var(--space-md) var(--space-xl);
  margin-bottom: var(--space-lg);
}
.pool-caps-head {
  display: flex;
  align-items: baseline;
  gap: var(--space-md);
  margin-bottom: var(--space-sm);
}
.pool-caps-title {
  font-size: 0.8rem;
  font-weight: 700;
  color: var(--color-foreground);
}
.pool-caps-sub {
  font-size: 0.75rem;
  color: var(--color-muted-foreground);
}
.pool-cap-row {
  display: flex;
  align-items: center;
  gap: var(--space-lg);
  padding: var(--space-sm) 0;
}
.pool-cap-name {
  width: 140px;
  flex-shrink: 0;
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--color-foreground);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
/* 槽底用边框族加深档：在 muted 面板底上两种主题都可辨（--color-border 过淡） */
.pool-cap-track {
  flex: 1;
  height: 10px;
  border-radius: 999px;
  background: var(--color-border-strong);
  overflow: hidden;
}
.pool-cap-fill {
  height: 100%;
  border-radius: 999px;
  transition: width var(--dur-base) var(--ease-standard);
}
.pool-cap-text {
  flex-shrink: 0;
  font-size: 0.8rem;
  color: var(--color-muted-foreground);
}
.pool-cap-pct {
  flex-shrink: 0;
  width: 44px;
  text-align: right;
  font-size: 0.8rem;
  font-weight: 700;
}
</style>
