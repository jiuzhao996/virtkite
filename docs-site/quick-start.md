# 快速开始（10 分钟跑起来）

本章目标：从一台全新的 Ubuntu 服务器，到浏览器里登录平台、看到第一台虚拟机。全程复制粘贴即可。

## 你需要准备什么

| 项目 | 最低要求 | 说明 |
|---|---|---|
| 操作系统 | Ubuntu 22.04 / 24.04 | 其他 Linux 也可，命令以 Ubuntu 为准 |
| 内存 | 8 GB |宿主机自身 + 1-2 台小虚拟机 |
| 磁盘 | 40 GB 空闲 | 虚拟机磁盘放这里 |
| CPU 虚拟化 | 已开启 | BIOS 里开 VT-x / AMD-V（台式机常见坑，见 FAQ） |
| 软件 | Docker + Go ≥ 1.25 | 下面第 1 步帮你检查 |

## 第 1 步：检查环境（1 分钟）

把下面整段复制到终端执行：

```bash
docker info > /dev/null 2>&1 && echo "✔ Docker 正常" || echo "✘ 请先装 Docker：curl -fsSL https://get.docker.com | bash"
go version || echo "✘ 请先装 Go：https://go.dev/dl/"
systemctl is-active libvirtd && echo "✔ libvirtd 正常" || echo "⚠ libvirtd 未运行，虚拟化功能受限"
egrep -c '(vmx|svm)' /proc/cpuinfo || echo "⚠ CPU 虚拟化未开启，请进 BIOS 打开 VT-x/AMD-V"
```

四行都打勾？继续。哪行报错就按提示先解决。

## 第 2 步：获取代码并一键安装（3 分钟）

```bash
git clone https://github.com/jiuzhao996/virtkite.git
cd virtkite
bash install.sh
```

`install.sh` 会自动完成：
1. 生成 `.env`（所有口令**强随机**、权限 600，绝不使用默认弱口令）
2. 用 Docker Compose 拉起 MySQL / Prometheus / Grafana / Alertmanager 四个容器
3. 编译 Go 后端并启动（同时开启 SSH 跳板 `:2222`）
4. 打印**访问地址 + 管理员初始口令**（随机生成，只在屏幕上出现这一次）

::: warning 保存初始口令
安装结束时屏幕上的管理员初始口令请立即保存。首次登录后第一件事就是改密码（右上角头像 → 个人中心）。
:::

## 第 3 步：首次登录（1 分钟）

1. 浏览器打开 `http://服务器IP:8080`
2. 输入 `admin` + 刚才保存的初始口令
3. 右上角头像 → **个人中心** → 修改密码

## 第 4 步：创建你的第一台虚拟机（5 分钟）

1. 左侧菜单 **虚拟机 → 新建**
2. 安装方式选 **云镜像**（推荐：不用挂 ISO，自动注入配置）——选一个镜像源里的 Rocky/Ubuntu
3. 计算资源：1 vCPU / 1 GB 内存（测试够用）
4. 磁盘与网络：默认即可；有 cloud-init 需求可展开面板设置主机名/密码
5. 点 **确认创建** → 任务中心会出现一条异步任务 → 状态变「运行中」即成功

## 下一步去哪

- 不认识「存储池」「网桥」这些词？→ [五分钟理解核心概念](/concepts)
- 想知道每个页面怎么用？→ [使用手册](/manual)
- 装好了想玩 SSH 跳板和授权审批？→ [4A 教学闭环](/4a)
- 出错了？→ [运维手册 · FAQ](/ops)
