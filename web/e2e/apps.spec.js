// 应用商店 E2E（L5）：参数 schema 校验、SSH 目标白名单、use_saved 凭据缺失。
//
// 运行：
//   ADMIN_PASS=... npm run e2e -- --grep @apps
//
// 全部是「在拨号前就该被拒」的负例，不会在 guest 内执行任何脚本。
// 真实安装在具备 apt/网络的环境另行验收（见文档 §11.2）。
import { test, expect } from '@playwright/test'
import { ACCOUNTS } from './helpers/env.js'
import { login } from './helpers/auth.js'
import { api, listVMs } from './helpers/api.js'

const PASS = ACCOUNTS.admin.pass

test.describe('应用商店 @apps', () => {
  test.skip(!PASS, '未提供 ADMIN_PASS')

  test('参数 schema 校验：范围/正则/候选/必填', async ({ page }) => {
    await login(page)
    const vms = await listVMs(page)
    test.skip(!vms.length, '平台没有 VM（参数校验不依赖 VM 状态，但接口要求有效 vm_id）')
    const vmId = vms[0].id

    const cases = [
      [{ WP_PORT: '70000' }, '端口越界'],
      [{ WP_DB_NAME: 'bad name!' }, '库名正则'],
      [{ WP_DB_IMAGE: 'postgres:16' }, '候选值'],
    ]
    for (const [params, label] of cases) {
      const r = await api(page, 'POST', '/api/vms/apps/install', {
        vm_id: vmId,
        app_id: 'wordpress',
        params,
      })
      expect(r.status, `${label} 应 400，实际 ${r.status} ${r.message || ''}`).toBe(400)
    }

    const noApp = await api(page, 'POST', '/api/vms/apps/install', { vm_id: vmId, app_id: 'no-such-app' })
    expect(noApp.status).toBe(400)
  })

  test('SSH 目标白名单：公网地址被拒', async ({ page }) => {
    await login(page)
    const vms = await listVMs(page)
    test.skip(!vms.length, '平台没有 VM')
    const r = await api(page, 'POST', '/api/vms/apps/install', {
      vm_id: vms[0].id,
      app_id: 'nginx',
      host: '8.8.8.8',
      user: 'root',
      password: 'whatever',
    })
    expect(r.status, '公网目标必须被拒').toBe(400)
  })

  test('use_saved 但未保存凭据 → 400 中文原因', async ({ page }) => {
    await login(page)
    const vms = await listVMs(page)
    test.skip(!vms.length, '平台没有 VM')
    const cred = await api(page, 'GET', `/api/vms/${vms[0].id}/credentials`)
    test.skip(!!cred.data?.configured, '该 VM 已有托管凭据，跳过（避免真的提交安装任务）')

    const r = await api(page, 'POST', '/api/vms/apps/install', {
      vm_id: vms[0].id,
      app_id: 'nginx',
      host: vms[0].ip || '10.0.0.1',
      use_saved: true,
    })
    expect(r.status, `无托管凭据应 400，实际 ${r.status}`).toBe(400)
    expect(String(r.message || '')).toContain('凭据')
  })
})
