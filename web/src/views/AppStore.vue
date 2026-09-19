<template>
  <div>
    <div class="page-head">
      <div>
        <h3 class="page-title">应用商店</h3>
        <p class="page-desc">{{ viewMode === 'vm' ? '选择一台虚拟机，一键安装常用服务（在虚拟机内通过 SSH 执行）' : '在宿主机上经 Docker Compose 一键安装常用容器应用（拉取镜像可能需要数分钟）' }}</p>
      </div>
      <el-radio-group v-model="viewMode">
        <el-radio-button value="vm">VM 应用（SSH）</el-radio-button>
        <el-radio-button value="container">容器应用（Docker）</el-radio-button>
      </el-radio-group>
    </div>

    <!-- ===== VM 应用（SSH）视图：原有功能保持不动 ===== -->
    <template v-if="viewMode === 'vm'">
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
      <!-- 分类筛选与搜索：与容器应用视图同款 radio-button 形态，关键词两视图共用 -->
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
              <span class="app-name">{{ app.name }}</span>
              <el-tag :type="categoryTag(app.category)" effect="light" size="small">{{ categoryText(app.category) }}</el-tag>
            </div>
            <p class="app-desc">{{ app.desc || '暂无介绍' }}</p>
            <div class="app-actions">
              <el-button
                type="primary"
                :disabled="runningVMs.length === 0 || installing"
                :title="runningVMs.length === 0 ? '需要至少一台运行中的虚拟机' : ''"
                @click="openInstall(app)"
              >安装</el-button>
              <el-button text type="primary" :disabled="installing" @click="openScript(app)">查看脚本</el-button>
            </div>
          </el-card>
        </el-col>
      </el-row>
    </el-card>

    <!-- 安装抽屉：目标 VM + SSH 凭据；提交后转任务轮询，进度条展示（形态对齐容器应用安装抽屉） -->
    <el-drawer
      v-model="installDialog"
      :title="'安装 ' + (currentApp ? currentApp.name : '')"
      size="40%"
      :close-on-click-modal="false"
      :close-on-press-escape="!installing"
      :show-close="!installing"
      @closed="resetInstall"
    >
      <el-form label-width="90px">
        <el-form-item label="目标虚拟机" required>
          <el-select v-model="form.vm_id" placeholder="选择运行中的虚拟机" style="width: 100%" :disabled="installing" @change="onInstallVMChange">
            <el-option v-for="vm in runningVMs" :key="vm.id" :label="vm.name" :value="vm.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="SSH 地址" required>
          <el-input v-model="form.host" placeholder="虚拟机 IP，如 10.0.0.15" :disabled="installing" />
        </el-form-item>
        <el-form-item label="端口 / 用户">
          <div class="port-user">
            <el-input-number v-model="form.port" :min="1" :max="65535" controls-position="right" :disabled="installing" />
            <el-input v-model="form.user" placeholder="用户名" style="flex: 1" :disabled="installing" />
          </div>
        </el-form-item>
        <el-form-item label="密码" required>
          <el-input v-model="form.password" type="password" show-password placeholder="SSH 登录密码" :disabled="installing" />
        </el-form-item>
        <el-form-item v-if="installing || installDone" label="安装进度">
          <div class="progress-wrap">
            <el-progress :percentage="installProgress" :status="installDone ? 'success' : undefined" />
            <span class="progress-hint">{{ installHint }}</span>
          </div>
        </el-form-item>
        <el-form-item v-if="installOutput" label="输出摘要">
          <pre class="output-pre">{{ installOutput }}</pre>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button :disabled="installing" @click="installDialog = false">取消</el-button>
        <el-button type="primary" :loading="installing" @click="submitInstall">{{ installing ? '安装中…' : '开始安装' }}</el-button>
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
    </template>

    <!-- ===== 容器应用（Docker Compose）视图 ===== -->
    <template v-else>
      <!-- Docker 不可用兜底：状态接口失败（含 503）时给出与 DockerList 同款引导 + 重试，安装钮一并禁用 -->
      <el-alert v-if="dockerError" type="error" show-icon :closable="false" style="margin-bottom: 12px">
        <template #title>{{ dockerError }}</template>
        <div class="docker-retry-row">
          <span>容器应用依赖宿主机 Docker 服务，请安装并启动 Docker（systemctl enable --now docker）后重试。</span>
          <el-button size="small" type="primary" :loading="cLoading" @click="retryContainer">重试</el-button>
        </div>
      </el-alert>

      <el-card shadow="never" v-loading="cLoading">
        <div class="cat-filter">
          <div class="cat-filter-left">
            <el-radio-group v-model="activeCCategory">
              <el-radio-button v-for="c in cCategoryTabs" :key="c.value" :value="c.value">{{ c.label }}</el-radio-button>
            </el-radio-group>
            <el-input v-model="appKeyword" class="app-search" placeholder="按应用名搜索" clearable :prefix-icon="Search" />
          </div>
          <el-button text type="primary" :icon="Refresh" @click="loadContainer">刷新</el-button>
        </div>

        <el-empty v-if="filteredCApps.length === 0" description="该分类下没有匹配的容器应用" :image-size="80" />
        <el-row v-else :gutter="12">
          <el-col v-for="app in filteredCApps" :key="app.key" :xs="24" :sm="12" :md="8" style="margin-bottom: 12px">
            <el-card shadow="hover" class="app-card">
              <div class="app-head">
                <span class="app-name">{{ app.name }}</span>
                <span class="app-head-tags">
                  <el-tag v-if="cStatusOf(app)" :type="Number(cStatusOf(app).running) > 0 ? 'success' : 'info'" effect="light" size="small">
                    {{ cStatusText(app) }}
                  </el-tag>
                  <el-tag :type="categoryTag(app.category)" effect="light" size="small">{{ categoryText(app.category) }}</el-tag>
                </span>
              </div>
              <p class="app-desc">{{ app.description || '暂无介绍' }}</p>
              <div class="app-meta">
                <el-tag v-for="t in app.tags" :key="t" size="small" effect="plain" type="info">{{ t }}</el-tag>
                <span v-if="app.version" class="app-ver">v{{ app.version }}</span>
              </div>
              <div class="app-actions">
                <el-button
                  v-if="!cInstalled(app)"
                  type="primary"
                  :disabled="cInstalling || !!dockerError"
                  :title="dockerError ? 'Docker 服务不可用' : ''"
                  @click="openCInstall(app)"
                >安装</el-button>
                <el-button v-else type="danger" plain :loading="uninstallingKey === app.key" @click="uninstallCApp(app)">卸载</el-button>
                <el-button text type="primary" @click="openCompose(app)">查看 compose</el-button>
              </div>
            </el-card>
          </el-col>
        </el-row>
      </el-card>

      <!-- 安装抽屉：按 formFields 动态生成表单；安装为同步执行（compose up 含镜像拉取），loading 直到返回 -->
      <el-drawer
        v-model="cInstallDrawer"
        :title="'安装 ' + (currentCApp ? currentCApp.name : '')"
        size="40%"
        :close-on-click-modal="false"
        :close-on-press-escape="!cInstalling"
        :show-close="!cInstalling"
        @closed="resetCInstall"
      >
        <div v-if="currentCApp" class="cinstall-body">
          <el-alert
            v-if="cInstalling"
            type="info"
            show-icon
            :closable="false"
            title="正在拉取镜像并启动，请耐心等待"
            description="安装为同步执行，镜像较大时可能需要数分钟，请勿关闭本页"
            style="margin-bottom: 16px"
          />
          <el-alert
            v-else
            type="info"
            show-icon
            :closable="false"
            title="按需调整参数，不填的项将使用默认值"
            style="margin-bottom: 16px"
          />
          <el-form label-position="top">
            <el-form-item v-for="f in currentCApp.formFields" :key="f.envKey" :label="f.label" :required="f.required">
              <el-input-number
                v-if="f.type === 'number'"
                v-model="cForm[f.envKey]"
                controls-position="right"
                style="width: 100%"
                :disabled="cInstalling"
              />
              <el-input
                v-else-if="f.type === 'password'"
                v-model="cForm[f.envKey]"
                type="password"
                show-password
                autocomplete="new-password"
                :disabled="cInstalling"
              />
              <el-select v-else-if="f.type === 'select'" v-model="cForm[f.envKey]" style="width: 100%" :disabled="cInstalling">
                <el-option v-for="opt in f.values" :key="opt.value" :label="opt.label" :value="opt.value" />
              </el-select>
              <el-input v-else v-model="cForm[f.envKey]" :disabled="cInstalling" />
              <div v-if="f.rule === 'paramPort'" class="field-tip">请填写 1-65535 之间的整数端口</div>
            </el-form-item>
          </el-form>
        </div>
        <template #footer>
          <el-button :disabled="cInstalling" @click="cInstallDrawer = false">取消</el-button>
          <el-button type="primary" :loading="cInstalling" @click="submitCInstall">{{ cInstalling ? '安装中…' : '安装' }}</el-button>
        </template>
      </el-drawer>

      <!-- compose 预览抽屉：安装前审查将启动的内容 -->
      <el-drawer v-model="composeDrawer" :title="'docker-compose.yml — ' + (composeApp ? composeApp.name : '')" size="40%">
        <div v-loading="composeLoading">
          <pre class="compose-pre">{{ composeData || '（无）' }}</pre>
        </div>
      </el-drawer>
    </template>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Search } from '@element-plus/icons-vue'
