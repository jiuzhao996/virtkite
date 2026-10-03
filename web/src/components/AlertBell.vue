<template>
  <!-- 告警铃铛（告警中心批次 2026-10）：未读站内通知角标 + popover 列表。
       与任务铃（Bell）并排：任务=平台在做什么，告警=环境出了什么事。 -->
  <el-popover trigger="click" width="320" @show="onOpen">
    <template #reference>
      <el-badge
        :value="unread"
        :hidden="!unread"
        :max="99"
        class="alert-bell"
        aria-label="告警通知"
      >
        <el-icon :size="18"><Warning /></el-icon>
      </el-badge>
    </template>

    <div class="alert-pop-head">
      <span>告警通知<span v-if="unread" class="alert-pop-unread">（{{ unread }} 未读）</span></span>
      <el-button v-if="items.length" text size="small" type="primary" @click="markAll">全部已读</el-button>
    </div>

    <div v-if="!items.length" class="alert-pop-empty">暂无告警通知</div>
    <div v-else class="alert-pop-list">
      <!-- 条目点击：带 vm 的告警深链虚拟机详情（故障定位动线），否则只标已读 -->
      <div
        v-for="n in items"
        :key="n.id"
        class="alert-pop-item"
        :class="{ unread: !n.read }"
        @click="openItem(n)"
      >
        <div class="alert-pop-item-top">
          <span class="alert-pop-dot" :class="n.level" />
          <span class="alert-pop-title">{{ n.title }}</span>
          <span class="alert-pop-time">{{ fmtDateTime(n.created_at) }}</span>
        </div>
        <div v-if="n.content" class="alert-pop-content">{{ n.content }}</div>
      </div>
    </div>
    <div v-if="total > pageSize" class="alert-pop-more">
      <el-button text type="primary" size="small" @click="loadMore">加载更多</el-button>
    </div>
  </el-popover>
</template>

<script setup>
// 站内通知轮询：间隔走 utils/settings 的 tasks 档（与任务铃同节奏），未读数驱动红点。
// 告警是低频事件，popover 打开才拉列表，轮询只刷未读数。
import { ref, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Warning } from '@element-plus/icons-vue'
import { api } from '../api'
import { fmtDateTime } from '../utils/format'
import { getPollInterval, POLL_DEFAULTS } from '../utils/settings'

const router = useRouter()

const items = ref([])
const unread = ref(0)
const total = ref(0)
const page = ref(1)
const pageSize = 10

async function refreshUnread() {
  try {
    const res = await api.listNotifications({ page: 1, page_size: 1 })
    unread.value = res.data.unread || 0
  } catch (e) {
    /* 静默：铃铛拉取失败不打扰 */
  }
}

async function loadList() {
  try {
    const res = await api.listNotifications({ page: page.value, page_size: pageSize })
    if (page.value === 1) items.value = res.data.items
    else items.value = items.value.concat(res.data.items)
    total.value = res.data.total
    unread.value = res.data.unread
  } catch (e) {
    /* 静默同上 */
  }
}

function onOpen() {
  page.value = 1
  loadList()
}

function loadMore() {
  page.value++
  loadList()
}

async function markAll() {
  try {
    await api.markAllNotificationsRead()
    await Promise.all([loadList(), refreshUnread()])
  } catch (e) {
    ElMessage.error('标记已读失败')
  }
}

// 单条点击：标已读 + 带 vm 深链详情页（vm_id 为 null 时只标已读）
async function openItem(n) {
  if (!n.read) {
    try { await api.markNotificationRead(n.id) } catch (e) { /* 已读失败不打扰跳转 */ }
    n.read = true
    unread.value = Math.max(0, unread.value - 1)
  }
  if (n.vm_id) router.push('/vms/' + n.vm_id)
}

let timer = null
onMounted(() => {
  refreshUnread()
  timer = setInterval(refreshUnread, getPollInterval('tasks', POLL_DEFAULTS.tasks))
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<style scoped>
/* 与任务铃同款盒模型（26×26 居中）：消除「一大一小」与基线错位 */
.alert-bell {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  cursor: pointer;
}
.alert-pop-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-weight: 600;
  color: var(--color-foreground);
  margin-bottom: 8px;
}
.alert-pop-unread {
  color: var(--el-color-danger);
  font-weight: 600;
}
.alert-pop-empty {
  color: var(--color-muted-foreground);
  font-size: 0.85rem;
  text-align: center;
  padding: 16px 0;
}
.alert-pop-list {
  max-height: 380px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.alert-pop-item {
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  cursor: pointer;
  transition: background 0.15s ease;
}
.alert-pop-item:hover {
  background: var(--el-fill-color-light);
}
.alert-pop-item.unread {
  background: var(--el-color-primary-light-9);
}
.alert-pop-item-top {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.alert-pop-dot {
  flex: none;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--color-info);
}
.alert-pop-dot.critical { background: var(--color-danger); }
.alert-pop-dot.warning { background: var(--color-warning); }
.alert-pop-title {
  font-size: 0.85rem;
  color: var(--color-foreground);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.alert-pop-time {
  margin-left: auto;
  flex: none;
  font-size: 0.72rem;
  color: var(--color-muted-foreground);
}
.alert-pop-content {
  margin-top: 4px;
  font-size: 0.78rem;
  color: var(--color-muted-foreground);
  line-height: 1.5;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.alert-pop-more {
  text-align: center;
  margin-top: 6px;
}
</style>
