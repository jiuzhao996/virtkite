<template>
  <div class="ipam-wrap">
    <Toolbar>
      <template #left>
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        <el-tag effect="plain" size="small">网段 {{ items.length }}</el-tag>
        <el-tag effect="plain" size="small" type="warning">已用 {{ totalUsed }}</el-tag>
        <el-tag effect="plain" size="small" type="success">空闲 {{ totalFree }}</el-tag>
      </template>
    </Toolbar>

    <!-- 网段概览表：展开行显示「已用 / 空闲交错」的地址分配明细（NetBox Prefix 详情思路） -->
    <el-table :data="items" v-loading="loading" row-key="key" size="small" stripe>
      <template #empty><EmptyState description="暂无带网段的网络" /></template>

      <!-- 展开行：已用地址与空闲区间按地址交错排成一张表，一眼看清空洞在哪 -->
      <el-table-column type="expand">
        <template #default="{ row: net }">
          <div class="ipam-expand">
            <el-table :data="net.rows" size="small" row-key="_key">
              <template #empty><el-empty description="该网段暂无地址记录" :image-size="50" /></template>
              <el-table-column label="" width="72">
                <template #default="{ row: r }">
                  <el-tag :type="r.kind === 'free' ? 'success' : 'info'" effect="plain" size="small">
                    {{ r.kind === 'free' ? '空闲' : '已用' }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column label="地址" min-width="210">
                <template #default="{ row: r }">
                  <span class="mono">{{ r.kind === 'free' ? r.start + ' - ' + r.end : r.ip }}</span>
                </template>
              </el-table-column>
              <el-table-column label="归属 / 大小" min-width="220">
                <template #default="{ row: r }">
                  <template v-if="r.kind === 'used'">
                    <el-link v-if="r.vm_id" type="primary" :underline="false" class="owner-link" @click="openVm(r.vm_id)">
                      {{ r.owner }}
                    </el-link>
                    <span v-else>{{ r.owner }}</span>
                    <el-tag v-if="r.vm_status" :type="vmTag(r.vm_status)" effect="plain" size="small" class="owner-status">
                      {{ r.vm_status }}
                    </el-tag>
                  </template>
                  <span v-else class="ipam-free-size">{{ r.size }} 个可用</span>
                </template>
              </el-table-column>
              <el-table-column label="来源 / 操作" width="230" align="right">
                <template #default="{ row: r }">
                  <el-tag v-if="r.kind === 'used'" :type="srcTag(r.source)" effect="plain" size="small">{{ srcText(r.source) }}</el-tag>
                  <template v-else>
                    <el-tooltip v-if="canOperate && net.kind === 'libvirt'" :content="'预填静态 IP ' + r.start + ' 进入建机向导'" placement="top" :show-after="200">
                      <el-button size="small" type="primary" plain :icon="Plus" @click="useIP(r.start, net)">用此 IP 建机</el-button>
                    </el-tooltip>
                    <CopyButton :text="r.start" tip="复制起始 IP" />
                  </template>
                </template>
              </el-table-column>
            </el-table>
          </div>
        </template>
      </el-table-column>

      <el-table-column label="网络" min-width="140">
        <template #default="{ row }"><span class="mono">{{ row.name }}</span></template>
      </el-table-column>
      <el-table-column label="类型" width="90">
        <template #default="{ row }">
          <el-tag :type="row.kind === 'libvirt' ? 'primary' : 'info'" effect="plain" size="small">{{ row.kind }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="网段" min-width="140"><template #default="{ row }"><span class="mono">{{ row.subnet }}</span></template></el-table-column>
      <el-table-column label="网关" width="130"><template #default="{ row }"><span class="mono">{{ row.gateway || '—' }}</span></template></el-table-column>
      <el-table-column label="已用 / 总数" width="110">
        <template #default="{ row }"><b>{{ row.used }}</b><span class="ipam-na"> / {{ row.total || '—' }}</span></template>
      </el-table-column>
      <el-table-column label="使用率" width="170">
        <template #default="{ row }">
          <el-progress v-if="row.total" :percentage="pct(row)" :stroke-width="10" :color="pctColor(row)" />
          <span v-else class="ipam-na">—</span>
        </template>
      </el-table-column>
      <el-table-column label="下一个可用 IP" min-width="170">
        <template #default="{ row }">
          <template v-if="row.next_free">
            <span class="mono">{{ row.next_free }}</span>
            <CopyButton :text="row.next_free" tip="复制" />
          </template>
          <span v-else class="ipam-na">{{ row.total ? '已满' : '—' }}</span>
        </template>
      </el-table-column>
      <el-table-column label="DHCP 范围" min-width="180">
        <template #default="{ row }">
          <span class="mono">{{ row.dhcp_start ? row.dhcp_start + ' - ' + row.dhcp_end : '—' }}</span>
        </template>
      </el-table-column>
    </el-table>

    <p class="ipam-hint">
      已用地址来自 DHCP 租约、虚拟机静态 IP（vms.ip）、容器网络端点与宿主邻居表，多源合并去重。
      空闲区间为「网段全集 − 已占用（含网关保留地址）」现算得出，不落库；网络地址与广播地址不计入。
      展开网段可看「已用 / 空闲」交错明细，空闲行可一键预填静态 IP 进入建机向导。
    </p>
  </div>
</template>

<script setup>
// IP 分配一览（N2b）：各网段的已用/空闲地址与归属。
// 可用地址由后端现算成连续区间（service/ipam），前端把「已用行」与「空闲区间行」交错排成一张表。
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Refresh, Plus } from '@element-plus/icons-vue'
import { api } from '../../api'
import { errMsg } from '../../utils/format'
import { copyText } from '../../utils/clipboard'
import { useAuth } from '../../store/auth'
import Toolbar from '../../components/Toolbar.vue'
import CopyButton from '../../components/CopyButton.vue'
import EmptyState from '../../components/EmptyState.vue'

const router = useRouter()
const { canOperate } = useAuth()

const items = ref([])
const loading = ref(false)

const totalUsed = computed(() => items.value.reduce((a, i) => a + i.used, 0))
const totalFree = computed(() => items.value.reduce((a, i) => a + i.free, 0))

// ipNum IPv4 字符串 → 数值（用于把已用地址与空闲区间按地址交错排序）；非法值排到最后。
function ipNum(ip) {
  const p = String(ip).split('.').map(Number)
  if (p.length !== 4 || p.some((n) => !Number.isInteger(n))) return Number.MAX_SAFE_INTEGER
  return p[0] * 16777216 + p[1] * 65536 + p[2] * 256 + p[3]
}

// buildRows 把已用地址与空闲区间合并成按地址升序的交错行（NetBox AvailableIPSpace 思路）。
function buildRows(it) {
  const rows = []
  for (const a of it.addresses || []) {
    rows.push({ kind: 'used', ip: a.ip, owner: a.owner, source: a.source, vm_id: a.vm_id, vm_status: a.vm_status, _key: 'u:' + a.ip, _sort: ipNum(a.ip) })
  }
  for (const r of it.free_ranges || []) {
    rows.push({ kind: 'free', start: r.start, end: r.end, size: r.size, _key: 'f:' + r.start, _sort: ipNum(r.start) })
  }
  rows.sort((x, y) => x._sort - y._sort)
  return rows
}

function pct(row) {
  if (!row.total) return 0
  return Math.min(100, Math.round((row.used / row.total) * 100))
}
function pctColor(row) {
  const p = pct(row)
  if (p >= 90) return '#f56c6c'
  if (p >= 70) return '#e6a23c'
  return '#3aa76d'
}
function srcText(s) {
  return { dhcp: 'DHCP 租约', vm: '静态 IP', container: '容器', neighbor: '邻居表', reserved: '保留' }[s] || s
}
function srcTag(s) {
  return { dhcp: 'primary', vm: 'success', container: 'success', neighbor: 'info', reserved: 'warning' }[s] || 'info'
}
function vmTag(s) {
  return { running: 'success', 'shut off': 'info', paused: 'warning', error: 'danger' }[s] || 'info'
}

function openVm(id) {
  if (id) router.push({ name: 'vm-detail', params: { id } })
}

// 建机向导草稿的 sessionStorage 键（与 CreateVmWizard.vue 约定一致）。
// 静态 IP 仅「云镜像 + cloud-init」安装方式支持，故预填时把安装方式切到 cloudimage 并开启 cloud-init；
// 其余草稿字段（计算资源/磁盘/网卡等）保留用户已填内容，避免整份草稿被覆盖。
const WIZARD_DRAFT_KEY = 'vmops_wizard_draft_v1'

// useIP 用某个空闲 IP 预填建机向导：写入向导草稿后跳转；sessionStorage 不可用时退回复制到剪贴板。
function useIP(ip, net) {
  if (!canOperate.value) {
    ElMessage.warning('当前角色无创建虚拟机权限')
    return
  }
  let draft = {}
  try {
    draft = JSON.parse(sessionStorage.getItem(WIZARD_DRAFT_KEY) || '{}') || {}
  } catch {
    draft = {}
  }
  draft.step = 0
  draft.installMode = 'cloudimage'
  draft.cloudInitEnabled = true
  draft.cloudInit = { ...(draft.cloudInit || {}), netMode: 'static', ip, gateway: net.gateway || '' }
  let stored = true
  try {
    sessionStorage.setItem(WIZARD_DRAFT_KEY, JSON.stringify(draft))
  } catch {
    stored = false
  }
  if (stored) {
    ElMessage.success('已预填静态 IP ' + ip + '（安装方式已切到云镜像 + cloud-init）')
  } else {
    // 草稿存不下（隐私模式等）：退回「复制到剪贴板 + 提示」，让用户手动填写
    copyText(ip).then((ok) => {
      ok ? ElMessage.success('已复制 IP ' + ip + '，请在向导中手动填写静态 IP') : ElMessage.warning('请手动记录 IP：' + ip)
    })
  }
  router.push({ name: 'vm-create' })
}

async function load() {
  loading.value = true
  try {
    const res = await api.networkIpam()
    const list = (res.data && res.data.items) || []
    // 稳定 key（虚拟网络与 docker 网络可能重名，加 kind 前缀）；预构造交错行
    items.value = list.map((it) => ({ ...it, key: it.kind + ':' + it.name, rows: buildRows(it) }))
  } catch (e) {
    ElMessage.error(errMsg(e, '获取 IP 分配失败'))
  } finally {
    loading.value = false
  }
}

onMounted(load)
defineExpose({ load })
</script>

<style scoped>
.ipam-wrap {
  display: flex;
  flex-direction: column;
}
.ipam-expand {
  padding: 4px 12px 8px 40px;
}
.ipam-na {
  color: var(--color-muted-foreground);
}
.ipam-free-size {
  color: var(--color-muted-foreground);
}
.owner-link {
  vertical-align: baseline;
}
.owner-status {
  margin-left: 8px;
}
.ipam-hint {
  margin: 10px 2px 0;
  font-size: 0.8rem;
  color: var(--color-muted-foreground);
}
</style>
