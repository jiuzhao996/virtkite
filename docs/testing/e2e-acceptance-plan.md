# 鸢航 VirtKite · E2E 验收测试方案

> 定位：本文是**验收执行手册**。`AGENTS.md` 规定「用什么工具、门槛是多少」，本文规定「按什么链路点、点完看什么、出问题算谁的」。
>
> 版本：2026-10-17 首版（基于当日前端路由/控件与后端路由/中间件全量盘点）。
> 维护规则：新增页面/接口/建机方式时，**同一个提交里**补本文对应链路与 Playwright 用例，否则视为未完成。

---

## 0. 一句话原则

**验收不是「页面能打开」，而是「用户能通过界面把一条真实业务闭环走完，并且失败路径给出正确提示」。**

三层，缺一不可：

1. **能打开**：18 个页面加载、无 JS 异常、无 4xx/5xx。
2. **能点**：每个页面所有非破坏性控件逐个点击，打开即关，不产生副作用。
3. **能办成事**：按业务链路真实创建资源、等待后台任务、验证结果（状态/IP/SSH/XML/审计），再清理。

---

## 1. 测试分层与准入门槛

| 层 | 工具 | 覆盖内容 | 准入门槛 |
|---|---|---|---|
| L1 静态 | ESLint / golangci-lint / gosec / govulncheck | 语法、未定义变量、死分支、依赖 CVE、安全模式 | 0 error；无新增高危 |
| L2 单元 | `go test ./...` / Vitest | 纯函数、状态机、XML 构造、工具函数 | 全绿 |
| L3 接口 | `scripts/smoke.sh` / `scripts/e2e-provision.sh` | 只读接口、建机/删机任务、metrics、真实建机+装应用 | 断言全过 |
| L4 UI 冒烟 | Playwright + `scripts/ui-smoke.mjs` | 登录、18 页、深链、全按钮点击、axe | 0 failed、无 pageerror |
| L5 业务链路 | Playwright（本文第 7 章） | 建机各方式、生命周期、存储/网络/镜像、容器/应用、自动化、RBAC | P0 全过；P1 允许登记已知缺陷 |
| L6 性能/可访问性 | Lighthouse + axe-core | 登录页与核心页 | performance ≥70、a11y ≥95、best-practices 100 |

**「验收」默认跑 L1–L4 + L5 的 P0；发布前跑全量 L1–L6。**

---

## 2. 环境与账号

### 2.1 环境前提

| 项 | 要求 | 缺失时 |
|---|---|---|
| 后端 | `http://127.0.0.1:8080`（`BASE` 可覆盖） | 无法验收 |
| 前端产物 | `web/dist` 已构建（后端托管） | 先 `cd web && npm run build` |
| libvirt | `qemu:///system` 可连，至少一个可用存储池与 NAT 网络 | 建机链路标 BLOCKED |
| MySQL | `vmops-mysql` 或本机 MySQL | 无法验收 |
| Docker | 容器/应用链路需要 | 容器域 BLOCKED |
| Ansible | 自动化链路需要 | 自动化域 BLOCKED |
| 监控栈 | Prometheus / VictoriaLogs | 曲线/日志允许「未配置」降级，不算失败 |
| 外网 | 镜像市场下载；安装树可用本地 HTTP 模拟 | 下载用例 BLOCKED |

### 2.2 账号矩阵（口令只走环境变量，禁止入库）

| 角色 | 环境变量 | 能力边界（以中间件为准，见第 5 章） |
|---|---|---|
| admin | `ADMIN_USER` / `ADMIN_PASS` | 全部 |
| operator | `OPERATOR_USER` / `OPERATOR_PASS` | 读全平台；写仅 `/api/vms`；Docker 写（除创建容器）；VM 需被授权 |
| viewer | `VIEWER_USER` / `VIEWER_PASS` | 读大部分平台域；`/api/docker`、`/api/ai`、`/api/monitor`、`/api/logs` 全 403；VM 需被授权 |

> 缺失口令 → 对应用例 `test.skip`，报告标 `SKIPPED`，不得算 PASS。

---

## 3. 数据与安全策略（动手前先读）

### 3.1 命名与归属

- 所有测试资源统一前缀 `e2e-<runId>-`（`runId` 默认时间戳，`E2E_RUN_ID` 可覆盖）。
- **只允许删除/修改自己创建且带 `e2e-` 前缀的资源**。
- **守卫在 `helpers/api.js` 的 `api()` 里统一拦截，不在各用例重复写**：所有 `DELETE` 必经该函数，
  非 `e2e-` 前缀直接抛错、请求根本不发。放在这一层才无法被绕过——早期版本只在文档里写了
  「删除前过 `isE2EName()`」，实测 24 个删除点零调用，承诺等于落空（2026-10-18 修正）。
- 传参：`api(page, 'DELETE', path, undefined, { guard: <e2e 名> })`。数字型主键（VM/任务/容器 ID）
  无法从 URL 判断归属，**必须显式传 guard**，否则抛错。任务标题形如「创建虚拟机 e2e-xxx」时，
  用 `e2eGuardOf(title)` 抽出名称。
- 每轮结束按「VM → 快照/授权 → 镜像/卷 → 网络/池 → 用户/组」顺序清理；清理失败保留并打印，禁止强删。

### 3.2 危险操作开关

| 操作 | 默认 | 开启方式 |
|---|---|---|
| 删除自己创建的 e2e 资源 | 允许 | 默认开启 |
| 关机/重启/暂停自己创建的 e2e VM | 允许 | 默认开启 |
| 批量删除、清理未引用卷、清空回收站、清空 AI 会话 | **禁止** | `E2E_ALLOW_DESTRUCTIVE=1` 且目标全为 e2e 前缀 |
| 改系统设置、用户、凭据、网络/存储池 | **禁止** | 单独用例 + 显式开关 + 执行后还原 |
| 回滚快照 | 允许（仅 e2e VM 自己创建的快照） | 默认开启 |

### 3.3 执行顺序

1. 预检（服务/账号/池/网络/Docker/Ansible）。
2. 只读冒烟（18 页 + 全按钮）。
3. 创建类链路（建机 → 验证 → 清理）。
4. 修改类链路（启停/改配/快照/回滚）。
5. 破坏类链路（删除/回收站/守卫验证）。
6. 清理与报告。

### 3.4 现场保留

失败时保留：trace、截图、页面 URL、任务 ID、后端日志（`/tmp/vmops.log`）、`virsh dumpxml` / `qemu-img info` 输出。

---

## 4. 用例编号与分级

- 编号 `VT-<域>-<三位>`：`AUTH/VM/STG/NET/IMG/DKR/APP/AUT/TSK/AUD/USR/SET/CON/RBAC`。
- P0 主流程闭环（失败即阻断）；P1 主要分支与常见失败路径；P2 边界/极端输入。
- 每个用例写清：**前置 → 操作 → 预期 → 断言 → 清理**。

---

## 5. 权限矩阵（**以中间件实现为准，不以路由注释为准**）

盘点发现多处「注释写 operator+、实际 operator 403」的偏差。E2E 断言必须按本表：

