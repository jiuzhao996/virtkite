<template>
  <!-- 域定义健康检查横幅（幽灵盘）：磁盘文件缺失时置顶告警并提供摘除入口。 -->
  <el-alert
    type="error"
    :closable="false"
    show-icon
    class="health-alert"
  >
    <template #title>
      域定义异常：{{ health.missing_disks.filter((d) => d.fatal).length }} 块磁盘的文件不存在，虚拟机无法开机
    </template>
    <div v-for="d in health.missing_disks" :key="d.target" class="health-row">
      <span class="mono health-src">{{ d.target }} → {{ d.source }}</span>
      <span v-if="!d.fatal" class="health-note">光驱介质缺失，不影响开机</span>
      <el-button
        size="small" type="danger" plain
        :loading="busy === 'detach-' + d.target"
        @click="emit('detach', d)"
      >摘除该磁盘</el-button>
    </div>
    <div class="health-tip">
      摘除只从域定义移除该磁盘条目，不动任何文件；若文件仍需要，可先补齐文件再开机。
    </div>
  </el-alert>
</template>

<script setup>
// 域定义健康告警：展示缺失磁盘并上抛「摘除」意图；确认框与 API 调用留在详情页壳。
defineProps({
  health: { type: Object, required: true },
  busy: { type: String, default: '' }
})

const emit = defineEmits(['detach'])
</script>

<style scoped>
.health-alert {
  margin: 8px 0 0;
}
.health-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-top: 6px;
}
.health-src {
  font-size: 0.8rem;
  word-break: break-all;
}
.health-note {
  font-size: 0.78rem;
  color: var(--el-color-info);
}
.health-tip {
  margin-top: 8px;
  font-size: 0.78rem;
  opacity: 0.85;
}
</style>
