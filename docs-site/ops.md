# 运维手册（备份 / 升级 / 排障）

## 日常启停

```bash
# 启动（容器 + 后端）
docker start vmops-mysql vmops-prometheus vmops-grafana vmops-alertmanager
cd /path/to/virtkite && ./start.sh

# 停止
pkill -f '^\./vmops$'          # 后端
docker stop vmops-mysql vmops-prometheus vmops-grafana vmops-alertmanager
```

## 备份与恢复

**必须备份的三样**：MySQL 数据库、`.env`（口令与密钥，丢了 = 所有托管凭据报废）、虚拟机磁盘（存储池目录）。

### 数据库备份（可挂计划任务自动执行）

```bash
docker exec vmops-mysql mysqldump -uvmops -p"$(grep '^DB_PASSWORD=' .env | cut -d= -f2)" vmops > backup-$(date +%F).sql
```

### 恢复

```bash
cat backup-2026-09-26.sql | docker exec -i vmops-mysql mysql -uvmops -p"口令" vmops
```

## 升级

```bash
cd /path/to/virtkite
git pull
go build -o vmops .
pkill -f '^\./vmops$' && sleep 1
set -a; . ./.env; set +a; SERVER_MODE=debug nohup ./vmops > vmops.log 2>&1 &
```

::: warning 必须重启后端
前端重新 `npm run build` 后**必须重启后端进程**——运行中的进程会持有旧版 index.html 的资源哈希，不重启会出现整站白屏（这是最高频的「改完不生效」原因）。
:::

数据库结构变更由 GORM AutoMigrate 在启动时自动完成，无需手工执行 SQL。

## 常见故障 FAQ（按症状索引）

### 页面整站白屏

**症状**：登录页一片白，F12 看到模块脚本 MIME 错误。
**原因**：前端重新构建后未重启后端，页面持有已失效的资源哈希。
**解决**：重启后端进程即可（见上文升级步骤）。

### 改了代码但「不生效」

**原因**：十有八九是 `vmops.pid` 陈旧导致旧进程未被杀掉、新进程绑不上 8080 静默退出，一直在服务的还是旧二进制。
**解决**：`pkill -f '^\./vmops$'` 全清后重启，并 `ss -ltnp | grep 8080` 核对监听进程的 PID。

### 虚拟机无法启动：CPU 虚拟化

**症状**：创建/启动虚拟机报 KVM 相关错误。
**排查**：`egrep -c '(vmx|svm)' /proc/cpuinfo`——输出 0 说明 BIOS 未开 VT-x/AMD-V；嵌套虚拟化场景还需在宿主机开启 `kvm-intel nested`。

### SSH 跳板连不上

**排查顺序**：`ss -ltn | grep 2222` 确认监听（`.env` 需 `JUMPD_ENABLED=1`）→ 提示「失败次数过多」= 限流触发，等 1 分钟 → 「菜单里没有机器」= 该机未运行 / 无 IP / 未托管凭据 / 未授权，四条件缺一不可。

### 凭据解密失败

**症状**：使用已保存凭据报「解密失败」。
**原因**：历史凭据由旧主密钥加密（如轮换过 `CREDENTIAL_MASTER_KEY`）。
**解决**：`go run ./scripts/credential-rekey`（先 dry-run 看条数，确认后 `--apply`）。

### 容器栈启动后 mysql 反复退出

**排查**：`docker logs vmops-mysql`。常见：数据卷首次初始化后修改过 `MYSQL_ROOT_PASSWORD`（该口令仅首初始化生效，改口令需进库 ALTER USER）。

### /metrics 返回 401

正常现象：`METRICS_TOKEN` 已配置，Prometheus 抓取自带配对凭证。手工探测请带 `Authorization: Bearer <token>`。

## 安全基线清单（公网部署前必查）

- [ ] `.env` 权限 600，所有口令强随机且互不相同
- [ ] `METRICS_TOKEN` 已设置（`/metrics` 非公开）
- [ ] 登录安全入口已设置（防扫描爆破）
- [ ] SSH 跳板端口 2222 加防火墙白名单，或改为非标端口
- [ ] 首次登录后立即修改 admin 初始口令
- [ ] 定期检查审计中心与告警推送是否在位
