<template>
    <div class="pane-toolbar">
      <el-button type="primary" @click="openPull">拉取镜像</el-button>
	<el-button :icon="RefreshRight" :loading="vcLoading" @click="checkVersions">检查更新</el-button>
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
      <el-table-column label="更新" width="92">
        <template #default="{ row }">
          <template v-if="vcMap[`${row.Repository}:${row.Tag}`]">
            <el-tag v-if="vcMap[`${row.Repository}:${row.Tag}`].status === 'outdated'" size="small" type="warning" effect="light">有更新</el-tag>
            <el-tag v-else-if="vcMap[`${row.Repository}:${row.Tag}`].status === 'up_to_date'" size="small" type="success" effect="light">最新</el-tag>
            <el-tooltip v-else :content="vcMap[`${row.Repository}:${row.Tag}`].note || '未知'" placement="top">
              <span class="mono" style="color: var(--color-muted-foreground)">—</span>
            </el-tooltip>
          </template>
          <span v-else class="mono" style="color: var(--color-muted-foreground)">—</span>
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
// 镜像页（原镜像 tab，1Panel 式子路由化）：数据自持（不再由壳共享镜像列表），搜索 / 拉取 / 清理 / 删除自洽；
// 取数失败经 inject('dockerPage') 上报布局壳（503 置门控 alert，其余 toast），操作成功后本地 refresh 重拉。
import { ref, computed, inject, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Search, RefreshRight } from '@element-plus/icons-vue'
import { api } from '../../../api'
import { errMsg, isCancel } from '../../../utils/format'
import { shortId, dockerSize, imageTime } from '../../../utils/docker-format'

// 布局壳通信：失败上报 / 成功清 503 门控
const { reportLoadError, clearLoadError } = inject('dockerPage')

const images = ref([])
const loading = ref(false)
// 版本检测（P3）：digest 对比结果 map，key=repository:tag
const vcMap = ref({})
const vcLoading = ref(false)

async function checkVersions() {
  vcLoading.value = true
  try {
    const res = await api.versionCheck()
    const m = {}
    for (const it of (res.data && res.data.items) || []) {
      m[`${it.repository}:${it.tag}`] = it
    }
    vcMap.value = m
    const counts = { outdated: 0, up_to_date: 0 }
    for (const it of Object.values(m)) if (counts[it.status] !== undefined) counts[it.status]++
    ElMessage.success(`检测完成：${counts.outdated} 个有更新，${counts.up_to_date} 个最新`)
  } catch (e) {
    ElMessage.error(errMsg(e, '版本检测失败'))
  } finally {
    vcLoading.value = false
  }
}

async function refresh() {
  loading.value = true
  try {
    const res = await api.dockerImages()
    images.value = (res.data || {}).items || []
    clearLoadError()
  } catch (e) {
    reportLoadError(e, '获取镜像列表失败')
  } finally {
    loading.value = false
  }
}

onMounted(refresh)
defineExpose({ refresh })

// ── 镜像页：关键字搜索（仓库名 / Tag 前端过滤，与容器页同款交互）──

const imageKeyword = ref('')

const filteredImages = computed(() => {
  const kw = imageKeyword.value.trim().toLowerCase()
  if (!kw) return images.value
  return images.value.filter((r) => `${r.Repository || ''} ${r.Tag || ''}`.toLowerCase().includes(kw))
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
    // 拉取耗时不可控（后端上限 10 分钟），api.dockerPullImage 已单独放开 15s 全局超时
    const res = await api.dockerPullImage(name)
    ElMessage.success((res.data && res.data.message) || '镜像拉取完成')
    pullDialog.value = false
    await refresh()
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
    const res = await api.dockerPrune('images')
    showPruneResult(res.data || {})
    await refresh()
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
    // 镜像 ID 可能含特殊字符（sha256: 前缀），api 层已 encodeURIComponent
    await api.dockerDeleteImage(row.ID)
    ElMessage.success(`已删除镜像 ${full}`)
    await refresh()
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
