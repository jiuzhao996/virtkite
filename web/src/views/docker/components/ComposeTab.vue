<template>
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
            size="small" text type="success"
            :loading="composeKey === row.Name + ':start'"
            :disabled="composeKey !== ''"
            @click="composeAction(row, 'start')"
          >启动</el-button>
          <el-button
            size="small" text type="warning"
            :loading="composeKey === row.Name + ':stop'"
            :disabled="(row.Status || '').indexOf('running') !== 0 || composeKey !== ''"
            @click="composeAction(row, 'stop')"
          >停止</el-button>
          <el-button
            size="small" text type="primary"
            :loading="composeKey === row.Name + ':restart'"
            :disabled="(row.Status || '').indexOf('running') !== 0 || composeKey !== ''"
            @click="composeAction(row, 'restart')"
          >重启</el-button>
          <el-button
            size="small" text type="danger"
            :loading="composeKey === row.Name + ':down'"
            :disabled="composeKey !== ''"
            @click="composeAction(row, 'down')"
          >下线</el-button>
        </template>
      </el-table-column>
    </el-table>
</template>

<script setup>
// 编排页（原编排 tab，1Panel 式子路由化）：compose 项目列表与项目级操作自持；
// 取数失败经 inject('dockerPage') 上报布局壳（503 置门控 alert，其余 toast），操作成功后本地 refresh 重拉。
import { ref, inject, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '../../../api'
import { errMsg, isCancel } from '../../../utils/format'
import { composeTag } from '../../../utils/docker-format'

// 布局壳通信：失败上报 / 成功清 503 门控
const { reportLoadError, clearLoadError } = inject('dockerPage')

const composeProjects = ref([])
const loading = ref(false)

// 首次挂载 / 壳刷新按钮 / 操作成功后 共用的重拉入口
async function refresh() {
  loading.value = true
  try {
    const res = await api.dockerComposeList()
    composeProjects.value = (res.data || {}).items || []
    clearLoadError()
  } catch (e) {
    reportLoadError(e, '获取编排项目失败')
  } finally {
    loading.value = false
  }
}

onMounted(refresh)
defineExpose({ refresh })

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
    // 项目级操作可能重建多个容器（后端上限 2 分钟），api.dockerComposeAction 已放宽超时
    const res = await api.dockerComposeAction(row.Name, action)
    ElMessage.success((res.data && res.data.message) || '操作完成')
    await refresh()
  } catch (e) {
    ElMessage.error(errMsg(e, '编排操作失败'))
  } finally {
    composeKey.value = ''
  }
}
</script>

<style scoped>
/* 各 tab 的工具行：筛选/搜索/批量/主操作 + 计数 */
.pane-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.ct-count-inline {
  margin-left: 0;
}
</style>
