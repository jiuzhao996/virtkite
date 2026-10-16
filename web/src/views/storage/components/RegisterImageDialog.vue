<template>
  <!-- 登记为云镜像（把池内已有卷纳入镜像库，不复制不移动） -->
  <el-dialog v-model="visible" :title="'登记为云镜像 - ' + volName" width="460px">
    <el-form :model="form" label-width="100px">
      <el-form-item label="镜像名称" required>
        <el-input v-model="form.name" placeholder="镜像库中的显示名称" />
      </el-form-item>
      <el-form-item label="OS 版本">
        <el-input v-model="form.os_version" placeholder="如 ubuntu-22.04，可留空" />
      </el-form-item>
      <el-form-item label="模板镜像">
        <el-switch v-model="form.is_template" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="saving" @click="emit('register')">登记</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
// 存储池「登记为云镜像」弹窗：把池内已有卷纳入镜像库（不复制不移动）。
// 直接 v-model 编辑页面传入的 form（含隐藏的 path 字段，用于 api.registerImage）；
// 登记 emit('register') 由页面调接口（名称必填校验留在页面的 doRegister 里）。
defineProps({
  // 页面持有的表单对象 { name, os_version, is_template, path }
  form: { type: Object, required: true },
  // 被登记的卷名（标题展示）
  volName: { type: String, default: '' },
  saving: { type: Boolean, default: false }
})
const emit = defineEmits(['register'])
// 弹窗开关（父级 v-model="regDialog"）
const visible = defineModel({ type: Boolean, default: false })
</script>
