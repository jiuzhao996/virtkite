// useChart：ECharts 实例生命周期统一托管（S1-2 收敛批次）。
//
// 此前 7 个文件 10 处各自 echarts.init/resize/dispose，手法不一：window resize
// 监听有加有不加、dispose 有漏、Monitor 还踩过「v-show 容器 0 尺寸初始化」与
// 「ResizeObserver 闭包吞 resize」两个坑。本 composable 把生命周期收成一种写法：
//
//   const { chartRef, setOption, resize, getChart } = useChart()
//
// - init 惰性：首次 setOption 时才 init（容器 v-show 隐藏时不会以 0 尺寸建实例）
// - ResizeObserver（非 window.resize）：容器尺寸变化即触发，侧栏折叠/tab 切回/
//   抽屉开合全场景覆盖，且自动跟随 unmount 断开
// - dispose/监听清理随 onUnmounted 自动完成，页面只管 setOption
//
// 约定：容器由调用方通过 chartRef 绑定；多次 setOption 默认 notMerge=true（与既有
// 各页「整体重画」语义一致），需要增量合并的传第二参 false。
import { onUnmounted, ref, watch } from 'vue'
import echarts from '../utils/echarts'

export function useChart() {
  const chartRef = ref(null)
  let chart = null
  let ro = null

  // 容器被 v-if 卸载重建（如运行状态切换隐藏/重现图表区）后，模板会重绑新 el；
  // 旧实例仍持有已脱离 DOM 的 canvas，继续 setOption 会画进空处。检测 el 更替并重建。
  watch(chartRef, (el) => {
    if (chart && el && chart.getDom() !== el) {
      dispose()
    }
  })

  function ensureInit() {
    if (chart) return chart
    if (!chartRef.value) return null
    chart = echarts.init(chartRef.value)
    // ResizeObserver 单实例直连当前 chart（不复用闭包变量之外的旧引用），
    // observe 的元素卸载后由 disconnect 统一收尾
    ro = new ResizeObserver(() => chart && chart.resize())
    ro.observe(chartRef.value)
    return chart
  }

  function setOption(option, notMerge = true) {
    const c = ensureInit()
    if (!c) return
    c.setOption(option, notMerge)
  }

  function resize() {
    chart && chart.resize()
  }

  function getChart() {
    return chart
  }

  function dispose() {
    if (ro) {
      ro.disconnect()
      ro = null
    }
    if (chart) {
      chart.dispose()
      chart = null
    }
  }

  onUnmounted(dispose)

  return { chartRef, setOption, resize, getChart, dispose }
}
