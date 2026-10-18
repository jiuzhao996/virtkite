<template>
  <div v-loading="loading">
    <PageHead title="任务中心" subtitle="创建、克隆、删除、关机等耗时操作都在后台异步执行，这里看每个任务的进度和结果；点任务行可查看失败原因，以及删除虚拟机时被保护保留的共享卷" />
    <!-- Q3-B（用户 2026-10-18 拍板）：任务读接口对三角色均放行（路由注释已论证
         「viewer 本就能 GET 全量日志，不构成越权」），此处只做可见性说明，
         不在前端假装收紧——那会让 viewer 以为自己没权限却能从接口读到。 -->
    <el-alert v-if="!isAdmin" type="info" :closable="false" show-icon class="tl-visibility"
      description="任务记录对所有登录角色可见（含执行人与失败原因）；取消与删除仅管理员可操作。" />
    <el-card shadow="never">
      <!-- 原左分组为 gap 8px + flex-wrap，经 wrap 传入保持不变；计数为 .toolbar 直接子元素走默认插槽 -->
      <Toolbar wrap>
        <template #left>
          <el-button type="primary" :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <el-select v-model="q.status" placeholder="状态筛选" clearable style="width: 140px" @change="search">
            <el-option label="进行中" value="active" />
            <el-option label="成功" value="success" />
            <el-option label="失败" value="failed" />
          </el-select>
          <!-- 前端过滤当前页：任务名 / 虚拟机名模糊匹配，不动服务端查询 -->
          <el-input
            v-model="keyword"
            placeholder="搜索任务名 / 虚拟机名"
            clearable
            :prefix-icon="Search"
            style="width: 220px"
          />
          <!-- 批量操作（XXL-JOB 手感）：按钮按选中数自动启停，未选中/不满足条件即置灰。
               查看详情=selectOnlyOne（恰好选 1 条）；批量取消/删除=selectAny（选中里有可处理项即可） -->
          <el-button :icon="View" :disabled="!selectOnlyOne" @click="openSelectedDetail">查看详情</el-button>
          <el-button
            v-if="isAdmin"
            type="warning"
            plain
            :disabled="!canBatchCancel"
            :loading="batchBusy"
            @click="batchCancel"
          >批量取消</el-button>
          <el-button
            v-if="isAdmin"
            type="danger"
            plain
            :icon="Delete"
            :disabled="!canBatchDelete"
            :loading="batchBusy"
            @click="batchDelete"
          >批量删除</el-button>
          <el-button
            v-if="isAdmin"
            type="danger"
            :icon="Delete"
            :disabled="!finishedCount"
            @click="clearFinished"
          >清理本页已完成 ({{ finishedCount }})</el-button>
        </template>
        <span class="count">共 {{ total }} 个任务<span v-if="activeCount" class="running-hint"> · 本页 {{ activeCount }} 个进行中</span><span v-if="selectedRows.length" class="running-hint"> · 已选 {{ selectedRows.length }}</span></span>
      </Toolbar>

      <!-- 行点击钻取：整行可点开任务详情抽屉；行内按钮/链接一律 .stop 防止误触发钻取 -->
      <el-table :data="filteredItems" stripe border style="width: 100%" row-class-name="task-row" @row-click="openDetail" @selection-change="onSelectionChange">
        <template #empty><EmptyState description="暂无任务" /></template>
        <el-table-column type="selection" width="46" />
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column prop="title" label="任务" min-width="200" show-overflow-tooltip />
        <el-table-column label="类型" width="130">
          <template #default="{ row }">{{ taskTypeText(row.type) }}</template>
        </el-table-column>
        <el-table-column label="状态" width="104">
          <template #default="{ row }">
            <el-tag :type="taskStatusTag(row.status)" effect="light">
              <span v-if="row.status === 'running' || row.status === 'pending'" class="pulse-dot" />
              {{ taskStatusText(row.status) }}
            </el-tag>
            <!-- 自动重试可见性：只有重试白名单内的类型才有 max_retry，重试过才显示进度 -->
            <div v-if="row.max_retry && row.retry_count" class="retry-hint">重试 {{ row.retry_count }}/{{ row.max_retry }}</div>
          </template>
        </el-table-column>
        <el-table-column label="进度" min-width="150">
          <template #default="{ row }">
            <el-progress
              :percentage="row.progress || 0"
              :status="row.status === 'failed' ? 'exception' : row.status === 'success' ? 'success' : ''"
              :format="() => (row.progress || 0) + '%'"
            />
          </template>
        </el-table-column>
        <el-table-column label="虚拟机" width="140" show-overflow-tooltip>
          <template #default="{ row }">
            <!-- vm_id 在任务数据里：有则点击跳虚拟机详情，无（如数据库备份）显示占位 -->
            <el-link v-if="row.vm_id" type="primary" @click.stop="goVM(row.vm_id)">{{ row.vm_name }}</el-link>
            <span v-else>{{ row.vm_name || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="username" label="执行人" width="110" />
        <el-table-column label="结果/错误" min-width="200" show-overflow-tooltip>
          <template #default="{ row }">
            <span v-if="row.status === 'failed'" class="err-text">{{ row.error || '失败' }}</span>
            <span v-else-if="row.status === 'success'" class="ok-text">完成</span>
            <span v-else-if="row.status === 'cancelled'" class="muted-text">已取消</span>
            <span v-else class="muted-text">执行中…</span>
          </template>
        </el-table-column>
        <el-table-column prop="created_at" label="创建时间" width="170">
          <template #default="{ row }">{{ fmtDateTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="140" fixed="right">
          <template #default="{ row }">
            <el-button
              v-if="isAdmin && row.status === 'running'"
              text type="warning" size="small"
              :loading="cancellingId === row.id"
              @click.stop="cancelTask(row)"
            >取消</el-button>
            <el-button
              v-if="isAdmin"
              text
              type="danger"
              size="small"
              :disabled="!isFinal(row.status)"
              :title="isFinal(row.status) ? '' : '任务未结束，暂不能删除'"
              @click.stop="remove(row)"
            >删除</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-pagination
        :current-page="page"
        v-model:page-size="pageSize"
        :total="total"
        :page-sizes="[20, 50, 100]"
        layout="total, sizes, prev, pager, next"
        class="pager"
        @current-change="onPage"
        @size-change="onPageSizeChange"
      />
    </el-card>

    <!-- 任务详情抽屉：概要 tag + 时间线（提交→结束，进行中实时计时）+ 关联 VM 链接 + 结构化 result
         （保留卷每卷一行带原因 / 其余键值对，敏感键打码），兑现 task-contract 的展示承诺 -->
    <el-drawer v-model="detailDrawer" :title="detail ? `任务详情 #${detail.id}` : '任务详情'" size="480px">
      <template v-if="detail">
        <!-- 概要：类型 + 状态大 tag，执行中/pending 带进度条 -->
        <div class="dt-head">
          <div class="dt-tags">
            <el-tag effect="plain">{{ taskTypeLabel(detail.type) }}</el-tag>
            <el-tag :type="taskStatusTag(detail.status)" effect="light">
              <span v-if="detail.status === 'running' || detail.status === 'pending'" class="pulse-dot" />
              {{ taskStatusText(detail.status) }}
            </el-tag>
          </div>
          <div class="dt-title">{{ detail.title }}</div>
          <div class="dt-meta">执行人 {{ detail.username || '—' }} · 创建于 {{ fmtDateTime(detail.created_at) }}</div>
          <el-progress
            v-if="detail.status === 'running' || detail.status === 'pending'"
            :percentage="detail.progress || 0"
            :stroke-width="8"
          />
        </div>

        <!-- 时间线：提交 → 开始执行（仅 started_at 存在时渲染，当前接口不下发则自动隐藏）→ 结束/进行中 -->
        <h4 class="detail-sec">执行时间线</h4>
        <el-timeline class="dt-timeline">
          <el-timeline-item
            v-for="node in detailTimeline"
            :key="node.key"
            :type="node.type"
            :hollow="node.hollow"
            :timestamp="node.time"
            placement="top"
          >
            <div class="tl-node" :class="{ 'tl-node-active': node.active }">{{ node.label }}</div>
            <div v-if="node.duration" class="tl-duration">{{ node.duration }}</div>
          </el-timeline-item>
        </el-timeline>

        <!-- 关联虚拟机（包C）：行 vm_id / result.vm_id / payload 里的 id，可点直达 VM 详情 -->
        <template v-if="vmLinks.length">
          <h4 class="detail-sec">关联虚拟机</h4>
          <div class="vm-chips">
            <el-tag
              v-for="vm in vmLinks"
              :key="vm.id"
              class="vm-chip"
              effect="plain"
              @click="goVM(vm.id)"
            >
              <el-icon><Monitor /></el-icon>
              {{ vm.name || `虚拟机 #${vm.id}` }}
            </el-tag>
          </div>
        </template>

        <!-- 非 VM 任务的对象出口（栈升级/应用安装/镜像下载）：流程终点给下一步 -->
        <template v-if="taskObjectLink">
          <h4 class="detail-sec">相关入口</h4>
          <el-button size="small" type="primary" plain @click="goTaskObject">{{ taskObjectLink.text }}</el-button>
        </template>

        <div v-if="detail.status === 'failed' && detail.error" class="detail-error">{{ detail.error }}</div>

        <!-- 结构化执行日志（条目数组）：首屏 GET 全量 → WS 追平增量；hidden 调试/噪音行默认折叠、可展开。
             渲染整体下沉到 TaskLogPanel 子组件，页面只喂原始 entries + 实时标志、转发 scrollToBottom -->
        <TaskLogPanel
          v-if="logStructured"
          ref="logPanelRef"
          v-model:debug="showDebug"
          :entries="logEntries"
          :live="logLive"
        />

        <!-- 任务参数 payload：后端 model.Task.Payload 为 json:"-" 不下发，本块仅在接口放开后自动生效；
             键名含 password/passwd/secret/token/key（大小写不敏感）时值一律以 ****** 展示 -->
        <template v-if="payloadKVs.length">
          <h4 class="detail-sec">任务参数</h4>
          <TaskKVList :kvs="payloadKVs" :label-fn="kvLabel" />
        </template>

        <template v-if="detailParsed">
          <h4 class="detail-sec">执行结果</h4>

          <!-- 保留卷：delete_vm 的 kept_volumes（"卷名（原因）"字符串）/ cleanup_volumes 的 kept（{name,reason}），
               统一归一成「每卷一行 + 保留原因」 -->
          <div v-if="keptVolumes.length" class="detail-row">
            <div class="kept-title">已保留的共享卷（删除保护命中，未删）</div>
            <ul class="kept-list">
              <li v-for="(v, i) in keptVolumes" :key="i">
                <span class="mono">{{ v.name }}</span>
                <span v-if="v.reason" class="kept-reason">{{ v.reason }}</span>
              </li>
            </ul>
          </div>

          <!-- 已删除卷：cleanup_volumes 的 deleted 数组 -->
          <div v-if="deletedVolumes.length" class="detail-row">
            <div class="kept-title">已删除卷（{{ deletedVolumes.length }} 个）</div>
            <ul class="kept-list kept-list-deleted">
              <li v-for="(v, i) in deletedVolumes" :key="i"><span class="mono">{{ v }}</span></li>
            </ul>
          </div>

          <!-- 其余键值对：嵌套对象 / 长文本（如 ansible 输出）进等宽 pre 限高滚动，敏感键打码 -->
          <TaskKVList v-if="resultKVs.length" :kvs="resultKVs" :label-fn="kvLabel" />
        </template>
        <el-empty
          v-else-if="!payloadKVs.length && detail.status === 'success'"
          description="该任务没有结构化结果数据"
          :image-size="60"
        />
      </template>
    </el-drawer>
  </div>
</template>

<script setup>
// ============================================================================
// 任务中心（TaskList）
// ----------------------------------------------------------------------------
// 职责：展示后台异步任务（创建/克隆/删除/关机/装应用/跑 Playbook/镜像构建…）的
//       列表、进度与结果。所有耗时操作都走「提交得 task → 后台执行 → 轮询看进度」，
//       本页就是这条链路的可视窗口。
//
// 数据流：
//   列表 ← fetchTaskPage()（api.listTasks；"进行中"筛选 = pending+running 两请求并发合并）
//          ← usePagination 管分页/首屏 loading，search() 回第 1 页、load() 原地重拉
//          ← tick 心跳：本页有进行中任务才每 3s 静默刷新，无则停
//   详情 ← openDetail(row)：先落行快照开抽屉，活动态再单查 getTask 并用 tick 跟进终态
//   日志 ← openLogStream()：首屏 GET 全量 → 非终态连 WS（一次性票据）追平增量，
//          按 seq 去重、滚到底；抽屉关闭即断开
//   批量 ← selectedRows 驱动：查看详情=恰好选 1 条，批量取消/删除=选中里有可处理项即可
//
// 关键坑位：见各函数上方注释（行点击钻取要 .stop、敏感键脱敏、保留卷两种形态归一、
//           tasks 表无 started_at 的时间线降级等）。
//
// 已拆出的纯展示子组件（页面保留列表/筛分页/批量/轮询/日志 WS 逻辑）：
//   tasks/components/TaskLogPanel.vue  结构化执行日志面板（含调试行折叠、实时徽标、滚到底）
//   tasks/components/TaskKVList.vue    「任务参数」「执行结果」共用的键值对渲染器
// 详情抽屉整体未拆：它的时间线/进度/日志与页面的轮询心跳（tick→refreshDetail）和
// 日志 WS 生命周期强耦合，外拆需复制计时器或引入跨组件调用，不满足「明确自包含」。
// ============================================================================
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Delete, Search, Monitor, View } from '@element-plus/icons-vue'
import { api } from '../api'
import { POLL_DEFAULTS, getPollInterval } from '../utils/settings'
import PageHead from '../components/PageHead.vue'
import Toolbar from '../components/Toolbar.vue'
import TaskKVList from './tasks/components/TaskKVList.vue'
import TaskLogPanel from './tasks/components/TaskLogPanel.vue'
import { useTaskLogStream } from '../composables/useTaskLogStream'
import { useAuth } from '../store/auth'
import { taskTypeText, taskStatusText, taskStatusTag, fmtDateTime, errMsg, isCancel } from '../utils/format'
import { usePagination } from '../composables/usePagination'
import EmptyState from '../components/EmptyState.vue'

const router = useRouter()
const route = useRoute()
const { isAdmin } = useAuth()

const items = ref([])
// 分页状态与流转收进 usePagination（范式与 AuditList/SessionList 一致）：筛选变更/改页大回第 1 页、
// 翻页保留筛选（原实现筛选 @change 调 load 不回第 1 页，筛选后停留高页码会看空页，迁移顺手修正）
const {
  page,
  pageSize,
  total,
  loading,
  handleCurrentChange: onPage,
  handleSizeChange: onPageSizeChange,
  reloadFromFirst: search,
  reload: load
} = usePagination(fetchTaskPage, { defaultPageSize: 50 })
const q = ref({ status: '' })

// format.js 的 TASK_TYPE_TEXT 只覆盖 5 种 VM 类任务；这里补齐 service/tasks 里其余注册类型
// （vm_tasks.go 的 cleanup_volumes、app_tasks.go 的 app_install、image_download.go 的 image_download、
//   ansible_tasks.go 的 ansible_run、image_build.go 的 image_build），本表优先命中，
// 其余回落共享映射，后端新增类型原样兜底
const EXTRA_TASK_TYPE_TEXT = {
  cleanup_volumes: '清理孤儿卷',
  app_install: '安装应用',
  image_download: '下载镜像',
  ansible_run: '执行 Playbook',
  image_finalize: '镜像清洗',
  image_build: '构建镜像'
}

/** 任务类型中文：补齐表优先，未命中回落 utils/format 的共享映射 */
function taskTypeLabel(type) {
  return EXTRA_TASK_TYPE_TEXT[type] || taskTypeText(type)
}

// ==================== 详情抽屉：时间线 / 结构化 result / 脱敏（包A）====================

const detailDrawer = ref(false)
const cancellingId = ref(null)
// 终态判断（cancelled 后也算终态，可删除）
function isFinal(status) {
  return status === 'success' || status === 'failed' || status === 'cancelled'
}
async function cancelTask(row) {
  try {
    await ElMessageBox.confirm(`取消任务「${row.title || row.id}」？正在执行的远程操作将被中止。`, '取消任务', { type: 'warning', confirmButtonText: '取消任务' })
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  cancellingId.value = row.id
  try {
    await api.cancelTask(row.id)
    ElMessage.success('已发出取消')
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '取消失败'))
  } finally {
    cancellingId.value = null
  }
}
const detail = ref(null)

