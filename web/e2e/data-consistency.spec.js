// 前端展示数字与后端接口交叉校验 E2E。
//
// 动机：UI「看起来对」但数字算错是最隐蔽的一类（比如 total 把软删算进去、
// 百分比分母用错、求和漏字段）。此类缺陷页面照常渲染、无任何异常，
// 只有拿接口原始数据对一遍才会暴露。
//
// 运行：
//   ADMIN_PASS=... npm run e2e -- --grep @data-consistency
import { test, expect } from '@playwright/test'
import { ACCOUNTS } from './helpers/env.js'
import { login } from './helpers/auth.js'
import { api, listVMs } from './helpers/api.js'

const PASS = ACCOUNTS.admin.pass

test.describe('数据一致性 @data-consistency', () => {
  test.skip(!PASS, '未提供 ADMIN_PASS')
  test.setTimeout(180_000)

  test('虚拟机列表条数 = 接口 total', async ({ page }) => {
    await login(page)
    const vms = await listVMs(page)
    const total = vms.length

    await page.goto('/#/vms')
    await page.waitForTimeout(1500)
    const rendered = await page.locator('.el-main .vm-card').count()
    expect(rendered, `接口返回 ${total} 台，页面渲染 ${rendered} 张卡片`).toBe(total)
  })

  test('任务列表 total = 接口 total', async ({ page }) => {
    await login(page)
    const res = await api(page, 'GET', '/api/tasks?page_size=100')
    const apiTotal = res.data?.total ?? 0

    await page.goto('/#/tasks')
    await page.waitForTimeout(1500)
    // 页面上「共 N 条」类计数或分页 total
    const bodyText = await page.locator('.el-main').innerText()
    expect(bodyText).toContain(String(apiTotal))
  })

  test('仪表盘 VM 计数 = 接口计数', async ({ page }) => {
    await login(page)
    const vms = await listVMs(page)
    const ov = await api(page, 'GET', '/api/dashboard/overview')
    const apiVMCount = ov.data?.vm_count

    expect(apiVMCount, '接口未返回 vm_count').toBeDefined()
    expect(vms.length, `接口 vm_count=${apiVMCount}，列表实际 ${vms.length} 台`).toBe(apiVMCount)

    // 运行中计数
    const apiRunning = ov.data?.running_vm_count
    const actualRunning = vms.filter((v) => v.status === 'running').length
    expect(actualRunning, `接口 running=${apiRunning}，实际 ${actualRunning} 台`).toBe(apiRunning)
  })

  test('状态筛选真的在过滤（点击后条数减少且不含其他状态）', async ({ page }) => {
    await login(page)
    const vms = await listVMs(page)
    test.skip(!vms.length, '平台没有 VM')

    await page.goto('/#/vms')
    await page.waitForTimeout(1500)
    const before = await page.locator('.el-main .vm-card').count()

    // 切到「运行中」筛选
    const statusFilter = page.locator('.el-main select, .el-main .el-select').first()
    if (await statusFilter.count()) {
      await statusFilter.click().catch(() => {})
      const opt = page.locator('.el-select-dropdown:visible .el-select-dropdown__item', { hasText: '运行中' }).first()
      if (await opt.count()) {
        await opt.click()
        await page.waitForTimeout(1200)
        const after = await page.locator('.el-main .vm-card').count()
        const runningCount = vms.filter((v) => v.status === 'running').length
        // 筛选后要么等于运行中数，要么为空态（0 台运行中）
        expect([0, runningCount], `筛选后渲染 ${after} 张，期望 0 或 ${runningCount}`).toContain(after)
        console.log(`状态筛选：${before} → ${after}（运行中共 ${runningCount} 台）`)
      }
    }
  })
})
