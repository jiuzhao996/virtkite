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
  getVMDetail: (id) => unwrap(http.get('/vms/' + id + '/detail')),
  getVMXML: (id) => unwrap(http.get('/vms/' + id + '/xml')),
  updateVMXML: (id, xml) => unwrap(http.put('/vms/' + id + '/xml', { xml })),
  createVM: (payload) => unwrap(http.post('/vms', payload)),
  startVM: (id) => unwrap(http.post('/vms/' + id + '/start')),
  stopVM: (id) => unwrap(http.post('/vms/' + id + '/stop')),
  restartVM: (id) => unwrap(http.post('/vms/' + id + '/restart')),
  deleteVM: (id) => unwrap(http.delete('/vms/' + id)),

  // 虚拟机 - 配置模型 / 硬件管理 / 动作（virt-manager 对齐）
  getVMSpec: (id) => unwrap(http.get('/vms/' + id + '/spec')),
  updateVMSpec: (id, spec) => unwrap(http.put('/vms/' + id + '/spec', spec)),
  pauseVM: (id) => unwrap(http.post('/vms/' + id + '/pause')),
  resumeVM: (id) => unwrap(http.post('/vms/' + id + '/resume')),
  getVMStats: (id) => unwrap(http.get('/vms/' + id + '/stats')),
  setVcpu: (id, vcpu) => unwrap(http.put('/vms/' + id + '/cpu', { vcpu })),
  setMemory: (id, memory_mb) => unwrap(http.put('/vms/' + id + '/memory', { memory_mb })),
  setAutostart: (id, enabled) => unwrap(http.put('/vms/' + id + '/autostart', { enabled })),
  setBoot: (id, devices) => unwrap(http.put('/vms/' + id + '/boot', { devices })),
  attachDisk: (id, disk) => unwrap(http.post('/vms/' + id + '/devices/disks', { disk })),
  detachDisk: (id, target) => unwrap(http.delete('/vms/' + id + '/devices/disks/' + target)),
  attachInterface: (id, iface) => unwrap(http.post('/vms/' + id + '/devices/interfaces', { interface: iface })),
  detachInterface: (id, mac) => unwrap(http.delete('/vms/' + id + '/devices/interfaces/' + mac)),
  cloneVM: (id, payload) => unwrap(http.post('/vms/' + id + '/clone', payload)),
  vmOptions: () => unwrap(http.get('/vms/options')),

  listSnapshots: (id) => unwrap(http.get('/vms/' + id + '/snapshots')),
  createSnapshot: (id, name, description) => unwrap(http.post('/vms/' + id + '/snapshots', { name, description })),
  deleteSnapshot: (id, snap) => unwrap(http.delete('/vms/' + id + '/snapshots/' + snap)),
  revertSnapshot: (id, snap) => unwrap(http.post('/vms/' + id + '/snapshots/' + snap + '/revert')),
  vncToken: (id) => unwrap(http.post('/vms/' + id + '/vnc-token')),
  scanImportVMs: () => unwrap(http.get('/vms/import/scan')),
  importVMs: (hostId, names) => unwrap(http.post('/vms/import', { host_id: hostId, names })),

  // 存储池
  listStoragePools: () => unwrap(http.get('/storage/pools')),
  getStoragePool: (name) => unwrap(http.get('/storage/pools/' + name)),
  createStoragePool: (payload) => unwrap(http.post('/storage/pools', payload)),
  deleteStoragePool: (name) => unwrap(http.delete('/storage/pools/' + name)),
  createVolume: (pool, payload) => unwrap(http.post('/storage/pools/' + pool + '/volumes', payload)),
  deleteVolume: (pool, vol) => unwrap(http.delete('/storage/pools/' + pool + '/volumes/' + vol)),

  // 镜像
  listImages: (params) => unwrap(http.get('/images', { params })),
  getImage: (id) => unwrap(http.get('/images/' + id)),
  uploadImage: (formData) =>
    unwrap(http.post('/images/upload', formData, { headers: { 'Content-Type': 'multipart/form-data' } })),
  deleteImage: (id) => unwrap(http.delete('/images/' + id)),
  setImageTemplate: (id, is_template) => unwrap(http.put('/images/' + id + '/template', { is_template })),
  cloneImage: (id, payload) => unwrap(http.post('/images/' + id + '/clone', payload)),

  // 网络
  listNetworks: () => unwrap(http.get('/networks')),
  getNetwork: (name) => unwrap(http.get('/networks/' + name)),
  createNetwork: (payload) => unwrap(http.post('/networks', payload)),
  defineNetworkXML: (payload) => unwrap(http.post('/networks/xml', payload)),
  updateNetwork: (name, xml) => unwrap(http.put('/networks/' + name, { xml })),
  startNetwork: (name) => unwrap(http.post('/networks/' + name + '/start')),
  stopNetwork: (name) => unwrap(http.post('/networks/' + name + '/stop')),
  deleteNetwork: (name) => unwrap(http.delete('/networks/' + name)),

  // 仪表盘
  dashboardOverview: () => unwrap(http.get('/dashboard/overview')),
  vmStatus: () => unwrap(http.get('/dashboard/vm-status')),
  hostStats: () => unwrap(http.get('/dashboard/host-stats')),
  vmPerf: () => unwrap(http.get('/dashboard/vm-perf')),

  // 审计
  listAudit: (params) => unwrap(http.get('/audit', { params })),
  auditSummary: () => unwrap(http.get('/audit/summary')),
  auditActions: () => unwrap(http.get('/audit/actions')),

  // 异步任务（耗时操作转后台：createVM/deleteVM/cloneVM/cloneImage/stopVM 返回 202 {task_id}，再轮询任务）
  getTask: (id) => unwrap(http.get('/tasks/' + id)),
  listTasks: (params) => unwrap(http.get('/tasks', { params }))
}

export { TOKEN_KEY }
export default http
