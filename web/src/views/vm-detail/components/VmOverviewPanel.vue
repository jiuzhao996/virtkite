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
        <el-descriptions-item label="启动顺序">
          <span class="boot-order">{{ bootOrderText }}</span>
          <el-button
            v-if="canOperate && spec"
            text
            type="primary"
            size="small"
            :loading="busy === 'boot'"
            @click="openBootDialog"
          >调整</el-button>
        </el-descriptions-item>
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

    <!-- 启动顺序调整：只影响下次启动，运行中的虚拟机不被踢下线 -->
    <el-dialog v-model="bootDialog" title="调整启动顺序" width="420px" append-to-body>
      <p class="boot-tip">从上到下依次尝试启动。系统安装完成后应把「硬盘」放在第一位，否则会再次从安装 ISO 启动。</p>
      <div v-for="(dev, i) in bootDraft" :key="dev" class="boot-row">
        <span class="boot-idx">{{ i + 1 }}</span>
        <span class="boot-name">{{ bootLabel(dev) }}</span>
        <span class="boot-code mono">{{ dev }}</span>
        <el-button :icon="ArrowUp" circle size="small" :disabled="i === 0" aria-label="上移" @click="moveBoot(i, -1)" />
        <el-button :icon="ArrowDown" circle size="small" :disabled="i === bootDraft.length - 1" aria-label="下移" @click="moveBoot(i, 1)" />
      </div>
      <el-alert
        type="info"
        :closable="false"
        show-icon
        title="修改写入虚拟机持久配置，下次启动生效；当前运行状态不受影响。"
        class="boot-alert"
      />
      <template #footer>
        <el-button @click="bootDialog = false">取消</el-button>
        <el-button type="primary" :loading="busy === 'boot'" :disabled="!bootDirty" @click="saveBootOrder">保存</el-button>
      </template>
    </el-dialog>
  </section>
</template>

<script setup>
// 虚拟机概览分区：展示 spec/vm 派生信息；开机自启与启动顺序只上抛事件，
// API 调用与刷新由壳（VmDetail）统一负责。
import { ref, computed } from 'vue'
import { ArrowUp, ArrowDown } from '@element-plus/icons-vue'
import { vmStatusText, vmStatusTag } from '../../../utils/format'

const props = defineProps({
  vm: { type: Object, default: null },
  spec: { type: Object, default: null },
  hostName: { type: String, default: '—' },
  createdText: { type: String, default: '—' },
  macText: { type: String, default: '—' },
  busy: { type: String, default: '' },
  canOperate: { type: Boolean, default: false }
})

const emit = defineEmits(['autostart-change', 'boot-order-change'])

const BOOT_LABELS = { hd: '硬盘', cdrom: '光驱', network: '网络' }
function bootLabel(dev) {
  return BOOT_LABELS[dev] || dev
}

const bootDevices = () => {
  const ds = props.spec && props.spec.boot && props.spec.boot.devices
  return Array.isArray(ds) && ds.length ? ds.slice() : ['hd']
}
const bootOrderText = computed(() => bootDevices().map(bootLabel).join(' → '))

const bootDialog = ref(false)
const bootDraft = ref([])
const bootOriginal = ref([])

function openBootDialog() {
  bootOriginal.value = bootDevices()
  bootDraft.value = bootDevices()
  bootDialog.value = true
}

function moveBoot(i, delta) {
  const j = i + delta
  if (j < 0 || j >= bootDraft.value.length) return
  const arr = bootDraft.value.slice()
  ;[arr[i], arr[j]] = [arr[j], arr[i]]
  bootDraft.value = arr
}

const bootDirty = computed(() => bootDraft.value.join(',') !== bootOriginal.value.join(','))

function saveBootOrder() {
  bootDialog.value = false
  emit('boot-order-change', bootDraft.value.slice())
}
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
.boot-order {
  margin-right: 8px;
}
.boot-tip {
  margin: 0 0 16px;
  color: var(--color-muted-foreground);
  font-size: 0.85rem;
  line-height: 1.6;
}
.boot-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  margin-bottom: 8px;
  background: var(--color-muted);
  border-radius: var(--radius-md);
}
.boot-idx {
  width: 20px;
  color: var(--color-muted-foreground);
  font-size: 0.85rem;
  text-align: center;
}
.boot-name {
  flex: 1;
  font-size: 0.9rem;
}
.boot-code {
  color: var(--color-muted-foreground);
  font-size: 0.75rem;
}
.boot-alert {
  margin-top: 12px;
}
</style>
