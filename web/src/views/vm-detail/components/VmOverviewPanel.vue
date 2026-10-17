<template>
  <!-- 概览分区：腾讯云式信息行（无框线、label 灰色固定宽，一行一条信息）。 -->
  <section class="panel">
    <div class="panel-head">
      <h3 class="panel-title">概览</h3>
    </div>
    <el-card shadow="never">
      <el-descriptions :column="2" class="ov-desc">
        <el-descriptions-item label="名称">{{ spec ? spec.name : '—' }}</el-descriptions-item>
        <el-descriptions-item label="UUID">{{ spec ? spec.uuid : '—' }}</el-descriptions-item>
        <el-descriptions-item label="状态">
          <el-tag :type="vmStatusTag(vm ? vm.status : '')" effect="light">{{ vmStatusText(vm ? vm.status : '', '—') }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="宿主机">{{ hostName }}</el-descriptions-item>
        <el-descriptions-item label="存储池">{{ vm ? vm.storage_pool || '—' : '—' }}</el-descriptions-item>
        <el-descriptions-item label="IP">{{ vm ? vm.ip || '—' : '—' }}</el-descriptions-item>
        <el-descriptions-item label="vCPU">{{ spec ? spec.vcpu + ' 核' : '—' }}</el-descriptions-item>
        <el-descriptions-item label="内存">{{ spec ? spec.memory_mb + ' MB' : '—' }}</el-descriptions-item>
        <el-descriptions-item label="系统类型">{{ spec ? spec.os_type : (vm && vm.os_type) || '—' }}</el-descriptions-item>
        <el-descriptions-item label="架构">{{ spec ? spec.arch : '—' }}</el-descriptions-item>
        <el-descriptions-item label="机器类型">{{ spec ? spec.machine : '—' }}</el-descriptions-item>
        <el-descriptions-item label="创建时间">{{ createdText }}</el-descriptions-item>
        <el-descriptions-item label="MAC 地址">{{ macText }}</el-descriptions-item>
        <el-descriptions-item label="开机自启">
          <el-switch
            :model-value="!!(spec && spec.autostart)"
            :loading="busy === 'autostart'"
            :disabled="!spec || !canOperate"
            @change="emit('autostart-change', $event)"
          />
        </el-descriptions-item>
      </el-descriptions>
    </el-card>
  </section>
</template>

<script setup>
// 虚拟机概览分区：纯展示 spec/vm 派生信息；开机自启开关只上抛 change 事件。
import { vmStatusText, vmStatusTag } from '../../../utils/format'

defineProps({
  vm: { type: Object, default: null },
  spec: { type: Object, default: null },
  hostName: { type: String, default: '—' },
  createdText: { type: String, default: '—' },
  macText: { type: String, default: '—' },
  busy: { type: String, default: '' },
  canOperate: { type: Boolean, default: false }
})

const emit = defineEmits(['autostart-change'])
</script>

<style scoped>
/* 面板头与 VmDetail.vue / VmXmlPanel.vue 各留一份（scoped 无法跨组件命中）；改一处须同步。 */
.panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
}
.panel-title {
  margin: 0;
  font-size: 1rem;
  font-weight: 600;
  color: var(--color-foreground);
}
/* 概览信息行（腾讯云式）：label 灰色固定宽，值区留足行距 */
.ov-desc :deep(.el-descriptions__label) {
  color: var(--el-text-color-secondary);
  min-width: 78px;
}
.ov-desc :deep(.el-descriptions__cell) {
  padding-bottom: 16px;
  vertical-align: middle;
}
</style>
