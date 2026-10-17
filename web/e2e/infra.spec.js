// 基础设施 E2E（L5）：存储卷 CRUD、虚拟网络 CRUD。
//
// 运行：
//   ADMIN_PASS=... E2E_ALLOW_DESTRUCTIVE=1 npm run e2e -- --grep @infra
//
// 只创建/删除 e2e- 前缀资源；失败时用 API 兜底清理。
import { test, expect } from '@playwright/test'
import { ACCOUNTS, ALLOW_DESTRUCTIVE, e2eName } from './helpers/env.js'
import { login } from './helpers/auth.js'
import { collectPageErrors } from './helpers/ui.js'
import { api, getVMOptions } from './helpers/api.js'

const PASS = ACCOUNTS.admin.pass

test.describe('基础设施 @infra', () => {
  test.skip(!PASS, '未提供 ADMIN_PASS')
  test.skip(!ALLOW_DESTRUCTIVE, '未开启 E2E_ALLOW_DESTRUCTIVE')
  test.setTimeout(180_000)

  test('存储卷：新建 → 列表可见 → 删除', async ({ page }) => {
    await login(page)
    const errors = collectPageErrors(page)
    const options = await getVMOptions(page)
    const pool = options.default_storage_pool || options.storage_pools?.[0]?.name
    test.skip(!pool, '没有可用存储池')

    const volName = e2eName('vol') + '.qcow2'
    try {
      await page.goto('/#/storage')
      await page.locator('.el-tabs__item', { hasText: '存储池' }).click()

      // 打开目标池的卷抽屉
      const card = page.locator('.pool-card', { hasText: pool }).first()
      await expect(card).toBeVisible()
      await card.getByRole('button', { name: '浏览卷' }).click()

      await page.getByRole('button', { name: '新建卷' }).click()
      await page.getByPlaceholder('如 vm-disk1.qcow2').fill(volName)
      await page.locator('.el-dialog').getByRole('button', { name: '创建' }).click()
      await expect(page.locator('.el-message').last()).toContainText('卷已创建', { timeout: 30_000 })

      const row = page.locator('.el-table__row', { hasText: volName })
      await expect(row).toBeVisible({ timeout: 30_000 })

      await row.getByRole('button', { name: '删除' }).click()
      await page.locator('.el-message-box').getByRole('button', { name: '删除' }).click()
      await expect(row).toHaveCount(0, { timeout: 30_000 })

      expect(errors, `存储操作出现 JS 异常：\n${errors.join('\n')}`).toHaveLength(0)
    } finally {
      // 兜底：卷若残留则用 API 删除（仅 e2e 前缀）
      if (volName.startsWith('e2e-')) {
        await api(page, 'DELETE', `/api/storage/pools/${encodeURIComponent(pool)}/volumes/${encodeURIComponent(volName)}`).catch(() => {})
      }
    }
  })

  test('虚拟网络：新建（隔离）→ 卡片可见 → 删除', async ({ page }) => {
    await login(page)
    const errors = collectPageErrors(page)
    const netName = e2eName('net')
    try {
      await page.goto('/#/networks')
      await page.locator('.el-tabs__item', { hasText: '虚拟网络' }).click()

      await page.getByRole('button', { name: '新建网络' }).click()
      await page.getByPlaceholder('仅字母、数字、_、-').fill(netName)
      // 隔离网络不需要网关/网桥，避免占用宿主网段
      await page.locator('.el-radio-button', { hasText: '隔离' }).click()
      await page.locator('.el-dialog').getByRole('button', { name: '创建' }).click()
      await expect(page.locator('.el-message').last()).toContainText(/创建|成功/, { timeout: 30_000 })

      const card = page.locator('.net-card', { hasText: netName }).first()
      await expect(card).toBeVisible({ timeout: 30_000 })

      await card.getByRole('button', { name: '删除' }).click()
      await page.locator('.el-message-box').getByRole('button', { name: '删除' }).click()
      // 卡片本身有「点击打开详情」的冒泡行为，删除后可能残留抽屉；用接口断言更稳
      await page.keyboard.press('Escape').catch(() => {})
      await expect
        .poll(
          async () => {
            const r = await api(page, 'GET', '/api/networks')
            return (r.data?.items || []).some((n) => n.name === netName)
          },
          { timeout: 30_000, intervals: [1000] }
        )
        .toBe(false)

      expect(errors, `网络操作出现 JS 异常：\n${errors.join('\n')}`).toHaveLength(0)
    } finally {
      if (netName.startsWith('e2e-')) {
        await api(page, 'DELETE', `/api/networks/${encodeURIComponent(netName)}`).catch(() => {})
      }
    }
  })
})
