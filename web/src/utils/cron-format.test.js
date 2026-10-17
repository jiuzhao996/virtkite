// cron-format 纯函数单测：动作/状态映射、链式提示、死开关、最近执行、参数摘要。
import { describe, it, expect } from 'vitest'
import {
  cronActionText,
  cronActionTag,
  cronRunStatusText,
  cronRunStatusTag,
  cronChainTip,
  cronStaleTip,
  cronLatestRun,
  cronLatestRunTip,
  cronParamsText,
} from './cron-format'

describe('动作映射', () => {
  it('已知动作给中文与颜色', () => {
    expect(cronActionText('vm_snapshot')).toBe('虚拟机快照')
    expect(cronActionText('db_backup')).toBe('数据库备份')
    expect(cronActionText('ansible_playbook')).toBe('Playbook 执行')
    expect(cronActionTag('vm_snapshot')).toBe('primary')
    expect(cronActionTag('db_backup')).toBe('success')
    expect(cronActionTag('ansible_playbook')).toBe('warning')
  })

  it('未知动作回落 info、空值不炸', () => {
    expect(cronActionTag('brand-new')).toBe('info')
    expect(cronActionText('')).toBe('')
  })
})

describe('执行状态映射', () => {
  it('四种状态全覆盖', () => {
    expect(cronRunStatusText('success')).toBe('成功')
    expect(cronRunStatusText('failed')).toBe('失败')
    expect(cronRunStatusText('running')).toBe('执行中')
    expect(cronRunStatusText('skipped')).toBe('跳过')
    expect(cronRunStatusText('')).toBe('未知')
    expect(cronRunStatusText('weird')).toBe('weird')
    expect(cronRunStatusTag('failed')).toBe('danger')
    expect(cronRunStatusTag('weird')).toBe('info')
  })
})

describe('链式下一跳提示', () => {
  const items = [{ id: 3, name: '每晚快照' }]

  it('成功/失败链都列出任务名', () => {
    expect(cronChainTip({ on_success_task_id: 3, on_failure_task_id: 9 }, items)).toBe(
      '成功 → 每晚快照；失败 → #9'
    )
  })

  it('无链返回空串', () => {
    expect(cronChainTip({}, items)).toBe('')
  })
})

describe('死开关与最近执行', () => {
  it('stale 提示含「检查调度器」', () => {
    const tip = cronStaleTip({ stale_due: '2026-10-18T02:00:00+08:00' })
    expect(tip).toContain('检查调度器')
  })

  it('取 recent_runs 首条', () => {
    const row = { recent_runs: [{ id: 2 }, { id: 1 }] }
    expect(cronLatestRun(row).id).toBe(2)
    expect(cronLatestRun({})).toBeNull()
  })

  it('输出超 80 字截断', () => {
    const row = { recent_runs: [{ started_at: '2026-10-18T02:00:00+08:00', output: 'x'.repeat(200) }] }
    const tip = cronLatestRunTip(row)
    expect(tip).toContain('…')
  })
})

describe('参数列摘要', () => {
  it('vm_id 翻译成虚拟机名', () => {
    const row = { params: '{"vm_id":5}' }
    expect(cronParamsText(row, [{ id: 5, name: 'web-01' }])).toBe('虚拟机：web-01')
    expect(cronParamsText(row, [])).toBe('vm_id: 5')
  })

  it('playbook 列出目标名，空目标提示未选', () => {
    expect(
      cronParamsText({ params: '{"playbook":"nginx","targets":[5]}' }, [{ id: 5, name: 'web-01' }])
    ).toBe('playbook：nginx → web-01')
    expect(cronParamsText({ params: '{"playbook":"nginx"}' }, [])).toBe('playbook：nginx → 未选目标')
  })

  it('脏 JSON 原样返回、空参数给中文占位', () => {
    expect(cronParamsText({ params: 'not-json' }, [])).toBe('not-json')
    expect(cronParamsText({ params: '{}' }, [])).toBe('（无参数）')
  })
})
