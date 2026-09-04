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
        <el-menu-item index="/audit">
          <el-icon><Document /></el-icon>
          <span>审计日志</span>
        </el-menu-item>
      </el-menu>
      <div v-else class="collapse-nav">
        <el-tooltip
          v-for="item in navItems"
          :key="item.index"
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
      </div>
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
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Monitor, Fold, Expand } from '@element-plus/icons-vue'
import { useAuth } from '../store/auth'

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
  { index: '/audit', label: '审计日志', icon: 'Document' }
]

const activeIndex = computed(() => '/' + (route.path.split('/')[1] || 'dashboard'))
const titleMap = {
  dashboard: '仪表盘',
  vms: '虚拟机管理',
  hosts: '宿主机管理',
  images: '镜像管理',
  storage: '存储池管理',
  networks: '网络管理',
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
