<template>
  <!-- 通用键值对列表（任务详情抽屉的「任务参数」与「执行结果」共用） -->
  <div class="kv-list">
    <div v-for="kv in kvs" :key="kv.k" class="kv-row">
      <div class="kv-key mono">{{ labelFn(kv.k) }}</div>
      <div class="kv-val">
        <!-- 敏感键：值固定打码；长文本 / 嵌套对象：等宽块限高滚动；其余：普通文本 -->
        <span v-if="kv.masked" class="mono kv-masked">******</span>
        <pre v-else-if="kv.pre" class="kv-pre mono">{{ kv.text }}</pre>
        <span v-else class="kv-text">{{ kv.text }}</span>
      </div>
    </div>
  </div>
</template>

<script setup>
// 任务详情抽屉里的「键值对」渲染器：把 buildKV() 归一出来的
// [{k, text, masked, pre}] 逐条铺成一行「中文键名（可选原键名）+ 值」。
// 结构与 class 与原 TaskList.vue 内联块逐字一致，仅把 kvs 与键名翻译函数抽成 props。
defineProps({
  // 已归一化的键值对数组，元素形如 { k, text?, masked?, pre? }
  kvs: { type: Array, default: () => [] },
  // 原始键名 → 中文标签（未命中回显原键名），由页面传入以复用 RESULT_KEY_TEXT 映射
  labelFn: { type: Function, default: (k) => k }
})
</script>

<style scoped>
/* 以下 .kv-* 规则自 TaskList.vue 原样搬入（类名不变，DOM 不变） */
.kv-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.kv-row {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 8px;
  border-radius: var(--radius-sm);
  background: var(--el-fill-color-light);
}
.kv-key {
  font-size: 12px;
  color: var(--color-muted-foreground);
}
.kv-val {
  font-size: 13px;
  word-break: break-all;
}
.kv-pre {
  margin: 0;
  padding: 8px;
  max-height: 240px;
  overflow: auto;
  background: var(--el-fill-color);
  border-radius: var(--radius-sm);
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
}
.kv-masked {
  color: var(--color-muted-foreground);
  letter-spacing: 2px;
}
</style>
