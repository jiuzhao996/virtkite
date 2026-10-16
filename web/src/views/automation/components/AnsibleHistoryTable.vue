<template>
  <!-- 执行历史表格：行可点（跳转回看该任务的输出）；行内操作=取消（运行中）/任务中心 -->
  <el-table :data="history" v-loading="loading" size="small" row-class-name="clickable-row" @row-click="(row) => emit('view', row)">
    <el-table-column prop="id" label="ID" width="70" />
    <el-table-column prop="title" label="任务" min-width="220" show-overflow-tooltip />
    <el-table-column label="状态" width="100">
      <template #default="{ row }">
        <el-tag size="small" effect="plain" :type="statusTag(row.status)">{{ statusLabel(row.status) }}</el-tag>
      </template>
    </el-table-column>
    <el-table-column prop="created_at" label="提交时间" width="180" />
    <el-table-column label="操作" width="170" fixed="right">
      <template #default="{ row }">
        <el-button
          v-if="row.status === 'running'"
          text size="small" type="warning"
          :loading="cancellingId === row.id"
          @click.stop="emit('cancel', row)"
        >取消</el-button>
        <!-- 出口：跳到任务中心看该任务的完整时间线与结构化结果 -->
        <el-button text size="small" @click.stop="emit('task-center', row)">任务中心 →</el-button>
      </template>
    </el-table-column>
  </el-table>
</template>

<script setup>
// 运维自动化「执行历史」tab 的表格本体（ansible_run 类型任务）：
// 行点击=回看输出（emit view），行内=取消 / 跳任务中心；由页面处理并持有数据。
defineProps({
  // 历史任务列表 [{ id, title, status, created_at, ... }]
  history: { type: Array, default: () => [] },
  // 表格 loading
  loading: { type: Boolean, default: false },
  // 正在取消的任务 id（用于按钮 loading）
  cancellingId: { type: [Number, String], default: null },
  // 状态 → el-tag 类型 / 中文标签（复用页面映射）
  statusTag: { type: Function, required: true },
  statusLabel: { type: Function, required: true }
})
// view(回看) / cancel(取消，行) / task-center(跳任务中心，行)
const emit = defineEmits(['view', 'cancel', 'task-center'])
</script>

<style scoped>
/* 行点击钻取：el-table 内部 tr 拿不到 scoped 属性，须 :deep 穿透（自 Automation.vue 搬入） */
:deep(tr.clickable-row) { cursor: pointer; }
</style>
