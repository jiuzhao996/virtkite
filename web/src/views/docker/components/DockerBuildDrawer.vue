<template>
  <!-- Dockerfile 构建抽屉：构建上下文（文件管理 + Dockerfile 编辑）→ 构建 → 实时日志 -->
  <el-drawer v-model="visible" title="构建镜像（Dockerfile）" size="72%" :close-on-click-modal="false">
    <div class="bd-bar">
      <el-select v-model="ctxName" placeholder="选择构建上下文" style="width: 220px" @change="onCtxChange">
        <el-option v-for="c in contexts" :key="c.name" :label="c.name + '（' + c.file_count + ' 文件' + (c.has_dockerfile ? '' : '，无 Dockerfile') + '）'" :value="c.name" />
      </el-select>
      <el-button size="small" :icon="Plus" @click="createCtx">新建上下文</el-button>
      <el-button size="small" :icon="Delete" :disabled="!ctxName" @click="deleteCtx">删除上下文</el-button>
      <el-button size="small" :icon="Refresh" @click="loadAll">刷新</el-button>
      <span class="bd-hint">上下文目录：data/docker-builds/&lt;名称&gt;/；Dockerfile 与需 COPY 的文件都放这里</span>
    </div>

    <div v-if="!ctxName" class="bd-empty">
      <el-empty description="还没有构建上下文。构建上下文是一个目录，放 Dockerfile 和需要 COPY 进镜像的文件。" :image-size="80">
        <el-button type="primary" :icon="Plus" @click="createCtx">新建上下文</el-button>
      </el-empty>
    </div>

    <div v-else class="bd-body">
      <div class="bd-files">
        <div class="bd-files-head">
          <span>上下文文件</span>
          <el-button size="small" text type="primary" :icon="Plus" @click="newFile">新建文件</el-button>
          <el-button size="small" text type="primary" :icon="Upload" @click="pickUpload">上传</el-button>
          <input ref="uploadInput" type="file" style="display: none" @change="doUpload" />
        </div>
        <div class="bd-crumb mono">
          <el-button size="small" text :disabled="curPath === '.'" :icon="Top" @click="goUp">上级</el-button>
          <span class="bd-crumb-path" @click="curPath = '.'">/</span>
          <template v-for="(seg, i) in crumbs" :key="i">
            <span class="bd-crumb-sep">/</span>
            <span class="bd-crumb-path" @click="curPath = crumbs.slice(0, i + 1).join('/')">{{ seg }}</span>
          </template>
        </div>
        <el-table :data="files" size="small" height="330" @row-click="openFile">
          <template #empty><el-empty description="空目录，先建一个 Dockerfile" :image-size="60" /></template>
          <el-table-column label="文件" min-width="200" show-overflow-tooltip>
            <template #default="{ row }">
              <el-icon v-if="row.path.endsWith('/')"><Folder /></el-icon>
              <el-icon v-else><Document /></el-icon>
              <span style="margin-left: 6px">{{ baseName(row.path) }}</span>
            </template>
          </el-table-column>
          <el-table-column label="大小" width="90"><template #default="{ row }">{{ row.path.endsWith('/') ? '—' : fmtSize(row.size) }}</template></el-table-column>
          <el-table-column label="" width="60">
            <template #default="{ row }">
              <el-button text type="danger" size="small" :icon="Delete" @click.stop="removeFile(row)" />
            </template>
          </el-table-column>
        </el-table>
      </div>

      <div class="bd-editor">
        <div class="bd-editor-head">
          <span class="mono">{{ editPath || '（点左侧文件编辑）' }}</span>
          <el-button size="small" type="primary" :disabled="!editPath" :loading="saving" @click="saveFile">保存</el-button>
        </div>
        <el-input v-model="editContent" type="textarea" :rows="20" class="mono bd-textarea" placeholder="选择或新建一个文件后在此编辑（Dockerfile 建议首行 FROM ...）" />
      </div>
    </div>

    <el-divider />
    <el-form label-width="90px" inline>
      <el-form-item label="镜像标签" required>
        <el-input v-model="buildForm.tag" placeholder="如 myapp:1.0" style="width: 200px" />
      </el-form-item>
      <el-form-item label="Dockerfile">
        <el-input v-model="buildForm.dockerfile" placeholder="留空用上下文根下的 Dockerfile" style="width: 220px" />
      </el-form-item>
      <el-form-item label="构建参数">
        <el-input v-model="buildArgsText" placeholder="K=V，多个用换行/逗号分隔" style="width: 260px" />
      </el-form-item>
      <el-form-item>
        <el-checkbox v-model="buildForm.no_cache">--no-cache</el-checkbox>
      </el-form-item>
      <el-form-item>
        <el-button type="primary" :icon="Tools" :loading="building" :disabled="!ctxName" @click="startBuild">开始构建</el-button>
      </el-form-item>
    </el-form>
    <div v-if="buildLog" class="bd-log mono">{{ buildLog }}</div>
  </el-drawer>
