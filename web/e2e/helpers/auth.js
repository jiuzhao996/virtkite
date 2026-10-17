// 登录与 token 读取。所有需要登录的用例统一走这里，
// 避免各 spec 自己填表单导致选择器漂移。
import { expect } from '@playwright/test'
import { ACCOUNTS } from './env.js'

export const TOKEN_KEY = 'vmops_token'

/** 用指定账号登录并等待 token 落盘 + 路由离开登录页 */
export async function login(page, role = 'admin') {
  const { user, pass } = ACCOUNTS[role] || ACCOUNTS.admin
  if (!pass) throw new Error(`缺少 ${role} 口令（环境变量 ${role.toUpperCase()}_PASS）`)

  await page.goto('/#/login')
  await page.locator('input[autocomplete="username"]').fill(user)
  await page.locator('input[autocomplete="current-password"]').fill(pass)
  await page.locator('.submit-btn').click()
  await expect.poll(() => page.evaluate((k) => !!localStorage.getItem(k), TOKEN_KEY)).toBe(true)
  await expect(page).not.toHaveURL(/#\/login/)
}
