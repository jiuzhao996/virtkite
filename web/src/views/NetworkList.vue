<template>
  <div v-loading="loading">
    <div class="page-head">
      <h2 class="page-title">网络管理</h2>
      <span class="page-desc">管理 libvirt 虚拟网络：NAT、桥接、隔离网络</span>
    </div>

    <el-card shadow="never">
      <div class="toolbar">
        <div class="toolbar-left">
          <el-button type="primary" :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <el-button v-if="isAdmin" type="success" :icon="Plus" @click="openCreate">新建 NAT 网络</el-button>
          <el-button v-if="isAdmin" :icon="Document" @click="openXML">从 XML 定义</el-button>
        </div>
        <div class="toolbar-right">
          <el-tag type="success" effect="plain" size="small">运行 {{ activeCount }}</el-tag>
          <el-tag v-if="autostartCount" effect="plain" size="small">自启 {{ autostartCount }}</el-tag>
          <span class="count">共 {{ networks.length }} 个网络</span>
        </div>
      </div>

      <!-- 网络卡片（腾讯云风格）：网络属性多（网桥/转发/网关/DHCP），少量对象时卡片比表格信息层次更好 -->
      <el-empty v-if="!networks.length && !loading" description="暂无虚拟网络，点击「新建 NAT 网络」创建" :image-size="72" />
      <el-row v-else :gutter="16">
        <el-col v-for="row in networks" :key="row.name" :xs="24" :sm="12" :md="8">
        <el-card shadow="hover" class="net-card" :class="{ inactive: !row.active }">
          <div class="nc-head">
            <span class="nc-name mono">{{ row.name }}</span>
            <el-tag :type="row.active ? 'success' : 'info'" effect="light">{{ row.active ? '运行' : '停止' }}</el-tag>
            <div class="nc-autostart">
              <span class="nc-label">自启</span>
              <el-switch
                v-if="isAdmin"
                :model-value="row.autostart"
                :loading="autostartBusy.has(row.name)"
                @change="(v) => toggleAutostart(row, v)"
              />
              <el-tag v-else :type="row.autostart ? 'primary' : 'info'" effect="plain" size="small">
                {{ row.autostart ? '启用' : '禁用' }}
              </el-tag>
            </div>
          </div>
          <div class="nc-rows">
            <div class="nc-row"><span class="nc-label">网桥</span><span class="mono">{{ row.bridge || '—' }}</span></div>
            <div class="nc-row"><span class="nc-label">转发模式</span><span>{{ row.forward || '—' }}</span></div>
            <div class="nc-row"><span class="nc-label">网关</span><span class="mono">{{ row.gateway || '—' }}</span></div>
            <div class="nc-row wide"><span class="nc-label">DHCP 范围</span><span class="mono">{{ row.dhcp_start && row.dhcp_end ? row.dhcp_start + ' - ' + row.dhcp_end : '—' }}</span></div>
          </div>
          <div class="nc-actions">
            <el-button v-if="isAdmin" :icon="VideoPlay" :disabled="row.active" @click="act(row, 'start')">启动</el-button>
            <el-button v-if="isAdmin" :icon="VideoPause" :disabled="!row.active" @click="act(row, 'stop')">停止</el-button>
            <el-button v-if="isAdmin" :icon="Edit" @click="openEdit(row)">编辑 XML</el-button>
            <el-button v-if="isAdmin" type="danger" plain :icon="Delete" @click="remove(row)">删除</el-button>
          </div>
        </el-card>
        </el-col>
      </el-row>
    </el-card>

    <!-- 新建 NAT 网络 -->
    <el-dialog v-model="createDialog" title="新建 NAT 网络" width="460px">
      <el-form :model="createForm" label-width="80px">
        <el-form-item label="名称" required>
          <el-input v-model="createForm.name" placeholder="仅字母、数字、_、-" />
        </el-form-item>
        <el-form-item label="网关">
          <el-input v-model="createForm.gateway" placeholder="如 192.168.100.1" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="createNetwork">创建</el-button>
      </template>
    </el-dialog>

    <!-- 从 XML 定义 -->
    <el-dialog v-model="xmlDialog" title="从 XML 定义网络" width="640px">
      <el-input v-model="xmlForm.xml" type="textarea" :rows="14" class="edit-input" placeholder="<network>...</network>" />
      <template #footer>
        <el-button @click="xmlDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="defineXML">定义</el-button>
      </template>
    </el-dialog>

    <!-- 编辑网络 XML -->
    <el-dialog v-model="editDialog" :title="'编辑网络 XML - ' + (editRow.name || '')" width="680px">
      <el-alert type="info" :closable="false" show-icon class="edit-tip" title="保存后网络将按新 XML 重建，XML 中的网络名称需保持不变" />
      <el-input v-model="editForm.xml" type="textarea" :rows="16" class="edit-input" />
      <template #footer>
        <el-button @click="editDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveEdit">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Plus, Document, VideoPlay, VideoPause, Delete, Edit } from '@element-plus/icons-vue'
import { api } from '../api'
import { useAuth } from '../store/auth'
import { errMsg, isCancel } from '../utils/format'

const { isAdmin } = useAuth()

const networks = ref([])
const loading = ref(false)
const saving = ref(false)
const createDialog = ref(false)
const xmlDialog = ref(false)
const editDialog = ref(false)
const editRow = ref({})

const createForm = ref({ name: '', gateway: '' })
const xmlForm = ref({ xml: '' })
const editForm = ref({ xml: '' })

