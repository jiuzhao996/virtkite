import { reactive, computed } from 'vue'
import { TOKEN_KEY } from '../api'

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

function logout() {
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
    logout
  }
}
