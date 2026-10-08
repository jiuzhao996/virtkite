<template>
  <div>
    <PageHead title="回收站">
      <template #subtitle>
        <!-- 历史 p.page-desc（UA 外边距参与布局），经插槽原样保留 -->
        <p class="page-desc">
          删除 = 软删记录 + 删除 libvirt 域定义 + 按守卫删除自有磁盘（快照随之丢弃）。
          <b>此处保留的是数据库记录，不保证磁盘还在</b>——域与磁盘都存在可原样恢复；
          域没了但磁盘还在会按记录重建精简定义（单系统盘 + 默认网络）；两者都不在则
          只能恢复一条空记录。彻底清除将物理删除记录与无主卷
        </p>
      </template>
    </PageHead>

    <el-card shadow="never" v-loading="loading">
      <!-- 计数走默认插槽（左），操作按钮走 right 插槽（Toolbar 自带 flex 骨架与 gap） -->
      <Toolbar>
        <span class="count">共 {{ items.length }} 条记录<span v-if="checked.length" class="checked-hint"> · 已选 {{ checked.length }} 条</span></span>
        <template #right>
          <el-button
            v-if="checked.length"
            type="danger"
            plain
            :icon="Delete"
            :loading="bulkBusy"
            @click="bulkPurge"
          >批量清除（{{ checked.length }}）</el-button>
          <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        </template>
      </Toolbar>
      <!-- 空态：回收站没有软删记录 -->
      <el-empty v-if="!loading && items.length === 0" description="回收站是空的" :image-size="80" />
      <!-- 行点击进入原机信息抽屉；行内按钮 .stop 防冒泡；勾选列点击不触发抽屉（selection 列判断） -->
      <el-table v-else ref="tableRef" :data="items" size="small" @row-click="openDetail" @selection-change="(rows) => (checked = rows)" row-class-name="clickable-row">
        <el-table-column type="selection" width="86">
          <!-- 默认表头只有裸复选框，意图不自明：自绘「复选框 + 全选」文字，
               勾选走 el-table 原生 toggleAllSelection（半选态由 checked/items 推导） -->
          <template #header>
            <el-checkbox
              :model-value="allSelected"
              :indeterminate="checked.length > 0 && !allSelected"
              @change="tableRef && tableRef.toggleAllSelection()"
            >全选</el-checkbox>
          </template>
        </el-table-column>
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
        <el-table-column label="磁盘" width="100">
          <template #header>
            <el-tooltip
              content="存在 = 系统盘卷仍在存储池中，恢复时可重建定义；不存在 = 磁盘已被删除，恢复只能捞回记录（开机必然失败）"
              placement="top"
            >
              <span class="col-help">
                磁盘
                <el-icon><QuestionFilled /></el-icon>
              </span>
            </el-tooltip>
          </template>
          <template #default="{ row }">
            <el-tooltip :content="row.disk_volume || '未找到系统盘卷'" placement="top" :disabled="!row.disk_volume">
              <el-tag :type="row.disk_exists ? 'success' : 'danger'" effect="light" size="small">
                {{ row.disk_exists ? '存在' : '已删除' }}
              </el-tag>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column label="可恢复性" width="110">
          <template #header>
            <el-tooltip content="域与磁盘都在 = 原样恢复；仅域残留 = 直接可开；仅磁盘在且有删除时存档 = 精确重建（多盘/固件/网卡全还原）；仅磁盘在无存档 = 精简重建；都不在 = 只能恢复记录" placement="top">
              <span class="col-help">
                可恢复性
                <el-icon><QuestionFilled /></el-icon>
              </span>
            </el-tooltip>
          </template>
          <template #default="{ row }">
            <el-tag :type="restoreLevel(row).type" effect="light" size="small">{{ restoreLevel(row).text }}</el-tag>
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
              :disabled="bulkBusy"
              @click.stop="restore(row)"
            >恢复</el-button>
            <el-button
              text
              type="danger"
              size="small"
              :icon="Delete"
              :disabled="actingId === row.id || bulkBusy"
              @click.stop="purge(row)"
            >彻底清除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 原机信息抽屉（行点击进入）：数据全部来自列表行，无需再请求。
         字段以回收站接口 deletedVMItem 实际返回为准（id/name/uuid/status/storage_pool/deleted_at/domain_exists/disk_exists/disk_volume），
         vCPU/内存等规格字段后端未下发，缺的字段不编造 -->
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
              : '域定义已彻底删除，恢复后可重建精简定义' }}
          </el-descriptions-item>
          <el-descriptions-item label="系统盘卷">
            {{ detail.disk_exists
              ? `仍在存储池中：${detail.disk_volume || '（卷名未取到）'}`
              : '已被物理删除——恢复只能捞回记录，该记录无法开机' }}
          </el-descriptions-item>
          <el-descriptions-item label="可恢复性">
            <el-tag :type="restoreLevel(detail).type" effect="light" size="small">{{ restoreLevel(detail).text }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="域定义存档">
            {{ detail.has_archive
              ? '有（删除时留存了完整域定义，恢复可精确重建：多盘/固件/光驱/原网络配置全还原）'
              : '无（恢复只能按记录精简重建：单系统盘 + 默认 NAT 网卡）' }}
          </el-descriptions-item>
        </el-descriptions>
        <div class="rb-note">
          恢复的边界：域与磁盘都在 → 原样恢复；仅域残留 → 直接可开；仅磁盘在 → 重建精简定义
          （单系统盘 + 默认 NAT 网卡，多盘需手动挂回，机器类型/光驱等不回填）；两者都不在 → 只能捞回记录。
        </div>
      </template>
    </el-drawer>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, RefreshLeft, Delete, QuestionFilled } from '@element-plus/icons-vue'
