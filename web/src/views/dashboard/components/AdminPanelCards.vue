<template>
  <!-- Row: 操作分布 + 告警概览 -->
  <el-row :gutter="16" class="mt">
    <el-col v-if="isAdmin" :md="12">
      <el-card shadow="hover">
        <template #header>
          <div class="alert-card-head">
            <span class="card-title">操作类型分布</span>
            <el-link type="primary" :underline="false" @click="$router.push('/audit')">进审计中心</el-link>
          </div>
        </template>
        <div v-if="topActions.length === 0" class="empty">暂无数据</div>
        <div v-for="a in topActions" :key="a.action" class="action-row">
          <span class="action-name">{{ actionLabel(a.action) }}</span>
          <div class="action-track">
            <div class="action-fill" :style="{ width: actionPct(a.count) + '%', background: actionColor(a.action) }" />
          </div>
          <span class="action-count">{{ a.count }}</span>
        </div>
      </el-card>
    </el-col>
    <el-col v-if="canOperate" :md="isAdmin ? 12 : 24">
      <el-card shadow="hover" class="alert-overview-card" :class="{ firing: firingAlerts.length }">
        <template #header>
          <div class="alert-card-head">
            <span class="card-title">告警概览</span>
            <div class="alert-head-actions">
              <el-tag v-if="!alertsError && firingAlerts.length" type="danger" effect="light" size="small">
                {{ firingAlerts.length }} 条待处理
              </el-tag>
              <el-link type="primary" :underline="false" @click="$emit('go-monitor')">前往监控中心</el-link>
            </div>
          </div>
        </template>
        <div v-if="alertsError" class="empty">监控栈未连接（docker compose up -d 启动 Prometheus / Alertmanager）</div>
        <template v-else>
          <div v-if="firingAlerts.length === 0" class="empty alert-ok">当前无告警，一切正常</div>
          <div v-for="a in firingAlerts.slice(0, 4)" :key="a.fingerprint" class="alert-row">
            <el-tag :type="(a.labels && a.labels.severity) === 'critical' ? 'danger' : 'warning'" effect="dark" size="small">
              {{ (a.labels && a.labels.severity) === 'critical' ? '严重' : '警告' }}
            </el-tag>
            <span class="alert-name">{{ a.labels && a.labels.alertname }}</span>
            <span class="alert-summary">{{ (a.annotations && a.annotations.summary) || '' }}</span>
          </div>
          <div v-if="firingAlerts.length > 4" class="empty">还有 {{ firingAlerts.length - 4 }} 条告警，见监控中心</div>
        </template>
      </el-card>
    </el-col>
  </el-row>

  <el-row :gutter="16" class="mt">
    <el-col :span="24">
      <el-card shadow="hover">
        <template #header>
          <span class="card-title">平台信息</span>
        </template>
        <div class="info-rows">
          <div><span>平台</span><strong>鸢航 VirtKite · KVM 私有云</strong></div>
          <div><span>后端</span><strong>Go + Gin + GORM + Libvirt</strong></div>
          <div><span>前端</span><strong>Vue 3 + Element Plus + ECharts</strong></div>
          <div><span>当前用户</span><strong>{{ userText }}</strong></div>
          <template v-if="sysInfo">
            <div><span>libvirt URI</span><strong>{{ sysInfo.virt && sysInfo.virt.libvirt_uri }}</strong></div>
            <div><span>存储池</span><strong>{{ (sysInfo.storage && sysInfo.storage.pools || []).join('、') || '—' }}</strong></div>
            <div><span>虚拟网络</span><strong>{{ (sysInfo.network && sysInfo.network.networks || []).join('、') || '—' }}</strong></div>
            <div><span>运行模式</span><strong>{{ sysInfo.platform && sysInfo.platform.server_mode }} · :{{ sysInfo.platform && sysInfo.platform.server_port }}</strong></div>
          </template>
        </div>
      </el-card>
    </el-col>
  </el-row>