| 路由组 | admin | operator | viewer |
|---|---|---|---|
| `/api/auth/*`、`/api/announcement`、`/api/vnc/token/:t` | ✅ | ✅ | ✅（公开） |
| `/api/users`、`/api/user-groups`、`/api/ssh-host-keys`、`/api/host-kvm`、`/api/vms-recycle`、`/api/crons`、`/api/audit`、`/api/settings` | ✅ | ❌ 403（含 GET） | ❌ 403 |
| `/api/vms` | ✅ | ✅ GET + **全部写** | ✅ GET（`terminal`/`serial` 除外）+ `POST vnc-token` |
| `/api/hosts`、`/api/storage`、`/api/networks`、`/api/images`、`/api/apps`、`/api/ansible`、`/api/cloud-init-templates`、`/api/stacks`、`/api/designer`、`/api/dashboard`、`/api/tasks`、`/api/sessions` | ✅ | ✅ GET；**写 403** | ✅ GET；写 403 |
| `/api/docker`、`/api/ai`、`/api/monitor`、`/api/logs` | ✅ | ✅ | ❌ 403（含 GET） |

补充事实：

1. **非 admin 的 VM 可见性靠授权**：未授权访问详情/操作返回 **404（不是 403）**；列表直接过滤掉。测试 operator/viewer 前先用 admin 建 `vm_grants` 或 `vm_group_grants`。
2. **`POST /api/docker/containers` 仅 admin**；其余 Docker 写 operator 可用。
3. **`GET /api/vms/:id/export` 仅 operator/admin，且必须关机**。
4. **两条导入路径权限不同**：`POST /api/vms/import`（存量域纳管）operator 可用；`POST /api/vms/import-file`（导出包恢复）仅 admin。
5. **viewer 的 VNC `view_only=true`**；operator/admin 为 false。
6. **没有 logout / token 吊销接口**：不要把「退出后旧 token 失效」写成预期。
7. **离线读写挂载仅 admin，且挂载期间禁止开机**。

---

## 6. 全按钮点击矩阵（L4）

### 6.1 点击策略

1. 遍历每个页面的可见、未禁用控件：`el-button`、`el-dropdown-item`、`el-switch`、`el-radio`、`el-tab`、可点击 `el-tag`、表格行内操作。
2. 分类：
   - **打开类** → 点击 → 等 500ms → 检查弹层/抽屉/消息 → 关闭。
   - **导航类** → 点击 → 校验 hash 与目标页标志文案 → 返回。
   - **危险类** → **不点**（只在「目标全部为 e2e 资源」的专项用例里点）。
   - **纯展示**（复制/展开/切换 tab）→ 点击后校验无异常。
3. 每次点击收集 `pageerror`；任一异常记 FAIL，附控件文案与页面。
4. **lazy tab 先点 tab 再找控件**（监控/拓扑/编排/部署栈/镜像市场/模板制作/容器镜像/IP 分配/通信流量/Docker 网络/用户组/计划任务）。

### 6.2 前置数据依赖

行内操作与批量按钮只在有数据时出现。L4 先探测列表是否有数据：无数据标 `SKIP(no-data)`，由 L5 造数据后补点。

### 6.3 页面 × 控件总表（人工复核用）

| 页面 | 标志文案 | 打开类 | 危险类 | 前置 |
|---|---|---|---|---|
| 登录 | 鸢航 VirtKite | 登录、回车提交 | — | 账号 |
| 仪表盘 | 仪表盘 | 刷新、3 tab、统计卡、监控大盘、VM 状态/性能、活动流 | 公告已读 | 概览数据 |
| 虚拟机 | 虚拟机管理 | 刷新、新建、导入、筛选、详情、控制台 | 批量开关机/删除、单机开关机 | VM |
| 建机向导 | 创建虚拟机 | 3 种方式、ISO/云镜像/克隆、磁盘/网卡增删、上一步/下一步 | 创建、存为模板 | 池/网络/安装源 |
| VM 详情 | VM 名 + 左侧菜单 | 返回、控制台、硬件应用、快照、XML、文件、授权 | 开关机/暂停/重启/固化/删除/回滚快照/移除磁盘/XML 保存/文件删除 | VM/快照/授权/凭据 |
| 控制台 | 选择连接方式 | VNC/SSH/串口、重连、全屏、字号 | 开机并连接、终端输入 | running / 凭据 |
| 容器 | 容器 | 刷新、2 tab、创建容器、详情、日志、终端 | 批量启停/删除、清理已停止、下线 | Docker |
| 应用商店 | 应用商店 | 安装、查看脚本、部署栈、去编排 | 一键部署/升级/重部署/回滚/保存编排 | running VM / 已部署栈 |
| 架构设计 | 架构设计 | 保存、导出 YAML/Ansible、整理、刷新、导入、漂移、落地 | 删除节点/计划、一键落地、按差异收敛 | 计划/节点 |
| 资产申请 | 资产申请 | 提交、批准、驳回、筛选 | 提交/批准/驳回 | running VM / pending |
| 回收站 | 回收站 | 恢复、刷新、行点击 | 彻底清除、批量清除 | 软删记录 |
| 运维自动化 | 运维自动化 | 新建 Playbook/Role/主机组、执行、历史 | 删除、取消、分发公钥 | Ansible / 凭据 |
| 计划任务 | 计划任务 | 新建、模板、历史、编辑、复制 | 立即运行、删除、启用切换 | 任务 |
| 宿主机 | 宿主机管理 | 添加、刷新、测试连通、查看状态、编辑 | 删除 | admin |
| 镜像 | 镜像管理 | 5 tab、上传、克隆建机、标记模板、制作机、市场下载 | 删除、清理悬空、构建 | 镜像/ISO |
| 存储 | 存储管理 | 3 tab、新建池/卷、拓扑、登记云镜像 | 删除池/卷、清理孤儿卷 | 池/卷 |
| 网络 | 网络管理 | 5 tab、新建网络、IPAM 建机、拓扑 | 启停/删除网络、Docker 网络 | 网络/可用 IP |
| 任务中心 | 任务中心 | 刷新、筛选、查看详情、关联入口 | 批量取消/删除、清理已完成 | 任务 |
| 审计 | 审计中心 | 2 tab、筛选、查询/重置/刷新/导出、行点击 | 断开会话 | 审计记录 |
| 日志 | 日志 | 查询、刷新、来源/条数、自动刷新 | — | VictoriaLogs（可降级） |
| 个人中心 | 个人中心 | 保存轮询偏好 | 修改密码（强制退出） | 旧密码 |
| 用户管理 | 用户管理 | 2 tab、新建、编辑、角色筛选、用户组/成员 | 删除用户/组、禁用 | admin |
| 系统设置 | 系统设置 | 9 tab、各保存、内嵌宿主机/cloud-init | 删除 SSH 指纹、开关嵌套虚拟化 | admin |

---

## 7. 业务链路清单（L5，核心）

### 7.1 认证与权限（AUTH）

