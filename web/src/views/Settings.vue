<template>
  <div v-loading="loading">
    <PageHead title="系统设置" subtitle="宿主机登记、平台运行参数、安全与通知、模板与扩展组件。运行参数保存进数据库、立即生效无需重启；生效配置的只读快照见仪表盘「平台信息」卡；界面轮询偏好在顶栏「个人中心」；计划任务已并入「运维自动化」页">
      <el-tag v-if="dirtyForms" type="warning" effect="plain" class="dirty-tag">有未保存的改动</el-tag>
      <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
    </PageHead>

    <!-- 左侧竖向 tab：本页 9 个分节归属四类（平台全局 / 宿主机 / 模板 / 集成），纵向堆叠约 3.6 屏。
         此前用「浮动吸顶锚点条」导航，滚动时吸顶条会从卡片内容上「切」过去，看着像错位（用户反馈）；
         改为左侧竖向 tab：一次只显示一节，无滚动、无遮挡。有未保存改动的 tab 带黄点提示。 -->
    <el-tabs v-model="activeTab" tab-position="left" class="settings-tabs">
      <!-- 宿主机（IA 归并批次 2026-10 并入）：单宿主机平台一页一卡太薄，admin 属性的
           登记/SSH 测试收进设置页；侧栏不再单列 -->
      <el-tab-pane name="hosts" label="宿主机">
        <div id="sec-hosts">
          <h3 class="sub-section-title">宿主机</h3>
          <HostList embedded />
        </div>
      </el-tab-pane>

      <!-- SSH 主机密钥（TOFU）：首次 SSH 连接记录指纹，指纹变化拒绝连接（防中间人）；虚拟机重建后删旧记录重连 -->
      <el-tab-pane name="sshkeys" label="SSH 主机密钥">
        <el-card shadow="never" id="sec-sshkeys">
          <template #header>
            <div class="card-head">
              <span class="card-title">
                SSH 主机密钥（TOFU）
                <el-tooltip
                  content="首次 SSH 连接时记录主机指纹；指纹变化会拒绝连接（防中间人）。虚拟机重建后如遇连接被拒，可删除旧记录后重连"
                  placement="top"
                >
                  <el-icon class="title-help"><QuestionFilled /></el-icon>
                </el-tooltip>
              </span>
              <el-button :icon="Refresh" :loading="keysLoading" @click="loadKeys">刷新</el-button>
            </div>
          </template>
          <el-table :data="hostKeys" v-loading="keysLoading" size="small">
            <template #empty><EmptyState description="暂无已记录的主机指纹" /></template>
            <el-table-column prop="host" label="主机" min-width="140" show-overflow-tooltip />
            <el-table-column prop="port" label="端口" width="76" />
            <el-table-column prop="key_type" label="算法" width="110" />
            <el-table-column label="指纹" min-width="300">
              <template #default="{ row }">
                <span class="mono">{{ row.fingerprint }}</span>
              </template>
            </el-table-column>
            <el-table-column label="记录时间" width="180">
              <template #default="{ row }">{{ fmtDateTime(row.created_at) }}</template>
            </el-table-column>
            <el-table-column label="操作" width="90" fixed="right">
              <template #default="{ row }">
                <el-button text size="small" type="danger" @click="removeKey(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>

      <!-- 可写配置：DB 持久化、写入即生效 -->
      <el-tab-pane name="writable">
        <template #label>
          <span>运行参数<span v-if="dirtyGroups.writable" class="tab-dot" /></span>
        </template>
        <el-card shadow="never" id="sec-params">
          <template #header>
            <div class="card-head">
              <span class="card-title">运行参数</span>
              <el-button type="primary" :loading="saving" @click="saveWritable">保存并生效</el-button>
            </div>
          </template>
          <el-form label-width="170px" style="max-width: 560px">
            <el-form-item label="默认存储池">
              <!-- 用下拉而不是自由文本：这里填错一个字母，创建/删除 VM 就会打到不存在的池上，
                   而错误只在真正建机时才暴露。候选来自 libvirt 实际池列表，选不出错的池。 -->
              <el-select
                v-model="writable.default_storage_pool"
                filterable
                placeholder="未指定池时创建/删除 VM 使用的池名"
                style="width: 260px"
              >
                <el-option v-for="n in poolOptions" :key="n" :label="n" :value="n" />
              </el-select>
              <span class="unit">建机/删机未显式指定池时用它</span>
            </el-form-item>
            <el-form-item label="VNC token 有效期">
              <el-input-number v-model="writable.vnc_token_ttl_min" :min="1" :max="60" controls-position="right" />
              <span class="unit">分钟（每次生成 token 实时读取）</span>
            </el-form-item>
            <el-form-item label="VNC 会话过期判定">
              <el-input-number v-model="writable.vnc_stale_min" :min="5" :max="1440" controls-position="right" />
              <span class="unit">分钟（超时无活动将被清扫收敛）</span>
            </el-form-item>
            <el-form-item label="跳板命令黑名单">
              <el-input v-model="writable.jumpd_cmd_blacklist" placeholder="逗号分隔，留空 = 内置默认（rm -rf, mkfs, dd if= …）" />
              <div class="input-help">学生经 SSH 跳板敲入的整行命中任一子串即被拦截并写入审计（大小写/多空格自动归一）</div>
            </el-form-item>
          </el-form>
          <p class="tip">以上配置持久化在数据库中，保存后立即生效，无需重启后端。</p>
        </el-card>
      </el-tab-pane>

      <!-- 安全设置（批次 D）：安全入口 + 密码策略，独立保存 -->
      <el-tab-pane name="sec">
        <template #label>
          <span>安全设置<span v-if="dirtyGroups.sec" class="tab-dot" /></span>
        </template>
        <el-card shadow="never" id="sec-security">
          <template #header>
            <div class="card-head">
              <span class="card-title">安全设置</span>
              <el-button type="primary" :loading="savingSec" @click="saveSec">保存</el-button>
            </div>
          </template>
          <el-form label-width="170px" style="max-width: 560px">
            <el-form-item label="安全入口">
              <el-input v-model="sec.entrance" placeholder="留空 = 关闭（默认登录地址）" clearable style="width: 320px" />
              <div class="input-help">设置后登录 API 必须携带该入口参数（?entrance=值），否则一律 404——防扫描爆破。请妥善保存，遗忘可经 SSH 按文档重置。</div>
            </el-form-item>
            <el-form-item label="密码最小长度">
              <el-input-number v-model="sec.pwdMin" :min="0" :max="64" controls-position="right" style="width: 160px" />
              <div class="input-help">0 = 关闭策略；≥8 时密码需同时包含字母与数字</div>
            </el-form-item>
          </el-form>
          <p class="tip">安全入口遗忘时的应急处理见 docs/07 部署文档。</p>
        </el-card>
      </el-tab-pane>

      <!-- 告警通知：alert_notify_url 单键，留空 = 关闭推送；独立保存 -->
      <el-tab-pane name="notify">
        <template #label>
          <span>告警通知<span v-if="dirtyGroups.notify" class="tab-dot" /></span>
        </template>
        <el-card shadow="never" id="sec-notify">
          <template #header>
            <div class="card-head">
              <span class="card-title">告警通知</span>
              <el-button type="primary" :loading="savingNotify" @click="saveNotify">保存</el-button>
            </div>
          </template>
          <el-form label-width="170px" style="max-width: 560px">
            <el-form-item label="通知地址">
              <el-input v-model="notify.url" placeholder="飞书/钉钉自定义机器人 incoming 地址，留空关闭" clearable />
              <div class="input-help">告警触发时向该地址推送文本消息（飞书/钉钉自定义机器人格式）；仅在告警新增或恢复后再触发时通知，不重复轰炸。</div>
            </el-form-item>
          </el-form>
        </el-card>
      </el-tab-pane>

      <!-- 系统公告（v3 批次 P）：登录页与仪表盘公开展示，独立保存，只提交 announcement 键 -->
      <el-tab-pane name="announce">
        <template #label>
          <span>系统公告<span v-if="dirtyGroups.announce" class="tab-dot" /></span>
        </template>
        <el-card shadow="never" id="sec-announce">
          <template #header>
            <div class="card-head">
              <span class="card-title">系统公告</span>
              <el-button type="primary" :loading="savingAnn" @click="saveAnnouncement">保存</el-button>
            </div>
          </template>
          <el-form label-width="170px">
            <el-form-item label="公告内容">
              <el-input
                v-model="ann.content"
                type="textarea"
                :rows="5"
                maxlength="2000"
                show-word-limit
                placeholder="留空 = 不展示公告。保存后立即在登录页与仪表盘顶部展示"
              />
            </el-form-item>
          </el-form>
          <p class="tip">公告对全部用户公开（含未登录的登录页），请勿填写敏感信息；清空内容并保存即撤下公告。仪表盘对同一内容仅提示一次，内容变更后重新提示。</p>
        </el-card>
      </el-tab-pane>

      <!-- cloud-init 模板（IA 精简批次并入）：创建向导「云镜像 + cloud-init」面板一键套用的初始化模板 -->
      <el-tab-pane name="cloudinit" label="cloud-init 模板">
        <div id="sec-cloudinit">
          <CloudInitTemplates embedded />
        </div>
      </el-tab-pane>

      <!-- AI 设置（运维助手）：与「运行参数」独立保存，只提交 ai_* 三个键 -->
      <el-tab-pane name="ai">
        <template #label>
          <span>AI 设置<span v-if="dirtyGroups.ai" class="tab-dot" /></span>
        </template>
        <el-card shadow="never" id="sec-ai">
          <template #header>
            <div class="card-head">
              <span class="card-title">AI 设置（运维助手）</span>
              <el-button type="primary" :loading="savingAI" @click="saveAI">保存</el-button>
            </div>
          </template>
          <el-form label-width="170px" style="max-width: 560px">
            <el-form-item label="API 地址">
              <el-input v-model="ai.base_url" placeholder="如 https://api.deepseek.com/v1" clearable />
            </el-form-item>
            <el-form-item label="API Key">
              <!-- GET 返回明文，表单用密码框掩码展示；new-password 防浏览器把 Key 当登录口令自动填充 -->
              <el-input
                v-model="ai.api_key"
                type="password"
                show-password
                autocomplete="new-password"
                placeholder="粘贴服务商提供的 API Key"
              />
            </el-form-item>
            <el-form-item label="模型名">
              <el-input v-model="ai.model" placeholder="如 deepseek-chat" clearable />
            </el-form-item>
          </el-form>
          <p class="tip">配置后可通过顶栏「AI 助手」使用智能问答；Key 保存在服务端，不会下发到浏览器。</p>
        </el-card>
      </el-tab-pane>

      <!-- 嵌套虚拟化（VM 里再跑 VM）：宿主内核层开关，独立保存 -->
      <el-tab-pane name="nested" label="嵌套虚拟化">
        <el-card shadow="never" v-loading="nestedLoading" id="sec-nested">
          <template #header>
            <div class="card-head">
              <span class="card-title">嵌套虚拟化</span>
              <el-button type="primary" :loading="nestedBusy" @click="toggleNested(!nested.enabled)">
                {{ nested.enabled ? '关闭' : '开启' }}
              </el-button>
            </div>
          </template>
          <div class="nested-row">
            <el-tag :type="nested.enabled ? 'success' : 'info'" effect="light">{{ nested.enabled ? '已开启' : '未开启' }}</el-tag>
            <span class="tip">
              内核模块 {{ nested.module || '—' }} · 当前值 {{ nested.value || '—' }}
              <template v-if="nested.persisted"> · 已持久化（重启后仍生效）</template>
              <template v-if="nested.reason"> · {{ nested.reason }}</template>
            </span>
          </div>
          <p class="tip">开启后可在虚拟机内再运行虚拟机（需 CPU 支持 VT-x/AMD-V）。会写入 modprobe.d 持久化；运行时重载需所有虚拟机已关机，否则重启宿主机后生效。</p>
        </el-card>
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, QuestionFilled } from '@element-plus/icons-vue'
import { api } from '../api'
import { errMsg, fmtDateTime } from '../utils/format'
import CloudInitTemplates from './CloudInitTemplates.vue'
import HostList from './HostList.vue'
import PageHead from '../components/PageHead.vue'
import { onBeforeRouteLeave } from 'vue-router'
import EmptyState from '../components/EmptyState.vue'

