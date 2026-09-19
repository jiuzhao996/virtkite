<template>
  <div>
    <div class="page-head">
      <div>
        <h3 class="page-title">回收站</h3>
        <p class="page-desc">删除的虚拟机在此保留，可一键恢复；彻底清除将物理删除记录与无主卷</p>
      </div>
      <el-button text type="primary" :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
    </div>

    <el-card shadow="never" v-loading="loading">
      <!-- 空态：回收站没有软删记录 -->
      <el-empty v-if="!loading && items.length === 0" description="回收站是空的" :image-size="80" />
      <el-table v-else :data="items" size="small">
        <el-table-column label="名称" prop="name" min-width="140" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="vm-name">{{ row.name }}</span>
          </template>
        </el-table-column>
        <el-table-column label="UUID" min-width="220" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="mono uuid">{{ row.uuid || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="最后状态" width="100">
          <template #default="{ row }">
            <el-tag :type="vmStatusTag(row.status)" effect="light" size="small">{{ vmStatusText(row.status, '未知') }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="存储池" prop="storage_pool" width="110">
          <template #default="{ row }">{{ row.storage_pool || '—' }}</template>
        </el-table-column>
        <el-table-column label="删除时间" width="170">
          <template #default="{ row }">{{ fmtDateTime(row.deleted_at) }}</template>
        </el-table-column>
        <el-table-column label="域状态" width="90">
          <template #default="{ row }">
            <!-- 域仍存在多为删除中途失败的残留，恢复后可直接开机；不存在则需重新定义 -->
            <el-tag :type="row.domain_exists ? 'success' : 'info'" effect="light" size="small">
              {{ row.domain_exists ? '存在' : '不存在' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="150" fixed="right">
          <template #default="{ row }">
            <el-button
              text
              type="primary"
              size="small"
              :icon="RefreshLeft"
              :loading="actingId === row.id"
              @click="restore(row)"
            >恢复</el-button>
            <el-button
              text
              type="danger"
              size="small"
              :icon="Delete"
              :disabled="actingId === row.id"
              @click="purge(row)"
            >彻底清除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, RefreshLeft, Delete } from '@element-plus/icons-vue'
import http from '../api'
import { errMsg, isCancel, vmStatusText, vmStatusTag, fmtDateTime } from '../utils/format'

// ===== 列表（GET /vms-recycle → {total, items}）=====
const loading = ref(false)
const items = ref([])

async function load() {
  loading.value = true
  try {
    const res = await http.get('/vms-recycle')
    const d = res.data.data
    // 兼容 data 直接是数组或包一层 { items }（当前实现为后者）
    items.value = Array.isArray(d) ? d : (d && d.items) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '获取回收站列表失败'))
  } finally {
    loading.value = false
  }
}

// ===== 恢复（POST /vms-recycle/:id/restore）=====
const actingId = ref(null)

async function restore(row) {
  actingId.value = row.id
  try {
    const res = await http.post(`/vms-recycle/${row.id}/restore`)
    const d = res.data.data || {}
    // 域已 undefine 的记录只能恢复数据库行：诚实提示而非假装「干净恢复」
    if (d.domain_defined === false) {
      ElMessage.warning(d.message || '记录已恢复，但虚拟机定义已不存在，可在创建向导用同名卷重新定义')
    } else {
      ElMessage.success(d.message || `已恢复 ${row.name}`)
    }
    load()
  } catch (e) {
    ElMessage.error(errMsg(e, '恢复失败'))
  } finally {
    actingId.value = null
  }
}

// ===== 彻底清除（DELETE /vms-recycle/:id/purge?purge_volumes=true）=====
async function purge(row) {
  try {
    await ElMessageBox.confirm(
      `确定彻底清除「${row.name}」？该操作不可恢复：数据库记录将物理删除，卷文件将一并删除，共享卷会被平台保留。`,
      '彻底清除确认',
      { type: 'warning', confirmButtonText: '彻底清除', cancelButtonText: '取消' }
    )
  } catch (e) {
    return // 用户取消
  }
  actingId.value = row.id
  try {
    const res = await http.delete(`/vms-recycle/${row.id}/purge`, { params: { purge_volumes: 'true' } })
    const d = res.data.data || {}
    const kept = Array.isArray(d.volumes_kept) ? d.volumes_kept : []
    if (kept.length > 0) {
      // 守卫保留的卷（镜像库登记 / 增量克隆父盘等）：提示原因，属预期行为非失败
      ElMessage.warning(`已清除记录，${kept.length} 个卷被保留：${kept.join('；')}`)
    } else {
      ElMessage.success(d.message || `已彻底清除 ${row.name}`)
    }
    load()
  } catch (e) {
    ElMessage.error(errMsg(e, '彻底清除失败'))
  } finally {
    actingId.value = null
  }
}

onMounted(load)
</script>

<style scoped>
.vm-name {
  font-weight: 600;
}
.uuid {
  font-size: 0.85rem;
}
</style>
