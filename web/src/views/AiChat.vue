<template>
  <div v-loading="statusLoading" class="ai-page">
    <!-- 页头：标题 + 带平台上下文开关 + 清空会话（布局走全局 .page-head/.page-title） -->
    <div class="page-head">
      <div>
        <h2 class="page-title">AI 运维助手</h2>
        <span class="page-desc">
          {{
            ai.configured
              ? '模型 ' + ai.model + ' · 会话仅保存在当前页面，刷新或切页后丢失'
              : '基于平台实时状态的智能运维问答'
          }}
        </span>
      </div>
      <div class="head-actions">
        <el-tooltip
          content="开启后助手可感知平台虚拟机、容器与告警的实时状态，回答更准确"
          placement="top"
        >
          <span class="ctx-label">带平台上下文<el-switch v-model="withContext" aria-label="带平台上下文" /></span>
        </el-tooltip>
        <el-button :icon="Delete" :disabled="!messages.length" @click="clearSession">清空会话</el-button>
      </div>
    </div>

    <!-- 状态获取失败（区别于「未配置」：后者是正常引导，前者是请求出错可重试） -->
    <div v-if="statusError" class="ai-state">
      <el-result icon="error" title="AI 状态获取失败" sub-title="请检查后端服务是否正常，然后重试">
        <template #extra>
          <el-button type="primary" :icon="Refresh" @click="loadStatus">重试</el-button>
        </template>
      </el-result>
    </div>

    <!-- 未配置：整页引导，隐藏输入区 -->
    <div v-else-if="!ai.configured" class="ai-state">
      <el-result
        icon="info"
        title="AI 尚未配置"
        sub-title="请管理员在 系统设置 → AI 设置 填写 API 地址、Key 与模型名"
      />
    </div>

    <!-- 聊天主体 -->
    <template v-else>
      <div class="chat-panel">
        <!-- 点击委托：markdown 渲染出的代码块右上角复制钮（v-html 内容无法绑事件） -->
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
                @click="send(s)"
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
      </div>

      <!-- 会话超长提示（历史只在前端内存，必须清空才能继续） -->
      <el-alert v-if="historyFull" class="limit-alert" type="warning" show-icon :closable="false">
        <template #title>
          会话已达 {{ MAX_MESSAGES }} 条上限，请点击右上角「清空会话」后继续提问
        </template>
      </el-alert>

      <!-- 输入区：Enter 发送、Shift+Enter 换行 -->
      <div class="input-bar">
        <el-input
          ref="inputRef"
          v-model="input"
          type="textarea"
          :autosize="{ minRows: 2, maxRows: 5 }"
          resize="none"
          placeholder="输入问题，Enter 发送，Shift+Enter 换行"
          @keydown.enter.exact="onEnterKey"
        />
        <!-- streaming 时发送钮切换为停止钮：中断 SSE 流，当前回答尾注「（已停止）」 -->
        <el-button
          v-if="streaming"
          type="warning"
          class="send-btn"
          :icon="VideoPause"
          @click="stopGen"
        >停止</el-button>
        <el-button
          v-else
          type="primary"
          class="send-btn"
          :icon="Promotion"
          :disabled="!canSend"
          @click="send()"
        >发送</el-button>
      </div>
    </template>
  </div>
</template>

<script setup>
import { ref, reactive, computed, watch, nextTick, onMounted, onBeforeUnmount } from 'vue'
import { ElMessageBox } from 'element-plus'
import { ChatDotRound, Delete, MagicStick, Promotion, Refresh, User, VideoPause, Warning } from '@element-plus/icons-vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
// GET /ai/status 走既有 axios 实例（baseURL=/api 已带 token 拦截器）；
// POST /ai/chat 是 SSE 流式，axios 拿不到 ReadableStream，必须用原生 fetch（见 streamAnswer）。
import http from '../api'
import { TOKEN_KEY } from '../store/auth'

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

// 会话长度上限：达到后禁发并提示清空（历史只在前端内存，切页即丢，故不裁剪只提示）
const MAX_MESSAGES = 40

// ── AI 配置状态（GET /ai/status）───────────────────────────────
const statusLoading = ref(false)
const statusError = ref(false)
const ai = reactive({ configured: false, model: '', baseUrl: '', keyMasked: '' })

async function loadStatus() {
  statusLoading.value = true
  statusError.value = false
  try {
    // 统一结构 { code, message, data }，此处手动解包 data（未走 api/index 的 unwrap 助手）
    const res = await http.get('/ai/status')
    const data = (res.data && res.data.data) || {}
    ai.configured = !!data.configured
    ai.model = data.model || ''
    ai.baseUrl = data.base_url || ''
    ai.keyMasked = data.key_masked || ''
  } catch {
    statusError.value = true
  } finally {
    statusLoading.value = false
  }
}

// ── 会话状态（仅前端内存，切页丢失可接受）─────────────────────
const messages = ref([]) // { role: 'user'|'assistant', content, streaming?, error? }
const input = ref('')
const withContext = ref(true) // 「带平台上下文」默认开
const streaming = ref(false)
const abortRef = ref(null) // 进行中请求的 AbortController，清空会话/离开页面时中断
const listRef = ref(null)
const inputRef = ref(null)

const historyFull = computed(() => messages.value.length >= MAX_MESSAGES)
const canSend = computed(() => ai.configured && !streaming.value && !historyFull.value)

const suggestions = [
  '我现在有几台虚拟机在运行？',
  '如何给虚拟机创建快照？',
  'Docker 容器退出了怎么排查？',
  '平台当前有哪些告警？',
  '如何基于云镜像克隆一台新虚拟机？'
]

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
  () => [messages.value.length, messages.value.length && messages.value[messages.value.length - 1].content],
  () => {
    if (isNearBottom()) nextTick(scrollToBottom)
  }
)

