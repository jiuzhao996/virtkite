<template>
  <el-container class="layout">
    <el-aside
      :width="asideWidth"
      class="aside"
      :class="{ 'aside-mobile': isMobile, 'aside-mobile-open': isMobile && drawerOpen }"
    >
      <div class="brand" :class="{ collapsed }">
        <template v-if="!collapsed">
          <img class="brand-logo" src="/brand/mark-white.svg" alt="鸢航" />
          <span class="brand-text">鸢航 VirtKite</span>
          <!-- 收缩入口唯一化：只保留底部胶囊条，brand 角落的重复箭头已移除（用户要求） -->
        </template>
        <!-- 折叠态只显示 logo：收起/展开统一走底部条，保证两个方向入口位置一致 -->
        <img v-else class="brand-logo" src="/brand/mark-white.svg" alt="鸢航" style="margin: 0 auto" />
      </div>
      <el-menu
        v-if="!collapsed || isMobile"
        :default-active="activeIndex"
        router
        class="menu"
        background-color="transparent"
      >
        <!-- 分组折叠菜单：展开状态由本地 closedGroups 自管（el-menu 的 default-openeds 只在
             挂载瞬间生效，isAdmin 异步到达后重渲染的分组接不到，会出现刷新后全部收起的竞态） -->
        <template v-for="group in menuGroups" :key="group.name">
          <!-- 组内 >1 项才渲染分组标题与折叠；单项目组（如只剩仪表盘的总览组）直接平铺菜单项 -->
          <el-menu-item-group v-if="group.items.length > 1">
            <template #title>
              <!-- 分组标题可折叠：role/tabindex/键盘 Enter 触发（ui-ux-pro-max 可访问性基线） -->
              <span
                class="nav-group-title"
                role="button"
                tabindex="0"
                :aria-expanded="!closedGroups.has(group.name)"
                @click="toggleGroup(group.name)"
                @keydown.enter.prevent="toggleGroup(group.name)"
                @keydown.space.prevent="toggleGroup(group.name)"
              >
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
          <template v-else>
            <el-menu-item v-for="item in group.items" :key="item.index" :index="item.index">
              <el-icon><component :is="item.icon" /></el-icon>
              <span>{{ item.label }}</span>
            </el-menu-item>
          </template>
        </template>
      </el-menu>
      <div v-else-if="collapsed && !isMobile" class="collapse-nav">
        <template v-for="item in navItems" :key="item.index">
        <el-tooltip
          v-if="(!item.adminOnly || isAdmin) && (!item.operateOnly || canOperate)"
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
      <!-- 底部常驻收起/展开条：顶部 brand 角落的收缩键太隐蔽（用户反馈"压根看不出来"），这里给全宽可点的显式入口 -->
      <!-- 折叠态只剩图标，必须给 tooltip 提示（用户反馈"没有任何提示"）；展开态有文字，tooltip 关掉 -->
      <el-tooltip :content="collapsed ? '展开侧栏' : '收起侧栏'" placement="right" :disabled="!collapsed" :show-after="200">
        <div class="aside-collapse-bar" @click="collapsed = !collapsed">
          <el-icon class="bar-arrow"><component :is="collapsed ? ArrowRight : ArrowLeft" /></el-icon>
          <span v-if="!collapsed">收起侧栏</span>
        </div>
      </el-tooltip>
    </el-aside>

    <el-container>
      <el-header class="header">
        <!-- 顶栏不放页面标题（职责在页面自身页头，避免双标题重复）；
             改为全局搜索（VM 名直达详情，对标云控制台顶栏分工）+ 全屏切换 -->
        <div class="header-left">
          <!-- 移动端汉堡：打开侧栏抽屉（桌面端隐藏） -->
          <el-button
            v-if="isMobile"
            text
            :icon="Menu"
            class="menu-btn"
            aria-label="打开菜单"
            @click="drawerOpen = true"
          />
          <el-select
            v-model="searchSel"
            class="global-search"
            filterable
            clearable
            placeholder="搜索虚拟机名称，回车直达详情"
            :loading="searchLoading"
            @visible-change="onSearchVisible"
            @change="goSearchVM"
          >
            <el-option v-for="vm in searchVMs" :key="vm.id" :label="vm.name" :value="vm.id">
              <span class="s-name">{{ vm.name }}</span>
              <el-tag :type="vmStatusTag(vm.status)" size="small" effect="light" class="s-tag">
                {{ vmStatusText(vm.status) }}
              </el-tag>
            </el-option>
            <template #empty>无匹配虚拟机</template>
          </el-select>
          <el-tooltip content="全屏切换" placement="bottom">
            <el-button text :icon="FullScreen" class="fs-btn" @click="toggleFullscreen" />
          </el-tooltip>
        </div>
        <div class="header-right">
          <!-- AI 助手：顶栏抽屉形态（IA 精简批次撤销独立页面），operator+ 可见 -->
          <el-tooltip v-if="canOperate" content="AI 运维助手" placement="bottom">
            <el-button
              text
              :icon="ChatDotRound"
              class="ai-btn"
              aria-label="打开 AI 运维助手"
              @click="aiOpen = true"
            />
          </el-tooltip>
          <!-- 任务铃：有进行中的后台任务时亮角标，点开看进度、跳任务中心 -->
          <el-popover trigger="click" width="320">
            <template #reference>
              <!-- icon-only 触发器：badge 无文字，必须带 title/aria-label（ui-ux-pro-max §1 aria-labels） -->
              <el-badge
                :value="activeTasks.length"
                :hidden="!activeTasks.length"
                :max="99"
                class="task-bell"
                title="任务通知"
                aria-label="任务通知"
              >
                <el-icon :size="18"><Bell /></el-icon>
              </el-badge>
            </template>
            <div class="task-pop-head">进行中任务（{{ activeTasks.length }}）</div>
            <div v-if="!activeTasks.length" class="task-pop-empty">当前没有进行中的任务</div>
            <div v-else class="task-pop-list">
              <!-- 条目可点直达任务中心（与底部「前往任务中心」同目标） -->
              <div v-for="t in activeTasks" :key="t.id" class="task-pop-item" @click="router.push('/tasks')">
                <span class="task-pop-title">{{ t.title }}</span>
                <el-tag :type="t.status === 'running' ? 'primary' : 'info'" size="small">
                  {{ t.status === 'running' ? '执行中' : '等待中' }}
                </el-tag>
              </div>
            </div>
            <el-button text type="primary" class="task-pop-more" @click="router.push('/tasks')">前往任务中心</el-button>
          </el-popover>
          <!-- 角色徽标：文案统一走 utils/format 的 roleText（与仪表盘平台信息同源，operator 正确显示「操作员」） -->
          <el-tag
            :type="isAdmin ? 'warning' : role === 'operator' ? 'primary' : 'info'"
            :effect="isAdmin ? 'dark' : 'plain'"
            size="small"
          >{{ roleText(role) }}</el-tag>
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
  <!-- 移动端抽屉遮罩：点击关闭侧栏 -->
  <div v-if="isMobile && drawerOpen" class="drawer-backdrop" @click="drawerOpen = false" />

  <!-- AI 助手抽屉（IA 精简批次）：会话仅存内存，抽屉关闭仅隐藏；宽度 520px 兼顾气泡排版与代码块 -->
  <el-drawer v-model="aiOpen" title="AI 运维助手" size="520px" :append-to-body="true">
    <AiChat embedded />
  </el-drawer>
