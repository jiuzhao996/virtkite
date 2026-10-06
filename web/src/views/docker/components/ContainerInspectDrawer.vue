<template>
    <!-- 容器详情抽屉：docker inspect 结构化多 tab（概要/环境变量/端口/挂载/网络）+ 原始 JSON -->
    <el-drawer v-model="inspectDrawer" :title="'容器详情 — ' + inspectName" size="55%">
      <div v-loading="inspectLoading">
        <el-tabs v-model="activeTab">
          <el-tab-pane label="概要" name="summary">
            <el-descriptions :column="2" border size="small">
              <el-descriptions-item label="名称">
                <span class="mono">{{ vm.summary.name || '—' }}</span>
              </el-descriptions-item>
              <el-descriptions-item label="状态">
                <el-tag :type="stateTag(vm.summary.state)" effect="light" size="small">{{ stateText(vm.summary.state) }}</el-tag>
              </el-descriptions-item>
              <el-descriptions-item label="镜像" :span="2">
                <!-- 镜像名可点跳镜像页：容器↔镜像织网第一针 -->
                <router-link v-if="vm.summary.image" class="mono lk" :to="{ path: '/images' }">{{ vm.summary.image }}</router-link>
                <span v-else>—</span>
              </el-descriptions-item>
              <el-descriptions-item label="容器 ID" :span="2"><span class="mono">{{ shortId(vm.summary.id) }}</span></el-descriptions-item>
              <el-descriptions-item label="启动命令" :span="2"><span class="mono">{{ vm.summary.command || '—' }}</span></el-descriptions-item>
              <el-descriptions-item label="入口点" :span="2"><span class="mono">{{ vm.summary.entrypoint || '—' }}</span></el-descriptions-item>
              <el-descriptions-item label="重启策略">{{ vm.summary.restartPolicy || 'no' }}</el-descriptions-item>
              <el-descriptions-item label="网络模式">{{ vm.summary.networkMode || '—' }}</el-descriptions-item>
              <el-descriptions-item label="重启次数">{{ vm.summary.restartCount ?? '—' }}</el-descriptions-item>
              <el-descriptions-item label="退出码">{{ vm.summary.exitCode ?? '—' }}</el-descriptions-item>
              <el-descriptions-item label="创建时间" :span="2">{{ dockerTime(vm.summary.created) }}</el-descriptions-item>
            </el-descriptions>
          </el-tab-pane>

          <el-tab-pane :label="`环境变量（${vm.env.length}）`" name="env">
            <el-empty v-if="!vm.env.length" description="无环境变量" :image-size="70" />
            <el-table v-else :data="vm.env" size="small" stripe max-height="calc(100vh - 260px)">
              <el-table-column label="变量名" min-width="160"><template #default="{ row }"><span class="mono">{{ row.key }}</span></template></el-table-column>
              <el-table-column label="值" min-width="240"><template #default="{ row }"><span class="mono">{{ row.value }}</span></template></el-table-column>
              <el-table-column label="" width="50"><template #default="{ row }"><CopyButton :text="`${row.key}=${row.value}`" tip="复制" /></template></el-table-column>
            </el-table>
          </el-tab-pane>

          <el-tab-pane :label="`端口映射（${vm.ports.length}）`" name="ports">
            <el-empty v-if="!vm.ports.length" description="无端口映射" :image-size="70" />
            <el-table v-else :data="vm.ports" size="small" stripe>
              <el-table-column label="映射" min-width="200"><template #default="{ row }"><span class="mono">{{ portText(row) }}</span></template></el-table-column>
              <el-table-column label="" width="50"><template #default="{ row }"><CopyButton :text="portCopyText(row)" tip="复制 host:port" /></template></el-table-column>
            </el-table>
          </el-tab-pane>

          <el-tab-pane :label="`挂载（${vm.mounts.length}）`" name="mounts">
            <el-empty v-if="!vm.mounts.length" description="无挂载" :image-size="70" />
            <el-table v-else :data="vm.mounts" size="small" stripe>
              <el-table-column label="类型" width="80"><template #default="{ row }">{{ row.type }}</template></el-table-column>
              <el-table-column label="源" min-width="180"><template #default="{ row }"><span class="mono">{{ row.source }}</span></template></el-table-column>
              <el-table-column label="目标" min-width="180"><template #default="{ row }"><span class="mono">{{ row.destination }}</span></template></el-table-column>
              <el-table-column label="读写" width="70"><template #default="{ row }"><el-tag size="small" :type="row.rw ? 'success' : 'warning'" effect="light">{{ row.mode }}</el-tag></template></el-table-column>
            </el-table>
          </el-tab-pane>

          <el-tab-pane :label="`网络（${vm.networks.length}）`" name="networks">
            <el-empty v-if="!vm.networks.length" description="无网络" :image-size="70" />
            <el-table v-else :data="vm.networks" size="small" stripe>
              <el-table-column label="网络" min-width="120"><template #default="{ row }"><span class="mono">{{ row.name }}</span></template></el-table-column>
              <el-table-column label="IP" min-width="120"><template #default="{ row }"><span class="mono">{{ row.ip || '—' }}</span></template></el-table-column>
              <el-table-column label="网关" min-width="120"><template #default="{ row }"><span class="mono">{{ row.gateway || '—' }}</span></template></el-table-column>
              <el-table-column label="MAC" min-width="140"><template #default="{ row }"><span class="mono">{{ row.mac || '—' }}</span></template></el-table-column>
            </el-table>
          </el-tab-pane>

          <el-tab-pane label="原始 JSON" name="raw">
            <div class="logs-toolbar">
              <el-button size="small" :icon="Refresh" :loading="inspectLoading" @click="fetchInspect">刷新</el-button>
              <CopyButton :text="inspectText" tip="复制详情 JSON" success-msg="详情 JSON 已复制到剪贴板" />
            </div>
            <pre class="logs-pre">{{ inspectText || '（暂无数据）' }}</pre>
          </el-tab-pane>
        </el-tabs>
      </div>
    </el-drawer>
