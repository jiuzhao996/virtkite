<template>
  <div v-loading="loading">
    <!-- 动作组原本就是 .page-head 直接子元素，经默认插槽保持同构（.page-actions 的 scoped 样式仍生效） -->
    <PageHead title="存储管理">
      <div class="page-actions">
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        <el-button v-if="isAdmin" type="primary" :icon="Plus" @click="openCreatePool">新建存储池</el-button>
      </div>
    </PageHead>

    <!-- Docker 资源拆分批次：拓扑图 + 平台存储（qcow2 池）+ Docker 卷，统一存储入口 -->
    <el-tabs v-model="activeTab" class="storage-tabs">
      <el-tab-pane label="拓扑图" name="topo">
        <StorageTopology @open-pool="activeTab = 'pools'" />
      </el-tab-pane>
      <el-tab-pane label="存储池" name="pools" lazy>

    <!-- 汇总条（纯展示，数据由页面算好后传入 → storage/components/PoolSummary.vue） -->
    <PoolSummary :phys-cap="physCap" :phys-pct="physPct" :seed-dir="seedDir" :default-pool="defaultPool" />

    <!-- 池卡片网格（纯展示，见 storage/components/PoolCardGrid.vue；点击/编辑/删除经 emit 回页面处理） -->
    <PoolCardGrid
      :pools="pools"
      :loading="loading"
      :is-admin="isAdmin"
      @open="openVolumes"
      @edit="openMeta"
      @remove="removePool"
    />
      </el-tab-pane>
      <!-- Docker 卷（Docker 资源拆分批次）：operator+ 可见，lazy 首次激活才挂载 -->
      <el-tab-pane v-if="canOperate" label="Docker 卷" name="docker" lazy>
        <DockerGate @retry="dockerVolRef?.refresh?.()">
          <VolumeTab ref="dockerVolRef" />
        </DockerGate>
      </el-tab-pane>
    </el-tabs>

    <!-- 池信息编辑（角色 + 描述）→ storage/components/PoolMetaDialog.vue -->
    <PoolMetaDialog
      v-model="metaDialog"
      :form="metaForm"
      :roles="poolRoles"
      :saving="metaSaving"
      @save="saveMeta"
    />

    <!-- 卷抽屉（池名 · 角色）→ storage/components/VolumeDrawer.vue（血缘图 + 卷表格） -->
    <VolumeDrawer
      v-model="volDrawer"
      :cur-pool="curPool"
      :cur-role="curRole"
      :pool-graph-loading="poolGraphLoading"
      :pool-graph-nodes="poolGraphNodes"
      :pool-graph-edges="poolGraphEdges"
      :pool-in-count="poolInCount"
      :pool-boundary-count="poolBoundaryCount"
      :pool-virtual-text="poolVirtualText"
      :pool-actual-text="poolActualText"
      :volumes="volumes"
      :vol-refs="volRefs"
      :is-admin="isAdmin"
      @refresh-graph="loadPoolGraph"
      @create-vol="openCreateVol"
      @cleanup-orphans="cleanupOrphans"
      @register="openRegister"
      @remove="removeVolume"
    />

    <!-- 登记为云镜像（把池内已有卷纳入镜像库，不复制不移动）→ storage/components/RegisterImageDialog.vue -->
    <RegisterImageDialog
      v-model="regDialog"
      :form="regForm"
      :vol-name="regVol"
      :saving="regSaving"
      @register="doRegister"
    />

    <!-- 新建存储池 → storage/components/PoolCreateDialog.vue（暴露 validate() 供 createPool 行内校验） -->
    <PoolCreateDialog
      ref="poolFormRef"
      v-model="poolDialog"
      :form="poolForm"
      :rules="poolRules"
      :saving="saving"
      @create="createPool"
    />

    <!-- 新建卷 → storage/components/VolumeCreateDialog.vue -->
    <VolumeCreateDialog
      v-model="volCreateDialog"
      :form="volForm"
      :saving="saving"
      @create="createVolume"
    />
  </div>
</template>

