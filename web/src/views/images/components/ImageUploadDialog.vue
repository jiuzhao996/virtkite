<template>
  <!-- 上传镜像 -->
  <el-dialog :close-on-click-modal="false" v-model="visible" title="上传镜像" width="500px">
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
          accept=".qcow2,.raw,.vmdk"
        >
          <el-icon class="el-icon--upload"><UploadFilled /></el-icon>
          <div class="el-upload__text">拖入文件或 <em>点击选择</em></div>
        </el-upload>
      </el-form-item>
      <!-- 上传进度：镜像可达数 GB，无进度条时用户无法判断是否卡死 -->
      <el-form-item v-if="uploading" label="进度">
        <el-progress
          :percentage="uploadPct"
          :stroke-width="10"
          :format="(p) => (p >= 100 ? '服务端写入中…' : p + '%')"
          style="width: 100%"
        />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="uploading" :disabled="!file" @click="emit('upload')">上传</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
// 镜像页「上传镜像」弹窗：名称/OS/存储池/是否模板 + 文件拖拽选择 + 上传进度。
// form 为页面持有的对象（直接改字段）；所选文件经 v-model:file 双向绑定页面 file 状态，
// 由页面 upload() 提交（本组件不自建请求）。暴露 clearFiles()，供页面 openUpload 复位组件内文件列表。
import { ref } from 'vue'
import { UploadFilled } from '@element-plus/icons-vue'

defineProps({
  // 页面表单对象 { name, os_version, is_template, pool }
  form: { type: Object, required: true },
  // 存储池下拉候选（页面 loadPools 产出）
  poolOptions: { type: Array, default: () => [] },
  uploading: { type: Boolean, default: false },
  uploadPct: { type: Number, default: 0 }
})
const emit = defineEmits(['upload'])
const visible = defineModel({ type: Boolean, default: false })    // 父级 v-model="dialog"
const file = defineModel('file', { type: Object, default: null })  // 父级 v-model:file="file"（原始 File）

const uploadRef = ref(null)

function onFileChange(uploadFile) {
  file.value = uploadFile.raw
}
function onFileRemove() {
  file.value = null
}

// 暴露给页面：openUpload 里清空 el-upload 遗留文件列表（limit=1 下否则无法再选新文件）
defineExpose({ clearFiles: () => uploadRef.value?.clearFiles() })
</script>
