<template>
  <!-- 消息列表：空会话欢迎区 + user/assistant 气泡 + markdown 渲染 + 代码块复制 + 贴底滚动。
       只消费父级传入的 messages，发送意图通过 send 事件上抛。 -->
  <div ref="listRef" class="msg-list" @click="onListClick">
    <!-- 空会话欢迎区：示例问题 chips，点击即发送 -->
    <div v-if="!messages.length" class="welcome">
      <el-icon class="welcome-icon"><ChatDotRound /></el-icon>
      <h3 class="welcome-title">有什么可以帮忙？</h3>
      <p class="welcome-desc">开启「带平台上下文」后，我可以直接感知虚拟机、容器与告警的实时状态</p>
      <div class="chips">
        <button
          v-for="s in suggestions"
          :key="s"
          type="button"
          class="chip"
          :disabled="historyFull || streaming"
          @click="emit('send', s)"
        >
          {{ s }}
        </button>
      </div>
    </div>

    <!-- 消息气泡：user 右侧主题色、assistant 左侧白底 -->
    <div
      v-for="(m, i) in messages"
      :key="i"
      class="msg-row"
      :class="m.role === 'user' ? 'is-user' : 'is-ai'"
    >
      <div class="avatar" :class="m.role">
        <el-icon><component :is="m.role === 'user' ? User : MagicStick" /></el-icon>
      </div>
      <div class="bubble" :class="{ 'is-error': m.error && !m.content }">
        <template v-if="m.content">
          <!-- assistant：markdown 渲染（v-html 前必须 DOMPurify.sanitize）；user：纯文本 -->
          <div v-if="m.role === 'assistant'" class="msg-md" v-html="renderMd(m.content)"></div>
          <span v-else class="msg-text">{{ m.content }}</span>
        </template>
        <span v-if="m.streaming && !m.content" class="msg-thinking">正在思考…</span>
        <div v-if="m.error" class="msg-error">
          <el-icon><Warning /></el-icon>
          <span>{{ m.content ? '回答中断：' + m.error : m.error }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
// AI 消息列表：欢迎区、气泡、markdown 安全渲染、代码复制事件委托、增量输出贴底滚动。
// 滚动策略与消息增量强相关，因此 listRef / watch 一并内聚在本组件，父级只维护 messages。
import { ref, watch, nextTick } from 'vue'
import { ChatDotRound, MagicStick, User, Warning } from '@element-plus/icons-vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'

const props = defineProps({
  // { role: 'user'|'assistant', content, streaming?, error?, aborted? }
  messages: { type: Array, default: () => [] },
  suggestions: { type: Array, default: () => [] },
  historyFull: { type: Boolean, default: false },
  streaming: { type: Boolean, default: false }
})

const emit = defineEmits(['send'])

// markdown 渲染（assistant 消息）：breaks 让单个换行也断行，对齐聊天软件习惯
marked.setOptions({ breaks: true })

// 代码块复制钮：marked 输出的 <pre> 在 sanitize 之后注入此静态可信标记，
// 点击经消息容器上的事件委托处理（v-html 内容无法直接绑 Vue 事件）。
// 内联 SVG 摘自 @element-plus/icons-vue 的 CopyDocument / Check（与全局图标同源）。
const COPY_BTN_HTML =
  '<button type="button" class="code-copy-btn" title="复制代码" aria-label="复制代码">' +
  '<svg class="ic ic-copy" viewBox="0 0 1024 1024" aria-hidden="true">' +
  '<path fill="currentColor" d="M768 832a128 128 0 0 1-128 128H192A128 128 0 0 1 64 832V384a128 128 0 0 1 128-128v64a64 64 0 0 0-64 64v448a64 64 0 0 0 64 64h448a64 64 0 0 0 64-64z"/>' +
  '<path fill="currentColor" d="M384 128a64 64 0 0 0-64 64v448a64 64 0 0 0 64 64h448a64 64 0 0 0 64-64V192a64 64 0 0 0-64-64zm0-64h448a128 128 0 0 1 128 128v448a128 128 0 0 1-128 128H384a128 128 0 0 1-128-128V192A128 128 0 0 1 384 64"/>' +
  '</svg>' +
  '<svg class="ic ic-check" viewBox="0 0 1024 1024" aria-hidden="true">' +
  '<path fill="currentColor" d="M406.656 706.944 195.84 496.256a32 32 0 1 0-45.248 45.248l256 256 512-512a32 32 0 0 0-45.248-45.248L406.592 706.944z"/>' +
  '</svg>' +
  '</button>'

const listRef = ref(null)

function scrollToBottom() {
  const el = listRef.value
  if (el) el.scrollTop = el.scrollHeight
}

// 仅当用户本就停在距底部 80px 内才自动跟随；上滑回看历史时不被增量输出拽回
function isNearBottom() {
  const el = listRef.value
  if (!el) return true
  return el.scrollHeight - el.scrollTop - el.clientHeight < 80
}

// 消息条数变化或最后一条内容增量（打字机）时贴底（判断发生在 DOM 更新前，反映用户所在位置）
watch(
  () => [props.messages.length, props.messages.length && props.messages[props.messages.length - 1].content],
  () => {
    if (isNearBottom()) nextTick(scrollToBottom)
  }
)

/**
 * assistant 消息 markdown → 安全 HTML：marked 解析后必须过 DOMPurify 再 v-html
 * （模型输出不可信，防 XSS）。复制钮在 sanitize 之后注入（静态可信标记）。
 */
function renderMd(content) {
  const html = marked.parse(String(content || ''))
  return DOMPurify.sanitize(html).replace(/<pre>/g, '<pre>' + COPY_BTN_HTML)
}

// 代码块复制（事件委托）：点击 .code-copy-btn 时取其所在 pre 的文本写剪贴板
function onListClick(e) {
  const btn = e.target.closest('.code-copy-btn')
  if (!btn) return
  const pre = btn.closest('pre')
  if (!pre) return
  const text = ((pre.querySelector('code') || pre).textContent || '').replace(/^\n+/, '').replace(/\s+$/, '')
  copyCodeText(text, btn)
}

async function copyCodeText(text, btn) {
  let ok = false
  try {
    await navigator.clipboard.writeText(text)
    ok = true
  } catch {
    // http 环境无 clipboard API：降级 execCommand（隐藏 textarea 选中复制）
    try {
      const ta = document.createElement('textarea')
      ta.value = text
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      ok = document.execCommand('copy')
      ta.remove()
    } catch {
      ok = false
    }
  }
  if (!ok) return
  btn.classList.add('copied')
  btn.title = '已复制'
  if (btn._copyTimer) clearTimeout(btn._copyTimer)
  btn._copyTimer = setTimeout(() => {
    btn.classList.remove('copied')
    btn.title = '复制代码'
  }, 1500)
}
</script>

<style scoped>
.msg-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* ── 欢迎区 ── */
.welcome {
  margin: auto;
  max-width: 560px;
  padding: 24px;
  text-align: center;
}
.welcome-icon {
  font-size: 40px;
  color: var(--el-color-primary);
}
.welcome-title {
  margin: 12px 0 6px;
  font-size: 1.1rem;
}
.welcome-desc {
  margin: 0 0 20px;
  color: var(--color-muted-foreground);
  font-size: 0.9rem;
}
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  justify-content: center;
}
.chip {
  padding: 7px 14px;
  border: 1px solid var(--color-border-strong);
  border-radius: 999px; /* 刻意胶囊形，对齐 el-tag 视觉 */
  background: var(--color-card);
  color: var(--color-foreground);
  font-size: 0.88rem;
  font-family: inherit;
  transition: all 0.15s ease;
}
.chip:hover:not(:disabled) {
  border-color: var(--el-color-primary);
  color: var(--el-color-primary);
  background: var(--el-color-primary-light-9);
}
.chip:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* ── 消息气泡 ── */
.msg-row {
  display: flex;
  gap: 10px;
  align-items: flex-start;
}
.msg-row.is-user {
  flex-direction: row-reverse;
}
.avatar {
  width: 34px;
  height: 34px;
  border-radius: 50%;
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
}
.avatar.assistant {
  background: var(--el-color-primary-light-8);
  color: var(--el-color-primary);
}
.avatar.user {
  background: var(--el-color-primary);
  color: var(--color-on-primary);
}
.bubble {
  max-width: 78%;
  padding: 10px 14px;
  border-radius: var(--radius-md);
  font-size: 0.92rem;
  line-height: 1.7;
  box-sizing: border-box;
}
.is-ai .bubble {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-top-left-radius: var(--radius-sm);
}
.is-user .bubble {
  background: var(--el-color-primary);
  color: var(--color-on-primary);
  border-top-right-radius: var(--radius-sm);
}
.msg-text {
  display: block;
  white-space: pre-wrap;
  word-break: break-word;
}
/* ── assistant markdown 正文（v-html 生成的内容没有 scoped 属性，需 :deep 穿透）── */
.msg-md {
  word-break: break-word;
}
.msg-md :deep(p) {
  margin: 0 0 8px;
}
.msg-md :deep(p:last-child) {
  margin-bottom: 0;
}
.msg-md :deep(h1),
.msg-md :deep(h2),
.msg-md :deep(h3),
.msg-md :deep(h4) {
  margin: 12px 0 8px;
  font-size: 1.02rem;
  font-weight: 600;
  line-height: 1.5;
}
.msg-md :deep(h1:first-child),
.msg-md :deep(h2:first-child),
.msg-md :deep(h3:first-child),
.msg-md :deep(p:first-child) {
  margin-top: 0;
}
.msg-md :deep(ul),
.msg-md :deep(ol) {
  margin: 4px 0 8px;
  padding-left: 20px;
}
.msg-md :deep(li) {
  margin: 2px 0;
}
.msg-md :deep(blockquote) {
  margin: 6px 0;
  padding: 2px 12px;
  border-left: 3px solid var(--color-border);
  color: var(--color-muted-foreground);
}
/* 行内代码浅灰底；代码块沿用原 .msg-code 深色配色（#0e1626 / #e6edf3，取自终端面板） */
.msg-md :deep(code) {
  padding: 1px 5px;
  border-radius: var(--radius-sm);
  background: var(--el-fill-color);
  font-family: var(--font-mono);
  font-size: 0.85em;
}
.msg-md :deep(pre) {
  position: relative;
  margin: 6px 0;
  padding: 10px 14px;
  border-radius: var(--radius-sm);
  background: #0e1626;
  border: 1px solid rgba(88, 166, 255, 0.15);
  font-family: var(--font-mono);
  font-size: 0.85rem;
  line-height: 1.6;
  overflow-x: auto;
  white-space: pre;
}
.msg-md :deep(pre code) {
  padding: 0;
  background: transparent;
  border-radius: 0;
  color: #e6edf3;
  font-size: inherit;
}
/* 代码块右上角悬浮复制钮：hover pre 才显现，点击后打勾 1.5s（见 onListClick） */
.msg-md :deep(.code-copy-btn) {
  position: absolute;
  top: 6px;
  right: 6px;
  width: 24px;
  height: 24px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: var(--radius-sm);
  background: rgba(230, 237, 243, 0.12);
  color: #e6edf3;
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.15s ease, background 0.15s ease;
}
.msg-md :deep(pre:hover .code-copy-btn) {
  opacity: 1;
}
.msg-md :deep(.code-copy-btn:hover) {
  background: rgba(230, 237, 243, 0.25);
}
.msg-md :deep(.code-copy-btn .ic) {
  width: 13px;
  height: 13px;
}
.msg-md :deep(.code-copy-btn .ic-check) {
  display: none;
  color: var(--el-color-success-light-3, #95d475);
}
.msg-md :deep(.code-copy-btn.copied .ic-copy) {
  display: none;
}
.msg-md :deep(.code-copy-btn.copied .ic-check) {
  display: block;
}
.msg-thinking {
  color: var(--color-muted-foreground);
  animation: breathe 1.2s ease-in-out infinite; /* 全局 keyframes */
}
.msg-error {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  margin-top: 6px;
  color: var(--color-danger);
  font-size: 0.85rem;
}
.msg-error .el-icon {
  margin-top: 3px;
  flex: none;
}
.bubble.is-error {
  background: var(--el-color-danger-light-9);
  border-color: var(--el-color-danger-light-5);
}
</style>
