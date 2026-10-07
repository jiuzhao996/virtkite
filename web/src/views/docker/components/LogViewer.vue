<template>
  <div class="lv">
    <div class="lv-toolbar">
      <el-input
        v-model="keyword"
        class="lv-search"
        size="small"
        placeholder="过滤日志（高亮匹配）"
        clearable
        :prefix-icon="Search"
      />
      <span v-if="keyword.trim()" class="lv-count">{{ matchedCount }}/{{ totalLines }} 行</span>
      <el-checkbox v-model="wrap" size="small" title="长行自动换行">换行</el-checkbox>
      <el-checkbox :model-value="timestamps" size="small" title="显示 docker 写入时间戳" @change="(v) => $emit('update:timestamps', v)">时间戳</el-checkbox>
      <el-checkbox :model-value="follow" size="small" title="跟随最新日志并自动贴底" @change="(v) => $emit('update:follow', v)">跟随</el-checkbox>
      <el-button
        size="small"
        :type="paused ? 'warning' : 'default'"
        :icon="paused ? VideoPlay : VideoPause"
        :title="paused ? '已暂停渲染（后台仍在接收），点击恢复并追平' : '暂停渲染（后台继续接收）'"
        @click="togglePause"
      >{{ paused ? '已暂停' : '暂停' }}</el-button>
      <el-button size="small" :icon="Refresh" :loading="loading" title="立即刷新" @click="$emit('refresh')" />
      <CopyButton :text="rawText" tip="复制日志" success-msg="日志已复制到剪贴板" />
      <el-button size="small" :icon="Download" title="下载日志" @click="download" />
      <!-- 数据源专属控件（如 tail 行数选择）由父级经具名插槽注入 -->
      <slot name="toolbar-extra" />
    </div>
    <pre
      ref="preRef"
      class="lv-pre"
      :class="{ 'lv-nowrap': !wrap }"
    ><template v-for="(ln, i) in displayLines" :key="i"><span :class="{ 'lv-err': ln.err }"><span
      v-for="(s, j) in ln.segs"
      :key="j"
      :class="{ 'lv-hit': s.hit }"
    >{{ s.t }}</span>{{ '\n' }}</span></template><span v-if="!displayLines.length" class="lv-empty">{{ emptyText }}</span></pre>
  </div>
</template>

<script setup>
// 日志展示内核：与数据来源无关（HTTP 全量文本 / WS 行数组两种喂法），R3/R5 复用同一渲染层。
// 只负责「渲染 + 前端过滤/高亮 + 暂停/换行/下载」，取数与跟随节奏由父级（数据源）决定。
//
// 两种输入：
//   - text: String —— HTTP 轮询源（父级每次给全量文本）
//   - rows: Array<{stream,data}> —— WS 流源（父级累积行，stderr 可单独着色）
// rows 非空时优先于 text。
import { ref, computed, nextTick, watch } from 'vue'
import { Search, Refresh, Download, VideoPause, VideoPlay } from '@element-plus/icons-vue'
import CopyButton from '../../../components/CopyButton.vue'

const props = defineProps({
  text: { type: String, default: '' },
  rows: { type: Array, default: null },
  loading: { type: Boolean, default: false },
  follow: { type: Boolean, default: false },
  timestamps: { type: Boolean, default: false },
  filename: { type: String, default: 'container' },
  emptyText: { type: String, default: '（暂无日志输出）' }
})
const emit = defineEmits(['update:follow', 'update:timestamps', 'refresh'])

// 环形缓冲上限：长会话/巨量日志只保留末尾 N 行，防 DOM 膨胀
const MAX_LINES = 5000

const preRef = ref(null)
const keyword = ref('')
const wrap = ref(true)
// 暂停：冻结渲染快照（后台数据仍在更新），恢复时一次性追平到最新
const paused = ref(false)
const frozenRows = ref(null)
const frozenText = ref('')

// 统一内部行模型：[{stream, data}]
const allRows = computed(() => {
  if (props.rows && props.rows.length) return props.rows
  const t = props.text || ''
  if (!t) return []
  return t.split('\n').map((l) => ({ stream: 'stdout', data: l }))
})

