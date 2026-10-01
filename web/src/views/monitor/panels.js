// 原生看板扩展层：面板注册表（「Grafana 模板位」的自研答案，2026-10 批次 4b）。
// 加一张监控面板 = 在 PANELS 里加一行声明，零后端改动——
// 数据走通用端点 POST /api/monitor/prom-query（query + minutes，operator+ 权限）。
//
// 字段：
//   id      面板唯一键（React key / 图表实例索引）
//   title   卡片标题
//   series  [{ name, expr }] 多序列同图（Promise.all 并行拉取后按 name 合图）
//   unit    'percent' → y 轴 0-100%；'bytes' → fmtRateBytes 刻度；缺省 = 原始数值
//   empty   无序列时的占位文案（如「暂无抓取目标」这类解释性提示）
//
// 注意：expr 是真实 PromQL，长度受后端 promQueryMaxLen=512 钳制；跨度过大时
// step 自动 15s，序列点数 = minutes×4，返回体积由后端 promQueryMaxSeries 截断兜底。
export const PANELS = [
  {
    id: 'tasks-queue',
    title: '任务队列深度',
    series: [
      { name: '等待中', expr: 'vmops_tasks_pending' },
      { name: '执行中', expr: 'vmops_tasks_running' }
    ],
    unit: 'count',
    empty: '暂无任务队列采样（提交一个异步任务后 here 会有曲线）'
  },
  {
    id: 'scrape-health',
    title: '抓取目标存活',
    series: [
      { name: '平台 exporter', expr: 'up{job="vmops"}' },
      { name: 'VM node_exporter', expr: 'up{job="vm-node"}' }
    ],
    unit: 'bool',
    empty: '暂无抓取数据（监控栈未启动，或 VM 内未安装 node_exporter）'
  },
  {
    id: 'running-trend',
    title: '运行虚拟机数趋势',
    series: [{ name: '运行中台数', expr: 'sum(vmops_vm_running)' }],
    unit: 'count',
    empty: '暂无采样（监控栈未启动或还没有 VM 运行过）'
  }
]
