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

    <el-tabs v-model="activeTab" @tab-change="onTabChange">
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
            <el-form-item label="目标方式">
              <el-radio-group v-model="targetMode">
                <el-radio-button value="vms">按虚拟机</el-radio-button>
                <el-radio-button value="group">按主机组（动态）</el-radio-button>
              </el-radio-group>
              <span class="auto-hint">按组执行：成员在触发时动态解析，改组即生效</span>
            </el-form-item>
            <el-form-item v-if="targetMode === 'group'" label="主机组">
              <el-select v-model="groupId" placeholder="选择主机组" style="width: 300px">
                <el-option v-for="g in hostGroups" :key="g.id" :label="g.name + '（' + groupRunnableCount(g) + ' 台可执行）'" :value="g.id" />
              </el-select>
            </el-form-item>
            <el-form-item v-else label="目标虚拟机">
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
              <span class="auto-hint">目标需运行中、已获 IP 且保存过托管凭据（建机时勾选「自动初始化」会自动托管；存量机需管理员预置）</span>
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
          <AnsiblePlaybookTable
            :playbooks="playbooks"
            :loading="pbLoading"
            :installed="engine.installed"
            @run="openRunDialog"
            @files="openFiles"
            @edit="openEditor"
            @delete="removePlaybook"
          />
        </el-card>
      </el-tab-pane>

      <!-- Tab 3 Role 库（共享 role：一个 role 对全部 playbook 可见，无需复制） -->
      <el-tab-pane :label="'Role 库（' + roles.length + '）'" name="roles">
        <el-card shadow="never">
          <div class="auto-pb-bar">
            <el-button size="small" :icon="Refresh" @click="loadRoles">刷新</el-button>
            <el-button type="primary" size="small" :icon="Plus" @click="createRole">新建 Role</el-button>
            <span class="auto-hint">
              存在 data/ansible/roles/，任何 playbook 都能 <code>roles: [名称]</code> 引用，不必各自复制一份；
              playbook 本地同名 role 会覆盖共享库里的（想单独特化某个 playbook 时用）
            </span>
          </div>
          <AnsibleRoleTable
            :roles="roles"
            :loading="roleLoading"
            @files="openRoleFiles"
            @delete="removeRole"
          />
        </el-card>
      </el-tab-pane>

      <!-- Tab 4 执行历史 -->
      <el-tab-pane label="执行历史" name="history">
        <el-card shadow="never">
          <div class="auto-pb-bar">
            <el-button size="small" :icon="Refresh" @click="loadHistory">刷新</el-button>
            <span class="auto-hint">ansible_run 类型任务（含 adhoc 与 playbook）</span>
          </div>
          <AnsibleHistoryTable
            :history="history"
            :loading="histLoading"
            :cancelling-id="cancellingId"
            :status-tag="statusTag"
            :status-label="statusLabel"
            @view="viewHistoryTask"
            @cancel="cancelHistoryTask"
            @task-center="goTaskCenter"
          />
        </el-card>
      </el-tab-pane>

      <!-- Tab 4 计划任务（2026-10-04 并入，原独立菜单撤销）：调度与引擎同页各司其职；
           后端 /api/crons 挂 AdminMiddleware，tab 仅管理员可见；lazy 使切换到本 tab 才挂载拉数 -->
      <el-tab-pane v-if="isAdmin" label="计划任务" name="cron" lazy>
        <CronList embedded />
      </el-tab-pane>
    </el-tabs>

    <!-- 执行输出（tab 外共用：adhoc / playbook / 历史回看）；渲染下沉到 AnsibleOutputCard 子组件，
         页面只喂任务/结果/文本，并在每轮轮询后转发 scrollToBottom -->
    <AnsibleOutputCard
      v-if="runTask"
      ref="outputCardRef"
      :task="runTask"
      :recap-rows="recapRows"
      :log-text="logText"
      :is-running="isRunning"
      :status-tag="statusTag"
      :status-label="statusLabel"
      @close="closeRunCard"
    />

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

    <!-- 新建 Role：名称 + 说明一个表单收齐（连开两个 ElMessageBox.prompt 的写法既脆又难用） -->
    <el-dialog v-model="roleDialogOpen" title="新建 Role" width="480px" append-to-body>
      <el-form label-width="72px">
        <el-form-item label="名称">
          <el-input v-model="roleForm.name" placeholder="字母数字开头，可含 _ -（如 web-baseline）" />
        </el-form-item>
        <el-form-item label="说明">
          <el-input v-model="roleForm.desc" placeholder="一句话说明，写进 role 头部与清单页" />
        </el-form-item>
      </el-form>
      <div class="auto-hint">
        会生成标准 role 骨架（tasks/ handlers/ defaults/ vars/ meta/ templates/ files/），
        建完直接进文件编辑；任何 playbook 都能 <code>roles: [名称]</code> 引用它。
      </div>
      <template #footer>
        <el-button @click="roleDialogOpen = false">取消</el-button>
        <el-button type="primary" :loading="roleCreating" @click="submitRole">创建</el-button>
      </template>
    </el-dialog>

    <!-- 受管目录文件管理：playbook 目录（role 结构）与共享 role 库共用同一组件 -->
    <DirFilesDialog ref="filesDialog" kind="playbook" @changed="loadPlaybooks" />
    <DirFilesDialog ref="roleFilesDialog" kind="role" @changed="loadRoles" />

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
        <el-form-item label="目标方式">
          <el-radio-group v-model="targetMode">
            <el-radio-button value="vms">按虚拟机</el-radio-button>
            <el-radio-button value="group">按主机组（动态）</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="targetMode === 'group'" label="主机组">
          <el-select v-model="groupId" placeholder="选择主机组" style="width: 300px">
            <el-option v-for="g in hostGroups" :key="g.id" :label="g.name + '（' + groupRunnableCount(g) + ' 台可执行）'" :value="g.id" />
          </el-select>
        </el-form-item>
        <el-form-item v-else label="目标虚拟机">
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
        <el-form-item v-if="runPb" label="执行选项">
          <div class="run-opts">
            <el-checkbox v-model="runCheck">预演（--check：只报会改什么，不真改）</el-checkbox>
            <el-checkbox v-model="runDiff">显示差异（--diff）</el-checkbox>
            <el-input v-model="runTags" size="small" placeholder="只跑标签 -t，如 nginx,config" style="width: 240px" />
            <el-input v-model="runSkipTags" size="small" placeholder="跳过标签 --skip-tags" style="width: 200px" />
          </div>
        </el-form-item>
        <el-form-item v-if="runPb" label="说明">
          <span class="auto-hint">{{ runPb.desc || '（无说明）' }}</span>
        </el-form-item>
        <!-- 执行参数（Survey Var）：按类型渲染控件——select/enum 下拉、int 数字、text 多行、
             secret 密码框（不回显、不落前端持久化）、str 单行；必填项前端先挡一道 -->
        <template v-if="runSurvey.length">
          <el-divider content-position="left"><span class="auto-survey-title">执行参数</span></el-divider>
          <el-form-item v-for="v in runSurvey" :key="v.name" :label="surveyLabel(v)" :required="v.required">
            <div class="auto-survey-field">
              <el-select v-if="v.type === 'select' || v.type === 'enum'" v-model="extraVars[v.name]" clearable :placeholder="'请选择' + surveyLabel(v)" style="width: 260px">
                <el-option v-for="c in (v.values || [])" :key="c.value" :label="c.name" :value="c.value" />
              </el-select>
              <el-input-number v-else-if="v.type === 'int'" v-model="extraVars[v.name]" :controls="false" class="mono" style="width: 200px" placeholder="整数" />
              <el-input v-else-if="v.type === 'text'" v-model="extraVars[v.name]" type="textarea" :rows="3" class="mono" :placeholder="v.description || ''" />
              <el-input v-else-if="v.type === 'secret'" v-model="extraVars[v.name]" type="password" autocomplete="new-password" class="mono" :placeholder="v.description || '敏感值：加密存储，不回显，执行时经环境变量注入'" />
              <el-input v-else v-model="extraVars[v.name]" class="mono" :placeholder="v.description || ''" />
              <span v-if="v.description && v.type !== 'text' && v.type !== 'secret'" class="auto-hint">{{ v.description }}</span>
              <el-tag v-if="v.type === 'secret'" size="small" type="warning" effect="plain" class="auto-secret-tag">密钥</el-tag>
            </div>
          </el-form-item>
        </template>
      </el-form>
      <template #footer>
        <el-button @click="runDialogOpen = false">取消</el-button>
        <el-button type="primary" :loading="submitting" :disabled="targetMode === 'group' ? !groupId : !targets.length" @click="runPlaybook">开始执行</el-button>
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
// ============================================================================
// 运维自动化（Automation）
// ----------------------------------------------------------------------------
// 职责：Ansible 批量执行（快速执行/Playbook/Role 库）与计划任务调度的同页入口。
//       五个 tab：快速执行(adhoc) / Playbook 库 / Role 库 / 执行历史 / 计划任务(仅管理员)。
//
// 数据流：
//   执行 ← runAdhoc / runPlaybook 提交 api.ansibleRun 得 task_id → startPolling 每 1.5s
//          轮询 api.getTask，把 runTask 喂给执行输出卡（executor 端 2s 节流落库 Result）
//   目标 ← runnableVMs（运行中且有 IP）+ hostGroups（主机组，触发时动态解析成员）
//          + 凭据标记 sshOK（免密 ∪ 已托管凭据）
//   深链 ← ?tab= 双向同步（进来读 query、切 tab 写 query，见 queryTabName/onTabChange）
//
// 已拆出的子组件（列表/输出的纯展示壳，动作 emit 回本页）：
//   automation/components/AnsibleOutputCard.vue    执行输出卡（RECAP 矩阵 + 日志）
//   automation/components/AnsiblePlaybookTable.vue Playbook 库表格
//   automation/components/AnsibleRoleTable.vue     Role 库表格
//   automation/components/AnsibleHistoryTable.vue  执行历史表格
// ============================================================================
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh, Edit, Delete, Setting } from '@element-plus/icons-vue'
import { useRoute, useRouter } from 'vue-router'
import { syncTabToUrl } from '../utils/tab-url'
import { api } from '../api'
import { errMsg, isCancel } from '../utils/format'
import PageHead from '../components/PageHead.vue'
import AnsibleOutputCard from './automation/components/AnsibleOutputCard.vue'
import AnsiblePlaybookTable from './automation/components/AnsiblePlaybookTable.vue'
import AnsibleRoleTable from './automation/components/AnsibleRoleTable.vue'
import AnsibleHistoryTable from './automation/components/AnsibleHistoryTable.vue'
import { useAuth } from '../store/auth'
import CronList from './CronList.vue'
import DirFilesDialog from './ansible/DirFilesDialog.vue'

