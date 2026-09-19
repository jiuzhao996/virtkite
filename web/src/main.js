import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
// Element Plus 中文语言包：修复全站确认框 / 分页 / 日期组件英文 OK/Cancel 问题
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import './global.css'
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
