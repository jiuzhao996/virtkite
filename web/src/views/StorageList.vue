<template>
  <div v-loading="loading">
    <div class="page-head">
      <h2 class="page-title">存储管理</h2>
      <div class="page-actions">
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        <el-button v-if="isAdmin" type="success" :icon="Plus" @click="openCreatePool">新建存储池</el-button>
      </div>
    </div>

    <!-- 汇总条：物理容量 / cloud-init 种子目录 / 默认存储池 -->
    <!-- dir 池容量是文件系统级的，同盘多池口径相同，取 items 里最大 capacity 及其 available，不再每行重复 -->
    <el-card shadow="never" class="summary-card">
      <div class="summary-row">
        <div class="summary-item summary-cap">
          <div class="summary-label">物理容量</div>
          <template v-if="physCap.capacity">
            <div class="summary-value">
              {{ fmtSizeBytes(physCap.capacity) }}
              <span class="summary-sub">可用 {{ fmtSizeBytes(physCap.available) }}</span>
            </div>
            <el-progress
              :percentage="physPct"
              :color="usageColor(physPct)"
              :stroke-width="8"
              :show-text="false"
            />
          </template>
          <div v-else class="summary-sub">暂无存储池数据</div>
        </div>
        <div class="summary-item">
          <div class="summary-label">cloud-init 种子目录</div>
          <div class="summary-value mono summary-path" :title="seedDir.path">{{ seedDir.path || '—' }}</div>
          <div class="summary-sub">{{ seedDir.count }} 个 *-seed.iso 种子文件（每台 VM 一份）</div>
        </div>
        <div class="summary-item">
          <div class="summary-label">默认存储池</div>
          <div class="summary-value">{{ defaultPool || '—' }}</div>
          <div class="summary-sub">创建虚拟机未指定池时的落点</div>
        </div>
      </div>
    </el-card>

    <!-- 池卡片网格（替代 el-table，卡片样式对齐 VmList） -->
    <el-empty v-if="!pools.length && !loading" description="暂无存储池" :image-size="80" />
    <el-row v-else :gutter="16">
      <el-col v-for="pool in pools" :key="pool.name" :xs="24" :sm="12" :md="8" class="pool-col">
        <el-card shadow="hover" class="pool-card" @click="openVolumes(pool)">
          <div class="pool-head">
            <span class="pool-name" :title="pool.name">{{ pool.name }}</span>
            <span class="pool-tags">
              <el-tag v-if="!pool.active" type="danger" size="small" effect="light">未激活</el-tag>
              <el-tag v-if="pool.role" :type="roleTagType(pool.role)" size="small" effect="light">{{ pool.role }}</el-tag>
              <el-tag v-else type="info" size="small" effect="plain">未分类</el-tag>
            </span>
          </div>
          <div class="pool-path mono" :title="pool.path">{{ pool.path }}</div>
          <div
            class="pool-desc"
            :class="{ placeholder: !pool.description }"
            :title="pool.description || '点击编辑添加描述'"
            @click.stop="openMeta(pool)"
          >
            {{ pool.description || '点击编辑添加描述' }}
          </div>
          <div class="pool-stats">
            <span title="dir 型池的容量统计是文件系统级的：同盘多个池显示同一口径，非池内卷独占占用">已用 <b class="mono">{{ fmtSizeBytes(pool.allocation) }}</b></span>
            <el-divider direction="vertical" />
            <span>卷数 <b class="mono">{{ pool.vol_count == null ? 0 : pool.vol_count }}</b></span>
          </div>
          <div class="pool-actions" @click.stop>
            <el-button size="small" :icon="FolderOpened" @click="openVolumes(pool)">浏览卷</el-button>
            <el-button v-if="isAdmin" size="small" :icon="Edit" @click="openMeta(pool)">编辑</el-button>
            <el-button v-if="isAdmin" size="small" type="danger" plain :icon="Delete" @click="removePool(pool)">删除</el-button>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <!-- 池信息编辑（角色 + 描述） -->
    <el-dialog v-model="metaDialog" :title="'编辑池信息 - ' + metaForm.name" width="480px">
      <el-form :model="metaForm" label-width="80px">
        <el-form-item label="角色">
          <el-select v-model="metaForm.role" style="width: 100%">
            <el-option label="自动推断（空）" value="" />
            <el-option v-for="r in poolRoles" :key="r" :label="r" :value="r" />
          </el-select>
        </el-form-item>
        <el-form-item label="描述">
          <el-input
            v-model="metaForm.description"
            type="textarea"
            :rows="3"
            maxlength="500"
            show-word-limit
            placeholder="池用途说明（最长 500 字符）"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="metaDialog = false">取消</el-button>
        <el-button type="primary" :loading="metaSaving" @click="saveMeta">保存</el-button>
      </template>
    </el-dialog>

    <!-- 卷抽屉（池名 · 角色） -->
    <el-drawer v-model="volDrawer" :title="curPool ? curPool + ' · ' + (curRole || '未分类') : '卷管理'" size="72%">
      <div class="toolbar">
        <div>
          <el-button v-if="isAdmin" type="success" size="small" :icon="Plus" @click="openCreateVol">新建卷</el-button>
          <el-button v-if="isAdmin" size="small" :icon="Brush" @click="cleanupOrphans">清理孤儿卷</el-button>
        </div>
      </div>
      <el-table :data="volumes" stripe border size="small" style="width: 100%" max-height="560" empty-text="该存储池暂无卷">
        <el-table-column prop="name" label="卷名" min-width="150" show-overflow-tooltip />
        <el-table-column label="类型" width="130">
          <template #default="{ row }">
            <!-- 判定优先级：有子卷=模板基盘 > 有 backing=增量系统盘 > .iso=安装镜像；可叠加为 el-tag 组 -->
            <template v-if="volTypeTags(row).length">
              <el-tag
                v-for="t in volTypeTags(row)"
                :key="t.text"
                :type="t.type"
                size="small"
                effect="light"
                class="type-tag"
              >{{ t.text }}</el-tag>
            </template>
            <el-tag v-else type="info" size="small" effect="plain">普通卷</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="容量" width="90">
          <template #default="{ row }">{{ fmtSizeBytes(row.capacity) }}</template>
        </el-table-column>
        <el-table-column label="已用" width="90">
          <template #default="{ row }">{{ fmtSizeBytes(row.allocation) }}</template>
        </el-table-column>
        <el-table-column label="在用" width="170">
          <template #default="{ row }">
            <template v-if="volRefs[row.name] && refsInUse(volRefs[row.name])">
              <el-tooltip placement="top" :content="refsTooltip(volRefs[row.name])">
                <span class="ref-tags">
                  <el-tag v-if="volRefs[row.name].vms && volRefs[row.name].vms.length" type="warning" size="small" effect="light">
                    VM×{{ volRefs[row.name].vms.length }}
                  </el-tag>
                  <el-tag v-if="volRefs[row.name].images && volRefs[row.name].images.length" type="primary" size="small" effect="light">
                    镜像×{{ volRefs[row.name].images.length }}
                  </el-tag>
                  <el-tag v-if="volRefs[row.name].children && volRefs[row.name].children.length" type="danger" size="small" effect="light">
                    子卷×{{ volRefs[row.name].children.length }}
                  </el-tag>
                </span>
              </el-tooltip>
            </template>
            <el-tag v-else type="info" size="small" effect="plain">未使用</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="200" fixed="right">
          <template #default="{ row }">
            <el-button
              v-if="isAdmin && !hasImageRef(row)"
              size="small"
              type="primary"
              plain
              :icon="Collection"
              @click="openRegister(row)"
            >登记为云镜像</el-button>
            <el-button v-if="isAdmin" size="small" type="danger" :icon="Delete" @click="removeVolume(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-drawer>

    <!-- 登记为云镜像（把池内已有卷纳入镜像库，不复制不移动） -->
    <el-dialog v-model="regDialog" :title="'登记为云镜像 - ' + regVol" width="460px">
      <el-form :model="regForm" label-width="100px">
        <el-form-item label="镜像名称" required>
          <el-input v-model="regForm.name" placeholder="镜像库中的显示名称" />
        </el-form-item>
        <el-form-item label="OS 版本">
          <el-input v-model="regForm.os_version" placeholder="如 ubuntu-22.04，可留空" />
        </el-form-item>
        <el-form-item label="模板镜像">
          <el-switch v-model="regForm.is_template" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="regDialog = false">取消</el-button>
        <el-button type="primary" :loading="regSaving" @click="doRegister">登记</el-button>
      </template>
    </el-dialog>

    <!-- 新建存储池 -->
    <el-dialog v-model="poolDialog" title="新建存储池" width="460px">
      <el-form :model="poolForm" label-width="80px">
        <el-form-item label="名称" required>
          <el-input v-model="poolForm.name" placeholder="仅字母、数字、_、-" />
        </el-form-item>
        <el-form-item label="路径" required>
          <el-input v-model="poolForm.path" placeholder="如 /var/lib/libvirt/xxx-images" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input
            v-model="poolForm.description"
            type="textarea"
            :rows="2"
            maxlength="500"
            show-word-limit
            placeholder="可选，创建成功后保存为池描述"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="poolDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="createPool">创建</el-button>
      </template>
    </el-dialog>

    <!-- 新建卷 -->
    <el-dialog v-model="volCreateDialog" title="新建存储卷" width="460px">
      <el-form :model="volForm" label-width="90px">
        <el-form-item label="名称" required>
          <el-input v-model="volForm.name" placeholder="如 vm-disk1.qcow2" />
        </el-form-item>
        <el-form-item label="格式">
          <el-select v-model="volForm.format" style="width: 100%">
            <el-option label="qcow2" value="qcow2" />
            <el-option label="raw" value="raw" />
          </el-select>
        </el-form-item>
        <el-form-item label="容量(GB)">
          <el-input-number v-model="volForm.capacity" :min="1" :max="500" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="volCreateDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="createVolume">创建</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Plus, FolderOpened, Edit, Delete, Collection, Brush } from '@element-plus/icons-vue'
