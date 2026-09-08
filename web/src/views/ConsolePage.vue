<template>
  <div class="console-page" v-loading="loading">
    <!-- 顶部条 -->
    <div class="topbar">
      <el-button text class="back" @click="$router.push('/vms')">
        <el-icon><ArrowLeft /></el-icon><span>返回</span>
      </el-button>
      <div class="vm-info">
        <span class="vm-name">{{ vm ? vm.name : '...' }}</span>
        <el-tag v-if="vm" :type="vm.status === 'running' ? 'success' : 'info'" effect="dark" size="small">
          {{ vm.status === 'running' ? '运行中' : '已关机' }}
        </el-tag>
      </div>
      <div class="topbar-tip">
        <span v-if="view === 'vnc'"><el-icon><Monitor /></el-icon>图形控制台 (VNC)</span>
        <span v-else-if="view === 'ssh'"><el-icon><Platform /></el-icon>Web 终端 (SSH)</span>
        <span v-else-if="view === 'serial'"><el-icon><Connection /></el-icon>串口 Console</span>
        <span v-else>选择连接方式</span>
      </div>
    </div>

    <div class="body">
      <!-- 可折叠侧边栏 -->
      <aside class="sidebar" :class="{ collapsed }">
        <button class="collapse-btn" :title="collapsed ? '展开侧边栏' : '收起侧边栏'" @click="collapsed = !collapsed">
          <el-icon v-if="collapsed"><ArrowRight /></el-icon>
          <el-icon v-else><ArrowLeft /></el-icon>
        </button>
        <button class="nav-item" :class="{ active: view === 'vnc' }" @click="select('vnc')">
          <el-icon class="icon"><Monitor /></el-icon><span v-if="!collapsed" class="label">图形控制台 (VNC)</span>
        </button>
        <button
          v-if="isAdmin"
          class="nav-item"
          :class="{ active: view === 'ssh' }"
          @click="select('ssh')"
        >
          <el-icon class="icon"><Platform /></el-icon><span v-if="!collapsed" class="label">Web 终端 (SSH)</span>
        </button>
        <button
          v-if="isAdmin"
          class="nav-item serial-item"
          :class="{ active: view === 'serial' }"
          @click="select('serial')"
        >
          <el-icon class="icon"><Connection /></el-icon><span v-if="!collapsed" class="label">串口 Console</span>
          <span v-if="!collapsed" class="rec">免IP</span>
        </button>
      </aside>

      <!-- 主区域 -->
      <main class="main">
        <!-- 白底选择页 -->
        <div v-if="!view" class="pick-panel">
          <h2 class="pick-title">选择连接方式</h2>
          <p class="pick-sub">选择一种方式进入「{{ vm ? vm.name : '虚拟机' }}」的控制台</p>
          <div class="cards">
            <div class="card" @click="select('vnc')">
              <div class="card-icon"><el-icon><Monitor /></el-icon></div>
              <div class="card-title">图形控制台 (VNC)</div>
              <div class="card-desc">noVNC 图形远程桌面，所见即所得。需 VM 运行中，无需 IP 与账号。</div>
              <div class="card-badge" :class="vm && vm.status === 'running' ? 'ok' : 'warn'">
                {{ vm && vm.status === 'running' ? '● 可用' : '● 需运行中' }}
              </div>
            </div>
            <div v-if="isAdmin" class="card" @click="select('ssh')">
              <div class="card-icon"><el-icon><Platform /></el-icon></div>
              <div class="card-title">Web 终端 (SSH)</div>
              <div class="card-desc">字符 SSH 终端（xterm.js），比 VNC 更顺滑。需 VM IP 与账号密码。</div>
              <div class="card-badge ok">● 需网络可达</div>
            </div>
            <div v-if="isAdmin" class="card serial" @click="select('serial')">
              <div class="card-icon"><el-icon><Connection /></el-icon></div>
              <div class="card-title">串口 Console</div>
              <div class="card-desc">免 IP 直连 VM 串口（virsh console），无网卡 / 未配置 IP 也能进系统。</div>
              <div class="card-badge" :class="serialUnavailable ? 'warn' : 'gold'">
                <template v-if="serialUnavailable">● 不可用：{{ serialReason }}</template>
                <template v-else><el-icon><StarFilled /></el-icon>先尝试它</template>
              </div>
            </div>
          </div>
          <p v-if="!isAdmin" class="pick-note">
            当前为只读角色：SSH 终端与串口 Console 会向虚拟机内部写入，已限定为管理员使用；
            图形控制台以只读模式打开（可查看画面，键鼠输入禁用）。
          </p>
        </div>

        <!-- VNC 图形控制台：浅色干净背景，无背景图 -->
        <div v-else-if="view === 'vnc'" class="vnc-view">
          <div v-if="!vm || vm.status !== 'running'" class="vnc-placeholder">
            <el-alert type="warning" :closable="false" show-icon
              title="VM 未运行，无法连接图形控制台（VNC 需运行中）。可在此直接开机，开机后自动连接。" />
            <div class="vnc-placeholder-btns">
              <el-button type="primary" size="large" :loading="powerLoading" @click="powerOnAndConnect">一键开机并连接</el-button>
              <el-button @click="$router.push('/vms')">去虚拟机列表</el-button>
            </div>
          </div>
          <div v-else-if="!vncUrl" class="vnc-placeholder">
            <el-button type="primary" size="large" :loading="vncLoading" @click="connectVNC">连接图形控制台</el-button>
            <p class="hint">noVNC 直连虚拟机虚拟显示，无需知道 IP。</p>
          </div>
          <div v-else class="vnc-frame">
            <div v-if="vncFrameLoading" class="vnc-loading" v-loading="true" element-loading-text="图形桌面加载中…" />
            <iframe :src="vncUrl" class="vnc" @load="onVncLoad" />
            <div class="vnc-bar">
              <span><el-icon><Monitor /></el-icon>图形控制台已连接</span>
              <el-tag v-if="vncViewOnly" type="warning" size="small" effect="dark">只读观看（键鼠已禁用）</el-tag>
              <div class="vnc-bar-btns">
                <el-button size="small" text @click="openVncNewWindow">新窗口打开</el-button>
                <el-button size="small" text @click="vncUrl = ''">重新连接</el-button>
              </div>
            </div>
          </div>
        </div>

        <!-- SSH / 串口 共用终端视图：深色 + console-bg.jpg 背景 -->
        <div v-else class="term-view" :style="{ backgroundImage: 'url(' + consoleBg + ')' }">
          <!-- 星星划过背景 -->
          <div class="stars-bg">
            <div v-for="n in 40" :key="n" class="star" :style="{
              left: Math.random() * 100 + '%',
              top: Math.random() * 100 + '%',
              animationDelay: Math.random() * 6 + 's',
              animationDuration: (2 + Math.random() * 4) + 's',
            }" />
          </div>

          <!-- JumpServer 风格顶部信息栏 -->
          <div class="term-header">
            <div class="term-header-left">
              <span class="term-logo"><el-icon><Lock /></el-icon>VMOps</span>
              <span class="term-divider">|</span>
              <span v-if="connected" class="term-status online">● 已连接</span>
              <span v-else class="term-status offline">○ 未连接</span>
            </div>
            <div class="term-header-center">
              <span class="term-user"><el-icon><User /></el-icon>当前用户：{{ currentUser }}</span>
              <span class="term-divider">|</span>
              <span class="term-host"><el-icon><Monitor /></el-icon>{{ hostLabel }}</span>
            </div>
            <div class="term-header-right">
              <span class="term-clock"><el-icon><Clock /></el-icon>{{ clock || '--' }}</span>
            </div>
          </div>

          <div v-if="termError" class="term-error"><el-icon><WarningFilled /></el-icon>{{ termError }}</div>

          <!-- SSH 连接表单 -->
          <div v-if="view === 'ssh' && !connected" class="ssh-form-wrap">
            <div class="ssh-form">
              <h3 class="form-title"><el-icon><Platform /></el-icon>SSH 连接</h3>
              <el-form label-width="70px">
                <el-form-item label="主机">
                  <el-input v-model="sshForm.host" placeholder="VM IP 或域名，默认取虚拟机 IP" />
                </el-form-item>
                <el-form-item label="端口">
                  <el-input-number v-model="sshForm.port" :min="1" :max="65535" controls-position="right" style="width: 100%" />
                </el-form-item>
                <el-form-item label="用户名">
                  <el-input v-model="sshForm.user" placeholder="如 root" />
                </el-form-item>
                <el-form-item label="密码">
                  <el-input v-model="sshForm.password" type="password" show-password @keyup.enter="connectSSH" />
                </el-form-item>
              </el-form>
              <el-button type="primary" class="form-btn" :loading="connecting" @click="connectSSH">连接终端</el-button>
            </div>
          </div>

          <!-- 串口连接面板：无表单，醒目入口 -->
          <div v-else-if="view === 'serial' && !connected" class="serial-panel">
            <div class="serial-big-icon"><el-icon><Connection /></el-icon></div>
            <div class="serial-title">串口 Console · 免 IP 直连</div>
            <div class="serial-desc">等价 virsh console，直接读写 guest 串口 ttyS0。无需 IP / 账号，无网卡也能进系统，建议优先尝试。</div>
            <el-button type="warning" size="large" class="serial-btn" :loading="connecting" @click="connectSerial">
              <el-icon v-if="!connecting"><CaretRight /></el-icon>
              <span>{{ connecting ? '连接中…' : '连接串口 Console' }}</span>
            </el-button>
          </div>

          <!-- 终端主体（SSH / 串口共用） -->
          <div v-else-if="connected" class="term-body">
            <div ref="termEl" class="terminal-container" />
            <!-- 倾斜水印 -->
            <div class="watermark">
              <div class="watermark-line">{{ vm ? vm.name : 'vm' }}</div>
              <div class="watermark-line watermark-sub">VirtKite console</div>
            </div>
          </div>

          <!-- 底部操作栏 -->
          <div class="term-footer">
            <div class="term-footer-left">
              <template v-if="connected">
                <el-button size="small" class="ft-btn" :loading="connecting" @click="reconnect">
                  <el-icon v-if="!connecting"><Refresh /></el-icon><span>重新连接</span>
                </el-button>
                <el-button size="small" class="ft-btn" @click="disconnectFromTerminal">断开</el-button>
              </template>
              <template v-else>
                <el-button size="small" class="ft-btn" @click="goBackToPick">
                  <el-icon><ArrowLeft /></el-icon><span>返回选择</span>
                </el-button>
              </template>
            </div>
            <div class="term-footer-right">
              <span class="text-muted"><el-icon><InfoFilled /></el-icon>鼠标选中复制，Ctrl+Shift+V 粘贴</span>
              <span class="term-size">{{ connected ? termSize : '--' }}</span>
            </div>
          </div>
        </div>
      </main>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
