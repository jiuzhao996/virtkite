<template>
  <div>
    <PageHead title="运维自动化" subtitle="Ansible 批量执行与计划任务调度同页各司其职——inventory 由平台虚拟机资产自动生成，SSH 口令经托管凭据注入，目标白名单结构性成立" />

    <!-- 引擎状态 -->
    <el-card shadow="never" class="auto-engine" v-loading="statusLoading">
      <template #header><span class="auto-h">执行引擎</span></template>
      <template v-if="engine.installed">
        <div class="auto-engine-row">
          <el-tag type="success" effect="dark">已就绪</el-tag>
          <span class="mono auto-path">{{ engine.path }}</span>
          <el-tag effect="plain">ansible {{ engine.version }}</el-tag>
        </div>
        <div class="auto-engine-desc">adhoc：ping / command / shell ｜ playbook：{{ playbooks.length }} 个（内置种子 + 自建，保存前自动语法校验）</div>
        <div v-if="engine.installed && engine.key_fingerprint" class="auto-keyrow">
          <el-tag size="small" type="success" effect="plain">平台密钥</el-tag>
          <span class="mono auto-path">{{ engine.key_fingerprint }}</span>
          <span class="auto-count">免密 {{ engine.ansible_ready_count || 0 }} 台 / 已托管凭据 {{ engine.cred_count || 0 }} 台</span>
          <el-button size="small" :loading="deploying" @click="deployKey">分发公钥到未免密 VM</el-button>
        </div>
      </template>
      <el-empty v-else :description="engine.hint || '正在探测宿主机引擎…'" :image-size="60" />
    </el-card>

    <el-tabs v-model="activeTab">
      <!-- Tab 1 快速执行 -->
      <el-tab-pane label="快速执行" name="adhoc">
        <el-card shadow="never">
          <el-form label-width="96px" :disabled="!engine.installed">
