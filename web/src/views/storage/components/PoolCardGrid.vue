<template>
  <!-- 池卡片网格（替代 el-table，卡片样式对齐 VmList） -->
  <!-- 空态引导：孤岛页此前 0 个出站链接，空态连个下一步都没有 -->
  <el-empty v-if="!pools.length && !loading" description="暂无存储池" :image-size="80">
    <template #description>
      <p>暂无存储池，可到系统设置登记宿主机后再回来查看</p>
    </template>
    <el-button type="primary" size="small" @click="$router.push('/settings')">去系统设置</el-button>
    <el-button size="small" @click="$router.push('/vms')">查看虚拟机</el-button>
  </el-empty>
  <el-row v-else :gutter="16">
    <el-col v-for="pool in pools" :key="pool.name" :xs="24" :sm="12" :md="8" class="pool-col">
      <el-card shadow="hover" class="pool-card" @click="emit('open', pool)">
        <div class="pool-head">
          <span class="pool-name" :title="pool.name">{{ pool.name }}</span>
          <span class="pool-tags">
            <el-tag v-if="!pool.active" type="danger" size="small" effect="light">未激活</el-tag>
            <el-tag v-if="pool.role" :type="roleTagType(pool.role)" size="small" effect="light">{{ pool.role }}</el-tag>
            <el-tag v-else type="info" size="small" effect="plain">未分类</el-tag>
          </span>
        </div>
        <div class="pool-path mono" :title="pool.path">{{ pool.path }}</div>
        <!-- 描述是**只读**展示：此前整行可点去编辑，热区横跨整张卡片，误触率很高
             （想开抽屉却弹出编辑框）。编辑统一走下方「编辑」按钮。
             后端已按池角色补缺省文案，所以这里基本不会是空的；真为空时也不显示
             「点击编辑…」这类诱导文案——那正是误触的来源。 -->
        <div class="pool-desc" :class="{ placeholder: !pool.description }" :title="pool.description">
          {{ pool.description || '—' }}
        </div>
        <!-- 池卡片不再画容量条：dir 池容量是文件系统级的，同盘多池口径相同，
             统一由顶部「物理容量」汇总条展示；卡片只留池自有信息（卷数） -->
        <div class="pool-stats">
          <span>卷数 <b class="mono">{{ pool.vol_count == null ? 0 : pool.vol_count }}</b></span>
        </div>
        <div class="pool-actions" @click.stop>
          <el-button size="small" :icon="FolderOpened" @click="emit('open', pool)">浏览卷</el-button>
          <el-button v-if="isAdmin" size="small" :icon="Edit" @click="emit('edit', pool)">编辑</el-button>
          <el-button v-if="isAdmin" size="small" type="danger" plain :icon="Delete" @click="emit('remove', pool)">删除</el-button>
        </div>
      </el-card>
    </el-col>
  </el-row>
</template>

<script setup>
// 存储池卡片网格：只负责把 pools 铺成响应式卡片 + 空态引导。
// 点击卡片/「浏览卷」→ open；「编辑」→ edit；「删除」→ remove（均由页面执行实际动作）。
// 空态两个跳转按钮用 $router.push（router 已全局安装，与拆分前逐字一致）。
import { FolderOpened, Edit, Delete } from '@element-plus/icons-vue'

defineProps({
  // 存储池列表（每项含 name/path/role/description/active/vol_count）
  pools: { type: Array, default: () => [] },
  // 首次加载中时不渲染空态（避免「暂无存储池」与 loading 骨架同时出现）
  loading: { type: Boolean, default: false },
  // 编辑/删除仅管理员可见
  isAdmin: { type: Boolean, default: false }
})
const emit = defineEmits(['open', 'edit', 'remove'])

// 池角色 → el-tag type（其余/空串走「未分类」灰 tag，不在本表）——自页面原样搬入的纯函数
const ROLE_TAG_TYPES = {
  模板基盘: 'warning',
  系统盘: 'primary',
  数据盘: 'success',
  安装镜像: 'info'
}
function roleTagType(role) {
  return ROLE_TAG_TYPES[role] || 'info'
}
</script>

<style scoped>
/* 以下 .pool-* 规则自 StorageList.vue 原样搬入（类名不变，DOM 不变） */
.pool-col {
  margin-bottom: 16px;
}
.pool-card {
  cursor: pointer;
  height: 100%;
  transition: transform var(--dur-base) var(--ease-standard), border-color var(--dur-base) var(--ease-standard), box-shadow var(--dur-base) var(--ease-standard);
}
.pool-card:hover {
  border-color: var(--el-color-primary);
  transform: translateY(-2px);
  box-shadow: var(--elev-hover);
}
.pool-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.pool-name {
  flex: 1;
  font-size: 1rem;
  font-weight: 700;
  color: var(--color-foreground);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pool-tags {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}
.pool-path {
  font-size: 0.78rem;
  color: var(--color-muted-foreground);
  margin-bottom: 8px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pool-desc {
  font-size: 0.85rem;
  line-height: 1.4;
  color: var(--color-foreground);
  min-height: 40px;
  margin-bottom: 8px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.pool-desc.placeholder {
  color: var(--color-muted-foreground);
  opacity: 0.7;
}
.pool-stats {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 0.85rem;
  color: var(--color-muted-foreground);
  margin-bottom: 12px;
}
.pool-stats b {
  color: var(--color-foreground);
  font-weight: 700;
}
/* 卡片 mini 用量条：贴在统计行与操作区之间，capacity 缺省时不渲染、间距回落原状 */
.pool-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding-top: 12px;
  border-top: 1px solid var(--color-border);
}
/* el-button 相邻默认 margin-left 会与 flex gap 叠加，归零由 gap 控距 */
.pool-actions .el-button + .el-button {
  margin-left: 0;
}
</style>
