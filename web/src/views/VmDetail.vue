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
        <el-button type="primary" :icon="Monitor" :disabled="!isRunning" @click="goConsole">控制台</el-button>
        <!-- 电源 / 挂起 状态切换按钮：一个按钮按当前状态显示对应动作（运行中→关机/暂停，关机→开机，暂停→恢复）。
             语义保持与拆分版一致：暂停态须先恢复（电源钮禁用），关机态禁用挂起钮；busy 期间锁定防止动作切换闪烁 -->
        <el-button
          v-if="isAdmin && vm"
          :icon="isRunning ? SwitchButton : VideoPlay"
          :loading="busy === 'stop' || busy === 'start'"
          :disabled="isPaused || (!!busy && busy !== 'resume')"
          @click="act(isRunning ? 'stop' : 'start')"
        >{{ isRunning ? '关机' : '开机' }}</el-button>
        <el-button
          v-if="isAdmin && vm"
          :icon="isPaused ? VideoPlay : VideoPause"
          :loading="busy === 'pause' || busy === 'resume'"
          :disabled="(!isRunning && !isPaused) || (!!busy && busy !== 'stop' && busy !== 'start')"
          @click="act(isPaused ? 'resume' : 'pause')"
        >{{ isPaused ? '恢复' : '暂停' }}</el-button>
        <el-button v-if="isAdmin && vm" :icon="RefreshRight" :loading="busy === 'restart'" :disabled="!isRunning" @click="act('restart')">重启</el-button>
        <el-button v-if="isAdmin" type="danger" :icon="Delete" :loading="busy === 'delete'" @click="doDelete">删除</el-button>
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
        </el-menu>
      </el-aside>

      <!-- 右侧内容区 -->
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
                  :disabled="!spec || !isAdmin"
                  @change="onAutostartChange"
                />
              </el-descriptions-item>
            </el-descriptions>
          </el-card>
        </section>

        <!-- 处理器 -->
        <section v-show="activeView === 'cpu'" class="panel">
          <div class="panel-head">
            <h3 class="panel-title">处理器</h3>
          </div>
          <el-card shadow="never" class="edit-card">
            <div class="field-row">
              <span class="field-label">vCPU 数</span>
              <el-input-number v-model="vcpuInput" :min="1" :max="256" controls-position="right" :disabled="!isAdmin" />
              <el-button v-if="isAdmin" type="primary" :loading="busy === 'vcpu'" :disabled="!spec" @click="applyVcpu">应用</el-button>
            </div>
            <p class="field-tip">热调整：live + config 双生效，运行中即可在线增减 CPU 核数。</p>
          </el-card>
        </section>

        <!-- 内存 -->
        <section v-show="activeView === 'memory'" class="panel">
          <div class="panel-head">
            <h3 class="panel-title">内存</h3>
          </div>
          <el-card shadow="never" class="edit-card">
            <div class="field-row">
              <span class="field-label">内存大小（MB）</span>
              <el-input-number v-model="memInput" :min="256" :step="256" controls-position="right" :disabled="!isAdmin" />
              <el-button v-if="isAdmin" type="primary" :loading="busy === 'memory'" :disabled="!spec" @click="applyMemory">应用</el-button>
            </div>
            <p class="field-tip">热调整：需 ≥ 当前占用，运行中可在线调整（live + config）。</p>
          </el-card>
        </section>


        <!-- 磁盘 -->
        <section v-show="activeView === 'disk'" class="panel">
          <div class="panel-head">
            <h3 class="panel-title">磁盘</h3>
          </div>
          <el-empty v-if="!(spec && spec.disks && spec.disks.length)" description="暂无磁盘设备" :image-size="80" />
          <template v-else>
            <div
              v-for="(disk, i) in spec.disks"
              :key="(disk.target || 'disk') + i"
              class="dev-card"
              :class="{ selected: i === activeDisk }"
              @click="activeDisk = i"
            >
              <div class="dev-card-head">
                <span class="dev-name mono">{{ disk.target || '—' }}</span>
                <el-tag :type="disk.device === 'cdrom' ? 'warning' : 'info'" size="small" effect="light">{{ disk.device }}</el-tag>
                <!-- 移除：打开确认弹窗（可选择是否同时删除存储卷），替代原先的 popconfirm -->
                <el-button v-if="isAdmin" size="small" type="danger" text class="dev-remove" :icon="Delete" @click="openRemoveDisk(disk)">移除</el-button>
              </div>
              <el-descriptions :column="2" size="small" class="dev-desc">
                <el-descriptions-item label="目标">{{ disk.target || '—' }}</el-descriptions-item>
                <el-descriptions-item label="总线">{{ disk.bus || '—' }}</el-descriptions-item>
                <el-descriptions-item label="驱动">{{ disk.driver || '—' }}</el-descriptions-item>
                <el-descriptions-item label="只读">{{ disk.read_only ? '是' : '否' }}</el-descriptions-item>
                <el-descriptions-item label="类型">{{ disk.type || '—' }}</el-descriptions-item>
                <el-descriptions-item label="设备">{{ disk.device || '—' }}</el-descriptions-item>
                <el-descriptions-item label="源路径" :span="2">{{ disk.source || '—' }}</el-descriptions-item>
                <el-descriptions-item v-if="disk.backing_file" label="父卷" :span="2">{{ disk.backing_file }}</el-descriptions-item>
              </el-descriptions>
            </div>
          </template>
          <div class="panel-actions">
            <el-button v-if="isAdmin" type="success" plain :icon="MagicStick" :loading="quickDiskLoading" @click="quickAddDisk">一键数据盘（20G）</el-button>
            <el-tooltip placement="top" content="自动检查并补齐两件标准配置：① guest-agent 通信通道——装了 qemu-guest-agent 的虚拟机靠它向平台上报 IP；② virtio-rng 随机数设备——提升虚拟机熵池，加快开机。已存在的会自动跳过，缺什么补什么。">
              <el-button v-if="isAdmin" plain :icon="Connection" :loading="standardLoading" @click="ensureStandard">补齐标准设备</el-button>
            </el-tooltip>
            <el-button v-if="isAdmin" type="primary" :icon="Plus" @click="openDiskDialog">添加磁盘</el-button>
          </div>
        </section>

        <!-- 网卡 -->
        <section v-show="activeView === 'nic'" class="panel">
          <div class="panel-head">
            <h3 class="panel-title">网卡</h3>
          </div>
          <el-empty v-if="!(spec && spec.interfaces && spec.interfaces.length)" description="暂无网卡设备" :image-size="80" />
          <template v-else>
            <div
              v-for="(nic, i) in spec.interfaces"
              :key="(nic.mac || 'nic') + i"
              class="dev-card"
              :class="{ selected: i === activeNic }"
              @click="activeNic = i"
            >
              <div class="dev-card-head">
                <span class="dev-name mono">{{ nic.mac || '—' }}</span>
                <el-tag type="info" size="small" effect="light">{{ nic.model }}</el-tag>
                <el-popconfirm v-if="isAdmin" :title="'确定移除网卡「' + (nic.mac || '') + '」？'" width="220" @confirm="removeNic(nic)">
                  <template #reference>
                    <el-button size="small" type="danger" text :icon="Delete">移除</el-button>
                  </template>
                </el-popconfirm>
              </div>
              <el-descriptions :column="2" size="small" class="dev-desc">
                <el-descriptions-item label="MAC">{{ nic.mac || '—' }}</el-descriptions-item>
                <el-descriptions-item label="型号">{{ nic.model || '—' }}</el-descriptions-item>
                <el-descriptions-item label="类型">{{ nic.type || '—' }}</el-descriptions-item>
                <el-descriptions-item label="网络">{{ nic.source || '—' }}</el-descriptions-item>
              </el-descriptions>
            </div>
          </template>
          <div class="panel-actions">
            <el-button v-if="isAdmin" type="success" plain :icon="MagicStick" :loading="quickNicLoading" @click="quickAddNic">一键网卡（default）</el-button>
            <el-button v-if="isAdmin" type="primary" :icon="Plus" @click="openNicDialog">添加网卡</el-button>
          </div>
        </section>

        <!-- 快照 -->
        <section v-show="activeView === 'snapshots'" class="panel">
          <div class="panel-head">
            <h3 class="panel-title">快照</h3>
            <el-button v-if="isAdmin" type="primary" :icon="Plus" @click="openSnapCreate">新建快照</el-button>
          </div>
          <el-card shadow="never">
            <el-table :data="snapshots" size="small" border style="width: 100%" v-loading="snapLoading">
              <template #empty><el-empty description="暂无快照" :image-size="70" /></template>
              <el-table-column prop="name" label="名称" min-width="160" />
              <el-table-column prop="description" label="描述" min-width="200" show-overflow-tooltip />
              <el-table-column label="创建时间" min-width="170">
                <template #default="{ row }">{{ row.timeText }}</template>
              </el-table-column>
              <el-table-column label="状态" width="90">
                <template #default="{ row }">
                  <el-tag :type="row.stateTag" size="small" effect="light">{{ row.stateText }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column label="操作" width="180" fixed="right">
                <template #default="{ row }">
                  <el-button v-if="isAdmin" size="small" :icon="RefreshLeft" @click="revertSnap(row)">回滚</el-button>
                  <el-button v-if="isAdmin" size="small" type="danger" :icon="Delete" @click="removeSnap(row)">删除</el-button>
                </template>
              </el-table-column>
            </el-table>
          </el-card>
        </section>

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
              <el-button v-if="isAdmin" type="primary" :loading="xmlSaving" @click="saveXML">保存</el-button>
            </div>
            <el-input v-model="xmlText" type="textarea" :rows="18" class="xml-area" placeholder="加载中…" />
          </el-card>
        </section>
      </el-main>
    </el-container>

    <!-- 添加磁盘 -->
    <el-dialog v-model="diskDialog" title="添加磁盘" width="460px">
      <el-form ref="diskFormRef" :model="diskForm" :rules="diskRules" label-width="96px">
        <el-form-item label="设备类型" prop="device">
          <el-select v-model="diskForm.device" style="width: 100%">
            <el-option label="磁盘 (disk)" value="disk" />
            <el-option label="光盘 (cdrom)" value="cdrom" />
          </el-select>
        </el-form-item>
        <el-form-item label="总线" prop="bus">
          <el-select v-model="diskForm.bus" style="width: 100%">
            <el-option v-for="b in ['virtio', 'ide', 'sata', 'scsi']" :key="b" :label="b" :value="b" />
          </el-select>
        </el-form-item>
        <el-form-item label="驱动" prop="driver">
          <el-select v-model="diskForm.driver" style="width: 100%">
            <el-option v-for="d in ['qcow2', 'raw', 'iso']" :key="d" :label="d" :value="d" />
          </el-select>
        </el-form-item>
        <el-form-item label="源路径" prop="source">
          <el-input v-model="diskForm.source" placeholder="/var/lib/libvirt/images/xxx.qcow2" />
        </el-form-item>
        <el-form-item label="只读">
          <el-switch v-model="diskForm.read_only" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="diskDialog = false">取消</el-button>
        <el-button type="primary" :loading="diskSaving" @click="submitDisk">添加</el-button>
      </template>
    </el-dialog>

    <!-- 移除磁盘确认弹窗：默认仅分离保留卷；「分离并删除存储卷」为危险项，cdrom（ISO 介质）不提供删卷 -->
    <el-dialog v-model="removeDiskDialog" title="移除磁盘" width="480px">
      <p class="rm-disk-tip">
        磁盘「<span class="mono">{{ (removeDiskForm.disk && removeDiskForm.disk.target) || '—' }}</span>」将从此虚拟机移除（运行中为热分离，关机状态为改配置），请选择存储卷的处理方式：
      </p>
      <el-radio-group v-model="removeDiskForm.deleteVolume" class="rm-disk-options">
        <el-radio :value="false" class="rm-opt">
          <span class="rm-opt-text">
            <span class="rm-opt-title">仅分离（保留存储卷）</span>
            <span class="rm-opt-desc">只把磁盘从虚拟机配置中卸载，存储池中的卷文件原样保留，可再次挂载。</span>
          </span>
        </el-radio>
        <el-radio :value="true" class="rm-opt" :disabled="removeDiskIsCdrom">
          <span class="rm-opt-text">
            <span class="rm-opt-title is-danger">分离并删除存储卷</span>
            <span class="rm-opt-desc is-danger">
              <el-icon class="rm-opt-icon"><WarningFilled /></el-icon>从存储池中删除该卷文件，不可恢复。
            </span>
            <span v-if="removeDiskIsCdrom" class="rm-opt-desc is-hint">ISO 安装介质为共享文件，不随分离删除。</span>
          </span>
        </el-radio>
      </el-radio-group>
      <template #footer>
        <el-button @click="removeDiskDialog = false">取消</el-button>
        <el-button type="danger" :loading="removeDiskSaving" :disabled="!removeDiskForm.disk || !removeDiskForm.disk.target" @click="confirmRemoveDisk">确认移除</el-button>
      </template>
    </el-dialog>

    <!-- 添加网卡 -->
    <el-dialog v-model="nicDialog" title="添加网卡" width="460px">
      <el-form ref="nicFormRef" :model="nicForm" :rules="nicRules" label-width="96px">
        <el-form-item label="网络" prop="source">
          <el-select v-model="nicForm.source" filterable allow-create default-first-option placeholder="选择或输入网络名" style="width: 100%">
            <el-option v-for="n in networks" :key="n" :label="n" :value="n" />
          </el-select>
        </el-form-item>
        <el-form-item label="型号" prop="model">
          <el-select v-model="nicForm.model" style="width: 100%">
            <el-option v-for="m in ['virtio', 'e1000', 'rtl8139']" :key="m" :label="m" :value="m" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="nicDialog = false">取消</el-button>
        <el-button type="primary" :loading="nicSaving" @click="submitNic">添加</el-button>
      </template>
    </el-dialog>

    <!-- 新建快照 -->
    <el-dialog v-model="snapDialog" title="新建快照" width="440px">
      <el-form label-width="72px">
        <el-form-item label="名称" required>
          <el-input v-model="snapForm.name" placeholder="如 snap-20260904" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="snapForm.description" type="textarea" :rows="3" placeholder="快照用途说明（可选）" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="snapDialog = false">取消</el-button>
        <el-button type="primary" :loading="snapSaving" @click="submitSnapshot">创建</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, computed, watch, onMounted, onUnmounted, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import * as echarts from 'echarts'
import { ArrowLeft, Monitor, VideoPlay, VideoPause, SwitchButton, RefreshRight, Delete, Plus, Refresh, RefreshLeft, Odometer, TrendCharts, Cpu, Coin, FolderOpened, Connection, CameraFilled, Document, MagicStick, Edit, WarningFilled } from '@element-plus/icons-vue'
import { api } from '../api'
import { useAuth } from '../store/auth'
import { pollTask, extractTaskId, taskErrorMessage } from '../utils/task.js'
import { POLL_DEFAULTS, getPollInterval } from '../utils/settings'
import { vmStatusText, vmStatusTag, usageColor, fmtRateBytes, nowClock, isCancel, cssVar } from '../utils/format'

const route = useRoute()
const router = useRouter()
const { isAdmin } = useAuth()
const id = route.params.id

// echarts 不解析 var()，实时曲线需要真实色值：挂载时读一次 CSS 变量
const CHART_CPU_COLOR = cssVar('--el-color-primary', '#2a9da5')
const CHART_MEM_COLOR = cssVar('--color-warning', '#d97706')
const CHART_AXIS_COLOR = cssVar('--color-info', '#64748b')

/* ---------- 基础状态 ---------- */
const vm = ref(null)
const spec = ref(null)
const loading = ref(true)
const busy = ref('')

const activeView = ref('overview')
const activeDisk = ref(0)
const activeNic = ref(0)

/* ---------- 性能（轮询间隔取系统设置的 vmstats 偏好，独立于 spec） ---------- */
// 原先硬编码 2000ms，是唯一没接入 utils/settings.js 轮询偏好的定时器
const statsIntervalMs = getPollInterval('vmstats', POLL_DEFAULTS.vmstats)
const stats = ref(null)
const cpuHistory = ref([])
const memHistory = ref([])
const perfChartEl = ref(null)
let statsTimer = null
let perfChart = null

/* ---------- 快照 / XML ---------- */
const snapshots = ref([])
const snapLoading = ref(false)
const xmlText = ref('')
const xmlSaving = ref(false)

/* ---------- 添加磁盘 / 网卡 / 快照 弹窗 ---------- */
const diskDialog = ref(false)
const diskFormRef = ref(null)
const diskForm = reactive({ device: 'disk', bus: 'virtio', driver: 'qcow2', source: '', read_only: false })
const diskSaving = ref(false)

const nicDialog = ref(false)
const nicFormRef = ref(null)
const nicForm = reactive({ source: '', model: 'virtio' })
const nicSaving = ref(false)
const networks = ref([])

// 一键添加硬件
const quickDiskLoading = ref(false)
const quickNicLoading = ref(false)
const standardLoading = ref(false)

async function quickAddDisk() {
  quickDiskLoading.value = true
  try {
    const res = await api.quickAttachDisk(id, { size_gb: 20 })
    const d = res.data || {}
    ElMessage.success('已创建并挂载 20G 数据盘：' + (d.volume || '') + '（' + (d.pool || '') + '）')
    await loadSpec()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '一键添加数据盘失败'))
  } finally {
    quickDiskLoading.value = false
  }
}

