# P4 · 自动化运维（Ansible Playbook 引擎）

> 2026-10-04 规划。定位回应：「我们这个平台就是缝合怪，很多运维的东西都能看到，尽量发挥它们最大的优点」——
> Ansible 是缝合怪的**批量执行引擎**层，向上承接调度（定时任务）与编排（架构设计器），向下复用平台的资产库（VM = Inventory）与凭据体系。

## 0. 缝合怪地图（本模块在其中的位置）

| 层 | 工具 | 平台模块 | 状态 |
|---|---|---|---|
| 虚拟化 | KVM/libvirt | VM 全生命周期 | ✅ |
| 容器 | Docker Compose | 栈商店（P2A） | ✅ |
| 单机脚本 | SSH + shell | 应用商店（apps） | ✅ |
| **批量编排** | **Ansible** | **本模块（P4）** | 🚧 |
| 调度 | 定时器 | 计划任务（vm_snapshot / db_backup） | ✅（S3 与 Ansible 缝合） |
| 监控 | Prometheus | 告警中心 | ✅ |

P2A 规划中「明确不做：ansible 执行器」在此正式接续。

## 1. 可行性现状（2026-10-04 实测宿主机）

- ansible core **2.21.2** 已装（`~/.local/bin/ansible-playbook`，pip 用户级）→ 后端进程同用户运行，可直接探测调用；探测顺序：配置项 → `$HOME/.local/bin` → `/usr/bin` → `/usr/local/bin`
- **sshpass 已装**（`/usr/bin/sshpass`）→ 口令认证 adhoc/playbook 开箱即用（S3 换 SSH key 免密后仍保留作存量过渡）
- Python 3.14.4；目标 VM 全在宿主机网桥网段（apps 模块已验证 SSH 可达），**inventory 从 vms 表生成 = 目标白名单结构性成立**（不收用户手输 IP）

## 2. 分阶段规划（按序开工，每阶段交付即验收）

### S1 引擎接入（约 1 天）——「ansible 能从平台跑起来」

**后端**
- `service/ansible` 新包：
  - `Detect()` 探测 ansible-playbook 绝对路径 + 版本（缓存）
  - `BuildInventory(vms []model.VM, groups map[string][]uint) (string, error)`：内存拼 inventory ini（`[running]`/`[all]`/按 OS 分组），写 `data/ansible/runs/<runID>/inventory`，目录 **0700**、执行完即删
  - `RunPlaybook(ctx, opts)` / `RunAdhoc(ctx, opts)`：`exec.CommandContext` 跑 ansible-playbook / ansible adhoc，`ANSIBLE_HOST_KEY_CHECKING=False`（TOFU 已在 vmssh 层做过，ansible 侧对受管 VM 关闭严格校验），口令经 `sshpass -e`（`SSHPASS` 环境变量注入，**命令行参数与 inventory 均不落明文**）
- 任务类型 `ansible_run`（tasks.Manager 新 executor，复用 app_install 的异步模式）：输出节流落任务 `Result` + 完整日志落 `data/ansible/runs/<runID>/output.log`
- API：
  - `GET /api/ansible/status` → {installed, path, version}
  - `POST /api/ansible/run`（operator+）→ {targets:[vm_ids], playbook?|module+args?}，Submit ansible_run 返回 task_id
- 安全（对齐 AGENTS 第 15 条精神）：目标集合只接受 vm_id（服务端查库取 IP，前端不传 IP）；审计记录「谁、对哪几台、跑了什么」；口令/密文不进任何日志

**前端**
- 新页 `views/Automation.vue`（运维自动化）：顶部引擎状态卡（未装则显示引导文案与探测路径）；「快速执行」：VM 多选（复用现有表格多选模式，仅列 running）+ adhoc 表单（模块下拉：ping/command/shell + 参数）→ 提交 → 任务输出实时滚动（轮询 task，渲染日志）
- 侧栏「运维」组加「自动化」入口；**同批把「计划任务」入口从系统设置前置到运维组**（上轮遗留建议 #1，零成本顺带做）

**验收**：真机 E2E——选 2 台 running VM，adhoc ping 全绿；adhoc shell `hostname` 返回两台主机名；输出在页面实时滚动。

### S2 Playbook 库（约 1.5 天）——「平台里能管 playbook」

