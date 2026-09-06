/**
 * 全站通用格式化 / 映射 / 配色工具（唯一实现，各页面禁止再本地重复定义）
 *
 * 约定：
 * - 所有「状态 → 中文」映射按**领域**分开命名（vm / task / host / session），
 *   不同领域的状态字面量绝不混进同一张表（例如 VM 的 running 与任务的 running 含义不同）。
 * - 所有涉及单位的函数，函数名与注释里都必须写明**入参单位**（fmtSizeGB 收 GB、
 *   fmtSizeBytes 收字节、fmtRateBytes 收字节/秒），避免调用方传错量级。
 * - 颜色一律走 global.css 里的 CSS 变量，不在 JS 里散落十六进制；
 *   echarts / conic-gradient 等必须要真实色值的场景用 cssVar() 读取。
 */

/* ==================== VM 状态 ==================== */

/**
 * VM 状态 → 中文。
 * 取值以后端为准（service/virt/state.go 的 StateToPlatform 只产出这 4 个）：
 *   running / shut off（空格，不是下划线）/ paused / error
 * `stopped` 是历史遗留键（早期列表接口曾返回），合并各页面映射表时刻意保留，
 * 以免旧数据 / 旧缓存落到兜底分支直接显示英文。
 */
export const VM_STATUS_TEXT = {
  running: '运行中',
  'shut off': '已关机',
  stopped: '已关机',
  paused: '已暂停',
  error: '异常'
}

/**
 * VM 状态文案。
 * @param {string} status 后端 vms.status 字面量
 * @param {string} fallback 状态为空时的占位（详情页传 '—'，列表页传空串）
 * @returns {string} 中文文案；未知状态原样返回（便于暴露后端新增取值）
 */
export function vmStatusText(status, fallback = '') {
  return VM_STATUS_TEXT[status] || status || fallback
}

/**
 * VM 状态 → el-tag 的 type。
 * @param {string} status 后端 vms.status 字面量
 * @param {string} fallback 未命中时的 type（列表页历史行为是 'primary'，详情页是 'info'）
 */
export function vmStatusTag(status, fallback = 'info') {
  if (status === 'running') return 'success'
  if (status === 'paused') return 'warning'
  if (status === 'error') return 'danger'
  if (status === 'shut off' || status === 'stopped') return 'info'
  return fallback
}

/**
 * VM 状态 → 语义色（CSS 变量形式，可直接用于 style / el-progress 的 color）。
 * 未知状态按「异常」处理，与仪表盘原行为一致。
 */
export function vmStatusColor(status) {
  if (status === 'running') return 'var(--color-success)'
  if (status === 'paused') return 'var(--color-warning)'
  if (status === 'shut off' || status === 'stopped') return 'var(--color-info)'
  return 'var(--color-danger)'
}

/**
 * VM 状态 → 真实色值（十六进制）。
 * 供 conic-gradient / echarts 等**不接受 var() 的场景**使用，值仍来自 CSS 变量。
 */
export function vmStatusHex(status) {
  if (status === 'running') return cssVar('--color-success', '#16a34a')
  if (status === 'paused') return cssVar('--color-warning', '#d97706')
  if (status === 'shut off' || status === 'stopped') return cssVar('--color-info', '#64748b')
  if (status === 'error') return cssVar('--color-danger', '#dc2626')
  return cssVar('--color-primary', '#2a9da5')
}

/* ==================== 任务状态 / 类型 ==================== */

/** 任务状态 → 中文。后端 tasks.status 只有 pending / running / success / failed。 */
export const TASK_STATUS_TEXT = {
  pending: '等待中',
  running: '执行中',
  success: '成功',
  failed: '失败'
}

/** 任务状态文案；未知状态原样返回。 */
export function taskStatusText(status) {
  return TASK_STATUS_TEXT[status] || status
}

/** 任务状态 → el-tag 的 type（执行中用 primary 区别于 VM 的 running=success）。 */
export function taskStatusTag(status) {
  if (status === 'success') return 'success'
  if (status === 'failed') return 'danger'
  if (status === 'running') return 'primary'
  return 'info'
}

/** 任务类型 → 中文（后端 tasks.type，见 docs/task-contract.md）。 */
export const TASK_TYPE_TEXT = {
  create_vm: '创建虚拟机',
  delete_vm: '删除虚拟机',
  clone_vm: '克隆虚拟机',
  clone_image_vm: '从镜像创建',
  stop_vm: '停止虚拟机'
}

