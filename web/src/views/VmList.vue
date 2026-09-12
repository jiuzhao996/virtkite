<template>
  <div v-loading="loading">
      <div class="page-head">
        <h2 class="page-title">虚拟机管理</h2>
      </div>
      <el-card shadow="never">
        <div class="toolbar">
          <div class="toolbar-left">
            <el-button type="primary" :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
            <el-button v-if="isAdmin" type="primary" :icon="Plus" @click="router.push({ name: 'vm-create' })">新建虚拟机</el-button>
            <el-button v-if="isAdmin" type="warning" plain :icon="Upload" @click="openImport">导入存量 VM</el-button>
            <!-- 批量操作条：勾选后出现；按选中状态智能禁用（全在运行时开机禁用、全已关机时关机禁用），
                 按钮统一 plain 弱化视觉，避免一排实底彩钮压过主操作 -->
            <el-divider v-if="isAdmin && checked.length" direction="vertical" />
            <template v-if="isAdmin && checked.length">
              <span class="bulk-count">已选 {{ checked.length }} 台</span>
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
          </div>
          <span class="count">共 {{ total }} 台<span v-if="runningCount" class="running-hint"> · 运行中 {{ runningCount }} 台</span></span>
        </div>

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

        <!-- 卡片网格（替代 el-table，对齐 KvmDash 卡片 + virt-manager 实时条） -->
        <el-empty v-if="!filteredItems.length && !loading" description="暂无虚拟机" :image-size="80" />
        <div v-else class="vm-grid">
          <el-card
            v-for="vm in filteredItems"
            :key="vm.id"
            shadow="hover"
            class="vm-card"
            :class="{ selected: isChecked(vm), running: vm.status === 'running' }"
          >
            <div class="vm-head">
              <el-checkbox
                :model-value="isChecked(vm)"
                :disabled="busy.has(vm.id)"
                @change="toggleCheck(vm, $event)"
              />
              <span class="vm-name" :title="vm.name">{{ vm.name }}</span>
              <!-- 腾讯云式状态：圆点 + 文字，运行态呼吸灯 -->
              <span class="vm-status" :class="'st-' + (vm.status || 'unknown').replace(' ', '-')">
                <span class="status-dot" />
                {{ vmStatusText(vm.status) }}
              </span>
            </div>
            <div class="vm-meta">
              <span class="meta-item"><el-icon><Cpu /></el-icon>{{ vm.host ? vm.host.name : ('ID ' + vm.host_id) }}</span>
              <span class="meta-item"><el-icon><FolderOpened /></el-icon>{{ vm.storage_pool || '—' }}</span>
              <span class="meta-item mono" v-if="vm.ip"><el-icon><Connection /></el-icon>{{ vm.ip }}</span>
            </div>
            <div class="vm-spec">
              <span>{{ vm.vcpu }} 核</span>
              <el-divider direction="vertical" />
              <span>{{ (vm.memory_mb / 1024).toFixed(1) }} GB</span>
              <el-divider direction="vertical" />
              <span>{{ vm.disk_gb }} GB</span>
            </div>
            <div class="vm-perf" v-if="vm.status === 'running' && perfOf(vm)">
              <div class="perf-values">
                <span class="live-tag"><span class="live-dot" />实时</span>
                <span class="perf-val">CPU <b :style="{ color: usageColor(perfOf(vm).cpu_percent || 0) }">{{ (perfOf(vm).cpu_percent || 0).toFixed(1) }}%</b></span>
                <span class="perf-val">内存 <b :style="{ color: usageColor(perfOf(vm).mem_pct || 0) }">{{ (perfOf(vm).mem_pct || 0).toFixed(1) }}%</b></span>
                <span class="perf-time" v-if="perfAt(vm)">{{ perfAt(vm) }}</span>
              </div>
              <div class="spark" :ref="(el) => setSparkRef(vm.id, el)" />
            </div>
            <div class="vm-perf-idle" v-else-if="vm.status !== 'running'">
              <span class="idle-text">未运行，无实时指标</span>
            </div>
            <div class="vm-actions">
              <el-button size="small" :icon="Search" @click="router.push({ name: 'vm-detail', params: { id: vm.id } })">详情</el-button>
              <el-button
                v-if="isAdmin && vm.status !== 'running'"
                size="small"
                :icon="VideoPlay"
                :disabled="busy.has(vm.id)"
                @click="action(vm, 'start')"
              >开机</el-button>
              <el-button
                v-else-if="isAdmin"
                size="small"
                :icon="SwitchButton"
                :disabled="busy.has(vm.id)"
                @click="action(vm, 'stop')"
              >关机</el-button>
              <el-button size="small" :icon="Monitor" :disabled="vm.status !== 'running'" @click="openConsole(vm)">控制台</el-button>
              <!-- 删除常驻（原「更多」下拉悬浮突兀，重启去详情页操作）：删除有输入名称确认弹窗兜底 -->
              <el-button
                v-if="isAdmin"
                class="vm-delete"
                size="small"
                type="danger"
                plain
                :icon="Delete"
                :disabled="busy.has(vm.id)"
                :title="'删除 ' + vm.name"
                @click="moreAction(vm, 'delete')"
              />
            </div>
          </el-card>
        </div>
      </el-card>

    <!-- 导入存量 VM（纳管 virsh 已有域） -->
    <el-dialog v-model="importDialog" title="导入存量 VM" width="780px">
      <div v-loading="importScanning" class="import-body">
        <el-alert
          v-if="importHostName"
          type="info"
          :closable="false"
          show-icon
          :title="`宿主机「${importHostName}」共检测到 ${importTotal} 台域：已纳管 ${importManaged} 台，未纳管 ${importUnmanaged} 台`"
          style="margin-bottom: 12px"
        />
        <el-empty v-if="!importScanning && !unmanaged.length" description="暂无未纳管的存量 VM" />
        <el-table
          v-else
          :data="unmanaged"
          stripe
          border
          size="small"
          style="width: 100%"
          @selection-change="selected = $event"
        >
          <el-table-column type="selection" width="44" />
          <el-table-column prop="name" label="名称" min-width="130" />
          <el-table-column label="状态" width="90">
            <template #default="{ row }">
              <el-tag :type="vmStatusTag(row.state, 'primary')" effect="light">{{ vmStatusText(row.state) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="规格" width="150">
            <template #default="{ row }">{{ row.vcpu }}核 / {{ (row.memory_mb / 1024).toFixed(0) }}GB / {{ row.disk_gb }}GB</template>
          </el-table-column>
          <el-table-column prop="mac_address" label="MAC" width="150" />
          <el-table-column prop="disk_path" label="磁盘路径" min-width="220" show-overflow-tooltip />
        </el-table>
      </div>
      <template #footer>
        <el-button @click="importDialog = false">取消</el-button>
        <el-button type="primary" :disabled="!selected.length" :loading="importing" @click="doImport">
          导入所选（{{ selected.length }} 台）
        </el-button>
      </template>
    </el-dialog>

  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import * as echarts from 'echarts'
import { Refresh, Plus, Upload, VideoPlay, SwitchButton, Monitor, Delete, Search, Cpu, FolderOpened, Connection } from '@element-plus/icons-vue'
import { api } from '../api'
import { POLL_DEFAULTS, getPollInterval } from '../utils/settings'
import { useAuth } from '../store/auth'
import { pollTask, extractTaskId, taskErrorMessage } from '../utils/task.js'
import { vmStatusText, vmStatusTag, usageColor, nowClock, errMsg, isCancel, cssVar } from '../utils/format'

const router = useRouter()
const { isAdmin } = useAuth()

// echarts 不解析 var()，需要真实色值：挂载时读一次 CSS 变量（避免散落 hex）
const CHART_CPU_COLOR = cssVar('--el-color-primary', '#2a9da5')
const CHART_MEM_COLOR = cssVar('--color-warning', '#d97706')
const CHART_BASELINE_COLOR = cssVar('--color-border-strong', '#cbd5e1')

const items = ref([])
const total = ref(0)
const loading = ref(false)
const busy = ref(new Set())
const checked = ref([])
const bulkBusy = ref(false)
// 批量电源按钮：选中状态唯一时给出确定动作（全关机→start / 全运行→stop）；
// 状态混合（含 paused/error 混入或运行关机并存）时无单一语义，禁用并提示分开操作
const bulkPower = computed(() => checked.value.some((r) => r.status === 'running') ? 'stop' : 'start')
const bulkPowerMixed = computed(() => new Set(checked.value.map((r) => r.status)).size > 1)
// 实时性能：vm id → {cpu_percent, mem_pct}，随列表静默刷新
const perfMap = ref({})
// 折线历史：vm id → {t: [], cpu: [], mem: []}，上限 30 点
const histMap = ref({})
// 每次成功采样的时间戳：vm id → 'HH:MM:SS'（LIVE 心跳证明）
const perfAtMap = ref({})
const HIST_MAX = 30
// sparkline 图实例与容器：vm id → echarts 实例 / DOM
const sparkInsts = new Map()
const sparkEls = {}

const importDialog = ref(false)
const importScanning = ref(false)
const importing = ref(false)
const importHostId = ref(null)
const importHostName = ref('')
const importTotal = ref(0)
const importManaged = ref(0)
const importUnmanaged = ref(0)
const unmanaged = ref([])
const selected = ref([])

const runningCount = computed(() => items.value.filter((i) => i.status === 'running').length)

// 筛选：关键词（名称）+ 状态（客户端即时过滤）
const q = reactive({ keyword: '', status: '' })
const isFiltered = computed(() => !!(q.keyword.trim() || q.status))
const filteredItems = computed(() => {
  const kw = q.keyword.trim().toLowerCase()
  return items.value.filter((vm) => {
    if (q.status && vm.status !== q.status) return false
    if (kw && !(vm.name || '').toLowerCase().includes(kw)) return false
    return true
  })
})

function perfOf(row) {
  return perfMap.value[row.id] || null
}
function perfAt(row) {
  return perfAtMap.value[row.id] || ''
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
  }
}

// 应用实时性能：后端 listVMs 已合并 perf（{id: {cpu_percent, mem_pct}}），无需第二次请求
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
  nextTick(syncCharts)
}

