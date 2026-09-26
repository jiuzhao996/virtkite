<template>
  <div>
    <div class="toolbar">
      <div class="toolbar-left">
        <el-select v-model="memberFilter" clearable placeholder="按成员筛组" style="width: 160px">
          <el-option v-for="u in users" :key="u.id" :label="u.username" :value="u.id" />
        </el-select>
      </div>
      <div class="toolbar-right">
        <span class="count">共 {{ filteredGroups.length }} 个组</span>
        <el-button :icon="Refresh" :loading="loading" @click="loadAll">刷新</el-button>
        <el-button type="primary" :icon="Plus" @click="openCreate">新建用户组</el-button>
      </div>
    </div>

    <el-table :data="filteredGroups" v-loading="loading" stripe>
      <el-table-column prop="name" label="组名" min-width="140" />
      <el-table-column prop="description" label="描述" min-width="200" show-overflow-tooltip>
        <template #default="{ row }">{{ row.description || '—' }}</template>
      </el-table-column>
      <el-table-column label="成员数" width="90">
        <template #default="{ row }">{{ row.member_count }}</template>
      </el-table-column>
      <el-table-column label="组授权资产" width="100">
        <template #default="{ row }">{{ row.grant_count }}</template>
      </el-table-column>
      <el-table-column label="创建时间" width="170">
        <template #default="{ row }"><span class="mono">{{ fmtDateTime(row.created_at) }}</span></template>
      </el-table-column>
      <el-table-column label="操作" width="200" fixed="right">
        <template #default="{ row }">
          <el-button text type="primary" size="small" @click="openMembers(row)">管理成员</el-button>
          <el-button text type="primary" size="small" @click="openEdit(row)">编辑</el-button>
          <el-button text type="danger" size="small" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
      <template #empty>
        <el-empty description="还没有用户组。给「一个班」批量授权时先建组" :image-size="80">
          <el-button type="primary" plain @click="openCreate">新建用户组</el-button>
        </el-empty>
      </template>
    </el-table>

    <!-- 新建 / 编辑 -->
    <el-dialog v-model="dialog" :title="editingId ? '编辑用户组' : '新建用户组'" width="420px">
      <el-form :model="form" label-width="70px">
        <el-form-item label="组名" required>
          <el-input v-model="form.name" :disabled="!!editingId" placeholder="如「网络 2401 班」" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="form.description" placeholder="可选，便于区分班级/学期" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">{{ editingId ? '保存' : '创建' }}</el-button>
      </template>
    </el-dialog>

    <!-- 成员管理：多选整体替换 -->
    <el-dialog v-model="memberDialog" :title="'管理成员 - ' + (activeGroup.name || '')" width="480px">
      <el-select v-model="memberIds" multiple filterable style="width: 100%" placeholder="选择组内成员（可搜索）">
        <el-option v-for="u in users" :key="u.id" :label="u.username + '（' + roleText(u.role) + '）'" :value="u.id" />
      </el-select>
      <div class="input-help">保存为全量替换：以本次勾选为准。组成员在组授权有效期内可见对应虚拟机。</div>
      <template #footer>
        <el-button @click="memberDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveMembers">保存成员</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { api } from '../api'
import { errMsg, fmtDateTime } from '../utils/format'

const groups = ref([])
const users = ref([])
const loading = ref(false)
const dialog = ref(false)
const memberDialog = ref(false)
const saving = ref(false)
const editingId = ref(null)
const form = ref({ name: '', description: '' })
const activeGroup = ref({})
const memberIds = ref([])
const memberFilter = ref('')

const roleText = (role) => ({ admin: '管理员', operator: '操作员', viewer: '普通用户' }[role] || role)

// 按成员筛组（前端过滤， member_count 后端给的总量不做交集计算，粗筛即可）
const filteredGroups = computed(() =>
  memberFilter.value ? groups.value.filter((g) => g.member_ids && g.member_ids.includes(memberFilter.value)) : groups.value
)

async function loadAll() {
  loading.value = true
  try {
    const [gRes, uRes] = await Promise.all([api.listUserGroups(), api.listUsers()])
    groups.value = (gRes.data && gRes.data.items) || []
    users.value = (uRes.data && uRes.data.items) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '获取用户组失败'))
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editingId.value = null
  form.value = { name: '', description: '' }
  dialog.value = true
}

function openEdit(row) {
  editingId.value = row.id
  form.value = { name: row.name, description: row.description || '' }
  dialog.value = true
}

async function save() {
  if (!form.value.name.trim()) {
    ElMessage.warning('请填写组名')
    return
  }
  saving.value = true
  try {
    if (editingId.value) {
      await api.updateUserGroup(editingId.value, { description: form.value.description })
      ElMessage.success('已保存')
    } else {
      await api.createUserGroup({ name: form.value.name.trim(), description: form.value.description })
      ElMessage.success('用户组已创建')
    }
    dialog.value = false
    loadAll()
  } catch (e) {
    ElMessage.error(errMsg(e, '保存失败'))
  } finally {
    saving.value = false
  }
}

async function openMembers(row) {
  activeGroup.value = row
  memberIds.value = []
  memberDialog.value = true
  try {
    // 拉该组当前成员（组列表不带成员明细，进入弹窗时按需查一次）
    const res = await api.listUserGroups()
    const fresh = ((res.data && res.data.items) || []).find((g) => g.id === row.id)
    if (fresh && fresh.member_ids) memberIds.value = fresh._memberIds
  } catch {
    /* 成员回显失败不阻塞，默认空选 */
  }
}

async function saveMembers() {
  saving.value = true
  try {
    const res = await api.setUserGroupMembers(activeGroup.value.id, memberIds.value)
    ElMessage.success((res.data && res.data.message) || '成员已更新')
    memberDialog.value = false
    loadAll()
  } catch (e) {
    ElMessage.error(errMsg(e, '保存成员失败'))
  } finally {
    saving.value = false
  }
}

async function remove(row) {
  try {
    await ElMessageBox.confirm(
      `确定删除用户组「${row.name}」？组内 ${row.member_count} 个成员的成员关系与 ${row.grant_count} 条组授权将一并清除（成员的直接授权不受影响）。`,
      '删除用户组',
      { type: 'warning', confirmButtonText: '删除', confirmButtonClass: 'el-button--danger' }
    )
  } catch {
    return
  }
  try {
    await api.deleteUserGroup(row.id)
    ElMessage.success('已删除')
    loadAll()
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

onMounted(loadAll)
</script>
