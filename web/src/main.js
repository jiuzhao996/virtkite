import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import 'element-plus/theme-chalk/dark/css-vars.css'
// Element Plus 中文语言包：修复全站确认框 / 分页 / 日期组件英文 OK/Cancel 问题
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import './global.css'

// 主题（深色模式）：localStorage 'vmops-theme' = 'dark' | 'light'（默认 light）。
// html.dark 同时驱动 EP dark css-vars 与 global.css 的 html.dark 令牌覆盖
if (localStorage.getItem('vmops-theme') === 'dark') {
  document.documentElement.classList.add('dark')
}
import * as ElementPlusIconsVue from '@element-plus/icons-vue'
import App from './App.vue'
import router from './router'

const app = createApp(App)

for (const [key, component] of Object.entries(ElementPlusIconsVue)) {
  app.component(key, component)
}

app.use(ElementPlus, { locale: zhCn })
app.use(router)
app.mount('#app')
