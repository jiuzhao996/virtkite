// Tab 深链 + lazy 加载专项 E2E。
//
// 覆盖两类易腐化点：
//   1) 深链：?tab=x 直达某 tab（原 ui-smoke 只测了 9 条，且当时踩过
//      「同路由只改 query 不重挂组件」的坑——组件若只在 mounted 读一次
//      route.query，从别的路由跳进来不刷新就失效）
//   2) lazy 加载：非默认 tab 首次点击才渲染，断言点击后内容真的出现
//
// 运行：
//   ADMIN_PASS=... npm run e2e -- --grep @tab-deeplink
import { test, expect } from '@playwright/test'
import { ACCOUNTS } from './helpers/env.js'
import { login } from './helpers/auth.js'

const PASS = ACCOUNTS.admin.pass

// [页面基础路径, 默认 tab 名, [深链 tab, 该 tab 必须出现的标志文案]]
// 深链一律「先绕到别的路由再进」——同路由内只改 query 不重挂组件，
// 那样测不出「组件只在 mounted 读一次 query」的缺陷。
const TABS = [
  {
    page: '#/dashboard', def: '概览',
    links: [
      ['monitor', '告警'],
      ['topology', '拓扑'],
    ],
  },
  {
    page: '#/images', def: '云镜像',
    links: [
      ['iso', 'ISO'],
      ['build', '模板'],
      ['docker', '容器镜像'],
      ['market', '镜像市场'],
    ],
  },
  {
    page: '#/containers', def: '容器',
    links: [
      ['compose', '编排'],
    ],
  },
  {
    page: '#/storage', def: '拓扑图',
    links: [
      ['pools', '存储池'],
      ['docker', 'Docker 卷'],
    ],
  },
  {
    page: '#/networks', def: '拓扑图',
    links: [
      ['ipam', 'IP 分配'],
      ['flow', '通信流量'],
      ['libvirt', '虚拟网络'],
      ['docker', 'Docker 网络'],
    ],
  },
  {
    page: '#/automation', def: '快速执行',
    links: [
      ['playbooks', 'Playbook'],
      ['roles', 'Role'],
      ['history', '执行历史'],
      ['cron', '计划任务'],
    ],
  },
  {
    page: '#/audit', def: '操作日志',
    links: [
      ['sessions', '会话'],
    ],
  },
]

test.describe('Tab 深链 @tab-deeplink', () => {
  test.skip(!PASS, '未提供 ADMIN_PASS')
  test.setTimeout(600_000)

  test('所有 tab 深链直达生效', async ({ page }) => {
    const errors = []
    page.on('pageerror', (e) => errors.push(String(e)))
    await login(page)

    const checked = []
    for (const group of TABS) {
      for (const [tab, marker] of group.links) {
        // 先绕到登录页/仪表盘，再进目标 URL：强制组件重新挂载
        await page.goto('/#/dashboard')
        await page.goto(`/${group.page}?tab=${tab}`)
        await page.waitForTimeout(1600)

        // 1) URL 保住了 query（未被重置为默认 tab）
        expect(page.url(), `${group.page}?tab=${tab} 的 query 被重置`).toContain(`tab=${tab}`)

        // 2) 活动 tab 不得仍是默认 tab（默认 tab 说明 query 没被采纳）
        const active = await page.evaluate(() => {
          const el = document.querySelector('.el-main .el-tabs__item.is-active')
          return el ? el.textContent.trim() : ''
        })
        expect(active, `${group.page}?tab=${tab} 仍停留在默认 tab「${active}」`).not.toBe(group.def)

        // 3) 内容真的渲染了（lazy tab 首次加载；标志文案而非 tab key——显示的是中文）
        const body = await page.locator('.el-main').innerText()
        expect(body, `${group.page}?tab=${tab} 内容未渲染（标志词「${marker}」缺失）`).toContain(marker)

        checked.push(`${group.page}?tab=${tab} → 「${active}」`)
      }
    }

    console.log(`Tab 深链：${checked.length} 条全部生效`)
    for (const c of checked) console.log('  ' + c)
    expect(errors, `页面 JS 异常：\n${errors.join('\n')}`).toHaveLength(0)
  })

  test('lazy tab 点击后内容渲染（非空即空态，不能白屏）', async ({ page }) => {
    await login(page)

    // 抽查三个有 lazy tab 的页面：点过去后内容区必须有文字。
    // 注意：不能用 test.skip() 做循环内守卫——它是声明式的，条件在第一次迭代后
    // 就固化了（实测第一个 tab 找不到会把整个用例判为 skip，后面两条根本没跑）。
    // 改用 console 记录 + continue，缺 tab 只跳过该条。
    for (const [hash, tab, marker] of [
      ['#/networks', 'IP 分配', 'IP'],
      ['#/images', '容器镜像', '镜像'],
      ['#/users', '用户组', '组'],
    ]) {
      await page.goto('/' + hash)
      const t = page.locator('.el-main .el-tabs__item', { hasText: tab }).first()
      if (!(await t.count())) {
        console.log(`跳过 ${hash}：无「${tab}」tab`)
        continue
      }
      await t.click()
      await page.waitForTimeout(1400)
      const body = await page.locator('.el-main').innerText()
      expect(body.trim().length, `${hash} 的「${tab}」tab 内容区为空`).toBeGreaterThan(10)
      console.log(`${hash}「${tab}」渲染 ${body.trim().length} 字`)
    }
  })
})
