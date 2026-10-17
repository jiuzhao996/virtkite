// Playwright 端到端验收（按钮/页面/可访问性）。
//
// 运行：
//   ADMIN_PASS=<口令> npm run e2e                 # 全量（登录 + 页面 + 按钮 + a11y）
//   npm run e2e -- --grep 登录页                  # 只跑登录页（不需要口令）
//   BASE=http://127.0.0.1:8080 ADMIN_PASS=... npm run e2e
//
// 设计原则与 scripts/ui-smoke.mjs 一致：
// - 危险按钮（删除/关机/重启/保存/执行…）一律不点，自动化误操作的代价太大；
// - 只点「打开类」按钮（新建/刷新/查看/编辑…），点开即关，零副作用；
// - 任何页面 JS 异常都算失败（pageerror），这是抓「按钮绑定写坏」最有效的手段。
import { test, expect } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'

const USER = process.env.ADMIN_USER || 'admin'
const PASS = process.env.ADMIN_PASS || ''
const TOKEN_KEY = 'vmops_token'

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

// 按钮白名单/黑名单：宁可漏点，不可误点
const BTN_OPEN = /新建|添加|上传|刷新|查看|详情|编辑|导入|导出|安装|脚本|配置|管理|调整|关联|绑定|登记|克隆|复制|预览|展开|筛选|重置|更多|设置/
const BTN_DANGER = /删除|清除|彻底|回收|重启|关机|停止|暂停|恢复|启动|开机|批量|保存|确定|提交|执行|运行|部署|应用|注销|退出|移除|摘除|下线|销毁|清空/

/** 登录并等待 token 落盘 + 路由离开登录页 */
async function login(page) {
  await page.goto('/#/login')
  await page.locator('input[autocomplete="username"]').fill(USER)
  await page.locator('input[autocomplete="current-password"]').fill(PASS)
  await page.locator('.submit-btn').click()
  await expect.poll(() => page.evaluate((k) => !!localStorage.getItem(k), TOKEN_KEY)).toBe(true)
  await expect(page).not.toHaveURL(/#\/login/)
}

/** 切到指定 hash 页并等首屏请求落地 */
async function gotoPage(page, hash) {
  await page.goto('/' + hash)
  await page.waitForTimeout(1200)
}

/** 收集页面级 JS 异常（不收集 console.error：网络类噪音会掩盖真问题） */
function collectPageErrors(page) {
  const errors = []
  page.on('pageerror', (e) => errors.push(String(e && e.message ? e.message : e)))
  return errors
}

test.describe('登录页（无需口令）', () => {
  test('渲染品牌、表单与标题', async ({ page }) => {
    await page.goto('/#/login')
    await expect(page).toHaveTitle(/鸢航 VirtKite/)
    await expect(page.locator('input[autocomplete="username"]')).toBeVisible()
    await expect(page.locator('input[autocomplete="current-password"]')).toBeVisible()
    await expect(page.locator('.submit-btn')).toBeVisible()
  })

  test('无严重可访问性缺陷（axe-core, WCAG A/AA）', async ({ page }) => {
    await page.goto('/#/login')
    await page.waitForTimeout(800)
    const { violations } = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
    const critical = violations.filter((v) => v.impact === 'critical')
    // 打印全部违规摘要与命中节点，便于人工判断；门槛只卡 critical
    for (const v of violations) {
      const targets = v.nodes.map((n) => n.target.join(' ')).slice(0, 3).join(' | ')
      console.log(`[a11y] ${v.impact} ${v.id}: ${v.help}（${v.nodes.length} 处）→ ${targets}`)
    }
    expect(critical, `critical 级可访问性问题：${critical.map((v) => v.id).join(', ')}`).toHaveLength(0)
  })
})

test.describe('登录后验收', () => {
  test.skip(!PASS, '未提供 ADMIN_PASS，跳过登录后用例')

  test('登录并遍历全部页面（无 JS 异常）', async ({ page }) => {
    const errors = collectPageErrors(page)
    await login(page)

    for (const [hash, marker] of PAGES) {
      await gotoPage(page, hash)
      const body = await page.locator('body').innerText()
      expect(body, `页面 ${hash} 未找到标志文案「${marker}」`).toContain(marker)
    }
    expect(errors, `页面出现 JS 异常：\n${errors.join('\n')}`).toHaveLength(0)
  })

  test('按钮验收：打开类按钮可点且无异常', async ({ page }) => {
    const errors = collectPageErrors(page)
    await login(page)

    const BTN_PAGES = ['#/vms', '#/containers', '#/apps', '#/storage', '#/networks', '#/images', '#/tasks', '#/audit', '#/users', '#/automation', '#/hosts', '#/settings', '#/logs']
    let clicked = 0
    let opened = 0

    for (const hash of BTN_PAGES) {
      await gotoPage(page, hash)
      // 先取一份可见按钮文案快照，再逐个按文本精确点击（页面重渲染后旧句柄会失效）
      const labels = await page.locator('.el-main .el-button:visible').evaluateAll((els) =>
        els
          .filter((b) => !b.disabled && !b.classList.contains('is-disabled'))
          .map((b) => (b.innerText || '').trim().replace(/\s+/g, ' ').slice(0, 16))
          .filter(Boolean)
      )

      for (const label of labels) {
        if (BTN_DANGER.test(label) || !BTN_OPEN.test(label)) continue
        const btn = page.locator('.el-main .el-button:visible', { hasText: label }).first()
        if (!(await btn.count())) continue
        await btn.click({ timeout: 3000 }).catch(() => {})
        clicked++
        await page.waitForTimeout(500)
        const openedNow = await page.evaluate(() => {
          const visible = (sel) =>
            Array.from(document.querySelectorAll(sel)).some((el) => el.offsetParent !== null && !el.className.includes('display: none'))
          return visible('.el-dialog') || visible('.el-drawer') || !!document.querySelector('.el-message')
        })
        if (openedNow) opened++

        // 关闭弹层/消息，避免影响后续点击
        await page.evaluate(() => {
          document.querySelector('.el-dialog__headerbtn')?.click()
          document.querySelector('.el-drawer__close-btn')?.click()
          document.querySelector('.el-message__closeBtn')?.click()
        })
        await page.keyboard.press('Escape').catch(() => {})
        await page.waitForTimeout(250)
      }
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
      const critical = violations.filter((v) => v.impact === 'critical')
      for (const v of violations) console.log(`[a11y ${hash}] ${v.impact} ${v.id}: ${v.help}`)
      expect(critical, `${hash} 存在 critical 可访问性问题：${critical.map((v) => v.id).join(', ')}`).toHaveLength(0)
    }
  })
})
