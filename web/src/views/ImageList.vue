<template>
  <div v-loading="loading">
    <div class="page-head">
      <h2 class="page-title">镜像管理</h2>
      <span class="page-desc">统一管理镜像与模板，模板可直接用于创建虚拟机</span>
    </div>

    <el-card shadow="never">
      <div class="toolbar">
        <div class="toolbar-left">
          <el-button type="primary" :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
          <el-button v-if="isAdmin" type="success" :icon="Upload" @click="openUpload">上传镜像</el-button>
          <el-select v-model="filter" placeholder="筛选" clearable style="width: 150px" @change="load">
            <el-option label="全部镜像" value="" />
            <el-option label="仅模板" value="true" />
          </el-select>
        </div>
        <div class="toolbar-right">
          <el-tag v-if="templateCount" type="success" effect="plain" size="small">模板 {{ templateCount }}</el-tag>
          <span class="count">共 {{ total }} 个</span>
        </div>
      </div>

      <el-table :data="items" stripe border style="width: 100%" empty-text="暂无镜像，可点击上传镜像添加">
        <el-table-column prop="name" label="名称" min-width="150" />
        <el-table-column prop="os_version" label="OS 版本" min-width="130" />
        <el-table-column label="大小(GB)" width="110">
          <template #default="{ row }">{{ fmtSize(row.size_gb) }}</template>
        </el-table-column>
        <el-table-column label="格式" width="90">
          <template #default="{ row }">
            <el-tag effect="plain" size="small">{{ row.format || '—' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="模板标记" width="100">
          <template #default="{ row }">
            <el-tag v-if="row.is_template" type="success" effect="light">模板</el-tag>
            <el-tag v-else type="info" effect="plain">普通</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="上传时间" min-width="172">
          <template #default="{ row }">
            <span class="mono">{{ fmtTime(row.created_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" min-width="300" fixed="right">
          <template #default="{ row }">
            <div class="ops">
              <el-button v-if="isAdmin && row.is_template" size="small" type="primary" :icon="Cpu" @click="openClone(row)">基于此创建 VM</el-button>
              <el-button v-if="isAdmin && row.is_template" size="small" :icon="StarFilled" @click="toggleTemplate(row)">取消模板</el-button>
              <el-button v-if="isAdmin && !row.is_template" size="small" :icon="Star" @click="toggleTemplate(row)">标记为模板</el-button>
              <el-button v-if="isAdmin" size="small" type="danger" :icon="Delete" @click="remove(row)">删除</el-button>
            </div>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 上传镜像 -->
    <el-dialog v-model="dialog" title="上传镜像" width="500px">
      <el-form label-width="90px">
        <el-form-item label="名称" required>
          <el-input v-model="form.name" placeholder="镜像名称" />
        </el-form-item>
        <el-form-item label="OS 版本">
          <el-input v-model="form.os_version" placeholder="如 Ubuntu 22.04" />
        </el-form-item>
        <el-form-item label="存储池">
          <el-select
            v-model="form.pool"
            filterable
            allow-create
            default-first-option
            placeholder="默认 img 池"
            style="width: 100%"
          >
            <el-option v-for="p in poolOptions" :key="p" :label="p" :value="p" />
          </el-select>
        </el-form-item>
        <el-form-item label="作为模板">
          <el-switch v-model="form.is_template" />
        </el-form-item>
        <el-form-item label="文件" required>
          <el-upload
            ref="uploadRef"
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

    <!-- 基于模板创建虚拟机 -->
    <el-dialog v-model="cloneDialog" :title="'基于模板创建虚拟机 - ' + (cloneImg.name || '')" width="480px">
      <el-alert type="info" :closable="false" show-icon class="clone-tip" title="将引用模板文件作为系统盘创建虚拟机，创建后可在虚拟机列表管理" />
      <el-form label-width="90px">
        <el-form-item label="名称" required>
          <el-input v-model="cloneForm.name" placeholder="仅字母、数字、_、-" />
        </el-form-item>
        <el-form-item label="CPU 核数">
          <el-input-number v-model="cloneForm.vcpu" :min="1" :max="64" />
        </el-form-item>
        <el-form-item label="内存(MB)">
          <el-input-number v-model="cloneForm.memory_mb" :min="256" :max="131072" :step="256" />
        </el-form-item>
        <el-form-item label="网络">
          <el-input v-model="cloneForm.network" placeholder="默认 default" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="cloneDialog = false">取消</el-button>
        <el-button type="primary" :loading="cloning" @click="cloneVm">创建</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Upload, UploadFilled, Delete, Star, StarFilled, Cpu } from '@element-plus/icons-vue'
import { api } from '../api'
import { useAuth } from '../store/auth'
import { pollTask, extractTaskId, taskErrorMessage } from '../utils/task'

const { isAdmin } = useAuth()

const items = ref([])
const total = ref(0)
const loading = ref(false)
const filter = ref('')
const dialog = ref(false)
const uploading = ref(false)
const file = ref(null)
const uploadRef = ref(null)
const poolOptions = ref(['img'])
const cloneDialog = ref(false)
const cloneImg = ref({})
const cloning = ref(false)

const form = reactive({ name: '', os_version: '', is_template: false, pool: 'img' })
const cloneForm = reactive({ name: '', vcpu: 1, memory_mb: 1024, network: 'default' })

const templateCount = computed(() => items.value.filter((i) => i.is_template).length)

function errMsg(e, fallback) {
  return (e.response && e.response.data && e.response.data.message) || fallback
}

function fmtSize(gb) {
  const n = Number(gb)
  if (!isFinite(n)) return '0.00'
  return n.toFixed(2)
}

function fmtTime(s) {
  if (!s) return '—'
  const d = new Date(s)
  if (isNaN(d.getTime())) return s
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
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
    ElMessage.error(errMsg(e, '获取镜像列表失败'))
  } finally {
    loading.value = false
  }
}

// 存储池下拉：默认 img，可手输其他池名
async function loadPools() {
  try {
    const res = await api.listStoragePools()
    const names = ((res.data && res.data.items) || []).map((p) => p.name)
    poolOptions.value = [...new Set(['img', ...names])]
  } catch (e) {
    poolOptions.value = ['img']
  }
}

function openUpload() {
  Object.assign(form, { name: '', os_version: '', is_template: false, pool: 'img' })
  file.value = null
  // 清空上传组件遗留的文件列表，避免上次上传的文件残留（limit=1 下无法再选新文件）
  if (uploadRef.value) uploadRef.value.clearFiles()
  dialog.value = true
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
  fd.append('pool', form.pool || 'img')
  fd.append('is_template', form.is_template ? 'true' : 'false')
  uploading.value = true
  try {
    await api.uploadImage(fd)
    ElMessage.success('上传成功')
    dialog.value = false
    file.value = null
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '上传失败'))
  } finally {
    uploading.value = false
  }
}

// 标记/取消模板
async function toggleTemplate(img) {
  const next = !img.is_template
  const label = next ? '标记为模板' : '取消模板'
  try {
    await ElMessageBox.confirm(
      `确定将镜像「${img.name}」${label}？${next ? '模板镜像可直接用于创建虚拟机。' : '取消后仍作为普通镜像保留。'}`,
      '确认操作',
      { type: next ? 'warning' : 'info' }
    )
    await api.setImageTemplate(img.id, next)
    ElMessage.success(`${label}成功`)
    await load()
  } catch (e) {
    if (e !== 'cancel' && e?.message !== 'cancel') {
      ElMessage.error(errMsg(e, '操作失败'))
    }
  }
}

// 基于模板创建虚拟机
function openClone(img) {
  cloneImg.value = img
  Object.assign(cloneForm, {
    name: img.name.replace(/[^a-zA-Z0-9_-]/g, '-') + '-clone',
    vcpu: 1,
    memory_mb: 1024,
    network: 'default'
  })
  cloneDialog.value = true
}

async function cloneVm() {
  if (!cloneForm.name) {
    ElMessage.warning('请填写虚拟机名称')
    return
  }
  cloning.value = true
  try {
    const res = await api.cloneImage(cloneImg.value.id, {
      name: cloneForm.name,
      vcpu: cloneForm.vcpu,
      memory_mb: cloneForm.memory_mb,
      network: cloneForm.network
    })
    ElMessage.info('克隆任务已提交，正在后台执行…')
    await pollTask(extractTaskId(res))
    ElMessage.success('虚拟机已创建，可在虚拟机列表查看')
    cloneDialog.value = false
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '创建虚拟机失败'))
  } finally {
    cloning.value = false
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
      ElMessage.error(errMsg(e, '删除失败'))
    }
  }
}

onMounted(() => {
  load()
  loadPools()
})
</script>

<style scoped>
.page-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-xl);
}
.page-title {
  margin: 0;
  font-size: 1.1rem;
  font-weight: 700;
}
.page-desc {
  color: var(--color-muted-foreground);
  font-size: 0.9rem;
}
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-xl);
}
.toolbar-left,
.toolbar-right {
  display: flex;
  align-items: center;
  gap: var(--space-lg);
}
.count {
  color: var(--color-muted-foreground);
  font-size: 0.9rem;
}
.ops {
  display: flex;
  align-items: center;
  gap: var(--space-md);
  flex-wrap: wrap;
}
.mono {
  font-family: var(--font-mono);
  font-size: 0.85rem;
}
.clone-tip {
  margin-bottom: var(--space-lg);
}
</style>