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
        <el-table-column prop="path" label="路径" min-width="180" show-overflow-tooltip />
        <el-table-column label="容量使用" min-width="220">
          <template #default="{ row }">
            <div class="cap-cell">
              <el-progress
                :percentage="usedPct(row)"
                :color="usageColor(usedPct(row))"
                :stroke-width="8"
                :show-text="false"
              />
              <span class="cap-text">
                {{ fmtSizeBytes(row.allocation) }} / {{ fmtSizeBytes(row.capacity) }} · 可用 {{ fmtSizeBytes(row.available) }}
              </span>
            </div>
          </template>
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
        <el-table-column prop="name" label="名称" min-width="150" />
        <el-table-column label="在用" width="170">
          <template #default="{ row }">
            <template v-if="volRefs[row.name] && refsInUse(volRefs[row.name])">
              <el-tooltip placement="top" :content="refsTooltip(volRefs[row.name])">
                <span class="ref-tags">
                  <el-tag v-if="volRefs[row.name].vms && volRefs[row.name].vms.length" type="warning" size="small" effect="light">
                    VM×{{ volRefs[row.name].vms.length }}
                  </el-tag>
                  <el-tag v-if="volRefs[row.name].images && volRefs[row.name].images.length" type="primary" size="small" effect="light">
                    镜像×{{ volRefs[row.name].images.length }}
                  </el-tag>
                  <el-tag v-if="volRefs[row.name].children && volRefs[row.name].children.length" type="danger" size="small" effect="light">
                    子卷×{{ volRefs[row.name].children.length }}
                  </el-tag>
                </span>
              </el-tooltip>
            </template>
            <el-tag v-else type="info" size="small" effect="plain">未使用</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="path" label="路径" min-width="200" show-overflow-tooltip />
        <el-table-column label="容量" width="100">
          <template #default="{ row }">{{ fmtSizeBytes(row.capacity) }}</template>
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
// 容量格式化 / 错误文案 / 取消判定统一走 utils/format.js（原本地三份实现已删）
// 本页的 .page-head / .page-title / .toolbar / .count 与其他列表页逐字相同，已收进 global.css
import { fmtSizeBytes, errMsg, isCancel, usageColor, clampPct } from '../utils/format'

const { isAdmin } = useAuth()

const pools = ref([])
const volumes = ref([])
const volRefs = ref({}) // 卷名 → {vms, images, children}
const loading = ref(false)
const saving = ref(false)
const poolDialog = ref(false)
const volDialog = ref(false)
const volCreateDialog = ref(false)
const curPool = ref('')

const poolForm = ref({ name: '', path: '' })
const volForm = ref({ name: '', format: 'qcow2', capacity: 20 })

// 池容量使用率（allocation/capacity），配色走 format.js 的阈值色
function usedPct(row) {
  if (!row.capacity) return 0
  return clampPct(Math.round((row.allocation / row.capacity) * 100))
}

function refsInUse(r) {
  return (r.vms && r.vms.length) || (r.images && r.images.length) || (r.children && r.children.length)
}
function refsTooltip(r) {
  const parts = []
  if (r.vms && r.vms.length) parts.push('虚拟机: ' + r.vms.join('、'))
  if (r.images && r.images.length) parts.push('镜像: ' + r.images.join('、'))
  if (r.children && r.children.length) parts.push('增量克隆子卷: ' + r.children.join('、'))
  return parts.join('；')
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

// 卷管理弹窗 + 引用数据（一次拉全池 refs，"在用"徽标与删卷确认共用）
async function openVolumes(row) {
  curPool.value = row.name
  volDialog.value = true
  try {
    const [poolRes, refsRes] = await Promise.all([
      api.getStoragePool(row.name),
      api.volumeRefs(row.name)
    ])
    volumes.value = (poolRes.data && poolRes.data.volumes) || []
    volRefs.value = (refsRes.data && refsRes.data.refs) || {}
  } catch (e) {
    ElMessage.error(errMsg(e, '获取卷列表失败'))
  }
}

// 刷新卷列表 + 引用（新建卷/删卷后调用）
async function refreshVolumes() {
  try {
    const [poolRes, refsRes] = await Promise.all([
      api.getStoragePool(curPool.value),
      api.volumeRefs(curPool.value)
    ])
    volumes.value = (poolRes.data && poolRes.data.volumes) || []
    volRefs.value = (refsRes.data && refsRes.data.refs) || {}
  } catch (e) {
    ElMessage.error(errMsg(e, '刷新卷列表失败'))
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
    await refreshVolumes()
  } catch (e) {
    ElMessage.error(errMsg(e, '创建失败'))
  } finally {
    saving.value = false
  }
}

async function removeVolume(row) {
  // 删卷确认带上引用详情；后端守卫同样会拦截（双保险）
  const r = volRefs.value[row.name]
  let msg = '确定删除卷「' + row.name + '」？此操作不可恢复。'
  if (r && refsInUse(r)) {
    msg = '卷「' + row.name + '」正在被使用：' + refsTooltip(r) + '。\n强删可能导致虚拟机磁盘损坏，确定继续？'
  }
  try {
    await ElMessageBox.confirm(msg, '确认删除', { type: refsInUse(r) ? 'error' : 'warning' })
    await api.deleteVolume(curPool.value, row.name)
    ElMessage.success('卷已删除')
    await refreshVolumes()
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '删除失败'))
  }
}

onMounted(load)
</script>

<style scoped>
/* 池容量使用率单元格：进度条 + 数字行 */
.cap-cell {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding-right: 12px;
}
.cap-text {
  font-size: 0.8rem;
  color: var(--color-muted-foreground);
  white-space: nowrap;
}
.ref-tags {
  display: inline-flex;
  gap: 4px;
}
</style>
