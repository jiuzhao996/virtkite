<template>
  <div class="vm-detail" v-loading="loading">
    <!-- 顶部工具栏：返回 / 名称 / 状态 / IP / 操作 -->
    <div class="toolbar">
      <div class="tb-left">
        <el-button text :icon="ArrowLeft" @click="back">返回</el-button>
        <span class="tb-name">{{ vmName }}</span>
        <el-tag v-if="vm" :type="vmStatusTag(vm.status)" effect="dark" size="small">{{ vmStatusText(vm.status, '—') }}</el-tag>
        <span v-if="vm && vm.ip" class="tb-ip">{{ vm.ip }}</span>
      </div>
      <div class="tb-actions">
        <el-button type="primary" :icon="Monitor" :disabled="!isRunning" :title="isRunning ? '' : '开机后可用控制台'" @click="goConsole">控制台</el-button>
        <!-- 电源 / 挂起 状态切换按钮：一个按钮按当前状态显示对应动作（运行中→关机/暂停，关机→开机，暂停→恢复）。
             语义保持与拆分版一致：暂停态须先恢复（电源钮禁用），关机态禁用挂起钮；busy 期间锁定防止动作切换闪烁 -->
        <el-button
          v-if="canOperate && vm"
          :icon="isRunning ? SwitchButton : VideoPlay"
          :loading="busy === 'stop' || busy === 'start'"
          :disabled="isPaused || (!!busy && busy !== 'resume')"
          @click="act(isRunning ? 'stop' : 'start')"
        >{{ isRunning ? '关机' : '开机' }}</el-button>
        <el-button
          v-if="canOperate && vm"
          :icon="isPaused ? VideoPlay : VideoPause"
          :loading="busy === 'pause' || busy === 'resume'"
          :disabled="(!isRunning && !isPaused) || (!!busy && busy !== 'stop' && busy !== 'start')"
          @click="act(isPaused ? 'resume' : 'pause')"
        >{{ isPaused ? '恢复' : '暂停' }}</el-button>
        <el-button v-if="canOperate && vm" :icon="RefreshRight" :loading="busy === 'restart'" :disabled="!isRunning" :title="isRunning ? '' : '开机后才能重启'" @click="act('restart')">重启</el-button>
        <el-button v-if="canOperate" type="danger" :icon="Delete" :loading="busy === 'delete'" @click="doDelete">删除</el-button>
      </div>
    </div>

    <el-container class="body">
      <!-- 左侧导航 -->
      <el-aside width="216px" class="side">
        <el-menu :default-active="activeMenu" @select="onMenuSelect" class="side-menu">
          <el-menu-item index="overview">
            <el-icon><Odometer /></el-icon>
            <span>概览</span>
          </el-menu-item>
          <el-menu-item index="perf">
            <el-icon><TrendCharts /></el-icon>
            <span>性能</span>
          </el-menu-item>
          <el-menu-item index="cpu">
            <el-icon><Cpu /></el-icon>
            <span>处理器</span>
          </el-menu-item>
          <el-menu-item index="memory">
            <el-icon><Coin /></el-icon>
            <span>内存</span>
          </el-menu-item>
          <el-menu-item-group title="磁盘">
            <el-menu-item v-for="item in diskMenuItems" :key="item.index" :index="item.index">
              <el-icon><FolderOpened /></el-icon>
              <span class="mono">{{ item.target }}</span>
            </el-menu-item>
          </el-menu-item-group>
          <el-menu-item-group title="网卡">
            <el-menu-item v-for="item in nicMenuItems" :key="item.index" :index="item.index">
              <el-icon><Connection /></el-icon>
              <span>{{ item.label }}</span>
            </el-menu-item>
          </el-menu-item-group>
          <el-menu-item index="snapshots">
            <el-icon><CameraFilled /></el-icon>
            <span>快照</span>
          </el-menu-item>
          <el-menu-item index="xml">
            <el-icon><Document /></el-icon>
            <span>XML 定义</span>
          </el-menu-item>
          <el-menu-item v-if="canOperate" index="files">
            <el-icon><FolderOpened /></el-icon>
            <span>文件管理</span>
          </el-menu-item>
          <el-menu-item v-if="isAdmin" index="grants">
            <el-icon><User /></el-icon>
            <span>授权管理</span>
          </el-menu-item>
        </el-menu>
      </el-aside>

      <!-- 右侧内容区：概览 / XML / 文件管理留在壳，其余分区为 vm-detail/components 下的自持子组件
           （均 v-show 常驻挂载——切走再切回不丢状态、性能曲线不重置） -->
      <el-main class="content">
        <!-- 概览 -->
        <section v-show="activeView === 'overview'" class="panel">
          <div class="panel-head">
            <h3 class="panel-title">概览</h3>
          </div>
          <el-card shadow="never">
            <!-- 腾讯云式信息行：无框线、label 灰色固定宽，一行一条信息，可读性优于带框表格 -->
            <el-descriptions :column="2" class="ov-desc">
              <el-descriptions-item label="名称">{{ spec ? spec.name : '—' }}</el-descriptions-item>
              <el-descriptions-item label="UUID">{{ spec ? spec.uuid : '—' }}</el-descriptions-item>
              <el-descriptions-item label="状态">
                <el-tag :type="vmStatusTag(vm ? vm.status : '')" effect="light">{{ vmStatusText(vm ? vm.status : '', '—') }}</el-tag>
              </el-descriptions-item>
              <el-descriptions-item label="宿主机">{{ hostName }}</el-descriptions-item>
              <el-descriptions-item label="存储池">{{ vm ? vm.storage_pool || '—' : '—' }}</el-descriptions-item>
              <el-descriptions-item label="IP">{{ vm ? vm.ip || '—' : '—' }}</el-descriptions-item>
              <el-descriptions-item label="vCPU">{{ spec ? spec.vcpu + ' 核' : '—' }}</el-descriptions-item>
              <el-descriptions-item label="内存">{{ spec ? spec.memory_mb + ' MB' : '—' }}</el-descriptions-item>
              <el-descriptions-item label="系统类型">{{ spec ? spec.os_type : (vm && vm.os_type) || '—' }}</el-descriptions-item>
              <el-descriptions-item label="架构">{{ spec ? spec.arch : '—' }}</el-descriptions-item>
              <el-descriptions-item label="机器类型">{{ spec ? spec.machine : '—' }}</el-descriptions-item>
              <el-descriptions-item label="创建时间">{{ createdText }}</el-descriptions-item>
              <el-descriptions-item label="MAC 地址">{{ macText }}</el-descriptions-item>
              <el-descriptions-item label="开机自启">
                <el-switch
                  :model-value="!!(spec && spec.autostart)"
                  :loading="busy === 'autostart'"
                  :disabled="!spec || !canOperate"
                  @change="onAutostartChange"
                />
              </el-descriptions-item>
            </el-descriptions>
          </el-card>
        </section>

        <!-- 性能（曲线 echarts / 60 采样拼装 / Prometheus 预填内聚在 VmPerfCard，壳只编排轮询） -->
        <VmPerfCard
          ref="perfCardRef"
          :vm-id="id"
          :active="activeView === 'perf'"
          :is-running="isRunning"
          :stats="stats"
          :interval-ms="statsIntervalMs"
        />

        <!-- Guest 内部指标（node_exporter，file_sd 自动纳管；未装显示安装引导，懒加载） -->
        <GuestMetricsCard :vm-id="id" :active="activeView === 'perf'" :is-running="isRunning" />

        <!-- 处理器 / 内存 / 磁盘 / 网卡 四分区（含添加/移除磁盘、添加网卡对话框，内聚在 VmHardwareDialogs） -->
        <VmHardwarePanels
          :vm-id="id"
          :view="activeView"
          :spec="spec"
          :can-operate="canOperate"
          :reload="loadSpec"
          v-model:busy="busy"
          v-model:active-disk="activeDisk"
          v-model:active-nic="activeNic"
        />

        <!-- 快照（列表/创建/回滚/删除自持，回滚后经 reload 刷新规格） -->
        <VmSnapshotCard :vm-id="id" :active="activeView === 'snapshots'" :can-operate="canOperate" :reload="loadSpec" />

        <!-- XML 定义 -->
        <section v-show="activeView === 'xml'" class="panel">
          <div class="panel-head">
            <h3 class="panel-title">XML 定义</h3>
          </div>
          <el-alert
            type="info"
            :closable="false"
            class="xml-alert"
            title="双通道编辑：左侧各结构化页面为推荐方式；此处可直接编辑原始 XML（getVMXML / updateVMXML）。"
          />
          <el-card shadow="never">
            <div class="xml-toolbar">
              <el-button :icon="Refresh" @click="loadXML">重新加载</el-button>
              <el-button v-if="canOperate" type="primary" :loading="xmlSaving" @click="saveXML">保存</el-button>
            </div>
            <el-input v-model="xmlText" type="textarea" :rows="18" class="xml-area" placeholder="加载中…" />
          </el-card>
        </section>

        <!-- 文件管理（v2：SSH 在线通道，浏览/下载/上传/删除 VM 内文件） -->
        <section v-show="activeView === 'files'" class="panel">
          <div class="panel-head">
            <h3 class="panel-title">文件管理</h3>
          </div>
          <el-card shadow="never">
            <VmFileBrowser v-if="vm" :id="vm.id" :ip="vm.ip || ''" />
          </el-card>
        </section>

        <!-- 授权管理（统一面板：主体切换/组授权/合并表格/收回分发，完全自持于 VmGrantCard） -->
        <VmGrantCard :vm-id="id" :active="activeView === 'grants'" />
      </el-main>
    </el-container>
  </div>
