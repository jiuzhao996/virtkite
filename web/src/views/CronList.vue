<template>
  <div>
    <PageHead v-if="!embedded" title="计划任务">
      <template #subtitle>
        <!-- 历史 p.page-desc（UA 外边距参与布局），经插槽原样保留 -->
        <p class="page-desc">按 cron 表达式定时执行虚拟机快照、数据库备份等例行运维动作</p>
      </template>
    </PageHead>

    <el-card shadow="never">
      <!-- 原左右分组均为 gap 8px + flex-wrap，经 wrap 传入保持不变 -->
      <Toolbar wrap right-wrap>
        <template #left>
          <span class="count">共 {{ filteredItems.length }} 个任务</span>
          <!-- 前端过滤当前数据，不动服务端查询 -->
          <el-select v-model="actionFilter" placeholder="动作类型" clearable style="width: 140px">
            <el-option v-for="a in actionFilterOptions" :key="a.value" :label="a.label" :value="a.value" />
          </el-select>
          <el-select v-model="enabledFilter" placeholder="启用状态" clearable style="width: 120px">
            <el-option label="已启用" value="enabled" />
            <el-option label="已停用" value="disabled" />
          </el-select>
        </template>
        <template #right>
          <el-dropdown trigger="click" @command="applyTemplate">
            <el-button :icon="MagicStick">从模板新建</el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item v-for="t in templates" :key="t.key" :command="t">
                  <div class="tpl-item">
                    <b>{{ t.name }}</b>
                    <span class="tpl-desc">{{ t.desc }}</span>
                  </div>
                </el-dropdown-item>
                <el-dropdown-item v-if="!templates.length" disabled>模板加载中…</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
          <el-button type="primary" :icon="Plus" @click="openCreate">新建任务</el-button>
          <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        </template>
      </Toolbar>

      <el-table v-loading="loading" :data="filteredItems" stripe size="small">
        <template #empty>
          <EmptyState description="暂无计划任务，点击新建创建第一个定时任务" />
        </template>
        <el-table-column prop="name" label="名称" min-width="150" show-overflow-tooltip />
        <el-table-column label="表达式" width="130">
          <template #default="{ row }">
            <span class="mono">{{ row.cron_expr }}</span>
          </template>
        </el-table-column>
        <el-table-column label="动作" width="120">
          <template #default="{ row }">
            <el-tag :type="cronActionTag(row.action)" effect="light" size="small">{{ cronActionText(row.action) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="参数" min-width="150" show-overflow-tooltip>
          <template #default="{ row }">{{ cronParamsText(row, vms) }}</template>
        </el-table-column>
        <el-table-column prop="keep" label="保留" width="70" align="center" />
        <el-table-column label="启用" width="80">
          <template #default="{ row }">
            <el-switch
              :model-value="row.enabled"
              :disabled="togglingId === row.id"
              @change="toggleRow(row)"
            />
          </template>
        </el-table-column>
        <el-table-column label="上次执行" width="170">
          <template #default="{ row }">
            <span class="mono">{{ row.last_run ? fmtDateTime(row.last_run) : '从未执行' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="最近执行" width="110" align="center">
          <template #default="{ row }">
            <!-- 死开关命中优先展示（预期已过宽限期仍无执行记录，说明调度器可能没运行） -->
            <el-tooltip v-if="row.stale" :content="cronStaleTip(row)" placement="top">
              <el-tag type="danger" effect="dark" size="small">超时未跑</el-tag>
            </el-tooltip>
            <!-- 列表接口内嵌 recent_runs（最近 3 条、倒序），首条即最近一次 -->
            <el-tooltip v-else-if="cronLatestRun(row)" :content="cronLatestRunTip(row)" placement="top">
              <el-tag :type="cronRunStatusTag(cronLatestRun(row).status)" effect="light" size="small">
                {{ cronRunStatusText(cronLatestRun(row).status) }}
              </el-tag>
            </el-tooltip>
            <span v-else class="mono">—</span>
          </template>
        </el-table-column>
        <el-table-column label="下次执行" width="170">
          <template #default="{ row }">
            <span class="mono">{{ row.next_run ? fmtDateTime(row.next_run) : '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="run_count" label="次数" width="70" align="center" />
        <el-table-column label="链" width="56" align="center">
          <template #default="{ row }">
            <el-tooltip v-if="row.on_success_task_id || row.on_failure_task_id" :content="cronChainTip(row, items)" placement="top">
              <el-tag size="small" effect="plain" type="warning">链</el-tag>
            </el-tooltip>
            <span v-else class="mono">—</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="300" fixed="right">
          <template #default="{ row }">
            <el-button
              text
              size="small"
              type="primary"
              :loading="runningId === row.id"
              :disabled="!!runningId && runningId !== row.id"
              :title="runningId && runningId !== row.id ? '另一个任务正在执行中' : ''"
              @click="runNow(row)"
            >立即运行</el-button>
            <el-button size="small" text type="primary" @click="openHistory(row)">历史</el-button>
            <el-button size="small" text type="primary" @click="duplicate(row)">复制</el-button>
            <el-button size="small" text type="primary" @click="openEdit(row)">编辑</el-button>
            <el-button size="small" text type="danger" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 新建 / 编辑（复用一个弹窗：editingId 区分模式）→ cron/components/CronFormDialog.vue -->
    <CronFormDialog
      v-model="dialog"
      v-model:preset="preset"
      :form="form"
      :editing-id="editingId"
      :cron-preview="cronPreview"
      :vms="vms"
      :vms-loading="vmsLoading"
      :playbooks="playbooks"
      :pb-loading="pbLoading"
      :host-groups="hostGroups"
      :other-tasks="otherTasks"
      :saving="saving"
      @save="save"
    />

    <!-- 执行历史抽屉 → cron/components/CronRunsDrawer.vue -->
    <CronRunsDrawer
      v-model="drawerVisible"
      v-model:page="runsPage"
      :title="drawerTitle"
      :runs="runs"
      :runs-loading="runsLoading"
      :runs-total="runsTotal"
      :runs-page-size="runsPageSize"
      :rerunning="runningId === (drawerTask && drawerTask.id)"
      @rerun="rerunCurrent"
      @page-change="onRunsPageChange"
    />
  </div>
</template>

<script setup>
// ============================================================================
// 计划任务（CronList）
// ----------------------------------------------------------------------------
// 职责：按 cron 表达式定时执行虚拟机快照 / 数据库备份 / Playbook / 容器巡检等例行运维
//       动作；支持前端筛选、启停、立即运行、复制、编辑、删除、执行历史查看、链式编排
//       （成功/失败触发下一跳）、死开关（超时未跑告警）。
//
// 数据流：
//   列表 ← api.cronList()（含内嵌 recent_runs，供「最近执行」列）；前端过滤 actionFilter/
//          enabledFilter 得 filteredItems
//   表单 ← openCreate/openEdit（editingId 区分模式，params JSON 双向翻译）→ save() 组装
//          payload 后 api.cronCreate/cronUpdate；表达式预览经 debounce 500ms 调 cronPreview
//   历史 ← usePagination(fetchRuns) → api.cronRuns（分页倒序）
//
// 已拆出的子组件（web/src/views/cron/components/）：
//   CronFormDialog.vue   新建/编辑弹窗（预设回填、主机组并入等表单内逻辑随之下沉）
//   CronRunsDrawer.vue   执行历史抽屉（分页；状态/耗时纯函数搬入）
//
// 刻意没拆的：主表格——它串联十余个行内动作（启停/运行/历史/复制/编辑/删除）与较多
//   行内状态（togglingId/runningId），且「参数」列要把 vm_id 翻译成虚拟机名；
//   Toolbar 分组与模板下拉留在页面。
// ⚠️ runStatusTag / runStatusText 在主表格与 CronRunsDrawer 各留一份——改一处须同步。
// ============================================================================
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh, MagicStick } from '@element-plus/icons-vue'
import { api } from '../api'
import { errMsg, isCancel, fmtDateTime } from '../utils/format'
import {
  cronActionText, cronActionTag, cronRunStatusText, cronRunStatusTag,
  cronChainTip, cronStaleTip, cronLatestRun, cronLatestRunTip, cronParamsText,
} from '../utils/cron-format'
import { usePagination } from '../composables/usePagination'
import PageHead from '../components/PageHead.vue'
import CronFormDialog from './cron/components/CronFormDialog.vue'
import CronRunsDrawer from './cron/components/CronRunsDrawer.vue'

// embedded：嵌入系统设置页时隐藏独立页头（IA 归并批次 2026-10）
defineProps({ embedded: { type: Boolean, default: false } })
import Toolbar from '../components/Toolbar.vue'
import EmptyState from '../components/EmptyState.vue'

// ===== 列表 =====
const items = ref([])
const loading = ref(false)

// ===== 前端筛选：动作类型（按现有数据归纳选项） + 启用状态 =====
const actionFilter = ref('')
const enabledFilter = ref('')

const actionFilterOptions = computed(() => {
  const seen = new Map()
  items.value.forEach((it) => {
    if (it.action && !seen.has(it.action)) seen.set(it.action, cronActionText(it.action))
  })
  return Array.from(seen, ([value, label]) => ({ value, label }))
})

const filteredItems = computed(() =>
  items.value.filter((it) => {
    if (actionFilter.value && it.action !== actionFilter.value) return false
    if (enabledFilter.value === 'enabled' && !it.enabled) return false
    if (enabledFilter.value === 'disabled' && it.enabled) return false
    return true
  })
)

// 动作 → 中文 / tag 颜色
// 可选的链式下一跳（排除自身）
const otherTasks = computed(() => items.value.filter((t) => t.id !== editingId.value))

async function load() {
  loading.value = true
  try {
    const res = await api.cronList()
    items.value = (res.data && res.data.items) || []
    // 列表刷新后同步刷新已打开的历史抽屉（保留当前页码）
    if (drawerVisible.value && drawerTask.value) loadRuns()
  } catch (e) {
    ElMessage.error(errMsg(e, '获取计划任务列表失败'))
  } finally {
    loading.value = false
  }
}

// ===== VM 下拉数据（快照目标选择 + 参数翻译） =====
const vms = ref([])
const vmsLoading = ref(false)
// playbook 下拉（ansible_playbook 动作）
const playbooks = ref([])
const pbLoading = ref(false)
// runnableVMs（可执行目标过滤）与 vmName（名称+状态标签）已随表单弹窗下沉到 CronFormDialog.vue

async function loadVMs() {
  vmsLoading.value = true
  try {
    const res = await api.listVMs()
    vms.value = (res.data && res.data.items) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '获取虚拟机列表失败'))
  } finally {
    vmsLoading.value = false
  }
}

async function loadPlaybooks() {
  pbLoading.value = true
  try {
    const res = await api.ansiblePlaybooks()
    playbooks.value = (res.data && res.data.items) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '获取 playbook 列表失败'))
  } finally {
    pbLoading.value = false
  }
}

// ===== 新建 / 编辑 =====
const dialog = ref(false)
const saving = ref(false)
const editingId = ref(null)
const preset = ref('')

// 常用 cron 预设（PRESETS）已随表单弹窗下沉到 CronFormDialog.vue

// keep：保留份数（快照/备份只留最近 N 份），后端校验 1-365，默认 7
const DEFAULT_KEEP = 7
const DEFAULT_GRACE = 30
const DEFAULT_RETRY_INTERVAL = 60
const form = ref({ name: '', cron_expr: '', action: 'vm_snapshot', vm_id: null, keep: DEFAULT_KEEP, grace_minutes: DEFAULT_GRACE, retry_count: 0, retry_interval: DEFAULT_RETRY_INTERVAL, on_success_task_id: null, on_failure_task_id: null, enabled: true })

// ===== cron 表达式「下次执行」预览 =====
// 契约：GET /crons/preview?expr=<表达式> → data.next = 最多 5 个「YYYY-MM-DD HH:mm:ss」字符串；
// 表达式非法时后端 400。请求失败 / 400 统一展示红色「表达式无效」，不弹 toast 打断填表。
const cronPreview = ref({ state: 'idle', times: [] }) // state: idle（无输入/输入中）| ok | invalid
let previewSeq = 0 // 时间戳守卫：慢响应晚到时若已非最新请求则丢弃，防旧结果覆盖新输入
let previewTimer = null

watch(
  () => form.value.cron_expr,
  (expr) => {
    clearTimeout(previewTimer)
    cronPreview.value = { state: 'idle', times: [] }
    const trimmed = (expr || '').trim()
    if (!trimmed) return
    // debounce 500ms：等用户停止输入再请求
    previewTimer = setTimeout(() => fetchPreview(trimmed), 500)
  }
)

// 弹窗关闭即取消挂起的预览请求并复位提示
watch(dialog, (open) => {
  if (!open) {
    clearTimeout(previewTimer)
    cronPreview.value = { state: 'idle', times: [] }
  }
})

async function fetchPreview(expr) {
  const seq = ++previewSeq
  try {
    const res = await api.cronPreview(expr)
    if (seq !== previewSeq) return
    const next = (res && res.data && res.data.next) || []
    cronPreview.value = { state: 'ok', times: Array.isArray(next) ? next.slice(0, 3) : [] }
  } catch (e) {
    if (seq !== previewSeq) return
    cronPreview.value = { state: 'invalid', times: [] }
  }
}

// snapshotParamsPreview（vm_snapshot 参数预览）与 applyPreset（预设回填表达式）
// 已随表单弹窗下沉到 CronFormDialog.vue；本页只保留 preset 状态并在开弹窗时复位。

function openCreate() {
  editingId.value = null
  preset.value = ''
  form.value = { name: '', cron_expr: '', action: 'vm_snapshot', vm_id: null, pb_playbook: '', pb_targets: [], pb_target_mode: 'vms', pb_group_id: null, keep: DEFAULT_KEEP, grace_minutes: DEFAULT_GRACE, retry_count: 0, retry_interval: DEFAULT_RETRY_INTERVAL, on_success_task_id: null, on_failure_task_id: null, enabled: true }
  dialog.value = true
  if (vms.value.length === 0) loadVMs()
  if (playbooks.value.length === 0) loadPlaybooks()
}

function openEdit(row) {
  editingId.value = row.id
  preset.value = ''
  let vmId = null
  let pbParams = { playbook: '', targets: [], group_id: null }
  try {
    const obj = JSON.parse(row.params || '{}')
    if (obj && obj.vm_id != null) vmId = obj.vm_id
    if (obj && obj.playbook) pbParams = { playbook: obj.playbook, targets: obj.targets || [], group_id: obj.group_id || null }
  } catch (e) {
    // 旧数据 params 非法时按空处理，保存时会重新生成
  }
  form.value = {
    name: row.name,
    cron_expr: row.cron_expr,
    action: row.action,
    vm_id: vmId,
    pb_playbook: pbParams.playbook,
    pb_targets: pbParams.targets,
    pb_target_mode: pbParams.group_id ? 'group' : 'vms',
    pb_group_id: pbParams.group_id,
    keep: Number(row.keep) || DEFAULT_KEEP,
    grace_minutes: row.grace_minutes == null ? DEFAULT_GRACE : Number(row.grace_minutes),
    retry_count: Number(row.retry_count) || 0,
    retry_interval: Number(row.retry_interval) || DEFAULT_RETRY_INTERVAL,
    on_success_task_id: row.on_success_task_id || null,
    on_failure_task_id: row.on_failure_task_id || null,
    enabled: !!row.enabled
  }
  dialog.value = true
  if (vms.value.length === 0) loadVMs()
  if (playbooks.value.length === 0) loadPlaybooks()
}

async function save() {
  if (!form.value.name.trim()) return ElMessage.warning('请填写任务名称')
  if (!form.value.cron_expr.trim()) return ElMessage.warning('请填写 cron 表达式（或选择常用预设）')
  if (form.value.action === 'vm_snapshot' && form.value.vm_id == null) {
    return ElMessage.warning('请选择要快照的虚拟机')
  }
  if (form.value.action === 'ansible_playbook') {
    if (!form.value.pb_playbook) return ElMessage.warning('请选择 playbook')
    if (form.value.pb_target_mode === 'group') {
      if (!form.value.pb_group_id) return ElMessage.warning('请选择主机组')
    } else if (!form.value.pb_targets.length) {
      return ElMessage.warning('请选择目标虚拟机')
    }
  }
  if (!form.value.keep || form.value.keep < 1) {
    return ElMessage.warning('保留份数必须是 1-365 的整数')
  }
  // params 是 JSON 字符串：vm_snapshot 带 {"vm_id":N}，ansible_playbook 带
  // {playbook, targets}，container_healthcheck 带 {notify:true}，db_backup 空对象
  const params = form.value.action === 'vm_snapshot'
    ? JSON.stringify({ vm_id: form.value.vm_id })
    : form.value.action === 'ansible_playbook'
      ? JSON.stringify(
          form.value.pb_target_mode === 'group'
            ? { playbook: form.value.pb_playbook, group_id: form.value.pb_group_id }
            : { playbook: form.value.pb_playbook, targets: form.value.pb_targets }
        )
      : form.value.action === 'container_healthcheck'
        ? JSON.stringify({ notify: true })
        : '{}'
  const payload = {
    name: form.value.name.trim(),
    cron_expr: form.value.cron_expr.trim(),
    action: form.value.action,
    params,
    keep: form.value.keep,
    grace_minutes: form.value.grace_minutes,
    retry_count: form.value.retry_count,
    retry_interval: form.value.retry_interval,
    // 链式引用：清空(null/undefined)时送 0，后端把 0 归一化为「不链」
    on_success_task_id: Number(form.value.on_success_task_id) || 0,
    on_failure_task_id: Number(form.value.on_failure_task_id) || 0,
    enabled: form.value.enabled
  }
  saving.value = true
  try {
    if (editingId.value) {
      // 表达式解析错误等校验失败，后端返回 400 + 中文 message，errMsg 直接透出
      await api.cronUpdate(editingId.value, payload)
      ElMessage.success('已保存')
    } else {
      await api.cronCreate(payload)
      ElMessage.success('已创建')
    }
    dialog.value = false
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '保存失败'))
  } finally {
    saving.value = false
  }
}

