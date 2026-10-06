<template>
  <div v-loading="loading && !firstLoading">
      <PageHead title="虚拟机管理" />
      <el-card shadow="never">
        <!-- 状态分布堆叠色条：按列表全量数据的各状态占比分段，随 5s 轮询联动刷新；
             图例 chip 点击写回 q.status（与下方状态下拉同一状态源），再点一次取消；
             列表为空（含首载骨架期）整块不渲染 -->
        <VmStatusBar v-if="statusDist.length" :dist="statusDist" :active="q.status" @toggle="toggleStatusFilter" />
        <!-- 原左分组为 gap 8px + flex-wrap，经 wrap 传入保持不变；计数为 .toolbar 直接子元素走默认插槽 -->
        <Toolbar wrap>
          <template #left>
            <!-- 刷新是次操作：default 描边（主操作「新建虚拟机」才用实底 primary） -->
            <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
            <el-button v-if="canOperate" type="primary" :icon="Plus" @click="router.push({ name: 'vm-create' })">新建虚拟机</el-button>
            <el-button v-if="canOperate" type="warning" plain :icon="Upload" @click="importDlg.open()">导入存量 VM</el-button>
            <!-- 批量操作条：勾选后出现；按选中状态智能禁用（全在运行时开机禁用、全已关机时关机禁用），
                 按钮统一 plain 弱化视觉，避免一排实底彩钮压过主操作 -->
            <el-divider v-if="canOperate && checked.length" direction="vertical" />
            <template v-if="canOperate && checked.length">
              <span class="bulk-count">{{ bulkBusy ? bulkProgress : `已选 ${checked.length} 台` }}</span>
              <!-- 批量电源：全关机→批量开机，全运行→批量关机；混合状态按钮禁用并提示分开操作
                   （混合时"批量开关机"没有单一语义，硬执行会既开机又关机） -->
              <el-button
                :icon="bulkPower === 'stop' ? SwitchButton : VideoPlay"
                :loading="bulkBusy" plain type="primary"
                :disabled="bulkPowerMixed"
                :title="bulkPowerMixed ? '选中虚拟机电源状态不一致，请分开勾选后操作' : ''"
                @click="bulkAction(bulkPower)"
              >{{ bulkPower === 'stop' ? '批量关机' : '批量开机' }}</el-button>
              <el-button
                type="danger" plain :icon="Delete" :loading="bulkBusy"
                @click="bulkAction('delete')"
              >批量删除</el-button>
              <el-button text :disabled="bulkBusy" @click="checked = []">取消选择</el-button>
            </template>
          </template>
          <span class="count">共 {{ total }} 台<span v-if="runningCount" class="running-hint"> · 运行中 {{ runningCount }} 台</span></span>
        </Toolbar>

        <!-- 筛选栏（JumpServer 式：关键词 + 状态） -->
        <div class="filter-bar">
          <el-input
            v-model="q.keyword"
            placeholder="搜索虚拟机名称"
            clearable
            :prefix-icon="Search"
            style="width: 240px"
          />
          <el-select v-model="q.status" placeholder="状态筛选" clearable style="width: 140px">
            <el-option label="运行中" value="running" />
            <el-option label="已关机" value="shut off" />
            <el-option label="已暂停" value="paused" />
            <el-option label="异常" value="error" />
          </el-select>
          <span v-if="isFiltered" class="filter-count">筛选出 {{ filteredItems.length }} 台</span>
        </div>

        <!-- 卡片网格（替代 el-table，对齐 KvmDash 卡片 + virt-manager 实时条）；
             筛选无结果与列表真空是两种空态，文案区分并提供「清除筛选」出口 -->
        <el-empty v-if="!filteredItems.length && !loading" :description="isFiltered ? '无匹配虚拟机' : '暂无虚拟机'" :image-size="80">
          <el-button v-if="isFiltered" :icon="Refresh" @click="clearFilters">清除筛选</el-button>
        </el-empty>
        <!-- 首载骨架（批④）：8 张形状匹配的占位卡；刷新仍走根级 v-loading 不闪骨架 -->
        <div v-if="firstLoading" class="vm-grid" aria-label="虚拟机列表加载中">
          <el-card v-for="i in 8" :key="i" shadow="never" class="vm-card skel-vm-card">
            <div class="skeleton-text" style="width: 45%"></div>
            <div class="skeleton-text" style="width: 28%; margin-bottom: 12px"></div>
            <div class="skeleton-block" style="height: 56px; margin-bottom: 12px"></div>
            <div class="skeleton-text" style="width: 60%"></div>
          </el-card>
        </div>
        <div v-else-if="filteredItems.length" class="vm-grid">
          <VmCard
            v-for="vm in filteredItems"
            :key="vm.id"
            :vm="vm"
            :perf="perfMap[vm.id] || null"
            :perf-at="perfAtMap[vm.id] || ''"
            :hist="histMap[vm.id]"
            :checked="canOperate && isChecked(vm)"
            :busy="busy.has(vm.id)"
            :can-operate="canOperate"
            @check="toggleCheck"
            @action="action"
            @console="openConsole"
          />
        </div>
      </el-card>

    <!-- 导入存量 VM（纳管 virsh 已有域）：弹窗内扫描/导入/失败明细自包含，导入成功后重载列表 -->
    <VmImportDialog ref="importDlg" @imported="load" @update:scanning="importScanning = $event" />

  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Plus, Upload, VideoPlay, SwitchButton, Delete, Search } from '@element-plus/icons-vue'
