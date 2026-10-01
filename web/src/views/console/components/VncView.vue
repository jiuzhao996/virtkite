<template>
  <div ref="vncViewEl" class="vnc-view">
    <div v-if="!vm || vm.status !== 'running'" class="vnc-placeholder">
      <el-alert type="warning" :closable="false" show-icon
        :title="vm && vm.status === 'paused'
          ? 'VM 已暂停，无法连接图形控制台（VNC 需运行中）。可在此直接恢复，恢复后自动连接。'
          : 'VM 未运行，无法连接图形控制台（VNC 需运行中）。可在此直接开机，开机后自动连接。'" />
      <div class="vnc-placeholder-btns">
        <el-button type="primary" size="large" :loading="powerLoading" @click="powerOnAndConnect">
          {{ vm && vm.status === 'paused' ? '恢复并连接' : '一键开机并连接' }}
        </el-button>
        <el-button @click="$router.push('/vms')">去虚拟机列表</el-button>
      </div>
    </div>
    <div v-else-if="!vncUrl" class="vnc-placeholder">
      <el-button type="primary" size="large" :loading="vncLoading" @click="connectVNC">连接图形控制台</el-button>
      <p class="hint">noVNC 直连虚拟机虚拟显示，无需知道 IP。</p>
    </div>
    <div v-else class="vnc-frame">
      <div v-if="vncFrameLoading" class="vnc-loading" v-loading="true" element-loading-text="图形桌面加载中…" />
      <iframe :src="vncUrl" class="vnc" allow="fullscreen" title="noVNC 图形控制台" @load="onVncLoad" />
      <div class="vnc-bar">
        <span><el-icon><Monitor /></el-icon>图形控制台已连接</span>
        <el-tag v-if="vncViewOnly" type="warning" size="small" effect="dark">只读观看（键鼠已禁用）</el-tag>
        <div class="vnc-bar-btns">
          <el-button size="small" text @click="openVncNewWindow">新窗口打开</el-button>
          <el-button size="small" text @click="reconnectVNC">重新连接</el-button>
          <el-button size="small" text :title="isVncFull ? '退出全屏' : '图形控制台全屏'" @click="toggleVncFullscreen">
            <el-icon><FullScreen /></el-icon><span>{{ isVncFull ? '退出全屏' : '全屏' }}</span>
          </el-button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
// VNC 图形控制台视图：noVNC iframe + token 获取 + 未运行时的页内「开机/恢复并连接」闭环。
// 自 ConsolePage 拆出（2026-10 前端收敛批次），连接/全屏/开机轮询逻辑原样保留；
// vm 状态由壳持有传入，轮询发现状态变化经 vm-updated 回传壳同步（选择页徽标等共用）。
import { ref, onMounted, onUnmounted } from 'vue'
import { ElMessage } from 'element-plus'
import { FullScreen, Monitor } from '@element-plus/icons-vue'
import { api } from '../../../api'
import { errMsg } from '../../../utils/format'

const props = defineProps({
  vm: { type: Object, default: null },
  vmId: { type: [String, Number], required: true }
})
const emit = defineEmits(['vm-updated', 'request-reload'])

const vncLoading = ref(false)
const vncUrl = ref('')
const vncFrameLoading = ref(false)
// 只读角色以 noVNC view_only 模式打开：能看画面，键鼠输入禁用（后端 vnc-token 返回该标记）
const vncViewOnly = ref(false)
// 页内开机（VNC 未运行时闭环，不跳走）
const powerLoading = ref(false)
const isVncFull = ref(false)
const vncViewEl = ref(null)

let vncTimer = null
let powerCancelled = false

function onVncLoad() {
  if (vncTimer) { clearTimeout(vncTimer); vncTimer = null }
  vncFrameLoading.value = false
}

async function connectVNC() {
  vncLoading.value = true
  try {
    const res = await api.vncToken(props.vmId)
    const token = (res.data && res.data.token) || ''
    if (!token) throw new Error('token 为空')
    const host = window.location.hostname
    vncFrameLoading.value = true
    // 只读角色由后端返回 view_only=true：noVNC 侧禁用键鼠输入，
    // 使「只读运维」名副其实（VNC 协议本身没有只读模式，必须在客户端关掉输入）
    vncViewOnly.value = !!(res.data && res.data.view_only)
    const viewOnlyParam = vncViewOnly.value ? '&view_only=1' : ''
    // HTTPS 部署（如 https://kpyun.fun）下 http://host:6080 会被浏览器当混合内容拦截：
    // 改走同源 /vnc/ 前缀（云端 nginx 反代 websockify 并做 wss 升级），本地 http 直连 6080 行为不变
    const isHttps = window.location.protocol === 'https:'
    const vncBase = isHttps ? `${window.location.origin}/vnc` : `http://${host}:6080`
    const wsPath = isHttps ? 'vnc/websockify' : 'websockify'
    vncUrl.value = `${vncBase}/vnc.html?autoconnect=1&resize=scale${viewOnlyParam}&path=${wsPath}?token=${token}`
    // 兜底：iframe onload 失败时 15s 后关闭 loading，避免无限转圈（onVncLoad 会清掉）
    if (vncTimer) clearTimeout(vncTimer)
    vncTimer = setTimeout(() => { vncFrameLoading.value = false; vncTimer = null }, 15000)
  } catch (e) {
    ElMessage.error(errMsg(e, '获取控制台失败'))
  } finally {
    vncLoading.value = false
  }
}

