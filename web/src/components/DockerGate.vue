<template>
  <div>
    <!-- 503 门控（原 DockerPage 壳职责，Docker 资源分散后四处复用）：
         Docker 守护进程不可用时顶部 alert + 隐藏内容区；重试交由父页（调子组件 refresh） -->
    <el-alert v-if="error" type="error" :title="error" show-icon :closable="false" class="docker-gate">
      <div class="docker-gate-desc">
        <span>安装并启动 Docker 后可用（systemctl enable --now docker）</span>
        <el-button size="small" type="primary" plain :loading="retrying" @click="onRetry">重试</el-button>
      </div>
    </el-alert>
    <div v-show="!error">
      <slot />
    </div>
  </div>
</template>

<script setup>
// Docker 503 门控包装：向插槽内的 docker 子组件提供与原 DockerPage 壳完全一致的
// inject('dockerPage') 契约（子页取数失败上报 → 503 置门控，其余 toast；成功清门控），
// 五个 docker tab 组件零改动即可在任意落点页工作。重试语义：清门控 + emit('retry')，
// 父页接到后调子组件暴露的 refresh()（KeepAlive/常驻实例不会重跑 onMounted）。
import { ref, provide } from 'vue'
import { ElMessage } from 'element-plus'
import { errMsg } from '../utils/format'

const emit = defineEmits(['retry'])

const error = ref('')
const retrying = ref(false)

function reportLoadError(e, fallback = '获取 Docker 数据失败') {
  if (e && e.response && e.response.status === 503) {
    error.value = errMsg(e, 'Docker 服务不可用')
  } else {
    ElMessage.error(errMsg(e, fallback))
  }
}

function clearLoadError() {
  error.value = ''
}

provide('dockerPage', { reportLoadError, clearLoadError })

async function onRetry() {
  error.value = ''
  retrying.value = true
  try {
    emit('retry')
  } finally {
    retrying.value = false
  }
}

defineExpose({ clearLoadError })
</script>

<style scoped>
.docker-gate {
  margin-bottom: 12px;
}
.docker-gate-desc {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
</style>
