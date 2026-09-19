<template>
  <div>
    <div v-if="!embedded" class="page-head">
      <div>
        <h2 class="page-title">云镜像市场</h2>
        <span class="page-desc">一键下载官方云镜像到存储池并自动登记进镜像库；下载为分钟级后台任务，可离开页面，进度可在任务中心继续跟踪</span>
      </div>
    </div>

    <el-card shadow="never">
      <div class="toolbar">
        <div class="toolbar-left">
          <el-button type="primary" :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <el-tag v-if="defaultPool" type="primary" effect="plain">默认下载池：{{ defaultPool }}</el-tag>
        </div>
        <span class="count">共 {{ items.length }} 个官方镜像</span>
      </div>

      <el-alert
        v-if="loadError"
        :title="loadError"
        type="error"
        show-icon
        style="margin-bottom: 16px"
        @close="loadError = ''"
      />

      <el-row v-if="items.length" :gutter="16">
        <el-col v-for="it in items" :key="it.key" :xs="24" :sm="12" :md="8">
          <el-card shadow="hover" class="mk-card">
            <div class="mk-name">{{ it.name }}</div>
            <p class="mk-desc">{{ it.description || '官方发布的云镜像' }}</p>
            <div class="mk-meta">
              <el-tag size="small" type="info" effect="plain">{{ it.os_name || '通用' }}</el-tag>
              <el-tag size="small" effect="plain">≈ {{ hintGB(it.size_hint) }}</el-tag>
              <el-tag size="small" type="primary" effect="plain">
                下载到 {{ it.pool || defaultPool || '默认池' }}
              </el-tag>
            </div>
            <div class="mk-file mono">{{ it.file_name || it.url }}</div>

            <!-- 下载区：空闲出按钮、下载中出进度条（分钟级大文件，进度要醒目）、完成出结果标签 -->
            <div v-if="stateOf(it.key).phase === 'idle'" class="mk-actions">
              <el-button
                type="primary"
                :icon="Download"
                :loading="stateOf(it.key).submitting"
                @click="download(it)"
              >下载</el-button>
              <el-link
                v-if="it.official"
                :href="it.official"
                target="_blank"
                type="info"
                class="mk-official"
              >官方来源</el-link>
            </div>

            <div v-else-if="stateOf(it.key).phase === 'downloading'" class="mk-progress">
              <el-progress
                :percentage="stateOf(it.key).percent"
                :stroke-width="10"
                striped
                striped-flow
                :duration="16"
              />
              <div class="mk-progress-text">
                {{ stateOf(it.key).phaseText || '正在下载' }} · {{ stateOf(it.key).percent }}%，请保持平台宿主机在线
              </div>
            </div>

            <div v-else class="mk-done">
              <el-tag type="success" effect="light">
                <el-icon style="vertical-align: -2px"><CircleCheck /></el-icon>
                已下载并登记进镜像库
              </el-tag>
              <el-button text type="primary" @click="resetItem(it.key)">再下一次</el-button>
            </div>
          </el-card>
        </el-col>
      </el-row>

      <el-empty v-else-if="!loading && !loadError" description="镜像市场清单为空" :image-size="90" />
    </el-card>
  </div>
</template>

<script setup>
// embedded=true 时作为嵌入组件使用（镜像管理页「镜像市场」tab），隐藏独立页头
const props = defineProps({ embedded: { type: Boolean, default: false } })
import { ref, reactive, onMounted, onUnmounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh, Download, CircleCheck } from '@element-plus/icons-vue'
import http from '../api'
import { errMsg, clampPct } from '../utils/format'
import { pollTask, extractTaskId, taskErrorMessage } from '../utils/task.js'

const items = ref([])
const defaultPool = ref('')
const loading = ref(false)
const loadError = ref('')

// 每个镜像一条下载态：phase idle→downloading→done；submitting 是提交任务瞬间的按钮 loading
// （提交后进入轮询期，progress 条接管反馈，按钮不再存在，天然防重复点击）
const dlStates = reactive({})

function stateOf(key) {
  if (!dlStates[key]) dlStates[key] = { phase: 'idle', submitting: false, percent: 0, phaseText: '' }
  return dlStates[key]
}

function resetItem(key) {
  dlStates[key] = { phase: 'idle', submitting: false, percent: 0, phaseText: '' }
}

/**
 * 字节 → GB（两位小数）。
 * 不用 fmtSizeBytes 的原因：其一位小数粒度在亚 GB 云镜像上区分度不足
 * （6 个镜像里 0.6 GB 会出现两次），这里需要 0.58 / 0.68 这种精度帮用户对比。
 */
function hintGB(bytes) {
  const n = Number(bytes)
  if (!isFinite(n) || n <= 0) return '—'
  return (n / 1024 / 1024 / 1024).toFixed(2) + ' GB'
}

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const res = await http.get('/images/market')
    const data = res.data.data || {}
    items.value = data.items || []
    defaultPool.value = data.pool || ''
  } catch (e) {
    items.value = []
    loadError.value = errMsg(e, '镜像市场清单加载失败')
  } finally {
    loading.value = false
  }
}

// 离开页面后停止更新界面状态（后台轮询由 pollTask 自然结束或超时，不写状态即可）
let disposed = false
onUnmounted(() => {
  disposed = true
})

onMounted(load)

/**
 * 下载一个云镜像：提交 202 任务 → pollTask 轮询到终态。
 * 大文件分钟级：timeout 给足 30 分钟；progress 由后端任务回写，onProgress 直接透传给进度条。
 */
async function download(item) {
  const st = stateOf(item.key)
  if (st.phase !== 'idle' || st.submitting) return // 下载中/已完成态防重复点击
  st.submitting = true
  try {
    const res = await http.post('/images/market/download', { key: item.key, pool: item.pool || '' })
    const taskId = extractTaskId(res.data)
    st.phase = 'downloading'
    st.phaseText = '等待任务调度'
    st.percent = 0
    await pollTask(taskId, {
      interval: 2000,
      timeout: 30 * 60 * 1000,
      onProgress: (task) => {
        if (disposed) return
        st.percent = clampPct(task.progress)
        st.phaseText = task.status === 'running' ? '正在下载' : '等待任务调度'
      }
    })
    if (disposed) return
    st.phase = 'done'
    st.percent = 100
    ElMessage.success(`「${item.name}」下载完成，已登记进镜像库`)
  } catch (e) {
    if (disposed) return
    st.phase = 'idle' // 失败回到可重试态
    ElMessage.error(taskErrorMessage(e, '下载任务提交失败'))
  } finally {
    st.submitting = false
  }
}
</script>

<style scoped>
.mk-card {
  margin-bottom: 16px;
  display: flex;
  flex-direction: column;
}
.mk-card :deep(.el-card__body) {
  display: flex;
  flex-direction: column;
  flex: 1;
}
.mk-name {
  font-size: 1rem;
  font-weight: 600;
}
.mk-desc {
  margin: 6px 0 10px;
  min-height: 40px;
  color: var(--color-muted-foreground);
  font-size: 0.85rem;
  line-height: 1.6;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.mk-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.mk-file {
  margin: 10px 0 14px;
  color: var(--color-muted-foreground);
  font-size: 0.75rem;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.mk-actions {
  margin-top: auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.mk-official {
  font-size: 0.85rem;
}
.mk-progress {
  margin-top: auto;
}
.mk-progress-text {
  margin-top: 6px;
  color: var(--color-muted-foreground);
  font-size: 0.8rem;
}
.mk-done {
  margin-top: auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
}
</style>
