<template>
    <div class="pane-toolbar">
      <!-- 主操作「创建容器」primary 实底（「拉取镜像」在镜像 tab，同为各自 tab 的主操作） -->
      <el-button type="primary" :icon="Plus" @click="openCreateDrawer">创建容器</el-button>
      <el-button :loading="pruneLoading" @click="pruneStopped">清理已停止容器</el-button>
      <el-select v-model="stateFilter" class="ct-state" placeholder="全部状态">
        <el-option label="全部状态" value="" />
        <el-option v-for="s in stateOptions" :key="s.value" :label="`${s.value}（${s.count}）`" :value="s.value" />
      </el-select>
      <el-input
        v-model="keyword"
        class="ct-search"
        placeholder="按名称 / 镜像搜索"
        clearable
        :prefix-icon="Search"
      />
      <!-- 自动刷新：10s 静默轮询的总开关（记忆到 localStorage），关闭即整体停表（useAutoRefresh 托管） -->
      <div class="ct-auto" title="每 10 秒自动刷新容器列表与资源占用">
        <el-switch v-model="autoRefresh" />
        <span class="ct-auto-label">自动刷新</span>
      </div>
      <template v-if="selection.length">
        <span class="ct-sel">已选 {{ selection.length }} 项</span>
        <el-button type="primary" plain :disabled="!bulkStartable" :loading="bulkLoading" @click="bulkAction('start')">批量启动</el-button>
        <el-button type="warning" plain :disabled="!bulkStoppable" :loading="bulkLoading" @click="bulkAction('stop')">批量停止</el-button>
        <el-button type="danger" plain :disabled="bulkLoading" @click="bulkAction('delete')">批量删除</el-button>
      </template>
      <span class="count ct-count">共 {{ filteredContainers.length }} 个容器</span>
    </div>

    <!-- 纯卡片视图（用户拍板：去表格只留卡片）：整卡可点进详情抽屉；
         勾选/操作钮在头部与操作区 .stop 防误触详情 -->
    <div v-loading="loading" class="ct-grid">
      <el-empty v-if="!filteredContainers.length" description="暂无容器" :image-size="80" />
      <el-card v-for="row in filteredContainers" :key="row.ID" shadow="hover" class="ct-card" @click="openDetail(row)">
        <div class="ct-card-head">
          <el-checkbox
            :model-value="selection.some((s) => s.ID === row.ID)"
            @click.stop
            @change="toggleCardSelect(row)"
          />
          <span class="ct-card-name mono" :title="containerName(row.Names)">{{ containerName(row.Names) }}</span>
          <!-- 运行中状态点带呼吸动效（复用全局 breathe；与 VM 卡片同款观感） -->
          <span v-if="row.State === 'running'" class="ct-live-dot" title="运行中" />
          <el-tag :type="stateTag(row.State)" effect="light" size="small">{{ stateText(row.State) }}</el-tag>
        </div>
        <div class="ct-card-meta mono" :title="row.Image">{{ row.Image || '—' }}</div>
        <div class="ct-card-meta" :title="row.Status || ''">{{ row.Status || '—' }}</div>
        <div class="ct-card-metrics">
          <span>CPU <b class="mono">{{ cpuText(row) }}</b></span>
          <span>内存 <b class="mono" :title="memTitle(row)">{{ memText(row) }}</b></span>
          <span v-if="portsText(row.Ports) !== '—'" class="mono ct-card-ports">{{ portsText(row.Ports) }}</span>
        </div>
        <div class="ct-card-meta ct-card-time mono">{{ dockerTime(row.CreatedAt || row.Created) }}</div>
        <div class="ct-card-actions" @click.stop>
          <el-button
            v-if="row.State !== 'running' && row.State !== 'paused'"
            size="small" :icon="VideoPlay"
            :loading="actingKey === row.ID + ':start'"
            :disabled="!!actingKey && actingKey !== row.ID + ':start'"
            @click="containerAction(row, 'start')"
          >启动</el-button>
          <el-button
            v-else
            size="small" :icon="VideoPause"
            :loading="actingKey === row.ID + ':stop'"
            :disabled="!!actingKey && actingKey !== row.ID + ':stop'"
            @click="containerAction(row, 'stop')"
          >停止</el-button>
          <el-button size="small" :icon="Monitor" :disabled="row.State !== 'running'" @click="openTerminal(row)">终端</el-button>
          <!-- 日志/重启/暂停恢复收进「更多」下拉：主行 4 元素保单行（5 钮实测在 305px 卡宽差 13px 换行） -->
          <el-dropdown trigger="click" @command="(cmd) => cardMore(row, cmd)">
            <el-button size="small" class="ct-card-more" :icon="MoreFilled" />
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="logs">日志</el-dropdown-item>
                <el-dropdown-item v-if="row.State === 'running'" command="pause">暂停</el-dropdown-item>
                <el-dropdown-item v-else-if="row.State === 'paused'" command="unpause">恢复</el-dropdown-item>
                <el-dropdown-item command="restart" :disabled="row.State !== 'running'">重启</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
          <el-tooltip :content="'删除 ' + containerName(row.Names)" placement="top">
            <el-button
              size="small" type="danger" plain :icon="Delete"
              class="ct-card-del"
              @click="removeContainer(row)"
            />
          </el-tooltip>
        </div>
      </el-card>
    </div>

    <!-- 容器终端抽屉：55% 深色（抽屉挂载于 body，深色样式在底部非 scoped 样式块） -->
    <el-drawer v-model="termDrawer" class="term-drawer" :title="'容器终端 — ' + termName" size="55%" :close-on-click-modal="false">
      <ContainerTerminal v-if="termDrawer" :container-id="termId" />
    </el-drawer>

    <!-- 容器详情旗舰抽屉（概要/统计/日志/JSON + 头部快捷操作）：替换旧 inspect JSON 抽屉 -->
    <ContainerDetailDrawer ref="detailDrawerRef" @terminal="openTerminal" @changed="onDetailChanged" />
    <!-- 容器日志抽屉（tail 切换 / 跟随 / 复制 / 下载）：内聚于 ContainerLogsDrawer -->
    <ContainerLogsDrawer ref="logsDrawerRef" />

    <!-- 创建容器抽屉：抽为独立组件，容器页与镜像页「从镜像运行」共用同一表单 -->
    <ContainerCreateDrawer ref="createDrawerRef" @created="onCreated" />