import http from '../api'
import { errMsg, clampPct } from '../utils/format'
import { pollTask, extractTaskId, taskErrorMessage } from '../utils/task.js'

// ===== 应用列表与分类 =====
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

// 应用名搜索关键词（两视图共用一个输入，前端过滤）
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
  const res = await http.get('/apps')
  apps.value = Array.isArray(res.data.data) ? res.data.data : []
}

// ===== 目标虚拟机 =====
const vms = ref([])
const runningVMs = computed(() => vms.value.filter((v) => v.status === 'running'))

async function loadVMs() {
  const res = await http.get('/vms')
  vms.value = (res.data.data && res.data.data.items) || []
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

// ===== 安装对话框 =====
const installDialog = ref(false)
const installing = ref(false)
const installProgress = ref(0)
const installDone = ref(false)
const installOutput = ref('')
const currentApp = ref(null)
const form = ref({ vm_id: null, host: '', port: 22, user: 'root', password: '' })

const installHint = computed(() => {
  if (installDone.value) return '安装完成'
  if (installProgress.value > 0) return '任务执行中，进度由后端任务上报'
  return '已提交，等待任务调度…'
})

function openInstall(app) {
  currentApp.value = app
  form.value = { vm_id: null, host: '', port: 22, user: 'root', password: '' }
  installProgress.value = 0
  installDone.value = false
  installOutput.value = ''
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
  installing.value = true
  installProgress.value = 0
  installDone.value = false
  installOutput.value = ''
  try {
    const res = await http.post('/vms/apps/install', {
      vm_id: form.value.vm_id,
      app_id: currentApp.value.id,
      host: form.value.host.trim(),
      port: form.value.port,
      user: form.value.user || 'root',
      password: form.value.password
    })
    // 返回 202 {task_id}；extractTaskId 吃统一响应封套（res.data 即 {code,message,data:{task_id}}）
    const task = await pollTask(extractTaskId(res.data), {
      interval: 2000,
      timeout: 600000,
      onProgress: (t) => {
        installProgress.value = clampPct(t.progress)
      }
    })
    installProgress.value = 100
    installDone.value = true
    // 任务结果 JSON 里的 output 字段是安装输出，截断展示
    installOutput.value = extractOutput(task.result)
    ElMessage.success(`${currentApp.value.name} 安装完成`)
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '安装失败'))
  } finally {
    installing.value = false
  }
}

