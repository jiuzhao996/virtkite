<template>
  <!-- XML 定义分区：双通道编辑提示 + 重新加载/保存 + 原始 XML 文本域。 -->
  <section class="panel">
    <div class="panel-head">
      <h3 class="panel-title">XML 定义</h3>
    </div>
    <el-alert
      type="info"
      :closable="false"
      class="xml-alert"
      title="双通道编辑：左侧各结构化页面为推荐方式；此处可直接编辑原始 XML（getVMXML / updateVMXML）。"
    />
    <el-card shadow="never">
      <div class="xml-toolbar">
        <el-button :icon="Refresh" @click="emit('reload')">重新加载</el-button>
        <el-button v-if="canOperate" type="primary" :loading="xmlSaving" @click="emit('save')">保存</el-button>
      </div>
      <el-input
        :model-value="xmlText"
        type="textarea"
        :rows="18"
        class="xml-area"
        placeholder="加载中…"
        @update:model-value="emit('update:xmlText', $event)"
      />
    </el-card>
  </section>
</template>

<script setup>
// 虚拟机 XML 分区：文本域受控于壳（xmlText），加载/保存仍由壳调用 API。
import { Refresh } from '@element-plus/icons-vue'

defineProps({
  xmlText: { type: String, default: '' },
  xmlSaving: { type: Boolean, default: false },
  canOperate: { type: Boolean, default: false }
})

const emit = defineEmits(['update:xmlText', 'reload', 'save'])
</script>

<style scoped>
/* 面板头与 VmDetail.vue / VmOverviewPanel.vue 各留一份（scoped 无法跨组件命中）；改一处须同步。 */
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
.xml-alert {
  margin-bottom: 12px;
}
.xml-toolbar {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-bottom: 12px;
}
.xml-area :deep(textarea) {
  font-family: var(--font-mono);
  font-size: 0.82rem;
}
</style>
