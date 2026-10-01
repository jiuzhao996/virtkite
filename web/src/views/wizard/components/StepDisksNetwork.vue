<template>
  <div class="step-pane">
    <div class="step-head">
      <h3 class="step-title">磁盘与网络</h3>
      <p class="step-desc">配置磁盘设备与网络接口。</p>
    </div>

    <template v-if="installMode !== 'clone'">
      <div class="section-bar">
        <h4 class="section-title">磁盘设备</h4>
        <el-button size="small" type="primary" plain :icon="Plus" @click="openDiskDialog">添加磁盘</el-button>
      </div>
      <el-table :data="diskRows" stripe border size="small" style="width: 100%">
        <el-table-column label="设备" width="110">
          <template #default="{ row }">
            <el-tag v-if="row.isSystem" type="primary" effect="plain">系统盘</el-tag>
            <el-tag v-else type="info" effect="plain">数据盘</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="类型" width="110">
          <template #default="{ row }">
            <el-tag size="small">{{ kindLabel(row.kind) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="来源" min-width="220" show-overflow-tooltip>
          <template #default="{ row }">{{ diskSourceLabel(row) }}</template>
        </el-table-column>
        <el-table-column label="容量" width="110">
          <template #default="{ row }">{{ diskCapacityLabel(row) }}</template>
        </el-table-column>
        <el-table-column v-if="diskRows.length > 1" label="操作" width="90" align="center">
          <template #default="{ row }">
            <el-button v-if="!row.isSystem" size="small" type="danger" text :icon="Delete" title="移除该磁盘" aria-label="移除该磁盘" @click="removeDisk(row)" />
          </template>
        </el-table-column>
      </el-table>
    </template>
    <el-alert
      v-else
      type="info"
      :closable="false"
      show-icon
      title="克隆方式下系统磁盘继承自源虚拟机，不支持修改磁盘。"
      style="margin-bottom: var(--space-xl)"
    />

    <el-divider />

    <div class="section-bar">
      <h4 class="section-title">网络接口</h4>
      <el-button size="small" type="primary" plain :icon="Plus" @click="addNic">添加第二网卡</el-button>
    </div>
    <div v-for="(nic, idx) in nics" :key="nic.id" class="nic-row">
      <span class="nic-index">网卡 {{ idx + 1 }}</span>
      <el-select v-model="nic.source" placeholder="选择网络" style="width: 260px">
        <!-- 按 libvirt 转发类型分组：NAT / 桥接 / 隔离，附网关提示 -->
        <el-option-group v-for="g in networkGroups" :key="g.label" :label="g.label">
          <el-option
            v-for="n in g.items"
            :key="n.name"
            :label="n.gateway ? n.name + '（网关 ' + n.gateway + '）' : n.name"
            :value="n.name"
          />
        </el-option-group>
      </el-select>
      <span class="os-hint">{{ nicModel }} 模型</span>
      <el-button v-if="nics.length > 1" size="small" type="danger" text :icon="Delete" title="移除该网卡" aria-label="移除该网卡" @click="removeNic(idx)" />
    </div>

    <el-dialog :close-on-click-modal="false" v-model="diskDialog" title="添加磁盘" width="460px">
      <el-form label-width="90px">
        <el-form-item label="磁盘类型">
          <el-radio-group v-model="diskForm.kind">
            <el-radio value="create">新建空白盘</el-radio>
            <el-radio value="source">引用现有路径</el-radio>
            <el-radio value="image">引用云镜像</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="diskForm.kind === 'create'" label="卷名">
          <el-input v-model="diskForm.volName" placeholder="可选，留空自动命名（虚拟机名-dN）" clearable />
        </el-form-item>
        <el-form-item v-if="diskForm.kind === 'create'" label="容量 (GB)" required>
          <el-input-number v-model="diskForm.createGb" :min="1" :max="500" controls-position="right" />
        </el-form-item>
        <el-form-item v-if="diskForm.kind === 'source'" label="磁盘路径" required>
          <el-input v-model="diskForm.source" placeholder="如 /var/lib/libvirt/images/data.qcow2" style="width: 100%" />
        </el-form-item>
        <el-form-item v-if="diskForm.kind === 'image'" label="云镜像" required>
          <el-select v-model="diskForm.imageId" filterable placeholder="选择云镜像" style="width: 100%">
            <el-option v-for="img in cloudImageList" :key="img.id" :label="img.name" :value="img.id" />
            <template #empty><span class="opt-hint">镜像库暂无云镜像</span></template>
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="diskDialog = false">取消</el-button>
        <el-button type="primary" @click="confirmDisk">确定</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
// 向导第 3 步「磁盘与网络」：磁盘表（含添加磁盘弹窗，本组件自持弹窗状态）与多网卡编辑。
// nics/extraDisks 由壳持有（草稿持久化与提交装配都依赖），props 传入后成员变更/push 写回共享实例。
import { computed, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Plus, Delete } from '@element-plus/icons-vue'

const props = defineProps({
  installMode: { type: String, required: true },
  form: { type: Object, required: true },
  nics: { type: Array, required: true },
  extraDisks: { type: Array, required: true },
  options: { type: Object, required: true },
  diskRows: { type: Array, required: true },
  nicModel: { type: String, default: 'virtio' }
})

// ── 网络下拉按 libvirt 转发类型分组 ──
const networkGroups = computed(() => {
  const infos = props.options.networkInfo || []
  if (!infos.length) return [{ label: '可用网络', items: (props.options.networks || []).map((n) => ({ name: n })) }]
  const gLabel = { nat: 'NAT 网络', bridge: '桥接网络', isolated: '隔离网络' }
  const groups = {}
  for (const n of infos) {
    const key = gLabel[n.forward] || '隔离/其它'
    ;(groups[key] = groups[key] || []).push(n)
  }
  return Object.keys(groups).map((label) => ({ label, items: groups[label] }))
})

// 云镜像列表：镜像库里非 ISO 的登记卷（添加磁盘弹窗的候选）
const cloudImageList = computed(() =>
  (props.options.cloudImages || []).filter((i) => (i.format || '').toLowerCase() !== 'iso')
)

function findImage(id) {
  return props.options.cloudImages.find((i) => i.id === id) || null
}

function addNic() {
  props.nics.push({ id: Date.now(), source: props.options.networks.length ? props.options.networks[0] : '' })
}

function removeNic(idx) {
  if (props.nics.length > 1) props.nics.splice(idx, 1)
}

// ── 添加磁盘弹窗（本步自持状态；确认后 push 进共享的 extraDisks） ──
const diskDialog = ref(false)
const diskForm = reactive({ kind: 'create', createGb: 20, source: '', imageId: null, volName: '' })

function openDiskDialog() {
  Object.assign(diskForm, { kind: 'create', createGb: 20, source: '', imageId: null })
  diskDialog.value = true
}

function confirmDisk() {
  if (diskForm.kind === 'create' && (!diskForm.createGb || diskForm.createGb < 1)) {
    ElMessage.warning('请填写磁盘容量')
    return
  }
  if (diskForm.kind === 'source' && !diskForm.source) {
    ElMessage.warning('请填写磁盘路径')
    return
  }
  if (diskForm.kind === 'image' && !diskForm.imageId) {
    ElMessage.warning('请选择云镜像')
    return
  }
  const row = { id: Date.now(), isSystem: false }
  if (diskForm.kind === 'create') {
    Object.assign(row, { kind: 'create', createGb: diskForm.createGb, volName: diskForm.volName.trim() })
  } else if (diskForm.kind === 'source') {
    Object.assign(row, { kind: 'source', source: diskForm.source })
  } else {
    const img = findImage(diskForm.imageId)
    Object.assign(row, { kind: 'image', imageId: diskForm.imageId, imageName: img ? img.name : '', sizeGb: img ? img.size_gb : 0 })
  }
  props.extraDisks.push(row)
  diskDialog.value = false
}

function removeDisk(row) {
  const idx = props.extraDisks.findIndex((d) => d.id === row.id)
  if (idx > -1) props.extraDisks.splice(idx, 1)
}

// ── 磁盘表展示标签 ──
function kindLabel(k) {
  return { create: '新建卷', source: '引用路径', image: '云镜像' }[k] || k
}

function diskSourceLabel(d) {
  if (d.kind === 'create') return '新建空白卷（' + d.createGb + ' GB，池：' + (props.form.storagePool || 'vmops') + '）'
  if (d.kind === 'source') return d.source || '—'
  if (d.kind === 'image') return d.imageName || ('云镜像 #' + d.imageId)
  return ''
}

function diskCapacityLabel(d) {
  if (d.kind === 'create') return d.createGb + ' GB'
  // 云镜像增量盘：显示声明的读写上限，不是基镜像文件大小（sizeGb 仅在未声明容量时兜底）
  if (d.kind === 'image') return d.createGb ? d.createGb + ' GB' : (d.sizeGb ? d.sizeGb.toFixed(2) : '—') + ' GB'
  return '—'
}
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

.section-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-lg);
}

.section-title {
  margin: 0;
  font-size: 0.95rem;
  font-weight: 600;
}

.nic-row {
  display: flex;
  align-items: center;
  gap: var(--space-lg);
  margin-bottom: var(--space-lg);
}

.nic-index {
  width: 70px;
  color: var(--color-muted-foreground);
  font-size: 0.88rem;
}

.os-hint {
  margin-left: var(--space-lg);
  color: var(--color-muted-foreground);
  font-size: 0.82rem;
}

.opt-hint {
  color: var(--color-muted-foreground);
  font-size: 0.8rem;
}
</style>