| 编号 | 链路 | 预期 |
|---|---|---|
| VT-AUTH-001 | admin 登录成功 | 跳仪表盘；token 落盘；无 JS 异常 |
| VT-AUTH-002 | 错误口令 | 停留登录页；中文提示；无 token |
| VT-AUTH-003 | 连续失败 5 次 | 限流生效（429/明确提示） |
| VT-AUTH-004 | 未登录直链 `#/vms` | 重定向登录页 |
| VT-AUTH-005 | 篡改 token 后刷新 | 回登录页并清理状态 |
| VT-AUTH-006 | 空用户名/密码 | 提示「请输入用户名和密码」 |
| VT-AUTH-007 | 安全入口已配置时登录 | 缺 `X-Entrance`/`?entrance=` 返回 404 伪装；带上可登录 |
| VT-AUTH-008 | 登出 | 前端清 token 回登录页；**不要求**旧 token 服务端失效 |
| VT-AUTH-009 | 修改密码后 | 前端强制退出；旧 JWT 仍可用（已知实现事实，不判失败） |

### 7.2 虚拟机创建（VM-CREATE，本方案重点）

> 建机是本平台最复杂的链路。向导实际有 **3 种安装方式**；导入、导出包恢复、设计器落地是另外三个独立入口。**每一种都要单独走一遍。**

#### 7.2.1 向导三种方式（精确字段）

向导固定 4 步：安装方式 → 计算资源 → 磁盘与网络 → 确认创建。`installMode` 只有 `iso` / `cloudimage` / `clone` 三个值；**「网络安装源」属于 ISO 模式内的一个字段，不是独立模式**。

| 编号 | 方式 | 关键输入 | 预期 |
|---|---|---|---|
| VT-VM-101 | 本地 ISO（池内已有） | 存储池 `.iso` 卷、操作系统、vCPU/内存/系统盘、网络 | 任务成功；XML 有 `cdrom(raw,ide,read_only)`；`boot=[cdrom,hd]`；VM 可启动 |
| VT-VM-102 | 本地 ISO（手动路径） | 勾选「手动输入路径」填绝对路径 | 不做存在性校验；路径不存在时任务在 define/启动阶段失败且清理 |
| VT-VM-103 | ISO + HTTP 安装树 | `http://<host>/<tree>/` | 「测试」返回「安装树可达」；建机下载 `images/pxeboot/vmlinuz|initrd.img`；`boot=[hd]`；cmdline 含 `inst.repo=` 与 `console=ttyS0,115200n8` |
| VT-VM-104 | ISO + FTP 安装树 | `ftp://<host>/<tree>/` | 同上（需本机 FTP，否则 BLOCKED） |
| VT-VM-105 | 安装树 URL 非法 | 缺 scheme / 缺路径 / 端口关闭 | 前端正则拦截或「测试」返回「安装源不可用」；不产生半成品 |
| VT-VM-106 | 云镜像 + cloud-init（口令） | 云镜像、主机名、用户、密码、DHCP | 任务成功；seed ISO 生成；`boot=[cdrom,hd]`；开机等 IP；托管凭据；SSH 可登录 |
| VT-VM-107 | 云镜像 + cloud-init（SSH 公钥） | 公钥文本 | `ssh_authorized_keys` 生效；免密登录；平台 ansible 公钥追加 |
| VT-VM-108 | 云镜像 + 静态 IP | IP/网关/DNS | seed `dhcp4:false`、`addresses:[ip/24]`、`gateway4`；平台回填正确 |
| VT-VM-109 | 云镜像 + DHCP | 不填 IP | 从 NAT 池拿 IP；平台按 MAC 回填 |
| VT-VM-110 | cloud-init 模板套用 | 先存模板 → 再套用 | 字段回填正确；建机结果与模板一致 |
| VT-VM-111 | 克隆现有 VM | 源 VM + 新名称 | `clone_vm` 任务成功；新 UUID/MAC；只克隆系统盘；源机不受影响 |
| VT-VM-112 | 克隆 + 增量盘 | 观察子卷 | `<backingStore>` 存在；`qemu-img check` 无错误；卷名 `<newName>-diska` |
| VT-VM-113 | 克隆源机带数据盘/光驱 | 源机有额外盘或 cdrom | 额外盘**仍引用源机同一路径**；cdrom/seed 原样复制（验收需明确记录该行为） |

**关键校验文案**（断言用原文）：

- ISO：`请选择操作系统`、`请选择安装介质（存储池 → ISO / 手动输入路径 / 网络安装源 URL）`、`网络安装源格式不正确：需 ftp:// 或 http(s):// 开头并含安装树路径，如 ftp://192.168.219.1/rocky10/`
- 云镜像：`请选择云镜像`、`静态网络模式请填写 IP 地址`、`静态网络模式请填写网关`
- 克隆：`请选择源虚拟机`、`请填写虚拟机名称`、`虚拟机名称仅允许字母、数字、_、-`、`已存在同名虚拟机「X」，请更换名称`
- 计算资源：`请填写系统盘容量（5 ~ 500 GB）`（云镜像）；池空间不足仅红色预警不阻断
- 提交失败：优先后端 `message`，其次任务 `error`，最后 `创建失败`

#### 7.2.2 其他创建入口

| 编号 | 入口 | 预期 |
|---|---|---|
| VT-VM-121 | 导入存量 VM（VM 列表） | 扫描未纳管域 → 勾选 → 导入；DB 出现；状态一致 |
| VT-VM-122 | 导入导出包（admin） | tar.gz 解包 → 恢复卷 → define；同名/同 UUID 冲突被拒 |
| VT-VM-123 | 镜像页「基于此创建 VM」 | 克隆弹窗 → 任务成功；新机可启动 |
| VT-VM-124 | 镜像页「从镜像运行」容器 | 跳容器创建抽屉并预填镜像 |
| VT-VM-125 | 设计器「一键落地」 | 计划保存 → 落地 → 产生 VM/容器/网络；`apply-status` 可查 |
| VT-VM-126 | 网络 IPAM「用此 IP 建机」 | 跳向导并预填 IP |
| VT-VM-127 | 模板制作 → 固化 | 制作机 → 固化模板 → 镜像列表出现模板 |

#### 7.2.3 变量矩阵（每维至少一条）

