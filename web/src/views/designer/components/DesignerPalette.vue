<template>
  <!-- 左栏：设备栏（拖入建节点）+ 预置架构 + 已保存计划。
       从 DesignerPage.vue 抽出（原文件 996 行，展示层与画布编排逻辑挤在一起）。
       画布操作一律 emit 上抛：本组件不 import X6，拖拽由父组件的 dnd/graph 完成。 -->
  <el-card shadow="never" class="ds-left">
    <div class="ds-h ds-hrow"><span>设备栏</span><span class="ds-hint">拖进画布添加</span></div>
    <template v-for="g in groups" :key="g.kind">
      <div class="ds-palette-title" :style="{ color: g.color }">{{ g.title }}</div>
      <div class="ds-palette">
        <div
          v-for="it in g.items" :key="it.ref"
          class="ds-palette-item" :style="{ borderLeftColor: g.color }"
          :title="it.label + '（按住拖入画布）'"
          @mousedown="$emit('drag-start', $event, g.kind, it)"
        >{{ it.label }}</div>
      </div>
    </template>
    <el-divider />
    <span class="ds-h">预置架构</span>
    <div v-for="t in templates" :key="t.id" class="ds-tpl" @click="$emit('load-template', t)">
      <span class="ds-tpl-name">{{ t.name }}</span>
      <span class="ds-tpl-desc">{{ t.desc }}</span>
    </div>
    <el-divider />
    <span class="ds-h">已保存计划</span>
    <div v-for="p in plans" :key="p.id" class="ds-plan" @click="$emit('load-plan', p)">
      <span class="ds-tpl-name mono">{{ p.id }}</span>
      <el-button text size="small" type="primary" :icon="CopyDocument" title="复制计划" @click.stop="$emit('duplicate-plan', p)" />
      <el-button text size="small" type="danger" :icon="Delete" @click.stop="$emit('remove-plan', p.id)" />
    </div>
  </el-card>
</template>

<script setup>
// 设备栏/模板/计划列表：纯展示 + emit。
// props 全部由父组件（DesignerPage）喂入——数据仍从父组件的接口调用处来，
// 本文件不发请求，便于单独回归「列表渲染与点击分发」。
import { CopyDocument, Delete } from '@element-plus/icons-vue'

defineProps({
  // groups：物料分组 [{kind, title, color, items:[{ref,label}]}]（父组件从栈/镜像/网络接口拼）
  groups: { type: Array, default: () => [] },
  // templates：预置架构列表
  templates: { type: Array, default: () => [] },
  // plans：已保存计划列表
  plans: { type: Array, default: () => [] },
})
defineEmits(['drag-start', 'load-template', 'load-plan', 'duplicate-plan', 'remove-plan'])
</script>

<style scoped>
.ds-h { font-weight: 600; font-size: 0.9rem; }
.ds-hrow { display: flex; align-items: baseline; justify-content: space-between; }
.ds-hint { font-weight: 400; font-size: 0.72rem; color: var(--color-muted-foreground); }
.ds-tpl {
  display: flex; flex-direction: column; gap: 2px;
  padding: 8px 10px; margin-bottom: 6px;
  border: 1px solid var(--color-border); border-radius: var(--radius-sm);
  cursor: pointer; transition: all 0.15s ease;
}
.ds-tpl:hover { border-color: var(--el-color-primary); transform: translateY(-1px); }
.ds-tpl-name { font-weight: 600; font-size: 0.88rem; }
.ds-tpl-desc { font-size: 0.78rem; color: var(--color-muted-foreground); }
.ds-plan {
  display: flex; align-items: center; justify-content: space-between;
  padding: 6px 10px; margin-bottom: 4px;
  border-radius: var(--radius-sm); cursor: pointer;
}
.ds-plan:hover { background: var(--el-fill-color-light); }
.ds-palette-title { font-size: 0.75rem; font-weight: 600; margin: 8px 0 5px; }
.ds-palette { display: grid; grid-template-columns: 1fr 1fr; gap: 5px; }
.ds-palette-item {
  padding: 5px 8px; font-size: 0.78rem;
  border: 1px solid var(--color-border); border-left: 3px solid;
  border-radius: var(--radius-sm); cursor: grab; user-select: none;
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  transition: all 0.15s ease; background: var(--el-bg-color);
}
.ds-palette-item:hover { border-color: var(--el-color-primary); box-shadow: var(--shadow-sm); transform: translateY(-1px); }
</style>
