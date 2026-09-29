<template>
    <div class="pane-toolbar">
      <el-button type="primary" @click="openPull">拉取镜像</el-button>
      <el-button type="warning" plain :loading="pruneLoading" @click="pruneImages">清理悬空镜像</el-button>
      <el-input
        v-model="imageKeyword"
        class="ct-search"
        placeholder="按仓库名 / Tag 搜索"
        clearable
        :prefix-icon="Search"
      />
      <span class="count ct-count">共 {{ filteredImages.length }} 个镜像</span>
    </div>
    <el-table :data="filteredImages" v-loading="loading" stripe size="small">
      <template #empty><el-empty description="暂无镜像" :image-size="80" /></template>
      <el-table-column label="仓库" min-width="220" show-overflow-tooltip>
        <template #default="{ row }">
          <span class="mono">{{ row.Repository || '—' }}</span>
        </template>
      </el-table-column>
      <el-table-column label="Tag" width="130">
        <template #default="{ row }">
          <el-tag effect="plain" size="small">{{ row.Tag || '—' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="ID" width="130">
        <template #default="{ row }">
          <span class="mono">{{ shortId(row.ID) }}</span>
        </template>
      </el-table-column>
      <el-table-column label="大小" width="110">
        <template #default="{ row }">{{ dockerSize(row.Size) }}</template>
      </el-table-column>
      <el-table-column label="创建时间" width="160">
        <template #default="{ row }">
          <span class="mono">{{ imageTime(row.CreatedSince || row.Created) }}</span>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="90" fixed="right">
        <template #default="{ row }">
          <el-button size="small" text type="danger" @click="removeImage(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- 拉取镜像对话框 -->
    <el-dialog
      v-model="pullDialog"
      title="拉取镜像"
      width="480px"
      :close-on-click-modal="false"
      :close-on-press-escape="!pullLoading"
      :show-close="!pullLoading"
    >
      <el-form label-width="80px" @submit.prevent>
        <el-form-item label="镜像名" required>
          <el-input
            v-model="pullName"
            placeholder="如 nginx:latest（省略 tag 默认 latest）"
            :disabled="pullLoading"
            @keyup.enter="confirmPull"
          />
        </el-form-item>
      </el-form>
      <p class="dialog-hint">拉取同步执行，大镜像可能需要数分钟，请保持页面打开。</p>
      <template #footer>
        <el-button :disabled="pullLoading" @click="pullDialog = false">取消</el-button>
        <el-button type="primary" :loading="pullLoading" @click="confirmPull">{{ pullLoading ? '正在拉取镜像…' : '开始拉取' }}</el-button>
      </template>
    </el-dialog>
</template>

<script setup>
// 镜像 tab：搜索 / 拉取 / 清理 / 删除自持；镜像数据由壳持有（「创建容器」抽屉的候选下拉同源消费），
// 操作成功后的强制重拉经壳 reloadTab('images')（保持共享 loading + 成功清 backendError 的原语义）。
import { ref, computed, h } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Search } from '@element-plus/icons-vue'
import http from '../../../api'
import { errMsg, isCancel } from '../../../utils/format'
import { shortId, dockerSize, imageTime } from '../../../utils/docker-format'

const props = defineProps({
  images: { type: Array, default: () => [] },
  loading: { type: Boolean, default: false },
  // 操作成功后的强制重拉（壳的 loadTab(name, { force: true })，返回 Promise 供 await）
  reloadTab: { type: Function, required: true }
})

// ── 镜像 tab：关键字搜索（仓库名 / Tag 前端过滤，与容器 tab 同款交互）──

const imageKeyword = ref('')

const filteredImages = computed(() => {
  const kw = imageKeyword.value.trim().toLowerCase()
  if (!kw) return props.images
  return props.images.filter((r) => `${r.Repository || ''} ${r.Tag || ''}`.toLowerCase().includes(kw))
})

// ═══════════════ 镜像：拉取 / 清理 / 删除 ═══════════════

const pullDialog = ref(false)
const pullLoading = ref(false)
const pullName = ref('')

function openPull() {
  pullName.value = ''
  pullDialog.value = true
}

async function confirmPull() {
  const name = pullName.value.trim()
  if (!name) {
    ElMessage.warning('请输入镜像名（如 nginx:latest）')
    return
  }
  pullLoading.value = true
  try {
    // 拉取耗时不可控（后端上限 10 分钟），本请求单独放开 axios 15s 全局超时
    const res = await http.post('/docker/images/pull', { name }, { timeout: 0 })
    ElMessage.success((res.data.data && res.data.data.message) || '镜像拉取完成')
    pullDialog.value = false
    await props.reloadTab('images')
  } catch (e) {
    ElMessage.error(errMsg(e, '镜像拉取失败'))
  } finally {
    pullLoading.value = false
  }
}

const pruneLoading = ref(false)

async function pruneImages() {
  try {
    await ElMessageBox.confirm(
      '将删除所有悬空镜像（未被任何容器引用的未标记镜像层），此操作不可恢复。确定清理？',
      '清理悬空镜像',
      { type: 'warning', confirmButtonText: '清理', confirmButtonClass: 'el-button--danger' }
    )
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  pruneLoading.value = true
  try {
    const res = await http.post('/docker/prune', { type: 'images' })
    showPruneResult(res.data.data || {})
    await props.reloadTab('images')
  } catch (e) {
    ElMessage.error(errMsg(e, '清理失败'))
  } finally {
    pruneLoading.value = false
  }
}

// prune 结果展示：后端 message 已含释放空间，原始输出较长则折叠进弹窗
function showPruneResult(data) {
  const msg = data.message || '清理完成'
  if (data.output) {
    ElMessageBox.alert(
      h('pre', { style: 'max-height:260px;overflow:auto;margin:0;font-size:12px;line-height:1.6;white-space:pre-wrap;word-break:break-all;' }, String(data.output)),
      msg,
      { confirmButtonText: '知道了' }
    )
  } else {
    ElMessage.success(msg)
  }
}

async function removeImage(row) {
  const full = `${row.Repository || '—'}:${row.Tag || '—'}`
  try {
    await ElMessageBox.confirm(`确定删除镜像 ${full}（${shortId(row.ID)}）？`, '删除镜像', {
      type: 'warning',
      confirmButtonText: '删除',
      confirmButtonClass: 'el-button--danger'
    })
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  try {
    // 镜像 ID 可能含特殊字符（sha256: 前缀），必须 encodeURIComponent
    await http.delete('/docker/images/' + encodeURIComponent(row.ID))
    ElMessage.success(`已删除镜像 ${full}`)
    await props.reloadTab('images')
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
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
.ct-search {
  width: 220px;
}
.ct-count {
  margin-left: auto;
}
.dialog-hint {
  margin: 4px 0 0;
  color: var(--el-text-color-secondary, #909399);
  font-size: 0.8rem;
}
</style>
