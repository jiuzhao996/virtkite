<template>
  <div class="wizard" v-loading="loading">
    <div class="wizard-head">
      <el-button text :icon="ArrowLeft" @click="router.push({ name: 'vms' })">返回</el-button>
      <h2 class="wizard-title">创建虚拟机</h2>
      <span class="wizard-sub">三种安装方式：ISO 安装、云镜像 + cloud-init、克隆现有虚拟机</span>
    </div>

    <!-- 前置条件检查：缺网络/安装源/存储池时给出引导链接，不让用户走到中途才发现卡住（基建先行） -->
    <!-- loading 期间 options 为空会误报"缺网络/缺镜像"，必须等数据加载完再判定 -->
    <el-alert
      v-if="!loading && precheckIssues.length"
      type="warning"
      :closable="false"
      class="precheck"
    >
      <template #title>创建环境未就绪：{{ precheckIssues.join('；') }}</template>
      <div class="precheck-links">
        <el-button v-if="!options.networks.length" size="small" text type="primary" @click="$router.push('/networks')">去创建网络 →</el-button>
        <el-button v-if="!hasInstallSource" size="small" text type="primary" @click="$router.push('/images')">去镜像管理登记云镜像 →</el-button>
        <el-button v-if="!usablePools.length" size="small" text type="primary" @click="$router.push('/storage')">去存储池查看 →</el-button>
      </div>
    </el-alert>
    <!-- 已经过的步骤可点击回跳（后续步骤必须走「下一步」过校验，不能跳） -->
    <el-steps :active="step" finish-status="success" align-center class="wizard-steps">
      <el-step title="安装方式" :class="{ 'step-clickable': step > 0 }" @click="goStep(0)" />
      <el-step title="计算资源" :class="{ 'step-clickable': step > 1 }" @click="goStep(1)" />
      <el-step title="磁盘与网络" :class="{ 'step-clickable': step > 2 }" @click="goStep(2)" />
      <el-step title="确认创建" />
    </el-steps>

    <!-- 四步模板拆至 wizard/components/（壳持有全部共享状态与提交装配，步骤组件同名 props 接收） -->
    <el-card shadow="never" class="wizard-body">
      <StepInstallMode
        v-if="step === 0"
        v-model:install-mode="installMode"
        v-model:cloud-init-enabled="cloudInitEnabled"
        :form="form" :iso="iso" :cloud-image="cloudImage" :clone-vm="cloneVm" :vms="vms"
        :options="options" :cloud-init="cloudInit" :disk-bus="diskBus" :nic-model="nicModel"
        :cloud-init-supported="cloudInitSupported" :is-admin="isAdmin" :build-ci-spec="buildCiSpec"
      />
      <StepCompute
        v-else-if="step === 1"
        :form="form" :install-mode="installMode" :usable-pools="usablePools"
        :disk-over-pool="diskOverPool" :pool-label="poolLabel" :pool-avail-text="poolAvailText"
      />
      <StepDisksNetwork
        v-else-if="step === 2"
        :install-mode="installMode" :form="form" :nics="nics" :extra-disks="extraDisks"
        :options="options" :disk-rows="diskRows" :nic-model="nicModel"
      />
      <StepConfirm
        v-else-if="step === 3"
        v-model:submit-error="submitError"
        :form="form" :submitting="submitting" :submit-text="submitText" :create-progress="createProgress"
        :preview-disks="previewDisks" :preview-nics="previewNics" :boot-devices-label="bootDevicesLabel"
        :cloud-init-enabled="cloudInitEnabled" :primary-net="primaryNet" :summary-os="summaryOs"
        :total-capacity="totalCapacity" :domain-spec-json="domainSpecJson"
      />
    </el-card>

    <div class="wizard-footer">
      <el-button v-if="step > 0" :disabled="submitting" @click="step--">上一步</el-button>
      <div class="footer-right">
        <el-button v-if="step < 3" type="primary" :icon="ArrowRight" @click="next">下一步</el-button>
        <el-button v-else type="primary" :icon="Check" :loading="submitting" :disabled="submitting" @click="submit">{{ submitText }}</el-button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { ArrowLeft, ArrowRight, Check } from '@element-plus/icons-vue'