// 图标一律用组件（禁止 emoji 当图标）。main.js 已全量全局注册，这里仍显式 import：
// 一是模板里能看出图标来源，二是将来改按需引入不用回头翻模板。
import {
  ArrowLeft,
  ArrowRight,
  CaretRight,
  Clock,
  Connection,
  InfoFilled,
  Lock,
  Monitor,
  Platform,
  Refresh,
  StarFilled,
  User,
  WarningFilled,
} from '@element-plus/icons-vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { api, TOKEN_KEY } from '../api'
import { useAuth } from '../store/auth'
import consoleBg from '../assets/console-bg.jpg'

const route = useRoute()
const id = route.params.id
const auth = useAuth()
const { isAdmin } = auth

const vm = ref(null)
const loading = ref(true)
const collapsed = ref(false)   // 侧边栏折叠
const view = ref(null)         // 'vnc' | 'ssh' | 'serial' | null(选择页)

// VNC
const vncLoading = ref(false)
const vncUrl = ref('')
const vncFrameLoading = ref(false)
// 只读角色以 noVNC view_only 模式打开：能看画面，键鼠输入禁用（后端 vnc-token 返回该标记）
const vncViewOnly = ref(false)
// 页内开机（VNC 未运行时闭环，不跳走）
const powerLoading = ref(false)

