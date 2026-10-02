<template>
  <div class="console-page" v-loading="loading">
    <!-- 顶部条 -->
    <div class="topbar">
      <el-button text class="back" @click="$router.push('/vms')">
        <el-icon><ArrowLeft /></el-icon><span>返回</span>
      </el-button>
      <div class="vm-info">
        <span class="vm-name">{{ vm ? vm.name : '...' }}</span>
        <el-tag v-if="vm" :type="vmStatusTag(vm.status)" effect="dark" size="small">
          {{ vmStatusText(vm.status) }}
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
        <!-- 折叠态 icon-only 按钮必须有 title 提示（无障碍：icon-only 无 label 即反模式） -->
        <button class="nav-item" :class="{ active: view === 'vnc' }" :title="collapsed ? '图形控制台 (VNC)' : ''" @click="select('vnc')">
          <el-icon class="icon"><Monitor /></el-icon><span v-if="!collapsed" class="label">图形控制台 (VNC)</span>
        </button>
        <button
          v-if="canOperate"
          class="nav-item"
          :class="{ active: view === 'ssh' }"
          :title="collapsed ? 'Web 终端 (SSH)' : ''"
          @click="select('ssh')"
        >
          <el-icon class="icon"><Platform /></el-icon><span v-if="!collapsed" class="label">Web 终端 (SSH)</span>
        </button>
        <button
          v-if="canOperate"
          class="nav-item serial-item"
          :class="{ active: view === 'serial' }"
          :title="collapsed ? '串口 Console（免IP）' : ''"
          @click="select('serial')"
        >
          <el-icon class="icon"><Connection /></el-icon><span v-if="!collapsed" class="label">串口 Console</span>
          <span v-if="!collapsed" class="rec">免IP</span>
        </button>
      </aside>

      <!-- 主区域 -->
      <main class="main">
        <!-- 选择页（表面走平台令牌，暗色模式跟随全站） -->
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
            <div v-if="canOperate" class="card" @click="select('ssh')">
              <div class="card-icon"><el-icon><Platform /></el-icon></div>
              <div class="card-title">Web 终端 (SSH)</div>
              <div class="card-desc">字符 SSH 终端（xterm.js），比 VNC 更顺滑。需 VM IP 与账号密码。</div>
              <div class="card-badge ok">● 需网络可达</div>
            </div>
            <div v-if="canOperate" class="card serial" @click="select('serial')">
              <div class="card-icon"><el-icon><Connection /></el-icon></div>
              <div class="card-title">串口 Console</div>
              <div class="card-desc">免 IP 直连 VM 串口（virsh console），无网卡 / 未配置 IP 也能进系统。</div>
              <div class="card-badge" :class="serialUnavailable ? 'warn' : 'gold'">
                <template v-if="serialUnavailable">● 不可用：{{ serialReason }}</template>
                <template v-else><el-icon><StarFilled /></el-icon>先尝试它</template>
              </div>
            </div>
          </div>
          <p v-if="!canOperate" class="pick-note">
            当前为只读角色：SSH 终端与串口 Console 会向虚拟机内部写入，已限定为管理员与操作角色使用；
            图形控制台以只读模式打开（可查看画面，键鼠输入禁用）。
          </p>
        </div>

        <!-- VNC 图形控制台（浅色干净背景，无背景图） -->
        <VncView
          v-else-if="view === 'vnc'"
          :vm="vm" :vm-id="id"
          @vm-updated="vm = $event"
          @request-reload="load"
        />

        <!-- SSH / 串口 共用终端视图：深色 + console-bg.jpg 背景 -->
        <TermView
          v-else
          ref="termView"
          :mode="view" :vm-id="id"
          :vm-name="vm ? vm.name : ''"
          :current-user="currentUser"
          :initial-host="vm && vm.ip ? vm.ip : ''"
          :probe="serialProbing"
          @back="view = null"
          @probe-failed="onProbeFailed"
          @serial-ok="onSerialOk"
        />
      </main>
    </div>
  </div>
</template>

