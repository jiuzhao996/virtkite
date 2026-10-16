<template>
  <el-card shadow="never">
    <Toolbar gap="var(--space-lg)" right-gap="var(--space-lg)">
      <template #left>
        <el-button :icon="Refresh" :loading="isoLoading" @click="emit('refresh')">刷新</el-button>
        <span class="os-hint">ISO 为共享安装介质，管理（上传/删除）请到对应存储池</span>
      </template>
      <template #right>
        <span class="count">共 {{ isoItems.length }} 个</span>
      </template>
    </Toolbar>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      style="margin-bottom: 12px"
      title="ISO 用于创建 VM 的「本地安装介质 (ISO)」方式：安装系统时从激活存储池选择 ISO 挂载光驱。可到「ISO 市场」tab 一键下载官方安装镜像。"
    />
    <el-table v-loading="isoLoading" :data="isoItems" stripe border style="width: 100%">
      <template #empty><el-empty description="激活存储池中未发现 .iso 卷" :image-size="80" /></template>
      <el-table-column label="名称" min-width="200">
        <template #default="{ row }">
          <span class="mono">{{ row.name }}</span>
        </template>
      </el-table-column>
      <el-table-column label="大小" width="120">
        <template #default="{ row }">{{ fmtSizeBytes(row.capacity) }}</template>
      </el-table-column>
      <el-table-column label="来源存储池" width="140">
        <template #default="{ row }">{{ row.pool }}</template>
      </el-table-column>
      <el-table-column label="路径" min-width="280" show-overflow-tooltip>
        <template #default="{ row }">
          <span class="mono">{{ row.path }}</span>
        </template>
      </el-table-column>
    </el-table>
  </el-card>
</template>

<script setup>
// 镜像页「ISO 安装镜像」tab 的内容：只读列出激活存储池中的 .iso 卷
// （创建 VM「本地安装介质」方式的数据源）。纯展示；刷新 emit('refresh') 由页面 loadIso 处理。
import { Refresh } from '@element-plus/icons-vue'
import { fmtSizeBytes } from '../../../utils/format'
import Toolbar from '../../../components/Toolbar.vue'

defineProps({
  // 聚合后的 .iso 卷列表（每项 { name, capacity, pool, path }）
  isoItems: { type: Array, default: () => [] },
  isoLoading: { type: Boolean, default: false }
})
const emit = defineEmits(['refresh'])
</script>

<style scoped>
/* .os-hint 自 ImageList.vue 原样搬入（类名不变，DOM 不变） */
.os-hint {
  color: var(--color-muted-foreground);
  font-size: 0.82rem;
}
</style>