// 从任务 result JSON 提取 output 字段并截断（完整输出可到任务中心查看）
function extractOutput(resultJson) {
  try {
    const r = JSON.parse(resultJson || '{}')
    const out = String(r.output || '').trim()
    if (!out) return ''
    return out.length > 1000 ? out.slice(0, 1000) + '\n…（已截断，完整输出见任务中心）' : out
  } catch (e) {
    return ''
  }
}

function resetInstall() {
  currentApp.value = null
  installOutput.value = ''
  installDone.value = false
  installProgress.value = 0
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
    const res = await http.get('/apps/' + app.id)
    const d = res.data.data || {}
    scriptData.value = { detect: d.detect || '', install: d.install || '' }
  } catch (e) {
    ElMessage.error(errMsg(e, '获取脚本失败'))
  } finally {
    scriptLoading.value = false
  }
}

// ===== 视图切换：VM 应用（SSH）/ 容器应用（Docker Compose）=====
const viewMode = ref('vm')

// ===== 容器应用视图（GET /appstore：宿主机 Docker Compose 声明式应用包，1Panel 式）=====
const cLoading = ref(false)
const cLoaded = ref(false)
const capps = ref([])
const cStatusList = ref([])
const activeCCategory = ref('all')

// 响应字段兼容：后端实际输出 camelCase（formFields/envKey），兜底任务契约里的 snake_case 写法
function normalizeCApp(raw) {
  const fields = raw.formFields || raw.form_fields || []
  return {
    key: raw.key,
    name: raw.name,
    category: raw.category,
    description: raw.description || raw.desc || '',
    tags: Array.isArray(raw.tags) ? raw.tags : [],
    version: raw.version || '',
    formFields: fields.map((f) => ({
      envKey: f.envKey || f.env_key || '',
      label: f.label || '',
      type: f.type || 'text',
      default: f.default !== undefined && f.default !== null ? f.default : '',
      required: !!f.required,
      rule: f.rule || '',
      values: Array.isArray(f.values) ? f.values : []
    }))
  }
}

