<template>
  <div v-loading="loading">
    <div class="page-head">
      <div>
        <h2 class="page-title">系统设置</h2>
        <span class="page-desc">平台运行参数，保存进数据库、立即生效无需重启；生效配置的只读快照见仪表盘「平台信息」卡，界面轮询偏好已移至顶栏「个人中心」</span>
      </div>
      <el-button :icon="Refresh" :loading="loading" circle text @click="load" />
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

async function load() {
  loading.value = true
  try {
    const res = await api.getSettings()
    const w = (res.data && res.data.writable) || {}
    if (w.default_storage_pool) writable.default_storage_pool = w.default_storage_pool
    if (w.vnc_token_ttl_min) writable.vnc_token_ttl_min = Number(w.vnc_token_ttl_min) || writable.vnc_token_ttl_min
    if (w.vnc_stale_min) writable.vnc_stale_min = Number(w.vnc_stale_min) || writable.vnc_stale_min
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