// 左侧竖向 tab 的当前项；默认落在「宿主机」（单宿主机平台的「机器在哪」比调参更常看）。
const activeTab = ref('hosts')

// 默认存储池的下拉候选：来自 libvirt 实际池列表，避免手输打错池名。
// 取不到时保持空列表——此时下拉为空，但不会阻断本页其它设置项的修改。
const poolOptions = ref([])
async function loadPools() {
  try {
    const res = await api.listStoragePools()
    const names = ((res.data && res.data.items) || []).map((p) => p.name)
    // 把当前已配置的值并进去：池被删掉后配置可能仍指向它，不并入的话下拉会显示成空白，
    // 用户以为「没配」，一保存反而把有效配置清掉了
    const cur = writable.default_storage_pool
    poolOptions.value = cur && !names.includes(cur) ? [cur, ...names] : names
  } catch (e) {
    /* 池列表拉取失败不影响本页其它设置项 */
  }
}

const loading = ref(false)
const saving = ref(false)

// ── 「有未保存改动」追踪（按表单分组）──
// 设置页是分卡片「改完点保存」的表单，用户很容易改了几项就直接切走——此前是静默丢失，
// 界面上没有任何痕迹。这里在每次成功读取/保存后对每组表单拍一份快照，之后比对差异：
// 全局标记（页头「有未保存的改动」标签 + 离开拦截）取任意一组脏；
// 每组的脏值驱动对应 tab 上的黄点（隐藏 tab 里改了没存更容易被忘掉）。
// 只做提示与拦截，不改变「攒一批再保存」的交互：这几个字段多是文本框，逐字符即时
// 提交反而是错的（1Panel 那种逐控件即时生效只适用于单选/开关）。
const snapshots = reactive({ writable: '', sec: '', ai: '', ann: '', notify: '' })
function groupState(g) {
  const groups = { writable, sec, ai, ann, notify }
  return JSON.stringify(groups[g])
}
function markSaved() {
  for (const g of Object.keys(snapshots)) snapshots[g] = groupState(g)
}
const dirtyGroups = computed(() => {
  const out = {}
  for (const g of Object.keys(snapshots)) out[g] = snapshots[g] !== '' && snapshots[g] !== groupState(g)
  return out
})
const dirtyForms = computed(() => Object.values(dirtyGroups.value).some(Boolean))