async function loadCApps() {
  const res = await http.get('/appstore')
  const d = res.data.data
  // 契约演进兜底：data 可能直接是数组，也可能包一层 { items: [...] }（当前实现为后者）
  const list = Array.isArray(d) ? d : (d && d.items) || []
  capps.value = list.map(normalizeCApp)
}

// Docker 不可用（/appstore/status 失败，含 503）时的引导态：页顶 alert + 重试，安装钮禁用
const dockerError = ref('')

async function loadCStatus() {
  try {
    const res = await http.get('/appstore/status')
    const d = res.data.data
    cStatusList.value = Array.isArray(d) ? d : (d && d.items) || []
    dockerError.value = ''
  } catch (e) {
    // 状态拿不到则按 Docker 不可用处理：不再「一律未安装」静默展示（会诱导重复安装）
    cStatusList.value = []
    dockerError.value = errMsg(e, 'Docker 服务不可用')
  }
}

function retryContainer() {
  dockerError.value = ''
  loadContainer()
}

async function loadContainer() {
  cLoading.value = true
  try {
    await Promise.all([loadCApps(), loadCStatus()])
    cLoaded.value = true
  } catch (e) {
    ElMessage.error(errMsg(e, '获取容器应用列表失败'))
  } finally {
    cLoading.value = false
  }
}

// 切到容器应用视图时按需加载一次（回 VM 视图再切回不重复拉）
watch(viewMode, (v) => {
  if (v === 'container' && !cLoaded.value) loadContainer()
})

// 分类横排按钮：按 category 去重（保持目录出现顺序），「全部」固定在最前
const cCategoryTabs = computed(() => {
  const seen = []
  for (const a of capps.value) {
    if (a.category && !seen.includes(a.category)) seen.push(a.category)
  }
  return [{ value: 'all', label: '全部' }].concat(seen.map((c) => ({ value: c, label: categoryText(c) })))
})

const filteredCApps = computed(() =>
  capps.value.filter((a) => {
    if (activeCCategory.value !== 'all' && a.category !== activeCCategory.value) return false
    const kw = appKeyword.value.trim().toLowerCase()
    if (kw && String(a.name || '').toLowerCase().indexOf(kw) === -1) return false
    return true
  })
)

// 已装状态匹配（尽力）：status 条目的 name 字段即 compose 项目名 = 应用 key，
// 拿不到时退回按 name 显示名匹配
function cStatusOf(app) {
  return (
    cStatusList.value.find((s) => (s.key || s.name) === app.key) ||
    cStatusList.value.find((s) => s.name === app.name) ||
    null
  )
}

function cInstalled(app) {
  return !!cStatusOf(app)
}

function cStatusText(app) {
  const s = cStatusOf(app)
  // 调用方以 cStatusOf(app) 作 v-if，空状态实际不可达；返回空串而非「已安装」这类说谎文案
  if (!s) return ''
  if (Number(s.services) > 0) return `${s.running}/${s.services} 运行中`
  return Number(s.running) > 0 ? '运行中' : '已停止'
}

// ===== 安装抽屉（按 formFields 动态生成表单）=====
const cInstallDrawer = ref(false)
const cInstalling = ref(false)
const currentCApp = ref(null)
const cForm = ref({})

function openCInstall(app) {
  currentCApp.value = app
  // default 回填：number 转 Number、select 兜底首项、其余按字符串
  const form = {}
  for (const f of app.formFields) {
    if (f.type === 'number') {
      const n = Number(f.default)
      form[f.envKey] = f.default !== '' && isFinite(n) ? n : undefined
    } else if (f.type === 'select') {
      form[f.envKey] = f.default !== '' ? f.default : (f.values[0] && f.values[0].value) || ''
    } else {
      form[f.envKey] = String(f.default || '')
    }
  }
  cForm.value = form
  cInstallDrawer.value = true
}

