<template>
  <div v-loading="loading">
    <div class="page-head">
      <h2 class="page-title">审计日志</h2>
      <span class="page-desc">记录平台关键操作，可按操作类型、对象、时间等条件追溯</span>
    </div>

    <el-card shadow="never">
      <div class="filters">
        <el-select v-model="q.action" placeholder="操作类型" clearable filterable style="width: 180px" @change="load">
          <el-option v-for="a in actionOptions" :key="a.value" :label="a.label" :value="a.value" />
        </el-select>
        <el-select v-model="q.object_type" placeholder="对象类型" clearable style="width: 130px" @change="load">
          <el-option v-for="o in objectOptions" :key="o" :label="o" :value="o" />
        </el-select>
        <el-input v-model="q.username" placeholder="操作人" clearable style="width: 140px" @keyup.enter="load" @clear="load" />
        <el-select v-model="q.status" placeholder="状态" clearable style="width: 110px" @change="load">
          <el-option label="成功" value="success" />
          <el-option label="失败" value="failed" />
        </el-select>
        <el-date-picker
          v-model="range"
          type="daterange"
          range-separator="至"
          start-placeholder="开始日期"
          end-placeholder="结束日期"
          value-format="YYYY-MM-DD"
          @change="onRange"
        />
        <el-button type="primary" :icon="Search" :loading="loading" @click="load">查询</el-button>
        <el-button :icon="RefreshLeft" @click="reset">重置</el-button>
      </div>

      <el-table :data="items" stripe border style="width: 100%">
        <el-table-column label="时间" min-width="172">
          <template #default="{ row }">
            <span class="mono">{{ fmtTime(row.created_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="username" label="操作人" width="120" />
        <el-table-column label="操作" width="160">
          <template #default="{ row }">
            <el-tag effect="plain">{{ actionLabel(row.action) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="object_type" label="对象" width="100" />
        <el-table-column label="来源 IP" width="150">
          <template #default="{ row }">
            <span class="mono">{{ row.source_ip || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="row.status === 'success' ? 'success' : 'danger'" effect="light">
              {{ row.status === 'success' ? '成功' : '失败' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="详情" min-width="220">
          <template #default="{ row }">
            <el-tooltip
              v-if="row.detail"
              :content="row.detail"
              placement="top"
              :show-after="300"
              :enterable="false"
            >
              <span class="detail-cell mono" @click="openDetail(row)">{{ row.detail }}</span>
            </el-tooltip>
            <span v-else class="muted">—</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="90" fixed="right">
          <template #default="{ row }">
            <el-button size="small" :icon="View" @click="openDetail(row)">详情</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-pagination
        class="pager"
        layout="total, prev, pager, next"
        :total="total"
        :current-page="q.page"
        :page-size="q.page_size"
        @current-change="onPage"
      />
    </el-card>

    <!-- 审计详情弹窗 -->
    <el-dialog v-model="detailDialog" title="审计详情" width="580px">
      <div v-if="current" class="detail-grid">
        <div class="d-item">
          <span class="d-label">时间</span>
          <span class="mono">{{ fmtTime(current.created_at) }}</span>
        </div>
        <div class="d-item">
          <span class="d-label">操作人</span>
          <span>{{ current.username || '—' }}</span>
        </div>
        <div class="d-item">
          <span class="d-label">操作</span>
          <el-tag effect="plain">{{ actionLabel(current.action) }}</el-tag>
        </div>
        <div class="d-item">
          <span class="d-label">对象</span>
          <span>{{ current.object_type || '—' }}</span>
        </div>
        <div class="d-item">
          <span class="d-label">来源 IP</span>
          <span class="mono">{{ current.source_ip || '—' }}</span>
        </div>
        <div class="d-item">
          <span class="d-label">状态</span>
          <el-tag :type="current.status === 'success' ? 'success' : 'danger'" effect="light" size="small">
            {{ current.status === 'success' ? '成功' : '失败' }}
          </el-tag>
        </div>
        <div class="d-item d-full">
          <span class="d-label">详情</span>
          <pre class="detail-text">{{ current.detail || '—' }}</pre>
        </div>
      </div>
      <template #footer>
        <el-button @click="detailDialog = false">关闭</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Search, RefreshLeft, View } from '@element-plus/icons-vue'
import { api } from '../api'

const items = ref([])
const total = ref(0)
const loading = ref(false)
const range = ref(null)
const detailDialog = ref(false)
const current = ref(null)

const objectOptions = ['vm', 'host', 'image', 'user', 'system']

// 兜底映射：auditActions() 不可用时兜底展示中文
const FALLBACK_ACTION_LABELS = {
  login: '登录', logout: '登出',
  create_vm: '创建虚拟机', delete_vm: '删除虚拟机', start_vm: '开机', stop_vm: '关机',
  restart_vm: '重启', import_vm: '导入虚拟机', pause_vm: '暂停虚拟机', resume_vm: '恢复虚拟机',
  clone_vm: '克隆虚拟机',
  attach_disk: '挂载磁盘', detach_disk: '移除磁盘', attach_nic: '添加网卡', detach_nic: '移除网卡',
  create_snapshot: '创建快照', delete_snapshot: '删除快照', revert_snapshot: '回滚快照',
  update_vm_spec: '更新虚拟机配置', update_vm_xml: '更新虚拟机XML', update_vm: '更新虚拟机',
  set_vcpu: '调整CPU核数', set_memory: '调整内存', set_autostart: '设置开机自启', set_boot: '设置引导顺序',
  create_host: '添加宿主机', update_host: '更新宿主机', delete_host: '删除宿主机',
  upload_image: '上传镜像', delete_image: '删除镜像', set_image_template: '设置镜像模板', clone_image: '镜像创建虚拟机',
  create_network: '创建网络', update_network: '更新网络', delete_network: '删除网络',
  create_volume: '创建存储卷', delete_volume: '删除存储卷', access: '访问'
}

const actionMap = ref({ ...FALLBACK_ACTION_LABELS })

const actionOptions = computed(() =>
  Object.keys(actionMap.value)
    .map((k) => ({ value: k, label: actionMap.value[k] || k }))
    .sort((a, b) => a.label.localeCompare(b.label, 'zh-CN'))
)

function actionLabel(action) {
  if (!action) return '—'
  return actionMap.value[action] || action
}

const q = reactive({ action: '', object_type: '', username: '', status: '', start: '', end: '', page: 1, page_size: 20 })

function fmtTime(s) {
  if (!s) return '—'
  const d = new Date(s)
  if (isNaN(d.getTime())) return s
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

function onRange(val) {
  if (val && val.length === 2) {
    q.start = val[0]
    q.end = val[1]
  } else {
    q.start = ''
    q.end = ''
  }
  load()
}

function onPage(p) {
  q.page = p
  load()
}

function reset() {
  Object.assign(q, { action: '', object_type: '', username: '', status: '', start: '', end: '', page: 1 })
  range.value = null
  load()
}

async function load() {
  loading.value = true
  try {
    const params = {}
    for (const k of ['action', 'object_type', 'username', 'status', 'start', 'end', 'page', 'page_size']) {
      if (q[k]) params[k] = q[k]
    }
    const res = await api.listAudit(params)
    items.value = (res.data && res.data.items) || []
    total.value = (res.data && res.data.total) || 0
  } catch (e) {
    ElMessage.error((e.response && e.response.data && e.response.data.message) || '获取审计日志失败')
  } finally {
    loading.value = false
  }
}

function openDetail(row) {
  current.value = row
  detailDialog.value = true
}

// 拉取后端操作类型→中文映射（{action: label}），转数组供下拉展示
async function loadActions() {
  try {
    const res = await api.auditActions()
    const map = (res && res.data) || {}
    if (map && typeof map === 'object' && Object.keys(map).length) {
      actionMap.value = { ...FALLBACK_ACTION_LABELS, ...map }
    }
  } catch (e) {
    // 接口失败时保留前端兜底映射
  }
}

onMounted(() => {
  load()
  loadActions()
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
.page-desc {
  color: var(--color-muted-foreground);
  font-size: 0.9rem;
}
.filters {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-lg);
  margin-bottom: var(--space-xl);
}
.pager {
  margin-top: var(--space-xl);
  justify-content: flex-end;
}
.mono {
  font-family: var(--font-mono);
  font-size: 0.85rem;
}
.muted {
  color: var(--color-muted-foreground);
}
.detail-cell {
  display: block;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-primary);
  cursor: pointer;
}
.detail-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--space-lg) var(--space-2xl);
}
.d-item {
  display: flex;
  flex-direction: column;
  gap: var(--space-sm);
}
.d-full {
  grid-column: 1 / -1;
}
.d-label {
  color: var(--color-muted-foreground);
  font-size: 0.8rem;
}
.detail-text {
  margin: 0;
  padding: var(--space-lg);
  background: var(--color-muted);
  border-radius: var(--radius-sm);
  font-family: var(--font-mono);
  font-size: 0.82rem;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 320px;
  overflow: auto;
}
</style>