import { api } from '../api'
import { useAutoRefresh } from '../composables/useAutoRefresh'
import { useAuth } from '../store/auth'
import PageHead from '../components/PageHead.vue'
import Toolbar from '../components/Toolbar.vue'
import { pollTask, extractTaskId, taskErrorMessage } from '../utils/task.js'
import { nowClock, isCancel } from '../utils/format'
import VmCard from './vm/components/VmCard.vue'
import VmStatusBar from './vm/components/VmStatusBar.vue'
import VmImportDialog from './vm/components/VmImportDialog.vue'

const router = useRouter()
const { canOperate } = useAuth()

const items = ref([])
const total = ref(0)
const loading = ref(false)
// 首载标记（批④骨架屏）：只在首次 load 期间为 true，之后刷新走 v-loading 不再闪骨架
const firstLoading = ref(true)
const busy = ref(new Set())
const checked = ref([])
const bulkBusy = ref(false)
// 批量操作逐台进度文案（bulkBusy 期间替换「已选 X 台」显示，避免批量关机逐台等待时全程无反馈）
const bulkProgress = ref('')
// 批量电源按钮：选中状态唯一时给出确定动作（全关机→start / 全运行→stop）；
// 状态混合（含 paused/error 混入或运行关机并存）时无单一语义，禁用并提示分开操作
const bulkPower = computed(() => checked.value.some((r) => r.status === 'running') ? 'stop' : 'start')
const bulkPowerMixed = computed(() => new Set(checked.value.map((r) => r.status)).size > 1)
// 实时性能：vm id → {cpu_percent, mem_pct}，随列表静默刷新
const perfMap = ref({})
// 折线历史：vm id → {t: [], cpu: [], mem:[]}，上限 30 点（VmCard deep 监听重画）
const histMap = ref({})
// 每次成功采样的时间戳：vm id → 'HH:MM:SS'（LIVE 心跳证明）
const perfAtMap = ref({})
const HIST_MAX = 30
// 导入弹窗 ref（open() 打开并自动扫描）；scanning 态回传作静默轮询守卫
const importDlg = ref(null)
const importScanning = ref(false)

const runningCount = computed(() => items.value.filter((i) => i.status === 'running').length)

