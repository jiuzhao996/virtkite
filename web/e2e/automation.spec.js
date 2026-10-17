// 运维自动化 E2E（L5）：Playbook 新建/语法校验/删除、计划任务新建/删除。
//
// 运行：
//   ADMIN_PASS=... E2E_ALLOW_DESTRUCTIVE=1 npm run e2e -- --grep @auto
//
// 不执行 Playbook、不触发计划任务（避免在真实环境跑命令/备份）；
// 只验证「配置面」的完整闭环。执行链路由 scripts/e2e-provision.sh 在 API 层覆盖。
import { test, expect } from '@playwright/test'
import { ACCOUNTS, ALLOW_DESTRUCTIVE, e2eName } from './helpers/env.js'
import { login } from './helpers/auth.js'
import { collectPageErrors } from './helpers/ui.js'
import { api } from './helpers/api.js'

const PASS = ACCOUNTS.admin.pass

test.describe('运维自动化 @auto', () => {
  test.skip(!PASS, '未提供 ADMIN_PASS')
  test.skip(!ALLOW_DESTRUCTIVE, '未开启 E2E_ALLOW_DESTRUCTIVE')
  test.setTimeout(180_000)

  test('Playbook：新建 → 语法校验 → 保存 → 删除', async ({ page }) => {
    await login(page)
    const errors = collectPageErrors(page)
    const id = e2eName('pb')

    // Ansible 未安装时新建按钮禁用，直接跳过
    const status = await api(page, 'GET', '/api/ansible/status')
    test.skip(!status.data?.installed, '宿主机未安装 Ansible')

    const yaml = [
      `# vmops-playbook: name=${id} | desc=e2e acceptance | targets=linux`,
      '---',
      '- name: e2e ping',
      '  hosts: all',
      '  gather_facts: false',
      '  tasks:',
      '    - name: ping',
      '      ansible.builtin.ping:',
    ].join('\n')

    try {
      await page.goto('/#/automation')
      await page.locator('.el-tabs__item', { hasText: 'Playbook 库' }).click()
      await page.getByRole('button', { name: '新建 Playbook' }).click()

      await page.getByPlaceholder('ID（字母数字 - _，如 my-init）').fill(id)
      await page.locator('.auto-yaml textarea').fill(yaml)
      await page.getByRole('button', { name: '语法校验' }).click()
      await expect(page.locator('.el-message').last()).toContainText(/通过|语法/, { timeout: 60_000 })

      await page.getByRole('button', { name: '保 存' }).click()
      await expect(page.locator('.el-message').last()).toContainText(/已创建|已保存|保存成功/, { timeout: 60_000 })
      // 保存后编辑抽屉仍停留，先关闭再操作表格
      await page.locator('.el-drawer__close-btn:visible').click()

      const row = page.locator('.el-table__row', { hasText: id })
      await expect(row).toBeVisible({ timeout: 30_000 })

      await row.getByRole('button', { name: '删除' }).click()
      await page.locator('.el-popconfirm').getByRole('button', { name: '删除' }).click()
      await expect(row).toHaveCount(0, { timeout: 30_000 })

      expect(errors, `Playbook 操作出现 JS 异常：\n${errors.join('\n')}`).toHaveLength(0)
    } finally {
      if (id.startsWith('e2e-')) {
        await api(page, 'DELETE', `/api/ansible/playbooks/${encodeURIComponent(id)}`).catch(() => {})
      }
    }
  })

  test('计划任务：新建（数据库备份）→ 列表可见 → 删除', async ({ page }) => {
    await login(page)
    const errors = collectPageErrors(page)
    const name = e2eName('cron')

    try {
      await page.goto('/#/automation?tab=cron')
      await expect(page.getByRole('button', { name: '新建任务' })).toBeVisible({ timeout: 30_000 })
      await page.getByRole('button', { name: '新建任务' }).click()

      await page.getByPlaceholder('如：每晚上报 node1 快照').fill(name)
      await page.getByPlaceholder('分 时 日 月 周，如 0 2 * * *').fill('0 3 * * *')
      // 数据库备份不需要选 VM，是最安全的动作
      await page.locator('.el-dialog .el-radio', { hasText: '数据库备份' }).click()
      await page.locator('.el-dialog').getByRole('button', { name: '创建' }).click()
      await expect(page.locator('.el-message').last()).toContainText(/创建|成功/, { timeout: 30_000 })

      const row = page.locator('.el-table__row', { hasText: name })
      await expect(row).toBeVisible({ timeout: 30_000 })

      await row.getByRole('button', { name: '删除' }).click()
      await page.locator('.el-message-box').getByRole('button', { name: '删除' }).click()
      await expect(row).toHaveCount(0, { timeout: 30_000 })

      expect(errors, `计划任务操作出现 JS 异常：\n${errors.join('\n')}`).toHaveLength(0)
    } finally {
      // 兜底：按名称找到 cron 并删除
      const list = await api(page, 'GET', '/api/crons').catch(() => ({}))
      const items = list.data?.items || []
      const hit = items.find((c) => c.name === name)
      if (hit) await api(page, 'DELETE', `/api/crons/${hit.id}`).catch(() => {})
    }
  })
})