// ==================== 结构化执行日志 + 实时增量 WS ====================

// 日志面板子组件实例：仅用于把「滚到底」能力暴露出来（模板 ref）
const logPanelRef = ref(null)
// 滚到底交给日志面板子组件（它持有滚动容器 ref），页面只转发调用
function scrollLogToBottom() {
  logPanelRef.value?.scrollToBottom()
}

// 任务结构化日志流（GET 首屏 + WS 增量 + 一次性票据 + 退避重连）收进 composable，
// 与 AppInstallLogDrawer 共用同一套语义，避免两边行为漂移。
// onAppend 由 composable 在每次追加后回调，页面借它把面板滚到底。
const { entries: logEntries, structured: logStructured, live: logLive, debug: showDebug,
  open: openLogStream, close: closeLogStream } = useTaskLogStream(scrollLogToBottom)

// ==================== 批量操作（XXL-JOB 手感：按选中数自动启停）====================

const selectedRows = ref([])
const batchBusy = ref(false)
const selectOnlyOne = computed(() => selectedRows.value.length === 1)
const canBatchCancel = computed(() => selectedRows.value.some((t) => t.status === 'running'))
const canBatchDelete = computed(() => selectedRows.value.some((t) => isFinal(t.status)))

function onSelectionChange(rows) {
  selectedRows.value = rows || []
}

