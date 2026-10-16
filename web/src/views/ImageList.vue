<template>
  <div v-loading="loading">
    <PageHead title="镜像管理" subtitle="云镜像模板与 ISO 安装镜像分类查看，模板可直接用于创建虚拟机" inline />

    <el-tabs v-model="activeTab" @tab-change="onTabChange">
      <!-- Tab 1 云镜像/模板盘：登记列表（创建 VM「云镜像」方式的数据源） -->
      <el-tab-pane label="云镜像 / 模板盘" name="images">
        <!-- 存储容量条（合并显示，取容量最大的池）→ images/components/PoolCapacityBar.vue -->
        <PoolCapacityBar :pool-cap="poolCap" :pool-caps="poolCaps" />
        <el-card shadow="never">
          <!-- 原左右分组 gap 为 var(--space-lg)（12px），经 gap/right-gap 传入保持不变 -->
          <Toolbar gap="var(--space-lg)" right-gap="var(--space-lg)">
            <template #left>
              <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
              <el-button v-if="isAdmin" type="primary" :icon="Upload" @click="openUpload">上传镜像</el-button>
              <el-input
                v-model="search"
                placeholder="按名称搜索"
                clearable
                :prefix-icon="Search"
                style="width: 180px"
              />
              <el-select v-model="filter" placeholder="筛选" clearable style="width: 150px" @change="load">
                <el-option label="全部镜像" value="" />
                <el-option label="仅模板" value="true" />
              </el-select>
            </template>
            <template #right>
              <el-tag v-if="templateCount" type="primary" effect="plain" size="small">模板 {{ templateCount }}</el-tag>
              <span class="count">共 {{ filteredItems.length }} 个</span>
            </template>
          </Toolbar>

          <el-alert
            type="info"
            :closable="false"
            show-icon
            style="margin-bottom: 12px"
            title="登记的是 qcow2 磁盘模板（增量克隆父盘）——创建 VM 选「云镜像 + cloud-init」时从这里选。"
          />
          <el-table :data="filteredItems" stripe border style="width: 100%">
            <template #empty>
              <el-empty description="暂无镜像" :image-size="80">
                <el-button size="small" @click="$router.push('/storage')">去存储池登记既有卷</el-button>
                <el-button size="small" @click="$router.push('/vms')">查看虚拟机</el-button>
              </el-empty>
            </template>
            <el-table-column prop="name" label="名称" min-width="150" />
            <el-table-column prop="os_version" label="OS 版本" min-width="130" />
            <el-table-column label="大小(GB)" width="110">
              <template #default="{ row }">{{ fmtSizeGB(row.size_gb) }}</template>
            </el-table-column>
            <el-table-column label="格式" width="90">
              <template #default="{ row }">
                <el-tag effect="plain" size="small">{{ row.format || '—' }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column label="来源存储池" width="120">
              <template #default="{ row }">{{ row.pool || '—' }}</template>
            </el-table-column>
            <el-table-column label="模板标记" width="100">
              <template #default="{ row }">
                <el-tag v-if="row.is_template" type="success" effect="light">模板</el-tag>
                <el-tag v-else type="info" effect="plain">普通</el-tag>
              </template>
            </el-table-column>
            <el-table-column label="上传时间" min-width="172">
              <template #default="{ row }">
                <span class="mono">{{ fmtDateTime(row.created_at) }}</span>
              </template>
            </el-table-column>
            <el-table-column label="操作" min-width="270" fixed="right">
              <template #default="{ row }">
                <div class="ops">
                  <el-button v-if="isAdmin && row.is_template" text size="small" type="primary" :icon="Cpu" @click="openClone(row)">基于此创建 VM</el-button>
                  <el-button v-if="isAdmin && row.is_template" text size="small" type="primary" :icon="StarFilled" @click="toggleTemplate(row)">取消模板</el-button>
                  <el-button v-if="isAdmin && !row.is_template" text size="small" type="primary" :icon="Star" @click="toggleTemplate(row)">标记为模板</el-button>
                  <el-button v-if="isAdmin" text size="small" type="danger" :icon="Delete" @click="remove(row)">删除</el-button>
                </div>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>

      <!-- Tab 2 ISO 安装镜像：只读展示激活存储池中的 .iso 卷（创建 VM「本地安装介质」方式的数据源） -->
      <el-tab-pane label="ISO 安装镜像" name="iso">
        <ImageIsoTab :iso-items="isoItems" :iso-loading="isoLoading" @refresh="loadIso" />
      </el-tab-pane>
      <!-- Tab 模板制作：ISO → 制作机 → 固化模板 的引导入口（operator+；模板就是「自己的云镜像」） -->
      <el-tab-pane v-if="canOperate" label="模板制作" name="build" lazy>
        <ImageBuildTab
          :build-form="buildForm"
          :build-busy="buildBusy"
          :build-vms="buildVMs"
          :build-vm-loading="buildVMLoading"
          :iso-items="isoItems"
          :iso-loading="isoLoading"
          :pool-names="poolNames"
          :net-names="netNames"
          @create-builder="createBuilder"
          @finalize-done="loadBuildVMs"
        />
      </el-tab-pane>
      <!-- Tab 3 容器镜像（Docker 资源拆分批次）：operator+ 可见（viewer 不出 tab）；
           lazy 首次激活才挂载（避免未激活时打 docker 接口）；门控重试接 ImageTab.refresh
           （2026-10-04 用户拍板：容器镜像前调、镜像市场殿后） -->
      <el-tab-pane v-if="canOperate" label="容器镜像" name="docker" lazy>
        <DockerGate @retry="dockerTabRef?.refresh?.()">
          <ImageTab ref="dockerTabRef" />
        </DockerGate>
      </el-tab-pane>
      <!-- Tab 4 镜像市场：入口落地页（云镜像 / 安装 ISO 两个二级入口，1Panel 式） -->
      <el-tab-pane label="镜像市场" name="market" lazy>
        <ImageMarketHome embedded />
      </el-tab-pane>
    </el-tabs>

    <!-- 上传镜像 → images/components/ImageUploadDialog.vue（文件经 v-model:file 回传页面） -->
    <ImageUploadDialog
      ref="uploadRef"
      v-model="dialog"
      v-model:file="file"
      :form="form"
      :pool-options="poolOptions"
      :uploading="uploading"
      :upload-pct="uploadPct"
      @upload="upload"
    />

    <!-- 基于模板创建虚拟机 → images/components/ImageCloneDialog.vue -->
    <ImageCloneDialog
      v-model="cloneDialog"
      :clone-img="cloneImg"
      :clone-form="cloneForm"
      :net-options="netOptions"
      :cloning="cloning"
      @create="cloneVm"
    />
  </div>
</template>

<script setup>
// ============================================================================
// 镜像管理（ImageList）
// ----------------------------------------------------------------------------
// 职责：五 tab——「云镜像/模板盘」（登记列表，创建 VM 的数据源）、「ISO 安装镜像」
//       （激活池中的 .iso 卷，只读）、「模板制作」（ISO→制作机→固化模板，operator+）、
//       「容器镜像」（ImageTab，operator+）、「镜像市场」（ImageMarketHome 入口页）。
//       并承载上传镜像、标记/取消模板、删除、基于模板创建 VM。
//
// 数据流：
//   云镜像 ← load() → api.listImages；前端 search 关键字过滤得 filteredItems
//   容量条 ← loadPools() → api.listStoragePools（与上传下拉一次响应双消费：poolOptions + poolCaps）
//   ISO ← loadIso()（逐激活池 getStoragePool 聚合 .iso，allSettled 单池失败不拖垮全局）
//   模板制作 ← loadBuild()（池/网络候选 + 关机态 VM 列表）；createBuilder 走 api.createVM + pollTask
//   深链 ?tab=（白名单内采纳）；tab 变更经 syncTabToUrl 写回 URL
//
// 已拆出的子组件（web/src/views/images/components/）：
//   PoolCapacityBar.vue     存储容量合并条（纯展示）
//   ImageIsoTab.vue         ISO 安装镜像 tab 内容（纯展示 + refresh）
//   ImageBuildTab.vue       模板制作 tab 内容（建机/固化经 emit 回页面；固化弹窗自持状态）
//   ImageUploadDialog.vue   上传镜像弹窗（文件经 v-model:file 回传，upload 由页面执行）
//   ImageCloneDialog.vue    基于模板创建 VM 弹窗（create 由页面执行）
//
// 刻意没拆的：云镜像主表格（与 Toolbar/搜索/筛选/行内动作强耦合，且是本页主内容）。
// ⚠️ .mono 与 .ops 留在本页；下拉/表单用到的 fmtSizeGB 等仍在页面。
// ============================================================================
import { ref, reactive, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { syncTabToUrl } from '../utils/tab-url'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Upload, Delete, Star, StarFilled, Cpu, Search } from '@element-plus/icons-vue'
import ImageMarketHome from './images/components/ImageMarketHome.vue'
import ImageMarket from './images/components/ImageMarket.vue'
import ImageIsoMarket from './images/components/ImageIsoMarket.vue'
import PoolCapacityBar from './images/components/PoolCapacityBar.vue'
import ImageIsoTab from './images/components/ImageIsoTab.vue'
import ImageBuildTab from './images/components/ImageBuildTab.vue'
import ImageUploadDialog from './images/components/ImageUploadDialog.vue'
import ImageCloneDialog from './images/components/ImageCloneDialog.vue'
import DockerGate from '../components/DockerGate.vue'
import ImageTab from './docker/components/ImageTab.vue'
const dockerTabRef = ref(null)
import PageHead from '../components/PageHead.vue'
import Toolbar from '../components/Toolbar.vue'
import { api } from '../api'
import { useAuth } from '../store/auth'
import { pollTask, extractTaskId, taskErrorMessage } from '../utils/task'
import { fmtSizeGB, fmtDateTime, errMsg, isCancel, clampPct } from '../utils/format'

const { isAdmin, canOperate } = useAuth()
const route = useRoute()
const router = useRouter()

const activeTab = ref('images')
// 镜像市场 tab 懒加载：组件 onMounted 自拉数据，无需额外处理
const items = ref([])
const total = ref(0)
const loading = ref(false)
const filter = ref('')
// 云镜像 tab 名称关键字搜索：纯前端过滤（后端列表无该参数），不区分大小写
const search = ref('')
const netOptions = ref([])
const dialog = ref(false)
const uploading = ref(false)
const uploadPct = ref(0)
const file = ref(null)
const uploadRef = ref(null)
const poolOptions = ref(['base'])
const cloneDialog = ref(false)
const cloneImg = ref({})
const cloning = ref(false)
const isoItems = ref([])
const isoLoading = ref(false)
const isoLoaded = ref(false)

const form = reactive({ name: '', os_version: '', is_template: false, pool: 'base' })
const cloneForm = reactive({ name: '', vcpu: 1, memory_mb: 1024, network: 'default' })

const templateCount = computed(() => items.value.filter((i) => i.is_template).length)

// 表格展示 = 关键字过滤后的列表；计数 tag「模板 N」仍按全量统计（库内模板总数，不随搜索变）
const filteredItems = computed(() => {
  const kw = search.value.trim().toLowerCase()
  if (!kw) return items.value
  return items.value.filter((i) => (i.name || '').toLowerCase().includes(kw))
})

// onFileChange / onFileRemove（上传文件选择）已随上传弹窗下沉到 ImageUploadDialog.vue

async function load() {
  loading.value = true
  try {
    const params = {}
    if (filter.value) params.is_template = filter.value
    const res = await api.listImages(params)
    items.value = (res.data && res.data.items) || []
    total.value = (res.data && res.data.total) || 0
  } catch (e) {
    ElMessage.error(errMsg(e, '获取镜像列表失败'))
  } finally {
    loading.value = false
  }
}

// ISO 安装镜像列表：listStoragePools 响应只有 vol_count 不含卷数组，
// 对每个激活池逐个调 getStoragePool（与存储池页「浏览卷」同款接口）聚合 .iso 卷；
// 单池查询失败不拖垮整个列表（allSettled 跳过该池，其余池照常展示）
async function loadIso() {
  isoLoading.value = true
  try {
    const res = await api.listStoragePools()
    const actives = ((res.data && res.data.items) || []).filter((p) => p.active)
    const results = await Promise.allSettled(
      actives.map(async (p) => {
        const r = await api.getStoragePool(p.name)
        const vols = (r.data && r.data.volumes) || []
        return vols
          .filter((v) => (v.name || '').toLowerCase().endsWith('.iso'))
          .map((v) => ({ ...v, pool: p.name }))
      })
    )
    const list = []
    for (const r of results) {
      if (r.status === 'fulfilled') list.push(...r.value)
    }
    list.sort((a, b) => (a.pool + a.name).localeCompare(b.pool + b.name))
    isoItems.value = list
  } catch (e) {
    ElMessage.error(errMsg(e, '获取 ISO 镜像列表失败'))
  } finally {
    isoLoading.value = false
  }
}

// ISO 卷扫描要额外查 N 个池，首次切到该 tab 才拉数据
function onTabChange(name) {
  if (name === 'iso' && !isoLoaded.value) {
    isoLoaded.value = true
    loadIso()
  }
  if (name === 'build' && !buildLoaded.value) {
    buildLoaded.value = true
    loadBuild()
  }
  // URL 跟着 tab 走，深链可分享（云镜像是默认 tab，不写 query）
  syncTabToUrl(router, route, name, 'images')
}

/* ── 模板制作 tab：ISO → 制作机 → 固化模板 ── */
const buildLoaded = ref(false)
const buildForm = reactive({ name: '', isoPath: '', diskGb: 20, pool: 'base', network: 'default' })
const buildBusy = ref(false)
const buildVMs = ref([])
const buildVMLoading = ref(false)
const poolNames = ref(['base', 'images', 'exten'])
const netNames = ref(['default'])
// 固化弹窗的开关 / 目标 VM / 组件 ref 已随「模板制作」tab 下沉到 ImageBuildTab.vue

async function loadBuild() {
  if (!isoLoaded.value) {
    isoLoaded.value = true
    loadIso()
  }
  try {
    const r = await api.listStoragePools()
    const names = ((r.data && r.data.items) || []).map((p) => p.name)
    if (names.length) poolNames.value = names
  } catch (e) {
    /* 保持内置候选 */
  }
  try {
    const r = await api.listNetworks()
    const list = (r.data && (r.data.items || r.data)) || []
    netNames.value = [...new Set(['default', ...list.map((n) => n.name || n).filter(Boolean)])]
  } catch (e) {
    /* 保持 default */
  }
  await loadBuildVMs()
}

async function loadBuildVMs() {
  buildVMLoading.value = true
  try {
    const r = await api.listVMs()
    buildVMs.value = ((r.data && r.data.items) || []).filter((v) => v.status === 'shut off')
  } catch (e) {
    ElMessage.error(errMsg(e, '获取虚拟机列表失败'))
  } finally {
    buildVMLoading.value = false
  }
}

async function createBuilder() {
  if (!buildForm.name.trim()) return ElMessage.warning('请填写制作机名称')
  if (!buildForm.isoPath) return ElMessage.warning('请选择 ISO（可先在「ISO 安装镜像」tab 下载）')
  buildBusy.value = true
  try {
    const payload = {
      name: buildForm.name.trim(),
      storage_pool: buildForm.pool,
      vcpu: 2,
      memory_mb: 2048,
      disks: [{ create_gb: buildForm.diskGb }],
      iso_path: buildForm.isoPath,
      network: buildForm.network,
      interfaces: [{ type: 'network', source: buildForm.network, model: 'virtio' }]
    }
    await pollTask(extractTaskId(await api.createVM(payload)))
    ElMessage.success('制作机已创建（关机态）；请去「虚拟机 → 详情 → 控制台」装系统，装完关机再回来固化')
    await loadBuildVMs()
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '创建制作机失败'))
  } finally {
    buildBusy.value = false
  }
}