<script setup>
// 控制台页壳：布局（顶栏/可折叠侧栏/选择页）+ VM 状态加载 + 视图切换决策
// （含「智能默认：运行中先探串口」）。三个视图的实现拆至 console/components/
// （VncView / TermView，2026-10 前端收敛批次），WS/探测/全屏等协议逻辑在子组件内原样保留。
import { ref, computed, onMounted, nextTick, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
// 图标一律用组件（禁止 emoji 当图标）。main.js 已全量全局注册，这里仍显式 import：
// 一是模板里能看出图标来源，二是将来改按需引入不用回头翻模板。
import {
  ArrowLeft,
  ArrowRight,
  Connection,
  Monitor,
  Platform,
  StarFilled,
} from '@element-plus/icons-vue'
import { api } from '../api'
import { errMsg, vmStatusTag, vmStatusText } from '../utils/format'
import { useAuth } from '../store/auth'
import VncView from './console/components/VncView.vue'
import TermView from './console/components/TermView.vue'

const route = useRoute()
const id = route.params.id
const auth = useAuth()
const { canOperate } = auth

const vm = ref(null)
const loading = ref(true)
const collapsed = ref(false)   // 侧边栏折叠
const view = ref(null)         // 'vnc' | 'ssh' | 'serial' | null(选择页)
const termView = ref(null)

// 串口探测与可用性（选择页徽标展示）
const serialProbing = ref(false)
const serialUnavailable = ref(false)
const serialReason = ref('')

const currentUser = computed(() => auth.state.user?.username || '—')

async function load() {
  loading.value = true
  try {
    const res = await api.getVM(id)
    vm.value = res.data || null
    // 智能默认：VM 运行中先自动尝试串口 Console（免 IP 最轻），失败再回到选择页。
    // 只读角色没有串口权限（后端 403），直接留在选择页只展示图形控制台。
    if (!view.value && vm.value && vm.value.status === 'running' && canOperate.value) autoEnterSerial()
    else if (vm.value && !view.value) {
      serialUnavailable.value = true
      serialReason.value = canOperate.value ? 'VM 未运行' : '只读角色不可用串口'
    }
  } catch (e) {
    ElMessage.error(errMsg(e, '虚拟机加载失败'))
  } finally {
    loading.value = false
  }
}

function autoEnterSerial() {
  if (view.value) return
  serialProbing.value = true
  view.value = 'serial' // TermView 挂载即连（probe 模式：失败上抛 probe-failed）
}

function onProbeFailed(reason) {
  serialProbing.value = false
  serialUnavailable.value = true
  serialReason.value = reason
  view.value = null
}

function onSerialOk() {
  serialProbing.value = false
  serialUnavailable.value = false
  serialReason.value = ''
}

function select(v) {
  if (view.value === v) return
  // 兜底：SSH / 串口是对 guest 的写入通道，只读角色由后端 403 拦，前端不给入口
  if ((v === 'ssh' || v === 'serial') && !canOperate.value) {
    ElMessage.warning('只读角色不能使用 SSH 终端与串口控制台，请使用图形控制台查看')
    return
  }
  serialProbing.value = false
  view.value = v
}

// 折叠/展开侧栏后终端容器宽度变化：nextTick 先补一刀，宽度 transition（0.2s）结束后再校准一次，
// 两处都走 TermView.refit（fit + SSH 同步 PTY 尺寸），避免折叠后终端留白或横向滚动
watch(collapsed, () => {
  nextTick(() => termView.value && termView.value.refit())
  setTimeout(() => termView.value && termView.value.refit(), 260)
})

onMounted(() => {
  // 窄屏默认收起侧边栏，给终端/表单让出宽度
  if (window.innerWidth < 720) collapsed.value = true
  load()
})
</script>

<style scoped>
.console-page {
  position: relative;
  height: 100vh;
  display: flex;
  flex-direction: column;
  background: var(--term-page-bg);
  overflow: hidden;
}

/* ========== 行内图标统一微调 ==========
   图标一律用 @element-plus/icons-vue 组件（禁止 emoji）。el-icon 是 inline-flex，
   默认按基线对齐 → 1em 的图标盒整体压在基线上，与中文混排时目测偏高；
   统一下压 0.15em 并补 4px 右间距（原来 emoji 后面跟的那个空格已删）。
   注意：不覆盖 el-button 内的图标，按钮的图标/文字间距由 Element Plus 自己管。 */
.topbar-tip .el-icon,
.card-badge .el-icon {
  vertical-align: -0.15em;
  margin-right: 4px;
}

/* ========== 顶部条 ========== */
.topbar {
  position: relative;
  z-index: 5;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 16px;
  background: var(--term-overlay);
  border-bottom: 1px solid var(--term-hairline);
}
.back { color: var(--term-accent-soft); }
.vm-info { display: flex; align-items: center; gap: 10px; }
.vm-name { color: var(--term-text); font-size: 1.05rem; font-weight: 600; }
.topbar-tip { margin-left: auto; color: var(--term-text-dim); font-size: 0.85rem; }

/* ========== 主体（侧边栏 + 主区域） ========== */
.body { flex: 1; display: flex; min-height: 0; }

/* ---------- 可折叠侧边栏 ---------- */
.sidebar {
  width: 210px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px 8px;
  background: var(--term-panel-bg);
  border-right: 1px solid var(--term-hairline);
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
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--term-accent-soft);
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
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--term-text-sub);
  cursor: pointer;
  text-align: left;
  font-size: 0.92rem;
  white-space: nowrap;
}
.nav-item:hover { background: rgba(88, 166, 255, 0.08); color: var(--term-text); }
.nav-item.active { background: rgba(88, 166, 255, 0.15); color: var(--term-accent); }
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
  color: var(--color-serial-gold-ink);
  background: var(--color-serial-gold);
  padding: 1px 6px;
  border-radius: var(--radius-md);
  font-weight: 600;
}

