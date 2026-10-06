<template>
  <el-card shadow="hover">
    <template #header>
      <div class="alert-card-head">
        <span class="card-title">虚拟机状态</span>
        <el-link type="primary" underline="never" @click="$router.push('/vms')">查看全部</el-link>
      </div>
    </template>
    <div v-if="vmStatus.length === 0" class="empty">暂无数据</div>
    <div v-else class="donut-wrap">
      <div class="donut" :style="{ background: donutStyle }"><span class="donut-center">{{ totalVM }}<small>台</small></span></div>
      <div class="donut-legend">
        <!-- 图例整行可点：跳 VM 列表并按该状态预筛选（环图分段此前 0 可点） -->
        <div
          v-for="item in vmStatus" :key="item.status"
          class="legend-item legend-clickable"
          :title="`查看${vmStatusText(item.status)}的虚拟机`"
          @click="goStatus(item.status)"
        >
          <span class="dot" :style="{ background: vmStatusHex(item.status) }" />
          <span>{{ vmStatusText(item.status) }}</span>
          <b>{{ item.count }}</b>
        </div>
      </div>
    </div>
    <div class="status-rows">
      <div v-for="item in vmStatus" :key="item.status" class="status-row">
        <span class="status-name">{{ vmStatusText(item.status) }}</span>
        <el-progress
          class="status-bar"
          :percentage="pct(item.count)"
          :color="vmStatusColor(item.status)"
          :format="() => item.count + ' 台'"
        />
      </div>
    </div>
  </el-card>

  <!-- 资源容量（超分视角）：已分配 vs 宿主机物理容量。云平台核心指标——
       一台宿主机"装下"了多少申请出来的资源，ratio>1 即超分（KVM 只分配不预留） -->
  <el-card shadow="hover" class="mt">
    <template #header>
      <span class="card-title">资源容量</span>
    </template>
    <div v-if="!capacity.has_host" class="empty">暂无宿主机记录，无法对比物理容量</div>
    <template v-else>
      <div class="cap-row">
        <span class="cap-label">vCPU</span>
        <div class="cap-track"><div class="cap-fill" :class="{ over: capacity.cpu_ratio > 1 }" :style="{ width: capBar(capacity.cpu_ratio) }" /></div>
        <span class="cap-num">{{ capacity.allocated_vcpu }} / {{ capacity.physical_cores }} 核</span>
      </div>
      <div class="cap-row">
        <span class="cap-label">内存</span>
        <div class="cap-track"><div class="cap-fill" :class="{ over: capacity.mem_ratio > 1 }" :style="{ width: capBar(capacity.mem_ratio) }" /></div>
        <span class="cap-num">{{ capGB(capacity.allocated_mem_mb) }} / {{ (capacity.physical_mem_mb / 1024).toFixed(1) }} GB</span>
      </div>
      <div class="cap-ratio">
        <span>超分比</span>
        <b :class="{ over: capacity.cpu_ratio > 1 }">CPU {{ capacity.cpu_ratio }}×</b>
        <b :class="{ over: capacity.mem_ratio > 1 }">内存 {{ capacity.mem_ratio }}×</b>
        <el-tooltip content="KVM 只分配不预留：超分是云平台的常态设计，前提是负载不同时跑满" placement="top">
          <el-icon class="cap-help"><InfoFilled /></el-icon>
        </el-tooltip>
      </div>
    </template>
  </el-card>
</template>

<script setup>
// 虚拟机状态环图 + 资源容量（超分视角）卡（自 Dashboard.vue 拆出，渲染输出不变）。
// 纯展示组件：vmStatus / capacity 由 shell 统一拉取后经 props 下发。
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { InfoFilled } from '@element-plus/icons-vue'
import { vmStatusText, vmStatusColor, vmStatusHex } from '../../../utils/format'

const props = defineProps({
  vmStatus: { type: Array, required: true }, // [{ status, count }]
  capacity: { type: Object, required: true } // { has_host, allocated_vcpu, ... cpu_ratio, mem_ratio }
})

const router = useRouter()

// 状态图例下钻：VmList 读 query.status 预置筛选（此前环图分段完全不可点）
function goStatus(status) {
  router.push({ path: '/vms', query: { status } })
}