</template>

<script setup>
// 虚拟机详情页薄壳：只持页头电源操作、左侧分区菜单与 activeView 切换、stats 轮询编排、
// 基础数据获取（loadSpec / XML）。四个重分区已拆至 ./vm-detail/components/：
//   VmPerfCard（性能曲线 + 60 采样）、VmHardwarePanels（处理器/内存/磁盘/网卡 + 硬件对话框）、
//   VmSnapshotCard（快照）、VmGrantCard（授权管理）。
// 分区显隐全部保持 v-show 常驻挂载语义（不许改成 v-if），切走再切回不丢状态。
// 共享数据流：vm/spec/busy/activeView 在壳；分区自持数据各自的 API 调用不经过壳。
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowLeft, Monitor, VideoPlay, VideoPause, SwitchButton, RefreshRight, Delete, Refresh, Odometer, TrendCharts, Cpu, Coin, FolderOpened, Connection, CameraFilled, Document, User } from '@element-plus/icons-vue'
import { api } from '../api'
import VmFileBrowser from '../components/VmFileBrowser.vue'
import { useAuth } from '../store/auth'
import { pollTask, extractTaskId, taskErrorMessage } from '../utils/task.js'
import { POLL_DEFAULTS, getPollInterval } from '../utils/settings'
import { useAutoRefresh } from '../composables/useAutoRefresh'
import { vmStatusText, vmStatusTag, isCancel, fmtDateTime } from '../utils/format'
import VmPerfCard from './vm-detail/components/VmPerfCard.vue'
import GuestMetricsCard from './vm-detail/components/GuestMetricsCard.vue'
import VmHardwarePanels from './vm-detail/components/VmHardwarePanels.vue'
import VmSnapshotCard from './vm-detail/components/VmSnapshotCard.vue'
import VmGrantCard from './vm-detail/components/VmGrantCard.vue'