const route = useRoute()
const router = useRouter()
const { isAdmin } = useAuth()
// ?tab= 深链（/crons 重定向、任务中心出口、Role 库直达等用）。
// 两条路径都要覆盖，少一条就有一半场景「点了没反应」：
//   - 冷加载/换路由进来 → 组件重挂，取初始值（ref 初始化里判）
//   - 已在自动化页内只改 query → 组件不重挂，靠 watch
// 白名单校验：非法 tab 值一律回落默认，避免 URL 被塞垃圾时页面白屏。
const TAB_NAMES = ['adhoc', 'playbooks', 'roles', 'history', 'cron']
function queryTabName() {
  const t = route.query.tab
  if (typeof t !== 'string' || !TAB_NAMES.includes(t)) return ''
  if (t === 'cron' && !isAdmin.value) return '' // 计划任务 tab 仅管理员可见
  return t
}
const activeTab = ref(queryTabName() || 'adhoc')
function applyQueryTab() {
  const t = queryTabName()
  if (t) activeTab.value = t
}
watch(() => route.query.tab, applyQueryTab)
// 反向：点 tab 时把 URL 也改过来，链接才可分享（快速执行是默认 tab，不写 query）
function onTabChange(name) {
  syncTabToUrl(router, route, name, 'adhoc')
}
const engine = ref({})
const statusLoading = ref(true)
const vms = ref([])
const targets = ref([])
// 目标方式：vms=按选中的虚拟机；group=按主机组（后端在触发时动态解析成员，改组即生效）
const targetMode = ref('vms')
const groupId = ref(null)
const module = ref('ping')
const args = ref('')
const submitting = ref(false)
const deploying = ref(false)
const extraVars = reactive({})
const runTask = ref(null)
// 用户手动关闭输出卡后置 true：运行中的轮询不再把卡片弹回来
const runDismissed = ref(false)
// 执行输出卡子组件实例：经它转发 scrollToBottom（滚动容器在子组件内）
const outputCardRef = ref(null)
let pollTimer = null

