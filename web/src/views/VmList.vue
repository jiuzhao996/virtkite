<template>
  <div v-loading="loading">
    <el-card shadow="never">
      <div class="toolbar">
        <div>
          <el-button type="primary" :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <el-button type="success" :icon="Plus" @click="openCreate">新建虚拟机</el-button>
        </div>
        <span class="count">共 {{ total }} 台</span>
      </div>

      <el-table :data="items" stripe border style="width: 100%">
        <el-table-column prop="name" label="名称" min-width="140" />
        <el-table-column label="宿主机" min-width="130">
          <template #default="{ row }">{{ row.host ? row.host.name : ('ID ' + row.host_id) }}</template>
        </el-table-column>
        <el-table-column prop="storage_pool" label="存储池" width="100" />
        <el-table-column label="vCPU" width="90">
          <template #default="{ row }">{{ row.vcpu }} 核</template>
        </el-table-column>
        <el-table-column label="内存" width="110">
          <template #default="{ row }">{{ (row.memory_mb / 1024).toFixed(1) }} GB</template>
        </el-table-column>
        <el-table-column label="磁盘" width="100">
          <template #default="{ row }">{{ row.disk_gb }} GB</template>
        </el-table-column>
        <el-table-column prop="ip" label="IP" width="140" />
        <el-table-column label="状态" width="110">
          <template #default="{ row }">
            <el-tag :type="statusTag(row.status)" effect="light">{{ statusText(row.status) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" min-width="360" fixed="right">
          <template #default="{ row }">
            <el-button size="small" :disabled="busy.has(row.id)" @click="action(row, 'start')">开机</el-button>
            <el-button size="small" :disabled="busy.has(row.id)" @click="action(row, 'stop')">关机</el-button>
            <el-button size="small" :disabled="busy.has(row.id)" @click="action(row, 'restart')">重启</el-button>
            <el-button size="small" :disabled="busy.has(row.id)" @click="openSnapshots(row)">快照</el-button>
            <el-button size="small" :disabled="busy.has(row.id)" @click="openXML(row)">XML</el-button>
            <el-button size="small" :disabled="row.status !== 'running'" @click="openConsole(row)">控制台</el-button>
            <el-button size="small" type="danger" :disabled="busy.has(row.id)" @click="action(row, 'delete')">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="dialog" title="新建虚拟机" width="480px">
      <el-form :model="form" label-width="90px">
        <el-form-item label="名称" required>
          <el-input v-model="form.name" placeholder="仅字母、数字、_、-" />
        </el-form-item>
        <el-form-item label="宿主机" required>
          <el-select v-model="form.host_id" placeholder="选择宿主机" style="width: 100%">
            <el-option v-for="h in hosts" :key="h.id" :label="h.name" :value="h.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="存储池">
          <el-select v-model="form.storage_pool" placeholder="选择存储池" style="width: 100%">
            <el-option v-for="p in pools" :key="p" :label="p" :value="p" />
          </el-select>
        </el-form-item>
        <el-form-item label="ISO 路径">
          <el-input v-model="form.iso_path" placeholder="可选，如 /home/jiuzhao/data/img/Rocky-10.2-x86_64-minimal.iso" />
        </el-form-item>
        <el-form-item label="模板">
          <el-input v-model="form.template" placeholder="可选，如 ubuntu-22.04" />
        </el-form-item>
        <el-form-item label="CPU">
          <el-input-number v-model="form.vcpu" :min="1" :max="32" />
        </el-form-item>
        <el-form-item label="内存(MB)">
          <el-input-number v-model="form.memory_mb" :min="256" :step="256" />
        </el-form-item>
        <el-form-item label="磁盘(GB)">
          <el-input-number v-model="form.disk_gb" :min="1" :max="500" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">取消</el-button>
        <el-button type="primary" :loading="creating" @click="create">创建</el-button>
      </template>
    </el-dialog>

    <!-- 快照管理 -->
    <el-dialog v-model="snapDialog" :title="'快照管理 - ' + (curVM || '')" width="560px">
      <div class="toolbar">
        <div>
          <el-button type="success" size="small" :icon="Plus" @click="openCreateSnap">新建快照</el-button>
        </div>
        <span class="count">共 {{ snapshots.length }} 个</span>
      </div>
      <el-table :data="snapshots" stripe border size="small" style="width: 100%">
        <el-table-column prop="name" label="名称" min-width="180" />
        <el-table-column label="操作" width="160">
          <template #default="{ row }">
            <el-button size="small" @click="revertSnap(row)">回滚</el-button>
            <el-button size="small" type="danger" @click="removeSnap(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-dialog>

    <!-- 新建快照 -->
    <el-dialog v-model="snapCreateDialog" title="新建快照" width="420px">
      <el-form label-width="80px">
        <el-form-item label="名称" required>
          <el-input v-model="snapForm.name" placeholder="如 snap-20260903" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="snapCreateDialog = false">取消</el-button>
        <el-button type="primary" :loading="snapSaving" @click="createSnap">创建</el-button>
      </template>
    </el-dialog>

    <!-- XML 查看/编辑 -->
    <el-dialog v-model="xmlDialog" :title="'XML 定义 - ' + (curVM || '')" width="720px">
      <el-input v-model="xmlText" type="textarea" :rows="16" class="xml-area" />
      <template #footer>
        <el-button @click="xmlDialog = false">关闭</el-button>
        <el-button type="warning" @click="reloadXML">重新加载</el-button>
        <el-button type="primary" :loading="xmlSaving" @click="saveXML">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Plus } from '@element-plus/icons-vue'
import { api } from '../api'

const items = ref([])
const hosts = ref([])
const pools = ref([])
const total = ref(0)
const loading = ref(false)
const busy = ref(new Set())
const dialog = ref(false)
const creating = ref(false)
const snapDialog = ref(false)
const snapCreateDialog = ref(false)
const snapSaving = ref(false)
const curVM = ref('')
const curVMId = ref(null)
const snapshots = ref([])
const snapForm = ref({ name: '' })
const xmlDialog = ref(false)
const xmlText = ref('')
const xmlSaving = ref(false)

const form = reactive({ name: '', host_id: null, template: '', storage_pool: 'vmops', iso_path: '', vcpu: 1, memory_mb: 1024, disk_gb: 20 })

function statusText(s) {
  return { running: '运行中', stopped: '已关机', 'shut off': '已关机', paused: '已暂停', error: '异常' }[s] || s
}
function statusTag(s) {
  if (s === 'running') return 'success'
  if (s === 'paused') return 'warning'
  if (s === 'error') return 'danger'
  if (s === 'shut off' || s === 'stopped') return 'info'
  return 'primary'
}

async function load() {
  loading.value = true
  try {
    const [vms, hs, ps] = await Promise.all([api.listVMs(), api.listHosts(), api.listStoragePools()])
    items.value = (vms.data && vms.data.items) || []
    total.value = (vms.data && vms.data.total) || 0
    hosts.value = (hs.data && hs.data.items) || []
    pools.value = (ps.data && ps.data.items) ? ps.data.items.map((p) => p.name) : (ps.data || [])
    if (!pools.value.includes(form.storage_pool) && pools.value.length) form.storage_pool = pools.value[0]
  } catch (e) {
    ElMessage.error('获取虚拟机列表失败')
  } finally {
    loading.value = false
  }
}

function openCreate() {
  form.name = ''
  form.host_id = hosts.value.length ? hosts.value[0].id : null
  form.template = ''
  form.storage_pool = pools.value.includes('vmops') ? 'vmops' : (pools.value[0] || '')
  form.iso_path = ''
  form.vcpu = 1
  form.memory_mb = 1024
  form.disk_gb = 20
  dialog.value = true
}

async function create() {
  if (!form.name || !form.host_id) {
    ElMessage.warning('请填写名称和宿主机')
    return
  }
  creating.value = true
  try {
    await api.createVM({ ...form })
    ElMessage.success('创建成功（已在 KVM 宿主机落地）')
    dialog.value = false
    await load()
  } catch (e) {
    ElMessage.error((e.response && e.response.data && e.response.data.message) || e.response?.data?.detail || '创建失败')
  } finally {
    creating.value = false
  }
}

async function action(vm, type) {
  busy.value.add(vm.id)
  busy.value = new Set(busy.value)
  try {
    if (type === 'delete') {
      await ElMessageBox.confirm('确定删除虚拟机「' + vm.name + '」？此操作不可撤销。', '确认删除', { type: 'warning' })
      await api.deleteVM(vm.id)
    } else {
      await api[type + 'VM'](vm.id)
    }
    const label = { start: '开机', stop: '关机', restart: '重启', delete: '删除' }[type]
    ElMessage.success(label + '指令已执行')
    await load()
  } catch (e) {
    if (e !== 'cancel' && e?.message !== 'cancel') {
      ElMessage.error((e.response && e.response.data && e.response.data.message) || '操作失败')
    }
  } finally {
    busy.value.delete(vm.id)
    busy.value = new Set(busy.value)
  }
}

async function openSnapshots(vm) {
  curVM.value = vm.name
  curVMId.value = vm.id
  snapDialog.value = true
  try {
    const res = await api.listSnapshots(vm.id)
    snapshots.value = (res.data && res.data) || []
  } catch (e) {
    ElMessage.error('获取快照列表失败')
  }
}

function openCreateSnap() {
  snapForm.value = { name: '' }
  snapCreateDialog.value = true
}

async function createSnap() {
  if (!snapForm.value.name) {
    ElMessage.warning('请填写快照名称')
    return
  }
  snapSaving.value = true
  try {
    await api.createSnapshot(curVMId.value, snapForm.value.name)
    ElMessage.success('快照已创建')
    snapCreateDialog.value = false
    const res = await api.listSnapshots(curVMId.value)
    snapshots.value = (res.data && res.data) || []
  } catch (e) {
    ElMessage.error((e.response && e.response.data && e.response.data.error) || '创建失败')
  } finally {
    snapSaving.value = false
  }
}

async function revertSnap(snap) {
  try {
    await ElMessageBox.confirm('确定回滚到快照「' + snap.name + '」？此操作会覆盖当前状态。', '确认回滚', { type: 'warning' })
    await api.revertSnapshot(curVMId.value, snap.name)
    ElMessage.success('已回滚到快照')
  } catch (e) {
    if (e !== 'cancel') ElMessage.error((e.response && e.response.data && e.response.data.error) || '回滚失败')
  }
}

async function removeSnap(snap) {
  try {
    await ElMessageBox.confirm('确定删除快照「' + snap.name + '」？', '确认删除', { type: 'warning' })
    await api.deleteSnapshot(curVMId.value, snap.name)
    ElMessage.success('快照已删除')
    const res = await api.listSnapshots(curVMId.value)
    snapshots.value = (res.data && res.data) || []
  } catch (e) {
    if (e !== 'cancel') ElMessage.error((e.response && e.response.data && e.response.data.error) || '删除失败')
  }
}

async function openConsole(vm) {
  try {
    const res = await api.vncToken(vm.id)
    const d = res.data || {}
    if (!d.token) {
      ElMessage.error('获取控制台失败')
      return
    }
    // 新窗口打开 noVNC，连接 websockify :6080
    const url = window.location.origin.replace(/:8080$/, '')
    const base = url.includes(':') ? url.split(':')[0] : url
    const noVncUrl = 'http://' + base + ':6080/vnc.html?path=websockify?token=' + d.token
    window.open(noVncUrl, '_blank', 'width=1024,height=700')
  } catch (e) {
    ElMessage.error((e.response && e.response.data && e.response.data.error) || '无法连接控制台')
  }
}

async function openXML(vm) {
  curVM.value = vm.name
  curVMId.value = vm.id
  xmlDialog.value = true
  try {
    const res = await api.getVMXML(vm.id)
    xmlText.value = (res.data && res.data.xml) || ''
  } catch (e) {
    ElMessage.error('获取 XML 失败')
  }
}

async function reloadXML() {
  try {
    const res = await api.getVMXML(curVMId.value)
    xmlText.value = (res.data && res.data.xml) || ''
    ElMessage.success('已重新加载')
  } catch (e) {
    ElMessage.error('重新加载失败')
  }
}

async function saveXML() {
  if (!xmlText.value.trim()) {
    ElMessage.warning('XML 不能为空')
    return
  }
  xmlSaving.value = true
  try {
    await api.updateVMXML(curVMId.value, xmlText.value)
    ElMessage.success('XML 已保存')
  } catch (e) {
    ElMessage.error((e.response && e.response.data && e.response.data.error) || '保存失败')
  } finally {
    xmlSaving.value = false
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
.xml-area :deep(textarea) {
  font-family: monospace;
  font-size: 0.82rem;
}
</style>
