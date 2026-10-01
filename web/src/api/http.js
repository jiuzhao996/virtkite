import axios from 'axios'
// 依赖方向刻意单向：api → store。TOKEN_KEY 与 logout 都由 store/auth.js 持有，
// store 不再 import api，避免循环依赖（详见 store/auth.js 顶部注释）。
import { TOKEN_KEY, logout } from '../store/auth'

// 普通接口 15s 超时；镜像上传等大流量接口单独覆盖（见 uploadConfig）
export const http = axios.create({
  baseURL: '/api',
  timeout: 15000
})

// 镜像上传专用配置：timeout 置 0（axios 语义为不限时）。
// 上传体积可达数 GB，任何固定超时都是错的——15s 全局值必然在传输中途中断请求，
// 后端明明还在正常接收，前端却报「超时」。改为不限时 + onUploadProgress 给用户反馈。
const UPLOAD_TIMEOUT = 0

/**
 * 构造上传请求配置。
 * @param {(percent:number, loaded:number, total:number)=>void} [onProgress] 进度回调，percent 为 0~100 整数
 */
export function uploadConfig(onProgress) {
  const cfg = {
    headers: { 'Content-Type': 'multipart/form-data' },
    timeout: UPLOAD_TIMEOUT
  }
  if (typeof onProgress === 'function') {
    cfg.onUploadProgress = (e) => {
      // total 在部分浏览器/代理下可能缺失（chunked），此时只回传已传字节
      const total = e.total || 0
      const percent = total ? Math.min(100, Math.round((e.loaded / total) * 100)) : 0
      onProgress(percent, e.loaded || 0, total)
    }
  }
  return cfg
}

http.interceptors.request.use((cfg) => {
  const t = localStorage.getItem(TOKEN_KEY)
  if (t) cfg.headers.Authorization = 'Bearer ' + t
  return cfg
})

http.interceptors.response.use(
  (res) => res,
  (err) => {
    // 登录接口本身的 401（口令错误）不算会话失效：既不清已有会话，也不跳转，
    // 由 Login.vue 自行展示错误文案。
    const isLoginCall = !!(err.config && String(err.config.url || '').includes('/auth/login'))
    if (err.response && err.response.status === 401 && !isLoginCall) {
      // 必须调 store 的 logout()：同时清 state.token（内存）与 localStorage。
      // 只删 localStorage 会让 isLoggedIn 仍为 true，路由守卫把用户从 /login 弹回 dashboard。
      logout()
      // 触发全局未授权：跳转登录（已在登录页则不重复写 hash）
      if (window.location.hash !== '#/login') window.location.hash = '#/login'
    }
    return Promise.reject(err)
  }
)

// 统一解包：剥掉 axios 一层，返回后端统一结构 { code, message, data }（业务 data 仍需 .data 取）
export function unwrap(promise) {
  return promise.then((res) => res.data)
}
