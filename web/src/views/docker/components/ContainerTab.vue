<template>
    <div class="pane-toolbar">
      <!-- 主操作「创建容器」primary 实底（「拉取镜像」在镜像 tab，同为各自 tab 的主操作） -->
      <el-button type="primary" :icon="Plus" @click="openCreateDrawer">创建容器</el-button>
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
      <!-- 视图切换（用户拍板：容器做成虚拟机列表同款卡片）：卡片默认，表格可切回；
           偏好记忆到 localStorage -->
      <el-radio-group v-model="viewMode" size="small" class="ct-view">
        <el-radio-button value="card">卡片</el-radio-button>
        <el-radio-button value="table">表格</el-radio-button>
      </el-radio-group>
      <template v-if="selection.length">
        <span class="ct-sel">已选 {{ selection.length }} 项</span>
        <el-button type="primary" plain :disabled="!bulkStartable" :loading="bulkLoading" @click="bulkAction('start')">批量启动</el-button>
        <el-button type="warning" plain :disabled="!bulkStoppable" :loading="bulkLoading" @click="bulkAction('stop')">批量停止</el-button>
        <el-button type="danger" plain :disabled="bulkLoading" @click="bulkAction('delete')">批量删除</el-button>
      </template>
      <span class="count ct-count">共 {{ filteredContainers.length }} 个容器</span>
    </div>

    <!-- row-key + reserve-selection：10s 轮询整体替换数据后保留勾选（P0） -->
    <el-table
      v-if="viewMode === 'table'"
      ref="containerTableRef"
      :data="filteredContainers"
      row-key="ID"
      v-loading="loading"
      stripe
      size="small"
      @selection-change="onSelectionChange"
    >
      <template #empty><el-empty description="暂无容器" :image-size="80" /></template>
      <el-table-column type="selection" width="36" reserve-selection />
      <el-table-column label="名称" min-width="96" show-overflow-tooltip>
        <template #default="{ row }">
          <span class="mono">{{ containerName(row.Names) }}</span>
        </template>
      </el-table-column>
      <el-table-column prop="Image" label="镜像" min-width="108" show-overflow-tooltip>
        <template #default="{ row }">
          <span class="mono">{{ row.Image || '—' }}</span>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="72">
        <template #default="{ row }">
          <el-tag :type="stateTag(row.State)" effect="light" size="small">{{ stateText(row.State) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="CPU%" width="62">
        <template #default="{ row }">
          <span class="mono">{{ cpuText(row) }}</span>
        </template>
      </el-table-column>
      <el-table-column label="内存%" width="66">
        <template #default="{ row }">
          <span class="mono" :title="memTitle(row)">{{ memText(row) }}</span>
        </template>
      </el-table-column>
      <el-table-column prop="Status" label="明细" min-width="96" show-overflow-tooltip>
        <template #default="{ row }">{{ row.Status || '—' }}</template>
      </el-table-column>
      <el-table-column label="端口" min-width="96" show-overflow-tooltip>
        <template #default="{ row }">{{ portsText(row.Ports) }}</template>
      </el-table-column>
      <el-table-column label="创建时间" width="146">
        <template #default="{ row }">
          <span class="mono">{{ dockerTime(row.CreatedAt || row.Created) }}</span>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="330" fixed="right" class-name="ct-op">
        <template #default="{ row }">
          <el-button
            v-if="row.State !== 'running'"
            size="small" text type="success"
            :loading="actingKey === row.ID + ':start'"
            :disabled="!!actingKey && actingKey !== row.ID + ':start'"
            @click="containerAction(row, 'start')"
          >启动</el-button>
          <el-button
            v-else
            size="small" text type="warning"
            :loading="actingKey === row.ID + ':stop'"
            :disabled="!!actingKey && actingKey !== row.ID + ':stop'"
            @click="containerAction(row, 'stop')"
          >停止</el-button>
          <el-button
            size="small" text type="primary"
            :loading="actingKey === row.ID + ':restart'"
            :disabled="!!actingKey && actingKey !== row.ID + ':restart'"
            @click="containerAction(row, 'restart')"
          >重启</el-button>
          <el-button size="small" text type="primary" :disabled="row.State !== 'running'" @click="openTerminal(row)">终端</el-button>
          <el-button size="small" text type="primary" @click="openInspect(row)">详情</el-button>
          <el-button size="small" text type="primary" @click="openLogs(row)">日志</el-button>
          <el-button size="small" text type="danger" @click="removeContainer(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- 卡片视图（用户拍板：虚拟机列表同款）：状态徽标 + 实时 CPU/内存 + 操作钮一张卡；
         勾选与表格共用 selection 数组，批量操作两视图通用 -->
    <div v-if="viewMode === 'card'" v-loading="loading" class="ct-grid">
      <el-empty v-if="!filteredContainers.length" description="暂无容器" :image-size="80" />
      <el-card v-for="row in filteredContainers" :key="row.ID" shadow="hover" class="ct-card">
        <div class="ct-card-head">
          <el-checkbox
            :model-value="selection.some((s) => s.ID === row.ID)"
            @change="toggleCardSelect(row)"
          />
          <span class="ct-card-name mono" :title="containerName(row.Names)">{{ containerName(row.Names) }}</span>
          <el-tag :type="stateTag(row.State)" effect="light" size="small">{{ stateText(row.State) }}</el-tag>
        </div>
        <div class="ct-card-meta mono" :title="row.Image">{{ row.Image || '—' }}</div>
        <div class="ct-card-meta" :title="row.Status || ''">{{ row.Status || '—' }}</div>
        <div class="ct-card-metrics">
          <span>CPU <b class="mono">{{ cpuText(row) }}</b></span>
          <span>内存 <b class="mono" :title="memTitle(row)">{{ memText(row) }}</b></span>
          <span v-if="portsText(row.Ports) !== '—'" class="mono ct-card-ports">{{ portsText(row.Ports) }}</span>
        </div>
        <div class="ct-card-actions">
          <el-button
            v-if="row.State !== 'running'"
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
          <!-- 日志/重启/详情收进「更多」下拉：主行 4 元素保单行（5 钮实测在 305px 卡宽差 13px 换行） -->
          <el-dropdown trigger="click" @command="(cmd) => cardMore(row, cmd)">
            <el-button size="small" class="ct-card-more" :icon="MoreFilled" />
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="logs">日志</el-dropdown-item>
                <el-dropdown-item command="restart" :disabled="row.State !== 'running'">重启</el-dropdown-item>
                <el-dropdown-item command="inspect">详情</el-dropdown-item>
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

    <!-- 容器详情抽屉（docker inspect JSON）：内聚于 ContainerInspectDrawer -->
    <ContainerInspectDrawer ref="inspectDrawerRef" />
    <!-- 容器日志抽屉（tail 切换 / 跟随 / 复制 / 下载）：内聚于 ContainerLogsDrawer -->
    <ContainerLogsDrawer ref="logsDrawerRef" />

    <!-- 创建容器抽屉：name/image 必填；端口/挂载/环境变量为动态行（空行提交前过滤）；重启策略四选一 -->
    <el-drawer v-model="createDrawer" title="创建容器" size="42%" :close-on-click-modal="false" @close="resetCreateForm">
      <el-form label-width="88px" @submit.prevent>
        <el-form-item label="名称" required>
          <el-input v-model="createForm.name" placeholder="如 my-nginx，字母数字开头，可含 _ . -" clearable />
          <div v-if="nameError" class="field-error">{{ nameError }}</div>
        </el-form-item>
        <el-form-item label="镜像" required>
          <!-- filterable + allow-create：下拉选本页镜像 tab 已加载的镜像（打开时未加载会自动补拉），也可手输任意镜像名 -->
          <el-select
            v-model="createForm.image"
            filterable
            allow-create
            default-first-option
            clearable
            placeholder="选择已有镜像或输入如 nginx:1.27"
            style="width: 100%"
          >
            <el-option v-for="img in imageOptions" :key="img" :label="img" :value="img" />
          </el-select>
        </el-form-item>
        <el-form-item label="端口映射">
          <div class="dyn-list">
            <div v-for="(row, i) in createForm.ports" :key="'port-' + i" class="dyn-row">
              <el-input v-model="createForm.ports[i]" placeholder="宿主:容器 如 8080:80" clearable />
              <el-tooltip content="删除该行" placement="top">
                <el-button
                  :icon="Delete" text type="danger"
                  :aria-label="'删除端口映射第 ' + (i + 1) + ' 行'"
                  @click="createForm.ports.splice(i, 1)"
                />
              </el-tooltip>
            </div>
            <div v-if="portsError" class="field-error">{{ portsError }}</div>
            <el-button text type="primary" :icon="Plus" @click="createForm.ports.push('')">添加</el-button>
          </div>
        </el-form-item>
        <el-form-item label="挂载卷">
          <div class="dyn-list">
            <div v-for="(row, i) in createForm.volumes" :key="'vol-' + i" class="dyn-row">
              <el-input v-model="createForm.volumes[i]" placeholder="宿主路径:容器路径[:ro]" clearable />
              <el-tooltip content="删除该行" placement="top">
                <el-button
                  :icon="Delete" text type="danger"
                  :aria-label="'删除挂载卷第 ' + (i + 1) + ' 行'"
                  @click="createForm.volumes.splice(i, 1)"
                />
              </el-tooltip>
            </div>
            <el-button text type="primary" :icon="Plus" @click="createForm.volumes.push('')">添加</el-button>
          </div>
        </el-form-item>
        <el-form-item label="环境变量">
          <div class="dyn-list">
            <div v-for="(row, i) in createForm.envs" :key="'env-' + i" class="dyn-row">
              <el-input v-model="createForm.envs[i]" placeholder="KEY=VALUE" clearable />
              <el-tooltip content="删除该行" placement="top">
                <el-button
                  :icon="Delete" text type="danger"
                  :aria-label="'删除环境变量第 ' + (i + 1) + ' 行'"
                  @click="createForm.envs.splice(i, 1)"
                />
              </el-tooltip>
            </div>
            <el-button text type="primary" :icon="Plus" @click="createForm.envs.push('')">添加</el-button>
          </div>
        </el-form-item>
        <el-form-item label="重启策略">
          <el-select v-model="createForm.restart" style="width: 100%">
            <el-option v-for="opt in RESTART_OPTIONS" :key="opt.value" :value="opt.value" :label="opt.label">
              <el-tooltip :content="opt.tip" placement="left" :show-after="200">
                <span>{{ opt.label }}</span>
              </el-tooltip>
            </el-option>
          </el-select>
        </el-form-item>
        <el-form-item label="启动命令">
          <el-input v-model="createForm.command" placeholder="留空使用镜像默认 ENTRYPOINT" clearable />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button :disabled="creating" @click="createDrawer = false">取消</el-button>
        <el-button type="primary" :loading="creating" @click="submitCreate">{{ creating ? '正在创建…' : '创建' }}</el-button>
      </template>
    </el-drawer>
</template>

<script setup>
// 容器页（原容器 tab，1Panel 式子路由化）：数据（containers / statsMap）与容器域全部交互自持。
// 取数失败经 inject('dockerPage') 上报布局壳（503 置门控 alert，其余 toast）；10s 静默轮询随本页走，
// KeepAlive 下 onUnmounted 不触发，故用 onActivated/onDeactivated 显式启停轮询（防切走后后台空转）。
import { ref, computed, reactive, inject, watch, onMounted, onActivated, onDeactivated } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {  Search, Plus, Delete, VideoPlay, VideoPause, Monitor, Document, MoreFilled } from '@element-plus/icons-vue'
import { api } from '../../../api'
import { errMsg, isCancel } from '../../../utils/format'
import { containerName, stateTag, stateText, portsText, dockerTime } from '../../../utils/docker-format'
import { useAutoRefresh } from '../../../composables/useAutoRefresh'
import ContainerTerminal from '../../../components/ContainerTerminal.vue'
import ContainerInspectDrawer from './ContainerInspectDrawer.vue'
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

// 全容器实时 stats（docker stats --no-stream）：按容器名建索引，供 CPU%/内存% 列查询
async function fetchStats() {
  try {
    const res = await api.dockerStats()
    const items = (res.data || {}).items || []
    const m = {}
    for (const it of items) m[it.Name || it.Container || it.ID] = it
    statsMap.value = m
  } catch (e) {
    // stats 是增强列，拉取失败静默（列显示 —），不打扰列表主流程
  }
}

// 首次挂载 / 壳刷新按钮 / 创建容器成功 共用的取数入口：列表 + stats 一起拉，
// 成功清 503 门控，失败交壳上报（stats 内部静默）
async function refresh() {
  loading.value = true
  try {
    await Promise.all([fetchContainers(), fetchStats()])
    clearLoadError()
  } catch (e) {
    reportLoadError(e, '获取容器列表失败')
  } finally {
    loading.value = false
  }
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
    await Promise.all([fetchContainers(), fetchStats()])
    clearLoadError()
  } catch (e) {
    if (e.response && e.response.status === 503) reportLoadError(e, 'Docker 服务不可用')
  } finally {
    refreshing = false
  }
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

const containerTableRef = ref(null)
const selection = ref([])
// 视图切换（卡片默认/表格）：localStorage 记忆（ct-view = card | table）
const viewMode = ref(localStorage.getItem('ct-view') || 'card')
watch(viewMode, (v) => {
  try { localStorage.setItem('ct-view', v) } catch { /* 隐私模式忽略 */ }
})
// 卡片勾选：与表格 selection 共用同一数组（批量操作两视图通用）
function toggleCardSelect(row) {
  const idx = selection.value.findIndex((s) => s.ID === row.ID)
  if (idx > -1) selection.value.splice(idx, 1)
  else selection.value.push(row)
}
// 卡片「更多」下拉：日志 / 重启 / 详情（inspect 抽屉）
function cardMore(row, cmd) {
  if (cmd === 'logs') return openLogs(row)
  if (cmd === 'restart') return containerAction(row, 'restart')
  if (cmd === 'inspect') return openInspect(row)
}
const bulkLoading = ref(false)

function onSelectionChange(rows) {
  selection.value = rows
}

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
    if (containerTableRef.value) containerTableRef.value.clearSelection()
    await Promise.all([fetchContainers(), fetchStats()])
  } finally {
    bulkLoading.value = false
  }
}

