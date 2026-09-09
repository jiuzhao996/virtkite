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
    <el-card class="login-card" shadow="always">
      <div class="login-brand">
        <img class="logo" src="/brand/logo-teal.svg" alt="鸢航 VirtKite" />
        <h1>鸢航 <span class="en">VirtKite</span></h1>
        <p>基于 KVM 的轻量级私有云管理平台</p>
      </div>

      <el-alert v-if="error" :title="error" type="error" show-icon :closable="false" class="mb" />

      <el-form @submit.prevent="submit" label-position="top">
        <el-form-item label="用户名">
          <el-input v-model="form.username" placeholder="请输入用户名" size="large" @keyup.enter="submit" />
        </el-form-item>
        <el-form-item label="密码">
          <el-input v-model="form.password" type="password" placeholder="请输入密码" size="large" show-password @keyup.enter="submit" />
        </el-form-item>
        <el-button type="primary" class="submit-btn" size="large" :loading="submitting" @click="submit">
          登 录
        </el-button>
      </el-form>

      <div class="demo-tip">
        <el-icon><InfoFilled /></el-icon>演示账号：<br />
        管理员 <code>admin</code> / <code>password</code><br />
        普通用户 <code>user</code> / <code>123456</code>
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { reactive, ref, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { InfoFilled } from '@element-plus/icons-vue'
import { api } from '../api'
import { useAuth } from '../store/auth'
import loginBg from '../assets/login-bg.jpg'

const router = useRouter()
const { setToken, setUser } = useAuth()

const form = reactive({ username: '', password: '' })
const error = ref('')
const submitting = ref(false)
const bgOk = ref(false)
const bgImg = ref(null)

onMounted(() => {
  // 登录页禁止页面级滚动（内容居中，不应出现右侧滚动条）
  document.documentElement.style.overflow = 'hidden'
  if (bgImg.value && bgImg.value.complete && bgImg.value.naturalWidth > 0) bgOk.value = true
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
    error.value = (e.response && e.response.data && e.response.data.message) || '网络错误，请稍后重试'
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
  background: linear-gradient(160deg, #0f1b2d 0%, #16283f 45%, #1d3a52 100%);
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
.login-card {
  position: relative;
  width: 100%;
  max-width: 400px;
  border-radius: 14px;
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
  border-radius: 6px;
  font-size: 0.85rem;
  color: var(--color-primary);
  line-height: 1.9;
}
/* 提示图标：el-icon 默认基线对齐，与中文混排偏高，下压 0.15em 并补右间距 */
.demo-tip .el-icon {
  margin-right: 4px;
  vertical-align: -0.15em;
}
</style>
