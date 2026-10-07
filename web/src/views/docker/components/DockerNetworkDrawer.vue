<template>
  <el-drawer v-model="visible" :title="''" size="55%" :close-on-click-modal="false">
    <template #header>
      <div class="dd-head">
        <span class="dd-name mono">{{ name }}</span>
        <el-tag v-if="detail && detail.builtin" effect="plain" size="small">内置</el-tag>
        <el-tag v-if="detail" effect="plain" size="small" type="info">{{ detail.driver }}</el-tag>
      </div>
    </template>

    <div v-loading="loading">
      <el-descriptions :column="2" border size="small">
        <el-descriptions-item label="驱动">{{ (detail && detail.driver) || '—' }}</el-descriptions-item>
        <el-descriptions-item label="类型">{{ detail && detail.builtin ? '内置网络' : '自定义网络' }}</el-descriptions-item>
        <el-descriptions-item label="网段"><span class="mono">{{ (detail && detail.subnet) || '—' }}</span></el-descriptions-item>
        <el-descriptions-item label="网关"><span class="mono">{{ (detail && detail.gateway) || '—' }}</span></el-descriptions-item>
      </el-descriptions>

      <div class="dd-sec">
        <div class="dd-sec-title">
          已连接容器
          <span class="dd-count">{{ containers.length }}</span>
        </div>
        <el-table :data="containers" size="small" stripe>
          <template #empty><el-empty description="该网络下暂无运行中的容器" :image-size="60" /></template>
          <el-table-column label="容器" min-width="160">
            <template #default="{ row }">
              <el-link type="primary" class="mono" @click="goContainer(row.id)">{{ row.name }}</el-link>
            </template>
          </el-table-column>
          <el-table-column label="IPv4" width="150"><template #default="{ row }"><span class="mono">{{ row.ipv4 || '—' }}</span></template></el-table-column>
          <el-table-column label="MAC" min-width="150"><template #default="{ row }"><span class="mono">{{ row.mac || '—' }}</span></template></el-table-column>
        </el-table>
        <p class="dd-hint">仅运行中/暂停的容器保有网络端点，停止的容器不出现在此表（其端点已随停止移除）。</p>
      </div>
    </div>
  </el-drawer>
</template>

<script setup>
// Docker 网络详情抽屉（N2）：概要 + 已连接容器表（容器名可点跳容器详情）。
// 数据来自后端 GET /api/docker/networks/:name（network inspect 的 Containers 字段）。
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { api } from '../../../api'
import { errMsg } from '../../../utils/format'

const router = useRouter()

const visible = ref(false)
const loading = ref(false)
const name = ref('')
const detail = ref(null)
const containers = ref([])

function goContainer(id) {
  router.push({ path: '/containers', query: { tab: 'containers', id } })
}

async function open(n) {
  name.value = n
  detail.value = null
  containers.value = []
  visible.value = true
  loading.value = true
  try {
    const res = await api.dockerNetworkDetail(n)
    const d = res.data || {}
    detail.value = d
    containers.value = d.containers || []
  } catch (e) {
    ElMessage.error(errMsg(e, '获取网络详情失败'))
  } finally {
    loading.value = false
  }
}

defineExpose({ open })
</script>

<style scoped>
.dd-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.dd-name {
  font-weight: 600;
  font-size: 1rem;
}
.dd-sec {
  margin-top: 18px;
}
.dd-sec-title {
  font-weight: 600;
  font-size: 0.92rem;
  margin-bottom: 8px;
}
.dd-count {
  color: var(--color-muted-foreground);
  font-weight: 400;
  margin-left: 4px;
}
.dd-hint {
  margin: 8px 0 0;
  font-size: 0.78rem;
  color: var(--color-muted-foreground);
}
</style>