/* ---------- 主区域 ---------- */
.main { flex: 1; min-width: 0; display: flex; flex-direction: column; }

/* ---------- 选择页（表面走平台令牌：暗色模式跟随全站） ---------- */
.pick-panel {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: var(--color-background);
}
.pick-title { margin: 0; color: var(--color-foreground); font-size: 1.5rem; }
.pick-sub { margin: 8px 0 28px; color: var(--color-muted-foreground); font-size: 0.92rem; }
.pick-note {
  max-width: 720px;
  margin: 24px auto 0;
  padding: 12px 16px;
  border: 1px dashed var(--color-warning);
  border-radius: var(--radius-sm);
  background: var(--status-paused-bg);
  color: var(--color-foreground);
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
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  padding: 26px 22px;
  text-align: center;
  cursor: pointer;
  transition: transform var(--dur-base) var(--ease-standard), border-color var(--dur-base) var(--ease-standard), box-shadow var(--dur-base) var(--ease-standard);
  box-shadow: var(--elev-card);
}
.card:hover {
  transform: translateY(-3px);
  border-color: var(--term-accent);
  box-shadow: var(--elev-hover);
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
  color: var(--term-accent);
}
.card .card-title { margin: 10px 0 6px; color: var(--color-foreground); font-size: 1.02rem; font-weight: 600; }
.card .card-desc { min-height: 46px; color: var(--color-muted-foreground); font-size: 0.82rem; line-height: 1.55; }
.card-badge {
  display: inline-block;
  margin-top: 12px;
  padding: 2px 10px;
  border-radius: var(--radius-md);
  font-size: 0.75rem;
  font-weight: 600;
}
.card-badge.ok { color: var(--color-success); background: var(--status-running-bg); }
.card-badge.warn { color: var(--color-warning); background: var(--status-paused-bg); }
.card.serial {
  border: 1.5px solid var(--color-serial-gold);
}
.card.serial:hover {
  border-color: var(--color-serial-gold);
  box-shadow: var(--elev-hover);
}
/* 串口卡沿用金色主题，图标跟着卡片走 */
.card.serial .card-icon { color: var(--color-serial-gold); }
.card-badge.gold { color: var(--color-serial-gold-ink); background: var(--color-serial-gold); }

/* ---------- 窄屏适配（≤640px） ---------- */
@media (max-width: 640px) {
  .pick-panel { padding: 16px; }
  /* 窄屏卡片单列：minmax(230px,290px) 在 390px 下会横向溢出 */
  .cards { grid-template-columns: 1fr; max-width: 340px; width: 100%; }
  .card .card-desc { min-height: 0; }
  .topbar-tip { display: none; }
}
</style>
