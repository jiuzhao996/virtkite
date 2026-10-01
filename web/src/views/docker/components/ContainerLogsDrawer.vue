<template>
    <!-- 容器日志抽屉：深色背景等宽展示，支持 tail 行数切换 / 跟随滚动 / 复制 / 下载 -->
    <el-drawer v-model="logsDrawer" :title="'容器日志 — ' + logsName" size="55%">
      <div v-loading="logsLoading">
        <div class="logs-toolbar">
          <el-button size="small" :icon="Refresh" :loading="logsLoading" @click="fetchLogs">刷新</el-button>
          <el-select v-model="logsTail" class="logs-tail" @change="onTailChange">
            <!-- 后端将 tail 钳制到 [200, 2000]，无法真正「不限行数」，「全部」即后端支持的 2000 行上限 -->
            <el-option label="全部（2000 行）" :value="2000" />
            <el-option label="100 行" :value="100" />
            <el-option label="200 行" :value="200" />
            <el-option label="500 行" :value="500" />
            <el-option label="1000 行" :value="1000" />
          </el-select>
          <div class="ct-auto" title="开启后每 2 秒自动拉取新日志，滚动贴底时自动滚到最新">
            <el-switch v-model="logsFollow" size="small" />
            <span class="ct-auto-label">跟随</span>
          </div>
          <el-button size="small" :icon="Download" @click="downloadLogs">下载</el-button>
          <CopyButton :text="logsText" tip="复制日志" success-msg="日志已复制到剪贴板" />
        </div>
        <pre ref="logsPreRef" class="logs-pre">{{ logsText || '（暂无日志输出）' }}</pre>
      </div>
    </el-drawer>
</template>

<script setup>
// 容器日志抽屉：tail 行数切换 / 2s 跟随轮询 / 复制 / 下载全部自持，经 open(row) 由容器表格行触发。

// 这是拆分前就存在的现状，为守住「零行为变化」红线原样保留；后续可单独评估接入 CopyButton。
import { ref, nextTick, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh, Download } from '@element-plus/icons-vue'
import CopyButton from '../../../components/CopyButton.vue'
import { api } from '../../../api'
import { errMsg } from '../../../utils/format'
import { containerName } from '../../../utils/docker-format'
import { useAutoRefresh } from '../../../composables/useAutoRefresh'

const logsDrawer = ref(false)
const logsLoading = ref(false)
const logsText = ref('')
const logsName = ref('')
const logsId = ref('')
// tail 行数（后端钳制到 [200, 2000]）；「跟随」开关：每 2s 静默重拉新日志
const logsTail = ref(200)
const logsFollow = ref(false)
const logsPreRef = ref(null)

function open(row) {
  logsId.value = row.ID
  logsName.value = containerName(row.Names)
  logsText.value = ''
  logsDrawer.value = true
  fetchLogs()
}

async function fetchLogs() {
  if (!logsId.value) return
  logsLoading.value = true
  try {
    const res = await api.dockerContainerLogs(logsId.value, logsTail.value)
    const data = res.data || {}
    logsText.value = data.logs || ''
  } catch (e) {
    ElMessage.error(errMsg(e, '获取日志失败'))
  } finally {
    logsLoading.value = false
  }
}

function onTailChange() {
  fetchLogs()
}

// ── 跟随：定时静默重拉（不动 loading），贴底（距底 <40px）才自动滚到底 ──

function logsNearBottom() {
  const el = logsPreRef.value
  if (!el) return false
  return el.scrollHeight - el.scrollTop - el.clientHeight < 40
}

function scrollLogsBottom() {
  const el = logsPreRef.value
  if (el) el.scrollTop = el.scrollHeight
}

// 跟随轮询统一交 useAutoRefresh 托管：start/stop 由下方 logsFollow / logsDrawer 两个 watch 驱动，
// 组件卸载自动停表。回调内的 drawer/id 守卫保留（与原实现一致）。
const { start: startLogsTimer, stop: stopLogsTimer } = useAutoRefresh(pullLogsFollow, { intervalMs: 2000 })

async function pullLogsFollow() {
  if (!logsDrawer.value || !logsId.value) return
  try {
    const near = logsNearBottom() // 更新内容前先记贴底状态，新日志到达后据此决定是否滚动
    const res = await api.dockerContainerLogs(logsId.value, logsTail.value)
    logsText.value = (res.data || {}).logs || ''
    if (near) nextTick(scrollLogsBottom)
  } catch (e) {
    // 跟随轮询失败静默（下拉手动刷新会给错误提示），不打扰阅读
  }
}

watch(logsFollow, (on) => {
  if (on && logsDrawer.value) startLogsTimer()
  else stopLogsTimer()
})

// 抽屉关闭即停跟随轮询（下次打开时按开关状态重启）
watch(logsDrawer, (open) => {
  if (!open) stopLogsTimer()
  else if (logsFollow.value) startLogsTimer()
})

// 按当前 tail 拉取日志内容，Blob 下载为 <容器名>.log
async function downloadLogs() {
  if (!logsId.value) return
  try {
    const res = await api.dockerContainerLogs(logsId.value, logsTail.value)
    const text = (res.data || {}).logs || ''
    const blob = new Blob([text], { type: 'text/plain;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    // 容器名形如 "/web"，下载文件名清理掉路径非法字符
    a.download = (logsName.value || 'container').replace(/[\\/:*?"<>|]/g, '_').replace(/^_+/, '') + '.log'
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
  } catch (e) {
    ElMessage.error(errMsg(e, '下载日志失败'))
  }
}

defineExpose({ open })
</script>

<style scoped>
/* 开关 + 文字标签（容器工具栏「自动刷新」/ 日志抽屉「跟随」共用） */
.ct-auto {
  display: flex;
  align-items: center;
  gap: 6px;
}
.ct-auto-label {
  font-size: 0.85rem;
  color: var(--el-text-color-regular, #606266);
}
.logs-tail {
  width: 150px;
}
.logs-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
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
