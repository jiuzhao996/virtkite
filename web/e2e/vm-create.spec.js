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
import { ACCOUNTS, ALLOW_DESTRUCTIVE, e2eName } from './helpers/env.js'
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
    await expect(page.getByText('云镜像 / 模板')).toBeVisible()

    // 克隆：源虚拟机 + 新名称
    await page.locator('.mode-card', { hasText: '克隆现有 VM' }).click()
    await expect(page.getByText('源虚拟机')).toBeVisible()

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
  test.setTimeout(420_000)

  test('UI 建机（ISO 或云镜像）→ 任务成功 → 清理', async ({ page }) => {
    await login(page)
    const options = await getVMOptions(page)
    const pools = (options.storage_pools || []).filter((p) => p.active)
    test.skip(!pools.length, '没有可用存储池')

    await page.goto('/#/vms/new')
    await expect(page.getByText('选择安装方式')).toBeVisible()

    // 优先云镜像（启动快、可验证 cloud-init），否则退回 ISO
    const images = (options.cloud_images || []).filter((i) => !String(i.format || '').includes('iso'))
    const isoMode = !images.length
    if (!isoMode) {
      await page.locator('.mode-card', { hasText: '云镜像 + cloud-init' }).click()
    }

    if (isoMode) {
      // ISO 列表为空则无法建机
      const isoSelect = page.getByPlaceholder('选择 ISO 安装镜像（全部存储池）')
      await isoSelect.click()
      const firstIso = page.locator('.el-select-dropdown__item').first()
      test.skip(!(await firstIso.count()), '存储池中没有 ISO')
      await firstIso.click()
    } else {
      await page.getByPlaceholder('选择云镜像或模板').click()
      await page.locator('.el-select-dropdown__item').first().click()
    }

    // 操作系统：ISO 若未自动识别则手动选第一个
    const osSelect = page.getByPlaceholder('选择操作系统（可按 ISO 文件名自动识别）')
    if (isoMode && (await osSelect.count())) {
      const selected = await osSelect.evaluate((el) => el.closest('.el-select')?.querySelector('.el-select__selected-item')?.textContent || '')
      if (!selected || !selected.trim()) {
        await osSelect.click()
        await page.locator('.el-select-dropdown__item').first().click()
      }
    }

    await page.getByRole('button', { name: '下一步' }).click()

    // 第 2 步：名称与规格
    const name = e2eName('ui-vm')
    const nameInput = page.getByPlaceholder(/虚拟机名称|名称/)
    await nameInput.fill(name)
    await page.getByRole('button', { name: '下一步' }).click()
    await page.getByRole('button', { name: '下一步' }).click()

    // 第 4 步：确认创建
    await expect(page.getByRole('button', { name: /创建虚拟机/ })).toBeVisible()
    await page.getByRole('button', { name: /创建虚拟机/ }).click()

    // 前端会轮询任务并跳回列表；最多等 5 分钟（后端 create_vm 上限 15 分钟）
    await expect(page).toHaveURL(/#\/vms/, { timeout: 360_000 })

    const vm = await findVMByName(page, name)
    expect(vm, `建机后列表未找到 ${name}`).toBeTruthy()

    // 清理：软删 + 回收站彻底清除（仅 e2e 资源）
    const del = await api(page, 'DELETE', `/api/vms/${vm.id}`)
    if (del.data && del.data.task_id) await waitTask(page, del.data.task_id, { timeout: 180_000 })
  })
})