| 维度 | 取值 | 校验点 |
|---|---|---|
| vCPU | 1 / 2 / 64 / 0 / 65 | 前端 1~64；后端 0→1，无显式上限（由 libvirt 判定） |
| 内存 | 256MB / 1GB / 128GB / 0 / 超物理 | 前端 256~131072；后端 0→1024 |
| 系统盘（ISO） | 1GB / 500GB / 0 / 501 | 前端 1~500；后端无上限校验，0 时走默认 20 |
| 系统盘（云镜像） | 5GB / 500GB / 4 / 501 | 前端下一步强校验 5~500；小于源卷容量时后端钳制为源卷容量 |
| 额外盘 | 1GB / 500GB / 0 | 弹窗默认 20；后端无上限 |
| 磁盘格式 | qcow2 / raw | 新建卷固定 qcow2；直接引用不探测实际格式 |
| 名称 | 合法 / 中文 / 空格 / 超长 / 重复 / 特殊字符 | `^[a-zA-Z0-9_-]+$`；DB 列 100；中文/空格被拒 |
| 磁盘卷名 | 合法 / 中文 / 留空 | 同名称规则；留空自动 `<vm>` / `<vm>-dN` |
| 存储池 | 默认池 / 指定池 / 不存在 / 非 active | 不存在→任务 `创建磁盘卷失败`；非 active 不在下拉中 |
| 网络 | NAT / 桥接 / 隔离 / macvtap / 不选 | 不选回落 `default`；macvtap mode 空→`bridge` |
| 自启 | — | **向导没有自启项**，建机后固定 false，需另用 `PUT /autostart` |
| cloud-init | 仅密码 / 仅密钥 / 两者 / 都不填 / 静态缺网关 | 用户/密码/公钥均 ≤ 对应长度；静态缺 IP/网关前端拦截 |
| 主机名 | 合法 / 中文 / 超长 / 控制字符 | 直连建机允许中文，≤100；模板入库更严（字母数字开头） |
| DNS | 单个 / 多个逗号分隔 / 非法 IPv4 | 每项必须合法 IPv4；空则 seed 默认 8.8.8.8 |
| 机器类型 | 空 / q35 / pc / 非法 | 白名单；clone 提交时忽略 |

#### 7.2.4 失败路径（必须验证「不产生半成品」）

| 编号 | 场景 | 预期 |
|---|---|---|
| VT-VM-151 | 安装源不可达 | 任务 failed；中文原因；无残留卷/Domain |
| VT-VM-152 | 存储池空间不足 | 任务 failed；明确提示；无半成品 |
| VT-VM-153 | 卷创建失败 | 回滚已创建资源 |
| VT-VM-154 | 重名创建 | 拒绝且不覆盖原 VM |
| VT-VM-155 | 并发双击创建 | 只创建一个（guardVMIdle/lockVM） |
| VT-VM-156 | 建机中刷新页面 | 任务继续；任务中心可查；重进状态正确 |
| VT-VM-157 | 建机中登出 | 后台任务不中断；无脏数据 |
| VT-VM-158 | 慢池并发 >2 个建机 | 任务排队；不出现入队超时（慢池 2 worker） |

#### 7.2.5 安装树本地模拟（推荐）

```bash
mkdir -p /tmp/e2e-tree/images/pxeboot
# 放入真实 vmlinuz/initrd.img（可从已下载 ISO 提取）
python3 -m http.server 18080 --directory /tmp/e2e-tree
```

正向：`http://127.0.0.1:18080/`；反向：不存在的路径、关闭端口。

#### 7.2.6 建机完成后的验证面

| 验证项 | 方法 | 通过标准 |
|---|---|---|
| 任务 | 任务中心 / `GET /api/tasks/:id` | `success`；耗时可读 |
| VM 状态 | 列表/详情 / `virsh domstate` | 与平台一致 |
| XML | 详情 XML 面板 / `virsh dumpxml` | 磁盘/网卡/内存/vCPU 与表单一致 |
| IP | 列表/详情 | DHCP 或静态 IP 正确回填 |
| SSH | 控制台 / 外部 ssh | 云镜像链路可登录 |
| 控制台 | VNC/串口 | 能打开、能输入、无 WS 异常 |
| 快照 | 建 → 列表 → 回滚 → 删 | 每步成功；回滚后状态正确 |
| 审计 | 审计中心 | 出现 create_vm，含操作者与对象 |
| 磁盘 | `qemu-img info` / `vol-dumpxml` | 增量克隆有 backing file；容量正确 |

#### 7.2.7 建机已知缺陷与风险（盘点发现，验收必须逐条确认）

以下不是猜测，是代码里可定位的行为；E2E 要**明确断言实际表现**，并在报告里标注「符合预期 / 待修」：

| 编号 | 风险 | 实际行为 | 建议断言 |
|---|---|---|---|
| R-01 | 勾了「自动初始化」但没填用户名/密码 | 前端静默省略 `auto_provision`，VM 建完仍关机，**无任何错误提示** | 建机成功但状态 `shut off`；标记 UX 缺陷 |
| R-02 | 克隆复制光驱/seed 与引导顺序 | 源机 cdrom/seed 原样出现在克隆机，`boot` 也复制 | 克隆机 `spec.disks` 含 cdrom；确认是否符合预期 |
| R-03 | 克隆只克隆系统盘 | 额外数据盘仍引用源机同一路径（共享写） | 克隆机额外盘 `source` 与源机相同；数据一致性风险 |
| R-04 | 克隆不要求源机关机 | 运行中克隆允许，存在快照一致性风险 | 运行中克隆能成功；记录风险 |
| R-05 | 后端不查重名 | DB 无唯一索引，靠 libvirt define 兜底 | 同名第二次提交：任务 failed，错误前缀 `定义虚拟机失败`，本次卷被清理 |
| R-06 | 云镜像不在池内 | 回退「直接引用镜像文件」，多机共享同一文件 | 日志有 warn；`spec.disks[].source` 指向镜像原路径 |
| R-07 | 镜像克隆忽略 `storage_pool` | 实际落在镜像所在池 | `vm.storage_pool` 等于镜像池，不是请求池 |
| R-08 | 静态 IP 掩码固定 /24 且不查重 | 不校验网段归属，不写 IPAM，可与既有地址冲突 | 填入网段外 IP 仍能创建；标记风险 |
| R-09 | 前端轮询 300s < 后端 15m | 长任务前端先报 `任务超时`，后台仍在跑 | 长任务场景：前端提示超时后，任务中心最终 success |
| R-10 | `pollTask` 不识别 `cancelled` | 任务被取消后前端一直轮询到超时 | 取消建机后前端最终报超时（而非「已取消」） |
| R-11 | 审计状态不代表任务结果 | `audit.status=success` 只表示 HTTP 提交成功 | 建机任务 failed 时审计仍 success；不能用审计判成败 |
| R-12 | 安装树只支持 RHEL 布局 | Debian 需 `install_kernel/initrd/cmdline` 覆盖，向导不暴露 | Debian 安装树用例应失败并给出明确文案 |
| R-13 | Windows 磁盘总线推荐不生效 | 前端预览 sata，后端新建盘固定 virtio | `spec.disks[].bus` 恒为 virtio |
| R-14 | 磁盘弹窗卷名残留 | 再次打开弹窗保留上次 `volName` | UI 用例：打开-关闭-再打开，卷名应为空（缺陷登记） |
| R-15 | 慢池仅 2 worker | 同时提交 3 个建机，第 3 个排队；入队超 3s 直接 failed | 并发 3 个：至多 2 个 running，第 3 个 pending/failed |

### 7.3 生命周期（VM-LIFE）

