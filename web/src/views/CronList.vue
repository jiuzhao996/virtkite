<template>
  <div>
    <div class="page-head">
      <div>
        <h3 class="page-title">计划任务</h3>
        <p class="page-desc">按 cron 表达式定时执行虚拟机快照、数据库备份等例行运维动作</p>
      </div>
    </div>

    <el-card shadow="never">
      <div class="toolbar">
        <span class="count">共 {{ items.length }} 个任务</span>
        <el-button type="primary" :icon="Plus" @click="openCreate">新建任务</el-button>
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
      </div>

      <el-table v-loading="loading" :data="items" stripe size="small">
        <template #empty>
          <el-empty description="暂无计划任务，点击新建创建第一个定时任务" :image-size="80" />
        </template>
        <el-table-column prop="name" label="名称" min-width="150" show-overflow-tooltip />
        <el-table-column label="表达式" width="130">
          <template #default="{ row }">
            <span class="mono">{{ row.cron_expr }}</span>
          </template>
        </el-table-column>
        <el-table-column label="动作" width="120">
          <template #default="{ row }">
            <el-tag :type="actionTag(row.action)" effect="light" size="small">{{ actionText(row.action) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="参数" min-width="150" show-overflow-tooltip>
          <template #default="{ row }">{{ paramsText(row) }}</template>
        </el-table-column>
        <el-table-column label="启用" width="80">
          <template #default="{ row }">
            <el-switch
              :model-value="row.enabled"
              :disabled="togglingId === row.id"
              @change="toggleRow(row)"
            />
          </template>
        </el-table-column>
        <el-table-column label="上次执行" width="170">
          <template #default="{ row }">
            <span class="mono">{{ row.last_run ? fmtDateTime(row.last_run) : '从未执行' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="下次执行" width="170">
          <template #default="{ row }">
            <span class="mono">{{ row.next_run ? fmtDateTime(row.next_run) : '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="run_count" label="次数" width="70" align="center" />
        <el-table-column label="操作" width="190" fixed="right">
          <template #default="{ row }">
            <el-button
              size="small"
              type="success"
              plain
              :loading="runningId === row.id"
              :disabled="!!runningId && runningId !== row.id"
              @click="runNow(row)"
            >立即运行</el-button>
            <el-button size="small" text type="primary" @click="openEdit(row)">编辑</el-button>
            <el-button size="small" text type="danger" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 新建 / 编辑（复用一个弹窗：editingId 区分模式） -->
    <el-dialog v-model="dialog" :title="editingId ? '编辑计划任务' : '新建计划任务'" width="480px">
      <el-form label-width="90px">
        <el-form-item label="任务名称" required>
          <el-input v-model="form.name" placeholder="如：每晚上报 node1 快照" maxlength="100" />
        </el-form-item>
        <el-form-item label="表达式" required>
          <div class="expr-row">
            <el-input v-model="form.cron_expr" placeholder="分 时 日 月 周，如 0 2 * * *" class="mono" />
            <el-select v-model="preset" placeholder="常用预设" style="width: 150px" clearable @change="applyPreset">
              <el-option v-for="p in PRESETS" :key="p.expr" :label="p.label" :value="p.expr" />
            </el-select>
          </div>
          <div class="field-tip">5 个字段：分 时 日 月 周；示例「0 2 * * *」= 每天 02:00</div>
        </el-form-item>
        <el-form-item label="动作" required>
          <el-radio-group v-model="form.action">
            <el-radio value="vm_snapshot">虚拟机快照</el-radio>
            <el-radio value="db_backup">数据库备份</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="form.action === 'vm_snapshot'" label="目标虚拟机" required>
          <el-select v-model="form.vm_id" placeholder="选择要定时快照的虚拟机" style="width: 100%" v-loading="vmsLoading">
            <el-option v-for="vm in vms" :key="vm.id" :label="vmName(vm)" :value="vm.id" />
          </el-select>
          <div class="field-tip">保存后参数自动生成为 {{ snapshotParamsPreview }}</div>
        </el-form-item>
        <el-form-item v-else-if="form.action === 'db_backup'" label="说明">
          <div class="field-tip">备份数据库到主机 backup 目录，保留最近 7 份</div>
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="form.enabled" active-text="启用" inactive-text="停用" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">{{ editingId ? '保存' : '创建' }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import http from '../api'
import { errMsg, isCancel, fmtDateTime, vmStatusText } from '../utils/format'

// ===== 列表 =====
const items = ref([])
const loading = ref(false)

// 动作 → 中文 / tag 颜色
function actionText(a) {
  return { vm_snapshot: '虚拟机快照', db_backup: '数据库备份' }[a] || a
}
function actionTag(a) {
  return { vm_snapshot: 'primary', db_backup: 'success' }[a] || 'info'
}

// 参数列：JSON 字符串解析后给中文摘要（vm_id 尽量翻译成虚拟机名）
function paramsText(row) {
  let obj = null
  try {
    obj = JSON.parse(row.params || '{}')
  } catch (e) {
    return row.params || '—'
  }
  if (!obj || typeof obj !== 'object') return String(row.params)
  if (obj.vm_id != null) {
    const vm = vms.value.find((v) => v.id === obj.vm_id)
    return vm ? `虚拟机：${vm.name}` : `vm_id: ${obj.vm_id}`
  }
  const keys = Object.keys(obj)
  return keys.length === 0 ? '（无参数）' : JSON.stringify(obj)
}

async function load() {
  loading.value = true
  try {
    const res = await http.get('/crons')
    items.value = (res.data.data && res.data.data.items) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '获取计划任务列表失败'))
  } finally {
    loading.value = false
  }
}

// ===== VM 下拉数据（快照目标选择 + 参数翻译） =====
const vms = ref([])
const vmsLoading = ref(false)

function vmName(vm) {
  return vm.status ? `${vm.name}（${vmStatusText(vm.status)}）` : vm.name
}

async function loadVMs() {
  vmsLoading.value = true
  try {
    const res = await http.get('/vms')
    vms.value = (res.data.data && res.data.data.items) || []
  } catch (e) {
    ElMessage.error(errMsg(e, '获取虚拟机列表失败'))
  } finally {
    vmsLoading.value = false
  }
}

// ===== 新建 / 编辑 =====
const dialog = ref(false)
const saving = ref(false)
const editingId = ref(null)
const preset = ref('')

// 常用 cron 预设（label 与 expr 成对，选中即回填表达式输入框）
const PRESETS = [
  { label: '每 5 分钟', expr: '*/5 * * * *' },
  { label: '每小时', expr: '0 * * * *' },
  { label: '每天 02:00', expr: '0 2 * * *' },
  { label: '每周日 03:00', expr: '0 3 * * 0' },
  { label: '每月 1 日 04:00', expr: '0 4 1 * *' }
]

const form = ref({ name: '', cron_expr: '', action: 'vm_snapshot', vm_id: null, enabled: true })

// vm_snapshot 时参数预览：未选虚拟机用 ? 占位，选中后即最终提交的 JSON 字符串
const snapshotParamsPreview = computed(() =>
  JSON.stringify({ vm_id: form.value.vm_id == null ? '?' : form.value.vm_id })
)

function applyPreset(expr) {
  if (expr) form.value.cron_expr = expr
}

function openCreate() {
  editingId.value = null
  preset.value = ''
  form.value = { name: '', cron_expr: '', action: 'vm_snapshot', vm_id: null, enabled: true }
  dialog.value = true
  if (vms.value.length === 0) loadVMs()
}

function openEdit(row) {
  editingId.value = row.id
  preset.value = ''
  let vmId = null
  try {
    const obj = JSON.parse(row.params || '{}')
    if (obj && obj.vm_id != null) vmId = obj.vm_id
  } catch (e) {
    // 旧数据 params 非法时按空处理，保存时会重新生成
  }
  form.value = {
    name: row.name,
    cron_expr: row.cron_expr,
    action: row.action,
    vm_id: vmId,
    enabled: !!row.enabled
  }
  dialog.value = true
  if (vms.value.length === 0) loadVMs()
}

async function save() {
  if (!form.value.name.trim()) return ElMessage.warning('请填写任务名称')
  if (!form.value.cron_expr.trim()) return ElMessage.warning('请填写 cron 表达式（或选择常用预设）')
  if (form.value.action === 'vm_snapshot' && form.value.vm_id == null) {
    return ElMessage.warning('请选择要快照的虚拟机')
  }
  // params 是 JSON 字符串：vm_snapshot 带 {"vm_id":N}，db_backup 空对象
  const params = form.value.action === 'vm_snapshot'
    ? JSON.stringify({ vm_id: form.value.vm_id })
    : '{}'
  const payload = {
    name: form.value.name.trim(),
    cron_expr: form.value.cron_expr.trim(),
    action: form.value.action,
    params,
    enabled: form.value.enabled
  }
  saving.value = true
  try {
    if (editingId.value) {
      // 表达式解析错误等校验失败，后端返回 400 + 中文 message，errMsg 直接透出
      await http.put('/crons/' + editingId.value, payload)
      ElMessage.success('已保存')
    } else {
      await http.post('/crons', payload)
      ElMessage.success('已创建')
    }
    dialog.value = false
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '保存失败'))
  } finally {
    saving.value = false
  }
}

// ===== 启用开关（:model-value 不绑 v-model，失败时状态不翻转） =====
const togglingId = ref(null)

async function toggleRow(row) {
  togglingId.value = row.id
  try {
    await http.post('/crons/' + row.id + '/toggle')
    row.enabled = !row.enabled
    ElMessage.success(row.enabled ? '已启用' : '已停用')
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '切换失败'))
  } finally {
    togglingId.value = null
  }
}

