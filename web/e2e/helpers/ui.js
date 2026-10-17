// 页面交互工具：JS 异常收集、弹层关闭、按钮分类与安全点击。
// 所有「全按钮点击」类用例共用，保证点击策略只有一份实现。

/** 收集页面级 JS 异常（不收集 console.error：网络噪音会掩盖真问题） */
export function collectPageErrors(page) {
  const errors = []
  page.on('pageerror', (e) => errors.push(String(e && e.message ? e.message : e)))
  return errors
}

/** 关闭当前打开的弹窗/抽屉/消息，并补一次 ESC */
export async function closeOverlays(page) {
  await page.evaluate(() => {
    document.querySelector('.el-dialog__headerbtn')?.click()
    document.querySelector('.el-drawer__close-btn')?.click()
    document.querySelector('.el-message__closeBtn')?.click()
  })
  await page.keyboard.press('Escape').catch(() => {})
  await page.waitForTimeout(200)
}

/** 当前是否出现了弹层/消息 */
export async function hasOverlay(page) {
  return page.evaluate(() => {
    const visible = (sel) =>
      Array.from(document.querySelectorAll(sel)).some(
        (el) => el.offsetParent !== null && !String(el.className).includes('display: none')
      )
    return visible('.el-dialog') || visible('.el-drawer') || !!document.querySelector('.el-message')
  })
}

/** 取当前页可见、未禁用的按钮文案（去重） */
export async function visibleButtonLabels(page) {
  return page.locator('.el-main .el-button:visible').evaluateAll((els) => {
    const labels = els
      .filter((b) => !b.disabled && !b.classList.contains('is-disabled'))
      .map((b) => (b.innerText || '').trim().replace(/\s+/g, ' ').slice(0, 16))
      .filter(Boolean)
    return Array.from(new Set(labels))
  })
}

// 打开类：点开即关，零副作用
export const BTN_OPEN =
  /新建|添加|上传|刷新|查看|详情|编辑|导入|导出|安装|脚本|配置|管理|调整|关联|绑定|登记|克隆|复制|预览|展开|筛选|重置|更多|设置/
// 危险类：自动化一律跳过（专项用例在确认目标是 e2e- 资源后才点）
export const BTN_DANGER =
  /删除|清除|彻底|回收|重启|关机|停止|暂停|恢复|启动|开机|批量|保存|确定|提交|执行|运行|部署|应用|注销|退出|移除|摘除|下线|销毁|清空/

/**
 * 点击当前页所有「打开类」按钮，点开即关。
 * @returns {{clicked:number, opened:number, skipped:number}}
 */
export async function clickSafeButtons(page) {
  const labels = await visibleButtonLabels(page)
  let clicked = 0
  let opened = 0
  let skipped = 0

  for (const label of labels) {
    if (BTN_DANGER.test(label) || !BTN_OPEN.test(label)) {
      skipped++
      continue
    }
    const btn = page.locator('.el-main .el-button:visible', { hasText: label }).first()
    if (!(await btn.count())) continue
    await btn.click({ timeout: 3000 }).catch(() => {})
    clicked++
    await page.waitForTimeout(500)
    if (await hasOverlay(page)) opened++
    await closeOverlays(page)
  }
  return { clicked, opened, skipped }
}