const route = useRoute()
const router = useRouter()
const { isAdmin, canOperate } = useAuth()
const id = route.params.id

/* ---------- 基础状态 ---------- */
const vm = ref(null)
const spec = ref(null)
const loading = ref(true)
const busy = ref('')

const activeView = ref('overview')
const activeDisk = ref(0)
const activeNic = ref(0)

/* ---------- 性能轮询编排（曲线绘制与 60 采样拼装在 VmPerfCard，壳只负责拉取与分发） ---------- */
// 轮询间隔取系统设置的 vmstats 偏好，独立于 spec（原先硬编码 2000ms，唯一没接入 settings 轮询偏好的定时器）
const statsIntervalMs = getPollInterval('vmstats', POLL_DEFAULTS.vmstats)
const stats = ref(null)
const perfCardRef = ref(null)
// 性能轮询无用户开关：周期取 vmstats 偏好，挂载后起表、卸载自动清理（useAutoRefresh 托管）
// 注意 start() 只起表不触发 fn，保持「挂载后等第一个周期（默认 2s）再拉」的原行为
const { start: startStatsPolling } = useAutoRefresh(pollStats, { intervalMs: statsIntervalMs })

/* ---------- XML ---------- */
const xmlText = ref('')
const xmlSaving = ref(false)

