import { createRouter, createWebHashHistory } from 'vue-router'
import { useAuth } from '../store/auth'
// 首屏必需：未登录必然落 Login，登录后必然落 MainLayout 外壳。
// 这两个懒加载只会多一次网络往返（还会闪白），所以刻意保持静态 import 留在入口 chunk。
import Login from '../views/Login.vue'
import MainLayout from '../layout/MainLayout.vue'

// 其余页面全部动态 import：每个页面单独成 chunk，进到该路由才下载。
// ConsolePage 引入 xterm（含 xterm.css），Dashboard/VmList/VmDetail 引入 echarts，
// 静态 import 时它们必然被并进入口 chunk —— 这是原先 2.8MB 单包的主要来源。
const routes = [
  { path: '/login', name: 'login', component: Login, meta: { public: true } },
  { path: '/console/:id', name: 'console', component: () => import('../views/ConsolePage.vue') },
  {
    path: '/',
    component: MainLayout,
    redirect: '/dashboard',
    children: [
      { path: 'dashboard', name: 'dashboard', component: () => import('../views/Dashboard.vue') },
      { path: 'vms', name: 'vms', component: () => import('../views/VmList.vue') },
      { path: 'vms/new', name: 'vm-create', component: () => import('../views/CreateVmWizard.vue'), meta: { requiresAdmin: true } },
      { path: 'vms/:id', name: 'vm-detail', component: () => import('../views/VmDetail.vue') },
      { path: 'hosts', name: 'hosts', component: () => import('../views/HostList.vue') },
      { path: 'images', name: 'images', component: () => import('../views/ImageList.vue') },
      { path: 'storage', name: 'storage', component: () => import('../views/StorageList.vue') },
      { path: 'networks', name: 'networks', component: () => import('../views/NetworkList.vue') },
      { path: 'tasks', name: 'tasks', component: () => import('../views/TaskList.vue') },
      { path: 'audit', name: 'audit', component: () => import('../views/AuditList.vue') },
      { path: 'monitor', redirect: '/dashboard?tab=monitor' },
      { path: 'profile', name: 'profile', component: () => import('../views/Profile.vue') },
      { path: 'users', name: 'users', component: () => import('../views/UserList.vue'), meta: { requiresAdmin: true } },
      { path: 'settings', name: 'settings', component: () => import('../views/Settings.vue'), meta: { requiresAdmin: true } }
    ]
  }
]

const router = createRouter({
  history: createWebHashHistory(),
  routes
})

router.beforeEach((to) => {
  const { isLoggedIn, isAdmin } = useAuth()
  if (!to.meta.public && !isLoggedIn.value) {
    return { name: 'login' }
  }
  if (to.name === 'login' && isLoggedIn.value) {
    return { name: 'dashboard' }
  }
  // 管理员专属页：viewer 直输 URL 也进不去
  if (to.meta.requiresAdmin && !isAdmin.value) {
    return { name: 'dashboard' }
  }
  return true
})

export default router
