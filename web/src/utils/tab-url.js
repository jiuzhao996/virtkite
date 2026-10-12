/**
 * 把当前 tab 写回 URL 的 `?tab=`。
 *
 * 为什么需要：页面本来只做「URL → tab」单向（深链进入时读一次），用户点 tab 后
 * URL 不变——于是复制地址栏发给别人，对方打开的是另一个 tab；刷新也回不到刚才那页。
 * 补上「tab → URL」这半边，深链才是真的可分享。
 *
 * 约定：
 *   - 默认 tab 不写 query（URL 保持干净，`/images` 就是默认 tab）
 *   - 用 replace 而非 push：切 tab 是同一页内的视图切换，不该往浏览历史里塞记录，
 *     否则用户按「后退」要连点好几次才回得到上一页
 *   - 与页面里的 watch(route.query.tab) 不构成回环：值相同就提前返回
 *
 * @param {import('vue-router').Router} router
 * @param {import('vue-router').RouteLocationNormalizedLoaded} route
 * @param {string} name 当前选中的 tab 名
 * @param {string} defaultTab 该页的默认 tab 名
 */
export function syncTabToUrl(router, route, name, defaultTab) {
  const cur = typeof route.query.tab === 'string' ? route.query.tab : ''
  const want = name === defaultTab ? '' : name
  if (cur === want) return
  const query = { ...route.query }
  if (want) query.tab = want
  else delete query.tab
  router.replace({ query })
}