</template>

<script setup>
// 容器页（原容器 tab，1Panel 式子路由化）：数据（containers / statsMap）与容器域全部交互自持。
// 取数失败经 inject('dockerPage') 上报布局壳（503 置门控 alert，其余 toast）；10s 静默轮询随本页走，
// KeepAlive 下 onUnmounted 不触发，故用 onActivated/onDeactivated 显式启停轮询（防切走后后台空转）。
import { ref, computed, inject, onMounted, onActivated, onDeactivated } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Search, Plus, Delete, VideoPlay, VideoPause, Monitor, MoreFilled } from '@element-plus/icons-vue'
import { api } from '../../../api'
import { errMsg, isCancel } from '../../../utils/format'
import { containerName, stateTag, stateText, portsText, dockerTime } from '../../../utils/docker-format'
import { useAutoRefresh } from '../../../composables/useAutoRefresh'
import ContainerTerminal from '../../../components/ContainerTerminal.vue'
import ContainerCreateDrawer from './ContainerCreateDrawer.vue'
import ContainerDetailDrawer from './ContainerDetailDrawer.vue'
import ContainerLogsDrawer from './ContainerLogsDrawer.vue'

// 布局壳通信：失败上报 / 成功清 503 门控
const { reportLoadError, clearLoadError } = inject('dockerPage')

const containers = ref([])
const statsMap = ref({})
const loading = ref(false)

async function fetchContainers() {
  const res = await api.listContainers()
  containers.value = (res.data || {}).items || []
}

// 全容器实时 stats（docker stats --no-stream 单次要 2s 采样）：按容器名建索引，
// 供 CPU%/内存% 展示。刻意不 await 进 refresh——列表（45ms）秒开，stats 后台补数，
// 数字到达后自动填充（否则 loading 陪跑 2s+，进页面必卡）
async function loadStatsSilent() {
  try {
    const res = await api.dockerStats()
    const items = (res.data || {}).items || []
    const m = {}
    for (const it of items) m[it.Name || it.Container || it.ID] = it
    statsMap.value = m
  } catch {
    // stats 是增强数据，拉取失败静默（显示 —），不打扰列表主流程
  }
}

// 首次挂载 / 壳刷新按钮 / 创建容器成功 共用的取数入口：loading 只等列表，
// 成功清 503 门控，失败交壳上报；stats 始终后台静默补
async function refresh() {
  loading.value = true
  try {
    await fetchContainers()
    clearLoadError()
  } catch (e) {
    reportLoadError(e, '获取容器列表失败')
  } finally {
    loading.value = false
  }
  loadStatsSilent()
}

