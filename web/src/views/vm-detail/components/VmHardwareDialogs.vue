<template>
  <!-- 添加磁盘 -->
  <el-dialog :close-on-click-modal="false" v-model="diskDialog" title="添加磁盘" width="460px">
    <el-form ref="diskFormRef" :model="diskForm" :rules="diskRules" label-width="96px">
      <el-form-item label="设备类型" prop="device">
        <el-select v-model="diskForm.device" style="width: 100%">
          <el-option label="磁盘 (disk)" value="disk" />
          <el-option label="光盘 (cdrom)" value="cdrom" />
        </el-select>
      </el-form-item>
      <el-form-item label="总线" prop="bus">
        <el-select v-model="diskForm.bus" style="width: 100%">
          <el-option v-for="b in ['virtio', 'ide', 'sata', 'scsi']" :key="b" :label="b" :value="b" />
        </el-select>
      </el-form-item>
      <el-form-item label="驱动" prop="driver">
        <el-select v-model="diskForm.driver" style="width: 100%">
          <el-option v-for="d in ['qcow2', 'raw', 'iso']" :key="d" :label="d" :value="d" />
        </el-select>
      </el-form-item>
      <el-form-item label="从池选择">
        <!-- 卷选择器（幽灵盘防线）：手填路径极易写出不存在的文件——关机域写 XML 时
             libvirt 不校验，直到开机才炸且报错与加盘操作脱节。选卷时自动填路径 -->
        <el-select
          v-model="pickedVolume"
          filterable clearable
          placeholder="选择存储池中的已有卷（自动填下方路径）"
          style="width: 100%"
          :loading="volumeLoading"
          @change="onPickVolume"
        >
          <el-option
            v-for="v in volumeOptions" :key="v.path"
            :label="`${v.pool}/${v.name}（${fmtCap(v.capacity_gb)}）`" :value="v.path"
          />
        </el-select>
      </el-form-item>
      <el-form-item label="源路径" prop="source">
        <el-input v-model="diskForm.source" placeholder="/var/lib/libvirt/images/xxx.qcow2" />
      </el-form-item>
      <el-form-item label="只读">
        <el-switch v-model="diskForm.read_only" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="diskDialog = false">取消</el-button>
      <el-button type="primary" :loading="diskSaving" @click="submitDisk">添加</el-button>
    </template>
  </el-dialog>

  <!-- 移除磁盘确认弹窗：默认仅分离保留卷；「分离并删除存储卷」为危险项，cdrom（ISO 介质）不提供删卷 -->
  <el-dialog v-model="removeDiskDialog" title="移除磁盘" width="480px">
    <p class="rm-disk-tip">
      磁盘「<span class="mono">{{ (removeDiskForm.disk && removeDiskForm.disk.target) || '—' }}</span>」将从此虚拟机移除（运行中为热分离，关机状态为改配置），请选择存储卷的处理方式：
    </p>
    <el-radio-group v-model="removeDiskForm.deleteVolume" class="rm-disk-options">
      <el-radio :value="false" class="rm-opt">
        <span class="rm-opt-text">
          <span class="rm-opt-title">仅分离（保留存储卷）</span>
          <span class="rm-opt-desc">只把磁盘从虚拟机配置中卸载，存储池中的卷文件原样保留，可再次挂载。</span>
        </span>
      </el-radio>
      <el-radio :value="true" class="rm-opt" :disabled="removeDiskIsCdrom">
        <span class="rm-opt-text">
          <span class="rm-opt-title is-danger">分离并删除存储卷</span>
          <span class="rm-opt-desc is-danger">
            <el-icon class="rm-opt-icon"><WarningFilled /></el-icon>从存储池中删除该卷文件，不可恢复。
          </span>
          <span v-if="removeDiskIsCdrom" class="rm-opt-desc is-hint">ISO 安装介质为共享文件，不随分离删除。</span>
        </span>
      </el-radio>
    </el-radio-group>
    <template #footer>
      <el-button @click="removeDiskDialog = false">取消</el-button>
      <el-button type="danger" :loading="removeDiskSaving" :disabled="!removeDiskForm.disk || !removeDiskForm.disk.target" @click="confirmRemoveDisk">确认移除</el-button>
    </template>
  </el-dialog>

  <!-- 添加网卡 -->
  <el-dialog v-model="nicDialog" title="添加网卡" width="460px">
    <el-form ref="nicFormRef" :model="nicForm" :rules="nicRules" label-width="96px">
      <el-form-item label="网络" prop="source">
        <el-select v-model="nicForm.source" filterable allow-create default-first-option placeholder="选择或输入网络名" style="width: 100%">
          <el-option v-for="n in networks" :key="n" :label="n" :value="n" />
        </el-select>
      </el-form-item>
      <el-form-item label="型号" prop="model">
        <el-select v-model="nicForm.model" style="width: 100%">
          <el-option v-for="m in ['virtio', 'e1000', 'rtl8139']" :key="m" :label="m" :value="m" />
        </el-select>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="nicDialog = false">取消</el-button>
      <el-button type="primary" :loading="nicSaving" @click="submitNic">添加</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
