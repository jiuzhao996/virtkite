<template>
  <div v-loading="loading">
    <div class="page-head">
      <h2 class="page-title">存储管理</h2>
    </div>
    <el-card shadow="never">
      <div class="toolbar">
        <div>
          <el-button type="primary" :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <el-button v-if="isAdmin" type="success" :icon="Plus" @click="openCreatePool">新建存储池</el-button>
        </div>
        <span class="count">共 {{ pools.length }} 个存储池</span>
      </div>

      <el-table :data="pools" stripe border style="width: 100%" empty-text="暂无存储池数据">
        <el-table-column prop="name" label="名称" min-width="120" />
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.active ? 'success' : 'info'" effect="light">
              {{ row.active ? '活动' : '停止' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="path" label="路径" min-width="200" show-overflow-tooltip />
        <el-table-column label="容量" width="110">
          <template #default="{ row }">{{ fmtSize(row.capacity) }}</template>
        </el-table-column>
        <el-table-column label="已分配" width="110">
          <template #default="{ row }">{{ fmtSize(row.allocation) }}</template>
        </el-table-column>
        <el-table-column label="可用" width="110">
          <template #default="{ row }">{{ fmtSize(row.available) }}</template>
        </el-table-column>
        <el-table-column prop="vol_count" label="卷数" width="80" />
        <el-table-column label="操作" width="200" fixed="right">
          <template #default="{ row }">
            <el-button size="small" :icon="FolderOpened" @click="openVolumes(row)">卷管理</el-button>
            <el-button v-if="isAdmin" size="small" type="danger" :icon="Delete" @click="removePool(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 新建存储池 -->
    <el-dialog v-model="poolDialog" title="新建存储池" width="460px">
      <el-form :model="poolForm" label-width="80px">
        <el-form-item label="名称" required>
          <el-input v-model="poolForm.name" placeholder="仅字母、数字、_、-" />
        </el-form-item>
        <el-form-item label="路径" required>
          <el-input v-model="poolForm.path" placeholder="如 /var/lib/libvirt/xxx-images" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="poolDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="createPool">创建</el-button>
      </template>
    </el-dialog>

    <!-- 卷管理 -->
    <el-dialog v-model="volDialog" :title="'卷管理 - ' + (curPool || '')" width="680px">
      <div class="toolbar">
        <div>
          <el-button v-if="isAdmin" type="success" size="small" :icon="Plus" @click="openCreateVol">新建卷</el-button>
        </div>
      </div>
      <el-table :data="volumes" stripe border size="small" style="width: 100%" max-height="380" empty-text="该存储池暂无卷">
        <el-table-column prop="name" label="名称" min-width="160" />
        <el-table-column prop="path" label="路径" min-width="220" show-overflow-tooltip />
        <el-table-column label="容量" width="100">
          <template #default="{ row }">{{ fmtSize(row.capacity) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="90">
          <template #default="{ row }">
            <el-button v-if="isAdmin" size="small" type="danger" :icon="Delete" @click="removeVolume(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-dialog>

    <!-- 新建卷 -->
    <el-dialog v-model="volCreateDialog" title="新建存储卷" width="460px">
      <el-form :model="volForm" label-width="90px">
        <el-form-item label="名称" required>
          <el-input v-model="volForm.name" placeholder="如 vm-disk1.qcow2" />
        </el-form-item>
        <el-form-item label="格式">
          <el-select v-model="volForm.format" style="width: 100%">
            <el-option label="qcow2" value="qcow2" />
            <el-option label="raw" value="raw" />
          </el-select>
        </el-form-item>
        <el-form-item label="容量(GB)">
          <el-input-number v-model="volForm.capacity" :min="1" :max="500" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="volCreateDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="createVolume">创建</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Plus, FolderOpened, Delete } from '@element-plus/icons-vue'
import { api } from '../api'
import { useAuth } from '../store/auth'

const { isAdmin } = useAuth()

const pools = ref([])
const volumes = ref([])
const loading = ref(false)
const saving = ref(false)
const poolDialog = ref(false)
const volDialog = ref(false)
const volCreateDialog = ref(false)
const curPool = ref('')

const poolForm = ref({ name: '', path: '' })
const volForm = ref({ name: '', format: 'qcow2', capacity: 20 })

function fmtSize(n) {
  if (n === null || n === undefined || n === '') return '—'
  const gb = n / 1024 / 1024 / 1024
  return gb >= 1024 ? (gb / 1024).toFixed(1) + ' TB' : gb.toFixed(1) + ' GB'
}

// 后端统一返回 {code, message, data}，错误提示取 message 字段
function errMsg(e, fallback) {
  return (e.response && e.response.data && e.response.data.message) || fallback
}

// 确认框点取消/点 X 关闭都视为取消，不弹错误提示
function isCancel(e) {
  return e === 'cancel' || e === 'close' || e?.message === 'cancel' || e?.message === 'close'
}

async function load() {
  loading.value = true
  try {
    const res = await api.listStoragePools()
    pools.value = (res.data && res.data.items) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '获取存储池失败'))
  } finally {
    loading.value = false
  }
}

function openCreatePool() {
  poolForm.value = { name: '', path: '' }
  poolDialog.value = true
}

async function createPool() {
  if (!poolForm.value.name || !poolForm.value.path) {
    ElMessage.warning('请填写名称和路径')
    return
  }
  saving.value = true
  try {
    await api.createStoragePool({ ...poolForm.value })
    ElMessage.success('存储池已创建')
    poolDialog.value = false
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '创建失败'))
  } finally {
    saving.value = false
  }
}

async function removePool(row) {
  try {
    await ElMessageBox.confirm('确定删除存储池「' + row.name + '」？', '确认删除', { type: 'warning' })
    await api.deleteStoragePool(row.name)
    ElMessage.success('存储池已删除')
    await load()
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '删除失败'))
  }
}

async function openVolumes(row) {
  curPool.value = row.name
  volDialog.value = true
  try {
    const res = await api.getStoragePool(row.name)
    volumes.value = (res.data && res.data.volumes) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '获取卷列表失败'))
  }
}

function openCreateVol() {
  volForm.value = { name: '', format: 'qcow2', capacity: 20 }
  volCreateDialog.value = true
}

async function createVolume() {
  if (!volForm.value.name) {
    ElMessage.warning('请填写卷名称')
    return
  }
  saving.value = true
  try {
    await api.createVolume(curPool.value, { ...volForm.value })
    ElMessage.success('卷已创建')
    volCreateDialog.value = false
    const res = await api.getStoragePool(curPool.value)
    volumes.value = (res.data && res.data.volumes) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '创建失败'))
  } finally {
    saving.value = false
  }
}

async function removeVolume(row) {
  try {
    await ElMessageBox.confirm('确定删除卷「' + row.name + '」？', '确认删除', { type: 'warning' })
    await api.deleteVolume(curPool.value, row.name)
    ElMessage.success('卷已删除')
    const res = await api.getStoragePool(curPool.value)
    volumes.value = (res.data && res.data.volumes) || []
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '删除失败'))
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
</style>