// 可写配置表单（默认值兜底，后端 GET /settings 的 writable 节回填）
/* ── 嵌套虚拟化（宿主内核层，独立保存）── */
const nested = ref({ supported: false, module: '', enabled: false, value: '', persisted: false, reason: '' })
const nestedLoading = ref(false)
const nestedBusy = ref(false)

async function loadNested() {
  nestedLoading.value = true
  try {
    nested.value = (await api.nestedKVMStatus()).data || {}
  } catch (e) {
    // 探测失败静默（卡片展示空态）
  } finally {
    nestedLoading.value = false
  }
}

async function toggleNested(enable) {
  nestedBusy.value = true
  try {
    const res = await api.setNestedKVM({ enable })
    nested.value = res.data.status || {}
    ElMessage.success('已' + (enable ? '开启' : '关闭') + '嵌套虚拟化' + (res.data.note ? '：' + res.data.note : ''))
  } catch (e) {
    ElMessage.error(errMsg(e, '设置失败'))
  } finally {
    nestedBusy.value = false
  }
}

const writable = reactive({  default_storage_pool: 'vmops',
  vnc_token_ttl_min: 5,
  vnc_stale_min: 60,
  jumpd_cmd_blacklist: ''
})

// 安全设置（批次 D）：security_entrance / password_min_length，独立保存；
// savedEntrance 记录后端当前生效值，保存时检测 entrance 是否真的发生变更（变了才弹高危确认）
const savingSec = ref(false)
const sec = reactive({ entrance: '', pwdMin: 8 })
let savedEntrance = ''

