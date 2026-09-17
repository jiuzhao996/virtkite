# 鸢航 VirtKite 大版本升级规划（v2：从虚拟机管理平台到轻量服务器管理平台）

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
