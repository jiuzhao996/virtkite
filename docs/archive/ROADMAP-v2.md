# 鸢航 VirtKite 大版本升级 v2（已实现，待验收）

> **状态：五个批次全部落地，本地已提交（3ef45e8 之后的追加提交），未推送远端。**
> 验收方式：`docker start vmops-mysql vmops-prometheus vmops-alertmanager vmops-grafana && ./start.sh`，新入口在侧栏「资源 → Docker 管理」「应用 → 应用商店」「运维 → 计划任务」，VmDetail 左栏新增「文件管理」。
> 回退方式：`git reset --hard 47276ad`（v2 前的最后状态）后 `go build -o vmops . && cd web && npm run build` 再重启即可。

## 落地清单（与批次一一对应）

| 批次 | 交付物 | 验证 |
|---|---|---|
| 1 Docker 管理 | `service/dockerx`（CLI 封装）+ `handler/docker.go` + `DockerList.vue`；/api/docker NonViewerMiddleware | 容器/镜像 API 实测 4 容器 10 镜像；viewer 403/operator 200 |
| 2 文件管理 | `service/vmssh`（SSH 执行）+ `handler/vm_files.go`（在线通道）+ `handler/vm_files_offline.go`（guestmount 离线只读，含 LVM 根分区自动识别与 FUSE 权限 sudo 读取）+ `VmFileBrowser.vue`（双模式切换） | 离线挂载 Docker VM：列 /root 27 文件、下载 /etc/hostname 内容正确、路径穿越 400、运行中拒挂、卸载 ✓；SSH 通道错误密码中文提示、stu 未授权 404 |
| 3 应用商店 | `service/apps`（10 款幂等脚本）+ `app_install` executor + `AppStore.vue` | 目录 API 实测 10 应用；安装走异步任务（SSH 执行、进度/输出入任务结果） |
| 4 计划任务 | `model.ScheduledTask` + `service/cron`（手写 cron 解析/调度器，44 子用例）+ `CronList.vue`；动作 vm_snapshot/db_backup | CRUD/toggle/run API 实测；cron 解析测试全绿 |
| 5 信息架构 | 侧栏新增「应用」组；资源组 +Docker；运维组 +计划任务(adminOnly)；审计对象类型补 docker/app/cron | 前端构建通过 |

> 定位升级：管理 KVM 虚拟机的平台 → **管理"虚拟机 + 容器 + 应用"的一体化服务器管理平台**（对标宝塔面板的使用心智：浏览器点一点，搞定服务器）。
> 本批次为实验性大版本：**完成后仅本地提交，不推送远端**，用户验收后决定合入或回退。

## 北极星

把原本需要敲命令行的服务器操作，变成图形化点击。核心心智：
- 虚拟机是「机器」，容器是「服务」，应用是「能力」——三层都可视化
- 每个功能都能在 5 秒内找到、3 次点击内完成

## 功能批次（按价值密度排序）

### 批次 1：Docker 容器管理（与 KVM 并列的第二支柱）
- 后端 `service/dockerx`：封装 docker CLI（`--format '{{json .}}'` 结构化输出），零新依赖
- 容器：列表（状态/镜像/端口/名称）、启动/停止/重启/删除、日志查看（尾部 N 行）
- 镜像：列表、删除
- 路由 `/api/docker/*` 挂 OperatorMiddleware；审计中间件覆盖
- 前端 `DockerList.vue`：容器表 + 镜像表 + 日志抽屉；侧栏新增「容器」分组

### 批次 2：虚拟机文件管理（guestfish 离线 + SSH 在线双通道）
- 首选 guestfish（libguestfs）：对**关机** VM 的磁盘直接浏览/下载/上传/删除，不依赖 VM 内 SSH
- 在线通道：SSH exec（复用 x/crypto/ssh，terminal.go 同款拨号）做 `ls/cat/rm/mv/mkdir` 封装
- 后端自动探测 guestfish 可用性；未安装返回引导文案
- 入口：VmDetail 左栏新增「文件管理」；路径白名单防穿越；命令一律 exec.Command 数组参数

### 批次 3：应用商店（宝塔精髓：一键部署 LNMP/WordPress…）
- `service/vmssh`：SSH 在 VM 内执行命令（密码认证、validateSSHTarget 复用、输出捕获）
- 应用目录：内置 Go map（名称/图标/描述/分类/安装脚本/检测命令），首期 8-10 款：
  Nginx、MySQL、Redis、PHP、WordPress（Docker 版）、Node.js、Python3、Docker 引擎自身、宝塔…（脚本走国内源、幂等、检测已装）
- 安装走**异步任务系统**（新增 app_install executor：SSH 执行脚本，进度按脚本段落上报，输出写 Task.Result）
- 前端 `AppStore.vue`：分类卡片 + 安装弹窗（填 SSH 凭据）+ 进度展示 + 已装检测

### 批次 4：计划任务（定时快照/定时清理）
- 新表 `scheduled_tasks`（name/cron/action/params/enabled）+ 内存调度器（每分钟 tick，手写 5 字段 cron 简化解析）
- 动作首期：`vm_snapshot`（对指定 VM 打快照，复用 virt.CreateSnapshot）、`db_backup`（mysqldump 到 SEED_DIR 同级 backup 目录）
- 前端 `CronList.vue`：列表 + 启停 + 手动触发 + 下次执行时间预览

### 批次 5：信息架构整合
- 侧栏四组重构：虚拟化（VM/镜像/存储/网络）｜容器（Docker）｜应用（应用商店/计划任务）｜运维（任务/审计/监控）｜管理
- 仪表盘统计卡新增「容器数」；应用商店入口卡

## 技术红线（不因求快破坏）
- 所有新命令执行一律 exec.Command 数组参数（禁 shell 拼接）；SSH 命令经 validateSSHTarget
- 新路由全部过鉴权中间件 + 审计；Docker/应用/计划任务写操作仅 admin/operator
- 每批次 `go build + go test -race` 全绿再进下一批；完成后本地 commit（**不 push**）

## 明确不做（本批次）
- 宿主机自身的文件管理/网站反代管理（宝塔最重的部分，与本平台定位冲突）
- FTP 服务管理、SSL 证书签发（ Let's Encrypt 依赖公网，教学场景弱）
- Ansible 引入（SSH 直连已覆盖同能力，引 Ansible 是为背书而背书；写入展望）