**种子 playbook**（`data/ansible/seed/`，首次启动落盘；git 同步跟踪一份在 `ansible/playbooks/`；dnf/apt 双系写法，参考 apps nginx 脚本套路；内容学以致用取自用户运维笔记主题）：
1. `init-node` 装机初始化：hostname/时区/chrony/firewalld 放行 SSH/基础包
2. `harden-ssh` SSH 加固：MaxAuthTries、fail2ban（**保留平台 root 通道**，注释里写明取舍，防自我锁死）
3. `install-docker` Docker 安装（dnf/apt 双系 + daemon.json 镜像加速）
4. `sysctl-tuning` 内核优化：somaxconn/file-max/swappiness/BBR
5. `node-exporter` 监控 agent：裸装二进制 + systemd 单元（联动告警中心）
6. `deploy-keys` 运维用户与 authorized_key 分发
7. `install-nginx`（与 apps 互为印证：脚本版 vs playbook 版，答辩可对比讲）

**后端**
- playbook CRUD：`GET/POST/PUT/DELETE /api/ansible/playbooks`（存 `data/ansible/playbooks/*.yml`，头部机器可读行 `# vmops-playbook: name=x | desc=x | targets=linux`，同 stacks 惯例）；保存时 `ansible-playbook --syntax-check` 校验，报错回传行号
- `POST /api/ansible/run` 扩展 playbook 分支；执行结果解析：stdout 末尾 `PLAY RECAP` 解析出每台主机 ok/changed/failed 计数，结构化进 `Result`（`{"hosts":{...}}`）
- 审计挂钩（写操作类 playbook 记审计）

**前端**
- Automation 页加「Playbook 库」tab：卡片=名称/描述/最近执行状态；编辑抽屉 YAML textarea（等宽字体、dark 兼容）+「语法校验」按钮；执行对话框：VM 复选（含「全部 running」快捷）→ 提交 → 轮询
- 「执行历史」tab：任务中心筛 ansible_run 类型，点开看输出 + RECAP 矩阵（每台 ok/changed/failed 徽标）

**验收**：对 Rocky+Ubuntu 两台真机跑 `init-node`，双系均成功；故意写坏 YAML，校验返回中文错误与行号；RECAP 矩阵正确显示。

### S3 编排联动（约 0.5-1 天）——「缝合怪成体」

- **设计器 × Ansible**：VM 节点属性新增「落地后执行 playbook」多选 → `provisionVM` 在 app_install 之后逐个跑（app 装的是服务，playbook 做的是初始化/加固/优化，各司其职）
- **计划任务 × Ansible**：cron 新增动作类型 `ansible_playbook`（参数：playbook + 目标 VM 集）——调度器与执行引擎缝合，cron 不被替代而是被增强；原 vm_snapshot/db_backup 保持不动
- **SSH key 免密（渐进）**：平台托管一对密钥（`data/ansible/id_ed25519`，权限 0600），create_vm 的 cloud-init 注入公钥（`ansible_key` 字段透传）→ 新 VM 天然免密；存量 VM 保留 sshpass 通道；`vms` 表加 `ansible_ready` 标记
- 验收：设计器拖一台 Rocky → 落地后自动跑 init-node + sysctl-tuning → RECAP 全绿；建一个「每周日 3:00 跑 sysctl-tuning」计划任务并手动触发成功

### S4 进阶（可选，答辩前有余力再做）

- playbook `vars` 表单化：解析 `vars:` 段自动渲染表单，执行前填参
- 执行报告页：多次执行的趋势（changed 率）
- roles 目录支持

## 3. 明确不做（本阶段）

- 多宿主机/跨网段执行（多宿主机本身是论文展望，inventory 仅本宿主机 VM）
- Windows 受管节点、ansible-runner、Ansible Vault UI、roles 市场
- playbook 在线调试终端（有 adhoc + 语法校验足够）

## 4. 风险与取舍

| 风险 | 处置 |
|---|---|
| harden-ssh 把平台自己锁死 | 种子 playbook 刻意不关 root 密码登录（注释写明「平台通道依赖」）；文档标注危险项 |
| ansible 输出量大撑爆任务 Result | 节流落库（每 2s 截尾）+ 完整日志落盘文件，前端按需拉 |
| 并发执行互相踩 | runs 目录按 runID 隔离；同一时刻平台最多 1 个 ansible_run（任务队列天然串行，够用） |
| sshpass 明文暴露 | 仅经 SSHPASS 环境变量注入；inventory 不写口令；S3 后逐步被 key 替代 |