function resetCInstall() {
  currentCApp.value = null
  cForm.value = {}
}

async function submitCInstall() {
  const app = currentCApp.value
  if (!app) return
  // 前置校验：必填 + paramPort 范围（后端仍会以 400 中文文案兜底）
  for (const f of app.formFields) {
    const v = cForm.value[f.envKey]
    if (f.required && (v === '' || v === undefined || v === null)) {
      return ElMessage.warning(`请填写「${f.label}」`)
    }
    if (f.rule === 'paramPort' && v !== '' && v !== undefined && v !== null) {
      const n = Number(v)
      if (!Number.isInteger(n) || n < 1 || n > 65535) {
        return ElMessage.warning(`「${f.label}」需为 1-65535 的整数`)
      }
    }
  }
  // 后端 values 是 map[string]string：number 一律转字符串提交（JSON 数字会导致后端绑定失败）
  const values = {}
  for (const [k, v] of Object.entries(cForm.value)) {
    values[k] = v === undefined || v === null ? '' : String(v)
  }
  cInstalling.value = true
  try {
    // 同步安装（服务层 compose up 含镜像拉取，10 分钟超时）：必须覆盖 axios 全局 15s 超时
    await http.post(`/appstore/${app.key}/install`, { values }, { timeout: 660000 })
    ElMessage.success(`${app.name} 安装完成`)
    cInstallDrawer.value = false
    loadCStatus()
  } catch (e) {
    ElMessage.error(errMsg(e, '安装失败'))
  } finally {
    cInstalling.value = false
  }
}

// ===== 卸载（compose down，保留数据目录）=====
const uninstallingKey = ref('')

async function uninstallCApp(app) {
  try {
    await ElMessageBox.confirm(
      `确定卸载 ${app.name}？容器将停止并移除，数据目录将保留，重新安装后数据仍在。`,
      '卸载确认',
      { type: 'warning', confirmButtonText: '卸载', cancelButtonText: '取消', confirmButtonClass: 'el-button--danger' }
    )
  } catch (e) {
    return // 用户取消
  }
  uninstallingKey.value = app.key
  try {
    await http.post(`/appstore/${app.key}/uninstall`, { remove_data: false }, { timeout: 120000 })
    ElMessage.success(`${app.name} 已卸载（数据目录已保留）`)
    loadCStatus()
  } catch (e) {
    ElMessage.error(errMsg(e, '卸载失败'))
  } finally {
    uninstallingKey.value = ''
  }
}

// ===== compose 预览抽屉 =====
const composeDrawer = ref(false)
const composeLoading = ref(false)
const composeApp = ref(null)
const composeData = ref('')

async function openCompose(app) {
  composeApp.value = app
  composeData.value = ''
  composeDrawer.value = true
  composeLoading.value = true
  try {
    const res = await http.get('/appstore/' + app.key)
    composeData.value = (res.data.data && res.data.data.compose) || ''
  } catch (e) {
    ElMessage.error(errMsg(e, '获取 compose 内容失败'))
  } finally {
    composeLoading.value = false
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
.docker-retry-row {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.app-card :deep(.el-card__body) {
  display: flex;
  flex-direction: column;
  height: 100%;
}
.app-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.app-name {
  font-size: 0.95rem;
  font-weight: 600;
}
.app-head-tags {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
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
.app-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 12px;
}
.app-ver {
  margin-left: auto;
  color: var(--el-text-color-secondary, #909399);
  font-size: 0.75rem;
  font-family: var(--font-mono, monospace);
}
.app-actions {
  margin-top: auto;
  display: flex;
  align-items: center;
}
.cinstall-body {
  min-height: 120px;
}
.field-tip {
  font-size: 0.75rem;
  color: var(--el-text-color-secondary, #909399);
  line-height: 1.4;
  margin-top: 4px;
}
.compose-pre {
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
.port-user {
  display: flex;
  gap: 8px;
  width: 100%;
}
.progress-wrap {
  width: 100%;
}
.progress-hint {
  font-size: 0.78rem;
  color: var(--el-text-color-secondary, #909399);
}
.output-pre {
  margin: 0;
  padding: 10px;
  max-height: 180px;
  overflow: auto;
  width: 100%;
  background: #0d1b2a;
  color: #cfe8ff;
  border-radius: var(--radius-md, 8px);
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
  font-size: 0.78rem;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
  user-select: text;
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