import { api } from '../api'
import { useAuth } from '../store/auth'
import { pollTask, extractTaskId } from '../utils/task.js'
// 容量格式化 / 错误文案 / 取消判定统一走 utils/format.js（原本地三份实现已删）
// 本页的 .page-head / .page-title / .toolbar / .count 与其他列表页逐字相同，已收进 global.css
import { fmtSizeBytes, errMsg, isCancel, usageColor, clampPct } from '../utils/format'

const { isAdmin } = useAuth()

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

const volDrawer = ref(false)
const volCreateDialog = ref(false)
const regDialog = ref(false)
const regSaving = ref(false)
const curPool = ref('')
const curRole = ref('')
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

// 池角色 → el-tag type（其余/空串走「未分类」灰 tag，不在本表）
const ROLE_TAG_TYPES = {
  模板基盘: 'warning',
  系统盘: 'primary',
  数据盘: 'success',
  安装镜像: 'info',
  系统池: 'info'
}
function roleTagType(role) {
  return ROLE_TAG_TYPES[role] || 'info'
}

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

// 卷类型徽标组（按优先级叠加）：有子卷=模板基盘；有 backing 父盘=增量系统盘；.iso=安装镜像
function volTypeTags(row) {
  const tags = []
  const r = volRefs.value[row.name] || {}
  if (r.children && r.children.length) tags.push({ text: '模板基盘', type: 'warning' })
  if (row.backing_file) tags.push({ text: '增量系统盘', type: 'primary' })
  if (/\.iso$/i.test(row.name || '')) tags.push({ text: '安装镜像', type: 'info' })
  return tags
}