/* ── 存储池容量区（质感专项）── */
// 与上传下拉共用 listStoragePools 一次响应（loadPools 内双消费）；仅收激活池
// （capacity/allocation 为 libvirt 实时口径，未激活池拿不到值）；拉取失败 → 空数组 → 区块隐藏
const poolCaps = ref([])
// 同盘多池容量相同 → 取容量最大的那条作为「总容量」展示（只画一条，不再逐池重复）
const poolCap = computed(() => {
  if (!poolCaps.value.length) return null
  return poolCaps.value.reduce((a, b) => (b.total > a.total ? b : a))
})
// capColor（容量条配色）已随容量条下沉到 PoolCapacityBar.vue

// 存储池下拉：默认 img，可手输其他池名；同一响应顺带产出容量条数据（一次拉取，双消费）
async function loadPools() {
  try {
    const res = await api.listStoragePools()
    const list = (res.data && res.data.items) || []
    const names = list.map((p) => p.name)
    poolOptions.value = [...new Set(['base', ...names])]
    // 已用口径与存储池页一致：allocation/capacity（StorageList.vue poolPct 同式），钳制 0~100；
    // 按已用比例降序，「快满的池」排前面更符合阅读顺序
    poolCaps.value = list
      .filter((p) => p.active && p.capacity)
      .map((p) => ({
        name: p.name,
        used: p.allocation || 0,
        total: p.capacity,
        pct: clampPct(((p.allocation || 0) / p.capacity) * 100)
      }))
      .sort((a, b) => b.pct - a.pct)
  } catch (e) {
    poolOptions.value = ['base']
    poolCaps.value = [] // 容量区静默隐藏，不打扰镜像主功能
  }
}

