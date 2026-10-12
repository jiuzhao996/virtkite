<template>
  <div>
    <PageHead title="应用商店">
      <template #subtitle>
        <!-- 历史 p.page-desc（UA 外边距参与布局），经插槽原样保留 -->
        <p class="page-desc">选择一台虚拟机，一键安装常用服务（在虚拟机内通过 SSH 执行）</p>
      </template>
    </PageHead>
    <!-- 双 tab（P2A）：VM 内脚本应用（原内容）+ 容器部署栈（声明式 YAML） -->
    <el-tabs v-model="activeTab">
      <el-tab-pane label="脚本应用" name="apps">

    <!-- 没有运行中的虚拟机时无法安装 -->
    <el-alert
      v-if="!loading && runningVMs.length === 0"
      type="warning"
      title="需要至少一台运行中的虚拟机"
      description="请先到「虚拟机」页启动一台虚拟机，再回到本页安装应用。"
      show-icon
      :closable="false"
      style="margin-bottom: 12px"
    />

    <el-card shadow="never" v-loading="loading">
      <!-- 分类筛选与搜索：radio-button 形态横排分类，关键词前端过滤 -->
      <div class="cat-filter">
        <div class="cat-filter-left">
          <el-radio-group v-model="activeCategory">
            <el-radio-button v-for="cat in categoryTabs" :key="cat.value" :value="cat.value">{{ cat.label }}</el-radio-button>
          </el-radio-group>
          <el-input v-model="appKeyword" class="app-search" placeholder="按应用名搜索" clearable :prefix-icon="Search" />
        </div>
      </div>

      <el-empty v-if="filteredApps.length === 0" description="该分类下没有匹配的应用" :image-size="80" />
      <el-row v-else :gutter="12">
        <el-col v-for="app in filteredApps" :key="app.id" :xs="24" :sm="12" :md="8" style="margin-bottom: 12px">
          <el-card shadow="hover" class="app-card">
            <div class="app-head">
              <span class="app-name">
                {{ app.name }}
                <span v-if="app.version" class="app-version">{{ app.version }}</span>
              </span>
              <div class="app-tags">
                <el-tag :type="categoryTag(app.category)" effect="light" size="small">{{ categoryText(app.category) }}</el-tag>
                <!-- 安装状态机：installing / installed / already / failed，行操作随状态置灰 -->
                <el-tag v-if="installTag(app.id)" :type="installTag(app.id).type" effect="light" size="small">
                  {{ installTag(app.id).text }}
                </el-tag>
              </div>
            </div>
            <p class="app-desc">{{ app.desc || '暂无介绍' }}</p>
            <div class="app-actions">
              <el-button
                type="primary"
                :disabled="runningVMs.length === 0 || installing || installState[app.id] === 'installing'"
                :title="runningVMs.length === 0 ? '需要至少一台运行中的虚拟机' : ''"
                @click="openInstall(app)"
              >{{ installState[app.id] === 'installing' ? '安装中…' : '安装' }}</el-button>
              <el-button text type="primary" :disabled="installing" @click="openScript(app)">查看脚本</el-button>
            </div>
          </el-card>
        </el-col>
      </el-row>
    </el-card>

    <!-- 安装抽屉：应用名 + 版本 + 动态参数表单（按后端 schema 渲染）+ 高级折叠区；
         提交后立即转入任务日志抽屉，实时看安装输出 -->
    <el-drawer
      v-model="installDialog"
      :title="currentApp ? `安装 ${currentApp.name}` : '安装应用'"
      size="40%"
      :close-on-click-modal="false"
      :close-on-press-escape="!installing"
      :show-close="!installing"
      @closed="resetInstall"
    >
      <div v-if="currentApp" class="drawer-sub">
        <el-tag v-if="currentApp.version" effect="plain" size="small">版本 {{ currentApp.version }}</el-tag>
        <span class="drawer-desc">{{ currentApp.desc }}</span>
      </div>
      <el-form label-width="96px">
        <el-form-item label="目标虚拟机" required>
          <el-select v-model="form.vm_id" placeholder="选择运行中的虚拟机" style="width: 100%" :disabled="installing" @change="onInstallVMChange">
            <el-option v-for="vm in runningVMs" :key="vm.id" :label="vm.name" :value="vm.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="SSH 地址" required>
          <el-input v-model="form.host" placeholder="虚拟机 IP，如 10.0.0.15" :disabled="installing" />
        </el-form-item>
        <el-form-item label="登录密码" required>
          <el-input v-model="form.password" type="password" show-password placeholder="SSH 登录密码" :disabled="installing" />
        </el-form-item>

        <!-- 动态参数表单：由后端 fields schema 通用渲染，前端不再硬编码每个应用的参数 -->
        <template v-if="normalFields.length">
          <el-divider content-position="left">安装参数</el-divider>
          <el-form-item v-for="f in normalFields" :key="f.key" :label="f.label" :required="f.required">
            <el-input v-if="f.type === 'text'" v-model="form.params[f.key]" :placeholder="f.placeholder || ''" :disabled="installing" />
            <el-input v-else-if="f.type === 'password'" v-model="form.params[f.key]" type="password" show-password :placeholder="f.placeholder || ''" :disabled="installing" />
            <el-input v-else-if="f.type === 'textarea'" v-model="form.params[f.key]" type="textarea" :rows="3" :placeholder="f.placeholder || ''" :disabled="installing" />
            <el-input-number v-else-if="f.type === 'number'" v-model="form.params[f.key]" :min="f.min" :max="f.max" controls-position="right" :disabled="installing" style="width: 100%" />
            <el-select v-else-if="f.type === 'select'" v-model="form.params[f.key]" :disabled="installing" style="width: 100%">
              <el-option v-for="o in (f.options || [])" :key="o.value" :label="o.label" :value="o.value" />
            </el-select>
            <el-switch v-else-if="f.type === 'bool'" v-model="form.params[f.key]" active-value="true" inactive-value="false" :disabled="installing" />
            <div v-if="f.help" class="field-help">{{ f.help }}</div>
          </el-form-item>
        </template>

        <!-- 高级折叠区：advanced 参数 + SSH 端口/用户名（有默认值，通常无需改动） -->
        <el-collapse class="adv-collapse">
          <el-collapse-item title="高级选项" name="adv">
            <el-form-item v-for="f in advancedFields" :key="f.key" :label="f.label" :required="f.required">
              <el-input v-if="f.type === 'text'" v-model="form.params[f.key]" :placeholder="f.placeholder || ''" :disabled="installing" />
              <el-input v-else-if="f.type === 'password'" v-model="form.params[f.key]" type="password" show-password :placeholder="f.placeholder || ''" :disabled="installing" />
              <el-input v-else-if="f.type === 'textarea'" v-model="form.params[f.key]" type="textarea" :rows="3" :placeholder="f.placeholder || ''" :disabled="installing" />
              <el-input-number v-else-if="f.type === 'number'" v-model="form.params[f.key]" :min="f.min" :max="f.max" controls-position="right" :disabled="installing" style="width: 100%" />
              <el-select v-else-if="f.type === 'select'" v-model="form.params[f.key]" :disabled="installing" style="width: 100%">
                <el-option v-for="o in (f.options || [])" :key="o.value" :label="o.label" :value="o.value" />
              </el-select>
              <el-switch v-else-if="f.type === 'bool'" v-model="form.params[f.key]" active-value="true" inactive-value="false" :disabled="installing" />
              <div v-if="f.help" class="field-help">{{ f.help }}</div>
            </el-form-item>
            <el-form-item label="SSH 端口 / 用户">
              <div class="port-user">
                <el-input-number v-model="form.port" :min="1" :max="65535" controls-position="right" :disabled="installing" />
                <el-input v-model="form.user" placeholder="用户名" style="flex: 1" :disabled="installing" />
              </div>
            </el-form-item>
          </el-collapse-item>
        </el-collapse>
      </el-form>
      <template #footer>
        <el-button :disabled="installing" @click="installDialog = false">取消</el-button>
        <el-button type="primary" :loading="installing" @click="submitInstall">{{ installing ? '提交中…' : '开始安装' }}</el-button>
      </template>
    </el-drawer>

    <!-- 任务日志抽屉：提交后立即打开，首屏 GET 全量 + WS 实时增量（复用任务中心的结构化日志端点） -->
    <el-drawer v-model="logDrawer" :title="logTitle" size="55%" @closed="closeLogStream">
      <div class="log-head">
        <el-tag :type="logStatusTag" effect="light">{{ logStatusText }}</el-tag>
        <span v-if="logLive" class="log-live">● 实时</span>
        <el-progress v-if="logRunning" :percentage="logProgress" :stroke-width="8" class="log-progress" />
      </div>
      <div ref="logViewRef" class="log-view mono">
        <div v-if="!logEntries.length" class="log-empty">（等待日志输出…）</div>
        <div v-for="e in logEntries" :key="e.seq" class="log-line" :class="'log-' + e.kind">
          <span class="log-ts">{{ fmtLogTs(e.ts) }}</span>
          <span class="log-text">{{ e.text }}</span>
        </div>
      </div>
      <template #footer>
        <el-button @click="logDrawer = false">关闭</el-button>
      </template>
    </el-drawer>

    <!-- 脚本预览抽屉：Detect / Install 两段 -->
    <el-drawer v-model="scriptDrawer" :title="'安装脚本 — ' + (scriptApp ? scriptApp.name : '')" size="50%">
      <div v-loading="scriptLoading">
        <h4 class="script-title">检测脚本（detect）</h4>
        <pre class="script-pre">{{ scriptData.detect || '（无）' }}</pre>
        <h4 class="script-title">安装脚本（install）</h4>
        <pre class="script-pre">{{ scriptData.install || '（无）' }}</pre>
      </div>
    </el-drawer>

      </el-tab-pane>
      <el-tab-pane label="部署栈" name="stacks" lazy>
        <StackStore />
      </el-tab-pane>
    </el-tabs>
