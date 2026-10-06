<template>
    <!-- 栈详情抽屉（Dockge 式）：服务 / 组合日志 / 编排文件编辑 / 参考笔记 -->
    <el-drawer v-model="visible" :title="''" size="60%" :close-on-click-modal="false" @closed="onClosed">
      <template #header>
        <div class="sd-head">
          <span class="sd-name">{{ detail.id || stackId }}</span>
          <el-tag size="small" effect="plain" type="info">{{ detail.category || '未分类' }}</el-tag>
          <el-tag v-if="detail.deployed" size="small" type="success" effect="light">已部署</el-tag>
          <el-tag v-else size="small" effect="light">未部署</el-tag>
          <!-- 漂移：部署副本与仓库模板不一致（用户编辑过） -->
          <el-tooltip v-if="detail.drift" content="部署副本已改动，与仓库模板不一致" placement="top">
            <el-tag size="small" type="warning" effect="light">已改动</el-tag>
          </el-tooltip>
          <span class="sd-spacer" />
          <el-button size="small" type="primary" plain :loading="upgrading" :disabled="!detail.deployed" @click="upgrade">一键升级</el-button>
          <el-button size="small" plain :loading="redeploying" :disabled="!detail.deployed" @click="redeploy">重新部署</el-button>
        </div>
      </template>

      <el-tabs v-model="tab">
        <el-tab-pane label="服务" name="services">
          <el-empty v-if="!services.length" description="未部署或服务列表为空" :image-size="70" />
          <template v-else>
            <!-- 服务卡片矩阵：点击密度主体（状态描边 + 行内按钮） -->
            <div class="sd-svc-grid">
              <el-card
                v-for="s in services" :key="s.Name || s.Service"
                shadow="hover" class="sd-svc-card" :class="'sd-st-' + svcState(s)"
              >
                <div class="sd-svc-head">
                  <span class="sd-svc-name mono" :title="s.Service">{{ s.Service || '—' }}</span>
                  <el-tag :type="svcTag(s)" effect="light" size="small">{{ svcStateText(s) }}</el-tag>
                </div>
                <div class="sd-svc-meta mono" :title="s.Image">{{ s.Image || '—' }}</div>
                <div class="sd-svc-meta mono" :title="s.Name">{{ s.Name || '—' }}</div>
                <div class="sd-svc-actions">
                  <el-button size="small" text type="primary" :disabled="!s.Name" @click="openLogs(s)">日志</el-button>
                  <el-button size="small" text type="primary" :disabled="!s.Name || svcState(s) !== 'running'" @click="openTerminal(s)">终端</el-button>
                  <el-button size="small" text :loading="svcActing === s.Service" :disabled="!s.Service" @click="restartService(s)">重启</el-button>
                </div>
              </el-card>
            </div>
            <!-- 表格视图：端口/状态明细 -->
            <el-table :data="services" size="small" stripe class="sd-table">
              <el-table-column label="服务" min-width="110"><template #default="{ row }"><span class="mono">{{ row.Service || '—' }}</span></template></el-table-column>
              <el-table-column label="容器" min-width="150"><template #default="{ row }"><span class="mono">{{ row.Name || '—' }}</span></template></el-table-column>
              <el-table-column label="镜像" min-width="160" show-overflow-tooltip><template #default="{ row }"><span class="mono">{{ row.Image || '—' }}</span></template></el-table-column>
              <el-table-column label="状态" width="90"><template #default="{ row }"><el-tag :type="svcTag(row)" effect="light" size="small">{{ svcStateText(row) }}</el-tag></template></el-table-column>
              <el-table-column label="端口" min-width="140" show-overflow-tooltip><template #default="{ row }">{{ row.Ports || '—' }}</template></el-table-column>
            </el-table>
          </template>
        </el-tab-pane>

        <el-tab-pane label="组合日志" name="logs" lazy>
          <div v-if="!services.length" class="sd-placeholder">未部署，无日志可看</div>
          <template v-else>
            <div class="sd-log-bar">
              <span class="sd-log-hint">多服务日志合并显示，行首为服务名（按服务着色）</span>
              <el-checkbox v-model="logsFollow" size="small" title="每 2 秒重拉各服务日志">跟随</el-checkbox>
              <el-button size="small" :icon="Refresh" :loading="logsLoading" @click="fetchLogs">刷新</el-button>
            </div>
            <pre class="sd-log-pre"><template v-for="(r, i) in combinedLogs" :key="i"><span :style="{ color: svcColor(r.service) }">{{ r.service }} </span>{{ r.data }}{{ '\n' }}</template><span v-if="!combinedLogs.length" class="sd-log-empty">（暂无日志）</span></pre>
          </template>
        </el-tab-pane>

        <el-tab-pane label="编排文件" name="file" lazy>
          <div class="sd-file-bar">
            <el-button size="small" @click="validateFile" :loading="validating">校验</el-button>
            <el-button size="small" type="primary" @click="saveFile" :loading="saving">保存</el-button>
            <el-button size="small" plain @click="resetToTemplate" :disabled="!detail.drift">重置为模板</el-button>
            <span class="sd-file-hint">仅写部署副本（data/stacks），仓库模板不会被修改</span>
          </div>
          <textarea v-model="fileContent" class="sd-editor" spellcheck="false" />
          <el-collapse class="sd-tmpl">
            <el-collapse-item title="仓库模板原文（只读）" name="t">
              <pre class="sd-tmpl-pre">{{ detail.template || '—' }}</pre>
            </el-collapse-item>
          </el-collapse>
        </el-tab-pane>

        <el-tab-pane v-if="detail.docs && detail.docs.length" label="参考笔记" name="docs" lazy>
          <div v-loading="docsLoading" class="markdown-body" v-html="docsHtml" />
        </el-tab-pane>
      </el-tabs>
    </el-drawer>