| 编号 | 链路 | 预期 |
|---|---|---|
| VT-VM-201 | 开机 | 任务/接口成功；running；IP 回填 |
| VT-VM-202 | 关机（优雅） | 成功；shut off |
| VT-VM-203 | 强制关机 | 超时后 destroy；状态正确 |
| VT-VM-204 | 重启 | running；uptime 变化 |
| VT-VM-205 | 暂停/恢复 | paused → running |
| VT-VM-206 | 改 vCPU/内存 | XML 与 `virsh dominfo` 一致 |
| VT-VM-207 | 挂载/卸载磁盘 | 详情变化；XML 正确；在用卷不可删 |
| VT-VM-208 | 添加/移除网卡 | MAC 唯一；网络正确 |
| VT-VM-209 | 自启开关 | `virsh dominfo` 一致 |
| VT-VM-210 | 删除（软删） | 列表消失；回收站出现；卷按守卫保留 |
| VT-VM-211 | 回收站恢复 | 恢复成功；状态/磁盘正确 |
| VT-VM-212 | 彻底清除 | 记录与卷清理；四重守卫生效 |
| VT-VM-213 | 运行中删除 | 先强制关机再删；无残留 Domain |
| VT-VM-214 | 克隆后删父机 | 父盘保留；子机仍可运行 |
| VT-VM-215 | 基于共享基镜像的 VM 删除 | 基镜像保留，仅删子卷 |
| VT-VM-216 | XML 保存（admin） | 重 define；运行中 409；非法 XML 拒绝 |
| VT-VM-217 | 导出（关机） | tar.gz 下载；运行中拒绝；viewer 403 |

### 7.4 存储 / 网络 / 镜像（STG/NET/IMG）

| 编号 | 链路 | 预期 |
|---|---|---|
| VT-STG-001 | 池列表/容量 | 与 `virsh pool-info` 一致 |
| VT-STG-002 | 新建目录池 | active；路径正确 |
| VT-STG-003 | 创建卷 | 卷出现；容量正确 |
| VT-STG-004 | 删除卷 | 在用 409；空闲删除成功 |
| VT-STG-005 | 卷拓扑图 | 节点/边正确；无 JS 异常 |
| VT-STG-006 | 清理孤儿卷 | 仅零引用删除；结果含 deleted/kept |
| VT-NET-001 | 网络列表 | 与 `virsh net-list` 一致 |
| VT-NET-002 | 创建 NAT 网络 | active；DHCP 范围正确 |
| VT-NET-003 | 停止/删除网络 | 断连；被 VM 使用时删除失败或明确提示 |
| VT-NET-004 | IPAM 分配 | 与租约一致；可跳向导建机 |
| VT-NET-005 | 拓扑/流量 | 渲染正确 |
| VT-IMG-001 | 镜像列表 | 容量/格式/来源正确 |
| VT-IMG-002 | 上传 ISO | 成功；记录出现 |
| VT-IMG-003 | 市场下载 | 任务成功；入池 |
| VT-IMG-004 | 标记/取消模板 | 状态切换；列表筛选生效 |
| VT-IMG-005 | 删除被引用镜像 | 拒绝并提示引用数 |
| VT-IMG-006 | 镜像市场嵌入页 | 标题不重复；刷新可用 |

### 7.5 容器 / 应用（DKR/APP）

| 编号 | 链路 | 预期 |
|---|---|---|
| VT-DKR-001 | 容器列表 | 与 `docker ps` 一致 |
| VT-DKR-002 | 创建容器（admin） | 成功；running |
| VT-DKR-003 | 启动/停止/重启/暂停 | 状态正确 |
| VT-DKR-004 | 日志/终端 | 有输出；WS 无异常；票据一次性 |
| VT-DKR-005 | 清理未引用卷 | 结果正确（验证「假失败」已修） |
| VT-DKR-006 | 批量启停/删除 | 仅选中项变化 |
| VT-APP-001 | 应用商店列表/详情 | 应用与参数 schema 正确 |
| VT-APP-002 | 安装应用 | 任务成功；输出可查 |
| VT-APP-003 | 使用托管凭据安装 | `use_saved` 生效 |
| VT-APP-004 | 栈一键部署/升级/重部署 | 任务成功；服务 running |
| VT-APP-005 | 栈回滚 | 版本回滚成功；编排文件变化 |
| VT-APP-006 | 保存/重置编排 | 校验通过才保存；重置回模板 |

### 7.6 自动化 / 定时任务（AUT）

| 编号 | 链路 | 预期 |
|---|---|---|
| VT-AUT-001 | Playbook 列表/详情 | 内容正确 |
| VT-AUT-002 | 新建/编辑 Playbook | 语法校验通过/失败提示 |
| VT-AUT-003 | 执行 Playbook（选 VM） | 任务成功；输出/RECAP 可查 |
| VT-AUT-004 | 执行（主机组） | 动态解析成员 |
| VT-AUT-005 | Role 增删 | 骨架创建；删除生效 |
| VT-AUT-006 | 分发公钥 | 幂等；未免密 VM 可登录 |
| VT-AUT-007 | 定时任务各 action 新建 | 参数按 action 变化；预览正确 |
| VT-AUT-008 | 立即运行 | **同步等待**结束；历史新增记录 |
| VT-AUT-009 | 启用/停用/复制/删除 | 状态与列表正确 |

### 7.7 任务 / 审计 / 日志（TSK/AUD）

| 编号 | 链路 | 预期 |
|---|---|---|
| VT-TSK-001 | 任务列表筛选/分页 | 结果正确 |
| VT-TSK-002 | 任务详情/日志 | 实时刷新；终态停止 |
| VT-TSK-003 | 取消运行中任务 | 仅 running 可取消；非 running 409 |
| VT-TSK-004 | 删除已完成任务 | 仅 success/failed 可删 |
| VT-AUD-001 | 审计筛选/详情 | 条件生效；详情完整 |
| VT-AUD-002 | CSV 导出 | 可打开；中断行 10 列 |
| VT-AUD-003 | 控制台会话断开 | 会话结束；审计记录 |
| VT-TSK-005 | 日志查询 | 有/无/语法错误均有明确反馈 |
| VT-TSK-006 | 日志自动刷新 | 开关生效；离开停止 |

### 7.8 用户 / 设置 / 申请（USR/SET/RBAC）

| 编号 | 链路 | 预期 |
|---|---|---|
| VT-USR-001 | 新建用户 | 口令策略生效；列表出现 |
| VT-USR-002 | 编辑用户 | 用户名不可改；编辑自己时角色/状态禁用 |
| VT-USR-003 | 禁用/启用 | 登录行为变化 |
| VT-USR-004 | 删除用户 | 不能删自己 |
| VT-USR-005 | 用户组 + 成员 | 全量覆盖；组授权可见性变化 |
| VT-USR-006 | 资产申请 → 审批 | 申请 → 批准 → 授权生效 |
| VT-SET-001 | 各设置分区保存 | 保存成功；刷新仍生效 |
| VT-SET-002 | 非法值保存 | 前后端双重校验 |
| VT-SET-003 | 设置项脏标记 | 修改后 tab 圆点；保存后消失 |
| VT-RBAC-001 | operator 写 `/api/ansible` 等 | **403**（不是 200） |
| VT-RBAC-002 | operator 写 `/api/vms` | 允许 |
| VT-RBAC-003 | viewer 访问 `/api/docker`、`/api/logs`、`/api/monitor` | 403 |
| VT-RBAC-004 | viewer 访问未授权 VM | 404（不是 403） |
| VT-RBAC-005 | viewer 打开 VNC | `view_only=true` |
| VT-RBAC-006 | operator 访问 `#/users`、`#/settings` | 前端重定向仪表盘 |
| VT-RBAC-007 | 未授权 operator 操作 VM | 404；授权后可操作 |

