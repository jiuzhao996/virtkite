# Design System Master File

> **LOGIC:** When building a specific page, first check `design-system/pages/[page-name].md`.
> If that file exists, its rules **override** this Master file.
> If not, strictly follow the rules below.

---

**Project:** VMOps
**Generated:** 2026-09-04 20:28:59
**Category:** SaaS (General)
**Design Dials:** Density 8/10 (Dense / Dashboard)

---

## Global Rules

### Color Palette

> **品牌叙事（鸢航 VirtKite）**：深空蓝海面 + 鸢蓝纸鸢 + 金色牵线。交互主色用鸢蓝，深色表面用深空蓝，金色仅作点缀（不作正文，对比度不足）。

| Role | Hex | CSS Variable |
|------|------|--------------|
| Primary（交互主色：按钮/链接/激活态） | `#2E6BD6` | `--color-primary` / `--el-color-primary` |
| On Primary | `#FFFFFF` | `--color-on-primary` |
| Secondary（深空蓝：次级强调/深色点缀） | `#1B3A62` | `--color-secondary` |
| Accent/CTA | `#2E6BD6` | `--color-accent` |
| Kite Blue（鸢蓝浅版：hover/浅底强调） | `#6DB6FF` | `--color-kite` |
| Gold（金：仅装饰点缀） | `#FFD268` | `--color-gold` |
| Violet（紫色：分类标识，如镜像） | `#7C3AED` | `--color-violet` |
| Brand Deep 1（侧栏/登录渐变起点） | `#0D2444` | `--brand-deep-1` |
| Brand Deep 2（侧栏/登录渐变终点） | `#1B3A62` | `--brand-deep-2` |
| Background | `#F8FAFC` | `--color-background` |
| Foreground | `#1E293B` | `--color-foreground` |
| Card | `#FFFFFF` | `--color-card` |
| Muted | `#E9EFF8` | `--color-muted` |
| Muted Foreground | `#475569` | `--color-muted-foreground` |
| Border | `#E2E8F0` | `--color-border` |
| Success（运行/成功） | `#16A34A` | `--color-success` |
| Warning（暂停/警告） | `#D97706` | `--color-warning` |
| Danger（异常/删除） | `#DC2626` | `--color-danger` |
| Info（关机/中性） | `#64748B` | `--color-info` |
| Ring（焦点环） | `#2E6BD6` | `--color-ring` |

**Color Notes:** 浅色管理端 · 深空蓝 + 鸢蓝 + 金（KVM 私有云控制台，对标 1Panel / 公有云控制台密度）。

### Typography

- **Display / Body Font（拉丁/数字）：** Manrope（带几何辨识度）
- **CJK Font（中文）：** Noto Sans SC（回退 PingFang SC / Microsoft YaHei / 系统黑体）
- **Mood:** modern, professional, clean, technical, trustworthy
- **加载方式：** `index.html` 中 `<link>` 引入（已配 `preconnect` + `display=swap`），**不要**在 CSS 里 `@import` 整包。

**index.html：**
```html
<link rel="preconnect" href="https://fonts.googleapis.com" />
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
<link href="https://fonts.googleapis.com/css2?family=Manrope:wght@400;500;600;700&family=Noto+Sans+SC:wght@400;500;700&display=swap" rel="stylesheet" />
```

**CSS 变量：**
```css
--font-body: 'Manrope', 'Noto Sans SC', 'PingFang SC', 'Microsoft YaHei', system-ui, sans-serif;
--font-display: 'Manrope', 'Noto Sans SC', 'PingFang SC', 'Microsoft YaHei', system-ui, sans-serif;
--font-mono: 'Cascadia Code', 'Fira Code', ui-monospace, Consolas, monospace;
```

### Spacing Variables

*Density: 8/10 — Dense / Dashboard*

| Token | Value | Usage |
|-------|-------|-------|
| `--space-xs` | `2px` / `0.125rem` | Tight gaps |
| `--space-sm` | `4px` / `0.25rem` | Icon gaps, inline spacing |
| `--space-md` | `8px` / `0.5rem` | Standard padding |
| `--space-lg` | `12px` / `0.75rem` | Section padding |
| `--space-xl` | `16px` / `1rem` | Large gaps |
| `--space-2xl` | `24px` / `1.5rem` | Section margins |
| `--space-3xl` | `32px` / `2rem` | Hero padding |

### Shadow Depths

| Level | Value | Usage |
|-------|-------|-------|
| `--shadow-sm` | `0 1px 2px rgba(0,0,0,0.05)` | Subtle lift |
| `--shadow-md` | `0 4px 6px rgba(0,0,0,0.1)` | Cards, buttons |
| `--shadow-lg` | `0 10px 15px rgba(0,0,0,0.1)` | Modals, dropdowns |
| `--shadow-xl` | `0 20px 25px rgba(0,0,0,0.15)` | Hero images, featured cards |

---

## Component Specs

