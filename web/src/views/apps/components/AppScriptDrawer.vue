<template>
  <!-- 安装脚本预览抽屉：Detect / Install 两段。由父级通过 defineExpose.open(app) 打开。 -->
  <el-drawer v-model="visible" :title="'安装脚本 — ' + (app ? app.name : '')" size="50%">
    <div v-loading="loading">
      <h4 class="script-title">检测脚本（detect）</h4>
      <pre class="script-pre">{{ data.detect || '（无）' }}</pre>
      <h4 class="script-title">安装脚本（install）</h4>
      <pre class="script-pre">{{ data.install || '（无）' }}</pre>
    </div>
  </el-drawer>
</template>

<script setup>
// 应用脚本预览抽屉：按 app.id 拉取 detect/install 两段脚本并只读展示。
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { api } from '../../../api'
import { errMsg } from '../../../utils/format'

const visible = ref(false)
const loading = ref(false)
const app = ref(null)
const data = ref({ detect: '', install: '' })

async function open(target) {
  app.value = target
  data.value = { detect: '', install: '' }
  visible.value = true
  loading.value = true
  try {
    const res = await api.appDetail(target.id)
    const d = res.data || {}
    data.value = { detect: d.detect || '', install: d.install || '' }
  } catch (e) {
    ElMessage.error(errMsg(e, '获取脚本失败'))
  } finally {
    loading.value = false
  }
}

defineExpose({ open })
</script>

<style scoped>
.script-title {
  margin: 12px 0 8px;
  font-size: 0.9rem;
}
.script-pre {
  margin: 0;
  padding: 12px;
  background: #0d1b2a;
  color: #cfe8ff;
  border-radius: var(--radius-md, 8px);
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
  font-size: 0.8rem;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
  user-select: text;
  overflow: auto;
}
</style>
