<template>
  <div>
    <div class="page-head">
      <div>
        <h2 class="page-title">Docker 管理</h2>
        <p class="page-desc">容器 / 镜像 / 网络 / 卷 / 编排一体化管理（对标 1Panel 容器页）</p>
      </div>
    </div>

    <!-- 后端 Docker 不可用（HTTP 503）时的引导提示 -->
    <el-alert
      v-if="backendError"
      type="error"
      :title="backendError"
      description="安装并启动 Docker 后可用（systemctl enable --now docker）"
      show-icon
      :closable="false"
      style="margin-bottom: 12px"
    />

    <el-card shadow="never">
      <!-- 顶部：tab 导航 + 刷新（刷新永远作用于当前 tab） -->
      <div class="docker-top">
        <el-tabs v-model="tab" class="docker-tabs" @tab-change="onTabChange">
          <!-- ═══════ 容器 ═══════ -->
          <el-tab-pane label="容器" name="containers">
            <div class="pane-toolbar">
              <el-select v-model="stateFilter" class="ct-state" placeholder="全部状态">
                <el-option label="全部状态" value="" />
                <el-option v-for="s in stateOptions" :key="s.value" :label="`${s.value}（${s.count}）`" :value="s.value" />
              </el-select>
              <el-input
                v-model="keyword"
                class="ct-search"
                placeholder="按名称 / 镜像搜索"
                clearable
                :prefix-icon="Search"
              />
              <!-- 自动刷新：10s 静默轮询的总开关（记忆到 localStorage），关闭后定时器回调直接跳过 -->
              <div class="ct-auto" title="每 10 秒自动刷新容器列表与资源占用">
                <el-switch v-model="autoRefresh" @change="onAutoRefreshChange" />
                <span class="ct-auto-label">自动刷新</span>
              </div>
              <template v-if="selection.length">
                <span class="ct-sel">已选 {{ selection.length }} 项</span>
                <el-button type="primary" plain :disabled="!bulkStartable" :loading="bulkLoading" @click="bulkAction('start')">批量启动</el-button>
                <el-button type="warning" plain :disabled="!bulkStoppable" :loading="bulkLoading" @click="bulkAction('stop')">批量停止</el-button>
                <el-button type="danger" plain :disabled="bulkLoading" @click="bulkAction('delete')">批量删除</el-button>
              </template>
              <span class="count ct-count">共 {{ filteredContainers.length }} 个容器</span>
            </div>

            <!-- row-key + reserve-selection：10s 轮询整体替换数据后保留勾选（P0） -->
            <el-table
              ref="containerTableRef"
              :data="filteredContainers"
              row-key="ID"
              v-loading="loading"
              stripe
              size="small"
              @selection-change="onSelectionChange"
            >
              <template #empty><el-empty description="暂无容器" :image-size="80" /></template>
              <el-table-column type="selection" width="36" reserve-selection />
              <el-table-column label="名称" min-width="96" show-overflow-tooltip>
                <template #default="{ row }">
                  <span class="mono">{{ containerName(row.Names) }}</span>
                </template>
              </el-table-column>
              <el-table-column prop="Image" label="镜像" min-width="108" show-overflow-tooltip>
                <template #default="{ row }">
                  <span class="mono">{{ row.Image || '—' }}</span>
                </template>
              </el-table-column>
              <el-table-column label="状态" width="72">
                <template #default="{ row }">
                  <el-tag :type="stateTag(row.State)" effect="light" size="small">{{ stateText(row.State) }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column label="CPU%" width="62">
                <template #default="{ row }">
                  <span class="mono">{{ cpuText(row) }}</span>
                </template>
              </el-table-column>
              <el-table-column label="内存%" width="66">
                <template #default="{ row }">
                  <span class="mono" :title="memTitle(row)">{{ memText(row) }}</span>
                </template>
              </el-table-column>
              <el-table-column prop="Status" label="明细" min-width="96" show-overflow-tooltip>
                <template #default="{ row }">{{ row.Status || '—' }}</template>
              </el-table-column>
              <el-table-column label="端口" min-width="96" show-overflow-tooltip>
                <template #default="{ row }">{{ portsText(row.Ports) }}</template>
              </el-table-column>
              <el-table-column label="创建时间" width="146">
                <template #default="{ row }">
                  <span class="mono">{{ dockerTime(row.CreatedAt || row.Created) }}</span>
                </template>
              </el-table-column>
              <el-table-column label="操作" width="330" fixed="right" class-name="ct-op">
                <template #default="{ row }">
                  <el-button
                    v-if="row.State !== 'running'"
                    size="small" text type="success"
                    :loading="actingKey === row.ID + ':start'"
                    :disabled="!!actingKey && actingKey !== row.ID + ':start'"
                    @click="containerAction(row, 'start')"
                  >启动</el-button>
                  <el-button
                    v-else
                    size="small" text type="warning"
                    :loading="actingKey === row.ID + ':stop'"
                    :disabled="!!actingKey && actingKey !== row.ID + ':stop'"
                    @click="containerAction(row, 'stop')"
                  >停止</el-button>
                  <el-button
                    size="small" text type="primary"
                    :loading="actingKey === row.ID + ':restart'"
                    :disabled="!!actingKey && actingKey !== row.ID + ':restart'"
                    @click="containerAction(row, 'restart')"
                  >重启</el-button>
                  <el-button size="small" text type="primary" :disabled="row.State !== 'running'" @click="openTerminal(row)">终端</el-button>
                  <el-button size="small" text type="primary" @click="openInspect(row)">详情</el-button>
                  <el-button size="small" text type="primary" @click="openLogs(row)">日志</el-button>
                  <el-button size="small" text type="danger" @click="removeContainer(row)">删除</el-button>
                </template>
              </el-table-column>
            </el-table>
          </el-tab-pane>

          <!-- ═══════ 镜像 ═══════ -->
          <el-tab-pane label="镜像" name="images">
            <div class="pane-toolbar">
              <el-button type="primary" @click="openPull">拉取镜像</el-button>
              <el-button type="warning" plain :loading="pruneLoading" @click="pruneImages">清理悬空镜像</el-button>
              <el-input
                v-model="imageKeyword"
                class="ct-search"
                placeholder="按仓库名 / Tag 搜索"
                clearable
                :prefix-icon="Search"
              />
              <span class="count ct-count">共 {{ filteredImages.length }} 个镜像</span>
            </div>
            <el-table :data="filteredImages" v-loading="loading" stripe size="small">
              <template #empty><el-empty description="暂无镜像" :image-size="80" /></template>
              <el-table-column label="仓库" min-width="220" show-overflow-tooltip>
                <template #default="{ row }">
                  <span class="mono">{{ row.Repository || '—' }}</span>
                </template>
              </el-table-column>
              <el-table-column label="Tag" width="130">
                <template #default="{ row }">
                  <el-tag effect="plain" size="small">{{ row.Tag || '—' }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column label="ID" width="130">
                <template #default="{ row }">
                  <span class="mono">{{ shortId(row.ID) }}</span>
                </template>
              </el-table-column>
              <el-table-column label="大小" width="110">
                <template #default="{ row }">{{ dockerSize(row.Size) }}</template>
              </el-table-column>
              <el-table-column label="创建时间" width="160">
                <template #default="{ row }">
                  <span class="mono">{{ imageTime(row.CreatedSince || row.Created) }}</span>
                </template>
              </el-table-column>
              <el-table-column label="操作" width="90" fixed="right">
                <template #default="{ row }">
                  <el-button size="small" text type="danger" @click="removeImage(row)">删除</el-button>
                </template>
              </el-table-column>
            </el-table>
          </el-tab-pane>

          <!-- ═══════ 网络 ═══════ -->
          <el-tab-pane label="网络" name="networks">
            <div class="pane-toolbar">
              <el-button type="primary" @click="openNetworkDialog">创建网络</el-button>
              <span class="count ct-count">共 {{ networks.length }} 个网络</span>
            </div>
            <el-table :data="networks" v-loading="loading" stripe size="small">
              <template #empty><el-empty description="暂无网络" :image-size="80" /></template>
              <el-table-column label="名称" min-width="180" show-overflow-tooltip>
                <template #default="{ row }">
                  <span class="mono">{{ row.Name || '—' }}</span>
                  <el-tag v-if="isBuiltinNetwork(row.Name)" effect="plain" size="small" style="margin-left: 8px">内置</el-tag>
                </template>
              </el-table-column>
              <el-table-column label="驱动" width="120">
                <template #default="{ row }">{{ row.Driver || '—' }}</template>
              </el-table-column>
              <el-table-column label="Scope" width="120">
                <template #default="{ row }">{{ row.Scope || '—' }}</template>
              </el-table-column>
              <el-table-column label="创建时间" min-width="180">
                <template #default="{ row }">
                  <span class="mono">{{ dockerTime(row.CreatedAt) }}</span>
                </template>
              </el-table-column>
              <el-table-column label="操作" width="90" fixed="right">
                <template #default="{ row }">
                  <!-- bridge/host/none 等内置网络是 docker 底座，前后端双重禁删，按钮置灰 -->
                  <el-button size="small" text type="danger" :disabled="isBuiltinNetwork(row.Name)" @click="removeNetwork(row)">删除</el-button>
                </template>
              </el-table-column>
            </el-table>
          </el-tab-pane>

          <!-- ═══════ 卷 ═══════ -->
          <el-tab-pane label="卷" name="volumes">
            <div class="pane-toolbar">
              <el-button type="primary" @click="openVolumeDialog">创建卷</el-button>
              <el-button type="danger" plain @click="pruneVolumes">清理未引用卷</el-button>
              <span class="count ct-count">共 {{ volumes.length }} 个卷</span>
            </div>
            <el-table :data="volumes" v-loading="loading" stripe size="small">
              <template #empty><el-empty description="暂无卷" :image-size="80" /></template>
              <el-table-column label="名称" min-width="260" show-overflow-tooltip>
                <template #default="{ row }">
                  <span class="mono">{{ row.Name || '—' }}</span>
                </template>
              </el-table-column>
              <el-table-column label="驱动" width="140">
                <template #default="{ row }">{{ row.Driver || '—' }}</template>
              </el-table-column>
              <el-table-column label="Scope" width="140">
                <template #default="{ row }">{{ row.Scope || '—' }}</template>
              </el-table-column>
              <el-table-column label="操作" width="90" fixed="right">
                <template #default="{ row }">
                  <el-button size="small" text type="danger" @click="removeVolume(row)">删除</el-button>
                </template>
              </el-table-column>
            </el-table>
          </el-tab-pane>

          <!-- ═══════ 编排 ═══════ -->
          <el-tab-pane label="编排" name="compose">
            <div class="pane-toolbar">
              <span class="count ct-count-inline">共 {{ composeProjects.length }} 个编排项目</span>
            </div>
            <el-table :data="composeProjects" v-loading="loading" stripe size="small">
              <template #empty><el-empty description="暂无 compose 编排项目（docker compose ls 为空）" :image-size="80" /></template>
              <el-table-column label="名称" min-width="180" show-overflow-tooltip>
                <template #default="{ row }">
                  <span class="mono">{{ row.Name || '—' }}</span>
                </template>
              </el-table-column>
              <el-table-column label="状态" width="140">
                <template #default="{ row }">
                  <el-tag :type="composeTag(row.Status)" effect="light" size="small">{{ row.Status || '—' }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column label="配置文件路径" min-width="280" show-overflow-tooltip>
                <template #default="{ row }">
                  <span class="mono">{{ row.ConfigFiles || '—' }}</span>
                </template>
              </el-table-column>
              <el-table-column label="操作" width="270" fixed="right">
                <template #default="{ row }">
                  <el-button
                    size="small" type="success" plain
                    :loading="composeKey === row.Name + ':start'"
                    :disabled="composeKey !== ''"
                    @click="composeAction(row, 'start')"
                  >启动</el-button>
                  <el-button
                    size="small" type="warning" plain
                    :loading="composeKey === row.Name + ':stop'"
                    :disabled="(row.Status || '').indexOf('running') !== 0 || composeKey !== ''"
                    @click="composeAction(row, 'stop')"
                  >停止</el-button>
                  <el-button
                    size="small" type="primary" plain
                    :loading="composeKey === row.Name + ':restart'"
                    :disabled="(row.Status || '').indexOf('running') !== 0 || composeKey !== ''"
                    @click="composeAction(row, 'restart')"
                  >重启</el-button>
                  <el-button
                    size="small" type="danger" plain
                    :loading="composeKey === row.Name + ':down'"
                    :disabled="composeKey !== ''"
                    @click="composeAction(row, 'down')"
                  >下线</el-button>
                </template>
              </el-table-column>
            </el-table>
          </el-tab-pane>
        </el-tabs>
        <el-button class="docker-refresh" :icon="Refresh" :loading="loading" @click="reload">刷新</el-button>
      </div>
    </el-card>

    <!-- 容器终端抽屉：55% 深色（抽屉挂载于 body，深色样式在底部非 scoped 样式块） -->
    <el-drawer v-model="termDrawer" class="term-drawer" :title="'容器终端 — ' + termName" size="55%" :close-on-click-modal="false">
      <ContainerTerminal v-if="termDrawer" :container-id="termId" />
    </el-drawer>

    <!-- 容器详情抽屉：docker inspect 原始 JSON，深色等宽展示 -->
    <el-drawer v-model="inspectDrawer" :title="'容器详情 — ' + inspectName" size="55%">
      <div v-loading="inspectLoading">
        <div class="logs-toolbar">
          <el-button size="small" :icon="Refresh" :loading="inspectLoading" @click="fetchInspect">刷新</el-button>
          <el-button size="small" :icon="CopyDocument" @click="copyInspect">复制</el-button>
          <span class="logs-hint">docker inspect 原始 JSON</span>
        </div>
        <pre class="logs-pre">{{ inspectText || '（暂无数据）' }}</pre>
      </div>
    </el-drawer>

    <!-- 容器日志抽屉：深色背景等宽展示，支持 tail 行数切换 / 跟随滚动 / 复制 / 下载 -->
    <el-drawer v-model="logsDrawer" :title="'容器日志 — ' + logsName" size="55%">
      <div v-loading="logsLoading">
        <div class="logs-toolbar">
          <el-button size="small" :icon="Refresh" :loading="logsLoading" @click="fetchLogs">刷新</el-button>
          <el-select v-model="logsTail" class="logs-tail" @change="onTailChange">
            <!-- 后端将 tail 钳制到 [200, 2000]，无法真正「不限行数」，「全部」即后端支持的 2000 行上限 -->
            <el-option label="全部（2000 行）" :value="2000" />
            <el-option label="100 行" :value="100" />
            <el-option label="200 行" :value="200" />
            <el-option label="500 行" :value="500" />
            <el-option label="1000 行" :value="1000" />
          </el-select>
          <div class="ct-auto" title="开启后每 2 秒自动拉取新日志，滚动贴底时自动滚到最新">
            <el-switch v-model="logsFollow" size="small" />
            <span class="ct-auto-label">跟随</span>
          </div>
          <el-button size="small" :icon="Download" @click="downloadLogs">下载</el-button>
          <el-button size="small" :icon="CopyDocument" @click="copyLogs">复制</el-button>
        </div>
        <pre ref="logsPreRef" class="logs-pre">{{ logsText || '（暂无日志输出）' }}</pre>
      </div>
    </el-drawer>

    <!-- 拉取镜像对话框 -->
    <el-dialog
      v-model="pullDialog"
      title="拉取镜像"
      width="480px"
      :close-on-click-modal="false"
      :close-on-press-escape="!pullLoading"
      :show-close="!pullLoading"
    >
      <el-form label-width="80px" @submit.prevent>
        <el-form-item label="镜像名" required>
          <el-input
            v-model="pullName"
            placeholder="如 nginx:latest（省略 tag 默认 latest）"
            :disabled="pullLoading"
            @keyup.enter="confirmPull"
          />
        </el-form-item>
      </el-form>
      <p class="dialog-hint">拉取同步执行，大镜像可能需要数分钟，请保持页面打开。</p>
      <template #footer>
        <el-button :disabled="pullLoading" @click="pullDialog = false">取消</el-button>
        <el-button type="primary" :loading="pullLoading" @click="confirmPull">{{ pullLoading ? '正在拉取镜像…' : '开始拉取' }}</el-button>
      </template>
    </el-dialog>

    <!-- 创建网络对话框 -->
    <el-dialog v-model="networkDialog" title="创建网络" width="520px" :close-on-click-modal="false">
      <el-form ref="networkFormRef" :model="networkForm" :rules="networkRules" label-width="80px">
        <el-form-item label="名称" prop="name">
          <el-input v-model="networkForm.name" placeholder="如 mynet" />
        </el-form-item>
        <el-form-item label="驱动" prop="driver">
          <el-select v-model="networkForm.driver" style="width: 100%">
            <el-option v-for="d in networkDrivers" :key="d" :label="d" :value="d" />
          </el-select>
        </el-form-item>
        <el-form-item label="子网" prop="subnet">
          <el-input v-model="networkForm.subnet" placeholder="可选，CIDR 格式如 172.30.0.0/16" />
        </el-form-item>
        <el-form-item label="网关" prop="gateway">
          <el-input v-model="networkForm.gateway" placeholder="可选，如 172.30.0.1" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="networkDialog = false">取消</el-button>
        <el-button type="primary" :loading="networkSubmitting" @click="submitNetwork">创建</el-button>
      </template>
    </el-dialog>

    <!-- 创建卷对话框 -->
    <el-dialog v-model="volumeDialog" title="创建卷" width="460px" :close-on-click-modal="false">
      <el-form ref="volumeFormRef" :model="volumeForm" :rules="volumeRules" label-width="80px">
        <el-form-item label="名称" prop="name">
          <el-input v-model="volumeForm.name" placeholder="如 app-data" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="volumeDialog = false">取消</el-button>
        <el-button type="primary" :loading="volumeSubmitting" @click="submitVolume">创建</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, computed, h, onMounted, onUnmounted, nextTick, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, CopyDocument, Search, Download } from '@element-plus/icons-vue'
import http from '../api'
import ContainerTerminal from '../components/ContainerTerminal.vue'
import { errMsg, isCancel, fmtDateTime, fmtDateTimeLocale, fmtSizeBytes } from '../utils/format'

// ═══════════════ Tab 骨架与按需加载 ═══════════════
// 首次进入某 tab 才拉取对应数据；右上刷新按钮强制重拉当前 tab。
const tab = ref('containers')
const loading = ref(false)
const loadedTabs = ref(['containers'])
// HTTP 503（Docker 守护进程不可用）时置为后端 message，页面顶部 alert 展示；成功加载后清空
const backendError = ref('')

// 「自动刷新」开关：容器视图 10s 轮询的总开关，记忆到 localStorage（默认开）
const AUTO_REFRESH_KEY = 'vmops-docker-autorefresh'
const autoRefresh = ref(localStorage.getItem(AUTO_REFRESH_KEY) !== '0')

function onAutoRefreshChange(v) {
  localStorage.setItem(AUTO_REFRESH_KEY, v ? '1' : '0')
}

const containers = ref([])
const images = ref([])
const networks = ref([])
const volumes = ref([])
const composeProjects = ref([])
const statsMap = ref({})

function handleLoadError(e, fallback = '获取 Docker 数据失败') {
  if (e.response && e.response.status === 503) {
    backendError.value = errMsg(e, 'Docker 服务不可用')
  } else {
    ElMessage.error(errMsg(e, fallback))
  }
}

async function loadTab(name, { force = false } = {}) {
  if (!force && loadedTabs.value.includes(name)) return
  loading.value = true
  try {
    if (name === 'containers') await Promise.all([fetchContainers(), fetchStats()])
    else if (name === 'images') await fetchImages()
    else if (name === 'networks') await fetchNetworks()
    else if (name === 'volumes') await fetchVolumes()
    else if (name === 'compose') await fetchCompose()
    backendError.value = ''
    if (!loadedTabs.value.includes(name)) loadedTabs.value.push(name)
  } catch (e) {
    handleLoadError(e)
  } finally {
    loading.value = false
  }
}

function onTabChange(name) {
  loadTab(name)
}

// 顶部刷新：重拉当前 tab
function reload() {
  loadTab(tab.value, { force: true })
}

// ═══════════════ 展示格式化 ═══════════════

function containerName(names) {
  if (Array.isArray(names)) return names.join(', ') || '—'
  return names || '—'
}

// 容器状态 → tag 颜色：running 绿 / exited 灰 / 其他橙
function stateTag(state) {
  if (state === 'running') return 'success'
  if (state === 'exited') return 'info'
  return 'warning'
}

// Docker 容器状态 → 中文（docker ps 的 State 全取值集）；未知状态原样返回便于暴露新取值
const DOCKER_STATE_TEXT = {
  running: '运行中',
  exited: '已退出',
  paused: '已暂停',
  created: '已创建',
  restarting: '重启中',
  removing: '删除中',
  dead: '死亡'
}

function stateText(state) {
  return DOCKER_STATE_TEXT[state] || state || '未知'
}

// compose 项目状态 → tag 颜色
function composeTag(status) {
  const s = String(status || '')
  if (s.indexOf('running') === 0) return 'success'
  if (s.indexOf('exited') === 0 || s.indexOf('stopped') === 0) return 'info'
  return 'warning'
}

// Ports 兼容两类后端形态：docker SDK 对象数组或已拼好的字符串（本项目后端为字符串）
function portsText(ports) {
  if (!ports || (Array.isArray(ports) && ports.length === 0)) return '—'
  if (Array.isArray(ports)) {
    return ports
      .map((p) => {
        const host = p.PublicPort ? `${p.IP || ''}:${p.PublicPort}->` : ''
        return `${host}${p.PrivatePort}/${p.Type || 'tcp'}`
      })
      .join('  ')
  }
  return String(ports)
}

// ID 截短 12 位（docker 惯例）
function shortId(id) {
  return id ? String(id).replace(/^sha256:/, '').slice(0, 12) : '—'
}

// Created 兼容 unix 秒级时间戳与「3 days ago」这类相对时间串；解析不了原样展示
function dockerTime(v) {
  if (v === null || v === undefined || v === '') return '—'
  if (typeof v === 'number') return fmtDateTime(v * (v > 1e12 ? 1 : 1000))
  const d = new Date(v)
  if (!isNaN(d.getTime()) && /\d{4}/.test(String(v))) return fmtDateTime(d)
  return String(v)
}

// docker 相对时间串（「3 days ago」「About an hour ago」）→ 中文；不匹配返回空串
const REL_TIME_ZH_UNITS = { second: '秒', minute: '分钟', hour: '小时', day: '天', week: '周', month: '个月', year: '年' }

function relativeTimeZh(v) {
  const s = String(v).trim()
  let m = s.match(/^(\d+)\s*(second|minute|hour|day|week|month|year)s?\s+ago$/i)
  if (m) return `${m[1]} ${REL_TIME_ZH_UNITS[m[2].toLowerCase()]}前`
  m = s.match(/^about an? (second|minute|hour|day)\s+ago$/i)
  if (m) return `约 1 ${REL_TIME_ZH_UNITS[m[1].toLowerCase()]}前`
  return ''
}

// 镜像创建时间：Created 为 unix 秒时间戳时换算本地化时间；
// 「3 days ago」相对时间串映射中文（docker images 的 CreatedSince 形态）
function imageTime(v) {
  if (v === null || v === undefined || v === '') return '—'
  if (typeof v === 'number') return fmtDateTimeLocale(new Date(v > 1e12 ? v : v * 1000))
  const rel = relativeTimeZh(v)
  if (rel) return rel
  const d = new Date(v)
  if (!isNaN(d.getTime()) && /\d{4}/.test(String(v))) return fmtDateTimeLocale(d)
  return String(v)
}

// Size 兼容字节（数字）与「1.2GB」（字符串）两种形态
function dockerSize(v) {
  if (v === null || v === undefined || v === '') return '—'
  if (typeof v === 'number') return fmtSizeBytes(v)
  return String(v)
}

// ═══════════════ 数据拉取 ═══════════════

async function fetchContainers() {
  const res = await http.get('/docker/containers')
  containers.value = (res.data.data || {}).items || []
}

// 全容器实时 stats（docker stats --no-stream）：按容器名建索引，供 CPU%/内存% 列查询
async function fetchStats() {
  try {
    const res = await http.get('/docker/stats')
    const items = (res.data.data || {}).items || []
    const m = {}
    for (const it of items) m[it.Name || it.Container || it.ID] = it
    statsMap.value = m
  } catch (e) {
    // stats 是增强列，拉取失败静默（列显示 —），不打扰列表主流程
  }
}

async function fetchImages() {
  const res = await http.get('/docker/images')
  images.value = (res.data.data || {}).items || []
}

async function fetchNetworks() {
  const res = await http.get('/docker/networks')
  networks.value = (res.data.data || {}).items || []
}

async function fetchVolumes() {
  const res = await http.get('/docker/volumes')
  volumes.value = (res.data.data || {}).items || []
}

async function fetchCompose() {
  const res = await http.get('/docker/compose')
  composeProjects.value = (res.data.data || {}).items || []
}

// 容器视图 10s 静默轮询：列表 + stats 一起刷；不动 loading，失败不打扰用户（503 时同步顶部 alert）
// 「自动刷新」开关关闭时回调直接 return（定时器保留，见 onMounted）
let pollTimer = null
let refreshing = false
async function silentRefresh() {
  if (!autoRefresh.value || tab.value !== 'containers' || refreshing || loading.value) return
  refreshing = true
  try {
    await Promise.all([fetchContainers(), fetchStats()])
    backendError.value = ''
  } catch (e) {
    if (e.response && e.response.status === 503) {
      backendError.value = errMsg(e, 'Docker 服务不可用')
    }
  } finally {
    refreshing = false
  }
}

// ═══════════════ 容器：筛选 / 批量 / 行内操作 ═══════════════

const stateFilter = ref('')
const keyword = ref('')

// 状态筛选选项带计数（label 渲染为「running（3）」），按状态名排序
const stateOptions = computed(() => {
  const counts = {}
  for (const r of containers.value) {
    if (!r.State) continue
    counts[r.State] = (counts[r.State] || 0) + 1
  }
  return Object.keys(counts)
    .sort()
    .map((s) => ({ value: s, count: counts[s] }))
})

const filteredContainers = computed(() =>
  containers.value.filter((r) => {
    if (stateFilter.value && r.State !== stateFilter.value) return false
    const kw = keyword.value.trim().toLowerCase()
    if (kw) {
      const hay = `${r.Names || ''} ${r.Image || ''}`.toLowerCase()
      if (!hay.includes(kw)) return false
    }
    return true
  })
)

// ── 镜像 tab：关键字搜索（仓库名 / Tag 前端过滤，与容器 tab 同款交互）──

const imageKeyword = ref('')

const filteredImages = computed(() => {
  const kw = imageKeyword.value.trim().toLowerCase()
  if (!kw) return images.value
  return images.value.filter((r) => `${r.Repository || ''} ${r.Tag || ''}`.toLowerCase().includes(kw))
})

// ── stats 列（CPU% / 内存%）──

function statsOf(row) {
  if (!row) return null
  return statsMap.value[row.Names] || statsMap.value[row.ID] || null
}

// docker stats 的 MemUsage 形如「12.3MiB / 1.944GiB」，按单位换算字节数
const DOCKER_SIZE_UNITS = {
  B: 1, kB: 1e3, KB: 1e3, KiB: 1024,
  MB: 1e6, MiB: 1024 ** 2, GB: 1e9, GiB: 1024 ** 3, TB: 1e12, TiB: 1024 ** 4
}
function parseDockerBytes(s) {
  const m = String(s || '').trim().match(/^([\d.]+)\s*(B|kB|KB|KiB|MB|MiB|GB|GiB|TB|TiB)$/)
  if (!m) return NaN
  return parseFloat(m[1]) * (DOCKER_SIZE_UNITS[m[2]] || 1)
}

function cpuText(row) {
  const st = statsOf(row)
  return st && st.CPUPerc ? st.CPUPerc : '—'
}

function memPctOf(row) {
  const st = statsOf(row)
  if (!st) return null
  // 优先用 docker 直接给的 MemPerc；缺失时按 MemUsage「used / total」换算
  if (st.MemPerc) {
    const n = parseFloat(st.MemPerc)
    if (!isNaN(n)) return n
  }
  const parts = String(st.MemUsage || '').split('/')
  if (parts.length !== 2) return null
  const used = parseDockerBytes(parts[0])
  const total = parseDockerBytes(parts[1])
  if (!isFinite(used) || !isFinite(total) || total <= 0) return null
  return Math.min(100, (used / total) * 100)
}

function memText(row) {
  const pct = memPctOf(row)
  return pct === null ? '—' : pct.toFixed(1) + '%'
}

function memTitle(row) {
  const st = statsOf(row)
  return st && st.MemUsage ? '内存占用 ' + st.MemUsage : ''
}

// ── 批量操作 ──

const containerTableRef = ref(null)
const selection = ref([])
const bulkLoading = ref(false)

function onSelectionChange(rows) {
  selection.value = rows
}

const bulkStartable = computed(() => selection.value.some((r) => r.State !== 'running'))
const bulkStoppable = computed(() => selection.value.some((r) => r.State === 'running'))

async function bulkAction(action) {
  // 启动只对非 running、停止只对 running 生效；删除全量（running 带 force）
  const targets = selection.value.filter((r) =>
    action === 'start' ? r.State !== 'running' : action === 'stop' ? r.State === 'running' : true
  )
  if (!targets.length) return
  const label = { start: '启动', stop: '停止', delete: '删除' }[action]
  const extra = action === 'delete' ? '运行中的容器将被强制删除（force），容器内未持久化的数据会丢失。' : ''
  const confirmOpts = { type: 'warning', confirmButtonText: label }
  if (action === 'delete') confirmOpts.confirmButtonClass = 'el-button--danger'
  try {
    await ElMessageBox.confirm(`确定批量${label}选中的 ${targets.length} 个容器？${extra}`, `批量${label}`, confirmOpts)
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  bulkLoading.value = true
  try {
    const results = await Promise.allSettled(
      targets.map((r) => {
        if (action === 'delete') {
          return http.delete('/docker/containers/' + r.ID, { params: r.State === 'running' ? { force: 'true' } : {} })
        }
        return http.post('/docker/containers/' + r.ID + '/' + action)
      })
    )
    const ok = results.filter((x) => x.status === 'fulfilled').length
    const fail = results.length - ok
    if (fail) ElMessage.warning(`批量${label}完成：成功 ${ok} 个，失败 ${fail} 个`)
    else ElMessage.success(`批量${label}完成（${ok} 个）`)
    if (containerTableRef.value) containerTableRef.value.clearSelection()
    await Promise.all([fetchContainers(), fetchStats()])
  } finally {
    bulkLoading.value = false
  }
}

// ── 行内操作（启动 / 停止 / 重启，per-row loading 防连点）──

const actingKey = ref('')

async function containerAction(row, action) {
  const label = { start: '启动', stop: '停止', restart: '重启' }[action]
  actingKey.value = row.ID + ':' + action
  try {
    await http.post('/docker/containers/' + row.ID + '/' + action)
    ElMessage.success(`已${label} ${containerName(row.Names)}`)
    await Promise.all([fetchContainers(), fetchStats()])
  } catch (e) {
    ElMessage.error(errMsg(e, `${label}失败`))
  } finally {
    actingKey.value = ''
  }
}

async function removeContainer(row) {
  const running = row.State === 'running'
  const name = containerName(row.Names)
  try {
    await ElMessageBox.confirm(
      running
        ? `容器 ${name} 正在运行，将强制删除（force），容器内未持久化的数据会丢失。确定删除？`
        : `确定删除容器 ${name}？`,
      '删除容器',
      { type: 'warning', confirmButtonText: '删除', confirmButtonClass: 'el-button--danger' }
    )
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  try {
    // 运行中的容器必须带 force=true，否则 Docker API 拒绝删除
    await http.delete('/docker/containers/' + row.ID, { params: running ? { force: 'true' } : {} })
    ElMessage.success(`已删除 ${name}`)
    await Promise.all([fetchContainers(), fetchStats()])
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

// ═══════════════ 容器终端 / 详情 / 日志抽屉 ═══════════════

const termDrawer = ref(false)
const termId = ref('')
const termName = ref('')

function openTerminal(row) {
  if (row.State !== 'running') {
    ElMessage.warning('容器未运行，无法打开终端')
    return
  }
  termId.value = row.ID
  termName.value = containerName(row.Names)
  termDrawer.value = true
}

const inspectDrawer = ref(false)
const inspectLoading = ref(false)
const inspectText = ref('')
const inspectName = ref('')
const inspectId = ref('')

function openInspect(row) {
  inspectId.value = row.ID
  inspectName.value = containerName(row.Names)
  inspectText.value = ''
  inspectDrawer.value = true
  fetchInspect()
}

async function fetchInspect() {
  if (!inspectId.value) return
  inspectLoading.value = true
  try {
    const res = await http.get('/docker/containers/' + inspectId.value + '/inspect')
    inspectText.value = JSON.stringify(res.data.data ?? {}, null, 2)
  } catch (e) {
    ElMessage.error(errMsg(e, '获取容器详情失败'))
  } finally {
    inspectLoading.value = false
  }
}

async function copyInspect() {
  if (!inspectText.value) {
    ElMessage.warning('暂无可复制的内容')
    return
  }
  try {
    await navigator.clipboard.writeText(inspectText.value)
    ElMessage.success('详情 JSON 已复制到剪贴板')
  } catch (e) {
    ElMessage.error('复制失败，请手动选择文本复制')
  }
}

const logsDrawer = ref(false)
const logsLoading = ref(false)
const logsText = ref('')
const logsName = ref('')
const logsId = ref('')
// tail 行数（后端钳制到 [200, 2000]）；「跟随」开关：每 2s 静默重拉新日志
const logsTail = ref(200)
const logsFollow = ref(false)
const logsPreRef = ref(null)
let logsTimer = null

function openLogs(row) {
  logsId.value = row.ID
  logsName.value = containerName(row.Names)
  logsText.value = ''
  logsDrawer.value = true
  fetchLogs()
}

async function fetchLogs() {
  if (!logsId.value) return
  logsLoading.value = true
  try {
    const res = await http.get('/docker/containers/' + logsId.value + '/logs', { params: { tail: logsTail.value } })
    const data = res.data.data || {}
    logsText.value = data.logs || ''
  } catch (e) {
    ElMessage.error(errMsg(e, '获取日志失败'))
  } finally {
    logsLoading.value = false
  }
}

function onTailChange() {
  fetchLogs()
}

// ── 跟随：定时静默重拉（不动 loading），贴底（距底 <40px）才自动滚到底 ──

function logsNearBottom() {
  const el = logsPreRef.value
  if (!el) return false
  return el.scrollHeight - el.scrollTop - el.clientHeight < 40
}

function scrollLogsBottom() {
  const el = logsPreRef.value
  if (el) el.scrollTop = el.scrollHeight
}

function startLogsTimer() {
  stopLogsTimer()
  logsTimer = setInterval(async () => {
    if (!logsDrawer.value || !logsId.value) return
    try {
      const near = logsNearBottom() // 更新内容前先记贴底状态，新日志到达后据此决定是否滚动
      const res = await http.get('/docker/containers/' + logsId.value + '/logs', { params: { tail: logsTail.value } })
      logsText.value = (res.data.data || {}).logs || ''
      if (near) nextTick(scrollLogsBottom)
    } catch (e) {
      // 跟随轮询失败静默（下拉手动刷新会给错误提示），不打扰阅读
    }
  }, 2000)
}

function stopLogsTimer() {
  if (logsTimer) {
    clearInterval(logsTimer)
    logsTimer = null
  }
}

watch(logsFollow, (on) => {
  if (on && logsDrawer.value) startLogsTimer()
  else stopLogsTimer()
})

// 抽屉关闭即停跟随轮询（下次打开时按开关状态重启）
watch(logsDrawer, (open) => {
  if (!open) stopLogsTimer()
  else if (logsFollow.value) startLogsTimer()
})

// 按当前 tail 拉取日志内容，Blob 下载为 <容器名>.log
async function downloadLogs() {
  if (!logsId.value) return
  try {
    const res = await http.get('/docker/containers/' + logsId.value + '/logs', { params: { tail: logsTail.value } })
    const text = (res.data.data || {}).logs || ''
    const blob = new Blob([text], { type: 'text/plain;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    // 容器名形如 "/web"，下载文件名清理掉路径非法字符
    a.download = (logsName.value || 'container').replace(/[\\/:*?"<>|]/g, '_').replace(/^_+/, '') + '.log'
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
  } catch (e) {
    ElMessage.error(errMsg(e, '下载日志失败'))
  }
}

async function copyLogs() {
  if (!logsText.value) {
    ElMessage.warning('暂无可复制的日志')
    return
  }
  try {
    await navigator.clipboard.writeText(logsText.value)
    ElMessage.success('日志已复制到剪贴板')
  } catch (e) {
    ElMessage.error('复制失败，请手动选择文本复制')
  }
}

// ═══════════════ 镜像：拉取 / 清理 / 删除 ═══════════════

const pullDialog = ref(false)
const pullLoading = ref(false)
const pullName = ref('')

function openPull() {
  pullName.value = ''
  pullDialog.value = true
}

async function confirmPull() {
  const name = pullName.value.trim()
  if (!name) {
    ElMessage.warning('请输入镜像名（如 nginx:latest）')
    return
  }
  pullLoading.value = true
  try {
    // 拉取耗时不可控（后端上限 10 分钟），本请求单独放开 axios 15s 全局超时
    const res = await http.post('/docker/images/pull', { name }, { timeout: 0 })
    ElMessage.success((res.data.data && res.data.data.message) || '镜像拉取完成')
    pullDialog.value = false
    await loadTab('images', { force: true })
  } catch (e) {
    ElMessage.error(errMsg(e, '镜像拉取失败'))
  } finally {
    pullLoading.value = false
  }
}

const pruneLoading = ref(false)

async function pruneImages() {
  try {
    await ElMessageBox.confirm(
      '将删除所有悬空镜像（未被任何容器引用的未标记镜像层），此操作不可恢复。确定清理？',
      '清理悬空镜像',
      { type: 'warning', confirmButtonText: '清理', confirmButtonClass: 'el-button--danger' }
    )
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  pruneLoading.value = true
  try {
    const res = await http.post('/docker/prune', { type: 'images' })
    showPruneResult(res.data.data || {})
    await loadTab('images', { force: true })
  } catch (e) {
    ElMessage.error(errMsg(e, '清理失败'))
  } finally {
    pruneLoading.value = false
  }
}

// prune 结果展示：后端 message 已含释放空间，原始输出较长则折叠进弹窗
function showPruneResult(data) {
  const msg = data.message || '清理完成'
  if (data.output) {
    ElMessageBox.alert(
      h('pre', { style: 'max-height:260px;overflow:auto;margin:0;font-size:12px;line-height:1.6;white-space:pre-wrap;word-break:break-all;' }, String(data.output)),
      msg,
      { confirmButtonText: '知道了' }
    )
  } else {
    ElMessage.success(msg)
  }
}

async function removeImage(row) {
  const full = `${row.Repository || '—'}:${row.Tag || '—'}`
  try {
    await ElMessageBox.confirm(`确定删除镜像 ${full}（${shortId(row.ID)}）？`, '删除镜像', {
      type: 'warning',
      confirmButtonText: '删除',
      confirmButtonClass: 'el-button--danger'
    })
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  try {
    // 镜像 ID 可能含特殊字符（sha256: 前缀），必须 encodeURIComponent
    await http.delete('/docker/images/' + encodeURIComponent(row.ID))
    ElMessage.success(`已删除镜像 ${full}`)
    await loadTab('images', { force: true })
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

// ═══════════════ 网络 ═══════════════

// docker 内置网络禁删（bridge/host/none 及 overlay 的隐藏网关桥），与后端黑名单一致
const BUILTIN_NETWORKS = ['bridge', 'host', 'none', 'docker_gwbridge']

function isBuiltinNetwork(name) {
  return BUILTIN_NETWORKS.includes(name)
}

async function removeNetwork(row) {
  if (isBuiltinNetwork(row.Name)) return
  try {
    await ElMessageBox.confirm(`确定删除网络 ${row.Name}？`, '删除网络', {
      type: 'warning',
      confirmButtonText: '删除',
      confirmButtonClass: 'el-button--danger'
    })
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  try {
    await http.delete('/docker/networks/' + encodeURIComponent(row.Name))
    ElMessage.success(`已删除网络 ${row.Name}`)
    await loadTab('networks', { force: true })
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

const networkDialog = ref(false)
const networkSubmitting = ref(false)
const networkFormRef = ref(null)
const networkForm = reactive({ name: '', driver: 'bridge', subnet: '', gateway: '' })
const networkDrivers = ['bridge', 'overlay', 'macvlan', 'ipvlan', 'host', 'none']
const networkRules = {
  name: [
    { required: true, message: '请输入网络名称', trigger: 'blur' },
    { pattern: /^[A-Za-z0-9][A-Za-z0-9_.-]*$/, message: '仅允许字母数字与 -_. 且不以符号开头', trigger: 'blur' }
  ],
  subnet: [
    { pattern: /^(\d{1,3}\.){3}\d{1,3}\/(3[0-2]|[12]?\d)$/, message: '应为 CIDR 格式（掩码 0-32），如 172.30.0.0/16', trigger: 'blur' }
  ],
  gateway: [
    { pattern: /^(\d{1,3}\.){3}\d{1,3}$/, message: '应为合法 IPv4 地址', trigger: 'blur' }
  ]
}

function openNetworkDialog() {
  networkForm.name = ''
  networkForm.driver = 'bridge'
  networkForm.subnet = ''
  networkForm.gateway = ''
  networkDialog.value = true
  nextTick(() => networkFormRef.value && networkFormRef.value.clearValidate())
}

async function submitNetwork() {
  try {
    await networkFormRef.value.validate()
  } catch (e) {
    return
  }
  networkSubmitting.value = true
  try {
    const payload = { name: networkForm.name.trim(), driver: networkForm.driver }
    if (networkForm.subnet.trim()) payload.subnet = networkForm.subnet.trim()
    if (networkForm.gateway.trim()) payload.gateway = networkForm.gateway.trim()
    const res = await http.post('/docker/networks', payload)
    ElMessage.success((res.data.data && res.data.data.message) || '网络已创建')
    networkDialog.value = false
    await loadTab('networks', { force: true })
  } catch (e) {
    ElMessage.error(errMsg(e, '创建网络失败'))
  } finally {
    networkSubmitting.value = false
  }
}

// ═══════════════ 卷 ═══════════════

async function removeVolume(row) {
  try {
    await ElMessageBox.confirm(`确定删除卷 ${row.Name}？卷内数据将一并删除。`, '删除卷', {
      type: 'warning',
      confirmButtonText: '删除',
      confirmButtonClass: 'el-button--danger'
    })
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  try {
    await http.delete('/docker/volumes/' + encodeURIComponent(row.Name))
    ElMessage.success(`已删除卷 ${row.Name}`)
    await loadTab('volumes', { force: true })
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

const volumeDialog = ref(false)
const volumeSubmitting = ref(false)
const volumeFormRef = ref(null)
const volumeForm = reactive({ name: '' })
const volumeRules = {
  name: [
    { required: true, message: '请输入卷名称', trigger: 'blur' },
    { pattern: /^[A-Za-z0-9][A-Za-z0-9_.-]*$/, message: '仅允许字母数字与 -_. 且不以符号开头', trigger: 'blur' }
  ]
}

function openVolumeDialog() {
  volumeForm.name = ''
  volumeDialog.value = true
  nextTick(() => volumeFormRef.value && volumeFormRef.value.clearValidate())
}

async function submitVolume() {
  try {
    await volumeFormRef.value.validate()
  } catch (e) {
    return
  }
  volumeSubmitting.value = true
  try {
    const res = await http.post('/docker/volumes', { name: volumeForm.name.trim() })
    ElMessage.success((res.data.data && res.data.data.message) || '卷已创建')
    volumeDialog.value = false
    await loadTab('volumes', { force: true })
  } catch (e) {
    ElMessage.error(errMsg(e, '创建卷失败'))
  } finally {
    volumeSubmitting.value = false
  }
}

async function pruneVolumes() {
  try {
    await ElMessageBox.confirm(
      '将删除所有未被任何容器引用的卷，卷内数据将永久丢失且不可恢复！确定清理未引用卷？',
      '清理未引用卷',
      { type: 'error', confirmButtonText: '仍要清理', confirmButtonClass: 'el-button--danger' }
    )
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  try {
    const res = await http.post('/docker/volumes/prune')
    showPruneResult(res.data.data || {})
    await loadTab('volumes', { force: true })
  } catch (e) {
    ElMessage.error(errMsg(e, '清理失败'))
  }
}

// ═══════════════ 编排（compose 项目）═══════════════

// 行内任一项目操作进行中则整列锁定（compose 操作是项目级的，并发互相踩）
const composeKey = ref('')

async function composeAction(row, action) {
  if (action === 'down') {
    try {
      await ElMessageBox.confirm(
        `下线编排项目 ${row.Name} 将移除其全部容器（具名卷保留），确定下线？`,
        '下线编排项目',
        { type: 'warning', confirmButtonText: '下线', confirmButtonClass: 'el-button--danger' }
      )
    } catch (e) {
      if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
      return
    }
  }
  composeKey.value = row.Name + ':' + action
  try {
    // 项目级操作可能重建多个容器（后端上限 2 分钟），放宽前端 15s 默认超时
    const res = await http.post('/docker/compose/' + encodeURIComponent(row.Name) + '/' + action, null, { timeout: 150000 })
    ElMessage.success((res.data.data && res.data.data.message) || '操作完成')
    await loadTab('compose', { force: true })
  } catch (e) {
    ElMessage.error(errMsg(e, '编排操作失败'))
  } finally {
    composeKey.value = ''
  }
}

// ═══════════════ 生命周期 ═══════════════

onMounted(() => {
  loadTab('containers')
  pollTimer = setInterval(silentRefresh, 10000)
})
onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
  stopLogsTimer()
})
</script>

<style scoped>
.docker-top {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}
.docker-tabs {
  flex: 1;
  min-width: 0;
}
.docker-tabs :deep(.el-tabs__header) {
  margin-bottom: 16px;
}
.docker-refresh {
  flex: none;
  margin-top: 2px;
}
/* 各 tab 的工具行：筛选/搜索/批量/主操作 + 计数 */
.pane-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.ct-state {
  width: 132px;
}
.ct-search {
  width: 220px;
}
.ct-sel {
  color: var(--el-color-primary, #409eff);
  font-size: 0.85rem;
}
/* 容器行内 7 个操作全部 text 化 + 收紧间距，保证 1440 宽下 CPU%/内存% 列不被固定列遮住 */
.ct-op .el-button + .el-button {
  margin-left: 6px;
}
.ct-count {
  margin-left: auto;
}
.ct-count-inline {
  margin-left: 0;
}
/* 开关 + 文字标签（容器工具栏「自动刷新」/ 日志抽屉「跟随」共用） */
.ct-auto {
  display: flex;
  align-items: center;
  gap: 6px;
}
.ct-auto-label {
  font-size: 0.85rem;
  color: var(--el-text-color-regular, #606266);
}
.logs-tail {
  width: 150px;
}
.logs-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.logs-hint {
  color: var(--el-text-color-secondary, #909399);
  font-size: 0.8rem;
}
.logs-pre {
  margin: 0;
  padding: 12px;
  min-height: 300px;
  max-height: calc(100vh - 220px);
  overflow: auto;
  background: #0d1b2a;
  color: #cfe8ff;
  border-radius: var(--radius-md, 8px);
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
  font-size: 0.8rem;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
  user-select: text;
}
.dialog-hint {
  margin: 4px 0 0;
  color: var(--el-text-color-secondary, #909399);
  font-size: 0.8rem;
}
</style>

<style>
/* 容器终端抽屉整体深色。抽屉挂载于 body 之下，scoped 选择器无法命中，须用全局样式块；
   class 落在 .el-drawer 面板根节点上，据此限定作用范围。 */
.term-drawer .el-drawer__body {
  height: calc(100% - 54px);
  padding: 0 12px 12px;
  background: #0d1b2a;
  overflow: hidden;
  box-sizing: border-box;
}
</style>