// AI 设置（运维助手）：ai_base_url / ai_api_key / ai_model 三个键，独立保存
const savingAI = ref(false)
const ai = reactive({ base_url: '', api_key: '', model: '' })

// 系统公告（v3 批次 P）：announcement 单键，独立保存；空串=撤下公告（后端放行空值）
const savingAnn = ref(false)
const ann = reactive({ content: '' })

// SSH 主机密钥（TOFU）：GET /api/ssh-host-keys 的记录表；指纹变化拒绝连接属预期防护，
// 虚拟机重建后指纹必然变化，删除旧记录即可重新信任（本页是唯一的管理入口）
const keysLoading = ref(false)
const hostKeys = ref([])

// 告警通知（alert_notify_url 单键）：留空 = 关闭推送；独立保存，回填自 writable 节
const savingNotify = ref(false)
const notify = reactive({ url: '' })

async function loadKeys() {
  keysLoading.value = true
  try {
    const res = await api.listSSHHostKeys()
    hostKeys.value = (res.data && res.data.items) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '获取 SSH 主机密钥失败'))
  } finally {
    keysLoading.value = false
  }
}

async function removeKey(row) {
  try {
    await ElMessageBox.confirm(
      `确定删除 ${row.host}:${row.port} 的主机指纹记录？删除后下次 SSH 连接将重新记录指纹。`,
      '删除主机密钥',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消', confirmButtonClass: 'el-button--danger' }
    )
  } catch {
    return // 用户取消
  }
  try {
    await api.deleteSSHHostKey(row.id)
    ElMessage.success('已删除')
    loadKeys()
  } catch (e) {
    ElMessage.error(errMsg(e, '删除主机密钥失败'))
  }
}

