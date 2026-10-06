<template>
  <section v-show="active" class="panel">
    <div class="panel-head">
      <h3 class="panel-title">授权管理</h3>
    </div>
    <el-alert
      type="info"
      :closable="false"
      class="xml-alert"
      title="把这台虚拟机授权给学生/教师后，对方登录即可在列表中看到并操作它；未授权用户完全看不到（查无此项）。到期时间留空 = 长期有效，可配合实验周期设置到期自动收回。"
    />
    <el-card shadow="never">
      <div class="grant-form">
        <el-radio-group v-model="grantSubjectType" :disabled="grantSaving">
          <el-radio-button value="user">按用户</el-radio-button>
          <el-radio-button value="group">按用户组</el-radio-button>
        </el-radio-group>
        <el-select
          v-if="grantSubjectType === 'user'"
          v-model="grantForm.userId"
          filterable
          placeholder="选择要授权的用户"
          style="width: 240px"
        >
          <el-option v-for="u in grantUsers" :key="u.id" :label="u.username + '（' + u.role + '）'" :value="u.id" />
        </el-select>
        <el-select
          v-else
          v-model="groupGrantForm.groupId"
          filterable
          placeholder="选择用户组（教学班）"
          style="width: 240px"
        >
          <el-option v-for="g in userGroups" :key="g.id" :label="g.name + '（' + g.member_count + ' 人）'" :value="g.id" />
        </el-select>
        <el-date-picker
          v-model="grantForm.expiresAt"
          type="datetime"
          placeholder="到期时间（留空 = 长期有效）"
          value-format="YYYY-MM-DDTHH:mm:ssZ"
          style="width: 210px"
        />
        <el-button
          type="primary"
          :disabled="grantSubjectType === 'user' ? !grantForm.userId : !groupGrantForm.groupId"
          :loading="grantSaving"
          @click="submitGrant"
        >授权</el-button>
      </div>
      <el-table :data="grantSubjects" size="small" border style="width: 100%" v-loading="grantsLoading">
        <template #empty><el-empty description="暂无授权（该虚拟机当前仅管理员可见）" :image-size="70" /></template>
        <el-table-column label="类型" width="96">
          <template #default="{ row }">
            <el-tag :type="row.subjectType === 'user' ? 'primary' : 'warning'" effect="plain" size="small">
              {{ row.subjectType === 'user' ? '用户' : '用户组' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="subjectName" label="名称" min-width="150">
          <template #default="{ row }">
            {{ row.subjectName }}<span v-if="row.subjectType === 'group'" class="input-help">（{{ row.memberLabel }}）</span>
          </template>
        </el-table-column>
        <el-table-column label="有效期" min-width="170">
          <template #default="{ row }">
            <el-tag v-if="!row.expires_at" size="small" effect="light">长期有效</el-tag>
            <span v-else>{{ grantExpiryText(row.expires_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="授权时间" min-width="170">
          <template #default="{ row }">{{ grantExpiryText(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="90" fixed="right">
          <template #default="{ row }">
            <el-button text size="small" type="danger" :icon="Delete" @click="revokeSubject(row)">收回</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </section>
</template>

<script setup>
// 授权管理统一面板（借鉴堡垒机 4A：分配是一等实体，授权决定可见性）。
// 完全自持：主体类型切换（按用户/按用户组）、用户授权、组授权（教学班批量可见性）、
// grantSubjects 合并视图（用户授权 ∪ 组授权、按授权时间倒序）、revokeSubject 分发收回。
// 只依赖 vmId；激活（active 翻 true，等价原壳 watch(activeView) 命中 'grants'）时按原顺序
// 触发 loadGrants / loadGrantUsers / loadUserGroups / loadGroupGrants 四路加载（不互相等待），
// 且每次切入都重新拉取（与原 watch 行为一致，无一次性守卫）。
import { ref, computed, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Delete } from '@element-plus/icons-vue'
import { api } from '../../../api'
import { taskErrorMessage } from '../../../utils/task.js'
import { fmtDateTime } from '../../../utils/format'

const props = defineProps({
  vmId: { type: [String, Number], required: true },
  active: { type: Boolean, default: false } // 是否处于授权管理分区（壳 activeView === 'grants'）
})

const grants = ref([])
const grantsLoading = ref(false)
const grantUsers = ref([])
const grantForm = ref({ userId: null, expiresAt: null })
// 组授权（教学班批量可见性）
const userGroups = ref([])
const groupGrants = ref([])
const groupGrantForm = ref({ groupId: null, expiresAt: null })
// 统一授权表单：主体类型（用户/用户组）+ 到期
const grantSubjectType = ref('user')
const grantSaving = ref(false)

async function loadGrants() {
  grantsLoading.value = true
  try {
    const res = await api.listVMGrants(props.vmId)
    grants.value = (res.data && res.data.items) || []
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '获取授权列表失败'))
  } finally {
    grantsLoading.value = false
  }
}

async function loadUserGroups() {
  try {
    const res = await api.listUserGroups()
    userGroups.value = (res.data && res.data.items) || []
  } catch (e) {
    /* 静默：组下拉非核心，失败不阻塞授权面板 */
  }
}

async function loadGroupGrants() {
  try {
    const res = await api.listVMGroupGrants(props.vmId)
    groupGrants.value = (res.data && res.data.items) || []
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '获取组授权列表失败'))
  }
}

// grantSubjects 用户授权 ∪ 组授权的统一视图（表格数据源，按授权时间倒序）
const grantSubjects = computed(() => {
  const groupMeta = new Map(userGroups.value.map((g) => [g.id, g]))
  const users = grants.value.map((g) => ({
    key: 'u' + g.id,
    subjectType: 'user',
    subjectName: g.username,
    memberLabel: '',
    expires_at: g.expires_at,
    created_at: g.created_at,
    rawId: g.id
  }))
  const groups = groupGrants.value.map((g) => ({
    key: 'g' + g.id,
    subjectType: 'group',
    subjectName: g.group_name,
    memberLabel: (groupMeta.get(g.group_id) || {}).member_count != null ? groupMeta.get(g.group_id).member_count + ' 人' : '',
    expires_at: g.expires_at,
    created_at: g.created_at,
    rawId: g.id
  }))
  return [...users, ...groups].sort((a, b) => new Date(b.created_at) - new Date(a.created_at))
})

async function submitGrant() {
  grantSaving.value = true
  try {
    if (grantSubjectType.value === 'user') {
      if (!grantForm.value.userId) {
        ElMessage.warning('请选择要授权的用户')
        return
      }
      const payload = { user_id: grantForm.value.userId }
      if (grantForm.value.expiresAt) payload.expires_at = grantForm.value.expiresAt
      const res = await api.grantVM(props.vmId, payload)
      ElMessage.success((res && res.message) || '已授权')
      grantForm.value = { userId: null, expiresAt: null }
      await loadGrants()
    } else {
      if (!groupGrantForm.value.groupId) {
        ElMessage.warning('请选择要授权的用户组')
        return
      }
      const payload = { group_id: groupGrantForm.value.groupId }
      if (groupGrantForm.value.expiresAt) payload.expires_at = groupGrantForm.value.expiresAt
      const res = await api.grantVMToGroup(props.vmId, payload)
      ElMessage.success((res.data && res.data.message) || '已授权给组')
      groupGrantForm.value = { groupId: null, expiresAt: null }
      await loadGroupGrants()
    }
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '授权失败'))
  } finally {
    grantSaving.value = false
  }
}

