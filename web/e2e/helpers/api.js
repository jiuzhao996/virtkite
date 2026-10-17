// 测试数据准备/清理用的接口客户端。
//
// 为什么用接口而不是全走 UI：
// - 前置数据（存储池、网络、镜像）用 UI 造太慢且脆；
// - 清理必须可靠（UI 删除失败会留垃圾）。
// 但**被验收的行为本身仍然走 UI 点击**——接口只负责「摆好场景」和「收尾」。
import { TOKEN_KEY } from './auth.js'

/** 统一请求：自动带 token，返回 {status, code, message, data} */
export async function api(page, method, path, data) {
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

/** 删除 VM（仅 e2e 资源）；返回接口结果 */
export async function deleteVM(page, id) {
  return api(page, 'DELETE', `/api/vms/${id}`)
}

/** 按名称找 VM */
export async function findVMByName(page, name) {
  const vms = await listVMs(page)
  return vms.find((v) => v.name === name) || null
}

/** 存储池 / 网络 / 镜像的只读清单（前置检查用） */
export async function listPools(page) {
  const r = await api(page, 'GET', '/api/storage/pools')
  return (r.data && r.data.items) || []
}
export async function listNetworks(page) {
  const r = await api(page, 'GET', '/api/networks')
  return (r.data && r.data.items) || []
}
export async function listImages(page) {
  const r = await api(page, 'GET', '/api/images')
  return (r.data && (r.data.items || r.data)) || []
}
