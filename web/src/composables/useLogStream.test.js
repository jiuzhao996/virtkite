// useLogStream 单测：日志流的两处协议约束。
//
// 锁住的都是真实缺陷（2026-10-19 补 Docker 域时发现）：
//  1) 服务端没有 since 游标，每次连接都重放 tail 行 —— 重连不清空就会整段重复；
//  2) 服务端发 eof（容器停止）后仍会触发 onclose —— 不区分就会无限重连。
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

const { wsTicket } = vi.hoisted(() => ({ wsTicket: vi.fn() }))
vi.mock('../api', () => ({ api: { dockerContainerWsTicket: wsTicket } }))

import { useLogStream } from './useLogStream'

// 可控的假 WebSocket：测试里手动触发 open/message/close
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
  // 测试辅助
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

/** 让 connect() 里的 await 取票落地（fake timers 下不能用 setTimeout 刷新） */
const flush = async () => {
  await Promise.resolve()
  await Promise.resolve()
}

describe('useLogStream', () => {
  beforeEach(() => {
    FakeWS.instances = []
    vi.stubGlobal('WebSocket', FakeWS)
    wsTicket.mockResolvedValue({ data: { ticket: 't1' } })
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('重连时清空已收日志，避免服务端重放导致整段重复', async () => {
    const { lines, start } = useLogStream()
    start('c1', { tail: 500 })
    await flush()

    const ws1 = FakeWS.instances[0]
    expect(ws1, '首次应建立 WS').toBeTruthy()
    ws1.open()
    ws1.msg({ type: 'ready' })
    ws1.msg({ type: 'log', stream: 'stdout', data: 'line-A' })
    expect(lines.value).toHaveLength(1)

    // 未 eof 的异常断开 → 退避重连（1s）
    ws1.die()
    await vi.advanceTimersByTimeAsync(1100)
    await flush()

    const ws2 = FakeWS.instances[1]
    expect(ws2, '应发起重连').toBeTruthy()
    ws2.open()
    ws2.msg({ type: 'ready' })
    // 服务端重放 tail：同样的 line-A 再发一次
    ws2.msg({ type: 'log', stream: 'stdout', data: 'line-A' })

    expect(lines.value, '重连后不应保留旧行（否则重复）').toHaveLength(1)
    expect(lines.value[0].data).toBe('line-A')
  })

  it('服务端 eof 后不再重连（容器已停止，流应结束）', async () => {
    const { status, start } = useLogStream()
    start('c2')
    await flush()

    const ws1 = FakeWS.instances[0]
    ws1.open()
    ws1.msg({ type: 'ready' })
    ws1.msg({ type: 'eof' })
    expect(status.value).toBe('closed')

    // 服务端随 eof 关闭连接
    ws1.die()
    await vi.advanceTimersByTimeAsync(5000)
    await flush()

    expect(FakeWS.instances, 'eof 后不得再建连').toHaveLength(1)
    expect(status.value).toBe('closed')
  })

  it('已 live 后网络异常断开也会重连（而非直接判定结束）', async () => {
    const { start } = useLogStream()
    start('c3')
    await flush()

    const ws1 = FakeWS.instances[0]
    ws1.open()
    ws1.msg({ type: 'ready' })
    ws1.die() // 无 eof 的闪断
    await vi.advanceTimersByTimeAsync(1100)
    await flush()

    expect(FakeWS.instances.length, '闪断后应重连').toBe(2)
  })
})