</div>
</template>

<script setup>
import { ref, computed, onMounted, nextTick } from 'vue'
import { ElMessage } from 'element-plus'
import { Search } from '@element-plus/icons-vue'
import { api } from '../api'
import { errMsg, clampPct } from '../utils/format'
import { pollTask, extractTaskId, taskErrorMessage } from '../utils/task.js'
import PageHead from '../components/PageHead.vue'
import StackStore from './apps/StackStore.vue'

// ===== 应用列表与分类 =====
const activeTab = ref('apps')
const loading = ref(false)
const apps = ref([])
const activeCategory = ref('all')

const CATEGORY_TEXT = {
  web: 'Web 服务',
  database: '数据库',
  cache: '缓存',
  runtime: '运行时',
  cms: '内容管理',
  ops: '运维工具'
}

const categoryTabs = [
  { value: 'all', label: '全部' },
  { value: 'web', label: CATEGORY_TEXT.web },
  { value: 'database', label: CATEGORY_TEXT.database },
  { value: 'cache', label: CATEGORY_TEXT.cache },
  { value: 'runtime', label: CATEGORY_TEXT.runtime },
  { value: 'cms', label: CATEGORY_TEXT.cms },
  { value: 'ops', label: CATEGORY_TEXT.ops }
]

function categoryText(c) {
  return CATEGORY_TEXT[c] || c || '其他'
}