function onEnterKey(e) {
  // 中文输入法选词的回车不发送（isComposing + keyCode 229 兜底旧版 Safari）
  if (e.isComposing || e.keyCode === 229) return
  e.preventDefault()
  send()
}

async function send(text) {
  const content = (text !== undefined ? text : input.value).trim()
  if (!content || !canSend.value) return
  input.value = ''
  messages.value.push({ role: 'user', content })
  await streamAnswer()
  nextTick(() => inputRef.value && inputRef.value.focus())
}

// 停止生成：中断当前 SSE 流，catch 分支会给 assistant 消息尾注「（已停止）」
function stopGen() {
  abortRef.value?.abort()
}

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

/**
 * 发起 SSE 流式问答：fetch + ReadableStream 逐行解析。
 * 协议：逐段 `data: {...}`（OpenAI 兼容 chunk，取 choices[0].delta.content 增量拼接），
 * 以 `data: [DONE]` 结束。assistant 占位消息随 delta 增量更新（打字机效果），
 * 完成后整条留在 messages 历史里；失败/中断在该条消息上标 error（错误气泡）。
 */
async function streamAnswer() {
  // 组装历史：只送有效内容（错误气泡、被手动停止的回答与空占位不进上下文）
  const history = messages.value
    .filter((m) => !m.error && !m.aborted && m.content)
    .map((m) => ({ role: m.role, content: m.content }))
  const assistant = reactive({ role: 'assistant', content: '', streaming: true, error: '', aborted: false })
  messages.value.push(assistant)
  streaming.value = true

  const ctrl = new AbortController()
  abortRef.value = ctrl
  try {
    const res = await fetch('/api/ai/chat', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: 'Bearer ' + (localStorage.getItem(TOKEN_KEY) || '')
      },
      body: JSON.stringify({ messages: history, with_context: withContext.value }),
      signal: ctrl.signal
    })
    if (!res.ok) {
      // 后端统一错误结构 { code, message, data }，优先给前端中文 message
      let message = '请求失败（HTTP ' + res.status + '）'
      try {
        const j = await res.json()
        if (j && j.message) message = j.message
      } catch {
        /* 错误体不是 JSON 时保留 HTTP 状态文案 */
      }
      throw new Error(message)
    }
    if (!res.body) throw new Error('当前浏览器不支持流式读取')

    const reader = res.body.getReader()
    const decoder = new TextDecoder('utf-8')
    let buf = ''
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      buf += decoder.decode(value, { stream: true })
      let nl
      while ((nl = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, nl).trim()
        buf = buf.slice(nl + 1)
        if (!line.startsWith('data:')) continue
        const payload = line.slice(5).trim()
        if (payload === '[DONE]') {
          buf = ''
          break
        }
        try {
          const chunk = JSON.parse(payload)
          const delta = chunk && chunk.choices && chunk.choices[0] && chunk.choices[0].delta
          if (delta && typeof delta.content === 'string' && delta.content) {
            assistant.content += delta.content
          }
        } catch {
          /* 心跳或非 JSON 行忽略 */
        }
      }
    }
    if (!assistant.content) assistant.error = '模型没有返回内容，请稍后重试'
  } catch (e) {
    if (ctrl.signal.aborted) {
      // 「清空会话」中断：列表已清空，assistant 不在 messages 里，无需处理；
      // 「停止生成」中断：当前消息仍在列表，尾注「（已停止）」并标记 aborted（不再进后续上下文）
      if (messages.value.includes(assistant)) {
        assistant.aborted = true
        assistant.content = assistant.content ? assistant.content + '\n\n（已停止）' : '（已停止）'
      }
    } else if (e && e.name === 'TypeError') {
      assistant.error = '网络异常，无法连接 AI 服务'
    } else {
      assistant.error = (e && e.message) || '回答中断，请稍后重试'
    }
  } finally {
    abortRef.value = null
    streaming.value = false
    assistant.streaming = false
  }
}

async function clearSession() {
  if (!messages.value.length) return
  try {
    await ElMessageBox.confirm('确定清空当前会话？聊天记录仅保存在本页内存，清空后不可恢复。', '清空会话', {
      type: 'warning',
      confirmButtonText: '清空',
      cancelButtonText: '取消'
    })
  } catch {
    return // 用户取消
  }
  if (abortRef.value) abortRef.value.abort()
  messages.value.splice(0, messages.value.length)
}

onMounted(async () => {
  await loadStatus()
  if (ai.configured) nextTick(() => inputRef.value && inputRef.value.focus())
})

// 离开页面中断进行中的流式请求（历史本就不持久化，无需善后）
onBeforeUnmount(() => {
  if (abortRef.value) abortRef.value.abort()
})
</script>

<style scoped>
/* 顶栏 60px + el-main 上下内边距 40px；聊天页自己管滚动，不靠 el-main 整页滚 */
.ai-page {
  height: calc(100vh - 100px);
  min-height: 460px;
  display: flex;
  flex-direction: column;
}

.head-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}
.ctx-label {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 0.9rem;
  color: var(--color-muted-foreground);
}

/* 状态页（未配置/加载失败）垂直居中 */
.ai-state {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
}

/* ── 聊天面板 ── */
.chat-panel {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  overflow: hidden;
}
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

/* ── 底部输入区 ── */
.limit-alert {
  margin-top: 12px;
}
.input-bar {
  display: flex;
  align-items: flex-end;
  gap: 12px;
  margin-top: 12px;
}
.send-btn {
  flex: none;
  margin-bottom: 4px;
}
</style>
