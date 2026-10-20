<!--
  应用商店页（脚本应用 tab 的编排壳）
  ────────────────────────────────────────────────────────────────
  职责：加载应用目录与运行中虚拟机、维护分类/搜索过滤、安装状态机、安装提交与任务日志编排。
  数据流：
    GET /apps + GET /vms → filteredApps / runningVMs
    安装提交 → POST /apps/install → 任务日志抽屉（首屏 GET + WS 增量）
    轮询终态 → 回写 installState（卡片状态）+ 日志抽屉状态/进度
  子组件（./apps/components/）：
    AppCatalog            分类/搜索 + 应用卡片网格（纯展示，意图上抛）
    AppInstallDrawer      安装表单（schema 动态字段，表单对象由本页持有）
    AppInstallLogDrawer   安装日志（GET 全量 + WS 增量，流生命周期自持）
    AppScriptDrawer       detect/install 脚本预览（自拉取）
  刻意没拆：安装提交与轮询编排（submitInstall）——它同时驱动 installState、日志抽屉与全局提示，
  拆出去需要跨组件回传三份状态，收益低于复杂度；保留在壳内便于答辩时一条线讲清安装流程。
-->
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

    <!-- 应用目录：分类筛选 + 关键词搜索 + 卡片网格 -->
    <AppCatalog
      v-model:active-category="activeCategory"
      v-model:app-keyword="appKeyword"
      :loading="loading"
      :apps="filteredApps"
      :running-vms="runningVMs"
      :installing="installing"
      :install-state="installState"
      @install="openInstall"
      @script="openScript"
    />

    <!-- 安装抽屉：应用名 + 版本 + 动态参数表单（按后端 schema 渲染）+ 高级折叠区；
         提交后立即转入任务日志抽屉，实时看安装输出 -->
    <AppInstallDrawer
      v-model="installDialog"
      :current-app="currentApp"
      :running-vms="runningVMs"
      :installing="installing"
      :form="form"
      :normal-fields="normalFields"
      :advanced-fields="advancedFields"
      @vm-change="onInstallVMChange"
      @submit="submitInstall"
      @closed="resetInstall"
    />

    <!-- 任务日志抽屉：提交后立即打开，首屏 GET 全量 + WS 实时增量（复用任务中心的结构化日志端点） -->
    <AppInstallLogDrawer ref="logRef" />

    <!-- 脚本预览抽屉：Detect / Install 两段 -->
    <AppScriptDrawer ref="scriptRef" />

      </el-tab-pane>
      <el-tab-pane label="部署栈" name="stacks" lazy>
        <StackStore />
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<script setup>
// 应用商店壳：只保留跨子组件的数据与安装编排；展示细节见 ./apps/components/。
import { ref, computed, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { api } from '../api'
import { errMsg, clampPct } from '../utils/format'
import { pollTask, extractTaskId, taskErrorMessage } from '../utils/task.js'
import PageHead from '../components/PageHead.vue'
import StackStore from './apps/StackStore.vue'
import AppCatalog from './apps/components/AppCatalog.vue'
import AppInstallDrawer from './apps/components/AppInstallDrawer.vue'
import AppInstallLogDrawer from './apps/components/AppInstallLogDrawer.vue'
import AppScriptDrawer from './apps/components/AppScriptDrawer.vue'

// ===== 应用列表与分类 =====
const activeTab = ref('apps')
const loading = ref(false)
const apps = ref([])
const activeCategory = ref('all')

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

// 日志抽屉与脚本抽屉由子组件自持，壳只通过 ref 调用其公开方法
const logRef = ref(null)
const scriptRef = ref(null)

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
    logRef.value?.open(taskId, appName)
    // 与日志 WS 并行轮询终态：用于卡片状态机与最终提示
    const task = await pollTask(taskId, {
      interval: 2000,
      timeout: 20 * 60 * 1000, // app_install 后端上限 15min（compose pull 不可控），前端必须 ≥ 它
      onProgress: (t) => {
        logRef.value?.setProgress(clampPct(t.progress))
      }
    })
    const skipped = extractSkipped(task.result)
    setInstallState(appId, skipped ? 'already' : 'installed')
    logRef.value?.setStatus('success')
    logRef.value?.setProgress(100)
    ElMessage.success(skipped ? `${appName} 已安装，已跳过` : `${appName} 安装完成`)
  } catch (e) {
    // 提交阶段失败（未产生任务）回到空闲；任务执行失败则标记 failed
    setInstallState(appId, submitted ? 'failed' : '')
    if (submitted) logRef.value?.setStatus('failed')
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

// ===== 脚本预览抽屉 =====
function openScript(app) {
  scriptRef.value?.open(app)
}

onMounted(load)
</script>