// ===== 启用开关（:model-value 不绑 v-model，失败时状态不翻转） =====
const togglingId = ref(null)

async function toggleRow(row) {
  togglingId.value = row.id
  try {
    await api.cronToggle(row.id)
    row.enabled = !row.enabled
    ElMessage.success(row.enabled ? '已启用' : '已停用')
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '切换失败'))
  } finally {
    togglingId.value = null
  }
}

// ===== 立即运行 =====
const runningId = ref(null)

async function runNow(row) {
  runningId.value = row.id
  try {
    await api.cronRun(row.id)
    ElMessage.success(`已触发「${row.name}」执行`)
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '触发失败'))
  } finally {
    runningId.value = null
  }
}

// ===== 执行历史抽屉 =====
// 内置模板（从模板新建）：挂载时拉一次，只读
const templates = ref([])
async function loadTemplates() {
  try {
    const res = await api.cronTemplates()
    templates.value = (res && res.data && res.data.items) || []
  } catch (e) {
    // 模板是可选便捷入口，失败静默（仍可手动新建）
  }
}

// 用模板填充新建表单（模板都需要目标 vm/playbook 时留空待选）
function applyTemplate(t) {
  editingId.value = null
  preset.value = ''
  form.value = {
    name: t.name,
    cron_expr: t.cron_expr,
    action: t.action,
    vm_id: null,
    pb_playbook: '',
    pb_targets: [],
    keep: Number(t.keep) || DEFAULT_KEEP,
    grace_minutes: t.grace_minutes == null ? DEFAULT_GRACE : Number(t.grace_minutes),
    retry_count: Number(t.retry_count) || 0,
    retry_interval: Number(t.retry_interval) || DEFAULT_RETRY_INTERVAL,
    on_success_task_id: null,
    on_failure_task_id: null,
    enabled: true
  }
  dialog.value = true
  if (vms.value.length === 0) loadVMs()
  if (playbooks.value.length === 0) loadPlaybooks()
}

