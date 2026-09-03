<template>
  <div v-loading="loading">
    <el-card shadow="never">
      <div class="toolbar">
        <div>
          <el-button type="primary" :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <el-button type="success" :icon="Upload" @click="dialog = true">上传镜像</el-button>
          <el-select v-model="filter" placeholder="筛选" style="width: 140px" @change="load">
            <el-option label="全部镜像" value="" />
            <el-option label="仅模板" value="true" />
          </el-select>
        </div>
        <span class="count">共 {{ total }} 个</span>
      </div>

      <el-table :data="items" stripe border style="width: 100%">
        <el-table-column prop="name" label="名称" min-width="140" />
        <el-table-column prop="os_version" label="系统版本" min-width="120" />
        <el-table-column label="格式" width="90">
          <template #default="{ row }">{{ row.format }}</template>
        </el-table-column>
        <el-table-column label="大小" width="110">
          <template #default="{ row }">{{ fmtSize(row.size_gb) }} GB</template>
        </el-table-column>
        <el-table-column label="模板" width="90">
          <template #default="{ row }">
            <el-tag v-if="row.is_template" type="success" effect="plain">模板</el-tag>
            <span v-else class="muted">—</span>
          </template>
        </el-table-column>
        <el-table-column prop="created_at" label="创建时间" min-width="170" />
        <el-table-column label="操作" width="100" fixed="right">
          <template #default="{ row }">
            <el-button size="small" type="danger" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="dialog" title="上传镜像" width="480px">
      <el-form label-width="90px">
        <el-form-item label="名称" required>
          <el-input v-model="form.name" placeholder="镜像名称" />
        </el-form-item>
        <el-form-item label="系统版本">
          <el-input v-model="form.os_version" placeholder="如 Ubuntu 22.04" />
        </el-form-item>
        <el-form-item label="作为模板">
          <el-switch v-model="form.is_template" />
        </el-form-item>
        <el-form-item label="文件" required>
          <el-upload
            drag
            :auto-upload="false"
            :show-file-list="true"
            :limit="1"
            :on-change="onFileChange"
            :on-remove="onFileRemove"
            accept=".qcow2,.raw,.vmdk,.iso"
          >
            <el-icon class="el-icon--upload"><UploadFilled /></el-icon>
            <div class="el-upload__text">拖入文件或 <em>点击选择</em></div>
          </el-upload>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">取消</el-button>
        <el-button type="primary" :loading="uploading" :disabled="!file" @click="upload">上传</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Upload, UploadFilled } from '@element-plus/icons-vue'
import { api } from '../api'

const items = ref([])
const total = ref(0)
const loading = ref(false)
const filter = ref('')
const dialog = ref(false)
const uploading = ref(false)
const file = ref(null)

const form = reactive({ name: '', os_version: '', is_template: false })

function fmtSize(gb) {
  const n = Number(gb)
  if (!isFinite(n)) return '0.00'
  return n.toFixed(2)
}

function onFileChange(uploadFile) {
  file.value = uploadFile.raw
}
function onFileRemove() {
  file.value = null
}

async function load() {
  loading.value = true
  try {
    const params = {}
    if (filter.value) params.is_template = filter.value
    const res = await api.listImages(params)
    items.value = (res.data && res.data.items) || []
    total.value = (res.data && res.data.total) || 0
  } catch (e) {
    ElMessage.error('获取镜像列表失败')
  } finally {
    loading.value = false
  }
}

async function upload() {
  if (!form.name) {
    ElMessage.warning('请填写镜像名称')
    return
  }
  if (!file.value) {
    ElMessage.warning('请选择镜像文件')
    return
  }
  const fd = new FormData()
  fd.append('file', file.value)
  fd.append('name', form.name)
  fd.append('os_version', form.os_version)
  fd.append('is_template', form.is_template ? 'true' : 'false')
  uploading.value = true
  try {
    await api.uploadImage(fd)
    ElMessage.success('上传成功')
    dialog.value = false
    file.value = null
    await load()
  } catch (e) {
    ElMessage.error((e.response && e.response.data && e.response.data.message) || '上传失败')
  } finally {
    uploading.value = false
  }
}

async function remove(img) {
  try {
    await ElMessageBox.confirm('确定删除镜像「' + img.name + '」？磁盘文件将一并清理。', '确认删除', { type: 'warning' })
    await api.deleteImage(img.id)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    if (e !== 'cancel' && e?.message !== 'cancel') {
      ElMessage.error((e.response && e.response.data && e.response.data.message) || '删除失败')
    }
  }
}

onMounted(load)
</script>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 14px;
}
.count {
  color: #888;
  font-size: 0.9rem;
}
.muted {
  color: #bbb;
}
</style>
