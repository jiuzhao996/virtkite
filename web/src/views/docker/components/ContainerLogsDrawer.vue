<template>
    <!-- 容器日志抽屉：壳 + 数据源（WS 实时流，失败降级 HTTP 轮询）+ LogViewer 展示内核 -->
    <el-drawer v-model="logsDrawer" :title="'容器日志 — ' + logsName" size="55%">
      <div v-loading="logsLoading">
        <!-- 流状态条：仅 WS 模式展示（HTTP 降级时提示已退回轮询） -->
        <div v-if="mode === 'ws' || wsFallback" class="ls-bar">
          <span class="ls-dot" :class="dotClass">●</span>
          <span>{{ wsStatusText }}</span>
          <el-button v-if="wsFallback" text size="small" @click="retryStream">重试实时流</el-button>
        </div>
        <LogViewer
          ref="viewerRef"
          :text="mode === 'http' ? logsText : ''"
          :rows="mode === 'ws' ? streamLines : null"
          :loading="logsLoading"
          :follow="logsFollow"
          :timestamps="logsTimestamps"
          :filename="logsName"
          empty-text="（暂无日志输出）"
          @update:follow="onFollowChange"
          @update:timestamps="onTimestampsChange"
          @refresh="reload"
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
// 容器日志抽屉（R4）：默认走 WS 实时流（Engine API logs follow），
// 流不可用（建连失败/重试用尽）自动降级为既有 HTTP 轮询，保底不比改造前差。
// 展示内核（搜索/暂停/换行/stderr 分色/下载）在 LogViewer，本组件只管数据源与跟随节奏。
import { ref, computed, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { api } from '../../../api'
import { errMsg } from '../../../utils/format'
import { containerName } from '../../../utils/docker-format'
import { useLogStream } from '../../../composables/useLogStream'
import LogViewer from './LogViewer.vue'

const logsDrawer = ref(false)
const logsLoading = ref(false)
const logsText = ref('')
const logsName = ref('')
const logsId = ref('')
// tail 行数（后端钳制到 [200, 2000]）
const logsTail = ref(200)
const logsFollow = ref(false)
const logsTimestamps = ref(false)
const viewerRef = ref(null)

// ws = 实时流；http = 降级轮询
const mode = ref('ws')
const { lines: streamLines, status, errorMsg, fallback: wsFallback, start: startStream, stop: stopStream, reset: resetStream } = useLogStream()

const dotClass = computed(() => ({ live: 'ok', connecting: 'wait', closed: 'off', error: 'err' }[status.value] || 'off'))
const wsStatusText = computed(() => {
  if (wsFallback.value) return '实时流不可用，已退回轮询刷新'
  if (status.value === 'live') return '实时流已连接'
  if (status.value === 'connecting') return '实时流连接中…'
  if (status.value === 'error') return errorMsg.value || '实时流错误'
  if (status.value === 'closed') return '日志流已结束（容器停止）'
  return '实时流未连接'
})

function open(row) {
  logsId.value = row.ID
  logsName.value = containerName(row.Names)
  logsText.value = ''
  mode.value = 'ws'
  resetStream()
  logsDrawer.value = true
  reload()
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

// 打开/切 tail/切时间戳：WS 模式重启流（参数变了必须重连），HTTP 模式重拉
function reload() {
  if (mode.value === 'ws') {
    // tail 大于 2000 会被后端钳制，WS 端同样按 2000 上限发起
    startStream(logsId.value, { tail: Math.min(logsTail.value, 2000), timestamps: logsTimestamps.value })
  } else {
    fetchLogs()
  }
}

function onTailChange() {
  reload()
}

function onTimestampsChange(v) {
  logsTimestamps.value = v
  reload()
}

// 跟随：WS 模式本就实时，跟随仅控制自动滚动（LogViewer 内部按贴底判定）；
// HTTP 模式才需要 2s 轮询。
function onFollowChange(v) {
  logsFollow.value = v // 仅状态记录；轮询节奏与跟随解耦，见 wsFallback watch
}

// ── HTTP 降级模式的 2s 轮询（仅降级时启用）──
let httpTimer = null
function startHttpTimer() {
  stopHttpTimer()
  httpTimer = setInterval(() => {
    if (logsDrawer.value && logsId.value) fetchLogs()
  }, 2000)
}
function stopHttpTimer() {
  if (httpTimer) { clearInterval(httpTimer); httpTimer = null }
}

// WS 重试用尽 → 永久切 HTTP（保留手动重试入口）
watch(wsFallback, (on) => {
  if (!on) return
  mode.value = 'http'
  fetchLogs()
  startHttpTimer()
})

function retryStream() {
  wsFallback.value = false
  mode.value = 'ws'
  reload()
}

// 抽屉关闭：停流 + 停轮询
watch(logsDrawer, (open) => {
  if (!open) {
    stopStream()
    stopHttpTimer()
  }
})

defineExpose({ open })
</script>

<style scoped>
.logs-tail {
  width: 150px;
}
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
