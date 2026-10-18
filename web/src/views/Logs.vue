<template>
  <div>
    <PageHead
      title="日志"
      subtitle="平台与宿主机日志检索——数据来自 VictoriaLogs（四类源：平台自身 / 容器 / 宿主系统 / libvirt）。查询用 LogsQL，如 _msg:~&quot;error&quot;（= 全部）。"
    >
      <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
    </PageHead>

    <el-card shadow="never">
      <div class="log-toolbar">
        <el-input
          v-model="query"
          class="log-q"
          placeholder='LogsQL 查询，如 _msg:~"error" 或 up（留空/* = 全部）'
          clearable
          @keyup.enter="load"
        />
        <el-select v-model="source" style="width: 140px" @change="load">
          <el-option label="全部来源" value="all" />
          <el-option label="平台自身" value="platform" />
          <el-option label="容器" value="docker_logs" />
          <el-option label="宿主系统" value="host" />
          <el-option label="libvirt" value="libvirt" />
        </el-select>
        <el-select v-model="limit" style="width: 110px" @change="load">
          <el-option :value="50" label="50 条" />
          <el-option :value="100" label="100 条" />
          <el-option :value="200" label="200 条" />
          <el-option :value="500" label="500 条" />
        </el-select>
        <el-button type="primary" :icon="Search" :loading="loading" @click="load">查询</el-button>
        <el-switch v-model="auto" active-text="自动刷新" />
      </div>

      <div v-if="notice" class="log-notice">{{ notice }}</div>
      <el-table v-else v-loading="loading" :data="rows" size="small" class="log-table">
        <template #empty><EmptyState description="没有匹配的日志" /></template>
        <el-table-column label="时间" width="180">
          <template #default="{ row }">{{ fmtTime(row._time) }}</template>
        </el-table-column>
        <el-table-column label="来源" width="104">
          <template #default="{ row }">
            <el-tag size="small" effect="plain">{{ sourceLabel(row.source_type) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="内容" min-width="600" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="mono">{{ row._msg }}</span>
          </template>
        </el-table-column>
      </el-table>
      <p class="tip">
        共 {{ rows.length }} 条（上限 {{ limit }}）。要更精确可写完整 LogsQL，如
        <span class="mono">_msg:~"error" AND source_type:"host"</span>。
      </p>
    </el-card>
  </div>
</template>

<script setup>
import { ref, watch, onMounted, onUnmounted } from 'vue'
import { Refresh, Search } from '@element-plus/icons-vue'
import { api } from '../api'
import { errMsg, fmtDateTime } from '../utils/format'
import PageHead from '../components/PageHead.vue'
import EmptyState from '../components/EmptyState.vue'

const query = ref('')
const source = ref('all')
const limit = ref(100)
const rows = ref([])
const loading = ref(false)
const notice = ref('') // 非空时用一行提示替代表格（后端未配置 / 不可达）
const auto = ref(false)
let timer = null

// 组合查询：来源下拉拼成 source_type 过滤；关键词留空/* 则不拼 → 整体缺省 *（全部）
function buildQuery() {
  const parts = []
  if (source.value !== 'all') parts.push('source_type:"' + source.value + '"')
  const q = query.value.trim()
  if (q && q !== '*') parts.push(q)
  return parts.join(' ') || '*'
}

async function load() {
  loading.value = true
  notice.value = ''
  try {
    const res = await api.queryLogs(buildQuery(), limit.value)
    const d = res.data || {}
    if (d.source === 'disabled' || d.source === 'unavailable') {
      rows.value = []
      notice.value = d.message || '日志后端不可用'
      return
    }
    rows.value = Array.isArray(d.items) ? d.items : []
  } catch (e) {
    rows.value = []
    notice.value = errMsg(e, '查询日志失败')
  } finally {
    loading.value = false
  }
}

const SOURCE_LABELS = { platform: '平台', host: '宿主', libvirt: 'libvirt', docker_logs: '容器' }
function sourceLabel(s) {
  return SOURCE_LABELS[s] || s || '—'
}
function fmtTime(t) {
  return t ? fmtDateTime(t) : '—'
}

// 自动刷新：5s 一次（仅翻当前查询的前 limit 条，够用于盯实时输出）
watch(auto, (on) => {
  if (timer) {
    clearInterval(timer)
    timer = null
  }
  if (on) timer = setInterval(load, 5000)
})
onMounted(load)
onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<style scoped>
.log-toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.log-q {
  max-width: 420px;
}
.log-notice {
  color: var(--color-muted-foreground);
  text-align: center;
  padding: 28px 0;
}
.log-table .mono {
  font-family: var(--font-mono);
  font-size: 0.82rem;
}
.tip {
  color: var(--color-muted-foreground);
  font-size: 0.8rem;
  margin: 10px 0 0;
}
.tip .mono {
  font-family: var(--font-mono);
}
</style>
