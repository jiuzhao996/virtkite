// task.js 单测：task_id 提取、错误文案归一、轮询状态机。
// 轮询部分用 vi.mock 顶掉 api，避免真实请求。
import { describe, it, expect, vi, beforeEach } from 'vitest'

const { getTask } = vi.hoisted(() => ({ getTask: vi.fn() }))
vi.mock('../api/index.js', () => ({ api: { getTask } }))

import { extractTaskId, taskErrorMessage, pollTask } from './task'

describe('extractTaskId', () => {
  it('支持 data.task_id 与顶层 task_id 两种形态', () => {
    expect(extractTaskId({ data: { task_id: 7 } })).toBe(7)
    expect(extractTaskId({ task_id: 'abc' })).toBe('abc')
  })

  it('缺失时抛出后端 message，没有 message 用 fallback', () => {
    expect(() => extractTaskId({ message: '磁盘不足' })).toThrow('磁盘不足')
    expect(() => extractTaskId({})).toThrow('提交任务失败，未返回 task_id')
    expect(() => extractTaskId(null)).toThrow('提交任务失败，未返回 task_id')
  })

  it('task_id 为空串/0 视为缺失（0 不是合法主键）', () => {
    // 0 在 JS 里是 falsy，当前实现按缺失处理——锁定该行为，避免有人改成 ?? 后静默提交 0
    expect(() => extractTaskId({ data: { task_id: 0 } })).toThrow()
    expect(() => extractTaskId({ data: { task_id: '' } })).toThrow()
  })
})

describe('taskErrorMessage', () => {
  it('优先 axios 响应里的后端 message', () => {
    expect(taskErrorMessage({ response: { data: { message: '凭据无效' } } }, '失败')).toBe('凭据无效')
  })

  it('其次 pollTask 抛出的普通 Error message', () => {
    expect(taskErrorMessage(new Error('任务超时'), '失败')).toBe('任务超时')
  })

  it('axios 自身错误不把英文原文透给用户', () => {
    const axiosErr = new Error('Network Error')
    axiosErr.request = {}
    expect(taskErrorMessage(axiosErr, '失败')).toBe('失败')
  })

  it('字符串错误原样返回，其余走 fallback', () => {
    expect(taskErrorMessage('磁盘不足', '失败')).toBe('磁盘不足')
    expect(taskErrorMessage(null, '失败')).toBe('失败')
  })
})

describe('pollTask 状态机', () => {
  beforeEach(() => getTask.mockReset())

  it('缺任务 ID 直接拒绝，不发请求', async () => {
    await expect(pollTask('')).rejects.toThrow('缺少任务 ID')
    expect(getTask).not.toHaveBeenCalled()
  })

  it('success 立即返回任务对象', async () => {
    getTask.mockResolvedValueOnce({ data: { id: 1, status: 'success', result: 'ok' } })
    await expect(pollTask(1, { interval: 0 })).resolves.toMatchObject({ status: 'success' })
  })

  it('failed 抛出任务 error 字段', async () => {
    getTask.mockResolvedValueOnce({ data: { status: 'failed', error: '磁盘不足' } })
    await expect(pollTask(1, { interval: 0 })).rejects.toThrow('磁盘不足')
  })

  it('failed 无 error 时用兜底文案', async () => {
    getTask.mockResolvedValueOnce({ data: { status: 'failed' } })
    await expect(pollTask(1, { interval: 0 })).rejects.toThrow('任务失败')
  })

  it('非终态回调 onProgress，终态后停止轮询', async () => {
    getTask
      .mockResolvedValueOnce({ data: { status: 'pending' } })
      .mockResolvedValueOnce({ data: { status: 'running', progress: 50 } })
      .mockResolvedValueOnce({ data: { status: 'success' } })
    const seen = []
    const task = await pollTask(1, { interval: 0, onProgress: (t) => seen.push(t.status) })
    expect(task.status).toBe('success')
    expect(seen).toEqual(['pending', 'running'])
    expect(getTask).toHaveBeenCalledTimes(3)
  })

  it('onProgress 抛错不影响轮询', async () => {
    getTask.mockResolvedValueOnce({ data: { status: 'running' } }).mockResolvedValueOnce({ data: { status: 'success' } })
    await expect(
      pollTask(1, {
        interval: 0,
        onProgress: () => {
          throw new Error('回调炸了')
        },
      })
    ).resolves.toMatchObject({ status: 'success' })
  })

  it('超时抛「任务超时」', async () => {
    getTask.mockResolvedValue({ data: { status: 'running' } })
    await expect(pollTask(1, { interval: 0, timeout: 0 })).rejects.toThrow('任务超时')
  })
})
