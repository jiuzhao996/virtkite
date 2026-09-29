<template>
  <section v-show="active" class="panel">
    <div class="panel-head">
      <h3 class="panel-title">快照</h3>
      <el-button v-if="canOperate" type="primary" :icon="Plus" @click="openSnapCreate">新建快照</el-button>
    </div>
    <el-card shadow="never">
      <el-table :data="snapshots" size="small" border style="width: 100%" v-loading="snapLoading">
        <template #empty><el-empty description="暂无快照" :image-size="70" /></template>
        <el-table-column prop="name" label="名称" min-width="160" />
        <el-table-column prop="description" label="描述" min-width="200" show-overflow-tooltip />
        <el-table-column label="创建时间" min-width="170">
          <template #default="{ row }">{{ row.timeText }}</template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="row.stateTag" size="small" effect="light">{{ row.stateText }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="180" fixed="right">
          <template #default="{ row }">
            <el-button v-if="canOperate" size="small" :icon="RefreshLeft" @click="revertSnap(row)">回滚</el-button>
            <el-button v-if="canOperate" size="small" type="danger" :icon="Delete" @click="removeSnap(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 新建快照 -->
    <el-dialog :close-on-click-modal="false" v-model="snapDialog" title="新建快照" width="440px">
      <el-form label-width="72px">
        <el-form-item label="名称" required>
          <el-input v-model="snapForm.name" placeholder="如 snap-20260904" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="snapForm.description" type="textarea" :rows="3" placeholder="快照用途说明（可选）" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="snapDialog = false">取消</el-button>
        <el-button type="primary" :loading="snapSaving" @click="submitSnapshot">创建</el-button>
      </template>
    </el-dialog>
  </section>
</template>

<script setup>
// 快照分区：列表 / 创建 / 回滚 / 删除，全部自持。
// - 挂载即拉列表：原壳 onMounted 的 Promise.all([loadSpec, loadSnapshots]) 中快照一路移入本组件
//   自挂载执行，与 loadSpec 仍然并发，请求集合与参数不变。
// - 回滚会改变虚拟机状态：成功后除重拉列表外调 props.reload()（壳 loadSpec）刷新基础信息，
//   与拆分前「loadSnapshots + loadSpec」的先后顺序一致。
// - v-show 常驻（active 为壳 activeView === 'snapshots'），切走再切回列表状态不丢。
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, RefreshLeft, Delete } from '@element-plus/icons-vue'
import { api } from '../../../api'
import { taskErrorMessage } from '../../../utils/task.js'
import { vmStatusText, vmStatusTag, isCancel } from '../../../utils/format'

const props = defineProps({
  vmId: { type: [String, Number], required: true },
  active: { type: Boolean, default: false },
  canOperate: { type: Boolean, default: false },
  reload: { type: Function, required: true } // 壳 loadSpec：回滚后刷新虚拟机状态
})

const snapshots = ref([])
const snapLoading = ref(false)

const snapDialog = ref(false)
const snapForm = reactive({ name: '', description: '' })
const snapSaving = ref(false)

async function loadSnapshots() {
  snapLoading.value = true
  try {
    const res = await api.listSnapshots(props.vmId)
    const raw = (res.data && (res.data.items || res.data)) || []
    snapshots.value = raw.map((s) => ({
      ...s,
      timeText: s.creation_time ? new Date(s.creation_time * 1000).toLocaleString() : '—',
      stateText: vmStatusText(s.state, '—'),
      stateTag: vmStatusTag(s.state)
    }))
  } catch (e) {
    snapshots.value = []
    ElMessage.error(taskErrorMessage(e, '获取快照列表失败'))
  } finally {
    snapLoading.value = false
  }
}

function openSnapCreate() {
  snapForm.name = ''
  snapForm.description = ''
  snapDialog.value = true
}

async function submitSnapshot() {
  if (!snapForm.name.trim()) {
    ElMessage.warning('快照名称不能为空')
    return
  }
  snapSaving.value = true
  try {
    await api.createSnapshot(props.vmId, snapForm.name.trim(), snapForm.description.trim())
    ElMessage.success('快照已创建')
    snapDialog.value = false
    await loadSnapshots()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '创建快照失败'))
  } finally {
    snapSaving.value = false
  }
}

async function revertSnap(snap) {
  try {
    await ElMessageBox.confirm('确定回滚到快照「' + snap.name + '」？此操作会覆盖当前状态。', '确认回滚', { type: 'warning' })
    await api.revertSnapshot(props.vmId, snap.name)
    ElMessage.success('已回滚')
    await loadSnapshots()
    await props.reload()
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(taskErrorMessage(e, '回滚失败'))
  }
}

async function removeSnap(snap) {
  try {
    await ElMessageBox.confirm('确定删除快照「' + snap.name + '」？', '确认删除', { type: 'warning' })
    await api.deleteSnapshot(props.vmId, snap.name)
    ElMessage.success('快照已删除')
    await loadSnapshots()
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(taskErrorMessage(e, '删除失败'))
  }
}

onMounted(loadSnapshots)
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
</style>