async function quickAddNic() {
  quickNicLoading.value = true
  try {
    await api.attachInterface(id, { type: 'network', source: 'default', model: 'virtio' })
    ElMessage.success('已在 default 网络添加 virtio 网卡')
    await loadSpec()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '一键添加网卡失败'))
  } finally {
    quickNicLoading.value = false
  }
}

// 补齐标准设备：给存量虚拟机挂 guest-agent 通道与 virtio-rng（新装机已默认携带），幂等
async function ensureStandard() {
  standardLoading.value = true
  try {
    const res = await api.ensureStandardDevices(id)
    const d = res.data || {}
    if (d.attached && d.attached.length) ElMessage.success(d.message || '已补齐标准设备')
    else ElMessage.info('已是标准配置，无需补齐')
    await loadSpec()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '补齐标准设备失败'))
  } finally {
    standardLoading.value = false
  }
}

const snapDialog = ref(false)
const snapForm = reactive({ name: '', description: '' })
const snapSaving = ref(false)

const diskRules = {
  source: [{ required: true, message: '请输入磁盘路径', trigger: 'blur' }]
}
const nicRules = {
  source: [{ required: true, message: '请输入或选择网络名', trigger: 'blur' }]
}

/* ---------- 派生 ---------- */
const isRunning = computed(() => !!(vm.value && vm.value.status === 'running'))
const isPaused = computed(() => !!(vm.value && vm.value.status === 'paused'))
const vmName = computed(() => (spec.value && spec.value.name) || (vm.value && vm.value.name) || '…')
const hostName = computed(() => (vm.value && vm.value.host && vm.value.host.name) || '—')
const createdText = computed(() =>
  vm.value && vm.value.created_at ? new Date(vm.value.created_at).toLocaleString() : '—'
)
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
    ? spec.value.interfaces.map((n, i) => ({ index: `nic-${i}`, label: `eth${i + 1} ${n.mac || ''}`.trim() }))
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

