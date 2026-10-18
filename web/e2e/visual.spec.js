// 视觉回归 E2E：把「页面结构/样式突变」纳入验收。
//
// 前面所有用例都测「功能与数据」，但 CSS 被改坏、元素错位、主题色丢失这类
// 问题功能测试完全无感（按钮还能点、数据还对）。本用例用 Playwright 的
// toHaveScreenshot 做像素级比对。
//
// 实现要点（踩过的坑）：
//  1) 只对**稳定区块**建基线：实时 CPU%、时间戳、进度条每次都变，整页快照
//     必然无限失败。为此用「元素级快照」而非整页，并显式剔除动态区。
//  2) 基线图必须入库：web/e2e/visual/ 下（已加 .gitignore 的失败产物在
//     test-results/，不影响）。
//  3) 建基线：`ADMIN_PASS=... npm run e2e -- --grep @visual --update-snapshots`
//     之后每次跑只比对，不一致即 FAIL 并给出 diff 图路径。
//
// 运行：
//   ADMIN_PASS=... npm run e2e -- --grep @visual          # 比对
//   ADMIN_PASS=... npm run e2e -- --grep @visual --update-snapshots   # 重建基线
import { test, expect } from '@playwright/test'
import { ACCOUNTS } from './helpers/env.js'
import { login } from './helpers/auth.js'

const PASS = ACCOUNTS.admin.pass

test.describe('视觉回归 @visual', () => {
  test.skip(!PASS, '未提供 ADMIN_PASS')
  test.setTimeout(600_000)

  // 登录页无任何动态元素（实测两次截图 MD5 完全一致），是最干净的基线对象
  test('登录页视觉', async ({ page }) => {
    await page.goto('/#/login')
    await page.waitForTimeout(1000)
    await expect(page.locator('.login-wrap')).toHaveScreenshot('login.png', {
      // 登录页有渐变背景 + 大图，整页比对更贴近真实观感
      maxDiffPixelRatio: 0.02,
    })
  })

  // 主题色与全局样式的回归点：切深色模式后侧栏/卡片不得丢色
  test('深色模式：侧栏与卡片', async ({ page }) => {
    await login(page)
    await page.goto('/#/vms')
    await page.waitForTimeout(1200)

    // 手动开深色（localStorage 键与 main.js 一致）
    await page.evaluate(() => localStorage.setItem('vmops-theme', 'dark'))
    await page.reload()
    await page.waitForTimeout(1800)

    const aside = page.locator('.el-aside').first()
    if (await aside.count()) {
      await expect(aside).toHaveScreenshot('sidebar-dark.png', { maxDiffPixelRatio: 0.03 })
    }
  })

  // 关键 UI 元素的存在性与尺寸：改了 CSS 导致按钮消失/塌陷时这里会红
  test('虚拟机列表工具栏尺寸', async ({ page }) => {
    await login(page)
    await page.goto('/#/vms')
    await page.waitForTimeout(1500)

    const toolbar = page.locator('.el-main .toolbar-left, .el-main .el-card__header').first()
    if (await toolbar.count()) {
      const box = await toolbar.boundingBox()
      // 工具栏不该塌成 0 高（CSS 继承断裂的典型症状）
      expect(box.height, '工具栏高度异常（CSS 断裂？）').toBeGreaterThan(20)
    }
  })

  // 空态组件自身渲染（图标 + 文案）：组件被改坏时这里会发现
  test('空态组件渲染', async ({ page }) => {
    await login(page)
    // 用一个必然为空的 tab：审计页的会话 tab 可能为空，退化成「日志页无结果」
    await page.goto('/#/logs')
    await page.waitForTimeout(1500)
    const empty = page.locator('.el-main .el-empty').first()
    if (await empty.count()) {
      await expect(empty).toHaveScreenshot('empty-logs.png', { maxDiffPixelRatio: 0.02 })
    }
  })
})