// SSH 表单
const sshForm = ref({ host: '', port: 22, user: 'root', password: '' })

// 终端共用状态
const connected = ref(false)
const connecting = ref(false)
const termError = ref('')
const termEl = ref(null)
const clock = ref('')
const termSize = ref('')
const serialUnavailable = ref(false)
const serialReason = ref('')

let term = null
let fitAddon = null
let ws = null
let timeTimer = null
let resizeHandler = null
let probeMode = false
let probeTimer = null
let vncTimer = null
let powerCancelled = false

const currentUser = computed(() => auth.state.user?.username || 'admin')
const hostLabel = computed(() => {
  if (view.value === 'ssh') {
    if (!sshForm.value.host) return '-'
    return `${sshForm.value.user}@${sshForm.value.host}:${sshForm.value.port}`
  }
  if (view.value === 'serial') return vm.value ? vm.value.name : '-'
  return '-'
})

function updateTime() {
  clock.value = new Date().toLocaleString('zh-CN', {
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}
function startClock() {
  if (timeTimer) clearInterval(timeTimer)
  updateTime()
  timeTimer = setInterval(updateTime, 1000)
}
function stopClock() {
  if (timeTimer) { clearInterval(timeTimer); timeTimer = null }
}

async function load() {
  loading.value = true
  try {
    const res = await api.getVM(id)
    vm.value = res.data || null
    if (vm.value && vm.value.ip) sshForm.value.host = vm.value.ip
    // 恢复上次成功的 SSH 参数（只记 host/port/user，不记密码）
    restoreSshForm()
    // 智能默认：VM 运行中先自动尝试串口 Console（免 IP 最轻），失败再回到选择页。
    // 只读角色没有串口权限（后端 403），直接留在选择页只展示图形控制台。
    if (vm.value && vm.value.status === 'running' && isAdmin.value) autoEnterSerial()
    else if (vm.value) {
      serialUnavailable.value = true
      serialReason.value = isAdmin.value ? 'VM 未运行' : '仅管理员可用'
    }
  } catch (e) {
    ElMessage.error('虚拟机不存在')
  } finally {
    loading.value = false
  }
}

function sshMemoryKey() {
  return `vmops-ssh-${id}`
}
function restoreSshForm() {
  try {
    const raw = localStorage.getItem(sshMemoryKey())
    if (!raw) return
    const saved = JSON.parse(raw)
    if (saved.host) sshForm.value.host = saved.host
    if (saved.port) sshForm.value.port = saved.port
    if (saved.user) sshForm.value.user = saved.user
  } catch (e) {
    // 忽略损坏的缓存
  }
}
// 连接成功后记忆参数（密码永不落盘）
function rememberSshForm() {
  try {
    localStorage.setItem(sshMemoryKey(), JSON.stringify({
      host: sshForm.value.host,
      port: sshForm.value.port,
      user: sshForm.value.user
    }))
  } catch (e) {
    // 配额不足等忽略
  }
}

function clearProbe() {
  probeMode = false
  if (probeTimer) { clearTimeout(probeTimer); probeTimer = null }
}

function failProbe(reason) {
  clearProbe()
  serialUnavailable.value = true
  serialReason.value = reason
  cleanupConnection()
  view.value = null
  ElMessage.warning('串口不可用：' + reason + '，已回到选择页')
}

function autoEnterSerial() {
  if (view.value) return
  view.value = 'serial'
  probeMode = true
  startClock()
  connectSerial()
  // 兜底：若 2.5s 内既无 error 也无 connected，视为已连上
  probeTimer = setTimeout(() => { clearProbe() }, 2500)
}

// 切换连接类型/返回选择页时，先彻底清理上一个连接
function cleanupConnection() {
  stopClock()
  clearProbe()
  powerCancelled = true
  if (vncTimer) { clearTimeout(vncTimer); vncTimer = null }
  if (ws) {
    ws.onopen = null; ws.onmessage = null; ws.onerror = null; ws.onclose = null
    try { ws.close() } catch (e) {}
    ws = null
  }
  if (term) {
    if (resizeHandler) window.removeEventListener('resize', resizeHandler)
    resizeHandler = null
    try { term.dispose() } catch (e) {}
    term = null
    fitAddon = null
  }
  connected.value = false
  connecting.value = false
  termError.value = ''
}

function select(v) {
  if (view.value === v) return
  // 兜底：SSH / 串口是对 guest 的写入通道，只读角色由后端 403 拦，前端不给入口
  if ((v === 'ssh' || v === 'serial') && !isAdmin.value) {
    ElMessage.warning('只读角色不能使用 SSH 终端与串口控制台，请使用图形控制台查看')
    return
  }
  cleanupConnection()
  probeMode = false
  view.value = v
  if (v === 'ssh' || v === 'serial') startClock()
  if (v === 'serial') connectSerial()
}

function goBackToPick() {
  cleanupConnection()
  view.value = null
}

function disconnectFromTerminal() {
  cleanupConnection()
  ElMessage.info('已断开连接')
}

function onVncLoad() {
  if (vncTimer) { clearTimeout(vncTimer); vncTimer = null }
  vncFrameLoading.value = false
}

async function connectVNC() {
  vncLoading.value = true
  try {
    const res = await api.vncToken(id)
    const token = (res.data && res.data.token) || ''
    if (!token) throw new Error('token 为空')
    const host = window.location.hostname
    vncFrameLoading.value = true
    // 只读角色由后端返回 view_only=true：noVNC 侧禁用键鼠输入，
    // 使「只读运维」名副其实（VNC 协议本身没有只读模式，必须在客户端关掉输入）
    vncViewOnly.value = !!(res.data && res.data.view_only)
    const viewOnlyParam = vncViewOnly.value ? '&view_only=1' : ''
    vncUrl.value = `http://${host}:6080/vnc.html?autoconnect=1&resize=scale${viewOnlyParam}&path=websockify?token=${token}`
    // 兜底：iframe onload 失败时 15s 后关闭 loading，避免无限转圈（onVncLoad 会清掉）
    if (vncTimer) clearTimeout(vncTimer)
    vncTimer = setTimeout(() => { vncFrameLoading.value = false; vncTimer = null }, 15000)
  } catch (e) {
    ElMessage.error((e.response && e.response.data && (e.response.data.message || e.response.data.error)) || '获取控制台失败')
  } finally {
    vncLoading.value = false
  }
}

function openVncNewWindow() {
  if (vncUrl.value) window.open(vncUrl.value, '_blank')
}

// 页内一键开机并自动连接 VNC：开机指令 → 轮询状态至 running（最长 ~60s）→ 自动 connectVNC
async function powerOnAndConnect() {
  powerLoading.value = true
  powerCancelled = false
  try {
    await api.startVM(id)
    ElMessage.success('开机指令已发送，等待虚拟机启动…')
    const deadline = Date.now() + 60000
    while (Date.now() < deadline) {
      if (powerCancelled) return // 中途切走/卸载：停止轮询
      await new Promise((r) => setTimeout(r, 2000))
      if (powerCancelled) return
      try {
        const res = await api.getVM(id)
        vm.value = res.data || vm.value
        if (vm.value && vm.value.status === 'running') {
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
    await load()
  } catch (e) {
    ElMessage.error((e.response && e.response.data && e.response.data.message) || '开机失败')
  } finally {
    powerLoading.value = false
  }
}

function openWs(path) {
  const token = localStorage.getItem(TOKEN_KEY) || ''
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return new WebSocket(`${proto}//${location.host}/api/vms/${id}/${path}?token=${encodeURIComponent(token)}`)
}

// 建连超时兜底：代理/网络黑洞导致 WS open 挂起时，避免“连接中…”无限转圈
function openWsWithTimeout(path, ms = 10000) {
  return new Promise((resolve, reject) => {
    const socket = openWs(path)
    socket.binaryType = 'arraybuffer'
    const timer = setTimeout(() => {
      try { socket.close() } catch (e) {}
      reject(new Error('WebSocket 连接超时'))
    }, ms)
    socket.onopen = () => { clearTimeout(timer); resolve(socket) }
    socket.onerror = () => { clearTimeout(timer); reject(new Error('WebSocket 连接失败')) }
  })
}

async function connectSSH() {
  if (!sshForm.value.host || !sshForm.value.user || !sshForm.value.password) {
    ElMessage.warning('请填写主机、用户名和密码')
    return
  }
  cleanupConnection()
  connecting.value = true
  startClock()
  try {
    ws = await openWsWithTimeout('terminal')
    connected.value = true
    await nextTick()
    initTerminal()
    ws.onmessage = handleMsg
    ws.onclose = onWsClose
    ws.onerror = () => { if (ws) termError.value = 'WebSocket 错误' }
    ws.send(JSON.stringify({
      type: 'auth',
      host: sshForm.value.host,
      port: sshForm.value.port,
      user: sshForm.value.user,
      password: sshForm.value.password,
    }))
  } catch (e) {
    termError.value = e.message || '连接失败'
    connected.value = false
    if (ws) { ws.close(); ws = null }
  } finally {
    connecting.value = false
  }
}

async function connectSerial() {
  cleanupConnection()
  connecting.value = true
  startClock()
  try {
    ws = await openWsWithTimeout('serial')
    connected.value = true
    await nextTick()
    initTerminal()
    ws.onmessage = handleMsg
    ws.onclose = onWsClose
    ws.onerror = () => { if (ws) termError.value = 'WebSocket 错误' }
  } catch (e) {
    termError.value = e.message || '连接失败'
    connected.value = false
    if (ws) { ws.close(); ws = null }
    if (probeMode) failProbe(e.message || 'WebSocket 连接失败')
  } finally {
    connecting.value = false
  }
}

function reconnect() {
  if (view.value === 'ssh') connectSSH()
  else if (view.value === 'serial') connectSerial()
}

function onWsClose() {
  const wasConnecting = connecting.value
  connected.value = false
  connecting.value = false
  // 探测期静默断开也算失败：回到选择页并标注原因（勿回退智能默认约定）
  if (probeMode) {
    failProbe('连接已断开')
    return
  }
  // 建连中途断开（非主动清理）：给出提示，避免静默停留在未连接态
  if (wasConnecting && view.value) termError.value = '连接已断开，请重试'
}

async function initTerminal() {
  await nextTick()
  if (!termEl.value) return
  term = new Terminal({
    cursorBlink: true,
    cursorStyle: 'bar',
    fontSize: 15,
    fontFamily: "'Cascadia Code', 'Fira Code', Consolas, monospace",
    theme: {
      background: 'rgba(10, 22, 40, 0.18)',
      foreground: '#e6edf3',
      cursor: '#58a6ff',
      selectionBackground: 'rgba(31, 58, 95, 0.7)',
      black: '#1b2838', red: '#f85149', green: '#3fb950', yellow: '#d2991d',
      blue: '#58a6ff', magenta: '#bc8cff', cyan: '#39c5cf', white: '#b1bac4',
      brightBlack: '#30363d', brightRed: '#ff6e6a', brightGreen: '#56d364',
      brightYellow: '#e3b341', brightBlue: '#79c0ff', brightMagenta: '#d2a8ff',
      brightCyan: '#56d4dd', brightWhite: '#f0f6fc',
    },
  })
  fitAddon = new FitAddon()
  term.loadAddon(fitAddon)
  term.open(termEl.value)
  fitAddon.fit()
  updateTermSize()

  term.onData((data) => {
    if (!ws || ws.readyState !== WebSocket.OPEN) return
    if (view.value === 'ssh') ws.send(JSON.stringify({ type: 'input', data }))
    else ws.send(data) // 串口：直接发原始字节
  })

  resizeHandler = () => onResize()
  window.addEventListener('resize', resizeHandler)
  termEl.value.addEventListener('click', () => term && term.focus())
  term.focus()
}

function updateTermSize() {
  if (term) termSize.value = `${term.cols}×${term.rows}`
}

function onResize() {
  if (fitAddon) fitAddon.fit()
  updateTermSize()
  if (view.value === 'ssh' && ws && ws.readyState === WebSocket.OPEN && term) {
    ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
  }
}

function handleMsg(ev) {
  if (!term) return
  if (ev.data instanceof ArrayBuffer) {
    clearProbe()
    term.write(new Uint8Array(ev.data))
    return
  }
  if (ev.data instanceof Blob) {
    clearProbe()
    ev.data.arrayBuffer().then((buf) => { if (term) term.write(new Uint8Array(buf)) })
    return
  }
  try {
    const msg = JSON.parse(ev.data)
    if (msg.type === 'error') {
      if (probeMode) {
        failProbe(msg.msg || '不可用')
        return
      }
      ElMessage.error(msg.msg || '连接失败')
      termError.value = msg.msg || '连接失败'
      disconnectFromTerminal()
    } else if (msg.type === 'connected') {
      clearProbe()
      serialUnavailable.value = false
      serialReason.value = ''
      // SSH 连通成功后记忆参数（下次自动填，密码不记）
      if (view.value === 'ssh') rememberSshForm()
      onResize()
    }
  } catch {
    clearProbe()
    term.write(ev.data)
  }
}

onMounted(() => {
  // 窄屏默认收起侧边栏，给终端/表单让出宽度
  if (window.innerWidth < 720) collapsed.value = true
  load()
})
onUnmounted(() => cleanupConnection())
</script>

<style scoped>
.console-page {
  position: relative;
  height: 100vh;
  display: flex;
  flex-direction: column;
  background: #0a111f;
  overflow: hidden;
}

/* ========== 行内图标统一微调 ==========
   图标一律用 @element-plus/icons-vue 组件（禁止 emoji）。el-icon 是 inline-flex，
   默认按基线对齐 → 1em 的图标盒整体压在基线上，与中文混排时目测偏高；
   统一下压 0.15em 并补 4px 右间距（原来 emoji 后面跟的那个空格已删）。
   注意：不覆盖 el-button 内的图标，按钮的图标/文字间距由 Element Plus 自己管。 */
.topbar-tip .el-icon,
.vnc-bar > span .el-icon,
.term-logo .el-icon,
.term-user .el-icon,
.term-host .el-icon,
.term-clock .el-icon,
.term-error .el-icon,
.form-title .el-icon,
.card-badge .el-icon,
.text-muted .el-icon {
  margin-right: 4px;
  vertical-align: -0.15em;
}

/* ========== 顶部条 ========== */
.topbar {
  position: relative;
  z-index: 5;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 16px;
  background: rgba(10, 17, 31, 0.95);
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
}
.back { color: #8ab4ff; }
.vm-info { display: flex; align-items: center; gap: 10px; }
.vm-name { color: #e6edf3; font-size: 1.05rem; font-weight: 600; }
.topbar-tip { margin-left: auto; color: #7f92ab; font-size: 0.85rem; }

/* ========== 主体（侧边栏 + 主区域） ========== */
.body { flex: 1; display: flex; min-height: 0; }

/* ---------- 可折叠侧边栏 ---------- */
.sidebar {
  width: 210px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px 8px;
  background: #0e1626;
  border-right: 1px solid rgba(255, 255, 255, 0.08);
  overflow: hidden;
  transition: width 0.2s ease;
}
.sidebar.collapsed { width: 56px; }
.collapse-btn {
  align-self: flex-start;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  margin-bottom: 6px;
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 6px;
  background: transparent;
  color: #8ab4ff;
  cursor: pointer;
  font-size: 0.9rem;
}
.collapse-btn:hover { background: rgba(88, 166, 255, 0.1); }
.sidebar.collapsed .collapse-btn { align-self: center; }
.nav-item {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 10px;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: #9db1c8;
  cursor: pointer;
  text-align: left;
  font-size: 0.92rem;
  white-space: nowrap;
}
.nav-item:hover { background: rgba(88, 166, 255, 0.08); color: #e6edf3; }
.nav-item.active { background: rgba(88, 166, 255, 0.15); color: #58a6ff; }
.sidebar.collapsed .nav-item { justify-content: center; padding: 12px 0; }
/* 侧栏图标：固定 24px 槽位并自身居中，折叠态槽位收成 auto 由 .nav-item 居中；
   1.15rem 是与原 emoji 目测等大的字号（el-icon 的 svg 恒为 1em） */
.nav-item .icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  flex: none;
  font-size: 1.15rem;
  line-height: 1;
}
.sidebar.collapsed .nav-item .icon { width: auto; }
.nav-item .rec {
  margin-left: auto;
  font-size: 0.68rem;
  color: #7c4a03;
  background: #f0b90b;
  padding: 1px 6px;
  border-radius: 8px;
  font-weight: 600;
}

/* ---------- 主区域 ---------- */
.main { flex: 1; min-width: 0; display: flex; flex-direction: column; }

/* ---------- 白底选择页 ---------- */
.pick-panel {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: #ffffff;
}
.pick-title { margin: 0; color: #1f2937; font-size: 1.5rem; }
.pick-sub { margin: 8px 0 28px; color: #6b7280; font-size: 0.92rem; }
.pick-note {
  max-width: 720px;
  margin: 24px auto 0;
  padding: 12px 16px;
  border: 1px solid #fde68a;
  border-radius: 6px;
  background: #fffbeb;
  color: #92400e;
  font-size: 0.85rem;
  line-height: 1.7;
  text-align: left;
}
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(230px, 290px));
  gap: 22px;
  justify-content: center;
  max-width: 980px;
}
.card {
  background: #fff;
  border: 1px solid #e5e7eb;
  border-radius: 12px;
  padding: 26px 22px;
  text-align: center;
  cursor: pointer;
  transition: all 0.2s ease;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.06);
}
.card:hover {
  transform: translateY(-3px);
  border-color: #58a6ff;
  box-shadow: 0 10px 24px rgba(88, 166, 255, 0.18);
}
/* 卡片装饰大图标：原 emoji 为 2.2rem 且自带颜色；换成单色 svg 后
   字号上调到 2.4rem 补足视觉体量，并固定 2.6rem 行高保持卡片总高不变、显式给色 */
.card .card-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 2.6rem;
  font-size: 2.4rem;
  line-height: 1;
  color: #58a6ff;
}
.card .card-title { margin: 10px 0 6px; color: #111827; font-size: 1.02rem; font-weight: 600; }
.card .card-desc { min-height: 46px; color: #6b7280; font-size: 0.82rem; line-height: 1.55; }
.card-badge {
  display: inline-block;
  margin-top: 12px;
  padding: 2px 10px;
  border-radius: 10px;
  font-size: 0.75rem;
  font-weight: 600;
}
.card-badge.ok { color: #166534; background: #dcfce7; }
.card-badge.warn { color: #92400e; background: #fef3c7; }
.card.serial {
  border: 1.5px solid #f0b90b;
  background: linear-gradient(180deg, #fffdf5, #ffffff);
}
.card.serial:hover {
  border-color: #f0b90b;
  box-shadow: 0 10px 24px rgba(240, 185, 11, 0.22);
}
/* 串口卡沿用金色主题，图标跟着卡片走 */
.card.serial .card-icon { color: #f0b90b; }
.card-badge.gold { color: #7c4a03; background: #fde68a; }

/* ---------- VNC 视图（浅色，无背景图） ---------- */
.vnc-view {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 20px;
  background: #f5f7fa;
  min-height: 0;
}
.vnc-placeholder { text-align: center; color: #374151; }
.vnc-placeholder-btns {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  margin-top: 16px;
}
.vnc-placeholder .hint { margin-top: 14px; font-size: 0.85rem; color: #7f92ab; }
.vnc-frame { position: relative; width: 100%; height: 100%; display: flex; flex-direction: column; }
.vnc-loading {
  position: absolute;
  inset: 0;
  z-index: 2;
  border-radius: 8px;
  background: #f5f7fa;
}
.vnc {
  flex: 1;
  width: 100%;
  border: 1px solid #d1d5db;
  border-radius: 8px;
  background: #000;
}
.vnc-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 4px 0;
  color: #374151;
  font-size: 0.85rem;
}
.vnc-bar-btns {
  display: flex;
  align-items: center;
  gap: 4px;
}

/* ---------- 终端视图（深色 + 背景图） ---------- */
.term-view {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background-size: cover;
  background-position: center;
  overflow: hidden;
}

.stars-bg { position: absolute; inset: 0; pointer-events: none; overflow: hidden; z-index: 0; }
.star {
  position: absolute;
  width: 2px;
  height: 2px;
  background: #fff;
  border-radius: 50%;
  opacity: 0;
  animation: shootingStar linear infinite;
  box-shadow: 0 0 4px 1px rgba(88, 166, 255, 0.6);
}
@keyframes shootingStar {
  0% { opacity: 0; transform: translateX(0) translateY(0); }
  5% { opacity: 1; }
  20% { opacity: 0; transform: translateX(-120px) translateY(80px); }
  100% { opacity: 0; transform: translateX(-120px) translateY(80px); }
}

/* ---------- JumpServer 风格顶部信息栏 ---------- */
.term-header {
  position: relative;
  z-index: 3;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 16px;
  background: rgba(8, 14, 24, 0.55);
  backdrop-filter: blur(4px);
  border-bottom: 1px solid rgba(88, 166, 255, 0.15);
  color: #8faac7;
  font-size: 0.95rem;
}
.term-header-left, .term-header-center, .term-header-right {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: 1;
  min-width: 0;
}
.term-header-center { justify-content: center; }
.term-header-right { justify-content: flex-end; flex-shrink: 0; }
.term-logo { font-weight: 700; color: #58a6ff; font-size: 1rem; letter-spacing: 0.5px; white-space: nowrap; }
.term-status { white-space: nowrap; }
.term-divider { color: rgba(88, 166, 255, 0.2); }
.term-status.online { color: #3fb950; font-weight: 600; }
.term-status.offline { color: #8b949e; }
.term-user { color: #c9d1d9; }
.term-host { color: #8faac7; }
.term-user, .term-host {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
.term-clock {
  font-family: 'SF Mono', 'Cascadia Code', Consolas, monospace;
  color: #79c0ff;
  font-weight: 500;
}

.term-error {
  position: relative;
  z-index: 3;
  margin: 8px 16px 0;
  padding: 8px 14px;
  border-radius: 6px;
  background: rgba(248, 81, 73, 0.1);
  border: 1px solid rgba(248, 81, 73, 0.2);
  color: #f85149;
  font-size: 0.85rem;
}

/* ---------- SSH 连接表单 ---------- */
.ssh-form-wrap {
  position: relative;
  z-index: 2;
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  min-height: 0;
}
.ssh-form {
  width: 440px;
  max-width: 100%;
  padding: 26px 30px;
  border-radius: 12px;
  background: rgba(8, 14, 24, 0.75);
  border: 1px solid rgba(88, 166, 255, 0.2);
  box-shadow: 0 8px 30px rgba(0, 0, 0, 0.4);
}
.form-title { margin: 0 0 18px; color: #e6edf3; font-size: 1.1rem; }
.ssh-form :deep(.el-form-item__label) { color: #c6d4e4; }
.ssh-form :deep(.el-input__wrapper),
.ssh-form :deep(.el-input-number .el-input__wrapper) {
  background: rgba(255, 255, 255, 0.06);
  box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.15) inset;
}
.ssh-form :deep(.el-input__inner) { color: #e6edf3; }
.ssh-form :deep(.el-input-number__decrease),
.ssh-form :deep(.el-input-number__increase) {
  color: #c6d4e4;
  background: rgba(255, 255, 255, 0.08);
  border-color: rgba(255, 255, 255, 0.12) !important;
  box-shadow: none !important;
}
.form-btn { width: 100%; margin-top: 4px; }

/* ---------- 串口连接面板 ---------- */
.serial-panel {
  position: relative;
  z-index: 2;
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  text-align: center;
  padding: 24px;
}
.serial-big-icon {
  width: 84px;
  height: 84px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #f0b90b;
  border: 2px solid rgba(240, 185, 11, 0.5);
  border-radius: 50%;
  background: rgba(8, 14, 24, 0.6);
}
/* 圆盘内的大图标：svg 不吃 text-shadow，原来的金色发光改用 drop-shadow 保留 */
.serial-big-icon .el-icon {
  font-size: 2.8rem;
  filter: drop-shadow(0 0 12px rgba(240, 185, 11, 0.55));
}
.serial-title { color: #e6edf3; font-size: 1.25rem; font-weight: 600; }
.serial-desc { max-width: 460px; color: #9db1c8; font-size: 0.88rem; line-height: 1.6; }
.serial-btn { margin-top: 8px; }

/* ---------- 终端主体（含水印） ---------- */
.term-body {
  position: relative;
  z-index: 1;
  flex: 1;
  min-height: 0;
  margin: 6px 12px 0;
  border-radius: 8px;
  overflow: hidden;
  background: transparent;
}
.terminal-container { width: 100%; height: 100%; position: relative; z-index: 1; }
.terminal-container :deep(.xterm) {
  height: 100% !important;
  padding: 8px 12px;
  background: transparent !important;
}
.terminal-container :deep(.xterm-viewport) {
  overflow-y: auto !important;
  background: transparent !important;
}

.watermark {
  position: absolute;
  inset: 0;
  pointer-events: none;
  z-index: 10;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 20px;
  transform: rotate(-20deg);
  opacity: 0.38;
}
.watermark-line {
  font-family: 'Cascadia Code', 'Fira Code', Consolas, monospace;
  font-size: 2.6rem;
  font-weight: 700;
  color: #fff;
  white-space: nowrap;
  text-shadow: 0 0 10px rgba(0, 0, 0, 0.6), 0 0 60px rgba(255, 255, 255, 0.5);
}
.watermark-sub { font-size: 1.4rem; font-weight: 400; }

/* ---------- 底部操作栏 ---------- */
.term-footer {
  position: relative;
  z-index: 3;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  background: rgba(8, 14, 24, 0.55);
  backdrop-filter: blur(4px);
  border-top: 1px solid rgba(88, 166, 255, 0.15);
  font-size: 0.85rem;
  color: #9db1c8;
}
.term-footer-left { display: flex; gap: 8px; }
.term-footer-right { display: flex; align-items: center; gap: 14px; }
.text-muted { color: #8b949e; }
.term-size {
  font-family: 'SF Mono', 'Cascadia Code', Consolas, monospace;
  color: #79c0ff;
  min-width: 52px;
  text-align: right;
}
.ft-btn {
  color: #79c0ff;
  border-color: rgba(88, 166, 255, 0.3);
}
.ft-btn:hover {
  color: #a0d8ff;
  border-color: rgba(88, 166, 255, 0.5);
  background: rgba(88, 166, 255, 0.08);
}

/* ---------- 窄屏适配（≤640px）：header 三段换行、footer 换行保操作区 ---------- */
@media (max-width: 640px) {
  .term-header {
    flex-wrap: wrap;
    row-gap: 4px;
    font-size: 0.8rem;
    padding: 8px 12px;
  }
  .term-header-left { flex: 1 1 auto; }
  .term-header-right { flex: 0 0 auto; }
  .term-header-center {
    order: 3;
    flex: 1 1 100%;
    justify-content: flex-start;
  }
  .term-footer {
    flex-wrap: wrap;
    row-gap: 6px;
    padding: 8px 12px;
  }
  .term-footer-right { flex-wrap: wrap; row-gap: 4px; }
  .term-footer .text-muted { display: none; }
  .pick-panel { padding: 16px; }
  /* 窄屏卡片单列：minmax(230px,290px) 在 390px 下会横向溢出 */
  .cards { grid-template-columns: 1fr; max-width: 340px; width: 100%; }
  .card .card-desc { min-height: 0; }
  .ssh-form { padding: 20px 18px; }
  .topbar-tip { display: none; }
}
</style>