// 筛选：关键词（名称）+ 状态（客户端即时过滤）
// 初值取自路由 query（仪表盘环图下钻 /vms?status=running；监控页出口 /vms?keyword=<name>）
const route = useRoute()
const q = reactive({ keyword: String(route.query.keyword || ''), status: String(route.query.status || '') })
const isFiltered = computed(() => !!(q.keyword.trim() || q.status))
// 清除筛选：重置关键词与状态，回到全量列表（筛选空态的「清除筛选」按钮入口）
function clearFilters() {
  q.keyword = ''
  q.status = ''
}
const filteredItems = computed(() => {
  const kw = q.keyword.trim().toLowerCase()
  return items.value.filter((vm) => {
    if (q.status && vm.status !== q.status) return false
    if (kw && !(vm.name || '').toLowerCase().includes(kw)) return false
    return true
  })
})

/* ── 状态分布堆叠色条（质感专项）── */
// 段色一律走主题变量（禁止浅色专用 hex）：running=品牌青绿主色（答辩主视觉），
// 已关机=边框族加深档（--color-border 在浅色卡片上过淡、图例色点难辨认，取同族
// --color-border-strong 保证深浅色双模式可读），暂停/异常用语义色
const STATUS_SEGMENTS = [
  { status: 'running', label: '运行中', color: 'var(--color-primary)' },
  { status: 'shut off', label: '已关机', color: 'var(--color-border-strong)' },
  { status: 'paused', label: '已暂停', color: 'var(--color-warning)' },
  { status: 'error', label: '异常', color: 'var(--color-danger)' }
]
// 各状态计数 → 分段数据（0 计数段不渲染）；stopped 为早期列表接口的历史键，并入「已关机」口径
const statusDist = computed(() => {
  if (!items.value.length) return []
  const counts = {}
  for (const vm of items.value) {
    const st = vm.status === 'stopped' ? 'shut off' : vm.status
    counts[st] = (counts[st] || 0) + 1
  }
  const sum = items.value.length
  return STATUS_SEGMENTS.map((s) => ({ ...s, count: counts[s.status] || 0 }))
    .filter((s) => s.count > 0)
    .map((s) => ({ ...s, pct: Math.round((s.count / sum) * 100) }))
})
// 图例 chip 点击过滤：写回现有 q.status（与状态下拉同源），命中同一状态再点一次即取消
function toggleStatusFilter(status) {
  q.status = q.status === status ? '' : status
}

// 卡片多选（替代 el-table selection 列）
function isChecked(vm) {
  return checked.value.some((r) => r.id === vm.id)
}
function toggleCheck(vm, on) {
  if (on) {
    if (!isChecked(vm)) checked.value = [...checked.value, vm]
  } else {
    checked.value = checked.value.filter((r) => r.id !== vm.id)
  }
}

async function load() {
  loading.value = true
  try {
    const vms = await api.listVMs()
    items.value = (vms.data && vms.data.items) || []
    total.value = (vms.data && vms.data.total) || 0
    applyPerf((vms.data && vms.data.perf) || {})
  } catch (e) {
    ElMessage.error('获取虚拟机列表失败')
  } finally {
    loading.value = false
    firstLoading.value = false
  }
}

// 应用实时性能：后端 listVMs 已合并 perf（{id: {cpu_percent, mem_pct}}），无需第二次请求。
// hist 原地追加（上限 30 点），VmCard 对 hist prop 做 deep 监听自动重画
function applyPerf(perfObj) {
  const m = {}
  const tstr = nowClock()
  for (const [id, p] of Object.entries(perfObj || {})) {
    if (!p) continue
    m[id] = p
    perfAtMap.value[id] = tstr
    let h = histMap.value[id]
    if (!h) {
      h = { t: [], cpu: [], mem: [] }
      histMap.value[id] = h
    }
    h.t.push(tstr)
    h.cpu.push(Number((p.cpu_percent || 0).toFixed(1)))
    h.mem.push(Number((p.mem_pct || 0).toFixed(1)))
    if (h.t.length > HIST_MAX) {
      h.t.shift()
      h.cpu.shift()
      h.mem.shift()
    }
  }
  perfMap.value = m
}

