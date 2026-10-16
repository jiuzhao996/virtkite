<template>
  <!-- 审计详情抽屉（替代原小弹窗）：行点击 / 详情单元格两处共用 openDetail 入口。
       形态对齐 TaskList 任务详情抽屉：顶部概要大字 + descriptions 结构化字段 + 关联跳转 + 详情原文 -->
  <el-drawer
    v-model="visible"
    :title="current ? '审计详情 #' + current.id : '审计详情'"
    size="480px"
    append-to-body
    destroy-on-close
  >
    <template v-if="current">
      <!-- 顶部概要：动作中文大字 + 状态大 tag + 原始动作串与时间（mono 等宽便于对齐追溯） -->
      <div class="dt-head">
        <div class="dt-title-row">
          <span class="dt-title">{{ actionLabel(current.action) }}</span>
          <el-tag :type="current.status === 'success' ? 'success' : 'danger'" effect="light" size="large">
            {{ current.status === 'success' ? '成功' : '失败' }}
          </el-tag>
        </div>
        <div class="dt-meta mono">{{ current.action }} · {{ fmtDateTime(current.created_at) }}</div>
      </div>

      <!-- 结构化字段：column=2 border small；耗时从 detail 文本里正则抽取（后端中间件固定写「请求处理时间: X」） -->
      <el-descriptions :column="2" border size="small" class="dt-desc">
        <el-descriptions-item label="操作人">{{ current.username || '—' }}</el-descriptions-item>
        <el-descriptions-item label="对象类型">{{ objectTypeLabel(current.object_type) }}</el-descriptions-item>
        <el-descriptions-item label="对象ID">
          <span class="mono">{{ current.object_id != null ? '#' + current.object_id : '—' }}</span>
        </el-descriptions-item>
        <el-descriptions-item label="来源 IP">
          <span class="mono">{{ current.source_ip || '—' }}</span>
        </el-descriptions-item>
        <el-descriptions-item label="请求处理耗时" :span="2">
          <span class="mono">{{ parseDuration(current.detail) }}</span>
        </el-descriptions-item>
      </el-descriptions>

      <!-- 关联跳转（包C）：vm 直达虚拟机详情、user 直达用户列表，其余对象无详情落点不渲染 -->
      <template v-if="relatedLink">
        <h4 class="detail-sec">关联跳转</h4>
        <el-link type="primary" :underline="false" @click="goRelated">{{ relatedLink.text }}</el-link>
      </template>

      <!-- 变更内容（前后快照）：只有显式拍过快照的写操作才有值（改规格/授权等）。
           按**字段级 diff** 展示而不是把两坨 JSON 并排贴——看的人要的是
           「哪个字段从什么变成了什么」，并排两段 JSON 得自己找。 -->
      <template v-if="changeRows.length">
        <h4 class="detail-sec">变更内容</h4>
        <el-table :data="changeRows" size="small" border class="dt-diff">
          <el-table-column prop="label" label="字段" width="140" />
          <el-table-column label="变更前">
            <template #default="{ row }"><span class="mono dt-before">{{ row.before }}</span></template>
          </el-table-column>
          <el-table-column label="变更后">
            <template #default="{ row }"><span class="mono dt-after">{{ row.after }}</span></template>
          </el-table-column>
        </el-table>
      </template>

      <h4 class="detail-sec">详情原文</h4>
      <pre class="detail-text">{{ current.detail || '—' }}</pre>
    </template>
    <template #footer>
      <el-button @click="visible = false">关闭</el-button>
    </template>
  </el-drawer>
</template>

<script setup>
// 审计详情抽屉：展示单条审计记录（概要 + 结构化字段 + 关联跳转 + 变更内容字段级 diff + 详情原文）。
// current（行数据）为 props；变更 diff / 关联链接 / 字段中文名 / 耗时抽取等**派生逻辑**随本组件下沉，
// 行为与拆分前逐字一致。actionMap 由页面传入（动作→中文随 api.auditActions 更新）。
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { fmtDateTime } from '../../../utils/format'

const props = defineProps({
  // 当前审计行（为 null 时不渲染内容，与拆前一致）
  current: { type: Object, default: null },
  // 动作 → 中文映射（页面 actionMap）
  actionMap: { type: Object, default: () => ({}) }
})
// 抽屉开关（父级 v-model="drawerOpen"）
const visible = defineModel({ type: Boolean, default: false })

const router = useRouter()

// ⚠️ 与 AuditOpsTable.vue / AuditList.vue 的动作中文口径保持一致——改一处须同步
function actionLabel(action) {
  if (!action) return '—'
  return props.actionMap[action] || action
}

// 前后快照的字段中文名：审计是给人看的，字段名保持后端原样（vcpu/memory_mb）读起来费劲。
const DIFF_FIELD_LABELS = {
  vcpu: 'vCPU 核数',
  memory_mb: '内存 (MB)',
  disk_gb: '磁盘 (GB)',
  vm: '虚拟机',
  user: '用户',
  granted: '已授权',
  expires_at: '有效期至',
  status: '状态',
  name: '名称',
  ip: 'IP',
  autostart: '开机自启'
}