/* ---------- IP 手动编辑（仅管理员） ---------- */
// 简单格式校验：IPv4 点分十进制 / IPv6（含 :: 缩写，须含冒号；不做完整性语义检查，后端为准）
const ipv4Re = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/
const ipv6Re = /^([0-9a-fA-F]{0,4}:){1,7}[0-9a-fA-F]{0,4}$/


const vcpuInput = ref(1)
const memInput = ref(1024)
watch(
  () => spec.value,
  (s) => {
    if (s) {
      vcpuInput.value = s.vcpu
      memInput.value = s.memory_mb
    }
  },
  { immediate: true }
)

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

/* ---------- 性能轮询 + echarts ---------- */
const memUsedMB = computed(() => ((stats.value && stats.value.mem_used_kib) || 0) / 1024)
const memTotalMB = computed(() => ((stats.value && stats.value.mem_total_kib) || 0) / 1024)
const guestUsedMB = computed(() => ((stats.value && stats.value.guest_used_kib) || 0) / 1024)
const guestTotalMB = computed(() => ((stats.value && stats.value.guest_total_kib) || 0) / 1024)
const hasGuestMem = computed(() => guestTotalMB.value > 0)
const memPct = computed(() => {
  if (hasGuestMem.value) return Math.min(100, (guestUsedMB.value / guestTotalMB.value) * 100)
  if (memTotalMB.value > 0) return Math.min(100, (memUsedMB.value / memTotalMB.value) * 100)
  return 0
})
const cpuPct = computed(() => Math.max(0, Math.min(100, Number((stats.value && stats.value.cpu_percent) || 0))))

