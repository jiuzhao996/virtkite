<template>
  <div class="filters">
    <el-select v-model="q.action" placeholder="操作类型" clearable filterable style="width: 180px" @change="emit('search')">
      <el-option v-for="a in actionOptions" :key="a.value" :label="a.label" :value="a.value" />
    </el-select>
    <el-select v-model="q.object_type" placeholder="对象类型" clearable style="width: 130px" @change="emit('search')">
      <el-option v-for="o in objectOptions" :key="o.value" :label="o.label" :value="o.value" />
    </el-select>
    <el-input v-model="q.username" placeholder="操作人" clearable style="width: 140px" @keyup.enter="emit('search')" @clear="emit('search')" />
    <el-select v-model="q.status" placeholder="状态" clearable style="width: 110px" @change="emit('search')">
      <el-option label="成功" value="success" />
      <el-option label="失败" value="failed" />
    </el-select>
    <el-date-picker
      v-model="range"
      type="daterange"
      range-separator="至"
      start-placeholder="开始日期"
      end-placeholder="结束日期"
      value-format="YYYY-MM-DD"
      :shortcuts="dateShortcuts"
      @change="emit('range-change', $event)"
    />
    <el-button type="primary" :icon="Search" :loading="loading" @click="emit('search')">查询</el-button>
    <el-button :icon="RefreshLeft" @click="emit('reset')">重置</el-button>
    <el-button :icon="Refresh" :loading="loading" @click="emit('refresh')">刷新</el-button>
    <el-button :icon="Download" :loading="exporting" @click="emit('export')">导出</el-button>
  </div>
</template>

<script setup>
// 审计中心「操作日志」筛选条：操作类型/对象类型/操作人/状态 + 日期区间 + 查询/重置/刷新/导出。
// q 为页面持有的 reactive 筛选对象（直接改字段，与拆分前一致）；range 双向绑定页面的日期区间。
// 各按钮动作一律 emit 上抛，实际查询/导出仍由页面执行。
import { Search, RefreshLeft, Refresh, Download } from '@element-plus/icons-vue'

defineProps({
  // 页面筛选对象 { action, object_type, username, status, start, end }（直接改其字段）
  q: { type: Object, required: true },
  // 操作类型下拉项 / 对象类型下拉项 / 日期快捷项（均由页面提供）
  actionOptions: { type: Array, default: () => [] },
  objectOptions: { type: Array, default: () => [] },
  dateShortcuts: { type: Array, default: () => [] },
  loading: { type: Boolean, default: false },
  exporting: { type: Boolean, default: false }
})
const emit = defineEmits(['search', 'reset', 'refresh', 'export', 'range-change'])
// 日期区间（父级 v-model:range="range"）
const range = defineModel('range', { type: Array, default: null })
</script>

<style scoped>
/* .filters 规则自 AuditList.vue 原样搬入（类名不变，DOM 不变） */
.filters {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-lg);
  margin-bottom: var(--space-xl);
}
</style>
