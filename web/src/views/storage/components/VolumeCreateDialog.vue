<template>
  <!-- 新建卷 -->
  <el-dialog v-model="visible" title="新建存储卷" width="460px">
    <el-form :model="form" label-width="90px">
      <el-form-item label="名称" required>
        <el-input v-model="form.name" placeholder="如 vm-disk1.qcow2" />
      </el-form-item>
      <el-form-item label="格式">
        <el-select v-model="form.format" style="width: 100%">
          <el-option label="qcow2" value="qcow2" />
          <el-option label="raw" value="raw" />
        </el-select>
      </el-form-item>
      <el-form-item label="容量(GB)">
        <el-input-number v-model="form.capacity" :min="1" :max="500" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="saving" @click="emit('create')">创建</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
// 存储卷「新建」弹窗：名称/格式/容量。直接 v-model 编辑页面传入的 form，
// 提交 emit('create') 由页面调 api.createVolume（卷名必填校验留在页面的 createVolume 里）。
defineProps({
  // 页面持有的表单对象 { name, format, capacity }
  form: { type: Object, required: true },
  saving: { type: Boolean, default: false }
})
const emit = defineEmits(['create'])
// 弹窗开关（父级 v-model="volCreateDialog"）
const visible = defineModel({ type: Boolean, default: false })
</script>
