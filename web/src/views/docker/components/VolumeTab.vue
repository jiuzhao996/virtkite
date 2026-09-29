<template>
    <div class="pane-toolbar">
      <el-button type="primary" @click="openVolumeDialog">创建卷</el-button>
      <el-button type="danger" plain @click="pruneVolumes">清理未引用卷</el-button>
      <span class="count ct-count">共 {{ volumes.length }} 个卷</span>
    </div>
    <el-table :data="volumes" v-loading="loading" stripe size="small">
      <template #empty><el-empty description="暂无卷" :image-size="80" /></template>
      <el-table-column label="名称" min-width="260" show-overflow-tooltip>
        <template #default="{ row }">
          <span class="mono">{{ row.Name || '—' }}</span>
        </template>
      </el-table-column>
      <el-table-column label="驱动" width="140">
        <template #default="{ row }">{{ row.Driver || '—' }}</template>
      </el-table-column>
      <el-table-column label="Scope" width="140">
        <template #default="{ row }">{{ row.Scope || '—' }}</template>
      </el-table-column>
      <el-table-column label="操作" width="90" fixed="right">
        <template #default="{ row }">
          <el-button size="small" text type="danger" @click="removeVolume(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- 创建卷对话框 -->
    <el-dialog v-model="volumeDialog" title="创建卷" width="460px" :close-on-click-modal="false">
      <el-form ref="volumeFormRef" :model="volumeForm" :rules="volumeRules" label-width="80px">
        <el-form-item label="名称" prop="name">
          <el-input v-model="volumeForm.name" placeholder="如 app-data" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="volumeDialog = false">取消</el-button>
        <el-button type="primary" :loading="volumeSubmitting" @click="submitVolume">创建</el-button>
      </template>
    </el-dialog>
</template>

<script setup>
// 卷 tab：数据 / 取数 / 创建对话框 / 删除与清理自持；惰性加载经 refresh() 由壳调，操作成功后经壳 reloadTab 强制重拉。
import { ref, reactive, nextTick, h } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import http from '../../../api'
import { errMsg, isCancel } from '../../../utils/format'

const props = defineProps({
  loading: { type: Boolean, default: false },
  reloadTab: { type: Function, required: true }
})

const volumes = ref([])

async function fetchVolumes() {
  const res = await http.get('/docker/volumes')
  volumes.value = (res.data.data || {}).items || []
}

// 惰性加载入口（壳 loadTab 调用），错误上抛交壳统一处理
function refresh() {
  return fetchVolumes()
}

defineExpose({ refresh })

// ═══════════════ 卷 ═══════════════

async function removeVolume(row) {
  try {
    await ElMessageBox.confirm(`确定删除卷 ${row.Name}？卷内数据将一并删除。`, '删除卷', {
      type: 'warning',
      confirmButtonText: '删除',
      confirmButtonClass: 'el-button--danger'
    })
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  try {
    await http.delete('/docker/volumes/' + encodeURIComponent(row.Name))
    ElMessage.success(`已删除卷 ${row.Name}`)
    await props.reloadTab('volumes')
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

const volumeDialog = ref(false)
const volumeSubmitting = ref(false)
const volumeFormRef = ref(null)
const volumeForm = reactive({ name: '' })
const volumeRules = {
  name: [
    { required: true, message: '请输入卷名称', trigger: 'blur' },
    { pattern: /^[A-Za-z0-9][A-Za-z0-9_.-]*$/, message: '仅允许字母数字与 -_. 且不以符号开头', trigger: 'blur' }
  ]
}

function openVolumeDialog() {
  volumeForm.name = ''
  volumeDialog.value = true
  nextTick(() => volumeFormRef.value && volumeFormRef.value.clearValidate())
}

async function submitVolume() {
  try {
    await volumeFormRef.value.validate()
  } catch (e) {
    return
  }
  volumeSubmitting.value = true
  try {
    const res = await http.post('/docker/volumes', { name: volumeForm.name.trim() })
    ElMessage.success((res.data.data && res.data.data.message) || '卷已创建')
    volumeDialog.value = false
    await props.reloadTab('volumes')
  } catch (e) {
    ElMessage.error(errMsg(e, '创建卷失败'))
  } finally {
    volumeSubmitting.value = false
  }
}

async function pruneVolumes() {
  try {
    await ElMessageBox.confirm(
      '将删除所有未被任何容器引用的卷，卷内数据将永久丢失且不可恢复！确定清理未引用卷？',
      '清理未引用卷',
      { type: 'error', confirmButtonText: '仍要清理', confirmButtonClass: 'el-button--danger' }
    )
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  try {
    const res = await http.post('/docker/volumes/prune')
    showPruneResult(res.data.data || {})
    await props.reloadTab('volumes')
  } catch (e) {
    ElMessage.error(errMsg(e, '清理失败'))
  }
}
</script>

<style scoped>
/* 各 tab 的工具行：筛选/搜索/批量/主操作 + 计数 */
.pane-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.ct-count {
  margin-left: auto;
}
</style>
