<template>
  <div v-loading="loading">
    <el-row :gutter="16">
      <el-col :xs="12" :sm="8" :md="6" v-for="s in stats" :key="s.label">
        <el-card shadow="hover" class="stat-card">
          <div class="stat-icon">{{ s.icon }}</div>
          <el-statistic :value="s.value" :value-style="{ color: '#2a9da5', fontWeight: 700 }" />
          <div class="stat-label">{{ s.label }}</div>
        </el-card>
      </el-col>
    </el-row>

    <el-row :gutter="16" class="mt">
      <el-col :md="12">
        <el-card shadow="hover" header="虚拟机状态分布">
          <div v-if="vmStatus.length === 0" class="empty">暂无数据</div>
          <div v-for="item in vmStatus" :key="item.status" class="status-row">
            <span class="status-name">{{ statusText(item.status) }}</span>
            <el-progress
              class="status-bar"
              :percentage="pct(item.count)"
              :color="statusColor(item.status)"
              :format="() => item.count + ' 台'"
            />
          </div>
        </el-card>
      </el-col>
      <el-col :md="12">
        <el-card shadow="hover" header="操作概览">
          <div class="info-rows">
            <div><span>平台</span><strong>vmops · KVM 私有云</strong></div>
            <div><span>后端</span><strong>Go + Gin + GORM + Libvirt</strong></div>
            <div><span>前端</span><strong>Vue 3 + Element Plus + Vite</strong></div>
            <div><span>当前用户</span><strong>{{ userText }}</strong></div>
          </div>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { api } from '../api'
import { useAuth } from '../store/auth'

const { state } = useAuth()
const loading = ref(false)
const overview = ref(null)
const vmStatus = ref([])

const stats = computed(() => {
  const o = overview.value || {}
  return [
    { label: '宿主机', icon: '🖥', value: o.host_count || 0 },
    { label: '虚拟机', icon: '🖥️', value: o.vm_count || 0 },
    { label: '运行中', icon: '🟢', value: o.running_vm_count || 0 },
    { label: '存储池', icon: '💾', value: o.pool_count || 0 },
    { label: '网络', icon: '🌐', value: o.network_count || 0 },
    { label: '镜像', icon: '🖼️', value: o.image_count || 0 },
    { label: '用户', icon: '👤', value: o.user_count || 0 },
    { label: '审计', icon: '📝', value: o.audit_count || 0 }
  ]
})

const userText = computed(() => {
  const u = state.user
  if (!u) return '—'
  return u.username + '（' + (u.role === 'admin' ? '管理员' : '用户') + '）'
})

const totalVM = computed(() => vmStatus.value.reduce((a, b) => a + b.count, 0))

function pct(count) {
  if (!totalVM.value) return 0
  return Math.round((count / totalVM.value) * 100)
}
function statusText(s) {
  return { running: '运行中', 'shut off': '已关机', paused: '已暂停', 'shut off ': '已关机' }[s] || s
}
function statusColor(s) {
  if (s === 'running') return '#27ae60'
  if (s === 'paused') return '#f39c12'
  if (s === 'shut off') return '#95a5a6'
  return '#3db8bf'
}

async function load() {
  loading.value = true
  try {
    const [ov, vs] = await Promise.all([api.dashboardOverview(), api.vmStatus()])
    overview.value = ov.data
    vmStatus.value = vs.data || []
  } catch (e) {
    // 忽略
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.stat-card {
  text-align: center;
  margin-bottom: 4px;
}
.stat-icon {
  font-size: 1.8rem;
}
.stat-label {
  font-size: 0.85rem;
  color: #888;
  margin-top: 4px;
}
.mt {
  margin-top: 16px;
}
.status-row {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 14px;
}
.status-name {
  width: 70px;
  font-size: 0.9rem;
  color: #555;
}
.status-bar {
  flex: 1;
}
.empty {
  color: #999;
  text-align: center;
  padding: 20px 0;
}
.info-rows {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.info-rows > div {
  display: flex;
  gap: 10px;
  font-size: 0.95rem;
}
.info-rows span {
  color: #999;
  min-width: 56px;
}
</style>
