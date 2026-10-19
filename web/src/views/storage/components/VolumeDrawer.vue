<template>
  <el-drawer v-model="visible" :title="curPool ? curPool + ' · ' + (curRole || '未分类') : '卷管理'" size="72%">
    <!-- 本池血缘图（P4 后新增，克隆家谱同款渲染）：开抽屉即见——池内卷的
         backing 链与挂载关系一图看全；跨池克隆链保留对端（置灰边界节点） -->
    <el-card shadow="never" class="pool-graph-card" v-loading="poolGraphLoading">
      <template #header>
        <div class="pool-graph-head">
          <span class="pool-graph-title">本池血缘图谱<span class="pool-graph-hint">金菱=模板 · 青圆=在用 · 灰圆=孤儿 · 淡圆=跨池链对端 · 点击节点看 tooltip</span></span>
          <el-button text size="small" :icon="Refresh" @click="emit('refresh-graph')">刷新图</el-button>
        </div>
      </template>
      <VolumeLineageGraph :nodes="poolGraphNodes" :edges="poolGraphEdges" height="360px" />
      <div v-if="poolGraphNodes.length" class="pool-graph-stats mono">
        本池 {{ poolInCount }} 卷 ｜ 虚拟 {{ poolVirtualText }} / 实际 {{ poolActualText }}
        <template v-if="poolBoundaryCount"> ｜ 跨池链对端 {{ poolBoundaryCount }}</template>
      </div>
      <el-empty v-if="!poolGraphLoading && !poolGraphNodes.length" description="该池暂无存储卷" :image-size="60" />
    </el-card>
    <div class="toolbar">
      <div>
        <el-button v-if="isAdmin" type="primary" size="small" :icon="Plus" @click="emit('create-vol')">新建卷</el-button>
        <el-button v-if="isAdmin" size="small" :icon="Brush" @click="emit('cleanup-orphans')">清理孤儿卷</el-button>
      </div>
    </div>
    <el-table :data="volumes" stripe border size="small" style="width: 100%" max-height="560" empty-text="该存储池暂无卷">
      <el-table-column prop="name" label="卷名" min-width="150" show-overflow-tooltip />
      <el-table-column label="类型" width="130">
        <template #default="{ row }">
          <!-- 判定优先级：有子卷=模板基盘 > 有 backing=增量系统盘 > .iso=安装镜像；可叠加为 el-tag 组 -->
          <template v-if="volTypeTags(row).length">
            <el-tag
              v-for="t in volTypeTags(row)"
              :key="t.text"
              :type="t.type"
              size="small"
              effect="light"
              class="type-tag"
            >{{ t.text }}</el-tag>
          </template>
          <el-tag v-else type="info" size="small" effect="plain">普通卷</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="容量" width="90">
        <template #default="{ row }">{{ fmtSizeBytes(row.capacity) }}</template>
      </el-table-column>
      <el-table-column label="已用" width="90">
        <template #default="{ row }">{{ fmtSizeBytes(row.allocation) }}</template>
      </el-table-column>
      <el-table-column label="在用" width="170">
        <template #default="{ row }">
          <template v-if="volRefs[row.name] && refsInUse(volRefs[row.name])">
            <el-tooltip placement="top" :content="refsTooltip(volRefs[row.name])">
              <span class="ref-tags">
                <el-tag v-if="volRefs[row.name].vms && volRefs[row.name].vms.length" type="warning" size="small" effect="light">
                  VM×{{ volRefs[row.name].vms.length }}
                </el-tag>
                <el-tag v-if="volRefs[row.name].images && volRefs[row.name].images.length" type="primary" size="small" effect="light">
                  镜像×{{ volRefs[row.name].images.length }}
                </el-tag>
                <el-tag v-if="volRefs[row.name].children && volRefs[row.name].children.length" type="danger" size="small" effect="light">
                  子卷×{{ volRefs[row.name].children.length }}
                </el-tag>
              </span>
            </el-tooltip>
          </template>
          <el-tag v-else type="info" size="small" effect="plain">未使用</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="150" fixed="right">
        <template #default="{ row }">
          <el-button
            v-if="isAdmin && !hasImageRef(row)"
            size="small"
            type="primary"
            plain
            :icon="Collection"
            @click="emit('register', row)"
          >登记为云镜像</el-button>
          <el-button v-if="isAdmin" size="small" type="danger" :icon="Delete" @click="emit('remove', row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
  </el-drawer>
