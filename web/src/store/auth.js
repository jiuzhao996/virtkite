import { reactive, computed } from 'vue'

// token 的 localStorage 键：**定义在 store 而非 api**。
// 原先定义在 api/index.js，store 反向 import 它；一旦 api 需要 import store 的 logout()
// 就会构成 api ↔ store 循环依赖。token 本身属于会话状态，归 store 更自然，
// api/index.js 改为从这里 import 并原样 re-export（ConsolePage 等从 ../api 取 TOKEN_KEY 的写法不受影响）。
export const TOKEN_KEY = 'vmops_token'

const state = reactive({
  token: localStorage.getItem(TOKEN_KEY) || '',
  user: null,
  // 顶栏动态页标题：详情页（如 VmDetail）拿到实体后写入，离开路由时清空。
  // MainLayout 的 title 优先显示它，为空时回退到按路径映射的静态标题。
  pageTitle: ''
})

// setPageTitle 设置/清除顶栏动态标题（传空串清除）。
function setPageTitle(t) {
  state.pageTitle = t || ''
}

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
const isLoggedIn = computed(() => !!state.token)

export function useAuth() {
  return {
    state,
    isAdmin,
    isLoggedIn,
    setToken,
    setUser,
    setPageTitle,
    logout
  }
}
