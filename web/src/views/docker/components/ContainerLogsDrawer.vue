<template>
    <!-- 容器日志抽屉：壳 + HTTP 轮询数据源 + LogViewer 展示内核（R4 将换为 WS 源，渲染层不动） -->
    <el-drawer v-model="logsDrawer" :title="'容器日志 — ' + logsName" size="55%">
      <div v-loading="logsLoading">
        <LogViewer
          ref="viewerRef"
          :text="logsText"
          :loading="logsLoading"
          :follow="logsFollow"
          :timestamps="logsTimestamps"
          :filename="logsName"
          @update:follow="(v) => (logsFollow = v)"
          @update:timestamps="onTimestampsChange"
          @refresh="fetchLogs"
        >
          <template #toolbar-extra>
            <el-select v-model="logsTail" class="logs-tail" size="small" @change="onTailChange">
              <!-- 后端将 tail 钳制到 [200, 2000]，无法真正「不限行数」，「全部」即后端支持的 2000 行上限 -->
              <el-option label="全部（2000 行）" :value="2000" />
              <el-option label="100 行" :value="100" />
              <el-option label="200 行" :value="200" />
              <el-option label="500 行" :value="500" />
              <el-option label="1000 行" :value="1000" />
            </el-select>
          </template>
        </LogViewer>
      </div>
    </el-drawer>
</template>

<script setup>
// 容器日志抽屉：tail 切换 / 2s 跟随轮询自持，经 open(row) 由容器表格行触发。
// 展示内核（搜索/暂停/换行/下载）在 LogViewer，本组件只管取数与跟随节奏。
import { ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { api } from '../../../api'
import { errMsg } from '../../../utils/format'
import { containerName } from '../../../utils/docker-format'
import { useAutoRefresh } from '../../../composables/useAutoRefresh'
import LogViewer from './LogViewer.vue'

const logsDrawer = ref(false)
const logsLoading = ref(false)
const logsText = ref('')
const logsName = ref('')
const logsId = ref('')
// tail 行数（后端钳制到 [200, 2000]）；「跟随」开关：每 2s 静默重拉新日志
const logsTail = ref(200)
const logsFollow = ref(false)
const logsTimestamps = ref(false)
const viewerRef = ref(null)

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
    const res = await api.dockerContainerLogs(logsId.value, logsTail.value, logsTimestamps.value)
    logsText.value = (res.data || {}).logs || ''
    if (viewerRef.value) viewerRef.value.scrollToEndOnce()
  } catch (e) {
    ElMessage.error(errMsg(e, '获取日志失败'))
  } finally {
    logsLoading.value = false
  }
}

function onTailChange() {
  fetchLogs()
}

// 时间戳开关：带 timestamps 重拉（后端按行前置时间戳）
function onTimestampsChange(v) {
  logsTimestamps.value = v
  fetchLogs()
}

// ── 跟随：定时静默重拉（不动 loading）；贴底判定与滚动由 LogViewer 负责 ──
// 跟随轮询统一交 useAutoRefresh 托管：start/stop 由下方 logsFollow / logsDrawer 两个 watch 驱动，
// 组件卸载自动停表。回调内的 drawer/id 守卫保留（与原实现一致）。
const { start: startLogsTimer, stop: stopLogsTimer } = useAutoRefresh(pullLogsFollow, { intervalMs: 2000 })

async function pullLogsFollow() {
  if (!logsDrawer.value || !logsId.value) return
  try {
    const res = await api.dockerContainerLogs(logsId.value, logsTail.value, logsTimestamps.value)
    logsText.value = (res.data || {}).logs || ''
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

defineExpose({ open })
</script>

<style scoped>
.logs-tail {
  width: 150px;
}
</style>
