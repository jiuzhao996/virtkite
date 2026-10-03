<template>
    <div class="pane-toolbar">
      <el-button type="primary" @click="openNetworkDialog">创建网络</el-button>
      <span class="count ct-count">共 {{ networks.length }} 个网络</span>
    </div>
    <!-- 卡片化（与「虚拟网络」tab 卡片同形态，同页统一视觉语言）：
         内置网络禁删（bridge/host/none 是 docker 底座）；网段/网关由后端批量 inspect 补齐 -->
    <div v-loading="loading" class="net-cards">
      <el-empty v-if="!networks.length" description="暂无网络" :image-size="80" />
      <el-card v-for="row in networks" :key="row.Name" shadow="hover" class="dn-card">
        <div class="dn-head">
          <span class="dn-name mono">{{ row.Name }}</span>
          <el-tag v-if="isBuiltinNetwork(row.Name)" effect="plain" size="small">内置</el-tag>
        </div>
        <div class="dn-rows">
          <div class="dn-row"><span class="dn-label">驱动</span><span class="mono">{{ row.Driver || '—' }}</span></div>
          <div class="dn-row"><span class="dn-label">网段</span><span class="mono">{{ row.Subnet || '—' }}</span></div>
          <div class="dn-row"><span class="dn-label">网关</span><span class="mono">{{ row.Gateway || '—' }}</span></div>
          <div class="dn-row"><span class="dn-label">创建时间</span><span class="mono">{{ dockerTime(row.CreatedAt) }}</span></div>
        </div>
        <div class="dn-actions">
          <!-- bridge/host/none 等内置网络是 docker 底座，前后端双重禁删，按钮置灰 -->
          <el-button size="small" text type="danger" :disabled="isBuiltinNetwork(row.Name)" @click="removeNetwork(row)">删除</el-button>
        </div>
      </el-card>
    </div>

    <!-- 创建网络对话框 -->
    <el-dialog v-model="networkDialog" title="创建网络" width="520px" :close-on-click-modal="false">
      <el-form ref="networkFormRef" :model="networkForm" :rules="networkRules" label-width="80px">
        <el-form-item label="名称" prop="name">
          <el-input v-model="networkForm.name" placeholder="如 mynet" />
        </el-form-item>
        <el-form-item label="驱动" prop="driver">
          <el-select v-model="networkForm.driver" style="width: 100%">
            <el-option v-for="d in networkDrivers" :key="d" :label="d" :value="d" />
          </el-select>
        </el-form-item>
        <el-form-item label="子网" prop="subnet">
          <el-input v-model="networkForm.subnet" placeholder="可选，CIDR 格式如 172.30.0.0/16" />
        </el-form-item>
        <el-form-item label="网关" prop="gateway">
          <el-input v-model="networkForm.gateway" placeholder="可选，如 172.30.0.1" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="networkDialog = false">取消</el-button>
        <el-button type="primary" :loading="networkSubmitting" @click="submitNetwork">创建</el-button>
      </template>
    </el-dialog>
</template>

<script setup>
// 网络页（原网络 tab，1Panel 式子路由化）：数据 / 取数 / 创建对话框 / 删除自持；
// 取数失败经 inject('dockerPage') 上报布局壳（503 置门控 alert，其余 toast），操作成功后本地 refresh 重拉。
import { ref, reactive, nextTick, inject, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '../../../api'
import { errMsg, isCancel } from '../../../utils/format'
import { dockerTime } from '../../../utils/docker-format'

// 布局壳通信：失败上报 / 成功清 503 门控
const { reportLoadError, clearLoadError } = inject('dockerPage')

const networks = ref([])
const loading = ref(false)

// 首次挂载 / 壳刷新按钮 / 操作成功后 共用的重拉入口
async function refresh() {
  loading.value = true
  try {
    const res = await api.dockerNetworks()
    networks.value = (res.data || {}).items || []
    clearLoadError()
  } catch (e) {
    reportLoadError(e, '获取网络列表失败')
  } finally {
    loading.value = false
  }
}

onMounted(refresh)
defineExpose({ refresh })

// ═══════════════ 网络 ═══════════════

// docker 内置网络禁删（bridge/host/none 及 overlay 的隐藏网关桥），与后端黑名单一致
const BUILTIN_NETWORKS = ['bridge', 'host', 'none', 'docker_gwbridge']

function isBuiltinNetwork(name) {
  return BUILTIN_NETWORKS.includes(name)
}

async function removeNetwork(row) {
  if (isBuiltinNetwork(row.Name)) return
  try {
    await ElMessageBox.confirm(`确定删除网络 ${row.Name}？`, '删除网络', {
      type: 'warning',
      confirmButtonText: '删除',
      confirmButtonClass: 'el-button--danger'
    })
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  try {
    await api.dockerDeleteNetwork(row.Name)
    ElMessage.success(`已删除网络 ${row.Name}`)
    await refresh()
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

const networkDialog = ref(false)
const networkSubmitting = ref(false)
const networkFormRef = ref(null)
const networkForm = reactive({ name: '', driver: 'bridge', subnet: '', gateway: '' })
const networkDrivers = ['bridge', 'overlay', 'macvlan', 'ipvlan', 'host', 'none']
const networkRules = {
  name: [
    { required: true, message: '请输入网络名称', trigger: 'blur' },
    { pattern: /^[A-Za-z0-9][A-Za-z0-9_.-]*$/, message: '仅允许字母数字与 -_. 且不以符号开头', trigger: 'blur' }
  ],
  subnet: [
    { pattern: /^(\d{1,3}\.){3}\d{1,3}\/(3[0-2]|[12]?\d)$/, message: '应为 CIDR 格式（掩码 0-32），如 172.30.0.0/16', trigger: 'blur' }
  ],
  gateway: [
    { pattern: /^(\d{1,3}\.){3}\d{1,3}$/, message: '应为合法 IPv4 地址', trigger: 'blur' }
  ]
}

function openNetworkDialog() {
  networkForm.name = ''
  networkForm.driver = 'bridge'
  networkForm.subnet = ''
  networkForm.gateway = ''
  networkDialog.value = true
  nextTick(() => networkFormRef.value && networkFormRef.value.clearValidate())
}

async function submitNetwork() {
  try {
    await networkFormRef.value.validate()
  } catch (e) {
    return
  }
  networkSubmitting.value = true
  try {
    const payload = { name: networkForm.name.trim(), driver: networkForm.driver }
    if (networkForm.subnet.trim()) payload.subnet = networkForm.subnet.trim()
    if (networkForm.gateway.trim()) payload.gateway = networkForm.gateway.trim()
    const res = await api.dockerCreateNetwork(payload)
    ElMessage.success((res.data && res.data.message) || '网络已创建')
    networkDialog.value = false
    await refresh()
  } catch (e) {
    ElMessage.error(errMsg(e, '创建网络失败'))
  } finally {
    networkSubmitting.value = false
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
.ct-count {
  margin-left: auto;
}
/* ── Docker 网络卡片（与虚拟网络 nc-* 同形态）── */
.net-cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: var(--space-lg);
}
.dn-card:hover {
  transform: translateY(-2px);
}
.dn-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
}
.dn-name {
  font-weight: 600;
  color: var(--color-foreground);
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dn-rows {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 12px;
}
.dn-row {
  display: grid;
  grid-template-columns: 72px 1fr;
  gap: 8px;
  font-size: 0.85rem;
}
.dn-label {
  color: var(--color-muted-foreground);
}
.dn-actions {
  padding-top: 10px;
  border-top: 1px solid var(--color-border);
  display: flex;
  justify-content: flex-end;
}
</style>