import { api } from '../api'
import { errMsg } from '../utils/format.js'
import { useAuth } from '../store/auth'
import { pollTask, extractTaskId, taskErrorMessage } from '../utils/task.js'
import StepInstallMode from './wizard/components/StepInstallMode.vue'
import StepCompute from './wizard/components/StepCompute.vue'
import StepDisksNetwork from './wizard/components/StepDisksNetwork.vue'
import StepConfirm from './wizard/components/StepConfirm.vue'

const router = useRouter()
const { canOperate, isAdmin } = useAuth()
if (!canOperate.value) router.replace({ name: 'vms' })

const step = ref(0)
const loading = ref(false)
const submitting = ref(false)
// 创建失败的常驻错误（el-alert 展示，可关闭）：toast 会消失，长错误必须留在页面上可读可复制
const submitError = ref('')
// 后台创建任务进度（0-100），驱动按钮文字与汇总页进度条
const createProgress = ref(0)
const submitText = computed(() =>
  !submitting.value ? '创建虚拟机' : createProgress.value > 0 ? `创建中 ${createProgress.value}%` : '创建中...'
)
const installMode = ref('iso')

const options = reactive({
  pools: [],
  storagePools: [],
  networks: [],
  networkInfo: [],
  cloudImages: [],
  osList: []
})
const vms = ref([])

const form = reactive({ name: '', storagePool: '', vcpu: 2, memoryMb: 2048, diskGb: 20, machine: '', sysVolName: '' })
const iso = reactive({ osName: '', isoPath: '', pick: null, manual: false })
const cloudImage = reactive({ imageId: null, osName: '' })
const cloneVm = reactive({ sourceVmId: null })
const extraDisks = reactive([])
const nics = reactive([{ id: 1, source: '' }])

const cloudInitEnabled = ref(false)
const cloudInit = reactive({ hostname: '', user: '', password: '', sshKey: '', netMode: 'dhcp', ip: '', gateway: '', dns: '' })

// ── 向导草稿持久化（sessionStorage）：中途切到别的模块再回来不丢已填内容（2026-09 用户反馈） ──
const DRAFT_KEY = 'vmops_wizard_draft_v1'
function saveDraft() {
  try {
    sessionStorage.setItem(DRAFT_KEY, JSON.stringify({
      step: step.value,
      installMode: installMode.value,
      form: { ...form },
      iso: { ...iso },
      cloudImage: { ...cloudImage },
      cloneVm: { ...cloneVm },
      cloudInitEnabled: cloudInitEnabled.value,
      cloudInit: { ...cloudInit },
      extraDisks: JSON.parse(JSON.stringify(extraDisks)),
      nics: JSON.parse(JSON.stringify(nics))
    }))
  } catch { /* 存储不可用静默 */ }
}
function restoreDraft() {
  try {
    const d = JSON.parse(sessionStorage.getItem(DRAFT_KEY) || 'null')
    if (!d) return false
    step.value = d.step ?? 0
    installMode.value = d.installMode ?? 'iso'
    Object.assign(form, d.form || {})
    Object.assign(iso, d.iso || {})
    Object.assign(cloudImage, d.cloudImage || {})
    Object.assign(cloneVm, d.cloneVm || {})
    cloudInitEnabled.value = !!d.cloudInitEnabled
    Object.assign(cloudInit, d.cloudInit || {})
    if (Array.isArray(d.extraDisks)) extraDisks.splice(0, extraDisks.length, ...d.extraDisks)
    if (Array.isArray(d.nics) && d.nics.length) nics.splice(0, nics.length, ...d.nics)
    return true
  } catch { return false }
}
watch(
  [step, installMode, form, iso, cloudImage, cloneVm, cloudInitEnabled, cloudInit, extraDisks, nics],
  () => saveDraft(),
  { deep: true }
)

