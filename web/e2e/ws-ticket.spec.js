// WebSocket 一次性票据负例（L5）：未授权、伪造、跨资源、一次性、过期、任务日志流。
//
// 运行：
//   ADMIN_PASS=... npm run e2e -- --grep @ws-ticket
//
// 票据语义（service/wsticket）：
// - 30s 有效、一经消费即删除、绑定资源（vm:<id> / docker:<id> / task:<id>）
// - 浏览器 WS 无法带 Authorization 头，只能 ?ticket=；本组用例直接建 WS 验证。
import { test, expect } from '@playwright/test'
import { ACCOUNTS } from './helpers/env.js'
import { login } from './helpers/auth.js'
import { api, listVMs } from './helpers/api.js'

const PASS = ACCOUNTS.admin.pass
const WS_BASE = (process.env.BASE || 'http://127.0.0.1:8080').replace(/^http/, 'ws')

/** 在页面里建一条 WS，返回首帧/关闭/错误结果 */
async function wsProbe(page, url) {
  return page.evaluate(
    (u) =>
      new Promise((resolve) => {
        let done = false
        const finish = (r) => {
          if (done) return
          done = true
          try {
            ws.close()
          } catch (_) {
            /* ignore */
          }
          resolve(r)
        }
        const ws = new WebSocket(u)
        ws.onopen = () => setTimeout(() => finish('open'), 600)
        ws.onmessage = (e) => finish('message:' + String(e.data).slice(0, 120))
        ws.onerror = () => finish('error')
        ws.onclose = (e) => finish('close:' + e.code)
        setTimeout(() => finish('timeout'), 6000)
      }),
    url
  )
}

async function issue(page, path) {
  const r = await api(page, 'POST', path)
  return r.data?.ticket || ''
}

test.describe('WS 票据 @ws-ticket', () => {
  test.skip(!PASS, '未提供 ADMIN_PASS')

  test('无凭证 / 伪造票据一律 401', async ({ page }) => {
    await login(page)
    const vms = await listVMs(page)
    test.skip(!vms.length, '平台没有 VM')

    const noAuth = await wsProbe(page, `${WS_BASE}/api/vms/${vms[0].id}/serial`)
    expect(noAuth, '无凭证应握手失败').toMatch(/error|close:1006/)

    const forged = await wsProbe(page, `${WS_BASE}/api/vms/${vms[0].id}/serial?ticket=deadbeef`)
    expect(forged, '伪造票据应握手失败').toMatch(/error|close:1006/)
  })

  test('票据绑定资源：docker 票打 VM 终端被拒', async ({ page }) => {
    await login(page)
    const vms = await listVMs(page)
    const ctr = await api(page, 'GET', '/api/docker/containers')
    const container = (ctr.data?.items || [])[0]
    test.skip(!vms.length || !container, '缺少 VM 或容器')

    const dockerTicket = await issue(page, `/api/docker/containers/${container.ID}/ws-ticket`)
    expect(dockerTicket, 'docker 取票失败').not.toBe('')

    const r = await wsProbe(page, `${WS_BASE}/api/vms/${vms[0].id}/serial?ticket=${dockerTicket}`)
    expect(r, '跨资源票据必须被拒').toMatch(/error|close:1006/)
  })

  test('票据一次性：同一张票第二次连接被拒', async ({ page }) => {
    await login(page)
    const vms = await listVMs(page)
    test.skip(!vms.length, '平台没有 VM')
    const id = vms[0].id

    const ticket = await issue(page, `/api/vms/${id}/ws-ticket`)
    expect(ticket).not.toBe('')

    // 第一次：认证应通过（业务上串口可能不可用，但握手/首帧会到达）
    const first = await wsProbe(page, `${WS_BASE}/api/vms/${id}/serial?ticket=${ticket}`)
    expect(first, '第一次连接不应是 401').not.toMatch(/error|close:1006/)

    // 第二次：票据已被消费
    const second = await wsProbe(page, `${WS_BASE}/api/vms/${id}/serial?ticket=${ticket}`)
    expect(second, '票据复用必须被拒').toMatch(/error|close:1006/)
  })

  test('任务日志流：票据可用且一次性', async ({ page }) => {
    await login(page)
    const tasks = await api(page, 'GET', '/api/tasks?page_size=1')
    const task = (tasks.data?.items || [])[0]
    test.skip(!task, '平台没有任务记录')

    const ticket = await issue(page, `/api/tasks/${task.id}/ws-ticket`)
    expect(ticket).not.toBe('')

    const first = await wsProbe(page, `${WS_BASE}/api/tasks/${task.id}/logs/ws?since=0&ticket=${ticket}`)
    expect(first, '任务日志流首次连接应成功').toMatch(/open|message:/)

    const second = await wsProbe(page, `${WS_BASE}/api/tasks/${task.id}/logs/ws?since=0&ticket=${ticket}`)
    expect(second, '任务票据复用必须被拒').toMatch(/error|close:1006/)
  })

  test('票据过期（31s 后）被拒', async ({ page }) => {
    test.setTimeout(60_000)
    await login(page)
    const vms = await listVMs(page)
    test.skip(!vms.length, '平台没有 VM')

    const ticket = await issue(page, `/api/vms/${vms[0].id}/ws-ticket`)
    expect(ticket).not.toBe('')
    await page.waitForTimeout(31_000)

    const r = await wsProbe(page, `${WS_BASE}/api/vms/${vms[0].id}/serial?ticket=${ticket}`)
    expect(r, '过期票据必须被拒').toMatch(/error|close:1006/)
  })
})
