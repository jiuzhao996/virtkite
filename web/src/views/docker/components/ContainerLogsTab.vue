<template>
  <!-- 容器「日志」tab：WS 实时流状态条 + LogViewer（WS 增量 / HTTP 降级文本二选一）。
       流生命周期与轮询在抽屉壳，本组件只负责渲染与把工具栏事件上抛。 -->
  <div v-loading="logsLoading">
    <div v-if="logsMode === 'ws' || wsFallback" class="ls-bar">
      <span class="ls-dot" :class="dotClass">●</span>
      <span>{{ wsStatusText }}</span>
    </div>
    <LogViewer
      ref="viewerRef"
      :text="logsMode === 'http' ? logsText : ''"
      :rows="logsMode === 'ws' ? streamLines : null"
      :loading="logsLoading"
      :follow="logsFollow"
      :timestamps="logsTimestamps"
      :filename="displayName"
      @update:follow="emit('update:follow', $event)"
      @update:timestamps="emit('timestamps-change', $event)"
      @refresh="emit('refresh')"
    />
  </div>
</template>

<script setup>
// 容器日志 tab：LogViewer 的薄包装；对外暴露 scrollToEndOnce 供壳在首拉/切 tab 时贴底。
import { ref } from 'vue'
import LogViewer from './LogViewer.vue'

defineProps({
  logsLoading: { type: Boolean, default: false },
  logsMode: { type: String, default: 'ws' },
  wsFallback: { type: Boolean, default: false },
  dotClass: { type: String, default: 'off' },
  wsStatusText: { type: String, default: '' },
  logsText: { type: String, default: '' },
  streamLines: { type: Array, default: null },
  logsFollow: { type: Boolean, default: true },
  logsTimestamps: { type: Boolean, default: false },
  displayName: { type: String, default: '' }
})

const emit = defineEmits(['update:follow', 'timestamps-change', 'refresh'])

const viewerRef = ref(null)

function scrollToEndOnce() {
  viewerRef.value?.scrollToEndOnce()
}

defineExpose({ scrollToEndOnce })
</script>

<style scoped>
/* 日志流状态条（WS 实时/降级提示） */
.ls-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 6px;
  font-size: 0.82rem;
  color: var(--el-text-color-secondary, #909399);
}
.ls-dot {
  font-size: 0.7rem;
}
.ls-dot.ok { color: #67c23a; }
.ls-dot.wait { color: #e6a23c; }
.ls-dot.off { color: #909399; }
.ls-dot.err { color: #f56c6c; }
</style>
