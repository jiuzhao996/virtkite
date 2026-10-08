<template>
  <div class="step-pane">
    <div class="step-head">
      <h3 class="step-title">确认创建</h3>
      <p class="step-desc">核对配置后点击「创建虚拟机」，可随时返回上一步修改。</p>
    </div>

    <!-- 创建失败常驻展示（可关闭）：toast 会消失，任务失败原因必须留在页面上看 -->
    <el-alert
      v-if="submitError && !submitting"
      type="error"
      show-icon
      closable
      class="creating-bar"
      :title="submitError"
      @close="submitError = ''"
    />

    <el-card v-if="submitting" shadow="never" class="creating-bar">
      <div class="creating-text">{{ submitText }}，请稍候…</div>
      <el-progress :percentage="createProgress" :stroke-width="8" />
      <div class="creating-actions">
        创建任务已在后台执行，可<router-link :to="{ name: 'tasks' }">离开，去任务中心查看</router-link>
      </div>
    </el-card>

    <div class="summary-grid">
      <el-card shadow="never" class="summary-col">
        <template #header><span class="col-title">硬件清单</span></template>
        <div class="hw-list">
          <div class="hw-item"><span class="hw-key">vCPU</span><span class="hw-val">{{ form.vcpu }} 核</span></div>
          <div class="hw-item"><span class="hw-key">内存</span><span class="hw-val">{{ form.memoryMb }} MB（{{ (form.memoryMb / 1024).toFixed(1) }} GB）</span></div>
          <div class="hw-item"><span class="hw-key">引导顺序</span><span class="hw-val">{{ bootDevicesLabel }}</span></div>
          <div class="hw-item"><span class="hw-key">cloud-init</span><span class="hw-val">{{ cloudInitEnabled ? '已启用' : '未启用' }}</span></div>
          <el-divider />
          <div v-for="(d, i) in previewDisks" :key="i" class="hw-item">
            <span class="hw-key">{{ d.target }} · {{ d.device }}</span>
            <span class="hw-val hw-source">{{ d.source }}</span>
          </div>
          <el-divider />
          <div v-for="(n, i) in previewNics" :key="i" class="hw-item">
            <span class="hw-key">网卡 {{ i + 1 }}</span>
            <span class="hw-val">{{ n.model }} · {{ n.source }} · {{ n.mac }}</span>
          </div>
        </div>
      </el-card>

      <el-card shadow="never" class="summary-col">
        <template #header><span class="col-title">配置预览（DomainSpec）</span></template>
        <pre class="xml-preview">{{ domainSpecJson }}</pre>
      </el-card>
    </div>

    <el-card shadow="never" class="summary-bottom">
      <div class="summary-item"><span class="hw-key">虚拟机名称</span><span>{{ form.name || '—' }}</span></div>
      <div class="summary-item"><span class="hw-key">存储池</span><span>{{ form.storagePool || '—' }}</span></div>
      <div class="summary-item"><span class="hw-key">网络</span><span>{{ primaryNet || '—' }}</span></div>
      <div class="summary-item"><span class="hw-key">操作系统</span><span>{{ summaryOs }}</span></div>
      <div class="summary-item"><span class="hw-key">总容量</span><span>{{ totalCapacity }} GB</span></div>
      <!-- 自动初始化：建机后自动开机 → 等 IP → 托管凭据（需 cloud-init 用户名+口令） -->
      <div v-if="cloudInitEnabled" class="summary-item summary-auto">
        <el-checkbox v-model="autoProvision">创建后自动开机并初始化（等 IP、托管凭据）</el-checkbox>
      </div>
    </el-card>
  </div>
</template>

<script setup>
// 向导第 4 步「确认创建」：硬件清单 / DomainSpec 预览 / 创建进度与常驻错误。
// submitError 双向（壳的 submit 写入、此处可关闭清空），其余为只读快照 props。
defineProps({
  form: { type: Object, required: true },
  submitting: { type: Boolean, default: false },
  submitText: { type: String, default: '创建虚拟机' },
  createProgress: { type: Number, default: 0 },
  previewDisks: { type: Array, required: true },
  previewNics: { type: Array, required: true },
  bootDevicesLabel: { type: String, default: '' },
  cloudInitEnabled: { type: Boolean, default: false },
  primaryNet: { type: String, default: '' },
  summaryOs: { type: String, default: '' },
  totalCapacity: { type: Number, default: 0 },
  domainSpecJson: { type: String, default: '' }
})

const submitError = defineModel('submitError', { type: String, default: '' })
// 自动初始化开关（壳持有，此处可切换）；仅 cloud-init 启用时展示
const autoProvision = defineModel('autoProvision', { type: Boolean, default: true })
</script>

<style scoped>
.step-pane {
  padding: var(--space-lg);
}

.step-head {
  margin-bottom: var(--space-2xl);
}

.step-title {
  margin: 0 0 var(--space-sm);
  font-size: 1.05rem;
  font-weight: 600;
}

.step-desc {
  margin: 0;
  color: var(--color-muted-foreground);
  font-size: 0.88rem;
}

.summary-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: var(--space-xl);
}

/* 窄屏汇总双栏回落单列，XML 预览不再被挤压成窄条 */
@media (max-width: 1100px) {
  .summary-grid {
    grid-template-columns: 1fr;
  }
}

.summary-col {
  min-width: 0;
}

.summary-col :deep(.el-card__header) {
  padding: var(--space-lg) var(--space-xl);
}

.summary-col :deep(.el-card__body) {
  padding: var(--space-lg) var(--space-xl);
}

.col-title {
  font-weight: 600;
  color: var(--color-foreground);
}

.hw-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-lg);
}

.hw-item {
  display: flex;
  align-items: baseline;
  gap: var(--space-lg);
}

.hw-key {
  flex-shrink: 0;
  width: 120px;
  color: var(--color-muted-foreground);
  font-size: 0.85rem;
}

.hw-val {
  color: var(--color-foreground);
  font-size: 0.88rem;
  word-break: break-all;
}

.hw-source {
  font-family: var(--font-mono);
  font-size: 0.8rem;
}

.xml-preview {
  margin: 0;
  padding: var(--space-xl);
  background: #0f172a;
  color: #d8e2ef;
  border-radius: var(--radius-md);
  font-family: var(--font-mono);
  font-size: 0.78rem;
  line-height: 1.55;
  max-height: 460px;
  overflow: auto;
  white-space: pre;
}

.summary-bottom {
  margin-top: var(--space-xl);
}

.creating-bar {
  margin-bottom: var(--space-xl);
}

.creating-text {
  margin-bottom: var(--space-lg);
  font-weight: 600;
  color: var(--color-foreground);
}
.summary-bottom :deep(.el-card__body) {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2xl) var(--space-3xl);
  padding: var(--space-lg) var(--space-xl);
}

.summary-item {
  display: flex;
  align-items: baseline;
  gap: var(--space-lg);
}

.summary-item .hw-key {
  width: auto;
}
</style>
