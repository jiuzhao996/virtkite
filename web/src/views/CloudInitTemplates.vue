<template>
  <div>
    <div class="page-head">
      <div>
        <h3 class="page-title">cloud-init 模板</h3>
        <p class="page-desc">可复用的初始化配置（主机名 / 用户 / 密码 / SSH 公钥 / 网络），创建向导「云镜像 + cloud-init」方式下一键套用</p>
      </div>
      <el-button type="primary" :icon="Plus" @click="openCreate">新建模板</el-button>
    </div>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="本页管理 cloud-init 配置模板；使用入口在「创建虚拟机 → 云镜像方式 → cloud-init 面板」的「套用模板」下拉。"
      style="margin-bottom: var(--space-lg)"
    />

    <el-card shadow="never">
      <div class="toolbar">
        <span class="count">共 {{ items.length }} 个模板</span>
        <div class="toolbar-right">
          <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <el-button text type="primary" @click="$router.push('/vms/new')">去创建虚拟机 →</el-button>
        </div>
      </div>

      <el-table :data="items" v-loading="loading" stripe>
        <el-table-column prop="name" label="模板名" min-width="140" show-overflow-tooltip />
        <el-table-column prop="description" label="描述" min-width="180" show-overflow-tooltip>
          <template #default="{ row }">{{ row.description || '—' }}</template>
        </el-table-column>
        <el-table-column label="配置摘要" min-width="260">
          <template #default="{ row }">
            <span class="mono">{{ specSummary(row) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="网络模式" width="100">
          <template #default="{ row }">
            <el-tag :type="row.spec && row.spec.net_mode === 'static' ? 'warning' : 'info'" effect="plain" size="small">
              {{ row.spec && row.spec.net_mode === 'static' ? '静态 IP' : 'DHCP' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="更新时间" width="170">
          <template #default="{ row }">
            <span class="mono">{{ fmtDateTime(row.updated_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="190" fixed="right">
          <template #default="{ row }">
            <el-button text type="primary" size="small" @click="copyTemplate(row)">复制</el-button>
            <el-button text type="primary" size="small" @click="openEdit(row)">编辑</el-button>
            <el-button text type="danger" size="small" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty description="还没有模板，点击右上角「新建模板」创建第一个">
            <el-button type="primary" plain @click="openCreate">新建模板</el-button>
          </el-empty>
        </template>
      </el-table>
    </el-card>

    <!-- 新建 / 编辑（复用一个弹窗：editingId 区分模式；字段与向导第 1 步 cloud-init 面板一致） -->
    <el-dialog v-model="dialog" :title="editingId ? '编辑模板' : '新建模板'" width="520px">
      <el-form :model="form" label-width="90px">
        <el-form-item label="模板名" required>
          <el-input v-model="form.name" placeholder="如「教学实验机默认配置」" maxlength="100" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="form.description" placeholder="可选，便于他人理解用途" maxlength="500" />
        </el-form-item>
        <el-divider content-position="left">初始化配置</el-divider>
        <el-form-item label="主机名">
          <el-input v-model="form.hostname" placeholder="可选，套用时默认用虚拟机名" style="width: 320px" />
        </el-form-item>
        <el-form-item label="用户名">
          <el-input v-model="form.user" placeholder="如 ubuntu / root，可选" style="width: 320px" />
        </el-form-item>
        <el-form-item label="密码">
          <el-input v-model="form.password" type="password" show-password placeholder="可选" style="width: 320px" />
        </el-form-item>
        <el-form-item label="SSH 公钥">
          <el-input v-model="form.sshKey" type="textarea" :rows="3" placeholder="粘贴 ssh-rsa / ssh-ed25519 公钥，可选" style="width: 420px" />
        </el-form-item>
        <el-form-item label="网络模式">
          <el-radio-group v-model="form.netMode">
            <el-radio value="dhcp">DHCP（自动获取）</el-radio>
            <el-radio value="static">静态 IP</el-radio>
          </el-radio-group>
        </el-form-item>
        <template v-if="form.netMode === 'static'">
          <el-form-item label="IP 地址" required>
            <el-input v-model="form.ip" placeholder="如 192.168.122.10" style="width: 320px" />
          </el-form-item>
          <el-form-item label="网关" required>
            <el-input v-model="form.gateway" placeholder="如 192.168.122.1" style="width: 320px" />
          </el-form-item>
          <el-form-item label="DNS">
            <el-input v-model="form.dns" placeholder="逗号分隔，如 114.114.114.114" style="width: 320px" />
          </el-form-item>
        </template>
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">{{ editingId ? '保存' : '创建' }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { api } from '../api'
import { errMsg, fmtDateTime } from '../utils/format'

// 合法 IPv4（0-255 四段）；静态 IP / 网关保存前拦截，格式错误直接提示不提交
const IPV4_RE = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/

const items = ref([])
const loading = ref(false)
const dialog = ref(false)
const saving = ref(false)
const editingId = ref(null)
// 表单字段与向导 cloudInit reactive 同构（dns 在表单里是逗号串，提交时拆数组）
const blankForm = () => ({ name: '', description: '', hostname: '', user: '', password: '', sshKey: '', netMode: 'dhcp', ip: '', gateway: '', dns: '' })
const form = ref(blankForm())

// spec → 表单（编辑回显；dns 数组拼回逗号串）
function specToForm(spec) {
  const s = spec || {}
  return {
    name: '',
    description: '',
    hostname: s.hostname || '',
    user: s.user || '',
    password: s.password || '',
    sshKey: s.ssh_key || '',
    netMode: s.net_mode === 'static' ? 'static' : 'dhcp',
    ip: s.ip || '',
    gateway: s.gateway || '',
    dns: Array.isArray(s.dns) ? s.dns.join(', ') : ''
  }
}

// 表单 → spec 对象（只带非空字段，与向导 buildCloudInit 的裁剪口径一致）
function formToSpec() {
  const spec = { net_mode: form.value.netMode }
  const v = (k) => String(form.value[k] || '').trim()
  if (v('hostname')) spec.hostname = v('hostname')
  if (v('user')) spec.user = v('user')
  if (v('password')) spec.password = v('password')
  if (v('sshKey')) spec.ssh_key = v('sshKey')
  if (form.value.netMode === 'static') {
    spec.ip = v('ip')
    spec.gateway = v('gateway')
    const dnsList = form.value.dns.split(/[,，\s]+/).filter(Boolean)
    if (dnsList.length) spec.dns = dnsList
  }
  return spec
}

// 配置摘要（密码不回显内容，只标注「已设密码」）
function specSummary(row) {
  const s = row.spec || {}
  const parts = []
  const who = [s.user, s.hostname].filter(Boolean).join('@')
  if (who) parts.push(who)
  if (s.net_mode === 'static') parts.push('静态 ' + (s.ip || '—') + ' · 网关 ' + (s.gateway || '—'))
  if (s.ssh_key) parts.push('SSH 密钥')
  if (s.password) parts.push('已设密码')
  return parts.join(' · ') || '空配置（仅 DHCP）'
}

async function load() {
  loading.value = true
  try {
    const res = await api.listCloudInitTemplates()
    items.value = (res.data && res.data.items) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '获取模板列表失败'))
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editingId.value = null
  form.value = blankForm()
  dialog.value = true
}

function openEdit(row) {
  editingId.value = row.id
  form.value = { ...specToForm(row.spec), name: row.name, description: row.description || '' }
  dialog.value = true
}

// 复制模板：以该模板 spec 预填新建表单，名称建议加 -copy 后缀，可直接改名后保存
function copyTemplate(row) {
  editingId.value = null
  form.value = { ...specToForm(row.spec), name: row.name + '-copy', description: row.description || '' }
  dialog.value = true
}

async function save() {
  if (!form.value.name.trim()) {
    ElMessage.warning('请填写模板名')
    return
  }
  if (form.value.netMode === 'static') {
    if (!form.value.ip.trim() || !form.value.gateway.trim()) {
      ElMessage.warning('静态网络模式请填写 IP 地址与网关')
      return
    }
    if (!IPV4_RE.test(form.value.ip.trim())) {
      ElMessage.warning('IP 地址格式不正确，请填写合法 IPv4 地址（如 192.168.122.10）')
      return
    }
    if (!IPV4_RE.test(form.value.gateway.trim())) {
      ElMessage.warning('网关格式不正确，请填写合法 IPv4 地址（如 192.168.122.1）')
      return
    }
  }
  saving.value = true
  const payload = { name: form.value.name.trim(), spec: formToSpec(), description: form.value.description.trim() }
  try {
    if (editingId.value) {
      await api.updateCloudInitTemplate(editingId.value, payload)
      ElMessage.success('已保存')
    } else {
      await api.createCloudInitTemplate(payload)
      ElMessage.success('模板已创建')
    }
    dialog.value = false
    load()
  } catch (e) {
    ElMessage.error(errMsg(e, '保存失败'))
  } finally {
    saving.value = false
  }
}

async function remove(row) {
  try {
    await ElMessageBox.confirm(`确定删除模板「${row.name}」？不影响已创建的虚拟机。`, '删除模板', { type: 'warning' })
  } catch {
    return
  }
  try {
    await api.deleteCloudInitTemplate(row.id)
    ElMessage.success('已删除')
    load()
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

onMounted(load)
</script>

<style scoped>
/* .toolbar / .count 已收进 global.css；右侧分组仅本地使用 */
.toolbar-right {
  display: flex;
  align-items: center;
  gap: var(--space-lg);
}
</style>
