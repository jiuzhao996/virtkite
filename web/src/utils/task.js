import { api } from '../api/index.js'

// 终态：后端任务 status 只有 pending / running / success / failed
const SUCCESS = 'success'
const FAILED = 'failed'

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

/**
 * 从提交返回中提取 task_id。
 * 后端统一响应经 unwrap 后为 {code, message, data}，异步接口 data 为 {task_id}。
 * 缺失时抛出后端 message，便于调用方统一展示。
 */
export function extractTaskId(res, fallback = '提交任务失败，未返回 task_id') {
  const taskId = (res && res.data && res.data.task_id) || (res && res.task_id)
  if (taskId === undefined || taskId === null || taskId === '') {
    throw new Error((res && res.message) || fallback)
  }
  return taskId
}

/**
 * 统一错误文案：优先后端 message（提交失败），其次任务 error（轮询失败透出的 Error message），最后 fallback。
 * 用 request/code 排除 axios 自身错误，避免把英文网络错误原文展示给用户。
 */
export function taskErrorMessage(e, fallback = '操作失败') {
  if (e && e.response && e.response.data && e.response.data.message) {
    return e.response.data.message
  }
  if (e instanceof Error && !e.request && !e.code && e.message) {
    return e.message
  }
  if (typeof e === 'string' && e) {
    return e
  }
  return fallback
}

/**
 * 轮询 GET /api/tasks/:id 直到终态。
 * success → resolve(task)；failed → reject(Error(task.error || '任务失败'))；超时 → reject(Error('任务超时'))。
 * 非终态每次返回后调用 onProgress(task)，调用方可据此更新进度条 / 按钮文字。
 *
 * @param {number|string} taskId 任务 ID
 * @param {{interval?: number, timeout?: number, onProgress?: (task: object) => void}} opts
 * @returns {Promise<object>} 终态为 success 的任务对象
 */
export async function pollTask(taskId, opts = {}) {
  if (taskId === undefined || taskId === null || taskId === '') {
    throw new Error('缺少任务 ID')
  }
  const { interval = 2000, timeout = 300000, onProgress = null } = opts
  const deadline = Date.now() + timeout
  for (;;) {
    const res = await api.getTask(taskId)
    const task = (res && res.data) || {}
    if (task.status === SUCCESS) {
      return task
    }
    if (task.status === FAILED) {
      throw new Error(task.error || '任务失败')
    }
    if (Date.now() >= deadline) {
      throw new Error('任务超时')
    }
    if (typeof onProgress === 'function') {
      try {
        onProgress(task)
      } catch (_) {
        // 进度回调异常不影响轮询
      }
    }
    await sleep(interval)
    if (Date.now() >= deadline) {
      throw new Error('任务超时')
    }
  }
}