// 硬件对话框集：添加磁盘 / 移除磁盘（含存储卷处理单选）/ 添加网卡。
// 面板（VmHardwarePanels）经模板 ref 调 openDiskDialog / openRemoveDisk / openNicDialog 同名入口打开；
// 提交/移除成功后调 props.reload()（壳 loadSpec）刷新规格——语义与拆分前逐行一致。
// 表单状态（diskForm/nicForm/removeDiskForm）与校验规则、networks 候选拉取均内聚在本组件。
import { ref, reactive, computed } from 'vue'
import { ElMessage } from 'element-plus'
import { WarningFilled } from '@element-plus/icons-vue'
import { api } from '../../../api'
import { taskErrorMessage } from '../../../utils/task.js'

const props = defineProps({
  vmId: { type: [String, Number], required: true },
  reload: { type: Function, required: true }
})

/* ---------- 添加磁盘 ---------- */
const diskDialog = ref(false)
const diskFormRef = ref(null)
const diskForm = reactive({ device: 'disk', bus: 'virtio', driver: 'qcow2', source: '', read_only: false })
const diskSaving = ref(false)

// 存储池卷清单（卷选择器数据源）：取 volume-graph 的去幻影节点——它已聚合全部
// 激活池的卷（含路径与容量），无需再逐池调用
const pickedVolume = ref('')
const volumeOptions = ref([])
const volumeLoading = ref(false)
async function loadVolumeOptions() {
  volumeLoading.value = true
  try {
    const res = await api.volumeGraph()
    const nodes = (res.data && res.data.nodes) || []
    volumeOptions.value = nodes
      .filter((n) => !n.phantom && n.path)
      .map((n) => ({ path: n.path, name: n.name, pool: n.pool, capacity_gb: n.capacity_gb }))
  } catch (e) {
    volumeOptions.value = []
  } finally {
    volumeLoading.value = false
  }
}
function onPickVolume(path) {
  if (path) diskForm.source = path
}
function fmtCap(gb) {
  const n = Number(gb) || 0
  return n >= 100 ? Math.round(n) + ' GB' : (n >= 1 ? n.toFixed(1) + ' GB' : Math.round(n * 1024) + ' MB')
}

const diskRules = {
  source: [{ required: true, message: '请输入磁盘路径', trigger: 'blur' }]
}

function openDiskDialog() {
  diskForm.device = 'disk'
  diskForm.bus = 'virtio'
  diskForm.driver = 'qcow2'
  diskForm.source = ''
  diskForm.read_only = false
  pickedVolume.value = ''
  loadVolumeOptions()
  if (diskFormRef.value) diskFormRef.value.clearValidate()
  diskDialog.value = true
}

async function submitDisk() {
  if (!diskFormRef.value) return
  try {
    await diskFormRef.value.validate()
  } catch (_) {
    return
  }
  diskSaving.value = true
  try {
    const disk = {
      device: diskForm.device,
      bus: diskForm.bus,
      driver: diskForm.driver,
      source: diskForm.source.trim(),
      read_only: diskForm.read_only
    }
    await api.attachDisk(props.vmId, disk)
    ElMessage.success('磁盘已添加')
    diskDialog.value = false
    await props.reload()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '添加磁盘失败'))
  } finally {
    diskSaving.value = false
  }
}

