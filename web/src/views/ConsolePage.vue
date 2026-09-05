<template>
  <div class="console-page" v-loading="loading">
    <!-- 顶部条 -->
    <div class="topbar">
      <el-button text class="back" @click="$router.push('/vms')">← 返回</el-button>
      <div class="vm-info">
        <span class="vm-name">{{ vm ? vm.name : '...' }}</span>
        <el-tag v-if="vm" :type="vm.status === 'running' ? 'success' : 'info'" effect="dark" size="small">
          {{ vm.status === 'running' ? '运行中' : '已关机' }}
        </el-tag>
      </div>
      <div class="topbar-tip">
        <span v-if="view === 'vnc'">🖥️ 图形控制台 (VNC)</span>
        <span v-else-if="view === 'ssh'">⌨️ Web 终端 (SSH)</span>
        <span v-else-if="view === 'serial'">▮ 串口 Console</span>
        <span v-else>选择连接方式</span>
      </div>
    </div>

    <div class="body">
      <!-- 可折叠侧边栏 -->
      <aside class="sidebar" :class="{ collapsed }">
        <button class="collapse-btn" :title="collapsed ? '展开侧边栏' : '收起侧边栏'" @click="collapsed = !collapsed">
          {{ collapsed ? '»' : '«' }}
        </button>
        <button class="nav-item" :class="{ active: view === 'vnc' }" @click="select('vnc')">
          <span class="icon">🖥️</span><span v-if="!collapsed" class="label">图形控制台 (VNC)</span>
        </button>
        <button class="nav-item" :class="{ active: view === 'ssh' }" @click="select('ssh')">
          <span class="icon">⌨️</span><span v-if="!collapsed" class="label">Web 终端 (SSH)</span>
        </button>
        <button class="nav-item serial-item" :class="{ active: view === 'serial' }" @click="select('serial')">
          <span class="icon">▮</span><span v-if="!collapsed" class="label">串口 Console</span>
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
              <div class="card-icon">🖥️</div>
              <div class="card-title">图形控制台 (VNC)</div>
              <div class="card-desc">noVNC 图形远程桌面，所见即所得。需 VM 运行中，无需 IP 与账号。</div>
              <div class="card-badge" :class="vm && vm.status === 'running' ? 'ok' : 'warn'">
                {{ vm && vm.status === 'running' ? '● 可用' : '● 需运行中' }}
              </div>
            </div>
            <div class="card" @click="select('ssh')">
              <div class="card-icon">⌨️</div>
              <div class="card-title">Web 终端 (SSH)</div>
              <div class="card-desc">字符 SSH 终端（xterm.js），比 VNC 更顺滑。需 VM IP 与账号密码。</div>
              <div class="card-badge ok">● 需网络可达</div>
            </div>
            <div class="card serial" @click="select('serial')">
              <div class="card-icon">▮</div>
              <div class="card-title">串口 Console</div>
              <div class="card-desc">免 IP 直连 VM 串口（virsh console），无网卡 / 未配置 IP 也能进系统。</div>
              <div class="card-badge" :class="serialUnavailable ? 'warn' : 'gold'">
                {{ serialUnavailable ? '● 不可用：' + serialReason : '★ 先尝试它' }}
              </div>
            </div>
          </div>
        </div>

        <!-- VNC 图形控制台：浅色干净背景，无背景图 -->
        <div v-else-if="view === 'vnc'" class="vnc-view">
          <div v-if="!vm || vm.status !== 'running'" class="vnc-placeholder">
            <el-alert type="warning" :closable="false" show-icon
              title="VM 未运行，无法连接图形控制台（VNC 需运行中）。可在此直接开机，开机后自动连接。" />
            <div class="vnc-placeholder-btns">
              <el-button type="primary" size="large" :loading="powerLoading" @click="powerOnAndConnect">一键开机并连接</el-button>
              <el-button class="mt" @click="$router.push('/vms')">去虚拟机列表</el-button>
            </div>
          </div>
          <div v-else-if="!vncUrl" class="vnc-placeholder">
            <el-button type="primary" size="large" :loading="vncLoading" @click="connectVNC">连接图形控制台</el-button>
            <p class="hint">noVNC 直连虚拟机虚拟显示，无需知道 IP。</p>
          </div>
          <div v-else class="vnc-frame">
            <div v-if="vncFrameLoading" class="vnc-loading" v-loading="true" element-loading-text="图形桌面加载中…" />
            <iframe :src="vncUrl" class="vnc" @load="vncFrameLoading = false" />
            <div class="vnc-bar">
              <span>🖥️ 图形控制台已连接</span>
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
              <span class="term-logo">🛡️ VMOps</span>
              <span class="term-divider">|</span>
              <span v-if="connected" class="term-status online">● 已连接</span>
              <span v-else class="term-status offline">○ 未连接</span>
            </div>
            <div class="term-header-center">
              <span class="term-user">👤 当前用户：{{ currentUser }}</span>
              <span class="term-divider">|</span>
              <span class="term-host">🖥️ {{ hostLabel }}</span>
            </div>
            <div class="term-header-right">
              <span class="term-clock">🕐 {{ clock || '--' }}</span>
            </div>
          </div>

          <div v-if="termError" class="term-error">⚠️ {{ termError }}</div>

          <!-- SSH 连接表单 -->
          <div v-if="view === 'ssh' && !connected" class="ssh-form-wrap">
            <div class="ssh-form">
              <h3 class="form-title">⌨️ SSH 连接</h3>
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
            <div class="serial-big-icon">▮</div>
            <div class="serial-title">串口 Console · 免 IP 直连</div>
            <div class="serial-desc">等价 virsh console，直接读写 guest 串口 ttyS0。无需 IP / 账号，无网卡也能进系统，建议优先尝试。</div>
            <el-button type="warning" size="large" class="serial-btn" :loading="connecting" @click="connectSerial">
              {{ connecting ? '连接中…' : '▶ 连接串口 Console' }}
            </el-button>
          </div>

          <!-- 终端主体（SSH / 串口共用） -->
          <div v-else-if="connected" class="term-body">
            <div ref="termEl" class="terminal-container" />
            <!-- 倾斜水印 -->
            <div class="watermark">
              <div class="watermark-line">{{ vm ? vm.name : 'vm' }}</div>
              <div class="watermark-line watermark-sub">vmops console</div>
            </div>
          </div>

          <!-- 底部操作栏 -->
          <div class="term-footer">
            <div class="term-footer-left">
              <template v-if="connected">
                <el-button size="small" class="ft-btn" :loading="connecting" @click="reconnect">🔄 重新连接</el-button>
                <el-button size="small" class="ft-btn" @click="disconnectFromTerminal">断开</el-button>
              </template>
              <template v-else>
                <el-button size="small" class="ft-btn" @click="goBackToPick">← 返回选择</el-button>
              </template>
            </div>
            <div class="term-footer-right">
              <span class="text-muted">💡 鼠标选中复制，Ctrl+Shift+V 粘贴</span>
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
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { api, TOKEN_KEY } from '../api'
import { useAuth } from '../store/auth'
import consoleBg from '../assets/console-bg.jpg'

