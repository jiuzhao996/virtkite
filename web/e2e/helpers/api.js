// 测试数据准备/清理用的接口客户端。
//
// 为什么用接口而不是全走 UI：
// - 前置数据（存储池、网络、镜像）用 UI 造太慢且脆；
// - 清理必须可靠（UI 删除失败会留垃圾）。
// 但**被验收的行为本身仍然走 UI 点击**——接口只负责「摆好场景」和「收尾」。
import { TOKEN_KEY } from './auth.js'
import { isE2EName } from './env.js'

// 删除类请求必经 api()，守卫放在这一层才无法被绕过：
// 逐用例各写一遍判断早晚会漂移（实测 24 个删除点，isE2EName 此前零调用，
// 文档承诺的「删除前统一过守卫」等于落空）。
const DELETE_METHODS = new Set(['DELETE', 'delete'])

// 按 URL 取出「要删除的那个资源名」，供前缀守卫判定。
// 各域形态：
//   /api/vms/123            → 走 id，跳过（见下）
//   /api/vms-recycle/123/purge
//   /api/networks/<name>
//   /api/volumes/<name>
//   /api/docker/volumes/<name>
//   /api/docker/containers/<id>?force=true
//   /api/ansible/playbooks/<id>
//   /api/designer/plans/<id>
//   /api/tasks/<id>
// 数值型主键（VM/任务/容器 ID）无法从 URL 判断归属，由传入的 guard 参数把关。
function urlResourceName(path) {
  const clean = String(path).split('?')[0]
  const segs = clean.split('/').filter(Boolean)
  return segs.length ? decodeURIComponent(segs[segs.length - 1]) : ''
}

/**
 * 统一请求：自动带 token，返回 {status, code, message, data}
 *
 * @param {object} page Playwright page
 * @param {string} method HTTP 方法
 * @param {string} path 路径
 * @param {*} [data] 请求体
 * @param {{guard?: string}} [opts]
 *   guard：删除操作要保护的资源名，必须是 e2e 前缀；
 *   非 e2e 前缀直接抛错，请求根本不发（防误删用户真实资源）。
 */
export async function api(page, method, path, data, opts = {}) {
  if (DELETE_METHODS.has(method)) {
    const guard = opts.guard ?? urlResourceName(path)
    // 数值主键无法判定归属时，调用方必须显式传 guard；否则拦不住「删 id=113 的真机」
    if (guard === '' || /^\d+$/.test(guard)) {
      throw new Error(
        `E2E 安全守卫：DELETE ${path} 无法确定资源归属（guard=${guard || '空'}），` +
          '请显式传 { guard: <e2e 资源名> }'
      )
    }
    if (!isE2EName(guard)) {
      throw new Error(`E2E 安全守卫：拒绝删除非 e2e 资源 ${guard}（${method} ${path}）`)
    }
  }

  const token = await page.evaluate((k) => localStorage.getItem(k) || '', TOKEN_KEY)
  const res = await page.request.fetch(path, {
    method,
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    data: data === undefined ? undefined : data,
    failOnStatusCode: false,
  })
  const text = await res.text()
  let json = {}
  try {
    json = text ? JSON.parse(text) : {}
  } catch {
    json = { raw: text }
  }
  return { status: res.status(), ...json }
}

export async function listVMs(page) {
  const r = await api(page, 'GET', '/api/vms')
  return (r.data && r.data.items) || []
}

export async function getVMOptions(page) {
  const r = await api(page, 'GET', '/api/vms/options')
  return r.data || {}
}

export async function getTask(page, taskId) {
  const r = await api(page, 'GET', `/api/tasks/${taskId}`)
  return r.data || {}
}

/** VM 详情（data 即 VM 对象） */
export async function getVM(page, id) {
  const r = await api(page, 'GET', `/api/vms/${id}`)
  return r.data || null
}

/** VM spec（{vm, spec}）；spec.autostart / disks / interfaces 用于断言 */
export async function getVMSpec(page, id) {
  const r = await api(page, 'GET', `/api/vms/${id}/spec`)
  return r.data || {}
}

/** 轮询任务直到终态；success 返回任务对象，failed/超时抛错 */
export async function waitTask(page, taskId, { timeout = 300000, interval = 1500 } = {}) {
  const deadline = Date.now() + timeout
  for (;;) {
    const t = await getTask(page, taskId)
    if (t.status === 'success') return t
    if (t.status === 'failed') throw new Error(`任务失败：${t.error || t.message || '未知原因'}`)
    if (Date.now() > deadline) throw new Error(`任务超时：${taskId}`)
    await page.waitForTimeout(interval)
  }
}

/** 按名称找 VM */
export async function findVMByName(page, name) {
  const vms = await listVMs(page)
  return vms.find((v) => v.name === name) || null
}