<script setup>
// ============================================================================
// 存储管理（StorageList）
// ----------------------------------------------------------------------------
// 职责：三 tab 统一存储入口——「拓扑图」（StorageTopology）/「存储池」（池卡片 +
//       卷管理抽屉，含血缘图谱）/「Docker 卷」（VolumeTab，operator+）。
//       同时承载池的增删改（新建/编辑角色描述/删除）与卷的增删、登记为云镜像、
//       孤儿卷清理（转后台任务）。
//
// 数据流：
//   load() ← api.listStoragePools()：pools / seedDir / defaultPool / poolRoles
//   打开某池 openVolumes(pool) → fetchVolumes（卷 + volRefs 引用映射）+ loadPoolGraph
//     （全库血缘图 → poolGraphNodes/Edges 按当前池过滤出本池 + 跨池边界节点）
//   池信息/建池/建卷/登记镜像/删卷/清理孤儿 → 各 api.*，成功后统一 load()/refreshVolumes()
//
// 已拆出的子组件（web/src/views/storage/components/）：
//   PoolSummary.vue          顶部汇总条（物理容量 / 种子目录 / 默认池）
//   PoolCardGrid.vue         池卡片网格 + 空态引导（open/edit/remove 经 emit 回页面）
//   VolumeDrawer.vue         卷管理抽屉（血缘图 + 卷表格；refresh-graph/create-vol/
//                            cleanup-orphans/register/remove 经 emit 回页面）
//   PoolMetaDialog.vue       编辑池信息（角色 + 描述）
//   PoolCreateDialog.vue     新建存储池（暴露 validate() 供 createPool 行内校验）
//   VolumeCreateDialog.vue   新建存储卷
//   RegisterImageDialog.vue  登记为云镜像
//
// 刻意没拆的：池/卷/血缘图的数据请求与状态（pools/volumes/volRefs/poolGraphRaw 及各
//   计算属性）留在页面——它们是子组件的数据源，且互相耦合（如删卷后要同时刷新
//   卷表、引用、血缘图与容量汇总），下沉反而要来回抛事件。
// ⚠️ refsInUse / refsTooltip 在页面与 VolumeDrawer 各留一份（页面用于孤儿卷筛选与
//   删卷确认文案，抽屉用于表格「在用」列）——改一处须同步另一处。
// ============================================================================
import { ref, computed, h, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Plus } from '@element-plus/icons-vue'
import { api } from '../api'
import { useAuth } from '../store/auth'
import { pollTask, extractTaskId } from '../utils/task.js'
import PageHead from '../components/PageHead.vue'
import StorageTopology from './storage/components/StorageTopology.vue'
import PoolSummary from './storage/components/PoolSummary.vue'
import PoolCardGrid from './storage/components/PoolCardGrid.vue'
import VolumeDrawer from './storage/components/VolumeDrawer.vue'
import PoolMetaDialog from './storage/components/PoolMetaDialog.vue'
import PoolCreateDialog from './storage/components/PoolCreateDialog.vue'
import VolumeCreateDialog from './storage/components/VolumeCreateDialog.vue'
import RegisterImageDialog from './storage/components/RegisterImageDialog.vue'
import DockerGate from '../components/DockerGate.vue'
import VolumeTab from './docker/components/VolumeTab.vue'
// 容量格式化 / 错误文案 / 取消判定统一走 utils/format.js（原本地三份实现已删）
// 本页的 .page-head / .page-title / .toolbar / .count 与其他列表页逐字相同，已收进 global.css
// （fmtSizeBytes / usageColor 的使用点已随汇总条与卷抽屉下沉到子组件）
import { fmtGBSum, errMsg, isCancel, clampPct } from '../utils/format'

const { isAdmin, canOperate } = useAuth()
// 默认落在拓扑图（与网络页同款首屏），池列表/卷管理备查
const activeTab = ref('topo')
const dockerVolRef = ref(null)

const pools = ref([])
const volumes = ref([])
const volRefs = ref({}) // 卷名 → {vms, images, children}
const loading = ref(false)
const saving = ref(false)
const seedDir = ref({ path: '', count: 0 })
const defaultPool = ref('')
// 角色白名单以后端下发为准，这里只做接口不可用时的兜底（与 handler/storage.go poolRoleNames 一致）
const FALLBACK_POOL_ROLES = ['模板基盘', '系统盘', '数据盘', '安装镜像', '系统池', '其他']
const poolRoles = ref([...FALLBACK_POOL_ROLES])