// 暂停快照：冻结「当时的数据形态」，避免两种源混用时丢失 stream 信息
function snapshot() {
  return { rows: props.rows && props.rows.length ? props.rows.slice() : null, text: props.text || '' }
}

const renderRows = computed(() => {
  if (!paused.value) return allRows.value
  if (frozenRows.value) return frozenRows.value
  return (frozenText.value || '').split('\n').map((l) => ({ stream: 'stdout', data: l }))
})

const rawText = computed(() => renderRows.value.map((r) => r.data).join('\n'))
const totalLines = computed(() => renderRows.value.length)

const displayLines = computed(() => {
  let rows = renderRows.value
  if (rows.length > MAX_LINES) rows = rows.slice(rows.length - MAX_LINES)
  const kw = keyword.value.trim()
  const out = []
  for (const r of rows) {
    const line = r.data
    if (kw && !line.toLowerCase().includes(kw.toLowerCase())) continue
    const segs = kw ? splitHighlight(line, kw.toLowerCase(), kw.length) : [{ t: line, hit: false }]
    out.push({ segs, err: r.stream === 'stderr' })
  }
  return out
})

const matchedCount = computed(() => {
  const kw = keyword.value.trim()
  if (!kw) return 0
  const lower = kw.toLowerCase()
  let n = 0
  for (const r of renderRows.value) if (r.data.toLowerCase().includes(lower)) n++
  return n
})

function splitHighlight(line, lowerKw, kwLen) {
  const segs = []
  let rest = line
  let guard = 0
  while (guard < 500) {
    const idx = rest.toLowerCase().indexOf(lowerKw)
    if (idx === -1) break
    guard++
    if (idx > 0) segs.push({ t: rest.slice(0, idx), hit: false })
    segs.push({ t: rest.slice(idx, idx + kwLen), hit: true })
    rest = rest.slice(idx + kwLen)
  }
  segs.push({ t: rest, hit: false })
  return segs
}

function togglePause() {
  if (paused.value) {
    paused.value = false
    frozenRows.value = null
    frozenText.value = ''
    nextTick(scrollBottom)
  } else {
    const snap = snapshot()
    frozenRows.value = snap.rows
    frozenText.value = snap.text
    paused.value = true
  }
}

function nearBottom() {
  const el = preRef.value
  if (!el) return false
  return el.scrollHeight - el.scrollTop - el.clientHeight < 40
}

function scrollBottom() {
  const el = preRef.value
  if (el) el.scrollTop = el.scrollHeight
}

// 贴底自动滚动：跟随开=无条件贴底；关=仅当更新前本来就贴底才跟随（用户上滚翻历史不受打扰）。
// pre 阶段记「更新前是否贴底」，post 阶段再滚，避免新日志撑高后判定失真；暂停时完全不打断阅读。
let wasNearBottom = true
watch(allRows, () => { wasNearBottom = nearBottom() }, { flush: 'pre' })
watch(allRows, () => {
  if (paused.value) return
  if (props.follow || wasNearBottom) nextTick(scrollBottom)
}, { flush: 'post' })

// 首批日志到达强制贴底一次：新会话总是从最新处看起（此时布局未稳，wasNearBottom 判定可能失真）
watch(() => renderRows.value.length, (n, o) => {
  if (!o && n && !paused.value) nextTick(scrollBottom)
})

function download() {
  const blob = new Blob([rawText.value], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = (props.filename || 'container').replace(/[\\/:*?"<>|]/g, '_').replace(/^_+/, '') + '.log'
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

// 切换容器/清空重建后强制贴底一次（新会话总是从底部看起）
function scrollToEndOnce() {
  nextTick(scrollBottom)
}

defineExpose({ scrollToEndOnce })
</script>

<style scoped>
.lv-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.lv-search {
  width: 240px;
}
.lv-count {
  font-size: 0.8rem;
  color: var(--el-text-color-secondary, #909399);
}
.lv-pre {
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
.lv-pre.lv-nowrap {
  white-space: pre;
  word-break: normal;
}
.lv-hit {
  background: #ffd54f;
  color: #1b2634;
  border-radius: 2px;
}
/* stderr 行红色着色（Dozzle 观感：流类型一眼可辨） */
.lv-err {
  color: #ff9a9a;
}
.lv-empty {
  color: #7f9ab5;
}
</style>