// playbook 库
const playbooks = ref([])
const pbLoading = ref(false)
const editorOpen = ref(false)
const editorId = ref('')
const newId = ref('')
const filesDialog = ref(null)
const roleFilesDialog = ref(null)
// 共享 role 库（data/ansible/roles/）
const roles = ref([])
const roleLoading = ref(false)
const roleDialogOpen = ref(false)
const roleCreating = ref(false)
const roleForm = reactive({ name: '', desc: '' })
const editorContent = ref('')
const checking = ref(false)
const saving = ref(false)
const runDialogOpen = ref(false)
const runPb = ref(null)
// 执行参数定义（Survey）：优先用后端解析的带类型定义；无则回落旧 vars= 清单（全按 str）
const runSurvey = computed(() => {
  const pb = runPb.value
  if (!pb) return []
  if (Array.isArray(pb.survey) && pb.survey.length) return pb.survey
  return (pb.vars || []).map((name) => ({ name, type: 'str' }))
})
const surveyLabel = (v) => v.title || v.name
// playbook 执行选项：预演 / 差异 / 标签过滤
const runCheck = ref(false)
const runDiff = ref(false)
const runTags = ref('')
const runSkipTags = ref('')

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
  const payload = { module: module.value, args: args.value.trim() }
  if (targetMode.value === 'group') {
    if (!groupId.value) return ElMessage.warning('请选择主机组')
    payload.group_id = groupId.value
  } else {
    if (!targets.value.length) return ElMessage.warning('请选择目标虚拟机')
    payload.targets = targets.value
  }
  if (module.value !== 'ping' && !args.value.trim()) return ElMessage.warning('请填写执行参数')
  submitting.value = true
  try {
    const res = await api.ansibleRun(payload)
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
  // 预填默认值（Survey DefaultValue）：int 转数字，其余按字符串
  for (const v of (pb.survey || [])) {
    if (v.default_value === undefined || v.default_value === null || v.default_value === '') continue
    extraVars[v.name] = v.type === 'int' ? Number(v.default_value) : v.default_value
  }
  runCheck.value = false
  runDiff.value = false
  runTags.value = ''
  runSkipTags.value = ''
  runDialogOpen.value = true
}