function memText() {
  if (hasGuestMem.value) return guestUsedMB.value.toFixed(0) + ' / ' + guestTotalMB.value.toFixed(0) + ' MB'
  return memUsedMB.value.toFixed(0) + ' / ' + memTotalMB.value.toFixed(0) + ' MB'
}

function pushHistory() {
  const t = nowClock()
  cpuHistory.value.push({ t, v: Number(cpuPct.value.toFixed(1)) })
  memHistory.value.push({ t, v: Number(memPct.value.toFixed(1)) })
  if (cpuHistory.value.length > 60) cpuHistory.value.shift()
  if (memHistory.value.length > 60) memHistory.value.shift()
}

function pollStats() {
  if (!vm.value || vm.value.status !== 'running') return
  api
    .getVMStats(id)
    .then((res) => {
      stats.value = res.data || null
      if (stats.value) pushHistory()
      if (activeView.value === 'perf') renderPerfChart()
    })
    .catch(() => {})
}

// 进详情页时从 Prometheus 预填历史曲线（替代"从零攒点、刷新即失"）：
// 拉不到（监控栈未起/VM 从未运行）静默降级为原行为。之后轮询继续无缝追加。
async function prefillStatsHistory() {
  try {
    const res = await api.vmStatsHistory(id, 30)
    const pts = (res.data && res.data.points) || []
    if (!pts.length) return
    const recent = pts.slice(-60)
    cpuHistory.value = recent.map((p) => ({ t: p.t, v: p.cpu }))
    memHistory.value = recent.map((p) => ({ t: p.t, v: p.mem }))
    if (activeView.value === 'perf') renderPerfChart()
  } catch (e) {
    /* 静默降级 */
  }
}

