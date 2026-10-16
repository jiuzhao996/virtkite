<template>
  <!-- 执行输出卡：头部（标题 + 状态 + 进度 + 关闭）+ 可选 RECAP 矩阵 + 等宽日志正文 -->
  <el-card shadow="never" class="auto-out-card">
    <template #header>
      <div class="auto-out-head">
        <span class="auto-h">执行输出<span v-if="task.title" class="auto-out-title">——{{ task.title }}</span></span>
        <div class="auto-out-meta">
          <el-tag size="small" effect="plain" :type="statusTag(task.status)">{{ statusLabel(task.status) }}</el-tag>
          <el-progress v-if="isRunning" :percentage="task.progress || 0" :stroke-width="8" style="width: 160px" />
          <el-button text size="small" :icon="Close" @click="emit('close')" />
        </div>
      </div>
    </template>
    <!-- RECAP 矩阵（playbook 执行才有内容） -->
    <el-table v-if="recapRows.length" :data="recapRows" size="small" class="auto-recap">
      <el-table-column prop="host" label="主机" min-width="160">
        <template #default="{ row }"><span class="mono">{{ row.host }}</span></template>
      </el-table-column>
      <el-table-column label="ok" width="80">
        <template #default="{ row }"><el-tag size="small" type="success" effect="plain">{{ row.ok }}</el-tag></template>
      </el-table-column>
      <el-table-column label="changed" width="90">
        <template #default="{ row }"><el-tag size="small" :type="row.changed ? 'warning' : 'info'" effect="plain">{{ row.changed }}</el-tag></template>
      </el-table-column>
      <el-table-column label="failed" width="80">
        <template #default="{ row }"><el-tag size="small" :type="row.failed ? 'danger' : 'info'" effect="plain">{{ row.failed }}</el-tag></template>
      </el-table-column>
      <el-table-column label="unreachable" width="110">
        <template #default="{ row }"><el-tag size="small" :type="row.unreachable ? 'danger' : 'info'" effect="plain">{{ row.unreachable }}</el-tag></template>
      </el-table-column>
    </el-table>
    <pre ref="logBox" class="auto-log mono">{{ logText || '（等待输出…）' }}</pre>
  </el-card>
</template>

<script setup>
// 运维自动化的「执行输出」卡（tab 外共用：adhoc / playbook / 历史回看）：
// 展示正在轮询或回看的 ansible_run 任务结果——RECAP 矩阵（playbook 才有）+ 输出正文。
// 页面在每次拉取后经模板 ref 调 scrollToBottom() 保持日志跟随到底。
import { nextTick, ref } from 'vue'
import { Close } from '@element-plus/icons-vue'

defineProps({
  // 当前展示的任务（runTask）：终态含 result，进行中含 progress
  task: { type: Object, required: true },
  // RECAP 矩阵行 [{ host, ok, changed, failed, unreachable }]（无则空数组）
  recapRows: { type: Array, default: () => [] },
  // 输出正文（已由页面解析：失败=error / 终态 JSON=拼好的文本 / 中间态原文）
  logText: { type: String, default: '' },
  // 是否进行中（决定进度条显隐）
  isRunning: { type: Boolean, default: false },
  // 任务状态 → el-tag 类型 / 中文标签（复用页面映射）
  statusTag: { type: Function, required: true },
  statusLabel: { type: Function, required: true }
})
const emit = defineEmits(['close'])

const logBox = ref(null)
/** 滚动日志到底部（页面在轮询 tick / 切换任务后调用）。 */
function scrollToBottom() {
  nextTick(() => {
    if (logBox.value) logBox.value.scrollTop = logBox.value.scrollHeight
  })
}
defineExpose({ scrollToBottom })
</script>

<style scoped>
/* 以下规则自 Automation.vue 原样搬入（类名不变，DOM 不变） */
.auto-out-card { margin-top: 16px; }
.auto-out-head { display: flex; align-items: center; justify-content: space-between; }
.auto-out-title { font-weight: 400; font-size: 0.82rem; color: var(--color-muted-foreground); }
.auto-out-meta { display: flex; align-items: center; gap: 12px; }
.auto-recap { margin-bottom: 12px; }
.auto-log {
  margin: 0; padding: 12px 14px; max-height: 420px; overflow: auto;
  background: var(--color-code-bg, var(--el-fill-color-darker));
  border-radius: var(--radius-sm); font-size: 0.78rem; line-height: 1.7;
  white-space: pre-wrap; word-break: break-all;
}
/* 与 Automation.vue 的 .auto-h（区块小标题）一致——页面执行引擎卡也用它，故两处各持一份 */
.auto-h { font-weight: 600; font-size: 0.9rem; }
</style>