async function runPlaybook() {
  // 必填校验（前端先挡一道；服务端 Run 还会按 survey 定义强校验）
  for (const v of runSurvey.value) {
    const val = extraVars[v.name]
    if (v.required && (val === '' || val === null || val === undefined)) {
      return ElMessage.warning(`请填写「${surveyLabel(v)}」`)
    }
  }
  // 只提交 survey 声明过的参数（值经 -e @file 注入；secret 由服务端加密后走环境变量）
  const ev = {}
  for (const v of runSurvey.value) {
    const val = extraVars[v.name]
    if (val === '' || val === null || val === undefined) continue
    ev[v.name] = val
  }
  const payload = { playbook: runPb.value.id, extra_vars: ev }
  if (runCheck.value) payload.check = true
  if (runDiff.value) payload.diff = true
  if (runTags.value.trim()) payload.tags = runTags.value.trim()
  if (runSkipTags.value.trim()) payload.skip_tags = runSkipTags.value.trim()
  if (targetMode.value === 'group') {
    if (!groupId.value) return ElMessage.warning('请选择主机组')
    payload.group_id = groupId.value
  } else {
    if (!targets.value.length) return ElMessage.warning('请选择目标虚拟机')
    payload.targets = targets.value
  }
  submitting.value = true
  try {
    const res = await api.ansibleRun(payload)
    runDialogOpen.value = false
    ElMessage.success('任务已提交')
    startPolling(res.data.task_id)
  } catch (e) {
    ElMessage.error(errMsg(e, '提交失败'))
  } finally {
    submitting.value = false
  }
}