</template>

<script setup>
// 管理侧三卡合体（自 Dashboard.vue 拆出，渲染输出不变）：
// 操作类型分布（admin 可见）/ 告警概览（操作员及以上可见）/ 平台信息（登录即可见）。
// 可见性沿用 useAuth 的 isAdmin / canOperate；「前往监控中心」改为 emit('go-monitor')
// 由 shell 切 tab（保证 visitedTabs / lazy 挂载编排仍收敛在壳里）。
import { computed } from 'vue'
import { useAuth } from '../../../store/auth'

const props = defineProps({
  auditActions: { type: Array, required: true }, // /audit/actions 原始行，组件内取前 8
  actionLabels: { type: Object, required: true }, // FALLBACK_ACTION_LABELS + 后端覆盖（shell 合并后下发）
  firingAlerts: { type: Array, required: true }, // state === 'active' 的告警（shell 过滤后下发）
  alertsError: { type: Boolean, default: false }, // 监控栈未连接 → 空态提示
  sysInfo: { type: Object, default: null }, // /settings 系统信息（仅 admin 拉取）
  userText: { type: String, required: true } // 「当前用户」展示文案
})

defineEmits(['go-monitor'])

const { isAdmin, canOperate } = useAuth()

// 审计动作分布（取前 8）
const topActions = computed(() => [...props.auditActions].sort((a, b) => b.count - a.count).slice(0, 8))
const maxAction = computed(() => (topActions.value.length ? Math.max(...topActions.value.map((a) => a.count)) : 1))
function actionPct(c) {
  return Math.max(3, Math.round((c / maxAction.value) * 100))
}
function actionLabel(a) {
  return props.actionLabels[a] || a
}
function actionColor(a) {
  if (a.includes('delete')) return 'var(--color-danger)'
  if (a.includes('create') || a.includes('upload') || a.includes('import')) return 'var(--color-primary)'
  if (a.includes('start') || a.includes('login')) return 'var(--color-success)'
  if (a.includes('stop') || a.includes('restart')) return 'var(--color-warning)'
  return 'var(--color-info)'
}
</script>

<style scoped>
.card-title {
  font-weight: 600;
  color: var(--color-foreground);
}
.mt {
  margin-top: 16px;
}
.empty {
  color: var(--color-muted-foreground);
  text-align: center;
  padding: 20px 0;
}
.alert-card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.alert-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 0;
  border-bottom: 1px solid var(--color-border);
}
.alert-row:last-of-type {
  border-bottom: none;
}
.alert-name {
  font-weight: 600;
  font-size: 13px;
  white-space: nowrap;
}
.alert-summary {
  color: var(--color-muted-foreground);
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.alert-ok {
  color: var(--color-success);
}
.info-rows {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px 24px;
}
.info-rows > div {
  display: flex;
  gap: 10px;
  font-size: 0.95rem;
}
.info-rows span {
  color: var(--color-muted-foreground);
  min-width: 56px;
}
/* 操作类型分布条形图 */
.action-row {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}
.action-name {
  width: 84px;
  font-size: 0.85rem;
  color: var(--color-muted-foreground);
  white-space: nowrap;
}
.action-track {
  flex: 1;
  height: 10px;
  border-radius: var(--radius-sm);
  background: var(--el-fill-color); /* 与资源容量 .cap-track 同 token（原硬编码 #eef2f6） */
  overflow: hidden;
}
.action-fill {
  height: 100%;
  border-radius: var(--radius-sm);
  transition: width 0.3s ease;
}
.action-count {
  min-width: 44px;
  text-align: right;
  font-family: var(--font-mono);
  font-size: 0.85rem;
  color: var(--color-foreground);
}
/* 告警概览卡：有待处理告警时标题区标红边（对齐监控中心 alert-firing 模式） */
.alert-overview-card.firing :deep(.el-card__header) {
  border-top: 2px solid var(--el-color-danger);
}
.alert-head-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}
</style>
