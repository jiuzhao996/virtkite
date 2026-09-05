<template>
  <el-container class="layout">
    <el-aside :width="collapsed ? '64px' : '180px'" class="aside">
      <div class="brand" :class="{ collapsed }">
        <template v-if="!collapsed">
          <el-icon class="brand-icon"><Monitor /></el-icon>
          <span class="brand-text">vmops</span>
          <el-icon class="collapse-btn" @click="collapsed = true"><Fold /></el-icon>
        </template>
        <el-icon v-else class="collapse-btn center" @click="collapsed = false"><Expand /></el-icon>
      </div>
      <el-menu
        v-if="!collapsed"
        :default-active="activeIndex"
        router
        class="menu"
        background-color="transparent"
      >
        <el-menu-item index="/dashboard">
          <el-icon><DataLine /></el-icon>
          <span>仪表盘</span>
        </el-menu-item>
        <el-menu-item index="/vms">
          <el-icon><Monitor /></el-icon>
          <span>虚拟机</span>
        </el-menu-item>
        <el-menu-item index="/hosts">
          <el-icon><Cpu /></el-icon>
          <span>宿主机</span>
        </el-menu-item>
        <el-menu-item index="/images">
          <el-icon><Picture /></el-icon>
          <span>镜像管理</span>
        </el-menu-item>
        <el-menu-item index="/storage">
          <el-icon><FolderOpened /></el-icon>
          <span>存储池</span>
        </el-menu-item>
        <el-menu-item index="/networks">
          <el-icon><Connection /></el-icon>
          <span>网络</span>
        </el-menu-item>
        <el-menu-item index="/tasks">
          <el-icon><List /></el-icon>
          <span>任务中心</span>
        </el-menu-item>
        <el-menu-item index="/sessions">
          <el-icon><Link /></el-icon>
          <span>会话管理</span>
        </el-menu-item>
        <el-menu-item v-if="isAdmin" index="/settings">
          <el-icon><Setting /></el-icon>
          <span>系统设置</span>
        </el-menu-item>
        <el-menu-item v-if="isAdmin" index="/audit">
          <el-icon><Document /></el-icon>
          <span>审计日志</span>
        </el-menu-item>
      </el-menu>
      <div v-else class="collapse-nav">
        <template v-for="item in navItems" :key="item.index">
        <el-tooltip
          v-if="!item.adminOnly || isAdmin"
          :content="item.label"
          placement="right"
        >
          <div
            class="collapse-item"
            :class="{ active: activeIndex === item.index }"
            @click="$router.push(item.index)"
          >
            <el-icon><component :is="item.icon" /></el-icon>
          </div>
        </el-tooltip>
        </template>
      </div>
    </el-aside>

    <el-container>
      <el-header class="header">
        <h2 class="header-title">{{ title }}</h2>
        <div class="header-right">
          <el-tag v-if="isAdmin" type="warning" effect="dark" size="small">管理员</el-tag>
          <el-tag v-else type="info" effect="plain" size="small">普通用户</el-tag>
          <span class="username">{{ state.user ? state.user.username : '—' }}</span>
          <el-button text type="primary" @click="pwdDialog = true">修改密码</el-button>
          <el-button text type="primary" @click="onLogout">退出登录</el-button>
        </div>
      </el-header>

      <el-main class="main">
        <router-view />
      </el-main>
    </el-container>

    <!-- 修改密码（所有角色，改完强制重新登录） -->
    <el-dialog v-model="pwdDialog" title="修改密码" width="400px">
      <el-form :model="pwdForm" label-width="80px">
        <el-form-item label="旧密码" required>
          <el-input v-model="pwdForm.old_password" type="password" show-password placeholder="请输入旧密码" />
        </el-form-item>
        <el-form-item label="新密码" required>
          <el-input v-model="pwdForm.new_password" type="password" show-password placeholder="至少 6 位" />
        </el-form-item>
        <el-form-item label="确认新密码" required>
          <el-input v-model="pwdForm.confirm" type="password" show-password placeholder="再次输入新密码" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="pwdDialog = false">取消</el-button>
        <el-button type="primary" :loading="pwdSaving" @click="doChangePwd">确认修改</el-button>
      </template>
    </el-dialog>
  </el-container>
</template>

<script setup>
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Monitor, Fold, Expand } from '@element-plus/icons-vue'
import { useAuth } from '../store/auth'
import { api } from '../api'

const route = useRoute()
const router = useRouter()
const { state, isAdmin, logout } = useAuth()
const collapsed = ref(false)

const navItems = [
  { index: '/dashboard', label: '仪表盘', icon: 'DataLine' },
  { index: '/vms', label: '虚拟机', icon: 'Monitor' },
  { index: '/hosts', label: '宿主机', icon: 'Cpu' },
  { index: '/images', label: '镜像管理', icon: 'Picture' },
  { index: '/storage', label: '存储池', icon: 'FolderOpened' },
  { index: '/networks', label: '网络', icon: 'Connection' },
  { index: '/tasks', label: '任务中心', icon: 'List' },
  { index: '/sessions', label: '会话管理', icon: 'Link' },
  { index: '/settings', label: '系统设置', icon: 'Setting', adminOnly: true },
  { index: '/audit', label: '审计日志', icon: 'Document', adminOnly: true }
]