// 复制任务（后端默认把副本置为停用，避免与原任务同时跑）
async function duplicate(row) {
  try {
    await api.cronDuplicate(row.id)
    ElMessage.success('已复制任务（默认停用，请确认后启用）')
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '复制失败'))
  }
}

// runDuration（执行耗时计算）已随执行历史抽屉下沉到 CronRunsDrawer.vue

// 再从历史抽屉触发一次执行
async function rerunCurrent() {
  const t = drawerTask.value
  if (!t) return
  await runNow(t)
  if (loadRuns) await loadRuns()
}

const drawerVisible = ref(false)
const drawerTask = ref(null)
const runs = ref([])

// 抽屉分页收进 usePagination；以解构别名保留 runsPage 等原名，模板绑定零改动。
// 20 与后端 ListRuns 默认页大小一致（本页 layout 只有 total/prev/pager/next，无改页大入口）
const {
  page: runsPage,
  pageSize: runsPageSize,
  total: runsTotal,
  loading: runsLoading,
  handleCurrentChange: onRunsPageChange,
  reloadFromFirst: resetRunsPage,
  reload: loadRuns
} = usePagination(fetchRuns, { defaultPageSize: 20 })

const drawerTitle = computed(() =>
  drawerTask.value ? `执行历史 · ${drawerTask.value.name}` : '执行历史'
)