function openUpload() {
  Object.assign(form, { name: '', os_version: '', is_template: false, pool: 'base' })
  file.value = null
  uploadPct.value = 0
  // 清空上传组件遗留的文件列表，避免上次上传的文件残留（limit=1 下无法再选新文件）
  if (uploadRef.value) uploadRef.value.clearFiles()
  dialog.value = true
}

async function upload() {
  if (!form.name) {
    ElMessage.warning('请填写镜像名称')
    return
  }
  if (!file.value) {
    ElMessage.warning('请选择镜像文件')
    return
  }
  const fd = new FormData()
  fd.append('file', file.value)
  fd.append('name', form.name)
  fd.append('os_version', form.os_version)
  fd.append('pool', form.pool || 'base')
  fd.append('is_template', form.is_template ? 'true' : 'false')
  uploading.value = true
  uploadPct.value = 0
  try {
    // 上传接口已单独设为不限超时（见 api/index.js），进度由 onUploadProgress 回传
    await api.uploadImage(fd, (percent) => {
      uploadPct.value = percent
    })
    ElMessage.success('上传成功')
    dialog.value = false
    file.value = null
    await load()
    loadPools() // 大文件落池会改变已用比例，容量条随上传结果同步刷新（内部自带静默降级）
  } catch (e) {
    ElMessage.error(errMsg(e, '上传失败'))
  } finally {
    uploading.value = false
  }
}