</template>

<script setup>
import { ref, reactive, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Delete, Refresh, Upload, Tools, Folder, Document, Top } from '@element-plus/icons-vue'
import { api } from '../../../api'
import { errMsg, fmtFileSize } from '../../../utils/format'
import { pollTask, extractTaskId, taskErrorMessage } from '../../../utils/task'

const emit = defineEmits(['built'])
const visible = ref(false)
const contexts = ref([])
const ctxName = ref('')
const files = ref([])
const curPath = ref('.')
const editPath = ref('')
const editContent = ref('')
const saving = ref(false)
const uploadInput = ref(null)
const building = ref(false)
const buildLog = ref('')
const buildArgsText = ref('')
const buildForm = reactive({ tag: '', dockerfile: '', no_cache: false })

// 面包屑：curPath 为 '.' 时为空
const crumbs = computed(() => (curPath.value === '.' ? [] : curPath.value.split('/').filter(Boolean)))

function baseName(p) {
  return p.endsWith('/') ? p.slice(0, -1).split('/').pop() + '/' : p.split('/').pop()
}

function goUp() {
  if (curPath.value === '.') return
  const segs = crumbs.value.slice(0, -1)
  curPath.value = segs.length ? segs.join('/') : '.'
  loadFiles()
}

const fmtSize = (n) => fmtFileSize(n)

async function loadContexts() {
  try {
    const res = await api.dockerBuilds()
    contexts.value = (res.data && res.data.items) || []
    if (!ctxName.value && contexts.value.length) ctxName.value = contexts.value[0].name
  } catch (e) {
    ElMessage.error(errMsg(e, '读取构建上下文失败'))
  }
}

async function loadFiles() {
  if (!ctxName.value) {
    files.value = []
    return
  }
  try {
    const res = await api.dockerBuildFiles(ctxName.value, curPath.value)
    files.value = (res.data && res.data.items) || []
  } catch (e) {
    files.value = []
  }
}

async function loadAll() {
  await loadContexts()
  await loadFiles()
}

function onCtxChange() {
  curPath.value = '.'
  editPath.value = ''
  editContent.value = ''
  loadFiles()
}

async function createCtx() {
  let name = ''
  try {
    const r = await ElMessageBox.prompt('上下文名称（字母数字开头，可含 _ . -）', '新建构建上下文', { inputPattern: /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$/, inputErrorMessage: '名称非法' })
    name = (r.value || '').trim()
  } catch (e) {
    return
  }
  try {
    await api.dockerBuildCreate(name)
    ElMessage.success('已创建')
    ctxName.value = name
    await loadAll()
  } catch (e) {
    ElMessage.error(errMsg(e, '创建失败'))
  }
}