</template>

<script setup>
// 存储池「卷管理」抽屉：本池血缘图谱（VolumeLineageGraph）+ 卷表格（类型/在用/操作）。
// 所有数据（volumes/volRefs/血缘图 nodes/edges/统计）由页面算好传入，本组件只做展示；
// 刷新图 / 新建卷 / 清理孤儿卷 / 登记为云镜像 / 删卷 一律 emit 上抛，实际动作仍在页面执行。
// volTypeTags / refsInUse / refsTooltip / hasImageRef 为纯函数，自页面原样搬入。
import { Refresh, Plus, Brush, Collection, Delete } from '@element-plus/icons-vue'
import { fmtSizeBytes } from '../../../utils/format'
import VolumeLineageGraph from './VolumeLineageGraph.vue'

const props = defineProps({
  // 当前池名 / 角色（抽屉标题与图过滤口径）
  curPool: { type: String, default: '' },
  curRole: { type: String, default: '' },
  // 血缘图数据与统计（页面按当前池过滤后的结果）
  poolGraphLoading: { type: Boolean, default: false },
  poolGraphNodes: { type: Array, default: () => [] },
  poolGraphEdges: { type: Array, default: () => [] },
  poolInCount: { type: Number, default: 0 },
  poolBoundaryCount: { type: Number, default: 0 },
  poolVirtualText: { type: String, default: '—' },
  poolActualText: { type: String, default: '—' },
  // 卷列表与「卷名 → 引用」映射 {vms, images, children}
  volumes: { type: Array, default: () => [] },
  volRefs: { type: Object, default: () => ({}) },
  // 新建卷 / 清理孤儿卷 / 登记 / 删除 仅管理员可用
  isAdmin: { type: Boolean, default: false }
})
const emit = defineEmits(['refresh-graph', 'create-vol', 'cleanup-orphans', 'register', 'remove'])
// 抽屉开关（父级 v-model="volDrawer"）
const visible = defineModel({ type: Boolean, default: false })

function refsInUse(r) {
  return (r.vms && r.vms.length) || (r.images && r.images.length) || (r.children && r.children.length)
}
function refsTooltip(r) {
  const parts = []
  if (r.vms && r.vms.length) parts.push('虚拟机: ' + r.vms.join('、'))
  if (r.images && r.images.length) parts.push('镜像: ' + r.images.join('、'))
  if (r.children && r.children.length) parts.push('增量克隆子卷: ' + r.children.join('、'))
  return parts.join('；')
}

// 卷类型徽标组（按优先级叠加）：有子卷=模板基盘；有 backing 父盘=增量系统盘；.iso=安装镜像
function volTypeTags(row) {
  const tags = []
  const r = props.volRefs[row.name] || {}
  if (r.children && r.children.length) tags.push({ text: '模板基盘', type: 'warning' })
  if (row.backing_file) tags.push({ text: '增量系统盘', type: 'primary' })
  if (/\.iso$/i.test(row.name || '')) tags.push({ text: '安装镜像', type: 'info' })
  return tags
}

// 该卷是否已登记进镜像库（已登记则不显示「登记为云镜像」按钮）
function hasImageRef(row) {
  const r = props.volRefs[row.name]
  return !!(r && r.images && r.images.length)
}
</script>

<style scoped>
/* 以下规则自 StorageList.vue 原样搬入（类名不变，DOM 不变）；
   .toolbar 仍由 global.css 提供（本页未重复定义） */
/* ===== 卷抽屉 ===== */
.type-tag {
  margin-right: 4px;
}
.ref-tags {
  display: inline-flex;
  gap: 4px;
}

/* 本池血缘图卡（volDrawer 顶部） */
.pool-graph-card {
  margin-bottom: 12px;
}
.pool-graph-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.pool-graph-title {
  font-weight: 600;
  font-size: 0.9rem;
}
.pool-graph-hint {
  margin-left: 10px;
  font-weight: 400;
  font-size: 0.72rem;
  color: var(--color-muted-foreground);
}
.pool-graph-stats {
  margin-top: 10px;
  font-size: 0.78rem;
  color: var(--color-muted-foreground);
}
</style>
