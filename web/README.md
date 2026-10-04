# vmops-web · 前端工程

基于 **Vue 3 + Vite + Element Plus + vue-router** 的管理前端，构建产物 `dist/` 由后端 `vmops` 直接托管。

## 技术栈

- Vue 3（组合式 API）
- Vite 5（构建 / 开发服务器）
- Element Plus（UI 组件库）+ @element-plus/icons-vue
- vue-router 4（hash 模式路由，懒加载）
- axios（API 请求）
- ECharts（监控/拓扑图表）、@antv/x6（架构设计器画布）、@xterm/xterm（终端）、marked + DOMPurify（Markdown 渲染）

## 目录结构

```
web/
├── index.html              # 入口 HTML（品牌 favicon 三件套 + 标题）
├── vite.config.js          # Vite 配置（base、dev 代理、manualChunks 分包）
├── package.json
├── public/                 # 静态资源（brand/ 品牌 logo 与 favicon）
└── src/
    ├── main.js             # 应用入口（注册 Element Plus / 路由 / 图标全局注册）
    ├── App.vue             # 根组件（启动拉取用户信息 + 深色模式）
    ├── api/index.js        # axios 实例 + 全部接口封装
    ├── store/auth.js       # token / 用户响应式状态 + 路由守卫
    ├── router/index.js     # 路由表与鉴权守卫（懒加载）
    ├── layout/MainLayout.vue  # 侧栏（二级菜单）+ 顶栏布局
    ├── components/         # 公共组件（PageHead/Toolbar/AlertBell/VmFileBrowser/ContainerTerminal/CopyButton/DockerGate）
    ├── composables/        # useAutoRefresh / useChart / usePagination
    ├── utils/              # format / clipboard / echarts / term-theme 等工具
    └── views/              # 28 个顶层页面 + 按域子目录（子页与组件）
        ├── Login.vue / Dashboard.vue / VmList.vue / VmDetail.vue / ...（总览与资产页）
        ├── console/ images/ storage/ monitor/ vm-detail/ wizard/ ...（按域组件目录）
        ├── docker/         # Docker 管理（1Panel 式子路由）
        └── apps/StackStore.vue、network/FlowView.vue、monitor/panels.js
```

## 脚本

| 命令 | 说明 |
|------|------|
| `npm install` | 安装依赖 |
| `npm run dev` | 启动开发服务器（默认 5173），代理 `/api` 到 `http://localhost:8080` |
| `npm run build` | 生产构建，产物输出到 `dist/` |
| `npm run preview` | 本地预览构建产物 |

## 与后端联调

- **开发模式**：`npm run dev` 启动 Vite，浏览器访问 `http://localhost:5173`；所有 `/api` 请求经 Vite 代理转发到后端 `:8080`，无需处理跨域。
- **生产模式**：`npm run build` 后，后端 `main.go` 自动托管 `web/dist`（`/` 返回 index.html，`/assets/*` 返回静态资源），访问后端地址 `:8080` 即可，无需独立 Web 服务器。

## 约定

- 统一响应解包：后端返回 `{code,message,data}`，`api/index.js` 的 `unwrap` 直接返回 `res.data`，页面取 `res.data.xxx`。
- 鉴权：登录后 token 存入 `localStorage`，axios 请求拦截器自动附加 `Authorization` 头；响应拦截器在 401 时跳回登录页。
- 权限：路由守卫限制未登录访问；管理员专属接口由后端 `AdminMiddleware` 强制校验，前端仅做菜单/按钮层面的展示控制。
- 主题：顶栏开关切换深色模式（`html.dark` + Element Plus dark CSS 变量），偏好存 `localStorage` `vmops-theme`。

## 相关文档

- 后端总览：[/README.md](/README.md)
- 部署：[/docs/07-部署与运维.md](/docs/07-部署与运维.md)
