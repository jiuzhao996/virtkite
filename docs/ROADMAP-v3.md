# 鸢航 VirtKite v3 改造规划（对标 1Panel v2）

> 参照物：1Panel v2（Go+Gin+GORM+Vue3 同源技术栈，官方文档 1panel.cn/docs/v2）。
> 定位不变：**基于 KVM 的轻量级私有云管理平台**——v3 吸收 1Panel 的面板能力，但不做建站/WAF/多机付费赛道。
> 分批实施：每批独立可交付、独立验证，批间无强依赖，用户可逐批拍板砍/留。

## 前置研究结论（两份子代理调研报告摘要）

- 1Panel v2 = Core（面板）+ Agent（节点执行）双进程，SQLite + GORM + gormigrate；社区版实质单机面板（多机付费）→ **Core/Agent 不抄**，我们单机定位已定。
- 模块清单对比：概览/监控/终端/文件管理/操作审计/容器管理我们已有；差距集中在「应用商店机制」「计划任务细节」「安全设计」「AI」「工具箱」。
- 应用商店的灵魂：**应用 = 声明式表单（data.yml formFields）+ docker-compose.yml（${VAR} 占位）**，安装时生成 .env 后 `docker compose up -d`——零安装脚本，与容器管理天然打通。

## 批次 A：应用商店 v2（1Panel 式声明式应用包）★主菜

**目标**：把 bash/SSH 脚本商店升级为「声明式 compose 应用包」，应用跑在宿主机容器里，与 Docker 管理打通。

- 包结构（conf/appstore/ 内置 + go:embed）：
  ```
  conf/appstore/<key>/
  ├── data.yml                # 应用级：name/title/tags/category/description
  └── <version>/
      ├── data.yml            # 版本级：formFields 表单定义
      └── docker-compose.yml  # ${VAR} 占位模板
  ```
- formFields 字段（砍到最小集）：`envKey/label/type(text|number|password|select)/default/required/rule(paramPort|paramCommon)/values(select 选项)`
- 渲染机制（抄 1Panel 原味）：安装 = 拷贝版本目录到 data/apps/<key>/ → 按表单值写 .env → `docker compose -f docker-compose.yml --env-file .env up -d`。**不引模板引擎**。
- 校验：rule 落成 Go 函数（paramPort→1-65535、paramCommon→字符集）；必填校验
- 任务化：安装/卸载走 tasks Manager（新增 app_compose_install / app_compose_uninstall executor）
- 卸载：`docker compose down` + 保留数据目录（不删卷——沿用 shouldKeepVol 立场）
- 内置应用首期 6 个：nginx、redis、mysql、wordpress（含内置 mysql）、portainer、uptime-kuma（监控探针，好看）
- 前端：AppStore 页改造——商店卡片加「容器应用」标签；安装抽屉按 formFields **动态生成 el-form**（type→控件映射）；已装列表（读 data/apps 目录 + docker compose ps 状态）
- 现有 SSH 脚本版 10 应用保留，商店卡片加「VM 应用」标签——两类并存
- 毕设叙事：「容器应用（宿主机 compose）+ VM 应用（SSH）」两种安装形态的对比，本身就是答辩加分点

## 批次 B：AI 运维助手 ★差异化亮点

**目标**：平台内置 AI 聊天，套 OpenAI 兼容 API（用户自填 API Key），并注入平台环境上下文——它是"看得懂你服务器"的助手，不只是聊天框。

- 后端：
  - system_settings 白名单新增三键：`ai_base_url`（如 https://open.bigmodel.cn/api/paas/v4 或 https://api.deepseek.com/v1）、`ai_api_key`、`ai_model`
  - `POST /api/ai/chat`：OpenAI 兼容 /chat/completions 代理（Key 只存服务端不下发前端）；SSE 流式转发
  - 上下文注入：system prompt 自动附当前平台摘要（VM 数量/运行数/容器数/最近告警）；进阶——`POST /api/ai/chat` 带 `with_context=true` 时附 VM 列表概要
  - 权限：OperatorMiddleware（viewer 不可用）；API Key 未配置时 400 + 前端引导去设置页
- 前端：
  - 新页面「AI 助手」：聊天 UI（消息气泡 + 输入框 + 流式打字机效果 + 清空会话）
  - 系统设置页新增「AI 设置」卡片（Base URL/Key/模型名，Key 掩码显示）
- 毕设叙事：AI 助手知道你的环境（"我有几台虚拟机在跑？哪台 CPU 最高？"），可演示自然语言运维查询
- 边界：不做 function calling 自动执行操作（写操作走人工，安全边界清晰——答辩话术现成）

## 批次 C：计划任务增强（抄 1Panel 细节）

- 保留份数：scheduled_tasks 加 `keep` 字段；vm_snapshot/db_backup 执行后清理超出份数的旧产物（快照按名前缀、备份按文件序）
- 执行历史：新表 `cron_runs`（task_id/started/finished/status/output 摘要）；CronList 加「执行记录」抽屉
- 失败通知：执行失败写一条 alerts 告警（复用告警历史表，监控中心可见）

## 批次 D：安全入口 + 密码策略

- 安全入口：启动时生成随机后缀（settings 可改），`/login` 不带后缀 404，仅 `/login/<suffix>` 可登录——防扫描爆破；提供「忘后缀 SSH 重置」运维说明（抄 1Panel 应急思路）
- 密码策略：改密/建用户时强制最小长度 + 复杂度校验（可配置）
- 登录限流已有（loginLimiter），补：连续失败锁定状态在审计可见

