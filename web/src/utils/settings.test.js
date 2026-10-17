// settings.js 单测：轮询间隔偏好的读写与 1s~60s 钳制。
import { describe, it, expect, beforeEach } from 'vitest'
import { getPollInterval, setPollInterval, POLL_DEFAULTS, POLL_LABELS } from './settings'

describe('轮询间隔偏好', () => {
  beforeEach(() => localStorage.clear())

  it('未设置时返回默认值', () => {
    expect(getPollInterval('vmlist', 5000)).toBe(5000)
  })

  it('写入后可读回', () => {
    setPollInterval('vmlist', 8000)
    expect(getPollInterval('vmlist', 5000)).toBe(8000)
  })

  it('写入时钳制到 1000~60000', () => {
    expect(setPollInterval('tasks', 10)).toBe(1000)
    expect(setPollInterval('tasks', 999999)).toBe(60000)
    expect(setPollInterval('tasks', 'abc')).toBe(1000) // Number('abc')||0 → 0 → 钳到 1000
  })

  it('存储里是脏值时回退默认值', () => {
    localStorage.setItem('vmops-poll-dashboard', 'oops')
    expect(getPollInterval('dashboard', 3000)).toBe(3000)
    localStorage.setItem('vmops-poll-dashboard', '150')
    expect(getPollInterval('dashboard', 3000)).toBe(1000) // 读侧同样钳制
  })

  it('默认值表与标签表键集合一致', () => {
    expect(Object.keys(POLL_DEFAULTS).sort()).toEqual(Object.keys(POLL_LABELS).sort())
  })
})
