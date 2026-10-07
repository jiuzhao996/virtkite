<template>
  <div class="ipam-wrap">
    <Toolbar>
      <template #left>
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        <el-tag effect="plain" size="small">网段 {{ items.length }}</el-tag>
        <el-tag effect="plain" size="small" type="warning">已用 {{ totalUsed }}</el-tag>
        <el-tag effect="plain" size="small" type="success">可用 {{ totalFree }}</el-tag>
      </template>
    </Toolbar>

    <el-table :data="items" v-loading="loading" row-key="key" size="small" stripe>
      <template #empty><el-empty description="暂无带网段的网络" :image-size="80" /></template>
      <el-table-column type="expand">
        <template #default="{ row }">
          <div class="ipam-expand">
            <el-table :data="row.addresses" size="small">
              <template #empty><el-empty description="该网段暂无已用地址记录（不含静态 IP）" :image-size="50" /></template>
              <el-table-column label="IP" width="160"><template #default="{ row: a }"><span class="mono">{{ a.ip }}</span></template></el-table-column>
              <el-table-column label="归属" min-width="180"><template #default="{ row: a }">{{ a.owner }}</template></el-table-column>
              <el-table-column label="来源" width="120">
                <template #default="{ row: a }">
                  <el-tag :type="srcTag(a.source)" effect="plain" size="small">{{ srcText(a.source) }}</el-tag>
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
      <el-table-column label="已用" width="80"><template #default="{ row }"><b>{{ row.used }}</b></template></el-table-column>
      <el-table-column label="可用" width="100"><template #default="{ row }">{{ row.total ? row.free : '—' }}</template></el-table-column>
      <el-table-column label="使用率" width="180">
        <template #default="{ row }">
          <el-progress v-if="row.total" :percentage="pct(row)" :stroke-width="10" :color="pctColor(row)" />
          <span v-else class="ipam-na">—</span>
        </template>
      </el-table-column>
      <el-table-column label="DHCP 范围" min-width="180">
        <template #default="{ row }">
          <span class="mono">{{ row.dhcp_start ? row.dhcp_start + ' - ' + row.dhcp_end : '—' }}</span>
        </template>
      </el-table-column>
    </el-table>

    <p class="ipam-hint">
      已用地址来自 DHCP 租约、容器网络端点与宿主邻居表三源合并；静态 IP 的虚拟机不在租约中，可能不计入。
      「可用」= 网段可用地址总数 − 已用（不含静态占用）。
    </p>
  </div>
</template>

<script setup>
// IP 分配一览（N2b）：各网段的已用/可用 IP 与归属，展开行看具体地址。纯聚合视图，无新采集。
import { ref, computed, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { api } from '../../api'
import { errMsg } from '../../utils/format'
import Toolbar from '../../components/Toolbar.vue'

const items = ref([])
const loading = ref(false)

const totalUsed = computed(() => items.value.reduce((a, i) => a + i.used, 0))
const totalFree = computed(() => items.value.reduce((a, i) => a + (i.total ? i.free : 0), 0))

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
  return { dhcp: 'DHCP 租约', container: '容器', neighbor: '邻居表' }[s] || s
}
function srcTag(s) {
  return { dhcp: 'primary', container: 'success', neighbor: 'info' }[s] || 'info'
}

async function load() {
  loading.value = true
  try {
    const res = await api.networkIpam()
    const list = (res.data && res.data.items) || []
    // 稳定 key（虚拟网络与 docker 网络可能重名，加 kind 前缀）
    items.value = list.map((it) => ({ ...it, key: it.kind + ':' + it.name }))
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
.ipam-hint {
  margin: 10px 2px 0;
  font-size: 0.8rem;
  color: var(--color-muted-foreground);
}
</style>
