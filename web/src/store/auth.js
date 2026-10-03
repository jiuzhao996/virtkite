import { reactive, computed } from 'vue'

// token 的 localStorage 键：**定义在 store 而非 api**。
// 原先定义在 api/index.js，store 反向 import 它；一旦 api 需要 import store 的 logout()
// 就会构成 api ↔ store 循环依赖。token 本身属于会话状态，归 store 更自然，
// api/index.js 改为从这里 import 并原样 re-export（ConsolePage 等从 ../api 取 TOKEN_KEY 的写法不受影响）。
export const TOKEN_KEY = 'vmops_token'

const state = reactive({
  token: localStorage.getItem(TOKEN_KEY) || '',
  user: null
})

function setToken(token) {
  state.token = token
  if (token) localStorage.setItem(TOKEN_KEY, token)
  else localStorage.removeItem(TOKEN_KEY)
}

function setUser(user) {
  state.user = user
}

/**
 * 清空会话：内存 state 与 localStorage 一起清。
 * 401 拦截器必须调它（而不是只删 localStorage）——否则 state.token 残留导致
 * isLoggedIn 仍为 true，路由守卫「已登录访问 /login → 重定向 dashboard」会把
 * 用户从登录页弹回去，形成打转。
 */
export function logout() {
  setToken('')
  state.user = null
}

const isAdmin = computed(() => state.user && state.user.role === 'admin')
// canOperate：可操作虚拟机的角色（admin 全量 + operator=VM 全生命周期与三类控制台）。
// 与 isAdmin 分级对应后端 OperatorMiddleware：VM 操作看 canOperate，
// 平台管理（用户/审计/设置/宿主机/存储/网络/镜像的变更）看 isAdmin。
const canOperate = computed(() => {
  const r = state.user && state.user.role
  return r === 'admin' || r === 'operator'
})
const isLoggedIn = computed(() => !!state.token)

// 会话预取屏障（刷新竞态修复，S1-1）：整页刷新后 state.user 为 null，而路由守卫的
// 初始导航先于 App.vue onMounted 的 api.me() 返回——admin 直连 /vms/new 等
// requiresOperate 路由会被当成无角色弹回 dashboard。守卫改为先 await 本屏障：
// 有 token 且 user 未加载时补一次 me()（并发调用共享同一 Promise）。
// 401 由 axios 拦截器统一处理（清会话跳登录）；其它失败（网络抖动）静默返回，
// 守卫按未加载判定，行为与修复前一致（弹 dashboard），不会打转。
// 动态 import 断开 store → api 的静态依赖（token 键虽在本文件，api 仍 import 本文件，
// 静态反向引用会成环）。
let mePromise = null
export function ensureUserLoaded() {
  if (!state.token || state.user) return Promise.resolve()
  if (!mePromise) {
    mePromise = import('../api')
      .then((m) => m.api.me())
      .then((res) => {
        setUser(res.data)
      })
      .catch(() => { /* 401 拦截器已处理；其余静默见上 */ })
      .finally(() => {
        mePromise = null
      })
  }
  return mePromise
}

export function useAuth() {
  return {
    state,
    isAdmin,
    canOperate,
    isLoggedIn,
    setToken,
    setUser,
    logout
  }
}
