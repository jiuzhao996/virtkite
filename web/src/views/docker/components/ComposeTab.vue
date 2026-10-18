<template>
    <div class="pane-toolbar">
      <span class="count ct-count-inline">共 {{ composeProjects.length }} 个编排项目</span>
    </div>

    <!-- 卡片视图（与容器页同观感）：服务芯片一排展示项目内服务（状态点着色），
         商店部署的栈（ConfigFiles 含 data/stacks）整卡可点进栈详情抽屉 -->
    <div v-loading="loading" class="cp-grid">
      <EmptyState description="暂无 compose 编排项目（docker compose ls 为空）" />
      <el-card
        v-for="row in composeProjects" :key="row.Name" shadow="hover"
        class="cp-card" :class="{ 'cp-clickable': !!stackIdOf(row) }"
        :title="stackIdOf(row) ? '点击进入栈详情' : ''"
        @click="openStack(row)"
      >
        <div class="cp-card-head">
          <span v-if="isRunning(row)" class="cp-live-dot" title="运行中" />
          <span class="cp-card-name mono" :title="row.Name">{{ row.Name || '—' }}</span>
          <el-tag :type="stateTagOf(row)" effect="light" size="small">{{ stateTextOf(row) }}</el-tag>
        </div>
        <div class="cp-services">
          <template v-if="servicesMap[row.Name]">
            <span
              v-for="s in servicesMap[row.Name]" :key="s.Name || s.Service"
              class="cp-svc" :class="svcClass(s)" :title="svcTitle(s)"
            >
              <span class="cp-svc-dot" />
              <span class="mono">{{ s.Service || s.Name }}</span>
            </span>
            <span v-if="!servicesMap[row.Name].length" class="cp-card-meta">（无服务容器）</span>
          </template>
          <span v-else class="cp-card-meta">服务加载中…</span>
        </div>
        <div class="cp-card-meta mono" :title="row.ConfigFiles || ''">{{ row.ConfigFiles || '—' }}</div>
        <div class="cp-card-actions" @click.stop>
          <!-- 状态主键：运行→停止 / 未运行且有 compose 文件→构建启动(up) / 无文件→启动(start) -->
          <el-button
            v-if="isRunning(row)"
            size="small" :icon="VideoPause"
            :loading="composeKey === row.Name + ':stop'"
            :disabled="composeKey !== '' && composeKey !== row.Name + ':stop'"
            @click="composeAction(row, 'stop')"
          >停止</el-button>
          <el-button
            v-else-if="row.ConfigFiles"
            size="small" type="success" plain :icon="VideoPlay"
            :loading="composeKey === row.Name + ':up'"
            :disabled="composeKey !== '' && composeKey !== row.Name + ':up'"
            title="按 compose 文件重建并启动（up -d）"
            @click="composeAction(row, 'up')"
          >构建启动</el-button>
          <el-button
            v-else
            size="small" type="success" plain :icon="VideoPlay"
            :loading="composeKey === row.Name + ':start'"
            :disabled="composeKey !== '' && composeKey !== row.Name + ':start'"
            @click="composeAction(row, 'start')"
          >启动</el-button>
          <el-button
            size="small" :icon="RefreshRight"
            :loading="composeKey === row.Name + ':restart'"
            :disabled="!isRunning(row) || (composeKey !== '' && composeKey !== row.Name + ':restart')"
            @click="composeAction(row, 'restart')"
          >重启</el-button>
          <el-tooltip :content="'下线 ' + row.Name + '（移除全部容器，具名卷保留）'" placement="top">
            <el-button
              size="small" type="danger" plain :icon="Delete"
              class="cp-card-del"
              :loading="composeKey === row.Name + ':down'"
              :disabled="composeKey !== '' && composeKey !== row.Name + ':down'"
              @click="composeAction(row, 'down')"
            />
          </el-tooltip>
        </div>
      </el-card>
    </div>

    <!-- 草稿栈（文件在但未部署，Dockge 的 CREATED_FILE 态）：单列一节，可点进栈详情部署 -->
    <template v-if="draftStacks.length">
      <div class="cp-section-title">未部署（草稿）</div>
      <div class="cp-grid">
        <el-card
          v-for="d in draftStacks" :key="d.name" shadow="hover"
          class="cp-card cp-draft" title="点击进入栈详情部署"
          @click="openStackById(d.name)"
        >
          <div class="cp-card-head">
            <span class="cp-card-name mono" :title="d.name">{{ d.name }}</span>
            <el-tag type="info" effect="light" size="small">{{ stateTextOf(d) }}</el-tag>
          </div>
          <div class="cp-card-meta">compose 文件已就绪，尚未部署</div>
          <div class="cp-card-actions">
            <span class="cp-draft-hint">点击卡片进入栈详情部署</span>
          </div>
        </el-card>
      </div>
    </template>

    <!-- 栈详情抽屉（商店栈专用）：服务矩阵/组合日志/在线编辑/一键升级 -->
    <StackDetailDrawer ref="stackDrawerRef" @changed="refresh" @terminal="onServiceJump" />
</template>

