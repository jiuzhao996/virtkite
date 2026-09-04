<template>
  <div v-loading="loading">
    <div class="page-head">
      <h2 class="page-title">虚拟机管理</h2>
    </div>
    <el-card shadow="never">
      <div class="toolbar">
        <div>
          <el-button type="primary" :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <el-button type="success" :icon="Plus" @click="router.push({ name: 'vm-create' })">新建虚拟机</el-button>
          <el-button type="warning" :icon="Upload" @click="openImport">导入存量 VM</el-button>
        </div>
        <span class="count">共 {{ total }} 台</span>
      </div>

      <el-table :data="items" stripe border style="width: 100%">
        <template #empty><el-empty description="暂无虚拟机" :image-size="80" /></template>
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
        <el-table-column label="操作" min-width="300" fixed="right">
          <template #default="{ row }">
            <el-button size="small" :icon="Search" @click="router.push({ name: 'vm-detail', params: { id: row.id } })">详情</el-button>
            <el-button v-if="row.status !== 'running'" size="small" :icon="VideoPlay" :disabled="busy.has(row.id)" @click="action(row, 'start')">开机</el-button>
            <el-button v-else size="small" :icon="SwitchButton" :disabled="busy.has(row.id)" @click="action(row, 'stop')">关机</el-button>
            <el-button size="small" :icon="Monitor" :disabled="row.status !== 'running'" @click="openConsole(row)">控制台</el-button>
            <el-dropdown trigger="click" @command="(cmd) => moreAction(row, cmd)">
              <el-button size="small">
                更多<el-icon class="el-icon--right"><ArrowDown /></el-icon>
              </el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item command="restart" :icon="RefreshRight" :disabled="row.status !== 'running' || busy.has(row.id)">重启</el-dropdown-item>
                  <el-dropdown-item command="delete" :icon="Delete" divided :disabled="busy.has(row.id)">删除</el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 导入存量 VM（纳管 virsh 已有域） -->
    <el-dialog v-model="importDialog" title="导入存量 VM" width="780px">
      <div v-loading="importScanning" class="import-body">
        <el-alert
          v-if="importHostName"
          type="info"
          :closable="false"
          show-icon
          :title="`宿主机「${importHostName}」共检测到 ${importTotal} 台域：已纳管 ${importManaged} 台，未纳管 ${importUnmanaged} 台`"
          style="margin-bottom: 12px"
        />
        <el-empty v-if="!importScanning && !unmanaged.length" description="暂无未纳管的存量 VM" />
        <el-table
          v-else
          :data="unmanaged"
          stripe
          border
          size="small"
          style="width: 100%"
          @selection-change="selected = $event"
        >
          <el-table-column type="selection" width="44" />
          <el-table-column prop="name" label="名称" min-width="130" />
          <el-table-column label="状态" width="90">
            <template #default="{ row }">
              <el-tag :type="statusTag(row.state)" effect="light">{{ statusText(row.state) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="规格" width="150">
            <template #default="{ row }">{{ row.vcpu }}核 / {{ (row.memory_mb / 1024).toFixed(0) }}GB / {{ row.disk_gb }}GB</template>
          </el-table-column>
          <el-table-column prop="mac_address" label="MAC" width="150" />
          <el-table-column prop="disk_path" label="磁盘路径" min-width="220" show-overflow-tooltip />
        </el-table>
      </div>
      <template #footer>
        <el-button @click="importDialog = false">取消</el-button>
        <el-button type="primary" :disabled="!selected.length" :loading="importing" @click="doImport">
          导入所选（{{ selected.length }} 台）
        </el-button>
      </template>
    </el-dialog>

  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Plus, Upload, VideoPlay, SwitchButton, RefreshRight, Monitor, Delete, Search, ArrowDown } from '@element-plus/icons-vue'
import { api } from '../api'

const router = useRouter()

const items = ref([])
const total = ref(0)
const loading = ref(false)
const busy = ref(new Set())

const importDialog = ref(false)
const importScanning = ref(false)
const importing = ref(false)
const importHostId = ref(null)
const importHostName = ref('')
const importTotal = ref(0)
const importManaged = ref(0)
const importUnmanaged = ref(0)
const unmanaged = ref([])
const selected = ref([])

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
    const vms = await api.listVMs()
    items.value = (vms.data && vms.data.items) || []
    total.value = (vms.data && vms.data.total) || 0
  } catch (e) {
    ElMessage.error('获取虚拟机列表失败')
  } finally {
    loading.value = false
  }
}

// 静默轮询：仅刷新列表与总数，不动表单（参考 KvmDash 5s 轮询）
function silentRefresh() {
  if (busy.value.size || importScanning.value) return
  api
    .listVMs()
    .then((res) => {
      items.value = (res.data && res.data.items) || []
      total.value = (res.data && res.data.total) || 0
    })
    .catch(() => {})
}

let pollTimer = null

async function openImport() {
  importDialog.value = true
  importScanning.value = true
  importHostName.value = ''
  importUnmanaged.value = 0
  unmanaged.value = []
  selected.value = []
  try {
    const res = await api.scanImportVMs()
    const data = (res && res.data) || {}
    importHostId.value = data.host_id || null
    importHostName.value = data.host_name || ''
    importTotal.value = data.total || 0
    importManaged.value = data.managed || 0
    importUnmanaged.value = data.unmanaged || 0
    unmanaged.value = ((data.items || []).filter((i) => !i.managed))
  } catch (e) {
    ElMessage.error((e.response && e.response.data && e.response.data.detail) || '扫描失败，无法连接 libvirt')
  } finally {
    importScanning.value = false
  }
}

async function doImport() {
  if (!selected.value.length) {
    ElMessage.warning('请先勾选要导入的虚拟机')
    return
  }
  importing.value = true
  try {
    const res = await api.importVMs(importHostId.value, selected.value.map((i) => i.name))
    const d = (res && res.data) || {}
    ElMessage.success(`导入完成：成功 ${d.imported} 台${d.skipped ? '，跳过(已纳管) ' + d.skipped + ' 台' : ''}${d.failed ? '，失败 ' + d.failed + ' 台' : ''}`)
    importDialog.value = false
    await load()
  } catch (e) {
    ElMessage.error((e.response && e.response.data && e.response.data.message) || '导入失败')
  } finally {
    importing.value = false
  }
}

async function action(vm, type) {
  busy.value.add(vm.id)
  busy.value = new Set(busy.value)
  try {
    if (type === 'delete') {
      await ElMessageBox.prompt(
        '此操作不可撤销。请输入虚拟机名称「' + vm.name + '」以确认删除：',
        '确认删除',
        {
          type: 'warning',
          confirmButtonText: '确认删除',
          cancelButtonText: '取消',
          inputPlaceholder: vm.name,
          inputValidator: (v) => (v && v.trim() === vm.name) || '请输入正确的虚拟机名称'
        }
      )
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

// “更多”下拉统一入口：重启/快照/XML/删除
function moreAction(vm, cmd) {
  if (cmd === 'restart' || cmd === 'delete') action(vm, cmd)
}

async function openConsole(vm) {
  router.push({ name: 'console', params: { id: vm.id } })
}

onMounted(() => {
  load()
  pollTimer = setInterval(silentRefresh, 5000)
})
onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
})
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
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-xl);
}
.count {
  color: var(--color-muted-foreground);
  font-size: 0.9rem;
}
.xml-area :deep(textarea) {
  font-family: var(--font-mono);
  font-size: 0.82rem;
}
</style>