/* ---------- 派生 ---------- */
const isRunning = computed(() => !!(vm.value && vm.value.status === 'running'))
const isPaused = computed(() => !!(vm.value && vm.value.status === 'paused'))
const vmName = computed(() => (spec.value && spec.value.name) || (vm.value && vm.value.name) || '…')
const hostName = computed(() => (vm.value && vm.value.host && vm.value.host.name) || '—')
const createdText = computed(() => fmtDateTime(vm.value && vm.value.created_at))
const macText = computed(() => {
  if (spec.value && spec.value.interfaces && spec.value.interfaces.length) {
    const macs = spec.value.interfaces.map((i) => i.mac).filter(Boolean)
    return macs.length ? macs.join(' / ') : '—'
  }
  return (vm.value && vm.value.mac_address) || '—'
})

const diskMenuItems = computed(() =>
  (spec.value && spec.value.disks
    ? spec.value.disks.map((d, i) => ({ index: `disk-${i}`, target: d.target || `disk${i + 1}` }))
    : [])
)
const nicMenuItems = computed(() =>
  (spec.value && spec.value.interfaces
    ? spec.value.interfaces.map((n, i) => ({
        index: `nic-${i}`,
        // 菜单标签带所属网络，左侧一栏即可看清每块网卡挂在哪个网络
        label: `eth${i + 1}${n.source ? ' · ' + n.source : ''}`
      }))
    : [])
)
const activeMenu = computed(() => {
  if (activeView.value === 'disk') return `disk-${activeDisk.value}`
  if (activeView.value === 'nic') return `nic-${activeNic.value}`
  return activeView.value
})

function onMenuSelect(index) {
  if (index.startsWith('disk-')) {
    activeView.value = 'disk'
    activeDisk.value = Number(index.slice(5))
    return
  }
  if (index.startsWith('nic-')) {
    activeView.value = 'nic'
    activeNic.value = Number(index.slice(4))
    return
  }
  activeView.value = index
}

/* ---------- 数据加载 ---------- */
async function loadSpec() {
  loading.value = true
  try {
    const res = await api.getVMSpec(id)
    vm.value = (res.data && res.data.vm) || null
    spec.value = (res.data && res.data.spec) || null
    if (spec.value && spec.value.disks && activeDisk.value >= spec.value.disks.length) {
      activeDisk.value = Math.max(0, spec.value.disks.length - 1)
    }
    if (spec.value && spec.value.interfaces && activeNic.value >= spec.value.interfaces.length) {
      activeNic.value = Math.max(0, spec.value.interfaces.length - 1)
    }
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '获取虚拟机配置失败'))
  } finally {
    loading.value = false
  }
}

/* ---------- 顶部操作 ---------- */
async function act(type) {
  busy.value = type
  try {
    if (type === 'stop') {
      // 优雅关机走后台任务：提交即返 202，轮询等终态，根治 15s 超时误报
      ElMessage.info('关机任务已提交，正在执行…')
      await pollTask(extractTaskId(await api.stopVM(id)))
      ElMessage.success('已关机')
    } else {
      // start / restart / pause / resume 为快接口，保持同步直调
      await api[type + 'VM'](id)
      ElMessage.success({ start: '已开机', restart: '已重启', pause: '已暂停', resume: '已恢复' }[type])
    }
    await loadSpec()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '操作失败'))
  } finally {
    busy.value = ''
  }
}

async function doDelete() {
  if (!vm.value) return
  try {
    await ElMessageBox.prompt('此操作不可撤销。请输入虚拟机名称「' + vm.value.name + '」以确认删除：', '确认删除', {
      type: 'warning',
      confirmButtonText: '确认删除',
      cancelButtonText: '取消',
      confirmButtonClass: 'el-button--danger',
      inputPlaceholder: vm.value.name,
      inputValidator: (v) => (v && v.trim() === vm.value.name) || '请输入正确的虚拟机名称'
    })
    busy.value = 'delete'
    ElMessage.info('删除任务已提交，正在执行…')
    await pollTask(extractTaskId(await api.deleteVM(id)))
    ElMessage.success('虚拟机已删除')
    router.push('/vms')
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(taskErrorMessage(e, '删除失败'))
  } finally {
    busy.value = ''
  }
}

function goConsole() {
  router.push({ name: 'console', params: { id } })
}
function back() {
  router.push('/vms')
}

