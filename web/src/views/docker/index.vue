<template>
  <div>
    <div class="page-head">
      <div>
        <h2 class="page-title">Docker 管理</h2>
        <p class="page-desc">容器 / 镜像 / 网络 / 卷 / 编排一体化管理（对标 1Panel 容器页）</p>
      </div>
    </div>

    <!-- 后端 Docker 不可用（HTTP 503）时的引导提示 -->
    <el-alert
      v-if="backendError"
      type="error"
      :title="backendError"
      description="安装并启动 Docker 后可用（systemctl enable --now docker）"
      show-icon
      :closable="false"
      style="margin-bottom: 12px"
    />

    <el-card shadow="never">
      <!-- 顶部：tab 导航 + 刷新（刷新永远作用于当前 tab） -->
      <div class="docker-top">
        <el-tabs v-model="tab" class="docker-tabs" @tab-change="onTabChange">
          <!-- ═══════ 容器 ═══════ -->
          <el-tab-pane label="容器" name="containers">
            <ContainerTab
              ref="containerTabRef"
              v-model:auto-refresh="autoRefresh"
              :loading="loading"
              :images="images"
              :ensure-images="ensureImagesLoaded"
              @created="onContainerCreated"
            />
          </el-tab-pane>

          <!-- ═══════ 镜像 ═══════ -->
          <el-tab-pane label="镜像" name="images">
            <ImageTab :images="images" :loading="loading" :reload-tab="reloadTab" />
          </el-tab-pane>

          <!-- ═══════ 网络 ═══════ -->
          <el-tab-pane label="网络" name="networks">
            <NetworkTab ref="networkTabRef" :loading="loading" :reload-tab="reloadTab" />
          </el-tab-pane>

          <!-- ═══════ 卷 ═══════ -->
          <el-tab-pane label="卷" name="volumes">
            <VolumeTab ref="volumeTabRef" :loading="loading" :reload-tab="reloadTab" />
          </el-tab-pane>

          <!-- ═══════ 编排 ═══════ -->
          <el-tab-pane label="编排" name="compose">
            <ComposeTab ref="composeTabRef" :loading="loading" :reload-tab="reloadTab" />
          </el-tab-pane>
        </el-tabs>
        <el-button class="docker-refresh" :icon="Refresh" :loading="loading" @click="reload">刷新</el-button>
      </div>
    </el-card>
  </div>
</template>

<script setup>
// Docker 管理页薄壳：只持页面级编排（tab 惰性加载 loadedTabs/loadTab、共享 loading、
// Docker 可用性 backendError、images 跨 tab 数据、10s 静默轮询）。
// 五个 tab 的数据与交互内聚在 ./components/*Tab.vue；容器详情/日志抽屉是 ContainerTab 的独立子组件。
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import http from '../../api'
import { errMsg } from '../../utils/format'
import { useAutoRefresh } from '../../composables/useAutoRefresh'
import ContainerTab from './components/ContainerTab.vue'
import ImageTab from './components/ImageTab.vue'
import NetworkTab from './components/NetworkTab.vue'
import VolumeTab from './components/VolumeTab.vue'
import ComposeTab from './components/ComposeTab.vue'

// ═══════════════ Tab 骨架与按需加载 ═══════════════
// 首次进入某 tab 才拉取对应数据；右上刷新按钮强制重拉当前 tab。
const tab = ref('containers')
const loading = ref(false)
// 懒加载标记必须从空数组起步：预置 'containers' 会让 onMounted 的 loadTab('containers')
// 被下方守卫直接 return，容器列表永远不自动加载（v3.2 引入，10s 轮询掩盖至今）。
const loadedTabs = ref([])
// HTTP 503（Docker 守护进程不可用）时置为后端 message，页面顶部 alert 展示；成功加载后清空
const backendError = ref('')

// 「自动刷新」开关：容器视图 10s 轮询的总开关，记忆到 localStorage（默认开）。
// 定时器启停与持久化统一交 useAutoRefresh 托管：关闭即整体停表；
// runOnEnable=false 保持既有行为——打开开关不立即请求，等下一个 10s 周期。
const { enabled: autoRefresh, start: startPolling } = useAutoRefresh(silentRefresh, {
  storageKey: 'vmops-docker-autorefresh',
  intervalMs: 10000,
  runOnEnable: false
})