const metaDialog = ref(false)
const metaSaving = ref(false)
const metaForm = ref({ name: '', role: '', description: '' })
// 新建存储池弹窗（openCreatePool / createPool 控制）
const poolDialog = ref(false)
const poolFormRef = ref(null)
// 行内校验规则对齐后端 handler/storage.go：名称 volNameRegex（字母数字_-.），
// 路径 validPoolPath（绝对路径 + 字符白名单 + 非 .. / 结尾斜杠 / 根目录）——文案取后端同款
const poolRules = {
  name: [
    { required: true, message: '请填写名称', trigger: 'blur' },
    { pattern: /^[A-Za-z0-9_.-]+$/, message: '存储池名称只允许字母、数字、下划线、连字符和点', trigger: 'blur' }
  ],
  path: [
    { required: true, message: '请填写路径', trigger: 'blur' },
    {
      validator: (rule, value, callback) => {
        const v = (value || '').trim()
        if (!/^\/[A-Za-z0-9_./-]+$/.test(v)) {
          return callback(new Error('存储池路径必须是规范的绝对路径（仅允许字母、数字、下划线、点、连字符与斜杠）'))
        }
        if (v === '/' || v.includes('..') || v.endsWith('/')) {
          return callback(new Error('路径不能是根目录，且不含 .. 与结尾斜杠'))
        }
        callback()
      },
      trigger: 'blur'
    }
  ]
}

const volDrawer = ref(false)
// 克隆家谱抽屉（数据随池名取，open 事件内自拉）
const volCreateDialog = ref(false)
const regDialog = ref(false)
const regSaving = ref(false)
const curPool = ref('')
const curRole = ref('')
// 本池血缘图（克隆家谱同源数据，池维度过滤视图）
const poolGraphLoading = ref(false)
const poolGraphRaw = ref({ nodes: [], edges: [] })
const regVol = ref('')

const poolForm = ref({ name: '', path: '', description: '' })
const volForm = ref({ name: '', format: 'qcow2', capacity: 20 })
const regForm = ref({ name: '', os_version: '', is_template: false, path: '' })

// 物理容量：dir 池 capacity 是文件系统级的，取最大值那一池的 available（同一块盘多池口径相同）
const physCap = computed(() => {
  let best = { capacity: 0, available: 0 }
  for (const p of pools.value) {
    if ((p.capacity || 0) > best.capacity) {
      best = { capacity: p.capacity || 0, available: p.available || 0 }
    }
  }
  return best
})
const physPct = computed(() => {
  const { capacity, available } = physCap.value
  if (!capacity) return 0
  return clampPct(((capacity - available) / capacity) * 100)
})

// 池角色 → el-tag type 的映射与 roleTagType 已随池卡片下沉到 PoolCardGrid.vue（本页不再用）

function refsInUse(r) {
  return (r.vms && r.vms.length) || (r.images && r.images.length) || (r.children && r.children.length)
}
function refsTooltip(r) {
  const parts = []
  if (r.vms && r.vms.length) parts.push('虚拟机: ' + r.vms.join('、'))
  if (r.images && r.images.length) parts.push('镜像: ' + r.images.join('、'))
  if (r.children && r.children.length) parts.push('增量克隆子卷: ' + r.children.join('、'))
  return parts.join('；')
}

// volTypeTags（卷类型徽标组）与 hasImageRef（是否已登记镜像）已随卷表格下沉到
// VolumeDrawer.vue；本页保留的 refsInUse / refsTooltip 仅服务孤儿卷筛选与删卷确认文案。

async function load() {
  loading.value = true
  try {
    const res = await api.listStoragePools()
    const d = (res && res.data) || {}
    pools.value = d.items || []
    seedDir.value = d.seed_dir || { path: '', count: 0 }
    defaultPool.value = d.default_pool || ''
    if (d.pool_roles && d.pool_roles.length) poolRoles.value = d.pool_roles
  } catch (e) {
    ElMessage.error(errMsg(e, '获取存储池失败'))
  } finally {
    loading.value = false
  }
}

// ===== 池信息编辑 =====
function openMeta(pool) {
  if (!isAdmin.value) return
  metaForm.value = {
    name: pool.name,
    role: pool.role || '',
    description: pool.description || ''
  }
  metaDialog.value = true
}

async function saveMeta() {
  metaSaving.value = true
  try {
    await api.updatePoolMeta(metaForm.value.name, {
      role: metaForm.value.role,
      description: metaForm.value.description
    })
    ElMessage.success('池信息已保存')
    metaDialog.value = false
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '保存失败'))
  } finally {
    metaSaving.value = false
  }
}

// ===== 新建池（原有校验不动，追加可选描述） =====
function openCreatePool() {
  poolForm.value = { name: '', path: '', description: '' }
  poolDialog.value = true
}

