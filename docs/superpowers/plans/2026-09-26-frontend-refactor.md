# 前端重构总计划（五阶段，对标 1Panel 粒度）

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans（Native）。每阶段独立提交，构建+冒烟通过才进下一阶段。

**Goal:** 按阶段 0→4 把四个巨型 view（DockerList 1601 / VmDetail 1596 / Wizard 1430 / Console 1316 / Dashboard 1088）拆到 1Panel 粒度（薄壳 200-400 行 + 子组件），并收敛重复逻辑为 composables/公共组件。

**Architecture:** 搬代码不改行为（纯重构红线）；拆分目标为 `views/<module>/` 薄壳 + components 子组件 + utils/composables 收敛；保留 el-tabs（不学 1Panel 路由式 tab）；纯 JS + `<script setup>`。

**Tech Stack:** Vue3 + Element Plus + 现有构建链；零新依赖。

## Global Constraints

1. 每阶段可独立中断/回滚：独立 commit；完成后 `npm run build` + `/tmp/pwshot` 浏览器冒烟。
2. 红线：拆分提交里禁止夹带行为变更——想改逻辑记 TODO 另开任务。
3. 保留 el-tabs + hash 路由 + `embedded` prop 既有模式；lazy tab 必须维持 `@tab-change` visitedTabs 惯例。
4. 图标名先查 `web/node_modules/@element-plus/icons-vue/dist/types/components/index.d.ts` 再用。
5. 后端零改动（除纯删除）；`go test -race ./...` 全绿为准。

## Phases

- [x] **Phase 0 热身**：`utils/docker-format.js`（DockerList 格式化纯函数外迁）；`components/CopyButton.vue`；`components/StateTag.vue`
- [x] **Phase 1 composables**（useAutoRefresh 四变体收敛；usePagination 实测仅 2 处真实分页，3 文件诚实放弃）：`useAutoRefresh`（4 处变体收敛）；`usePagination`（5 个列表页收敛）
- [x] **Phase 2 DockerList 拆分**：`views/docker/index.vue` 207 行壳 + 8 组件（1601 行消解；CopyButton 顺手接入两抽屉，终结 P0 遗留）
- [x] **Phase 3 VmDetail 拆分**：壳 550 + VmPerfCard/VmHardwarePanels/VmHardwareDialogs/VmSnapshotCard/VmGrantCard（1596 行消解）：壳 + VmPerfCard / VmSnapshotCard / VmGrantCard / VmHardwareDialogs
- [x] **Phase 4 Dashboard 拆分**（提前完成：1088→389 行壳 + 5 卡片）：壳 + 5 卡片组件
- [ ] **Phase 5 收尾**：docs 同步 + devlog + 全量回归 + 推送

## Review Focus

| 场景 | 护栏 |
|---|---|
| 拆分后 tab 切换丢状态/白屏 | 每阶段冒烟含 tab 切换步骤；lazy+visitedTabs 惯例逐处核对 |
| 抽函数时行为漂移（时间格式/空值文案） | Phase 0 抽取前先记录原函数输出样例，抽取后 diff |
| composable 生命周期泄漏（定时器/监听器） | useAutoRefresh 单测 + 组件卸载清理断言 |
| 事件/props 改名漏改消费者 | 每次搬迁后 `grep` 全仓旧名 |
| 构建过但运行崩（图标 undefined 类） | 浏览器冒烟含 pageerror 监听 |
