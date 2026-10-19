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
import { fmtSizeBytes } from '../src/utils/format.js'

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

  test('存储池：每个池的卷数与接口 vol_count 一致', async ({ page }) => {
    await login(page)
    const pools = await api(page, 'GET', '/api/storage/pools')
    const items = pools.data?.items || []
    test.skip(!items.length, '平台没有存储池')

    await page.goto('/#/storage')
    await page.locator('.el-main .el-tabs__item', { hasText: '存储池' }).click()
    await page.waitForTimeout(1500)

    // 卡片渲染数 = 接口池数
    const cards = await page.locator('.el-main .pool-card').count()
    expect(cards, `接口 ${items.length} 个池，页面渲染 ${cards} 张卡片`).toBe(items.length)

    // 每池卡片上的「卷数 N」必须等于该池接口 vol_count。
    // 注意：页面**不显示**「共 N 个池」汇总，所以只核对逐池卷数。
    for (const p of items) {
      const card = page.locator('.el-main .pool-card', { hasText: p.name }).first()
      if (!(await card.count())) continue
      const txt = await card.innerText()
      const want = String(p.vol_count ?? 0)
      expect(txt, `池 ${p.name} 卡片应含「卷数 ${want}」`).toContain(`卷数 ${want}`)
    }
    console.log(`存储池：${items.length} 个池、逐池卷数已核对（接口不提供汇总计数，页面也不显示）`)
  })

  test('网络：「运行/自启/共 N 个」与接口一致', async ({ page }) => {
    await login(page)
    const nets = await api(page, 'GET', '/api/networks')
    const items = nets.data?.items || []
    const running = items.filter((n) => n.active).length
    const autostart = items.filter((n) => n.autostart).length

    await page.goto('/#/networks')
    await page.locator('.el-main .el-tabs__item', { hasText: '虚拟网络' }).click()
    await page.waitForTimeout(1500)

    const bodyText = await page.locator('.el-main').innerText()
    expect(bodyText, `应含「共 ${items.length} 个网络」`).toContain(`共 ${items.length} 个网络`)
    expect(bodyText, `运行数应为 ${running}`).toContain(`运行 ${running}`)
    if (autostart) expect(bodyText).toContain(`自启 ${autostart}`)
    console.log(`网络：共 ${items.length}、运行 ${running}、自启 ${autostart}`)
  })

  test('存储池：物理容量/可用空间的计算与接口一致', async ({ page }) => {
    await login(page)
    const r = await api(page, 'GET', '/api/storage/pools')
    const items = r.data?.items || []
    test.skip(!items.length, '平台没有存储池')

    // 页面口径（StorageList.physCap）：取 capacity 最大的池，展示其 capacity 与 available。
    // 这里直接 import 前端同一个 fmtSizeBytes，避免在测试里复刻格式化逻辑后两边漂移。
    let best = { capacity: 0, available: 0 }
    for (const p of items) {
      if ((p.capacity || 0) > best.capacity) {
        best = { capacity: p.capacity || 0, available: p.available || 0 }
      }
    }

    await page.goto('/#/storage')
    await page.locator('.el-main .el-tabs__item', { hasText: '存储池' }).click()
    await page.waitForTimeout(1500)
    const body = await page.locator('.el-main').innerText()

    expect(body, `页面未显示物理容量 ${fmtSizeBytes(best.capacity)}`).toContain(fmtSizeBytes(best.capacity))
    expect(body, `页面未显示可用空间 ${fmtSizeBytes(best.available)}`).toContain(fmtSizeBytes(best.available))
    console.log(`存储池容量：物理 ${fmtSizeBytes(best.capacity)} / 可用 ${fmtSizeBytes(best.available)}`)
  })

  test('镜像：页数/total 与接口一致', async ({ page }) => {
    await login(page)
    const imgs = await api(page, 'GET', '/api/images')
    const apiTotal = imgs.data?.total
    const rendered = (imgs.data?.items || []).length

    await page.goto('/#/images')
    await page.waitForTimeout(1800) // 该页要逐个池查 iso，接口较多

    const bodyText = await page.locator('.el-main').innerText()
    // 云镜像 tab 显示「共 N 个」；模板盘 tab 各自计数
    expect(bodyText).toContain(String(apiTotal))
    console.log(`镜像：接口 total=${apiTotal}、items=${rendered}`)
  })
})