async function createPool() {
  // 行内 rules 校验：出错字段红字提示（取代原先的 toast），通过才提交
  try {
    await poolFormRef.value.validate()
  } catch {
    return
  }
  saving.value = true
  try {
    await api.createStoragePool({ name: poolForm.value.name, path: poolForm.value.path })
    // 池本体创建成功后，若填了描述再补一条元数据；失败不回滚池，只提示
    if (poolForm.value.description) {
      try {
        await api.updatePoolMeta(poolForm.value.name, {
          role: '',
          description: poolForm.value.description
        })
      } catch (e) {
        ElMessage.warning('存储池已创建，但描述保存失败：' + errMsg(e, '未知错误'))
      }
    }
    ElMessage.success('存储池已创建')
    poolDialog.value = false
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '创建失败'))
  } finally {
    saving.value = false
  }
}

async function removePool(row) {
  try {
    await ElMessageBox.confirm('确定删除存储池「' + row.name + '」？', '确认删除', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      confirmButtonClass: 'el-button--danger'
    })
    await api.deleteStoragePool(row.name)
    ElMessage.success('存储池已删除')
    await load()
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '删除失败'))
  }
}

// ===== 卷抽屉 + 引用数据（一次拉全池 refs，"在用"徽标、类型徽标与删卷确认共用） =====
async function openVolumes(pool) {
  curPool.value = pool.name
  curRole.value = pool.role || ''
  volDrawer.value = true
  await Promise.all([fetchVolumes(pool.name), loadPoolGraph()])
}

// 全库图拉取 + 池维度过滤：本池节点 + 跨池克隆链对端（boundary，置灰），链路不断
async function loadPoolGraph() {
  if (!curPool.value) return
  poolGraphLoading.value = true
  try {
    const res = await api.volumeGraph()
    poolGraphRaw.value = res.data || { nodes: [], edges: [] }
  } catch (e) {
    // 图是增强视图，失败不阻断卷表格
    poolGraphRaw.value = { nodes: [], edges: [] }
  } finally {
    poolGraphLoading.value = false
  }
}

const poolGraphNodes = computed(() => {
  const pool = curPool.value
  const all = poolGraphRaw.value.nodes || []
  if (!pool) return all
  const inPool = all.filter((n) => n.pool === pool)
  const inPaths = new Set(inPool.map((n) => n.path))
  // 边界节点：不在本池、但与池内卷有 backing 关系（跨池克隆链对端/池外父盘）
  const boundary = new Set()
  for (const e of poolGraphRaw.value.edges || []) {
    if (inPaths.has(e.parent) && !inPaths.has(e.child)) boundary.add(e.child)
    if (inPaths.has(e.child) && !inPaths.has(e.parent)) boundary.add(e.parent)
  }
  return [
    ...inPool,
    ...all.filter((n) => boundary.has(n.path)).map((n) => ({ ...n, _boundary: true })),
  ]
})

const poolGraphEdges = computed(() => {
  const pool = curPool.value
  const paths = new Set(poolGraphNodes.value.map((n) => n.path))
  return (poolGraphRaw.value.edges || []).filter((e) => paths.has(e.parent) && paths.has(e.child))
})

const poolInCount = computed(() => poolGraphNodes.value.filter((n) => n.pool === curPool.value).length)
const poolBoundaryCount = computed(() => poolGraphNodes.value.filter((n) => n._boundary).length)
const poolVirtualText = computed(() => fmtGBSum(poolGraphNodes.value.filter((n) => n.pool === curPool.value)))
const poolActualText = computed(() => fmtGBSum(poolGraphNodes.value.filter((n) => n.pool === curPool.value), 'allocation_gb'))

// 刷新卷列表 + 引用（新建卷/删卷/登记镜像后调用）
async function refreshVolumes() {
  await fetchVolumes(curPool.value)
  loadPoolGraph() // 卷变动后本池血缘图同步刷新（失败不阻断表格）
}

async function fetchVolumes(poolName) {
  if (!poolName) return
  try {
    const [poolRes, refsRes] = await Promise.all([
      api.getStoragePool(poolName),
      api.volumeRefs(poolName)
    ])
    volumes.value = (poolRes.data && poolRes.data.volumes) || []
    volRefs.value = (refsRes.data && refsRes.data.refs) || {}
  } catch (e) {
    ElMessage.error(errMsg(e, '获取卷列表失败'))
  }
}

