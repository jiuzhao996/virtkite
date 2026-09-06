import axios from 'axios'
// 依赖方向刻意单向：api → store。TOKEN_KEY 与 logout 都由 store/auth.js 持有，
// store 不再 import api，避免循环依赖（详见 store/auth.js 顶部注释）。
import { TOKEN_KEY, logout } from '../store/auth'

// 普通接口 15s 超时；镜像上传等大流量接口单独覆盖（见 uploadConfig）
const http = axios.create({
  baseURL: '/api',
  timeout: 15000
})

// 镜像上传专用配置：timeout 置 0（axios 语义为不限时）。
// 上传体积可达数 GB，任何固定超时都是错的——15s 全局值必然在传输中途中断请求，
// 后端明明还在正常接收，前端却报「超时」。改为不限时 + onUploadProgress 给用户反馈。
const UPLOAD_TIMEOUT = 0

/**
 * 构造上传请求配置。
 * @param {(percent:number, loaded:number, total:number)=>void} [onProgress] 进度回调，percent 为 0~100 整数
 */
function uploadConfig(onProgress) {
  const cfg = {
    headers: { 'Content-Type': 'multipart/form-data' },
    timeout: UPLOAD_TIMEOUT
  }
  if (typeof onProgress === 'function') {
    cfg.onUploadProgress = (e) => {
      // total 在部分浏览器/代理下可能缺失（chunked），此时只回传已传字节
      const total = e.total || 0
      const percent = total ? Math.min(100, Math.round((e.loaded / total) * 100)) : 0
      onProgress(percent, e.loaded || 0, total)
    }
  }
  return cfg
}

http.interceptors.request.use((cfg) => {
  const t = localStorage.getItem(TOKEN_KEY)
  if (t) cfg.headers.Authorization = 'Bearer ' + t
  return cfg
})

http.interceptors.response.use(
  (res) => res,
  (err) => {
    // 登录接口本身的 401（口令错误）不算会话失效：既不清已有会话，也不跳转，
    // 由 Login.vue 自行展示错误文案。
    const isLoginCall = !!(err.config && String(err.config.url || '').includes('/auth/login'))
    if (err.response && err.response.status === 401 && !isLoginCall) {
      // 必须调 store 的 logout()：同时清 state.token（内存）与 localStorage。
      // 只删 localStorage 会让 isLoggedIn 仍为 true，路由守卫把用户从 /login 弹回 dashboard。
      logout()
      // 触发全局未授权：跳转登录（已在登录页则不重复写 hash）
      if (window.location.hash !== '#/login') window.location.hash = '#/login'
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
  changeMyPassword: (old_password, new_password) => unwrap(http.put('/users/me/password', { old_password, new_password })),

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
  // 上传大文件：不限超时 + 可选进度回调（percent, loaded, total）
  uploadImage: (formData, onProgress) => unwrap(http.post('/images/upload', formData, uploadConfig(onProgress))),
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
  dashboardHostStats: () => unwrap(http.get('/dashboard/host-stats')),
  vmPerf: () => unwrap(http.get('/dashboard/vm-perf')),

  // 审计
  listAudit: (params) => unwrap(http.get('/audit', { params })),
  auditSummary: () => unwrap(http.get('/audit/summary')),
  auditActions: () => unwrap(http.get('/audit/actions')),

  // 异步任务（耗时操作转后台：createVM/deleteVM/cloneVM/cloneImage/stopVM 返回 202 {task_id}，再轮询任务）
  getTask: (id) => unwrap(http.get('/tasks/' + id)),
  listTasks: (params) => unwrap(http.get('/tasks', { params })),
  deleteTask: (id) => unwrap(http.delete('/tasks/' + id)),

  // 控制台会话（谁连了哪台 VM、可强制断开 SSH/串口）
  listSessions: (params) => unwrap(http.get('/sessions', { params })),
  disconnectSession: (id) => unwrap(http.post('/sessions/' + id + '/disconnect')),

  // 系统设置快照（仅管理员）
  getSettings: () => unwrap(http.get('/settings'))
}

// TOKEN_KEY 实际定义在 store/auth.js，这里原样 re-export，保持既有的「从 ../api 导入 TOKEN_KEY」写法可用
export { TOKEN_KEY }
export default http
