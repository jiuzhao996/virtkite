// echarts 按需引入统一入口（对应全量版 `import * as echarts from 'echarts'`）。
//
// 为什么不用全量包：全量入包后 vendor-echarts chunk 1126kB（gzip 378kB），
// 必然触发 vite 500kB 告警；按需注册后 echarts 366kB + zrender 181kB
// （gzip 合计 186kB），体积降约一半，且单个 chunk 均低于 500kB。
//
// 注册清单 = 各使用方 setOption 配置的并集，
// **新增图表特性时必须来这里补注册**，否则运行时静默不渲染（构建不报错）：
// - LineChart            Dashboard/VmList/VmDetail/Monitor 的曲线图（series.type === 'line'，含 areaStyle 面积）
// - GraphChart           StorageList 克隆家谱血缘图（series.type === 'graph'，force 布局）
// - GridComponent        曲线图都配了 grid（直角坐标系）
// - TooltipComponent     Dashboard/VmDetail 轴触发 tooltip；VmList 配了 tooltip:{show:false}；家谱节点 tooltip
// - LegendComponent      Dashboard 主机大盘、VmDetail 性能曲线的图例
// - MarkLineComponent    VmList sparkline 的零基线虚线（series.markLine）
// - LegacyGridContainLabel  echarts 6 的 grid.containLabel 兼容特性（见下）
// - CanvasRenderer       Canvas 渲染器（按需模式必须显式注册，否则白屏）
//
// 未用到故未注册（用到再补）：dataZoom / title / pie / bar / tree / graphic 渐变等。
import * as echarts from 'echarts/core'
import { LineChart, GraphChart } from 'echarts/charts'
import {
  GridComponent,
  TooltipComponent,
  LegendComponent,
  MarkLineComponent
} from 'echarts/components'
// echarts 6 起 grid.containLabel 归入兼容特性（Dashboard/VmDetail 都配了 containLabel:true，
// 不注册则坐标轴标签可能溢出裁切，并打印 ECharts 警告）
import { LegacyGridContainLabel } from 'echarts/features'
import { CanvasRenderer } from 'echarts/renderers'

echarts.use([
  LineChart,
  GraphChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  MarkLineComponent,
  LegacyGridContainLabel,
  CanvasRenderer
])

export default echarts
