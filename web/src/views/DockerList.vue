<template>
  <div>
    <div class="page-head">
      <div>
        <h3 class="page-title">Docker 管理</h3>
        <p class="page-desc">管理宿主机上的 Docker 容器与镜像（容器启停 / 日志查看 / 镜像清理）</p>
      </div>
    </div>

    <!-- 后端 Docker 不可用（HTTP 503）时的引导提示 -->
    <el-alert
      v-if="backendError"
      type="error"
      :title="backendError"
      description="安装并启动 Docker 后可用（systemctl enable --now docker）"
      show-icon
      :closable="false"
      style="margin-bottom: 12px"
    />

    <el-card shadow="never">
      <div class="toolbar">
        <el-radio-group v-model="view" @change="switchView">
          <el-radio-button value="containers">容器</el-radio-button>
          <el-radio-button value="images">镜像</el-radio-button>
        </el-radio-group>
        <span class="count">共 {{ view === 'containers' ? containers.length : images.length }} 个{{ view === 'containers' ? '容器' : '镜像' }}</span>
        <el-button type="primary" :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
      </div>

      <!-- ===== 容器列表 ===== -->
      <el-table v-show="view === 'containers'" :data="containers" v-loading="loading" stripe size="small">
        <template #empty><el-empty description="暂无容器" :image-size="80" /></template>
        <el-table-column label="名称" min-width="160" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="mono">{{ containerName(row.Names) }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="Image" label="镜像" min-width="180" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="mono">{{ row.Image || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="stateTag(row.State)" effect="light" size="small">{{ row.State || '未知' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="Status" label="明细" min-width="150" show-overflow-tooltip>
          <template #default="{ row }">{{ row.Status || '—' }}</template>
        </el-table-column>
        <el-table-column label="端口" min-width="170" show-overflow-tooltip>
          <template #default="{ row }">{{ portsText(row.Ports) }}</template>
        </el-table-column>
        <el-table-column label="创建时间" width="170">
          <template #default="{ row }">
            <span class="mono">{{ dockerTime(row.Created) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="270" fixed="right">
          <template #default="{ row }">
            <el-button
              v-if="row.State !== 'running'"
              size="small" type="success" plain
              :loading="actingKey === row.ID + ':start'"
              :disabled="!!actingKey && actingKey !== row.ID + ':start'"
              @click="containerAction(row, 'start')"
            >启动</el-button>
            <el-button
              v-if="row.State === 'running'"
              size="small" type="warning" plain
              :loading="actingKey === row.ID + ':stop'"
              :disabled="!!actingKey && actingKey !== row.ID + ':stop'"
              @click="containerAction(row, 'stop')"
            >停止</el-button>
            <el-button
              size="small" type="primary" plain
              :loading="actingKey === row.ID + ':restart'"
              :disabled="!!actingKey && actingKey !== row.ID + ':restart'"
              @click="containerAction(row, 'restart')"
            >重启</el-button>
            <el-button size="small" text type="primary" @click="openLogs(row)">日志</el-button>
            <el-button size="small" text type="danger" @click="removeContainer(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>

      <!-- ===== 镜像列表 ===== -->
      <el-table v-show="view === 'images'" :data="images" v-loading="loading" stripe size="small">
        <template #empty><el-empty description="暂无镜像" :image-size="80" /></template>
        <el-table-column label="仓库" min-width="220" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="mono">{{ row.Repository || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="Tag" width="130">
          <template #default="{ row }">
            <el-tag effect="plain" size="small">{{ row.Tag || '—' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="ID" width="130">
          <template #default="{ row }">
            <span class="mono">{{ shortId(row.ID) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="大小" width="110">
          <template #default="{ row }">{{ dockerSize(row.Size) }}</template>
        </el-table-column>
        <el-table-column label="创建时间" width="170">
          <template #default="{ row }">
            <span class="mono">{{ dockerTime(row.Created) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="90" fixed="right">
          <template #default="{ row }">
            <el-button size="small" text type="danger" @click="removeImage(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 容器日志抽屉：深色背景等宽展示，支持复制 -->
    <el-drawer v-model="logsDrawer" :title="'容器日志 — ' + logsName" size="55%">
      <div v-loading="logsLoading">
        <div class="logs-toolbar">
          <el-button size="small" :icon="Refresh" :loading="logsLoading" @click="fetchLogs">刷新</el-button>
          <el-button size="small" :icon="CopyDocument" @click="copyLogs">复制</el-button>
          <span class="logs-hint">最近 200 行</span>
        </div>
        <pre class="logs-pre">{{ logsText || '（暂无日志输出）' }}</pre>
      </div>
    </el-drawer>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, CopyDocument } from '@element-plus/icons-vue'
import http from '../api'
import { errMsg, isCancel, fmtDateTime, fmtSizeBytes } from '../utils/format'

// ===== 视图与数据 =====
const view = ref('containers')
const loading = ref(false)
const containers = ref([])
const images = ref([])
// HTTP 503（Docker 守护进程不可用）时置为后端 message，页面顶部 alert 展示；成功加载后清空
const backendError = ref('')

function containerName(names) {
  if (Array.isArray(names)) return names.join(', ') || '—'
  return names || '—'
}

// 容器状态 → tag 颜色：running 绿 / exited 灰 / 其他橙
function stateTag(state) {
  if (state === 'running') return 'success'
  if (state === 'exited') return 'info'
  return 'warning'
}

// Ports 兼容两类后端形态：docker SDK 对象数组（{IP,PrivatePort,PublicPort,Type}）或已拼好的字符串
function portsText(ports) {
  if (!ports || (Array.isArray(ports) && ports.length === 0)) return '—'
  if (Array.isArray(ports)) {
    return ports
      .map((p) => {
        const host = p.PublicPort ? `${p.IP || ''}:${p.PublicPort}->` : ''
        return `${host}${p.PrivatePort}/${p.Type || 'tcp'}`
      })
      .join('  ')
  }
  return String(ports)
}

// ID 截短 12 位（docker 惯例）
function shortId(id) {
  return id ? String(id).replace(/^sha256:/, '').slice(0, 12) : '—'
}

// Created 兼容 unix 秒级时间戳与「3 days ago」这类相对时间串；解析不了原样展示
function dockerTime(v) {
  if (v === null || v === undefined || v === '') return '—'
  if (typeof v === 'number') return fmtDateTime(v * (v > 1e12 ? 1 : 1000))
  const d = new Date(v)
  if (!isNaN(d.getTime()) && /\d{4}/.test(String(v))) return fmtDateTime(d)
  return String(v)
}

// Size 兼容字节（数字）与「1.2GB」（字符串）两种形态
function dockerSize(v) {
  if (v === null || v === undefined || v === '') return '—'
  if (typeof v === 'number') return fmtSizeBytes(v)
  return String(v)
}

async function fetchContainers() {
  const res = await http.get('/docker/containers')
  const data = res.data.data || {}
  containers.value = data.items || []
}

async function fetchImages() {
  const res = await http.get('/docker/images')
  const data = res.data.data || {}
  images.value = data.items || []
}

// 手动刷新 / 首屏：带 loading；503 走页面顶部 alert，其余错误弹消息
async function load() {
  loading.value = true
  try {
    if (view.value === 'containers') await fetchContainers()
    else await fetchImages()
    backendError.value = ''
  } catch (e) {
    if (e.response && e.response.status === 503) {
      backendError.value = errMsg(e, 'Docker 服务不可用')
    } else {
      ElMessage.error(errMsg(e, '获取 Docker 数据失败'))
    }
  } finally {
    loading.value = false
  }
}

// 容器视图 10s 静默轮询：不动 loading，失败不打扰用户（503 时同步刷新顶部 alert）
let pollTimer = null
let refreshing = false
async function silentRefresh() {
  if (view.value !== 'containers' || refreshing || loading.value) return
  refreshing = true
  try {
    await fetchContainers()
    backendError.value = ''
  } catch (e) {
    if (e.response && e.response.status === 503) {
      backendError.value = errMsg(e, 'Docker 服务不可用')
    }
  } finally {
    refreshing = false
  }
}

// ===== 容器操作（启动 / 停止 / 重启，per-row loading 防连点） =====
const actingKey = ref('')

async function containerAction(row, action) {
  const label = { start: '启动', stop: '停止', restart: '重启' }[action]
  actingKey.value = row.ID + ':' + action
  try {
    await http.post('/docker/containers/' + row.ID + '/' + action)
    ElMessage.success(`已${label} ${containerName(row.Names)}`)
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, `${label}失败`))
  } finally {
    actingKey.value = ''
  }
}

async function removeContainer(row) {
  const running = row.State === 'running'
  const name = containerName(row.Names)
  try {
    await ElMessageBox.confirm(
      running
        ? `容器 ${name} 正在运行，将强制删除（force），容器内未持久化的数据会丢失。确定删除？`
        : `确定删除容器 ${name}？`,
      '删除容器',
      { type: 'warning', confirmButtonText: '删除', confirmButtonClass: 'el-button--danger' }
    )
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  try {
    // 运行中的容器必须带 force=true，否则 Docker API 拒绝删除
    await http.delete('/docker/containers/' + row.ID, { params: running ? { force: 'true' } : {} })
    ElMessage.success(`已删除 ${name}`)
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

// ===== 容器日志抽屉 =====
const logsDrawer = ref(false)
const logsLoading = ref(false)
const logsText = ref('')
const logsName = ref('')
const logsId = ref('')

function openLogs(row) {
  logsId.value = row.ID
  logsName.value = containerName(row.Names)
  logsText.value = ''
  logsDrawer.value = true
  fetchLogs()
}

async function fetchLogs() {
  if (!logsId.value) return
  logsLoading.value = true
  try {
    const res = await http.get('/docker/containers/' + logsId.value + '/logs', { params: { tail: 200 } })
    const data = res.data.data || {}
    logsText.value = data.logs || ''
  } catch (e) {
    ElMessage.error(errMsg(e, '获取日志失败'))
  } finally {
    logsLoading.value = false
  }
}

async function copyLogs() {
  if (!logsText.value) {
    ElMessage.warning('暂无可复制的日志')
    return
  }
  try {
    await navigator.clipboard.writeText(logsText.value)
    ElMessage.success('日志已复制到剪贴板')
  } catch (e) {
    ElMessage.error('复制失败，请手动选择文本复制')
  }
}

// ===== 镜像删除 =====
async function removeImage(row) {
  const full = `${row.Repository || '—'}:${row.Tag || '—'}`
  try {
    await ElMessageBox.confirm(`确定删除镜像 ${full}（${shortId(row.ID)}）？`, '删除镜像', {
      type: 'warning',
      confirmButtonText: '删除',
      confirmButtonClass: 'el-button--danger'
    })
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  try {
    // 镜像 ID 可能含特殊字符（sha256: 前缀），必须 encodeURIComponent
    await http.delete('/docker/images/' + encodeURIComponent(row.ID))
    ElMessage.success(`已删除镜像 ${full}`)
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

// 切视图后重拉对应数据（避免展示陈旧数据）
function switchView() {
  load()
}

onMounted(() => {
  load()
  // 容器视图 10s 静默刷新（silentRefresh 内部自行判断当前视图）
  pollTimer = setInterval(silentRefresh, 10000)
})
onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
})
</script>

<style scoped>
.logs-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.logs-hint {
  color: var(--el-text-color-secondary, #909399);
  font-size: 0.8rem;
}
.logs-pre {
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
</style>
