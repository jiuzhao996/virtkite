<template>
  <div class="login-wrap">
    <img
      ref="bgImg"
      :src="loginBg"
      class="login-bg"
      :class="{ on: bgOk }"
      alt=""
      @load="bgOk = true"
      @error="bgOk = false"
    />
    <!-- 品牌墙：青绿深底上的大尺寸白鸢水印，营造纵深（mark-white 在深色底上可见） -->
    <img class="brand-watermark" src="/brand/mark-white.svg" aria-hidden="true" alt="" />
    <!-- 系统公告（公开接口，无需认证）：非空即展示，置于登录卡片上方同宽展示 -->
    <div class="login-stack">
      <el-alert
        v-if="announcement"
        class="login-announcement"
        type="info"
        show-icon
        :closable="false"
        :title="announcement"
      />
      <el-card class="login-card" shadow="always">
        <div class="login-brand">
          <img class="logo" src="/brand/logo-teal.svg" alt="鸢航 VirtKite" />
          <h1>鸢航 <span class="en">VirtKite</span></h1>
          <p>基于 KVM 的轻量级私有云管理平台</p>
        </div>

        <el-alert v-if="error" :title="error" type="error" show-icon :closable="false" class="mb" />

        <el-form @submit.prevent="submit" label-position="top">
          <el-form-item label="用户名">
            <el-input v-model="form.username" placeholder="请输入用户名" size="large" autocomplete="username" @keyup.enter="submit" />
          </el-form-item>
          <el-form-item label="密码">
            <el-input v-model="form.password" type="password" placeholder="请输入密码" size="large" show-password autocomplete="current-password" @keyup.enter="submit" />
          </el-form-item>
          <el-button type="primary" class="submit-btn" size="large" :loading="submitting" @click="submit">
            登 录
          </el-button>
        </el-form>

        <!-- 演示账号提示：构建时 VITE_SHOW_DEMO_TIP=false 可隐藏（公开演示/截图归档时不应暴露口令） -->
        <div v-if="showDemoTip" class="demo-tip">
          <el-icon><InfoFilled /></el-icon>演示账号：<br />
          管理员 <code>admin</code> / <code>Password1</code><br />
          操作员 <code>stu</code> / <code>123456</code><br />
          普通用户 <code>user</code> / <code>123456</code>
        </div>
      </el-card>
    </div>
    <!-- 品牌收尾：弱化小字，不写版本号 -->
    <footer class="login-footer">鸢航 VirtKite · 基于 KVM 的轻量级私有云管理平台</footer>
  </div>
</template>

<script setup>
import { reactive, ref, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { InfoFilled } from '@element-plus/icons-vue'
import { api } from '../api'
import { errMsg } from '../utils/format'
import { useAuth } from '../store/auth'
import loginBg from '../assets/login-bg.jpg'

const router = useRouter()
const { setToken, setUser } = useAuth()

const form = reactive({ username: '', password: '' })
const error = ref('')
const submitting = ref(false)
const bgOk = ref(false)
const bgImg = ref(null)
// 演示账号提示开关：默认显示（开发/答辩演示用），构建时 VITE_SHOW_DEMO_TIP=false 隐藏
const showDemoTip = import.meta.env.VITE_SHOW_DEMO_TIP !== 'false'

// 系统公告（v3 批次 P）：公开接口拉取，非空展示于登录卡片上方；拉取失败静默（不挡登录主流程）
const announcement = ref('')

async function loadAnnouncement() {
  try {
    const res = await api.getAnnouncement()
    announcement.value = (res.data && res.data.content) || ''
  } catch (e) {
    /* 公告不可达不影响登录 */
  }
}

onMounted(() => {
  // 登录页禁止页面级滚动（内容居中，不应出现右侧滚动条）
  document.documentElement.style.overflow = 'hidden'
  if (bgImg.value && bgImg.value.complete && bgImg.value.naturalWidth > 0) bgOk.value = true
  loadAnnouncement()
})

onUnmounted(() => {
  document.documentElement.style.overflow = ''
})

async function submit() {
  error.value = ''
  if (!form.username || !form.password) {
    error.value = '请输入用户名和密码'
    return
  }
  submitting.value = true
  try {
    const res = await api.login(form.username, form.password)
    if (res.code === 200 && res.data) {
      setToken(res.data.access_token)
      setUser(res.data.user)
      ElMessage.success('登录成功')
      router.push({ name: 'dashboard' })
    } else {
      error.value = res.message || '登录失败'
    }
  } catch (e) {
    error.value = errMsg(e, '网络错误，请稍后重试')
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped>
.login-wrap {
  position: relative;
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  /* 品牌青绿深色渐变（AGENTS 品牌规范：#2a9da5 → #217d83） */
  background: linear-gradient(160deg, #1d8f96 0%, #157078 45%, #0f5257 100%);
  padding: 24px;
  overflow: hidden;
}
.login-bg {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
  opacity: 0;
  transition: opacity 0.8s ease;
}
.login-bg.on {
  opacity: 1;
}
.brand-watermark {
  position: absolute;
  top: 50%;
  left: 50%;
  width: min(440px, 70vw);
  height: auto;
  transform: translate(-50%, -50%);
  opacity: 0.1;
  z-index: 1;
  pointer-events: none;
  animation: brand-float 7s ease-in-out infinite;
}
@keyframes brand-float {
  0%, 100% { transform: translate(-50%, -54%); }
  50% { transform: translate(-50%, -46%); }
}
@media (prefers-reduced-motion: reduce) {
  .brand-watermark { animation: none; }
}
.login-stack {
  position: relative;
  z-index: 2;
  width: 100%;
  max-width: 400px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
/* 公告条与登录卡同宽同圆角，多行内容自动撑高 */
.login-announcement {
  border-radius: var(--radius-lg);
}
.login-card {
  position: relative;
  width: 100%;
  max-width: 400px;
  border-radius: var(--radius-lg);
  background: #fff;
  z-index: 2;
}
.login-brand {
  text-align: center;
  margin-bottom: 20px;
}
.logo {
  width: 84px;
  height: 84px;
  display: block;
  margin: 0 auto;
}
.login-brand h1 {
  font-size: 1.5rem;
  color: var(--color-primary);
  margin: 10px 0 2px;
  letter-spacing: 2px;
}
.login-brand h1 .en {
  font-size: 1.05rem;
  font-weight: 600;
  opacity: 0.85;
  letter-spacing: 1px;
}
.login-brand p {
  font-size: 0.9rem;
  color: var(--color-muted-foreground);
  margin: 0;
}
.submit-btn {
  width: 100%;
  margin-top: 4px;
}
.mb {
  margin-bottom: 16px;
}
.demo-tip {
  margin-top: 18px;
  padding: 12px 14px;
  background: var(--el-color-primary-light-9);
  border: 1px solid var(--el-color-primary-light-5);
  border-radius: var(--radius-sm);
  font-size: 0.85rem;
  color: var(--color-primary);
  line-height: 1.9;
}
/* 提示图标：el-icon 默认基线对齐，与中文混排偏高，下压 0.15em 并补右间距 */
.demo-tip .el-icon {
  margin-right: 4px;
  vertical-align: -0.15em;
}
.login-footer {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 16px;
  z-index: 2;
  text-align: center;
  color: rgba(255, 255, 255, 0.42);
  font-size: 0.78rem;
  letter-spacing: 1px;
  pointer-events: none;
}
</style>
