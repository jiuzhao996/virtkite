<template>
  <div>
    <PageHead title="运维自动化" subtitle="Ansible 批量执行引擎——inventory 由平台虚拟机资产自动生成，SSH 口令经托管凭据注入，目标白名单结构性成立" />

    <!-- 引擎状态 -->
    <el-card shadow="never" class="auto-engine" v-loading="statusLoading">
      <template #header><span class="auto-h">执行引擎</span></template>
      <template v-if="engine.installed">
        <div class="auto-engine-row">
          <el-tag type="success" effect="dark">已就绪</el-tag>
          <span class="mono auto-path">{{ engine.path }}</span>
          <el-tag effect="plain">ansible {{ engine.version }}</el-tag>
        </div>
        <div class="auto-engine-desc">adhoc 支持 ping / command / shell；Playbook 库与编排联动列 P4 S2/S3（docs/plans/P4）</div>
      </template>
      <el-empty v-else :description="engine.hint || '正在探测宿主机引擎…'" :image-size="60" />
    </el-card>

    <!-- 快速执行 -->
    <el-card shadow="never" class="auto-run">
      <template #header><span class="auto-h">快速执行（adhoc）</span></template>
      <el-form label-width="96px" :disabled="!engine.installed">
        <el-form-item label="目标虚拟机">
          <div class="auto-targets">
            <el-select v-model="targets" multiple filterable placeholder="选择运行中的虚拟机（可多选）" style="flex: 1; min-width: 320px">
              <el-option v-for="vm in runnableVMs" :key="vm.id" :label="vm.name + '（' + vm.ip + '）'" :value="vm.id" />
            </el-select>
            <el-button size="small" @click="selectAll" :disabled="!runnableVMs.length">全选</el-button>
            <span class="auto-count">已选 {{ targets.length }} / {{ runnableVMs.length }} 台</span>
          </div>
        </el-form-item>
        <el-form-item label="模块">
          <el-radio-group v-model="module">
            <el-radio-button value="ping">ping（连通性）</el-radio-button>
            <el-radio-button value="command">command（命令）</el-radio-button>
            <el-radio-button value="shell">shell（含管道/重定向）</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="module !== 'ping'" label="执行参数">
          <el-input v-model="args" placeholder="如 free -m ｜ df -h ｜ hostnamectl | grep Operating" class="mono" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="submitting" @click="run">执 行</el-button>
          <span class="auto-hint">目标需运行中、已获 IP 且保存过托管凭据（虚拟机详情页可保存）</span>
        </el-form-item>
      </el-form>
    </el-card>

    <!-- 执行输出 -->
    <el-card shadow="never" v-if="runTask">
      <template #header>
        <div class="auto-out-head">
          <span class="auto-h">执行输出</span>
          <div class="auto-out-meta">
            <el-tag size="small" effect="plain" :type="statusTag">{{ runTask.status }}</el-tag>
            <el-progress v-if="runTask.status === 'running'" :percentage="runTask.progress || 0" :stroke-width="8" style="width: 160px" />
          </div>
        </div>
      </template>
      <pre ref="logBox" class="auto-log mono">{{ logText || '（等待输出…）' }}</pre>
    </el-card>
  </div>
</template>

<script setup>
// 运维自动化（P4 S1）：引擎状态 + adhoc 批量执行 + 输出实时滚动。
// 执行走 ansible_run 异步任务：提交得 task_id → 轮询任务详情 → Result 即日志
// （executor 端 2s 节流落库，前端 1.5s 轮询），终态展示 RECAP 结构化计数。
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { api } from '../api'
import { errMsg } from '../utils/format'
import PageHead from '../components/PageHead.vue'

const engine = ref({})
const statusLoading = ref(true)
const vms = ref([])
const targets = ref([])
const module = ref('ping')
const args = ref('')
const submitting = ref(false)
const runTask = ref(null)
const logBox = ref(null)
let pollTimer = null

