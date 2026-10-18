<template>
  <!-- 安装任务日志抽屉：首屏 GET 全量 + WS 实时增量（复用任务中心的结构化日志端点）。
       打开、进度/状态更新由父级通过 defineExpose 调用；流生命周期完全自持。 -->
  <el-drawer v-model="visible" :title="title" size="55%" @closed="closeStream">
    <div class="log-head">
      <el-tag :type="statusTag" effect="light">{{ statusText }}</el-tag>
      <span v-if="live" class="log-live">● 实时</span>
      <el-progress v-if="running" :percentage="progress" :stroke-width="8" class="log-progress" />
    </div>
    <div ref="viewRef" class="log-view mono">
      <div v-if="!entries.length" class="log-empty">（等待日志输出…）</div>
      <div v-for="e in entries" :key="e.seq" class="log-line" :class="'log-' + e.kind">
        <span class="log-ts">{{ fmtLogTs(e.ts) }}</span>
        <span class="log-text">{{ e.text }}</span>
      </div>
    </div>
    <template #footer>
      <el-button @click="visible = false">关闭</el-button>
    </template>
  </el-drawer>
</template>

<script setup>
// 应用安装日志抽屉：任务结构化日志的「首屏全量 + WS 增量」展示。
// 数据流：AppStore.submitInstall 提交任务后调用 open(taskId, appName)，随后轮询把
// progress/status 推给 setProgress/setStatus。
//
// 日志流本身（GET 首屏 + 一次性票据 + seq 去重 + 退避重连）复用
// composables/useTaskLogStream.js——此前这里另抄了一份约 86 行的同语义实现，
// 与任务中心各写一遍，行为漂移没人发现。差异点用 open() 的 alwaysConnect 承接：
// 安装任务轮询可能已结束、日志仍在写，故无论终态都连 WS。
import { ref, computed } from 'vue'
import { useTaskLogStream } from '../../../composables/useTaskLogStream'

const visible = ref(false)
const appName = ref('')
const status = ref('running') // running | success | failed
const progress = ref(0)
const viewRef = ref(null)

const { entries, live, open: openStream, close: closeStream } = useTaskLogStream(() => {
  // 追加后贴底（composable 的 onAppend 回调）
  const el = viewRef.value
  if (el) el.scrollTop = el.scrollHeight
})

const title = computed(() => `安装日志${appName.value ? ' · ' + appName.value : ''}`)
const running = computed(() => status.value === 'running')
const statusText = computed(() => ({ running: '执行中', success: '安装完成', failed: '安装失败' }[status.value] || '执行中'))
const statusTag = computed(() => ({ running: 'warning', success: 'success', failed: 'danger' }[status.value] || 'info'))

function fmtLogTs(ts) {
  const d = ts ? new Date(ts) : new Date()
  if (isNaN(d.getTime())) return ''
  const p = (n) => String(n).padStart(2, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

/** 父级入口：打开抽屉并从头拉取指定任务的日志。 */
function open(id, name) {
  appName.value = name || ''
  status.value = 'running'
  progress.value = 0
  visible.value = true
  openStream({ id }, { alwaysConnect: true })
}

/** 父级轮询回调：更新进度条（0-100）。 */
function setProgress(v) {
  progress.value = v
}

/** 父级轮询终态：running | success | failed。 */
function setStatus(s) {
  status.value = s
}

defineExpose({ open, setProgress, setStatus })
</script>

<style scoped>
.log-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
}
.log-live {
  font-size: 12px;
  color: var(--color-success, #67c23a);
}
.log-progress {
  flex: 1;
}
.log-view {
  max-height: calc(100vh - 220px);
  overflow: auto;
  padding: 8px 10px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: var(--radius-sm, 6px);
  background: var(--el-fill-color-light);
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
}
.log-empty {
  color: var(--color-muted-foreground, #909399);
}
.log-line {
  display: flex;
  gap: 8px;
}
.log-ts {
  flex: none;
  color: var(--color-muted-foreground, #909399);
  user-select: none;
}
.log-text {
  flex: 1;
  min-width: 0;
}
.log-stderr .log-text {
  color: var(--el-color-danger);
}
.log-command .log-text {
  color: var(--el-color-primary);
}
.log-info .log-text {
  color: var(--color-success, #67c23a);
}
</style>
