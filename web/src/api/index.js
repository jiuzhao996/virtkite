import axios from 'axios'

const TOKEN_KEY = 'vmops_token'

const http = axios.create({
  baseURL: '/api',
  timeout: 15000
})

http.interceptors.request.use((cfg) => {
  const t = localStorage.getItem(TOKEN_KEY)
  if (t) cfg.headers.Authorization = 'Bearer ' + t
  return cfg
})

http.interceptors.response.use(
  (res) => res,
  (err) => {
    if (err.response && err.response.status === 401) {
      localStorage.removeItem(TOKEN_KEY)
      // 触发全局未授权：跳转登录
      window.location.hash = '#/login'
    }
    return Promise.reject(err)
  }
)

// 统一解包：返回 res.data（后端统一结构 { code, message, data }）
function unwrap(promise) {
  return promise.then((res) => res.data)
}

export const api = {
  // 认证
  login: (username, password) => unwrap(http.post('/auth/login', { username, password })),
  me: () => unwrap(http.get('/auth/me')),

  // 宿主机
  listHosts: () => unwrap(http.get('/hosts')),
  createHost: (payload) => unwrap(http.post('/hosts', payload)),
  updateHost: (id, payload) => unwrap(http.put('/hosts/' + id, payload)),
  deleteHost: (id) => unwrap(http.delete('/hosts/' + id)),
  testHost: (id) => unwrap(http.post('/hosts/' + id + '/test')),
  hostStats: (id) => unwrap(http.get('/hosts/' + id + '/stats')),

  // 虚拟机
  listVMs: () => unwrap(http.get('/vms')),
  getVM: (id) => unwrap(http.get('/vms/' + id)),
  createVM: (payload) => unwrap(http.post('/vms', payload)),
  startVM: (id) => unwrap(http.post('/vms/' + id + '/start')),
  stopVM: (id) => unwrap(http.post('/vms/' + id + '/stop')),
  restartVM: (id) => unwrap(http.post('/vms/' + id + '/restart')),
  deleteVM: (id) => unwrap(http.delete('/vms/' + id)),

  // 存储池
  listStoragePools: () => unwrap(http.get('/storage/pools')),

  // 镜像
  listImages: (params) => unwrap(http.get('/images', { params })),
  getImage: (id) => unwrap(http.get('/images/' + id)),
  uploadImage: (formData) =>
    unwrap(http.post('/images/upload', formData, { headers: { 'Content-Type': 'multipart/form-data' } })),
  deleteImage: (id) => unwrap(http.delete('/images/' + id)),

  // 仪表盘
  dashboardOverview: () => unwrap(http.get('/dashboard/overview')),
  vmStatus: () => unwrap(http.get('/dashboard/vm-status')),

  // 审计
  listAudit: (params) => unwrap(http.get('/audit', { params })),
  auditSummary: () => unwrap(http.get('/audit/summary'))
}

export { TOKEN_KEY }
export default http
