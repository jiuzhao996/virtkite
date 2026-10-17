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
          await api(page, 'DELETE', `/api/docker/containers/${leftover.ID}?force=true`, undefined, { guard: name }).catch(() => {})
        }
      }
    }
  })

  test('容器参数校验与暂停/恢复/重启/重命名', async ({ page }) => {
    await login(page)
    const probe = await api(page, 'GET', '/api/docker/containers')
    test.skip(probe.status !== 200, 'Docker 不可用')

    // 非法名称 / 非法端口格式必须在入口被拒
    const badName = await api(page, 'POST', '/api/docker/containers', { name: 'bad name', image: 'alpine:3.24' })
    expect(badName.status, '非法容器名应 400').toBe(400)
    const badPort = await api(page, 'POST', '/api/docker/containers', {
      name: e2eName('badport'),
      image: 'alpine:3.24',
      ports: ['not-a-port'],
    })
    expect(badPort.status, '非法端口映射应 400').toBe(400)

    // 合法创建 → 暂停/恢复/重启/重命名 → 删除
    const name = e2eName('ctr2')
    const created = await api(page, 'POST', '/api/docker/containers', {
      name,
      image: 'alpine:3.24',
      command: 'sleep 60',
      envs: ['E2E=1'],
      restart: 'no',
    })
    expect(created.status, `创建失败：${created.message || ''}`).toBe(200)
    const id = created.data?.id
    expect(id, '创建应返回容器 ID').toBeTruthy()

    try {
      for (const action of ['pause', 'unpause', 'restart']) {
        const r = await api(page, 'POST', `/api/docker/containers/${id}/${action}`)
        expect(r.status, `${action} 失败：${r.message || ''}`).toBe(200)
      }
      const renamed = `${name}-r`
      const rn = await api(page, 'POST', `/api/docker/containers/${id}/rename`, { name: renamed })
      expect(rn.status, `重命名失败：${rn.message || ''}`).toBe(200)
      const list = await api(page, 'GET', '/api/docker/containers')
      expect((list.data?.items || []).some((c) => (c.Names || '').includes(renamed))).toBe(true)
    } finally {
      await api(page, 'DELETE', `/api/docker/containers/${id}?force=true`, undefined, { guard: name }).catch(() => {})
    }
  })

  test('Docker 卷：创建 → 列表 → 删除', async ({ page }) => {
    await login(page)
    const probe = await api(page, 'GET', '/api/docker/volumes')
    test.skip(probe.status !== 200, 'Docker 不可用')

    const name = e2eName('vol')
    const created = await api(page, 'POST', '/api/docker/volumes', { name })
    expect(created.status, `创建卷失败：${created.message || ''}`).toBe(200)
    try {
      const list = await api(page, 'GET', '/api/docker/volumes')
      const items = list.data?.items || list.data || []
      expect((Array.isArray(items) ? items : []).some((v) => v.Name === name)).toBe(true)
    } finally {
      const del = await api(page, 'DELETE', `/api/docker/volumes/${encodeURIComponent(name)}`)
      expect([200, 404]).toContain(del.status)
    }
  })
})
