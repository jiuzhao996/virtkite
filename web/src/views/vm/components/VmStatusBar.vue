<template>
  <!-- 状态分布堆叠色条（质感专项）：按列表全量数据的各状态占比分段；
       图例 chip 点击写回状态筛选（emit toggle，与状态下拉同一状态源），再点一次取消 -->
  <div class="status-bar-block">
    <div class="status-bar" role="img" :aria-label="statusBarAria">
      <div
        v-for="seg in dist"
        :key="seg.status"
        class="status-bar-seg"
        :style="{ flexGrow: seg.count, flexBasis: 0, background: seg.color }"
        :title="`${seg.label} ${seg.count} 台（${seg.pct}%）`"
      />
    </div>
    <div class="status-legend">
      <button
        v-for="seg in dist"
        :key="seg.status"
        type="button"
        class="legend-chip"
        :class="{ active: active === seg.status }"
        :aria-pressed="active === seg.status"
        :title="active === seg.status ? '再次点击取消该状态筛选' : '点击只看' + seg.label"
        @click="$emit('toggle', seg.status)"
      >
        <span class="legend-dot" :style="{ background: seg.color }" />
        <span>{{ seg.label }}</span>
        <span class="legend-count">{{ seg.count }}</span>
      </button>
    </div>
  </div>
</template>

<script setup>
// 状态分布堆叠色条（从 VmList 拆出）：纯展示组件，dist 由父级按列表全量统计，
// active 为当前状态筛选值（高亮命中段，点击 toggle 语义由父级决定）
import { computed } from 'vue'

const props = defineProps({
  dist: { type: Array, default: () => [] },
  active: { type: String, default: '' }
})

defineEmits(['toggle'])

// 色条 role=img 无文本内容，读屏描述走 aria-label
const statusBarAria = computed(() => '虚拟机状态分布：' + props.dist.map((s) => `${s.label} ${s.count} 台`).join('，'))
</script>

<style scoped>
.status-bar-block {
  margin-bottom: var(--space-xl);
}
/* 条体：高 10px 圆角胶囊，段间 2px 缝露出卡片底色；段宽按各状态计数比例分配（flex-grow=计数） */
.status-bar {
  display: flex;
  gap: 2px;
  height: 10px;
  border-radius: 999px;
  overflow: hidden;
}
.status-bar-seg {
  min-width: 6px; /* 单台残留：占比极小的段仍保持可见 */
  transition: flex-grow var(--dur-base) var(--ease-standard);
}
.status-legend {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-md);
  margin-top: var(--space-md);
}
/* 图例 chip：原生 button 保键盘可达（焦点环走 global :focus-visible）；激活高亮与状态下拉同源 */
.legend-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 3px 10px;
  border: 1px solid var(--color-border);
  border-radius: 999px;
  background: transparent;
  color: var(--color-muted-foreground);
  font-size: 0.8rem;
  font-family: inherit;
  line-height: 1.5;
  cursor: pointer;
  transition: color var(--dur-fast) var(--ease-standard), border-color var(--dur-fast) var(--ease-standard), background-color var(--dur-fast) var(--ease-standard);
}
.legend-chip:hover {
  border-color: var(--color-border-strong);
  color: var(--color-foreground);
}
.legend-chip.active {
  border-color: var(--el-color-primary);
  color: var(--el-color-primary);
  background: var(--el-color-primary-light-9);
}
.legend-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
  box-shadow: inset 0 0 0 1px var(--color-border); /* 灰色圆点在浅底上的最低可见度兜底 */
}
.legend-count {
  font-family: var(--font-mono);
  font-weight: 700;
}
</style>