### Buttons

```css
/* Primary Button */
.btn-primary {
  background: #2E6BD6;
  color: white;
  padding: 12px 24px;
  border-radius: 8px;
  font-weight: 600;
  transition: all 200ms ease;
  cursor: pointer;
}

.btn-primary:hover {
  opacity: 0.9;
  transform: translateY(-1px);
}

/* Secondary Button */
.btn-secondary {
  background: transparent;
  color: #2E6BD6;
  border: 2px solid #2E6BD6;
  padding: 12px 24px;
  border-radius: 8px;
  font-weight: 600;
  transition: all 200ms ease;
  cursor: pointer;
}
```

### Cards

```css
.card {
  background: #F8FAFC;
  border-radius: 12px;
  padding: 24px;
  box-shadow: var(--shadow-md);
  transition: all 200ms ease;
  cursor: pointer;
}

.card:hover {
  box-shadow: var(--shadow-lg);
  transform: translateY(-2px);
}
```

### Inputs

```css
.input {
  padding: 12px 16px;
  border: 1px solid #E2E8F0;
  border-radius: 8px;
  font-size: 16px;
  transition: border-color 200ms ease;
}

.input:focus {
  border-color: #2E6BD6;
  outline: none;
  box-shadow: 0 0 0 3px rgba(46, 107, 214, 0.2);
}
```

### Modals

```css
.modal-overlay {
  background: rgba(0, 0, 0, 0.5);
  backdrop-filter: blur(4px);
}

.modal {
  background: white;
  border-radius: 16px;
  padding: 32px;
  box-shadow: var(--shadow-xl);
  max-width: 500px;
  width: 90%;
}
```

---

## Style Guidelines

**Style:** Flat Light · 扁平浅色管理端（Flat, clean, density-first）

**Keywords:** Flat surfaces, subtle shadows, semantic color, clear hierarchy, data-dense, 8px grid, brand blue

**Best For:** 运维/云控制台、B 端 dashboard、内部平台。对标 1Panel / 公有云控制台。

**Key Effects:** 浅灰页面底（`--color-background`）+ 白卡片（`--shadow-sm` 软阴影）；交互主色统一鸢蓝；深空蓝只用于侧栏/登录等品牌表面；金色仅点缀。

### Page Pattern

**Pattern Name:** Dashboard / 列表 + 卡片（管理端通用）

- **布局骨架：** 固定侧栏（可折叠 / 移动端抽屉）+ 顶栏（全局搜索 + 任务铃 + 用户）→ 内容区 `router-view`。
- **列表页：** 工具栏（主操作实底 primary，次要操作 plain）+ 筛选栏 + 卡片网格（`auto-fill minmax`，窄屏塌缩单列）或 `el-table`（桌面）。
- **仪表盘：** 统计卡（可点跳转）→ 资源大盘 → 状态环/容量 → 实时性能表 → 告警/平台信息。
- **密度：** Density 8/10，正文 14px、次要 12px、卡片标题 15px。**不**为「大字展示字体」牺牲信息密度。

---

## Anti-Patterns (Do NOT Use)

- ❌ 纯白/纯灰平铺背景无层次（用 `--color-background` + 白卡 + 软阴影营造层次）
- ❌ 两套品牌色混用（深空蓝 vs 鸢蓝必须区分：深空蓝=深色表面，鸢蓝=交互主色）
- ❌ 金色 `#FFD268` 用于正文/按钮文字（对比度不足，仅装饰）
- ❌ 超大展示字体挤压控制台信息密度
- ❌ 移动端无抽屉/无塌缩（侧栏必须转抽屉，表格/网格塌缩单列）
- ❌ Excessive animation
- ❌ Dark mode by default（当前仅维护浅色主题）

### Additional Forbidden Patterns

- ❌ **Emojis as icons** — Use SVG icons (Heroicons, Lucide, Simple Icons)
- ❌ **Missing cursor:pointer** — All clickable elements must have cursor:pointer
- ❌ **Layout-shifting hovers** — Avoid scale transforms that shift layout
- ❌ **Low contrast text** — Maintain 4.5:1 minimum contrast ratio
- ❌ **Instant state changes** — Always use transitions (150-300ms)
- ❌ **Invisible focus states** — Focus states must be visible for a11y

---

## Pre-Delivery Checklist

Before delivering any UI code, verify:

- [ ] No emojis used as icons (use SVG instead)
- [ ] All icons from consistent icon set (Heroicons/Lucide)
- [ ] `cursor-pointer` on all clickable elements
- [ ] Hover states with smooth transitions (150-300ms)
- [ ] Light mode: text contrast 4.5:1 minimum
- [ ] Focus states visible for keyboard navigation
- [ ] `prefers-reduced-motion` respected
- [ ] Responsive: 375px, 768px, 1024px, 1440px
- [ ] No content hidden behind fixed navbars
- [ ] No horizontal scroll on mobile
