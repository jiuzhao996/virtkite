<template>
  <div>
    <div class="page-head">
      <div>
        <h3 class="page-title">用户管理</h3>
        <p class="page-desc">平台账号与角色（admin 管理员 / operator 操作员 / viewer 普通用户）</p>
      </div>
    </div>

    <el-card shadow="never">
      <div class="toolbar">
        <span class="count">共 {{ users.length }} 个账号</span>
        <el-button v-if="isAdmin" type="primary" :icon="Plus" @click="openCreate">新建用户</el-button>
      </div>

      <el-table :data="users" v-loading="loading" stripe>
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
            <el-button text type="primary" size="small" @click="openEdit(row)">编辑</el-button>
            <el-button
              text
              type="danger"
              size="small"
              :disabled="row.username === (state.user && state.user.username)"
              @click="remove(row)"
            >删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 新建 / 编辑（复用一个弹窗：editingId 区分模式；编辑不含密码，改密码由用户本人操作） -->
    <el-dialog v-model="dialog" :title="editingId ? '编辑用户' : '新建用户'" width="440px">
      <el-form :model="form" label-width="80px">
        <el-form-item label="用户名" required>
          <el-input v-model="form.username" :disabled="!!editingId" placeholder="登录名" />
        </el-form-item>
        <el-form-item v-if="!editingId" label="初始密码" required>
          <el-input v-model="form.password" type="password" show-password placeholder="至少 6 位" />
        </el-form-item>
        <el-form-item label="角色" required>
          <el-select v-model="form.role" style="width: 100%">
            <el-option label="管理员（全部权限）" value="admin" />
            <el-option label="操作员（可操作虚拟机）" value="operator" />
            <el-option label="普通用户（只读 + 图形控制台）" value="viewer" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="editingId" label="账号状态">
          <el-switch v-model="form.is_active" active-text="启用" inactive-text="停用" />
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
  </div>
</template>

<script setup>
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'
import { api } from '../api'
import { useAuth } from '../store/auth'
import { errMsg, fmtDateTime } from '../utils/format'

const { state, isAdmin } = useAuth()
const users = ref([])
const loading = ref(false)
const dialog = ref(false)
const saving = ref(false)
const editingId = ref(null)
const form = ref({ username: '', password: '', role: 'viewer', is_active: true, real_name: '', phone: '', email: '' })

function roleText(role) {
  return { admin: '管理员', operator: '操作员', viewer: '普通用户' }[role] || role
}
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

function openCreate() {
  editingId.value = null
  form.value = { username: '', password: '', role: 'viewer', is_active: true, real_name: '', phone: '', email: '' }
  dialog.value = true
}

function openEdit(row) {
  editingId.value = row.id
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
    await ElMessageBox.confirm(`确定删除用户「${row.username}」？该操作不可恢复。`, '删除用户', { type: 'warning' })
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
