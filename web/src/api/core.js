// 核心域：认证/宿主机/虚拟机/存储池/镜像/用户组/网络/用户/cloud-init/监控/仪表盘/审计/任务/会话/设置/公告。
// 方法即原 api/index.js 的既有内容（2026-09 收口进目录时的原样迁移，调用方零改动）。
import { http, unwrap, uploadConfig } from './http'

export const core = {
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
  getVMXML: (id) => unwrap(http.get('/vms/' + id + '/xml')),
  updateVMXML: (id, xml) => unwrap(http.put('/vms/' + id + '/xml', { xml })),
  createVM: (payload) => unwrap(http.post('/vms', payload)),
  startVM: (id) => unwrap(http.post('/vms/' + id + '/start')),
  stopVM: (id) => unwrap(http.post('/vms/' + id + '/stop')),
  restartVM: (id) => unwrap(http.post('/vms/' + id + '/restart')),
  deleteVM: (id) => unwrap(http.delete('/vms/' + id)),

  // 虚拟机 - 配置模型 / 硬件管理 / 动作（virt-manager 对齐）
  getVMSpec: (id) => unwrap(http.get('/vms/' + id + '/spec')),
  // 手动设置虚拟机 IP（覆盖 DHCP 回填值，传空字符串清除；仅管理员）
  pauseVM: (id) => unwrap(http.post('/vms/' + id + '/pause')),
  resumeVM: (id) => unwrap(http.post('/vms/' + id + '/resume')),
  getVMStats: (id) => unwrap(http.get('/vms/' + id + '/stats')),
  setVcpu: (id, vcpu) => unwrap(http.put('/vms/' + id + '/cpu', { vcpu })),
  setMemory: (id, memory_mb) => unwrap(http.put('/vms/' + id + '/memory', { memory_mb })),
  setAutostart: (id, enabled) => unwrap(http.put('/vms/' + id + '/autostart', { enabled })),
  attachDisk: (id, disk) => unwrap(http.post('/vms/' + id + '/devices/disks', { disk })),
  // 分离磁盘：deleteVolume=true 时附带 query delete_volume，请求后端同时删除存储卷；
  // 不传（undefined）时 axios 自动省略该参数，保持旧的「仅分离」语义（后端契约：{ vm, target, volume_deleted, keep_reason }）
  detachDisk: (id, target, deleteVolume) => unwrap(http.delete('/vms/' + id + '/devices/disks/' + target, { params: { delete_volume: deleteVolume } })),
  attachInterface: (id, iface) => unwrap(http.post('/vms/' + id + '/devices/interfaces', { interface: iface })),
  quickAttachDisk: (id, opts = {}) => unwrap(http.post('/vms/' + id + '/devices/disks/quick', opts)),
  ensureStandardDevices: (id) => unwrap(http.post('/vms/' + id + '/devices/standard')),
  detachInterface: (id, mac) => unwrap(http.delete('/vms/' + id + '/devices/interfaces/' + mac)),
  cloneVM: (id, payload) => unwrap(http.post('/vms/' + id + '/clone', payload)),
  vmOptions: () => unwrap(http.get('/vms/options')),

  listSnapshots: (id) => unwrap(http.get('/vms/' + id + '/snapshots')),
  createSnapshot: (id, name, description) => unwrap(http.post('/vms/' + id + '/snapshots', { name, description })),
  deleteSnapshot: (id, snap) => unwrap(http.delete('/vms/' + id + '/snapshots/' + snap)),
  revertSnapshot: (id, snap) => unwrap(http.post('/vms/' + id + '/snapshots/' + snap + '/revert')),
  // 资产授权（借鉴堡垒机 4A：admin 把 VM 分配给用户，授权决定可见性，未授权查无此项）
  listVMGrants: (id) => unwrap(http.get('/vms/' + id + '/grants')),
  grantVM: (id, payload) => unwrap(http.post('/vms/' + id + '/grants', payload)),
  revokeVMGrant: (id, gid) => unwrap(http.delete('/vms/' + id + '/grants/' + gid)),
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
  // 上传大文件：不限超时 + 可选进度回调（percent, loaded, total）
  uploadImage: (formData, onProgress) => unwrap(http.post('/images/upload', formData, uploadConfig(onProgress))),
  deleteImage: (id) => unwrap(http.delete('/images/' + id)),
  setImageTemplate: (id, is_template) => unwrap(http.put('/images/' + id + '/template', { is_template })),
  cloneImage: (id, payload) => unwrap(http.post('/images/' + id + '/clone', payload)),

  // 用户组（教学场景按组批量授权）
  listUserGroups: () => unwrap(http.get('/user-groups')),
  createUserGroup: (payload) => unwrap(http.post('/user-groups', payload)),
  updateUserGroup: (id, payload) => unwrap(http.put('/user-groups/' + id, payload)),
  deleteUserGroup: (id) => unwrap(http.delete('/user-groups/' + id)),
  setUserGroupMembers: (id, userIds) => unwrap(http.post('/user-groups/' + id + '/members', { user_ids: userIds })),
  listVMGroupGrants: (id) => unwrap(http.get('/vms/' + id + '/group-grants')),
  grantVMToGroup: (id, payload) => unwrap(http.post('/vms/' + id + '/group-grants', payload)),
  revokeVMGroupGrant: (id, gid) => unwrap(http.delete('/vms/' + id + '/group-grants/' + gid)),
  // 授权申请/审批（v3.6）
  listApplyCatalog: () => unwrap(http.get('/vms/apply-catalog')),
  applyForAsset: (id, payload) => unwrap(http.post('/vms/' + id + '/grant-request', payload)),
  listMyRequests: () => unwrap(http.get('/grant-requests/mine')),
  listGrantRequests: (params) => unwrap(http.get('/grant-requests', { params })),
  approveGrantRequest: (id, payload) => unwrap(http.post('/grant-requests/' + id + '/approve', payload)),
  rejectGrantRequest: (id, payload) => unwrap(http.post('/grant-requests/' + id + '/reject', payload)),

  // 网络
  listNetworks: () => unwrap(http.get('/networks')),
  // 网络通信流量视图（P1：连接边 + 接口速率，前端 3s 轮询）
  networkFlows: () => unwrap(http.get('/networks/flows')),
  getNetwork: (name) => unwrap(http.get('/networks/' + name)),
  createNetwork: (payload) => unwrap(http.post('/networks', payload)),
  setNetworkAutostart: (name, autostart) => unwrap(http.put('/networks/' + name + '/autostart', { autostart })),
  startNetwork: (name) => unwrap(http.post('/networks/' + name + '/start')),
  stopNetwork: (name) => unwrap(http.post('/networks/' + name + '/stop')),
  deleteNetwork: (name) => unwrap(http.delete('/networks/' + name)),

  // 用户管理（仅管理员）
  listUsers: () => unwrap(http.get('/users')),
  createUser: (payload) => unwrap(http.post('/users', payload)),
  updateUser: (id, payload) => unwrap(http.put('/users/' + id, payload)),
  deleteUser: (id) => unwrap(http.delete('/users/' + id)),

  // cloud-init 配置模板（operator/admin：向导「套用模板 / 保存为模板」与管理页共用）
  // spec 为对象（后端出库已反序列化），字段 hostname/user/password/ssh_key/net_mode/ip/gateway/dns
  listCloudInitTemplates: () => unwrap(http.get('/cloud-init-templates')),
  getCloudInitTemplate: (id) => unwrap(http.get('/cloud-init-templates/' + id)),
  createCloudInitTemplate: (payload) => unwrap(http.post('/cloud-init-templates', payload)),
  updateCloudInitTemplate: (id, payload) => unwrap(http.put('/cloud-init-templates/' + id, payload)),
  deleteCloudInitTemplate: (id) => unwrap(http.delete('/cloud-init-templates/' + id)),

  // 监控中心（Alertmanager 告警代理，登录即可看）
  listAlerts: () => unwrap(http.get('/monitor/alerts')),
  // 告警历史（webhook 入库数据，params: { status, fingerprint, page, page_size }）
  monitorAlertHistory: (params) => unwrap(http.get('/monitor/alerts/history', { params })),
  // file_sd 抓取目标预览（与后台落盘文件同源）
  monitorFileSD: () => unwrap(http.get('/monitor/file-sd')),
  // 原生看板历史曲线（Grafana 退役批次 2026-10）
  poolHistory: (minutes) => unwrap(http.get('/monitor/pool-history', { params: { minutes } })),
  vmMetricsHistory: (minutes) => unwrap(http.get('/monitor/vm-metrics-history', { params: { minutes } })),

  // 池平台侧元数据（角色覆盖 + 描述；role 传空串 = 自动推断，description ≤500 字符）
  updatePoolMeta: (name, payload) => unwrap(http.put('/storage/pools/' + name + '/meta', payload)),
  // 把池内已有卷登记进镜像库（同路径重复登记返回 409）；不复制不移动，只建索引
  registerImage: (payload) => unwrap(http.post('/images/register', payload)),
  // 存储卷在用引用（卷管理弹窗的"在用"徽标与删卷确认）
  volumeRefs: (pool) => unwrap(http.get('/storage/pools/' + pool + '/volume-refs')),
  // 全库克隆家谱（跨池血缘图谱节点/边 + 回收候选聚合；数据与删卷守卫同源）
  volumeGraph: () => unwrap(http.get('/storage/volume-graph')),
  // 孤儿卷清理（转后台任务，202 返回 task_id）
  cleanupOrphans: (pool) => unwrap(http.post('/storage/pools/' + pool + '/orphan-cleanup')),

  // 仪表盘
  dashboardOverview: () => unwrap(http.get('/dashboard/overview')),
  dashboardCapacity: () => unwrap(http.get('/dashboard/capacity')),
  vmStatus: () => unwrap(http.get('/dashboard/vm-status')),
  dashboardHostStats: () => unwrap(http.get('/dashboard/host-stats')),
  vmPerf: () => unwrap(http.get('/dashboard/vm-perf')),
  // 历史性能曲线（Prometheus query_range，进页面即画满，无需等轮询攒点）
  hostHistory: (minutes) => unwrap(http.get('/dashboard/host-history', { params: { minutes } })),
  vmHistory: (minutes) => unwrap(http.get('/dashboard/vm-history', { params: { minutes } })),
  vmStatsHistory: (id, minutes) => unwrap(http.get('/vms/' + id + '/stats-history', { params: { minutes } })),
  // VM 内部指标（file_sd 抓取的 node_exporter：根分区/内存/负载；未装 exporter 时 available=false）
  guestMetrics: (id, minutes) => unwrap(http.get(`/vms/${id}/guest-metrics`, { params: { minutes } })),

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

  // 站内通知（告警到人）：铃铛列表/已读流转，unread 字段驱动红点
  listNotifications: (params) => unwrap(http.get('/notifications', { params })),
  markNotificationRead: (id) => unwrap(http.put(`/notifications/${id}/read`)),
  markAllNotificationsRead: () => unwrap(http.put('/notifications/read-all')),

  // SSH 主机密钥（TOFU：首次连接记录指纹，指纹变化拒绝连接防中间人；管理端点，admin）
  listSSHHostKeys: () => unwrap(http.get('/ssh-host-keys')),
  deleteSSHHostKey: (id) => unwrap(http.delete('/ssh-host-keys/' + id)),

  // 系统设置（仅管理员）：GET 生效配置快照 + 可写项当前值；PUT 修改可写项（写入即生效）
  getSettings: () => unwrap(http.get('/settings')),
  updateSettings: (payload) => unwrap(http.put('/settings', payload)),

  // 系统公告（公开接口，无需认证——登录页也展示）；写入口走 updateSettings 的 announcement 键
  getAnnouncement: () => unwrap(http.get('/announcement'))
}
