<template>
  <div>
    <PageHead title="用户管理">
      <template #subtitle>
        <!-- 历史 p.page-desc（UA 外边距参与布局），经插槽原样保留 -->
        <p class="page-desc">平台账号与角色（admin 管理员 / operator 操作员 / viewer 只读用户）</p>
      </template>
    </PageHead>

    <!-- 角色分布统计条：前端聚合 listUsers 结果，点击 chip 按角色过滤用户表（再点一次取消） -->
    <div class="role-strip">
      <button
        v-for="r in roleStats"
        :key="r.role"
        type="button"
        class="role-chip"
        :class="{ 'is-active': roleFilter === r.role }"
        @click="pickRole(r.role)"
      >
        <span class="role-dot" :style="{ background: r.color }" />
        {{ r.label }}
        <b>{{ r.count }}</b>
      </button>
    </div>

    <el-tabs v-model="activeTab">
      <el-tab-pane label="用户" name="users">
    <el-card shadow="never">
      <!-- 原左右分组 gap 为 var(--space-lg)（12px），经 gap/right-gap 传入保持不变 -->
      <Toolbar gap="var(--space-lg)" right-gap="var(--space-lg)">
        <template #left>
          <el-select v-model="roleFilter" clearable placeholder="全部角色" style="width: 140px">
            <el-option label="管理员" value="admin" />
            <el-option label="操作员" value="operator" />
            <el-option label="只读用户" value="viewer" />
          </el-select>
        </template>
        <template #right>
          <span class="count">共 {{ filteredUsers.length }} 个账号</span>
          <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <el-button v-if="isAdmin" type="primary" :icon="Plus" @click="openCreate">新建用户</el-button>
        </template>
      </Toolbar>

      <!-- 行点击进入用户概览抽屉；行内按钮 .stop 防冒泡 -->
      <el-table :data="filteredUsers" v-loading="loading" stripe @row-click="openUser" row-class-name="clickable-row">
        <el-table-column prop="username" label="用户名" min-width="120" />
        <el-table-column label="角色" width="110">
          <template #default="{ row }">
            <el-tag :type="roleTag(row.role)" effect="plain" size="small">{{ roleText(row.role) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="real_name" label="姓名" width="100">
          <template #default="{ row }">{{ row.real_name || '—' }}</template>
        </el-table-column>
        <el-table-column prop="phone" label="电话" width="130">
          <template #default="{ row }">{{ row.phone || '—' }}</template>
        </el-table-column>
        <el-table-column prop="email" label="邮箱" min-width="160" show-overflow-tooltip>
          <template #default="{ row }">{{ row.email || '—' }}</template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="row.is_active ? 'success' : 'danger'" effect="light" size="small">
              {{ row.is_active ? '启用' : '停用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="最近登录" width="170">
          <template #default="{ row }">
            <span class="mono">{{ row.last_login ? fmtDateTime(row.last_login) : '从未登录' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" width="170">
          <template #default="{ row }">
            <span class="mono">{{ fmtDateTime(row.created_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="150" fixed="right">
          <template #default="{ row }">
            <el-button text type="primary" size="small" @click.stop="openEdit(row)">编辑</el-button>
            <el-button
              text
              type="danger"
              size="small"
              :disabled="row.username === (state.user && state.user.username)"
              @click.stop="remove(row)"
            >删除</el-button>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty description="暂无账号，点击「新建用户」创建第一个平台账号" :image-size="80">
            <el-button v-if="isAdmin" type="primary" plain @click="openCreate">新建用户</el-button>
          </el-empty>
        </template>
      </el-table>
    </el-card>
      </el-tab-pane>
      <el-tab-pane label="用户组" name="groups" lazy>
        <UserGroups />
      </el-tab-pane>
    </el-tabs>

    <!-- 新建 / 编辑（复用一个弹窗：editingId 区分模式；编辑不含密码，改密码由用户本人操作） -->
    <el-dialog :close-on-click-modal="false" v-model="dialog" :title="editingId ? '编辑用户' : '新建用户'" width="440px">
      <el-form :model="form" label-width="80px">
        <el-form-item label="用户名" required>
          <el-input v-model="form.username" :disabled="!!editingId" placeholder="登录名" />
        </el-form-item>
        <el-form-item v-if="!editingId" label="初始密码" required>
          <el-input v-model="form.password" type="password" show-password placeholder="至少 6 位" />
        </el-form-item>
        <el-form-item label="角色" required>
          <el-select v-model="form.role" :disabled="editingSelf" style="width: 100%">
            <el-option label="管理员（全部权限）" value="admin" />
            <el-option label="操作员（可操作虚拟机）" value="operator" />
            <el-option label="只读用户（只读 + 图形控制台）" value="viewer" />
          </el-select>
          <div v-if="editingSelf" class="input-help">不能修改自己当前账号的角色（防止自锁失去管理权限）</div>
        </el-form-item>
        <el-form-item v-if="editingId" label="账号状态">
          <el-switch v-model="form.is_active" :disabled="editingSelf" active-text="启用" inactive-text="停用" />
          <div v-if="editingSelf" class="input-help">不能停用自己当前登录的账号</div>
        </el-form-item>
        <el-form-item label="姓名">
          <el-input v-model="form.real_name" />
        </el-form-item>
        <el-form-item label="电话">
          <el-input v-model="form.phone" />
        </el-form-item>
        <el-form-item label="邮箱">
          <el-input v-model="form.email" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">{{ editingId ? '保存' : '创建' }}</el-button>
      </template>
    </el-dialog>

    <!-- 用户概览抽屉（行点击进入）：基本信息 + 最近操作 + 最近控制台会话。
         敏感纪律：密码字段后端 json:"-" 本就不下发，抽屉也不展示任何凭据信息 -->
    <el-drawer v-model="drawerOpen" title="用户概览" :size="480" :append-to-body="true" destroy-on-close>
      <template v-if="detail.user">
        <div class="ud-head">
          <span class="ud-name">{{ detail.user.username }}</span>
          <el-tag :type="roleTag(detail.user.role)" :effect="detail.user.role === 'admin' ? 'dark' : 'light'" size="small">
            {{ roleText(detail.user.role) }}
          </el-tag>
          <el-tag :type="detail.user.is_active ? 'success' : 'danger'" effect="light" size="small">
            {{ detail.user.is_active ? '启用' : '停用' }}
          </el-tag>
        </div>

        <!-- 字段以 model/user.go 实际下发为准（id/username/role/is_active/real_name/phone/email/last_login/created_at） -->
        <el-descriptions :column="2" border size="small">
          <el-descriptions-item label="姓名">{{ detail.user.real_name || '—' }}</el-descriptions-item>
          <el-descriptions-item label="电话">{{ detail.user.phone || '—' }}</el-descriptions-item>
          <el-descriptions-item label="邮箱" :span="2">{{ detail.user.email || '—' }}</el-descriptions-item>
          <el-descriptions-item label="最近登录">
            <span class="mono">{{ detail.user.last_login ? fmtDateTime(detail.user.last_login) : '从未登录' }}</span>
          </el-descriptions-item>
          <el-descriptions-item label="创建时间">
            <span class="mono">{{ fmtDateTime(detail.user.created_at) }}</span>
          </el-descriptions-item>
        </el-descriptions>

        <!-- 最近操作：GET /audit 支持 username 筛（handler/audit.go LIKE 查询），
             LIKE 前后通配可能带出「用户名含该串」的他人记录，取回后按精确用户名再滤一遍，取前 8 条 -->
        <div class="ud-sec">
          <div class="ud-sec-title">最近操作</div>
          <div v-loading="detail.auditLoading" class="ud-list">
            <div v-for="a in detail.audit" :key="a.id" class="ud-li">
              <span class="dot" :class="a.status === 'success' ? 'is-ok' : 'is-fail'" />
              <span class="ud-main">{{ FALLBACK_ACTION_LABELS[a.action] || a.action }}</span>
              <span class="ud-time mono">{{ fmtDateTime(a.created_at) }}</span>
            </div>
            <div v-if="!detail.auditLoading && detail.audit.length === 0" class="ud-empty">
              {{ detail.auditFailed ? '操作记录加载失败' : '暂无操作记录' }}
            </div>
            <el-link v-else-if="detail.audit.length" type="primary" :underline="false" @click="goAuditCenter">
              进审计中心看全部
            </el-link>
          </div>
        </div>

        <!-- 最近控制台会话：GET /sessions 支持 username 筛（handler/session.go LIKE 查询），同样取回后精确过滤 -->
        <div class="ud-sec">
          <div class="ud-sec-title">最近控制台会话</div>
          <div v-loading="detail.sessLoading" class="ud-list">
            <div v-for="s in detail.sessions" :key="s.id" class="ud-li">
              <span class="dot" :class="s.status === 'active' ? 'is-ok' : 'is-idle'" />
              <el-tag :type="sessionTypeTag(s.type)" effect="plain" size="small">{{ sessionTypeText(s.type) }}</el-tag>
              <span class="ud-main">{{ s.vm_name }}</span>
              <span class="ud-time mono">{{ fmtDateTime(s.started_at) }}</span>
            </div>
            <div v-if="!detail.sessLoading && detail.sessions.length === 0" class="ud-empty">
              {{ detail.sessFailed ? '会话记录加载失败' : '暂无控制台会话记录' }}
            </div>
          </div>
        </div>
      </template>
    </el-drawer>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { api } from '../api'
import { useAuth } from '../store/auth'
import { errMsg, fmtDateTime, roleText, sessionTypeTag, sessionTypeText, FALLBACK_ACTION_LABELS } from '../utils/format'
import PageHead from '../components/PageHead.vue'
import Toolbar from '../components/Toolbar.vue'
import UserGroups from './UserGroups.vue'

const router = useRouter()
const { state, isAdmin } = useAuth()
const activeTab = ref('users')
const users = ref([])
const loading = ref(false)
const dialog = ref(false)
const saving = ref(false)
const editingId = ref(null)
const form = ref({ username: '', password: '', role: 'viewer', is_active: true, real_name: '', phone: '', email: '' })

// 角色筛选（前端过滤，不重复请求后端）；页头下的角色分布 chip 与此联动
const roleFilter = ref('')
const filteredUsers = computed(() =>
  roleFilter.value ? users.value.filter((u) => u.role === roleFilter.value) : users.value
)

// 角色分布统计（前端聚合 listUsers 结果）。点击 chip 按该角色过滤表格，再点一次取消；
// 统计条在页头下方，处于「用户组」tab 时点击先带回用户 tab
const roleStats = computed(() => {
  const cnt = { admin: 0, operator: 0, viewer: 0 }
  for (const u of users.value) {
    if (cnt[u.role] !== undefined) cnt[u.role]++
  }
  return [
    { role: 'admin', label: '管理员', count: cnt.admin, color: 'var(--color-warning)' },
    { role: 'operator', label: '操作员', count: cnt.operator, color: 'var(--color-primary)' },
    { role: 'viewer', label: '只读用户', count: cnt.viewer, color: 'var(--color-info)' }
  ]
})

function pickRole(role) {
  roleFilter.value = roleFilter.value === role ? '' : role
  activeTab.value = 'users'
}

// 编辑自己守卫：角色与账号状态禁改（自己给自己降级/停用会导致无法再管理平台）
const editingSelf = ref(false)
function isSelf(row) {
  const me = state.user || {}
  if (me.id != null && row.id === me.id) return true
  return !!me.username && row.username === me.username
}

// 角色中文用 utils/format 的唯一实现 roleText（未知角色兜底为只读用户）；
// roleTag 仅本页使用（admin=warning / operator=primary / viewer=info），format.js 无此映射
function roleTag(role) {
  return { admin: 'warning', operator: 'primary', viewer: 'info' }[role] || 'info'
}

async function load() {
  loading.value = true
  try {
    const res = await api.listUsers()
    users.value = (res.data && res.data.items) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '获取用户列表失败'))
  } finally {
    loading.value = false
  }
}

// ===== 用户概览抽屉（行点击进入）=====
const drawerOpen = ref(false)
const detail = ref({ user: null, audit: [], sessions: [], auditLoading: false, sessLoading: false, auditFailed: false, sessFailed: false })

function openUser(row) {
  detail.value = { user: row, audit: [], sessions: [], auditLoading: true, sessLoading: true, auditFailed: false, sessFailed: false }
  drawerOpen.value = true
  fetchUserAudit(row.username)
  fetchUserSessions(row.username)
}

// 最近操作：审计接口按 username 模糊筛（handler/audit.go），取回后精确匹配本用户名，取前 8 条
async function fetchUserAudit(username) {
  try {
    const res = await api.listAudit({ username, page_size: 20 })
    detail.value.audit = ((res.data && res.data.items) || [])
      .filter((a) => a.username === username)
      .slice(0, 8)
  } catch {
    // 抽屉内的辅线信息加载失败只降级为占位文案，不弹全局错误打断阅读
    detail.value.auditFailed = true
  } finally {
    detail.value.auditLoading = false
  }
}

// 最近控制台会话：/sessions 同样支持 username 筛（handler/session.go），同样精确过滤后取前 8 条
async function fetchUserSessions(username) {
  try {
    const res = await api.listSessions({ username, page_size: 50 })
    detail.value.sessions = ((res.data && res.data.items) || [])
      .filter((s) => s.username === username)
      .slice(0, 8)
  } catch {
    detail.value.sessFailed = true
  } finally {
    detail.value.sessLoading = false
  }
}

function goAuditCenter() {
  drawerOpen.value = false
  router.push('/audit')
}

function openCreate() {
  editingId.value = null
  editingSelf.value = false
  form.value = { username: '', password: '', role: 'viewer', is_active: true, real_name: '', phone: '', email: '' }
  dialog.value = true
}

function openEdit(row) {
  editingId.value = row.id
  editingSelf.value = isSelf(row)
  form.value = {
    username: row.username,
    password: '',
    role: row.role,
    is_active: !!row.is_active,
    real_name: row.real_name || '',
    phone: row.phone || '',
    email: row.email || ''
  }
  dialog.value = true
}

async function save() {
  if (!editingId.value && (!form.value.username || !form.value.password)) {
    ElMessage.warning('请填写用户名和初始密码')
    return
  }
  if (!editingId.value && form.value.password.length < 6) {
    ElMessage.warning('初始密码至少 6 位')
    return
  }
  saving.value = true
  try {
    if (editingId.value) {
      const { username, password, ...payload } = form.value
      await api.updateUser(editingId.value, payload)
      ElMessage.success('已保存')
    } else {
      await api.createUser(form.value)
      ElMessage.success('用户已创建')
    }
    dialog.value = false
    load()
  } catch (e) {
    ElMessage.error(errMsg(e, '保存失败'))
  } finally {
    saving.value = false
  }
}

async function remove(row) {
  try {
    await ElMessageBox.confirm(`确定删除用户「${row.username}」？该操作不可恢复。`, '删除用户', { type: 'warning', confirmButtonClass: 'el-button--danger' })
  } catch {
    return
  }
  try {
    await api.deleteUser(row.id)
    ElMessage.success('已删除')
    load()
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

onMounted(load)
</script>

<style scoped>
/* .toolbar / .count 已收进 global.css；.toolbar-left/.toolbar-right 骨架与 gap 由 Toolbar 组件承担 */

/* ===== 角色分布统计条（页头下方）：8px 栅格 + 全局色变量，点击按角色过滤 ===== */
.role-strip {
  display: flex;
  align-items: center;
  gap: var(--space-md);
  margin: var(--space-sm) 0 var(--space-lg);
}
.role-chip {
  display: inline-flex;
  align-items: center;
  gap: var(--space-sm);
  padding: 2px var(--space-lg);
  font: inherit;
  font-size: 12px;
  line-height: 20px;
  color: var(--color-foreground);
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: 999px;
  transition: border-color 0.15s, background-color 0.15s;
}
.role-chip:hover {
  border-color: var(--color-border-strong);
}
.role-chip.is-active {
  color: var(--el-color-primary-dark-2);
  background: var(--el-color-primary-light-9);
  border-color: var(--color-primary);
}
.role-chip b {
  font-weight: 600;
}
.role-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex: none;
}

/* ===== 用户概览抽屉 ===== */
.ud-head {
  display: flex;
  align-items: center;
  gap: var(--space-md);
  margin-bottom: var(--space-xl);
}
.ud-name {
  font-size: 20px;
  font-weight: 600;
}
.ud-sec {
  margin-top: var(--space-xl);
}
.ud-sec-title {
  margin-bottom: var(--space-md);
  font-size: 13px;
  font-weight: 600;
  color: var(--color-muted-foreground);
}
.ud-list {
  min-height: 40px;
}
.ud-li {
  display: flex;
  align-items: center;
  gap: var(--space-md);
  padding: var(--space-md) 0;
  font-size: 13px;
  border-bottom: 1px dashed var(--color-border);
}
.dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex: none;
}
.is-ok {
  background: var(--color-success);
}
.is-fail {
  background: var(--color-danger);
}
.is-idle {
  background: var(--color-info);
}
.ud-main {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ud-time {
  flex: none;
  margin-left: auto;
  font-size: 12px;
  color: var(--color-muted-foreground);
}
.ud-empty {
  padding: var(--space-md) 0;
  font-size: 13px;
  color: var(--color-muted-foreground);
}

/* 行点击进入概览抽屉：scoped 需穿透 el-table 内部行 */
:deep(.clickable-row) {
  cursor: pointer;
}
</style>
