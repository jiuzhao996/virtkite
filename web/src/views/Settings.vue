<template>
  <div v-loading="loading">
    <div class="page-head">
      <h2 class="page-title">系统设置</h2>
      <el-button :icon="Refresh" :loading="loading" circle text @click="load" />
    </div>

    <el-row :gutter="16">
      <!-- 平台 -->
      <el-col :xs="24" :md="12" class="mb">
        <el-card shadow="never">
          <template #header><span class="card-title">平台</span></template>
          <el-descriptions :column="1" size="small" border>
            <el-descriptions-item label="版本">{{ g('platform.version') }}</el-descriptions-item>
            <el-descriptions-item label="运行模式">{{ g('platform.server_mode') }}</el-descriptions-item>
            <el-descriptions-item label="监听端口">{{ g('platform.server_port') }}</el-descriptions-item>
            <el-descriptions-item label="后端时间">{{ g('platform.time') }}</el-descriptions-item>
          </el-descriptions>
        </el-card>
      </el-col>
      <!-- 虚拟化 -->
      <el-col :xs="24" :md="12" class="mb">
        <el-card shadow="never">
          <template #header><span class="card-title">虚拟化</span></template>
          <el-descriptions :column="1" size="small" border>
            <el-descriptions-item label="libvirt URI">{{ g('virt.libvirt_uri') }}</el-descriptions-item>
          </el-descriptions>
        </el-card>
      </el-col>
      <!-- 存储 -->
      <el-col :xs="24" :md="12" class="mb">
        <el-card shadow="never">
          <template #header><span class="card-title">存储</span></template>
          <el-descriptions :column="1" size="small" border>
            <el-descriptions-item label="镜像目录">{{ g('storage.image_dir') }}</el-descriptions-item>
            <el-descriptions-item label="seed 目录">{{ g('storage.seed_dir') }}</el-descriptions-item>
            <el-descriptions-item label="存储池">{{ arr('storage.pools').join('、') || '—' }}</el-descriptions-item>
          </el-descriptions>
        </el-card>
      </el-col>
      <!-- 网络 -->
      <el-col :xs="24" :md="12" class="mb">
        <el-card shadow="never">
          <template #header><span class="card-title">网络</span></template>
          <el-descriptions :column="1" size="small" border>
            <el-descriptions-item label="虚拟网络">{{ arr('network.networks').join('、') || '—' }}</el-descriptions-item>
          </el-descriptions>
        </el-card>
      </el-col>
      <!-- 任务与会话 -->
      <el-col :xs="24" :md="12" class="mb">
        <el-card shadow="never">
          <template #header><span class="card-title">任务与会话</span></template>
          <el-descriptions :column="1" size="small" border>
            <el-descriptions-item label="任务 worker 数">{{ g('tasks.workers') }}</el-descriptions-item>
            <el-descriptions-item label="任务队列缓冲">{{ g('tasks.queue_buffer') }}</el-descriptions-item>
            <el-descriptions-item label="VNC 会话过期">{{ g('sessions.vnc_stale_min') }} 分钟无解析</el-descriptions-item>
          </el-descriptions>
        </el-card>
      </el-col>
      <!-- 前端轮询偏好（本机浏览器生效） -->
      <el-col :xs="24" :md="12" class="mb">
        <el-card shadow="never">
          <template #header>
            <div class="card-head">
              <span class="card-title">前端轮询偏好</span>
              <el-button size="small" type="primary" @click="savePoll">保存</el-button>
            </div>
          </template>
          <el-form label-width="140px" size="small">
            <el-form-item v-for="(label, key) in POLL_LABELS" :key="key" :label="label">
              <el-input-number v-model="pollForm[key]" :min="1000" :max="60000" :step="1000" controls-position="right" />
              <span class="unit">毫秒</span>
            </el-form-item>
          </el-form>
          <p class="tip">保存在本机浏览器，下次进入页面生效（1000–60000ms）。</p>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { api } from '../api'
import { POLL_DEFAULTS, POLL_LABELS, getPollInterval, setPollInterval } from '../utils/settings'

const loading = ref(false)
const data = ref({})

function g(path) {
  return path.split('.').reduce((o, k) => (o && o[k] !== undefined ? o[k] : null), data.value) ?? '—'
}
// 数组安全取值（数据未到时 g() 回 '—'，直接 .join 会抛 TypeError）
function arr(path) {
  const v = path.split('.').reduce((o, k) => (o && o[k] !== undefined ? o[k] : null), data.value)
  return Array.isArray(v) ? v : []
}

const pollForm = reactive({})
function loadPollForm() {
  for (const key of Object.keys(POLL_DEFAULTS)) {
    pollForm[key] = getPollInterval(key, POLL_DEFAULTS[key])
  }
}
function savePoll() {
  for (const [key, val] of Object.entries(pollForm)) {
    pollForm[key] = setPollInterval(key, val)
  }
  ElMessage.success('已保存，下次进入页面生效')
}

async function load() {
  loading.value = true
  try {
    const res = await api.getSettings()
    data.value = res.data || {}
  } catch (e) {
    ElMessage.error('获取系统设置失败')
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  loadPollForm()
  load()
})
</script>

<style scoped>
/* .page-head / .page-title 已收进 global.css */
.mb {
  margin-bottom: 16px;
}
.card-title {
  font-weight: 600;
}
.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.unit {
  margin-left: 8px;
  color: var(--color-muted-foreground);
  font-size: 0.82rem;
}
.tip {
  color: var(--color-muted-foreground);
  font-size: 0.82rem;
  margin: 4px 0 0;
}
</style>