// 字节 → 可读 GB（2026-09 修复：函数在历次编辑中丢失，存储池下拉因 ReferenceError 渲染成 No data）
function gbText(bytes) {
  const n = Number(bytes || 0)
  if (!n) return '0 GB'
  const gb = n / 1024 ** 3
  return (gb >= 100 ? gb.toFixed(0) : gb.toFixed(1).replace(/\.0$/, '')) + ' GB'
}
function poolLabel(p) {
  return p.name + '（可用 ' + gbText(p.available) + '）'
}
function poolAvailText(name) {
  const p = (options.storagePools || []).find((x) => x.name === name)
  return p ? gbText(p.available) : '—'
}
// 新系统盘超出池剩余空间时预警（thin provisioning 下未必失败，但必须让用户看见）
const diskOverPool = computed(() => {
  const p = (options.storagePools || []).find((x) => x.name === form.storagePool)
  if (!p || !p.available) return false
  return Number(form.diskGb || 0) * 1024 ** 3 > Number(p.available)
})

// ── 前置条件检查：基建先行，缺什么给引导链接而不是让用户走到中途发现下拉是空的 ──
const usablePools = computed(() => (options.storagePools || []).filter((p) => p.active))
const hasInstallSource = computed(() => {
  if ((options.cloudImages || []).length) return true
  if ((options.storagePools || []).some((p) => (p.volumes || []).length)) return true
  return vms.value.length > 0
})
const precheckIssues = computed(() => {
  const issues = []
  if (!options.networks.length) issues.push('还没有可用的虚拟网络')
  if (!usablePools.value.length) issues.push('没有可用（激活）的存储池')
  if (!hasInstallSource.value) issues.push('没有任何安装来源（云镜像 / ISO 卷 / 存量虚拟机）')
  return issues
})

// ── 设备型号联动：按当前安装方式取「已选系统名」，再查 osList 得到系统对象 ──
const activeOsName = computed(() => {
  if (installMode.value === 'iso') return iso.osName
  if (installMode.value === 'cloudimage') return cloudImage.osName
  return ''
})
const selectedOs = computed(() => options.osList.find((o) => o.name === activeOsName.value) || null)
const nicModel = computed(() => (selectedOs.value && selectedOs.value.nic_model) || 'virtio')
const diskBus = computed(() => (selectedOs.value && selectedOs.value.disk_bus) || 'virtio')
const cloudInitSupported = computed(() => installMode.value === 'cloudimage' && !!(selectedOs.value && selectedOs.value.cloud_init))

const cloneSource = computed(() => vms.value.find((v) => v.id === cloneVm.sourceVmId) || null)

const systemDisk = computed(() => {
  if (installMode.value === 'iso') return [{ id: 'sys', kind: 'create', createGb: form.diskGb, volName: form.sysVolName.trim(), isSystem: true }]
  if (installMode.value === 'cloudimage') {
    const img = options.cloudImages.find((i) => i.id === cloudImage.imageId)
    // createGb：用户声明的增量盘读写上限（第 2 步输入），与镜像文件大小 sizeGb 分开携带
    return [{ id: 'sys', kind: 'image', imageId: cloudImage.imageId, imageName: img ? img.name : '', sizeGb: img ? img.size_gb : 0, createGb: form.diskGb, isSystem: true }]
  }
  return []
})

const diskRows = computed(() => [...systemDisk.value, ...extraDisks])

const isoPathLabel = computed(() => (installMode.value === 'iso' ? iso.isoPath : ''))

function findImage(id) {
  return options.cloudImages.find((i) => i.id === id) || null
}

