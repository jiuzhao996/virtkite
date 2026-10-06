<template>
  <el-card shadow="never" class="af-card">
    <template #header>
      <div class="af-head">
        <span class="af-title">最近动态</span>
        <el-button text size="small" :icon="Refresh" :loading="loading" @click="load" />
      </div>
    </template>
    <div v-loading="loading" class="af-body">
      <el-empty v-if="!items.length && !loading" description="暂无操作记录" :image-size="60" />
      <el-timeline v-else>
        <el-timeline-item
          v-for="it in items" :key="it.id"
          :type="it.status === 'failed' ? 'danger' : 'primary'"
          :hollow="it.status === 'failed'"
          size="normal"
        >
          <div class="af-row" :class="linkOf(it) ? 'af-clickable' : ''" @click="go(it)">
            <span class="af-who">{{ it.username || '系统' }}</span>
            <span class="af-act">{{ actionText(it.action) }}</span>
            <!-- 路径短显示：活动流靠它判断「跳哪个对象页」 -->
            <span v-if="shortPath(it)" class="af-path mono" :title="shortPath(it)">{{ shortPath(it) }}</span>
            <span class="af-spacer" />
            <el-tag v-if="it.status === 'failed'" size="small" type="danger" effect="light">失败</el-tag>
            <span class="af-time">{{ relTime(it.created_at) }}</span>
          </div>
        </el-timeline-item>
      </el-timeline>
      <div v-if="items.length" class="af-foot">
        <router-link class="af-more" to="/audit">查看完整审计 →</router-link>
      </div>
    </div>
  </el-card>
</template>

<script setup>
// 全站活动流（R6）：最近审计写操作的时间线，整行可点按对象类型跳对应对象页。
// 这是全站第一个「活动流」组件——此前 Dashboard 只有统计卡与图表，没有一个能看出
// 「刚刚发生了什么」的入口，也是「串不起来」体感的来源之一。
import { ref, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { Refresh } from '@element-plus/icons-vue'
import { api } from '../../../api'
import { errMsg, FALLBACK_ACTION_LABELS } from '../../../utils/format'

const props = defineProps({
  // 是否可见（Dashboard 切换 tab 时停止轮询）
  active: { type: Boolean, default: true }
})

const router = useRouter()
const items = ref([])
const loading = ref(false)
let timer = null

function actionText(a) {
  return FALLBACK_ACTION_LABELS[a] || a || '操作'
}

// 取 Detail 里的路径段（审计 Detail 形如 "<path> · 耗时 xx"）
function shortPath(it) {
  const d = String(it.detail || '')
  const seg = d.split(' · ')[0]
  return seg && seg.startsWith('/api/') ? seg : ''
}

// 对象类型（必要时结合路径）→ 目标路由；无映射则整行不可点
function linkOf(it) {
  const t = it.object_type
  const p = shortPath(it)
  if (t === 'vm' && it.object_id) return { path: `/vms/${it.object_id}` }
  if (t === 'vm') return { path: '/vms' }
  if (t === 'docker') return { path: '/containers' }
  if (t === 'image') return { path: '/images' }
  if (t === 'host') return { path: '/hosts' }
  if (t === 'user') return { path: '/users' }
  if (t === 'cron') return { path: '/automation', query: { tab: 'cron' } }
  if (t === 'app') return { path: '/apps' }
  // 栈动作落在 stacks 路径上（object_type 可能是 system）
  if (p.startsWith('/api/stacks')) return { path: '/apps', query: { tab: 'stacks' } }
  if (t === 'task') return { path: '/tasks' }
  if (t === 'system' && p) return { path: '/audit' }
  return null
}

function go(it) {
  const to = linkOf(it)
  if (to) router.push(to)
}

// 相对时间：审计行密集时绝对时间可读性差，「3 分钟前」更易扫
function relTime(v) {
  if (!v) return ''
  const t = new Date(v).getTime()
  if (isNaN(t)) return ''
  const diff = Math.floor((Date.now() - t) / 1000)
  if (diff < 60) return '刚刚'
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`
  if (diff < 86400 * 7) return `${Math.floor(diff / 86400)} 天前`
  const d = new Date(t)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

async function load() {
  loading.value = true
  try {
    const res = await api.dashboardActivity(20)
    items.value = (res.data && res.data.items) || []
  } catch (e) {
    // viewer 无权限（403）是预期内的：由父级 v-if 隐藏，这里不打扰
    items.value = []
  } finally {
    loading.value = false
  }
}

function startTimer() {
  stopTimer()
  timer = setInterval(() => { if (props.active) load() }, 30000)
}
function stopTimer() {
  if (timer) { clearInterval(timer); timer = null }
}

onMounted(() => { load(); startTimer() })
onUnmounted(stopTimer)
defineExpose({ load })
</script>

<style scoped>
.af-card {
  margin-bottom: 16px;
}
.af-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.af-title {
  font-weight: 600;
  font-size: 0.95rem;
}
.af-body {
  min-height: 160px;
  max-height: 320px;
  overflow: auto;
}
.af-row {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  font-size: 0.84rem;
  line-height: 1.5;
}
.af-clickable {
  cursor: pointer;
}
.af-clickable:hover .af-act {
  color: var(--el-color-primary);
  text-decoration: underline;
}
.af-who {
  font-weight: 600;
  color: var(--color-foreground);
}
.af-act {
  color: var(--color-foreground);
}
.af-path {
  font-size: 0.76rem;
  color: var(--el-text-color-secondary, #909399);
  max-width: 260px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.af-spacer { flex: 1; }
.af-time {
  font-size: 0.76rem;
  color: var(--el-text-color-secondary, #909399);
}
.af-foot {
  margin-top: 10px;
  text-align: right;
}
.af-more {
  font-size: 0.8rem;
  color: var(--el-color-primary);
  text-decoration: none;
}
.af-more:hover { text-decoration: underline; }
</style>
