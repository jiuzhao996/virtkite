<template>
  <!-- Role 库表格：双击行=打开文件；行内操作=文件/删除。
       工具栏（刷新/新建）留在页面，本组件只负责这张表。 -->
  <el-table :data="roles" v-loading="loading" size="small" @row-dblclick="(row) => emit('files', row)">
    <el-table-column prop="name" label="名称" width="200">
      <template #default="{ row }"><span class="mono">{{ row.name }}</span></template>
    </el-table-column>
    <el-table-column prop="desc" label="说明" min-width="300" show-overflow-tooltip />
    <el-table-column label="文件" width="80">
      <template #default="{ row }">{{ row.file_count }}</template>
    </el-table-column>
    <el-table-column label="标识" width="90">
      <template #default="{ row }">
        <el-tag size="small" :type="row.built_in ? 'primary' : 'success'" effect="plain">{{ row.built_in ? '内置' : '自建' }}</el-tag>
      </template>
    </el-table-column>
    <el-table-column prop="updated_at" label="更新时间" width="170" />
    <el-table-column label="操作" width="160" fixed="right">
      <template #default="{ row }">
        <el-button text size="small" :icon="Folder" @click="emit('files', row)">文件</el-button>
        <el-popconfirm title="删除该 role？引用它的 playbook 会报「role 不存在」" confirm-button-text="删除" @confirm="emit('delete', row.name)">
          <template #reference>
            <el-button text size="small" type="danger" :icon="Delete">删除</el-button>
          </template>
        </el-popconfirm>
      </template>
    </el-table-column>
  </el-table>
</template>

<script setup>
// 运维自动化「Role 库」tab 的表格本体（纯展示 + 事件上抛）：
// 共享 role（data/ansible/roles/）列表，行操作只有打开文件与删除，均由页面处理。
import { Folder, Delete } from '@element-plus/icons-vue'

defineProps({
  // role 列表项 [{ name, desc, file_count, built_in, updated_at }]
  roles: { type: Array, default: () => [] },
  // 表格 loading
  loading: { type: Boolean, default: false }
})
// files(打开文件，含双击行) / delete(name)
const emit = defineEmits(['files', 'delete'])
</script>