function initPerfChart() {
  if (perfChart || !perfChartEl.value) return
  perfChart = echarts.init(perfChartEl.value)
  window.addEventListener('resize', onWinResize)
}
function onWinResize() {
  if (perfChart) perfChart.resize()
}
function renderPerfChart() {
  if (!perfChart || !perfChartEl.value) return
  perfChart.resize()
  perfChart.setOption(
    {
      tooltip: { trigger: 'axis' },
      legend: { data: ['CPU %', '内存 %'], bottom: 0, itemWidth: 14, itemHeight: 8, textStyle: { fontSize: 11 } },
      grid: { left: 8, right: 12, top: 28, bottom: 30, containLabel: true },
      xAxis: {
        type: 'category',
        boundaryGap: false,
        data: cpuHistory.value.map((p) => p.t),
        axisLabel: { fontSize: 10, color: CHART_AXIS_COLOR }
      },
      yAxis: { type: 'value', min: 0, max: 100, axisLabel: { fontSize: 10, color: CHART_AXIS_COLOR } },
      series: [
        {
          name: 'CPU %',
          type: 'line',
          smooth: true,
          showSymbol: false,
          data: cpuHistory.value.map((p) => p.v),
          lineStyle: { width: 2, color: CHART_CPU_COLOR },
          itemStyle: { color: CHART_CPU_COLOR },
          areaStyle: { opacity: 0.06, color: CHART_CPU_COLOR }
        },
        {
          name: '内存 %',
          type: 'line',
          smooth: true,
          showSymbol: false,
          data: memHistory.value.map((p) => p.v),
          lineStyle: { width: 2, color: CHART_MEM_COLOR },
          itemStyle: { color: CHART_MEM_COLOR },
          areaStyle: { opacity: 0.06, color: CHART_MEM_COLOR }
        }
      ]
    },
    true
  )
}

