# 视觉改版批次 A：方向「现装微调」质感抛光 — 设计规格

日期：2026-10-02 ｜ 状态：待用户审核 ｜ 上游决策：五批路线收官批次（brainstorming 共创，用户已拍板方向）

## 一、定位与边界

- **方向**：A · 现装微调——保留品牌青绿（#2a9da5→#217d83）与现有信息密度（8px 栅格、表格行高 50px 不动），只做质感抛光与一致性收尾。不换组件库、不引入新依赖、不动布局结构、不碰批次 1 已收敛的 API/组件拆分。
- **覆盖**：令牌层（`web/src/global.css`）全站生效；高频 8 页逐页精修（Dashboard / VmList / VmDetail / StorageList / ImageList / Monitor / ConsolePage 选择页 / Login），低频页只吃令牌红利不逐页动。
- **主题**：亮/暗双主题同步抛光（深色是半成品状态，本轮拉齐）。

## 二、四小批递进（每批独立构建验证、独立提交、可回退）

### 批 ① 令牌升级 + 卡片质感统一
- global.css 新增语义阴影三档：`--elev-card` / `--elev-hover` / `--elev-pop`（亮暗各一套），页面散落的 box-shadow 归一到这三档（grep 全站 `box-shadow` 逐一判定替换，约 30+ 处）。
- 卡片圆角统一走现有 `--radius-md`，Dialog 维持 1Panel 对标 `--dialog-radius: 5px` 不动。
- 验收：任一高频页卡片悬停有统一的浮起层次；暗色下阴影不脏（暗色阴影用加深+透明而非纯黑）。

### 批 ② 表格与状态徽标
- 行 hover 高亮令牌化：`--row-hover-bg`（亮暗各一），替换各页自写的 hover 底色。
- 状态徽标浅底四色（`--status-*-bg`）在暗色下过暗的问题修正：暗色值整体提亮一档，保证徽标在暗底可辨。
- 操作列规范收口：text 型按钮为主、每页主操作（删除类）唯一化，参考 TaskList/HostList 现有好例子对齐其余页。
- 验收：表格页 hover/徽标观感页页一致；暗色下徽标可读。

### 批 ③ 微动效
- 新增动效令牌：`--ease-standard: cubic-bezier(0.2,0,0,1)`、`--dur-fast: 150ms`、`--dur-base: 200ms`。
- 应用点：卡片悬停浮起（transform + shadow 过渡）、页面进入淡入（.el-main 子级 12ms opacity）、按钮按压反馈（EP 自带，仅校准）。`prefers-reduced-motion` 全局降级已有（global.css 既有规则），新动效全部落在其覆盖面内。
- 禁区：不做路由转场动画、不做滚动视差、不做任何 >250ms 的动画。
- 验收：动效克制到「感觉不到动效但操作跟手」；reduced-motion 下全部静止。

### 批 ④ 骨架屏 + 空态统一
- global.css 新增 `--skeleton-base` / `--skeleton-shine` 令牌与 `.skeleton` / `.skeleton-text` 通用类（微光扫过动画，reduced-motion 降级为静止灰块）。
- 高频 8 页的首屏 v-loading 换骨架占位：Dashboard 统计卡、VmList 卡片网格、表格页首载（用 3 行骨架行）。表格内部刷新保留 v-loading（骨架行只用于首载，避免闪烁）。
- 空态统一：el-empty + 统一文案风格（动词开头 + 引导动作），补齐缺失空态的页面；41 处现有 el-empty 仅做文案对齐不重写。
- 验收：高频页首载无「白屏转圈」，骨架形状与真实内容形状匹配；空态页页同风格。

## 三、高频页精修清单（随批 ①④ 顺带，每页 15-40 行 scoped 微调）

| 页面 | 精修点 |
|---|---|
| Dashboard | 统计卡数字用 --font-display 加粗放大；告警卡红边只在 firing 时出现（已有，校准对比度） |
| VmList | 卡片 hover 浮起 + LIVE 心跳点微光；批量操作条质感 |
| VmDetail | 左侧分区导航激活态青绿竖条；描述列表键值对齐 |
| StorageList | 池卡容量条渐变统一；卷抽屉表头质感 |
| ImageList | 三 tab 切换过渡；市场卡片金边（serial-gold）与主青绿的边界校准 |
| Monitor | 图表卡边框与卡片阴影层次统一；面板卡标题栏质感 |
| ConsolePage | 选择页三卡 hover 浮起（现仅换边框）；暗色终端区与选择页亮区过渡 |
| Login | 品牌卡阴影升级 --elev-pop；输入框聚焦态主色光环统一 |

## 四、验收与流程

- 每批：`npm run build` + 浏览器亮暗双主题走查受影响页面 + 375 视口抽查 → commit（格式 `style(视觉抛光-批N)`）→ 推 gitee（github 顺手）。
- 全部完成后：8 页截图对照（改前/改后）作为答辩素材。
- 回退策略：每批独立 commit，任一批观感不佳 `git revert` 单批即可，令牌层改动全部集中在 global.css。

## 五、明确不做

换组件库 / 新依赖 / 布局重构 / 路由转场 / 表格虚拟滚动 / 低频页逐页精修（回收站、计划任务、授权申请等仅吃令牌红利）。