/* ---------- 性能轮询（拉取在壳，采样拼装/重绘在 VmPerfCard） ---------- */
function pollStats() {
  if (!vm.value || vm.value.status !== 'running') return
  api
    .getVMStats(id)
    .then((res) => {
      stats.value = res.data || null
      // 采样点追加在 VmPerfCard（传最新采样实参，避免 prop 未刷新读到旧值）
      if (stats.value) perfCardRef.value?.pushSample(stats.value)
      // 「perf 分区激活才重绘」的守卫原样保留（重绘走子组件，隐藏态重绘会把图表 resize 成 0 尺寸）
      if (activeView.value === 'perf') perfCardRef.value?.render()
    })
    .catch(() => {})
}

/* ---------- 概览：开机自启 ---------- */
async function onAutostartChange(val) {
  busy.value = 'autostart'
  try {
    await api.setAutostart(id, val)
    ElMessage.success(val ? '已开启开机自启' : '已关闭开机自启')
    await loadSpec()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '设置自启失败'))
    await loadSpec()
  } finally {
    busy.value = ''
  }
}

/* ---------- XML ---------- */
async function loadXML() {
  try {
    const res = await api.getVMXML(id)
    xmlText.value = (res.data && res.data.xml) || (spec.value && spec.value.raw_xml) || ''
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '获取 XML 失败'))
  }
}

async function saveXML() {
  if (!xmlText.value.trim()) {
    ElMessage.warning('XML 不能为空')
    return
  }
  xmlSaving.value = true
  try {
    await api.updateVMXML(id, xmlText.value)
    ElMessage.success('XML 已保存')
    await loadSpec()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '保存失败'))
  } finally {
    xmlSaving.value = false
  }
}

/* ---------- 生命周期 ---------- */
onMounted(async () => {
  // 快照列表由 VmSnapshotCard 自挂载拉取（与 loadSpec 并发，等价原 Promise.all 两路）
  await loadSpec()
  await loadXML()
  // Prometheus 预填历史曲线：保持在 loadXML 之后触发（原顺序），fire-and-forget 同原
  perfCardRef.value?.prefill()
  startStatsPolling()
})
// stats 轮询定时器由 useAutoRefresh 卸载时自动清理；性能图表 resize 监听与 echarts 实例销毁随 VmPerfCard 卸载执行
</script>

<style scoped>
.vm-detail {
  display: flex;
  flex-direction: column;
  min-height: 100%;
}

/* 顶部工具栏 */
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 12px 16px;
  box-shadow: var(--shadow-sm);
  margin-bottom: 16px;
}
.tb-left {
  display: flex;
  align-items: center;
  gap: 10px;
}
.tb-name {
  font-size: 1.1rem;
  font-weight: 700;
  color: var(--color-foreground);
}
.tb-ip {
  color: var(--color-muted-foreground);
  font-family: var(--font-mono);
  font-size: 0.9rem;
}
.tb-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

/* 主体：左侧导航 + 右侧内容 */
.body {
  flex: 1;
  align-items: stretch;
}
.side {
  background: var(--color-card);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-sm);
  overflow-y: auto;
  position: sticky;
  top: 0;
  align-self: flex-start;
  max-height: calc(100vh - 140px);
}
.side-menu {
  border-right: none;
  height: 100%;
  padding: 8px;
}
.side-menu :deep(.el-menu-item) {
  border-radius: var(--radius-sm);
  margin-bottom: 2px;
}
.side-menu :deep(.el-menu-item.is-active) {
  background: var(--el-color-primary-light-9);
  color: var(--color-primary);
  font-weight: 600;
}
.content {
  padding: 0 0 0 16px;
}

/* 面板头（概览 / XML / 文件管理留在壳的分区；子组件分区各自带同样式副本） */
.panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
}
.panel-title {
  margin: 0;
  font-size: 1rem;
  font-weight: 600;
  color: var(--color-foreground);
}

/* XML */
.xml-alert {
  margin-bottom: 12px;
}
.xml-toolbar {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-bottom: 12px;
}
.xml-area :deep(textarea) {
  font-family: var(--font-mono);
  font-size: 0.82rem;
}

@media (max-width: 900px) {
  .body {
    flex-direction: column;
  }
  .side {
    width: 100% !important;
    position: static;
    max-height: none;
    margin-bottom: 16px;
  }
  .content {
    padding: 0;
  }
}
/* 概览信息行（腾讯云式）：label 灰色固定宽，值区留足行距 */
.ov-desc :deep(.el-descriptions__label) {
  color: var(--el-text-color-secondary);
  min-width: 78px;
}
.ov-desc :deep(.el-descriptions__cell) {
  padding-bottom: 16px;
  vertical-align: middle;
}
</style>
