<template>
  <div>
    <!-- 独立路由使用时显示页头；被仪表盘以 tab 嵌入时隐藏（标题与刷新归属外层），
         刷新按钮移到「实时告警」卡头部，两种形态都可用 -->
    <div v-if="!embedded" class="page-head">
      <div>
        <h3 class="page-title">监控中心</h3>
        <p class="page-desc">Alertmanager 实时告警 + Grafana 可视化看板（Prometheus 指标 30s 刷新）</p>
      </div>
      <el-button :icon="Refresh" @click="loadAlerts">刷新告警</el-button>
    </div>

    <!-- Grafana 看板（kiosk 模式嵌入；地址跟随当前访问主机，兼容宿主机直跑与 docker compose 两种形态）。
         看板是本页视觉主体放首屏，告警作为状态摘要放下方（有告警时卡片标红提示）。 -->
    <el-card shadow="never" class="grafana-card">
      <template #header>
        <div class="alert-head">
          <el-radio-group v-model="board">
            <el-radio-button value="overview">宿主机</el-radio-button>
            <el-radio-button value="vms">虚拟机</el-radio-button>
          </el-radio-group>
          <el-link type="primary" :href="grafanaFull" target="_blank">在新窗口打开 Grafana</el-link>
        </div>
      </template>
      <div v-if="grafanaError" class="grafana-fallback">
        <el-empty description="Grafana 看板加载失败（需在 docker compose 中启动 grafana 服务并映射 3000 端口）" :image-size="72" />
      </div>
      <div v-else class="grafana-wrap">
        <!-- iframe 首次加载要拉完整 Grafana 前端（公网 gzip 后约 3MB，走云 nginx → frp 隧道），给个占位避免白/黑屏无反馈 -->
        <div v-if="frameStuck[board]" class="grafana-loading">
          <el-icon><WarningFilled /></el-icon>
          <span>Grafana 未连接：请确认监控栈已启动（docker compose up -d），或稍后点「重试」</span>
          <el-button type="primary" :icon="Refresh" @click="retryFrame">重试</el-button>
        </div>
        <div v-else-if="!frameReady[board]" class="grafana-loading">
          <el-icon class="is-loading"><Loading /></el-icon>
          <span>看板加载中（首次约 5-10 秒）…</span>
        </div>
        <!-- 窗口过窄时 Grafana 网格重排、内容变高，静态 iframe 高度会重新出现底部截断，提示改用新窗口 -->
        <div v-if="narrowWindow" class="grafana-loading narrow-hint">
          <el-icon><InfoFilled /></el-icon>
          <span>当前窗口过窄，看板可能显示不全——建议点击右上角「在新窗口打开 Grafana」查看完整看板</span>
        </div>
        <!-- 两个看板 iframe 常驻：首次激活时才挂载（v-if 过 mountedBoards），之后只 v-show 切换显隐、绝不销毁。
             原实现 :key="board" 每次切换都重建 iframe → 每次都重拉一遍约 3MB 的 Grafana 前端 JS；
             常驻后每个看板只承受一次首载成本，切换瞬时完成。两个 uid 已含文件名哈希的静态资源有 1 年强缓存，
             重复加载纯属浪费带宽。 -->
        <template v-for="b in ['overview', 'vms']" :key="b">
          <iframe
            v-if="mountedBoards.has(b)"
            v-show="board === b"
            :src="boardEmbed(b)"
            class="grafana-frame"
            :style="{ height: boardHeight(b) + 'px' }"
            frameborder="0"
            @load="onFrameLoad(b)"
          ></iframe>
        </template>
      </div>
    </el-card>

    <!-- 告警列表 -->
    <el-card shadow="never" class="alert-card" :class="{ 'alert-firing': firingCount }">
      <template #header>
        <div class="alert-head">
          <span>实时告警</span>
          <div class="alert-head-actions">
            <el-tag v-if="!alertsError" :type="firingCount ? 'danger' : 'success'" effect="light" size="small">
              {{ firingCount ? `${firingCount} 条待处理` : '当前无告警' }}
            </el-tag>
            <el-button :icon="Refresh" @click="loadAlerts">刷新</el-button>
          </div>
        </div>
      </template>

      <el-alert
        v-if="alertsError"
        type="warning"
        :closable="false"
        show-icon
        title="监控栈未连接"
        description="无法访问 Alertmanager（典型原因：docker compose 未启动监控栈）。执行 docker compose up -d 启动 Prometheus / Alertmanager / Grafana 后刷新本页。"
      />

      <el-table v-else-if="firingCount" :data="alerts" v-loading="alertsLoading" stripe>
        <el-table-column label="级别" width="90">
          <template #default="{ row }">
            <el-tag :type="row.labels && row.labels.severity === 'critical' ? 'danger' : 'warning'" effect="dark" size="small">
              {{ (row.labels && row.labels.severity) === 'critical' ? '严重' : '警告' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="告警名称" width="170">
          <template #default="{ row }">
            <span class="mono">{{ row.labels && row.labels.alertname }}</span>
          </template>
        </el-table-column>
        <el-table-column label="摘要" min-width="240">
          <template #default="{ row }">{{ (row.annotations && row.annotations.summary) || '—' }}</template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.status && row.status.state === 'active' ? 'danger' : 'info'" effect="plain" size="small">
              {{ row.status && row.status.state === 'active' ? '触发中' : '已抑制' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="触发时间" width="180">
          <template #default="{ row }">
            <span class="mono">{{ row.startsAt ? fmtDateTime(row.startsAt) : '—' }}</span>
          </template>
        </el-table-column>
      </el-table>

      <el-empty v-else description="当前无告警，一切正常" :image-size="60" />
    </el-card>

    <!-- 告警历史（Alertmanager webhook 推回平台入库的追溯数据，与上方实时列表互补） -->
    <el-card shadow="never" class="alert-card">
      <template #header>
        <div class="alert-head">
          <span><el-icon class="head-icon"><AlarmClock /></el-icon>告警历史</span>
          <div class="alert-head-actions">
            <el-select v-model="historyStatus" style="width: 130px" @change="onHistoryFilter">
              <el-option label="全部状态" value="" />
              <el-option label="触发中" value="firing" />
              <el-option label="已恢复" value="resolved" />
            </el-select>
            <el-button :icon="Refresh" @click="loadHistory">刷新</el-button>
          </div>
        </div>
      </template>

      <el-table :data="history" v-loading="historyLoading" stripe>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.status === 'firing' ? 'danger' : 'success'" effect="light" size="small">
              {{ row.status === 'firing' ? '触发中' : '已恢复' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="告警名称" width="180">
          <template #default="{ row }">
            <span class="mono">{{ row.labels && row.labels.alertname || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="级别" width="90">
          <template #default="{ row }">
            <el-tag
              :type="(row.labels && row.labels.severity) === 'critical' ? 'danger' : (row.labels && row.labels.severity) === 'warning' ? 'warning' : 'info'"
              effect="dark" size="small"
            >
              {{ (row.labels && row.labels.severity) === 'critical' ? '严重' : (row.labels && row.labels.severity) === 'warning' ? '警告' : (row.labels && row.labels.severity) || '—' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="实例" width="150">
          <template #default="{ row }">
            <span class="mono">{{ (row.labels && row.labels.instance) || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="开始时间" width="170">
          <template #default="{ row }">
            <span class="mono">{{ row.starts_at ? fmtDateTime(row.starts_at) : '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="最近更新" width="170">
          <template #default="{ row }">
            <span class="mono">{{ row.updated_at ? fmtDateTime(row.updated_at) : '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="摘要" min-width="220" show-overflow-tooltip>
          <template #default="{ row }">
            {{ (row.annotations && (row.annotations.summary || row.annotations.description)) || '—' }}
          </template>
        </el-table-column>
      </el-table>

      <div class="history-pager">
        <el-pagination
          v-model:current-page="historyPage"
          :page-size="historyPageSize"
          :total="historyTotal"
          layout="total, prev, pager, next"
          small
          background
          @current-change="loadHistory"
        />
      </div>
    </el-card>

    <!-- node_exporter 抓取目标预览（监控服务发现闭环）：
         平台把「运行中且已获取 IP」的 VM 自动下发为 Prometheus file_sd 抓取目标，
         与后端 StartFileSDWriter 落盘内容同源，此处只读预览（变化慢，手动刷新即可）。 -->
    <el-card shadow="never" class="alert-card">
      <template #header>
        <div class="alert-head">
          <span><el-icon class="head-icon"><Aim /></el-icon>node_exporter 抓取目标</span>
          <div class="alert-head-actions">
            <el-tag size="small" :type="sdEnabled ? 'success' : 'warning'" effect="plain">
              {{ sdEnabled ? '服务发现已启用' : 'FILE_SD_PATH 未配置' }}
            </el-tag>
            <el-tag size="small" :type="sdTargets.length ? 'primary' : 'info'" effect="plain">
              {{ sdTargets.length }} 个目标
            </el-tag>
            <el-button :icon="Refresh" @click="loadFileSD">刷新</el-button>
          </div>
        </div>
      </template>

      <el-empty
        v-if="!sdTargets.length"
        :description="sdEnabled ? '暂无抓取目标（仅运行中且已获取 IP 的虚拟机会被纳入）' : 'FILE_SD_PATH 环境变量未配置：预览可用，但平台不会把目标写入文件，Prometheus 侧需手工维护。配置后重启后端即启用。'"
        :image-size="60"
      />
      <!-- 有满足条件的目标但 file_sd 未启用时：表格上方说明「仅预览、Prometheus 暂不抓取」，
           消除「FILE_SD_PATH 未配置」标签与「N 个目标」并存带来的矛盾感 -->
      <template v-else>
        <el-alert
          v-if="!sdEnabled"
          class="sd-alert"
          type="info"
          :closable="false"
          show-icon
          :title="`以下 ${sdTargets.length} 台虚拟机满足自动监控条件（运行中且已获取 IP）`"
          description="当前 FILE_SD_PATH 未配置，此列表仅为预览，Prometheus 暂不会抓取。在后端环境变量中配置 FILE_SD_PATH 并重启后即自动接入。"
        />
        <el-table :data="sdTargets" v-loading="sdLoading" stripe>
          <el-table-column label="抓取端点" width="200">
            <template #default="{ row }">
              <span class="mono">{{ (row.targets && row.targets[0]) || '—' }}</span>
            </template>
          </el-table-column>
          <el-table-column label="虚拟机" min-width="160">
            <template #default="{ row }">{{ (row.labels && row.labels.vm_name) || '—' }}</template>
          </el-table-column>
          <el-table-column label="VM ID" width="110">
            <template #default="{ row }">
              <span class="mono">{{ (row.labels && row.labels.vm_id) || '—' }}</span>
            </template>
          </el-table-column>
          <el-table-column label="job" width="130">
            <template #default="{ row }">
              <span class="mono">{{ (row.labels && row.labels.job) || '—' }}</span>
            </template>
          </el-table-column>
        </el-table>
      </template>

      <!-- 底部说明两态：启用时保留完整说明（含 FILE_SD_PATH 写入逻辑）；
           未启用时换精简版——FILE_SD_PATH 的解释已由上方 alert 承载，不再重复 -->
      <p v-if="sdEnabled" class="sd-note">
        目标由监控服务发现（file_sd）自动生成，同 IP 去重；VM 内需安装 node_exporter（端口 9100）。
        设置 FILE_SD_PATH 环境变量后，平台会把本列表自动写入该路径供 Prometheus 读取；
        平台自身 exporter 走 prometheus.yml 静态抓取，不在此列。
      </p>
      <p v-else class="sd-note">
        目标由监控服务发现自动生成（同 IP 去重）；虚拟机内需安装并运行 node_exporter（端口 9100）才会产生监控数据。
      </p>
    </el-card>
  </div>
</template>

<script setup>
// embedded：被仪表盘 tab 嵌入时隐藏独立页头
defineProps({ embedded: { type: Boolean, default: false } })
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { Aim, AlarmClock, InfoFilled, Refresh, Loading, WarningFilled } from '@element-plus/icons-vue'
import { api } from '../api'
import { fmtDateTime } from '../utils/format'
import { getPollInterval, POLL_DEFAULTS } from '../utils/settings'

const alerts = ref([])
const alertsLoading = ref(false)
const alertsError = ref(false)
const grafanaError = ref(false)

// 只统计需要处理的告警（active = 触发中）
const firingCount = computed(
  () => alerts.value.filter((a) => a.status && a.status.state === 'active').length
)

// Grafana 看板 uid 与 deploy/grafana-dashboard.json 一致；kiosk 模式隐藏侧栏只留面板
// HTTPS（公网 kpyun.fun）走云端 nginx 同源反代 /grafana/（https 页面嵌 http iframe 会被混合内容拦截）；
// 本地 http 保持直连 3000
const isHttps = window.location.protocol === 'https:'
const grafanaBase = isHttps ? `${window.location.origin}/grafana` : `http://${window.location.hostname}:3000`
// 看板 uid 对应 deploy/ 下两个 provisioned 看板
const board = ref('overview')
// 各看板 iframe 首载完成标记（key = 看板名），加载占位按当前激活看板判断
const frameReady = reactive({ overview: false, vms: false })
// Grafana 未连接判定：后端探活 /monitor/grafana-status（主判定，进入页面即查）+
// iframe 挂载 15s 未 load 兜底（防挂起）。跨端口读不到 iframe 内部，onerror 不触发，
// 且容器未启动时浏览器错误页同样触发 load——纯前端手段判不了白屏，必须后端代探。
const frameStuck = reactive({ overview: false, vms: false })
const frameTimers = {}
function armStuckTimer(b) {
  clearTimeout(frameTimers[b])
  frameStuck[b] = false
  frameTimers[b] = setTimeout(() => {
    if (!frameReady[b]) frameStuck[b] = true
  }, 15000)
}
// 进入页面即探活：Grafana 没起时立刻亮「未连接」提示层，不等 15s 超时。
// 探活失败置位所有看板（未连接与看板无关）；探活成功则清除，交给超时兜底慢加载场景。
async function probeGrafana() {
  try {
    const res = await api.monitorGrafanaStatus()
    const ok = res.data && res.data.ok === true
    frameStuck.overview = frameStuck.vms = !ok
    if (ok) clearTimeout(frameTimers.overview)
  } catch (e) {
    frameStuck.overview = frameStuck.vms = true
  }
}
function onFrameLoad(b) {
  frameReady[b] = true
  frameStuck[b] = false
  clearTimeout(frameTimers[b])
}
// Grafana 未启动时的重试：清掉就绪/卡住标记并重新挂载当前看板的 iframe
function retryFrame() {
  const b = board.value
  frameReady[b] = false
  frameStuck[b] = false
  mountedBoards.delete(b)
  nextTick(() => {
    mountedBoards.add(b)
    armStuckTimer(b)
  })
}
// 窗口过窄时 Grafana 网格会重排、内容变高，静态 iframe 高度会重新出现底部截断——
// 提示改用新窗口打开（缩放/改高都治标），布局恢复后再给出正常展示
const narrowWindow = ref(window.innerWidth < 1100)
function onResize() {
  narrowWindow.value = window.innerWidth < 1100
}
// 已挂载过的看板集合：首次激活才创建 iframe（避免进页面就并发拉两份 Grafana 前端抢带宽），
// 挂载后常驻，切换只走 v-show 显隐
const mountedBoards = reactive(new Set(['overview']))
watch(board, (b) => { mountedBoards.add(b); armStuckTimer(b) })
function boardUid(b) {
  return b === 'vms' ? 'vmops-vms' : 'vmops-overview'
}
// 看板路径：HTTPS 的 grafanaBase 已含 /grafana（nginx 同源反代），本地直连 3000 必须补 /grafana 前缀——
// compose 设了 GF_SERVER_ROOT_URL=https://kpyun.fun/grafana/，不带前缀的请求会被 301 到公网域名，
// 本地访问会绕公网一圈（serve_from_sub_path 开启时带前缀的路径原地 200）。⚠️ 两分支只取其一，重复拼接 = 404。
function boardPath(b) {
  return `${grafanaBase}${isHttps ? '' : '/grafana'}/d/${boardUid(b)}/`
}
function boardEmbed(b) {
  return `${boardPath(b)}?kiosk=1&refresh=30s`
}
// 看板高度：按 1310px 宽 + kiosk=1 下实测内容底边定（宿主机看板 ≈730px、虚拟机看板 6 图三行 ≈855px）。
// 看板已按「宿主机/虚拟机」分组去重、图占整行分行排，高度可控；改 JSON 布局后须重新实测并同步此处。
function boardHeight(b) {
  return b === 'vms' ? 875 : 750
}
const grafanaFull = computed(() => boardPath(board.value))

async function loadAlerts() {
  if (alertsLoading.value) return
  alertsLoading.value = true
  try {
    const res = await api.listAlerts()
    alerts.value = Array.isArray(res.data) ? res.data : []
    alertsError.value = false
  } catch (e) {
    // 静默置错误态：页面内提示如何启动监控栈，不弹全局 toast
    alertsError.value = true
  } finally {
    alertsLoading.value = false
  }
}

// 告警历史（webhook 入库数据）：筛选 + 分页，不随实时告警轮询（历史变化慢，手动刷新即可）
const history = ref([])
const historyLoading = ref(false)
const historyTotal = ref(0)
const historyPage = ref(1)
const historyPageSize = 20
const historyStatus = ref('')

async function loadHistory() {
  if (historyLoading.value) return
  historyLoading.value = true
  try {
    const res = await api.monitorAlertHistory({
      status: historyStatus.value || undefined,
      page: historyPage.value,
      page_size: historyPageSize
    })
    const data = res.data || {}
    history.value = Array.isArray(data.items) ? data.items : []
    historyTotal.value = data.total || 0
  } catch (e) {
    // 历史查询失败不打断页面（实时告警仍在上方正常展示）
    history.value = []
    historyTotal.value = 0
  } finally {
    historyLoading.value = false
  }
}

// 切换状态筛选后回到第一页再查
function onHistoryFilter() {
  historyPage.value = 1
  loadHistory()
}

// node_exporter 抓取目标预览（file_sd）：实时查库计算，与落盘文件同源；
// 变化慢不进轮询，手动刷新即可。失败静默（监控栈未起/无权限时不打断页面其它区块）
const sdTargets = ref([])
const sdEnabled = ref(false)
const sdLoading = ref(false)

async function loadFileSD() {
  if (sdLoading.value) return
  sdLoading.value = true
  try {
    const res = await api.monitorFileSD()
    const data = res.data || {}
    // 兼容旧响应（纯数组）：无 enabled 字段时按「状态未知」处理不误导
    sdTargets.value = Array.isArray(data) ? data : (Array.isArray(data.items) ? data.items : [])
    sdEnabled.value = data.enabled === true
  } catch (e) {
    sdTargets.value = []
    sdEnabled.value = false
  } finally {
    sdLoading.value = false
  }
}

let timer = null
onMounted(() => {
  loadAlerts()
  loadHistory()
  loadFileSD()
  probeGrafana()
  armStuckTimer('overview')
  window.addEventListener('resize', onResize)
  timer = setInterval(loadAlerts, getPollInterval('dashboard', POLL_DEFAULTS.dashboard))
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
  window.removeEventListener('resize', onResize)
  Object.values(frameTimers).forEach(clearTimeout)
})
</script>

<style scoped>
.alert-card {
  margin-bottom: 16px;
}
/* 有待处理告警时卡片标红边，滚到看板下方也不会漏看 */
.alert-firing :deep(.el-card__header) {
  border-top: 2px solid var(--el-color-danger);
}
.alert-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-weight: 600;
}
.head-icon {
  vertical-align: -0.15em;
  margin-right: 4px;
  color: var(--el-color-primary);
}
.alert-head-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: normal;
}
.history-pager {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}
/* 未启用 file_sd 时的预览提示条：与下方表格留出间距 */
.sd-alert {
  margin-bottom: 12px;
}
.sd-note {
  margin: 12px 0 0;
  font-size: 0.82rem;
  color: var(--el-text-color-secondary);
  line-height: 1.6;
}
.grafana-wrap {
  position: relative;
  min-height: 750px;
}
.grafana-frame {
  width: 100%;
  border: none;
  border-radius: 8px;
  background: #fff;
}
.grafana-loading {
  position: absolute;
  inset: 0;
  display: flex;
  gap: 10px;
  align-items: center;
  justify-content: center;
  color: var(--el-text-color-secondary);
  font-size: 0.9rem;
  background: var(--el-fill-color-lighter);
  border-radius: 8px;
}
.grafana-fallback {
  padding: 24px 0;
}
/* 窄窗口提示层：贴顶部悬浮，不遮数据主体 */
.narrow-hint {
  top: 0;
  bottom: auto;
  height: auto;
  padding: 8px 16px;
  justify-content: flex-start;
  background: var(--el-color-warning-light-9);
  color: var(--el-color-warning-dark-2);
  font-size: 0.85rem;
  z-index: 2;
}
</style>