// 标记/取消模板
async function toggleTemplate(img) {
  const next = !img.is_template
  const label = next ? '标记为模板' : '取消模板'
  try {
    await ElMessageBox.confirm(
      `确定将镜像「${img.name}」${label}？${next ? '模板镜像可直接用于创建虚拟机。' : '取消后仍作为普通镜像保留。'}`,
      '确认操作',
      {
        type: next ? 'warning' : 'info',
        confirmButtonText: '确定',
        cancelButtonText: '取消'
      }
    )
    await api.setImageTemplate(img.id, next)
    ElMessage.success(`${label}成功`)
    await load()
  } catch (e) {
    if (!isCancel(e)) {
      ElMessage.error(errMsg(e, '操作失败'))
    }
  }
}

// 网络下拉（基于模板创建 VM）：listNetworks 拉取，选项显示名称+网关；拉取失败静默走空列表（可手清空回退 default）
async function loadNetworks() {
  try {
    const res = await api.listNetworks()
    netOptions.value = ((res.data && res.data.items) || []).map((n) => ({ name: n.name, gateway: n.gateway || '' }))
  } catch (e) {
    netOptions.value = []
  }
}

// 基于模板创建虚拟机
function openClone(img) {
  cloneImg.value = img
  Object.assign(cloneForm, {
    name: img.name.replace(/[^a-zA-Z0-9_-]/g, '-') + '-clone',
    vcpu: 1,
    memory_mb: 1024,
    network: 'default'
  })
  cloneDialog.value = true
  loadNetworks()
}