---

## 8. 测试数据准备与清理（接口，只用于摆场景/收尾）

> **被验收的行为仍走 UI 点击**；接口只负责造前置数据与可靠清理。

| 资源 | 准备 | 清理 |
|---|---|---|
| VM | `POST /api/vms`（202 → 轮询 task） | `DELETE /api/vms/:id`（202）→ 需要时 `DELETE /api/vms-recycle/:id/purge?purge_volumes=true` |
| 存储池 | `POST /api/storage/pools {name,path}` | `DELETE /api/storage/pools/:name`（不删目录） |
| 卷 | `POST /api/storage/pools/:name/volumes` | `DELETE .../volumes/:vol`（在用 409） |
| 网络 | `POST /api/networks {name,mode,gateway?}` | `DELETE /api/networks/:name` |
| 快照 | `POST /api/vms/:id/snapshots` | `DELETE /api/vms/:id/snapshots/:snap` |
| 用户 | `POST /api/users` | `DELETE /api/users/:id` |
| 授权 | `POST /api/vms/:id/grants` | `DELETE /api/vms/:id/grants/:gid` |
| 定时任务 | `POST /api/crons` | `DELETE /api/crons/:id` |
| 镜像登记 | `POST /api/images/register` | `DELETE /api/images/:id` |
| cloud-init 模板 | `POST /api/cloud-init-templates` | `DELETE /api/cloud-init-templates/:id` |
| 宿主机 | `POST /api/hosts` | `DELETE /api/hosts/:id`（有 VM 关联 400） |

**异步任务轮询**：`202/200 {task_id}` → 轮询 `GET /api/tasks/:id` 到 `success/failed/cancelled`。慢池仅 2 worker，同一时刻不要提交超过 2 个慢任务。

---

## 9. 自动化实现映射

| 文件 | 覆盖 | 标签 |
|---|---|---|
| `web/e2e/acceptance.spec.js` | 登录页、18 页遍历、全按钮点击、axe | `@smoke` |
| `web/e2e/vm-create.spec.js` | 向导三方式切换/校验、安装源探测、真实建机（门控） | `@vm-create` |
| `web/e2e/vm-lifecycle.spec.js` | 快照/开机/关机/回滚/自启/删除/彻底清除 | `@vm-life` |
| `web/e2e/infra.spec.js` | 存储卷 CRUD、虚拟网络 CRUD | `@infra` |
| `web/e2e/docker-apps.spec.js` | 容器创建/停止/删除 | `@docker` |
| `web/e2e/automation.spec.js` | Playbook 新建/语法校验/删除、计划任务新建/删除 | `@auto` |
| `web/e2e/rbac.spec.js` | 三角色权限负例、未授权 VM 404 | `@rbac` |
| `web/e2e/ws-ticket.spec.js` | 无凭证/伪造、跨资源、一次性、过期、任务日志票 | `@ws-ticket` |
| `web/e2e/task-center.spec.js` | 长任务取消、cancelled 删除、日志可读、参数校验 | `@task-center` |
| `web/e2e/recycle-bin.spec.js` | 软删 → 恢复（域已 undefine）→ 再删 → 彻底清除 | `@recycle` |
| `web/e2e/ansible-run.spec.js` | 真实建机 → adhoc ping → 任务成功/输出 → 清理 | `@ansible-run` |
| `web/e2e/designer.spec.js` | 计划保存、连线 kind 持久化、导出单个 stacks、删除 | `@designer` |
| `web/e2e/apps.spec.js` | 应用参数 schema、SSH 白名单、use_saved 缺凭据 | `@apps` |

公共设施（`web/e2e/helpers/`）：

| 文件 | 作用 |
|---|---|
| `env.js` | runId、`e2e-` 命名生成与守卫、账号、破坏性开关 |
| `auth.js` | 登录/清会话/取 token |
| `ui.js` | JS 异常收集、弹层关闭、按钮分类与安全点击 |
| `api.js` | 测试数据准备/清理、任务轮询、按名找 VM |

运行：

```bash
cd web
# 全量（推荐；含破坏性用例，会创建并清理 e2e- 资源）
ADMIN_PASS=... OPERATOR_USER=stu OPERATOR_PASS=... VIEWER_USER=user VIEWER_PASS=... \
  E2E_ALLOW_DESTRUCTIVE=1 npm run e2e

# 只跑某一层
ADMIN_PASS=... npm run e2e -- --grep @smoke
ADMIN_PASS=... E2E_ALLOW_DESTRUCTIVE=1 npm run e2e -- --grep @vm-create
ADMIN_PASS=... E2E_ALLOW_DESTRUCTIVE=1 npm run e2e -- --grep "@vm-life|@infra|@docker|@auto"
ADMIN_PASS=... OPERATOR_PASS=... VIEWER_PASS=... npm run e2e -- --grep @rbac
```

---

## 10. 执行步骤与报告

```bash
# 1) 静态与单测
go build ./... && go test ./... && golangci-lint run && govulncheck ./...
cd web && npm run lint && npm run test && npm run build

# 2) UI 冒烟
ADMIN_PASS=<口令> npm run e2e -- --grep @smoke

# 3) 业务链路
ADMIN_PASS=<口令> E2E_ALLOW_DESTRUCTIVE=1 npm run e2e -- --grep @vm-create
ADMIN_PASS=<口令> npm run e2e -- --grep @vm-life
ADMIN_PASS=<口令> npm run e2e -- --grep "@infra|@docker|@auto|@rbac"

# 4) 性能（可选；lighthouse 不进依赖，按需 npx 临时调用）
CHROME_PATH=/usr/bin/google-chrome npx --yes lighthouse "http://127.0.0.1:8080/#/login" \
  --chrome-flags="--headless=new --no-sandbox" --quiet \
  --only-categories=performance,accessibility,best-practices
```

可访问性日常以 axe-core 为准（随 `@smoke` 每次跑，门槛无 critical）；Lighthouse 只在需要性能基线时临时跑。

报告字段：`用例 | 结果(PASS/FAIL/SKIPPED/BLOCKED) | 耗时 | 证据 | 缺陷编号`。

**退出标准**：P0 100% PASS；P1 ≥95% PASS 且其余有缺陷编号；无新增 critical a11y、无 pageerror、无未登记 5xx；无残留 `e2e-` 资源。

---

## 11. 已知实现事实与高风险断言点（盘点发现，验收必须按实际写）

