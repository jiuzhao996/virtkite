// 任务中心 E2E（L5）：状态机、取消、删除前置、日志、参数校验。
//
// 运行：
//   ADMIN_PASS=... E2E_ALLOW_DESTRUCTIVE=1 npm run e2e -- --grep @task-center
//
// 取消用例用「云镜像 + cloud-init 自动初始化」造一个长任务（等 IP 窗口 3 分钟），
// 稳定命中 running 态；结束后清理半成品 VM。
import { test, expect } from '@playwright/test'
import { ACCOUNTS, ALLOW_DESTRUCTIVE, e2eName } from './helpers/env.js'
import { login } from './helpers/auth.js'
import { api, findVMByName, getTask, getVMOptions, waitTask } from './helpers/api.js'

const PASS = ACCOUNTS.admin.pass

async function submitLongTask(page) {
  const options = await getVMOptions(page)
  const image = (options.cloud_images || []).find((i) => !String(i.format || '').includes('iso'))
  if (!image) return null
  const name = e2eName('task')
  const res = await api(page, 'POST', '/api/vms', {
    name,
    storage_pool: options.default_storage_pool || (options.storage_pools?.[0]?.name ?? ''),
    vcpu: 1,
    memory_mb: 1024,
    disks: [{ source_image_id: image.id, create_gb: 10 }],
    network: (options.networks || [])[0] || 'default',
    cloud_init: { hostname: name, user: 'root', password: `E2e-${name}-a1` },
    auto_provision: true,
  })
  if (!res.data?.task_id) throw new Error(`建机提交失败：${res.message || ''}`)
  return { name, taskId: res.data.task_id }
}

test.describe('任务中心 @task-center', () => {
  test.skip(!PASS, '未提供 ADMIN_PASS')
  test.skip(!ALLOW_DESTRUCTIVE, '未开启 E2E_ALLOW_DESTRUCTIVE')
  test.setTimeout(300_000)

  test('提交 → running → 取消 → 终态 cancelled；日志可读', async ({ page }) => {
    await login(page)
    const fx = await submitLongTask(page)
    test.skip(!fx, '没有云镜像，无法造长任务')
    const { name, taskId } = fx

    try {
      // 等任务进入 running（创建卷 + 开机 + 等 IP 窗口足够长）
      await expect
        .poll(async () => (await getTask(page, taskId)).status, { timeout: 120_000, intervals: [1000] })
        .toBe('running')

      const cancel = await api(page, 'POST', `/api/tasks/${taskId}/cancel`)
      expect(cancel.status, `取消失败：${cancel.message || ''}`).toBe(200)

      await expect
        .poll(async () => (await getTask(page, taskId)).status, { timeout: 60_000, intervals: [1000] })
        .toBe('cancelled')

      // 日志：结构化 entries 或老任务回落 text
      const logs = await api(page, 'GET', `/api/tasks/${taskId}/logs`)
      expect(logs.status).toBe(200)
      expect(Array.isArray(logs.data?.entries) || typeof logs.data?.text === 'string').toBe(true)

      // 已取消任务不可再次取消
      const again = await api(page, 'POST', `/api/tasks/${taskId}/cancel`)
      expect(again.status, '非 running 任务取消应 409').toBe(409)

      // cancelled 是终态，应可删除（回归：此前后端只认 success/failed，
      // 而 UI 把 cancelled 当可删终态，取消过的任务永远清不掉）
      const del = await api(page, 'DELETE', `/api/tasks/${taskId}`)
      expect(del.status, 'cancelled 任务应可删除').toBe(200)
      expect((await api(page, 'GET', `/api/tasks/${taskId}`)).status).toBe(404)
    } finally {
      // 清理半成品 VM（取消发生在 provision 阶段，VM 可能已创建）
      const leftover = await findVMByName(page, name)
      if (leftover) {
        const del = await api(page, 'DELETE', `/api/vms/${leftover.id}`)
        if (del.data?.task_id) await waitTask(page, del.data.task_id, { timeout: 180_000 }).catch(() => {})
        await api(page, 'DELETE', `/api/vms-recycle/${leftover.id}/purge?purge_volumes=true`).catch(() => {})
      }
    }
  })

  test('删除/查询参数校验', async ({ page }) => {
    await login(page)

    // 非法 ID：400（不落到 404）
    expect((await api(page, 'GET', '/api/tasks/abc')).status).toBe(400)
    // 不存在：404
    expect((await api(page, 'GET', '/api/tasks/99999999')).status).toBe(404)

    // 删除一个非终态任务 → 409（找一个 running 的；没有则跳过）
    const list = await api(page, 'GET', '/api/tasks?status=running&page_size=1')
    const running = (list.data?.items || [])[0]
    if (running) {
      const del = await api(page, 'DELETE', `/api/tasks/${running.id}`)
      expect(del.status, '运行中任务删除应 409').toBe(409)
    }

    // 删除终态任务：只删本套 e2e 产生的（标题含 e2e-），不碰用户历史
    const done = await api(page, 'GET', '/api/tasks?status=success&page_size=50')
    const t = (done.data?.items || []).find((x) => String(x.title || '').includes('e2e-'))
    if (t) {
      const del = await api(page, 'DELETE', `/api/tasks/${t.id}`)
      expect([200, 409]).toContain(del.status) // 409 = 已被并发清理
    }
  })
})
