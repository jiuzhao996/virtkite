<template>
  <el-tooltip :content="copied ? '已复制' : tip" placement="top" :show-after="200">
    <el-button
      text
      :size="size"
      :icon="copied ? Check : CopyDocument"
      :aria-label="tip"
      class="copy-btn"
      @click="doCopy"
    />
  </el-tooltip>
</template>

<script setup>
// 通用一键复制钮：图标随成功态切换（Copy → Check 1.5s），剪贴板走 utils/clipboard 降级链
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Check, CopyDocument } from '@element-plus/icons-vue'
import { copyText } from '../utils/clipboard'

const props = defineProps({
  text: { type: String, required: true },
  tip: { type: String, default: '复制' },
  successMsg: { type: String, default: '' },
  size: { type: String, default: 'small' }
})

const copied = ref(false)
let timer = null

async function doCopy() {
  if (!props.text) {
    ElMessage.warning('暂无可复制的内容')
    return
  }
  const ok = await copyText(props.text)
  if (!ok) {
    ElMessage.error('复制失败，请手动选择文本复制')
    return
  }
  if (props.successMsg) ElMessage.success(props.successMsg)
  copied.value = true
  if (timer) clearTimeout(timer)
  timer = setTimeout(() => {
    copied.value = false
  }, 1500)
}
</script>

<style scoped>
.copy-btn {
  color: var(--color-muted-foreground);
}
.copy-btn:hover {
  color: var(--el-color-primary);
}
</style>
