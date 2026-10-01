// Docker 展示格式化纯函数（原 DockerList.vue 内迁出，ConsolePage 等页面可复用）。
// 只做形态兼容与中文化，不发请求、不持状态。
import { fmtDateTime, fmtSizeBytes } from './format'

// containerName 容器名数组/字符串归一
export function containerName(names) {
  if (Array.isArray(names)) return names.join(', ') || '—'
  return names || '—'
}

// stateTag 容器状态 → tag 颜色：running 绿 / exited 灰 / 其他橙
export function stateTag(state) {
  if (state === 'running') return 'success'
  if (state === 'exited') return 'info'
  return 'warning'
}

// Docker 容器状态 → 中文（docker ps 的 State 全取值集）；未知状态原样返回便于暴露新取值
export const DOCKER_STATE_TEXT = {
  running: '运行中',
  exited: '已退出',
  paused: '已暂停',
  created: '已创建',
  restarting: '重启中',
  removing: '删除中',
  dead: '死亡'
}

export function stateText(state) {
  return DOCKER_STATE_TEXT[state] || state || '未知'
}

// composeTag compose 项目状态 → tag 颜色
export function composeTag(status) {
  const s = String(status || '')
  if (s.indexOf('running') === 0) return 'success'
  if (s.indexOf('exited') === 0 || s.indexOf('stopped') === 0) return 'info'
  return 'warning'
}

// portsText Ports 兼容两类后端形态：docker SDK 对象数组或已拼好的字符串（本项目后端为字符串）
export function portsText(ports) {
  if (!ports || (Array.isArray(ports) && ports.length === 0)) return '—'
  if (Array.isArray(ports)) {
    return ports
      .map((p) => {
        const host = p.PublicPort ? `${p.IP || ''}:${p.PublicPort}->` : ''
        return `${host}${p.PrivatePort}/${p.Type || 'tcp'}`
      })
      .join('  ')
  }
  return String(ports)
}

// shortId ID 截短 12 位（docker 惯例）
export function shortId(id) {
  return id ? String(id).replace(/^sha256:/, '').slice(0, 12) : '—'
}

// dockerTime Created 兼容 unix 秒级时间戳与「3 days ago」这类相对时间串；解析不了原样展示
export function dockerTime(v) {
  if (v === null || v === undefined || v === '') return '—'
  if (typeof v === 'number') return fmtDateTime(v * (v > 1e12 ? 1 : 1000))
  const d = new Date(v)
  if (!isNaN(d.getTime()) && /\d{4}/.test(String(v))) return fmtDateTime(d)
  return String(v)
}

// relativeTimeZh docker 相对时间串（「3 days ago」「About an hour ago」）→ 中文；不匹配返回空串
const REL_TIME_ZH_UNITS = { second: '秒', minute: '分钟', hour: '小时', day: '天', week: '周', month: '个月', year: '年' }

export function relativeTimeZh(v) {
  const s = String(v).trim()
  let m = s.match(/^(\d+)\s*(second|minute|hour|day|week|month|year)s?\s+ago$/i)
  if (m) return `${m[1]} ${REL_TIME_ZH_UNITS[m[2].toLowerCase()]}前`
  m = s.match(/^about an? (second|minute|hour|day)\s+ago$/i)
  if (m) return `约 1 ${REL_TIME_ZH_UNITS[m[1].toLowerCase()]}前`
  return ''
}

// imageTime 镜像创建时间：Created 为 unix 秒时间戳时换算本地化时间；
// 「3 days ago」相对时间串映射中文（docker images 的 CreatedSince 形态）
export function imageTime(v) {
  if (v === null || v === undefined || v === '') return '—'
  if (typeof v === 'number') return fmtDateTime(new Date(v > 1e12 ? v : v * 1000))
  const rel = relativeTimeZh(v)
  if (rel) return rel
  const d = new Date(v)
  if (!isNaN(d.getTime()) && /\d{4}/.test(String(v))) return fmtDateTime(d)
  return String(v)
}

// dockerSize Size 兼容字节（数字）与「1.2GB」（字符串）两种形态
export function dockerSize(v) {
  if (v === null || v === undefined || v === '') return '—'
  if (typeof v === 'number') return fmtSizeBytes(v)
  return String(v)
}