// 静默轮询：刷新列表 + 实时性能（参考 KvmDash 5s 轮询）
async function silentRefresh() {
  if (busy.value.size || importScanning.value || bulkBusy.value) return
  try {
    const res = await api.listVMs()
    items.value = (res.data && res.data.items) || []
    total.value = (res.data && res.data.total) || 0
    // 剔除已不存在的勾选（删除后残留）
    if (checked.value.length) {
      const ids = new Set(items.value.map((i) => i.id))
      checked.value = checked.value.filter((r) => ids.has(r.id))
    }
    applyPerf((res.data && res.data.perf) || {})
  } catch (e) {
    // 忽略
  }
}

// 静默轮询收进 useAutoRefresh（周期取 vmlist 偏好，卸载自动停表）；
// 守卫逻辑留在 silentRefresh 内部（批量操作/导入扫描期间跳过刷新）
const { start: startPolling } = useAutoRefresh(silentRefresh, { intervalKey: 'vmlist' })

// 批量操作：start 同步直调（快接口）；stop/delete 逐台走后台任务（提交→poll→汇总）
async function bulkAction(type) {
  // 目标收敛：只作用于「勾选 ∩ 当前筛选结果」——被筛掉的机器对用户不可见，不应被批量波及
  const inFilter = new Set(filteredItems.value.map((r) => r.id))
  const scoped = checked.value.filter((r) => inFilter.has(r.id))
  if (!scoped.length) {
    ElMessage.warning('勾选的虚拟机不在当前筛选结果中，请调整或清除筛选')
    return
  }
  // 目标过滤：开机只对非 running 生效、关机只对 running 生效，避免对不适用机器白跑接口
  const rows = scoped.filter((r) => !busy.value.has(r.id))
    .filter((r) => (type === 'start' ? r.status !== 'running' : type === 'stop' ? r.status === 'running' : true))
  if (!rows.length) {
    ElMessage.warning(type === 'start' ? '选中的虚拟机均在运行中' : type === 'stop' ? '选中的虚拟机均已关机' : '请选择虚拟机')
    return
  }
  const label = { start: '批量开机', stop: '批量关机', delete: '批量删除' }[type]
  if (type === 'delete') {
    try {
      await ElMessageBox.confirm(
        `此操作不可撤销。确定删除选中的 ${rows.length} 台虚拟机（${rows.map((r) => r.name).join('、')}）？`,
        '确认批量删除',
        { type: 'warning', confirmButtonText: '确认删除', cancelButtonText: '取消', confirmButtonClass: 'el-button--danger' }
      )
    } catch (e) {
      return
    }
  }
  bulkBusy.value = true
  let ok = 0
  let fail = 0
  const failedNames = []
  for (let i = 0; i < rows.length; i++) {
    const vm = rows[i]
    bulkProgress.value = `正在${label}（${i + 1}/${rows.length}）：${vm.name}`
    try {
      if (type === 'start') {
        await api.startVM(vm.id)
      } else if (type === 'stop') {
        await pollTask(extractTaskId(await api.stopVM(vm.id)))
      } else {
        await pollTask(extractTaskId(await api.deleteVM(vm.id)))
      }
      ok++
    } catch (e) {
      fail++
      failedNames.push(vm.name)
    }
  }
  bulkBusy.value = false
  bulkProgress.value = ''
  checked.value = []
  // 失败时点名前 3 台（再多只报数量）：批量失败常见原因是某台任务超时/被锁，
  // 不点名的话用户得逐台翻任务列表才能定位是谁没成功
  const failDetail = failedNames.length
    ? `，失败 ${fail} 台（${failedNames.slice(0, 3).join('、')}${fail > 3 ? ` 等 ${fail} 台` : ''}）`
    : ''
  ElMessage[fail ? 'warning' : 'success'](`${label}完成：成功 ${ok} 台${failDetail}`)
  await load()
}

