// DOM 契约巡检 E2E：在真实数据下逐页检查「本不该同时出现」的组合。
//
// 这是空态 bug（有数据却显示「暂无」）的通用化。静态扫 v-if 只能查「指令在不在」，
// 查不了「条件是否被外层正确包住」——2026-10-18 空态那批教训：lint 全绿、E2E 全绿，
// 用户一截图才发现。本用例把这类「反向断言」扩展到更多模式。
//
// 运行：
//   ADMIN_PASS=... npm run e2e -- --grep @dom-contract
import { test, expect } from '@playwright/test'
import { ACCOUNTS } from './helpers/env.js'
import { login } from './helpers/auth.js'

const PASS = ACCOUNTS.admin.pass

test.describe('DOM 契约巡检 @dom-contract', () => {
  test.skip(!PASS, '未提供 ADMIN_PASS')
  test.setTimeout(300_000)

  test('逐页检查互斥元素不同时出现', async ({ page }) => {
    const jsErrors = []
    page.on('pageerror', (e) => jsErrors.push(String(e)))

    await login(page)

    // [页面, 数据标志, 互斥的「无数据」文案]
    // semantics: 有数据标志时，「无数据」文案必须不出现（空态 bug模式）
    const checks = [
      { name: '虚拟机列表', hash: '#/vms', dataSel: '.vm-card', emptyText: '无匹配虚拟机' },
      { name: '容器页', hash: '#/containers', dataSel: '.ct-card', emptyText: '暂无容器' },
      { name: '任务中心', hash: '#/tasks', dataSel: '.el-table__row', emptyText: '暂无任务' },
      { name: '用户管理', hash: '#/users', dataSel: '.el-table__row', emptyText: '暂无用户' },
      { name: '镜像列表', hash: '#/images', dataSel: '.el-table__row, .el-card', emptyText: '暂无镜像' },
      { name: '审计中心', hash: '#/audit', dataSel: '.el-table__row', emptyText: '暂无审计记录' },
    ]

    const results = []
    for (const c of checks) {
      await page.goto('/' + c.hash)
      await page.waitForTimeout(1500)

      const dataCount = await page.locator(`.el-main ${c.dataSel}`).count()
      const emptyVisible = await page
        .locator('.el-main .el-empty')
        .filter({ hasText: new RegExp(`^${c.emptyText}$`) })
        .count()

      results.push(`${c.name}: 数据=${dataCount} 空态=${emptyVisible}`)
      if (dataCount > 0) {
        expect(emptyVisible, `${c.name} 有 ${dataCount} 条数据却显示「${c.emptyText}」`).toBe(0)
      }
    }

    // loading 态不得与「有数据」并存残留（v-loading 遮罩不消失 = 请求悬挂）
    await page.goto('/#/vms')
    await page.waitForTimeout(2000)
    const spinner = await page.locator('.el-main .el-loading-mask').count()
    results.push(`虚拟机列表 loading 遮罩残留=${spinner}`)

    console.log('DOM 契约巡检：')
    for (const r of results) console.log('  ' + r)
    expect(jsErrors, `页面 JS 异常：${jsErrors.join('\n')}`).toHaveLength(0)
  })

  test('抽屉/弹窗的 v-if 条件生效（关闭后 DOM 必须消失）', async ({ page }) => {
    const jsErrors = []
    page.on('pageerror', (e) => jsErrors.push(String(e)))
    await login(page)

    await page.goto('/#/tasks')
    await page.waitForTimeout(1200)

    const rows = page.locator('.el-main .el-table__row')
    test.skip(!(await rows.count()), '没有任务数据')
    await rows.first().click()
    // 等抽屉标题渲染出来再断言（点行后有 300ms 过渡动画，直接断言根节点会踩在动画中）
    await expect(page.getByText(/任务详情 #\d+/)).toBeVisible()
    const visibleDrawer = page.locator('.el-drawer:visible')
    await expect(visibleDrawer).toHaveCount(1)

    // 关掉后必须从可见 DOM 消失（v-model 绑定，关闭即卸载）
    await page.locator('.el-drawer:visible .el-drawer__close-btn').first().click()
    await expect(page.locator('.el-drawer:visible')).toHaveCount(0, { timeout: 5000 })

    expect(jsErrors, `JS 异常：${jsErrors.join('\n')}`).toHaveLength(0)
  })
})