function categoryTag(c) {
  const map = { web: 'primary', database: 'success', cache: 'warning', runtime: 'info', cms: 'info', ops: 'info' }
  return map[c] || 'info'
}

// 应用名搜索关键词（前端过滤）
const appKeyword = ref('')

const filteredApps = computed(() =>
  apps.value.filter((a) => {
    if (activeCategory.value !== 'all' && a.category !== activeCategory.value) return false
    const kw = appKeyword.value.trim().toLowerCase()
    if (kw && String(a.name || '').toLowerCase().indexOf(kw) === -1) return false
    return true
  })
)

async function loadApps() {
  const res = await api.listApps()
  apps.value = Array.isArray(res.data) ? res.data : []
}

// ===== 安装状态机（卡片展示 + 按钮置灰）=====
// 本页只跟踪「本会话内发起过安装」的应用状态；跨会话的已装状态由后端 Detect 在安装时判定
// （已装则任务结果 skipped=true，这里映射为「已安装」）。
const installState = ref({}) // appId -> '' | 'installing' | 'installed' | 'already' | 'failed'

function setInstallState(id, state) {
  installState.value = { ...installState.value, [id]: state }
}

function installTag(id) {
  switch (installState.value[id]) {
    case 'installing':
      return { type: 'warning', text: '安装中' }
    case 'installed':
      return { type: 'success', text: '已安装' }
    case 'already':
      return { type: 'success', text: '已安装' }
    case 'failed':
      return { type: 'danger', text: '安装失败' }
    default:
      return null
  }
}

