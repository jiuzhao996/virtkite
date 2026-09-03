<template>
  <div v-loading="loading">
    <el-card shadow="never">
      <div class="toolbar">
        <div>
          <el-button type="primary" :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <el-button type="success" :icon="Plus" @click="openCreate">新建网络</el-button>
          <el-button type="warning" :icon="Plus" @click="openXML">从 XML 定义</el-button>
        </div>
        <span class="count">共 {{ networks.length }} 个网络</span>
      </div>

      <el-table :data="networks" stripe border style="width: 100%">
        <el-table-column prop="name" label="名称" min-width="120" />
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.active ? 'success' : 'info'" effect="light">
              {{ row.active ? '活动' : '停止' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="bridge" label="网桥" width="120" />
        <el-table-column prop="forward" label="转发模式" width="110" />
        <el-table-column prop="gateway" label="网关" width="140" />
        <el-table-column prop="cidr" label="掩码" width="130" />
        <el-table-column label="操作" width="260" fixed="right">
          <template #default="{ row }">
            <el-button size="small" @click="viewXML(row)">XML</el-button>
            <el-button size="small" :disabled="!row.active" @click="act(row, 'stop')">停止</el-button>
            <el-button size="small" :disabled="row.active" @click="act(row, 'start')">启动</el-button>
            <el-button size="small" type="danger" @click="remove(row)">删除</el-button>
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
    <el-dialog v-model="xmlDialog" title="从 XML 定义网络" width="620px">
      <el-input v-model="xmlForm.xml" type="textarea" :rows="12" placeholder="<network>...</network>" />
      <template #footer>
        <el-button @click="xmlDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="defineXML">定义</el-button>
      </template>
    </el-dialog>

    <!-- 查看 XML -->
    <el-dialog v-model="viewDialog" title="网络 XML" width="620px">
      <pre class="xml-view">{{ curXML }}</pre>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Plus } from '@element-plus/icons-vue'
import { api } from '../api'

const networks = ref([])
const loading = ref(false)
const saving = ref(false)
const createDialog = ref(false)
const xmlDialog = ref(false)
const viewDialog = ref(false)
const curXML = ref('')

const createForm = ref({ name: '', gateway: '' })
const xmlForm = ref({ xml: '' })

async function load() {
  loading.value = true
  try {
    const res = await api.listNetworks()
    networks.value = (res.data && res.data.items) || []
  } catch (e) {
    ElMessage.error('获取网络列表失败')
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
    ElMessage.error((e.response && e.response.data && e.response.data.error) || '创建失败')
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
    ElMessage.error((e.response && e.response.data && e.response.data.error) || '定义失败')
  } finally {
    saving.value = false
  }
}

async function viewXML(row) {
  try {
    const res = await api.getNetwork(row.name)
    curXML.value = (res.data && res.data.xml) || ''
    viewDialog.value = true
  } catch (e) {
    ElMessage.error('获取 XML 失败')
  }
}

async function act(row, type) {
  try {
    if (type === 'start') await api.startNetwork(row.name)
    else await api.stopNetwork(row.name)
    ElMessage.success(type === 'start' ? '网络已启动' : '网络已停止')
    await load()
  } catch (e) {
    ElMessage.error((e.response && e.response.data && e.response.data.error) || '操作失败')
  }
}

async function remove(row) {
  try {
    await ElMessageBox.confirm('确定删除网络「' + row.name + '」？', '确认删除', { type: 'warning' })
    await api.deleteNetwork(row.name)
    ElMessage.success('网络已删除')
    await load()
  } catch (e) {
    if (e !== 'cancel') ElMessage.error((e.response && e.response.data && e.response.data.error) || '删除失败')
  }
}

onMounted(load)
</script>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 14px;
}
.count {
  color: #888;
  font-size: 0.9rem;
}
.xml-view {
  background: #0f1720;
  color: #9fe8a8;
  padding: 14px;
  border-radius: 6px;
  max-height: 420px;
  overflow: auto;
  font-size: 0.82rem;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>