<div class="auto-groups">
            <span class="auto-groups-label">主机组</span>
            <el-tag v-for="g in hostGroups" :key="g.id" class="auto-group-chip" effect="plain"
              :title="'点击选中「' + g.name + '」组成员（并集）'" @click="applyGroup(g)">
              {{ g.name }} ×{{ groupRunnableCount(g) }}
            </el-tag>
            <el-button text size="small" :icon="Setting" @click="groupDrawerOpen = true">
              {{ hostGroups.length ? '管理' : '建主机组' }}
            </el-button>
          </div>
            <el-form-item label="目标虚拟机">
              <div class="auto-targets">
                <el-select v-model="targets" multiple filterable placeholder="选择运行中的虚拟机（可多选）" style="flex: 1; min-width: 320px">
                  <el-option v-for="vm in runnableVMs" :key="vm.id" :label="vmOptionLabel(vm)" :value="vm.id">
                    <span>{{ vm.name }}（{{ vm.ip }}）</span>
                    <el-tag v-if="!sshOK(vm.id)" type="danger" size="small" effect="plain" class="auto-cred-tag">无凭据</el-tag>
                  </el-option>
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
              <el-button type="primary" :loading="submitting" @click="runAdhoc">执 行</el-button>
              <span class="auto-hint">目标需运行中、已获 IP 且保存过托管凭据（虚拟机详情页可保存）</span>
            </el-form-item>
          </el-form>
        </el-card>
      </el-tab-pane>

      <!-- Tab 2 Playbook 库 -->
      <el-tab-pane :label="'Playbook 库（' + playbooks.length + '）'" name="playbooks">
        <el-card shadow="never">
          <div class="auto-pb-bar">
            <el-button size="small" :icon="Refresh" @click="loadPlaybooks">刷新</el-button>
            <el-button type="primary" size="small" :icon="Plus" @click="openEditor(null)" :disabled="!engine.installed" :title="engine.installed ? '' : '宿主机未安装 Ansible'">新建 Playbook</el-button>
            <span class="auto-hint">内置种子出厂预设，删掉会复活；自建完全自治。保存前自动过 ansible-playbook --syntax-check</span>
          </div>
          <el-table :data="playbooks" v-loading="pbLoading" size="small" @row-dblclick="(row) => openEditor(row)">
            <el-table-column prop="id" label="ID" width="150">
              <template #default="{ row }"><span class="mono">{{ row.id }}</span></template>
            </el-table-column>
            <el-table-column prop="name" label="名称" min-width="130" />
            <el-table-column prop="desc" label="说明" min-width="260" show-overflow-tooltip />
            <el-table-column label="标识" width="90">
              <template #default="{ row }">
                <el-tag size="small" :type="row.built_in ? 'primary' : 'success'" effect="plain">{{ row.built_in ? '内置' : '自建' }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="updated_at" label="更新时间" width="170" />
            <el-table-column label="操作" width="200" fixed="right">
              <template #default="{ row }">
                <el-button text size="small" type="primary" :icon="VideoPlay" @click="openRunDialog(row)" :disabled="!engine.installed" :title="engine.installed ? '' : '宿主机未安装 Ansible'">执行</el-button>
                <el-button text size="small" :icon="Edit" @click="openEditor(row)">编辑</el-button>
                <el-popconfirm title="删除该 playbook？" confirm-button-text="删除" @confirm="removePlaybook(row.id)">
                  <template #reference>
                    <el-button text size="small" type="danger" :icon="Delete">删除</el-button>
                  </template>
                </el-popconfirm>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>

      <!-- Tab 3 执行历史 -->
      <el-tab-pane label="执行历史" name="history">
        <el-card shadow="never">
          <div class="auto-pb-bar">
            <el-button size="small" :icon="Refresh" @click="loadHistory">刷新</el-button>
            <span class="auto-hint">ansible_run 类型任务（含 adhoc 与 playbook）</span>
          </div>
          <el-table :data="history" v-loading="histLoading" size="small" row-class-name="clickable-row" @row-click="viewHistoryTask">
            <el-table-column prop="id" label="ID" width="70" />
            <el-table-column prop="title" label="任务" min-width="220" show-overflow-tooltip />
            <el-table-column label="状态" width="100">
              <template #default="{ row }">
                <el-tag size="small" effect="plain" :type="statusTag(row.status)">{{ statusLabel(row.status) }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="created_at" label="提交时间" width="180" />
            <el-table-column label="操作" width="170" fixed="right">
              <template #default="{ row }">
                <el-button
                  v-if="row.status === 'running'"
                  text size="small" type="warning"
                  :loading="cancellingId === row.id"
                  @click.stop="cancelHistoryTask(row)"
                >取消</el-button>
                <!-- 出口：跳到任务中心看该任务的完整时间线与结构化结果 -->
                <el-button text size="small" @click.stop="goTaskCenter(row)">任务中心 →</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>

      <!-- Tab 4 计划任务（2026-10-04 并入，原独立菜单撤销）：调度与引擎同页各司其职；
           后端 /api/crons 挂 AdminMiddleware，tab 仅管理员可见；lazy 使切换到本 tab 才挂载拉数 -->
      <el-tab-pane v-if="isAdmin" label="计划任务" name="cron" lazy>
        <CronList embedded />
      </el-tab-pane>
    </el-tabs>

    <!-- 执行输出（tab 外共用：adhoc / playbook / 历史回看） -->
    <el-card shadow="never" v-if="runTask" class="auto-out-card">
      <template #header>
        <div class="auto-out-head">
          <span class="auto-h">执行输出<span v-if="runTask.title" class="auto-out-title">——{{ runTask.title }}</span></span>
          <div class="auto-out-meta">
            <el-tag size="small" effect="plain" :type="statusTag(runTask.status)">{{ statusLabel(runTask.status) }}</el-tag>
            <el-progress v-if="isRunning" :percentage="runTask.progress || 0" :stroke-width="8" style="width: 160px" />
            <el-button text size="small" :icon="Close" @click="runTask = null" />
          </div>
        </div>
      </template>
      <!-- RECAP 矩阵（playbook 执行才有内容） -->
      <el-table v-if="recapRows.length" :data="recapRows" size="small" class="auto-recap">
        <el-table-column prop="host" label="主机" min-width="160">
          <template #default="{ row }"><span class="mono">{{ row.host }}</span></template>
        </el-table-column>
        <el-table-column label="ok" width="80">
          <template #default="{ row }"><el-tag size="small" type="success" effect="plain">{{ row.ok }}</el-tag></template>
        </el-table-column>
        <el-table-column label="changed" width="90">
          <template #default="{ row }"><el-tag size="small" :type="row.changed ? 'warning' : 'info'" effect="plain">{{ row.changed }}</el-tag></template>
        </el-table-column>
        <el-table-column label="failed" width="80">
          <template #default="{ row }"><el-tag size="small" :type="row.failed ? 'danger' : 'info'" effect="plain">{{ row.failed }}</el-tag></template>
        </el-table-column>
        <el-table-column label="unreachable" width="110">
          <template #default="{ row }"><el-tag size="small" :type="row.unreachable ? 'danger' : 'info'" effect="plain">{{ row.unreachable }}</el-tag></template>
        </el-table-column>
      </el-table>
      <pre ref="logBox" class="auto-log mono">{{ logText || '（等待输出…）' }}</pre>
    </el-card>

    <!-- Playbook 编辑抽屉 -->
    <el-drawer v-model="editorOpen" :title="editorId ? '编辑 Playbook — ' + editorId : '新建 Playbook'" size="62%" :append-to-body="true" destroy-on-close>
      <div class="auto-editor">
        <div v-if="!editorId" class="auto-editor-id">
          <el-input v-model="newId" placeholder="ID（字母数字 - _，如 my-init）" class="mono" style="width: 320px" />
        </div>
        <el-input v-model="editorContent" type="textarea" :rows="22" class="auto-yaml mono" spellcheck="false" placeholder="# vmops-playbook: name=x | desc=y | targets=linux&#10;---&#10;- name: …" />
        <div class="auto-editor-bar">
          <el-button :loading="checking" @click="checkSyntax">语法校验</el-button>
          <el-button type="primary" :loading="saving" @click="savePlaybook">保 存</el-button>
          <span class="auto-hint">头部元数据行：# vmops-playbook: name=名称 | desc=说明 | targets=linux</span>
        </div>
      </div>
    </el-drawer>

    <!-- Playbook 执行对话框 -->
    <el-dialog v-model="runDialogOpen" :title="'执行 Playbook — ' + (runPb?.id || '')" width="560px" :append-to-body="true">
      <el-form label-width="96px">
<div class="auto-groups">
        <span class="auto-groups-label">主机组</span>
        <el-tag v-for="g in hostGroups" :key="g.id" class="auto-group-chip" effect="plain"
              :title="'点击选中「' + g.name + '」组成员（并集）'" @click="applyGroup(g)">
            {{ g.name }} ×{{ groupRunnableCount(g) }}
        </el-tag>
        <el-button text size="small" :icon="Setting" @click="groupDrawerOpen = true">
            {{ hostGroups.length ? '管理' : '建主机组' }}
        </el-button>
          </div>
        <el-form-item label="目标虚拟机">
          <div class="auto-targets">
            <el-select v-model="targets" multiple filterable placeholder="选择运行中的虚拟机（可多选）" style="flex: 1">
              <el-option v-for="vm in runnableVMs" :key="vm.id" :label="vmOptionLabel(vm)" :value="vm.id">
                <span>{{ vm.name }}（{{ vm.ip }}）</span>
                <el-tag v-if="!sshOK(vm.id)" type="danger" size="small" effect="plain" class="auto-cred-tag">无凭据</el-tag>
              </el-option>
            </el-select>
            <el-button size="small" @click="selectAll">全选</el-button>
          </div>
        </el-form-item>
        <el-form-item v-if="runPb" label="说明">
          <span class="auto-hint">{{ runPb.desc || '（无说明）' }}</span>
        </el-form-item>
        <template v-if="runPb && (runPb.vars || []).length">
          <el-form-item v-for="v in runPb.vars" :key="v" :label="v">
            <el-input v-model="extraVars[v]" :placeholder="v + ' 的值（经 -e 注入，覆盖 playbook 默认值）'" class="mono" />
          </el-form-item>
        </template>
      </el-form>
      <template #footer>
        <el-button @click="runDialogOpen = false">取消</el-button>
        <el-button type="primary" :loading="submitting" :disabled="!targets.length" @click="runPlaybook">开始执行</el-button>
      </template>
    </el-dialog>

    <!-- 主机组管理（AU1）：批量执行目标的快捷集合 -->
    <el-drawer v-model="groupDrawerOpen" title="主机组管理" size="440px">
      <div class="auto-group-bar">
        <el-button type="primary" size="small" :icon="Plus" @click="openGroupForm(null)">新建主机组</el-button>
        <span class="auto-hint">点击上方组标签即可一键选中组成员</span>
      </div>
      <el-empty v-if="!hostGroups.length" description="还没有主机组" :image-size="70" />
      <div v-for="g in hostGroups" :key="g.id" class="auto-group-row">
        <div class="auto-group-info">
          <b>{{ g.name }}</b>
          <span class="auto-group-desc">{{ g.description || '—' }}</span>
          <span class="auto-group-n">{{ (g.vm_ids || []).length }} 台成员 · 当前可执行 {{ groupRunnableCount(g) }} 台</span>
        </div>
        <el-button text size="small" type="primary" :icon="Edit" @click="openGroupForm(g)">编辑</el-button>
        <el-button text size="small" type="danger" :icon="Delete" @click="removeGroup(g)">删除</el-button>
      </div>
    </el-drawer>

    <!-- 主机组表单 -->
    <el-dialog v-model="groupFormOpen" :title="groupFormId ? '编辑主机组' : '新建主机组'" width="480px" :append-to-body="true">
      <el-form label-width="72px">
        <el-form-item label="组名" required>
          <el-input v-model="groupFormName" placeholder="如 ceph / k8s-node" maxlength="50" />
        </el-form-item>
        <el-form-item label="说明">
          <el-input v-model="groupFormDesc" placeholder="用途（可空）" maxlength="200" />
        </el-form-item>
        <el-form-item label="成员">
          <el-select v-model="groupFormVMs" multiple filterable placeholder="选择虚拟机（可多选，不限运行状态）" style="width: 100%">
            <el-option v-for="vm in vms" :key="vm.id" :label="vm.name + (vm.ip ? '（' + vm.ip + '）' : '')" :value="vm.id" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="groupFormOpen = false">取消</el-button>
        <el-button type="primary" :loading="groupSaving" @click="saveGroup">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
// 运维自动化（P4 S2）：三 tab——快速执行（adhoc）/ Playbook 库（CRUD + 语法校验）/
// 执行历史（RECAP 矩阵）。执行全走 ansible_run 异步任务：提交得 task_id → 轮询
// 任务详情（executor 端 2s 节流落库 Result，前端 1.5s 轮询即实时日志）。
import { computed, nextTick, onMounted, onUnmounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh, VideoPlay, Edit, Delete, Close, Setting } from '@element-plus/icons-vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api'
import { errMsg, isCancel } from '../utils/format'
import PageHead from '../components/PageHead.vue'
import { useAuth } from '../store/auth'
import CronList from './CronList.vue'

const route = useRoute()
const router = useRouter()
const { isAdmin } = useAuth()
// 深链支持：/crons 重定向到 /automation?tab=cron（仅管理员生效，否则落默认 tab）
const activeTab = ref(route.query.tab === 'cron' && isAdmin.value ? 'cron' : 'adhoc')
const engine = ref({})
const statusLoading = ref(true)
const vms = ref([])
const targets = ref([])
const module = ref('ping')
const args = ref('')
const submitting = ref(false)
const deploying = ref(false)
const extraVars = reactive({})
const runTask = ref(null)
const logBox = ref(null)
let pollTimer = null

// playbook 库
const playbooks = ref([])
const pbLoading = ref(false)
const editorOpen = ref(false)
const editorId = ref('')
const newId = ref('')
const editorContent = ref('')
const checking = ref(false)
const saving = ref(false)
const runDialogOpen = ref(false)
const runPb = ref(null)

// 执行历史
const history = ref([])
const histLoading = ref(false)

const runnableVMs = computed(() => vms.value.filter((v) => v.status === 'running' && v.ip))
const isRunning = computed(() => runTask.value?.status === 'running' || runTask.value?.status === 'pending')

const statusTag = (s) => ({ running: 'warning', success: 'success', failed: 'danger', cancelled: 'info' }[s] || 'info')
const statusLabel = (s) => ({ running: '执行中', pending: '排队中', success: '成功', failed: '失败', cancelled: '已取消' }[s] || s)

// 输出文本：终态 JSON（output/replay recap）；失败显示 error；中间态纯文本
const logText = computed(() => {
  const t = runTask.value
  if (!t) return ''
  if (t.status === 'failed' && t.error) return '✗ ' + t.error
  const r = t.result || ''
  if (!r) return ''
  if (r.startsWith('{')) {
    try {
      const d = JSON.parse(r)
      const head = d.playbook ? `playbook: ${d.playbook} ｜ 目标: ${d.targets} 台` : `模块: ${d.module} ｜ 目标: ${d.targets} 台`
      const parts = [head]
      if (d.output) parts.push(d.output)
      if (d.recap_raw) parts.push('--- PLAY RECAP ---\n' + d.recap_raw)
      return parts.filter(Boolean).join('\n\n')
    } catch {
      return r
    }
  }
  return r
})

// RECAP 矩阵行（playbook 终态 result.recap）
const recapRows = computed(() => {
  const r = runTask.value?.result || ''
  if (!r.startsWith('{')) return []
  try {
    const recap = JSON.parse(r).recap
    if (!recap || typeof recap !== 'object') return []
    return Object.entries(recap).map(([host, c]) => ({
      host,
      ok: c.ok || 0, changed: c.changed || 0, failed: c.failed || 0, unreachable: c.unreachable || 0,
    }))
  } catch {
    return []
  }
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

// ── 主机组 + 凭据标记（AU1）──
const hostGroups = ref([])
const groupDrawerOpen = ref(false)
async function loadHostGroups() {
  try {
    const res = await api.hostGroups()
    hostGroups.value = (res.data && res.data.items) || []
  } catch {
    // 组是增强入口，拉取失败静默（手动选目标不受影响）
  }
}
// SSH 可用口径 = 免密（ansible_ready）∪ 已托管凭据；其余标「无凭据」
const sshOKSet = computed(() => {
  const s = new Set((engine.value.ansible_ready_ids || []).map(Number))
  for (const id of engine.value.cred_vm_ids || []) s.add(Number(id))
  return s
})
function sshOK(id) {
  return sshOKSet.value.has(Number(id))
}
function vmOptionLabel(vm) {
  const base = vm.name + '（' + vm.ip + '）'
  return sshOK(vm.id) ? base : base + ' · 无凭据'
}
function groupRunnableCount(g) {
  const ids = new Set((g.vm_ids || []).map(Number))
  return runnableVMs.value.filter((v) => ids.has(v.id)).length
}
// 点组标签 = 该组成员并入已选（并集；只并入当前可执行的）
function applyGroup(g) {
  const ids = new Set((g.vm_ids || []).map(Number))
  const pick = runnableVMs.value.filter((v) => ids.has(v.id)).map((v) => v.id)
  if (!pick.length) return ElMessage.warning(`「${g.name}」当前没有可执行成员（需运行中且有 IP）`)
  targets.value = [...new Set([...targets.value, ...pick])]
  ElMessage.success(`已并入「${g.name}」可执行成员 ${pick.length} 台`)
}

const groupFormOpen = ref(false)
const groupFormId = ref(null)
const groupFormName = ref('')
const groupFormDesc = ref('')
const groupFormVMs = ref([])
const groupSaving = ref(false)
function openGroupForm(g) {
  groupFormId.value = g ? g.id : null
  groupFormName.value = g ? g.name : ''
  groupFormDesc.value = g ? g.description || '' : ''
  groupFormVMs.value = g ? [...(g.vm_ids || [])] : []
  groupFormOpen.value = true
}
async function saveGroup() {
  if (!groupFormName.value.trim()) return ElMessage.warning('请填写组名')
  groupSaving.value = true
  const payload = { name: groupFormName.value.trim(), description: groupFormDesc.value.trim(), vm_ids: groupFormVMs.value }
  try {
    if (groupFormId.value) await api.hostGroupUpdate(groupFormId.value, payload)
    else await api.hostGroupCreate(payload)
    ElMessage.success('主机组已保存')
    groupFormOpen.value = false
    await loadHostGroups()
  } catch (e) {
    ElMessage.error(errMsg(e, '保存失败'))
  } finally {
    groupSaving.value = false
  }
}
async function removeGroup(g) {
  try {
    await ElMessageBox.confirm(`删除主机组「${g.name}」？不影响虚拟机本身。`, '删除主机组', { type: 'warning', confirmButtonText: '删除', confirmButtonClass: 'el-button--danger' })
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  try {
    await api.hostGroupDelete(g.id)
    ElMessage.success('已删除')
    loadHostGroups()
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

// 取消执行中的任务（AU2）
const cancellingId = ref(null)
async function cancelHistoryTask(row) {
  try {
    await ElMessageBox.confirm(`取消任务「${row.title}」？正在执行的 ansible 进程将被中止。`, '取消任务', { type: 'warning', confirmButtonText: '取消任务' })
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  cancellingId.value = row.id
  try {
    await api.cancelTask(row.id)
    ElMessage.success('已发出取消')
    loadHistory()
  } catch (e) {
    ElMessage.error(errMsg(e, '取消失败'))
  } finally {
    cancellingId.value = null
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

async function loadPlaybooks() {
  pbLoading.value = true
  try {
    const res = await api.ansiblePlaybooks()
    playbooks.value = (res.data && res.data.items) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '读取 playbook 失败'))
  } finally {
    pbLoading.value = false
  }
}

async function deployKey() {
  try {
    await ElMessageBox.confirm('将平台公钥注入所有「已托管凭据且未免密」的虚拟机（幂等，重复执行不重复写）。继续？', '分发公钥', { type: 'info', confirmButtonText: '开始分发' })
  } catch (e) {
    if (!isCancel(e)) return
    else return
  }
  deploying.value = true
  try {
    const res = await api.ansibleDeployKey({})
    const d = res.data || {}
    ElMessage.success(`分发完成：成功 ${d.ok || 0}，跳过 ${d.skipped || 0}，失败 ${(d.failed || []).length}`)
    loadStatus()
  } catch (e) {
    ElMessage.error(errMsg(e, '分发失败'))
  } finally {
    deploying.value = false
  }
}

async function loadHistory() {
  histLoading.value = true
  try {
    const res = await api.listTasks({ page_size: 100 })
    history.value = ((res.data && res.data.items) || []).filter((t) => t.type === 'ansible_run')
  } catch (e) {
    ElMessage.error(errMsg(e, '读取执行历史失败'))
  } finally {
    histLoading.value = false
  }
}

function selectAll() {
  targets.value = runnableVMs.value.map((v) => v.id)
}

async function runAdhoc() {
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

function openRunDialog(pb) {
  runPb.value = pb
  targets.value = []
  Object.keys(extraVars).forEach((k) => delete extraVars[k])
  runDialogOpen.value = true
}

async function runPlaybook() {
  submitting.value = true
  try {
    const ev = {}
    for (const [k, v] of Object.entries(extraVars)) {
      if (v !== '' && v != null) ev[k] = v
    }
    const res = await api.ansibleRun({ targets: targets.value, playbook: runPb.value.id, extra_vars: ev })
    runDialogOpen.value = false
    ElMessage.success('任务已提交')
    startPolling(res.data.task_id)
  } catch (e) {
    ElMessage.error(errMsg(e, '提交失败'))
  } finally {
    submitting.value = false
  }
}

// 编辑器
function openEditor(row) {
  if (row) {
    editorId.value = row.id
    api.ansiblePlaybook(row.id).then((res) => {
      editorContent.value = res.data.content || ''
      editorOpen.value = true
    }).catch((e) => ElMessage.error(errMsg(e, '读取 playbook 失败')))
  } else {
    editorId.value = ''
    newId.value = ''
    editorContent.value = '# vmops-playbook: name=my-playbook | desc=说明 | targets=linux\n---\n- name: 示例\n  hosts: all\n  gather_facts: true\n\n  tasks:\n    - name: 示例任务\n      ansible.builtin.command: hostname\n      changed_when: false\n'
    editorOpen.value = true
  }
}

async function checkSyntax() {
  checking.value = true
  try {
    await api.ansiblePlaybookCheck({ content: editorContent.value })
    ElMessage.success('语法校验通过')
  } catch (e) {
    ElMessage.error(errMsg(e, '校验失败'))
  } finally {
    checking.value = false
  }
}

async function savePlaybook() {
  saving.value = true
  try {
    if (editorId.value) {
      await api.ansiblePlaybookUpdate(editorId.value, { content: editorContent.value })
      ElMessage.success('已保存')
    } else {
      const id = newId.value.trim()
      if (!id) {
        ElMessage.warning('请填写 ID')
        return
      }
      await api.ansiblePlaybookCreate({ id, content: editorContent.value })
      ElMessage.success('已创建：' + id)
      editorId.value = id
      await loadPlaybooks()
    }
    await loadPlaybooks()
  } catch (e) {
    ElMessage.error(errMsg(e, '保存失败'))
  } finally {
    saving.value = false
  }
}

async function removePlaybook(id) {
  try {
    await api.ansiblePlaybookDelete(id)
    ElMessage.success('已删除')
    loadPlaybooks()
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

// 轮询
function startPolling(taskId) {
  stopPolling()
  activeTab.value = 'adhoc'
  const tick = async () => {
    try {
      const res = await api.getTask(taskId)
      runTask.value = res.data
      scrollToBottom()
      if (res.data.status !== 'running' && res.data.status !== 'pending') {
        stopPolling()
        loadHistory()
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
function viewHistoryTask(row) {
  stopPolling()
  runTask.value = row
  scrollToBottom()
}
// 跳任务中心看该任务完整详情（自动化执行历史此前是流程终点，无出口）
function goTaskCenter(row) {
  router.push({ path: '/tasks', query: { id: row.id } })
}
function scrollToBottom() {
  nextTick(() => {
    if (logBox.value) logBox.value.scrollTop = logBox.value.scrollHeight
  })
}

onMounted(async () => {
  await Promise.all([loadStatus(), loadVMs(), loadPlaybooks(), loadHistory(), loadHostGroups()])
})
onUnmounted(stopPolling)
</script>

<style scoped>
.auto-h { font-weight: 600; font-size: 0.9rem; }
.auto-engine { margin-bottom: 16px; }
.auto-engine-row { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
.auto-path { font-size: 0.82rem; color: var(--color-muted-foreground); word-break: break-all; }
.auto-engine-desc { margin-top: 10px; font-size: 0.8rem; color: var(--color-muted-foreground); }
.auto-keyrow { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; margin-top: 10px; }
.auto-groups {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin: 0 0 10px 96px;
}
.auto-groups-label {
  font-size: 0.82rem;
  color: var(--color-muted-foreground);
}
.auto-group-chip {
  cursor: pointer;
}
.auto-cred-tag {
  margin-left: 8px;
}
.auto-group-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
}
.auto-group-row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 10px 4px;
  border-bottom: 1px solid var(--color-border);
}
.auto-group-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.auto-group-desc {
  font-size: 0.8rem;
  color: var(--color-muted-foreground);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.auto-group-n {
  font-size: 0.76rem;
  color: var(--color-muted-foreground);
}
.auto-targets { display: flex; align-items: center; gap: 10px; width: 100%; flex-wrap: wrap; }
.auto-count { font-size: 0.8rem; color: var(--color-muted-foreground); white-space: nowrap; }
.auto-hint { margin-left: 12px; font-size: 0.78rem; color: var(--color-muted-foreground); }
.auto-pb-bar { display: flex; align-items: center; gap: 10px; margin-bottom: 12px; flex-wrap: wrap; }
/* 执行历史表行点击钻取：el-table 内部 tr 拿不到 scoped 属性，须 :deep 穿透 */
:deep(tr.clickable-row) { cursor: pointer; }
.auto-out-card { margin-top: 16px; }
.auto-out-head { display: flex; align-items: center; justify-content: space-between; }
.auto-out-title { font-weight: 400; font-size: 0.82rem; color: var(--color-muted-foreground); }
.auto-out-meta { display: flex; align-items: center; gap: 12px; }
.auto-recap { margin-bottom: 12px; }
.auto-log {
  margin: 0; padding: 12px 14px; max-height: 420px; overflow: auto;
  background: var(--color-code-bg, var(--el-fill-color-darker));
  border-radius: var(--radius-sm); font-size: 0.78rem; line-height: 1.7;
  white-space: pre-wrap; word-break: break-all;
}
.auto-editor { display: flex; flex-direction: column; gap: 12px; }
.auto-editor-bar { display: flex; align-items: center; gap: 10px; }
.auto-yaml :deep(.el-textarea__inner) {
  font-family: var(--font-mono, monospace); font-size: 0.8rem; line-height: 1.7;
}
</style>