// 告警通知单独保存：只提交 alert_notify_url 键，与其它配置卡互不覆盖
async function saveNotify() {
  savingNotify.value = true
  try {
    await api.updateSettings({ alert_notify_url: notify.url.trim() })
    ElMessage.success(notify.url.trim() ? '告警通知已保存并生效' : '告警通知已关闭')
    markSaved()
    load()
  } catch (e) {
    ElMessage.error(errMsg(e, '保存告警通知失败'))
  } finally {
    savingNotify.value = false
  }
}

async function load() {
  loading.value = true
  try {
    const res = await api.getSettings()
    const w = (res.data && res.data.writable) || {}
    if (w.default_storage_pool) writable.default_storage_pool = w.default_storage_pool
    if (w.vnc_token_ttl_min) writable.vnc_token_ttl_min = Number(w.vnc_token_ttl_min) || writable.vnc_token_ttl_min
    if (w.vnc_stale_min) writable.vnc_stale_min = Number(w.vnc_stale_min) || writable.vnc_stale_min
    if (w.jumpd_cmd_blacklist !== undefined) writable.jumpd_cmd_blacklist = w.jumpd_cmd_blacklist || ''
    // 快照含 ai_* 当前值（api_key 为明文，表单以密码框掩码展示）；
    // 兼容键位于 writable 节或快照顶层两种返回形态
    const snap = res.data || {}
    ai.base_url = w.ai_base_url ?? snap.ai_base_url ?? ''
    ai.api_key = w.ai_api_key ?? snap.ai_api_key ?? ''
    ai.model = w.ai_model ?? snap.ai_model ?? ''
    if (w.security_entrance !== undefined) sec.entrance = w.security_entrance
    savedEntrance = String(w.security_entrance ?? '')
    if (w.password_min_length !== undefined) sec.pwdMin = Number(w.password_min_length) || 0
    if (w.announcement !== undefined) ann.content = w.announcement
    // 告警通知地址：键未上线（后端未加白名单）时保持空串，不误清用户输入
    if (w.alert_notify_url !== undefined) notify.url = w.alert_notify_url || ''
    markSaved()
  } catch (e) {
    ElMessage.error('获取系统设置失败')
  } finally {
    loading.value = false
  }
}

// 离开页面时拦一下未保存的改动（浏览器内路由跳转；直接关标签页由 beforeunload 兜）
onBeforeRouteLeave(async () => {
  if (!dirtyForms.value) return true
  try {
    await ElMessageBox.confirm('当前有未保存的改动，离开将丢失。确定离开？', '未保存的改动', {
      type: 'warning', confirmButtonText: '放弃改动并离开', cancelButtonText: '留下'
    })
    return true
  } catch (e) {
    return false
  }
})

async function saveWritable() {
  if (!writable.default_storage_pool) {
    ElMessage.warning('默认存储池不能为空')
    return
  }
  saving.value = true
  try {
    await api.updateSettings({
      default_storage_pool: writable.default_storage_pool,
      vnc_token_ttl_min: writable.vnc_token_ttl_min,
      vnc_stale_min: writable.vnc_stale_min,
      jumpd_cmd_blacklist: writable.jumpd_cmd_blacklist
    })
    ElMessage.success('已保存并生效')
    load()
  } catch (e) {
    ElMessage.error(errMsg(e, '保存失败'))
  } finally {
    saving.value = false
  }
}

