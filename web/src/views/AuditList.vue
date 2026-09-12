<template>
  <div v-loading="loading">
    <div class="page-head">
      <div>
        <h2 class="page-title">审计中心</h2>
        <span class="page-desc">操作日志记录谁、何时、对哪个对象做了什么、成功还是失败；控制台会话记录谁连过哪台虚拟机。均为只读记录，用于安全追溯</span>
      </div>
    </div>

    <el-tabs v-model="activeTab">
      <!-- 操作日志仅管理员可见（后端 /api/audit admin-only）；viewer 只能看会话流水 -->
      <el-tab-pane v-if="isAdmin" label="操作日志" name="ops">
        <el-card shadow="never">
      <div class="filters">
        <el-select v-model="q.action" placeholder="操作类型" clearable filterable style="width: 180px" @change="search">
          <el-option v-for="a in actionOptions" :key="a.value" :label="a.label" :value="a.value" />
        </el-select>
        <el-select v-model="q.object_type" placeholder="对象类型" clearable style="width: 130px" @change="search">
          <el-option v-for="o in objectOptions" :key="o" :label="o" :value="o" />
        </el-select>
        <el-input v-model="q.username" placeholder="操作人" clearable style="width: 140px" @keyup.enter="search" @clear="search" />
        <el-select v-model="q.status" placeholder="状态" clearable style="width: 110px" @change="search">
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
        <el-button type="primary" :icon="Search" :loading="loading" @click="search">查询</el-button>
        <el-button :icon="RefreshLeft" @click="reset">重置</el-button>
      </div>

      <el-table :data="items" stripe border style="width: 100%">
            <template #empty><el-empty description="暂无审计记录" :image-size="72" /></template>
        <el-table-column label="时间" min-width="172">
          <template #default="{ row }">
            <span class="mono">{{ fmtDateTime(row.created_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作人" width="120">
          <template #default="{ row }">
            <span>{{ row.username || '—' }}</span>
          </template>
        </el-table-column>
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
              popper-class="audit-detail-popper"
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
      </el-tab-pane>
      <el-tab-pane label="控制台会话" name="sessions">
        <SessionList />
      </el-tab-pane>
    </el-tabs>

    <!-- 审计详情弹窗 -->
    <el-dialog v-model="detailDialog" title="审计详情" width="580px">
      <div v-if="current" class="detail-grid">
        <div class="d-item">
          <span class="d-label">时间</span>
          <span class="mono">{{ fmtDateTime(current.created_at) }}</span>
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
import { useAuth } from '../store/auth'
import { FALLBACK_ACTION_LABELS, fmtDateTime, errMsg } from '../utils/format'
import SessionList from './SessionList.vue'

const { isAdmin } = useAuth()
// 默认 tab：管理员落在操作日志，普通用户只有会话流水可看
const activeTab = ref(isAdmin.value ? 'ops' : 'sessions')

const items = ref([])
const total = ref(0)
const loading = ref(false)
const range = ref(null)
const detailDialog = ref(false)
const current = ref(null)

const objectOptions = ['vm', 'host', 'image', 'user', 'system']

// 兜底映射（FALLBACK_ACTION_LABELS）与仪表盘共用，已收进 utils/format.js
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

function onRange(val) {
  if (val && val.length === 2) {
    q.start = val[0]
    q.end = val[1]
  } else {
    q.start = ''
    q.end = ''
  }
  search()
}

// 筛选条件变更：重置到第 1 页再查询
function search() {
  q.page = 1
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
    ElMessage.error(errMsg(e, '获取审计日志失败'))
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
  // 操作日志接口仅管理员可用（/api/audit admin-only），viewer 不发起请求
  if (isAdmin.value) {
    load()
    loadActions()
  }
})
</script>

<style scoped>
/* .page-head / .page-title / .page-desc 已收进 global.css；.mono 的 font-family 亦然，此处只留字号 */
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
/* flex 列默认 stretch 会把 el-tag 拉成整列宽：tag 保持内容宽度 */
.d-item .el-tag {
  align-self: flex-start;
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
/* tooltip 内容 teleport 到 body，scoped 下用 :deep 穿透；超长 detail 不把 tooltip 撑出视口 */
:deep(.audit-detail-popper) {
  max-width: 480px;
}
</style>