const previewDisks = computed(() => {
  const list = []
  let vIdx = 0
  let hIdx = 0
  const vd = () => 'vd' + String.fromCharCode(97 + vIdx++)
  const hd = () => 'hd' + String.fromCharCode(97 + hIdx++)
  for (const d of diskRows.value) {
    list.push({
      target: vd(),
      type: 'file',
      device: 'disk',
      driver: d.kind === 'image' ? (findImage(d.imageId)?.format ?? 'qcow2') : d.kind === 'source' ? 'auto' : 'qcow2',
      bus: diskBus.value,
      source: d.kind === 'create'
        ? '新建空白卷（' + d.createGb + ' GB，池：' + (form.storagePool || 'vmops') + '）'
        : d.kind === 'source' ? (d.source || '—') : (d.imageName || '云镜像 #' + d.imageId),
      read_only: false
    })
  }
  if (isoPathLabel.value) {
    list.push({ target: hd(), type: 'file', device: 'cdrom', driver: 'raw', bus: 'ide', source: isoPathLabel.value, read_only: true })
  }
  if (cloudInitEnabled.value) {
    list.push({ target: hd(), type: 'file', device: 'cdrom', driver: 'raw', bus: 'ide', source: form.name + '-seed.iso（cloud-init）', read_only: true })
  }
  return list
})

const previewNics = computed(() =>
  nics.map((n) => ({ type: 'network', source: n.source || 'default', model: nicModel.value, mac: '后端自动分配' }))
)

const bootDevices = computed(() => {
  const hasCd = !!isoPathLabel.value || cloudInitEnabled.value
  return hasCd ? ['cdrom', 'hd'] : ['hd']
})
const bootDevicesLabel = computed(() => bootDevices.value.join(' → '))

const primaryNet = computed(() => (nics.length ? nics[0].source || 'default' : 'default'))
const summaryOs = computed(() => {
  if (installMode.value === 'clone') return cloneSource.value ? '克隆：' + cloneSource.value.name : '克隆源未选'
  return activeOsName.value || '未指定'
})

const totalCapacity = computed(() => {
  if (installMode.value === 'clone') return cloneSource.value ? cloneSource.value.disk_gb || 0 : 0
  let gb = 0
  for (const d of diskRows.value) {
    if (d.kind === 'create') gb += d.createGb
    // 云镜像增量盘用输入的容量（读写上限），不用基镜像文件大小；弹窗加的 image 盘无输入，回退文件大小
    else if (d.kind === 'image') gb += d.createGb || d.sizeGb || 0
  }
  return gb
})

const domainSpecJson = computed(() => {
  if (installMode.value === 'clone') {
    return JSON.stringify(
      {
        name: form.name,
        vcpu: form.vcpu,
        memory_mb: form.memoryMb,
        note: '克隆方式：基于源 VM 磁盘生成链接克隆（linked clone），磁盘由后端继承',
        boot: { devices: ['hd'] },
        disks: [{ source: cloneSource.value ? cloneSource.value.name + ' 系统盘（linked clone）' : '源虚拟机系统盘' }],
        interfaces: previewNics.value,
        network: primaryNet.value,
        graphics: { type: 'vnc', port: -1 },
        autostart: false
      },
      null,
      2
    )
  }
  const spec = {
    name: form.name,
    vcpu: form.vcpu,
    memory_mb: form.memoryMb,
    os_type: 'hvm',
    arch: 'x86_64',
    boot: { devices: bootDevices.value },
    disks: previewDisks.value,
    interfaces: previewNics.value,
    graphics: { type: 'vnc', port: -1 },
    autostart: false
  }
  if (cloudInitEnabled.value) spec.cloud_init = buildCloudInit()
  return JSON.stringify(spec, null, 2)
})

// 默认存储池：优先选后端可写的池（路径不在 /var/lib 系统目录下，web 进程可写 seed ISO）
function pickDefaultPool(d) {
  const names = d.pools || []
  const infos = d.storage_pools || []
  const byName = {}
  for (const p of infos) byName[p.name] = p
  const writable = names.filter((n) => {
    const p = byName[n]
    return p && p.path && !p.path.startsWith('/var/lib')
  })
  return writable[0] || names[0] || 'vmops'
}

