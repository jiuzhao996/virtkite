<template>
  <el-card
    shadow="hover"
    class="vm-card"
    :class="{ selected: canOperate && checked, running: vm.status === 'running' }"
  >
    <div class="vm-head">
      <!-- viewer 只读：不给勾选框也不给选中高亮（批量操作属操作员/管理员） -->
      <el-checkbox
        v-if="canOperate"
        :model-value="checked"
        :disabled="busy"
        @change="$emit('check', vm, $event)"
      />
      <span class="vm-name" :title="vm.name">{{ vm.name }}</span>
      <!-- 腾讯云式状态：圆点 + 文字，运行态呼吸灯 -->
      <span class="vm-status" :class="statusClass(vm.status)">
        <span class="status-dot" />
        {{ vmStatusText(vm.status) }}
      </span>
    </div>
    <div class="vm-meta">
      <span class="meta-item"><el-icon><Cpu /></el-icon>{{ vm.host ? vm.host.name : ('ID ' + vm.host_id) }}</span>
      <span class="meta-item"><el-icon><FolderOpened /></el-icon>{{ vm.storage_pool || '—' }}</span>
      <!-- IP 可点复制（CopyDocument 小图标示意可点；剪贴板 API 失败降级报错提示） -->
      <span v-if="vm.ip" class="meta-item mono ip-copy" title="点击复制 IP" @click="copyIP(vm.ip)">
        <el-icon><Connection /></el-icon>{{ vm.ip }}
        <el-icon class="ip-copy-icon"><CopyDocument /></el-icon>
      </span>
    </div>
    <div class="vm-spec">
      <span>{{ vm.vcpu }} 核</span>
      <el-divider direction="vertical" />
      <span>{{ (vm.memory_mb / 1024).toFixed(1) }} GB</span>
      <el-divider direction="vertical" />
      <span>{{ vm.disk_gb }} GB</span>
    </div>
    <div class="vm-perf" v-if="vm.status === 'running' && perf">
      <div class="perf-values">
        <span class="live-tag"><span class="live-dot" />实时</span>
        <span class="perf-val">CPU <b :style="{ color: usageColor(perf.cpu_percent || 0) }">{{ (perf.cpu_percent || 0).toFixed(1) }}%</b></span>
        <span class="perf-val">内存 <b :style="{ color: usageColor(perf.mem_pct || 0) }">{{ (perf.mem_pct || 0).toFixed(1) }}%</b></span>
        <span class="perf-time" v-if="perfAt">{{ perfAt }}</span>
      </div>
      <div class="spark" :ref="chartRef" />
    </div>
    <div class="vm-perf-idle" v-else-if="vm.status !== 'running'">
      <div class="idle-meta">
        <div class="idle-chips">
          <span class="idle-chip" v-if="vm.created_at">建机 {{ String(vm.created_at).slice(0, 10) }}</span>
          <span class="idle-chip idle-desc" v-if="vm.description" :title="vm.description">{{ vm.description.length > 18 ? vm.description.slice(0, 17) + '…' : vm.description }}</span>
        </div>
        <span class="idle-text">开机后显示实时指标与曲线</span>
      </div>
    </div>
    <div class="vm-actions">
      <el-button size="small" :icon="View" @click="router.push({ name: 'vm-detail', params: { id: vm.id } })">详情</el-button>
      <el-button
        v-if="canOperate && vm.status !== 'running'"
        size="small"
        :icon="VideoPlay"
        :disabled="busy"
        @click="$emit('action', vm, 'start')"
      >开机</el-button>
      <el-button
        v-else-if="canOperate"
        size="small"
        :icon="SwitchButton"
        :disabled="busy"
        @click="$emit('action', vm, 'stop')"
      >关机</el-button>
      <el-button size="small" :icon="Monitor" :disabled="vm.status !== 'running'" @click="$emit('console', vm)">控制台</el-button>
      <!-- 删除常驻（原「更多」下拉悬浮突兀，重启去详情页操作）：删除有输入名称确认弹窗兜底；
           icon-only 必须带 tooltip + aria-label（ui-ux-pro-max §1） -->
      <el-tooltip :content="'删除 ' + vm.name" placement="top">
        <el-button
          v-if="canOperate"
          class="vm-delete"
          size="small"
          type="danger"
          plain
          :icon="Delete"
          :disabled="busy"
          :aria-label="'删除 ' + vm.name"
          @click="$emit('action', vm, 'delete')"
        />
      </el-tooltip>
    </div>
  </el-card>
</template>

