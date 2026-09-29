import { ref } from 'vue'

/**
 * 服务端分页状态与流转（与 el-pagination 配套的受控分页）。
 *
 * 只管页码状态与流转（翻页 / 改每页条数 / 筛选后回第 1 页 / 保页刷新）；
 * 请求发起与响应解包留在页面的 fetcher 内——各列表接口解包路径不同
 * （api.* 走 unwrap 后读 res.data.items，裸 http 需 res.data.data.items），
 * 收进 composable 反而要引入响应形态适配层，抽象成本大于收益。
 *
 * fetcher 契约：
 * - 入参 `{ page, pageSize }`（JS 驼峰；接口参数名 page/page_size 的映射留在页面内，线上参数名不变）
 * - 需自行捕获请求异常并提示（保持各页现有 ElMessage 行为），不要向上抛
 * - 返回 total（number）或 `{ total }` 同步给 composable；出错可不返回，total 保持原值
 *
 * el-pagination 两种绑法都兼容：
 * - `:current-page="page" @current-change="handleCurrentChange"`（受控单向，本仓库主流写法）
 * - `v-model:page-size="pageSize" @size-change="handleSizeChange"`（v-model 先改值，
 *   handler 收到相同 size 再赋值是 no-op，不会重复请求）
 *
 * @param {({ page: number, pageSize: number }) => Promise<number|{total:number}|void>} fetcher 单页数据获取
 * @param {{ defaultPage?: number, defaultPageSize?: number }} [options] 初始页码 / 初始每页条数
 * @returns {{ page: import('vue').Ref<number>, pageSize: import('vue').Ref<number>, total: import('vue').Ref<number>, loading: import('vue').Ref<boolean>, handleCurrentChange: (p?: number) => Promise<void>, handleSizeChange: (size?: number) => Promise<void>, reloadFromFirst: () => Promise<void>, reload: () => Promise<void> }}
 */
export function usePagination(fetcher, options = {}) {
  const { defaultPage = 1, defaultPageSize = 20 } = options

  const page = ref(defaultPage)
  const pageSize = ref(defaultPageSize)
  const total = ref(0)
  const loading = ref(false)

  async function run() {
    loading.value = true
    try {
      const result = await fetcher({ page: page.value, pageSize: pageSize.value })
      if (typeof result === 'number') {
        total.value = result
      } else if (result && typeof result.total === 'number') {
        total.value = result.total
      }
    } finally {
      loading.value = false
    }
  }

  /** 翻页：跳到指定页后请求（el-pagination @current-change） */
  function handleCurrentChange(p) {
    if (p != null) page.value = p
    return run()
  }

  /** 改每页条数：回到第 1 页后请求（el-pagination @size-change） */
  function handleSizeChange(size) {
    if (size != null) pageSize.value = size
    page.value = 1
    return run()
  }

  /** 回到第 1 页后请求：筛选条件变化 / 重置时用 */
  function reloadFromFirst() {
    page.value = 1
    return run()
  }

  /** 保持当前页码重新请求：刷新按钮 / 外层列表联动刷新抽屉等场景 */
  function reload() {
    return run()
  }

  return { page, pageSize, total, loading, handleCurrentChange, handleSizeChange, reloadFromFirst, reload }
}
