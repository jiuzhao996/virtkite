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
</template>

<script setup>
// 虚拟机状态环图卡。原「资源容量（超分视角）」部分已于 2026-10-13 删减批次移除
// （无 VM 时全是 0，信息量为零；组件随之由 VmStatusCapacityCard 更名 VmStatusCard）。
// 纯展示组件：vmStatus 由 shell 统一拉取后经 props 下发。
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { vmStatusText, vmStatusColor, vmStatusHex } from '../../../utils/format'

const props = defineProps({
  vmStatus: { type: Array, required: true } // [{ status, count }]
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
</style>
