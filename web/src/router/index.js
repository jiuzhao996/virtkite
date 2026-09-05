import { createRouter, createWebHashHistory } from 'vue-router'
import { useAuth } from '../store/auth'
import Login from '../views/Login.vue'
import MainLayout from '../layout/MainLayout.vue'
import ConsolePage from '../views/ConsolePage.vue'
import Dashboard from '../views/Dashboard.vue'
import VmList from '../views/VmList.vue'
import VmDetail from '../views/VmDetail.vue'
import CreateVmWizard from '../views/CreateVmWizard.vue'
import HostList from '../views/HostList.vue'
import ImageList from '../views/ImageList.vue'
import AuditList from '../views/AuditList.vue'
import StorageList from '../views/StorageList.vue'
import NetworkList from '../views/NetworkList.vue'
import TaskList from '../views/TaskList.vue'
import SessionList from '../views/SessionList.vue'

const routes = [
  { path: '/login', name: 'login', component: Login, meta: { public: true } },
  { path: '/console/:id', name: 'console', component: ConsolePage },
  {
    path: '/',
    component: MainLayout,
    redirect: '/dashboard',
    children: [
      { path: 'dashboard', name: 'dashboard', component: Dashboard },
      { path: 'vms', name: 'vms', component: VmList },
      { path: 'vms/new', name: 'vm-create', component: CreateVmWizard, meta: { requiresAdmin: true } },
      { path: 'vms/:id', name: 'vm-detail', component: VmDetail },
      { path: 'hosts', name: 'hosts', component: HostList },
      { path: 'images', name: 'images', component: ImageList },
      { path: 'storage', name: 'storage', component: StorageList },
      { path: 'networks', name: 'networks', component: NetworkList },
      { path: 'tasks', name: 'tasks', component: TaskList },
      { path: 'sessions', name: 'sessions', component: SessionList },
      { path: 'audit', name: 'audit', component: AuditList, meta: { requiresAdmin: true } }
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