// ===== 目标虚拟机 =====
const vms = ref([])
const runningVMs = computed(() => vms.value.filter((v) => v.status === 'running'))

async function loadVMs() {
  const res = await api.listVMs()
  vms.value = (res.data && res.data.items) || []
}

async function load() {
  loading.value = true
  try {
    await Promise.all([loadApps(), loadVMs()])
  } catch (e) {
    ElMessage.error(errMsg(e, '获取应用商店数据失败'))
  } finally {
    loading.value = false
  }
}

// ===== 安装对话框（含动态参数表单）=====
const installDialog = ref(false)
const installing = ref(false)
const currentApp = ref(null)
const form = ref({ vm_id: null, host: '', port: 22, user: 'root', password: '', params: {} })

// 参数字段来自后端 schema（无 schema 应用为空数组 → 前端不渲染参数区，回落既有交互）
const paramFields = computed(() => (currentApp.value && currentApp.value.fields) || [])
const normalFields = computed(() => paramFields.value.filter((f) => !f.advanced))
const advancedFields = computed(() => paramFields.value.filter((f) => f.advanced))

// initParams 按字段类型初始化表单值：number 用数值（el-input-number 需要），其余用字符串
function initParams(fields) {
  const p = {}
  for (const f of fields || []) {
    if (f.type === 'number') {
      p[f.key] = f.default !== undefined && f.default !== '' ? Number(f.default) : undefined
    } else {
      p[f.key] = f.default !== undefined ? f.default : ''
    }
  }
  return p
}

function openInstall(app) {
  currentApp.value = app
  form.value = {
    vm_id: null,
    host: '',
    port: 22,
    user: 'root',
    password: '',
    params: initParams(app.fields)
  }
  installDialog.value = true
}

// 选定虚拟机即用其已登记 IP（DHCP 租约 / qemu-guest-agent 回填）预填 SSH 地址，可手改
function onInstallVMChange(id) {
  const vm = runningVMs.value.find((v) => v.id === id)
  if (vm && vm.ip) form.value.host = vm.ip
}

async function submitInstall() {
  if (!form.value.vm_id) return ElMessage.warning('请选择目标虚拟机')
  if (!form.value.host.trim()) return ElMessage.warning('请填写虚拟机 SSH 地址')
  if (!form.value.password) return ElMessage.warning('请填写 SSH 登录密码')

  const app = currentApp.value
  const appId = app.id
  const appName = app.name

  // 前端按 schema 做一次轻校验（服务端仍会按同一 schema 再校验，前端仅提升体验）
  const params = {}
  for (const f of paramFields.value) {
    let v = form.value.params[f.key]
    if (v === undefined || v === null || Number.isNaN(v)) v = ''
    v = String(v)
    if (f.required && v.trim() === '' && !f.default) {
      return ElMessage.warning(`请填写「${f.label}」`)
    }
    params[f.key] = v
  }

  installing.value = true
  setInstallState(appId, 'installing')
  let submitted = false
  try {
    const res = await api.installApp({
      vm_id: form.value.vm_id,
      app_id: appId,
      host: form.value.host.trim(),
      port: form.value.port,
      user: form.value.user || 'root',
      password: form.value.password,
      params
    })
    const taskId = extractTaskId(res)
    submitted = true
    // 提交成功：关闭安装抽屉，立即打开任务日志抽屉（实时看安装输出）
    installDialog.value = false
    openLogDrawer(taskId, appName)
    // 与日志 WS 并行轮询终态：用于卡片状态机与最终提示
    const task = await pollTask(taskId, {
      interval: 2000,
      timeout: 600000,
      onProgress: (t) => {
        logProgress.value = clampPct(t.progress)
      }
    })
    const skipped = extractSkipped(task.result)
    setInstallState(appId, skipped ? 'already' : 'installed')
    logStatus.value = 'success'
    logProgress.value = 100
    ElMessage.success(skipped ? `${appName} 已安装，已跳过` : `${appName} 安装完成`)
  } catch (e) {
    // 提交阶段失败（未产生任务）回到空闲；任务执行失败则标记 failed
    setInstallState(appId, submitted ? 'failed' : '')
    if (submitted) logStatus.value = 'failed'
    ElMessage.error(taskErrorMessage(e, '安装失败'))
  } finally {
    installing.value = false
  }
}

