// useAutoRefresh：自动刷新（轮询）统一 composable
// 收敛原先分散在各视图的「localStorage 开关 + setInterval + onUnmounted 清理」四份变体：
//   - DockerList 容器视图 10s 静默轮询（vmops-docker-autorefresh 开关）
//   - DockerList 日志抽屉跟随轮询（开关由 logsFollow/logsDrawer 两个 watch 外部控制）
//   - VmDetail 性能轮询（无开关，周期取 vmstats 偏好）
//   - Monitor 告警轮询（无开关，周期取 dashboard 偏好）
//
// 用法：
//   const { enabled, toggle, start, stop } = useAutoRefresh(fn, {
//     storageKey: 'vmops-docker-autorefresh', // localStorage 开关键；缺省 = 不持久化、无用户开关
//     defaultEnabled: true,                   // 无存储值时的默认开关态（仅 storageKey 生效时相关）
//     intervalMs: 10000,                      // 固定周期（ms）
//     intervalKey: 'dashboard',               // 或从系统设置读周期：getPollInterval(key, POLL_DEFAULTS[key])
//     runOnEnable: true,                      // 开启开关时是否立即执行一次 fn 再起定时器
//   })
//
// 语义约定：
//   - start() 只起表不触发 fn（保持 VmDetail/Monitor「挂载后等第一个周期」的原行为）；重复 start 幂等（先停旧表）
//   - 通过 enabled/toggle 切换开关立即生效：关 = 停表；开 = 按 runOnEnable 决定是否先执行一次 fn 再起表
//   - enabled 的持久化格式沿用既有约定：'1'/'0'，读侧「非 '0' 即开」（与 DockerList 原实现逐字节兼容）
//   - 组件卸载自动停表（composable 在 setup 作用域调用时注册 onUnmounted）
import { ref, watch, onUnmounted, getCurrentInstance } from 'vue'
import { getPollInterval, POLL_DEFAULTS } from '../utils/settings'

export function useAutoRefresh(fn, options = {}) {
  const {
    storageKey = '',
    defaultEnabled = true,
    intervalMs = 0,
    intervalKey = '',
    runOnEnable = true
  } = options

  // 周期在挂载时解析一次，与原实现一致（不随设置页改动热更新）
  const ms = intervalKey ? getPollInterval(intervalKey, POLL_DEFAULTS[intervalKey]) : intervalMs

  function readStoredEnabled() {
    try {
      const raw = localStorage.getItem(storageKey)
      return raw === null ? defaultEnabled : raw !== '0'
    } catch (e) {
      return defaultEnabled
    }
  }

  const enabled = ref(storageKey ? readStoredEnabled() : defaultEnabled)

  let timer = null

  function stop() {
    if (timer) {
      clearInterval(timer)
      timer = null
    }
  }

  function start() {
    if (!enabled.value || !ms) return
    stop() // 幂等：重复 start 先清旧表，等价于原 startLogsTimer 的 stop-then-set
    timer = setInterval(fn, ms)
  }

  // 开关切换立即生效：关=停表；开=按 runOnEnable 先执行一次再起表。
  // 只有声明了 storageKey（存在用户开关）时才需要监听，纯 start/stop 场景不挂 watcher。
  if (storageKey) {
    watch(enabled, (on) => {
      try {
        localStorage.setItem(storageKey, on ? '1' : '0')
      } catch (e) {
        /* 隐私模式等写失败不致命，仅本次不记忆 */
      }
      if (!on) {
        stop()
        return
      }
      if (runOnEnable && ms) fn()
      start()
    })
  }

  // setup 作用域调用时注册卸载清理；万一在组件外使用则跳过（由调用方自行 stop）
  if (getCurrentInstance()) onUnmounted(stop)

  function toggle(v) {
    enabled.value = typeof v === 'boolean' ? v : !enabled.value
  }

  return { enabled, toggle, start, stop }
}
