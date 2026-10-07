// 运维与扩展域：计划任务/回收站/应用商店/镜像市场/AI 状态/VM 文件管理。
// 此前这些域全部绕过 api 裸调 http（2026-10 API 收口批次统一迁入）。
import { http, unwrap } from './http'

export const ops = {
  // 计划任务（cron 五字段表达式，平台侧整分调度）
  cronList: () => unwrap(http.get('/crons')),
  cronPreview: (expr) => unwrap(http.get('/crons/preview', { params: { expr } })),
  // 内置任务模板（只读）与复制任务
  cronTemplates: () => unwrap(http.get('/crons/templates')),
  cronDuplicate: (id) => unwrap(http.post(`/crons/${id}/duplicate`)),
  cronCreate: (payload) => unwrap(http.post('/crons', payload)),
  cronUpdate: (id, payload) => unwrap(http.put('/crons/' + id, payload)),
  cronToggle: (id) => unwrap(http.post(`/crons/${id}/toggle`)),
  cronRun: (id) => unwrap(http.post(`/crons/${id}/run`)),
  // 执行历史（params: { page, page_size }）
  cronRuns: (id, params) => unwrap(http.get(`/crons/${id}/runs`, { params })),
  cronDelete: (id) => unwrap(http.delete('/crons/' + id)),

  // 回收站（VM 软删恢复/彻底清除；purge 附带 purge_volumes=true 连卷清除）
  recycleList: () => unwrap(http.get('/vms-recycle')),
  recycleRestore: (id) => unwrap(http.post(`/vms-recycle/${id}/restore`)),
  recyclePurge: (id) => unwrap(http.delete(`/vms-recycle/${id}/purge`, { params: { purge_volumes: 'true' } })),

  // 应用商店（VM 内 SSH 脚本应用；安装转后台任务，202 返回 task_id）
  listApps: () => unwrap(http.get('/apps')),
  appDetail: (id) => unwrap(http.get('/apps/' + id)),
  installApp: (payload) => unwrap(http.post('/vms/apps/install', payload)),

  // 运维自动化（P4）：引擎状态 + 批量执行（adhoc/playbook，ansible_run 异步任务）
  ansibleStatus: () => unwrap(http.get('/ansible/status')),
  ansibleRun: (payload) => unwrap(http.post('/ansible/run', payload)),
  ansibleDeployKey: (payload) => unwrap(http.post('/ansible/deploy-key', payload)),
  // playbook CRUD（保存前服务端过 --syntax-check）
  ansiblePlaybooks: () => unwrap(http.get('/ansible/playbooks')),
  ansiblePlaybook: (id) => unwrap(http.get(`/ansible/playbooks/${id}`)),
  ansiblePlaybookCreate: (payload) => unwrap(http.post('/ansible/playbooks', payload)),
  ansiblePlaybookUpdate: (id, payload) => unwrap(http.put(`/ansible/playbooks/${id}`, payload)),
  ansiblePlaybookDelete: (id) => unwrap(http.delete(`/ansible/playbooks/${id}`)),
  ansiblePlaybookCheck: (payload) => unwrap(http.post('/ansible/playbooks/check', payload)),

  // 镜像市场（云镜像 + 官方安装 ISO；下载转后台任务，202 返回 task_id）
  imageMarket: () => unwrap(http.get('/images/market')),
  imageMarketIso: () => unwrap(http.get('/images/market/iso')),
  imageMarketDownload: (payload) => unwrap(http.post('/images/market/download', payload)),
  imageMarketIsoDownload: (payload) => unwrap(http.post('/images/market/iso/download', payload)),

  // AI 助手状态（模型/端点可用性）
  aiStatus: () => unwrap(http.get('/ai/status')),

  // 通用只读 PromQL 查询（原生看板扩展层，panels.js 注册表消费）：minutes 缺省后端按 instant 单点查询
  promQuery: (query, minutes) => unwrap(http.post('/monitor/prom-query', { query, minutes })),

  // VM 文件管理（在线走 VM 内 SSH，需 creds；离线走 guestmount 只读挂系统盘，仅 path）
  // creds = { host, port, user, password }（页面侧连接表单持有，密码不落盘）
  vmFilesList: (id, payload) => unwrap(http.post(`/vms/${id}/files/list`, payload)),
  vmFilesOfflineList: (id, path) => unwrap(http.post(`/vms/${id}/files/offline/list`, { path })),
  vmFilesOfflineMount: (id) => unwrap(http.post(`/vms/${id}/files/offline/mount`)),
  vmFilesOfflineUnmount: (id) => unwrap(http.post(`/vms/${id}/files/offline/unmount`)),
  // 下载直接返回文件内容（octet-stream），unwrap 后即内容本体，适合文本/配置文件
  vmFilesOfflineDownload: (id, path) => unwrap(http.get(`/vms/${id}/files/offline/download`, { params: { path } })),
  vmFilesDownload: (id, payload) => unwrap(http.post(`/vms/${id}/files/download`, payload)),
  vmFilesUpload: (id, payload) => unwrap(http.post(`/vms/${id}/files/upload`, payload)),
  vmFilesDelete: (id, payload) => unwrap(http.post(`/vms/${id}/files/delete`, payload)),
  vmFilesMkdir: (id, payload) => unwrap(http.post(`/vms/${id}/files/mkdir`, payload))
}
