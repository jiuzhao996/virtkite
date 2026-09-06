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
        <!-- 分组折叠菜单：展开状态由本地 closedGroups 自管（el-menu 的 default-openeds 只在
             挂载瞬间生效，isAdmin 异步到达后重渲染的分组接不到，会出现刷新后全部收起的竞态） -->
        <el-menu-item-group v-for="group in menuGroups" :key="group.name">
          <template #title>
            <span class="nav-group-title" @click="toggleGroup(group.name)">
              <span class="group-title">{{ group.name }}</span>
              <el-icon class="group-caret" :class="{ closed: closedGroups.has(group.name) }"><ArrowDown /></el-icon>
            </span>
          </template>
          <template v-if="!closedGroups.has(group.name)">
            <el-menu-item v-for="item in group.items" :key="item.index" :index="item.index">
              <el-icon><component :is="item.icon" /></el-icon>
              <span>{{ item.label }}</span>
            </el-menu-item>
          </template>
        </el-menu-item-group>
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
          <!-- 任务铃：有进行中的后台任务时亮角标，点开看进度、跳任务中心 -->
          <el-popover trigger="click" width="320">
            <template #reference>
              <el-badge :value="activeTasks.length" :hidden="!activeTasks.length" :max="99" class="task-bell">
                <el-icon :size="18"><Bell /></el-icon>
              </el-badge>
            </template>
            <div class="task-pop-head">进行中任务（{{ activeTasks.length }}）</div>
            <div v-if="!activeTasks.length" class="task-pop-empty">当前没有进行中的任务</div>
            <div v-else class="task-pop-list">
              <div v-for="t in activeTasks" :key="t.id" class="task-pop-item">
                <span class="task-pop-title">{{ t.title }}</span>
                <el-tag :type="t.status === 'running' ? 'primary' : 'info'" size="small">
                  {{ t.status === 'running' ? '执行中' : '等待中' }}
                </el-tag>
              </div>
            </div>
            <el-button text type="primary" class="task-pop-more" @click="router.push('/tasks')">前往任务中心</el-button>
          </el-popover>
          <el-tag v-if="isAdmin" type="warning" effect="dark" size="small">管理员</el-tag>
          <el-tag v-else type="info" effect="plain" size="small">普通用户</el-tag>
          <!-- 用户中心：资料/改密码/轮询偏好集中在个人中心页（对标云控制台顶栏分工） -->
          <el-dropdown trigger="click" @command="onUserCommand">
            <span class="user-entry">
              <el-icon :size="18"><UserFilled /></el-icon>
              <span class="username">{{ state.user ? state.user.username : '—' }}</span>
              <el-icon class="entry-caret" :size="12"><ArrowDown /></el-icon>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="profile">
                  <el-icon><User /></el-icon>个人中心
                </el-dropdown-item>
                <el-dropdown-item divided command="logout">
                  <el-icon><SwitchButton /></el-icon>退出登录
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </el-header>

      <el-main class="main">
        <router-view />
      </el-main>
    </el-container>
  </el-container>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowDown, Bell, SwitchButton, User, UserFilled } from '@element-plus/icons-vue'
import { useAuth } from '../store/auth'
import { api } from '../api'
import { getPollInterval, POLL_DEFAULTS } from '../utils/settings'

const route = useRoute()
const router = useRouter()
const { state, isAdmin, setPageTitle, logout } = useAuth()
const collapsed = ref(false)

// 分组导航：group 字段同时驱动展开态（el-menu-item-group）与折叠态 v-for，
// adminOnly 过滤在 menuGroups 里统一做。层级思路：资源组 = 用户生产消费的对象（虚拟机/镜像），
// 宿主机与存储池/网络同属基础设施（提供算力/存储/网络）；会话管理并入审计中心（审计页 tab）；
// 个人资料/改密码/轮询偏好收进顶栏「个人中心」（对标 JumpServer 审计模块与云控制台顶栏分工）。
const navItems = [
  { index: '/dashboard', label: '仪表盘', icon: 'DataLine', group: '总览' },
  { index: '/monitor', label: '监控中心', icon: 'Odometer', group: '总览' },
  { index: '/vms', label: '虚拟机', icon: 'Monitor', group: '资源' },
  { index: '/images', label: '镜像管理', icon: 'Picture', group: '资源' },
  { index: '/hosts', label: '宿主机', icon: 'Cpu', group: '基础设施' },
  { index: '/storage', label: '存储池', icon: 'FolderOpened', group: '基础设施' },
  { index: '/networks', label: '网络', icon: 'Connection', group: '基础设施' },
  { index: '/tasks', label: '任务中心', icon: 'List', group: '运维' },
  { index: '/audit', label: '审计中心', icon: 'Document', group: '运维' },
  { index: '/users', label: '用户管理', icon: 'User', group: '管理', adminOnly: true },
  { index: '/settings', label: '系统设置', icon: 'Setting', group: '管理', adminOnly: true }
]

const menuGroups = computed(() => {
  const visible = navItems.filter((it) => !it.adminOnly || isAdmin.value)
  const order = ['总览', '资源', '基础设施', '运维', '管理']
  return order
    .map((name) => ({ name, items: visible.filter((it) => it.group === name) }))
    .filter((g) => g.items.length > 0)
})

