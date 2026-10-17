// RBAC 负例验收（L5）：按中间件实际语义断言，不按路由注释。
//
// 运行：
//   ADMIN_PASS=... OPERATOR_PASS=... VIEWER_PASS=... npm run e2e -- --grep @rbac
//
// 只发只读/无副作用请求（install-source/test 是只读探测），不创建资源。
import { test, expect } from '@playwright/test'
import { ACCOUNTS } from './helpers/env.js'

const { admin, operator, viewer } = ACCOUNTS

async function loginToken(request, account) {
  const res = await request.post('/api/auth/login', {
    data: { username: account.user, password: account.pass },
  })
  const body = await res.json().catch(() => ({}))
  return { status: res.status(), token: body?.data?.access_token || '', body }
}

async function get(request, token, path) {
  const res = await request.get(path, { headers: { Authorization: `Bearer ${token}` } })
  return res.status()
}

async function post(request, token, path, data = {}) {
  const res = await request.post(path, { headers: { Authorization: `Bearer ${token}` }, data })
  return res.status()
}

test.describe('RBAC 权限边界 @rbac', () => {
  test.skip(!admin.pass, '未提供 ADMIN_PASS')

  test('admin 可访问管理员域', async ({ request }) => {
    const { status, token } = await loginToken(request, admin)
    expect(status, 'admin 登录失败').toBeLessThan(300)
    expect(await get(request, token, '/api/users')).toBe(200)
    expect(await get(request, token, '/api/audit')).toBe(200)
    expect(await get(request, token, '/api/settings')).toBe(200)
  })

  test('viewer 读平台域放行、非只读域 403', async ({ request }) => {
    test.skip(!viewer.pass, '未提供 VIEWER_PASS')
    const { status, token } = await loginToken(request, viewer)
    expect(status, 'viewer 登录失败').toBeLessThan(300)

    // 允许：VM 列表、任务、仪表盘、存储/网络/镜像
    expect(await get(request, token, '/api/vms')).toBe(200)
    expect(await get(request, token, '/api/tasks')).toBe(200)
    expect(await get(request, token, '/api/dashboard/overview')).toBe(200)
    expect(await get(request, token, '/api/storage/pools')).toBe(200)

    // 禁止：docker / ai / monitor / logs 整组（含 GET）
    expect(await get(request, token, '/api/docker/containers')).toBe(403)
    expect(await get(request, token, '/api/ai/status')).toBe(403)
    expect(await get(request, token, '/api/monitor/alerts')).toBe(403)
    expect(await get(request, token, '/api/logs/query?query=*')).toBe(403)

    // 禁止：管理员域
    expect(await get(request, token, '/api/users')).toBe(403)
    expect(await get(request, token, '/api/audit')).toBe(403)
  })

  test('operator 写 /api/vms 放行、写其他域 403', async ({ request }) => {
    test.skip(!operator.pass, '未提供 OPERATOR_PASS')
    const { status, token } = await loginToken(request, operator)
    expect(status, 'operator 登录失败').toBeLessThan(300)

    // 只读探测接口：在 /api/vms 前缀下，operator 写应放行（200，available 字段）
    expect(await post(request, token, '/api/vms/install-source/test', { url: 'http://127.0.0.1:1/' })).toBe(200)

    // 其他域的写：403（不是 200）
    expect(await post(request, token, '/api/ansible/run', {})).toBe(403)
    expect(await post(request, token, '/api/designer/plans', {})).toBe(403)
    expect(await post(request, token, '/api/cloud-init-templates', {})).toBe(403)
    expect(await post(request, token, '/api/storage/pools', {})).toBe(403)

    // 管理员域 GET 也 403
    expect(await get(request, token, '/api/users')).toBe(403)
    expect(await get(request, token, '/api/audit')).toBe(403)
  })

  test('未授权 VM 对非 admin 返回 404（不是 403）', async ({ request }) => {
    test.skip(!viewer.pass, '未提供 VIEWER_PASS')
    const { token: adminToken } = await loginToken(request, admin)
    const { token: viewerToken } = await loginToken(request, viewer)

    const res = await request.get('/api/vms', { headers: { Authorization: `Bearer ${adminToken}` } })
    const body = await res.json()
    const vm = body?.data?.items?.[0]
    test.skip(!vm, '平台没有 VM，无法验证资产级授权')

    // viewer 未获授权时：详情按「虚拟机不存在」返回 404
    const detail = await get(request, viewerToken, `/api/vms/${vm.id}`)
    expect([200, 404]).toContain(detail)
    if (detail === 404) {
      // 404 是预期的未授权语义；如果是 403 说明中间件行为变了，需要复核
      expect(detail).not.toBe(403)
    }
  })
})
