<template>
  <div v-loading="loading">
    <div class="page-head">
      <h2 class="page-title">会话管理</h2>
    </div>
    <el-card shadow="never">
      <div class="toolbar">
        <div class="toolbar-left">
          <el-button type="primary" :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <el-select v-model="q.status" placeholder="状态筛选" clearable style="width: 140px" @change="load">
            <el-option label="进行中" value="active" />
            <el-option label="已结束" value="closed" />
          </el-select>
        </div>
        <span class="count">共 {{ total }} 个会话<span v-if="activeCount" class="running-hint"> · {{ activeCount }} 个进行中</span></span>
      </div>
      <el-alert
        type="info"
        :closable="false"
        show-icon
        title="VNC 走 websockify 中转，后端看不到断开事件（靠过期自动收敛），仅 SSH/串口支持服务端强制断开"
        style="margin-bottom: 12px"
      />

      <el-table :data="items" stripe border style="width: 100%">
        <template #empty><el-empty description="暂无会话" :image-size="80" /></template>
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column label="虚拟机" min-width="140">
          <template #default="{ row }">
            <el-link type="primary" @click="$router.push({ name: 'vm-detail', params: { id: row.vm_id } })">{{ row.vm_name }}</el-link>
          </template>
        </el-table-column>
        <el-table-column label="方式" width="110">
          <template #default="{ row }">
            <el-tag :type="typeTag(row.type)" effect="light">{{ typeText(row.type) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.status === 'active' ? 'success' : 'info'" effect="light">
              <span v-if="row.status === 'active'" class="pulse-dot" />
              {{ row.status === 'active' ? '进行中' : '已结束' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="username" label="用户" width="120" />
        <el-table-column prop="client_ip" label="来源 IP" width="140" />
        <el-table-column label="开始时间" width="170">
          <template #default="{ row }">{{ fmtTime(row.started_at) }}</template>
        </el-table-column>
        <el-table-column label="时长" width="110">
          <template #default="{ row }">{{ duration(row) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="110" fixed="right">
          <template #default="{ row }">
            <el-button
              v-if="isAdmin"
              size="small"
              type="warning"
              :disabled="row.status !== 'active' || row.type === 'vnc'"
              :title="row.type === 'vnc' ? 'VNC 中转连接无法强制断开' : '强制断开'"
              @click="disconnect(row)"
            >断开</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { api } from '../api'
import { POLL_DEFAULTS, getPollInterval } from '../utils/settings'
import { useAuth } from '../store/auth'

const { isAdmin } = useAuth()

const items = ref([])
const total = ref(0)
const loading = ref(false)
const q = ref({ status: '' })

function typeText(t) {
  return { vnc: '图形控制台', ssh: 'Web 终端', serial: '串口' }[t] || t
}
function typeTag(t) {
  if (t === 'vnc') return 'primary'
  if (t === 'ssh') return 'success'
  return 'warning'
}
function fmtTime(s) {
  if (!s) return '—'
  return new Date(s).toLocaleString('zh-CN', { hour12: false })
}
function duration(row) {
  const start = new Date(row.started_at).getTime()
  const end = row.ended_at ? new Date(row.ended_at).getTime() : Date.now()
  const sec = Math.max(0, Math.floor((end - start) / 1000))
  if (sec < 60) return sec + ' 秒'
  if (sec < 3600) return Math.floor(sec / 60) + ' 分钟'
  return (sec / 3600).toFixed(1) + ' 小时'
}

const activeCount = computed(() => items.value.filter((s) => s.status === 'active').length)

async function load() {
  loading.value = true
  try {
    const params = {}
    if (q.value.status) params.status = q.value.status
    const res = await api.listSessions(params)
    items.value = (res.data && res.data.items) || []
    total.value = (res.data && res.data.total) || 0
  } catch (e) {
    ElMessage.error('获取会话列表失败')
  } finally {
    loading.value = false
  }
}

let pollTimer = null

async function disconnect(row) {
  try {
    await ElMessageBox.confirm(`确定强制断开 ${row.username || '未知用户'} 的 ${typeText(row.type)}会话（${row.vm_name}）？`, '确认断开', { type: 'warning' })
    await api.disconnectSession(row.id)
    ElMessage.success('已断开')
    await load()
  } catch (e) {
    if (e !== 'cancel') ElMessage.error((e.response && e.response.data && e.response.data.message) || '断开失败')
  }
}

onMounted(() => {
  load()
  pollTimer = setInterval(() => {
    if (!loading.value) load()
  }, getPollInterval('sessions', POLL_DEFAULTS.sessions))
})
onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
})
</script>

<style scoped>
.page-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-xl);
}
.page-title {
  margin: 0;
  font-size: 1.1rem;
  font-weight: 700;
}
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-xl);
}
.toolbar-left {
  display: flex;
  align-items: center;
  gap: 8px;
}
.count {
  color: var(--color-muted-foreground);
  font-size: 0.9rem;
}
.running-hint {
  color: var(--color-accent);
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
</style>
