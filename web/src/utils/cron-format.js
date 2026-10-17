// 计划任务展示格式化（从 views/CronList.vue 抽出）。
//
// 搬过来的是纯函数：状态→文案/tag、链式提示、死开关提示、最近执行摘要、
// 参数列中文摘要。它们与页面状态无关，抽走后可直接单测——
// 原实现在 696 行的页面里，改一处文案要靠点开浏览器肉眼看。
import { fmtDateTime } from './format'

/** 任务动作 → 中文 */
export function cronActionText(a) {
  return {
    vm_snapshot: '虚拟机快照',
    db_backup: '数据库备份',
    ansible_playbook: 'Playbook 执行',
    container_healthcheck: '容器健康巡检',
    image_version_check: '镜像版本巡检',
    designer_drift_check: '架构漂移巡检',
  }[a] || a
}

/** 任务动作 → el-tag 颜色 */
export function cronActionTag(a) {
  return {
    vm_snapshot: 'primary',
    db_backup: 'success',
    ansible_playbook: 'warning',
    container_healthcheck: 'info',
    image_version_check: 'info',
    designer_drift_check: 'info',
  }[a] || 'info'
}

/** 执行状态 → 中文（success 绿 / failed 红 / running 蓝 / skipped 灰） */
export function cronRunStatusText(s) {
  return { success: '成功', failed: '失败', running: '执行中', skipped: '跳过' }[s] || s || '未知'
}

/** 执行状态 → el-tag 颜色 */
export function cronRunStatusTag(s) {
  return { success: 'success', failed: 'danger', running: 'primary', skipped: 'warning' }[s] || 'info'
}

/**
 * 「链」列提示：解析成功/失败下一跳任务名。
 * @param {object} row 计划任务行（含 on_success_task_id / on_failure_task_id）
 * @param {Array} items 同页任务列表（用于把 id 翻成名；找不到回落 #id）
 */
export function cronChainTip(row, items) {
  const nameOf = (id) => {
    const t = (items || []).find((x) => x.id === id)
    return t ? t.name : `#${id}`
  }
  const parts = []
  if (row.on_success_task_id) parts.push('成功 → ' + nameOf(row.on_success_task_id))
  if (row.on_failure_task_id) parts.push('失败 → ' + nameOf(row.on_failure_task_id))
  return parts.join('；')
}

/** 死开关提示：预期时刻 + 处置建议（调度器可能没在跑） */
export function cronStaleTip(row) {
  const due = row.stale_due ? fmtDateTime(row.stale_due) : '预期时刻'
  return `预期 ${due} 执行，已过宽限期仍无执行记录——请检查调度器是否在运行`
}

/** 最近一次执行记录（recent_runs 倒序，首条最新） */
export function cronLatestRun(row) {
  return (row.recent_runs && row.recent_runs[0]) || null
}

/** 最近执行记录的 tooltip 摘要（输出过长截断，避免 tooltip 撑爆屏幕） */
export function cronLatestRunTip(row) {
  const r = cronLatestRun(row)
  if (!r) return ''
  const out = r.output
    ? '；结果：' + (r.output.length > 80 ? r.output.slice(0, 80) + '…' : r.output)
    : ''
  return `时间：${fmtDateTime(r.started_at)}${out}`
}

/**
 * 参数列：JSON 字符串解析后给中文摘要（vm_id 尽量翻译成虚拟机名）。
 * @param {object} row 任务行（params 为 JSON 字符串）
 * @param {Array} vms 虚拟机列表（id → name 翻译用）
 */
export function cronParamsText(row, vms) {
  let obj = null
  try {
    obj = JSON.parse(row.params || '{}')
  } catch (_e) {
    // 不是 JSON（历史脏数据）：原样展示，不猜
    return row.params || '—'
  }
  if (!obj || typeof obj !== 'object') return String(row.params)
  if (obj.vm_id != null) {
    const vm = (vms || []).find((v) => v.id === obj.vm_id)
    return vm ? `虚拟机：${vm.name}` : `vm_id: ${obj.vm_id}`
  }
  if (obj.playbook) {
    const names = (obj.targets || []).map((id) => (vms || []).find((v) => v.id === id)?.name || `#${id}`)
    return `playbook：${obj.playbook} → ${names.length ? names.join('、') : '未选目标'}`
  }
  const keys = Object.keys(obj)
  return keys.length === 0 ? '（无参数）' : JSON.stringify(obj)
}