// ===== 立即运行 =====
const runningId = ref(null)

async function runNow(row) {
  runningId.value = row.id
  try {
    await http.post('/crons/' + row.id + '/run')
    ElMessage.success(`已触发「${row.name}」执行`)
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '触发失败'))
  } finally {
    runningId.value = null
  }
}

// ===== 删除 =====
async function remove(row) {
  try {
    await ElMessageBox.confirm(`确定删除计划任务「${row.name}」？删除后不再定时执行。`, '删除任务', {
      type: 'warning',
      confirmButtonText: '删除',
      confirmButtonClass: 'el-button--danger'
    })
  } catch (e) {
    if (!isCancel(e)) ElMessage.error(errMsg(e, '操作失败'))
    return
  }
  try {
    await http.delete('/crons/' + row.id)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    ElMessage.error(errMsg(e, '删除失败'))
  }
}

onMounted(() => {
  load()
  // 参数列要把 vm_id 翻译成虚拟机名，列表数据里没有，进页面就拉一份
  loadVMs()
})
</script>

<style scoped>
.expr-row {
  display: flex;
  gap: 8px;
  width: 100%;
}
.field-tip {
  font-size: 0.78rem;
  color: var(--el-text-color-secondary, #909399);
  line-height: 1.5;
  margin-top: 4px;
}
</style>
