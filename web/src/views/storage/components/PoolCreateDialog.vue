<template>
  <!-- 新建存储池 -->
  <el-dialog v-model="visible" title="新建存储池" width="460px">
    <el-form ref="formRef" :model="form" :rules="rules" label-width="80px">
      <el-form-item label="名称" prop="name">
        <el-input v-model="form.name" placeholder="仅字母、数字、_、-、." />
      </el-form-item>
      <el-form-item label="路径" prop="path">
        <el-input v-model="form.path" placeholder="如 /var/lib/libvirt/xxx-images" />
      </el-form-item>
      <el-form-item label="描述">
        <el-input
          v-model="form.description"
          type="textarea"
          :rows="2"
          maxlength="500"
          show-word-limit
          placeholder="可选，创建成功后保存为池描述"
        />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="saving" @click="emit('create')">创建</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
// 存储池「新建」弹窗：名称/路径行内校验（rules 由页面传入，与后端 handler/storage.go 同名文案）。
// 直接 v-model 编辑页面传入的 form；提交动作 emit('create')，由页面先 validate() 再调接口。
// 暴露 validate()：页面在 createPool 里 await poolFormRef.value.validate()，语义与拆前一致
// （拆前该 ref 指向 el-form 本身，拆后指向本组件、内部再转发给 el-form）。
import { ref } from 'vue'

defineProps({
  // 页面持有的表单对象 { name, path, description }
  form: { type: Object, required: true },
  // 校验规则（页面 poolRules 原样传入）
  rules: { type: Object, default: () => ({}) },
  saving: { type: Boolean, default: false }
})
const emit = defineEmits(['create'])
// 弹窗开关（父级 v-model="poolDialog"）
const visible = defineModel({ type: Boolean, default: false })

const formRef = ref(null)
// 供页面 createPool 调用；el-form 未挂载时返回 resolved，避免抛错（与拆前 await 语义一致）
defineExpose({ validate: () => formRef.value?.validate() })
</script>
