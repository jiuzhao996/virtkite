<template>
  <!-- Playbook 库表格：双击行=编辑；行内操作=执行/文件/编辑/删除。
       工具栏（刷新/新建）留在页面，本组件只负责这张表。 -->
  <el-table :data="playbooks" v-loading="loading" size="small" @row-dblclick="(row) => emit('edit', row)">
    <el-table-column prop="id" label="ID" width="150">
      <template #default="{ row }"><span class="mono">{{ row.id }}</span></template>
    </el-table-column>
    <el-table-column prop="name" label="名称" min-width="130" />
    <el-table-column prop="desc" label="说明" min-width="260" show-overflow-tooltip />
    <el-table-column label="标识" width="90">
      <template #default="{ row }">
        <el-tag size="small" :type="row.built_in ? 'primary' : 'success'" effect="plain">{{ row.built_in ? '内置' : '自建' }}</el-tag>
      </template>
    </el-table-column>
    <el-table-column label="形态" width="90">
      <template #default="{ row }">
        <el-tag size="small" :type="row.is_dir ? 'warning' : 'info'" effect="plain">
          {{ row.is_dir ? '目录' : '单文件' }}
        </el-tag>
      </template>
    </el-table-column>
    <el-table-column prop="updated_at" label="更新时间" width="170" />
    <el-table-column label="操作" width="270" fixed="right">
      <template #default="{ row }">
        <el-button text size="small" type="primary" :icon="VideoPlay" @click="emit('run', row)" :disabled="!installed" :title="installed ? '' : '宿主机未安装 Ansible'">执行</el-button>
        <el-button v-if="row.is_dir" text size="small" :icon="Folder" @click="emit('files', row)">文件</el-button>
        <el-button text size="small" :icon="Edit" @click="emit('edit', row)">编辑</el-button>
        <el-popconfirm title="删除该 playbook？" confirm-button-text="删除" @confirm="emit('delete', row.id)">
          <template #reference>
            <el-button text size="small" type="danger" :icon="Delete">删除</el-button>
          </template>
        </el-popconfirm>
      </template>
    </el-table-column>
  </el-table>
</template>

<script setup>
// 运维自动化「Playbook 库」tab 的表格本体（纯展示 + 事件上抛）：
// 页面负责刷新/新建工具栏与数据加载，本组件把每行的执行/文件/编辑/删除动作 emit 回页面。
import { VideoPlay, Folder, Edit, Delete } from '@element-plus/icons-vue'

defineProps({
  // playbook 列表项 [{ id, name, desc, built_in, is_dir, updated_at }]
  playbooks: { type: Array, default: () => [] },
  // 表格 loading
  loading: { type: Boolean, default: false },
  // 宿主机是否已装 Ansible（false 时禁用「执行」）
  installed: { type: Boolean, default: false }
})
// run(执行) / files(文件) / edit(编辑，含双击行) / delete(id)
const emit = defineEmits(['run', 'files', 'edit', 'delete'])
</script>
