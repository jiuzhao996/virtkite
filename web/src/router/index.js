import { createRouter, createWebHashHistory } from 'vue-router'
import { useAuth } from '../store/auth'
import Login from '../views/Login.vue'
import MainLayout from '../layout/MainLayout.vue'
import Dashboard from '../views/Dashboard.vue'
import VmList from '../views/VmList.vue'
import HostList from '../views/HostList.vue'
import ImageList from '../views/ImageList.vue'
import AuditList from '../views/AuditList.vue'
import StorageList from '../views/StorageList.vue'

const routes = [
  { path: '/login', name: 'login', component: Login, meta: { public: true } },
  {
    path: '/',
    component: MainLayout,
    redirect: '/dashboard',
    children: [
      { path: 'dashboard', name: 'dashboard', component: Dashboard },
      { path: 'vms', name: 'vms', component: VmList },
      { path: 'hosts', name: 'hosts', component: HostList },
      { path: 'images', name: 'images', component: ImageList },
      { path: 'storage', name: 'storage', component: StorageList },
      { path: 'audit', name: 'audit', component: AuditList }
    ]
  }
]

const router = createRouter({
  history: createWebHashHistory(),
  routes
})

router.beforeEach((to) => {
  const { isLoggedIn } = useAuth()
  if (!to.meta.public && !isLoggedIn.value) {
    return { name: 'login' }
  }
  if (to.name === 'login' && isLoggedIn.value) {
    return { name: 'dashboard' }
  }
  return true
})

export default router