1. **operator 写权限只覆盖 `/api/vms`**：`/api/ansible`、`/api/designer`、`/api/cloud-init-templates`、`/api/stacks`、`/api/hosts`、`/api/storage`、`/api/networks`、`/api/images` 的写操作对 operator 是 **403**，尽管部分注释写「operator+」。
2. **viewer 的 GET 比预期宽**：dashboard/tasks/sessions/hosts/storage/networks/images 都可读；docker/ai/monitor/logs 全 403。
3. **非 admin 的 VM 可见性靠授权**：未授权返回 404（不是 403）。
4. **删除是软删**：列表消失但回收站有记录；彻底清理需 purge。
5. **删卷可能「删不掉」**：VM 删除、DetachDisk、删卷、删镜像都有共享/backing/在用守卫；断言看 `kept_volumes` / 409 文案。
6. **镜像删除守卫是 fail-open**：引用查询失败时仍会删（代码注释明确），不能当强一致保护。
7. **WS 票据 30 秒、一次性、绑定资源**：取票后立即连；二次连接必失败。
8. **VNC `view_only`**：viewer=true，operator/admin=false。
9. **导出是 GET 但权限更严**：仅 operator/admin，且必须关机。
10. **两条导入路径权限不同**：存量纳管 operator 可用；导出包恢复仅 admin。
11. **审计只记写操作**：GET 不产生审计。
12. **没有 logout / token 吊销**：不要把「退出后旧 token 失效」写成预期。
13. **计划任务 `run` 是同步等待**，不是 task_id。
14. **设计器 apply/reconcile 返回 `{id,mode,actions}`**，状态查 `/apply-status`，不是 task_id。
15. **栈升级/回滚是 HTTP 200 + task_id**，不是 202。
16. **容器创建仅 admin**；其余 Docker 写 operator 可用。
17. **离线读写挂载仅 admin**，挂载期间禁止开机。
18. **慢池只有 2 个 worker**：并发建机不要超过 2。
19. **前端 lazy tab**：先点 tab 再找控件。
20. **按钮文案随状态变化**（开机/关机、启动/停止、恢复/暂停、已部署/一键部署、安装/安装中…），用例要用状态断言而不是死文案。
21. **`auto_provision` 静默省略**：cloud-init 开了但用户名/密码为空时，前端不发送该字段，VM 建完关机且无提示（建机风险 R-01）。
22. **镜像克隆忽略请求的 `storage_pool`**：实际落在镜像所在池（R-07）。
23. **静态 IP 掩码固定 /24、不查 IPAM 冲突**（R-08）。
24. **审计 `status` 只反映 HTTP 提交**，后台任务 failed 时审计仍 success（R-11）。
25. **`pollTask` 不识别 `cancelled`**：取消后前端轮询到超时才停（R-10）。

### 11.1 首轮验收发现并修复的缺陷（2026-10-17）

| 缺陷 | 现象 | 修复 |
|---|---|---|
| `use_saved` 空指针 | 任何 `POST /vms/:id/files/list {use_saved:true}` 都 panic，gin Recovery 吞成 **500 空响应**；文件管理「用已保存凭据」整条链路不可用 | `resolve` 的命名返回值 `vm` 尚未赋值，改用 `record.ID`；补 nil 守卫；新增 `handler/vm_files_resolve_test.go` 两例回归 |
| 图标按钮无无障碍名称 | axe **critical** `button-name`：全屏按钮、活动流刷新 | 补 `aria-label` |
| 下拉/勾选框无标签 | axe **critical** `label`：全局搜索、状态筛选、VM 卡片勾选框 | 补 `aria-label`（Element Plus 的 `aria-label` prop） |
| 铃铛 ARIA 属性非法 | axe serious `aria-prohibited-attr`：告警铃/任务铃的 `aria-label` 挂在无角色的 `div` 上 | 补 `role="button" tabindex="0"` 与 Enter/Space 触发 |
| ECharts 隐藏 tab 渲染异常 | 切到其他 tab 后力导向图仍在零尺寸容器里 setOption，zrender 抛 `Cannot read properties of null` | `FlowView` 在容器隐藏时跳过 render；`dispose` 包 try/catch |

修复后全量 E2E（2026-10-17，`ADMIN_PASS=... OPERATOR_PASS=... VIEWER_PASS=... E2E_ALLOW_DESTRUCTIVE=1 npm run e2e`）：

```
21 passed (3.6m)
```

覆盖：登录页 + 18 页遍历 + **35 个 tab 逐个点开** + **55 个打开类按钮逐个点击**（23 个出现弹层/消息）+ axe；真实建机（云镜像 + cloud-init → running → 拿 IP → 托管凭据 SSH 列目录 → 删除+彻底清除）；生命周期（快照/开机/关机/回滚/自启/删除）；存储卷与虚拟网络 CRUD；容器创建/停止/删除；Playbook 与计划任务 CRUD；三角色 RBAC 负例。全程无残留 `e2e-` 资源。

已知未覆盖（下一批）：WS 票据一次性负例、任务取消/重试、回收站「域已 undefine」分支、设计器 reconcile 删除分支、应用商店真实安装（API 层由 `scripts/e2e-provision.sh` 覆盖）。

---

### 11.2 第二批深挖发现并修复的缺陷（2026-10-17，任务/WS/Ansible/回收站/设计器/应用栈）