const route = useRoute()
const id = route.params.id
const auth = useAuth()

const vm = ref(null)
const loading = ref(true)
const collapsed = ref(false)   // 侧边栏折叠
const view = ref(null)         // 'vnc' | 'ssh' | 'serial' | null(选择页)

// VNC
const vncLoading = ref(false)
const vncUrl = ref('')
const vncFrameLoading = ref(false)
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

const currentUser = computed(() => auth.state.user?.username || 'admin')
const hostLabel = computed(() => {
  if (view.value === 'ssh') return `${sshForm.value.user}@${sshForm.value.host}:${sshForm.value.port}`
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
    // 智能默认：VM 运行中先自动尝试串口 Console（免 IP 最轻），失败再回到选择页
    if (vm.value && vm.value.status === 'running') autoEnterSerial()
    else if (vm.value) {
      serialUnavailable.value = true
      serialReason.value = 'VM 未运行'
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

async function connectVNC() {
  vncLoading.value = true
  try {
    const res = await api.vncToken(id)
    const token = (res.data && res.data.token) || ''
    if (!token) throw new Error('token 为空')
    const host = window.location.hostname
    vncFrameLoading.value = true
    vncUrl.value = `http://${host}:6080/vnc.html?autoconnect=1&resize=scale&path=websockify?token=${token}`
    // 兜底：iframe onload 失败时 15s 后关闭 loading，避免无限转圈
    setTimeout(() => { vncFrameLoading.value = false }, 15000)
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
  try {
    await api.startVM(id)
    ElMessage.success('开机指令已发送，等待虚拟机启动…')
    const deadline = Date.now() + 60000
    while (Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 2000))
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

async function connectSSH() {
  if (!sshForm.value.host || !sshForm.value.user || !sshForm.value.password) {
    ElMessage.warning('请填写主机、用户名和密码')
    return
  }
  cleanupConnection()
  connecting.value = true
  startClock()
  try {
    ws = openWs('terminal')
    ws.binaryType = 'arraybuffer'
    await new Promise((resolve, reject) => {
      ws.onopen = resolve
      ws.onerror = () => reject(new Error('WebSocket 连接失败'))
    })
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
    ws = openWs('serial')
    ws.binaryType = 'arraybuffer'
    await new Promise((resolve, reject) => {
      ws.onopen = resolve
      ws.onerror = () => reject(new Error('WebSocket 连接失败'))
    })
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
    if (probeMode) failProbe('WebSocket 连接失败')
  } finally {
    connecting.value = false
  }
}

function reconnect() {
  if (view.value === 'ssh') connectSSH()
  else if (view.value === 'serial') connectSerial()
}

function onWsClose() {
  connected.value = false
  connecting.value = false
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

onMounted(load)
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
.nav-item .icon { width: 24px; flex: none; text-align: center; font-size: 1.05rem; }
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
.card .card-icon { font-size: 2.2rem; }
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
.vnc-placeholder .mt { margin-top: 16px; }
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
}
.term-header-center { justify-content: center; }
.term-header-right { justify-content: flex-end; }
.term-logo { font-weight: 700; color: #58a6ff; font-size: 1rem; letter-spacing: 0.5px; }
.term-divider { color: rgba(88, 166, 255, 0.2); }
.term-status.online { color: #3fb950; font-weight: 600; }
.term-status.offline { color: #8b949e; }
.term-user { color: #c9d1d9; }
.term-host { color: #8faac7; }
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
.ssh-form :deep(.el-input-number__increase) { color: #c6d4e4; }
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
  font-size: 2.6rem;
  color: #f0b90b;
  border: 2px solid rgba(240, 185, 11, 0.5);
  border-radius: 50%;
  background: rgba(8, 14, 24, 0.6);
  text-shadow: 0 0 14px rgba(240, 185, 11, 0.5);
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
</style>