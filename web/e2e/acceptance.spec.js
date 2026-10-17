// Playwright 冒烟验收（L4）：登录页、18 页遍历、全按钮点击、axe 可访问性。
//
// 运行：
//   ADMIN_PASS=<口令> npm run e2e -- --grep @smoke
//   npm run e2e -- --grep 登录页        # 不需要口令
//
// 业务链路（建机/生命周期/存储网络…）见同目录 vm-*.spec.js 与
// docs/testing/e2e-acceptance-plan.md。
import { test, expect } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'
import { ACCOUNTS } from './helpers/env.js'
import { login } from './helpers/auth.js'
import { collectPageErrors, clickSafeButtons } from './helpers/ui.js'

const PASS = ACCOUNTS.admin.pass

// 页面清单：hash → 该页必须出现的标志文案（改版时同步这里，找不到即失败）
const PAGES = [
  ['#/dashboard', '仪表盘'],
  ['#/vms', '虚拟机'],
  ['#/containers', '容器'],
  ['#/apps', '应用商店'],
  ['#/designer', '架构设计'],
  ['#/grant-requests', '资产申请'],
  ['#/recycle-bin', '回收站'],
  ['#/automation', '运维自动化'],
  ['#/hosts', '宿主机'],
  ['#/images', '镜像管理'],
  ['#/storage', '存储'],
  ['#/networks', '网络'],
  ['#/tasks', '任务中心'],
  ['#/audit', '审计中心'],
  ['#/logs', '日志'],
  ['#/profile', '个人中心'],
  ['#/users', '用户管理'],
  ['#/settings', '系统设置'],
]

const BTN_PAGES = [
  '#/vms',
  '#/containers',
  '#/apps',
  '#/storage',
  '#/networks',
  '#/images',
  '#/tasks',
  '#/audit',
  '#/users',
  '#/automation',
  '#/hosts',
  '#/settings',
  '#/logs',
]

/** 切到指定 hash 页并等首屏请求落地 */
async function gotoPage(page, hash) {
  await page.goto('/' + hash)
  await page.waitForTimeout(1200)
}

/** 输出 axe 违规摘要（门槛只卡 critical） */
function logViolations(violations, scope) {
  for (const v of violations) {
    const targets = v.nodes
      .map((n) => n.target.join(' '))
      .slice(0, 3)
      .join(' | ')
    console.log(`[a11y ${scope}] ${v.impact} ${v.id}: ${v.help}（${v.nodes.length} 处）→ ${targets}`)
  }
}

test.describe('登录页 @smoke', () => {
  test('渲染品牌、表单与标题', async ({ page }) => {
    await page.goto('/#/login')
    await expect(page).toHaveTitle(/鸢航 VirtKite/)
    await expect(page.locator('input[autocomplete="username"]')).toBeVisible()
    await expect(page.locator('input[autocomplete="current-password"]')).toBeVisible()
    await expect(page.locator('.submit-btn')).toBeVisible()
  })

  test('无 critical 可访问性缺陷（axe-core, WCAG A/AA）', async ({ page }) => {
    await page.goto('/#/login')
    await page.waitForTimeout(800)
    const { violations } = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
    logViolations(violations, 'login')
    const critical = violations.filter((v) => v.impact === 'critical')
    expect(critical, `critical 级可访问性问题：${critical.map((v) => v.id).join(', ')}`).toHaveLength(0)
  })
})

test.describe('登录后冒烟 @smoke', () => {
  test.skip(!PASS, '未提供 ADMIN_PASS，跳过登录后用例')

  test('遍历全部页面（无 JS 异常）', async ({ page }) => {
    const errors = collectPageErrors(page)
    await login(page)

    for (const [hash, marker] of PAGES) {
      await gotoPage(page, hash)
      const body = await page.locator('body').innerText()
      expect(body, `页面 ${hash} 未找到标志文案「${marker}」`).toContain(marker)
    }
    expect(errors, `页面出现 JS 异常：\n${errors.join('\n')}`).toHaveLength(0)
  })

  test('全按钮点击：打开类可点且无异常', async ({ page }) => {
    const errors = collectPageErrors(page)
    await login(page)

    let clicked = 0
    let opened = 0
    for (const hash of BTN_PAGES) {
      await gotoPage(page, hash)
      const r = await clickSafeButtons(page)
      clicked += r.clicked
      opened += r.opened
    }

    console.log(`按钮验收：点击 ${clicked} 个，其中 ${opened} 个出现弹层/消息`)
    expect(clicked, '未点到任何打开类按钮，选择器可能已失效').toBeGreaterThan(0)
    expect(errors, `点击过程中出现 JS 异常：\n${errors.join('\n')}`).toHaveLength(0)
  })

  test('核心页面无 critical 级可访问性问题', async ({ page }) => {
    await login(page)
    for (const hash of ['#/dashboard', '#/vms']) {
      await gotoPage(page, hash)
      const { violations } = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
      logViolations(violations, hash)
      const critical = violations.filter((v) => v.impact === 'critical')
      expect(critical, `${hash} 存在 critical 可访问性问题：${critical.map((v) => v.id).join(', ')}`).toHaveLength(0)
    }
  })
})
