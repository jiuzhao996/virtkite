<template>
  <div class="fb">
    <!-- SSH 连接表单：凭据仅内存透传给后端（不落盘），刷新即失 -->
    <el-form inline class="fb-conn">
      <el-form-item label="IP">
        <el-input v-model="conn.host" placeholder="虚拟机 IP" style="width: 150px" />
      </el-form-item>
      <el-form-item label="端口">
        <el-input-number v-model="conn.port" :min="1" :max="65535" controls-position="right" style="width: 100px" />
      </el-form-item>
      <el-form-item label="用户">
        <el-input v-model="conn.user" placeholder="root" style="width: 120px" />
      </el-form-item>
      <el-form-item label="密码">
        <el-input v-model="conn.password" type="password" show-password placeholder="SSH 密码" style="width: 160px" />
      </el-form-item>
      <el-form-item>
        <el-button type="primary" :loading="connecting" @click="connect">连接</el-button>
      </el-form-item>
    </el-form>
    <el-alert
      type="info" :closable="false" class="fb-tip"
      title="通过 SSH 在线浏览虚拟机内的文件（适合文本/配置文件：查看、下载、上传、删除、建目录）。凭据仅本次会话内存使用，不会保存。"
    />

    <template v-if="connected">
      <!-- 路径面包屑 -->
      <div class="fb-crumb">
        <el-breadcrumb separator="/">
          <el-breadcrumb-item v-for="(seg, i) in crumbs" :key="i">
            <el-link :underline="false" @click="goTo(seg.path)">{{ seg.label }}</el-link>
          </el-breadcrumb-item>
        </el-breadcrumb>
        <div class="fb-tools">
          <el-button size="small" :icon="Refresh" @click="load">刷新</el-button>
          <el-button size="small" :icon="FolderAdd" @click="mkdir">新建目录</el-button>
          <el-button size="small" type="primary" plain :icon="Upload" @click="pickUpload">上传文件</el-button>
          <input ref="uploadInput" type="file" class="fb-upload-input" @change="doUpload" />
        </div>
      </div>

      <el-table :data="items" stripe border size="small" style="width: 100%" v-loading="loading" @row-dblclick="openRow">
        <template #empty><el-empty description="目录为空" :image-size="60" /></template>
        <el-table-column label="名称" min-width="240">
          <template #default="{ row }">
            <el-link :underline="false" class="fb-name" @click="row.is_dir ? enter(row) : download(row)">
              <el-icon style="vertical-align: -2px; margin-right: 4px">
                <Folder v-if="row.is_dir" />
                <Document v-else />
              </el-icon>
              {{ row.name }}
            </el-link>
          </template>
        </el-table-column>
        <el-table-column label="大小" width="110">
          <template #default="{ row }">{{ row.is_dir ? '—' : sizeText(row.size) }}</template>
        </el-table-column>
        <el-table-column prop="mode" label="权限" width="120" />
        <el-table-column label="修改时间" width="170">
          <template #default="{ row }">{{ timeText(row.modified) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="150" fixed="right">
          <template #default="{ row }">
            <el-button v-if="!row.is_dir" size="small" :icon="Download" @click="download(row)">下载</el-button>
            <el-button size="small" type="danger" :icon="Delete" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </template>
  </div>
</template>

<script setup>
import { reactive, ref, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, FolderAdd, Upload, Folder, Document, Download, Delete } from '@element-plus/icons-vue'
import http from '../api'
import { errMsg } from '../utils/format'

const props = defineProps({
  id: { type: Number, required: true },
  ip: { type: String, default: '' }
})

const conn = reactive({ host: props.ip || '', port: 22, user: 'root', password: '' })
const connecting = ref(false)
const connected = ref(false)
const loading = ref(false)
const path = ref('/root')
const items = ref([])
const uploadInput = ref(null)

const crumbs = computed(() => {
  const list = [{ label: '/', path: '/' }]
  let acc = ''
  for (const seg of path.value.split('/').filter(Boolean)) {
    acc += '/' + seg
    list.push({ label: seg, path: acc })
  }
  return list
})

function creds() {
  return { host: conn.host, port: conn.port, user: conn.user, password: conn.password }
}