function openSelectedDetail() {
  if (selectOnlyOne.value) openDetail(selectedRows.value[0])
}

async function batchCancel() {
  const targets = selectedRows.value.filter((t) => t.status === 'running')
  if (!targets.length) return
  try {
    await ElMessageBox.confirm(`取消选中的 ${targets.length} 个运行中任务？`, '批量取消', {
      type: 'warning',
      confirmButtonText: '取消任务'
    })
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  batchBusy.value = true
  let failed = 0
  for (const t of targets) {
    try {
      await api.cancelTask(t.id)
    } catch (e) {
      failed++
    }
  }
  batchBusy.value = false
  if (failed) ElMessage.warning(`已取消 ${targets.length - failed} 个，${failed} 个失败`)
  else ElMessage.success('已发出取消')
  await load()
}

async function batchDelete() {
  const targets = selectedRows.value.filter((t) => isFinal(t.status))
  if (!targets.length) return
  try {
    await ElMessageBox.confirm(`删除选中的 ${targets.length} 条已完成任务记录？`, '批量删除', {
      type: 'warning',
      confirmButtonClass: 'el-button--danger'
    })
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  batchBusy.value = true
  let failed = 0
  for (const t of targets) {
    try {
      await api.deleteTask(t.id)
    } catch (e) {
      failed++
    }
  }
  batchBusy.value = false
  if (failed) ElMessage.warning(`已删除 ${targets.length - failed} 条，${failed} 条失败`)
  else ElMessage.success('已删除')
  await load()
}

// result / payload 都是 JSON 字符串，解析失败按无数据处理（与原 detailParsed 行为一致）
function parseJSONField(s) {
  if (!s) return null
  try {
    const v = JSON.parse(s)
    return v && typeof v === 'object' ? v : null
  } catch {
    return null
  }
}

const detailParsed = computed(() => parseJSONField(detail.value && detail.value.result))
const payloadParsed = computed(() => parseJSONField(detail.value && detail.value.payload))

// 包A 脱敏：键名含 password/passwd/secret/token/key（大小写不敏感）→ 值以 ****** 展示。
// payload 现行不下发（model.Task.Payload 为 json:"-"），该规则同时兜底 result 里出现的敏感键
const SENSITIVE_KEY_RE = /password|passwd|secret|token|key/i

// result/payload 已知键 → 中文标签（未命中显示原始键名，后端加键前端无需跟改）
const RESULT_KEY_TEXT = {
  vm_id: '虚拟机 ID',
  vm: '虚拟机',
  disk_gb: '磁盘 (GB)',
  pool: '存储池',
  module: '模块',
  playbook: 'Playbook',
  targets: '目标主机数',
  output: '执行输出',
  recap: 'PLAY RECAP',
  recap_raw: 'RECAP 原文',
  app_id: '应用 ID',
  skipped: '已跳过',
  path: '文件路径',
  size: '字节数',
  size_gb: '大小 (GB)',
  image_id: '镜像 ID',
  image_name: '镜像名',
  os_version: '系统版本',
  size_before: '清洗前字节数',
  size_after: '清洗后字节数',
  saved_bytes: '回收字节数',
  optimized: '已注入基础优化',
  sparsified: '已压缩磁盘',
  inventory: 'inventory（口令已脱敏）',
  check: '预演（--check）',
  diff: '差异（--diff）',
  tags: '标签'
}
function kvLabel(k) {
  return RESULT_KEY_TEXT[k] || k
}

/** 单个键值 → 渲染描述：masked 打码 / pre 进等宽块（嵌套对象、含换行或超长文本）/ text 普通文本 */
function buildKV(k, v) {
  if (SENSITIVE_KEY_RE.test(String(k))) return { k, masked: true }
  if (v === null || v === undefined) return { k, text: '—' }
  if (typeof v === 'boolean') return { k, text: v ? '是' : '否' }
  if (typeof v === 'object') return { k, pre: true, text: JSON.stringify(v, null, 2) }
  const s = String(v)
  if (s.includes('\n') || s.length > 120) return { k, pre: true, text: s }
  return { k, text: s }
}

// 已被专用区块消费的键不再进通用键值对：result 的关联 VM/保留卷/已删卷，payload 的 id 链接（见 vmLinks）
const RESULT_CONSUMED_KEYS = ['vm_id', 'vm', 'kept_volumes', 'kept', 'deleted']
const PAYLOAD_CONSUMED_KEYS = ['vm_id', 'vm_ids']

function toKVs(obj, consumed) {
  return Object.keys(obj)
    .filter((k) => !consumed.includes(k))
    .map((k) => buildKV(k, obj[k]))
}

const resultKVs = computed(() => (detailParsed.value ? toKVs(detailParsed.value, RESULT_CONSUMED_KEYS) : []))
const payloadKVs = computed(() => (payloadParsed.value ? toKVs(payloadParsed.value, PAYLOAD_CONSUMED_KEYS) : []))

// 保留卷归一化：delete_vm 的 kept_volumes 是「卷名（原因）」字符串数组（vm_delete.go 拼装），
// cleanup_volumes 的 kept 是 {name, reason} 对象数组（vm_tasks.go execCleanupVolumes），两形态都吃
const keptVolumes = computed(() => {
  const p = detailParsed.value
  if (!p) return []
  const out = []
  const push = (name, reason) => out.push({ name: String(name), reason: reason ? String(reason) : '' })
  for (const src of [p.kept_volumes, p.kept]) {
    if (!Array.isArray(src)) continue
    for (const v of src) {
      if (v && typeof v === 'object') push(v.name, v.reason)
      else {
        const s = String(v)
        const i = s.lastIndexOf('（')
        if (i > 0 && s.endsWith('）')) push(s.slice(0, i), s.slice(i + 1, -1))
        else push(s, '')
      }
    }
  }
  return out
})

// 已删除卷（cleanup_volumes 的 deleted 字符串数组）
const deletedVolumes = computed(() => {
  const p = detailParsed.value
  if (!p || !Array.isArray(p.deleted)) return []
  return p.deleted.map((v) => String(v))
})

// 包C 关联跳转：行 vm_id / result.vm_id / payload.vm_id·vm_ids 收敛成可点 chip。
// 只取确定是 VM 的 id 字段，不做任务标题字符串猜解析；删除任务的 VM 已不存在，
// 跳转后由 VM 详情页自行提示，比把名字渲染成死文本更好定位
const vmLinks = computed(() => {
  const d = detail.value
  if (!d) return []
  const out = []
  const seen = new Set()
  const push = (id, name) => {
    const key = String(id)
    if (!id || seen.has(key)) return
    seen.add(key)
    out.push({ id: key, name: name || '' })
  }
  if (d.vm_id) push(d.vm_id, d.vm_name)
  const p = detailParsed.value
  if (p && p.vm_id) push(p.vm_id, p.vm || d.vm_name)
  const pl = payloadParsed.value
  if (pl) {
    if (pl.vm_id) push(pl.vm_id)
    if (Array.isArray(pl.vm_ids)) for (const id of pl.vm_ids) push(id)
  }
  return out
})

// 非 VM 任务的对象出口：栈升级 → 部署栈、应用安装 → 应用商店。
// 任务是「流程终点」之一，此前执行完没有任何去向；这里按任务类型给下一步入口。
const taskObjectLink = computed(() => {
  const d = detail.value
  if (!d) return null
  if (d.type === 'stack_upgrade') return { text: '查看部署栈 →', to: '/apps?tab=stacks' }
  if (d.type === 'app_install') return { text: '查看应用商店 →', to: '/apps' }
  if (d.type === 'image_download') return { text: '查看镜像库 →', to: '/images' }
  if (d.type === 'image_finalize') return { text: '查看镜像库 →', to: '/images' }
  if (d.type === 'image_build') return { text: '查看容器镜像 →', to: '/images?tab=docker' }
  return null
})

function goTaskObject() {
  if (taskObjectLink.value) {
    detailDrawer.value = false
    router.push(taskObjectLink.value.to)
  }
}

// ==================== 时间线 ====================

// 数据事实：tasks 表只有 created_at/updated_at（model/task.go），没有 started_at/finished_at。
// 降级规则：
//   - 「开始执行」节点仅 started_at 存在时渲染（当前恒缺 → 自动隐藏，属预留，后端补字段即自动升级）
//   - 终态任务的「结束」时间取 updated_at（终态后任务不再有写入，≈ finished_at）
//   - 间隔时长无法拆「排队/执行」两段 → 降级为全程「总耗时」一条；有 started_at 时自动拆两段
const isTaskActive = (t) => !!t && (t.status === 'pending' || t.status === 'running')

// 进行中任务的实时耗时依赖每秒心跳；仅在抽屉打开且任务活动态时运行（watch 里启停）
const nowMs = ref(Date.now())
let elapsedTimer = null
function startElapsed() {
  if (elapsedTimer) return
  elapsedTimer = setInterval(() => {
    nowMs.value = Date.now()
  }, 1000)
}
function stopElapsed() {
  if (elapsedTimer) {
    clearInterval(elapsedTimer)
    elapsedTimer = null
  }
}
watch(
  () => [detailDrawer.value, detail.value && detail.value.status],
  ([open, st]) => {
    if (open && (st === 'pending' || st === 'running')) startElapsed()
    else stopElapsed()
  }
)

/** 毫秒时长 → 中文短文案：<10s 带 1 位小数（"2.3s"），进位 60 归整避免「1分60秒」 */
function fmtDuration(ms) {
  const s = (ms || 0) / 1000
  if (s <= 0) return '0s'
  if (s < 10) return s.toFixed(1) + 's'
  if (s < 60) return Math.round(s) + 's'
  let m = Math.floor(s / 60)
  let rs = Math.round(s % 60)
  if (rs === 60) {
    m += 1
    rs = 0
  }
  if (s < 3600) return `${m}分${rs}秒`
  let h = Math.floor(s / 3600)
  let rm = Math.round((s % 3600) / 60)
  if (rm === 60) {
    h += 1
    rm = 0
  }
  return `${h}小时${rm}分`
}

const detailTimeline = computed(() => {
  const d = detail.value
  if (!d) return []
  const created = new Date(d.created_at).getTime()
  const finished = isTaskActive(d) ? NaN : new Date(d.updated_at).getTime()
  const started = d.started_at ? new Date(d.started_at).getTime() : NaN
  const nodes = [{ key: 'created', label: '提交任务', time: fmtDateTime(d.created_at), type: 'primary' }]
  if (!Number.isNaN(started)) {
    nodes.push({
      key: 'started',
      label: '开始执行',
      time: fmtDateTime(d.started_at),
      type: 'primary',
      duration: '排队 ' + fmtDuration(started - created)
    })
  }
  if (!Number.isNaN(finished)) {
    const ok = d.status === 'success'
    const cancelled = d.status === 'cancelled'
    nodes.push({
      key: 'finished',
      // cancelled 不是「失败」：误标 danger 会让用户以为系统出错
      label: ok ? '执行成功' : cancelled ? '已取消' : '执行失败',
      time: fmtDateTime(d.updated_at),
      type: ok ? 'success' : cancelled ? 'info' : 'danger',
      duration: Number.isNaN(started)
        ? '总耗时 ' + fmtDuration(finished - created)
        : '执行 ' + fmtDuration(finished - started)
    })
  } else {
    nodes.push({
      key: 'running',
      label: d.status === 'pending' ? '排队等待调度' : '执行中',
      time: d.status === 'pending' ? '等待调度' : '进行中…',
      type: 'primary',
      hollow: true,
      active: true,
      duration: '已耗时 ' + fmtDuration(nowMs.value - created)
    })
  }
  return nodes
})

function openDetail(row, column) {
  // 勾选列的点击不应触发行钻取（否则点复选框会弹抽屉）
  if (column && column.type === 'selection') return
  detail.value = row
  detailDrawer.value = true
  // 列表行是轮询快照：活动态任务打开抽屉时立刻单查一次，随后跟随 tick 心跳跟进终态
  if (isTaskActive(row)) refreshDetail()
  // 首屏 GET 全量日志 + 非终态接 WS 追平增量
  openLogStream(row)
}

// 抽屉关闭即断开日志 WS（否则后台持续占着一个连接）
watch(detailDrawer, (open) => {
  if (!open) closeLogStream()
})

// 详情单查（api.getTask → {code,message,data:task}）：抽屉开着且任务在跑时跟随轮询刷新，
// 让时间线/进度条在抽屉里就地走到终态，而不是停在打开那一刻的快照
let detailRefreshing = false
async function refreshDetail() {
  if (detailRefreshing || !detail.value || !detailDrawer.value) return
  // 记下发起时的任务 id：请求期间用户可能已切换到另一行，过期响应不得覆盖新详情
  const id = detail.value.id
  detailRefreshing = true
  try {
    const res = await api.getTask(id)
    if (res.data && detailDrawer.value && detail.value && detail.value.id === id) detail.value = res.data
  } catch (e) {
    // 详情刷新失败静默：下一轮 tick 自动重试，不打扰用户
  } finally {
    detailRefreshing = false
  }
}

function goVM(id) {
  detailDrawer.value = false
  router.push({ name: 'vm-detail', params: { id: String(id) } })
}

const activeCount = computed(() => items.value.filter((t) => t.status === 'pending' || t.status === 'running').length)
const finishedCount = computed(() => items.value.filter((t) => t.status === 'success' || t.status === 'failed').length)

// 前端过滤当前页：任务名 / 虚拟机名模糊匹配（大小写不敏感），只影响表格展示不动计数
const keyword = ref('')
const filteredItems = computed(() => {
  const k = keyword.value.trim().toLowerCase()
  if (!k) return items.value
  return items.value.filter(
    (t) => (t.title || '').toLowerCase().includes(k) || (t.vm_name || '').toLowerCase().includes(k)
  )
})

// 拉取列表（手动刷新与静默轮询共用）：api 调用与响应解包留在页面内，
// 异常自行捕获提示（fetcher 契约），返回 total 由 composable 同步
async function fetchTaskPage({ page, pageSize }) {
  try {
    // "进行中"由后端按 status IN (pending,running) 统一分页/排序；
    // 前端各查一次再拼接会导致一页 2×pageSize、排序错乱、total 语义漂移。
    const params = { page, page_size: pageSize }
    if (q.value.status) params.status = q.value.status
    const res = await api.listTasks(params)
    items.value = (res.data && res.data.items) || []
    return (res.data && res.data.total) || items.value.length
  } catch (e) {
    ElMessage.error(errMsg(e, '获取任务列表失败'))
  }
}

// 智能轮询：有进行中任务才刷（3s），无则停
let pollTimer = null
// 轮询静默刷新：不动 loading（否则整页 v-loading 每 3s 闪一次），范式与 SessionList 一致；
// 保留「上一轮未回 / 首屏加载中就跳过」守卫，避免请求堆叠
let refreshing = false
async function silentRefresh() {
  if (refreshing || loading.value) return
  refreshing = true
  try {
    await fetchTaskPage({ page: page.value, pageSize: pageSize.value })
  } catch (e) {
    // 轮询失败静默，不打扰用户，下一轮自动重试
  } finally {
    refreshing = false
  }
}

function tick() {
  if (activeCount.value > 0) {
    silentRefresh()
  }
  // 抽屉打开且任务是活动态：跟随同一心跳刷新详情（进度/终态），不另起定时器
  if (detailDrawer.value && isTaskActive(detail.value)) {
    refreshDetail()
  }
}

async function remove(row) {
  try {
    await ElMessageBox.confirm(`确定删除任务「${row.title}」的记录？`, '确认删除', {
      type: 'warning',
      confirmButtonClass: 'el-button--danger'
    })
    await api.deleteTask(row.id)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '删除失败'))
  }
}