// sparkline 容器 ref（v-for 回调式）
function setSparkRef(id, el) {
  if (el) {
    sparkEls[id] = el
  } else {
    delete sparkEls[id]
  }
}

// 同步迷你折线：新建/更新运行中卡片，销毁已消失或已关机的
function syncCharts() {
  const alive = new Set()
  for (const vm of items.value) {
    if (vm.status !== 'running' || !histMap.value[vm.id]) continue
    alive.add(vm.id)
    const el = sparkEls[vm.id]
    if (!el) continue
    let inst = sparkInsts.get(vm.id)
    if (!inst) {
      inst = echarts.init(el)
      sparkInsts.set(vm.id, inst)
    }
    const h = histMap.value[vm.id]
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
    inst.setOption(
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
  for (const [id, inst] of sparkInsts) {
    if (!alive.has(id)) {
      try {
        inst.dispose()
      } catch (e) {}
      sparkInsts.delete(id)
      delete histMap.value[id]
      delete perfAtMap.value[id]
    }
  }
}

function onWinResize() {
  for (const [, inst] of sparkInsts) {
    try {
      inst.resize()
    } catch (e) {}
  }
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

let pollTimer = null

async function openImport() {
  importDialog.value = true
  importScanning.value = true
  importHostName.value = ''
  importUnmanaged.value = 0
  unmanaged.value = []
  selected.value = []
  try {
    const res = await api.scanImportVMs()
    const data = (res && res.data) || {}
    importHostId.value = data.host_id || null
    importHostName.value = data.host_name || ''
    importTotal.value = data.total || 0
    importManaged.value = data.managed || 0
    importUnmanaged.value = data.unmanaged || 0
    unmanaged.value = ((data.items || []).filter((i) => !i.managed))
  } catch (e) {
    // 后端已统一为 {code, message, data}（handler 层禁止再泄漏 detail），走统一提取
    ElMessage.error(errMsg(e, '扫描失败，无法连接 libvirt'))
  } finally {
    importScanning.value = false
  }
}

async function doImport() {
  if (!selected.value.length) {
    ElMessage.warning('请先勾选要导入的虚拟机')
    return
  }
  importing.value = true
  try {
    const res = await api.importVMs(importHostId.value, selected.value.map((i) => i.name))
    const d = (res && res.data) || {}
    ElMessage.success(`导入完成：成功 ${d.imported} 台${d.skipped ? '，跳过(已纳管) ' + d.skipped + ' 台' : ''}${d.failed ? '，失败 ' + d.failed + ' 台' : ''}`)
    importDialog.value = false
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '导入失败'))
  } finally {
    importing.value = false
  }
}

// 批量操作：start 同步直调（快接口）；stop/delete 逐台走后台任务（提交→poll→汇总）
async function bulkAction(type) {
  // 目标过滤：开机只对非 running 生效、关机只对 running 生效，避免对不适用机器白跑接口
  const rows = checked.value.filter((r) => !busy.value.has(r.id))
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
        { type: 'warning', confirmButtonText: '确认删除', cancelButtonText: '取消' }
      )
    } catch (e) {
      return
    }
  }
  bulkBusy.value = true
  let ok = 0
  let fail = 0
  for (const vm of rows) {
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
    }
  }
  bulkBusy.value = false
  checked.value = []
  ElMessage[fail ? 'warning' : 'success'](`${label}完成：成功 ${ok} 台${fail ? '，失败 ' + fail + ' 台' : ''}`)
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
      // start / restart 为快接口，保持同步直调
      await api[type + 'VM'](vm.id)
      const label = { start: '开机', restart: '重启' }[type]
      ElMessage.success(label + '指令已执行')
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