</template>

<script setup>
import { computed, defineAsyncComponent, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
// 图标按需显式 import：Share/Tickets 随拓扑图与 cloud-init 独立菜单项撤销一并移除
import { ArrowDown, ArrowLeft, ArrowRight, Bell, Box, ChatDotRound, Connection, Cpu, DataLine, Delete, Document, FolderOpened, FullScreen, Goods, List, Menu, Monitor, Odometer, Picture, Setting, SwitchButton, Ticket, Timer, User, UserFilled } from '@element-plus/icons-vue'
import { useAuth } from '../store/auth'
import { api } from '../api'
import { roleText, vmStatusText, vmStatusTag } from '../utils/format'
import { getPollInterval, POLL_DEFAULTS } from '../utils/settings'

// AI 助手抽屉：异步组件避免 marked/DOMPurify 进入口 chunk；el-drawer 首次打开才渲染内容，
// 关闭仅隐藏不清空（会话常驻，进行中的流式回答不被打断）
const AiChat = defineAsyncComponent(() => import('../views/AiChat.vue'))
const aiOpen = ref(false)

const route = useRoute()
const router = useRouter()
const { state, isAdmin, canOperate, logout } = useAuth()

// 侧栏折叠状态持久化：ref 工厂读初始值，watch 写回（刷新后保持上次的折叠选择）
const COLLAPSED_KEY = 'vmops-sidebar-collapsed'
const collapsed = ref(localStorage.getItem(COLLAPSED_KEY) === '1')
watch(collapsed, (v) => {
  try {
    localStorage.setItem(COLLAPSED_KEY, v ? '1' : '0')
  } catch (e) {
    /* localStorage 不可用（隐私模式等）时仅本次会话生效 */
  }
})

const role = computed(() => (state.user && state.user.role) || '')

// 移动端：侧栏转固定抽屉（<768px）。桌面端走折叠逻辑，移动端始终全宽抽屉。
const isMobile = ref(false)
const drawerOpen = ref(false)
function checkMobile() {
  isMobile.value = window.matchMedia('(max-width: 768px)').matches
  if (!isMobile.value) drawerOpen.value = false
}
// 抽屉宽度：移动端固定 220px，桌面端按折叠态 64/180
const asideWidth = computed(() =>
  isMobile.value ? '220px' : collapsed.value ? '64px' : '180px'
)
// 路由切换自动收起抽屉（手机选完菜单即关闭）
watch(() => route.path, () => {
  drawerOpen.value = false
})

// 分组导航：group 字段同时驱动展开态（el-menu-item-group）与折叠态 v-for，
// adminOnly / operateOnly 过滤在 menuGroups 里统一做。层级思路（IA 精简批次）：
// 资源组 = 用户生产消费的对象（虚拟机/镜像/应用商店）；基础设施 = 平台底座
// （宿主机 KVM 宿主/存储池/网络/Docker 容器运行时）；拓扑并入仪表盘 tab、AI 助手改顶栏抽屉、
// cloud-init 模板并入设置页（原三个独立菜单项与「应用」分组已撤销）。
const navItems = [
  { index: '/dashboard', label: '仪表盘', icon: DataLine, group: '总览' },
  { index: '/vms', label: '虚拟机', icon: Monitor, group: '资源' },
  { index: '/images', label: '镜像管理', icon: Picture, group: '资源' },
  // 应用商店/Docker 管理为 operator+ 页面（路由 requiresOperate）：operateOnly 让 viewer 不再看到点进去被弹回的菜单项
  { index: '/apps', label: '应用商店', icon: Goods, group: '资源', operateOnly: true },
  { index: '/grant-requests', label: '资产申请', icon: Ticket, group: '资源', operateOnly: true },
  { index: '/hosts', label: '宿主机', icon: Cpu, group: '基础设施' },
  { index: '/storage', label: '存储池', icon: FolderOpened, group: '基础设施' },
  { index: '/networks', label: '网络', icon: Connection, group: '基础设施' },
  { index: '/docker', label: 'Docker 管理', icon: Box, group: '基础设施', operateOnly: true },
  { index: '/tasks', label: '任务中心', icon: List, group: '运维' },
  { index: '/audit', label: '审计中心', icon: Document, group: '运维' },
  { index: '/crons', label: '计划任务', icon: Timer, group: '运维', adminOnly: true },
  { index: '/recycle-bin', label: '回收站', icon: Delete, group: '运维', adminOnly: true },
  // 工具箱（进程 Top/磁盘诊断）：低频管理员功能，归管理组而非运维组（运维组只留任务/审计/回收等动线）
  { index: '/toolbox', label: '工具箱', icon: Odometer, group: '管理', adminOnly: true },
  { index: '/users', label: '用户管理', icon: User, group: '管理', adminOnly: true },
  { index: '/settings', label: '系统设置', icon: Setting, group: '管理', adminOnly: true }
]

const menuGroups = computed(() => {
  const visible = navItems.filter(
    (it) => (!it.adminOnly || isAdmin.value) && (!it.operateOnly || canOperate.value)
  )
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

// 全局搜索：每次下拉展开都重新拉 VM 清单（不做常驻缓存，新建/删除的机器下次展开即生效）。
// viewer 也可用（GET /vms 对 viewer 放行）。拉取失败静默保留旧清单（搜索是辅助入口）。
const searchVMs = ref([])
const searchSel = ref('')
const searchLoading = ref(false)
async function loadSearchVMs() {
  searchLoading.value = true
  try {
    const res = await api.listVMs()
    searchVMs.value = (res.data && res.data.items) || []
  } catch (e) {
    /* 静默：保留上次清单 */
  } finally {
    searchLoading.value = false
  }
}
function onSearchVisible(visible) {
  if (visible) loadSearchVMs()
}
function goSearchVM(id) {
  if (!id) return
  searchSel.value = ''
  router.push({ name: 'vm-detail', params: { id: String(id) } })
}

// 全屏切换（演示投屏用）
function toggleFullscreen() {
  if (document.fullscreenElement) {
    document.exitFullscreen()
  } else {
    document.documentElement.requestFullscreen()
  }
}

// 任务铃轮询：pending/running 两路合并；失败静默（铃铛只是辅助入口，不打扰用户）
const activeTasks = ref([])
let taskTimer = null
async function loadActiveTasks() {
  try {
    const [run, pend] = await Promise.all([
      api.listTasks({ status: 'running', page_size: 100 }),
      api.listTasks({ status: 'pending', page_size: 100 })
    ])
    // unwrap 后是 {code,message,data} 包体，items 在 .data 里（审计 P0：原写法恒 undefined，任务铃从未工作）
    activeTasks.value = [...(run.data?.items || []), ...(pend.data?.items || [])]
  } catch (e) {
    /* 静默 */
  }
}
onMounted(() => {
  checkMobile()
  window.addEventListener('resize', checkMobile)
  loadActiveTasks()
  taskTimer = setInterval(loadActiveTasks, getPollInterval('tasks', POLL_DEFAULTS.tasks))
})
onUnmounted(() => {
  window.removeEventListener('resize', checkMobile)
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
  background: linear-gradient(180deg, var(--brand-deep-1) 0%, var(--brand-deep-2) 100%);
  border-right: none;
  display: flex;
  flex-direction: column;
  transition: width 0.2s ease;
  overflow: hidden;
}
.brand {
  height: 60px; /* 与顶栏 el-header 默认高度 60px 对齐，侧栏/顶栏分界线齐平 */
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
.brand-logo {
  width: 30px;
  height: 30px;
  border-radius: 8px;
  flex-shrink: 0;
}
.brand-text {
  font-size: 1.15rem;
  font-weight: 700;
  color: #fff;
  letter-spacing: 0.3px;
  white-space: nowrap;
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
  /* 折叠态图标同样可能超出 100vh：不滚动的话底部收起/展开条会被 .aside 的 overflow:hidden 裁掉（用户反馈"找不到收回侧边栏"） */
  overflow-y: auto;
  min-height: 0;
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
.header-left {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  max-width: 460px;
  margin-right: 16px;
}
.global-search {
  width: 100%;
}
.s-name {
  flex: 1;
  margin-right: 8px;
}
.s-tag {
  margin-left: auto;
}
.fs-btn {
  color: var(--color-muted-foreground);
}
/* 顶栏 AI 助手按钮（icon-only，配色对齐全屏钮） */
.ai-btn {
  color: var(--color-muted-foreground);
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
  cursor: pointer; /* 条目可点直达任务中心 */
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
  transition: background 0.2s ease;
}
/* 键盘焦点环保留（ui-ux-pro-max §1 focus-states，反模式"移除焦点环"）：
   现代浏览器 :focus-visible 只在键盘导航时出现，鼠标点击不再出现默认 outline */
.user-entry:focus-visible {
  outline: 2px solid var(--el-color-primary);
  outline-offset: 2px;
}
.user-entry:hover {
  background: var(--color-background);
}
.entry-caret {
  color: var(--color-muted-foreground);
}
.main {
  background: var(--color-background);
  padding: var(--space-2xl); /* 24px，8px 栅格（原 20px 不在栅格上） */
}
.aside-collapse-bar {
  margin: auto 12px 12px;
  flex-shrink: 0; /* 菜单区滚动时折叠条必须常驻可见，不允许被压缩出视野 */
  padding: 9px 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  border-radius: 18px;
  color: rgba(255, 255, 255, 0.85);
  font-size: 0.85rem;
  cursor: pointer;
  background: rgba(255, 255, 255, 0.08);
  transition: all 0.2s ease;
}
.aside-collapse-bar:hover {
  background: rgba(255, 255, 255, 0.1);
  color: #fff;
}

/* 移动端：侧栏转固定抽屉（<768px 生效；桌面端维持弹性布局） */
.aside-mobile {
  position: fixed;
  top: 0;
  left: 0;
  height: 100vh;
  z-index: 1001;
  transform: translateX(-100%);
  transition: transform 0.25s ease;
  box-shadow: var(--shadow-xl);
}
.aside-mobile-open {
  transform: translateX(0);
}
/* 移动端抽屉不显示底部折叠条（始终全宽展示分组菜单） */
.aside-mobile .aside-collapse-bar {
  display: none;
}
.drawer-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(13, 36, 68, 0.45); /* 深空蓝半透明遮罩 */
  z-index: 1000;
}
.menu-btn {
  color: var(--color-muted-foreground);
}

/* 窄屏响应式：压缩主区内边距、放开顶栏搜索宽度，避免横向溢出 */
@media (max-width: 768px) {
  .header-left {
    max-width: none;
    margin-right: 8px;
  }
  .global-search {
    min-width: 0;
  }
  .main {
    padding: 16px;
  }
}
@media (max-width: 480px) {
  .main {
    padding: 12px;
  }
  .header-right {
    gap: 8px;
  }
}
</style>
