// Ansible 真实执行 E2E（L5）：cloud-init 建机 → 等 IP/托管凭据 → adhoc ping → 清理。
//
// 运行：
//   ADMIN_PASS=... E2E_ALLOW_DESTRUCTIVE=1 npm run e2e -- --grep @ansible-run
//
// 前置：宿主机已安装 ansible（未安装时跳过）。用 ping 模块验证
// inventory/凭据/引擎/任务管线的完整链路，不修改 guest 状态。
import { test, expect } from '@playwright/test'
import { ACCOUNTS, ALLOW_DESTRUCTIVE, e2eName } from './helpers/env.js'
import { login } from './helpers/auth.js'
import { api, findVMByName, getVMOptions, waitTask } from './helpers/api.js'

const PASS = ACCOUNTS.admin.pass

// 用镜像自带的非 root 用户：Rocky/Ubuntu 云镜像里 root 的 authorized_keys
// 常带 forced-command（"Please login as the user ..."），用 root 走私钥通道
// 会把这句提示当成模块 stdout，报「Module result deserialization failed」。
// 这与平台无关，属镜像特性（见 model/vm.go 对 AnsibleUser 的注释）。
const SSH_USER = process.env.E2E_SSH_USER || 'rocky'

async function createRunnableVM(page) {
  const options = await getVMOptions(page)
  const image = (options.cloud_images || []).find((i) => !String(i.format || '').includes('iso'))
  if (!image) return null
  const name = e2eName('ans')
  const res = await api(page, 'POST', '/api/vms', {
    name,
    storage_pool: options.default_storage_pool || (options.storage_pools?.[0]?.name ?? ''),
    vcpu: 1,
    memory_mb: 1024,
    disks: [{ source_image_id: image.id, create_gb: 10 }],
    network: (options.networks || [])[0] || 'default',
    cloud_init: { hostname: name, user: SSH_USER, password: `E2e-${name}-a1` },
    auto_provision: true,
  })
  if (!res.data?.task_id) throw new Error(`建机提交失败：${res.message || ''}`)
  await waitTask(page, res.data.task_id, { timeout: 300_000 })
  const vm = await findVMByName(page, name)
  if (!vm) throw new Error('建机成功但列表找不到 VM')
  return vm
}

test.describe('Ansible 真实执行 @ansible-run', () => {
  test.skip(!PASS, '未提供 ADMIN_PASS')
  test.skip(!ALLOW_DESTRUCTIVE, '未开启 E2E_ALLOW_DESTRUCTIVE')
  test.setTimeout(480_000)

  test('adhoc ping：任务成功且输出含 SUCCESS', async ({ page }) => {
    await login(page)
    const status = await api(page, 'GET', '/api/ansible/status')
    test.skip(!status.data?.installed, '宿主机未安装 Ansible')

    const vm = await createRunnableVM(page)
    test.skip(!vm, '没有云镜像，无法造运行中 VM')

    try {
      // 等 IP 回填 + 托管凭据写入（auto_provision 已做，列表接口可能有 30s 节流）
      await expect
        .poll(async () => {
          const cur = await findVMByName(page, vm.name)
          return cur ? `${cur.status}|${cur.ip || ''}` : ''
        }, { timeout: 180_000, intervals: [3000] })
        .toMatch(/^running\|.+/)

      const run = await api(page, 'POST', '/api/ansible/run', { targets: [vm.id], module: 'ping' })
      expect(run.status, `提交失败：${run.message || ''}`).toBe(202)
      const taskId = run.data?.task_id
      expect(taskId).toBeTruthy()

      const task = await waitTask(page, taskId, { timeout: 300_000 })
      expect(task.status).toBe('success')
      expect(String(task.result || '')).toContain('"module":"ping"')
    } finally {
      const leftover = await findVMByName(page, vm.name)
      if (leftover) {
        const del = await api(page, 'DELETE', `/api/vms/${leftover.id}`, undefined, { guard: vm.name })
        if (del.data?.task_id) await waitTask(page, del.data.task_id, { timeout: 180_000 }).catch(() => {})
        await api(page, 'DELETE', `/api/vms-recycle/${leftover.id}/purge?purge_volumes=true`, undefined, { guard: vm.name }).catch(() => {})
      }
    }
  })
})