const totalVM = computed(() => props.vmStatus.reduce((a, b) => a + b.count, 0))

function pct(count) {
  if (!totalVM.value) return 0
  return Math.round((count / totalVM.value) * 100)
}
const donutStyle = computed(() => {
  if (!totalVM.value) return 'var(--color-muted)' // 空态底色走 token（style 绑定支持 var()）
  let acc = 0
  const segs = props.vmStatus.map((it) => {
    const from = Math.round((acc / totalVM.value) * 360)
    acc += it.count
    const to = Math.round((acc / totalVM.value) * 360)
    return `${vmStatusHex(it.status)} ${from}deg ${to}deg`
  })
  return `conic-gradient(${segs.join(', ')})`
})

// 超分条宽度：以 4× 超分为满格封顶，1×（不超分）= 25%；超分时条变橙
function capBar(ratio) {
  return Math.min(100, (ratio || 0) * 25) + '%'
}
function capGB(mb) {
  return (mb / 1024).toFixed(1) + ' GB'
}
</script>

<style scoped>
.card-title {
  font-weight: 600;
  color: var(--color-foreground);
}
.alert-card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.empty {
  color: var(--color-muted-foreground);
  text-align: center;
  padding: 20px 0;
}
.mt {
  margin-top: 16px;
}

/* 虚拟机状态 */
.donut-wrap {
  display: flex;
  align-items: center;
  gap: 24px;
  padding: 6px 0;
}
.donut {
  position: relative;
  width: 120px;
  height: 120px;
  border-radius: 50%;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}
.donut::before {
  content: '';
  position: absolute;
  width: 72px;
  height: 72px;
  border-radius: 50%;
  background: var(--color-card); /* 环心挖空色随卡片底色（原硬编码 #fff） */
}
.donut-center {
  position: relative;
  font-size: 1.3rem;
  font-weight: 700;
}
.donut-center small {
  font-size: 0.75rem;
  color: var(--color-muted-foreground);
  margin-left: 2px;
}
.donut-legend {
  display: flex;
  flex-direction: column;
  gap: 8px;
  flex: 1;
}
.legend-item {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 0.88rem;
}
.legend-item .dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
}
.legend-item b {
  margin-left: auto;
  font-family: var(--font-mono);
}
/* 图例可点下钻：悬停反馈让「能点」这件事可见 */
.legend-clickable {
  cursor: pointer;
  padding: 2px 4px;
  margin: 0 -4px;
  border-radius: var(--radius-sm, 4px);
  transition: background var(--dur-base) var(--ease-standard);
}
.legend-clickable:hover {
  background: var(--color-muted, #f5f7fa);
}
.legend-clickable:hover span:not(.dot) {
  color: var(--el-color-primary);
}
.status-rows {
  margin-top: 16px;
  border-top: 1px solid var(--color-border);
  padding-top: 12px;
}
.status-row {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 10px;
}
.status-name {
  width: 64px;
  font-size: 0.85rem;
  color: var(--color-muted-foreground);
}
.status-bar {
  flex: 1;
}

/* 资源容量卡（超分视角）：分配/物理 横条，ratio>1 超分变橙 */
.cap-row {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
}
.cap-label {
  width: 40px;
  flex-shrink: 0;
  font-size: 0.85rem;
  color: var(--el-text-color-secondary);
}
.cap-track {
  flex: 1;
  height: 8px;
  background: var(--el-fill-color);
  border-radius: var(--radius-sm);
  overflow: hidden;
}
.cap-fill {
  height: 100%;
  border-radius: var(--radius-sm);
  background: var(--el-color-success);
  transition: width 0.3s;
}
.cap-fill.over {
  background: var(--el-color-warning);
}
.cap-num {
  min-width: 116px;
  text-align: right;
  font-family: var(--font-mono);
  font-size: 0.85rem;
}
.cap-ratio {
  display: flex;
  align-items: center;
  gap: 14px;
  margin-top: 2px;
  font-size: 0.85rem;
  color: var(--el-text-color-secondary);
}
.cap-ratio b {
  font-family: var(--font-mono);
  color: var(--el-color-success);
}
.cap-ratio b.over {
  color: var(--el-color-warning);
}
.cap-help {
  cursor: help;
}
</style>
