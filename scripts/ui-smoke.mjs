#!/usr/bin/env node
// ui-smoke.mjs —— 前端 UI 冒烟回归（零依赖：Node 22 自带 fetch 与 WebSocket，直接说 CDP）。
//
// 为什么需要它：scripts/smoke.sh 是 API 层的，它证明不了「按钮点得动」。
// 2026-10-12 那轮真实点击走查抓到的 4 个缺陷（构建日志不回填、构建抽屉进不去子目录、
// ?tab= 深链点了没反应、新任务类型显示裸英文名）smoke.sh 一个都没覆盖到——
// 它们全是「页面渲染出来了、接口也是 200，但交互是坏的」。
//
// 用法：
//   ADMIN_PASS=<口令> node scripts/ui-smoke.mjs
//   ADMIN_PASS=<口令> BASE=http://127.0.0.1:8080 node scripts/ui-smoke.mjs
//   ADMIN_PASS=<口令> SHOTS=/tmp/ui-shots node scripts/ui-smoke.mjs   # 顺便存图
//
// 退出码：0 全过；1 有失败项（可直接接进 CI / 提交前自检）。
//
// 覆盖范围（全部只读，不建机不删机，可反复跑）：
//   1. 登录表单真实提交（不是塞 token）
//   2. 17 个主页面逐个进入：断言页面标志文案渲染 + 无 JS 异常 + 无 4xx/5xx
//   3. ?tab= 深链与旧地址重定向（先绕一跳再进，专测「同路由内只改 query」这个坑）
//      含「Role 库」tab —— 共享 role 是独立于 playbook 的一等资产，入口必须可达
//   4. 建机向导走到底（云镜像 → 计算资源 → 磁盘与网络 → 确认创建），
//      断言 DomainSpec 预览能生成 —— 到「确认创建」为止，不点创建按钮（无副作用）
import { spawn } from 'node:child_process'
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import net from 'node:net'

const BASE = process.env.BASE || 'http://127.0.0.1:8080'
const USER = process.env.ADMIN_USER || 'admin'
const PASS = process.env.ADMIN_PASS || ''
let CDP_PORT = Number(process.env.CDP_PORT || 9333)
const CHROME = process.env.CHROME || 'google-chrome'
const SHOTS = process.env.SHOTS || ''
const TOKEN_KEY = 'vmops_token' // 与 web/src/store/auth.js 的 TOKEN_KEY 保持一致

if (!PASS) {
  console.error('缺少 ADMIN_PASS。用法：ADMIN_PASS=<口令> node scripts/ui-smoke.mjs')
  process.exit(2)
}

// 调试端口探活：端口若已被占用（上一轮 ui-smoke 残留的 Chrome、或别处挂着的 headless
// 浏览器），新 Chrome 会因端口冲突起不了调试口，waitForCDP 反而连上那个「别人的」浏览器
// ——它可能停在前端改动前的旧页面、还带着旧会话，于是整轮测试静默测错对象
// （2026-10-13 实战踩到：Settings 已改成 el-tabs，测试却仍读到旧的 .sec-nav 页面）。
// 起 Chrome 前先扫一个空闲端口，确保测的是本轮自己的实例。
function isPortFree(port) {
  return new Promise((resolve) => {
    const srv = net.createServer()
    srv.once('error', () => resolve(false))
    srv.once('listening', () => srv.close(() => resolve(true)))
    srv.listen(port, '127.0.0.1')
  })
}
async function pickFreePort(start) {
  for (let p = start; p < start + 50; p++) {
    if (await isPortFree(p)) return p
  }
  throw new Error(`从 ${start} 起连续 50 个端口都被占用，无法为 ui-smoke 找到空闲调试端口`)
}