// 从任务 result JSON 提取 skipped（已装跳过标记）
function extractSkipped(resultJson) {
  try {
    return !!JSON.parse(resultJson || '{}').skipped
  } catch (e) {
    return false
  }
}

function resetInstall() {
  currentApp.value = null
}

// ===== 任务日志抽屉（复用任务中心结构化日志端点 + WS 增量）=====
const logDrawer = ref(false)
const logAppName = ref('')
const logEntries = ref([])
const logLive = ref(false)
const logStatus = ref('running') // running | success | failed
const logProgress = ref(0)
const logViewRef = ref(null)
let logTaskId = 0
let logLastSeq = 0
let logWs = null
let logRetryTimer = null
let logRetry = 0
let logManualClose = false

const logTitle = computed(() => `安装日志${logAppName.value ? ' · ' + logAppName.value : ''}`)
const logRunning = computed(() => logStatus.value === 'running')
const logStatusText = computed(() => ({ running: '执行中', success: '安装完成', failed: '安装失败' }[logStatus.value] || '执行中'))
const logStatusTag = computed(() => ({ running: 'warning', success: 'success', failed: 'danger' }[logStatus.value] || 'info'))

function openLogDrawer(taskId, appName) {
  logAppName.value = appName || ''
  logEntries.value = []
  logLive.value = false
  logStatus.value = 'running'
  logProgress.value = 0
  logDrawer.value = true
  openLogStream(taskId)
}

