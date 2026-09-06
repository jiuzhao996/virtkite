// 前端轮询偏好（localStorage，系统设置页编辑，各轮询页读取）
// key: 轮询场景；def: 默认毫秒；钳制 1000~60000
const PREFIX = 'vmops-poll-'

export function getPollInterval(key, def) {
  try {
    const raw = localStorage.getItem(PREFIX + key)
    if (raw == null) return def
    const n = parseInt(raw, 10)
    if (!isFinite(n)) return def
    return Math.min(60000, Math.max(1000, n))
  } catch (e) {
    return def
  }
}

export function setPollInterval(key, ms) {
  const n = Math.min(60000, Math.max(1000, Number(ms) || 0))
  try {
    localStorage.setItem(PREFIX + key, String(n))
  } catch (e) {}
  return n
}

// 各场景默认轮询间隔（ms），与现有页面硬编码一致
export const POLL_DEFAULTS = {
  vmlist: 5000, // 虚拟机列表静默刷新
  dashboard: 3000, // 仪表盘主机/VM 性能
  tasks: 3000, // 任务中心智能轮询
  sessions: 5000, // 会话列表
  vmstats: 2000 // 虚拟机详情页实时性能（CPU/内存/磁盘/网络 + 曲线）
}

export const POLL_LABELS = {
  vmlist: '虚拟机列表刷新',
  dashboard: '仪表盘性能刷新',
  tasks: '任务中心刷新',
  sessions: '会话列表刷新',
  vmstats: '虚拟机详情性能刷新'
}
