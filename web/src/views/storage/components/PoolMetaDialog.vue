<template>
  <!-- 池信息编辑（角色 + 描述） -->
  <el-dialog :close-on-click-modal="false" v-model="visible" :title="'编辑池信息 - ' + form.name" width="480px">
    <el-form :model="form" label-width="80px">
      <el-form-item label="角色">
        <el-select v-model="form.role" style="width: 100%">
          <el-option label="自动推断（空）" value="" />
          <el-option v-for="r in roles" :key="r" :label="r" :value="r" />
        </el-select>
      </el-form-item>
      <el-form-item label="描述">
        <el-input
          v-model="form.description"
          type="textarea"
          :rows="3"
          maxlength="500"
          show-word-limit
          placeholder="池用途说明（最长 500 字符）"
        />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="saving" @click="emit('save')">保存</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
// 存储池「编辑池信息」弹窗：改角色 + 描述。直接 v-model 编辑页面传入的 form 对象，
// 保存动作 emit('save') 由页面调 api.updatePoolMeta（本组件不持状态、不发请求）。
defineProps({
  // 页面持有的表单对象 { name, role, description }（直接改其字段）
  form: { type: Object, required: true },
  // 可选角色白名单（后端下发，兜底见页面 FALLBACK_POOL_ROLES）
  roles: { type: Array, default: () => [] },
  // 保存中（按钮 loading）
  saving: { type: Boolean, default: false }
})
const emit = defineEmits(['save'])
// 弹窗开关（父级 v-model="metaDialog"）
const visible = defineModel({ type: Boolean, default: false })
</script>
