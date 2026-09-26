<template>
  <div>
    <div class="page-head">
      <div>
        <h2 class="page-title">资产申请</h2>
        <span class="page-desc">{{ isAdmin ? '教师审批台：处理学生的资产授权申请' : '向教师申请虚拟机的使用授权，批准后即可在「虚拟机」列表看到并操作' }}</span>
      </div>
      <el-button :icon="Refresh" :loading="loading" @click="loadAll">刷新</el-button>
    </div>

    <!-- 学生：申请入口（目录 = 全部运行中虚拟机的花名册，不含 IP 等敏感信息） -->
    <el-card v-if="!isAdmin" shadow="never" class="mb">
      <template #header>
        <span class="card-title">发起申请</span>
      </template>
      <div class="apply-form">
        <el-select v-model="applyForm.vmId" filterable placeholder="选择要申请的虚拟机" style="width: 280px">
          <el-option v-for="v in catalog" :key="v.id" :label="v.name" :value="v.id" />
        </el-select>
        <el-select v-model="applyForm.hours" style="width: 150px">
          <el-option v-for="h in hourOptions" :key="h" :label="'申请 ' + h + ' 小时'" :value="h" />
        </el-select>
        <el-button type="primary" :disabled="!applyForm.vmId || !applyForm.reason.trim()" :loading="applying" @click="submitApply">
          提交申请
        </el-button>
      </div>
      <el-input
        v-model="applyForm.reason"
        type="textarea"
        :rows="2"
        maxlength="200"
        show-word-limit
        placeholder="申请理由（至少 5 个字），如：网络 2401 班实验作业，需要一台 Rocky 虚拟机练习 systemctl"
        style="margin-top: 10px"
      />
    </el-card>

    <!-- 学生：我的申请记录 -->
    <el-card v-if="!isAdmin" shadow="never" class="mb">
      <template #header>
        <span class="card-title">我的申请</span>
      </template>
      <el-table :data="mine" v-loading="loading" stripe size="small">
        <template #empty><el-empty description="还没有申请记录" :image-size="70" /></template>
        <el-table-column prop="vm_name" label="虚拟机" min-width="120" />
        <el-table-column prop="reason" label="理由" min-width="220" show-overflow-tooltip />
        <el-table-column label="时长" width="90">
          <template #default="{ row }">{{ row.hours }} 小时</template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="statusTag(row.status)" effect="light" size="small">{{ statusText(row.status) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="审批意见" min-width="180">
          <template #default="{ row }">{{ row.decide_note || '—' }}</template>
        </el-table-column>
        <el-table-column label="申请时间" width="170">
          <template #default="{ row }"><span class="mono">{{ fmtDateTime(row.created_at) }}</span></template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 教师：审批队列 -->
    <el-card shadow="never">
      <template #header>
        <div class="card-head">
          <span class="card-title">
            审批队列
            <el-tag v-if="pendingCount" type="danger" effect="light" size="small" style="margin-left: 8px">{{ pendingCount }} 条待处理</el-tag>
          </span>
          <el-radio-group v-model="statusFilter" size="small" @change="loadRequests">
            <el-radio-button value="pending">待审批</el-radio-button>
            <el-radio-button value="approved">已批准</el-radio-button>
            <el-radio-button value="rejected">已驳回</el-radio-button>
            <el-radio-button value="">全部</el-radio-button>
          </el-radio-group>
        </div>
      </template>
      <el-table :data="requests" v-loading="loading" stripe size="small">
        <template #empty><el-empty :description="statusFilter === 'pending' ? '没有待审批的申请' : '暂无记录'" :image-size="70" /></template>
        <el-table-column prop="username" label="申请人" width="110" />
        <el-table-column prop="vm_name" label="虚拟机" min-width="110" />
        <el-table-column prop="reason" label="理由" min-width="220" show-overflow-tooltip />
        <el-table-column label="时长" width="90">
          <template #default="{ row }">{{ row.hours }} 小时</template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="statusTag(row.status)" effect="light" size="small">{{ statusText(row.status) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="审批意见" min-width="160">
          <template #default="{ row }">{{ row.decide_note || '—' }}</template>
        </el-table-column>
        <el-table-column label="申请时间" width="160">
          <template #default="{ row }"><span class="mono">{{ fmtDateTime(row.created_at) }}</span></template>
        </el-table-column>
        <el-table-column v-if="statusFilter === 'pending'" label="操作" width="150" fixed="right">
          <template #default="{ row }">
            <el-button text type="primary" size="small" @click="openApprove(row)">批准</el-button>
            <el-button text type="danger" size="small" @click="openReject(row)">驳回</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { api } from '../api'
