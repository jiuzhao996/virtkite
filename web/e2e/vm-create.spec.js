// 建机链路 E2E（L5）：三种安装方式切换、必填校验、安装源探测、真实建机。
//
// 运行：
//   ADMIN_PASS=... npm run e2e -- --grep @vm-create
//   ADMIN_PASS=... E2E_ALLOW_DESTRUCTIVE=1 npm run e2e -- --grep @vm-create
//
// 安全策略：
// - 默认只做「走到确认页但不提交」的只读走查；
// - 真实建机需要 E2E_ALLOW_DESTRUCTIVE=1，且资源名带 e2e- 前缀，结束时清理。
import { test, expect } from '@playwright/test'
import { ACCOUNTS, ALLOW_DESTRUCTIVE, RUN_ID, e2eName } from './helpers/env.js'
import { login } from './helpers/auth.js'
import { collectPageErrors } from './helpers/ui.js'
import { api, findVMByName, getVMOptions, waitTask } from './helpers/api.js'

const PASS = ACCOUNTS.admin.pass

test.describe('建机向导走查 @vm-create', () => {
  test.skip(!PASS, '未提供 ADMIN_PASS')

  test.beforeEach(async ({ page }) => {
    await login(page)
    await page.goto('/#/vms/new')
    await expect(page.getByText('选择安装方式')).toBeVisible()
  })

  test('三种安装方式都能切换且出现各自字段', async ({ page }) => {
    const errors = collectPageErrors(page)

    // ISO：安装介质 + 网络安装源 + 操作系统
    await expect(page.getByText('安装介质', { exact: true })).toBeVisible()
    await expect(page.getByPlaceholder('可选，如 ftp://192.168.219.1/rocky10/')).toBeVisible()

    // 云镜像：云镜像选择器 + 系统盘说明
    await page.locator('.mode-card', { hasText: '云镜像 + cloud-init' }).click()
    await expect(page.getByText('云镜像 / 模板', { exact: true })).toBeVisible()

    // 克隆：源虚拟机 + 新名称
    await page.locator('.mode-card', { hasText: '克隆现有 VM' }).click()
    await expect(page.getByText('源虚拟机', { exact: true })).toBeVisible()

    expect(errors, `切换安装方式出现 JS 异常：\n${errors.join('\n')}`).toHaveLength(0)
  })

  test('ISO 模式空表单点「下一步」给出中文校验', async ({ page }) => {
    await page.getByRole('button', { name: '下一步' }).click()
    // 未选介质时先提示安装介质；顺序以 CreateVmWizard.next() 为准
    await expect(page.locator('.el-message').last()).toContainText(/请选择(操作系统|安装介质)/)
  })

  test('网络安装源格式错误：前端即时提示且「测试」禁用', async ({ page }) => {
    const input = page.getByPlaceholder('可选，如 ftp://192.168.219.1/rocky10/')
    await input.fill('not-a-url')
    await expect(page.getByText(/格式不正确：需 ftp:\/\/ 或 http\(s\):\/\/ 开头/)).toBeVisible()
    const testBtn = page.locator('.el-input-group__append').getByRole('button', { name: '测试' })
    await expect(testBtn).toBeDisabled()
  })

  test('不可达安装源点「测试」返回「安装源不可用」', async ({ page }) => {
    // 端口 1 必然不可达；后端 curl 超时 15s，给足等待
    await page.getByPlaceholder('可选，如 ftp://192.168.219.1/rocky10/').fill('http://127.0.0.1:1/tree/')
    const testBtn = page.locator('.el-input-group__append').getByRole('button', { name: '测试' })
    await testBtn.click()
    await expect(page.locator('.el-message').last()).toContainText('安装源不可用', { timeout: 30_000 })
  })

  test('克隆模式未选源虚拟机被拦截', async ({ page }) => {
    await page.locator('.mode-card', { hasText: '克隆现有 VM' }).click()
    await page.getByRole('button', { name: '下一步' }).click()
    await expect(page.locator('.el-message').last()).toContainText('请选择源虚拟机')
  })
})

