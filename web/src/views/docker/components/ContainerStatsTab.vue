<template>
  <!-- 容器「统计」tab：CPU/内存/网络/块 I/O/PIDs 指标块 + CPU/内存曲线容器。
       轮询与 echarts 实例仍在抽屉壳，本组件只渲染并交出两个图表挂载点。 -->
  <div v-if="!running" class="cd-placeholder">容器未运行，无实时统计</div>
  <template v-else>
    <div class="cd-tiles">
      <div class="cd-tile"><span class="cd-tile-k">CPU</span><b class="mono">{{ tiles.cpu }}</b></div>
      <div class="cd-tile"><span class="cd-tile-k">内存</span><b class="mono">{{ tiles.mem }}</b></div>
      <div class="cd-tile"><span class="cd-tile-k">网络 I/O</span><b class="mono">{{ tiles.net }}</b></div>
      <div class="cd-tile"><span class="cd-tile-k">块 I/O</span><b class="mono">{{ tiles.block }}</b></div>
      <div class="cd-tile"><span class="cd-tile-k">PIDs</span><b class="mono">{{ tiles.pids }}</b></div>
    </div>
    <div class="cd-charts">
      <div class="cd-chart-box">
        <div class="cd-chart-title">CPU %</div>
        <div :ref="setChartRef('cpu')" class="cd-chart" />
      </div>
      <div class="cd-chart-box">
        <div class="cd-chart-title">内存 %</div>
        <div :ref="setChartRef('mem')" class="cd-chart" />
      </div>
    </div>
    <div class="cd-hint">滚动窗口约 3 分钟（每 3 秒采样一次）</div>
  </template>
</template>

<script setup>
// 容器统计 tab：指标块 + 两个图表占位；setChartRef 由抽屉壳传入，用于把 DOM 节点
// 绑定到壳内的 useChart 实例（轮询与重绘逻辑不进本组件）。
defineProps({
  running: { type: Boolean, default: false },
  tiles: { type: Object, default: () => ({}) },
  setChartRef: { type: Function, required: true }
})
</script>

<style scoped>
.cd-tiles {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-bottom: 12px;
}
.cd-tile {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 8px 14px;
  border: 1px solid var(--color-border, #e4e7ed);
  border-radius: var(--radius-md, 8px);
  min-width: 110px;
}
.cd-tile-k {
  font-size: 0.75rem;
  color: var(--color-muted-foreground, #909399);
}
.cd-charts {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}
.cd-chart-box {
  border: 1px solid var(--color-border, #e4e7ed);
  border-radius: var(--radius-md, 8px);
  padding: 8px;
}
.cd-chart-title {
  font-size: 0.82rem;
  color: var(--color-muted-foreground, #909399);
  margin-bottom: 4px;
}
.cd-chart {
  height: 180px;
}
.cd-hint {
  margin-top: 8px;
  font-size: 0.78rem;
  color: var(--color-muted-foreground, #909399);
}
.cd-placeholder {
  padding: 40px 0;
  text-align: center;
  color: var(--color-muted-foreground, #909399);
}
</style>
