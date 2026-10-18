<template>
  <!-- 统一空态：收敛各页 image-size 48~90 七种取值（此前纯视觉不一致）。
       文案保持业务特定——由调用方传，本组件只统一尺寸与插槽能力。 -->
  <el-empty :description="description" :image-size="imageSize">
    <template v-if="$slots.default" #default>
      <slot />
    </template>
  </el-empty>
</template>

<script setup>
// 空态壳子：description 必传（空态必须说清「为什么空、下一步做什么」），
// imageSize 默认 80（全仓库出现最多的取值），个别页面可显式覆盖。
defineProps({
  description: { type: String, required: true },
  imageSize: { type: Number, default: 80 },
})
</script>
