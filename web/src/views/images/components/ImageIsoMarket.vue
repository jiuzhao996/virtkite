<template>
  <div>
    <PageHead
      v-if="!embedded"
      title="官方 ISO 市场"
      subtitle="一键下载官方安装镜像到存储池；下载为分钟级后台任务，进度可在任务中心跟踪"
    >
      <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
    </PageHead>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      style="margin-bottom: 12px"
      title="安装 ISO 用于创建虚拟机的「本地安装介质 (ISO)」方式——传统光盘装机流程。下载完成自动入池，即可在创建向导中选择。"
    />

    <el-row :gutter="16">
      <el-col v-for="it in items" :key="it.key" :xs="24" :md="12" :lg="8">
        <el-card shadow="hover" class="iso-card">
          <div class="iso-name">{{ it.name }}</div>
          <div class="iso-desc">{{ it.description }}</div>
          <div class="iso-meta">
            <el-tag size="small" effect="plain">≈ {{ hintGB(it.size_hint) }}</el-tag>
            <el-tag size="small" type="primary" effect="plain">
              下载到 {{ it.pool || defaultPool || '默认池' }}
            </el-tag>
          </div>
          <div class="iso-file mono">{{ it.file_name || it.url }}</div>

          <!-- 下载区：空闲出按钮、下载中出进度条、完成出结果 -->
          <div v-if="stateOf(it.key).phase === 'idle'" class="iso-actions">
            <el-button
              type="primary"
              :icon="Download"
              :loading="stateOf(it.key).submitting"
              @click="download(it)"
            >下载</el-button>
            <el-link v-if="it.official" :href="it.official" target="_blank" underline="never" class="iso-official">官方来源</el-link>
          </div>
          <div v-else-if="stateOf(it.key).phase === 'downloading'" class="iso-actions">
            <el-progress :percentage="stateOf(it.key).percent" style="flex: 1" />
          </div>
          <div v-else-if="stateOf(it.key).phase === 'done'" class="iso-actions">
            <el-tag type="success" effect="light">✔ 已下载入池，可在「ISO 安装镜像」tab 查看</el-tag>
          </div>
        </el-card>
      </el-col>
    </el-row>
    <el-empty v-if="!items.length && !loading" description="清单为空" />
  </div>
</template>

<script setup>
// 官方安装 ISO 市场（云镜像之外的另一形态——传统光盘装机介质）。
// 后端 /api/images/market/iso*（image_market_iso.go），下载复用 image_download executor。
import { ref, onMounted, onUnmounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh, Download } from '@element-plus/icons-vue'
import PageHead from '../../../components/PageHead.vue'
import http from '../../../api'
import { taskErrorMessage } from '../../../utils/task'

const props = defineProps({
  embedded: { type: Boolean, default: false }
})

const items = ref([])
const loading = ref(false)
const disposed = ref(false)
const defaultPool = ref('')

const dlStates = {}
function stateOf(key) {
  if (!dlStates[key]) dlStates[key] = { phase: 'idle', submitting: false, percent: 0, phaseText: '' }
  return dlStates[key]
}
function hintGB(bytes) {
  return (Number(bytes || 0) / 1024 / 1024 / 1024).toFixed(1) + ' GB'
}

async function load() {
  loading.value = true
  try {
    const res = await http.get('/images/market/iso')
    const d = res.data && res.data.data ? res.data.data : res.data
    items.value = d.items || []
    defaultPool.value = d.pool || ''
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '获取安装 ISO 清单失败'))
  } finally {
    loading.value = false
  }
}

async function download(item) {
  const st = stateOf(item.key)
  if (st.phase !== 'idle' || st.submitting) return
  st.submitting = true
  try {
    const res = await http.post('/images/market/iso/download', { key: item.key, pool: item.pool || '' })
    const taskId = extractTaskId(res.data)
    st.phase = 'downloading'
    st.phaseText = '等待任务调度'
    st.percent = 0
    await pollTask(taskId, {
      interval: 2000,
      timeout: 60 * 60 * 1000, // ISO 4~10GB，放宽到 1 小时
      onProgress: (task) => {
        if (disposed.value) return
        st.percent = clampPct(task.progress)
        st.phaseText = task.status === 'running' ? '正在下载' : '等待任务调度'
      }
    })
    if (disposed.value) return
    st.phase = 'done'
    st.percent = 100
    ElMessage.success(`「${item.name}」下载完成，已入池`)
  } catch (e) {
    if (disposed.value) return
    st.phase = 'idle'
    ElMessage.error(taskErrorMessage(e, '下载任务提交失败'))
  } finally {
    st.submitting = false
  }
}

function extractTaskId(data) {
  if (typeof data === 'object' && data) {
    if (data.task_id) return data.task_id
    if (data.data && data.data.task_id) return data.data.task_id
  }
  throw new Error('响应缺少 task_id')
}
async function pollTask(taskId, { interval, timeout, onProgress }) {
  const deadline = Date.now() + timeout
  for (;;) {
    if (Date.now() > deadline) throw new Error('下载超时')
    await new Promise((r) => setTimeout(r, interval))
    const res = await http.get('/tasks/' + taskId)
    const d = res.data && res.data.data ? res.data.data : res.data
    if (onProgress && d) onProgress(d)
    if (d.status === 'success') return d
    if (d.status === 'failed') throw new Error(d.error || '下载失败')
  }
}
function clampPct(p) {
  const n = Number(p) || 0
  return Math.max(0, Math.min(100, Math.round(n)))
}

onMounted(load)
onUnmounted(() => { disposed.value = true })
</script>

<style scoped>
.iso-card {
  margin-bottom: 16px;
}
.iso-name {
  font-weight: 600;
  font-size: 1.02rem;
  margin-bottom: 4px;
}
.iso-desc {
  color: var(--color-muted-foreground);
  font-size: 0.85rem;
  min-height: 2.4em;
  margin-bottom: 8px;
}
.iso-meta {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 6px;
}
.iso-file {
  font-size: 0.78rem;
  color: var(--color-muted-foreground);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  margin-bottom: 10px;
}
.iso-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 32px;
}
.iso-official {
  margin-left: auto;
  font-size: 12px;
}
</style>
