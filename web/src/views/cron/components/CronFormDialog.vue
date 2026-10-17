<template>
  <!-- 新建 / 编辑（复用一个弹窗：editingId 区分模式） -->
  <el-dialog :close-on-click-modal="false" v-model="visible" :title="editingId ? '编辑计划任务' : '新建计划任务'" width="480px">
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
        <!-- 下次执行预览：输入停顿 500ms 后调后端解析，非法表达式给红色提示 -->
        <div v-if="cronPreview.state === 'invalid'" class="cron-preview cron-preview-invalid">表达式无效</div>
        <div v-else-if="cronPreview.state === 'ok' && cronPreview.times.length" class="cron-preview cron-preview-ok">
          下次执行：{{ cronPreview.times.join('、') }}
        </div>
      </el-form-item>
      <el-form-item label="动作" required>
        <el-radio-group v-model="form.action">
          <el-radio value="vm_snapshot">虚拟机快照</el-radio>
          <el-radio value="db_backup">数据库备份</el-radio>
          <el-radio value="ansible_playbook">Playbook 执行</el-radio>
          <el-radio value="container_healthcheck">容器健康巡检</el-radio>
          <el-radio value="image_version_check">镜像版本巡检</el-radio>
          <el-radio value="designer_drift_check">架构漂移巡检</el-radio>
        </el-radio-group>
      </el-form-item>
      <el-form-item v-if="form.action === 'vm_snapshot'" label="目标虚拟机" required>
        <el-select v-model="form.vm_id" placeholder="选择要定时快照的虚拟机" style="width: 100%" v-loading="vmsLoading">
          <el-option v-for="vm in vms" :key="vm.id" :label="vmName(vm)" :value="vm.id" />
        </el-select>
        <div class="field-tip">保存后参数自动生成为 {{ snapshotParamsPreview }}</div>
      </el-form-item>
      <el-form-item v-else-if="form.action === 'ansible_playbook'" label="Playbook" required>
        <el-select v-model="form.pb_playbook" filterable placeholder="选择要定时执行的 playbook" style="width: 100%" v-loading="pbLoading">
          <el-option v-for="p in playbooks" :key="p.id" :label="p.id + (p.desc ? '（' + p.desc + '）' : '')" :value="p.id" />
        </el-select>
        <div class="field-tip">执行走 ansible_run 任务管线（凭据/输出/RECAP 全在任务中心可查）</div>
      </el-form-item>
      <el-form-item v-if="form.action === 'ansible_playbook'" label="目标方式">
        <el-radio-group v-model="form.pb_target_mode">
          <el-radio-button value="vms">按虚拟机</el-radio-button>
          <el-radio-button value="group">按主机组（动态）</el-radio-button>
        </el-radio-group>
      </el-form-item>
      <!-- 目标方式与下面的主机组/虚拟机选择必须属于同一条 v-if 链：
           上一版把「主机组」「目标虚拟机」写成 v-else-if，却接在「目标方式」的
           v-if="action === 'ansible_playbook'" 后面——条件被前项完全覆盖，
           两个分支永远不渲染，选 Playbook 时目标根本填不了。
           这里让「主机组」另起一条链（v-if），「目标虚拟机」作为它的 v-else-if。 -->
      <el-form-item v-if="form.action === 'ansible_playbook' && form.pb_target_mode === 'group'" label="主机组" required>
        <el-select v-model="form.pb_group_id" placeholder="选择主机组" style="width: 100%">
          <el-option v-for="g in hostGroups" :key="g.id" :label="g.name" :value="g.id" />
        </el-select>
        <div class="field-tip">成员在每次触发时动态解析，改组即生效（不存字面 targets）</div>
      </el-form-item>
      <el-form-item v-else-if="form.action === 'ansible_playbook'" label="目标虚拟机" required>
        <el-select v-model="form.pb_targets" multiple filterable placeholder="选择运行中的虚拟机（可多选）" style="width: 100%" v-loading="vmsLoading">
          <el-option v-for="vm in runnableVMs" :key="vm.id" :label="vmName(vm)" :value="vm.id" />
        </el-select>
        <div class="field-tip">执行时逐台复核：需运行中、有 IP、已保存托管凭据</div>
      <div v-if="hostGroups.length" class="group-chips">
            <span class="group-chips-label">主机组</span>
            <el-tag v-for="g in hostGroups" :key="g.id" class="group-chip" effect="plain"
              :title="'点击并入「' + g.name + '」组成员'" @click="applyPbGroup(g)">{{ g.name }}</el-tag>
          </div>
        </el-form-item>
      <el-form-item v-else-if="form.action === 'db_backup'" label="说明">
        <div class="field-tip">备份数据库到主机 backup 目录，按保留份数自动清理旧备份</div>
      </el-form-item>
      <el-form-item v-else-if="form.action === 'container_healthcheck'" label="说明">
        <div class="field-tip">巡检所有运行中容器，unhealthy 且连续失败 ≥3 次时自动重启并通知（需容器创建时配置了健康检查）</div>
      </el-form-item>
      <el-form-item v-else-if="form.action === 'image_version_check'" label="说明">
        <div class="field-tip">对比本地镜像与远端仓库 digest，有更新时推送镜像名列表（远端查询秒级，需外网可达）</div>
      </el-form-item>
      <el-form-item v-else-if="form.action === 'designer_drift_check'" label="说明">
        <div class="field-tip">巡检全部「已成功落地过」的设计器计划：对比落地快照与平台现实（虚拟机/容器栈/网络），有漂移时推送明细。无快照的计划自动跳过</div>
      </el-form-item>
      <el-form-item label="保留份数">
        <el-input-number v-model="form.keep" :min="1" :max="365" controls-position="right" style="width: 160px" />
        <div class="field-tip">快照 / 备份只保留最近 N 份，超出后自动清理最旧的</div>
      </el-form-item>
      <el-form-item label="死开关宽限">
        <el-input-number v-model="form.grace_minutes" :min="0" :max="1440" controls-position="right" style="width: 160px" />
        <div class="field-tip">超过预期执行时刻 N 分钟仍无执行记录即告警（0 表示关闭）</div>
      </el-form-item>
      <el-form-item label="失败重试">
        <el-input-number v-model="form.retry_count" :min="0" :max="3" controls-position="right" style="width: 120px" />
        <span class="retry-sep">次，间隔</span>
        <el-input-number v-model="form.retry_interval" :min="1" :max="120" controls-position="right" style="width: 120px" />
        <span class="retry-sep">秒</span>
        <div class="field-tip">执行失败后自动重试（0 次表示不重试）</div>
      </el-form-item>
      <el-form-item label="链式编排">
        <el-select v-model="form.on_success_task_id" clearable placeholder="成功后执行（可选）" style="width: 210px">
          <el-option v-for="t in otherTasks" :key="t.id" :label="t.name" :value="t.id" />
        </el-select>
        <el-select v-model="form.on_failure_task_id" clearable placeholder="失败后执行（可选）" style="width: 210px; margin-left: 8px">
          <el-option v-for="t in otherTasks" :key="t.id" :label="t.name" :value="t.id" />
        </el-select>
        <div class="field-tip">按结果触发下一跳（最多 5 级防环）；下一跳 params 里可用 {prev.output} 引用本次结果</div>
      </el-form-item>
      <el-form-item label="启用">
        <el-switch v-model="form.enabled" active-text="启用" inactive-text="停用" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="saving" @click="emit('save')">{{ editingId ? '保存' : '创建' }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
// 计划任务「新建 / 编辑」弹窗（editingId 区分模式，共用一个表单）。
// form 为页面持有的对象，直接 v-model 改其字段；预设回填/主机组并入等纯 UI 逻辑随表单下沉；
// 提交动作 emit('save') 仍由页面执行（组装 params + 调 api.cronCreate/Update）。
// preset（常用预设选中值）与 cronPreview（下次执行预览）由页面持有并传入：它们跟着
// openCreate/openEdit 复位、且被页面的 debounce watch 驱动，状态源留在页面更清晰。
import { computed } from 'vue'
import { ElMessage } from 'element-plus'
import { vmStatusText } from '../../../utils/format'

const props = defineProps({
  // 表单对象（页面 form.value 传入，直接改字段）
  form: { type: Object, required: true },
  // 当前编辑的任务 id（null=新建），只用于标题与按钮文案
  editingId: { type: [Number, String], default: null },
  // cron 表达式「下次执行」预览 { state, times }
  cronPreview: { type: Object, default: () => ({ state: 'idle', times: [] }) },
  // 各下拉数据源
  vms: { type: Array, default: () => [] },
  vmsLoading: { type: Boolean, default: false },
  playbooks: { type: Array, default: () => [] },
  pbLoading: { type: Boolean, default: false },
  hostGroups: { type: Array, default: () => [] },
  // 链式下一跳候选（页面已排除自身）
  otherTasks: { type: Array, default: () => [] },
  saving: { type: Boolean, default: false }
})
const emit = defineEmits(['save'])
// 弹窗开关（父级 v-model="dialog"）
const visible = defineModel({ type: Boolean, default: false })
// 常用预设选中值（父级 v-model:preset="preset"，openCreate/openEdit 会复位为空）
const preset = defineModel('preset', { type: String, default: '' })

// 常用 cron 预设（label 与 expr 成对，选中即回填表达式输入框）
const PRESETS = [
  { label: '每 5 分钟', expr: '*/5 * * * *' },
  { label: '每小时', expr: '0 * * * *' },
  { label: '每天 02:00', expr: '0 2 * * *' },
  { label: '每周日 03:00', expr: '0 3 * * 0' },
  { label: '每月 1 日 04:00', expr: '0 4 1 * *' }
]

function applyPreset(expr) {
  if (expr) props.form.cron_expr = expr
}

function vmName(vm) {
  return vm.status ? `${vm.name}（${vmStatusText(vm.status)}）` : vm.name
}

// 仅「运行中且有 IP」的虚拟机可作为 playbook 目标（执行时后端还会逐台复核）
const runnableVMs = computed(() => props.vms.filter((v) => v.status === 'running' && v.ip))

// vm_snapshot 时参数预览：未选虚拟机用 ? 占位，选中后即最终提交的 JSON 字符串
const snapshotParamsPreview = computed(() =>
  JSON.stringify({ vm_id: props.form.vm_id == null ? '?' : props.form.vm_id })
)

// 主机组（AU1）：把组内可执行成员并入 playbook 目标多选
function applyPbGroup(g) {
  const pick = (g.vm_ids || []).filter((id) => runnableVMs.value.some((v) => v.id === id))
  if (!pick.length) return ElMessage.warning(`「${g.name}」当前没有可执行成员（需运行中且有 IP）`)
  props.form.pb_targets = [...new Set([...props.form.pb_targets, ...pick])]
  ElMessage.success(`已并入「${g.name}」${pick.length} 台`)
}
</script>

<style scoped>
/* 以下规则自 CronList.vue 原样搬入（类名不变，DOM 不变） */
.expr-row {
  display: flex;
  gap: 8px;
  width: 100%;
}
.cron-preview {
  font-size: 0.78rem;
  line-height: 1.5;
  margin-top: 4px;
}
.cron-preview-ok {
  color: var(--color-success, #16a34a);
}
.cron-preview-invalid {
  color: var(--el-color-danger);
}
.group-chips {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 4px;
}
.group-chips-label {
  font-size: 0.78rem;
  color: var(--color-muted-foreground);
}
.group-chip {
  cursor: pointer;
}
.retry-sep {
  margin: 0 6px;
  color: var(--color-muted-foreground);
  font-size: 0.85rem;
}
.field-tip {
  font-size: 0.78rem;
  color: var(--el-text-color-secondary, #909399);
  line-height: 1.5;
  margin-top: 4px;
}
</style>