async function cloneVm() {
  if (!cloneForm.name) {
    ElMessage.warning('请填写虚拟机名称')
    return
  }
  cloning.value = true
  try {
    const res = await api.cloneImage(cloneImg.value.id, {
      name: cloneForm.name,
      vcpu: cloneForm.vcpu,
      memory_mb: cloneForm.memory_mb,
      network: cloneForm.network || 'default' // clearable 清空后为空串，回退默认网络
    })
    ElMessage.info('克隆任务已提交，正在后台执行…')
    await pollTask(extractTaskId(res))
    ElMessage.success('虚拟机已创建，可在虚拟机列表查看')
    cloneDialog.value = false
  } catch (e) {
    ElMessage.error(taskErrorMessage(e, '创建虚拟机失败'))
  } finally {
    cloning.value = false
  }
}

async function remove(img) {
  try {
    await ElMessageBox.confirm('确定删除镜像「' + img.name + '」？磁盘文件将一并清理。', '确认删除', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      confirmButtonClass: 'el-button--danger'
    })
    await api.deleteImage(img.id)
    ElMessage.success('已删除')
    await load()
    loadPools() // 删镜像释放池空间，容量条同步刷新（内部自带静默降级）
  } catch (e) {
    if (!isCancel(e)) {
      ElMessage.error(errMsg(e, '删除失败'))
    }
  }
}