async function clearFinished() {
  const done = items.value.filter((t) => t.status === 'success' || t.status === 'failed')
  if (!done.length) return
  try {
    await ElMessageBox.confirm(`确定清理本页 ${done.length} 条已完成任务记录？`, '确认清理', {
      type: 'warning',
      confirmButtonClass: 'el-button--danger'
    })
    let failed = 0
    for (const t of done) {
      try {
        await api.deleteTask(t.id)
      } catch (e) {
        failed++
      }
    }
    if (failed) {
      ElMessage.warning(`已清理 ${done.length - failed} 条，${failed} 条失败`)
    } else {
      ElMessage.success('已清理')
    }
    await load()
  } catch (e) {
    if (!isCancel(e)) ElMessage.error('清理失败')
  }
}

onMounted(() => {
  load()
  pollTimer = setInterval(tick, getPollInterval('tasks', POLL_DEFAULTS.tasks))
  // 外部跳转落点：/tasks?id=<taskID> 自动打开该任务详情（自动化执行历史「任务中心 →」入口）
  const qid = Number(route.query.id)
  if (qid) {
    const found = items.value.find((t) => t.id === qid)
    if (found) openDetail(found)
    else {
      // 列表尚未加载完或不在当前页：单查一次补开
      api.getTask(qid).then((res) => {
        if (res && res.data) openDetail(res.data)
      }).catch(() => {})
    }
  }
})
onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
  stopElapsed()
  closeLogStream()
})
</script>

