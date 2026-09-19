<template>
  <div v-loading="loading">
    <div class="page-head">
      <div>
        <h2 class="page-title">系统设置</h2>
        <span class="page-desc">平台运行参数，保存进数据库、立即生效无需重启；生效配置的只读快照见仪表盘「平台信息」卡，界面轮询偏好已移至顶栏「个人中心」</span>
      </div>
      <!-- icon-only 按钮必须带 tooltip（ui-ux-pro-max §1 aria-labels：icon-only 无文字必须有可访问名称） -->
      <el-tooltip content="刷新" placement="top">
        <el-button :icon="Refresh" :loading="loading" circle text aria-label="刷新" @click="load" />
      </el-tooltip>
    </div>

    <!-- 可写配置：DB 持久化、写入即生效 -->
    <el-card shadow="never" class="mb">
      <template #header>
        <div class="card-head">
          <span class="card-title">运行参数</span>
          <el-button type="primary" :loading="saving" @click="saveWritable">保存并生效</el-button>
        </div>
      </template>
      <el-form label-width="170px" style="max-width: 560px">
        <el-form-item label="默认存储池">
          <el-input v-model="writable.default_storage_pool" placeholder="未指定池时创建/删除 VM 使用的池名" />
        </el-form-item>
        <el-form-item label="VNC token 有效期">
          <el-input-number v-model="writable.vnc_token_ttl_min" :min="1" :max="60" controls-position="right" />
          <span class="unit">分钟（每次生成 token 实时读取）</span>
        </el-form-item>
        <el-form-item label="VNC 会话过期判定">
          <el-input-number v-model="writable.vnc_stale_min" :min="5" :max="1440" controls-position="right" />
          <span class="unit">分钟（超时无活动将被清扫收敛）</span>
        </el-form-item>
      </el-form>
      <p class="tip">以上配置持久化在数据库中，保存后立即生效，无需重启后端。</p>
    </el-card>

    <!-- AI 设置（运维助手）：与「运行参数」独立保存，只提交 ai_* 三个键 -->
    <el-card shadow="never">
      <template #header>
        <div class="card-head">
          <span class="card-title">AI 设置（运维助手）</span>
          <el-button type="primary" :loading="savingAI" @click="saveAI">保存</el-button>
        </div>
      </template>
      <el-form label-width="170px" style="max-width: 560px">
        <el-form-item label="API 地址">
          <el-input v-model="ai.base_url" placeholder="如 https://api.deepseek.com/v1" clearable />
        </el-form-item>
        <el-form-item label="API Key">
          <!-- GET 返回明文，表单用密码框掩码展示；new-password 防浏览器把 Key 当登录口令自动填充 -->
          <el-input
            v-model="ai.api_key"
            type="password"
            show-password
            autocomplete="new-password"
            placeholder="粘贴服务商提供的 API Key"
          />
        </el-form-item>
        <el-form-item label="模型名">
          <el-input v-model="ai.model" placeholder="如 deepseek-chat" clearable />
        </el-form-item>
      </el-form>
      <p class="tip">配置后可在「AI 助手」页使用智能问答；Key 保存在服务端，不会下发到浏览器。</p>
    </el-card>

    <!-- 安全设置（批次 D）：安全入口 + 密码策略，独立保存 -->
    <el-card shadow="never">
      <template #header>
        <div class="card-head">
          <span class="card-title">安全设置</span>
          <el-button type="primary" :loading="savingSec" @click="saveSec">保存</el-button>
        </div>
      </template>
      <el-form label-width="170px" style="max-width: 560px">
        <el-form-item label="安全入口">
          <el-input v-model="sec.entrance" placeholder="留空 = 关闭（默认登录地址）" clearable style="width: 320px" />
          <div class="input-help">设置后登录 API 必须携带该入口参数（?entrance=值），否则一律 404——防扫描爆破。请妥善保存，遗忘可经 SSH 按文档重置。</div>
        </el-form-item>
        <el-form-item label="密码最小长度">
          <el-input-number v-model="sec.pwdMin" :min="0" :max="64" controls-position="right" style="width: 160px" />
          <div class="input-help">0 = 关闭策略；≥8 时密码需同时包含字母与数字</div>
        </el-form-item>
      </el-form>
      <p class="tip">安全入口遗忘时的应急处理见 docs/07 部署文档。</p>
    </el-card>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { api } from '../api'
