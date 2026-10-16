<template>
  <div class="ssh-form-wrap">
    <div class="ssh-form">
      <h3 class="form-title"><el-icon><Platform /></el-icon>SSH 连接</h3>
      <el-form label-width="70px">
        <el-form-item label="主机">
          <el-input v-model="form.host" placeholder="VM IP 或域名，默认取虚拟机 IP" />
        </el-form-item>
        <el-form-item label="端口">
          <el-input-number v-model="form.port" :min="1" :max="65535" controls-position="right" style="width: 100%" />
        </el-form-item>
        <el-form-item label="用户名">
          <el-input v-model="form.user" placeholder="如 root" />
        </el-form-item>
        <el-form-item label="密码">
          <el-input v-model="form.password" type="password" show-password @keyup.enter="emit('connect')" />
        </el-form-item>
      </el-form>
      <el-button type="primary" class="form-btn" :loading="connecting" @click="emit('connect')">连接终端</el-button>
    </div>
  </div>
</template>

<script setup>
// SSH 未连接时的连接表单（主机/端口/用户名/密码）。form 为 TermView 持有的 sshForm 对象，
// 直接 v-model 改其字段（含 localStorage 记忆逻辑仍在 TermView）；连接 emit('connect') 上抛。
import { Platform } from '@element-plus/icons-vue'

defineProps({
  // SSH 表单对象 { host, port, user, password }（直接改字段）
  form: { type: Object, required: true },
  connecting: { type: Boolean, default: false }
})
const emit = defineEmits(['connect'])
</script>

<style scoped>
/* 以下规则自 TermView.vue 原样搬入（类名不变，DOM 不变）；
   .form-title .el-icon 的图标基线微调原与 header/error 共用一条规则，此处单独保留一份 */
.form-title .el-icon {
  vertical-align: -0.15em;
  margin-right: 4px;
}
/* ---------- SSH 连接表单 ---------- */
.ssh-form-wrap {
  position: relative;
  z-index: 2;
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  min-height: 0;
}
.ssh-form {
  width: 440px;
  max-width: 100%;
  padding: 26px 30px;
  border-radius: var(--radius-lg);
  background: var(--term-card-bg);
  border: 1px solid rgba(88, 166, 255, 0.2);
  box-shadow: var(--elev-pop);
}
.form-title { margin: 0 0 18px; color: var(--term-text); font-size: 1.1rem; }
.ssh-form :deep(.el-form-item__label) { color: var(--term-text-sub); }
.ssh-form :deep(.el-input__wrapper),
.ssh-form :deep(.el-input-number .el-input__wrapper) {
  background: rgba(255, 255, 255, 0.06);
  box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.15) inset;
}
.ssh-form :deep(.el-input__inner) { color: var(--term-text); }
.ssh-form :deep(.el-input-number__decrease),
.ssh-form :deep(.el-input-number__increase) {
  color: var(--term-text-sub);
  background: rgba(255, 255, 255, 0.08);
  border-color: rgba(255, 255, 255, 0.12) !important;
  box-shadow: none !important;
}
.form-btn { width: 100%; margin-top: 4px; }

/* 窄屏适配（原 TermView ≤640px 媒体查询中与 .ssh-form 相关的部分） */
@media (max-width: 640px) {
  .ssh-form { padding: 20px 18px; }
}
</style>
