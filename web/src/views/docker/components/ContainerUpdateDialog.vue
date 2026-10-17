<template>
  <!-- 调整资源 / 健康检查（docker update，运行中亦可；留空项不动原值） -->
  <el-dialog v-model="visible" title="调整资源与健康检查" width="480px" :close-on-click-modal="false" append-to-body>
    <el-form label-width="110px">
      <el-form-item label="CPU 上限"><el-input v-model="form.cpus" placeholder="如 1.5（核数）" clearable /></el-form-item>
      <el-form-item label="内存上限"><el-input v-model="form.memory" placeholder="如 512m / 1g" clearable /></el-form-item>
      <el-form-item label="内存+Swap"><el-input v-model="form.memory_swap" placeholder="如 1g；-1 表示不限 swap" clearable /></el-form-item>
      <el-form-item label="CPU 集"><el-input v-model="form.cpuset_cpus" placeholder="如 0-2 或 0,2" clearable /></el-form-item>
      <el-form-item label="检测间隔"><el-input v-model="form.health_interval" placeholder="如 30s" clearable /></el-form-item>
      <el-form-item label="失败次数"><el-input v-model="form.health_retries" placeholder="如 3" clearable /></el-form-item>
      <el-form-item label="检测超时"><el-input v-model="form.health_timeout" placeholder="如 5s" clearable /></el-form-item>
      <el-form-item label="启动宽限"><el-input v-model="form.health_start_period" placeholder="如 60s（启动慢的应用必需）" clearable /></el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="busy" @click="submit">更新</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
// 容器资源/健康检查调整弹窗：自持表单与提交；成功后 emit('updated')，
// 由抽屉壳负责刷新 inspect 并通知父列表（保持原来的刷新时序）。
import { ref, reactive } from 'vue'
import { ElMessage } from 'element-plus'
import { api } from '../../../api'
import { errMsg } from '../../../utils/format'

const props = defineProps({
  containerId: { type: [String, Number], default: '' }
})

const emit = defineEmits(['updated'])

const visible = ref(false)
const busy = ref(false)
const form = reactive({
  cpus: '', memory: '', memory_swap: '', cpuset_cpus: '',
  health_interval: '', health_retries: '', health_timeout: '', health_start_period: ''
})

function open() {
  Object.assign(form, {
    cpus: '', memory: '', memory_swap: '', cpuset_cpus: '',
    health_interval: '', health_retries: '', health_timeout: '', health_start_period: ''
  })
  visible.value = true
}

async function submit() {
  busy.value = true
  try {
    await api.dockerContainerUpdate(props.containerId, { ...form })
    ElMessage.success('已更新容器资源 / 健康检查')
    visible.value = false
    emit('updated')
  } catch (e) {
    ElMessage.error(errMsg(e, '更新失败'))
  } finally {
    busy.value = false
  }
}

defineExpose({ open })
</script>
