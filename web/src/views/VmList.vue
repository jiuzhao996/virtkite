<template>
  <div v-loading="loading">
      <div class="page-head">
        <h2 class="page-title">虚拟机管理</h2>
      </div>
      <el-card shadow="never">
        <div class="toolbar">
          <div class="toolbar-left">
            <el-button type="primary" :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
            <el-button type="success" :icon="Plus" @click="router.push({ name: 'vm-create' })">新建虚拟机</el-button>
            <el-button type="warning" :icon="Upload" @click="openImport">导入存量 VM</el-button>
            <el-divider v-if="checked.length" direction="vertical" />
            <template v-if="checked.length">
              <el-button size="default" type="success" :icon="VideoPlay" :loading="bulkBusy" @click="bulkAction('start')">批量开机 ({{ checked.length }})</el-button>
              <el-button size="default" type="warning" :icon="SwitchButton" :loading="bulkBusy" @click="bulkAction('stop')">批量关机 ({{ checked.length }})</el-button>
              <el-button size="default" type="danger" :icon="Delete" :loading="bulkBusy" @click="bulkAction('delete')">批量删除 ({{ checked.length }})</el-button>
            </template>
          </div>
          <span class="count">共 {{ total }} 台<span v-if="runningCount" class="running-hint"> · 运行中 {{ runningCount }} 台</span></span>
        </div>

        <!-- 卡片网格（替代 el-table，对齐 KvmDash 卡片 + virt-manager 实时条） -->
        <el-empty v-if="!items.length && !loading" description="暂无虚拟机" :image-size="80" />
        <div v-else class="vm-grid">
          <el-card
            v-for="vm in items"
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
                {{ statusText(vm.status) }}
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
                <span class="perf-val">CPU <b :style="{ color: barColor(perfOf(vm).cpu_percent || 0) }">{{ (perfOf(vm).cpu_percent || 0).toFixed(1) }}%</b></span>
                <span class="perf-val">内存 <b :style="{ color: barColor(perfOf(vm).mem_pct || 0) }">{{ (perfOf(vm).mem_pct || 0).toFixed(1) }}%</b></span>
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
                v-if="vm.status !== 'running'"
                size="small"
                :icon="VideoPlay"
                :disabled="busy.has(vm.id)"
                @click="action(vm, 'start')"
              >开机</el-button>
              <el-button
                v-else
                size="small"
                :icon="SwitchButton"
                :disabled="busy.has(vm.id)"
                @click="action(vm, 'stop')"
              >关机</el-button>
              <el-button size="small" :icon="Monitor" :disabled="vm.status !== 'running'" @click="openConsole(vm)">控制台</el-button>
              <el-dropdown trigger="click" @command="(cmd) => moreAction(vm, cmd)">
                <el-button size="small">
                  更多<el-icon class="el-icon--right"><ArrowDown /></el-icon>
                </el-button>
                <template #dropdown>
                  <el-dropdown-menu>
                    <el-dropdown-item command="restart" :icon="RefreshRight" :disabled="vm.status !== 'running' || busy.has(vm.id)">重启</el-dropdown-item>
                    <el-dropdown-item command="delete" :icon="Delete" divided :disabled="busy.has(vm.id)">删除</el-dropdown-item>
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
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
              <el-tag :type="statusTag(row.state)" effect="light">{{ statusText(row.state) }}</el-tag>
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
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import * as echarts from 'echarts'
import { Refresh, Plus, Upload, VideoPlay, SwitchButton, RefreshRight, Monitor, Delete, Search, ArrowDown, Cpu, FolderOpened, Connection } from '@element-plus/icons-vue'
import { api } from '../api'
import { pollTask, extractTaskId, taskErrorMessage } from '../utils/task.js'

const router = useRouter()

const items = ref([])
const total = ref(0)
const loading = ref(false)
const busy = ref(new Set())
const checked = ref([])
const bulkBusy = ref(false)
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

function perfOf(row) {
  return perfMap.value[row.id] || null
}
function perfAt(row) {
  return perfAtMap.value[row.id] || ''
}
function barColor(p) {
  if (p >= 80) return '#dc2626'
  if (p >= 60) return '#d97706'
  return '#16a34a'
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

function statusText(s) {
  return { running: '运行中', stopped: '已关机', 'shut off': '已关机', paused: '已暂停', error: '异常' }[s] || s
}
function statusTag(s) {
  if (s === 'running') return 'success'
  if (s === 'paused') return 'warning'
  if (s === 'error') return 'danger'
  if (s === 'shut off' || s === 'stopped') return 'info'
  return 'primary'
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
  const tstr = new Date().toLocaleTimeString('zh-CN', { hour12: false })
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
              lineStyle: { color: '#cbd5e1', type: 'dashed', width: 1 }
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
            lineStyle: { width: 1.5, color: '#2a9da5' },
            areaStyle: { opacity: 0.12, color: '#2a9da5' },
            // 零基线参考：动态 Y 下锚定 0，避免噪声误读为负载
            markLine: baseMark[0] || { silent: true, symbol: ['none', 'none'], data: [] }
          },
          {
            name: '内存',
            type: 'line',
            smooth: true,
            showSymbol: false,
            data: h.mem,
            lineStyle: { width: 1.5, color: '#d97706' },
            areaStyle: { opacity: 0.12, color: '#d97706' }
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
    ElMessage.error((e.response && e.response.data && e.response.data.detail) || '扫描失败，无法连接 libvirt')
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
    ElMessage.error((e.response && e.response.data && e.response.data.message) || '导入失败')
  } finally {
    importing.value = false
  }
}

// 批量操作：start 同步直调（快接口）；stop/delete 逐台走后台任务（提交→poll→汇总）
async function bulkAction(type) {
  const rows = checked.value.filter((r) => !busy.value.has(r.id))
  if (!rows.length) {
    ElMessage.warning('请选择虚拟机')
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
    if (e !== 'cancel' && e?.message !== 'cancel') {
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

onMounted(() => {
  load()
  pollTimer = setInterval(silentRefresh, 5000)
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
.page-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-xl);
}
.page-title {
  margin: 0;
  font-size: 1.1rem;
  font-weight: 700;
}
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-xl);
}
.toolbar-left {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
.count {
  color: var(--color-muted-foreground);
  font-size: 0.9rem;
}
.running-hint {
  color: var(--color-accent);
}
/* VM 卡片网格 */
.vm-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(330px, 1fr));
  gap: 16px;
}
.vm-card {
  transition: border-color 0.2s ease, box-shadow 0.2s ease;
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
.meta-item.mono {
  font-family: var(--font-mono);
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
  margin-bottom: 12px;
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
}
.xml-area :deep(textarea) {
  font-family: var(--font-mono);
  font-size: 0.82rem;
}
</style>
