<template>
  <div v-loading="loading">
    <div class="page-head">
      <div>
        <h2 class="page-title">工具箱</h2>
        <span class="page-desc">面向管理员的宿主机快捷运维工具：查看资源占用最高的进程、掌握磁盘水位，一键清理悬空镜像与过期任务记录</span>
      </div>
    </div>

    <el-alert
      v-if="loadError"
      :title="loadError"
      type="error"
      show-icon
      style="margin-bottom: 16px"
      @close="loadError = ''"
    />

    <!-- 卡一：进程 Top（ps 排序由后端完成，前端只切 sort 参数）；全宽单卡无需 row/col 包裹 -->
    <el-card shadow="never">
      <template #header>
        <div class="card-head">
          <span class="card-title">进程 Top（占用前 20）</span>
          <div class="card-head-right">
            <el-radio-group v-model="procSort" @change="loadProcesses">
              <el-radio-button value="cpu">CPU 占用</el-radio-button>
              <el-radio-button value="mem">内存占用</el-radio-button>
            </el-radio-group>
            <el-button :icon="Refresh" :loading="procLoading" @click="loadProcesses">刷新</el-button>
          </div>
        </div>
      </template>

      <el-table v-loading="procLoading" :data="procs" stripe size="small" style="width: 100%">
        <template #empty>
          <el-empty description="暂无进程数据" :image-size="80" />
        </template>
        <el-table-column prop="pid" label="PID" width="100" />
        <el-table-column prop="user" label="用户" width="130" show-overflow-tooltip />
        <el-table-column label="CPU %" width="110" align="right">
          <template #default="{ row }">
            <span :class="{ 'hot-num': row.cpu >= 50 }">{{ pctText(row.cpu) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="内存 %" width="110" align="right">
          <template #default="{ row }">{{ pctText(row.mem) }}</template>
        </el-table-column>
        <el-table-column prop="comm" label="命令" min-width="240" show-overflow-tooltip>
          <template #default="{ row }"><span class="mono">{{ row.comm }}</span></template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-row :gutter="16" class="tb-row">
      <!-- 卡二：磁盘水位 + 清理动作（独立加载态，不随进程卡联动） -->
      <el-col :xs="24" :md="15">
        <el-card v-loading="diskLoading" shadow="never" class="tb-card">
          <template #header>
            <span class="card-title">磁盘</span>
          </template>
          <template v-if="disk.root">
            <div class="disk-pct-line">
              <span class="disk-pct-label">根分区使用率</span>
              <span class="disk-pct-num" :style="{ color: usageColor(rootPct) }">{{ rootPct }}%</span>
            </div>
            <el-progress
              :percentage="rootPct"
              :color="usageColor(rootPct)"
              :stroke-width="12"
              :format="() => ''"
            />
            <div class="info-rows">
              <div class="info-row"><span class="info-label">总容量</span><span class="mono">{{ disk.root.total || '—' }}</span></div>
              <div class="info-row"><span class="info-label">已用</span><span class="mono">{{ disk.root.used || '—' }}</span></div>
              <div class="info-row"><span class="info-label">可用</span><span class="mono">{{ disk.root.avail || '—' }}</span></div>
              <div class="info-row">
                <span class="info-label">平台数据目录</span>
                <span class="mono">{{ disk.data_dir_size ? disk.data_dir_size + '（' + disk.data_dir + '）' : '—' }}</span>
              </div>
            </div>
          </template>
          <el-empty v-else description="暂无磁盘数据" :image-size="70" />

          <div class="clean-actions">
            <el-button
              type="warning"
              plain
              :icon="MagicStick"
              :loading="pruning"
              @click="pruneDocker"
            >清理悬空镜像</el-button>
            <el-button
              type="danger"
              plain
              :icon="Delete"
              :loading="purging"
              @click="purgeTasks"
            >清理 30 天前任务记录</el-button>
          </div>
        </el-card>
      </el-col>

      <!-- 卡三：定位说明 -->
      <el-col :xs="24" :md="9">
        <el-card shadow="never" class="tb-card">
          <template #header>
            <span class="card-title">关于工具箱</span>
          </template>
          <p class="about-line">
            工具箱是管理员在平台内完成日常宿主机运维的快捷入口，免去逐台登录执行命令。
          </p>
          <ul class="about-list">
            <li>进程 Top 展示宿主机 CPU / 内存占用最高的 20 个进程，辅助定位资源异常；</li>
            <li>清理悬空镜像只删除未被任何镜像层引用的悬空数据，不影响在用镜像与容器；</li>
            <li>任务记录清理物理删除 30 天前的历史任务行，运行中与近期任务不受影响。</li>
          </ul>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Delete, MagicStick } from '@element-plus/icons-vue'
import http from '../api'
import { errMsg, isCancel, usageColor } from '../utils/format'

const loading = ref(false)
const loadError = ref('')

/* ---------- 进程 Top ---------- */
const procs = ref([])
const procLoading = ref(false)
const procSort = ref('cpu')

// ps 的 pcpu/pmem 本身就是一位小数的百分比，这里只做数值兜底与显示统一
function pctText(v) {
  const n = Number(v)
  return (isFinite(n) ? n : 0).toFixed(1) + '%'
}

async function loadProcesses() {
  procLoading.value = true
  try {
    const res = await http.get('/toolbox/processes', { params: { sort: procSort.value } })
    procs.value = (res.data.data && res.data.data.items) || []
    loadError.value = ''
  } catch (e) {
    procs.value = []
    // 失败统一置页顶 alert（errMsg 取后端中文 message），不再逐卡弹 toast
    loadError.value = errMsg(e, '读取进程列表失败')
  } finally {
    procLoading.value = false
  }
}

/* ---------- 磁盘 ---------- */
const disk = ref({ root: null, data_dir: '', data_dir_size: '' })
const diskLoading = ref(false)
const rootPct = computed(() => {
  const n = Number(disk.value.root && disk.value.root.use_pct)
  return isFinite(n) ? Math.max(0, Math.min(100, Math.round(n))) : 0
})

async function loadDisk() {
  diskLoading.value = true
  try {
    const res = await http.get('/toolbox/disk')
    disk.value = res.data.data || { root: null, data_dir: '', data_dir_size: '' }
    loadError.value = ''
  } catch (e) {
    disk.value = { root: null, data_dir: '', data_dir_size: '' }
    loadError.value = errMsg(e, '读取磁盘占用失败')
  } finally {
    diskLoading.value = false
  }
}

/* ---------- 清理动作 ---------- */
const pruning = ref(false)
const purging = ref(false)

// 清理悬空镜像（docker image prune -f）：成功消息里带「释放空间 xxx」，直接透出给用户
async function pruneDocker() {
  try {
    await ElMessageBox.confirm(
      '将执行 docker image prune，仅删除未被任何镜像引用的悬空镜像层，不影响在用镜像与容器。是否继续？',
      '清理悬空镜像',
      {
        confirmButtonText: '开始清理',
        cancelButtonText: '取消',
        type: 'warning',
        confirmButtonClass: 'el-button--danger'
      }
    )
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  pruning.value = true
  try {
    const res = await http.post('/toolbox/docker-prune')
    ElMessage.success((res.data.data && res.data.data.message) || '清理完成')
    loadDisk()
  } catch (e) {
    ElMessage.error(errMsg(e, '清理失败'))
  } finally {
    pruning.value = false
  }
}

// 清理 30 天前的任务记录：结果消息带删除条数
async function purgeTasks() {
  try {
    await ElMessageBox.confirm(
      '将物理删除创建于 30 天前的历史任务记录（含成功与失败记录），此操作不可恢复。是否继续？',
      '清理历史任务记录',
      {
        confirmButtonText: '开始清理',
        cancelButtonText: '取消',
        type: 'warning',
        confirmButtonClass: 'el-button--danger'
      }
    )
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  purging.value = true
  try {
    const res = await http.post('/toolbox/tasks-purge', null, { params: { days: 30 } })
    ElMessage.success((res.data.data && res.data.data.message) || '清理完成')
  } catch (e) {
    ElMessage.error(errMsg(e, '清理失败'))
  } finally {
    purging.value = false
  }
}

onMounted(async () => {
  loading.value = true
  // 两个数据源互不依赖，并行拉取；单边失败各自提示，不拖垮另一边
  await Promise.allSettled([loadProcesses(), loadDisk()])
  loading.value = false
})
</script>

<style scoped>
.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.card-head-right {
  display: flex;
  align-items: center;
  gap: 12px;
}
.tb-row {
  margin-top: 16px;
}
.tb-card {
  height: 100%;
}
.hot-num {
  color: var(--color-danger);
  font-weight: 600;
}
.disk-pct-line {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  margin-bottom: 8px;
}
.disk-pct-label {
  color: var(--color-muted-foreground);
  font-size: 0.9rem;
}
.disk-pct-num {
  font-size: 1.3rem;
  font-weight: 700;
}
.info-rows {
  margin-top: 16px;
}
.info-row {
  display: flex;
  align-items: center;
  padding: 7px 0;
  font-size: 0.9rem;
  border-bottom: 1px dashed var(--color-border);
}
.info-row:last-child {
  border-bottom: none;
}
.info-label {
  width: 110px;
  flex: none;
  color: var(--color-muted-foreground);
}
.clean-actions {
  margin-top: 18px;
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.about-line {
  margin: 0 0 10px;
  font-size: 0.92rem;
  line-height: 1.7;
}
.about-list {
  margin: 0;
  padding-left: 18px;
  color: var(--color-muted-foreground);
  font-size: 0.88rem;
  line-height: 1.9;
}
</style>
