<template>
  <div>
    <div class="page-head">
      <div>
        <h2 class="page-title">Docker 管理</h2>
        <p class="page-desc">容器 / 镜像 / 网络 / 卷 / 编排一体化管理（对标 1Panel 容器页）</p>
      </div>
    </div>

    <!-- 503 门控：Docker 守护进程不可用时顶部 alert，子页内容 v-show 隐藏（KeepAlive 下实例保留，重试直接调其 refresh） -->
    <el-alert v-if="backendError" type="error" :title="backendError" show-icon :closable="false" class="docker-gate">
      <div class="docker-gate-desc">
        <span>安装并启动 Docker 后可用（systemctl enable --now docker）</span>
        <el-button size="small" type="primary" plain :loading="refreshing" @click="retry">重试</el-button>
      </div>
    </el-alert>

    <!-- 顶部按钮条导航（1Panel RouterButton 模式）：点击切子路由，当前路由按钮高亮；刷新永远作用于当前子页 -->
    <div class="docker-nav">
      <el-button
        v-for="item in NAV_ITEMS"
        :key="item.path"
        class="docker-nav-btn"
        :class="{ 'is-current': route.path === item.path }"
        @click="router.push(item.path)"
      >{{ item.label }}</el-button>
      <el-button class="docker-nav-btn docker-refresh" :icon="Refresh" :loading="refreshing" @click="refreshCurrent">刷新</el-button>
    </div>

    <!-- 子路由出口：KeepAlive 缓存五个子页（切走不丢筛选/勾选状态）；v-show 由 503 门控控制显隐 -->
    <div v-show="!backendError" class="docker-view">
      <el-card shadow="never">
        <router-view v-slot="{ Component }">
          <keep-alive>
            <component :is="Component" ref="viewRef" />
          </keep-alive>
        </router-view>
      </el-card>
    </div>
  </div>
</template>

<script setup>
// Docker 管理布局壳（1Panel 式子路由页面组，原「单页五 tab」改造）：
// 只持页头、顶部按钮条导航、503 门控与子页刷新转发；五个子页（components/*Tab.vue）各自取数自治。
// 壳与子页经 provide('dockerPage') 通信：子页取数失败上报（503 置门控 alert，其余 toast）、成功清门控。
import { ref, provide, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { errMsg } from '../../utils/format'

const route = useRoute()
const router = useRouter()

// 子路由按钮条（与 router/index.js 的 docker children 一一对应；'' 重定向到 containers）
const NAV_ITEMS = [
  { label: '容器', path: '/docker/containers' },
  { label: '镜像', path: '/docker/images' },
  { label: '网络', path: '/docker/networks' },
  { label: '卷', path: '/docker/volumes' },
  { label: '编排', path: '/docker/compose' }
]

// HTTP 503（Docker 守护进程不可用）时置为后端 message，页面 alert 展示并隐藏子页；任一子页加载成功后清空
const backendError = ref('')

// 壳 ↔ 子页通信：子页 inject('dockerPage') 后上报取数结果，门控语义与拆分前完全一致
function reportLoadError(e, fallback = '获取 Docker 数据失败') {
  if (e && e.response && e.response.status === 503) {
    backendError.value = errMsg(e, 'Docker 服务不可用')
  } else {
    ElMessage.error(errMsg(e, fallback))
  }
}

function clearLoadError() {
  backendError.value = ''
}

provide('dockerPage', { reportLoadError, clearLoadError })

// 子页模板引用：当前激活子页的实例（KeepAlive 下实例常驻，v-show 隐藏后 ref 仍可用）
const viewRef = ref(null)
const refreshing = ref(false)

// 顶部刷新：重拉当前子页（子页 refresh 自持 loading 与错误上报）
async function refreshCurrent() {
  if (refreshing.value) return
  refreshing.value = true
  try {
    await viewRef.value?.refresh?.()
  } finally {
    refreshing.value = false
  }
}

// 503 重试：清门控让子页重新可见，再强制重拉（KeepAlive 复用实例不会触发 onMounted，必须显式 refresh）
async function retry() {
  backendError.value = ''
  await nextTick()
  await refreshCurrent()
}
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
/* 1Panel RouterButton 模式：顶部按钮条 + 当前路由按钮反色高亮 */
.docker-nav {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.docker-nav-btn.is-current {
  background: var(--el-color-primary, #409eff);
  border-color: var(--el-color-primary, #409eff);
  color: #fff;
}
.docker-nav-btn.is-current:hover {
  background: var(--el-color-primary-light-3, #79bbff);
  border-color: var(--el-color-primary-light-3, #79bbff);
  color: #fff;
}
.docker-refresh {
  margin-left: auto;
}
</style>