// AI 设置单独保存：只提交 ai_* 三个键，不携带「运行参数」，互不覆盖
async function saveAI() {
  const baseUrl = ai.base_url.trim()
  const apiKey = ai.api_key.trim()
  const model = ai.model.trim()
  if (!baseUrl && !apiKey && !model) {
    ElMessage.warning('请先填写 AI 配置')
    return
  }
  // 三项为一组生效配置，缺任一项时后端校验也会 400，这里提前给出可读提示
  if (!baseUrl || !apiKey || !model) {
    ElMessage.warning('API 地址、API Key、模型名需完整填写')
    return
  }
  savingAI.value = true
  try {
    await api.updateSettings({ ai_base_url: baseUrl, ai_api_key: apiKey, ai_model: model })
    ElMessage.success('AI 设置已保存并生效')
    load()
  } catch (e) {
    ElMessage.error(errMsg(e, '保存 AI 设置失败'))
  } finally {
    savingAI.value = false
  }
}

// 安全设置单独保存：只提交两键，与运行参数/AI 互不覆盖。
// 安全入口属高危变更：值发生变更（含开启/关闭）时先弹确认说明后果，未变更直接提交
async function saveSec() {
  const next = sec.entrance.trim()
  if (next !== savedEntrance.trim()) {
    const tip = next
      ? `将把安全入口设置为「${next}」：保存后所有非该入口的请求一律返回 404，遗忘需按文档经 SSH 重置。确认修改？`
      : '将关闭安全入口：登录地址恢复为默认入口，任何人可直接访问登录页。确认修改？'
    try {
      await ElMessageBox.confirm(tip, '修改安全入口', {
        type: 'warning',
        confirmButtonText: '确认修改',
        cancelButtonText: '取消',
        confirmButtonClass: 'el-button--danger'
      })
    } catch {
      return // 用户取消
    }
  }
  savingSec.value = true
  try {
    await api.updateSettings({
      security_entrance: next,
      password_min_length: Number(sec.pwdMin) || 0
    })
    savedEntrance = next
    ElMessage.success('安全设置已保存并生效')
    load()
  } catch (e) {
    ElMessage.error(errMsg(e, '保存安全设置失败'))
  } finally {
    savingSec.value = false
  }
}

// 系统公告单独保存：只提交 announcement 键，与其它配置卡互不覆盖；
// 允许保存空串（=撤下公告），后端 Validate 放行空值
async function saveAnnouncement() {
  savingAnn.value = true
  try {
    await api.updateSettings({ announcement: ann.content })
    ElMessage.success(ann.content.trim() ? '公告已发布并生效' : '公告已撤下')
    load()
  } catch (e) {
    ElMessage.error(errMsg(e, '保存系统公告失败'))
  } finally {
    savingAnn.value = false
  }
}

// 关闭/刷新标签页的兜底：路由守卫拦不住这种情况（浏览器只允许弹原生确认框）
function onBeforeUnload(e) {
  if (!dirtyForms.value) return
  e.preventDefault()
  e.returnValue = ''
}
onMounted(async () => {
  window.addEventListener('beforeunload', onBeforeUnload)
  // loadPools 必须**等 load 完成**再调：它要把「当前已配置的池名」并进候选，
  // 并行发起时读到的是初始值（还没被 load 填充），并入的会是个无效值
  await load()
  loadPools()
  loadKeys()
  loadNested()
})
onUnmounted(() => {
  window.removeEventListener('beforeunload', onBeforeUnload)
})
</script>

<style scoped>
.nested-row {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 6px;
}
.sub-section-title {
  margin: 0 0 12px;
  font-size: 0.95rem;
  font-weight: 600;
  color: var(--color-foreground);
}
.card-title {
  font-weight: 600;
}
.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
/* 左侧竖向 tab：左栏标签左对齐、留出间距；内容区随卡片自适应 */
.settings-tabs :deep(.el-tabs__header.is-left) {
  margin-right: 24px;
}
.settings-tabs :deep(.el-tabs__item) {
  text-align: left;
  justify-content: flex-start;
}
/* tab 上的「未保存改动」黄点 */
.tab-dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  margin-left: 6px;
  border-radius: 50%;
  background: var(--el-color-warning);
  vertical-align: middle;
}
.unit {
  margin-left: 8px;
  color: var(--color-muted-foreground);
  font-size: 0.82rem;
}
.tip {
  color: var(--color-muted-foreground);
  font-size: 0.82rem;
  margin: 4px 0 0;
}
/* 卡头问号图标：白话说明入口（hover 展开 tooltip），弱化不抢标题 */
.title-help {
  margin-left: 6px;
  font-size: 0.9rem;
  color: var(--color-muted-foreground);
  cursor: help;
  vertical-align: -1px;
}
.dirty-tag {
  margin-right: 8px;
}
</style>