watch(activeView, (v) => {
  if (v === 'perf') nextTick(() => {
    initPerfChart()
    renderPerfChart()
  })
})
watch(isRunning, (r) => {
  if (r && activeView.value === 'perf') nextTick(() => {
    initPerfChart()
    renderPerfChart()
  })
})

/* ---------- 处理器 / 内存 / 引导 ---------- */
async function applyVcpu() {
  const n = vcpuInput.value
  if (!n || n <= 0) {
    ElMessage.warning('vCPU 数量必须大于 0')
    return
  }
  busy.value = 'vcpu'
  try {
    await api.setVcpu(id, n)
    ElMessage.success('vCPU 已调整为 ' + n + ' 核（live + config）')
    await loadSpec()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '调整失败'))
  } finally {
    busy.value = ''
  }
}

async function applyMemory() {
  const m = memInput.value
  if (!m || m <= 0) {
    ElMessage.warning('内存大小必须大于 0')
    return
  }
  busy.value = 'memory'
  try {
    await api.setMemory(id, m)
    ElMessage.success('内存已调整为 ' + m + ' MB（live + config）')
    await loadSpec()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '调整失败'))
  } finally {
    busy.value = ''
  }
}


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

/* ---------- 磁盘 / 网卡 ---------- */
// 移除磁盘弹窗：默认安全项「仅分离（保留存储卷）」；「分离并删除存储卷」为危险项，cdrom（ISO 介质）禁用
const removeDiskDialog = ref(false)
const removeDiskForm = reactive({ disk: null, deleteVolume: false })
const removeDiskSaving = ref(false)
// cdrom 为共享安装介质（ISO 文件可被多台虚拟机引用），不允许随分离删除
const removeDiskIsCdrom = computed(() => !!(removeDiskForm.disk && removeDiskForm.disk.device === 'cdrom'))