| 缺陷 | 现象 | 修复 |
|---|---|---|
| **cloud-init 明文口令落库** | `tasks.payload.cloud_init.password` 是明文（实测 task 236，31 位）。任务详情虽隐藏 payload，但 DB 可直接读到，违反「口令不得落库」 | 新增 `handler/cloud_init_seal.go`：建机 / 镜像克隆 / 设计器三条路径在提交前把 password 换成 `password_enc`+`salt`；executor 用主密钥解密（`parseTaskCloudInit`），解密失败 fail-closed |
| cancelled 任务不可删 | UI 把 cancelled 当终态可删，后端只认 success/failed → 取消过的任务永远清不掉 | 后端允许删除 cancelled；E2E 断言删除后 404 |
| 「进行中」分页错乱 | 前端并发查 running/pending 再拼接：一页最多 2×pageSize、排序非全局、total 相加 | 后端 `status=active` 统一 `IN (pending,running)` 分页；前端单请求 |
| cancelled 显示成「执行中…」/「执行失败」 | 结果列与时间线误标 | 结果列「已取消」；时间线 info 色「已取消」 |
| pending 任务无实时日志 | 首屏 `structured=false` 直接 return，不连 WS | 活动任务一律连 WS；首批 entries 到达时切结构化面板 |
| Ansible 失败丢 RECAP | executor 在 `eng.Run` 出错时直接 return，Result 为空，页面只剩「部分主机执行失败」 | 失败也写 `Result`（output/recap/recap_raw） |
| `setup` 模块被要求填 args | 无参模块被 400 拦截 | ping/setup 放行空 args |
| 组执行脱敏漏成员 | 脱敏集合只展开 targets，group_id 成员口令可能进日志 | 展开 `host_groups.vm_ids` 纳入脱敏 |
| 目录形态 playbook 被判不存在 | 定时任务/设计器只 `Stat(<id>.yml)` | 改走 `ansible.ResolvePlaybook`（支持 `<id>/site.yml`） |
| 单文件可遮蔽同名目录 | 创建只查 `<id>.yml` | 同名目录也 409 |
| 文件树可写 >1MB / 列隐藏文件 | 写入/上传无上限，`.git` 等进清单 | 读写统一 1MB；隐藏文件/目录跳过 |
| 栈忙语义 | 升级无预检（受理后任务才失败）；部署忙返回 500 | 升级预检 409；部署 `ErrStackBusy` → 409 |
| 彻底清除留孤儿 | 不 undefine 域；只删 `<name>.qcow2`，漏 `-diska/-sys` | 先 undefine（容忍不存在）；三种命名逐个过四重守卫 |
| 恢复误认领同名域 | 只按名字判断域存在，不校验 UUID | 比对 UUID，不一致按「域不存在」走重建 |
| 设计器连线类型丢失 | 后端 `dsgLink` 无 kind，保存后全变 net | `dsgLink` 增加 `kind` 并持久化 |
| 导出 YAML 非法 | 多个容器节点产生重复顶层 `stacks:`；未校验计划 ID | 聚合为单个 stacks 列表；ID 白名单 |
| apply-status 串计划 | `payload LIKE '%"plan_id":"x"%'`，`e2e-1` 命中 `e2e-10` | 取最近 50 条精确解析 plan_id |
| 栈部署前端假失败 | 全局 axios 15s，同步部署最长 10min | 部署请求单独 11min 超时 |
|「重新部署」文案行为不符 | 文案说「按当前编排文件」，实际用仓库模板覆盖 | 文案明确「会覆盖当前编排文件，修改先存版本历史」 |
| 恢复缺盘用 success | exact_missing 危险信息被绿色提示带过 | 含「磁盘文件已不存在」时用 warning |
| Automation 输出卡 | 运行中关闭无效；执行后强制切 tab；凭据提示指向不存在的入口 | 关闭后不再弹回；不再切 tab；提示改为「建机自动托管/管理员预置」 |
| 取消按钮权限不一致 | operator 可见取消/批量取消，点击 403 | 仅 admin 显示 |
| 取消竞态 | Cancel 读-改无 CAS，可能把刚完成的任务覆盖成 cancelled | `WHERE status='running'` 条件更新，RowsAffected=0 视为未取消 |
| 终态日志缺尾 | TaskLogger.Close 先关订阅再 flush，WS 收到 done 时最后一批未落库 | 先 flush 再关通道 |
| survey 解析失败泄明文 | 解析出错当作无 survey，secret 答案以明文进 extra_vars | 解析失败 400 fail-closed |
| 凭据含换行可注入 inventory | 口令只做单引号转义 | 保存时拒绝换行/控制字符 |

**仍未修复（已登记，下一批处理）**：

- 设计器 Apply 是全量新增，预览却来自 reconcile 差异；重复 Apply 会同名建机失败并把软删记录推进回收站（R5/R6）；首次失败后无基线，收敛退化为全量新增（R7）。
- 恢复不恢复资产授权/托管凭据/主机组成员（R8）——删除时已物理清理，恢复只回记录。
- 设计器等建机 12min < 任务 15min（R13）；运行位仅内存，重启/多实例失效（R14）。
- 任务列表/详情/日志对 viewer 全量可见（D-08）；任务日志 WS 取票仅 admin，operator 看不到「实时」徽标（D-49）。
- 应用安装 UI 强制填密码、走不了 use_saved（R20）；不校验 VM 运行态（R21）；前端轮询 10min < 任务 15min（R30）。
- 栈 Detail 与 List 的 `deployed` 口径不一致（R16）；保存编排不持锁、不记版本（R19）。
- 任务日志 WS 终态最后一条可能未落库就发 done（D-16）。
- **Docker 域深挖未完成**（盘点代理因服务端 503 中断）：容器创建/健康检查/构建上下文/compose 服务级操作/日志 WS 重连等仍只有基础 CRUD 覆盖。

---

## 12. 已知限制与待补

### 11.3 第三批：冗余清理与守卫落地（2026-10-18）

**守卫落地（安全承诺兑现）**：文档 §3.1 一直写「删除前统一过 `isE2EName()`」，但实测 24 个
`DELETE` 调用点**零调用**——`isE2EName` 从未被任何用例引用，承诺落空。已改为在
`helpers/api.js` 的 `api()` 里统一拦截（所有 DELETE 必经此处，无法绕过），非 `e2e-` 前缀
直接抛错不发请求；数字型主键必须显式传 `guard`，并新增 `e2eGuardOf()` 从任务标题
（形如「创建虚拟机 e2e-xxx」）抽出名称。

守卫上线后立即拦到一个真实误删风险：`task-center.spec.js` 原取「最新一条 success 任务」
直接删——那可能是用户的真实建机/备份记录。已改为只删本轮 `RUN_ID` 产生的任务。

**冗余清理**：

| 项 | 处置 |
|---|---|
| 13 个未使用导出（knip） | 内部使用的改为非导出；完全未用的删除（clearSession/getToken/logout/listPools/listNetworks/listImages/deleteVM 等薄封装） |
| `lighthouse` 依赖 | 移除（体积大、仅基线时用一次、knip 判未使用）；改为按需 `npx --yes lighthouse` 临时调用，不进 devDependencies |
| `VMCredentialHandler.UpsertForVM` | 删（零调用；tasks 包有自己的 upsertVMCredential，禁反向 import） |
| `ansible.ValidDirName` | 删（零调用；handler 直接用 `dirNameRe`） |
| `ansible.ForceReloadKeyPair` | 删（注释自认无调用方，测试也未调） |
| `ansible.SyntaxCheckDir` | 删（零调用；实际用 `SyntaxCheckContentInDir`） |

**保留不删**（有测试覆盖，非真死代码）：`handler.itoa`（terminal_test 断言其行为）、
`vmGrantActive`（单测）、`vmssh.Options.Host/Port/User`（字段刻意不可导出的安全设计，
getter 供本包与测试读取）。

清理后：knip 零输出、ESLint 零告警、deadcode 从 8 处降到 5 处（均为测试专用）。

---

## 12. 已知限制与待补
1. **需要管理员口令**才能跑 L5；无口令只跑登录页与 L1–L3。
2. FTP 安装树依赖本机 FTP 服务；未部署标 BLOCKED（HTTP 用例覆盖解析与下载）。
3. 云镜像 SSH 验证依赖镜像内置 cloud-init 与 sshd；不满足时改为验证 cloud-init 日志与 IP 回填。
4. 容器/应用依赖 Docker；Ansible 依赖引擎；缺失整域 BLOCKED。
5. 监控曲线依赖 Prometheus；未配置只验证「未配置」降级文案。
6. 破坏性用例默认关闭；开启前必须确认目标全部为 `e2e-` 前缀。
7. **当前尚未覆盖**（下一批补）：自动重试（image_build/image_download/stop_vm）、设计器 reconcile 删除分支与重复 Apply、栈回滚版本冲突、应用商店真实安装、Docker 全链路。WS 票据一次性/跨资源/过期、任务取消、回收站恢复已由 `ws-ticket.spec.js`、`task-center.spec.js`、`recycle-bin.spec.js` 覆盖。