<style scoped>
/* .page-head / .page-title / .toolbar / .count 已收进 global.css；.toolbar-left 骨架与 gap/换行由 Toolbar 组件承担 */
.running-hint {
  color: var(--el-color-primary);
}
.pulse-dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentcolor;
  margin-right: 4px;
  animation: breathe 1.6s ease-in-out infinite;
}
.err-text {
  color: var(--el-color-danger);
  font-size: 0.85rem;
}
.ok-text {
  color: var(--color-success);
  font-size: 0.85rem;
}
.muted-text {
  color: var(--color-muted-foreground);
  font-size: 0.85rem;
}
.pager {
  margin-top: 12px;
  justify-content: flex-end;
}
/* 行点击钻取：el-table 内部渲染的 tr 拿不到本组件 scoped 属性，必须 :deep 穿透 */
:deep(tr.task-row) {
  cursor: pointer;
}
/* 详情抽屉：概要 */
.dt-head {
  padding: 4px 0;
}
.dt-tags {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.dt-title {
  font-size: 15px;
  font-weight: 600;
  margin-bottom: 4px;
  word-break: break-all;
}
.dt-meta {
  font-size: 0.82rem;
  color: var(--color-muted-foreground);
  margin-bottom: 8px;
}
/* 详情抽屉：时间线 */
.dt-timeline {
  padding: 4px 4px 0;
}
.tl-node {
  font-size: 13px;
}
.tl-node-active {
  color: var(--el-color-primary);
}
.tl-duration {
  font-size: 12px;
  color: var(--color-muted-foreground);
  margin-top: 2px;
}
/* 详情抽屉：关联 VM chips */
.vm-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.vm-chip {
  cursor: pointer;
}
.vm-chip .el-icon {
  margin-right: 4px;
  vertical-align: -2px;
}
/* 详情抽屉：通用键值对（result/payload）样式已随渲染下沉到 TaskKVList.vue */
.detail-sec {
  margin: 16px 0 8px;
  font-size: 14px;
}
.detail-row {
  margin-bottom: 8px;
  font-size: 13px;
}
.detail-error {
  margin-top: 12px;
  padding: 8px 12px;
  border-radius: var(--radius-sm);
  background: var(--el-color-danger-light-9);
  color: var(--el-color-danger);
  font-size: 0.85rem;
  word-break: break-all;
}
.kept-title {
  font-size: 13px;
  font-weight: 600;
  margin-bottom: 6px;
}
.kept-list {
  margin: 0;
  padding-left: 4px;
  list-style: none;
  font-size: 13px;
}
.kept-list li {
  margin-bottom: 4px;
  word-break: break-all;
}
.kept-reason {
  margin-left: 8px;
  font-size: 12px;
  color: var(--el-color-warning-dark-2);
}
.kept-list-deleted {
  color: var(--color-muted-foreground);
}
/* 自动重试进度（状态列下方小字） */
.retry-hint {
  margin-top: 2px;
  font-size: 11px;
  color: var(--el-color-warning-dark-2);
}
/* 任务记录可见性说明（viewer/operator 才显示） */
.tl-visibility { margin-bottom: 12px; }

/* 结构化执行日志面板样式已随渲染下沉到 TaskLogPanel.vue */
</style>
