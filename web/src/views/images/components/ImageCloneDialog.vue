<template>
  <!-- 基于模板创建虚拟机 -->
  <el-dialog v-model="visible" :title="'基于模板创建虚拟机 - ' + (cloneImg.name || '')" width="480px">
    <el-alert type="info" :closable="false" show-icon class="clone-tip" title="将引用模板文件作为系统盘创建虚拟机，创建后可在虚拟机列表管理" />
    <el-form label-width="90px">
      <el-form-item label="名称" required>
        <el-input v-model="cloneForm.name" placeholder="仅字母、数字、_、-" />
      </el-form-item>
      <el-form-item label="CPU 核数">
        <el-input-number v-model="cloneForm.vcpu" :min="1" :max="64" />
      </el-form-item>
      <el-form-item label="内存(MB)">
        <el-input-number v-model="cloneForm.memory_mb" :min="256" :max="131072" :step="256" />
      </el-form-item>
      <el-form-item label="网络">
        <el-select v-model="cloneForm.network" clearable placeholder="留空使用 default" style="width: 100%">
          <el-option
            v-for="n in netOptions"
            :key="n.name"
            :label="n.gateway ? n.name + '（' + n.gateway + '）' : n.name"
            :value="n.name"
          />
        </el-select>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="cloning" @click="emit('create')">创建</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
// 镜像页「基于模板创建虚拟机」弹窗：名称/CPU/内存/网络。
// cloneImg（模板镜像对象，仅用于标题）与 cloneForm（页面表单对象）由页面传入、直接改字段；
// 提交 emit('create') 由页面 cloneVm() 执行（名称必填校验留在页面）。
defineProps({
  // 被克隆的模板镜像（标题用 name）
  cloneImg: { type: Object, default: () => ({}) },
  // 页面表单对象 { name, vcpu, memory_mb, network }
  cloneForm: { type: Object, required: true },
  // 网络下拉候选 [{ name, gateway }]
  netOptions: { type: Array, default: () => [] },
  cloning: { type: Boolean, default: false }
})
const emit = defineEmits(['create'])
// 弹窗开关（父级 v-model="cloneDialog"）
const visible = defineModel({ type: Boolean, default: false })
</script>

<style scoped>
/* .clone-tip 自 ImageList.vue 原样搬入（类名不变，DOM 不变） */
.clone-tip {
  margin-bottom: var(--space-lg);
}
</style>