test.describe('真实建机 @vm-create', () => {
  test.skip(!PASS, '未提供 ADMIN_PASS')
  test.skip(!ALLOW_DESTRUCTIVE, '未开启 E2E_ALLOW_DESTRUCTIVE，跳过真实建机')
  test.setTimeout(480_000)

  test('UI 建机（云镜像 + cloud-init）→ 开机拿 IP → SSH 连通 → 清理', async ({ page }) => {
    await login(page)
    const options = await getVMOptions(page)
    const pools = (options.storage_pools || []).filter((p) => p.active)
    test.skip(!pools.length, '没有可用存储池')

    await page.goto('/#/vms/new')
    await expect(page.getByText('选择安装方式')).toBeVisible()

    const images = (options.cloud_images || []).filter((i) => !String(i.format || '').includes('iso'))
    const isoMode = !images.length
    if (!isoMode) {
      await page.locator('.mode-card', { hasText: '云镜像 + cloud-init' }).click()
    }

    if (isoMode) {
      // Element Plus 的 select 占位符渲染成 span，不是 input placeholder；
      // 按表单项标签定位更稳。
      await page.locator('.el-form-item', { hasText: '安装介质' }).locator('.el-select').click({ timeout: 15000 })
      const firstIso = page.locator('.el-select-dropdown:visible .el-select-dropdown__item').first()
      test.skip(!(await firstIso.count()), '存储池中没有 ISO')
      await firstIso.click()
      const osSelect = page.locator('.el-form-item', { hasText: '操作系统' }).locator('.el-select')
      if (await osSelect.count()) {
        const selected = await osSelect.evaluate(
          (el) => el.querySelector('.el-select__selected-item')?.textContent || ''
        )
        if (!selected || !selected.trim()) {
          await osSelect.click()
          await page.locator('.el-select-dropdown:visible .el-select-dropdown__item').first().click({ timeout: 15000 })
        }
      }
    } else {
      await page.locator('.el-form-item', { hasText: '云镜像 / 模板' }).locator('.el-select').click({ timeout: 15000 })
      await page.locator('.el-select-dropdown:visible .el-select-dropdown__item').first().click({ timeout: 15000 })
    }

    // 云镜像：若该镜像被识别为支持 cloud-init，开启并填 root 口令，
    // 这样第 4 步的「创建后自动开机并初始化」才有意义（等 IP + 托管凭据）。
    const ciSwitch = page.locator('.el-form-item', { hasText: 'cloud-init' }).locator('.el-switch')
    const ciPass = `E2e-${RUN_ID}-a1`
    let cloudInit = false
    if (!isoMode && (await ciSwitch.count())) {
      await ciSwitch.click()
      await page.getByText('cloud-init 配置', { exact: true }).click()
      await page.getByPlaceholder('如 ubuntu / root，可选').fill('root')
      await page.locator('.ci-collapse input[type="password"]').fill(ciPass)
      cloudInit = true
    }

    await page.getByRole('button', { name: '下一步' }).click()

    // 第 2 步：名称与规格
    const name = e2eName('ui-vm')
    await page.getByPlaceholder('仅字母、数字、_、-').first().fill(name)
    await page.getByRole('button', { name: '下一步' }).click()
    await page.getByRole('button', { name: '下一步' }).click()

    // 第 4 步：确认自动初始化开关（cloud-init 且填了口令时应为勾选）
    if (cloudInit) {
      const auto = page.locator('.el-checkbox', { hasText: '创建后自动开机并初始化' })
      if (await auto.count()) {
        const checked = await auto.locator('input[type="checkbox"]').isChecked()
        if (!checked) await auto.click()
      }
    }

    await expect(page.getByRole('button', { name: /创建虚拟机/ })).toBeVisible()
    await page.getByRole('button', { name: /创建虚拟机/ }).click()

    let vm = null
    try {
      // 注意必须锚定结尾：`/#\/vms/` 会匹配 `#/vms/new`，导致创建还没完成就提前通过
      await expect(page).toHaveURL(/#\/vms(\?.*)?$/, { timeout: 420_000 })

      vm = await findVMByName(page, name)
      expect(vm, `建机后列表未找到 ${name}`).toBeTruthy()

      // 云镜像 + cloud-init：验证开机、IP 回填、SSH 可登录（托管凭据）
      if (cloudInit) {
        await expect
          .poll(
            async () => {
              const cur = await findVMByName(page, name)
              return cur ? `${cur.status}|${cur.ip || ''}` : ''
            },
            { timeout: 180_000, intervals: [3000] }
          )
          .toMatch(/^running\|.+/)

        const cur = await findVMByName(page, name)
        // 刚拿到 IP 时 sshd/cloud-init 可能还在启动，轮询到 SSH 真正可用。
        // 超时给到 3 分钟：全量跑时慢池只有 2 worker，VM 启动会被别的建机/删机
        // 任务拖慢，2 分钟曾出现过 sshd 还没起导致假失败（单独跑 28 秒就能通）。
        const sshCall = () =>
          api(page, 'POST', `/api/vms/${cur.id}/files/list`, { host: cur.ip, use_saved: true, path: '/' })
        await expect
          .poll(async () => (await sshCall()).status, { timeout: 180_000, intervals: [3000] })
          .toBe(200)

        const ssh = await sshCall()
        expect(ssh.status, `SSH 目录读取失败：${ssh.message || ''}`).toBe(200)
        expect(
          Array.isArray(ssh.data?.items),
          `SSH 返回目录项（实际响应：${JSON.stringify(ssh).slice(0, 400)}）`
        ).toBe(true)
      }
    } finally {
      // 失败也清理：只删本轮 e2e 前缀的 VM；软删后再彻底清除，避免回收站堆积
      const leftover = await findVMByName(page, name)
      if (leftover) {
        const del = await api(page, 'DELETE', `/api/vms/${leftover.id}`, undefined, { guard: name })
        if (del.data && del.data.task_id) {
          await waitTask(page, del.data.task_id, { timeout: 180_000 }).catch(() => {})
        }
        await api(page, 'DELETE', `/api/vms-recycle/${leftover.id}/purge?purge_volumes=true`, undefined, { guard: name }).catch(() => {})
      }
    }

    await expect.poll(async () => !(await findVMByName(page, name)), { timeout: 60_000 }).toBe(true)
  })
})
