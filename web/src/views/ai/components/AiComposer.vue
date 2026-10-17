<template>
  <!-- 输入区：Enter 发送、Shift+Enter 换行；streaming 时发送钮切换为停止钮。 -->
  <div class="input-bar">
    <el-input
      ref="inputRef"
      :model-value="modelValue"
      type="textarea"
      :autosize="{ minRows: 2, maxRows: 5 }"
      resize="none"
      placeholder="输入问题，Enter 发送，Shift+Enter 换行"
      @update:model-value="emit('update:modelValue', $event)"
      @keydown.enter.exact="onEnterKey"
    />
    <!-- streaming 时发送钮切换为停止钮：中断 SSE 流，当前回答尾注「（已停止）」 -->
    <el-button
      v-if="streaming"
      type="warning"
      class="send-btn"
      :icon="VideoPause"
      @click="emit('stop')"
    >停止</el-button>
    <el-button
      v-else
      type="primary"
      class="send-btn"
      :icon="Promotion"
      :disabled="!canSend"
      @click="emit('send')"
    >发送</el-button>
  </div>
</template>

<script setup>
// AI 输入区：受控输入 + 发送/停止按钮。输入框 ref 内聚于此，父级通过 focus() 在发送后回焦。
import { ref } from 'vue'
import { Promotion, VideoPause } from '@element-plus/icons-vue'

defineProps({
  modelValue: { type: String, default: '' },
  streaming: { type: Boolean, default: false },
  canSend: { type: Boolean, default: false }
})

const emit = defineEmits(['update:modelValue', 'send', 'stop'])

const inputRef = ref(null)

function onEnterKey(e) {
  // 中文输入法选词的回车不发送（isComposing + keyCode 229 兜底旧版 Safari）
  if (e.isComposing || e.keyCode === 229) return
  e.preventDefault()
  emit('send')
}

function focus() {
  inputRef.value && inputRef.value.focus()
}

defineExpose({ focus })
</script>

<style scoped>
.input-bar {
  display: flex;
  align-items: flex-end;
  gap: 12px;
  margin-top: 12px;
}
.send-btn {
  flex: none;
  margin-bottom: 4px;
}
</style>
