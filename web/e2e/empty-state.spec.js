// 空态展示契约 E2E：有数据时绝不能出现「暂无 X」。
//
// 背景（2026-10-18 用户截图抓到）：空态组件机械替换时丢了 v-if，
// 导致容器页「有 6 个容器却显示暂无容器」与卡片同时渲染。
// 5 处丢失的 v-if 里只有带 v-else 的会被 lint 发现，其余是**静默 bug**——
// 本用例就是为堵这个盲区：逐页断言「页面有数据 ⇏ 空态可见」。
//
// 运行：
//   ADMIN_PASS=... npm run e2e -- --grep @empty-state
import { test, expect } from '@playwright/test'
import { ACCOUNTS } from './helpers/env.js'

const PASS = ACCOUNTS.admin.pass

// 每页的「数据标志元素」与「空态文案」。
// 数据标志必须是列表渲染后一定出现的容器（卡片/表格行），空态文案取页面实际文案。
const PAGES = [
  { hash: '#/containers', listSel: '.ct-card', emptyText: '暂无容器' },
  { hash: '#/containers?tab=compose', listSel: '.cp-card, .el-table__row', emptyText: '暂无 compose 编排项目' },
  { hash: '#/storage', listSel: '.pool-card, .el-table__row', emptyText: '该池暂无存储卷' },
  { hash: '#/networks', listSel: '.net-card, .el-table__row', emptyText: '暂无网络' },
  { hash: '#/images', listSel: '.el-table__row, .el-card', emptyText: '暂无镜像' },
  { hash: '#/vms', listSel: '.vm-card', emptyText: '无匹配虚拟机' },
  { hash: '#/tasks', listSel: '.el-table__row', emptyText: '暂无任务' },
  { hash: '#/users', listSel: '.el-table__row', emptyText: '暂无用户' },
]

test.describe('空态契约 @empty-state', () => {
  test.skip(!PASS, '未提供 ADMIN_PASS')

  test('有数据的页面不得渲染空态', async ({ page }) => {
    // 登录：走 helpers 以免重复表单代码
    const { login } = await import('./helpers/auth.js')
    await login(page)

    const checked = []
    for (const pg of PAGES) {
      await page.goto('/' + pg.hash)
      await page.waitForTimeout(1600) // 等列表接口落地

      const listCount = await page.locator(`.el-main ${pg.listSel}`).count()
      if (!listCount) {
        // 该页本来就没数据：跳过（空态可见是正确的，本用例不管）
        continue
      }
      const emptyCount = await page
        .locator('.el-main .el-empty')
        .filter({ hasText: new RegExp(`^${pg.emptyText}$`) })
        .count()
      checked.push(`${pg.hash} 数据=${listCount} 空态=${emptyCount}`)
      expect(
        emptyCount,
        `${pg.hash} 有 ${listCount} 条数据却渲染了「${pg.emptyText}」空态（v-if 丢了？）`
      ).toBe(0)
    }
    console.log(`空态契约：校验了 ${checked.length} 个有数据的页面`)
    for (const c of checked) console.log('  ' + c)
  })
})
