<template>
  <el-drawer v-model="visible" :title="''" size="55%" :close-on-click-modal="false">
    <template #header>
      <div class="nd-head">
        <span class="nd-name mono">{{ name }}</span>
        <el-tag v-if="net" :type="net.active ? 'success' : 'info'" effect="light">{{ net.active ? '运行' : '停止' }}</el-tag>
        <el-tag v-if="net && net.autostart" effect="plain" size="small">自启</el-tag>
        <span class="nd-spacer" />
        <el-button size="small" :icon="Share" @click="$emit('open-topology')">在网络拓扑中查看</el-button>
      </div>
    </template>

    <div v-loading="loading">
      <el-descriptions :column="2" border size="small">
        <el-descriptions-item label="网桥"><span class="mono">{{ (net && net.bridge) || '—' }}</span></el-descriptions-item>
        <el-descriptions-item label="转发模式">{{ (net && net.forward) || '—' }}</el-descriptions-item>
        <el-descriptions-item label="网段"><span class="mono">{{ subnet || '—' }}</span></el-descriptions-item>
        <el-descriptions-item label="网关"><span class="mono">{{ (net && net.gateway) || '—' }}</span></el-descriptions-item>
        <el-descriptions-item label="DHCP 范围" :span="2">
          <span class="mono">{{ net && net.dhcp_start ? net.dhcp_start + ' - ' + net.dhcp_end : '—' }}</span>
        </el-descriptions-item>
      </el-descriptions>

      <div class="nd-sec">
        <div class="nd-sec-title">
          成员（虚拟机网卡）
          <span class="nd-count">{{ members.length }}</span>
        </div>
        <el-table :data="members" size="small" stripe>
          <template #empty><el-empty description="该网络下暂无虚拟机网卡" :image-size="60" /></template>
          <el-table-column label="虚拟机" min-width="140">
            <template #default="{ row }">
              <el-link type="primary" class="mono" @click="goVm(row.vm)">{{ row.vm }}</el-link>
            </template>
          </el-table-column>
          <el-table-column label="MAC" min-width="140"><template #default="{ row }"><span class="mono">{{ row.mac || '—' }}</span></template></el-table-column>
          <el-table-column label="IP" width="130"><template #default="{ row }"><span class="mono">{{ row.ip || '—' }}</span></template></el-table-column>
          <el-table-column label="网卡型号" width="100"><template #default="{ row }">{{ row.model || '—' }}</template></el-table-column>
          <el-table-column label="状态" width="90">
            <template #default="{ row }">
              <el-tag :type="row.state === 'running' ? 'success' : 'info'" effect="plain" size="small">{{ row.state || '—' }}</el-tag>
            </template>
          </el-table-column>
        </el-table>
        <p v-if="!members.some((m) => m.ip)" class="nd-hint">IP 仅在虚拟机运行并获取到 DHCP 租约时显示；静态 IP 需要客户机内 qemu-guest-agent。</p>
      </div>

      <el-collapse v-if="net && net.xml" class="nd-xml">
        <el-collapse-item title="网络定义 XML">
          <div class="nd-xml-bar">
            <CopyButton :text="net.xml" tip="复制 XML" />
          </div>
          <pre class="nd-pre">{{ net.xml }}</pre>
        </el-collapse-item>
      </el-collapse>
    </div>
  </el-drawer>
</template>

<script setup>
// libvirt 虚拟网络详情抽屉（N2）：概要 + 成员表（挂在该网络的虚拟机网卡）+ XML。
// 成员数据来自后端 GET /api/networks/:name 的 members 字段（域网卡 + DHCP 租约 IP 回填）。
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Share } from '@element-plus/icons-vue'
import CopyButton from '../../components/CopyButton.vue'
import { api } from '../../api'
import { errMsg } from '../../utils/format'

defineEmits(['open-topology'])
const router = useRouter()

const visible = ref(false)
const loading = ref(false)
const name = ref('')
const net = ref(null)
const members = ref([])
const subnet = ref('')

function goVm(vm) {
  router.push({ path: '/vms', query: { keyword: vm } })
}

async function open(n) {
  name.value = n
  net.value = null
  members.value = []
  visible.value = true
  loading.value = true
  try {
    const res = await api.getNetwork(n)
    const d = res.data || {}
    net.value = d.network || null
    members.value = d.members || []
    subnet.value = d.subnet || ''
  } catch (e) {
    ElMessage.error(errMsg(e, '获取网络详情失败'))
  } finally {
    loading.value = false
  }
}

defineExpose({ open })
</script>

<style scoped>
.nd-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.nd-name {
  font-weight: 600;
  font-size: 1rem;
}
.nd-spacer {
  flex: 1;
}
.nd-sec {
  margin-top: 18px;
}
.nd-sec-title {
  font-weight: 600;
  font-size: 0.92rem;
  margin-bottom: 8px;
}
.nd-count {
  color: var(--color-muted-foreground);
  font-weight: 400;
  margin-left: 4px;
}
.nd-hint {
  margin: 8px 0 0;
  font-size: 0.78rem;
  color: var(--color-muted-foreground);
}
.nd-xml {
  margin-top: 16px;
}
.nd-xml-bar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 6px;
}
.nd-pre {
  margin: 0;
  padding: 12px;
  background: #0d1b2a;
  color: #cfe8ff;
  border-radius: var(--radius-md, 8px);
  font-family: var(--font-mono);
  font-size: 0.78rem;
  line-height: 1.5;
  overflow: auto;
  max-height: 320px;
}
</style>
