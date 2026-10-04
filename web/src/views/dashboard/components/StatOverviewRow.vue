<template>
  <el-row :gutter="16">
    <el-col :xs="12" :sm="8" :md="3" v-for="s in stats" :key="s.label">
      <!-- 腾讯云控制台风格：大数字 + 名称 + 整卡可点跳转对应页面（键盘 Enter/空格同样可触发） -->
      <el-card
        shadow="hover"
        class="stat-card clickable"
        role="button"
        tabindex="0"
        @click="router.push(s.to)"
        @keydown.enter.prevent="router.push(s.to)"
        @keydown.space.prevent="router.push(s.to)"
      >
        <el-icon class="stat-icon" :style="{ color: s.color }">
          <component :is="s.icon" />
        </el-icon>
        <el-statistic :value="s.value" :value-style="{ color: 'var(--color-foreground)', fontWeight: 700, fontSize: '1.7rem' }" />
        <div class="stat-label">{{ s.label }}</div>
        <el-icon class="stat-arrow"><ArrowRight /></el-icon>
      </el-card>
    </el-col>
  </el-row>
</template>

<script setup>
// 顶部统计卡行（自 Dashboard.vue 拆出，渲染输出不变）。
// overview 概览数据由 shell loadAll 拉取后经 props 下发；
// adminOnly 卡（用户）过滤与跳转逻辑随组件走。
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowRight, Cpu, Monitor, VideoPlay, FolderOpened, Connection, Picture, User, Document } from '@element-plus/icons-vue'
import { useAuth } from '../../../store/auth'

const props = defineProps({
  overview: { type: Object, default: null } // /dashboard/overview 响应（未拉到时为 null，各卡显示 0）
})

const router = useRouter()
const { isAdmin, canOperate } = useAuth()

const stats = computed(() => {
  const o = props.overview || {}
  // 图标色一律 CSS 变量（硬编码 hex 是审计反模式）；
  // 「用户」卡仅管理员可见（/users 为 admin 专属页）；
  // 「宿主机/审计」卡 operator+（/hosts、/audit 后端均为 OperatorMiddleware 及以上，viewer 点击只会得到 403 页）
  const all = [
    { label: '宿主机', icon: Cpu, color: 'var(--color-primary)', value: o.host_count || 0, to: '/hosts', operateOnly: true },
    { label: '虚拟机', icon: Monitor, color: 'var(--color-primary)', value: o.vm_count || 0, to: '/vms' },
    { label: '运行中', icon: VideoPlay, color: 'var(--color-accent)', value: o.running_vm_count || 0, to: '/vms' },
    { label: '存储池', icon: FolderOpened, color: 'var(--color-warning)', value: o.pool_count || 0, to: '/storage' },
    { label: '网络', icon: Connection, color: 'var(--color-secondary)', value: o.network_count || 0, to: '/networks' },
    { label: '镜像', icon: Picture, color: 'var(--color-violet)', value: o.image_count || 0, to: '/images' },
    { label: '用户', icon: User, color: 'var(--color-info)', value: o.user_count || 0, to: '/users', adminOnly: true },
    { label: '审计', icon: Document, color: 'var(--color-info)', value: o.audit_count || 0, to: '/audit', operateOnly: true }
  ]
  return all.filter(
    (s) => (!s.adminOnly || isAdmin.value) && (!s.operateOnly || canOperate.value)
  )
})
</script>

<style scoped>
/* 统计卡：整体可点，右上角箭头 hover 才浮现（腾讯云控制台风格） */
.stat-card.clickable {
  cursor: pointer;
  position: relative;
  transition: transform 0.15s ease;
}
.stat-card.clickable:hover {
  transform: translateY(-2px);
}
.stat-arrow {
  position: absolute;
  top: 10px;
  right: 10px;
  color: var(--color-muted-foreground);
  opacity: 0;
  transition: opacity 0.15s ease;
}
.stat-card.clickable:hover .stat-arrow {
  opacity: 1;
  color: var(--el-color-primary);
}
.stat-card {
  text-align: center;
  margin-bottom: 16px; /* 窄屏两列/一列堆叠时保持行距（原 4px 挤在一起） */
}
.stat-icon {
  font-size: 1.5rem;
  margin-bottom: 6px;
}
.stat-label {
  font-size: 0.82rem;
  color: var(--color-muted-foreground);
  margin-top: 4px;
}
</style>
