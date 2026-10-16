<template>
  <!-- 漂移检查结果面板（画布期望 vs 基线 vs 平台现实的三方对比结论） -->
  <div class="ds-drift" :class="driftResult.drifted ? 'warn' : 'ok'">
    <b>漂移检查</b>
    <span v-if="!driftResult.has_snap">尚无基线——成功落地一次后才有对比基准</span>
    <span v-else-if="!driftResult.drifted">与现实一致</span>
    <span v-else>
      {{ driftResult.items.length }} 处差异
      （<b>{{ driftResult.pending || 0 }}</b> 处画布未落地 · <b>{{ driftResult.external || 0 }}</b> 处现实被外部改动）
    </span>
    <div v-for="(it, i) in driftResult.items" :key="i" class="ds-drift-item">
      <el-tag size="small" :type="actionMeta(it.action).type" effect="light">{{ actionMeta(it.action).label }}</el-tag>
      <span class="mono">{{ it.node }}</span>
      <span class="ds-drift-side">{{ it.side === 'pending' ? '画布改动' : '外部改动' }}</span>
      — {{ it.reason || it.detail }}
    </div>
    <div v-if="driftResult.drifted" class="ds-drift-actions">
      <el-button size="small" type="primary" @click="emit('reconcile')">按差异收敛</el-button>
      <el-button size="small" @click="emit('import-reality')">接纳实况（按现状重画）</el-button>
    </div>
  </div>
</template>

<script setup>
// 架构设计器「漂移检查」结果区：展示后端三方对比（reconcile-plan）返回的
// drifted / has_snap / items 等字段，逐项列出动作枚举 + 理由。纯展示，动作由父级处理。
// 根 div 的 v-if="driftResult" 由父级在标签上控制，本组件只在有结果时被挂载。
defineProps({
  // api.designerDrift 的 data：{ has_snap, drifted, pending, external, items:[...] }
  driftResult: { type: Object, required: true },
  // 动作枚举 → 展示元信息（标签/颜色/tag 类型）的翻译函数，由页面传入以复用 ACTION_META
  actionMeta: { type: Function, required: true }
})
const emit = defineEmits(['reconcile', 'import-reality'])
</script>

<style scoped>
/* 以下 .ds-drift* 规则自 DesignerPage.vue 原样搬入（类名不变，DOM 不变） */
.ds-drift {
  margin-top: 12px;
  padding: 10px 12px;
  border-radius: 8px;
  font-size: 0.85rem;
  border: 1px solid var(--color-border);
}
.ds-drift.ok {
  background: #f0f9f2;
  border-color: #b7e0c4;
}
.ds-drift.warn {
  background: #fff8e6;
  border-color: #f0d9a0;
}
.ds-drift-item {
  margin-top: 4px;
  font-size: 0.78rem;
  color: var(--color-muted-foreground);
  display: flex;
  align-items: center;
  gap: 6px;
}
.ds-drift-side {
  font-size: 0.72rem;
  color: var(--color-muted-foreground);
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 0 4px;
}
.ds-drift-actions {
  margin-top: 10px;
  display: flex;
  gap: 8px;
}
</style>