</template>

<script setup>
// 栈详情抽屉（R5，Dockge 式）：服务矩阵 + 组合日志（按服务着色）+ compose 在线编辑 + 一键升级。
// 组合日志刻意用 HTTP 轮询而非每服务一条 WS：栈服务常达 5-8 个，全开 WS 连接数不划算，
// 且栈日志关注的是「整体在不在动」；单服务实时日志仍走容器详情抽屉的 WS 流。
import { ref, computed, nextTick, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { api } from '../../api'
import { errMsg, isCancel } from '../../utils/format'

const emit = defineEmits(['changed', 'terminal'])

const visible = ref(false)
const stackId = ref('')
const tab = ref('services')
const detail = ref({})
const services = ref([])

const upgrading = ref(false)
const redeploying = ref(false)
const svcActing = ref('')

// ── 组合日志 ──
const logsFollow = ref(false)
const logsLoading = ref(false)
const combinedLogs = ref([]) // [{service, data}]
let logTimer = null

// ── 编排文件 ──
const fileContent = ref('')
const saving = ref(false)
const validating = ref(false)

// ── 参考笔记 ──
const docsLoading = ref(false)
const docsHtml = ref('')

// 服务着色：固定色板按服务名哈希分配，保证同服务跨刷新同色
const SVC_COLORS = ['#409eff', '#67c23a', '#e6a23c', '#f56c6c', '#909399', '#b37feb', '#36cfc9', '#ff85c0']
function svcColor(name) {
  let h = 0
  for (let i = 0; i < String(name).length; i++) h = (h * 31 + String(name).charCodeAt(i)) >>> 0
  return SVC_COLORS[h % SVC_COLORS.length]
}

function svcState(s) {
  return String((s && s.State) || '').toLowerCase()
}
function svcTag(s) {
  const st = svcState(s)
  if (st === 'running') return 'success'
  if (st === 'exited' || st === 'created') return 'info'
  return 'warning'
}
function svcStateText(s) {
  const map = { running: '运行中', exited: '已退出', created: '已创建', restarting: '重启中', paused: '已暂停', dead: '死亡' }
  return map[svcState(s)] || (s && s.State) || '未知'
}

async function open(id) {
  stackId.value = id
  tab.value = 'services'
  detail.value = {}
  services.value = []
  combinedLogs.value = []
  fileContent.value = ''
  docsHtml.value = ''
  visible.value = true
  await loadDetail()
}

async function loadDetail() {
  try {
    const res = await api.stackDetail(stackId.value)
    const d = (res && res.data) || {}
    detail.value = d
    services.value = d.services || []
    fileContent.value = d.content || ''
  } catch (e) {
    ElMessage.error(errMsg(e, '获取栈详情失败'))
  }
}

// ── 服务操作 ──
async function restartService(s) {
  try {
    await ElMessageBox.confirm(`重启服务 ${s.Service}？`, '重启服务', { type: 'warning', confirmButtonText: '重启' })
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  svcActing.value = s.Service
  try {
    await api.dockerComposeServiceAction(stackId.value, s.Service, 'restart')
    ElMessage.success(`已重启 ${s.Service}`)
    await loadDetail()
  } catch (e) {
    ElMessage.error(errMsg(e, '重启服务失败'))
  } finally {
    svcActing.value = ''
  }
}

// 单服务日志/终端：交给容器域（容器详情抽屉的 WS 流与终端更完整）
function openLogs(s) {
  if (!s.Name) return
  emit('terminal', { ID: s.Name, Names: s.Name, State: svcState(s), openLogs: true })
}
function openTerminal(s) {
  if (!s.Name) return
  emit('terminal', { ID: s.Name, Names: s.Name, State: svcState(s) })
}

// ── 组合日志（HTTP 轮询，多服务合并）──
async function fetchLogs() {
  const running = services.value.filter((s) => s.Name)
  if (!running.length) return
  logsLoading.value = true
  try {
    const parts = await Promise.all(
      running.map(async (s) => {
        try {
          const res = await api.dockerContainerLogs(s.Name, 100)
          const text = (res.data || {}).logs || ''
          return text
            .split('\n')
            .filter((l) => l.trim())
            .slice(-100)
            .map((l) => ({ service: s.Service || s.Name, data: l }))
        } catch (e) {
          return []
        }
      })
    )
    // 按服务分组拼接（非严格时间序：docker logs 无统一时钟，够用即可）
    combinedLogs.value = parts.flat().slice(-2000)
    nextTick(() => {
      const el = document.querySelector('.sd-log-pre')
      if (el && logsFollow.value) el.scrollTop = el.scrollHeight
    })
  } finally {
    logsLoading.value = false
  }
}

function startLogTimer() {
  stopLogTimer()
  logTimer = setInterval(() => { if (visible.value && tab.value === 'logs') fetchLogs() }, 2000)
}
function stopLogTimer() {
  if (logTimer) { clearInterval(logTimer); logTimer = null }
}

// ── 编排文件编辑 ──
async function validateFile() {
  validating.value = true
  try {
    // 用保存接口做「写入前校验」的语义等价物：后端保存前必校验，此处先干跑一次
    await api.saveStackFile(stackId.value, fileContent.value)
    ElMessage.success('compose 文件校验通过（已保存）')
    await loadDetail()
    emit('changed')
  } catch (e) {
    // 后端校验失败会带 docker 原生报错（含行号），直接透出对教学有价值
    ElMessage.error(errMsg(e, 'compose 文件校验失败'))
  } finally {
    validating.value = false
  }
}

async function saveFile() {
  try {
    await ElMessageBox.confirm('保存后需「重新部署」才会生效，确定保存？', '保存编排文件', {
      type: 'info', confirmButtonText: '保存'
    })
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  saving.value = true
  try {
    await api.saveStackFile(stackId.value, fileContent.value)
    ElMessage.success('已保存，需重新部署后生效')
    await loadDetail()
    emit('changed')
  } catch (e) {
    ElMessage.error(errMsg(e, '保存失败'))
  } finally {
    saving.value = false
  }
}

async function resetToTemplate() {
  try {
    await ElMessageBox.confirm('将用仓库模板覆盖部署副本（丢弃你的改动），确定重置？', '重置为模板', {
      type: 'warning', confirmButtonText: '重置', confirmButtonClass: 'el-button--danger'
    })
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  fileContent.value = detail.value.template || ''
  await saveFile()
}

// ── 升级 / 重新部署 ──
async function upgrade() {
  try {
    await ElMessageBox.confirm(
      '将拉取最新镜像并重建变化的服务（compose pull + up -d），耗时可达数十分钟，后台执行。确定升级？',
      '一键升级', { type: 'info', confirmButtonText: '提交升级' }
    )
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  upgrading.value = true
  try {
    const res = await api.upgradeStack(stackId.value)
    ElMessage.success((res.data && res.data.message) || '升级任务已提交')
    ElMessage.info('可在「任务中心」查看进度')
    emit('changed')
  } catch (e) {
    ElMessage.error(errMsg(e, '提交升级失败'))
  } finally {
    upgrading.value = false
  }
}

async function redeploy() {
  try {
    await ElMessageBox.confirm('按当前编排文件重新部署（compose up -d），确定？', '重新部署', {
      type: 'warning', confirmButtonText: '重新部署'
    })
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  redeploying.value = true
  try {
    await api.deployStack(stackId.value)
    ElMessage.success('已重新部署')
    await loadDetail()
    emit('changed')
  } catch (e) {
    ElMessage.error(errMsg(e, '重新部署失败'))
  } finally {
    redeploying.value = false
  }
}

// ── 参考笔记 ──
async function loadDocs() {
  const docs = detail.value.docs || []
  if (!docs.length) return
  docsLoading.value = true
  try {
    const { marked } = await import('marked')
    const DOMPurify = (await import('dompurify')).default
    const md = await api.stackDocs(stackId.value, docs[0])
    docsHtml.value = DOMPurify.sanitize(marked.parse(String(md)))
  } catch (e) {
    docsHtml.value = ''
  } finally {
    docsLoading.value = false
  }
}

// tab 切换：进入日志拉一次并按跟随启停；进入笔记首加载
function onTabChange(t) {
  if (t === 'logs') {
    fetchLogs()
    if (logsFollow.value) startLogTimer()
    else stopLogTimer()
  } else {
    stopLogTimer()
  }
  if (t === 'docs' && !docsHtml.value) loadDocs()
}

watch(tab, onTabChange)
watch(logsFollow, (on) => { if (on && tab.value === 'logs') startLogTimer(); else stopLogTimer() })

function onClosed() {
  stopLogTimer()
}

defineExpose({ open })
</script>

<style scoped>
.sd-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.sd-name {
  font-weight: 600;
  font-size: 1rem;
}
.sd-spacer { flex: 1; }
.sd-svc-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: 12px;
  margin-bottom: 14px;
}
.sd-svc-card :deep(.el-card__body) {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
/* 状态描边：一眼看出哪个服务不在跑 */
.sd-svc-card.sd-st-running { border-left: 3px solid #67c23a; }
.sd-svc-card.sd-st-exited { border-left: 3px solid #909399; }
.sd-svc-card.sd-st-created { border-left: 3px solid #e6a23c; }
.sd-svc-head {
  display: flex;
  align-items: center;
  gap: 6px;
}
.sd-svc-name {
  font-weight: 600;
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sd-svc-meta {
  font-size: 0.78rem;
  color: var(--color-muted-foreground);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sd-svc-actions {
  display: flex;
  gap: 4px;
  padding-top: 6px;
  margin-top: 4px;
  border-top: 1px solid var(--color-border);
}
.sd-table { margin-top: 4px; }
.sd-placeholder {
  padding: 40px 0;
  text-align: center;
  color: var(--color-muted-foreground);
}
.sd-log-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.sd-log-hint {
  font-size: 0.8rem;
  color: var(--el-text-color-secondary, #909399);
  flex: 1;
}
.sd-log-pre {
  margin: 0;
  padding: 12px;
  min-height: 280px;
  max-height: calc(100vh - 300px);
  overflow: auto;
  background: #0d1b2a;
  color: #cfe8ff;
  border-radius: var(--radius-md, 8px);
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
  font-size: 0.8rem;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
  user-select: text;
}
.sd-log-empty { color: #7f9ab5; }
.sd-file-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.sd-file-hint {
  font-size: 0.78rem;
  color: var(--el-text-color-secondary, #909399);
}
.sd-editor {
  width: 100%;
  min-height: 340px;
  max-height: calc(100vh - 360px);
  padding: 12px;
  box-sizing: border-box;
  background: #0d1b2a;
  color: #cfe8ff;
  border: 1px solid var(--color-border, #e4e7ed);
  border-radius: var(--radius-md, 8px);
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
  font-size: 0.8rem;
  line-height: 1.6;
  resize: vertical;
}
.sd-tmpl { margin-top: 10px; }
.sd-tmpl-pre {
  margin: 0;
  padding: 10px;
  max-height: 240px;
  overflow: auto;
  background: var(--color-muted, #f5f7fa);
  border-radius: var(--radius-sm, 4px);
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
  font-size: 0.78rem;
  white-space: pre-wrap;
  word-break: break-all;
}
.markdown-body {
  font-size: 0.9rem;
  line-height: 1.7;
}
</style>