function openHistory(row) {
  drawerTask.value = row
  drawerVisible.value = true
  runs.value = [] // 先清上一任务的残留，避免换任务时闪旧数据
  resetRunsPage()
}

// 单页历史获取：api.* 返回统一信封（data 即 {total,items}）；异常自行捕获提示（fetcher 契约），
// 返回 total 由 composable 同步
async function fetchRuns({ page, pageSize }) {
  if (!drawerTask.value) return
  try {
    // 返回 {total, page, page_size, items:[{id,task_name,started_at,finished_at,status,output}]}
    const res = await api.cronRuns(drawerTask.value.id, { page, page_size: pageSize })
    const d = (res && res.data) || {}
    runs.value = d.items || []
    return Number(d.total) || 0
  } catch (e) {
    ElMessage.error(errMsg(e, '获取执行历史失败'))
  }
}

// ===== 删除 =====
async function remove(row) {
  try {
    await ElMessageBox.confirm(`确定删除计划任务「${row.name}」？删除后不再定时执行。`, '删除任务', {
      type: 'warning',
      confirmButtonText: '删除',
      confirmButtonClass: 'el-button--danger'
    })
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  try {
    await api.cronDelete(row.id)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

// 主机组（AU1）：ansible 目标一键并入组成员
const hostGroups = ref([])
async function loadHostGroups() {
  try {
    const res = await api.hostGroups()
    hostGroups.value = (res.data && res.data.items) || []
  } catch {
    // 静默：无组时手选不受影响
  }
}
// applyPbGroup（主机组一键并入 playbook 目标）已随表单弹窗下沉到 CronFormDialog.vue

onMounted(() => {
  loadHostGroups()
  loadTemplates()
  load()
  // 参数列要把 vm_id 翻译成虚拟机名，列表数据里没有，进页面就拉一份
  loadVMs()
})

onUnmounted(() => {
  clearTimeout(previewTimer)
})
</script>

<style scoped>
/* .toolbar-left / .toolbar-right 骨架与 gap/换行由 Toolbar 组件承担。
   .expr-row / .cron-preview* / .group-chips* / .group-chip / .retry-sep / .field-tip →
   cron/components/CronFormDialog.vue；.runs-head / .runs-title / .runs-pager →
   cron/components/CronRunsDrawer.vue（各留一份，改一处须同步）。本页只留模板下拉样式。 */
.tpl-item {
  display: flex;
  flex-direction: column;
  line-height: 1.3;
  padding: 2px 0;
}
.tpl-desc {
  font-size: 0.76rem;
  color: var(--color-muted-foreground);
}
</style>
