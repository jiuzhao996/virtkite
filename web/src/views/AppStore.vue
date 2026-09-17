<template>
  <div>
    <div class="page-head">
      <div>
        <h3 class="page-title">应用商店</h3>
        <p class="page-desc">选择一台虚拟机，一键安装常用服务（在虚拟机内通过 SSH 执行）</p>
      </div>
    </div>

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
      <el-tabs v-model="activeCategory">
        <el-tab-pane v-for="cat in categoryTabs" :key="cat.value" :label="cat.label" :name="cat.value" />
      </el-tabs>

      <el-empty v-if="filteredApps.length === 0" description="该分类下暂无应用" :image-size="80" />
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

    <!-- 安装对话框：目标 VM + SSH 凭据；提交后转任务轮询，进度条展示 -->
    <el-dialog
      v-model="installDialog"
      :title="'安装 ' + (currentApp ? currentApp.name : '')"
      width="460px"
      :close-on-click-modal="false"
      :close-on-press-escape="!installing"
      :show-close="!installing"
      @closed="resetInstall"
    >
      <el-form label-width="90px">
        <el-form-item label="目标虚拟机" required>
          <el-select v-model="form.vm_id" placeholder="选择运行中的虚拟机" style="width: 100%" :disabled="installing">
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
    </el-dialog>

    <!-- 脚本预览抽屉：Detect / Install 两段 -->
    <el-drawer v-model="scriptDrawer" :title="'安装脚本 — ' + (scriptApp ? scriptApp.name : '')" size="50%">
      <div v-loading="scriptLoading">
        <h4 class="script-title">检测脚本（detect）</h4>
        <pre class="script-pre">{{ scriptData.detect || '（无）' }}</pre>
        <h4 class="script-title">安装脚本（install）</h4>
        <pre class="script-pre">{{ scriptData.install || '（无）' }}</pre>
      </div>
    </el-drawer>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
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
  const map = { web: 'primary', database: 'success', cache: 'warning', runtime: 'info', cms: 'danger', ops: 'info' }
  return map[c] || 'info'
}

const filteredApps = computed(() =>
  activeCategory.value === 'all' ? apps.value : apps.value.filter((a) => a.category === activeCategory.value)
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

onMounted(load)
</script>

<style scoped>
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
