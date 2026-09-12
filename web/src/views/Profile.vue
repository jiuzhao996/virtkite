<template>
  <div>
    <div class="page-head">
      <div>
        <h2 class="page-title">个人中心</h2>
        <span class="page-desc">当前账号的资料与个性化设置；资料修改请联系管理员，密码与轮询偏好由你自行管理</span>
      </div>
    </div>

    <el-row :gutter="16">
      <!-- 个人资料（只读：UpdateUser 仅管理员可调） -->
      <el-col :xs="24" :md="12" class="mb">
        <el-card shadow="never">
          <template #header><span class="card-title">个人资料</span></template>
          <el-descriptions :column="1" size="small" border>
            <el-descriptions-item label="用户名">{{ u.username || '—' }}</el-descriptions-item>
            <el-descriptions-item label="角色">
              <el-tag :type="roleTag" effect="plain" size="small">{{ roleText }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="姓名">{{ u.real_name || '—' }}</el-descriptions-item>
            <el-descriptions-item label="电话">{{ u.phone || '—' }}</el-descriptions-item>
            <el-descriptions-item label="邮箱">{{ u.email || '—' }}</el-descriptions-item>
            <el-descriptions-item label="最近登录">
              <span class="mono">{{ u.last_login ? fmtDateTime(u.last_login) : '—' }}</span>
            </el-descriptions-item>
          </el-descriptions>
        </el-card>
      </el-col>

      <!-- 修改密码（所有角色，改完强制重新登录） -->
      <el-col :xs="24" :md="12" class="mb">
        <el-card shadow="never">
          <template #header><span class="card-title">修改密码</span></template>
          <el-form :model="pwdForm" label-width="100px" style="max-width: 380px">
            <el-form-item label="旧密码" required>
              <el-input v-model="pwdForm.old_password" type="password" show-password placeholder="请输入旧密码" />
            </el-form-item>
            <el-form-item label="新密码" required>
              <el-input v-model="pwdForm.new_password" type="password" show-password placeholder="至少 6 位" />
            </el-form-item>
            <el-form-item label="确认新密码" required>
              <el-input v-model="pwdForm.confirm" type="password" show-password placeholder="再次输入新密码" />
            </el-form-item>
            <el-form-item>
              <el-button type="primary" :loading="pwdSaving" @click="doChangePwd">确认修改</el-button>
            </el-form-item>
          </el-form>
          <p class="tip">改完会强制重新登录，用新密码验证生效。</p>
        </el-card>
      </el-col>

      <!-- 前端轮询偏好（本机浏览器生效，属个人个性化设置，从系统设置页迁来） -->
      <el-col :xs="24" :md="12" class="mb">
        <el-card shadow="never">
          <template #header>
            <div class="card-head">
              <span class="card-title">界面轮询偏好</span>
              <el-button type="primary" @click="savePoll">保存</el-button>
            </div>
          </template>
          <el-form label-width="140px">
            <el-form-item v-for="(label, key) in POLL_LABELS" :key="key" :label="label">
              <el-input-number v-model="pollForm[key]" :min="1000" :max="60000" :step="1000" controls-position="right" />
              <span class="unit">毫秒</span>
            </el-form-item>
          </el-form>
          <p class="tip">保存在本机浏览器（1000–60000ms），只影响你当前浏览器的刷新节奏。</p>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup>
import { computed, reactive, ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { api } from '../api'
import { useAuth } from '../store/auth'
import { errMsg, fmtDateTime } from '../utils/format'
import { POLL_DEFAULTS, POLL_LABELS, getPollInterval, setPollInterval } from '../utils/settings'

const router = useRouter()
const { state, logout } = useAuth()
const u = computed(() => (state.user && state.user.username ? state.user : {}))

const roleText = computed(() => ({ admin: '管理员', operator: '操作员', viewer: '普通用户' }[u.value.role] || u.value.role || '—'))
const roleTag = computed(() => ({ admin: 'warning', operator: 'primary', viewer: 'info' }[u.value.role] || 'info'))

// 修改密码（自 MainLayout 迁入：改完强制重新登录）
const pwdSaving = ref(false)
const pwdForm = reactive({ old_password: '', new_password: '', confirm: '' })
async function doChangePwd() {
  if (!pwdForm.old_password || !pwdForm.new_password) {
    ElMessage.warning('请填写旧密码和新密码')
    return
  }
  if (pwdForm.new_password !== pwdForm.confirm) {
    ElMessage.warning('两次输入的新密码不一致')
    return
  }
  pwdSaving.value = true
  try {
    await api.changeMyPassword(pwdForm.old_password, pwdForm.new_password)
    ElMessage.success('密码修改成功，请重新登录')
    logout()
    router.push({ name: 'login' })
  } catch (e) {
    ElMessage.error(errMsg(e, '修改失败'))
  } finally {
    pwdSaving.value = false
  }
}

// 轮询偏好（自系统设置页迁入：本机浏览器 localStorage，属个人设置）
const pollForm = reactive({})
function loadPollForm() {
  for (const key of Object.keys(POLL_DEFAULTS)) {
    pollForm[key] = getPollInterval(key, POLL_DEFAULTS[key])
  }
}
function savePoll() {
  for (const [key, val] of Object.entries(pollForm)) {
    pollForm[key] = setPollInterval(key, val)
  }
  ElMessage.success('已保存，下次进入页面生效')
}

onMounted(loadPollForm)
</script>

<style scoped>
.mb {
  margin-bottom: 16px;
}
.card-title {
  font-weight: 600;
}
.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.unit {
  margin-left: 8px;
  color: var(--color-muted-foreground);
  font-size: 0.82rem;
}
.tip {
  color: var(--color-muted-foreground);
  font-size: 0.82rem;
  margin: 4px 0 0;
}
.mono {
  font-family: var(--font-mono, monospace);
  font-size: 0.85rem;
}
</style>