</template>

<script setup>
// 容器详情抽屉：结构化展示 docker inspect（解析逻辑在 utils/docker-inspect.js，R3 详情抽屉复用），
// 原始 JSON 折叠保留在末 tab。经 open(row) 由容器表格行触发。
import { ref, computed } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import CopyButton from '../../../components/CopyButton.vue'
import { api } from '../../../api'
import { errMsg } from '../../../utils/format'
import { containerName, stateTag, stateText, dockerTime, shortId } from '../../../utils/docker-format'
import { parseInspect, portText, portCopyText } from '../../../utils/docker-inspect'

const inspectDrawer = ref(false)
const inspectLoading = ref(false)
const inspectText = ref('')
const inspectName = ref('')
const inspectId = ref('')
const activeTab = ref('summary')
const raw = ref({})

const vm = computed(() => parseInspect(raw.value))

function open(row) {
  inspectId.value = row.ID
  inspectName.value = containerName(row.Names)
  inspectText.value = ''
  raw.value = {}
  activeTab.value = 'summary'
  inspectDrawer.value = true
  fetchInspect()
}

async function fetchInspect() {
  if (!inspectId.value) return
  inspectLoading.value = true
  try {
    const res = await api.dockerContainerInspect(inspectId.value)
    raw.value = res.data ?? {}
    inspectText.value = JSON.stringify(res.data ?? {}, null, 2)
  } catch (e) {
    ElMessage.error(errMsg(e, '获取容器详情失败'))
  } finally {
    inspectLoading.value = false
  }
}

defineExpose({ open })
</script>

<style scoped>
.logs-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.logs-pre {
  margin: 0;
  padding: 12px;
  min-height: 300px;
  max-height: calc(100vh - 260px);
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
.lk {
  color: var(--el-color-primary);
  text-decoration: none;
}
.lk:hover {
  text-decoration: underline;
}
</style>