import { api } from '../api'
import { errMsg, isCancel, vmStatusText, vmStatusTag, fmtDateTime } from '../utils/format'
import PageHead from '../components/PageHead.vue'
import Toolbar from '../components/Toolbar.vue'

// ===== 列表（GET /vms-recycle → {total, items}）=====
const router = useRouter()
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

// 可恢复性分级（C+B）：域/磁盘/存档三者组合决定恢复结果
function restoreLevel(row) {
  if (row.domain_exists && row.disk_exists) return { text: '原样恢复', type: 'success' }
  if (row.domain_exists) return { text: '域残留可开', type: 'success' }
  if (row.disk_exists && row.has_archive) return { text: '精确重建', type: 'success' }
  if (row.disk_exists) return { text: '精简重建', type: 'warning' }
  return { text: '仅恢复记录', type: 'danger' }
}

async function restore(row) {
  // 域与磁盘都不在：先说清后果再动手（此前是恢复完才提示，用户已白点一次）
  if (!row.domain_exists && !row.disk_exists) {
    try {
      await ElMessageBox.confirm(
        `${row.name} 的 libvirt 域定义与系统盘卷都已不存在。恢复只会捞回一条数据库记录，该记录无法开机（可在创建向导用同名卷重新定义）。确定继续恢复？`,
        '恢复后无法开机',
        { type: 'warning', confirmButtonText: '仍要恢复', cancelButtonText: '取消', confirmButtonClass: 'el-button--danger' }
      )
    } catch (e) {
      return
    }
  }
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
    // 流程出口：恢复完成给「下一步」入口（此前恢复后无任何去向引导）
    try {
      await ElMessageBox.confirm(
        `已恢复 ${row.name}，是否前往虚拟机列表查看？`,
        '恢复成功',
        { type: 'success', confirmButtonText: '查看虚拟机', cancelButtonText: '留在本页' }
      )
      router.push('/vms')
    } catch (e) {
      // 取消 = 留在本页，非错误
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

// ===== 批量彻底清除（勾选后循环调单条 purge：守卫逐条生效，语义与单条完全一致）=====
const tableRef = ref(null)
const checked = ref([])
const bulkBusy = ref(false)
// 表头自绘「全选」复选框的选中态：有勾选且勾满全部才算全选（半选由 indeterminate 表达）
const allSelected = computed(() => items.value.length > 0 && checked.value.length === items.value.length)

async function bulkPurge() {
  const rows = checked.value
  if (!rows.length) return
  // 名称列表 >5 台截断，确认框里一眼看清删的是谁（不可恢复操作，信息必须给足）
  const names = rows.map((r) => r.name)
  const nameText = names.length > 5 ? names.slice(0, 5).join('、') + ` 等 ${names.length} 台` : names.join('、')
  try {
    await ElMessageBox.confirm(
      `确定彻底清除以下 ${rows.length} 条记录？该操作不可恢复：${nameText}。数据库记录将物理删除，卷文件将一并删除，共享卷会被平台保留。`,
      '批量彻底清除确认',
      { type: 'warning', confirmButtonText: '彻底清除', cancelButtonText: '取消', confirmButtonClass: 'el-button--danger' }
    )
  } catch (e) {
    return // 用户取消
  }
  bulkBusy.value = true
  let ok = 0
  let keptTotal = 0
  const failed = []
  try {
    // 顺序执行：purge 带 per-VM 锁与卷守卫，并发会互相撞锁；回收站量级小，顺序无压力
    for (const row of rows) {
      try {
        const res = await api.recyclePurge(row.id)
        const d = res.data || {}
        const kept = Array.isArray(d.volumes_kept) ? d.volumes_kept : []
        keptTotal += kept.length
        ok++
      } catch (e) {
        failed.push(`${row.name}（${errMsg(e, '清除失败')}）`)
      }
    }
    if (failed.length) {
      ElMessage.warning(`清除完成：成功 ${ok} 台，失败 ${failed.length} 台：${failed.slice(0, 3).join('；')}${failed.length > 3 ? ` 等 ${failed.length} 台` : ''}`)
    } else if (keptTotal > 0) {
      ElMessage.warning(`已清除 ${ok} 条记录，${keptTotal} 个卷被守卫保留（共享/被引用）`)
    } else {
      ElMessage.success(`已彻底清除 ${ok} 条记录`)
    }
    checked.value = []
    await load()
  } finally {
    bulkBusy.value = false
  }
}

// ===== 原机信息抽屉（行点击进入）：数据全部来自列表行，无需再请求 =====
const detailOpen = ref(false)
const detail = ref(null)

function openDetail(row, column) {
  // 勾选列点击不弹抽屉（row-click 第二参为被点列；type=selection 即勾选框区域）
  if (column && column.type === 'selection') return
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