const activeCount = computed(() => networks.value.filter((n) => n.active).length)
const autostartCount = computed(() => networks.value.filter((n) => n.autostart).length)

async function load() {
  loading.value = true
  try {
    const res = await api.listNetworks()
    networks.value = (res.data && res.data.items) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '获取网络列表失败'))
  } finally {
    loading.value = false
  }
}

function openCreate() {
  createForm.value = { name: '', gateway: '' }
  createDialog.value = true
}

async function createNetwork() {
  if (!createForm.value.name) {
    ElMessage.warning('请填写名称')
    return
  }
  saving.value = true
  try {
    await api.createNetwork({ ...createForm.value })
    ElMessage.success('网络已创建')
    createDialog.value = false
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '创建失败'))
  } finally {
    saving.value = false
  }
}

function openXML() {
  xmlForm.value = { xml: '' }
  xmlDialog.value = true
}

async function defineXML() {
  if (!xmlForm.value.xml.trim()) {
    ElMessage.warning('请填写 XML')
    return
  }
  saving.value = true
  try {
    await api.defineNetworkXML({ xml: xmlForm.value.xml })
    ElMessage.success('网络已定义')
    xmlDialog.value = false
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '定义失败'))
  } finally {
    saving.value = false
  }
}

// 编辑 XML：先取当前 XML 填充，保存时提交新 XML
async function openEdit(row) {
  editRow.value = row
  editForm.value.xml = ''
  editDialog.value = true
  try {
    const res = await api.getNetwork(row.name)
    editForm.value.xml = (res.data && res.data.xml) || ''
  } catch (e) {
    ElMessage.error(errMsg(e, '获取网络 XML 失败'))
    editDialog.value = false
  }
}

async function saveEdit() {
  if (!editForm.value.xml.trim()) {
    ElMessage.warning('请填写 XML')
    return
  }
  saving.value = true
  try {
    await api.updateNetwork(editRow.value.name, editForm.value.xml)
    ElMessage.success('网络已更新')
    editDialog.value = false
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '更新失败'))
  } finally {
    saving.value = false
  }
}

async function act(row, type) {
  // 启动/停止影响连通性：停止会瞬断该网络上所有虚拟机的流量，二次确认防误触
  if (type === 'stop') {
    try {
      await ElMessageBox.confirm(
        `确定停止网络「${row.name}」？该网络上运行中的虚拟机将立即失去网络连接。`,
        '确认停止',
        { type: 'warning' }
      )
    } catch {
      return
    }
  } else {
    try {
      await ElMessageBox.confirm(`确定启动网络「${row.name}」？`, '确认启动', { type: 'info' })
    } catch {
      return
    }
  }
  try {
    if (type === 'start') await api.startNetwork(row.name)
    else await api.stopNetwork(row.name)
    ElMessage.success(type === 'start' ? '网络已启动' : '网络已停止')
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '操作失败'))
  }
}

// 自启动开关（对应 virsh net-autostart on|off）：按行 busy，成功后本地回写不整表刷新
const autostartBusy = ref(new Set())
async function toggleAutostart(row, value) {
  autostartBusy.value.add(row.name)
  autostartBusy.value = new Set(autostartBusy.value)
  try {
    await api.setNetworkAutostart(row.name, !!value)
    row.autostart = !!value
    ElMessage.success(value ? '已启用自启动' : '已禁用自启动')
  } catch (e) {
    ElMessage.error(errMsg(e, '设置自启动失败'))
  } finally {
    autostartBusy.value.delete(row.name)
    autostartBusy.value = new Set(autostartBusy.value)
  }
}

async function remove(row) {
  try {
    await ElMessageBox.confirm('确定删除网络「' + row.name + '」？运行中的网络将一并停止。', '确认删除', { type: 'warning' })
    await api.deleteNetwork(row.name)
    ElMessage.success('网络已删除')
    await load()
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '删除失败'))
  }
}

onMounted(load)
</script>

<style scoped>
/* .page-head / .page-title / .page-desc / .toolbar / .count 已收进 global.css；.mono 的 font-family 亦然 */
.toolbar-left,
.toolbar-right {
  display: flex;
  align-items: center;
  gap: var(--space-lg);
}
.mono {
  font-size: 0.85rem;
}
.muted {
  color: var(--color-muted-foreground);
}
.edit-tip {
  margin-bottom: var(--space-lg);
}
.edit-input :deep(.el-textarea__inner) {
  font-family: var(--font-mono);
  font-size: 0.82rem;
  line-height: 1.5;
}
.net-card {
  margin-bottom: 16px;
}
.net-card.inactive {
  opacity: 0.75;
}
.nc-head {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
}
.nc-name {
  font-size: 1.02rem;
  font-weight: 600;
}
.nc-autostart {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 6px;
}
.nc-label {
  width: 64px;
  flex-shrink: 0;
  white-space: nowrap;
  font-size: 0.82rem;
  color: var(--color-muted-foreground);
}
.nc-rows {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px 20px;
}
.nc-row {
  display: flex;
  gap: 10px;
  font-size: 0.88rem;
}
.nc-row .nc-label {
  width: 60px;
  flex-shrink: 0;
}
.nc-actions {
  display: flex;
  flex-wrap: wrap; /* 三列窄卡下四个操作按钮允许换行 */
  gap: 8px;
  margin-top: 14px;
  padding-top: 12px;
  border-top: 1px solid var(--color-border);
}
/* DHCP 范围值较长，单独占满一行避免挤压换行 */
.nc-row.wide {
  grid-column: 1 / -1;
}
</style>