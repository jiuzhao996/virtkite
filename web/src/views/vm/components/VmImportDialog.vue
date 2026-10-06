<template>
  <!-- 导入存量 VM（纳管 virsh 已有域）；部分失败时弹窗保持打开并展示失败明细，关闭即清空结果 -->
  <el-dialog v-model="dialogOpen" title="导入存量 VM" width="780px" @close="importResult = null">
    <div v-loading="scanning" class="import-body">
      <el-alert
        v-if="hostName"
        type="info"
        :closable="false"
        show-icon
        :title="`宿主机「${hostName}」共检测到 ${total} 台域：已纳管 ${managed} 台，未纳管 ${unmanagedCount} 台`"
        style="margin-bottom: 12px"
      />
      <!-- 导入结果（仅 failed > 0 时出现）：成功/跳过计数一行 + 失败明细逐行（后端 errors 为「域名: 原因」字符串数组） -->
      <el-alert
        v-if="importResult"
        type="error"
        show-icon
        :closable="false"
        :title="`本次导入：成功 ${importResult.imported} 台，跳过（已纳管） ${importResult.skipped} 台，失败 ${importResult.failed} 台`"
        style="margin-bottom: 12px"
      >
        <div v-if="importResult.errors.length" class="import-error-list">
          <div v-for="(err, i) in importResult.errors" :key="i" class="import-error-item mono">{{ err }}</div>
        </div>
        <div v-else class="import-error-item">后端未返回失败原因明细，可到「任务中心」或后端日志排查</div>
      </el-alert>
      <!-- 扫描失败：弹窗内给出错误与重试入口（不落「暂无未纳管」空态误导用户） -->
      <el-result
        v-if="!scanning && importError"
        icon="warning"
        title="扫描失败"
        :sub-title="importError"
        style="padding: 24px 0"
      >
        <template #extra>
          <el-button type="primary" :icon="Refresh" @click="open">重试扫描</el-button>
        </template>
      </el-result>
      <el-empty v-else-if="!scanning && !unmanaged.length" description="暂无未纳管的存量 VM" />
      <el-table
        v-else
        :data="unmanaged"
        stripe
        border
        size="small"
        style="width: 100%"
        @selection-change="selected = $event"
      >
        <el-table-column type="selection" width="44" />
        <el-table-column prop="name" label="名称" min-width="130" />
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="vmStatusTag(row.state, 'primary')" effect="light">{{ vmStatusText(row.state) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="规格" width="150">
          <template #default="{ row }">{{ row.vcpu }}核 / {{ (row.memory_mb / 1024).toFixed(0) }}GB / {{ row.disk_gb }}GB }}</template>
        </el-table-column>
        <el-table-column prop="mac_address" label="MAC" width="150" />
        <el-table-column prop="disk_path" label="磁盘路径" min-width="220" show-overflow-tooltip />
      </el-table>
    </div>
    <template #footer>
      <el-button @click="dialogOpen = false">取消</el-button>
      <el-button type="primary" :disabled="!selected.length" :loading="importing" @click="doImport">
        导入所选（{{ selected.length }} 台）
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup>
// 导入存量 VM 弹窗（从 VmList 拆出，扫描/导入/失败明细自包含）：
// 父组件通过 ref 调 open() 打开，监听 imported 重载列表、update:scanning 同步轮询守卫。
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { api } from '../../../api'
import { vmStatusText, vmStatusTag, errMsg } from '../../../utils/format'

const emit = defineEmits(['imported', 'update:scanning'])

const dialogOpen = ref(false)
const scanning = ref(false)
const importing = ref(false)
// 扫描失败信息：非空时弹窗内展示错误 + 重试按钮（不误显「暂无未纳管」空态）
const importError = ref('')
const hostId = ref(null)
const hostName = ref('')
const total = ref(0)
const managed = ref(0)
const unmanagedCount = ref(0)
const unmanaged = ref([])
const selected = ref([])
// 导入结果（仅 failed > 0 时填充展示；弹窗关闭/重新打开即清空）
const importResult = ref(null)

function setScanning(v) {
  scanning.value = v
  emit('update:scanning', v)
}

// 打开弹窗并扫描（重试按钮同样走这里：重置结果态后重扫）
async function open() {
  dialogOpen.value = true
  importResult.value = null
  hostName.value = ''
  unmanagedCount.value = 0
  unmanaged.value = []
  selected.value = []
  await scan()
}

// 扫描宿主机存量域（打开弹窗与导入后刷新共用）；
// 错误落弹窗内 importError 态（含重试按钮），不弹 toast 也不误显空态
async function scan() {
  setScanning(true)
  importError.value = ''
  try {
    const res = await api.scanImportVMs()
    const data = (res && res.data) || {}
    hostId.value = data.host_id || null
    hostName.value = data.host_name || ''
    total.value = data.total || 0
    managed.value = data.managed || 0
    unmanagedCount.value = data.unmanaged || 0
    unmanaged.value = ((data.items || []).filter((i) => !i.managed))
  } catch (e) {
    // 后端已统一为 {code, message, data}（handler 层禁止再泄漏 detail），走统一提取
    importError.value = errMsg(e, '扫描失败，无法连接 libvirt')
  } finally {
    setScanning(false)
  }
}

async function doImport() {
  if (!selected.value.length) {
    ElMessage.warning('请先勾选要导入的虚拟机')
    return
  }
  importing.value = true
  try {
    const res = await api.importVMs(hostId.value, selected.value.map((i) => i.name))
    const d = (res && res.data) || {}
    const imported = d.imported || 0
    const skipped = d.skipped || 0
    const failed = d.failed || 0
    ElMessage.success(`导入完成：成功 ${imported} 台${skipped ? '，跳过(已纳管) ' + skipped + ' 台' : ''}${failed ? '，失败 ' + failed + ' 台' : ''}`)
    if (failed > 0) {
      // 部分失败：弹窗保持打开，渲染计数 + 失败明细（errors 为「域名: 原因」中文字符串数组）；
      // 同时静默重扫未纳管列表（导入成功的域从表中消失），不 await 以免拖住按钮 loading
      importResult.value = {
        imported,
        skipped,
        failed,
        errors: Array.isArray(d.errors) ? d.errors : []
      }
      scan()
    } else {
      dialogOpen.value = false
    }
    emit('imported')
  } catch (e) {
    ElMessage.error(errMsg(e, '导入失败'))
  } finally {
    importing.value = false
  }
}

defineExpose({ open })
</script>

<style scoped>
/* 导入失败明细（el-alert 内容区）：逐行「域名: 原因」 */
.import-error-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-top: 4px;
}
.import-error-item {
  font-size: 0.82rem;
  line-height: 1.5;
  word-break: break-all;
}
</style>
