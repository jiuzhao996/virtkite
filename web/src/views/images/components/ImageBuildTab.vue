<template>
  <el-card shadow="never">
    <el-alert
      type="info" :closable="false" show-icon style="margin-bottom: 16px"
      title="流程：① 选一个已下载的 ISO 建「制作机」→ ② 用控制台（VNC）进系统装 OS 并配置 → ③ 关机后点「固化为模板」，平台清洗镜像、注入基础优化与平台公钥、登记为模板。之后即可在创建向导「云镜像」方式下批量克隆。"
    />

    <h4 class="build-h">① 建制作机</h4>
    <el-form label-width="72px" inline>
      <el-form-item label="名称">
        <el-input v-model="buildForm.name" placeholder="如 ubuntu2404-builder" style="width: 190px" />
      </el-form-item>
      <el-form-item label="ISO">
        <el-select v-model="buildForm.isoPath" placeholder="选择已下载的 ISO" filterable style="width: 300px" v-loading="isoLoading">
          <el-option v-for="i in isoItems" :key="i.path" :label="i.name + '（' + i.pool + '）'" :value="i.path" />
        </el-select>
      </el-form-item>
      <el-form-item label="磁盘">
        <el-input-number v-model="buildForm.diskGb" :min="5" :max="500" />
      </el-form-item>
      <el-form-item label="池">
        <el-select v-model="buildForm.pool" style="width: 120px">
          <el-option v-for="p in poolNames" :key="p" :label="p" :value="p" />
        </el-select>
      </el-form-item>
      <el-form-item label="网络">
        <el-select v-model="buildForm.network" style="width: 140px">
          <el-option v-for="n in netNames" :key="n" :label="n" :value="n" />
        </el-select>
      </el-form-item>
      <el-form-item>
        <el-button type="primary" :icon="Plus" :loading="buildBusy" @click="emit('create-builder')">创建制作机</el-button>
      </el-form-item>
    </el-form>
    <div class="build-tip">建好后是关机态：去「虚拟机 → 详情 → 控制台（VNC）」装系统，装完关机再回来固化。</div>

    <h4 class="build-h">② 固化为模板（关机态制作机）</h4>
    <el-table :data="buildVMs" v-loading="buildVMLoading" size="small" stripe style="width: 100%">
      <template #empty><el-empty description="没有关机态的制作机（新建或关机后出现在这里）" :image-size="60" /></template>
      <el-table-column prop="name" label="制作机" min-width="200" />
      <el-table-column label="磁盘 (GB)" width="110">
        <template #default="{ row }">{{ row.disk_gb }}</template>
      </el-table-column>
      <el-table-column label="创建时间" width="180">
        <template #default="{ row }">{{ fmtDateTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="150" fixed="right">
        <template #default="{ row }">
          <el-button text type="primary" size="small" :icon="Files" @click="openFinalizeFor(row)">固化为模板</el-button>
        </template>
      </el-table-column>
    </el-table>
  </el-card>
  <VmFinalizeDialog ref="finalizeRef" v-model="finalizeDlg" :vm-id="finalizeVM?.id || 0" :vm-name="finalizeVM?.name || ''" @done="emit('finalize-done')" />
</template>

<script setup>
// 镜像页「模板制作」tab：ISO → 制作机 → 固化模板 的三步引导。
// 建制作机 emit('create-builder')、固化完成 emit('finalize-done') 由页面执行（建机走
// api.createVM + pollTask，完成后刷新 buildVMs）；本组件自持固化弹窗的开关与目标 VM
// （openFinalizeFor 只操作组件内状态 + 调 VmFinalizeDialog.prepare）。
import { ref } from 'vue'
import { Plus, Files } from '@element-plus/icons-vue'
import { fmtDateTime } from '../../../utils/format'
import VmFinalizeDialog from '../../../components/VmFinalizeDialog.vue'

defineProps({
  // 建制作机表单（页面对象，直接改字段）
  buildForm: { type: Object, required: true },
  buildBusy: { type: Boolean, default: false },
  // 关机态制作机列表
  buildVMs: { type: Array, default: () => [] },
  buildVMLoading: { type: Boolean, default: false },
  // ISO 下拉数据（与「ISO 安装镜像」tab 共用页面 loadIso 的结果）
  isoItems: { type: Array, default: () => [] },
  isoLoading: { type: Boolean, default: false },
  // 池 / 网络候选
  poolNames: { type: Array, default: () => [] },
  netNames: { type: Array, default: () => [] }
})
const emit = defineEmits(['create-builder', 'finalize-done'])

const finalizeDlg = ref(false)
const finalizeRef = ref(null)
const finalizeVM = ref(null)

function openFinalizeFor(row) {
  finalizeVM.value = row
  finalizeRef.value?.prepare()
  finalizeDlg.value = true
}
</script>

<style scoped>
/* 以下规则自 ImageList.vue 原样搬入（类名不变，DOM 不变） */
/* ── 模板制作 tab ── */
.build-h {
  margin: 6px 0 14px;
  font-size: 0.95rem;
  font-weight: 600;
}
.build-tip {
  margin: -4px 0 20px;
  color: var(--color-muted-foreground);
  font-size: 0.82rem;
}
</style>
