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
      <el-checkbox :model-value="follow" size="small" title="每 2 秒自动拉取新日志，贴底时自动滚动">跟随</el-checkbox>
      <el-button
        size="small"
        :type="paused ? 'warning' : 'default'"
        :icon="paused ? VideoPlay : VideoPause"
        :title="paused ? '已暂停渲染（后台仍在拉取），点击恢复并追平' : '暂停渲染（后台继续拉取）'"
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
    ><template v-for="(segs, i) in displayLines" :key="i"><span
      v-for="(s, j) in segs"
      :key="j"
      :class="{ 'lv-hit': s.hit }"
    >{{ s.t }}</span>{{ '\n' }}</template><span v-if="!displayLines.length" class="lv-empty">{{ emptyText }}</span></pre>
  </div>
</template>

<script setup>
// 日志展示内核：与数据来源无关（HTTP 轮询 / WS 流均可喂 text），R3/R4/R5 复用同一渲染层。
// 只负责「渲染 + 前端过滤/高亮 + 暂停/换行/下载」，取数与跟随节奏由父级（数据源）决定。
import { ref, computed, nextTick, watch } from 'vue'
import { Search, Refresh, Download, VideoPause, VideoPlay } from '@element-plus/icons-vue'
import CopyButton from '../../../components/CopyButton.vue'

const props = defineProps({
  // 原始日志文本（已含全文，父级负责累积/替换）
  text: { type: String, default: '' },
  loading: { type: Boolean, default: false },
  // 跟随开关（受控，父级据此轮询）
  follow: { type: Boolean, default: false },
  // 时间戳开关（受控，父级据此带 timestamps 重拉）
  timestamps: { type: Boolean, default: false },
  // 下载文件名（不含扩展名）
  filename: { type: String, default: 'container' },
  emptyText: { type: String, default: '（暂无日志输出）' }
})
const emit = defineEmits(['update:follow', 'update:timestamps', 'refresh'])

// 环形缓冲上限：长会话/巨量日志只保留末尾 N 行，防 DOM 膨胀
const MAX_LINES = 5000

const preRef = ref(null)
const keyword = ref('')
const wrap = ref(true)
// 暂停：冻结渲染快照（后台 text 仍在更新），恢复时一次性追平到最新
const paused = ref(false)
const frozenText = ref('')

const rawText = computed(() => props.text || '')

// 实际参与渲染的文本（暂停时用冻结快照）
const renderText = computed(() => (paused.value ? frozenText.value : rawText.value))

const totalLines = computed(() => {
  const t = renderText.value
  if (!t) return 0
  return t.split('\n').length
})

// 展示行：先按 MAX_LINES 截尾，再按关键词过滤并切分为「高亮/普通」片段
const displayLines = computed(() => {
  const t = renderText.value
  if (!t) return []
  let lines = t.split('\n')
  if (lines.length > MAX_LINES) lines = lines.slice(lines.length - MAX_LINES)
  const kw = keyword.value.trim()
  if (!kw) return lines.map((l) => [{ t: l, hit: false }])
  const lower = kw.toLowerCase()
  const out = []
  for (const line of lines) {
    if (!line.toLowerCase().includes(lower)) continue
    out.push(splitHighlight(line, lower, kw.length))
  }
  return out
})

// 匹配行数（与 displayLines 同口径，单独算一遍避免依赖渲染结构）
const matchedCount = computed(() => {
  const kw = keyword.value.trim()
  if (!kw) return 0
  const lower = kw.toLowerCase()
  let n = 0
  for (const line of renderText.value.split('\n')) {
    if (line.toLowerCase().includes(lower)) n++
  }
  return n
})

// 把一行切成 [{t, hit}] 片段（大小写不敏感匹配，保留原文大小写）
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
    frozenText.value = ''
    nextTick(scrollBottom)
  } else {
    frozenText.value = rawText.value
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

// 贴底自动滚动：pre 阶段（DOM 更新前）记录「更新前是否贴底」，post 阶段据此决定滚到底，
// 避免新日志把内容撑高后再判定导致永不滚动；暂停时完全不打断阅读。
let wasNearBottom = true
watch(() => props.text, () => { wasNearBottom = nearBottom() }, { flush: 'pre' })
watch(() => props.text, () => {
  if (paused.value) return
  if (wasNearBottom) nextTick(scrollBottom)
}, { flush: 'post' })

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
.lv-empty {
  color: #7f9ab5;
}
</style>