async function deleteCtx() {
  try {
    await ElMessageBox.confirm(`确定删除构建上下文「${ctxName.value}」及其全部文件？`, '删除', { type: 'warning', confirmButtonClass: 'el-button--danger' })
  } catch (e) {
    return
  }
  try {
    await api.dockerBuildDelete(ctxName.value)
    ElMessage.success('已删除')
    ctxName.value = ''
    curPath.value = '.'
    editPath.value = ''
    await loadAll()
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

async function openFile(row) {
  if (row.path.endsWith('/')) {
    curPath.value = row.path.slice(0, -1)
    editPath.value = ''
    editContent.value = ''
    await loadFiles()
    return
  }
  try {
    const res = await api.dockerBuildReadFile(ctxName.value, row.path)
    editPath.value = row.path
    editContent.value = (res.data && res.data.content) || ''
  } catch (e) {
    ElMessage.error(errMsg(e, '读取失败'))
  }
}

async function newFile() {
  const prefix = curPath.value === '.' ? '' : curPath.value + '/'
  let name = ''
  try {
    const r = await ElMessageBox.prompt('文件相对路径（如 Dockerfile 或 conf/nginx.conf）', '新建文件', { inputValue: prefix + 'Dockerfile' })
    name = (r.value || '').trim()
  } catch (e) {
    return
  }
  if (!name) return
  editPath.value = name
  editContent.value = name === 'Dockerfile' ? 'FROM alpine:3.24\nRUN echo hello\nCMD ["sh", "-c", "echo ok"]\n' : ''
}

async function saveFile() {
  if (!editPath.value) return
  saving.value = true
  try {
    await api.dockerBuildWriteFile(ctxName.value, editPath.value, editContent.value)
    ElMessage.success('已保存')
    await loadFiles()
  } catch (e) {
    ElMessage.error(errMsg(e, '保存失败'))
  } finally {
    saving.value = false
  }
}

function pickUpload() {
  uploadInput.value?.click()
}

async function doUpload(ev) {
  const file = ev.target.files && ev.target.files[0]
  if (!file) return
  const fd = new FormData()
  fd.append('path', curPath.value === '.' ? file.name : curPath.value + '/' + file.name)
  fd.append('file', file)
  try {
    await api.dockerBuildUpload(ctxName.value, fd)
    ElMessage.success('已上传 ' + file.name)
    await loadFiles()
  } catch (e) {
    ElMessage.error(errMsg(e, '上传失败'))
  } finally {
    ev.target.value = ''
  }
}

async function removeFile(row) {
  try {
    await ElMessageBox.confirm(`删除「${row.path}」？`, '删除', { type: 'warning', confirmButtonClass: 'el-button--danger' })
  } catch (e) {
    return
  }
  try {
    await api.dockerBuildDeleteFile(ctxName.value, row.path)
    ElMessage.success('已删除')
    if (editPath.value === row.path) {
      editPath.value = ''
      editContent.value = ''
    }
    await loadFiles()
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

function parseBuildArgs(text) {
  const out = {}
  for (const part of text.split(/[\n,，]+/)) {
    const t = part.trim()
    if (!t) continue
    const i = t.indexOf('=')
    if (i > 0) out[t.slice(0, i).trim()] = t.slice(i + 1).trim()
  }
  return out
}

// 任务 result 两种形态：构建中的节流中间态是纯文本，终态是 {context,tag,output} 的 JSON。
// 统一取成可展示的文本，避免终态回填时把 JSON 原样糊到日志框里。
function taskLogText(raw) {
  if (!raw) return ''
  const s = String(raw)
  if (s.startsWith('{')) {
    try {
      const o = JSON.parse(s)
      return o.output || ''
    } catch (_) {
      /* 非法 JSON：原样展示 */
    }
  }
  return s
}

async function startBuild() {
  if (!buildForm.tag.trim()) {
    ElMessage.warning('请填写镜像标签（如 myapp:1.0）')
    return
  }
  building.value = true
  buildLog.value = ''
  try {
    const payload = { tag: buildForm.tag.trim(), no_cache: buildForm.no_cache }
    if (buildForm.dockerfile.trim()) payload.dockerfile = buildForm.dockerfile.trim()
    const ba = parseBuildArgs(buildArgsText.value)
    if (Object.keys(ba).length) payload.build_args = ba
    ElMessage.info('构建任务已提交，日志将实时显示…')
    const done = await pollTask(extractTaskId(await api.dockerBuildRun(ctxName.value, payload)), {
      timeout: 30 * 60 * 1000, // 拉基础镜像 + 装包是分钟级，5 分钟默认超时不够
      onProgress: (t) => {
        buildLog.value = taskLogText(t.result).slice(-6000)
      }
    })
    // 终态回填：基础镜像已在本地时整个构建可能不到一个节流窗口（2s），
    // 中间态永远为空——不回填终态输出，用户就只看得到一句「构建完成」。
    const final = taskLogText(done && done.result).slice(-6000)
    if (final) buildLog.value = final
    ElMessage.success('镜像构建完成')
    emit('built')
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '构建失败'))
  } finally {
    building.value = false
  }
}

function open() {
  visible.value = true
  loadAll()
}
defineExpose({ open })
</script>

<style scoped>
.bd-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.bd-hint {
  font-size: 0.8rem;
  color: var(--color-muted-foreground);
}
.bd-empty {
  padding: 12px 0 4px;
}
.bd-body {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.2fr);
  gap: 16px;
}
.bd-files-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
  font-weight: 600;
}
.bd-crumb {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 2px;
  margin-bottom: 6px;
  font-size: 0.78rem;
  color: var(--color-muted-foreground);
}
.bd-crumb-path {
  cursor: pointer;
  padding: 0 2px;
  border-radius: 3px;
}
.bd-crumb-path:hover {
  color: var(--el-color-primary);
  background: var(--el-fill-color-light);
}
.bd-crumb-sep {
  opacity: 0.5;
}
.bd-editor-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 6px;
}
.bd-textarea :deep(textarea) {
  font-family: var(--font-mono);
  font-size: 0.82rem;
}
.bd-log {
  margin-top: 12px;
  padding: 12px;
  max-height: 280px;
  overflow: auto;
  white-space: pre-wrap;
  font-size: 0.78rem;
  line-height: 1.5;
  background: #0f172a;
  color: #d8e2ef;
  border-radius: var(--radius-md);
}
</style>
