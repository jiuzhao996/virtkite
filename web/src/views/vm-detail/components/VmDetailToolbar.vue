<template>
  <!-- 顶部工具栏：返回 / 名称 / 状态 / IP / 电源与模板操作。全部动作以事件上抛给详情页壳。 -->
  <div class="toolbar">
    <div class="tb-left">
      <el-button text :icon="ArrowLeft" @click="emit('back')">返回</el-button>
      <span class="tb-name">{{ vmName }}</span>
      <el-tag v-if="vm" :type="vmStatusTag(vm.status)" effect="dark" size="small">{{ vmStatusText(vm.status, '—') }}</el-tag>
      <span v-if="vm && vm.ip" class="tb-ip">{{ vm.ip }}</span>
    </div>
    <div class="tb-actions">
      <el-button type="primary" :icon="Monitor" :disabled="!isRunning" :title="isRunning ? '' : '开机后可用控制台'" @click="emit('console')">控制台</el-button>
      <!-- 电源 / 挂起 状态切换按钮：一个按钮按当前状态显示对应动作（运行中→关机/暂停，关机→开机，暂停→恢复）。
           语义保持与拆分版一致：暂停态须先恢复（电源钮禁用），关机态禁用挂起钮；busy 期间锁定防止动作切换闪烁 -->
      <el-button
        v-if="canOperate && vm"
        :icon="isRunning ? SwitchButton : VideoPlay"
        :loading="busy === 'stop' || busy === 'start'"
        :disabled="isPaused || (!!busy && busy !== 'resume')"
        @click="emit('act', isRunning ? 'stop' : 'start')"
      >{{ isRunning ? '关机' : '开机' }}</el-button>
      <el-button
        v-if="canOperate && vm"
        :icon="isPaused ? VideoPlay : VideoPause"
        :loading="busy === 'pause' || busy === 'resume'"
        :disabled="(!isRunning && !isPaused) || (!!busy && busy !== 'stop' && busy !== 'start')"
        @click="emit('act', isPaused ? 'resume' : 'pause')"
      >{{ isPaused ? '恢复' : '暂停' }}</el-button>
      <el-button v-if="canOperate && vm" :icon="RefreshRight" :loading="busy === 'restart'" :disabled="!isRunning" :title="isRunning ? '' : '开机后才能重启'" @click="emit('act', 'restart')">重启</el-button>
      <!-- 模板制作：关机态把本机清洗固化为模板（此后可增量克隆批量建机） -->
      <el-button
        v-if="canOperate && vm && !isRunning"
        :icon="Files"
        :disabled="!finalizeCap.guestfs_available"
        :title="finalizeCap.guestfs_available ? '清洗镜像并固化为模板' : '镜像清洗工具不可用：' + (finalizeCap.reason || '请在宿主机安装 guestfs-tools')"
        @click="emit('finalize')"
      >固化为模板</el-button>
      <el-button v-if="canOperate" type="danger" :icon="Delete" :loading="busy === 'delete'" @click="emit('delete')">删除</el-button>
    </div>
  </div>
</template>

<script setup>
// 虚拟机详情顶部工具栏：纯展示 + 动作事件（返回/控制台/电源/固化模板/删除）。
import { ArrowLeft, Monitor, VideoPlay, VideoPause, SwitchButton, RefreshRight, Delete, Files } from '@element-plus/icons-vue'
import { vmStatusText, vmStatusTag } from '../../../utils/format'

defineProps({
  vmName: { type: String, default: '…' },
  vm: { type: Object, default: null },
  isRunning: { type: Boolean, default: false },
  isPaused: { type: Boolean, default: false },
  canOperate: { type: Boolean, default: false },
  busy: { type: String, default: '' },
  finalizeCap: { type: Object, default: () => ({ guestfs_available: false, reason: '' }) }
})

const emit = defineEmits(['back', 'console', 'act', 'finalize', 'delete'])
</script>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 12px 16px;
  box-shadow: var(--shadow-sm);
  margin-bottom: 16px;
}
.tb-left {
  display: flex;
  align-items: center;
  gap: 10px;
}
.tb-name {
  font-size: 1.1rem;
  font-weight: 700;
  color: var(--color-foreground);
}
.tb-ip {
  color: var(--color-muted-foreground);
  font-family: var(--font-mono);
  font-size: 0.9rem;
}
.tb-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
</style>