// 该卷是否已登记进镜像库（已登记则不显示「登记为云镜像」按钮）
function hasImageRef(row) {
  const r = volRefs.value[row.name]
  return !!(r && r.images && r.images.length)
}

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
  if (!poolForm.value.name || !poolForm.value.path) {
    ElMessage.warning('请填写名称和路径')
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
    await ElMessageBox.confirm('确定删除存储池「' + row.name + '」？', '确认删除', { type: 'warning' })
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
  await fetchVolumes(pool.name)
}

// 刷新卷列表 + 引用（新建卷/删卷/登记镜像后调用）
async function refreshVolumes() {
  await fetchVolumes(curPool.value)
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
    await ElMessageBox.confirm(
      `检测到 ${orphans.length} 个孤儿卷（无任何引用，删除不可恢复）：\n${orphans.join('、')}`,
      '清理孤儿卷',
      { type: 'warning' }
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
  // 删卷确认带上引用详情；后端守卫同样会拦截（双保险）
  const r = volRefs.value[row.name]
  let msg = '确定删除卷「' + row.name + '」？此操作不可恢复。'
  if (r && refsInUse(r)) {
    msg = '卷「' + row.name + '」正在被使用：' + refsTooltip(r) + '。\n强删可能导致虚拟机磁盘损坏，确定继续？'
  }
  try {
    await ElMessageBox.confirm(msg, '确认删除', { type: refsInUse(r) ? 'error' : 'warning' })
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
/* ===== 汇总条 ===== */
.summary-card {
  margin-bottom: 16px;
}
.summary-row {
  display: flex;
  align-items: flex-start;
  flex-wrap: wrap;
  gap: 16px 48px;
}
.summary-item {
  min-width: 180px;
}
.summary-cap {
  flex: 1;
  min-width: 240px;
  max-width: 380px;
}
.summary-label {
  font-size: 0.8rem;
  color: var(--color-muted-foreground);
  margin-bottom: 4px;
}
.summary-value {
  font-size: 1.05rem;
  font-weight: 700;
  color: var(--color-foreground);
  margin-bottom: 6px;
}
.summary-path {
  font-size: 0.85rem;
  max-width: 340px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.summary-sub {
  font-size: 0.8rem;
  color: var(--color-muted-foreground);
}
/* ===== 池卡片网格 ===== */
.pool-col {
  margin-bottom: 16px;
}
.pool-card {
  cursor: pointer;
  height: 100%;
  transition: border-color 0.2s ease, box-shadow 0.2s ease;
}
.pool-card:hover {
  border-color: var(--el-color-primary);
}
.pool-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.pool-name {
  flex: 1;
  font-size: 1rem;
  font-weight: 700;
  color: var(--color-foreground);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pool-tags {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}
.pool-path {
  font-size: 0.78rem;
  color: var(--color-muted-foreground);
  margin-bottom: 8px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pool-desc {
  font-size: 0.85rem;
  line-height: 1.4;
  color: var(--color-foreground);
  min-height: 40px;
  margin-bottom: 8px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.pool-desc.placeholder {
  color: var(--color-muted-foreground);
  opacity: 0.7;
}
.pool-stats {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 0.85rem;
  color: var(--color-muted-foreground);
  margin-bottom: 12px;
}
.pool-stats b {
  color: var(--color-foreground);
  font-weight: 700;
}
.pool-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding-top: 12px;
  border-top: 1px solid var(--color-border);
}
/* el-button 相邻默认 margin-left 会与 flex gap 叠加，归零由 gap 控距 */
.pool-actions .el-button + .el-button {
  margin-left: 0;
}
/* ===== 卷抽屉 ===== */
.type-tag {
  margin-right: 4px;
}
.ref-tags {
  display: inline-flex;
  gap: 4px;
}
</style>
