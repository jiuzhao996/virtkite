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
          <el-button type="success" :icon="Plus" @click="openCreate">新建 NAT 网络</el-button>
          <el-button :icon="Document" @click="openXML">从 XML 定义</el-button>
        </div>
        <div class="toolbar-right">
          <el-tag type="success" effect="plain" size="small">运行 {{ activeCount }}</el-tag>
          <el-tag v-if="autostartCount" effect="plain" size="small">自启 {{ autostartCount }}</el-tag>
          <span class="count">共 {{ networks.length }} 个网络</span>
        </div>
      </div>

      <el-table :data="networks" stripe border style="width: 100%">
        <el-table-column prop="name" label="名称" min-width="130">
          <template #default="{ row }">
            <span class="mono">{{ row.name }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="row.active ? 'success' : 'info'" effect="light">
              {{ row.active ? '运行' : '停止' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="自动启动" width="100">
          <template #default="{ row }">
            <el-tag :type="row.autostart ? 'primary' : 'info'" effect="plain" size="small">
              {{ row.autostart ? '启用' : '禁用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="bridge" label="网桥" width="130">
          <template #default="{ row }">
            <span class="mono">{{ row.bridge || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="转发模式" width="110">
          <template #default="{ row }">
            {{ row.forward || '—' }}
          </template>
        </el-table-column>
        <el-table-column label="网关" width="150">
          <template #default="{ row }">
            <span class="mono">{{ row.gateway || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="DHCP 范围" min-width="180">
          <template #default="{ row }">
            <span v-if="row.dhcp_start && row.dhcp_end" class="mono">
              {{ row.dhcp_start }} - {{ row.dhcp_end }}
            </span>
            <span v-else class="muted">—</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="300" fixed="right">
          <template #default="{ row }">
            <el-button size="small" type="success" :icon="VideoPlay" :disabled="row.active" @click="act(row, 'start')">启动</el-button>
            <el-button size="small" :icon="VideoPause" :disabled="!row.active" @click="act(row, 'stop')">停止</el-button>
            <el-button size="small" :icon="Edit" @click="openEdit(row)">编辑 XML</el-button>
            <el-button size="small" type="danger" :icon="Delete" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
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
      <el-input v-model="xmlForm.xml" type="textarea" :rows="14" placeholder="<network>...</network>" />
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

function errMsg(e, fallback) {
  return (e.response && e.response.data && e.response.data.message) || fallback
}

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
  try {
    if (type === 'start') await api.startNetwork(row.name)
    else await api.stopNetwork(row.name)
    ElMessage.success(type === 'start' ? '网络已启动' : '网络已停止')
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '操作失败'))
  }
}

async function remove(row) {
  try {
    await ElMessageBox.confirm('确定删除网络「' + row.name + '」？运行中的网络将一并停止。', '确认删除', { type: 'warning' })
    await api.deleteNetwork(row.name)
    ElMessage.success('网络已删除')
    await load()
  } catch (e) {
    if (e !== 'cancel' && e?.message !== 'cancel') {
      ElMessage.error(errMsg(e, '删除失败'))
    }
  }
}

onMounted(load)
</script>

<style scoped>
.page-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-xl);
}
.page-title {
  margin: 0;
  font-size: 1.1rem;
  font-weight: 700;
}
.page-desc {
  color: var(--color-muted-foreground);
  font-size: 0.9rem;
}
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-xl);
}
.toolbar-left,
.toolbar-right {
  display: flex;
  align-items: center;
  gap: var(--space-lg);
}
.count {
  color: var(--color-muted-foreground);
  font-size: 0.9rem;
}
.mono {
  font-family: var(--font-mono);
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
</style>