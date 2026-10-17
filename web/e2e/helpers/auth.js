// 登录/登出与 token 读取。所有需要登录的用例统一走这里，
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

/** 清空登录态（不经过 UI，用于「未登录直链」类用例） */
export async function clearSession(page) {
  await page.goto('/#/login')
  await page.evaluate((k) => localStorage.removeItem(k), TOKEN_KEY)
}

/** 读取当前 token（给接口准备/清理用） */
export async function getToken(page) {
  return page.evaluate((k) => localStorage.getItem(k) || '', TOKEN_KEY)
}

/** 通过 UI 登出（顶栏用户菜单 → 退出登录） */
export async function logout(page) {
  await page.evaluate(() => {
    const btn = Array.from(document.querySelectorAll('button, .el-dropdown-link, .user-trigger')).find((el) =>
      /退出|登出/.test((el.innerText || '').trim())
    )
    if (btn) btn.click()
  })
  await page.waitForTimeout(500)
}