// “更多”下拉统一入口：重启/快照/XML/删除
function moreAction(vm, cmd) {
  if (cmd === 'restart' || cmd === 'delete') action(vm, cmd)
}

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
    nextTick(syncCharts)
  } catch (e) {
    /* 静默降级 */
  }
}

onMounted(() => {
  load().then(prefillVMHistories)
  pollTimer = setInterval(silentRefresh, getPollInterval('vmlist', POLL_DEFAULTS.vmlist))
  window.addEventListener('resize', onWinResize)
})
onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
  window.removeEventListener('resize', onWinResize)
  for (const [, inst] of sparkInsts) {
    try {
      inst.dispose()
    } catch (e) {}
  }
  sparkInsts.clear()
})
</script>

<style scoped>
/* .page-head / .page-title / .toolbar / .count / .mono 已收进 global.css */
.toolbar-left {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
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
/* VM 卡片网格 */
.vm-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(330px, 1fr));
  gap: 16px;
  align-items: stretch; /* 同行卡片等高：运行中卡片内容多，其余卡片拉伸对齐 */
}
.vm-card {
  display: flex;
  flex-direction: column;
  transition: border-color 0.2s ease, box-shadow 0.2s ease;
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
/* 腾讯云式状态徽标：圆点 + 文字 */
.vm-status {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 0.82rem;
  font-weight: 600;
  padding: 3px 10px;
  border-radius: 999px;
  white-space: nowrap;
  background: #f1f5f9;
  color: #64748b;
}
.vm-status .status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: currentcolor;
  flex-shrink: 0;
}
.vm-status.st-running {
  background: #ecfdf5;
  color: #16a34a;
}
.vm-status.st-running .status-dot {
  animation: breathe 1.6s ease-in-out infinite;
}
.vm-status.st-paused {
  background: #fffbeb;
  color: #d97706;
}
.vm-status.st-error {
  background: #fef2f2;
  color: #dc2626;
}
.vm-status.st-shut-off,
.vm-status.st-stopped {
  background: #f1f5f9;
  color: #64748b;
}
@keyframes breathe {
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
  font-size: 0.72rem;
  font-weight: 700;
  color: #16a34a;
  letter-spacing: 0.5px;
}
.live-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #16a34a;
  animation: breathe 1.6s ease-in-out infinite;
}
.perf-time {
  margin-left: auto;
  font-size: 0.72rem;
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
.bulk-count {
  font-size: 0.88rem;
  color: var(--el-color-primary);
  font-weight: 600;
}
</style>