/** 任务类型文案；未知类型原样返回。 */
export function taskTypeText(type) {
  return TASK_TYPE_TEXT[type] || type
}

/* ==================== 宿主机状态 ==================== */

/** 宿主机状态 → 中文（hosts.status）。与 VM 状态是不同域，刻意分表。 */
export const HOST_STATUS_TEXT = {
  online: '在线',
  offline: '离线',
  unknown: '未知'
}

/** 宿主机状态文案；未知状态原样返回。 */
export function hostStatusText(status) {
  return HOST_STATUS_TEXT[status] || status
}

/** 宿主机状态 → el-tag 的 type。 */
export function hostStatusTag(status) {
  if (status === 'online') return 'success'
  if (status === 'offline') return 'danger'
  return 'info'
}

/* ==================== 控制台会话类型 ==================== */

/** 会话连接方式 → 中文（sessions.type）。 */
export const SESSION_TYPE_TEXT = {
  vnc: '图形控制台',
  ssh: 'Web 终端',
  serial: '串口'
}

/** 会话方式文案；未知类型原样返回。 */
export function sessionTypeText(type) {
  return SESSION_TYPE_TEXT[type] || type
}

/** 会话方式 → el-tag 的 type。 */
export function sessionTypeTag(type) {
  if (type === 'vnc') return 'primary'
  if (type === 'ssh') return 'success'
  return 'warning'
}

/* ==================== 审计动作 ==================== */

/**
 * 审计动作 → 中文兜底映射。
 * 后端 GET /api/audit/actions 返回的映射优先级更高（`{...FALLBACK_ACTION_LABELS, ...后端}`），
 * 这张表只在接口不可用（viewer 无权限 / 请求失败）时保证界面不出现英文动作名。
 */
export const FALLBACK_ACTION_LABELS = {
  login: '登录', logout: '登出',
  create_vm: '创建虚拟机', delete_vm: '删除虚拟机', start_vm: '开机', stop_vm: '关机',
  restart_vm: '重启', import_vm: '导入虚拟机', pause_vm: '暂停虚拟机', resume_vm: '恢复虚拟机',
  clone_vm: '克隆虚拟机',
  attach_disk: '挂载磁盘', detach_disk: '移除磁盘', attach_nic: '添加网卡', detach_nic: '移除网卡',
  create_snapshot: '创建快照', delete_snapshot: '删除快照', revert_snapshot: '回滚快照',
  update_vm_spec: '更新虚拟机配置', update_vm_xml: '更新虚拟机XML', update_vm: '更新虚拟机',
  set_vcpu: '调整CPU核数', set_memory: '调整内存', set_autostart: '设置开机自启', set_boot: '设置引导顺序',
  create_host: '添加宿主机', update_host: '更新宿主机', delete_host: '删除宿主机',
  upload_image: '上传镜像', delete_image: '删除镜像', set_image_template: '设置镜像模板', clone_image: '镜像创建虚拟机',
  create_network: '创建网络', update_network: '更新网络', delete_network: '删除网络',
  create_volume: '创建存储卷', delete_volume: '删除存储卷', access: '访问'
}

/* ==================== 时间 ==================== */

/**
 * 时间戳 → `YYYY-MM-DD HH:mm:ss`（补零，等宽，适合表格列对齐）。
 * @param {string|number|Date} v 后端时间字段（RFC3339 字符串 / 毫秒时间戳 / Date）
 * @returns {string} 空值返回 '—'；无法解析时原样返回入参（便于排查脏数据）
 */
