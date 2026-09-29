<template>
  <!-- 处理器 -->
  <section v-show="view === 'cpu'" class="panel">
    <div class="panel-head">
      <h3 class="panel-title">处理器</h3>
    </div>
    <el-card shadow="never" class="edit-card">
      <div class="field-row">
        <span class="field-label">vCPU 数</span>
        <el-input-number v-model="vcpuInput" :min="1" :max="256" controls-position="right" :disabled="!canOperate" />
        <el-button v-if="canOperate" type="primary" :loading="busy === 'vcpu'" :disabled="!spec" @click="applyVcpu">应用</el-button>
      </div>
      <p class="field-tip">热调整：live + config 双生效，运行中即可在线增减 CPU 核数。</p>
    </el-card>
  </section>

  <!-- 内存 -->
  <section v-show="view === 'memory'" class="panel">
    <div class="panel-head">
      <h3 class="panel-title">内存</h3>
    </div>
    <el-card shadow="never" class="edit-card">
      <div class="field-row">
        <span class="field-label">内存大小（MB）</span>
        <el-input-number v-model="memInput" :min="256" :step="256" controls-position="right" :disabled="!canOperate" />
        <el-button v-if="canOperate" type="primary" :loading="busy === 'memory'" :disabled="!spec" @click="applyMemory">应用</el-button>
      </div>
      <p class="field-tip">热调整：需 ≥ 当前占用，运行中可在线调整（live + config）。</p>
    </el-card>
  </section>

  <!-- 磁盘 -->
  <section v-show="view === 'disk'" class="panel">
    <div class="panel-head">
      <h3 class="panel-title">磁盘</h3>
    </div>
    <el-empty v-if="!(spec && spec.disks && spec.disks.length)" description="暂无磁盘设备" :image-size="80" />
    <template v-else>
      <div
        v-for="(disk, i) in spec.disks"
        :key="(disk.target || 'disk') + i"
        class="dev-card"
        :class="{ selected: i === activeDisk }"
        @click="activeDisk = i"
      >
        <div class="dev-card-head">
          <span class="dev-name mono">{{ disk.target || '—' }}</span>
          <el-tag :type="disk.device === 'cdrom' ? 'warning' : 'info'" size="small" effect="light">{{ disk.device }}</el-tag>
          <!-- 移除：打开确认弹窗（可选择是否同时删除存储卷），替代原先的 popconfirm -->
          <el-button v-if="canOperate" size="small" type="danger" text class="dev-remove" :icon="Delete" @click="openRemoveDisk(disk)">移除</el-button>
        </div>
        <el-descriptions :column="2" size="small" class="dev-desc">
          <el-descriptions-item label="目标">{{ disk.target || '—' }}</el-descriptions-item>
          <el-descriptions-item label="总线">{{ disk.bus || '—' }}</el-descriptions-item>
          <el-descriptions-item label="驱动">{{ disk.driver || '—' }}</el-descriptions-item>
          <el-descriptions-item label="只读">{{ disk.read_only ? '是' : '否' }}</el-descriptions-item>
          <el-descriptions-item label="类型">{{ disk.type || '—' }}</el-descriptions-item>
          <el-descriptions-item label="设备">{{ disk.device || '—' }}</el-descriptions-item>
          <el-descriptions-item label="源路径" :span="2">{{ disk.source || '—' }}</el-descriptions-item>
          <el-descriptions-item v-if="disk.backing_file" label="父卷" :span="2">{{ disk.backing_file }}</el-descriptions-item>
        </el-descriptions>
      </div>
    </template>
    <div class="panel-actions">
      <el-button v-if="canOperate" type="primary" plain :icon="MagicStick" :loading="quickDiskLoading" @click="quickAddDisk">一键数据盘（20G）</el-button>
      <el-tooltip placement="top" content="自动检查并补齐两件标准配置：① guest-agent 通信通道——装了 qemu-guest-agent 的虚拟机靠它向平台上报 IP；② virtio-rng 随机数设备——提升虚拟机熵池，加快开机。已存在的会自动跳过，缺什么补什么。">
        <el-button v-if="canOperate" plain :icon="Connection" :loading="standardLoading" @click="ensureStandard">补齐标准设备</el-button>
      </el-tooltip>
      <el-button v-if="canOperate" type="primary" :icon="Plus" @click="openDiskDialog">添加磁盘</el-button>
    </div>
  </section>

  <!-- 网卡 -->
  <section v-show="view === 'nic'" class="panel">
    <div class="panel-head">
      <h3 class="panel-title">网卡</h3>
    </div>
    <el-empty v-if="!(spec && spec.interfaces && spec.interfaces.length)" description="暂无网卡设备" :image-size="80" />
    <template v-else>
      <div
        v-for="(nic, i) in spec.interfaces"
        :key="(nic.mac || 'nic') + i"
        class="dev-card"
        :class="{ selected: i === activeNic }"
        @click="activeNic = i"
      >
        <div class="dev-card-head">
          <span class="dev-name mono">{{ nic.mac || '—' }}</span>
          <el-tag type="info" size="small" effect="light">{{ nic.model }}</el-tag>
          <el-popconfirm v-if="canOperate" :title="'确定移除网卡「' + (nic.mac || '') + '」？'" width="220" @confirm="removeNic(nic)">
            <template #reference>
              <el-button size="small" type="danger" text :icon="Delete">移除</el-button>
            </template>
          </el-popconfirm>
        </div>
        <el-descriptions :column="2" size="small" class="dev-desc">
          <el-descriptions-item label="MAC">{{ nic.mac || '—' }}</el-descriptions-item>
          <el-descriptions-item label="型号">{{ nic.model || '—' }}</el-descriptions-item>
          <el-descriptions-item label="类型">{{ nic.type || '—' }}</el-descriptions-item>
          <el-descriptions-item label="网络">{{ nic.source || '—' }}</el-descriptions-item>
        </el-descriptions>
      </div>
    </template>
    <div class="panel-actions">
      <el-button v-if="canOperate" type="primary" plain :icon="MagicStick" :loading="quickNicLoading" @click="quickAddNic">一键网卡（default）</el-button>
      <el-button v-if="canOperate" type="primary" :icon="Plus" @click="openNicDialog">添加网卡</el-button>
    </div>
  </section>

  <!-- 添加磁盘 / 移除磁盘 / 添加网卡 三个对话框：状态与提交逻辑内聚在子组件，经 ref 打开 -->
  <VmHardwareDialogs ref="dialogs" :vm-id="vmId" :reload="reload" />