async function action(vm, type) {
  busy.value.add(vm.id)
  busy.value = new Set(busy.value)
  try {
    if (type === 'delete') {
      await ElMessageBox.prompt(
        '此操作不可撤销。请输入虚拟机名称「' + vm.name + '」以确认删除：',
        '确认删除',
        {
          type: 'warning',
          confirmButtonText: '确认删除',
          cancelButtonText: '取消',
          confirmButtonClass: 'el-button--danger',
          inputPlaceholder: vm.name,
          inputValidator: (v) => (v && v.trim() === vm.name) || '请输入正确的虚拟机名称'
        }
      )
      ElMessage.info('删除任务已提交，正在执行…')
      await pollTask(extractTaskId(await api.deleteVM(vm.id)))
      ElMessage.success('删除成功')
      await load()
    } else if (type === 'stop') {
      // 优雅关机走后台任务：根治同步 15s 撞 axios 超时的误报
      ElMessage.info('关机任务已提交，正在执行…')
      await pollTask(extractTaskId(await api.stopVM(vm.id)))
      ElMessage.success('关机成功')
      await load()
    } else {
      // start 为快接口，同步直调（restart 入口已收敛到详情页顶栏）
      await api[type + 'VM'](vm.id)
      ElMessage.success('开机指令已执行')
      await load()
    }
  } catch (e) {
    if (!isCancel(e)) {
      ElMessage.error(taskErrorMessage(e, '操作失败'))
    }
  } finally {
    busy.value.delete(vm.id)
    busy.value = new Set(busy.value)
  }
}

// “更多”下拉已删除：删除钮常驻，重启去详情页顶栏；action 兜底保留 restart 分支

async function openConsole(vm) {
  router.push({ name: 'console', params: { id: vm.id } })
}

// 进页面时从 Prometheus 预填各 VM 迷你曲线的历史（替代"从零攒点、刷新即失"）：
// 后端一次返回全部 VM 的序列（按名字分组），按名字映射到卡片 id；拉不到静默降级。
async function prefillVMHistories() {
  try {
    const res = await api.vmHistory(30)
    const vms = (res.data && res.data.vms) || {}
    const nameToId = {}
    for (const vm of items.value) nameToId[vm.name] = vm.id
    for (const [name, pts] of Object.entries(vms)) {
      const id = nameToId[name]
      if (!id || !pts.length) continue
      const recent = pts.slice(-HIST_MAX)
      histMap.value[id] = {
        t: recent.map((p) => p.t),
        cpu: recent.map((p) => p.cpu),
        mem: recent.map((p) => p.mem)
      }
    }
  } catch (e) {
    /* 静默降级 */
  }
}

onMounted(() => {
  load().then(prefillVMHistories)
  startPolling()
})
</script>

<style scoped>
/* .page-head / .page-title / .toolbar / .count / .mono 已收进 global.css；.toolbar-left 骨架与 gap/换行由 Toolbar 组件承担 */
.running-hint {
  color: var(--color-accent);
}
/* 筛选栏 */
.filter-bar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
}
.filter-count {
  font-size: 0.85rem;
  color: var(--color-muted-foreground);
}
/* VM 卡片网格（卡片本体样式在 VmCard.vue） */
.vm-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(330px, 1fr));
  gap: 16px;
  align-items: stretch; /* 同行卡片等高：运行中卡片内容多，其余卡片拉伸对齐 */
}
.vm-card {
  display: flex;
  flex-direction: column;
}
/* 骨架占位卡（批④）：不吃 hover 浮起 */
.skel-vm-card:hover {
  transform: none;
}
.bulk-count {
  font-size: 0.88rem;
  color: var(--el-color-primary);
  font-weight: 600;
}
</style>
