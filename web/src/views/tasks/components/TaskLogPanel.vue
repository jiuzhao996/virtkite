<template>
  <!-- 结构化执行日志面板：标题（实时徽标 + 调试行折叠开关）+ 限高滚动的日志正文。
       根为「h4 + div」两个节点（与原内联块一致），不加包裹元素。 -->
  <h4 class="detail-sec log-head">
    <span>执行日志<span v-if="live" class="log-live">● 实时</span></span>
    <span v-if="hiddenCount" class="log-toggle" @click="debug = !debug">
      {{ debug ? '隐藏' : '显示' }} {{ hiddenCount }} 行调试
    </span>
  </h4>
  <div ref="logViewRef" class="log-view mono">
    <div v-if="!visibleLogEntries.length" class="log-empty">（暂无日志输出）</div>
    <div
      v-for="e in visibleLogEntries"
      :key="e.seq"
      class="log-line"
      :class="'log-' + e.kind"
    >
      <span class="log-ts">{{ fmtLogTs(e.ts) }}</span>
      <span class="log-text">{{ e.text }}</span>
    </div>
  </div>
</template>

<script setup>
// 任务详情抽屉的「执行日志」面板：接收首屏 GET 全量 + WS 追平的原始条目数组
// （含 hidden 标记），自行折叠调试行、渲染日志行，并把「滚到底」能力暴露给页面
// （页面在 append 后调用 scrollToBottom()，与原来操作内联 ref 的行为一致）。
import { computed, nextTick, ref } from 'vue'

const props = defineProps({
  // 原始结构化日志条目 [{ seq, ts, kind, text, hidden }]（未过滤）
  entries: { type: Array, default: () => [] },
  // 是否实时（真时标题旁显示「● 实时」徽标）
  live: { type: Boolean, default: false },
  // 是否展开调试行（showDebug），由页面持有，v-model:debug 双向
  debug: { type: Boolean, default: false }
})
const emit = defineEmits(['update:debug'])

// 可写计算：模板里 `debug = !debug` 经它回写页面持有的 showDebug
const debug = computed({ get: () => props.debug, set: (v) => emit('update:debug', v) })

const hiddenCount = computed(() => props.entries.filter((e) => e.hidden).length)
const visibleLogEntries = computed(() =>
  debug.value ? props.entries : props.entries.filter((e) => !e.hidden)
)

/** 毫秒时间戳 → HH:MM:SS（日志行前缀）。 */
function fmtLogTs(ts) {
  if (!ts) return ''
  const d = new Date(ts)
  const p = (n) => String(n).padStart(2, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

const logViewRef = ref(null)
/** 滚动到底部（新增条目后调用）。页面经模板 ref 调本方法。 */
function scrollToBottom() {
  nextTick(() => {
    const el = logViewRef.value
    if (el) el.scrollTop = el.scrollHeight
  })
}
defineExpose({ scrollToBottom })
</script>

<style scoped>
/* 以下 log-* 规则自 TaskList.vue 原样搬入（类名不变，DOM 不变） */
.log-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.log-live {
  margin-left: 6px;
  font-size: 12px;
  color: var(--color-success);
}
.log-toggle {
  font-size: 12px;
  font-weight: normal;
  color: var(--el-color-primary);
  cursor: pointer;
}
.log-view {
  max-height: 320px;
  overflow: auto;
  padding: 8px 10px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: var(--radius-sm);
  background: var(--el-fill-color-light);
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
}
.log-empty {
  color: var(--color-muted-foreground);
}
.log-line {
  display: flex;
  gap: 8px;
}
.log-ts {
  flex: none;
  color: var(--color-muted-foreground);
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
  color: var(--color-success);
}
/* 与 TaskList.vue 的 .detail-sec（区块小标题）保持一致——该 class 在页面与抽屉里共用，
   作用域隔离后子组件需自持一份，改一处务必两处同步 */
.detail-sec {
  margin: 16px 0 8px;
  font-size: 14px;
}
</style>
