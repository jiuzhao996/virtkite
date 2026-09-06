<template>
  <div v-loading="loading">
    <div class="page-head">
      <h2 class="page-title">任务中心</h2>
    </div>
    <el-card shadow="never">
      <div class="toolbar">
        <div class="toolbar-left">
          <el-button type="primary" :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <el-select v-model="q.status" placeholder="状态筛选" clearable style="width: 140px" @change="load">
            <el-option label="进行中" value="active" />
            <el-option label="成功" value="success" />
            <el-option label="失败" value="failed" />
          </el-select>
          <el-button
            v-if="isAdmin"
            type="danger"
            :icon="Delete"
            :disabled="!finishedCount"
            @click="clearFinished"
          >清理已完成 ({{ finishedCount }})</el-button>
        </div>
        <span class="count">共 {{ total }} 个任务<span v-if="activeCount" class="running-hint"> · {{ activeCount }} 个进行中</span></span>
      </div>

      <el-table :data="items" stripe border style="width: 100%">
        <template #empty><el-empty description="暂无任务" :image-size="80" /></template>
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column prop="title" label="任务" min-width="200" show-overflow-tooltip />
        <el-table-column label="类型" width="130">
          <template #default="{ row }">{{ taskTypeText(row.type) }}</template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="taskStatusTag(row.status)" effect="light">
              <span v-if="row.status === 'running' || row.status === 'pending'" class="pulse-dot" />
              {{ taskStatusText(row.status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="进度" min-width="150">
          <template #default="{ row }">
            <el-progress
              :percentage="row.progress || 0"
              :status="row.status === 'failed' ? 'exception' : row.status === 'success' ? 'success' : ''"
              :format="() => (row.progress || 0) + '%'"
            />
          </template>
        </el-table-column>
        <el-table-column prop="vm_name" label="虚拟机" width="140" show-overflow-tooltip />
        <el-table-column prop="username" label="执行人" width="110" />
        <el-table-column label="结果/错误" min-width="200" show-overflow-tooltip>
          <template #default="{ row }">
            <span v-if="row.status === 'failed'" class="err-text">{{ row.error || '失败' }}</span>
            <span v-else-if="row.status === 'success'" class="ok-text">完成</span>
            <span v-else class="muted-text">执行中…</span>
          </template>
        </el-table-column>
        <el-table-column prop="created_at" label="创建时间" width="170">
          <template #default="{ row }">{{ fmtDateTimeLocale(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="90" fixed="right">
          <template #default="{ row }">
            <el-button
              v-if="isAdmin"
              size="small"
              type="danger"
              :icon="Delete"
              :disabled="row.status !== 'success' && row.status !== 'failed'"
              @click="remove(row)"
            >删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Delete } from '@element-plus/icons-vue'
import { api } from '../api'
import { POLL_DEFAULTS, getPollInterval } from '../utils/settings'
import { useAuth } from '../store/auth'
import { taskTypeText, taskStatusText, taskStatusTag, fmtDateTimeLocale, errMsg, isCancel } from '../utils/format'

const { isAdmin } = useAuth()

const items = ref([])
const total = ref(0)
const loading = ref(false)
const q = ref({ status: '', limit: 50 })

const activeCount = computed(() => items.value.filter((t) => t.status === 'pending' || t.status === 'running').length)
const finishedCount = computed(() => items.value.filter((t) => t.status === 'success' || t.status === 'failed').length)

async function load() {
  loading.value = true
  try {
    const params = { limit: q.value.limit }
    // 后端 status 精确匹配；"进行中"需前端合并 pending+running，故传空全拉再过滤
    const res = await api.listTasks(params)
    let list = (res.data && res.data.items) || []
    if (q.value.status === 'active') {
      list = list.filter((t) => t.status === 'pending' || t.status === 'running')
    } else if (q.value.status) {
      list = list.filter((t) => t.status === q.value.status)
    }
    items.value = list
    total.value = (res.data && res.data.total) || 0
  } catch (e) {
    ElMessage.error('获取任务列表失败')
  } finally {
    loading.value = false
  }
}

// 智能轮询：有进行中任务才刷（3s），无则停
let pollTimer = null
function tick() {
  if (activeCount.value > 0) {
    load()
  }
}

async function remove(row) {
  try {
    await ElMessageBox.confirm(`确定删除任务「${row.title}」的记录？`, '确认删除', { type: 'warning' })
    await api.deleteTask(row.id)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '删除失败'))
  }
}

async function clearFinished() {
  const done = items.value.filter((t) => t.status === 'success' || t.status === 'failed')
  if (!done.length) return
  try {
    await ElMessageBox.confirm(`确定清理 ${done.length} 条已完成任务记录？`, '确认清理', { type: 'warning' })
    for (const t of done) {
      try {
        await api.deleteTask(t.id)
      } catch (e) {}
    }
    ElMessage.success('已清理')
    await load()
  } catch (e) {
    if (!isCancel(e)) ElMessage.error('清理失败')
  }
}

onMounted(() => {
  load()
  pollTimer = setInterval(tick, getPollInterval('tasks', POLL_DEFAULTS.tasks))
})
onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
})
</script>

<style scoped>
/* .page-head / .page-title / .toolbar / .count 已收进 global.css */
.toolbar-left {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
.running-hint {
  color: var(--el-color-primary);
}
.pulse-dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentcolor;
  margin-right: 4px;
  animation: breathe 1.6s ease-in-out infinite;
}
@keyframes breathe {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.4; }
}
.err-text {
  color: var(--el-color-danger);
  font-size: 0.85rem;
}
.ok-text {
  color: var(--el-color-success);
  font-size: 0.85rem;
}
.muted-text {
  color: var(--color-muted-foreground);
  font-size: 0.85rem;
}
</style>