</template>

<script setup>
// 硬件四分区（处理器/内存/磁盘/网卡）：热调整、设备卡片、一键/补齐操作。
// 分区显隐用 v-show（view 为壳的 activeView），四区常驻挂载——与拆分前行为一致。
//
// 数据流契约：
// - 壳下发：vmId、spec（loadSpec 结果）、canOperate、view（activeView）、activeDisk/activeNic（菜单联动选中项）、
//   busy（与页头/概览共享同一 busy 字符串，v-model:busy 双向——应用中页头按钮保持禁用，等价拆分前）。
// - 上行：卡片点击经 update:activeDisk / update:activeNic 回写壳（左侧菜单高亮依赖）；
//   一切改动规格成功的操作调 props.reload()（壳 loadSpec），activeDisk/activeNic 越界收敛仍在壳。
// - 对话框（添加/移除磁盘、添加网卡）内聚在 VmHardwareDialogs，本组件经模板 ref 委托打开。
import { ref, computed, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Delete, MagicStick, Connection, Plus } from '@element-plus/icons-vue'
import { api } from '../../../api'
import { taskErrorMessage } from '../../../utils/task.js'
import VmHardwareDialogs from './VmHardwareDialogs.vue'

const props = defineProps({
  vmId: { type: [String, Number], required: true },
  view: { type: String, default: 'cpu' }, // 壳 activeView：cpu / memory / disk / nic
  spec: { type: Object, default: null },
  canOperate: { type: Boolean, default: false },
  reload: { type: Function, required: true }, // 壳 loadSpec：规格变更后刷新
  busy: { type: String, default: '' }, // 与壳共享的操作忙标记（v-model:busy）
  activeDisk: { type: Number, default: 0 }, // 选中磁盘下标（与左侧菜单联动，v-model）
  activeNic: { type: Number, default: 0 } // 选中网卡下标（与左侧菜单联动，v-model）
})

const emit = defineEmits(['update:busy', 'update:activeDisk', 'update:activeNic'])

// v-model 代理：读走 prop，写回发事件（busy 还承担「应用期间页头按钮禁用」的共享语义）
const busy = computed({
  get: () => props.busy,
  set: (v) => emit('update:busy', v)
})
const activeDisk = computed({
  get: () => props.activeDisk,
  set: (v) => emit('update:activeDisk', v)
})
const activeNic = computed({
  get: () => props.activeNic,
  set: (v) => emit('update:activeNic', v)
})

/* ---------- 处理器 / 内存输入 ---------- */
const vcpuInput = ref(1)
const memInput = ref(1024)
watch(
  () => props.spec,
  (s) => {
    if (s) {
      vcpuInput.value = s.vcpu
      memInput.value = s.memory_mb
    }
  },
  { immediate: true }
)

