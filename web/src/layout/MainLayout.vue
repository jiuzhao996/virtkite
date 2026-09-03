<template>
  <el-container class="layout">
    <el-aside width="220px" class="aside">
      <div class="brand">
        <span class="brand-icon">🛡️</span>
        <span class="brand-text">vmops</span>
      </div>
      <el-menu :default-active="activeIndex" router class="menu" background-color="transparent">
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
        <el-menu-item index="/audit">
          <el-icon><Document /></el-icon>
          <span>审计日志</span>
        </el-menu-item>
      </el-menu>
    </el-aside>

    <el-container>
      <el-header class="header">
        <h2 class="header-title">{{ title }}</h2>
        <div class="header-right">
          <el-tag v-if="isAdmin" type="warning" effect="dark" size="small">管理员</el-tag>
          <el-tag v-else type="info" effect="plain" size="small">普通用户</el-tag>
          <span class="username">{{ state.user ? state.user.username : '—' }}</span>
          <el-button text type="primary" @click="onLogout">退出登录</el-button>
        </div>
      </el-header>

      <el-main class="main">
        <router-view />
      </el-main>
    </el-container>
  </el-container>
</template>

<script setup>
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuth } from '../store/auth'

const route = useRoute()
const router = useRouter()
const { state, isAdmin, logout } = useAuth()

const activeIndex = computed(() => '/' + (route.path.split('/')[1] || 'dashboard'))
const titleMap = {
  dashboard: '仪表盘',
  vms: '虚拟机管理',
  hosts: '宿主机管理',
  images: '镜像管理',
  storage: '存储池管理',
  audit: '审计日志'
}
const title = computed(() => titleMap[route.path.split('/')[1]] || 'vmops')

function onLogout() {
  logout()
  router.push({ name: 'login' })
}
</script>

<style scoped>
.layout {
  height: 100vh;
}
.aside {
  background: #ffffff;
  border-right: 1px solid var(--el-border-color-light);
  display: flex;
  flex-direction: column;
}
.brand {
  height: 56px;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 18px;
  border-bottom: 1px solid var(--el-border-color-light);
}
.brand-icon {
  font-size: 1.4rem;
}
.brand-text {
  font-size: 1.15rem;
  font-weight: 700;
  color: #2a9da5;
}
.menu {
  border-right: none;
  flex: 1;
}
.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: linear-gradient(135deg, #3db8bf 0%, #2a9da5 100%);
  color: #fff;
}
.header-title {
  margin: 0;
  font-size: 1.05rem;
  color: #fff;
}
.header-right {
  display: flex;
  align-items: center;
  gap: 12px;
}
.header-right :deep(.el-tag),
.username {
  color: #fff;
}
.username {
  font-weight: 500;
}
.main {
  background: #eef4f7;
  padding: 20px;
}
</style>
