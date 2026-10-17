// 容器链路 E2E（L5）：创建容器 → 运行 → 停止 → 删除。
//
// 运行：
//   ADMIN_PASS=... E2E_ALLOW_DESTRUCTIVE=1 npm run e2e -- --grep @docker
//
// 用 alpine + `sleep 30` 做 fixture：启动快、资源占用极低、不监听端口。
// 只操作 e2e- 前缀容器；失败时用 API 强制删除兜底。
import { test, expect } from '@playwright/test'
import { ACCOUNTS, ALLOW_DESTRUCTIVE, e2eName } from './helpers/env.js'
import { login } from './helpers/auth.js'
import { collectPageErrors } from './helpers/ui.js'
import { api } from './helpers/api.js'

const PASS = ACCOUNTS.admin.pass

async function findContainer(page, name) {
  const r = await api(page, 'GET', '/api/docker/containers')
  const items = (r.data && r.data.items) || []
  return items.find((c) => (c.Names || '').split(',').some((n) => n.replace(/^\//, '') === name)) || null
}

test.describe('容器 @docker', () => {
  test.skip(!PASS, '未提供 ADMIN_PASS')
  test.skip(!ALLOW_DESTRUCTIVE, '未开启 E2E_ALLOW_DESTRUCTIVE')
  test.setTimeout(180_000)

  test('创建 → 运行 → 停止 → 删除', async ({ page }) => {
    await login(page)
    const errors = collectPageErrors(page)
    const name = e2eName('ctr')

    // Docker 不可用时跳过（页面会显示不可用提示）
    const probe = await api(page, 'GET', '/api/docker/containers')
    test.skip(probe.status !== 200, 'Docker 不可用')

    try {
      await page.goto('/#/containers')
      await page.getByRole('button', { name: '创建容器' }).click()

      await page.getByPlaceholder('如 my-nginx，字母数字开头，可含 _ . -').fill(name)
      // Element Plus 的 select 占位符是 span，不是 input placeholder，按表单项标签定位
      await page.locator('.el-form-item', { hasText: '镜像' }).locator('.el-select').click({ timeout: 15_000 })
      const imageOpt = page.locator('.el-select-dropdown:visible .el-select-dropdown__item', { hasText: 'alpine:3.24' }).first()
      if (await imageOpt.count()) {
        await imageOpt.click()
      } else {
        // 没有 alpine 时退回第一个可用镜像
        await page.locator('.el-select-dropdown:visible .el-select-dropdown__item').first().click()
      }
      await page.getByPlaceholder('留空使用镜像默认 ENTRYPOINT').fill('sleep 30')
      await page.locator('.el-drawer').getByRole('button', { name: '创建' }).click()
      await expect(page.locator('.el-message').last()).toContainText(/创建|成功/, { timeout: 60_000 })

      const card = page.locator('.ct-card', { hasText: name }).first()
      await expect(card).toBeVisible({ timeout: 60_000 })

      // 停止（运行中才显示停止按钮）
      const stopBtn = card.getByRole('button', { name: '停止', exact: true })
      if (await stopBtn.count()) {
        await stopBtn.click()
        await expect(page.locator('.el-message').last()).toContainText(/已停止/, { timeout: 60_000 })
      }

      // 删除（卡片上的删除按钮是 icon-only，用类名定位）
      await card.locator('.ct-card-del').click()
      await page.locator('.el-message-box').getByRole('button', { name: '删除' }).click()
      await expect(page.locator('.el-message').last()).toContainText(/已删除/, { timeout: 60_000 })

      expect(errors, `容器操作出现 JS 异常：\n${errors.join('\n')}`).toHaveLength(0)
    } finally {
      if (!page.isClosed()) {
        const leftover = await findContainer(page, name).catch(() => null)
        if (leftover) {
          await api(page, 'DELETE', `/api/docker/containers/${leftover.ID}?force=true`).catch(() => {})
        }
      }
    }
  })
})
