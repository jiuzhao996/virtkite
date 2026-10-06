<template>
  <div>
    <PageHead title="回收站">
      <template #subtitle>
        <!-- 历史 p.page-desc（UA 外边距参与布局），经插槽原样保留 -->
        <p class="page-desc">删除的虚拟机在此保留，可一键恢复；记录永久保留，直至手动彻底清除；彻底清除将物理删除记录与无主卷</p>
      </template>
    </PageHead>

    <el-card shadow="never" v-loading="loading">
      <!-- 计数与按钮原为 .toolbar 直接子元素（两端对齐左右分列），经默认插槽保持同构 -->
      <Toolbar>
        <span class="count">共 {{ items.length }} 条记录</span>
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
      </Toolbar>
      <!-- 空态：回收站没有软删记录 -->
      <el-empty v-if="!loading && items.length === 0" description="回收站是空的" :image-size="80" />
      <!-- 行点击进入原机信息抽屉；行内按钮 .stop 防冒泡 -->
      <el-table v-else :data="items" size="small" @row-click="openDetail" row-class-name="clickable-row">
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
        <el-table-column label="域状态" width="100">
          <template #header>
            <el-tooltip
              content="存在 = libvirt 中仍有同名域定义（删除中途失败的残留），恢复后可直接开机；不存在 = 域定义已彻底删除，恢复后需重新定义"
              placement="top"
            >
              <span class="col-help">
                域状态
                <el-icon><QuestionFilled /></el-icon>
              </span>
            </el-tooltip>
          </template>
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
              @click.stop="restore(row)"
            >恢复</el-button>
            <el-button
              text
              type="danger"
              size="small"
              :icon="Delete"
              :disabled="actingId === row.id"
              @click.stop="purge(row)"
            >彻底清除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 原机信息抽屉（行点击进入）：数据全部来自列表行，无需再请求。
         字段以回收站接口 deletedVMItem 实际返回为准（id/name/uuid/status/storage_pool/deleted_at/domain_exists），
         vCPU/内存/磁盘等规格字段后端未下发，缺的字段不编造 -->
    <el-drawer v-model="detailOpen" title="原机信息" :size="440" :append-to-body="true" destroy-on-close>
      <template v-if="detail">
        <div class="rb-head">
          <span class="rb-name">{{ detail.name }}</span>
          <el-tag :type="vmStatusTag(detail.status)" effect="light" size="small">{{ vmStatusText(detail.status, '未知') }}</el-tag>
          <el-tag :type="detail.domain_exists ? 'success' : 'info'" effect="light" size="small">
            {{ detail.domain_exists ? '域存在' : '域不存在' }}
          </el-tag>
        </div>
        <el-descriptions :column="1" border size="small">
          <el-descriptions-item label="UUID">
            <span class="mono uuid">{{ detail.uuid || '—' }}</span>
          </el-descriptions-item>
          <el-descriptions-item label="所属存储池">{{ detail.storage_pool || '—' }}</el-descriptions-item>
          <el-descriptions-item label="删除时间">
            <span class="mono">{{ fmtDateTime(detail.deleted_at) }}</span>
          </el-descriptions-item>
          <el-descriptions-item label="libvirt 域">
            <!-- 语义与表格「域状态」列 tooltip 同源：存在多为删除中途失败的残留 -->
            {{ detail.domain_exists
              ? '仍存在同名域定义（删除中途失败的残留），恢复后可直接开机'
              : '域定义已彻底删除，恢复后需重新定义' }}
          </el-descriptions-item>
        </el-descriptions>
        <div class="rb-note">恢复后配置原样回归；彻底清除不可逆。</div>
      </template>
    </el-drawer>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, RefreshLeft, Delete, QuestionFilled } from '@element-plus/icons-vue'
import { api } from '../api'
import { errMsg, isCancel, vmStatusText, vmStatusTag, fmtDateTime } from '../utils/format'
import PageHead from '../components/PageHead.vue'
import Toolbar from '../components/Toolbar.vue'

// ===== 列表（GET /vms-recycle → {total, items}）=====
const loading = ref(false)
const items = ref([])

async function load() {
  loading.value = true
  try {
    const res = await api.recycleList()
    const d = res.data
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
    const res = await api.recycleRestore(row.id)
    const d = res.data || {}
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
      { type: 'warning', confirmButtonText: '彻底清除', cancelButtonText: '取消', confirmButtonClass: 'el-button--danger' }
    )
  } catch (e) {
    return // 用户取消
  }
  actingId.value = row.id
  try {
    const res = await api.recyclePurge(row.id)
    const d = res.data || {}
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

// ===== 原机信息抽屉（行点击进入）：数据全部来自列表行，无需再请求 =====
const detailOpen = ref(false)
const detail = ref(null)

function openDetail(row) {
  detail.value = row
  detailOpen.value = true
}

onMounted(load)
</script>

<style scoped>
/* .toolbar / .count 已收进 global.css */
.vm-name {
  font-weight: 600;
}
.uuid {
  font-size: 0.85rem;
}
/* 列头帮助图标（域状态 tooltip 触发区） */
.col-help {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  cursor: help;
}
.col-help .el-icon {
  font-size: 13px;
  color: var(--color-muted-foreground);
}

/* ===== 原机信息抽屉 ===== */
.rb-head {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-md);
  margin-bottom: var(--space-xl);
}
.rb-name {
  font-size: 18px;
  font-weight: 600;
}
.rb-note {
  margin-top: var(--space-xl);
  padding: var(--space-md) var(--space-lg);
  font-size: 12px;
  line-height: 1.6;
  color: var(--el-color-primary-dark-2);
  background: var(--el-color-primary-light-9);
  border-radius: var(--radius-sm);
}

/* 行点击进入信息抽屉：scoped 需穿透 el-table 内部行 */
:deep(.clickable-row) {
  cursor: pointer;
}
</style>