// 当前 cloud-init 表单 → spec 对象（只带非空字段；模板保存与建机 payload 共用的裁剪逻辑）
function buildCiSpec() {
  const cfg = { net_mode: cloudInit.netMode }
  if (cloudInit.hostname.trim()) cfg.hostname = cloudInit.hostname.trim()
  if (cloudInit.user.trim()) cfg.user = cloudInit.user.trim()
  if (cloudInit.password) cfg.password = cloudInit.password
  if (cloudInit.sshKey.trim()) cfg.ssh_key = cloudInit.sshKey.trim()
  if (cloudInit.netMode === 'static') {
    if (cloudInit.ip.trim()) cfg.ip = cloudInit.ip.trim()
    if (cloudInit.gateway.trim()) cfg.gateway = cloudInit.gateway.trim()
    const dnsList = cloudInit.dns.split(/[,，\s]+/).filter(Boolean)
    if (dnsList.length) cfg.dns = dnsList
  }
  return cfg
}

function buildCloudInit() {
  const cfg = buildCiSpec()
  // 主机名缺省回落为虚拟机名（模板保存走 buildCiSpec，不带这一层 VM 专属默认值）
  if (!cfg.hostname) cfg.hostname = form.name
  return cfg
}

function buildDisks() {
  const arr = []
  for (const d of systemDisk.value) {
    if (d.kind === 'create') arr.push({ create_gb: d.createGb, ...(d.volName ? { vol_name: d.volName } : {}) })
    else if (d.kind === 'source') arr.push({ source: d.source })
    else if (d.kind === 'image') arr.push({ source_image_id: d.imageId, ...(d.createGb ? { create_gb: d.createGb } : {}) })
  }
  for (const d of extraDisks) {
    if (d.kind === 'create') arr.push({ create_gb: d.createGb, ...(d.volName ? { vol_name: d.volName } : {}) })
    else if (d.kind === 'source') arr.push({ source: d.source })
    else if (d.kind === 'image') arr.push({ source_image_id: d.imageId, ...(d.createGb ? { create_gb: d.createGb } : {}) })
  }
  return arr
}

function buildPayload() {
  const payload = {
    name: form.name,
    storage_pool: form.storagePool,
    vcpu: form.vcpu,
    memory_mb: form.memoryMb
  }
  // 机器类型：q35/pc 透传，空 = libvirt 自动；CPU 直通由后端缺省启用
  if (form.machine) payload.machine = form.machine
  if (installMode.value === 'iso') {
    payload.disks = buildDisks()
    payload.iso_path = isoPathLabel.value
  } else if (installMode.value === 'cloudimage') {
    payload.disks = buildDisks()
  }
  payload.interfaces = nics.map((n) => ({ type: 'network', source: n.source || 'default', model: nicModel.value }))
  payload.network = primaryNet.value
  if (cloudInitEnabled.value) payload.cloud_init = buildCloudInit()
  return payload
}

// 已完成步骤点击回跳：只允许往回走，往前的步骤必须经「下一步」过校验
function goStep(i) {
  if (loading.value || submitting.value) return
  if (i >= 0 && i < step.value) step.value = i
}

// 虚拟机名称校验（新建与克隆共用）：非空 / 字符白名单 / 与存量 VM 重名
function validateVmName() {
  if (!(form.name || '').trim()) return '请填写虚拟机名称'
  if (!/^[\w-]+$/.test(form.name)) return '虚拟机名称仅允许字母、数字、_、-'
  const dup = vms.value.find((v) => v.name === form.name.trim())
  if (dup) return '已存在同名虚拟机「' + dup.name + '」，请更换名称'
  return ''
}

