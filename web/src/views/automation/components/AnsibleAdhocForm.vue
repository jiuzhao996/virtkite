<template>
  <!-- Ansible 快速执行（adhoc）表单：目标选择 + 模块 + 参数 + 执行。
       从 Automation.vue 抽出（原文件 984 行，五个 tab 的编排逻辑挤在一处）。
       表单状态内聚在本组件，提交后 emit('run', payload) 由父页走任务轮询。 -->
  <el-card shadow="never">
    <el-form label-width="96px" :disabled="!engine.installed">
      <div class="auto-groups">
        <span class="auto-groups-label">主机组</span>
        <el-tag v-for="g in hostGroups" :key="g.id" class="auto-group-chip" effect="plain"
          :title="'点击选中「' + g.name + '」组成员（并集）'" @click="applyGroup(g)">
          {{ g.name }} ×{{ groupRunnableCount(g) }}
        </el-tag>
        <el-button text size="small" :icon="Setting" @click="$emit('manage-groups')">
          {{ hostGroups.length ? '管理' : '建主机组' }}
        </el-button>
      </div>
      <el-form-item label="目标方式">
        <el-radio-group v-model="targetMode">
          <el-radio-button value="vms">按虚拟机</el-radio-button>
          <el-radio-button value="group">按主机组（动态）</el-radio-button>
        </el-radio-group>
        <span class="auto-hint">按组执行：成员在触发时动态解析，改组即生效</span>
      </el-form-item>
      <el-form-item v-if="targetMode === 'group'" label="主机组">
        <el-select v-model="groupId" placeholder="选择主机组" style="width: 300px">
          <el-option v-for="g in hostGroups" :key="g.id" :label="g.name + '（' + groupRunnableCount(g) + ' 台可执行）'" :value="g.id" />
        </el-select>
      </el-form-item>
      <el-form-item v-else label="目标虚拟机">
        <div class="auto-targets">
          <el-select v-model="targets" multiple filterable placeholder="选择运行中的虚拟机（可多选）" style="flex: 1; min-width: 320px">
            <el-option v-for="vm in runnableVMs" :key="vm.id" :label="vmOptionLabel(vm)" :value="vm.id">
              <span>{{ vm.name }}（{{ vm.ip }}）</span>
              <el-tag v-if="!sshOK(vm.id)" type="danger" size="small" effect="plain" class="auto-cred-tag">无凭据</el-tag>
            </el-option>
          </el-select>
          <el-button size="small" @click="selectAll" :disabled="!runnableVMs.length">全选</el-button>
          <span class="auto-count">已选 {{ targets.length }} / {{ runnableVMs.length }} 台</span>
        </div>
      </el-form-item>
      <el-form-item label="模块">
        <el-radio-group v-model="module">
          <el-radio-button value="ping">ping（连通性）</el-radio-button>
          <el-radio-button value="command">command（命令）</el-radio-button>
          <el-radio-button value="shell">shell（含管道/重定向）</el-radio-button>
        </el-radio-group>
      </el-form-item>
      <el-form-item v-if="module !== 'ping'" label="执行参数">
        <el-input v-model="args" placeholder="如 free -m ｜ df -h ｜ hostnamectl | grep Operating" class="mono" />
      </el-form-item>
      <el-form-item>
        <el-button type="primary" :loading="submitting" @click="runAdhoc">执 行</el-button>
        <span class="auto-hint">目标需运行中、已获 IP 且保存过托管凭据（建机时勾选「自动初始化」会自动托管；存量机需管理员预置）</span>
      </el-form-item>
    </el-form>
  </el-card>
</template>

<script setup>
// 快速执行表单：目标方式/模块/参数的内聚状态 + 提交前校验。
// 校验口径与后端 handler/ansible_run.go 一致（目标非空、非 ping 必须填参数），
// 前端先挡一道只为少一次无效往返。
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Setting } from '@element-plus/icons-vue'