function openRemoveDisk(disk) {
  removeDiskForm.disk = disk
  removeDiskForm.deleteVolume = false // 每次打开都重置回默认项，避免上一次的选择残留
  removeDiskDialog.value = true
}

// 确认移除：按单选结果附带 delete_volume 传给分离接口；后端契约 { vm, target, volume_deleted, keep_reason }
async function confirmRemoveDisk() {
  const disk = removeDiskForm.disk
  if (!disk || !disk.target) return
  removeDiskSaving.value = true
  try {
    // cdrom 的删卷选项已被禁用，这里再兜底一次，防止状态残留误传 true
    const res = await api.detachDisk(id, disk.target, removeDiskForm.deleteVolume && !removeDiskIsCdrom.value)
    const d = (res && res.data) || {}
    if (d.volume_deleted) ElMessage.success('已分离并删除卷')
    else if (d.keep_reason) ElMessage.success('已分离，卷保留：' + d.keep_reason)
    else ElMessage.success('已分离，卷已保留')
    removeDiskDialog.value = false
    await loadSpec()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '移除磁盘失败'))
  } finally {
    removeDiskSaving.value = false
  }
}

function openDiskDialog() {
  diskForm.device = 'disk'
  diskForm.bus = 'virtio'
  diskForm.driver = 'qcow2'
  diskForm.source = ''
  diskForm.read_only = false
  if (diskFormRef.value) diskFormRef.value.clearValidate()
  diskDialog.value = true
}

async function submitDisk() {
  if (!diskFormRef.value) return
  try {
    await diskFormRef.value.validate()
  } catch (_) {
    return
  }
  diskSaving.value = true
  try {
    const disk = {
      device: diskForm.device,
      bus: diskForm.bus,
      driver: diskForm.driver,
      source: diskForm.source.trim(),
      read_only: diskForm.read_only
    }
    await api.attachDisk(id, disk)
    ElMessage.success('磁盘已添加')
    diskDialog.value = false
    await loadSpec()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '添加磁盘失败'))
  } finally {
    diskSaving.value = false
  }
}

async function removeNic(nic) {
  if (!nic || !nic.mac) return
  try {
    await api.detachInterface(id, nic.mac)
    ElMessage.success('网卡已移除')
    await loadSpec()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '移除网卡失败'))
  }
}

async function openNicDialog() {
  nicForm.source = ''
  nicForm.model = 'virtio'
  if (nicFormRef.value) nicFormRef.value.clearValidate()
  if (!networks.value.length) {
    try {
      const res = await api.vmOptions()
      networks.value = (res.data && res.data.networks) || []
    } catch (e) {}
  }
  nicDialog.value = true
}

async function submitNic() {
  if (!nicFormRef.value) return
  try {
    await nicFormRef.value.validate()
  } catch (_) {
    return
  }
  nicSaving.value = true
  try {
    const iface = { type: 'network', source: nicForm.source.trim(), model: nicForm.model }
    await api.attachInterface(id, iface)
    ElMessage.success('网卡已添加')
    nicDialog.value = false
    await loadSpec()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '添加网卡失败'))
  } finally {
    nicSaving.value = false
  }
}

/* ---------- 快照 ---------- */
async function loadSnapshots() {
  snapLoading.value = true
  try {
    const res = await api.listSnapshots(id)
    const raw = (res.data && (res.data.items || res.data)) || []
    snapshots.value = raw.map((s) => ({
      ...s,
      timeText: s.creation_time ? new Date(s.creation_time * 1000).toLocaleString() : '—',
      stateText: vmStatusText(s.state, '—'),
      stateTag: vmStatusTag(s.state)
    }))
  } catch (e) {
    snapshots.value = []
  } finally {
    snapLoading.value = false
  }
}

function openSnapCreate() {
  snapForm.name = ''
  snapForm.description = ''
  snapDialog.value = true
}

async function submitSnapshot() {
  if (!snapForm.name.trim()) {
    ElMessage.warning('快照名称不能为空')
    return
  }
  snapSaving.value = true
  try {
    await api.createSnapshot(id, snapForm.name.trim(), snapForm.description.trim())
    ElMessage.success('快照已创建')
    snapDialog.value = false
    await loadSnapshots()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '创建快照失败'))
  } finally {
    snapSaving.value = false
  }
}