import { errMsg } from '../utils/format'

const loading = ref(false)
const saving = ref(false)

// 可写配置表单（默认值兜底，后端 GET /settings 的 writable 节回填）
const writable = reactive({
  default_storage_pool: 'vmops',
  vnc_token_ttl_min: 5,
  vnc_stale_min: 60
})

// 安全设置（批次 D）：security_entrance / password_min_length，独立保存
const savingSec = ref(false)
const sec = reactive({ entrance: '', pwdMin: 8 })

// AI 设置（运维助手）：ai_base_url / ai_api_key / ai_model 三个键，独立保存
const savingAI = ref(false)
const ai = reactive({ base_url: '', api_key: '', model: '' })

async function load() {
  loading.value = true
  try {
    const res = await api.getSettings()
    const w = (res.data && res.data.writable) || {}
    if (w.default_storage_pool) writable.default_storage_pool = w.default_storage_pool
    if (w.vnc_token_ttl_min) writable.vnc_token_ttl_min = Number(w.vnc_token_ttl_min) || writable.vnc_token_ttl_min
    if (w.vnc_stale_min) writable.vnc_stale_min = Number(w.vnc_stale_min) || writable.vnc_stale_min
    // 快照含 ai_* 当前值（api_key 为明文，表单以密码框掩码展示）；
    // 兼容键位于 writable 节或快照顶层两种返回形态
    const snap = res.data || {}
    ai.base_url = w.ai_base_url ?? snap.ai_base_url ?? ''
    ai.api_key = w.ai_api_key ?? snap.ai_api_key ?? ''
    ai.model = w.ai_model ?? snap.ai_model ?? ''
    if (w.security_entrance !== undefined) sec.entrance = w.security_entrance
    if (w.password_min_length !== undefined) sec.pwdMin = Number(w.password_min_length) || 0
  } catch (e) {
    ElMessage.error('获取系统设置失败')
  } finally {
    loading.value = false
  }
}

async function saveWritable() {
  if (!writable.default_storage_pool) {
    ElMessage.warning('默认存储池不能为空')
    return
  }
  saving.value = true
  try {
    await api.updateSettings({
      default_storage_pool: writable.default_storage_pool,
      vnc_token_ttl_min: writable.vnc_token_ttl_min,
      vnc_stale_min: writable.vnc_stale_min
    })
    ElMessage.success('已保存并生效')
    load()
  } catch (e) {
    ElMessage.error(errMsg(e, '保存失败'))
  } finally {
    saving.value = false
  }
}

// AI 设置单独保存：只提交 ai_* 三个键，不携带「运行参数」，互不覆盖
async function saveAI() {
  const baseUrl = ai.base_url.trim()
  const apiKey = ai.api_key.trim()
  const model = ai.model.trim()
  if (!baseUrl && !apiKey && !model) {
    ElMessage.warning('请先填写 AI 配置')
    return
  }
  // 三项为一组生效配置，缺任一项时后端校验也会 400，这里提前给出可读提示
  if (!baseUrl || !apiKey || !model) {
    ElMessage.warning('API 地址、API Key、模型名需完整填写')
    return
  }
  savingAI.value = true
  try {
    await api.updateSettings({ ai_base_url: baseUrl, ai_api_key: apiKey, ai_model: model })
    ElMessage.success('AI 设置已保存并生效')
    load()
  } catch (e) {
    ElMessage.error(errMsg(e, '保存 AI 设置失败'))
  } finally {
    savingAI.value = false
  }
}

// 安全设置单独保存：只提交两键，与运行参数/AI 互不覆盖
async function saveSec() {
  savingSec.value = true
  try {
    await api.updateSettings({
      security_entrance: sec.entrance.trim(),
      password_min_length: Number(sec.pwdMin) || 0
    })
    ElMessage.success('安全设置已保存并生效')
    load()
  } catch (e) {
    ElMessage.error(errMsg(e, '保存安全设置失败'))
  } finally {
    savingSec.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.mb {
  margin-bottom: 16px;
}
.card-title {
  font-weight: 600;
}
.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.unit {
  margin-left: 8px;
  color: var(--color-muted-foreground);
  font-size: 0.82rem;
}
.tip {
  color: var(--color-muted-foreground);
  font-size: 0.82rem;
  margin: 4px 0 0;
}
</style>