function fmtLogTs(ts) {
  const d = ts ? new Date(ts) : new Date()
  if (isNaN(d.getTime())) return ''
  const p = (n) => String(n).padStart(2, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

/** 按 seq 去重追加：GET 首屏全量与 WS 增量可能重叠，seq 保证幂等。 */
function appendLogEntries(list) {
  if (!Array.isArray(list) || !list.length) return
  const have = new Set(logEntries.value.map((e) => e.seq))
  let added = false
  for (const e of list) {
    if (!e || have.has(e.seq)) continue
    have.add(e.seq)
    logEntries.value.push(e)
    if (e.seq > logLastSeq) logLastSeq = e.seq
    added = true
  }
  if (added) scrollLogToBottom()
}

function scrollLogToBottom() {
  nextTick(() => {
    const el = logViewRef.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

function closeLogStream() {
  logManualClose = true
  if (logRetryTimer) {
    clearTimeout(logRetryTimer)
    logRetryTimer = null
  }
  if (logWs) {
    try {
      logWs.close()
    } catch (e) {
      /* 已关闭 */
    }
    logWs = null
  }
  logLive.value = false
}

const isTaskActive = (t) => !!t && (t.status === 'pending' || t.status === 'running')

/** 打开任务日志：首屏 GET 全量 → 记下最大 seq → 非终态则连 WS 追平增量。 */
async function openLogStream(id) {
  closeLogStream()
  logManualClose = false
  logTaskId = id
  logLastSeq = 0
  logRetry = 0
  try {
    const res = await api.getTaskLogs(id)
    if (logTaskId !== id) return // 请求期间已切换任务
    const data = (res && res.data) || {}
    if (data.structured) appendLogEntries(data.entries)
  } catch (e) {
    // 日志端点不可用：静默降级（不打扰用户），仍可由轮询给出最终结果
    return
  }
  connectLogWS(id)
}

/** 连接日志增量 WS（票据一次性，重连会重新取票）。 */
async function connectLogWS(id) {
  let ticket = ''
  try {
    const res = await api.mintTaskLogTicket(id)
    ticket = (res.data && res.data.ticket) || ''
  } catch (e) {
    return // 取票失败：降级为「仅首屏全量」，不空转重连
  }
  if (logManualClose || logTaskId !== id) return
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  const url = `${proto}//${location.host}/api/tasks/${id}/logs/ws?since=${logLastSeq}&ticket=${encodeURIComponent(ticket)}`
  logWs = new WebSocket(url)
  const openTimer = setTimeout(() => {
    try {
      logWs.close()
    } catch (e) {
      /* 已关闭 */
    }
  }, 10000)
  logWs.onopen = () => clearTimeout(openTimer)
  logWs.onmessage = (ev) => {
    let msg
    try {
      msg = JSON.parse(ev.data)
    } catch (e) {
      return
    }
    if (msg.type === 'ready') {
      clearTimeout(openTimer)
      logLive.value = true
    } else if (msg.type === 'entries') {
      appendLogEntries(msg.entries)
    } else if (msg.type === 'done') {
      logLive.value = false
      closeLogStream()
    } else if (msg.type === 'error') {
      closeLogStream()
    }
  }
  logWs.onclose = () => {
    clearTimeout(openTimer)
    logLive.value = false
    if (logManualClose || logTaskId !== id) return
    if (logRetry >= 3) return
    logRetry++
    logRetryTimer = setTimeout(() => connectLogWS(id), 1000 * 2 ** (logRetry - 1))
  }
  logWs.onerror = () => clearTimeout(openTimer)
}

// ===== 脚本预览抽屉 =====
const scriptDrawer = ref(false)
const scriptLoading = ref(false)
const scriptApp = ref(null)
const scriptData = ref({ detect: '', install: '' })

async function openScript(app) {
  scriptApp.value = app
  scriptData.value = { detect: '', install: '' }
  scriptDrawer.value = true
  scriptLoading.value = true
  try {
    const res = await api.appDetail(app.id)
    const d = res.data || {}
    scriptData.value = { detect: d.detect || '', install: d.install || '' }
  } catch (e) {
    ElMessage.error(errMsg(e, '获取脚本失败'))
  } finally {
    scriptLoading.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.cat-filter {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 12px;
}
.cat-filter-left {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.app-search {
  width: 200px;
}
.app-card :deep(.el-card__body) {
  display: flex;
  flex-direction: column;
  height: 100%;
}
.app-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
}
.app-name {
  font-size: 0.95rem;
  font-weight: 600;
}
.app-version {
  margin-left: 6px;
  font-size: 0.72rem;
  font-weight: normal;
  color: var(--el-text-color-secondary, #909399);
}
.app-tags {
  display: flex;
  align-items: center;
  gap: 4px;
  flex: none;
}
.app-desc {
  margin: 8px 0 12px;
  color: var(--el-text-color-secondary, #909399);
  font-size: 0.82rem;
  line-height: 1.5;
  min-height: 38px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.app-actions {
  margin-top: auto;
  display: flex;
  align-items: center;
}
.drawer-sub {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}
.drawer-desc {
  font-size: 0.8rem;
  color: var(--el-text-color-secondary, #909399);
}
.adv-collapse {
  margin-top: 4px;
}
.port-user {
  display: flex;
  gap: 8px;
  width: 100%;
}
.field-help {
  margin-top: 2px;
  font-size: 0.75rem;
  line-height: 1.4;
  color: var(--el-text-color-secondary, #909399);
}
.log-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
}
.log-live {
  font-size: 12px;
  color: var(--color-success, #67c23a);
}
.log-progress {
  flex: 1;
}
.log-view {
  max-height: calc(100vh - 220px);
  overflow: auto;
  padding: 8px 10px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: var(--radius-sm, 6px);
  background: var(--el-fill-color-light);
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
}
.log-empty {
  color: var(--color-muted-foreground, #909399);
}
.log-line {
  display: flex;
  gap: 8px;
}
.log-ts {
  flex: none;
  color: var(--color-muted-foreground, #909399);
  user-select: none;
}
.log-text {
  flex: 1;
  min-width: 0;
}
.log-stderr .log-text {
  color: var(--el-color-danger);
}
.log-command .log-text {
  color: var(--el-color-primary);
}
.log-info .log-text {
  color: var(--color-success, #67c23a);
}
.script-title {
  margin: 12px 0 8px;
  font-size: 0.9rem;
}
.script-pre {
  margin: 0;
  padding: 12px;
  background: #0d1b2a;
  color: #cfe8ff;
  border-radius: var(--radius-md, 8px);
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
  font-size: 0.8rem;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
  user-select: text;
  overflow: auto;
}
</style>
