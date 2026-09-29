# 鸢航 VirtKite 前端总体规划（设计规范 + 信息架构 + 路线图）

> 定位：本文档是前端的**单一事实源**——新页面/新组件/改交互，先对本文档的规则；规则缺失时按「设计原则」推导，并把结论补回本文。
> 版本：v1.0（2026-09-26，前端重构五阶段收官后首次成文）。素材：ui-ux-pro-max 规则体系、1Panel/pure-admin 对标、跳板机式 B 端控制台惯例、本仓库既有代码惯例。

---

## 1. 产品定位与三条用户动线

**B 端运维控制台**，三类角色三条动线，一切设计决策向这三条动线倾斜：

| 角色 | 核心动线 | 设计含义 |
|---|---|---|
| 学生（operator） | 找到机器 → 用机器 → 申请资源 | 列表页信息密度优先；申请入口常驻资源组 |
| 教师（admin 的一部分） | 批量授权 → 审批 → 巡检状态 | 批量操作与状态可视化优先 |
| 管理员（admin） | 配置平台 → 审计 → 处理告警 | 设置集中、审计可达、告警突出 |

## 2. 设计原则（决策时的优先级序）

1. **状态可见**：每个异步动作必须有 loading 或 busy 反馈；每个列表必须有空态文案（哪怕一行字）。
2. **危险分级**：普通操作直接执行；写操作走确认弹窗；不可逆操作输入名称确认；危险命令流内拦截。
3. **深链优先**：能进 URL 的状态都进 URL（tab、筛选、节点）；刷新/收藏/分享不断链。
4. **单页单职责**：一页只回答一个问题。超过 5 个并列关注点就拆子路由（Docker 拆分先例）。
5. **组件常驻**：分区切换用 `v-show`（详情页）/ KeepAlive（路由页），切走切回不丢状态。
6. **fail-closed 呈现**：不可用的东西不给入口（未授权 404、禁用菜单 operateOnly、503 页面级 alert）。
7. **无障碍基线**：焦点环保留、图标钮带 aria-label、对比度 4.5:1、触达区 ≥44px。

## 3. 信息架构（站点地图 v2）

```
总览
└─ 仪表盘 /#dashboard            tabs: 概览 | 监控 | 拓扑（lazy + visitedTabs）
资源
├─ 虚拟机 /#vms                  卡片网格 + 批量操作 + 回收站软删
│   └─ /#vms/:id 详情            左菜单 activeView（v-show 十分区，v3.6 授权统一面板）
├─ 镜像管理 /#images             tabs: 云镜像 | 模板盘 | ISO（+镜像市场嵌入）
├─ 应用商店 /#apps               operateOnly
└─ 资产申请 /#grant-requests     operateOnly；admin 同页切审批台（角色自适应）
基础设施
├─ 宿主机 /#hosts
├─ 存储池 /#storage              池卡片 + 卷抽屉
├─ 网络 /#networks               卡片网格
└─ Docker 管理 /#docker          ★路由化先例：按钮条 + 5 子路由（KeepAlive）
    ├─ /#docker/containers       （容器 tab 含终端/日志/inspect 抽屉 + 创建抽屉）
    ├─ /#docker/images  /networks  /volumes  /compose
运维
├─ 任务中心 /#tasks              异步任务 + 进度 + 详情抽屉
├─ 审计中心 /#audit              tabs: 操作日志 | 会话列表（跳板 blocked 落此）
├─ 计划任务 /#crons              + 执行历史抽屉（分页）
└─ 回收站 /#recycle-bin          adminOnly
管理
├─ 工具箱 /#toolbox              adminOnly
├─ 用户管理 /#users              tabs: 用户 | 用户组（组=教学批量授权）
└─ 系统设置 /#settings           六卡片（运行参数含命令黑名单 / AI / 安全 / 密钥 / 告警 / 公告）

独立路由：/#login  /#/console/:id（全屏控制台，脱离 MainLayout）
顶栏常驻：全局搜索（VM 直达）｜AI 助手抽屉｜任务铃｜角色徽标｜用户下拉
```

**菜单规则**：5 组封顶；单项目组平铺不渲染组标题；`operateOnly`/`adminOnly` 决定可见性；子路由页复用父菜单高亮（activeIndex 取路径首段）。

## 4. 导航与交互模式规范（从实战沉淀的四选一规则）

新增页面/功能时，按此决策树选容器：

| 模式 | 何时用 | 已落地先例 |
|---|---|---|
| **el-tabs**（页内 tab） | 关注点 ≤3 且切换频繁、轻量 | 仪表盘三 tab、审计中心、镜像管理、用户页双 tab |
| **子路由按钮条**（1Panel 式） | 关注点 ≥4、每页有独立数据域、需要深链 | Docker 管理五页（**后续新功能照此**） |
| **抽屉 drawer** | 不离开上下文的次级操作/详情（≤1 层） | inspect/日志/任务详情/创建容器/安装应用 |
| **对话框 dialog** | 表单录入与确认；**必须 `:close-on-click-modal="false"`** | 16 个表单弹窗（Vue Boolean prop 用 `:v-bind`，勿用静态字符串） |
| **独立页** | 流程 ≥3 步（向导）或需要沉浸（控制台全屏） | CreateVmWizard、ConsolePage |

其他硬规则：
- 详情页分区切换：`activeView + v-show`（**禁改 v-if**——图表/滚动状态随切走切回必须保留）
- lazy tab：`@tab-change` 维护 visitedTabs（Dashboard/审计先例），漏绑=白屏
- 交叉刷新：子组件补 resize 用 tick 信号（`overviewTick`/`active-tick` 先例），不在壳里持子组件 chart 实例
- KeepAlive 页的定时器：`onActivated/onDeactivated` 驱动（Docker 容器页先例），勿依赖 onUnmounted

