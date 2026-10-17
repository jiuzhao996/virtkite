// 回收站 E2E（L5）：软删 → 恢复（域已 undefine 的重建分支）→ 再删 → 彻底清除。
//
// 运行：
//   ADMIN_PASS=... E2E_ALLOW_DESTRUCTIVE=1 npm run e2e -- --grep @recycle
//
// 说明：软删任务本身会 undefine libvirt 域，因此「回收站 → 恢复」走的正是
// 「域已不存在、需按记录/磁盘重建」的分支（代码里的 domain_defined=false 路径）。
import { test, expect } from '@playwright/test'
import { ACCOUNTS, ALLOW_DESTRUCTIVE, e2eName } from './helpers/env.js'
import { login } from './helpers/auth.js'
import { collectPageErrors } from './helpers/ui.js'
import { api, findVMByName, getVMOptions, waitTask } from './helpers/api.js'

const PASS = ACCOUNTS.admin.pass

async function createFixture(page) {
  const options = await getVMOptions(page)
  const image = (options.cloud_images || []).find((i) => !String(i.format || '').includes('iso'))
  if (!image) return null
  const name = e2eName('rb')
  const res = await api(page, 'POST', '/api/vms', {
    name,
    storage_pool: options.default_storage_pool || (options.storage_pools?.[0]?.name ?? ''),
    vcpu: 1,
    memory_mb: 1024,
    disks: [{ source_image_id: image.id, create_gb: 10 }],
    network: (options.networks || [])[0] || 'default',
  })
  if (!res.data?.task_id) throw new Error(`建机提交失败：${res.message || ''}`)
  await waitTask(page, res.data.task_id, { timeout: 300_000 })
  const vm = await findVMByName(page, name)
  if (!vm) throw new Error('建机成功但列表找不到 VM')
  return vm
}

async function softDelete(page, id, name) {
  const del = await api(page, 'DELETE', `/api/vms/${id}`, undefined, { guard: name })
  if (del.data?.task_id) await waitTask(page, del.data.task_id, { timeout: 180_000 })
}

test.describe('回收站 @recycle', () => {
  test.skip(!PASS, '未提供 ADMIN_PASS')
  test.skip(!ALLOW_DESTRUCTIVE, '未开启 E2E_ALLOW_DESTRUCTIVE')
  test.setTimeout(300_000)

  test('软删 → 恢复（重建域）→ 再删 → 彻底清除', async ({ page }) => {
    await login(page)
    const vm = await createFixture(page)
    test.skip(!vm, '没有云镜像，无法造 fixture VM')
    const name = vm.name
    const errors = collectPageErrors(page)

    try {
      // 1) 软删
      await softDelete(page, vm.id, name)
      await expect.poll(async () => !(await findVMByName(page, name)), { timeout: 60_000 }).toBe(true)

      // 2) 回收站里应能看到
      await page.goto('/#/recycle-bin')
      const row = page.locator('.el-table__row', { hasText: name })
      await expect(row).toBeVisible({ timeout: 30_000 })
      await row.click()

      // 3) 恢复（域已被 undefine）
      const drawer = page.locator('.el-drawer:visible')
      await drawer.getByRole('button', { name: '恢复' }).click()

      // 若磁盘也被删除，会先弹「恢复后无法开机」的二次确认
      const stillRestore = page.getByRole('button', { name: '仍要恢复' })
      if (await stillRestore.count()) {
        await stillRestore.click()
      }

      // 恢复成功后弹「是否前往虚拟机列表」；留在本页
      await expect(page.getByRole('button', { name: '查看虚拟机' })).toBeVisible({ timeout: 60_000 })
      await page.getByRole('button', { name: '留在本页' }).click()

      // 4) 列表恢复，且能再次找到
      await expect.poll(async () => !!(await findVMByName(page, name)), { timeout: 60_000 }).toBe(true)
      const restored = await findVMByName(page, name)
      expect(restored.status, '恢复后应为关机态').toBe('shut off')

      // 5) 再删 + 彻底清除（先绕一跳，避免同 hash 导航不重挂组件导致列表陈旧）
      await softDelete(page, restored.id, name)
      await page.goto('/#/vms')
      await page.goto('/#/recycle-bin')
      const row2 = page.locator('.el-table__row', { hasText: name })
      await expect(row2).toBeVisible({ timeout: 30_000 })
      await row2.click()
      await page.locator('.el-drawer:visible').getByRole('button', { name: '彻底清除' }).click()
      await page.locator('.el-message-box').getByRole('button', { name: '彻底清除' }).click()
      await expect(page.locator('.el-message').last()).toContainText(/已彻底清除|已清除记录/, { timeout: 60_000 })

      // 回收站与 VM 列表都不应再有
      await expect(row2).toHaveCount(0, { timeout: 30_000 })
      await expect.poll(async () => !(await findVMByName(page, name)), { timeout: 30_000 }).toBe(true)

      expect(errors, `回收站操作出现 JS 异常：\n${errors.join('\n')}`).toHaveLength(0)
    } finally {
      if (!page.isClosed()) {
        // 兜底：仍在列表则软删 + 彻底清除
        const leftover = await findVMByName(page, name).catch(() => null)
        if (leftover) {
          await softDelete(page, leftover.id, name).catch(() => {})
        }
        const recycle = await api(page, 'GET', '/api/vms-recycle').catch(() => ({}))
        const items = recycle.data?.items || recycle.data || []
        const hit = (Array.isArray(items) ? items : []).find((x) => x.name === name)
        if (hit) await api(page, 'DELETE', `/api/vms-recycle/${hit.id}/purge?purge_volumes=true`, undefined, { guard: name }).catch(() => {})
      }
    }
  })
})