<script setup>
// 编排页（原编排 tab，1Panel 式子路由化）：compose 项目卡片列表与项目级操作自持。
// 卡片额外并行拉取项目服务（compose ps），以服务芯片呈现 Dockge 式密度；
// 商店部署的栈整卡可点进 StackDetailDrawer（服务矩阵/组合日志/编排编辑）。
// 取数失败经 inject('dockerPage') 上报布局壳（503 置门控 alert，其余 toast）。
import { ref, reactive, computed, inject, watch, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { VideoPlay, VideoPause, RefreshRight, Delete } from '@element-plus/icons-vue'
import { api } from '../../../api'
import { errMsg, isCancel } from '../../../utils/format'
import { composeTag } from '../../../utils/docker-format'
import StackDetailDrawer from '../../apps/StackDetailDrawer.vue'
import EmptyState from '../../../components/EmptyState.vue'

// 布局壳通信：失败上报 / 成功清 503 门控
const { reportLoadError, clearLoadError } = inject('dockerPage')
const router = useRouter()
const route = useRoute()

const composeProjects = ref([])
const loading = ref(false)
// 服务芯片数据：{ 项目名: [ComposeService] }；undefined=加载中，[]=确无服务
const servicesMap = reactive({})
// 栈状态（5 态，含草稿）：{ 栈名: state }；拉取失败时为空，前端回落到 compose ls 的 Status
const statesMap = ref({})

// 首次挂载 / 壳刷新按钮 / 操作成功后 共用的重拉入口：列表 + 各项目服务并行补
async function refresh() {
  loading.value = true
  try {
    const res = await api.dockerComposeList()
    composeProjects.value = (res.data || {}).items || []
    clearLoadError()
    loadAllServices()
  } catch (e) {
    reportLoadError(e, '获取编排项目失败')
  } finally {
    loading.value = false
  }
  loadStates() // 状态是增强信息，后台补（不阻塞列表）
}

// 拉栈状态（后端 5 态状态机，含「文件在但未部署」草稿态）
async function loadStates() {
  try {
    const res = await api.composeStates()
    const m = {}
    for (const it of ((res.data || {}).items || [])) m[it.name] = it.state
    statesMap.value = m
  } catch {
    // 增强信息，失败静默（状态标签回落 compose ls 的 Status）
  }
}

// 并行拉各项目服务列表（compose ps 快，项目数量级个位数）；失败置 [] 不阻塞卡片
function loadAllServices() {
  for (const p of composeProjects.value) {
    api.dockerComposeServices(p.Name)
      .then((res) => { servicesMap[p.Name] = ((res.data || {}).items || []) })
      .catch(() => { servicesMap[p.Name] = [] })
  }
}

onMounted(async () => {
  await refresh()
  openStackFromQuery() // 来源跳转：挂载后按 ?stack= 打开对应栈详情
})
defineExpose({ refresh })

// ═══════════════ 编排卡片（compose 项目）═══════════════

function isRunning(row) {
  return String(row.Status || '').startsWith('running')
}

// 商店栈识别：ConfigFiles 含 data/stacks/<id> 即商店部署副本，可进栈详情
function stackIdOf(row) {
  const m = String(row.ConfigFiles || '').match(/data\/stacks\/([A-Za-z0-9_-]+)/)
  return m ? m[1] : ''
}

function openStack(row) {
  openStackById(stackIdOf(row))
}

// 直接按栈名打开详情（草稿卡片与 ?stack= 跳转用）
function openStackById(id) {
  if (!id || !stackDrawerRef.value) return
  stackDrawerRef.value.open(id)
}

// 来源跳转落地：容器「来源」点击 → /containers?tab=compose&stack=<栈名>。
// 一次性消费，避免后续刷新轮询重复弹抽屉。
let stackQueryDone = false
function openStackFromQuery() {
  if (stackQueryDone) return
  const id = route.query.stack
  if (!id) return
  stackQueryDone = true
  const sid = String(id)
  // 仅当该项目确为商店栈（ConfigFiles 指向 data/stacks）或草稿栈时才开详情，
  // 避免对普通手工 compose 项目误开（其详情端点会 404）
  const row = composeProjects.value.find((p) => p.Name === sid)
  if (row) {
    if (stackIdOf(row)) openStackById(sid)
    return
  }
  if (draftStacks.value.some((d) => d.name === sid)) openStackById(sid)
}
watch(() => route.query.stack, (id) => {
  if (id) { stackQueryDone = false; openStackFromQuery() }
})

// ── 栈状态（5 态，借 Dockge）与草稿栈 ──

const STACK_STATE_TEXT = {
  unknown: '未知',
  created_file: '草稿（未部署）',
  created_stack: '已创建',
  running: '运行中',
  exited: '已退出'
}
const STACK_STATE_TAG = {
  unknown: 'info',
  created_file: 'info',
  created_stack: 'warning',
  running: 'success',
  exited: 'danger'
}

function stateTextOf(row) {
  const st = statesMap.value[row.Name || row.name]
  return st ? (STACK_STATE_TEXT[st] || st) : (row.Status || '—')
}
function stateTagOf(row) {
  const st = statesMap.value[row.Name || row.name]
  return st ? (STACK_STATE_TAG[st] || 'info') : composeTag(row.Status)
}

// 草稿栈：状态为 created_file 且不在已部署列表里（data/stacks 有文件但未 up）
const draftStacks = computed(() =>
  Object.entries(statesMap.value)
    .filter(([name, st]) => st === 'created_file' && !composeProjects.value.some((p) => p.Name === name))
    .map(([name, state]) => ({ name, state }))
)

// 栈详情里点服务的终端/日志：切回容器 tab 并由 query 自动开对应抽屉（消费方在 ContainerTab）
function onServiceJump(svc) {
  router.push({ path: '/containers', query: { tab: 'containers', id: svc.ID, open: svc.openLogs ? 'logs' : 'terminal' } })
}

// 服务芯片着色：运行绿 / 退出灰（非零退出码红）/ 暂停黄
function svcClass(s) {
  const st = String(s.State || '').toLowerCase()
  if (st === 'running') return 'cp-svc-run'
  if (st === 'exited') return s.ExitCode ? 'cp-svc-fail' : 'cp-svc-off'
  if (st === 'paused') return 'cp-svc-pause'
  return 'cp-svc-off'
}

function svcTitle(s) {
  return `${s.Service || s.Name} · ${s.Image || '—'} · ${s.Status || s.State || '—'}`
}

// 行内任一项目操作进行中则整列锁定（compose 操作是项目级的，并发互相踩）
const composeKey = ref('')
const stackDrawerRef = ref(null)

async function composeAction(row, action) {
  if (action === 'down') {
    try {
      await ElMessageBox.confirm(
        `下线编排项目 ${row.Name} 将移除其全部容器（具名卷保留），确定下线？`,
        '下线编排项目',
        { type: 'warning', confirmButtonText: '下线', confirmButtonClass: 'el-button--danger' }
      )
    } catch (e) {
      if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
      return
    }
  }
  composeKey.value = row.Name + ':' + action
  try {
    // 项目级操作可能重建多个容器（后端上限 2 分钟），api.dockerComposeAction 已放宽超时
    const res = await api.dockerComposeAction(row.Name, action)
    ElMessage.success((res.data && res.data.message) || '操作完成')
    await refresh()
  } catch (e) {
    ElMessage.error(errMsg(e, '编排操作失败'))
  } finally {
    composeKey.value = ''
  }
}
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
.ct-count-inline {
  margin-left: 0;
}
/* ── 卡片网格（与容器页 ct-grid 同观感）── */
.cp-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(270px, 1fr));
  gap: var(--space-lg);
}
.cp-card {
  display: flex;
  flex-direction: column;
  transition: transform var(--dur-base) var(--ease-standard), box-shadow var(--dur-base) var(--ease-standard);
}
.cp-clickable {
  cursor: pointer;
}
.cp-clickable:hover {
  transform: translateY(-2px);
}
.cp-card :deep(.el-card__body) {
  display: flex;
  flex-direction: column;
  flex: 1;
  gap: 6px;
}
.cp-card-head {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.cp-card-name {
  font-weight: 600;
  font-size: 0.95rem;
  color: var(--color-foreground);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
  min-width: 0;
}
.cp-card-head .el-tag {
  flex: none;
}
/* 运行中呼吸点（复用全局 breathe 关键帧，与容器卡片同款观感） */
.cp-live-dot {
  flex: none;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--color-success, #67c23a);
  animation: breathe 1.6s ease-in-out infinite;
}
.cp-card-meta {
  font-size: 0.82rem;
  color: var(--color-muted-foreground);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
/* ── 服务芯片行：Dockge 式服务密度 ── */
.cp-services {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  min-height: 24px;
}
.cp-svc {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 8px;
  border: 1px solid var(--color-border);
  border-radius: 12px;
  font-size: 0.78rem;
  color: var(--color-foreground);
  max-width: 100%;
}
.cp-svc .mono {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cp-svc-dot {
  flex: none;
  width: 6px;
  height: 6px;
  border-radius: 50%;
}
.cp-svc-run .cp-svc-dot {
  background: var(--color-success, #67c23a);
}
.cp-svc-off .cp-svc-dot {
  background: var(--color-muted-foreground, #909399);
}
.cp-svc-fail .cp-svc-dot {
  background: var(--color-danger, #f56c6c);
}
.cp-svc-pause .cp-svc-dot {
  background: var(--color-warning, #e6a23c);
}
.cp-card-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  padding-top: 12px;
  border-top: 1px solid var(--color-border);
  margin-top: auto;
}
/* 下线钮右对齐独立：危险动作与常规操作分离（与容器卡片同款约定） */
.cp-card-del {
  margin-left: auto;
}
/* 草稿栈分节标题 */
.cp-section-title {
  font-weight: 600;
  font-size: 0.9rem;
  color: var(--color-foreground);
  margin: 18px 0 10px;
}
.cp-draft {
  cursor: pointer;
  border-style: dashed;
}
.cp-draft:hover {
  transform: translateY(-2px);
}
.cp-draft-hint {
  font-size: 0.8rem;
  color: var(--color-muted-foreground);
}
</style>
