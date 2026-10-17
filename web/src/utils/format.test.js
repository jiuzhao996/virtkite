// format.js 纯函数单测：状态映射、时间/容量格式化、错误文案、校验。
// 这些函数被几十个页面共用，改错一处会全站显示异常，是最值得锁住的一层。
import { describe, it, expect } from 'vitest'
import {
  vmStatusText,
  vmStatusTag,
  vmStatusHex,
  roleText,
  taskStatusText,
  hostStatusText,
  fmtDateTime,
  fmtDate,
  fmtSizeGB,
  fmtSizeBytes,
  fmtFileSize,
  fmtRateBytes,
  fmtGBSum,
  errMsg,
  isCancel,
  usageColor,
  clampPct,
  isValidInstallTreeUrl,
} from './format'

describe('VM 状态映射', () => {
  it('已知状态返回中文与标签色', () => {
    expect(vmStatusText('running')).toBe('运行中')
    expect(vmStatusText('shut off')).toBe('已关机')
    expect(vmStatusTag('running')).toBe('success')
    expect(vmStatusHex('running')).toBeTruthy()
  })

  it('未知状态原样返回；空状态才用 fallback', () => {
    expect(vmStatusText('weird', '未知')).toBe('weird')
    expect(vmStatusText('', '未知')).toBe('未知')
    expect(vmStatusText('weird')).toBe('weird')
  })
})

describe('角色 / 任务 / 宿主机文案', () => {
  it('角色与任务状态中文化', () => {
    expect(roleText('admin')).toBe('管理员')
    expect(roleText('viewer')).toBe('只读用户')
    expect(taskStatusText('success')).toBe('成功')
    expect(taskStatusText('failed')).toBe('失败')
  })

  it('宿主机状态中文化', () => {
    expect(hostStatusText('online')).toBe('在线')
  })
})

describe('时间格式化', () => {
  it('空值与非法值不崩', () => {
    expect(fmtDateTime(null)).toBe('—')
    expect(fmtDateTime('not-a-date')).toBe('not-a-date')
  })

  it('日期与时间补零', () => {
    const d = new Date(2026, 0, 5, 3, 7, 9)
    expect(fmtDateTime(d)).toBe('2026-01-05 03:07:09')
    expect(fmtDate(d)).toBe('2026-01-05')
  })
})

describe('容量格式化', () => {
  it('fmtSizeGB 保留两位小数', () => {
    expect(fmtSizeGB(1.234)).toBe('1.23')
    expect(fmtSizeGB('abc')).toBe('0.00')
  })

  it('fmtSizeBytes 在 1024GB 进位到 TB', () => {
    expect(fmtSizeBytes(1024 ** 3)).toBe('1.0 GB')
    expect(fmtSizeBytes(1024 ** 4)).toBe('1.0 TB')
    expect(fmtSizeBytes(null)).toBe('—')
  })

  it('fmtFileSize 按 B/KB/MB 逐级进位', () => {
    expect(fmtFileSize(812)).toBe('812 B')
    expect(fmtFileSize(2048)).toBe('2.0 KB')
    expect(fmtFileSize(3 * 1024 * 1024)).toBe('3.0 MB')
  })

  it('fmtRateBytes 处理 0 与进位', () => {
    expect(fmtRateBytes(0)).toBe('0 B/s')
    expect(fmtRateBytes(1024)).toBe('1.0 KB/s')
    expect(fmtRateBytes(1024 * 1024)).toBe('1.0 MB/s')
  })

  it('fmtGBSum 自动进位', () => {
    expect(fmtGBSum([{ capacity_gb: 0.5 }])).toBe('512 MB')
    expect(fmtGBSum([{ capacity_gb: 120 }])).toBe('120 GB')
    expect(fmtGBSum([{ capacity_gb: 2048 }])).toBe('2.00 TB')
  })
})

describe('错误文案', () => {
  it('优先使用后端 message', () => {
    expect(errMsg({ response: { data: { message: '磁盘不足' } } }, '创建失败')).toBe('磁盘不足')
  })

  it('区分超时 / 服务异常 / 连不上', () => {
    expect(errMsg({ code: 'ECONNABORTED' }, '操作失败')).toBe('操作失败（请求超时）')
    expect(errMsg({ response: { status: 500 } }, '操作失败')).toBe('操作失败（服务异常 HTTP 500）')
    expect(errMsg({ request: {} }, '操作失败')).toBe('操作失败（无法连接服务器）')
    expect(errMsg(null, '操作失败')).toBe('操作失败')
  })

  it('isCancel 识别 Element Plus 的 cancel/close', () => {
    expect(isCancel('cancel')).toBe(true)
    expect(isCancel(new Error('close'))).toBe(true)
    expect(isCancel(new Error('boom'))).toBe(false)
  })
})

describe('百分比与配色', () => {
  it('usageColor 三档阈值', () => {
    expect(usageColor(10)).toBe('var(--color-success)')
    expect(usageColor(60)).toBe('var(--color-warning)')
    expect(usageColor(80)).toBe('var(--color-danger)')
  })

  it('clampPct 钳制到 0~100 整数', () => {
    expect(clampPct(-5)).toBe(0)
    expect(clampPct(150)).toBe(100)
    expect(clampPct(33.6)).toBe(34)
    expect(clampPct('abc')).toBe(0)
  })
})

describe('安装树 URL 校验', () => {
  it('接受 ftp/http/https 且带路径的地址', () => {
    expect(isValidInstallTreeUrl('ftp://mirror/rocky10/')).toBe(true)
    expect(isValidInstallTreeUrl('https://mirror/os/rocky/')).toBe(true)
    expect(isValidInstallTreeUrl('  http://host/path  ')).toBe(true)
  })

  it('拒绝缺 scheme、缺主机或缺路径的地址', () => {
    expect(isValidInstallTreeUrl('mirror/rocky/')).toBe(false)
    expect(isValidInstallTreeUrl('ftp://mirror')).toBe(false)
    expect(isValidInstallTreeUrl('ftp://mirror/')).toBe(false)
    expect(isValidInstallTreeUrl('')).toBe(false)
    expect(isValidInstallTreeUrl(null)).toBe(false)
  })
})
