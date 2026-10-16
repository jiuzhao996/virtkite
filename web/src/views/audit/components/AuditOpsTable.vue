<template>
  <!-- 行点击钻取：整行可点开详情抽屉（详情单元格同入口，操作列已无冗余按钮）；clickable-table 只作用于本表，
       不波及「控制台会话」tab（SessionList 为独立组件，行内另有虚拟机链接与断开按钮） -->
  <el-table :data="items" stripe border style="width: 100%" class="clickable-table" @row-click="(row) => emit('row-click', row)">
    <template #empty><el-empty description="暂无审计记录" :image-size="80" /></template>
    <el-table-column label="时间" min-width="172">
      <template #default="{ row }">
        <span class="mono">{{ fmtDateTime(row.created_at) }}</span>
      </template>
    </el-table-column>
    <el-table-column label="操作人" width="120">
      <template #default="{ row }">
        <span>{{ row.username || '—' }}</span>
      </template>
    </el-table-column>
    <el-table-column label="操作" width="160">
      <template #default="{ row }">
        <el-tag :type="actionTagType(row.action)" effect="plain">{{ actionLabel(row.action) }}</el-tag>
      </template>
    </el-table-column>
    <el-table-column prop="object_type" label="对象" width="100" />
    <el-table-column label="来源 IP" width="150">
      <template #default="{ row }">
        <span class="mono">{{ row.source_ip || '—' }}</span>
      </template>
    </el-table-column>
    <el-table-column label="状态" width="90">
      <template #default="{ row }">
        <el-tag :type="row.status === 'success' ? 'success' : 'danger'" effect="light">
          {{ row.status === 'success' ? '成功' : '失败' }}
        </el-tag>
      </template>
    </el-table-column>
    <el-table-column label="详情" min-width="220">
      <template #default="{ row }">
        <el-tooltip
          v-if="row.detail"
          :content="row.detail"
          placement="top"
          :show-after="300"
          :enterable="false"
          popper-class="audit-detail-popper"
        >
          <span class="detail-cell mono" @click.stop="emit('row-click', row)">{{ row.detail }}</span>
        </el-tooltip>
        <span v-else class="muted">—</span>
      </template>
    </el-table-column>
  </el-table>
</template>

<script setup>
// 审计中心「操作日志」表：行数据 + 动作标签着色（actionTagType 纯函数搬入）+
// 详情单元格工具提示。整行/详情单元格点击统一 emit('row-click', row)（页面打开详情抽屉）。
// actionMap（动作→中文）由页面传入并随 api.auditActions 更新，故用 props 而非本地常量。
import { fmtDateTime } from '../../../utils/format'

const props = defineProps({
  items: { type: Array, default: () => [] },
  // 动作 → 中文映射（页面 actionMap）
  actionMap: { type: Object, default: () => ({}) }
})
const emit = defineEmits(['row-click'])

// ⚠️ 与 AuditDetailDrawer.vue / AuditList.vue 的动作中文口径保持一致——改一处须同步
function actionLabel(action) {
  if (!action) return '—'
  return props.actionMap[action] || action
}

// 动作大类着色（加在既有 actionLabel 调用处）：登录/登出 primary、删除 danger、
// 创建/克隆/导入/上传 success、电源类 warning、其余 info；集合核实自 middleware/audit.go
// determineAction/vmAction 与 service/jumpd/server.go（jumpd.cmd_blocked 落「其余」info）
function actionTagType(action) {
  if (!action) return 'info'
  if (action === 'login' || action === 'logout') return 'primary'
  if (action.startsWith('delete_')) return 'danger'
  if (
    action.startsWith('create_') ||
    action.startsWith('clone_') ||
    action.startsWith('import_') ||
    action.startsWith('upload_')
  ) {
    return 'success'
  }
  if (['start_vm', 'stop_vm', 'restart_vm', 'pause_vm', 'resume_vm'].includes(action)) return 'warning'
  return 'info'
}
</script>

<style scoped>
/* 以下规则自 AuditList.vue 原样搬入（类名不变，DOM 不变） */
.mono {
  font-size: 0.85rem;
}
.muted {
  color: var(--color-muted-foreground);
}
.detail-cell {
  display: block;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-primary);
  cursor: pointer;
}
/* ===== 行点击钻取 ===== */
/* el-table 内部渲染的 tr 拿不到本组件 scoped 属性，必须 :deep 穿透；
   限定 .clickable-table 前缀，只对操作日志表生效，不波及「控制台会话」tab 的行 */
.clickable-table :deep(.el-table__row) {
  cursor: pointer;
}
/* tooltip 内容 teleport 到 body，scoped 下用 :deep 穿透；超长 detail 不把 tooltip 撑出视口 */
:deep(.audit-detail-popper) {
  max-width: 480px;
}
</style>