const props = defineProps({
  // engine：引擎探测结果。installed=false 时整个表单禁用（未安装 ansible）
  engine: { type: Object, default: () => ({}) },
  // vms：平台 VM 列表（父页加载）
  vms: { type: Array, default: () => [] },
  // hostGroups：主机组列表
  hostGroups: { type: Array, default: () => [] },
})
const emit = defineEmits(['run', 'manage-groups'])

// 目标方式：vms=按选中的虚拟机；group=按主机组（后端触发时动态解析成员，改组即生效）
const targetMode = ref('vms')
const groupId = ref(null)
const targets = ref([])
// 模块：ping 无参；command/shell 必须填参数（后端同口径）
const module = ref('ping')
const args = ref('')
const submitting = ref(false)

const runnableVMs = computed(() => props.vms.filter((v) => v.status === 'running' && v.ip))

// SSH 可用口径 = 免密（ansible_ready）∪ 已托管凭据；其余标「无凭据」
const sshOKSet = computed(() => {
  const s = new Set((props.engine.ansible_ready_ids || []).map(Number))
  for (const id of props.engine.cred_vm_ids || []) s.add(Number(id))
  return s
})
function sshOK(id) {
  return sshOKSet.value.has(Number(id))
}
function vmOptionLabel(vm) {
  const base = vm.name + '（' + vm.ip + '）'
  return sshOK(vm.id) ? base : base + ' · 无凭据'
}
function groupRunnableCount(g) {
  const ids = new Set((g.vm_ids || []).map(Number))
  return runnableVMs.value.filter((v) => ids.has(v.id)).length
}
// 点组标签 = 该组成员并入已选（并集；只并入当前可执行的）
function applyGroup(g) {
  const ids = new Set((g.vm_ids || []).map(Number))
  const pick = runnableVMs.value.filter((v) => ids.has(v.id)).map((v) => v.id)
  if (!pick.length) return ElMessage.warning(`「${g.name}」当前没有可执行成员（需运行中且有 IP）`)
  targets.value = [...new Set([...targets.value, ...pick])]
  ElMessage.success(`已并入「${g.name}」可执行成员 ${pick.length} 台`)
}

function selectAll() {
  targets.value = runnableVMs.value.map((v) => v.id)
}

// 打开「Playbook 执行」弹窗前清空选择（原 openRunDialog 的副作用）
function resetTargets() {
  targets.value = []
}

async function runAdhoc() {
  const payload = { module: module.value, args: args.value.trim() }
  if (targetMode.value === 'group') {
    if (!groupId.value) return ElMessage.warning('请选择主机组')
    payload.group_id = groupId.value
  } else {
    if (!targets.value.length) return ElMessage.warning('请选择目标虚拟机')
    payload.targets = targets.value
  }
  // ping 无参；其余模块必须填参数（与后端 ansibleModules 校验同口径）
  if (module.value !== 'ping' && module.value !== 'setup' && !args.value.trim()) {
    return ElMessage.warning('请填写执行参数')
  }
  submitting.value = true
  try {
    // 父页负责提交与轮询；提交前把 targets 归一成数组（group 模式不带 targets）
    emit('run', Array.isArray(payload.targets) ? { ...payload, targets: [...payload.targets] } : payload)
  } finally {
    submitting.value = false
  }
}

// 供父页共用：
//   resetTargets() 打开「Playbook 执行」弹窗前清空选择（原 openRunDialog 的副作用）
//   getTargets()   取当前目标（{targets:[...]} 或 {group_id:N}）；未选返回 null 并已提示
function getTargets() {
  if (targetMode.value === 'group') {
    if (!groupId.value) {
      ElMessage.warning('请选择主机组')
      return null
    }
    return { group_id: groupId.value }
  }
  if (!targets.value.length) {
    ElMessage.warning('请选择目标虚拟机')
    return null
  }
  return { targets: [...targets.value] }
}
defineExpose({ resetTargets, getTargets })
</script>
