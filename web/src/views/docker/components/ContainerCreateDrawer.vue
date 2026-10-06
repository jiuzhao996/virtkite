<template>
  <el-drawer v-model="visible" title="创建容器" size="42%" :close-on-click-modal="false" @close="resetCreateForm">
    <el-form label-width="88px" @submit.prevent>
      <el-form-item label="名称" required>
        <el-input v-model="form.name" placeholder="如 my-nginx，字母数字开头，可含 _ . -" clearable />
        <div v-if="nameError" class="field-error">{{ nameError }}</div>
      </el-form-item>
      <el-form-item label="镜像" required>
        <!-- filterable + allow-create：下拉选已加载镜像（打开时自动补拉），也可手输任意镜像名 -->
        <el-select
          v-model="form.image"
          filterable
          allow-create
          default-first-option
          clearable
          placeholder="选择已有镜像或输入如 nginx:1.27"
          style="width: 100%"
        >
          <el-option v-for="img in imageOptions" :key="img" :label="img" :value="img" />
        </el-select>
      </el-form-item>
      <el-form-item label="端口映射">
        <div class="dyn-list">
          <div v-for="(row, i) in form.ports" :key="'port-' + i" class="dyn-row">
            <el-input v-model="form.ports[i]" placeholder="宿主:容器 如 8080:80" clearable />
            <el-tooltip content="删除该行" placement="top">
              <el-button
                :icon="Delete" text type="danger"
                :aria-label="'删除端口映射第 ' + (i + 1) + ' 行'"
                @click="form.ports.splice(i, 1)"
              />
            </el-tooltip>
          </div>
          <div v-if="portsError" class="field-error">{{ portsError }}</div>
          <el-button text type="primary" :icon="Plus" @click="form.ports.push('')">添加</el-button>
        </div>
      </el-form-item>
      <el-form-item label="挂载卷">
        <div class="dyn-list">
          <div v-for="(row, i) in form.volumes" :key="'vol-' + i" class="dyn-row">
            <el-input v-model="form.volumes[i]" placeholder="宿主路径:容器路径[:ro]" clearable />
            <el-tooltip content="删除该行" placement="top">
              <el-button
                :icon="Delete" text type="danger"
                :aria-label="'删除挂载卷第 ' + (i + 1) + ' 行'"
                @click="form.volumes.splice(i, 1)"
              />
            </el-tooltip>
          </div>
          <el-button text type="primary" :icon="Plus" @click="form.volumes.push('')">添加</el-button>
        </div>
      </el-form-item>
      <el-form-item label="环境变量">
        <div class="dyn-list">
          <div v-for="(row, i) in form.envs" :key="'env-' + i" class="dyn-row">
            <el-input v-model="form.envs[i]" placeholder="KEY=VALUE" clearable />
            <el-tooltip content="删除该行" placement="top">
              <el-button
                :icon="Delete" text type="danger"
                :aria-label="'删除环境变量第 ' + (i + 1) + ' 行'"
                @click="form.envs.splice(i, 1)"
              />
            </el-tooltip>
          </div>
          <el-button text type="primary" :icon="Plus" @click="form.envs.push('')">添加</el-button>
        </div>
      </el-form-item>
      <el-form-item label="重启策略">
        <el-select v-model="form.restart" style="width: 100%">
          <el-option v-for="opt in RESTART_OPTIONS" :key="opt.value" :value="opt.value" :label="opt.label">
            <el-tooltip :content="opt.tip" placement="left" :show-after="200">
              <span>{{ opt.label }}</span>
            </el-tooltip>
          </el-option>
        </el-select>
      </el-form-item>
      <el-form-item label="启动命令">
        <el-input v-model="form.command" placeholder="留空使用镜像默认 ENTRYPOINT" clearable />
      </el-form-item>
      <!-- 健康检查（可选）：配置后 docker 维护 State.Health，容器健康自愈（cron）据此判定 -->
      <el-collapse class="hc-collapse">
        <el-collapse-item title="健康检查（可选）" name="hc">
          <el-form-item label="检测命令">
            <el-input v-model="form.health_cmd" placeholder="如 curl -f http://localhost/ 或 exit 0" clearable />
          </el-form-item>
          <el-form-item label="检测间隔">
            <el-input v-model="form.health_interval" placeholder="如 30s / 1m，留空用 docker 默认" clearable />
          </el-form-item>
        </el-collapse-item>
      </el-collapse>
    </el-form>
    <template #footer>
      <el-button :disabled="creating" @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="creating" @click="submit">{{ creating ? '正在创建…' : '创建' }}</el-button>
    </template>
  </el-drawer>
</template>

<script setup>
// 创建容器抽屉（从 ContainerTab 抽出为独立组件）：容器页与镜像页「从镜像运行」共用。
// props.prefillImage 为空时行为不变；镜像页传入 Repository:Tag 预填镜像。
import { ref, reactive, computed } from 'vue'
import { ElMessage } from 'element-plus'
import { Plus, Delete } from '@element-plus/icons-vue'
import { api } from '../../../api'
import { errMsg } from '../../../utils/format'