// 分组折叠状态：默认全展开（closedGroups 为空），点击组名切换；不依赖 el-menu 内部展开机制
const closedGroups = ref(new Set())
function toggleGroup(name) {
  const next = new Set(closedGroups.value)
  if (next.has(name)) next.delete(name)
  else next.add(name)
  closedGroups.value = next
}

const activeIndex = computed(() => '/' + (route.path.split('/')[1] || 'dashboard'))
const titleMap = {
  dashboard: '仪表盘',
  monitor: '监控中心',
  vms: '虚拟机管理',
  hosts: '宿主机管理',
  images: '镜像管理',
  storage: '存储池管理',
  networks: '网络管理',
  tasks: '任务中心',
  audit: '审计中心',
  users: '用户管理',
  settings: '系统设置',
  profile: '个人中心'
}
// 详情页先显示动态标题（如 VM 名），页面没写时回退静态映射
const title = computed(() => state.pageTitle || titleMap[route.path.split('/')[1]] || 'vmops')

// 离开详情类路由时清掉动态标题残留（/vms/new 不是详情页）
watch(
  () => route.path,
  (p) => {
    if (!/^\/vms\/\d+/.test(p)) setPageTitle('')
  }
)

// 任务铃轮询：pending/running 两路合并；失败静默（铃铛只是辅助入口，不打扰用户）
const activeTasks = ref([])
let taskTimer = null
async function loadActiveTasks() {
  try {
    const [run, pend] = await Promise.all([
      api.listTasks({ status: 'running', page_size: 100 }),
      api.listTasks({ status: 'pending', page_size: 100 })
    ])
    activeTasks.value = [...(run.items || []), ...(pend.items || [])]
  } catch (e) {
    /* 静默 */
  }
}
onMounted(() => {
  // 兜底清残留动态标题：从独立路由（如控制台）回到布局时 watch 不会触发
  if (!/^\/vms\/\d+/.test(route.path)) setPageTitle('')
  loadActiveTasks()
  taskTimer = setInterval(loadActiveTasks, getPollInterval('tasks', POLL_DEFAULTS.tasks))
})
onUnmounted(() => {
  if (taskTimer) clearInterval(taskTimer)
})

function onLogout() {
  logout()
  router.push({ name: 'login' })
}

// 顶栏用户下拉：个人中心走独立页面（资料/改密码/轮询偏好），退出直接登出
function onUserCommand(cmd) {
  if (cmd === 'profile') router.push('/profile')
  else if (cmd === 'logout') onLogout()
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
  /* 菜单项多时允许滚动（侧栏整体 100vh，brand 区之外是菜单区） */
  overflow-y: auto;
  min-height: 0;
}
/* 分组标题行（本地折叠状态，点击切换） */
.menu :deep(.el-menu-item-group__title) {
  padding: 0;
}
.nav-group-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  color: rgba(255, 255, 255, 0.72);
  font-size: 14px;
  font-weight: 600;
  height: 36px;
  padding: 0 16px;
  letter-spacing: 2px;
  cursor: pointer;
  user-select: none;
  transition: color 0.2s ease;
}
.nav-group-title:hover {
  color: rgba(255, 255, 255, 0.95);
}
.group-title {
  font-size: 14px;
  font-weight: 600;
  letter-spacing: 2px;
}
.group-caret {
  font-size: 13px;
  color: rgba(255, 255, 255, 0.6);
  transition: transform 0.2s ease;
}
.group-caret.closed {
  transform: rotate(-90deg);
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
/* 分组之间留呼吸感（首个分组不额外加） */
.menu :deep(.el-menu-item-group) {
  margin-top: 8px;
}
.menu :deep(.el-menu-item-group:first-child) {
  margin-top: 2px;
}
.menu :deep(.el-menu-item) {
  height: 42px;
  line-height: 42px;
  font-size: 14px;
  color: rgba(255, 255, 255, 0.78);
  margin: 2px 10px;
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
/* 任务铃 */
.task-bell {
  display: flex;
  align-items: center;
  cursor: pointer;
  color: var(--color-muted-foreground);
  padding: 4px;
  border-radius: 6px;
  transition: all 0.2s ease;
}
.task-bell:hover {
  background: var(--color-background);
  color: var(--color-foreground);
}
.task-pop-head {
  font-weight: 600;
  font-size: 13px;
  margin-bottom: 8px;
}
.task-pop-empty {
  color: var(--color-muted-foreground);
  font-size: 13px;
  padding: 8px 0;
}
.task-pop-list {
  max-height: 260px;
  overflow-y: auto;
}
.task-pop-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 6px 0;
  border-bottom: 1px solid var(--color-border);
}
.task-pop-title {
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.task-pop-more {
  width: 100%;
  margin-top: 8px;
}
.header-right :deep(.el-tag),
.username {
  color: var(--color-muted-foreground);
}
.username {
  font-weight: 500;
  color: var(--color-foreground);
}
/* 顶栏用户入口（头像+用户名+下拉箭头） */
.user-entry {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  padding: 4px 8px;
  border-radius: 8px;
  color: var(--color-foreground);
  outline: none;
  transition: background 0.2s ease;
}
.user-entry:hover {
  background: var(--color-background);
}
.entry-caret {
  color: var(--color-muted-foreground);
}
.main {
  background: var(--color-background);
  padding: 20px;
}
</style>
