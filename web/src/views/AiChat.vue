<!--
  AI 运维助手页
  ────────────────────────────────────────────────────────────────
  职责：AI 配置状态探测、会话内存状态、SSE 流式问答、清空会话与页面/抽屉两种形态切换。
  数据流：
    GET /ai/status → ai.configured/model（未配置/失败分别渲染引导与重试）
    POST /ai/chat（原生 fetch + ReadableStream）→ assistant 消息增量更新
  子组件（./ai/components/）：
    AiMessageList  欢迎区 + 气泡 + markdown 安全渲染 + 代码复制 + 贴底滚动
    AiComposer     输入框 + 发送/停止按钮（暴露 focus() 供发送后回焦）
  刻意没拆：页头/状态页（未配置、加载失败）与 SSE 解析（streamAnswer）——前者只是条件渲染分支，
  后者与 messages/streaming/abortRef 强耦合，拆出去需跨组件回传中止控制器，收益低且易错。
-->
<template>
  <div v-loading="statusLoading" class="ai-page" :class="{ embedded }">
    <!-- 页头：独立页面形态保留完整页头；抽屉形态（IA 精简批次）只留紧凑工具行，标题由抽屉头承担 -->
    <PageHead v-if="!embedded" title="AI 运维助手" :subtitle="ai.configured ? '模型 ' + ai.model + ' · 会话仅保存在当前页面，刷新或切页后丢失' : '基于平台实时状态的智能运维问答'">
      <div class="head-actions">
        <el-tooltip
          content="开启后助手可感知平台虚拟机、容器与告警的实时状态，回答更准确"
          placement="top"
        >
          <span class="ctx-label">带平台上下文<el-switch v-model="withContext" aria-label="带平台上下文" /></span>
        </el-tooltip>
        <el-button :icon="Delete" :disabled="!messages.length" @click="clearSession">清空会话</el-button>
      </div>
    </PageHead>
    <div v-else class="head-actions drawer-actions">
      <span class="drawer-desc">
        {{ ai.configured ? '模型 ' + ai.model : '基于平台实时状态的智能运维问答' }}
      </span>
      <el-tooltip
        content="开启后助手可感知平台虚拟机、容器与告警的实时状态，回答更准确"
        placement="top"
      >
        <span class="ctx-label">带平台上下文<el-switch v-model="withContext" aria-label="带平台上下文" /></span>
      </el-tooltip>
      <el-button :icon="Delete" :disabled="!messages.length" @click="clearSession">清空会话</el-button>
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
        <!-- 消息列表：点击委托、markdown 渲染与贴底滚动均在子组件内 -->
        <AiMessageList
          :messages="messages"
          :suggestions="suggestions"
          :history-full="historyFull"
          :streaming="streaming"
          @send="send"
        />
      </div>

      <!-- 会话超长提示（历史只在前端内存，必须清空才能继续） -->
      <el-alert v-if="historyFull" class="limit-alert" type="warning" show-icon :closable="false">
        <template #title>
          会话已达 {{ MAX_MESSAGES }} 条上限，请点击右上角「清空会话」后继续提问
        </template>
      </el-alert>

      <!-- 输入区：Enter 发送、Shift+Enter 换行 -->
      <AiComposer
        ref="composerRef"
        v-model="input"
        :streaming="streaming"
        :can-send="canSend"
        @send="send()"
        @stop="stopGen"
      />
    </template>
  </div>
</template>

<script setup>
// AI 助手壳：配置探测、会话内存、SSE 流式解析与生命周期；展示细节见 ./ai/components/。
import { ref, reactive, computed, nextTick, onMounted, onBeforeUnmount } from 'vue'
import { ElMessageBox } from 'element-plus'
import { Delete, Refresh } from '@element-plus/icons-vue'
import PageHead from '../components/PageHead.vue'
// GET /ai/status 走既有 axios 实例（baseURL=/api 已带 token 拦截器）；
// POST /ai/chat 是 SSE 流式，axios 拿不到 ReadableStream，必须用原生 fetch（见 streamAnswer）。
import { api } from '../api'
import { TOKEN_KEY } from '../store/auth'
import AiMessageList from './ai/components/AiMessageList.vue'
import AiComposer from './ai/components/AiComposer.vue'

// embedded：抽屉形态（MainLayout 顶栏拉起）。true 时隐藏页头只留工具行，
// 容器改为撑满抽屉体高度（会话状态仍在本组件内存，抽屉关闭仅隐藏不清空）
defineProps({
  embedded: { type: Boolean, default: false }
})

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
    const res = await api.aiStatus()
    const data = (res && res.data) || {}
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
const composerRef = ref(null)

const historyFull = computed(() => messages.value.length >= MAX_MESSAGES)
const canSend = computed(() => ai.configured && !streaming.value && !historyFull.value)

const suggestions = [
  '我现在有几台虚拟机在运行？',
  '如何给虚拟机创建快照？',
  'Docker 容器退出了怎么排查？',
  '平台当前有哪些告警？',
  '如何基于云镜像克隆一台新虚拟机？'
]

async function send(text) {
  const content = (text !== undefined ? text : input.value).trim()
  if (!content || !canSend.value) return
  input.value = ''
  messages.value.push({ role: 'user', content })
  await streamAnswer()
  nextTick(() => composerRef.value && composerRef.value.focus())
}

// 停止生成：中断当前 SSE 流，catch 分支会给 assistant 消息尾注「（已停止）」
function stopGen() {
  abortRef.value?.abort()
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
      cancelButtonText: '取消',
      confirmButtonClass: 'el-button--danger'
    })
  } catch {
    return // 用户取消
  }
  if (abortRef.value) abortRef.value.abort()
  messages.value.splice(0, messages.value.length)
}

onMounted(async () => {
  await loadStatus()
  if (ai.configured) nextTick(() => composerRef.value && composerRef.value.focus())
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
/* 抽屉形态：撑满抽屉体（el-drawer__body 已是 flex 容器高度的 100%），不按独立页留头高 */
.ai-page.embedded {
  height: 100%;
  min-height: 0;
}
/* 抽屉形态的紧凑工具行：替代独立页的 page-head，右对齐一行放下模型名/上下文开关/清空 */
.drawer-actions {
  justify-content: flex-end;
  margin-bottom: 8px;
}
.drawer-desc {
  color: var(--color-muted-foreground);
  font-size: 0.85rem;
  margin-right: auto;
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

/* ── 底部输入区 ── */
.limit-alert {
  margin-top: 12px;
}
</style>
