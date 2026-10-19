<template>
  <div class="cf">
    <!-- 文件查看模式：显示单个文本文件内容 -->
    <template v-if="viewing">
      <div class="cf-bar">
        <el-button size="small" :icon="Back" @click="closeFile">返回目录</el-button>
        <span class="cf-file mono" :title="filePath">{{ filePath }}</span>
        <el-tag v-if="fileTruncated" size="small" type="warning" effect="light">已截断（仅显示前 1MB）</el-tag>
        <span class="cf-spacer" />
        <CopyButton :text="fileContent" tip="复制文件内容" success-msg="文件内容已复制" />
      </div>
      <div v-loading="fileLoading" class="cf-viewer-wrap">
        <pre class="cf-pre">{{ fileContent || '（空文件）' }}</pre>
      </div>
    </template>

    <!-- 目录浏览模式 -->
    <template v-else>
      <div class="cf-bar">
        <el-button size="small" :icon="Back" :disabled="currentPath === '/'" @click="goUp">上级</el-button>
        <el-button size="small" :icon="Refresh" :loading="loading" @click="load(currentPath)">刷新</el-button>
        <span class="cf-crumb mono">
          <a class="cf-lk" @click="load('/')">/</a>
          <template v-for="(seg, i) in segments" :key="i">
            <a class="cf-lk" @click="load(pathTo(i))">{{ seg }}</a>
            <span class="cf-sep">/</span>
          </template>
        </span>
      </div>
      <div v-loading="loading" class="cf-body">
        <el-empty v-if="!running" description="容器未运行，无法浏览文件" :image-size="80" />
        <el-alert v-else-if="error" :title="error" type="error" :closable="false" show-icon />
        <el-table v-else :data="entries" size="small" stripe @row-click="onRowClick" class="cf-table">
          <el-table-column label="名称" min-width="220">
            <template #default="{ row }">
              <span class="cf-name">
                <el-icon class="cf-ico" :class="row.is_dir ? 'is-dir' : 'is-file'">
                  <Folder v-if="row.is_dir" />
                  <Document v-else />
                </el-icon>
                <span class="mono" :class="{ 'cf-dir-name': row.is_dir }">{{ row.name }}</span>
                <span v-if="row.is_link" class="cf-link-target mono" :title="row.link">→ {{ row.link }}</span>
              </span>
            </template>
          </el-table-column>
          <el-table-column label="大小" width="100" align="right">
            <template #default="{ row }"><span class="mono">{{ row.is_dir ? '—' : fmtSize(row.size) }}</span></template>
          </el-table-column>
          <el-table-column label="权限" width="120"><template #default="{ row }"><span class="mono cf-mode">{{ row.mode }}</span></template></el-table-column>
          <el-table-column label="" width="70" align="center">
            <template #default="{ row }">
              <el-button v-if="!row.is_dir" size="small" text type="primary" @click.stop="openFile(row)">查看</el-button>
            </template>
          </el-table-column>
        </el-table>
        <el-empty v-if="!loading && !error && running && !entries.length" description="目录为空" :image-size="60" />
      </div>
    </template>
  </div>
</template>

<script setup>
// 容器内文件浏览（只读，借 1Panel file-browser）：目录导航 + 文本文件查看（后端限 1MB）。
// 刻意不含上传/删除——写操作的风险面（宿主机挂载点、权限）与「浏览」不是一个量级。
import { ref, computed, watch, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Folder, Document, Refresh, Back } from '@element-plus/icons-vue'
import CopyButton from '../../../components/CopyButton.vue'
import { fmtSizeBytes, errMsg } from '../../../utils/format'
import { api } from '../../../api'

const props = defineProps({
  containerId: { type: String, required: true },
  running: { type: Boolean, default: false }
})

const currentPath = ref('/')
const entries = ref([])
const loading = ref(false)
const error = ref('')

const viewing = ref(false)
const filePath = ref('')
const fileContent = ref('')
const fileTruncated = ref(false)
const fileLoading = ref(false)

const segments = computed(() =>
  currentPath.value.split('/').filter((s) => s !== '')
)

function pathTo(i) {
  // 面包屑第 i 段对应的绝对路径
  return '/' + segments.value.slice(0, i + 1).join('/')
}

function fmtSize(n) {
  return fmtSizeBytes(n || 0)
}

async function load(path) {
  if (!props.running) return
  loading.value = true
  error.value = ''
  try {
    const res = await api.containerFiles(props.containerId, path)
    const d = res.data || {}
    entries.value = d.items || []
    currentPath.value = d.path || path
    // 目录排在文件前，各自按名排序
    entries.value.sort((a, b) => {
      if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1
      return String(a.name).localeCompare(String(b.name))
    })
  } catch (e) {
    error.value = errMsg(e, '浏览容器文件失败')
    entries.value = []
  } finally {
    loading.value = false
  }
}

function goUp() {
  if (currentPath.value === '/') return
  const parts = segments.value.slice(0, -1)
  load('/' + parts.join('/'))
}

function onRowClick(row) {
  if (row.is_dir) load(joinPath(currentPath.value, row.name))
}

function joinPath(base, name) {
  return base === '/' ? '/' + name : base + '/' + name
}

async function openFile(row) {
  const p = joinPath(currentPath.value, row.name)
  filePath.value = p
  viewing.value = true
  fileLoading.value = true
  fileContent.value = ''
  fileTruncated.value = false
  try {
    const res = await api.containerFileContent(props.containerId, p)
    const d = res.data || {}
    fileContent.value = d.content || ''
    fileTruncated.value = !!d.truncated
  } catch (e) {
    ElMessage.error(errMsg(e, '读取文件失败'))
    viewing.value = false
  } finally {
    fileLoading.value = false
  }
}

function closeFile() {
  viewing.value = false
  fileContent.value = ''
}

// 容器切换（同一抽屉换容器）时复位
watch(
  () => props.containerId,
  () => {
    viewing.value = false
    currentPath.value = '/'
    entries.value = []
    error.value = ''
    if (props.running) load('/')
  }
)

// 运行态从「非运行」变「运行」时首次加载
watch(
  () => props.running,
  (r) => {
    if (r && !entries.value.length) load('/')
  }
)

onMounted(() => {
  if (props.running) load('/')
})
</script>

<style scoped>
.cf {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}
.cf-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.cf-spacer {
  flex: 1;
}
.cf-crumb {
  display: flex;
  align-items: center;
  gap: 2px;
  font-size: 0.82rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cf-lk {
  color: var(--el-color-primary);
  cursor: pointer;
}
.cf-lk:hover {
  text-decoration: underline;
}
.cf-sep {
  color: var(--color-muted-foreground);
}
.cf-file {
  font-size: 0.85rem;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cf-body {
  flex: 1;
  min-height: 0;
}
.cf-name {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.cf-ico.is-dir {
  color: #e6a23c;
}
.cf-ico.is-file {
  color: #909399;
}
.cf-dir-name {
  cursor: pointer;
  font-weight: 600;
}
.cf-link-target {
  color: var(--color-muted-foreground);
  font-size: 0.78rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cf-mode {
  color: var(--color-muted-foreground);
  font-size: 0.78rem;
}
.cf-viewer-wrap {
  flex: 1;
  min-height: 0;
}
.cf-pre {
  margin: 0;
  padding: 12px;
  min-height: 300px;
  max-height: calc(100vh - 300px);
  overflow: auto;
  background: #0d1b2a;
  color: #cfe8ff;
  border-radius: var(--radius-md, 8px);
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
  font-size: 0.8rem;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
  user-select: text;
}
</style>