const runnableVMs = computed(() => vms.value.filter((v) => v.status === 'running' && v.ip))
const statusTag = computed(() => ({ running: 'warning', success: 'success', failed: 'danger' }[runTask.value?.status] || 'info'))
const logText = computed(() => {
  const t = runTask.value
  if (!t) return ''
  // 失败任务：错误文案优先（执行可能在管线早期就 fail-closed，没有输出）
  if (t.status === 'failed' && t.error) return '✗ ' + t.error
  const r = t.result || ''
  if (!r) return ''
  // 终态结果是 JSON（output=执行原文，recap=逐主机计数）；中间态是纯文本日志
  if (r.startsWith('{')) {
    try {
      const d = JSON.parse(r)
      const parts = [`模块: ${d.module} ｜ 目标: ${d.targets} 台`]
      if (d.output) parts.push(d.output)
      if (d.recap_raw) parts.push('--- PLAY RECAP ---\n' + d.recap_raw)
      else if (d.recap && typeof d.recap === 'object') parts.push('--- RECAP ---\n' + JSON.stringify(d.recap, null, 2))
      return parts.filter(Boolean).join('\n\n')
    } catch {
      return r
    }
  }
  return r
})

async function loadStatus() {
  statusLoading.value = true
  try {
    const res = await api.ansibleStatus()
    engine.value = res.data || {}
  } catch (e) {
    engine.value = { installed: false, hint: errMsg(e, '引擎探测失败') }
  } finally {
    statusLoading.value = false
  }
}

async function loadVMs() {
  try {
    const res = await api.listVMs()
    vms.value = (res.data && res.data.items) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '读取虚拟机列表失败'))
  }
}

function selectAll() {
  targets.value = runnableVMs.value.map((v) => v.id)
}

async function run() {
  if (!targets.value.length) return ElMessage.warning('请选择目标虚拟机')
  if (module.value !== 'ping' && !args.value.trim()) return ElMessage.warning('请填写执行参数')
  submitting.value = true
  try {
    const res = await api.ansibleRun({ targets: targets.value, module: module.value, args: args.value.trim() })
    ElMessage.success('任务已提交')
    startPolling(res.data.task_id)
  } catch (e) {
    ElMessage.error(errMsg(e, '提交失败'))
  } finally {
    submitting.value = false
  }
}

function startPolling(taskId) {
  stopPolling()
  const tick = async () => {
    try {
      const res = await api.getTask(taskId)
      runTask.value = res.data
      if (res.data.status !== 'running' && res.data.status !== 'pending') {
        stopPolling()
        scrollToBottom()
      } else {
        scrollToBottom()
      }
    } catch {
      /* 单次轮询失败静默，下个周期重试 */
    }
  }
  tick()
  pollTimer = setInterval(tick, 1500)
}
function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}
function scrollToBottom() {
  nextTick(() => {
    if (logBox.value) logBox.value.scrollTop = logBox.value.scrollHeight
  })
}

onMounted(async () => {
  await Promise.all([loadStatus(), loadVMs()])
})
onUnmounted(stopPolling)
</script>

<style scoped>
.auto-h { font-weight: 600; font-size: 0.9rem; }
.auto-engine { margin-bottom: 16px; }
.auto-engine-row { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
.auto-path { font-size: 0.82rem; color: var(--color-muted-foreground); word-break: break-all; }
.auto-engine-desc { margin-top: 10px; font-size: 0.8rem; color: var(--color-muted-foreground); }
.auto-run { margin-bottom: 16px; }
.auto-targets { display: flex; align-items: center; gap: 10px; width: 100%; flex-wrap: wrap; }
.auto-count { font-size: 0.8rem; color: var(--color-muted-foreground); white-space: nowrap; }
.auto-hint { margin-left: 12px; font-size: 0.78rem; color: var(--color-muted-foreground); }
.auto-out-head { display: flex; align-items: center; justify-content: space-between; }
.auto-out-meta { display: flex; align-items: center; gap: 12px; }
.auto-log {
  margin: 0; padding: 12px 14px; max-height: 420px; overflow: auto;
  background: var(--color-code-bg, var(--el-fill-color-darker));
  border-radius: var(--radius-sm); font-size: 0.78rem; line-height: 1.7;
  white-space: pre-wrap; word-break: break-all;
}
</style>
