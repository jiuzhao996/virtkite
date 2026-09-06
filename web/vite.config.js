import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 后端 :8080 的代理配置，dev 与 preview 共用。
// preview 也必须配代理：dev 是原生 ESM、根本不走 rollup 分包，
// manualChunks 拆错导致的白屏在 dev 下永远看不到，只能靠 preview 跑真实产物验。
const apiProxy = {
  '/api': {
    target: 'http://localhost:8080',
    changeOrigin: true,
    // 控制台串口/终端走 WebSocket，必须透传 ws 升级，否则 dev 模式 WS 黑洞
    ws: true
  }
}

// Element Plus 的运行时依赖清单：这些包和 element-plus 一起进同一个 chunk。
//
// 为什么不用「element-plus 单独一个 chunk + 其余 node_modules 全塞 vendor」那份
// 网上常见配方：实测过，本项目这套依赖下它能正常跑（chunk 图仍是 DAG，
// @vueuse / lodash-es / dayjs 都不会反向 import element-plus，所以没有环），
// 首屏体积也只差 2kB —— 也就是说拆开毫无收益。
// 但 catch-all 的 `return 'vendor'` 会无声吞掉将来新增的任何依赖，
// 只要哪天进来一个反向依赖 element-plus / vue-router 的包，chunk 图就成环，
// ESM 初始化顺序被打乱，浏览器抛 "Cannot access 'xxx' before initialization"。
// 用具名白名单换掉 catch-all，等于让这类 bug 没有入口。
// 硬性验收条件：产物的跨 chunk 静态 import 图必须是 DAG（有环就是 TDZ 隐患）。
const elementPlusScope = [
  'element-plus',
  '@vueuse/core',
  '@vueuse/shared',
  '@vueuse/metadata',
  '@floating-ui/core',
  '@floating-ui/dom',
  '@floating-ui/utils',
  '@popperjs/core',
  '@ctrl/tinycolor',
  'async-validator',
  'dayjs',
  'lodash',
  'lodash-es',
  'lodash-unified',
  'memoize-one',
  'normalize-wheel-es'
]

// id 是绝对路径，判定包归属必须带上 node_modules/ 前缀和结尾的 /，
// 否则 'lodash' 会误吞 'lodash-es'、'element-plus' 会误吞 '@element-plus/icons-vue'
const inPkg = (id, pkg) => id.includes(`node_modules/${pkg}/`)

function manualChunks(id) {
  if (!id.includes('node_modules')) return
  const path = id.replace(/\\/g, '/')

  // 终端：只有 ConsolePage 用；xterm 零第三方依赖，是干净的叶子包
  if (inPkg(path, '@xterm/xterm') || inPkg(path, '@xterm/addon-fit')) return 'vendor-xterm'

  // 图表：只有 Dashboard / VmList / VmDetail 用。
  // 依赖链单向 echarts → zrender → tslib，且 tslib 在本项目只被这两个包引用
  if (inPkg(path, 'echarts') || inPkg(path, 'zrender') || inPkg(path, 'tslib')) return 'vendor-echarts'

  // 图标：main.js 全量注册（import * + 遍历），所以必然进首屏。
  // 单独成 chunk 至少能长期缓存，且让"全量注册的代价"在构建报告里看得见。
  // 依赖方向 element-plus → icons-vue → vue，无环，可安全独立
  if (inPkg(path, '@element-plus/icons-vue')) return 'vendor-element-plus-icons'

  // 组件库 + 其全部运行时依赖（含 element-plus/dist/index.css）
  if (elementPlusScope.some((pkg) => inPkg(path, pkg))) return 'vendor-element-plus'

  // 框架层，全项目最稳定的一块，业务代码改动不会动它的 hash。
  // 依赖只出边（别人依赖 vue，vue 不依赖别人），拆出来无环
  if (inPkg(path, 'vue') || inPkg(path, '@vue') || inPkg(path, 'vue-router')) return 'vendor-vue'

  // 其余（axios 等）交给 rollup 默认算法，不写 catch-all（见上文 elementPlusScope 注释）
}

// 构建产物输出到 dist，base 设为根路径，便于 Go 后端直接托管
export default defineConfig({
  plugins: [vue()],
  base: '/',
  server: {
    port: 5173,
    proxy: apiProxy
  },
  preview: {
    port: 5288,
    proxy: apiProxy
  },
  build: {
    outDir: 'dist',
    assetsDir: 'assets',
    // vite 默认值就是 500，收回来当回归警报用（原先抬到 1500 只是把 2.8MB 单包的告警按掉）。
    // 仍会有两个 chunk 超限并打印告警，这是刻意留着的、有意义的告警：
    //   vendor-echarts     1126kB —— 三个页面都是 `import * as echarts`，全量入包；
    //                                 要降只能改成 echarts/core + 按需 use()，得动 .vue
    //   vendor-element-plus 906kB —— main.js 是 app.use(ElementPlus) 全量注册，
    //                                 要降只能上按需引入（unplugin-*），得加依赖 + 动 .vue
    // 两者都不是「再拆一层 chunk」能解决的，所以不再抬阈值掩盖。
    chunkSizeWarningLimit: 500,
    rollupOptions: {
      output: {
        manualChunks
      }
    }
  }
})
