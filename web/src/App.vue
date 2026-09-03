<template>
  <router-view />
</template>

<script setup>
import { onMounted } from 'vue'
import { useAuth } from './store/auth'
import { api } from './api'

const { state, setUser } = useAuth()

// 启动时若已有 token，拉取当前用户信息以保持会话
onMounted(async () => {
  if (state.token && !state.user) {
    try {
      const res = await api.me()
      setUser(res.data)
    } catch (e) {
      // token 失效由拦截器处理
    }
  }
})
</script>