// ---------- 页面清单：hash → 该页必须出现的标志文案 ----------
// 标志文案取自页面标题/空态，改版时同步这里（找不到即失败，正是我们要的灵敏度）
const PAGES = [
  ['#/dashboard', '仪表盘'],
  ['#/vms', '虚拟机'],
  ['#/containers', '容器'],
  ['#/apps', '应用商店'],
  ['#/designer', '架构设计'],
  ['#/grant-requests', '资产申请'],
  ['#/recycle-bin', '回收站'],
  ['#/automation', '运维自动化'],
  ['#/hosts', '宿主机'],
  ['#/images', '镜像管理'],
  ['#/storage', '存储'],
  ['#/networks', '网络'],
  ['#/tasks', '任务中心'],
  ['#/audit', '审计中心'],
  ['#/logs', '日志'],
  ['#/profile', '个人中心'],
  ['#/users', '用户管理'],
  ['#/settings', '系统设置']
]

// 深链与重定向：到达方式必须「先绕到别的路由再进」——同路由内只改 query 不重挂组件，
// 组件若只在挂载时读一次 route.query，这里就会红（2026-10-12 修过一次这个坑）。
// [目标 hash, 该页必须选中的 tab 文案]
const DEEPLINKS = [
  ['#/images?tab=iso', 'ISO 安装镜像'],
  ['#/images?tab=docker', '容器镜像'],
  ['#/automation?tab=cron', '计划任务'],
  ['#/automation?tab=roles', 'Role 库'],
  ['#/dashboard?tab=monitor', '监控'],
  ['#/dashboard?tab=topology', '拓扑'],
  ['#/crons', '计划任务'], // 旧地址重定向 → /automation?tab=cron
  ['#/monitor', '监控'], // 旧地址重定向 → /dashboard?tab=monitor
  ['#/topology', '拓扑'] // 旧地址重定向 → /dashboard?tab=topology
]

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const results = []
const pass = (name) => results.push({ ok: true, name })
const fail = (name, why) => results.push({ ok: false, name, why })
const check = (name, cond, why) => (cond ? pass(name) : fail(name, why))

// ---------- CDP 极简客户端 ----------
async function waitForCDP(timeoutMs = 20000) {
  const deadline = Date.now() + timeoutMs
  for (;;) {
    try {
      const r = await fetch(`http://127.0.0.1:${CDP_PORT}/json/version`)
      if (r.ok) return
    } catch (_) {
      /* 还没起来 */
    }
    if (Date.now() > deadline) throw new Error(`Chrome 调试端口 ${CDP_PORT} 未就绪`)
    await sleep(300)
  }
}

function connect(wsUrl) {
  const ws = new WebSocket(wsUrl)
  const pending = new Map()
  let id = 0
  const events = []
  ws.addEventListener('message', (ev) => {
    const m = JSON.parse(ev.data)
    if (m.id && pending.has(m.id)) {
      const p = pending.get(m.id)
      pending.delete(m.id)
      m.error ? p.rej(new Error(JSON.stringify(m.error))) : p.res(m.result)
    } else if (m.method) {
      events.push(m)
    }
  })
  const ready = new Promise((res, rej) => {
    ws.addEventListener('open', res)
    ws.addEventListener('error', () => rej(new Error('WebSocket 连接失败')))
  })
  const send = (method, params = {}) =>
    new Promise((res, rej) => {
      const i = ++id
      pending.set(i, { res, rej })
      ws.send(JSON.stringify({ id: i, method, params }))
      setTimeout(() => {
        if (pending.has(i)) {
          pending.delete(i)
          rej(new Error(`CDP 超时: ${method}`))
        }
      }, 30000)
    })
  return { ws, ready, send, events }
}

// killChromeTree 整组带走 Chrome（detached 启动才有独立进程组；拿不到组就退回杀主进程）
function killChromeTree(chrome) {
  try {
    process.kill(-chrome.pid, 'SIGKILL')
  } catch (_) {
    try {
      chrome.kill('SIGKILL')
    } catch (_) {
      /* 已经退了 */
    }
  }
}

