<template>
  <!-- 左侧分区导航：概览/性能/处理器/内存/磁盘/网卡/快照/XML/文件/授权。
       选中项由 activeMenu 受控，点击后仅上抛 index，由详情页壳解析 disk-N / nic-N。 -->
  <el-menu :default-active="activeMenu" @select="emit('select', $event)" class="side-menu">
    <el-menu-item index="overview">
      <el-icon><Odometer /></el-icon>
      <span>概览</span>
    </el-menu-item>
    <el-menu-item index="perf">
      <el-icon><TrendCharts /></el-icon>
      <span>性能</span>
    </el-menu-item>
    <el-menu-item index="cpu">
      <el-icon><Cpu /></el-icon>
      <span>处理器</span>
    </el-menu-item>
    <el-menu-item index="memory">
      <el-icon><Coin /></el-icon>
      <span>内存</span>
    </el-menu-item>
    <el-menu-item-group title="磁盘">
      <el-menu-item v-for="item in diskMenuItems" :key="item.index" :index="item.index">
        <el-icon><FolderOpened /></el-icon>
        <span class="mono">{{ item.target }}</span>
      </el-menu-item>
    </el-menu-item-group>
    <el-menu-item-group title="网卡">
      <el-menu-item v-for="item in nicMenuItems" :key="item.index" :index="item.index">
        <el-icon><Connection /></el-icon>
        <span>{{ item.label }}</span>
      </el-menu-item>
    </el-menu-item-group>
    <el-menu-item index="snapshots">
      <el-icon><CameraFilled /></el-icon>
      <span>快照</span>
    </el-menu-item>
    <el-menu-item index="xml">
      <el-icon><Document /></el-icon>
      <span>XML 定义</span>
    </el-menu-item>
    <el-menu-item v-if="canOperate" index="files">
      <el-icon><FolderOpened /></el-icon>
      <span>文件管理</span>
    </el-menu-item>
    <el-menu-item v-if="isAdmin" index="grants">
      <el-icon><User /></el-icon>
      <span>授权管理</span>
    </el-menu-item>
  </el-menu>
</template>

<script setup>
// 虚拟机详情左侧菜单：磁盘/网卡菜单项由壳按 spec 计算后传入，本组件不读业务数据。
import { Odometer, TrendCharts, Cpu, Coin, FolderOpened, Connection, CameraFilled, Document, User } from '@element-plus/icons-vue'

defineProps({
  activeMenu: { type: String, default: 'overview' },
  diskMenuItems: { type: Array, default: () => [] },
  nicMenuItems: { type: Array, default: () => [] },
  canOperate: { type: Boolean, default: false },
  isAdmin: { type: Boolean, default: false }
})

const emit = defineEmits(['select'])
</script>

<style scoped>
.side-menu {
  border-right: none;
  height: 100%;
  padding: 8px;
}
.side-menu :deep(.el-menu-item) {
  border-radius: var(--radius-sm);
  margin-bottom: 2px;
}
.side-menu :deep(.el-menu-item.is-active) {
  background: var(--el-color-primary-light-9);
  color: var(--color-primary);
  font-weight: 600;
}
</style>