// ── 行内操作（启动 / 停止 / 重启，per-row loading 防连点）──

const actingKey = ref('')

async function containerAction(row, action) {
  const label = { start: '启动', stop: '停止', restart: '重启' }[action]
  actingKey.value = row.ID + ':' + action
  try {
    await api.dockerContainerAction(row.ID, action)
    ElMessage.success(`已${label} ${containerName(row.Names)}`)
    await Promise.all([fetchContainers(), fetchStats()])
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
    await Promise.all([fetchContainers(), fetchStats()])
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
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
const inspectDrawerRef = ref(null)
const logsDrawerRef = ref(null)

function openInspect(row) {
  inspectDrawerRef.value.open(row)
}

function openLogs(row) {
  logsDrawerRef.value.open(row)
}

// ═══════════════ 创建容器（抽屉表单）═══════════════

const createDrawer = ref(false)
const creating = ref(false)
// 动态行用「一行空串占位」起步，提交前 trim + 过滤空行
const createForm = reactive({
  name: '',
  image: '',
  ports: [''],
  volumes: [''],
  envs: [''],
  restart: 'no',
  command: ''
})

// 重启策略四选一（对应 docker --restart）：label 为选项短文案，tip 为悬浮说明
const RESTART_OPTIONS = [
  { value: 'no', label: 'no（退出即停）', tip: '容器退出后不自动重启，需手动启动' },
  { value: 'always', label: 'always（总是重启）', tip: '任何退出都自动重启，Docker 守护进程启动时也会拉起' },
  { value: 'unless-stopped', label: 'unless-stopped（除非手动停止）', tip: '异常退出自动重启；手动停止后不再自动拉起' },
  { value: 'on-failure', label: 'on-failure（异常退出时）', tip: '仅非零退出码（异常退出）时自动重启' }
]

// 名称弱校验（行内红字）：字母数字开头，仅含 _ . -，最长 64；强校验在后端
const nameError = computed(() => {
  const n = createForm.name.trim()
  if (!n) return ''
  return /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$/.test(n)
    ? ''
    : '名称需以字母或数字开头，仅可包含字母、数字与 _ . -，最长 64 字符'
})

// 端口映射弱校验：非空行须含冒号（宿主:容器）；IP:宿主:容器 形态与端口范围校验交给后端
const portsError = computed(() => {
  for (const p of createForm.ports) {
    const v = p.trim()
    if (v && !v.includes(':')) return `端口映射「${v}」需包含冒号，格式如 8080:80`
  }
  return ''
})

// 镜像下拉候选：打开创建抽屉时自行拉取一次镜像列表（不再依赖壳的跨页共享 images），拼 Repository:Tag（跳过 <none> 悬空层）
const imageOptions = ref([])

async function fetchImageOptions() {
  try {
    const res = await api.dockerImages()
    const items = (res.data || {}).items || []
    imageOptions.value = items
      .filter((r) => r.Repository && r.Repository !== '<none>')
      .map((r) => `${r.Repository}:${r.Tag || 'latest'}`)
  } catch (e) {
    // fire-and-forget：候选拉取失败不阻断开抽屉，用户仍可手输任意镜像名
  }
}

function openCreateDrawer() {
  // 打开抽屉即补拉镜像候选（fire-and-forget）；不影响手输任意镜像名
  fetchImageOptions()
  createDrawer.value = true
}

// 抽屉关闭即清空表单（各动态区重置为一行空占位）
function resetCreateForm() {
  createForm.name = ''
  createForm.image = ''
  createForm.ports = ['']
  createForm.volumes = ['']
  createForm.envs = ['']
  createForm.restart = 'no'
  createForm.command = ''
}

// 动态行清洗：trim + 丢弃空行，空数组交给后端按缺省处理
function cleanRows(rows) {
  return rows.map((s) => String(s || '').trim()).filter(Boolean)
}

async function submitCreate() {
  const name = createForm.name.trim()
  const image = createForm.image.trim()
  if (!name) {
    ElMessage.warning('请输入容器名称')
    return
  }
  if (nameError.value) {
    ElMessage.warning(nameError.value)
    return
  }
  if (!image) {
    ElMessage.warning('请选择或输入镜像名')
    return
  }
  if (portsError.value) {
    ElMessage.warning(portsError.value)
    return
  }
  creating.value = true
  try {
    await api.createContainer({
      name,
      image,
      ports: cleanRows(createForm.ports),
      volumes: cleanRows(createForm.volumes),
      envs: cleanRows(createForm.envs),
      restart: createForm.restart,
      command: createForm.command.trim()
    })
    ElMessage.success('容器已创建')
    createDrawer.value = false
    // 强制重拉列表 + stats（本页即容器页，无需再切 tab）
    await refresh()
  } catch (e) {
    ElMessage.error(errMsg(e, '创建容器失败'))
  } finally {
    creating.value = false
  }
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
/* 容器行内 7 个操作全部 text 化 + 收紧间距，保证 1440 宽下 CPU%/内存% 列不被固定列遮住 */
.ct-op .el-button + .el-button {
  margin-left: 6px;
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
/* 创建容器抽屉：动态行列表（整行 = 输入框 + 删除图标钮）与行内校验红字 */
.dyn-list {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 8px;
  width: 100%;
}
.dyn-row {
  display: flex;
  align-items: center;
  gap: 4px;
  width: 100%;
}
.dyn-row .el-input {
  flex: 1;
}
/* icon-only 删除钮贴合行内布局，去掉相邻按钮默认左距（间距交给 flex gap） */
.dyn-row .el-button {
  margin-left: 0;
}
.field-error {
  width: 100%;
  color: var(--color-danger, #f56c6c);
  font-size: 0.78rem;
  line-height: 1.4;
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
.ct-card-meta {
  font-size: 0.82rem;
  color: var(--color-muted-foreground);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
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