async function main() {
  // 预检：BASE 不可达时先报清楚，别等 Chrome 起来后卡在 about:blank 上报个莫名的 SecurityError
  try {
    const r = await fetch(`${BASE}/api/health`, { signal: AbortSignal.timeout(5000) })
    if (!r.ok) throw new Error(`HTTP ${r.status}`)
  } catch (e) {
    console.error(`目标平台不可达：${BASE}（${e.message}）\n请先启动后端：./start.sh`)
    process.exit(2)
  }

  // 端口被占用则另选一个空闲端口（详见 pickFreePort 注释）：默认 9333 被别的浏览器
  // 占着时，不再连错对象、静默测到旧页面。
  const freePort = await pickFreePort(CDP_PORT)
  if (freePort !== CDP_PORT) {
    console.log(`注意：CDP 端口 ${CDP_PORT} 被占用，本轮改用 ${freePort} 起浏览器`)
    CDP_PORT = freePort
  }

  const profile = mkdtempSync(join(tmpdir(), 'vmops-ui-smoke-'))
  const chrome = spawn(
    CHROME,
    [
      '--headless=new',
      `--remote-debugging-port=${CDP_PORT}`,
      `--user-data-dir=${profile}`,
      '--no-first-run',
      '--no-default-browser-check',
      '--disable-gpu',
      '--disable-dev-shm-usage',
      '--disable-extensions',
      'about:blank'
    ],
    // detached：让 Chrome 自成一个进程组，收摊时能整组带走——只 kill 主进程的话
    // zygote/renderer 子进程还活着，会继续往 user-data-dir 里写，临时目录删不干净
    { stdio: 'ignore', detached: true }
  )

  let cdp
  try {
    await waitForCDP()
    const targets = await (await fetch(`http://127.0.0.1:${CDP_PORT}/json/list`)).json()
    const page = targets.find((t) => t.type === 'page')
    if (!page) throw new Error('没有可用的 page target')

    cdp = connect(page.webSocketDebuggerUrl)
    await cdp.ready
    await cdp.send('Page.enable')
    await cdp.send('Runtime.enable')
    await cdp.send('Network.enable')
    await cdp.send('Emulation.setDeviceMetricsOverride', {
      width: 1680,
      height: 1200,
      deviceScaleFactor: 1,
      mobile: false
    })

    const ev = async (expr) => {
      const r = await cdp.send('Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true })
      if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text)
      return r.result.value
    }

    // 事件按「当前步骤」打标，报错时能指到是哪一页出的问题
    let step = '启动'
    const jsErrors = []
    const httpErrors = []
    cdp.events.length = 0
    const drain = () => {
      while (cdp.events.length) {
        const m = cdp.events.shift()
        if (m.method === 'Runtime.exceptionThrown') {
          const d = m.params.exceptionDetails
          jsErrors.push({ step, text: (d.exception?.description || d.text || '').split('\n')[0].slice(0, 200) })
        } else if (m.method === 'Runtime.consoleAPICalled' && m.params.type === 'error') {
          const t = (m.params.args || []).map((a) => a.value ?? a.description ?? '').join(' ').slice(0, 200)
          jsErrors.push({ step, text: t })
        } else if (m.method === 'Network.responseReceived' && m.params.response.status >= 400) {
          httpErrors.push({ step, status: m.params.response.status, url: m.params.response.url.slice(0, 160) })
        }
      }
    }

    const shot = async (name) => {
      if (!SHOTS) return
      mkdirSync(SHOTS, { recursive: true })
      // 全页截图（captureBeyondViewport）会把 position:sticky 元素画到其滚动偏移处
      // ——Chrome 已知行为。任何吸顶元件（如页内导航条）因此「漂移」到页面中段，
      // 截图看着像 bug，其实真机滚动正常（用户 2026-10-13 据此反馈「漂移」）。
      // 捕获期间把 sticky 临时压回 static，只动 sticky、不碰 absolute/fixed（避免打乱
      // 布局），拍完立即按原值还原。
      await ev(`(() => {
        window.__shotSticky = []
        document.querySelectorAll('*').forEach((el) => {
          if (getComputedStyle(el).position === 'sticky') {
            window.__shotSticky.push([el, el.style.position])
            el.style.position = 'static'
          }
        })
      })()`)
      const r = await cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true })
      await ev(`(() => {
        (window.__shotSticky || []).forEach(([el, p]) => { el.style.position = p })
        window.__shotSticky = null
      })()`)
      writeFileSync(join(SHOTS, `${name}.png`), Buffer.from(r.data, 'base64'))
    }
    const goto = async (hash, waitMs = 2200) => {
      step = hash
      await ev(`location.hash = ${JSON.stringify(hash)}`)
      await sleep(waitMs)
      drain()
    }
    const text = async () => ev(`(document.querySelector('.el-main')?.innerText || document.body.innerText || '').replace(/\\s+/g, ' ')`)

    // ---------- 1) 登录（真实提交表单）----------
    step = '登录'
    await cdp.send('Page.navigate', { url: `${BASE}/#/login` })
    await sleep(3000)
    drain()
    const filled = await ev(`(() => {
      const setVal = (el, v) => {
        const d = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')
        d.set.call(el, v)
        el.dispatchEvent(new Event('input', { bubbles: true }))
        el.dispatchEvent(new Event('change', { bubbles: true }))
      }
      const u = document.querySelector('input[autocomplete="username"]')
      const p = document.querySelector('input[autocomplete="current-password"]')
      if (!u || !p) return false
      setVal(u, ${JSON.stringify(USER)})
      setVal(p, ${JSON.stringify(PASS)})
      return true
    })()`)
    check('登录表单渲染', filled, '找不到用户名/密码输入框')
    if (filled) {
      await ev(`document.querySelector('.submit-btn').click()`)
      await sleep(3000)
      drain()
      const loggedIn = await ev(`!!localStorage.getItem(${JSON.stringify(TOKEN_KEY)}) && location.hash !== '#/login'`)
      check('登录提交成功', loggedIn, '未拿到 token 或仍停在登录页')
      await shot('00-登录后')
    }

    // ---------- 2) 主页面逐个进入 ----------
    for (const [hash, marker] of PAGES) {
      await goto(hash)
      const body = await text()
      check(`页面 ${hash}`, body.includes(marker), `未找到标志文案「${marker}」`)
      await shot(hash.replace(/[#/?=&]+/g, '_'))
    }

    // ---------- 3) 深链 / 重定向（先绕一跳，专测同路由改 query）----------
    for (const [hash, tab] of DEEPLINKS) {
      await goto('#/vms', 1200) // 绕开：从别的路由进目标，才会重新挂载组件
      await goto(hash, 2600)
      const active = await ev(`(() => {
        const el = document.querySelector('.el-tabs__item.is-active')
        return el ? el.textContent.trim() : ''
      })()`)
      check(`深链 ${hash}`, active.includes(tab), `活动 tab 是「${active || '无'}」，期望含「${tab}」`)
      await shot(hash.replace(/[#/?=&]+/g, '_'))
    }

    // ---------- 3.5) 点 tab 时 URL 要跟着变（反向同步）----------
    // 页面原本只做「URL → tab」单向，用户点 tab 后地址栏不变——复制给别人打开的是
    // 另一个 tab，刷新也回不到刚才那页。这半边补上了，深链才真的可分享。
    await goto('#/images', 2200)
    const clickTab = async (label) => {
      const ok = await ev(`(() => {
        const el = Array.from(document.querySelectorAll('.el-tabs__item')).find((e) => e.textContent.trim() === ${JSON.stringify(label)})
        if (!el) return false
        el.click()
        return true
      })()`)
      await sleep(1200)
      return ok
    }
    const toIso = await clickTab('ISO 安装镜像')
    const hashIso = await ev('location.hash')
    check('点 tab 同步 URL', toIso && hashIso.includes('tab=iso'), `点了=${toIso} hash=${hashIso}`)
    const backDefault = await clickTab('云镜像 / 模板盘')
    const hashDefault = await ev('location.hash')
    check('回默认 tab 清掉 query', backDefault && !hashDefault.includes('tab='), `hash=${hashDefault}`)

    // ---------- 3.6) 存储池卡片：点描述必须开抽屉，不能弹编辑框 ----------
    // 这条是真实误触反馈：描述行此前整块是「点我编辑」的热区，横跨整张卡片，
    // 用户想开抽屉却弹出编辑框。现在描述是只读文本，编辑只走「编辑」按钮。
    await goto('#/storage', 2600)
    const tabClicked = await ev(`(() => {
      const t = Array.from(document.querySelectorAll('.el-tabs__item')).find((e) => e.textContent.trim() === '存储池')
      if (!t) return false
      t.click()
      return true
    })()`)
    await sleep(2000)
    const descCount = await ev(`document.querySelectorAll('.pool-desc').length`)
    check('存储池卡片渲染', tabClicked && descCount > 0, `tab=${tabClicked} 描述行=${descCount}`)
    // 描述文案由后端按池角色补缺省，卡片上不该再出现「点击编辑…」这类诱导文案
    const hintLeft = await ev(`Array.from(document.querySelectorAll('.pool-desc')).some((e) => e.textContent.includes('点击编辑'))`)
    check('卡片无「点击编辑」诱导文案', !hintLeft, '仍存在诱导点击的占位文案')
    await ev(`document.querySelector('.pool-desc')?.click()`)
    await sleep(1800)
    const afterClick = await ev(`JSON.stringify({
      drawer: !!document.querySelector('.el-drawer'),
      dialog: !!document.querySelector('.el-dialog')
    })`)
    const st = JSON.parse(afterClick)
    check('点描述开抽屉而非编辑框', st.drawer && !st.dialog, afterClick)
    await ev(`document.querySelector('.el-drawer__close-btn')?.click()`)
    await sleep(600)

    // ---------- 3.7) 系统设置：左侧竖向 tab（宿主机在最前、计划任务已移出、可切换）----------
    // 均来自用户反馈：此前 9 张卡纵向堆叠约 3.6 屏、靠浮动吸顶条导航（滚动时吸顶条从卡片
    // 内容上「切」过去看着像错位）；现改为左侧竖向 tab —— 一次只显示一节，无滚动、无遮挡。
    await goto('#/settings', 3200)
    const settingsShape = await ev(`(() => {
      const ids = ['sec-hosts','sec-sshkeys','sec-params','sec-security','sec-notify','sec-announce','sec-cloudinit','sec-ai','sec-nested']
      const present = ids.filter((i) => document.getElementById(i))
      const tabs = Array.from(document.querySelectorAll('.settings-tabs .el-tabs__item')).map((e) => e.textContent.trim())
      return JSON.stringify({
        first: tabs[0] || '',
        tabCount: tabs.length,
        presentCount: present.length,
        hasCron: /计划任务/.test(document.querySelector('.settings-tabs')?.innerText || '')
      })
    })()`)
    const sp = JSON.parse(settingsShape)
    check('设置页：首个 tab 是宿主机', sp.first === '宿主机', `首个=${sp.first}`)
    check('设置页：9 个 tab 与 9 个分节齐全', sp.tabCount === 9 && sp.presentCount === 9, `tab=${sp.tabCount} 分节=${sp.presentCount}`)
    check('设置页：计划任务已移出', !sp.hasCron, '本页仍出现「计划任务」')
    // 切到最后一个 tab，断言 active 项随之改变（内容随 tab 切换，无滚动/无吸顶遮挡）
    const tabCount = await ev(`(() => {
      const items = Array.from(document.querySelectorAll('.settings-tabs .el-tabs__item'))
      const last = items[items.length - 1]
      if (last) last.click()
      return items.length
    })()`)
    await sleep(700)
    const activeLabel = await ev(`document.querySelector('.settings-tabs .el-tabs__item.is-active')?.textContent.trim() || ''`)
    check('设置页：tab 可切换', tabCount === 9 && activeLabel === '嵌套虚拟化', `当前 tab=${activeLabel}`)

    // ---------- 4) 建机向导走到底（不点创建，无副作用）----------
    step = '建机向导'
    await ev(`sessionStorage.clear()`) // 向导有 sessionStorage 草稿，先清掉保证从第 1 步开始
    await goto('#/vms/new', 3000)
    const step1 = await text()
    check('向导第 1 步（安装方式）', step1.includes('选择安装方式'), '未渲染安装方式页')

    await ev(`(() => {
      const r = Array.from(document.querySelectorAll('.el-radio')).find((e) => e.textContent.includes('云镜像'))
      if (r) r.querySelector('input').click()
    })()`)
    await sleep(1000)
    await ev(`(() => {
      const s = Array.from(document.querySelectorAll('.el-select')).find((e) => /云镜像/.test(e.closest('.el-form-item')?.textContent || ''))
      if (!s) return
      s.querySelector('input').dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
      s.click()
    })()`)
    await sleep(1000)
    const pickedImg = await ev(`(() => {
      const it = document.querySelector('.el-select-dropdown__item')
      if (!it) return ''
      const t = it.textContent.trim()
      it.click()
      return t
    })()`)
    check('向导选云镜像', !!pickedImg, '镜像下拉没有可选项（镜像库为空？）')
    await sleep(800)

    const next = async () => {
      const r = await ev(`(() => {
        const b = Array.from(document.querySelectorAll('button')).find((x) => /下一步/.test(x.textContent))
        if (!b) return 'no-btn'
        if (b.disabled) return 'disabled'
        b.click()
        return 'ok'
      })()`)
      await sleep(1600)
      drain()
      return r
    }
    check('向导→计算资源', (await next()) === 'ok', '下一步不可用（第 1 步校验未过？）')
    const step2 = await text()
    check('向导第 2 步（计算资源）', step2.includes('虚拟机名称'), '未渲染计算资源页')

    await ev(`(() => {
      const it = Array.from(document.querySelectorAll('.el-form-item')).find((e) =>
        (e.querySelector('.el-form-item__label')?.textContent || '').includes('虚拟机名称'))
      if (!it) return
      const inp = it.querySelector('input')
      const d = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')
      d.set.call(inp, 'ui-smoke-tmp')
      inp.dispatchEvent(new Event('input', { bubbles: true }))
      inp.dispatchEvent(new Event('change', { bubbles: true }))
    })()`)
    await sleep(600)
    check('向导→磁盘与网络', (await next()) === 'ok', '下一步不可用（名称未填？）')
    check('向导→确认创建', (await next()) === 'ok', '下一步不可用')
    const confirm = await text()
    check('向导第 4 步生成 DomainSpec', confirm.includes('ui-smoke-tmp') && confirm.includes('配置预览'), '确认页未渲染出配置预览')
    await shot('99-向导确认页')
    // 到此为止：不点「创建虚拟机」——建机是 smoke.sh（API 层）的职责，UI 脚本保持零副作用

    // ---------- 5) 按钮验收：各页「打开类」按钮点一遍，靠全局 JS 异常检查兜住坏按钮 ----------
    // 只点会打开弹层/抽屉/下拉的按钮（新建/添加/查看/编辑/刷新/安装…），点开即关，零副作用。
    // 危险动作（删除/清除/重启/关机/批量/保存/执行/部署…）**一律跳过**——自动化误点删除的代价太大，
    // 留给人工验收。这里点一遍的意义：把"按钮 handler 写坏 / 绑定错 / 点一下就抛异常"暴露到 jsErrors。
    step = '按钮验收'
    const BTN_OPEN = /新建|添加|上传|刷新|查看|详情|编辑|导入|导出|安装|脚本|配置|管理|调整|关联|绑定|登记|克隆|复制|预览|展开|筛选|重置|更多|设置/
    const BTN_DANGER = /删除|清除|彻底|回收|重启|关机|停止|暂停|恢复|启动|开机|批量|保存|确定|提交|执行|运行|部署|应用|注销|退出|移除|摘除|下线|销毁|清空/
    const BTN_PAGES = ['#/vms', '#/containers', '#/apps', '#/storage', '#/networks', '#/images', '#/tasks', '#/audit', '#/users', '#/automation', '#/hosts', '#/settings', '#/logs']
    let btnClicked = 0, btnOpened = 0
    for (const pg of BTN_PAGES) {
      await goto(pg, 2000)
      const labels = JSON.parse(await ev(`(() => JSON.stringify(
        Array.from(document.querySelectorAll('.el-main .el-button'))
          .filter((b) => b.offsetParent !== null && !b.disabled && !b.classList.contains('is-disabled'))
          .map((b) => (b.innerText || '').trim().replace(/\\s+/g, ' ').slice(0, 16))
          .filter(Boolean)
      ))()`))
      for (const t of labels) {
        if (BTN_DANGER.test(t) || !BTN_OPEN.test(t)) continue
        // 按文本精确匹配点击（页面可能重渲染，故每次重新查找）
        const r = await ev(`(() => {
          const el = Array.from(document.querySelectorAll('.el-main .el-button'))
            .find((b) => (b.innerText || '').trim().replace(/\\s+/g, ' ').slice(0, 16) === ${JSON.stringify(t)} && b.offsetParent !== null && !b.disabled)
          if (!el) return 'gone'
          el.click()
          return 'ok'
        })()`)
        if (r !== 'ok') continue
        btnClicked++
        await sleep(600)
        const st = JSON.parse(await ev(`(() => JSON.stringify({
          dialog: !!document.querySelector('.el-dialog:not([style*="display: none"])'),
          drawer: !!document.querySelector('.el-drawer:not([style*="display: none"])'),
          msg: !!document.querySelector('.el-message'),
        }))()`))
        if (st.dialog || st.drawer || st.msg) btnOpened++
        // 关掉弹层/下拉，避免影响后续点击
        await ev(`(() => {
          document.querySelector('.el-dialog__headerbtn')?.click()
          document.querySelector('.el-drawer__close-btn')?.click()
          document.querySelector('.el-message__closeBtn')?.click()
          document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
        })()`)
        await sleep(250)
        drain()
      }
    }
    check('按钮验收：打开类按钮可点（无异常）', btnClicked > 0, `未点到任何打开类按钮（共 ${btnClicked}）`)
    console.log(`  按钮验收：点击 ${btnClicked} 个，其中 ${btnOpened} 个出现弹层/消息`)

    drain()
    for (const e of jsErrors) fail(`JS 异常 @${e.step}`, e.text)
    for (const e of httpErrors) fail(`HTTP ${e.status} @${e.step}`, e.url)
    if (!jsErrors.length && !httpErrors.length) pass('无 JS 异常 / 无 4xx-5xx')
  } finally {
    try {
      cdp?.ws.close()
    } catch (_) {
      /* ignore */
    }
    killChromeTree(chrome)
    await sleep(400) // 给子进程一点退场时间，否则刚删完又被写回
    try {
      rmSync(profile, { recursive: true, force: true })
    } catch (_) {
      console.warn(`提示：临时 profile 未删净，可手动清理 ${profile}`)
    }
  }

  // ---------- 汇总 ----------
  const bad = results.filter((r) => !r.ok)
  console.log(`\n${'='.repeat(60)}`)
  for (const r of results) console.log(`  ${r.ok ? 'PASS' : 'FAIL'}  ${r.name}${r.ok ? '' : '  -- ' + r.why}`)
  console.log(`${'='.repeat(60)}`)
  console.log(`RESULT: PASS=${results.length - bad.length} FAIL=${bad.length}${SHOTS ? `  截图: ${SHOTS}` : ''}`)
  process.exit(bad.length ? 1 : 0)
}

main().catch((e) => {
  console.error('ui-smoke 运行失败:', e.message)
  process.exit(2)
})
