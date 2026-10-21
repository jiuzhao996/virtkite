<!--
  虚拟机详情页（薄壳）
  ────────────────────────────────────────────────────────────────
  职责：页头电源操作编排、左侧分区菜单与 activeView 切换、stats 轮询编排、基础数据获取（loadSpec / XML / 健康检查）。
  数据流：
    GET /vms/:id/spec → vm/spec（同时驱动菜单、概览与各分区）
    GET /vms/:id/stats 每 vmstats 间隔 → 壳拉取后交 VmPerfCard 追加采样/重绘
    分区自持数据（快照/授权/guest 指标/硬件）各自请求，不经过壳
  子组件（./vm-detail/components/）：
    VmDetailToolbar   顶部工具栏（返回/控制台/电源/固化模板/删除）
    VmHealthAlert     域定义健康告警（幽灵盘摘除）
    VmSideMenu        左侧分区导航
    VmOverviewPanel   概览信息行
    VmXmlPanel        XML 定义编辑
    VmPerfCard / GuestMetricsCard / VmHardwarePanels / VmSnapshotCard / VmGrantCard（既有重分区）
  刻意没拆：文件管理分区（仅 10 行，直接复用 VmFileBrowser）与 stats 轮询编排（壳只拉取分发，
  与 activeView/perfCardRef 紧耦合）；分区显隐全部保持 v-show 常驻挂载语义，不许改成 v-if。
-->
<template>
  <div class="vm-detail" v-loading="loading">
    <!-- 顶部工具栏：返回 / 名称 / 状态 / IP / 操作 -->
    <VmDetailToolbar
      :vm-name="vmName"
      :vm="vm"
      :is-running="isRunning"
      :is-paused="isPaused"
      :can-operate="canOperate"
      :busy="busy"
      :finalize-cap="finalizeCap"
      @back="back"
      @console="goConsole"
      @act="act"
      @finalize="openFinalize"
      @delete="doDelete"
    />

    <!-- 域定义健康检查：磁盘源文件缺失（幽灵盘）会让 libvirt 拒绝启动整个域，而报错
         只在开机时出现、文案是「无法访问存储文件 + uid/gid」，与当初的加盘操作完全脱节 -->
    <VmHealthAlert
      v-if="health && health.checked && !health.ok"
      :health="health"
      :busy="busy"
      @detach="detachBrokenDisk"
    />

    <el-container class="body">
      <!-- 左侧导航 -->
      <el-aside width="216px" class="side">
        <VmSideMenu
          :active-menu="activeMenu"
          :disk-menu-items="diskMenuItems"
          :nic-menu-items="nicMenuItems"
          :can-operate="canOperate"
          :is-admin="isAdmin"
          @select="onMenuSelect"
        />
      </el-aside>

      <!-- 右侧内容区：概览 / XML / 文件管理留在壳，其余分区为 vm-detail/components 下的自持子组件
           （均 v-show 常驻挂载——切走再切回不丢状态、性能曲线不重置） -->
      <el-main class="content">
        <!-- 概览 -->
        <VmOverviewPanel
          v-show="activeView === 'overview'"
          :vm="vm"
          :spec="spec"
          :host-name="hostName"
          :created-text="createdText"
          :mac-text="macText"
          :busy="busy"
          :can-operate="canOperate"
          @autostart-change="onAutostartChange"
          @boot-order-change="onBootOrderChange"
        />

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
        <VmXmlPanel
          v-show="activeView === 'xml'"
          v-model:xml-text="xmlText"
          :xml-saving="xmlSaving"
          :can-operate="canOperate"
          @reload="loadXML"
          @save="saveXML"
        />

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
    <!-- 固化为模板（模板制作 / 镜像清洗）：关机态可见；弹窗组件与镜像管理页「模板制作」共用 -->
    <VmFinalizeDialog ref="finalizeRef" v-model="finalizeDlg" :vm-id="id" :vm-name="vmName" @done="onFinalized" />
  </div>
</template>