function diffVal(v) {
  if (v === undefined) return '—'
  if (v === null) return 'null'
  if (typeof v === 'object') return JSON.stringify(v)
  return String(v)
}

// 前后快照 → 字段级 diff。取两边键的并集，**只列真正变了的字段**（没变的占版面没意义）。
// 比较前统一字符串化：布尔/数字/字符串混排时直接比会误判。
// JSON 解析失败按空对象处理（后端字段是 text，历史行可能为空或非 JSON），不抛错。
const changeRows = computed(() => {
  const c = props.current
  if (!c || (!c.before_data && !c.after_data)) return []
  const parse = (raw) => {
    if (!raw) return {}
    try {
      const v = JSON.parse(raw)
      return v && typeof v === 'object' ? v : {}
    } catch (e) {
      return {}
    }
  }
  const b = parse(c.before_data)
  const a = parse(c.after_data)
  const keys = [...new Set([...Object.keys(b), ...Object.keys(a)])]
  return keys
    .filter((k) => diffVal(b[k]) !== diffVal(a[k]))
    .map((k) => ({
      field: k,
      label: DIFF_FIELD_LABELS[k] || k,
      before: diffVal(b[k]),
      after: diffVal(a[k])
    }))
})

// 对象类型中文映射：取值集合核实自 middleware/audit.go determineObjectType（vm/host/image/user/docker/app/cron/system）
// + 任务指定的 storage/network/task 预留位；未知类型显原文不臆造
const OBJECT_TYPE_LABELS = {
  vm: '虚拟机', user: '用户', image: '镜像', storage: '存储池', network: '网络',
  task: '任务', system: '系统', host: '宿主机', docker: 'Docker', app: '应用', cron: '计划任务'
}
function objectTypeLabel(t) {
  if (!t) return '—'
  return OBJECT_TYPE_LABELS[t] || t
}

// 请求处理耗时：中间件 detail 固定为「请求处理时间: 58.79ms」（Go time.Duration.String()，
// 可能是 ms/µs/s/m 复合形），抽取首个空白前的时长串；抽不到（如 jumpd 的 detail）显「—」
function parseDuration(detail) {
  if (!detail) return '—'
  const m = detail.match(/请求处理时间[:：]\s*([^\s，,]+)/)
  return m ? m[1] : '—'
}

// 关联跳转（包C）：vm → 虚拟机详情、user → 用户列表；对象ID 为空或其余类型不给链接。
// R6 扩展：此前只有 vm / user 两类能跳，docker/image/cron/task/app/stack 全是不可点的
// 死行——审计是天然的「跨对象索引」，这里补齐就是织网。
const relatedLink = computed(() => {
  if (!props.current) return null
  const t = props.current.object_type
  const id = props.current.object_id
  // 路径兜底：栈动作的 object_type 是 system，只有路径能区分
  const p = String(props.current.detail || '').split(' · ')[0]
  if (t === 'vm' && id != null) return { text: '查看虚拟机 →', to: '/vms/' + id }
  if (t === 'vm') return { text: '查看虚拟机列表 →', to: '/vms' }
  if (t === 'user') return { text: '查看用户列表 →', to: '/users' }
  if (t === 'docker') return { text: '查看容器 →', to: '/containers' }
  if (t === 'image') return { text: '查看镜像 →', to: '/images' }
  if (t === 'cron') return { text: '查看计划任务 →', to: '/automation?tab=cron' }
  if (t === 'app') return { text: '查看应用商店 →', to: '/apps' }
  if (t === 'task') return { text: '查看任务中心 →', to: '/tasks' }
  if (t === 'host') return { text: '查看宿主机 →', to: '/hosts' }
  if (p.startsWith('/api/stacks')) return { text: '查看部署栈 →', to: '/apps?tab=stacks' }
  if (p.startsWith('/api/networks')) return { text: '查看网络 →', to: '/networks' }
  if (p.startsWith('/api/storage')) return { text: '查看存储 →', to: '/storage' }
  return null
})

function goRelated() {
  if (!relatedLink.value) return
  visible.value = false
  router.push(relatedLink.value.to)
}
</script>

<style scoped>
/* 以下规则自 AuditList.vue 原样搬入（类名不变，DOM 不变）；.mono 字号由本组件自持 */
.mono {
  font-size: 0.85rem;
}
/* ===== 详情抽屉（形态对齐 TaskList 任务详情抽屉） ===== */
.dt-head {
  padding: 4px 0;
}
.dt-title-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}
.dt-title {
  font-size: 16px;
  font-weight: 600;
  word-break: break-all;
}
.dt-meta {
  font-size: 0.82rem;
  color: var(--color-muted-foreground);
}
.dt-desc {
  margin-bottom: 4px;
}
.detail-sec {
  margin: 16px 0 8px;
  font-size: 14px;
}
.detail-text {
  margin: 0;
  padding: var(--space-lg);
  background: var(--color-muted);
  border-radius: var(--radius-sm);
  font-family: var(--font-mono);
  font-size: 0.82rem;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 320px;
  overflow: auto;
}
.dt-diff {
  margin-bottom: 4px;
}
.dt-before {
  color: var(--color-muted-foreground);
  text-decoration: line-through;
}
.dt-after {
  color: var(--el-color-primary);
  font-weight: 600;
}
</style>