const props = defineProps({
  // 打开时预填的镜像名（可选）
  prefillImage: { type: String, default: '' }
})
const emit = defineEmits(['created'])

const visible = ref(false)
const creating = ref(false)
// 动态行用「一行空串占位」起步，提交前 trim + 过滤空行
const form = reactive({
  name: '',
  image: '',
  ports: [''],
  volumes: [''],
  envs: [''],
  restart: 'no',
  command: '',
  health_cmd: '',
  health_interval: ''
})

// 重启策略四选一（对应 docker --restart）：label 为选项短文案，tip 为悬浮说明
const RESTART_OPTIONS = [
  { value: 'no', label: 'no（退出即停）', tip: '容器退出后不自动重启，需手动启动' },
  { value: 'always', label: 'always（总是重启）', tip: '任何退出都自动重启，Docker 守护进程启动时也会拉起' },
  { value: 'unless-stopped', label: 'unless-stopped（除非手动停止）', tip: '异常退出自动重启；手动停止后不再自动拉起' },
  { value: 'on-failure', label: 'on-failure（异常退出时）', tip: '仅非零退出码（异常退出）时自动重启' }
]

// 名称弱校验（行内红字）：字母数字开头，仅含 _ . -，最长 64；强校验在后端
const nameError = computed(() => {
  const n = form.name.trim()
  if (!n) return ''
  return /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$/.test(n)
    ? ''
    : '名称需以字母或数字开头，仅可包含字母、数字与 _ . -，最长 64 字符'
})

// 端口映射弱校验：非空行须含冒号（宿主:容器）；IP:宿主:容器 形态与端口范围校验交给后端
const portsError = computed(() => {
  for (const p of form.ports) {
    const v = p.trim()
    if (v && !v.includes(':')) return `端口映射「${v}」需包含冒号，格式如 8080:80`
  }
  return ''
})

// 镜像下拉候选：打开时补拉一次（拼 Repository:Tag，跳过 <none> 悬空层）
const imageOptions = ref([])

async function fetchImageOptions() {
  try {
    const res = await api.dockerImages()
    const items = (res.data || {}).items || []
    imageOptions.value = items
      .filter((r) => r.Repository && r.Repository !== '<none>')
      .map((r) => `${r.Repository}:${r.Tag || 'latest'}`)
  } catch (e) {
    // fire-and-forget：候选拉取失败不阻断开抽屉，用户仍可手输任意镜像名
  }
}

// 打开抽屉（image 传空即不预填）；每次打开重置表单后再应用预填
function open(image = '') {
  resetCreateForm()
  form.image = image || ''
  fetchImageOptions()
  visible.value = true
}

// 抽屉关闭即清空表单（各动态区重置为一行空占位）
function resetCreateForm() {
  form.name = ''
  form.image = ''
  form.ports = ['']
  form.volumes = ['']
  form.envs = ['']
  form.restart = 'no'
  form.command = ''
  form.health_cmd = ''
  form.health_interval = ''
}

// 动态行清洗：trim + 丢弃空行，空数组交给后端按缺省处理
function cleanRows(rows) {
  return rows.map((s) => String(s || '').trim()).filter(Boolean)
}

async function submit() {
  const name = form.name.trim()
  const image = form.image.trim()
  if (!name) {
    ElMessage.warning('请输入容器名称')
    return
  }
  if (nameError.value) {
    ElMessage.warning(nameError.value)
    return
  }
  if (!image) {
    ElMessage.warning('请选择或输入镜像名')
    return
  }
  if (portsError.value) {
    ElMessage.warning(portsError.value)
    return
  }
  creating.value = true
  try {
    await api.createContainer({
      name,
      image,
      ports: cleanRows(form.ports),
      volumes: cleanRows(form.volumes),
      envs: cleanRows(form.envs),
      restart: form.restart,
      command: form.command.trim(),
      health_cmd: form.health_cmd.trim(),
      health_interval: form.health_interval.trim()
    })
    ElMessage.success('容器已创建')
    visible.value = false
    emit('created')
  } catch (e) {
    ElMessage.error(errMsg(e, '创建容器失败'))
  } finally {
    creating.value = false
  }
}

defineExpose({ open })
</script>

<style scoped>
/* 动态行列表（整行 = 输入框 + 删除图标钮）与行内校验红字 */
.dyn-list {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 8px;
  width: 100%;
}
.dyn-row {
  display: flex;
  align-items: center;
  gap: 4px;
  width: 100%;
}
.dyn-row .el-input {
  flex: 1;
}
/* icon-only 删除钮贴合行内布局，去掉相邻按钮默认左距（间距交给 flex gap） */
.dyn-row .el-button {
  margin-left: 0;
}
.field-error {
  width: 100%;
  color: var(--color-danger, #f56c6c);
  font-size: 0.78rem;
  line-height: 1.4;
}
</style>