<script setup>
// 虚拟机详情页薄壳：只持页头电源操作、左侧分区菜单与 activeView 切换、stats 轮询编排、
// 基础数据获取（loadSpec / XML）。重分区已拆至 ./vm-detail/components/：
//   VmPerfCard（性能曲线 + 60 采样）、VmHardwarePanels（处理器/内存/磁盘/网卡 + 硬件对话框）、
//   VmSnapshotCard（快照）、VmGrantCard（授权管理）；展示壳拆为 VmDetailToolbar / VmHealthAlert /
//   VmSideMenu / VmOverviewPanel / VmXmlPanel。
// 分区显隐全部保持 v-show 常驻挂载语义（不许改成 v-if），切走再切回不丢状态。
// 共享数据流：vm/spec/busy/activeView 在壳；分区自持数据各自的 API 调用不经过壳。
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '../api'
import VmFileBrowser from '../components/VmFileBrowser.vue'
import VmFinalizeDialog from '../components/VmFinalizeDialog.vue'
import { useAuth } from '../store/auth'
import { pollTask, extractTaskId, taskErrorMessage } from '../utils/task.js'
import { POLL_DEFAULTS, getPollInterval } from '../utils/settings'
import { useAutoRefresh } from '../composables/useAutoRefresh'
import { isCancel, fmtDateTime, errMsg } from '../utils/format'
import { deletePreviewHtml } from '../utils/vm-delete-preview'
import VmDetailToolbar from './vm-detail/components/VmDetailToolbar.vue'
import VmHealthAlert from './vm-detail/components/VmHealthAlert.vue'
import VmSideMenu from './vm-detail/components/VmSideMenu.vue'
import VmOverviewPanel from './vm-detail/components/VmOverviewPanel.vue'
import VmXmlPanel from './vm-detail/components/VmXmlPanel.vue'
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
// 域定义健康检查（幽灵盘）：随详情一起拉，失败静默（这是增强提示，不该挡住页面）
const health = ref(null)
async function loadHealth() {
  try {
    const res = await api.domainHealth(id)
    health.value = res.data || null
  } catch (e) {
    health.value = null
  }
}
// 摘除失效磁盘：只从域定义移除条目（deleteVolume=false），不动文件
async function detachBrokenDisk(d) {
  try {
    await ElMessageBox.confirm(
      `摘除磁盘 ${d.target}（${d.source}）？仅从域定义移除该条目，不会删除任何文件。`,
      '摘除失效磁盘',
      { type: 'warning', confirmButtonText: '摘除', confirmButtonClass: 'el-button--danger' }
    )
  } catch (e) {
    return
  }
  busy.value = 'detach-' + d.target
  try {
    await api.detachDisk(id, d.target, false)
    ElMessage.success('已摘除，现在可以开机了')
    await Promise.all([loadSpec(), loadHealth()])
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '摘除失败'))
  } finally {
    busy.value = ''
  }
}

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
  // A 批次：删前先拉预检，把「磁盘/快照会怎样」摊在确认框里；预检失败不阻断（退回纯名称确认）
  let previewHtml = ''
  try {
    const res = await api.deleteVMPreview(id)
    previewHtml = deletePreviewHtml(res.data)
  } catch (e) {
    previewHtml = ''
  }
  const head = '<p class="dp-head">此操作不可撤销。将被删除：</p>'
  const tail = '<p class="dp-tail">请输入虚拟机名称「' + vm.value.name + '」以确认删除：</p>'
  try {
    await ElMessageBox.prompt(previewHtml ? head + previewHtml + tail : '此操作不可撤销。请输入虚拟机名称「' + vm.value.name + '」以确认删除：', '确认删除', {
      type: 'warning',
      confirmButtonText: '确认删除',
      cancelButtonText: '取消',
      confirmButtonClass: 'el-button--danger',
      dangerouslyUseHTMLString: !!previewHtml,
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

/* ---------- 固化为模板（模板制作 / 镜像清洗）：弹窗在 VmFinalizeDialog 组件 ---------- */
const finalizeDlg = ref(false)
const finalizeRef = ref(null)
const finalizeCap = ref({ guestfs_available: false, version: '', reason: '', vm_shutoff: false })

async function loadFinalizeCap() {
  try {
    finalizeCap.value = (await api.finalizeVMCapability(id)).data || {}
  } catch (e) {
    finalizeCap.value = { guestfs_available: false, reason: errMsg(e, '能力探测失败') }
  }
}

function openFinalize() {
  if (!finalizeCap.value.guestfs_available) {
    ElMessage.warning('镜像清洗工具不可用：' + (finalizeCap.value.reason || '请在宿主机安装 guestfs-tools'))
    return
  }
  finalizeRef.value?.prepare()
  finalizeDlg.value = true
}

// 固化成功：制作机已被移除，去镜像管理看模板
function onFinalized() {
  router.push('/images')
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

/* ---------- 概览：启动顺序 ---------- */
// 只写持久配置，下次启动生效；运行中的 VM 不会被重启。
async function onBootOrderChange(devices) {
  busy.value = 'boot'
  try {
    await api.setBootOrder(id, devices)
    ElMessage.success('启动顺序已更新（下次启动生效）')
    await loadSpec()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '修改启动顺序失败'))
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
  // 与 XML 拉取并发：健康检查失败静默，不拖慢首屏
  loadHealth()
  loadFinalizeCap() // 固化为模板的能力探测（fire-and-forget）
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
.content {
  padding: 0 0 0 16px;
}

/* 面板头（仅文件管理分区留在壳；概览/XML 子组件各自带同样式副本，改一处须同步） */
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
</style>
