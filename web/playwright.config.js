// Playwright 验收配置。
// 默认复用系统 Chrome（channel: 'chrome'），不下载 Playwright 自带浏览器；
// 目标地址默认本机 8080，可用 BASE=https://... 覆盖。
import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  timeout: 90_000,
  expect: { timeout: 8_000 },
  // 串行执行：所有用例共享同一个后端，并发点击会互相干扰（弹层/任务/轮询）
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list'], ['html', { open: 'never', outputFolder: 'e2e-report' }]],
  use: {
    baseURL: process.env.BASE || 'http://127.0.0.1:8080',
    headless: true,
    ignoreHTTPSErrors: true,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
  },
  projects: [
    {
      name: 'chrome',
      use: { ...devices['Desktop Chrome'], channel: 'chrome' },
    },
  ],
})