// ═══════════════ 10s 静默轮询（KeepAlive 适配）═══════════════
// 「自动刷新」开关：记忆到 localStorage（键不变，默认开）；runOnEnable=false 保持原行为——
// 打开开关不立即请求，等下一个 10s 周期。定时器启停与持久化统一交 useAutoRefresh 托管。
const { enabled: autoRefresh, start: startPolling, stop: stopPolling } = useAutoRefresh(silentRefresh, {
  storageKey: 'vmops-docker-autorefresh',
  intervalMs: 10000,
  runOnEnable: false
})

// KeepAlive 下切走只 deactivate 不 unmount，useAutoRefresh 的 onUnmounted 清理不触发；
// 必须配 onActivated/onDeactivated 显式启停，否则离开页面后轮询在后台空转。
onActivated(() => startPolling())
onDeactivated(stopPolling)

// 静默轮询取数：本页不在激活态时定时器已被停掉（无需再守路由），只守并发与 loading；
// 失败只在 503 时置壳门控 alert，其余不打扰用户（与拆分前行为一致）
let refreshing = false
async function silentRefresh() {
  if (refreshing || loading.value) return
  refreshing = true
  try {
    await fetchContainers()
    clearLoadError()
  } catch (e) {
    if (e.response && e.response.status === 503) reportLoadError(e, 'Docker 服务不可用')
  } finally {
    refreshing = false
  }
  loadStatsSilent()
}

onMounted(() => {
  refresh()
  startPolling()
})

// ═══════════════ 容器：筛选 / 批量 / 行内操作 ═══════════════

const stateFilter = ref('')
const keyword = ref('')

// 状态筛选选项带计数（label 渲染为「running（3）」），按状态名排序
const stateOptions = computed(() => {
  const counts = {}
  for (const r of containers.value) {
    if (!r.State) continue
    counts[r.State] = (counts[r.State] || 0) + 1
  }
  return Object.keys(counts)
    .sort()
    .map((s) => ({ value: s, count: counts[s] }))
})

const filteredContainers = computed(() =>
  containers.value.filter((r) => {
    if (stateFilter.value && r.State !== stateFilter.value) return false
    const kw = keyword.value.trim().toLowerCase()
    if (kw) {
      const hay = `${r.Names || ''} ${r.Image || ''}`.toLowerCase()
      if (!hay.includes(kw)) return false
    }
    return true
  })
)

// ── stats 列（CPU% / 内存%）──

function statsOf(row) {
  if (!row) return null
  return statsMap.value[row.Names] || statsMap.value[row.ID] || null
}

// docker stats 的 MemUsage 形如「12.3MiB / 1.944GiB」，按单位换算字节数
const DOCKER_SIZE_UNITS = {
  B: 1, kB: 1e3, KB: 1e3, KiB: 1024,
  MB: 1e6, MiB: 1024 ** 2, GB: 1e9, GiB: 1024 ** 3, TB: 1e12, TiB: 1024 ** 4
}
function parseDockerBytes(s) {
  const m = String(s || '').trim().match(/^([\d.]+)\s*(B|kB|KB|KiB|MB|MiB|GB|GiB|TB|TiB)$/)
  if (!m) return NaN
  return parseFloat(m[1]) * (DOCKER_SIZE_UNITS[m[2]] || 1)
}

function cpuText(row) {
  const st = statsOf(row)
  return st && st.CPUPerc ? st.CPUPerc : '—'
}

function memPctOf(row) {
  const st = statsOf(row)
  if (!st) return null
  // 优先用 docker 直接给的 MemPerc；缺失时按 MemUsage「used / total」换算
  if (st.MemPerc) {
    const n = parseFloat(st.MemPerc)
    if (!isNaN(n)) return n
  }
  const parts = String(st.MemUsage || '').split('/')
  if (parts.length !== 2) return null
  const used = parseDockerBytes(parts[0])
  const total = parseDockerBytes(parts[1])
  if (!isFinite(used) || !isFinite(total) || total <= 0) return null
  return Math.min(100, (used / total) * 100)
}

function memText(row) {
  const pct = memPctOf(row)
  return pct === null ? '—' : pct.toFixed(1) + '%'
}

function memTitle(row) {
  const st = statsOf(row)
  return st && st.MemUsage ? '内存占用 ' + st.MemUsage : ''
}

// ── 批量操作 ──

