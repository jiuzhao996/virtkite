<template>
  <div class="page-head">
    <div>
      <h2 class="page-title">{{ title }}</h2>
      <!-- 副标题默认渲染为 span.page-desc；历史上有页面用 p.page-desc（p 的 UA 外边距参与布局，
           不可悄悄换成 span），这类页面用 #subtitle 插槽原样传 p 保持渲染不变 -->
      <slot name="subtitle">
        <span v-if="subtitle" class="page-desc">{{ subtitle }}</span>
      </slot>
    </div>
    <!-- 默认插槽 = 右侧动作区（按钮 / 按钮组 / 开关等），由 global .page-head 两端对齐分列 -->
    <slot />
  </div>
</template>

<script setup>
// 页头骨架：左「标题 + 描述」、右动作区。样式全部走 global.css 的
// .page-head / .page-title / .page-desc（原各列表页 scoped 逐字重复，已收口），本组件不重复定义。
// 标题统一 h2：.page-title 已覆盖 h2/h3 的 UA 差异（margin:0 / 1.1rem / 700），
// 全局 h1~h4 规则对两级行为一致，h3 页面迁移后视觉不变。
defineProps({
  title: { type: String, required: true },
  subtitle: { type: String, default: '' },
})
</script>
