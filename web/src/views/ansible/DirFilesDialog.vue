<template>
  <!-- 受管目录文件管理器：playbook 目录（role 结构）与共享 role 库共用同一套交互 -->
  <el-dialog
    v-model="visible"
    :title="(kind === 'role' ? '文件 — role ' : '文件 — ') + name"
    width="900px"
    :close-on-click-modal="false"
    append-to-body
  >
    <div class="pf-body">
      <div class="pf-list">
        <div class="pf-list-head">
          <span>文件（{{ files.length }}）</span>
          <el-button size="small" text type="primary" :icon="Plus" @click="newFile">新建</el-button>
          <el-button size="small" text type="primary" :icon="Upload" @click="pickUpload">上传</el-button>
          <input ref="uploadInput" type="file" style="display: none" @change="doUpload" />
        </div>
        <el-table :data="files" size="small" height="420" @row-click="openFile">
          <template #empty><el-empty description="空目录" :image-size="50" /></template>
          <el-table-column prop="path" label="路径" min-width="220" show-overflow-tooltip />
          <el-table-column label="大小" width="90"><template #default="{ row }">{{ fmtSize(row.size) }}</template></el-table-column>
          <el-table-column label="" width="50">
            <template #default="{ row }">
              <el-button text type="danger" size="small" :icon="Delete" @click.stop="removeFile(row)" />
            </template>
          </el-table-column>
        </el-table>
        <div v-if="kind === 'role'" class="pf-hint">
          role 的标准结构：tasks/ handlers/ defaults/ vars/ meta/ templates/ files/。
          任何 playbook 都能 <code>roles: [{{ name }}]</code> 引用它，无需复制。
        </div>
      </div>
      <div class="pf-editor">
        <div class="pf-editor-head">
          <span class="mono">{{ editPath || '（点左侧文件编辑）' }}</span>
          <el-button size="small" type="primary" :disabled="!editPath" :loading="saving" @click="saveFile">保存</el-button>
        </div>
        <el-input v-model="editContent" type="textarea" :rows="20" class="mono pf-textarea" placeholder="选择或新建文件后在此编辑" />
        <div v-if="warning" class="pf-warn">语法校验：{{ warning }}</div>
      </div>
    </div>
    <template #footer><el-button @click="visible = false">关闭</el-button></template>
  </el-dialog>
</template>

<script setup>
import { ref, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Delete, Upload } from '@element-plus/icons-vue'
import { api } from '../../api'
import { errMsg, fmtFileSize } from '../../utils/format'

const props = defineProps({
  // playbook：目录形态 playbook（role 结构）的文件；role：共享 role 库里的 role
  kind: { type: String, default: 'playbook' }
})

const emit = defineEmits(['changed'])
const visible = ref(false)
const name = ref('')
const files = ref([])
const editPath = ref('')
const editContent = ref('')
const saving = ref(false)
const warning = ref('')
const uploadInput = ref(null)

// 两种 kind 的 API 形状完全一致，只换一组函数
const apiSet = computed(() =>
  props.kind === 'role'
    ? {
        list: api.ansibleRoleFiles,
        read: api.ansibleRoleReadFile,
        write: api.ansibleRoleWriteFile,
        upload: api.ansibleRoleUploadFile,
        remove: api.ansibleRoleDeleteFile
      }
    : {
        list: api.ansiblePlaybookFiles,
        read: api.ansiblePlaybookReadFile,
        write: api.ansiblePlaybookWriteFile,
        upload: api.ansiblePlaybookUploadFile,
        remove: api.ansiblePlaybookDeleteFile
      }
)

const fmtSize = (n) => fmtFileSize(n)

async function loadFiles() {
  try {
    const res = await apiSet.value.list(name.value)
    files.value = (res.data && res.data.items) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '读取文件列表失败'))
  }
}

async function openFile(row) {
  try {
    const res = await apiSet.value.read(name.value, row.path)
    editPath.value = row.path
    editContent.value = (res.data && res.data.content) || ''
    warning.value = ''
  } catch (e) {
    ElMessage.error(errMsg(e, '读取失败'))
  }
}

async function newFile() {
  const preset = props.kind === 'role' ? 'tasks/' : 'roles/'
  let p = ''
  try {
    const r = await ElMessageBox.prompt(
      props.kind === 'role' ? '文件相对路径（如 tasks/main.yml）' : '文件相对路径（如 roles/nginx/tasks/main.yml）',
      '新建文件',
      { inputValue: preset }
    )
    p = (r.value || '').trim()
  } catch (e) {
    return
  }
  if (!p) return
  editPath.value = p
  editContent.value = '---\n'
  warning.value = ''
}

async function saveFile() {
  if (!editPath.value) return
  saving.value = true
  try {
    const res = await apiSet.value.write(name.value, editPath.value, editContent.value)
    warning.value = (res.data && res.data.warning) || ''
    ElMessage.success('已保存')
    await loadFiles()
    emit('changed')
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
  fd.append('path', file.name)
  fd.append('file', file)
  try {
    await apiSet.value.upload(name.value, fd)
    ElMessage.success('已上传 ' + file.name)
    await loadFiles()
    emit('changed')
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
    await apiSet.value.remove(name.value, row.path)
    ElMessage.success('已删除')
    if (editPath.value === row.path) {
      editPath.value = ''
      editContent.value = ''
    }
    await loadFiles()
    emit('changed')
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

// 打开：kind 由父组件绑定，name 是被管理的 playbook id / role 名
function open(target) {
  name.value = target
  editPath.value = ''
  editContent.value = ''
  warning.value = ''
  visible.value = true
  loadFiles()
}
defineExpose({ open })
</script>

<style scoped>
.pf-body {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.1fr);
  gap: 16px;
}
.pf-list-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
  font-weight: 600;
}
.pf-hint {
  margin-top: 8px;
  font-size: 0.78rem;
  line-height: 1.6;
  color: var(--color-muted-foreground);
}
.pf-hint code {
  background: var(--el-fill-color-light);
  padding: 1px 4px;
  border-radius: 3px;
}
.pf-editor-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 6px;
}
.pf-textarea :deep(textarea) {
  font-family: var(--font-mono);
  font-size: 0.82rem;
}
.pf-warn {
  margin-top: 8px;
  font-size: 0.8rem;
  color: var(--el-color-danger);
}
</style>
