<template>
  <!-- node_exporter 抓取目标预览卡（file_sd 服务发现闭环；只读预览 + 两态说明） -->
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
          <el-button :icon="Refresh" @click="emit('refresh')">刷新</el-button>
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
</template>

<script setup>
// 监控中心「node_exporter 抓取目标」卡：只读预览平台将下发为 Prometheus file_sd 的目标
// （与后端 StartFileSDWriter 落盘内容同源）。数据由页面 loadFileSD 拉取后传入，仅 @refresh 上抛。
import { Aim, Refresh } from '@element-plus/icons-vue'

defineProps({
  // 抓取目标数组（{ targets:[ip:port], labels:{vm_name,vm_id,job} }）
  sdTargets: { type: Array, default: () => [] },
  // FILE_SD_PATH 是否已配置（决定标签文案与两态说明）
  sdEnabled: { type: Boolean, default: false },
  sdLoading: { type: Boolean, default: false }
})
const emit = defineEmits(['refresh'])
</script>

<style scoped>
/* 与 Monitor.vue / RealtimeAlertsCard.vue / AlertHistoryCard.vue 各留一份（scoped 不跨组件）——
   改一处须同步 */
.alert-card {
  margin-bottom: 16px;
}
.alert-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-weight: 600;
}
.alert-head-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: normal;
  flex-wrap: wrap;
  justify-content: flex-end;
}
.head-icon {
  vertical-align: -0.15em;
  margin-right: 4px;
  color: var(--el-color-primary);
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
</style>