const activeIndex = computed(() => '/' + (route.path.split('/')[1] || 'dashboard'))
const titleMap = {
  dashboard: '仪表盘',
  vms: '虚拟机管理',
  hosts: '宿主机管理',
  images: '镜像管理',
  storage: '存储池管理',
  networks: '网络管理',
  tasks: '任务中心',
  sessions: '会话管理',
  settings: '系统设置',
  audit: '审计日志'
}
const title = computed(() => titleMap[route.path.split('/')[1]] || 'vmops')

function onLogout() {
  logout()
  router.push({ name: 'login' })
}

// 修改密码（改完强制重新登录，用新密码验证）
const pwdDialog = ref(false)
const pwdSaving = ref(false)
const pwdForm = ref({ old_password: '', new_password: '', confirm: '' })
async function doChangePwd() {
  if (!pwdForm.value.old_password || !pwdForm.value.new_password) {
    ElMessage.warning('请填写旧密码和新密码')
    return
  }
  if (pwdForm.value.new_password !== pwdForm.value.confirm) {
    ElMessage.warning('两次输入的新密码不一致')
    return
  }
  pwdSaving.value = true
  try {
    await api.changeMyPassword(pwdForm.value.old_password, pwdForm.value.new_password)
    ElMessage.success('密码修改成功，请重新登录')
    pwdDialog.value = false
    pwdForm.value = { old_password: '', new_password: '', confirm: '' }
    onLogout()
  } catch (e) {
    ElMessage.error((e.response && e.response.data && e.response.data.message) || '修改失败')
  } finally {
    pwdSaving.value = false
  }
}
</script>

<style scoped>
.layout {
  height: 100vh;
}
.aside {
  background: linear-gradient(180deg, var(--color-primary) 0%, var(--el-color-primary-dark-2) 100%);
  border-right: none;
  display: flex;
  flex-direction: column;
  transition: width 0.2s ease;
  overflow: hidden;
}
.brand {
  height: 56px;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 12px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.12);
  flex-shrink: 0;
}
.brand.collapsed {
  justify-content: center;
  padding: 0;
}
.brand-icon {
  font-size: 1.3rem;
  color: #fff;
}
.brand-text {
  font-size: 1.15rem;
  font-weight: 700;
  color: #fff;
  letter-spacing: 0.3px;
  white-space: nowrap;
}
.collapse-btn {
  margin-left: auto;
  font-size: 1.1rem;
  color: rgba(255, 255, 255, 0.85);
  cursor: pointer;
  border-radius: 6px;
  padding: 4px;
  transition: all 0.2s ease;
}
.collapse-btn:hover {
  background: rgba(255, 255, 255, 0.15);
  color: #fff;
}
.collapse-btn.center {
  margin: auto;
}
.menu {
  border-right: none;
  flex: 1;
  background: transparent;
}
.collapse-nav {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  padding: 10px 0;
}
.collapse-item {
  width: 44px;
  height: 44px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 10px;
  color: rgba(255, 255, 255, 0.72);
  font-size: 1.25rem;
  cursor: pointer;
  transition: all 0.2s ease;
}
.collapse-item:hover {
  background: rgba(255, 255, 255, 0.12);
  color: rgba(255, 255, 255, 0.95);
}
.collapse-item.active {
  background: #fff;
  color: var(--color-primary);
  font-weight: 600;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.15);
}
.menu :deep(.el-menu-item) {
  color: rgba(255, 255, 255, 0.72);
  margin: 2px 8px;
  border-radius: 10px;
  transition: all 0.2s ease;
}
.menu :deep(.el-menu-item:hover) {
  background: rgba(255, 255, 255, 0.12);
  color: rgba(255, 255, 255, 0.95);
}
.menu :deep(.el-menu-item.is-active) {
  position: relative;
  background: #fff; /* 白色胶囊 */
  color: var(--color-primary); /* 青绿加粗文字 */
  font-weight: 600;
  border-radius: 10px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.15); /* 轻微阴影 */
}
.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: #fff;
  color: var(--color-foreground);
  border-bottom: 1px solid var(--color-border);
}
.header-title {
  margin: 0;
  font-size: 1.05rem;
  font-weight: 600;
  color: var(--color-foreground);
}
.header-right {
  display: flex;
  align-items: center;
  gap: 12px;
}
.header-right :deep(.el-tag),
.username {
  color: var(--color-muted-foreground);
}
.username {
  font-weight: 500;
  color: var(--color-foreground);
}
.main {
  background: var(--color-background);
  padding: 20px;
}
</style>
