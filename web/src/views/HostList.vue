<template>
  <div v-loading="loading">
    <div class="page-head">
      <h2 class="page-title">宿主机管理</h2>
    </div>
    <el-card shadow="never">
      <div class="toolbar">
        <div>
          <el-button type="primary" :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <el-button v-if="isAdmin" type="success" :icon="Plus" @click="openCreate">添加宿主机</el-button>
        </div>
        <span class="count">共 {{ total }} 台</span>
      </div>

      <el-table :data="items" stripe border style="width: 100%">
        <el-table-column prop="name" label="名称" min-width="140" />
        <el-table-column prop="ssh_ip" label="SSH IP" min-width="140" />
        <el-table-column prop="ssh_user" label="SSH 用户" width="110" />
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.status === 'online' ? 'success' : row.status === 'offline' ? 'danger' : 'info'" effect="light">
              {{ statusText(row.status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" min-width="220" fixed="right">
          <template #default="{ row }">
            <el-button size="small" :icon="Connection" :loading="testBusy.has(row.id)" @click="test(row)">测试连通</el-button>
            <el-button size="small" :icon="DataLine" @click="showStats(row)">查看状态</el-button>
            <el-button v-if="isAdmin" size="small" type="danger" :icon="Delete" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="statsDialog" :title="(statsTarget ? statsTarget.name : '') + ' · 资源状态'" width="460px">
      <div v-if="!statsData" class="loading">加载中...</div>
      <el-alert v-else-if="statsData.error" :title="statsData.error" type="error" :closable="false" />
      <el-descriptions v-else :column="1" border>
        <el-descriptions-item label="主机名">{{ statsData.hostname }}</el-descriptions-item>
        <el-descriptions-item label="内核">{{ statsData.kernel }}</el-descriptions-item>
        <el-descriptions-item label="CPU">{{ statsData.cpu_cores }} 核</el-descriptions-item>
        <el-descriptions-item label="内存">{{ statsData.memory_used }} / {{ statsData.memory_total }}</el-descriptions-item>
        <el-descriptions-item label="运行时长">{{ statsData.uptime }}</el-descriptions-item>
      </el-descriptions>
    </el-dialog>

    <el-dialog v-model="dialog" title="添加宿主机" width="480px">
      <el-form :model="form" label-width="90px">
        <el-form-item label="名称" required>
          <el-input v-model="form.name" />
        </el-form-item>
        <el-form-item label="Libvirt URI">
          <el-input v-model="form.libvirt_uri" placeholder="qemu:///system" />
        </el-form-item>
        <el-form-item label="SSH IP" required>
          <el-input v-model="form.ssh_ip" />
        </el-form-item>
        <el-form-item label="SSH 端口">
          <el-input-number v-model="form.ssh_port" :min="1" :max="65535" />
        </el-form-item>
        <el-form-item label="SSH 用户">
          <el-input v-model="form.ssh_user" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="form.description" type="textarea" :rows="2" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">取消</el-button>
        <el-button type="primary" :loading="creating" @click="create">添加</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Plus, Connection, DataLine, Delete } from '@element-plus/icons-vue'
import { api } from '../api'
import { useAuth } from '../store/auth'

const { isAdmin } = useAuth()

const items = ref([])
const total = ref(0)
const loading = ref(false)
const testBusy = ref(new Set())
const statsDialog = ref(false)
const statsTarget = ref(null)
const statsData = ref(null)
const dialog = ref(false)
const creating = ref(false)

const form = reactive({
  name: '',
  libvirt_uri: 'qemu:///system',
  ssh_ip: '',
  ssh_port: 22,
  ssh_user: 'root',
  description: ''
})

function statusText(s) {
  return { online: '在线', offline: '离线', unknown: '未知' }[s] || s
}

async function load() {
  loading.value = true
  try {
    const res = await api.listHosts()
    items.value = (res.data && res.data.items) || []
    total.value = (res.data && res.data.total) || 0
  } catch (e) {
    ElMessage.error('获取宿主机列表失败')
  } finally {
    loading.value = false
  }
}

async function test(h) {
  testBusy.value.add(h.id)
  testBusy.value = new Set(testBusy.value)
  try {
    const res = await api.testHost(h.id)
    const d = res.data || {}
    if (d.reachable) ElMessage.success('连通 ✓ 延迟 ' + (d.latency_ms || '—') + ' ms')
    else ElMessage.warning('无法连通该宿主机')
  } catch (e) {
    ElMessage.error((e.response && e.response.data && e.response.data.message) || '连通性测试失败')
  } finally {
    testBusy.value.delete(h.id)
    testBusy.value = new Set(testBusy.value)
  }
}

async function showStats(h) {
  statsTarget.value = h
  statsData.value = null
  statsDialog.value = true
  try {
    const res = await api.hostStats(h.id)
    statsData.value = res.data || {}
  } catch (e) {
    statsData.value = { error: (e.response && e.response.data && e.response.data.message) || '获取失败' }
  }
}

async function remove(h) {
  try {
    await ElMessageBox.confirm('确定删除宿主机「' + h.name + '」？', '确认删除', { type: 'warning' })
    await api.deleteHost(h.id)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    if (e !== 'cancel' && e?.message !== 'cancel') {
      ElMessage.error((e.response && e.response.data && e.response.data.message) || '删除失败')
    }
  }
}

function openCreate() {
  Object.assign(form, {
    name: '',
    libvirt_uri: 'qemu:///system',
    ssh_ip: '',
    ssh_port: 22,
    ssh_user: 'root',
    description: ''
  })
  dialog.value = true
}

async function create() {
  if (!form.name || !form.ssh_ip) {
    ElMessage.warning('请填写名称和 SSH IP')
    return
  }
  creating.value = true
  try {
    await api.createHost({ ...form })
    ElMessage.success('添加成功')
    dialog.value = false
    await load()
  } catch (e) {
    ElMessage.error((e.response && e.response.data && e.response.data.message) || '添加失败')
  } finally {
    creating.value = false
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
.loading {
  color: var(--color-muted-foreground);
  text-align: center;
  padding: 30px 0;
}
</style>