// ── 共享 role 库 ──

async function loadRoles() {
  roleLoading.value = true
  try {
    const res = await api.ansibleRoles()
    roles.value = (res.data && res.data.items) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '读取 role 库失败'))
  } finally {
    roleLoading.value = false
  }
}

function createRole() {
  roleForm.name = ''
  roleForm.desc = ''
  roleDialogOpen.value = true
}

async function submitRole() {
  const name = roleForm.name.trim()
  if (!/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/.test(name)) {
    return ElMessage.warning('名称非法：字母数字开头，可含 _ -，≤64 字符')
  }
  roleCreating.value = true
  try {
    await api.ansibleRoleCreate({ name, desc: roleForm.desc.trim() })
    roleDialogOpen.value = false
    ElMessage.success('已创建，可在「文件」里编辑任务')
    await loadRoles()
    const created = roles.value.find((x) => x.name === name)
    if (created) openRoleFiles(created)
  } catch (e) {
    ElMessage.error(errMsg(e, '创建失败'))
  } finally {
    roleCreating.value = false
  }
}

async function removeRole(name) {
  try {
    await api.ansibleRoleDelete(name)
    ElMessage.success('已删除')
    loadRoles()
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

function openRoleFiles(row) {
  roleFilesDialog.value?.open(row.name)
}

// 编辑器
function openFiles(row) {
  filesDialog.value?.open(row.id)
}

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
  // 输出卡在 tab 外共用，不必把用户从当前 tab 拽走（回归：此前强制切到快速执行）
  runDismissed.value = false
  const tick = async () => {
    try {
      const res = await api.getTask(taskId)
      // 用户手动关闭后不再把卡片塞回来（运行中关闭此前无效）
      if (!runDismissed.value) runTask.value = res.data
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
/** 关闭执行输出卡：运行中也不允许下一轮轮询重新弹开 */
function closeRunCard() {
  runDismissed.value = true
  runTask.value = null
}
function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}
function viewHistoryTask(row) {
  stopPolling()
  runDismissed.value = false
  runTask.value = row
  scrollToBottom()
}
// 跳任务中心看该任务完整详情（自动化执行历史此前是流程终点，无出口）
function goTaskCenter(row) {
  router.push({ path: '/tasks', query: { id: row.id } })
}
// 滚到底交给执行输出卡子组件（它持有滚动容器 ref），页面只转发调用
function scrollToBottom() {
  outputCardRef.value?.scrollToBottom()
}

onMounted(async () => {
  await Promise.all([loadStatus(), loadVMs(), loadPlaybooks(), loadRoles(), loadHistory(), loadHostGroups()])
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
/* 执行历史表行点击钻取样式已随渲染下沉到 automation/components/AnsibleHistoryTable.vue */

/* 执行输出卡样式已随渲染下沉到 automation/components/AnsibleOutputCard.vue */
.auto-editor { display: flex; flex-direction: column; gap: 12px; }
.auto-editor-bar { display: flex; align-items: center; gap: 10px; }
.auto-yaml :deep(.el-textarea__inner) {
  font-family: var(--font-mono, monospace); font-size: 0.8rem; line-height: 1.7;
}
/* playbook 执行选项：预演/差异/标签横排 */
.run-opts {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 12px;
}
/* Survey 执行参数：控件 + 说明 + 密钥标记横排 */
.auto-survey-field {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  width: 100%;
}
.auto-survey-title { font-weight: 600; font-size: 0.82rem; }
.auto-secret-tag { flex: none; }
</style>