async function revertSnap(snap) {
  try {
    await ElMessageBox.confirm('确定回滚到快照「' + snap.name + '」？此操作会覆盖当前状态。', '确认回滚', { type: 'warning' })
    await api.revertSnapshot(id, snap.name)
    ElMessage.success('已回滚')
    await loadSnapshots()
    await loadSpec()
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(taskErrorMessage(e, '回滚失败'))
  }
}

async function removeSnap(snap) {
  try {
    await ElMessageBox.confirm('确定删除快照「' + snap.name + '」？', '确认删除', { type: 'warning' })
    await api.deleteSnapshot(id, snap.name)
    ElMessage.success('快照已删除')
    await loadSnapshots()
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(taskErrorMessage(e, '删除失败'))
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
  await loadSpec()
  await loadSnapshots()
  await loadXML()
  prefillStatsHistory()
  statsTimer = setInterval(pollStats, statsIntervalMs)
})

onUnmounted(() => {
  if (statsTimer) {
    clearInterval(statsTimer)
    statsTimer = null
  }
  window.removeEventListener('resize', onWinResize)
  if (perfChart) {
    perfChart.dispose()
    perfChart = null
  }
})
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
  background: #fff;
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
  background: #fff;
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

/* 面板 */
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
.panel-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  background: #fff;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 32px 0;
}
.card-title {
  font-weight: 600;
  color: var(--color-foreground);
}

/* 性能 */
.metric-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;
  margin-bottom: 12px;
}
.metric-grid-4 {
  grid-template-columns: repeat(4, 1fr);
}
.metric-label {
  font-size: 0.85rem;
  color: var(--color-muted-foreground);
  margin-bottom: 8px;
}
.metric-val {
  font-size: 1.25rem;
  font-weight: 600;
  color: var(--color-foreground);
}
.chart-card {
  margin-bottom: 12px;
}
.perf-chart {
  height: 260px;
}

/* 编辑页 */
.edit-card {
  max-width: 640px;
}
.field-row {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.field-label {
  width: 120px;
  color: var(--color-muted-foreground);
  flex-shrink: 0;
}
.field-tip {
  margin: 12px 0 0;
  color: var(--color-muted-foreground);
  font-size: 0.82rem;
}

/* 磁盘 / 网卡卡片 */
.dev-card {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 12px 16px;
  margin-bottom: 12px;
  background: #fff;
  cursor: pointer;
  transition: all 0.2s ease;
}
.dev-card:hover {
  border-color: var(--color-border-strong);
}
.dev-card.selected {
  border-color: var(--color-primary);
  box-shadow: 0 0 0 1px var(--color-primary);
}
.dev-card-head {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}
.dev-name {
  font-weight: 600;
  font-size: 0.95rem;
  color: var(--color-foreground);
}
.dev-card-head :deep(.el-popconfirm) {
  margin-left: auto;
}
/* 磁盘移除按钮：右对齐（弹窗化后不再有 popconfirm 占位，由按钮自身右推；网卡卡仍走上面的 popconfirm 规则） */
.dev-card-head .dev-remove {
  margin-left: auto;
}

/* 移除磁盘弹窗：单选选项做成两张带边框的说明卡，标题 + 辅助描述分层 */
.rm-disk-tip {
  margin: 0 0 12px;
  line-height: 1.7;
  color: var(--color-foreground);
}
.rm-disk-options {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 8px;
  width: 100%;
}
.rm-opt {
  height: auto;
  align-items: flex-start;
  margin-right: 0;
  padding: 10px 12px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
}
.rm-opt :deep(.el-radio__label) {
  padding-left: 8px;
  white-space: normal;
  line-height: 1.5;
}
.rm-opt-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.rm-opt-title {
  font-weight: 600;
  color: var(--color-foreground);
}
.rm-opt-desc {
  font-size: 0.82rem;
  color: var(--color-muted-foreground);
}
.rm-opt-title.is-danger,
.rm-opt-desc.is-danger {
  color: var(--el-color-danger);
}
.rm-opt-desc.is-hint {
  color: var(--el-color-warning);
}
.rm-opt-icon {
  vertical-align: -2px;
  margin-right: 2px;
}
.panel-actions {
  margin-top: 8px;
}

/* 快照 / XML */
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

@media (max-width: 1200px) {
  .metric-grid-4 {
    grid-template-columns: repeat(2, 1fr);
  }
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
/* IP 行内编辑按钮：紧跟文本、不撑高信息行 */
.dev-xml :deep(.el-textarea__inner) {
  font-family: var(--font-mono);
  font-size: 0.82rem;
  line-height: 1.55;
}
</style>