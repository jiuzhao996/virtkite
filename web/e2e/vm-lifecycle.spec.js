// 虚拟机生命周期 E2E（L5）：快照 → 开机 → 关机 → 回滚/删快照 → 自启 → 删除 → 彻底清除。
//
// 运行：
//   ADMIN_PASS=... E2E_ALLOW_DESTRUCTIVE=1 npm run e2e -- --grep @vm-life
//
// 前置数据用 API 造（云镜像增量盘，秒级），被验收的动作全部走 UI 点击；
// 结束时软删 + 回收站彻底清除，只操作 e2e- 前缀资源。
import { test, expect } from '@playwright/test'
import { ACCOUNTS, ALLOW_DESTRUCTIVE, e2eName } from './helpers/env.js'
import { login } from './helpers/auth.js'
import { collectPageErrors } from './helpers/ui.js'
import { api, findVMByName, getVM, getVMOptions, getVMSpec, waitTask } from './helpers/api.js'

const PASS = ACCOUNTS.admin.pass

/** 用云镜像造一台关机态 fixture VM（返回 {id, name}） */
async function createFixtureVM(page) {
  const options = await getVMOptions(page)
  const image = (options.cloud_images || []).find((i) => !String(i.format || '').includes('iso'))
  if (!image) return null

  const name = e2eName('life')
  const res = await api(page, 'POST', '/api/vms', {
    name,
    storage_pool: options.default_storage_pool || (options.storage_pools?.[0]?.name ?? ''),
    vcpu: 1,
    memory_mb: 1024,
    disks: [{ source_image_id: image.id, create_gb: 10 }],
    network: (options.networks || [])[0] || 'default',
  })
  if (!res.data?.task_id) throw new Error(`建机提交失败：${res.message || JSON.stringify(res)}`)
  await waitTask(page, res.data.task_id, { timeout: 300_000 })

  const vm = await findVMByName(page, name)
  if (!vm) throw new Error('建机任务成功但列表找不到 VM')
  return { id: vm.id, name }
}

/** 等待 VM 到达指定状态 */
async function expectStatus(page, id, status) {
  await expect
    .poll(async () => (await getVM(page, id))?.status, { timeout: 120_000, intervals: [1500] })
    .toBe(status)
}

test.describe('虚拟机生命周期 @vm-life', () => {
  test.skip(!PASS, '未提供 ADMIN_PASS')
  test.skip(!ALLOW_DESTRUCTIVE, '未开启 E2E_ALLOW_DESTRUCTIVE')
  test.setTimeout(420_000)

  test('快照/开机/关机/回滚/自启/删除全链路', async ({ page }) => {
    await login(page)
    const fixture = await createFixtureVM(page)
    test.skip(!fixture, '没有云镜像，无法造 fixture VM')
    const { id, name } = fixture
    const snapName = e2eName('snap')

    const errors = collectPageErrors(page)
    try {
      // 从列表点「详情」进入（走真实入口，不用直链）
      await page.goto('/#/vms')
      const card = page.locator('.vm-card', { hasText: name })
      await expect(card).toBeVisible()
      await card.getByRole('button', { name: '详情' }).click()
      await expect(page).toHaveURL(new RegExp(`#/vms/${id}$`))

      // ---- 快照：新建 ----
      await page.locator('.side-menu').getByText('快照', { exact: true }).click()
      await page.getByRole('button', { name: '新建快照' }).click()
      await page.getByPlaceholder('如 snap-20260904').fill(snapName)
      await page.locator('.el-dialog').getByRole('button', { name: '创建' }).click()
      await expect(page.locator('.el-table').getByText(snapName)).toBeVisible({ timeout: 60_000 })

      // ---- 开机（详情工具栏电源按钮）----
      await page.getByRole('button', { name: '开机', exact: true }).click()
      await expectStatus(page, id, 'running')

      // ---- 关机 ----
      await page.getByRole('button', { name: '关机', exact: true }).click()
      await expectStatus(page, id, 'shut off')

      // ---- 回滚快照（关机态）----
      const snapRow = page.locator('.el-table__row', { hasText: snapName })
      await snapRow.getByRole('button', { name: '回滚' }).click()
      await page.locator('.el-message-box').getByRole('button', { name: '确定' }).click()
      await expect(page.locator('.el-message').last()).toContainText(/回滚|成功/, { timeout: 60_000 })

      // ---- 删除快照 ----
      await snapRow.getByRole('button', { name: '删除' }).click()
      await page.locator('.el-message-box').getByRole('button', { name: '确定' }).click()
      await expect(page.locator('.el-table').getByText(snapName)).toHaveCount(0, { timeout: 60_000 })

      // ---- 开机自启 ----
      await page.locator('.side-menu').getByText('概览', { exact: true }).click()
      const autostart = page.locator('.el-descriptions__cell', { hasText: '开机自启' }).locator('.el-switch')
      await autostart.click()
      await expect
        .poll(async () => (await getVMSpec(page, id))?.spec?.autostart, { timeout: 30_000 })
        .toBe(true)

      // ---- 删除（输入名称确认）----
      await page.getByRole('button', { name: '删除', exact: true }).click()
      const box = page.locator('.el-message-box')
      await box.locator('input').fill(name)
      await box.getByRole('button', { name: '确认删除' }).click()
      await expect(page).toHaveURL(/#\/vms(\?.*)?$/, { timeout: 180_000 })
      await expect.poll(async () => !(await findVMByName(page, name)), { timeout: 60_000 }).toBe(true)

      expect(errors, `生命周期操作出现 JS 异常：\n${errors.join('\n')}`).toHaveLength(0)
    } finally {
      // 兜底：软删 + 回收站彻底清除（只针对本轮 e2e 资源）
      const leftover = await findVMByName(page, name)
      if (leftover) {
        const del = await api(page, 'DELETE', `/api/vms/${leftover.id}`, undefined, { guard: name })
        if (del.data?.task_id) await waitTask(page, del.data.task_id, { timeout: 180_000 }).catch(() => {})
      }
      await api(page, 'DELETE', `/api/vms-recycle/${id}/purge?purge_volumes=true`, undefined, { guard: name }).catch(() => {})
    }
  })
})