async function applyVcpu() {
  const n = vcpuInput.value
  if (!n || n <= 0) {
    ElMessage.warning('vCPU 数量必须大于 0')
    return
  }
  busy.value = 'vcpu'
  try {
    await api.setVcpu(props.vmId, n)
    ElMessage.success('vCPU 已调整为 ' + n + ' 核（live + config）')
    await props.reload()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '调整失败'))
  } finally {
    busy.value = ''
  }
}

async function applyMemory() {
  const m = memInput.value
  if (!m || m <= 0) {
    ElMessage.warning('内存大小必须大于 0')
    return
  }
  busy.value = 'memory'
  try {
    await api.setMemory(props.vmId, m)
    ElMessage.success('内存已调整为 ' + m + ' MB（live + config）')
    await props.reload()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '调整失败'))
  } finally {
    busy.value = ''
  }
}

/* ---------- 一键添加硬件 ---------- */
const quickDiskLoading = ref(false)
const quickNicLoading = ref(false)
const standardLoading = ref(false)

async function quickAddDisk() {
  quickDiskLoading.value = true
  try {
    const res = await api.quickAttachDisk(props.vmId, { size_gb: 20 })
    const d = res.data || {}
    ElMessage.success('已创建并挂载 20G 数据盘：' + (d.volume || '') + '（' + (d.pool || '') + '）')
    await props.reload()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '一键添加数据盘失败'))
  } finally {
    quickDiskLoading.value = false
  }
}

async function quickAddNic() {
  quickNicLoading.value = true
  try {
    await api.attachInterface(props.vmId, { type: 'network', source: 'default', model: 'virtio' })
    ElMessage.success('已在 default 网络添加 virtio 网卡')
    await props.reload()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '一键添加网卡失败'))
  } finally {
    quickNicLoading.value = false
  }
}

// 补齐标准设备：给存量虚拟机挂 guest-agent 通道与 virtio-rng（新装机已默认携带），幂等
async function ensureStandard() {
  standardLoading.value = true
  try {
    const res = await api.ensureStandardDevices(props.vmId)
    const d = res.data || {}
    if (d.attached && d.attached.length) ElMessage.success(d.message || '已补齐标准设备')
    else ElMessage.info('已是标准配置，无需补齐')
    await props.reload()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '补齐标准设备失败'))
  } finally {
    standardLoading.value = false
  }
}

/* ---------- 网卡移除（卡片级操作，popconfirm 确认） ---------- */
async function removeNic(nic) {
  if (!nic || !nic.mac) return
  try {
    await api.detachInterface(props.vmId, nic.mac)
    ElMessage.success('网卡已移除')
    await props.reload()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '移除网卡失败'))
  }
}

/* ---------- 对话框委托：打开入口留在面板，表单/提交在 VmHardwareDialogs ---------- */
const dialogs = ref(null)
function openDiskDialog() {
  if (dialogs.value) dialogs.value.openDiskDialog()
}
function openRemoveDisk(disk) {
  if (dialogs.value) dialogs.value.openRemoveDisk(disk)
}
function openNicDialog() {
  if (dialogs.value) dialogs.value.openNicDialog()
}
</script>

<style scoped>
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

/* 编辑页 */
.edit-card {
  max-width: 640px;
}
.field-row {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.field-label {
  width: 120px;
  color: var(--color-muted-foreground);
  flex-shrink: 0;
}
.field-tip {
  margin: 12px 0 0;
  color: var(--color-muted-foreground);
  font-size: 0.82rem;
}

/* 磁盘 / 网卡卡片 */
.dev-card {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 12px 16px;
  margin-bottom: 12px;
  background: #fff;
  cursor: pointer;
  transition: all 0.2s ease;
}
.dev-card:hover {
  border-color: var(--color-border-strong);
}
.dev-card.selected {
  border-color: var(--color-primary);
  box-shadow: 0 0 0 1px var(--color-primary);
}
.dev-card-head {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}
.dev-name {
  font-weight: 600;
  font-size: 0.95rem;
  color: var(--color-foreground);
}
.dev-card-head :deep(.el-popconfirm) {
  margin-left: auto;
}
/* 磁盘移除按钮：右对齐（弹窗化后不再有 popconfirm 占位，由按钮自身右推；网卡卡仍走上面的 popconfirm 规则） */
.dev-card-head .dev-remove {
  margin-left: auto;
}
.panel-actions {
  margin-top: 8px;
}
</style>