function next() {
  if (step.value === 0) {
    if (installMode.value === 'iso') {
      if (!iso.osName) return ElMessage.warning({ message: '请选择操作系统', grouping: true })
      if (!iso.isoPath) return ElMessage.warning('请选择安装介质（存储池 → ISO，或勾选手动输入路径）')
    } else if (installMode.value === 'cloudimage') {
      if (!cloudImage.imageId) return ElMessage.warning('请选择云镜像')
    } else if (installMode.value === 'clone') {
      if (!cloneVm.sourceVmId) return ElMessage.warning('请选择源虚拟机')
      const nameErr = validateVmName()
      if (nameErr) return ElMessage.warning(nameErr)
    }
    // cloud-init 静态 IP：IP 与网关都必填（缺网关的静态网络是不完整配置）
    if (installMode.value === 'cloudimage' && cloudInitEnabled.value && cloudInit.netMode === 'static') {
      if (!cloudInit.ip) return ElMessage.warning('静态网络模式请填写 IP 地址')
      if (!cloudInit.gateway) return ElMessage.warning('静态网络模式请填写网关')
    }
  }
  if (step.value === 1 && installMode.value !== 'clone') {
    const nameErr = validateVmName()
    if (nameErr) return ElMessage.warning(nameErr)
    // 云镜像系统盘容量：非空且 5-500（与输入框 min/max 一致；清空后的 null 也拦在这）
    if (installMode.value === 'cloudimage') {
      const gb = Number(form.diskGb)
      if (!gb || gb < 5 || gb > 500) return ElMessage.warning('请填写系统盘容量（5 ~ 500 GB）')
    }
  }
  step.value++
}

async function submit() {
  submitError.value = '' // 提交开始清空上一次的常驻错误
  submitting.value = true
  createProgress.value = 0
  try {
    const isClone = installMode.value === 'clone'
    const res = isClone
      ? await api.cloneVM(cloneVm.sourceVmId, {
          name: form.name,
          storage_pool: form.storagePool,
          vcpu: form.vcpu,
          memory_mb: form.memoryMb,
          network: primaryNet.value
        })
      : await api.createVM(buildPayload())
    // 后端返回 202 {task_id}，轮询到终态：成功跳转列表，失败留在汇总页展示 error
    await pollTask(extractTaskId(res), {
      onProgress: (t) => {
        createProgress.value = Math.max(0, Math.min(100, Number(t.progress) || 0))
      }
    })
    ElMessage.success(isClone ? '克隆创建成功' : '虚拟机创建成功')
    sessionStorage.removeItem(DRAFT_KEY) // 创建成功清草稿
    router.push({ name: 'vms' })
  } catch (e) {
    const msg = taskErrorMessage(e, '创建失败')
    submitError.value = msg // 常驻 alert（长错误可读可复制）
    ElMessage.error(msg)
  } finally {
    submitting.value = false
  }
}

onMounted(async () => {
  loading.value = true
  try {
    const [opt, vmsRes] = await Promise.all([api.vmOptions(), api.listVMs()])
    const d = opt.data || {}
    options.pools = d.pools || []
    options.storagePools = d.storage_pools || []
    options.networks = d.networks || []
    options.networkInfo = d.network_info || []
    options.cloudImages = d.cloud_images || []
    options.osList = d.os_list || []
    form.storagePool = pickDefaultPool(d)
    nics[0].source = options.networks.includes('default') ? 'default' : options.networks[0] || 'default'
    vms.value = (vmsRes.data && vmsRes.data.items) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '加载创建选项失败'))
  } finally {
    loading.value = false
  }
  // 草稿恢复必须放在选项加载之后：覆盖 pickDefaultPool 的存储池预填
  restoreDraft()
})
</script>

<style scoped>
.wizard {
  display: flex;
  flex-direction: column;
  min-height: 100%;
}

.wizard-head {
  display: flex;
  align-items: center;
  gap: var(--space-xl);
  margin-bottom: var(--space-2xl);
}

.wizard-title {
  margin: 0;
  font-size: 1.2rem;
  font-weight: 700;
}

.wizard-sub {
  color: var(--color-muted-foreground);
  font-size: 0.88rem;
}

.wizard-steps {
  margin-bottom: var(--space-2xl);
}

.wizard-body {
  flex: 1;
}

/* 已完成步骤可点击回跳（类挂在 el-step 根元素上，el-steps 无原生点击） */
.step-clickable {
  cursor: pointer;
}
.step-clickable:hover :deep(.el-step__title) {
  color: var(--el-color-primary);
}

.wizard-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: var(--space-2xl);
}
.precheck {
  margin-bottom: var(--space-lg);
}
.precheck-links {
  margin-top: 6px;
  display: flex;
  gap: 4px;
}
</style>