async function revokeSubject(row) {
  const isUser = row.subjectType === 'user'
  try {
    await ElMessageBox.confirm(
      isUser
        ? '确定收回 ' + row.subjectName + ' 对该虚拟机的授权？收回后对方立即不可见、不可操作。'
        : '确定收回组「' + row.subjectName + '」的组授权？组内成员将立即不可见。',
      '收回授权',
      { type: 'warning', confirmButtonText: '收回', cancelButtonText: '取消', confirmButtonClass: 'el-button--danger' }
    )
  } catch {
    return
  }
  try {
    if (isUser) {
      await api.revokeVMGrant(props.vmId, row.rawId)
      await loadGrants()
    } else {
      await api.revokeVMGroupGrant(props.vmId, row.rawId)
      await loadGroupGrants()
    }
    ElMessage.success('已收回授权')
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '收回授权失败'))
  }
}

async function loadGrantUsers() {
  if (grantUsers.value.length) return
  try {
    const res = await api.listUsers()
    grantUsers.value = (res.data && res.data.items) || []
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '获取用户列表失败'))
  }
}

function grantExpiryText(v) {
  // 统一走 fmtDateTime（空值 '—'、非法值原样返回，与原手写实现语义一致）
  return fmtDateTime(v)
}

// 分区激活时拉取（每次切入都刷，等价原壳 watch(activeView) 的 'grants' 分支）
watch(
  () => props.active,
  (on) => {
    if (on) {
      loadGrants()
      loadGrantUsers()
      loadUserGroups()
      loadGroupGrants()
    }
  }
)
</script>

<style scoped>
.panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
}
.panel-title {
  margin: 0;
  font-size: 1rem;
  font-weight: 600;
  color: var(--color-foreground);
}
.xml-alert {
  margin-bottom: 12px;
}
.grant-form {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-bottom: 12px;
}
</style>