## 批次 E：工具箱（轻量）

- 缓存清理：镜像 dangling 清理（docker image prune）、旧任务记录清理、审计日志归档导出
- 进程列表：宿主机 top 进程（ps 排序，只读展示）
- 磁盘占用：/ 挂载点 Top 消耗（du 定向）
- 单页聚合，卡片入口

## 批次 F（可选，视前五批完成度）：概览页 1Panel 式改版

- 仪表盘顶部加系统信息大卡（OS/内核/uptime/负载）
- 容器/应用状态聚合卡

## 增补批次（第二轮研究 + 用户补充确认后追加）

### 背景补充
1Panel「高级功能」中的虚拟机管理（专业版，基于容器化方案）、网站防篡改、WAF、高可用为企业版能力；我们的 KVM 原生 libvirt 直连相对其是**更完整的主航道能力**（答辩差异化论证点）。

### 批次 G：日志栈（Loki 路线，替代 ELK）
- 技术决策：**Loki + Promtail**（Grafana 原生，内存 <500MB）替代 ES+Logstash+Kibana+Kafka+Filebeat（5 组件 6-8GB，毕设机器跑不动）——形成「指标 Prometheus + 日志 Loki + 告警 Alertmanager + 可视化 Grafana」完整可观测性四件套
- 落地：应用商店 v2 内置「Loki 日志栈」compose 包（loki+promtail，采集宿主机与容器日志）；监控中心新增「日志」查询入口（Grafana Explore 嵌入或 LogQL API）
- ELK/Kafka 写入论文「企业级日志方案对比」节（选型论证即答辩加分）

### 批次 H：云镜像市场（KVM 平台特色）
- 内置主流发行版 cloud image 下载源（Ubuntu/Rocky/Debian/Alma 官方 URL 清单）
- 一键下载到 base 池（后台任务带进度）→ 自动登记进镜像库（复用 RegisterImage）
- 前端：镜像管理页新增「镜像市场」tab（卡片 + 下载按钮 + 进度）

### 批次 I：SSH 凭据托管（提升应用商店/文件管理体验）
- 新表 vm_credentials（vm_id 唯一，password AES-GCM 加密落盘，密钥派生自 JWT_SECRET + 随机盐）
- 应用商店安装/文件管理连接时可勾选「使用已保存凭据」免输入
- 仅 admin/operator 可写；凭据永不回传前端（掩码显示）

### 批次 J：VM 导出/导入
- 导出：关机 VM 的 qcow2 + domain XML 打包 tar.gz，浏览器下载（大文件流式）
- 导入：上传 tar.gz → 恢复卷 + define（复用 ImportVMs 思路）
- 定位：VM 迁移与备份的兜底手段

### 批次 K：VM 拓扑可视化
- echarts graph：宿主机—虚拟机—网络—存储池连线图（节点 = 资源，边 = 挂载/连接关系）
- 数据：现有 API 聚合（VM 列表 + 网络列表 + 存储池列表），无新后端
- 入口：仪表盘新 tab「拓扑」

### 批次 L：cloud-init 模板管理
- 新表 cloud_init_templates（name + CloudInitSpec JSON）
- 建机向导 cloud-init 块加「套用模板」下拉；模板 CRUD 入口在镜像管理或设置页
- 解决「每次建机重复填 hostname/user/password」的体验问题

### 与 1Panel 高级功能的对位（答辩用）
| 1Panel（专业版/企业版能力） | 本平台 | 对位说明 |
|---|---|---|
| 虚拟机管理（容器化方案） | KVM 原生 libvirt 直连（主功能） | 我们更贴近 KVM 本质，无容器化中间层 |
| 网站防篡改 / WAF / 高可用 | 不做 | 建站与企业高可用赛道，与 KVM 私有云定位冲突，写展望 |
| 日志审计（6 类） | 操作审计 + v3 批次 G 日志栈 | 打平并形成完整可观测性 |
| AI（MaxKB 等应用） | 批次 B 内置 AI 运维助手（环境感知） | 我们是平台内生能力，非外挂应用 |

### 题目建议（供决策，改题需走教务流程）
1. 基于 KVM 与 Docker 的私有云管理与智能运维平台设计与实现（体现容器 + AI）
2. 保持原题「基于 KVM 的轻量级私有云管理平台设计与实现」，扩展能力在摘要与章节体现（零流程成本，内容已足够支撑）
建议 2：题目保守、内容惊艳。

## 明确不做（理由记录，防跑偏）

- Core/Agent 多机架构（单机定位；多宿主机已砍，写展望）
- 网站建站/OpenResty/WAF/运行环境托管（运维面板赛道，稀释 KVM 定位）
- 数据库库表管理（可作 v4 展望）
- 上传自定义应用/远端商店同步/多语言/多架构镜像
- AI 自动执行写操作（安全边界：AI 只读问答，写操作必须人工）

## 验收与版本管理

- 每批完成：build + test -race 全绿 + 本地 commit（**推送与否按用户指示**）
- 全部完成：统一 E2E（API 三态 + 前端截图）+ 更新本文件标记落地状态
- 回退锚点：`47276ad`（v2 前）/ `aced919`（v2 完成态）
