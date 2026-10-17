// useShellChrome：应用外壳（shell）的通用交互——深色模式、侧栏折叠、全屏切换。
//
// 从 layout/MainLayout.vue 抽出。这三块逻辑的共同点：
//   - 都与具体业务无关，任何页面/布局都可能用到；
//   - 都依赖浏览器 API + localStorage，且都要在隐私模式（localStorage 抛异常）下降级；
//   - MainLayout 因此从 173 行脚本瘦身，布局代码不再混排「主题怎么存」这种细节。
//
// 用法：
//   const { isDark, toggleTheme, isFullscreen, toggleFullscreen, collapsed, toggleSidebar } = useShellChrome()
//
// 语义约定（与原 MainLayout 逐字节兼容）：
//   - 主题键 vmops-theme：'dark'/'light'，初值取 html.dark（main.js 启动时已应用同键）
//   - 折叠键 vmops-sidebar-collapsed：'1'/'0'，读侧「=== '1' 才折叠」
//   - localStorage 不可用时：主题/折叠仅本次会话生效（watch 里 catch 静默）
//   - 全屏无持久化：投屏演示用，刷新不保留状态
import { ref, watch, onUnmounted, getCurrentInstance } from 'vue'

const THEME_KEY = 'vmops-theme'
const COLLAPSED_KEY = 'vmops-sidebar-collapsed'

/** 安全写 localStorage：隐私模式/配额满时静默降级（不抛异常打断交互） */
function safeSet(key, val) {
  try {
    localStorage.setItem(key, val)
  } catch (_e) {
    /* 隐私模式或存储不可用：仅本次会话生效 */
  }
}

export function useShellChrome() {
  // ── 深色模式：初值取自 html.dark，与 main.js 启动初始化同一约定 ──
  const isDark = ref(document.documentElement.classList.contains('dark'))
  function toggleTheme() {
    isDark.value = !isDark.value
    document.documentElement.classList.toggle('dark', isDark.value)
    safeSet(THEME_KEY, isDark.value ? 'dark' : 'light')
  }

  // ── 侧栏折叠：ref 读初始值，watch 写回（刷新后保持上次选择）──
  const collapsed = ref(localStorage.getItem(COLLAPSED_KEY) === '1')
  watch(collapsed, (v) => safeSet(COLLAPSED_KEY, v ? '1' : '0'))
  function toggleSidebar() {
    collapsed.value = !collapsed.value
  }

  // ── 全屏切换（演示投屏用）：文档级 fullscreen，无持久化 ──
  const isFullscreen = ref(!!document.fullscreenElement)
  function toggleFullscreen() {
    if (document.fullscreenElement) {
      document.exitFullscreen()
    } else {
      document.documentElement.requestFullscreen()
    }
  }
  // 用户按 ESC / F11 退出时同步状态，避免按钮 aria-label 与实际不符
  const onFullscreenChange = () => {
    isFullscreen.value = !!document.fullscreenElement
  }
  document.addEventListener('fullscreenchange', onFullscreenChange)
  // 在 setup 作用域内调用时组件卸载自动摘监听；直接调用（非 setup）时跳过
  if (getCurrentInstance()) {
    onUnmounted(() => document.removeEventListener('fullscreenchange', onFullscreenChange))
  }

  return {
    isDark,
    toggleTheme,
    collapsed,
    toggleSidebar,
    isFullscreen,
    toggleFullscreen,
  }
}
