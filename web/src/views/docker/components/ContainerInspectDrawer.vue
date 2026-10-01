<template>
    <!-- 容器详情抽屉：docker inspect 原始 JSON，深色等宽展示 -->
    <el-drawer v-model="inspectDrawer" :title="'容器详情 — ' + inspectName" size="55%">
      <div v-loading="inspectLoading">
        <div class="logs-toolbar">
          <el-button size="small" :icon="Refresh" :loading="inspectLoading" @click="fetchInspect">刷新</el-button>
          <CopyButton :text="inspectText" tip="复制详情 JSON" success-msg="详情 JSON 已复制到剪贴板" />
          <span class="logs-hint">docker inspect 原始 JSON</span>
        </div>
        <pre class="logs-pre">{{ inspectText || '（暂无数据）' }}</pre>
      </div>
    </el-drawer>
</template>

<script setup>
// 容器详情抽屉：docker inspect 原始 JSON，取数与复制自持，经 open(row) 由容器表格行触发。

// 这是拆分前就存在的现状，为守住「零行为变化」红线原样保留；后续可单独评估接入 CopyButton。
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import CopyButton from '../../../components/CopyButton.vue'
import { api } from '../../../api'
import { errMsg } from '../../../utils/format'
import { containerName } from '../../../utils/docker-format'

const inspectDrawer = ref(false)
const inspectLoading = ref(false)
const inspectText = ref('')
const inspectName = ref('')
const inspectId = ref('')

function open(row) {
  inspectId.value = row.ID
  inspectName.value = containerName(row.Names)
  inspectText.value = ''
  inspectDrawer.value = true
  fetchInspect()
}

async function fetchInspect() {
  if (!inspectId.value) return
  inspectLoading.value = true
  try {
    const res = await api.dockerContainerInspect(inspectId.value)
    inspectText.value = JSON.stringify(res.data ?? {}, null, 2)
  } catch (e) {
    ElMessage.error(errMsg(e, '获取容器详情失败'))
  } finally {
    inspectLoading.value = false
  }
}

defineExpose({ open })
</script>

<style scoped>
.logs-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.logs-hint {
  color: var(--el-text-color-secondary, #909399);
  font-size: 0.8rem;
}
.logs-pre {
  margin: 0;
  padding: 12px;
  min-height: 300px;
  max-height: calc(100vh - 220px);
  overflow: auto;
  background: #0d1b2a;
  color: #cfe8ff;
  border-radius: var(--radius-md, 8px);
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
  font-size: 0.8rem;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
  user-select: text;
}
</style>
