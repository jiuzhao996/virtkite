<template>
  <!-- 固化为模板（模板制作 / 镜像清洗）：VM 详情页与镜像管理页「模板制作」共用 -->
  <el-dialog v-model="open" title="固化为模板" width="540px" :close-on-click-modal="false">
    <el-alert
      type="info" :closable="false" show-icon style="margin-bottom: 14px"
      title="清洗镜像（virt-sysprep：去 SSH 主机密钥 / bash 历史 / machine-id / udev 持久网卡等），并注入平台公钥与基础优化，随后移除本制作机并把系统盘登记为模板。清洗不可逆。"
    />
    <el-form label-width="96px">
      <el-form-item label="模板名称" required>
        <el-input v-model="form.name" placeholder="如 Rocky10-base" />
      </el-form-item>
      <el-form-item label="操作系统">
        <el-input v-model="form.os_version" placeholder="可空；如 Rocky Linux 10（克隆建机时用于自动匹配）" />
      </el-form-item>
      <el-form-item label="描述">
        <el-input v-model="form.description" type="textarea" :rows="2" placeholder="可空" />
      </el-form-item>
      <el-form-item label="自定义脚本">
        <el-input
          v-model="form.custom_script"
          type="textarea"
          :rows="4"
          class="mono"
          placeholder="可空。追加执行的 shell 片段（如自带的 base_config.sh 内容），在内置优化之后运行——用于镜像专属优化"
        />
      </el-form-item>
      <el-form-item label="选项">
        <div class="fin-opts">
          <el-checkbox v-model="form.optimize">注入基础优化（按发行版装 qemu-guest-agent + cloud-init、开串口 console、关 SELinux/firewalld、写彩色 MOTD）+ 平台公钥</el-checkbox>
          <el-checkbox v-model="form.sparsify">压缩磁盘（virt-sparsify --in-place；对 qcow2 收益有限且较慢，按需勾选）</el-checkbox>
        </div>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="open = false">取消</el-button>
      <el-button type="primary" :loading="busy" @click="submit">开始固化</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref, reactive } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '../api'
import { pollTask, extractTaskId, taskErrorMessage } from '../utils/task.js'

const props = defineProps({
  vmId: { type: Number, required: true },
  vmName: { type: String, default: '' }
})
const emit = defineEmits(['done'])

const open = defineModel({ type: Boolean, default: false })
const busy = ref(false)
const form = reactive({ name: '', description: '', os_version: '', custom_script: '', optimize: true, sparsify: false })

// 打开前由父组件调用，预填模板名称
function prepare() {
  Object.assign(form, { name: props.vmName || '', description: '', os_version: '', custom_script: '', optimize: true, sparsify: false })
}
defineExpose({ prepare })

async function submit() {
  if (!form.name.trim()) return ElMessage.warning('请填写模板名称')
  const steps = ['清洗镜像']
  if (form.optimize) steps.push('注入基础优化 + 平台公钥')
  if (form.sparsify) steps.push('压缩磁盘')
  try {
    await ElMessageBox.confirm(
      '将执行：' + steps.join(' → ') + '，随后移除本制作机并把系统盘登记为模板。清洗不可逆，确定继续？',
      '固化为模板',
      { type: 'warning', confirmButtonText: '开始固化', cancelButtonText: '取消' }
    )
  } catch (e) {
    return
  }
  busy.value = true
  try {
    ElMessage.info('固化任务已提交，正在执行（清洗耗时较长，可离开去任务中心查看）…')
    await pollTask(extractTaskId(await api.finalizeVMImage(props.vmId, { ...form })))
    ElMessage.success('已固化为模板，可在镜像管理查看')
    open.value = false
    emit('done')
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '固化失败'))
  } finally {
    busy.value = false
  }
}
</script>

<style scoped>
/* 选项纵向排列（勾选项文案较长，横排会挤） */
.fin-opts {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
</style>