<script setup>
// VM 卡片（从 VmList 拆出）：卡片网格单元，含实时性能 sparkline。
// 曲线实例由 useChart 托管（init 惰性 / ResizeObserver 自动跟随 / el 被运行态 v-if
// 重建时自动重绑）；历史数据 hist 由父级积累后传入（原地追加 → deep 监听重画）。
import { watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { VideoPlay, SwitchButton, Monitor, Delete, View, CopyDocument, Cpu, FolderOpened, Connection } from '@element-plus/icons-vue'
import { useChart } from '../../../composables/useChart'
import { vmStatusText, usageColor, cssVar } from '../../../utils/format'
import { copyText } from '../../../utils/clipboard'

const props = defineProps({
  vm: { type: Object, required: true },
  perf: { type: Object, default: null }, // 实时采样 {cpu_percent, mem_pct}，非运行/无采样为 null
  perfAt: { type: String, default: '' }, // 最近采样时刻 'HH:MM:SS'（LIVE 心跳证明）
  hist: { type: Object, default: undefined }, // 折线历史 {t, cpu, mem}（父级原地追加，上限 30 点）
  checked: { type: Boolean, default: false },
  busy: { type: Boolean, default: false },
  canOperate: { type: Boolean, default: false }
})

defineEmits(['check', 'action', 'console'])

const router = useRouter()

// echarts 不解析 var()，需要真实色值：挂载时读一次 CSS 变量（避免散落 hex）
const CHART_CPU_COLOR = cssVar('--el-color-primary', '#2a9da5')
// 内存曲线与仪表盘同色（--color-success 绿）；原 --color-warning 橙与 CPU 阈值色混淆
const CHART_MEM_COLOR = cssVar('--color-success', '#16a34a')
const CHART_BASELINE_COLOR = cssVar('--color-border-strong', '#cbd5e1')

const { chartRef, setOption } = useChart()

// hist 由父级 applyPerf 原地 push/shift（对象同一引用），deep 监听捕捉逐点追加；
// immediate 覆盖「父级 prefill 已带历史」的首渲染。运行态切换由 v-if 卸载/重建容器，
// useChart 的 el 重绑监听负责重建实例。
watch(
  () => props.hist,
  () => renderSpark(),
  { deep: true, immediate: true }
)

function renderSpark() {
  const h = props.hist
  if (!h || !h.t.length || props.vm.status !== 'running') return
  // 动态 Y 上限：max(20, 数据峰值*1.25)，0 基线保留，小波动可见（绝对值看上方文字）
  let peak = 0
  for (const v of h.cpu) if (v > peak) peak = v
  for (const v of h.mem) if (v > peak) peak = v
  const yMax = Math.max(20, Math.ceil(peak * 1.25))
  // 零基线：与数据等宽的有界虚线（不用无限 markLine，避免比数据线宽、端点小球残留）
  const baseMark =
    h.t.length >= 2
      ? [
          {
            silent: true,
            symbol: ['none', 'none'],
            data: [
              [
                { xAxis: h.t[0], yAxis: 0 },
                { xAxis: h.t[h.t.length - 1], yAxis: 0 }
              ]
            ],
            lineStyle: { color: CHART_BASELINE_COLOR, type: 'dashed', width: 1 }
          }
        ]
      : []
  setOption(
    {
      // 迷你图禁用 tooltip（数值看上方文字），避免悬停圆点 5s 更新后残留
      tooltip: { show: false },
      grid: { left: 4, right: 8, top: 6, bottom: 4, containLabel: false },
      xAxis: { type: 'category', boundaryGap: false, data: h.t, show: false },
      yAxis: { type: 'value', min: 0, max: yMax, show: false },
      series: [
        {
          name: 'CPU',
          type: 'line',
          smooth: true,
          showSymbol: false,
          data: h.cpu,
          lineStyle: { width: 1.5, color: CHART_CPU_COLOR },
          areaStyle: { opacity: 0.12, color: CHART_CPU_COLOR },
          // 零基线参考：动态 Y 下锚定 0，避免噪声误读为负载
          markLine: baseMark[0] || { silent: true, symbol: ['none', 'none'], data: [] }
        },
        {
          name: '内存',
          type: 'line',
          smooth: true,
          showSymbol: false,
          data: h.mem,
          lineStyle: { width: 1.5, color: CHART_MEM_COLOR },
          areaStyle: { opacity: 0.12, color: CHART_MEM_COLOR }
        }
      ]
    },
    true
  )
}

function statusClass(status) {
  return 'st-' + (status || 'unknown').replace(' ', '-')
}

// 卡片 IP 一键复制：navigator.clipboard 仅在安全上下文可用（localhost / HTTPS），
// 失败（http 部署 / 权限拒绝）降级为错误提示，不让点击无响应
async function copyIP(ip) {
  // http 部署（非安全上下文）时 navigator.clipboard 不可用，utils/clipboard 内置 execCommand 降级
  const ok = await copyText(ip)
  if (ok) ElMessage.success('已复制')
  else ElMessage.error('复制失败，请手动复制')
}
</script>

<style scoped>
.vm-card {
  display: flex;
  flex-direction: column;
  transition: transform var(--dur-base) var(--ease-standard), border-color var(--dur-base) var(--ease-standard), box-shadow var(--dur-base) var(--ease-standard);
}
/* 批③：悬停轻浮起（-2px，与 Console 选择卡同档）；选中态描边优先于浮起观感 */
.vm-card:not(.selected):hover {
  transform: translateY(-2px);
}
/* el-card body 撑满卡片，让 actions margin-top:auto 生效（按钮行贴底对齐） */
.vm-card :deep(.el-card__body) {
  display: flex;
  flex-direction: column;
  flex: 1;
}
.vm-card.selected {
  border-color: var(--el-color-primary);
  box-shadow: 0 0 0 1px var(--el-color-primary);
}
.vm-card.running {
  border-top: 3px solid var(--color-accent);
}
.vm-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}
.vm-name {
  flex: 1;
  font-size: 1rem;
  font-weight: 700;
  color: var(--color-foreground);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
/* 腾讯云式状态徽标：圆点 + 文字（前景色走 token 等值替换；浅底色无对应 token 保留原值） */
.vm-status {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 0.82rem;
  font-weight: 600;
  padding: 3px 10px;
  border-radius: 999px;
  white-space: nowrap;
  background: var(--status-off-bg);
  color: var(--color-info);
}
.vm-status .status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: currentcolor;
  flex-shrink: 0;
}
.vm-status.st-running {
  background: var(--status-running-bg);
  color: var(--color-success);
}
.vm-status.st-running .status-dot {
  animation: breathe-ring 1.6s ease-in-out infinite;
}
.vm-status.st-paused {
  background: var(--status-paused-bg);
  color: var(--color-warning);
}
.vm-status.st-error {
  background: var(--status-error-bg);
  color: var(--color-danger);
}
.vm-status.st-shut-off,
.vm-status.st-stopped {
  background: var(--status-off-bg);
  color: var(--color-info);
}
@keyframes breathe-ring {
  0%, 100% { opacity: 1; box-shadow: 0 0 0 0 rgba(22, 163, 74, 0.4); }
  50% { opacity: 0.65; box-shadow: 0 0 0 4px rgba(22, 163, 74, 0); }
}
.vm-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 16px;
  margin-bottom: 10px;
}
.meta-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 0.82rem;
  color: var(--color-muted-foreground);
}
/* IP 行可点复制：小图标示意可点性，hover 提示主色 */
.ip-copy {
  cursor: pointer;
}
.ip-copy:hover {
  color: var(--el-color-primary);
}
.ip-copy-icon {
  font-size: 0.9em;
  opacity: 0.6;
}
.vm-spec {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 0.85rem;
  color: var(--color-foreground);
  margin-bottom: 12px;
}
.vm-perf {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 12px;
}
.perf-values {
  display: flex;
  align-items: center;
  gap: 16px;
  font-size: 0.8rem;
  color: var(--color-muted-foreground);
  line-height: 1.5;
}
.perf-val {
  display: inline-flex;
  align-items: baseline;
  gap: 4px;
}
.perf-val b {
  font-family: var(--font-mono);
  font-weight: 700;
}
/* LIVE 心跳：证明 5s 轮询在工作 */
.live-tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 0.75rem;
  font-weight: 700;
  color: var(--color-success);
  letter-spacing: 0.5px;
}
.live-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--color-success);
  animation: breathe 1.6s ease-in-out infinite; /* 全局纯透明度版（global.css） */
}
.perf-time {
  margin-left: auto;
  font-size: 0.75rem;
  color: var(--color-muted-foreground);
  font-family: var(--font-mono);
}
.spark {
  width: 100%;
  height: 64px;
}
.vm-perf-idle {
  /* 与 .vm-perf（值行 + 64px 曲线）等高：未运行卡片占位撑起同样高度，保证所有卡片高度一致 */
  height: 92px;
  margin-bottom: 12px;
  display: flex;
  align-items: center;
}
/* 关机卡片不再空白：系统/建机时间/描述摘要元信息（信息密度优化 2026-10） */
.idle-meta {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
}
.idle-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.idle-chip {
  font-size: 0.75rem;
  color: var(--color-muted-foreground);
  background: var(--el-fill-color-light);
  border-radius: var(--radius-sm);
  padding: 2px 8px;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.idle-text {
  font-size: 0.8rem;
  color: var(--color-muted-foreground);
}
.vm-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding-top: 12px;
  border-top: 1px solid var(--color-border);
  margin-top: auto;
}
/* 删除钮右对齐独立：危险动作与常规操作分离（放不下时也单独成行靠右） */
.vm-delete {
  margin-left: auto;
}
</style>