// 深链 ?tab=xxx（任务中心「查看镜像库/查看容器镜像」等出口用）：白名单内才采纳，
// 非法值回落默认 tab；build/docker 两个 tab 是 v-if=canOperate，viewer 深链不生效。
// 监听而非只读一次：同路由内只改 query 不会重挂组件，漏掉监听就会「点了没反应」。
function applyQueryTab() {
  const t = route.query.tab
  const allowed = canOperate.value ? ['images', 'iso', 'build', 'docker', 'market'] : ['images', 'iso', 'market']
  if (typeof t === 'string' && allowed.includes(t)) activeTab.value = t
}
watch(() => route.query.tab, applyQueryTab)

onMounted(() => {
  applyQueryTab()
  load()
  loadPools()
})
</script>

<style scoped>
/* .page-head / .page-title / .page-desc / .toolbar / .count 已收进 global.css；.mono 的 font-family 亦然；
   .toolbar-left/.toolbar-right 骨架与 gap 由 Toolbar 组件承担 */
.ops {
  display: flex;
  align-items: center;
  gap: var(--space-md);
  flex-wrap: wrap;
}
.mono {
  font-size: 0.85rem;
}
/* .clone-tip / .pool-caps 前缀 / .os-hint / .build-h / .build-tip 已随各区块下沉到
   images/components/ 下的 PoolCapacityBar / ImageIsoTab / ImageBuildTab（各留一份，改一处须同步） */
</style>