// images 单独留在壳：除镜像 tab 展示外，「创建容器」抽屉的镜像候选下拉（ContainerTab）也消费同一份
const images = ref([])

// 各 tab 子组件引用：容器/网络/卷/编排的取数内聚在子组件，壳经模板 ref 调其 refresh()
const containerTabRef = ref(null)
const networkTabRef = ref(null)
const volumeTabRef = ref(null)
const composeTabRef = ref(null)

function handleLoadError(e, fallback = '获取 Docker 数据失败') {
  if (e.response && e.response.status === 503) {
    backendError.value = errMsg(e, 'Docker 服务不可用')
  } else {
    ElMessage.error(errMsg(e, fallback))
  }
}

// 各 tab 取数逻辑已下沉子组件：容器/网络/卷/编排经 ref 调 refresh()；镜像留壳（跨 tab 共享）。
// 惰性加载/共享 loading/成功清 backendError/失败 handleLoadError 的语义与拆分前完全一致。
async function loadTab(name, { force = false } = {}) {
  if (!force && loadedTabs.value.includes(name)) return
  loading.value = true
  try {
    if (name === 'containers') await containerTabRef.value.refresh()
    else if (name === 'images') await fetchImages()
    else if (name === 'networks') await networkTabRef.value.refresh()
    else if (name === 'volumes') await volumeTabRef.value.refresh()
    else if (name === 'compose') await composeTabRef.value.refresh()
    backendError.value = ''
    if (!loadedTabs.value.includes(name)) loadedTabs.value.push(name)
  } catch (e) {
    handleLoadError(e)
  } finally {
    loading.value = false
  }
}

function onTabChange(name) {
  loadTab(name)
}

// 顶部刷新：重拉当前 tab
function reload() {
  loadTab(tab.value, { force: true })
}

async function fetchImages() {
  const res = await http.get('/docker/images')
  images.value = (res.data.data || {}).items || []
}

// 容器视图 10s 静默轮询：列表 + stats 一起刷；不动 loading，失败不打扰用户（503 时同步顶部 alert）
// 「自动刷新」开关关闭时定时器整体停表（useAutoRefresh 托管），此处只需守住 tab/并发/loading
let refreshing = false
async function silentRefresh() {
  if (tab.value !== 'containers' || refreshing || loading.value) return
  refreshing = true
  try {
    await containerTabRef.value.refresh()
    backendError.value = ''
  } catch (e) {
    if (e.response && e.response.status === 503) {
      backendError.value = errMsg(e, 'Docker 服务不可用')
    }
  } finally {
    refreshing = false
  }
}

// 创建容器抽屉打开前：镜像 tab 未加载过则补拉一次（fire-and-forget），让镜像下拉有候选
function ensureImagesLoaded() {
  if (!loadedTabs.value.includes('images')) loadTab('images')
}

// 创建容器成功：跳回容器 tab 并强制重拉列表 + stats（loadTab 内部即 Promise.all 两路；入口按钮只在容器 tab，tab 赋值属双保险）
function onContainerCreated() {
  tab.value = 'containers'
  loadTab('containers', { force: true })
}

// 子 tab 操作成功后的强制重拉：即原 loadTab(name, { force: true })，返回 Promise 供子组件 await
function reloadTab(name) {
  return loadTab(name, { force: true })
}

onMounted(() => {
  loadTab('containers')
  startPolling()
})
// 容器视图 10s 轮询定时器由 useAutoRefresh 在卸载时自动清理；日志跟随 2s 定时器由 ContainerLogsDrawer 自行托管
</script>

<style scoped>
.docker-top {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}
.docker-tabs {
  flex: 1;
  min-width: 0;
}
.docker-tabs :deep(.el-tabs__header) {
  margin-bottom: 16px;
}
.docker-refresh {
  flex: none;
  margin-top: 2px;
}
</style>
