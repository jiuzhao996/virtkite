// 架构设计器计划 E2E（L5）：保存 → 连线类型持久化 → 导出 YAML → 删除。
//
// 运行：
//   ADMIN_PASS=... npm run e2e -- --grep @designer
//
// 不执行落地（会创建真实资源），只验证计划 CRUD 与导出契约；
// 落地/收敛链路留待专项环境验证（见验收文档 §11.2 未修复项）。
import { test, expect } from '@playwright/test'
import { ACCOUNTS, e2eName } from './helpers/env.js'
import { login } from './helpers/auth.js'
import { api } from './helpers/api.js'

const PASS = ACCOUNTS.admin.pass

test.describe('架构设计器 @designer', () => {
  test.skip(!PASS, '未提供 ADMIN_PASS')

  test('计划保存 → 连线 kind 持久化 → 导出单个 stacks → 删除', async ({ page }) => {
    await login(page)
    const id = e2eName('dsg')
    const plan = {
      id,
      name: 'E2E 设计器验收',
      nodes: [
        { id: 'n1', kind: 'container', ref: 'web-stack', name: '栈A' },
        { id: 'n2', kind: 'container', ref: 'monitor-stack', name: '栈B' },
        { id: 'n3', kind: 'net', ref: '10.0.0.0/24', name: 'e2e-net' },
      ],
      // kind 是本次修复点：此前后端 dsgLink 不接收，保存后一律变回 net
      links: [
        { from: 'n1', to: 'n3', kind: 'dep' },
        { from: 'n2', to: 'n3', kind: 'storage' },
      ],
    }

    try {
      const saved = await api(page, 'POST', '/api/designer/plans', plan)
      expect(saved.status, `保存失败：${saved.message || ''}`).toBe(200)

      const list = await api(page, 'GET', '/api/designer/plans')
      const items = list.data?.items || list.data || []
      const got = (Array.isArray(items) ? items : []).find((p) => p.id === id)
      expect(got, '列表应能读回计划').toBeTruthy()
      const kinds = (got.links || []).map((l) => l.kind)
      expect(kinds).toEqual(['dep', 'storage'])

      // 导出 YAML：两个容器节点必须聚合到一个顶层 stacks: 键
      const exp = await api(page, 'GET', `/api/designer/plans/${id}/export`)
      expect(exp.status).toBe(200)
      // api() 对 text/yaml 解析失败会落到 raw
      const text = exp.raw || exp.data || ''
      const stacksKeys = String(text).match(/^stacks:/gm) || []
      expect(stacksKeys.length, '顶层 stacks: 只能出现一次').toBe(1)
      expect(String(text)).toContain('web-stack')
      expect(String(text)).toContain('monitor-stack')
    } finally {
      await api(page, 'DELETE', `/api/designer/plans/${id}`).catch(() => {})
    }
  })

  test('导出非法 ID 被拒', async ({ page }) => {
    await login(page)
    const r = await api(page, 'GET', '/api/designer/plans/..%2Fetc/export')
    expect([400, 404]).toContain(r.status)
  })
})
