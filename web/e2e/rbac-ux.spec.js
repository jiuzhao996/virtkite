// Q2/Q3 前端语义验收（用户 2026-10-18 拍板：两者都走「UI 明示」而非改后端）。
//
//   Q2：回收站恢复——资产授权/托管凭据/主机组成员随删除已物理清理，后端恢复只回记录。
//       不在 UI 上讲清，用户会以为恢复完就能直接免密登录（实际不能）。
//   Q3：自动化写操作后端只对 admin 放行（OperatorMiddleware 只许 operator 写 /api/vms），
//       operator 进得来却每步 403；任务读接口对三角色均放行（路由注释已论证非越权）。
//
// 运行：
//   ADMIN_PASS=... OPERATOR_PASS=... VIEWER_PASS=... npm run e2e -- --grep @rbac-ux
import { test, expect } from '@playwright/test'
import { ACCOUNTS } from './helpers/env.js'

const { admin, operator, viewer } = ACCOUNTS

test.describe('RBAC 前端语义 @rbac-ux', () => {
  test.skip(!admin.pass, '未提供 ADMIN_PASS')
  test.skip(!operator.pass, '未提供 OPERATOR_PASS')

  test('Q3：operator 看自动化页是只读视图（写入口隐藏 + 有说明）', async ({ page }) => {
    const errors = []
    page.on('pageerror', (e) => errors.push(String(e)))

    // 用 operator 登录：走 UI 表单（账号口令由环境变量给）
    await page.goto('/#/login')
    await page.locator('input[autocomplete="username"]').fill(operator.user)
    await page.locator('input[autocomplete="current-password"]').fill(operator.pass)
    await page.locator('.submit-btn').click()
    await expect.poll(() => page.evaluate(() => !!localStorage.getItem('vmops_token'))).toBe(true)

    await page.goto('/#/automation')
    await expect(page.getByRole('heading', { name: '运维自动化' })).toBeVisible()

    const main = page.locator('.el-main')
    // 只读说明在位（快速执行 tab 被替换成说明卡片）
    await expect(main.getByText('批量执行为管理员能力', { exact: false }).first()).toBeVisible()
    await expect(main.getByText('自动化执行为管理员能力', { exact: false }).first()).toBeVisible()
    // 写入口隐藏（默认 tab 的执行按钮）
    await expect(main.getByRole('button', { name: '执 行' })).toHaveCount(0)
    // lazy tab 里也不能有写入口（切过去再验，避免「没渲染所以不存在」的假通过）
    await main.getByRole('tab', { name: /Playbook 库/ }).click()
    await expect(main.getByRole('button', { name: '新建 Playbook' })).toHaveCount(0)
    await expect(main.getByRole('button', { name: '分发公钥到未免密 VM' })).toHaveCount(0)
    await main.getByRole('tab', { name: /Role 库/ }).click()
    await expect(main.getByRole('button', { name: '新建 Role' })).toHaveCount(0)
    // 读入口仍在（只读视图不是白屏）
    await expect(main.getByRole('tab', { name: /Role 库/ })).toBeVisible()

    expect(errors, `页面异常：${errors.join('\n')}`).toHaveLength(0)
  })

  test('Q3：admin 看自动化页写入口齐全', async ({ page }) => {
    await page.goto('/#/login')
    await page.locator('input[autocomplete="username"]').fill(admin.user)
    await page.locator('input[autocomplete="current-password"]').fill(admin.pass)
    await page.locator('.submit-btn').click()
    await expect.poll(() => page.evaluate(() => !!localStorage.getItem('vmops_token'))).toBe(true)

    await page.goto('/#/automation')
    // 快速执行在默认 tab；Playbook/Role 是 lazy tab，要先点过去才渲染
    await expect(page.locator('.el-main').getByRole('button', { name: '执 行' })).toBeVisible()
    await page.locator('.el-main').getByRole('tab', { name: /Playbook 库/ }).click()
    await expect(page.locator('.el-main').getByRole('button', { name: '新建 Playbook' })).toBeVisible()
    await page.locator('.el-main').getByRole('tab', { name: /Role 库/ }).click()
    await expect(page.locator('.el-main').getByRole('button', { name: '新建 Role' })).toBeVisible()
  })

  test('Q2：回收站页头说明「恢复不还原授权/凭据」', async ({ page }) => {
    await page.goto('/#/login')
    await page.locator('input[autocomplete="username"]').fill(admin.user)
    await page.locator('input[autocomplete="current-password"]').fill(admin.pass)
    await page.locator('.submit-btn').click()
    await expect.poll(() => page.evaluate(() => !!localStorage.getItem('vmops_token'))).toBe(true)

    await page.goto('/#/recycle-bin')
    await expect(page.locator('.el-main').getByText('资产授权、托管凭据、主机组成员随删除一并清除，恢复不会还原')).toBeVisible()
  })

  test('Q3：viewer 看任务中心有可见性说明', async ({ page }) => {
    test.skip(!viewer.pass, '未提供 VIEWER_PASS')
    await page.goto('/#/login')
    await page.locator('input[autocomplete="username"]').fill(viewer.user)
    await page.locator('input[autocomplete="current-password"]').fill(viewer.pass)
    await page.locator('.submit-btn').click()
    await expect.poll(() => page.evaluate(() => !!localStorage.getItem('vmops_token'))).toBe(true)

    await page.goto('/#/tasks')
    await expect(page.locator('.el-main .el-alert').filter({ hasText: '任务记录对所有登录角色可见' })).toBeVisible()
  })
})
