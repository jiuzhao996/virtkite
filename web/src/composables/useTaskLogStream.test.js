// useTaskLogStream 单测：任务日志流的首屏/增量/票据/去重契约。
//
// 这个 composable 是从 1101 行的 TaskList.vue 抽出来的（含 WS 重连、一次性票据、
// seq 去重），此前只有 E2E 间接覆盖。这里用 mock api + 假 WebSocket 把语义钉住。
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

const { getTaskLogs, mintTicket } = vi.hoisted(() => ({
  getTaskLogs: vi.fn(),
  mintTicket: vi.fn(),
}))
vi.mock('../api', () => ({
  api: { getTaskLogs, mintTaskLogTicket: mintTicket },
}))

import { useTaskLogStream } from './useTaskLogStream'

class FakeWS {
  static instances = []
  constructor(url) {
    this.url = url
    this.readyState = 0
    FakeWS.instances.push(this)
  }
  send() {}
  close() {
    this.readyState = 3
    this.onclose?.()
  }
  open() {
    this.readyState = 1
    this.onopen?.()
  }
  msg(obj) {
    this.onmessage?.({ data: JSON.stringify(obj) })
  }
  die() {
    this.readyState = 3
    this.onclose?.()
  }
}

const flush = async () => {
  for (let i = 0; i < 5; i++) await Promise.resolve()
}

describe('useTaskLogStream', () => {
  beforeEach(() => {
    FakeWS.instances = []
    vi.stubGlobal('WebSocket', FakeWS)
    mintTicket.mockResolvedValue({ data: { ticket: 'tk' } })
    getTaskLogs.mockReset()
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('活动任务：先拉全量再连 WS，since 取最大 seq，增量按 seq 去重', async () => {
    getTaskLogs.mockResolvedValue({
      data: { structured: true, entries: [{ seq: 1, text: 'a' }, { seq: 2, text: 'b' }] },
    })
    const { entries, structured, live, open } = useTaskLogStream()
    await open({ id: 7, status: 'running' })
    await flush()

    expect(structured.value).toBe(true)
    expect(entries.value.map((e) => e.seq)).toEqual([1, 2])

    const ws = FakeWS.instances[0]
    expect(ws, '活动任务应连 WS').toBeTruthy()
    expect(ws.url, 'URL 必须带 since=最大 seq').toContain('since=2')
    expect(ws.url).toContain('ticket=tk')

    ws.open()
    ws.msg({ type: 'ready' })
    expect(live.value).toBe(true)
    // 增量与首屏重叠：seq 2 不重复，seq 3 追加
    ws.msg({ type: 'entries', entries: [{ seq: 2, text: 'b' }, { seq: 3, text: 'c' }] })
    expect(entries.value.map((e) => e.seq)).toEqual([1, 2, 3])
  })

  it('终态任务：只拉首屏，不连 WS', async () => {
    getTaskLogs.mockResolvedValue({ data: { structured: true, entries: [{ seq: 1, text: 'done' }] } })
    const { open } = useTaskLogStream()
    await open({ id: 8, status: 'success' })
    await flush()

    expect(FakeWS.instances).toHaveLength(0)
  })

  it('alwaysConnect：轮询已结束但仍连 WS（安装日志场景）', async () => {
    getTaskLogs.mockResolvedValue({ data: { structured: false, entries: [] } })
    const { open } = useTaskLogStream()
    await open({ id: 9, status: 'success' }, { alwaysConnect: true })
    await flush()

    expect(FakeWS.instances).toHaveLength(1)
  })

  it('首屏无结构化日志时，WS 首批 entries 到达后自动切结构化面板', async () => {
    getTaskLogs.mockResolvedValue({ data: { structured: false, entries: [] } })
    const { structured, open } = useTaskLogStream()
    await open({ id: 10, status: 'running' })
    await flush()

    expect(structured.value).toBe(false)
    FakeWS.instances[0].msg({ type: 'entries', entries: [{ seq: 1, text: 'x' }] })
    expect(structured.value).toBe(true)
  })

  it('取票失败：静默放弃，不建连也不抛错', async () => {
    getTaskLogs.mockResolvedValue({ data: { structured: true, entries: [] } })
    mintTicket.mockRejectedValue(new Error('403'))
    const { open } = useTaskLogStream()

    await expect(open({ id: 11, status: 'running' })).resolves.toBeUndefined()
    await flush()
    expect(FakeWS.instances).toHaveLength(0)
  })

  it('onAppend 回调：每次真正追加条目后触发（供调用方滚到底）', async () => {
    getTaskLogs.mockResolvedValue({ data: { structured: true, entries: [{ seq: 1, text: 'a' }] } })
    const onAppend = vi.fn()
    const { open } = useTaskLogStream(onAppend)
    await open({ id: 12, status: 'running' })
    await flush()
    expect(onAppend).toHaveBeenCalledTimes(1)

    const ws = FakeWS.instances[0]
    ws.msg({ type: 'entries', entries: [{ seq: 1, text: 'a' }] }) // 重复，不追加
    expect(onAppend).toHaveBeenCalledTimes(1)
    ws.msg({ type: 'entries', entries: [{ seq: 2, text: 'b' }] })
    expect(onAppend).toHaveBeenCalledTimes(2)
  })

  it('切换任务：上一个任务的迟到响应被丢弃', async () => {
    let resolveFirst
    getTaskLogs
      .mockImplementationOnce(() => new Promise((r) => (resolveFirst = r)))
      .mockResolvedValueOnce({ data: { structured: true, entries: [{ seq: 9, text: 'second' }] } })

    const { entries, open } = useTaskLogStream()
    const first = open({ id: 13, status: 'success' })
    await open({ id: 14, status: 'success' })
    resolveFirst({ data: { structured: true, entries: [{ seq: 1, text: 'first' }] } })
    await first
    await flush()

    expect(entries.value.map((e) => e.text)).toEqual(['second'])
  })
})