## 5. 视觉规范（Design Tokens，来源 web/src/global.css）

### 5.1 色板

| 令牌 | 值 | 用途 |
|---|---|---|
| `--color-primary` | `#2a9da5` 青绿 | 按钮/链接/激活/选中 |
| `--brand-deep-1/2` | `#2a9da5 → #217d83` | 侧栏渐变（品牌区唯一渐变） |
| `--color-danger` | `#dc2626` | 删除/危险（深色底用 `#f87171` 系） |
| 成功/警告/信息 | `#16a34a` / `#d97706` / `#64748b` | 状态语义色（VM 状态、tag） |
| 金色 | 仅品牌点缀（logo 牵线/鸢眼），UI 不使用 | |

**规则**：组件内禁止裸 hex（echarts 取色用 `cssVar()` 工具），一律 CSS 变量；品牌回归青绿，勿再改蓝（历史决策）。

### 5.2 布局与字体

- 8px 栅格：`--space-lg: 12px`、主区 padding 24px（`--space-2xl`）、移动端 16/12px
- 字号阶梯：正文 14px / 页标题 1.1rem / 辅助 0.9rem / mono 0.85rem（IP/ID/时间一律 `.mono`）
- 圆角：按钮 10px（`--radius-md`）、卡片 10px、tag 用 EP 默认
- 焦点环：`:focus-visible` 2px 主色描边，**禁止移除**

### 5.3 状态与反馈

- 异步按钮：`:loading`；行级操作用行级 busy 集合（DockerList `rowBusy` 先例）
- 消息层级：操作结果 ElMessage（顶部）；进行中任务 → 任务铃；表单校验 → 字段旁；危险确认 → ElMessageBox（不可逆的加 `confirmButtonClass: danger` + 输入名称）
- 空态：每个表格/卡片必须有空态文案（el-empty 或一行灰字），首次使用给行动按钮

## 6. 组件与代码规范

### 6.1 目录与分层

```
web/src/
├── views/<module>/          # 路由页；巨型页拆 index.vue 薄壳 + components/ 子组件
│   └── components/          # 仅该模块使用的子组件
├── components/              # 跨模块公共组件（CopyButton、VmFileBrowser、ContainerTerminal）
├── composables/             # useAutoRefresh / usePagination（新增前先查是否已有）
├── utils/                   # format / docker-format / clipboard / settings —— 纯函数，零状态
└── layout/MainLayout.vue    # 壳：侧栏/顶栏/抽屉（页面不碰壳职责）
```

### 6.2 页面公式（列表页三段式）

```
page-head（标题 + 描述 + 右侧主按钮）
└── el-card shadow="never"
    ├── toolbar（左：筛选/主操作 ｜ 右：计数 + 刷新 + 次操作）
    ├── 数据区（el-table 或卡片网格 el-row/el-col）
    └── 空态（el-empty + 行动按钮）
```

详情页公式：页头（返回 + 名称 + 状态 + 电源操作钮）+ 左菜单（el-menu，activeView）+ 右侧 v-show 分区。

### 6.3 已固化惯例（新代码必须遵守）

- 表单对话框：`:close-on-click-modal="false"`（Boolean prop 用 `:` 绑定，**静态字符串是坑**）
- 状态 tag：颜色语义固定（running 绿 / exited 灰 / 其他橙），文案进 `utils/docker-format`
- 复制：`utils/clipboard.copyText` + `components/CopyButton.vue`（不再手写 navigator.clipboard）
- 自动刷新：`composables/useAutoRefresh`（键名沿用 `vmops-*`，勿新造键）
- 分页：`composables/usePagination`（仅服务端分页场景）
- 轮询周期：`utils/settings.getPollInterval(key, POLL_DEFAULTS[key])`
- 图标：`@element-plus/icons-vue` 且**先查 index.d.ts 再写**（写错=静默空白）；显式 import
- 删除已迁出函数后：grep 旧名清零再提交（DockerList 白屏教训）

## 7. 差距清单与路线图（v1.0 时点）

### 已达标 ✅
信息架构（5 组 13 项）、Docker 子路由化、授权面板统一化、四个巨型文件拆分（DockerList 207/VmDetail 550/Dashboard 389）、composables×2、公共组件、防误触、深链（tab/节点/筛选入 URL）、无障碍基线。

### 待办（按收益排序，非本次实施）
| 项 | 说明 | 优先级 |
|---|---|---|
| PageHead/Toolbar 组件化 | page-head 模板在 21 个文件重复，抽 `<PageHead>` + `<Toolbar>` 两组件 | 高（下批次首选） |
| CreateVmWizard 分步组件化 | 1430 行，四步各一组件（向导天然内聚，此前刻意保留——做 PageHead 时顺带评估） | 中 |
| 移动端体验补全 | 已有侧栏抽屉/响应式断点，但表格类页面小屏横向滚动未专门设计 | 中 |
| 深色模式 | 令牌已集中（global.css CSS 变量），缺一套 dark 值 + EP dark 切换 | 低（答辩后） |
| 状态筛选入 URL | 列表页筛选入查询参数（Docker 树节点先例），刷新还原 | 低 |
| 表格工具行组件化 | Toolbar 三件套（筛选/主操作/右图标组）模式重复，可抽 slots 组件 | 低 |

### 不做（明确）
路由式 tab 全面替代 el-tabs（1Panel 为多标签页服务，本场景无收益）；深色模式的第二品牌色；多语言。

## 8. 变更流程

新页面/新交互落地前：查本文 §4 决策树 → §6 惯例 → 实现后跑 `/tmp/pwshot` 冒烟（含 pageerror 监听）→ 涉及视觉的截图对比 → 把新结论补回本文对应章节。