import { useAuth } from '../store/auth'
import { errMsg, fmtDateTime } from '../utils/format'

const { isAdmin } = useAuth()
const loading = ref(false)
const catalog = ref([])
const mine = ref([])
const requests = ref([])
const statusFilter = ref('pending')
const applying = ref(false)
const applyForm = ref({ vmId: null, hours: 2, reason: '' })
const hourOptions = [2, 4, 8, 24, 72, 168]

const pendingCount = computed(() => requests.value.filter((r) => r.status === 'pending').length)
const statusText = (s) => ({ pending: '待审批', approved: '已批准', rejected: '已驳回' }[s] || s)
const statusTag = (s) => ({ pending: 'warning', approved: 'success', rejected: 'info' }[s] || 'info')

async function loadAll() {
  loading.value = true
  try {
    if (isAdmin.value) {
      await loadRequests()
    } else {
      const [cRes, mRes] = await Promise.all([api.listApplyCatalog(), api.listMyRequests()])
      catalog.value = (cRes.data && cRes.data.items) || []
      mine.value = (mRes.data && mRes.data.items) || []
    }
  } catch (e) {
    ElMessage.error(errMsg(e, '加载失败'))
  } finally {
    loading.value = false
  }
}

async function loadRequests() {
  try {
    const res = await api.listGrantRequests(statusFilter.value ? { status: statusFilter.value } : {})
    requests.value = (res.data && res.data.items) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '获取申请队列失败'))
  }
}

async function submitApply() {
  if (applyForm.value.reason.trim().length < 5) {
    ElMessage.warning('申请理由至少 5 个字，请写明用途')
    return
  }
  applying.value = true
  try {
    const res = await api.applyForAsset(applyForm.value.vmId, {
      reason: applyForm.value.reason.trim(),
      hours: applyForm.value.hours
    })
    ElMessage.success((res.data && res.data.message) || '申请已提交')
    applyForm.value = { vmId: null, hours: 2, reason: '' }
    const mRes = await api.listMyRequests()
    mine.value = (mRes.data && mRes.data.items) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '提交失败'))
  } finally {
    applying.value = false
  }
}

async function openApprove(row) {
  let hours = String(row.hours)
  try {
    const res = await ElMessageBox.prompt(
      `批准 ${row.username} 对「${row.vm_name}」的授权申请。可调整授权时长（小时）。`,
      '批准申请',
      { inputValue: hours, inputPattern: /^\d+$/, inputErrorMessage: '请输入数字小时数', confirmButtonText: '批准' }
    )
    hours = res.value
  } catch {
    return
  }
  try {
    const res = await api.approveGrantRequest(row.id, { hours: Number(hours) })
    ElMessage.success((res.data && res.data.message) || '已批准')
    loadRequests()
  } catch (e) {
    ElMessage.error(errMsg(e, '批准失败'))
  }
}

async function openReject(row) {
  let note = ''
  try {
    const res = await ElMessageBox.prompt(`驳回 ${row.username} 对「${row.vm_name}」的申请。请填写驳回原因（学生可见）。`, '驳回申请', {
      inputPlaceholder: '如：实验机数量有限，请下课后找老师单独安排',
      confirmButtonText: '驳回',
      confirmButtonClass: 'el-button--danger'
    })
    note = res.value
  } catch {
    return
  }
  try {
    await api.rejectGrantRequest(row.id, { note })
    ElMessage.success('已驳回')
    loadRequests()
  } catch (e) {
    ElMessage.error(errMsg(e, '驳回失败'))
  }
}

onMounted(loadAll)
</script>

<style scoped>
.mb {
  margin-bottom: 16px;
}
.apply-form {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
</style>
