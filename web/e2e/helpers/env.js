// E2E 运行环境与安全命名约定。
//
// 所有测试资源必须带 `e2e-<runId>-` 前缀；任何删除/修改动作前先过 isE2EName()，
// 防止自动化误伤用户真实资源（这是本套用例的第一条纪律）。
const now = new Date()
const pad = (n) => String(n).padStart(2, '0')
export const RUN_ID =
  process.env.E2E_RUN_ID ||
  `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`

/** 生成带本轮运行前缀的资源名（长度控制在 40 字符内，避免撞后端长度限制） */
export function e2eName(prefix) {
  const p = String(prefix || 'res')
    .replace(/[^a-zA-Z0-9-]/g, '-')
    .slice(0, 16)
  return `e2e-${RUN_ID}-${p}`.slice(0, 40)
}

/** 资源名是否由本轮/本套 E2E 创建（删除前必须为 true） */
export function isE2EName(name) {
  return typeof name === 'string' && name.startsWith('e2e-')
}

/** 是否允许执行破坏性用例（批量删除/清空/改系统设置） */
export const ALLOW_DESTRUCTIVE = process.env.E2E_ALLOW_DESTRUCTIVE === '1'

/** 统一读取账号口令；缺失时返回空串，由用例自行 skip */
export const ACCOUNTS = {
  admin: { user: process.env.ADMIN_USER || 'admin', pass: process.env.ADMIN_PASS || '' },
  operator: { user: process.env.OPERATOR_USER || 'operator', pass: process.env.OPERATOR_PASS || '' },
  viewer: { user: process.env.VIEWER_USER || 'viewer', pass: process.env.VIEWER_PASS || '' },
}