const selection = ref([])
// 卡片勾选：批量操作的数据源
function toggleCardSelect(row) {
  const idx = selection.value.findIndex((s) => s.ID === row.ID)
  if (idx > -1) selection.value.splice(idx, 1)
  else selection.value.push(row)
}
// 卡片「更多」下拉：日志 / 暂停恢复 / 重启（详情走整卡点击）
function cardMore(row, cmd) {
  if (cmd === 'logs') return openLogs(row)
  if (cmd === 'pause') return containerAction(row, 'pause')
  if (cmd === 'unpause') return containerAction(row, 'unpause')
  if (cmd === 'restart') return containerAction(row, 'restart')
}
const bulkLoading = ref(false)

const bulkStartable = computed(() => selection.value.some((r) => r.State !== 'running'))
const bulkStoppable = computed(() => selection.value.some((r) => r.State === 'running'))

async function bulkAction(action) {
  // 启动只对非 running、停止只对 running 生效；删除全量（running 带 force）
  const targets = selection.value.filter((r) =>
    action === 'start' ? r.State !== 'running' : action === 'stop' ? r.State === 'running' : true
  )
  if (!targets.length) return
  const label = { start: '启动', stop: '停止', delete: '删除' }[action]
  const extra = action === 'delete' ? '运行中的容器将被强制删除（force），容器内未持久化的数据会丢失。' : ''
  const confirmOpts = { type: 'warning', confirmButtonText: label }
  if (action === 'delete') confirmOpts.confirmButtonClass = 'el-button--danger'
  try {
    await ElMessageBox.confirm(`确定批量${label}选中的 ${targets.length} 个容器？${extra}`, `批量${label}`, confirmOpts)
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  bulkLoading.value = true
  try {
    const results = await Promise.allSettled(
      targets.map((r) => {
        if (action === 'delete') {
          return api.dockerContainerDelete(r.ID, r.State === 'running')
        }
        return api.dockerContainerAction(r.ID, action)
      })
    )
    const ok = results.filter((x) => x.status === 'fulfilled').length
    const fail = results.length - ok
    if (fail) ElMessage.warning(`批量${label}完成：成功 ${ok} 个，失败 ${fail} 个`)
    else ElMessage.success(`批量${label}完成（${ok} 个）`)
    selection.value = []
    await fetchContainers()
    loadStatsSilent()
  } finally {
    bulkLoading.value = false
  }
}

// ── 行内操作（启动 / 停止 / 重启，per-row loading 防连点）──

const actingKey = ref('')

async function containerAction(row, action) {
  const label = { start: '启动', stop: '停止', restart: '重启', pause: '暂停', unpause: '恢复' }[action]
  actingKey.value = row.ID + ':' + action
  try {
    await api.dockerContainerAction(row.ID, action)
    ElMessage.success(`已${label} ${containerName(row.Names)}`)
    await fetchContainers()
    loadStatsSilent()
  } catch (e) {
    ElMessage.error(errMsg(e, `${label}失败`))
  } finally {
    actingKey.value = ''
  }
}

async function removeContainer(row) {
  const running = row.State === 'running'
  const name = containerName(row.Names)
  try {
    await ElMessageBox.confirm(
      running
        ? `容器 ${name} 正在运行，将强制删除（force），容器内未持久化的数据会丢失。确定删除？`
        : `确定删除容器 ${name}？`,
      '删除容器',
      { type: 'warning', confirmButtonText: '删除', confirmButtonClass: 'el-button--danger' }
    )
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  try {
    // 运行中的容器必须带 force=true，否则 Docker API 拒绝删除
    await api.dockerContainerDelete(row.ID, running)
    ElMessage.success(`已删除 ${name}`)
    await fetchContainers()
    loadStatsSilent()
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

// ── 清理已停止容器（docker container prune，运行中的不受影响）──

const pruneLoading = ref(false)

async function pruneStopped() {
  try {
    await ElMessageBox.confirm(
      '将删除所有已停止的容器（运行中与暂停中的不受影响），此操作不可恢复。确定清理？',
      '清理已停止容器',
      { type: 'warning', confirmButtonText: '清理', confirmButtonClass: 'el-button--danger' }
    )
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  pruneLoading.value = true
  try {
    const res = await api.dockerPrune('containers')
    ElMessage.success((res.data && res.data.message) || '清理完成')
    await fetchContainers()
    loadStatsSilent()
  } catch (e) {
    ElMessage.error(errMsg(e, '清理失败'))
  } finally {
    pruneLoading.value = false
  }
}

// ═══════════════ 容器终端 / 详情 / 日志抽屉 ═══════════════

const termDrawer = ref(false)
const termId = ref('')
const termName = ref('')

function openTerminal(row) {
  if (row.State !== 'running') {
    ElMessage.warning('容器未运行，无法打开终端')
    return
  }
  termId.value = row.ID
  termName.value = containerName(row.Names)
  termDrawer.value = true
}

// 详情 / 日志抽屉：状态与取数内聚在各自抽屉组件，这里只负责按行打开
const detailDrawerRef = ref(null)
const logsDrawerRef = ref(null)

function openDetail(row) {
  detailDrawerRef.value.open(row)
}

// 详情抽屉内操作（启停/暂停/重命名/删除）后重拉列表，保持与本页一致
async function onDetailChanged() {
  await fetchContainers()
  loadStatsSilent()
}

function openLogs(row) {
  logsDrawerRef.value.open(row)
}

// ═══════════════ 创建容器（独立抽屉组件）═══════════════

const createDrawerRef = ref(null)

function openCreateDrawer() {
  createDrawerRef.value.open()
}

// 创建成功后重拉列表 + stats（本页即容器页，无需再切 tab）
async function onCreated() {
  await refresh()
}

defineExpose({ refresh })
</script>

<style scoped>
/* 各 tab 的工具行：筛选/搜索/批量/主操作 + 计数 */
.pane-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.ct-state {
  width: 132px;
}
.ct-search {
  width: 220px;
}
.ct-sel {
  color: var(--el-color-primary, #409eff);
  font-size: 0.85rem;
}
.ct-count {
  margin-left: auto;
}
/* 开关 + 文字标签（容器工具栏「自动刷新」/ 日志抽屉「跟随」共用） */
.ct-auto {
  display: flex;
  align-items: center;
  gap: 6px;
}
.ct-auto-label {
  font-size: 0.85rem;
  color: var(--el-text-color-regular, #606266);
}
/* ── 卡片视图（用户拍板：虚拟机列表同款卡片）── */
.ct-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(270px, 1fr));
  gap: var(--space-lg);
}
.ct-card {
  display: flex;
  flex-direction: column;
  cursor: pointer;
  transition: transform var(--dur-base) var(--ease-standard), box-shadow var(--dur-base) var(--ease-standard);
}
.ct-card:hover {
  transform: translateY(-2px);
}
.ct-card :deep(.el-card__body) {
  display: flex;
  flex-direction: column;
  flex: 1;
  gap: 6px;
}
.ct-card-head {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.ct-card-name {
  font-weight: 600;
  font-size: 0.95rem;
  color: var(--color-foreground);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
  min-width: 0;
}
.ct-card-head .el-tag {
  flex: none;
}
/* 运行中呼吸点（复用全局 breathe 关键帧，与 VM 卡片同款观感） */
.ct-live-dot {
  flex: none;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--color-success, #67c23a);
  animation: breathe 1.6s ease-in-out infinite;
}
.ct-card-meta {
  font-size: 0.82rem;
  color: var(--color-muted-foreground);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
/* 创建时间行弱化展示（信息补全,不与状态明细争注意力） */
.ct-card-time {
  font-size: 0.76rem;
  opacity: 0.75;
}
.ct-card-metrics {
  display: flex;
  align-items: center;
  gap: var(--space-lg);
  flex-wrap: wrap;
  font-size: 0.82rem;
  color: var(--color-muted-foreground);
}
.ct-card-metrics b {
  color: var(--color-foreground);
  font-weight: 600;
}
.ct-card-ports {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ct-card-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  padding-top: 12px;
  border-top: 1px solid var(--color-border);
  margin-top: auto;
}
/* 删除钮右对齐独立：危险动作与常规操作分离（与 vm-actions 同款约定） */
.ct-card-del {
  margin-left: auto;
}
/* 「更多」钮轻量化：icon-only 幽灵钮 */
.ct-card-more {
  padding: 5px 7px;
}
</style>

<style>
/* 容器终端抽屉整体深色。抽屉挂载于 body 之下，scoped 选择器无法命中，须用全局样式块；
   class 落在 .el-drawer 面板根节点上，据此限定作用范围。 */
.term-drawer .el-drawer__body {
  height: calc(100% - 54px);
  padding: 0 12px 12px;
  background: #0d1b2a;
  overflow: hidden;
  box-sizing: border-box;
}
</style>
