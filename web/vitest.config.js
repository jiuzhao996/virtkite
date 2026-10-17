// Vitest 配置（与 Vite 同源，可直接复用 Vue SFC 插件）。
// 环境用 jsdom：工具函数里 cssVar/localStorage 依赖 DOM 与存储。
// 只测「不依赖网络与登录」的纯逻辑与轻组件，E2E 点击验收走 Playwright。
import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.js'],
    // 覆盖率用 v8，输出文本摘要（需要时加 --coverage 生成报告）
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html'],
      include: ['src/utils/**/*.js', 'src/composables/**/*.js'],
    },
  },
})
