<template>
  <div class="toolbar">
    <!-- 左右容器仅在有内容时渲染（$slots 判断），避免空 wrapper 参与两端对齐布局；
         未具名内容（如 .count 计数、无容器直排元素）保持 .toolbar 直接子元素原样渲染 -->
    <div v-if="$slots.left" class="toolbar-left" :style="leftStyle">
      <slot name="left" />
    </div>
    <slot />
    <div v-if="$slots.right" class="toolbar-right" :style="rightStyle">
      <slot name="right" />
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'

// 列表页工具条骨架：外层 .toolbar（global.css 两端对齐）+ 左右容器。
// 各页 scoped 的 .toolbar-left/.toolbar-right 历史上有两种档位：
//   gap：8px（= var(--space-md)）与 12px（= var(--space-lg)）；flex-wrap：有/无。
// global.css 刻意只收「完全一致」的规则，差异部分改由 props 注入，迁移时按各页原值传入。
const props = defineProps({
  // 左容器间隙；默认 8px（VmList/TaskList/SessionList/CronList/Topology 档）
  gap: { type: String, default: 'var(--space-md)' },
  // 左容器是否允许换行（窄屏筛选控件多时原页有 flex-wrap: wrap）
  wrap: { type: Boolean, default: false },
  // 右容器间隙；默认 8px
  rightGap: { type: String, default: 'var(--space-md)' },
  rightWrap: { type: Boolean, default: false },
})

const leftStyle = computed(() => ({ gap: props.gap, flexWrap: props.wrap ? 'wrap' : undefined }))
const rightStyle = computed(() => ({ gap: props.rightGap, flexWrap: props.rightWrap ? 'wrap' : undefined }))
</script>

<style scoped>
/* flex 骨架本组件承担；gap/换行因页而异，走 props（:style）注入 */
.toolbar-left,
.toolbar-right {
  display: flex;
  align-items: center;
}
</style>
