// useAutoRefresh 单测：定时器生命周期与开关持久化。
//
// 该 composable 收敛了原先分散在 4 个视图里的轮询实现，风险集中在两处：
//  1) 重复 start 导致定时器叠加（每秒请求次数翻倍）；
//  2) 组件卸载未停表（页面切走后仍在后台请求，长会话下越积越多）。
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { defineComponent, h, nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import { useAutoRefresh } from './useAutoRefresh'

describe('useAutoRefresh', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    localStorage.clear()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('start 按周期调用，stop 后不再调用', () => {
    const fn = vi.fn()
    const { start, stop } = useAutoRefresh(fn, { intervalMs: 1000 })

    start()
    vi.advanceTimersByTime(3000)
    expect(fn).toHaveBeenCalledTimes(3)

    stop()
    vi.advanceTimersByTime(3000)
    expect(fn).toHaveBeenCalledTimes(3)
  })

  it('重复 start 幂等：不叠加定时器', () => {
    const fn = vi.fn()
    const { start } = useAutoRefresh(fn, { intervalMs: 1000 })

    start()
    start()
    start()
    vi.advanceTimersByTime(2000)
    expect(fn).toHaveBeenCalledTimes(2)
  })

  it('intervalMs 为 0 时不起表（无有效周期）', () => {
    const fn = vi.fn()
    const { start } = useAutoRefresh(fn, { intervalMs: 0 })

    start()
    vi.advanceTimersByTime(10_000)
    expect(fn).not.toHaveBeenCalled()
  })

  it('开关关闭：停表并写入 0；重新开启：立即执行一次再起表', async () => {
    const fn = vi.fn()
    const { start, toggle } = useAutoRefresh(fn, {
      intervalMs: 1000,
      storageKey: 'test-autorefresh'
    })

    start()
    vi.advanceTimersByTime(1000)
    expect(fn).toHaveBeenCalledTimes(1)

    toggle(false)
    await nextTick()
    expect(localStorage.getItem('test-autorefresh')).toBe('0')
    vi.advanceTimersByTime(3000)
    expect(fn).toHaveBeenCalledTimes(1)

    toggle(true)
    await nextTick()
    expect(localStorage.getItem('test-autorefresh')).toBe('1')
    expect(fn).toHaveBeenCalledTimes(2) // runOnEnable 默认 true：立即跑一次
    vi.advanceTimersByTime(2000)
    expect(fn).toHaveBeenCalledTimes(4)
  })

  it('runOnEnable=false 时重新开启不立即执行', async () => {
    const fn = vi.fn()
    const { toggle } = useAutoRefresh(fn, {
      intervalMs: 1000,
      storageKey: 'test-autorefresh-off',
      defaultEnabled: false,
      runOnEnable: false
    })

    await nextTick()
    toggle(true)
    await nextTick()
    expect(fn).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1000)
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('读取已持久化的关闭态：默认不启动', () => {
    localStorage.setItem('test-autorefresh-saved', '0')
    const fn = vi.fn()
    const { enabled, start } = useAutoRefresh(fn, {
      intervalMs: 1000,
      storageKey: 'test-autorefresh-saved'
    })

    expect(enabled.value).toBe(false)
    start()
    vi.advanceTimersByTime(3000)
    expect(fn).not.toHaveBeenCalled()
  })

  it('组件卸载自动停表', () => {
    const fn = vi.fn()
    const Comp = defineComponent({
      setup() {
        const { start } = useAutoRefresh(fn, { intervalMs: 1000 })
        start()
        return () => h('div')
      }
    })

    const wrapper = mount(Comp)
    vi.advanceTimersByTime(1000)
    expect(fn).toHaveBeenCalledTimes(1)

    wrapper.unmount()
    vi.advanceTimersByTime(5000)
    expect(fn).toHaveBeenCalledTimes(1)
  })
})