function openVncNewWindow() {
  if (vncUrl.value) window.open(vncUrl.value, '_blank')
}

// 重新连接 VNC：不再只是清空 URL 停在占位页，直接重取 token 重建连接
async function reconnectVNC() {
  vncUrl.value = ''
  vncFrameLoading.value = false
  if (vncTimer) { clearTimeout(vncTimer); vncTimer = null }
  await connectVNC()
}

// 页内一键开机/恢复并自动连接 VNC：指令 → 轮询状态至 running（最长 ~60s）→ 自动 connectVNC
async function powerOnAndConnect() {
  const paused = !!(props.vm && props.vm.status === 'paused')
  powerLoading.value = true
  powerCancelled = false
  try {
    await (paused ? api.resumeVM(props.vmId) : api.startVM(props.vmId))
    ElMessage.success(paused ? '恢复指令已发送，等待虚拟机启动…' : '开机指令已发送，等待虚拟机启动…')
    const deadline = Date.now() + 60000
    while (Date.now() < deadline) {
      if (powerCancelled) return // 中途切走/卸载：停止轮询
      await new Promise((r) => setTimeout(r, 2000))
      if (powerCancelled) return
      try {
        const res = await api.getVM(props.vmId)
        if (res.data) emit('vm-updated', res.data)
        if (res.data && res.data.status === 'running') {
          ElMessage.success('虚拟机已启动，正在连接图形控制台…')
          await connectVNC()
          return
        }
      } catch (e) {
        // 轮询失败继续
      }
    }
    if (powerCancelled) return
    ElMessage.warning('等待超时，请确认虚拟机状态后手动连接')
    emit('request-reload')
  } catch (e) {
    ElMessage.error(errMsg(e, '开机失败'))
  } finally {
    powerLoading.value = false
  }
}

// ---------- 全屏（Fullscreen API，只管本视图根元素） ----------
function exitFullscreenIfAny() {
  if (document.fullscreenElement) {
    document.exitFullscreen().catch(() => { /* 用户已退出等场景忽略 */ })
    return true
  }
  return false
}
function requestFullscreen(el) {
  if (!el || !el.requestFullscreen) {
    ElMessage.warning('当前浏览器不支持全屏')
    return
  }
  el.requestFullscreen().catch(() => ElMessage.warning('进入全屏失败'))
}
function toggleVncFullscreen() {
  if (!exitFullscreenIfAny()) requestFullscreen(vncViewEl.value)
}
function onFullscreenChange() {
  const fs = document.fullscreenElement
  isVncFull.value = !!fs && fs === vncViewEl.value
}

onMounted(() => {
  document.addEventListener('fullscreenchange', onFullscreenChange)
})
onUnmounted(() => {
  document.removeEventListener('fullscreenchange', onFullscreenChange)
  powerCancelled = true
  if (vncTimer) { clearTimeout(vncTimer); vncTimer = null }
})
</script>

<style scoped>
/* 行内图标统一微调（图标一律用组件，禁 emoji）：el-icon 按基线对齐与中文混排偏高，下压 0.15em */
.vnc-bar > span .el-icon {
  vertical-align: -0.15em;
  margin-right: 4px;
}

.vnc-view {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 20px;
  background: var(--color-background);
  min-height: 0;
}
.vnc-placeholder { text-align: center; color: var(--color-foreground); }
.vnc-placeholder-btns {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  margin-top: 16px;
}
.vnc-placeholder .hint { margin-top: 14px; font-size: 0.85rem; color: var(--term-text-dim); }
.vnc-frame { position: relative; width: 100%; height: 100%; display: flex; flex-direction: column; }
.vnc-loading {
  position: absolute;
  inset: 0;
  z-index: 2;
  border-radius: var(--radius-md);
  background: var(--color-background);
}
.vnc {
  flex: 1;
  width: 100%;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: #000; /* 远程桌面画布恒黑（视频底色，与主题无关） */
}
.vnc-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 4px 0;
  color: var(--color-foreground);
  font-size: 0.85rem;
}
.vnc-bar-btns {
  display: flex;
  align-items: center;
  gap: 4px;
}
</style>