async function connect() {
  if (!conn.host || !conn.password) {
    ElMessage.warning('请填写 IP 与 SSH 密码')
    return
  }
  connecting.value = true
  try {
    await http.post(`/vms/${props.id}/files/list`, { ...creds(), path: '/root' })
    connected.value = true
    path.value = '/root'
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, 'SSH 连接失败'))
  } finally {
    connecting.value = false
  }
}

async function load() {
  loading.value = true
  try {
    const res = await http.post(`/vms/${props.id}/files/list`, { ...creds(), path: path.value })
    items.value = (res.data && res.data.data && res.data.data.items) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '读取目录失败'))
  } finally {
    loading.value = false
  }
}

function enter(row) {
  if (!row.is_dir) return
  const p = path.value === '/' ? '/' + row.name : path.value + '/' + row.name
  path.value = p
  load()
}

function goTo(p) {
  path.value = p
  load()
}

function openRow(row) {
  row.is_dir ? enter(row) : download(row)
}

async function download(row) {
  try {
    const res = await http.post(`/vms/${props.id}/files/download`, { ...creds(), path: join(row.name) })
    // 该接口直接返回文件内容（octet-stream）；适合文本/配置文件
    const blob = new Blob([res.data], { type: 'application/octet-stream' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = row.name
    a.click()
    URL.revokeObjectURL(a.href)
  } catch (e) {
    ElMessage.error(errMsg(e, '下载失败'))
  }
}

function pickUpload() {
  uploadInput.value && uploadInput.value.click()
}

async function doUpload(ev) {
  const file = ev.target.files && ev.target.files[0]
  if (!file) return
  if (file.size > 4 * 1024 * 1024) {
    ElMessage.warning('单文件建议不超过 4 MB（SSH 文本通道）')
    return
  }
  const reader = new FileReader()
  reader.onload = async () => {
    try {
      const base64 = String(reader.result).split(',')[1] || ''
      await http.post(`/vms/${props.id}/files/upload`, { ...creds(), path: join(file.name), content: base64 })
      ElMessage.success('已上传：' + file.name)
      await load()
    } catch (e) {
      ElMessage.error(errMsg(e, '上传失败'))
    }
  }
  reader.readAsDataURL(file)
  ev.target.value = ''
}

async function remove(row) {
  try {
    await ElMessageBox.confirm(`确定删除「${row.name}」？目录将递归删除，不可恢复。`, '删除', { type: 'warning' })
  } catch { return }
  try {
    await http.post(`/vms/${props.id}/files/delete`, { ...creds(), paths: [join(row.name)] })
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

async function mkdir() {
  let name = ''
  try {
    const r = await ElMessageBox.prompt('新目录名称', '新建目录', { inputPattern: /\S+/, inputErrorMessage: '名称不能为空' })
    name = (r.value || '').trim()
  } catch { return }
  try {
    await http.post(`/vms/${props.id}/files/mkdir`, { ...creds(), path: join(name) })
    ElMessage.success('已创建')
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '创建失败'))
  }
}

function join(name) {
  return path.value === '/' ? '/' + name : path.value + '/' + name
}

function sizeText(n) {
  const v = Number(n || 0)
  if (v < 1024) return v + ' B'
  if (v < 1024 * 1024) return (v / 1024).toFixed(1) + ' KB'
  if (v < 1024 ** 3) return (v / 1024 ** 2).toFixed(1) + ' MB'
  return (v / 1024 ** 3).toFixed(1) + ' GB'
}

function timeText(sec) {
  const n = Number(sec || 0)
  if (!n) return '—'
  return new Date(n * 1000).toLocaleString('zh-CN', { hour12: false })
}
</script>

<style scoped>
.fb-conn { margin-bottom: 8px; }
.fb-tip { margin-bottom: 12px; }
.fb-crumb { display: flex; align-items: center; justify-content: space-between; margin-bottom: 10px; gap: 12px; flex-wrap: wrap; }
.fb-tools { display: flex; gap: 8px; align-items: center; }
.fb-upload-input { display: none; }
.fb-name { display: inline-flex; align-items: center; }
</style>