/* ---------- 移除磁盘 ---------- */
// 移除磁盘弹窗：默认安全项「仅分离（保留存储卷）」；「分离并删除存储卷」为危险项，cdrom（ISO 介质）禁用
const removeDiskDialog = ref(false)
const removeDiskForm = reactive({ disk: null, deleteVolume: false })
const removeDiskSaving = ref(false)
// cdrom 为共享安装介质（ISO 文件可被多台虚拟机引用），不允许随分离删除
const removeDiskIsCdrom = computed(() => !!(removeDiskForm.disk && removeDiskForm.disk.device === 'cdrom'))

function openRemoveDisk(disk) {
  removeDiskForm.disk = disk
  removeDiskForm.deleteVolume = false // 每次打开都重置回默认项，避免上一次的选择残留
  removeDiskDialog.value = true
}

// 确认移除：按单选结果附带 delete_volume 传给分离接口；后端契约 { vm, target, volume_deleted, keep_reason }
async function confirmRemoveDisk() {
  const disk = removeDiskForm.disk
  if (!disk || !disk.target) return
  removeDiskSaving.value = true
  try {
    // cdrom 的删卷选项已被禁用，这里再兜底一次，防止状态残留误传 true
    const res = await api.detachDisk(props.vmId, disk.target, removeDiskForm.deleteVolume && !removeDiskIsCdrom.value)
    const d = (res && res.data) || {}
    if (d.volume_deleted) ElMessage.success('已分离并删除卷')
    else if (d.keep_reason) ElMessage.success('已分离，卷保留：' + d.keep_reason)
    else ElMessage.success('已分离，卷已保留')
    removeDiskDialog.value = false
    await props.reload()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '移除磁盘失败'))
  } finally {
    removeDiskSaving.value = false
  }
}

/* ---------- 添加网卡 ---------- */
const nicDialog = ref(false)
const nicFormRef = ref(null)
const nicForm = reactive({ source: '', model: 'virtio' })
const nicSaving = ref(false)
const networks = ref([])

const nicRules = {
  source: [{ required: true, message: '请输入或选择网络名', trigger: 'blur' }]
}

async function openNicDialog() {
  nicForm.source = ''
  nicForm.model = 'virtio'
  if (nicFormRef.value) nicFormRef.value.clearValidate()
  if (!networks.value.length) {
    try {
      const res = await api.vmOptions()
      networks.value = (res.data && res.data.networks) || []
    } catch (e) {
      ElMessage.warning('网络列表获取失败，可手动输入网络名')
    }
  }
  nicDialog.value = true
}

async function submitNic() {
  if (!nicFormRef.value) return
  try {
    await nicFormRef.value.validate()
  } catch (_) {
    return
  }
  nicSaving.value = true
  try {
    const iface = { type: 'network', source: nicForm.source.trim(), model: nicForm.model }
    await api.attachInterface(props.vmId, iface)
    ElMessage.success('网卡已添加')
    nicDialog.value = false
    await props.reload()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '添加网卡失败'))
  } finally {
    nicSaving.value = false
  }
}

defineExpose({ openDiskDialog, openRemoveDisk, openNicDialog })
</script>

<style scoped>
/* 移除磁盘弹窗：单选选项做成两张带边框的说明卡，标题 + 辅助描述分层 */
.rm-disk-tip {
  margin: 0 0 12px;
  line-height: 1.7;
  color: var(--color-foreground);
}
.rm-disk-options {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 8px;
  width: 100%;
}
.rm-opt {
  height: auto;
  align-items: flex-start;
  margin-right: 0;
  padding: 10px 12px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
}
.rm-opt :deep(.el-radio__label) {
  padding-left: 8px;
  white-space: normal;
  line-height: 1.5;
}
.rm-opt-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.rm-opt-title {
  font-weight: 600;
  color: var(--color-foreground);
}
.rm-opt-desc {
  font-size: 0.82rem;
  color: var(--color-muted-foreground);
}
.rm-opt-title.is-danger,
.rm-opt-desc.is-danger {
  color: var(--el-color-danger);
}
.rm-opt-desc.is-hint {
  color: var(--el-color-warning);
}
.rm-opt-icon {
  vertical-align: -2px;
  margin-right: 2px;
}
</style>
