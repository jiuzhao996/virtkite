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
            <el-tag :type="sessionTypeTag(row.type)" effect="light">{{ sessionTypeText(row.type) }}</el-tag>
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
          <template #default="{ row }">{{ fmtDateTimeLocale(row.started_at) }}</template>
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
import { sessionTypeText, sessionTypeTag, fmtDateTimeLocale, errMsg, isCancel } from '../utils/format'

const { isAdmin } = useAuth()

const items = ref([])
const total = ref(0)
const loading = ref(false)
const q = ref({ status: '' })

function duration(row) {
  const start = new Date(row.started_at).getTime()
  const end = row.ended_at ? new Date(row.ended_at).getTime() : Date.now()
  const sec = Math.max(0, Math.floor((end - start) / 1000))
  if (sec < 60) return sec + ' 秒'
  if (sec < 3600) return Math.floor(sec / 60) + ' 分钟'
  return (sec / 3600).toFixed(1) + ' 小时'
}

const activeCount = computed(() => items.value.filter((s) => s.status === 'active').length)

// 拉取列表（首屏/手动刷新与静默轮询共用，只负责取数与赋值）
async function fetchSessions() {
  const params = {}
  if (q.value.status) params.status = q.value.status
  const res = await api.listSessions(params)
  items.value = (res.data && res.data.items) || []
  total.value = (res.data && res.data.total) || 0
}

// 首屏 / 手动刷新 / 切筛选：带整页 loading
async function load() {
  loading.value = true
  try {
    await fetchSessions()
  } catch (e) {
    ElMessage.error('获取会话列表失败')
  } finally {
    loading.value = false
  }
}

// 轮询静默刷新：不动 loading，否则整页 v-loading 每 5s 闪一次（与其他轮询页一致）
// 仍保留「上一轮未回 / 首屏加载中就跳过」的守卫，避免请求堆叠（原实现靠 loading 判断）
let refreshing = false
async function silentRefresh() {
  if (refreshing || loading.value) return
  refreshing = true
  try {
    await fetchSessions()
  } catch (e) {
    // 忽略：轮询失败不打扰用户，下一轮自动重试
  } finally {
    refreshing = false
  }
}

let pollTimer = null

async function disconnect(row) {
  try {
    await ElMessageBox.confirm(`确定强制断开 ${row.username || '未知用户'} 的 ${sessionTypeText(row.type)}会话（${row.vm_name}）？`, '确认断开', { type: 'warning' })
    await api.disconnectSession(row.id)
    ElMessage.success('已断开')
    await load()
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '断开失败'))
  }
}

onMounted(() => {
  load()
  pollTimer = setInterval(silentRefresh, getPollInterval('sessions', POLL_DEFAULTS.sessions))
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
  gap: 8px;
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
