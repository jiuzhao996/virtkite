<template>
  <div v-loading="loading">
    <PageHead title="任务中心" subtitle="创建、克隆、删除、关机等耗时操作都在后台异步执行，这里看每个任务的进度和结果；点「详情」可查看失败原因，以及删除虚拟机时被保护保留的共享卷" />
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
          <el-button
            v-if="isAdmin"
            type="danger"
            :icon="Delete"
            :disabled="!finishedCount"
            @click="clearFinished"
          >清理本页已完成 ({{ finishedCount }})</el-button>
        </template>
        <span class="count">共 {{ total }} 个任务<span v-if="activeCount" class="running-hint"> · 本页 {{ activeCount }} 个进行中</span></span>
      </Toolbar>

      <!-- 行点击钻取：整行可点开任务详情抽屉；行内按钮/链接一律 .stop 防止误触发钻取 -->
      <el-table :data="filteredItems" stripe border style="width: 100%" row-class-name="task-row" @row-click="openDetail">
        <template #empty><el-empty description="暂无任务" :image-size="80" /></template>
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column prop="title" label="任务" min-width="200" show-overflow-tooltip />
        <el-table-column label="类型" width="130">
          <template #default="{ row }">{{ taskTypeText(row.type) }}</template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="taskStatusTag(row.status)" effect="light">
              <span v-if="row.status === 'running' || row.status === 'pending'" class="pulse-dot" />
              {{ taskStatusText(row.status) }}
            </el-tag>
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
            <span v-else class="muted-text">执行中…</span>
          </template>
        </el-table-column>
        <el-table-column prop="created_at" label="创建时间" width="170">
          <template #default="{ row }">{{ fmtDateTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="200" fixed="right">
          <template #default="{ row }">
            <el-button text type="primary" size="small" @click.stop="openDetail(row)">详情</el-button>
            <el-button
              v-if="row.status === 'running'"
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

        <!-- 任务参数 payload：后端 model.Task.Payload 为 json:"-" 不下发，本块仅在接口放开后自动生效；
             键名含 password/passwd/secret/token/key（大小写不敏感）时值一律以 ****** 展示 -->
        <template v-if="payloadKVs.length">
          <h4 class="detail-sec">任务参数</h4>
          <div class="kv-list">
            <div v-for="kv in payloadKVs" :key="kv.k" class="kv-row">
              <div class="kv-key mono">{{ kvLabel(kv.k) }}</div>
              <div class="kv-val">
                <span v-if="kv.masked" class="mono kv-masked">******</span>
                <pre v-else-if="kv.pre" class="kv-pre mono">{{ kv.text }}</pre>
                <span v-else class="kv-text">{{ kv.text }}</span>
              </div>
            </div>
          </div>
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
          <div v-if="resultKVs.length" class="kv-list">
            <div v-for="kv in resultKVs" :key="kv.k" class="kv-row">
              <div class="kv-key mono">{{ kvLabel(kv.k) }}</div>
              <div class="kv-val">
                <span v-if="kv.masked" class="mono kv-masked">******</span>
                <pre v-else-if="kv.pre" class="kv-pre mono">{{ kv.text }}</pre>
                <span v-else class="kv-text">{{ kv.text }}</span>
              </div>
            </div>
          </div>
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
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Delete, Search, Monitor } from '@element-plus/icons-vue'
import { api } from '../api'
import { POLL_DEFAULTS, getPollInterval } from '../utils/settings'
import PageHead from '../components/PageHead.vue'
import Toolbar from '../components/Toolbar.vue'
import { useAuth } from '../store/auth'
import { taskTypeText, taskStatusText, taskStatusTag, fmtDateTime, errMsg, isCancel } from '../utils/format'
import { usePagination } from '../composables/usePagination'

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
//   ansible_tasks.go 的 ansible_run），本表优先命中，其余回落共享映射，后端新增类型原样兜底
const EXTRA_TASK_TYPE_TEXT = {
  cleanup_volumes: '清理孤儿卷',
  app_install: '安装应用',
  image_download: '下载镜像',
  ansible_run: '执行 Playbook'
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
  os_version: '系统版本'
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
    nodes.push({
      key: 'finished',
      label: ok ? '执行成功' : '执行失败',
      time: fmtDateTime(d.updated_at),
      type: ok ? 'success' : 'danger',
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

function openDetail(row) {
  detail.value = row
  detailDrawer.value = true
  // 列表行是轮询快照：活动态任务打开抽屉时立刻单查一次，随后跟随 tick 心跳跟进终态
  if (isTaskActive(row)) refreshDetail()
}

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
    // "进行中"= pending+running 两请求并发合并（原"整页拉取再前端过滤"分页数与可见条数漂移）
    if (q.value.status === 'active') {
      const [run, pend] = await Promise.all([
        api.listTasks({ page, page_size: pageSize, status: 'running' }),
        api.listTasks({ page, page_size: pageSize, status: 'pending' })
      ])
      const rl = (run.data && run.data.items) || []
      const pl = (pend.data && pend.data.items) || []
      items.value = [...rl, ...pl]
      return ((run.data && run.data.total) || 0) + ((pend.data && pend.data.total) || 0)
    }
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
/* 详情抽屉：通用键值对（result/payload） */
.kv-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.kv-row {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 8px;
  border-radius: var(--radius-sm);
  background: var(--el-fill-color-light);
}
.kv-key {
  font-size: 12px;
  color: var(--color-muted-foreground);
}
.kv-val {
  font-size: 13px;
  word-break: break-all;
}
.kv-pre {
  margin: 0;
  padding: 8px;
  max-height: 240px;
  overflow: auto;
  background: var(--el-fill-color);
  border-radius: var(--radius-sm);
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
}
.kv-masked {
  color: var(--color-muted-foreground);
  letter-spacing: 2px;
}
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
</style>