export function fmtDateTime(v) {
  if (!v) return '—'
  const d = new Date(v)
  if (isNaN(d.getTime())) return v
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

/**
 * 时间戳 → 本地化 24 小时制（zh-CN，形如 `2026/9/6 12:03:04`）。
 * 与 fmtDateTime 的**输出格式不同**（斜杠、月日不补零），刻意保留两份：
 * 任务中心 / 会话管理沿用本格式，镜像 / 审计沿用 fmtDateTime，避免视觉回归。
 * @param {string|number|Date} v 后端时间字段
 * @returns {string} 空值返回 '—'
 */
export function fmtDateTimeLocale(v) {
  if (!v) return '—'
  return new Date(v).toLocaleString('zh-CN', { hour12: false })
}

/**
 * 当前时刻 → `HH:MM:SS`（24 小时制）。
 * 注意：这是「取现在的时间」，不是格式化某个入参，用于实时采样的时间轴标签。
 */
export function nowClock() {
  return new Date().toLocaleTimeString('zh-CN', { hour12: false })
}

/* ==================== 体积 / 速率 ==================== */

/**
 * 磁盘/镜像大小格式化，**入参单位是 GB**（后端 images.size_gb 这类已折算好的字段）。
 * @param {number|string} gb 大小，单位 GB
 * @returns {string} 保留两位小数的纯数字串（不带单位，列头已标注 GB）；非法值返回 '0.00'
 */
export function fmtSizeGB(gb) {
  const n = Number(gb)
  if (!isFinite(n)) return '0.00'
  return n.toFixed(2)
}

/**
 * 容量格式化，**入参单位是字节**（libvirt 存储池 / 卷的 capacity、allocation、available）。
 * @param {number} bytes 容量，单位 Byte
 * @returns {string} ≥1024GB 自动进位到 TB，形如 '512.0 GB' / '1.5 TB'；空值返回 '—'
 */
export function fmtSizeBytes(bytes) {
  if (bytes === null || bytes === undefined || bytes === '') return '—'
  const gb = bytes / 1024 / 1024 / 1024
  return gb >= 1024 ? (gb / 1024).toFixed(1) + ' TB' : gb.toFixed(1) + ' GB'
}

/**
 * 吞吐格式化，**入参单位是字节/秒**（VM 实时统计的 disk_read_bps、net_rx_bps 等）。
 * @param {number} bps 速率，单位 Byte/s
 * @returns {string} 自动进位的速率串，形如 '0 B/s' / '1.2 MB/s'
 */
export function fmtRateBytes(bps) {
  if (!bps || bps <= 0) return '0 B/s'
  const units = ['B/s', 'KB/s', 'MB/s', 'GB/s']
  let n = bps
  let i = 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return n.toFixed(i === 0 ? 0 : 1) + ' ' + units[i]
}

/* ==================== 错误 / 取消 ==================== */

/**
 * 从 axios 错误里提取后端中文消息。
 * 后端统一响应是 `{code, message, data}`，错误文案固定在 message（handler 层禁止再泄漏 detail）。
 * @param {any} e catch 到的异常
 * @param {string} fallback 取不到 message 时的兜底中文（各调用点语义不同，务必按场景传）
 * @returns {string} 展示给用户的中文文案
 *
 * 说明：任务型接口（提交 202 + 轮询）请用 utils/task.js 的 taskErrorMessage —
 * 它在本函数逻辑之上额外识别 pollTask 抛出的 Error（任务 error 字段）。
 */
export function errMsg(e, fallback = '操作失败') {
  return (e && e.response && e.response.data && e.response.data.message) || fallback
}

/**
 * 判断异常是否为「用户主动取消确认框」。
 * ElMessageBox 点「取消」reject 'cancel'，点右上角 X / 按 ESC reject 'close'；
 * 被包成 Error 时取 message。两者都不该弹错误提示。
 */
export function isCancel(e) {
  return e === 'cancel' || e === 'close' || e?.message === 'cancel' || e?.message === 'close'
}

/* ==================== 配色 ==================== */

/**
 * 读取 CSS 变量的**真实计算值**。
 * 用于 echarts / canvas / conic-gradient 这类不解析 `var()` 的场景；
 * 普通 style 绑定直接写 `var(--x)` 即可，无需本函数。
 * @param {string} name 变量名，含 `--` 前缀
 * @param {string} fallback 变量未定义时的兜底色值
 */
export function cssVar(name, fallback = '') {
  try {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback
  } catch (e) {
    return fallback
  }
}

/**
 * 使用率阈值配色：<60% 绿 / 60~80% 橙 / ≥80% 红。
 * @param {number|string} percent 百分比数值（0~100，非数字按 0 处理）
 * @returns {string} CSS 变量形式的颜色，可直接给 el-progress 的 color 或 style
 */
export function usageColor(percent) {
  const n = Number(percent) || 0
  if (n >= 80) return 'var(--color-danger)'
  if (n >= 60) return 'var(--color-warning)'
  return 'var(--color-success)'
}

/**
 * 百分比钳制到 0~100 的整数（进度条 percentage 只接受该范围，越界会告警）。
 */
export function clampPct(percent) {
  return Math.max(0, Math.min(100, Math.round(Number(percent) || 0)))
}