// 清理孤儿卷：先经 volume-refs 算出零引用卷候选，确认后转后台任务删除
async function cleanupOrphans() {
  try {
    const [poolRes, refsRes] = await Promise.all([
      api.getStoragePool(curPool.value),
      api.volumeRefs(curPool.value)
    ])
    const vols = (poolRes.data && poolRes.data.volumes) || []
    const refs = (refsRes.data && refsRes.data.refs) || {}
    const orphans = vols.filter((v) => !refsInUse(refs[v.name] || {})).map((v) => v.name)
    if (!orphans.length) {
      ElMessage.info('该存储池没有孤儿卷（所有卷都有引用）')
      return
    }
    // 名单用 pre VNode 展示：ElMessageBox 纯文本里 \n 不换行，长名单挤成一团不可读（同 DockerList showPruneResult）
    await ElMessageBox.confirm(
      h('div', null, [
        h('p', { style: 'margin:0 0 8px;' }, `检测到 ${orphans.length} 个孤儿卷（无任何引用，删除不可恢复）：`),
        h(
          'pre',
          { style: 'max-height:260px;overflow:auto;margin:0;font-size:12px;line-height:1.6;white-space:pre-wrap;word-break:break-all;' },
          orphans.join('\n')
        )
      ]),
      '清理孤儿卷',
      {
        type: 'warning',
        confirmButtonText: '删除',
        cancelButtonText: '取消',
        confirmButtonClass: 'el-button--danger'
      }
    )
    const res = await api.cleanupOrphans(curPool.value)
    const taskId = extractTaskId(res)
    ElMessage.success('清理任务已提交，后台执行中')
    const task = await pollTask(taskId)
    let detail = ''
    try {
      const r = JSON.parse(task.result || '{}')
      detail = `删除 ${(r.deleted || []).length} 个，保留 ${(r.kept || []).length} 个（详情见任务中心）`
    } catch {
      detail = '已完成'
    }
    ElMessage.success('孤儿卷清理完成：' + detail)
    await refreshVolumes()
    await load()
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '清理失败'))
  }
}

// ===== 新建卷 =====
function openCreateVol() {
  volForm.value = { name: '', format: 'qcow2', capacity: 20 }
  volCreateDialog.value = true
}

async function createVolume() {
  if (!volForm.value.name) {
    ElMessage.warning('请填写卷名称')
    return
  }
  saving.value = true
  try {
    await api.createVolume(curPool.value, { ...volForm.value })
    ElMessage.success('卷已创建')
    volCreateDialog.value = false
    await refreshVolumes()
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '创建失败'))
  } finally {
    saving.value = false
  }
}

async function removeVolume(row) {
  // 删卷确认带上引用详情；后端守卫同样会拦截（双保险）。
  // 有引用时消息含换行，纯文本里 \n 不换行——改 VNode 分行展示
  const r = volRefs.value[row.name]
  const inUse = refsInUse(r)
  let msg = '确定删除卷「' + row.name + '」？此操作不可恢复。'
  if (inUse && r) {
    msg = h('div', null, [
      h('p', { style: 'margin:0 0 8px;' }, '卷「' + row.name + '」正在被使用：' + refsTooltip(r)),
      h('p', { style: 'margin:0;color:var(--el-color-danger);' }, '强删可能导致虚拟机磁盘损坏，确定继续？')
    ])
  }
  try {
    await ElMessageBox.confirm(msg, '确认删除', {
      type: inUse ? 'error' : 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      confirmButtonClass: 'el-button--danger'
    })
    await api.deleteVolume(curPool.value, row.name)
    ElMessage.success('卷已删除')
    await refreshVolumes()
    await load()
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '删除失败'))
  }
}

// ===== 登记为云镜像 =====
function openRegister(row) {
  regVol.value = row.name
  regForm.value = {
    name: (row.name || '').replace(/\.[^.]+$/, ''), // 默认名 = 卷名去扩展名
    os_version: '',
    is_template: false,
    path: row.path || ''
  }
  regDialog.value = true
}

async function doRegister() {
  if (!regForm.value.name) {
    ElMessage.warning('请填写镜像名称')
    return
  }
  regSaving.value = true
  try {
    await api.registerImage({
      name: regForm.value.name,
      path: regForm.value.path,
      os_version: regForm.value.os_version,
      is_template: regForm.value.is_template
    })
    ElMessage.success('已登记为云镜像')
    regDialog.value = false
    await refreshVolumes()
  } catch (e) {
    // 同路径重复登记后端返回 409，errMsg 直接展示中文原因
    ElMessage.error(errMsg(e, '登记失败'))
  } finally {
    regSaving.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.page-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
/* ===== 汇总条 / 池卡片网格 / 卷抽屉的样式 =====
   已随各自渲染下沉到 storage/components/{PoolSummary,PoolCardGrid,VolumeDrawer}.vue
   （.summary-* → PoolSummary，.pool-* → PoolCardGrid，.pool-graph-* / .type-tag /
   .ref-tags → VolumeDrawer）。本页只留页头动作组样式。 */
</style>
