# deploy/ 部署目录

本目录集中存放部署相关配置，是唯一事实源。两种部署形态共用这里的文件。

## 公网入口（✅ 已上线）：https://kpyun.fun:4321

架构：浏览器 → 腾讯云 nginx（TLS 终止，证书 /etc/nginx/kpyun/，vhost conf.d/kpyun.conf，存档 kpyun.conf）
→ frp 隧道（云端 frps.service :7000，家里 virtkite-frpc.service，`proxyBindAddr=127.0.0.1` 保证回源端口不裸奔公网）
→ 家里 vmops(:8080) / websockify(:6080)。

- **4321 端口的原因**：kpyun.fun 尚未 ICP 备案，腾讯云对境内服务器 80/443 做 SNI 拦截（jzops.fun 已备案不受影响）；
  备案完成后 443 vhost 自动可用，无需改配置。控制台 noVNC 走同源 `/vnc/` 前缀（nginx wss 升级）。
- **公网暴露加固**（.env）：SERVER_MODE=release + 强随机 JWT_SECRET_KEY + CORS_ORIGINS=kpyun.fun。
- **隧道持久化**：两端 systemd（Restart=always，frp 心跳自动重连）；旧 SSH 反向隧道方案（virtkite-tunnel）已 disable 备用。

## 双存储监控（✅ 已上线）

| 位置 | 组件 | 保留 | 角色 |
|------|------|------|------|
| 家里 | Prometheus（docker，9090） | **90 天**（长存，本地 TSDB） | 主动抓取 vmops/exporter，告警规则评估 |
| 腾讯云 | VictoriaMetrics v1.150.0（二进制 systemd，8081） | **1 个月**（短存） | 接收本地 remote_write 推送，供公网查询/看板 |

- 推送过滤：`write_relabel_configs` 只推 `job="vmops"` 业务指标，丢弃 go_/process_ 运行时噪声。
- 云端入口安全：nginx 8081 TLS + Basic Auth（VM 二进制 v1.150 已移除 -httpAuthKey 旗标，改在 nginx 层认证）；
  VM 本体只绑回环 8428。凭据见家里 `deploy/tls/.vm_basic.secret`（不入库）。


## 形态一：混合形态（开发/演示推荐，当前使用）

```
宿主机原生：vmops(:8080) + websockify(:6080)
Docker 容器：mysql + prometheus + alertmanager + grafana
```

启动方式：

```bash
docker compose up -d mysql prometheus alertmanager grafana   # 容器侧
./start.sh                                                   # 宿主机侧（app + websockify）
```

说明：
- Prometheus 容器经 `host.docker.internal:8080`（compose `extra_hosts: host-gateway`）抓取宿主机上的 app。
- Grafana 已通过 compose 环境变量开启匿名只读 + iframe 嵌入（`GF_SECURITY_ALLOW_EMBEDDING` 等），产品「监控中心」页直接嵌 `:3000/d/vmops-overview/?kiosk`。
- 告警链路：prometheus.yml 的 `alerting:` 段 → alertmanager:9093（Prometheus 2.x 无 `--alertmanager.url` 参数，勿回退）。

## 形态二：一键全容器（发布形态）

```bash
docker compose up -d   # 6 个容器：mysql + app + websockify + prometheus + alertmanager + grafana
```

与形态一的差异与注意：
- app 容器经挂载 `/var/run/libvirt/libvirt-sock` 直通宿主机 libvirtd（libvirt 本身无法容器化）。
- websockify 容器由 `docker/websockify.Dockerfile` 构建（debian novnc + websockify 包），
  token-source 指向 compose 网络内的 app 服务。
- **混合形态请勿启动 websockify 容器**（`docker compose up -d mysql prometheus alertmanager grafana`
  按需选择即可）：它与宿主机 start.sh 起的 websockify 抢 6080 端口。
- app 切进容器后，Prometheus 抓取目标可改回 `app:8080`（见 prometheus.yml 注释）。

## 文件清单

| 文件 | 用途 |
|---|---|
| prometheus.yml | 抓取配置 + 告警规则引用 + alerting 对接 |
| alerts.yml | 5 条告警规则（VMRunningDrop/HostCpuHigh/PoolSpaceLow/TaskBacklog/VMCpuHot，全中文 summary） |
| alertmanager.yml | 告警分组/路由（占位 webhook + 平台告警网关 vmops-webhook，见下「告警网关」） |
| grafana-datasource.yml | 预置 Prometheus 数据源 |
| grafana-dashboard-provider.yml | 看板文件 provider |
| grafana-dashboard.json | vmops 私有云监控看板（uid: vmops-overview，9 面板） |
| file_sd/ | Prometheus file_sd 目标目录（平台自动生成 targets.json，见下「监控服务发现闭环」） |

## 监控服务发现闭环（file_sd）

平台建 VM 后，VM 内的 node_exporter 自动进入 Prometheus 抓取目标，无需手工改 prometheus.yml：

1. 平台侧设置 `FILE_SD_PATH` 环境变量，指向目标文件（默认空=不启用）：
   - docker compose 形态：compose 已为 app 配好 `FILE_SD_PATH: /app/deploy/file_sd/targets.json`，
     该目录同时挂载进 prometheus 容器的 `/etc/prometheus/file_sd`，两侧共享同一宿主机目录，开箱即用。
   - 宿主机原生直跑（混合形态）：在 `.env` 里加
     `FILE_SD_PATH=/home/<user>/vmops/deploy/file_sd/targets.json`（与 compose 挂载同一目录即可复用）。
2. 平台后台协程每分钟把「running 且 IP 非空」的虚拟机写入该文件（先写 `.tmp` 再原子 rename），
   格式 `[{"targets": ["<ip>:9100"], "labels": {"job": "vm-node", "vm_name": ..., "vm_id": ...}}]`；
   同 IP 多机去重（vm_name/vm_id 逗号拼接）。
3. `deploy/prometheus.yml` 的 `file_sd_configs` 每 30s 重载该目录下的 `*.json`。
4. 登录后 `GET /api/monitor/file-sd` 可实时预览当前将生成的内容。

> ⚠️ **VM 内需自行安装 node_exporter（默认 :9100）**，平台只负责下发抓取目标；
> 目标未装 exporter 会呈现 up 状态翻转，属预期。

## 告警网关（Alertmanager webhook 回推）

`deploy/alertmanager.yml` 的 default receiver 已并挂第二个 webhook：告警（含 resolved）
推回平台 `POST /api/monitor/webhook`，按 fingerprint 去重入库 `alerts` 表，
监控中心页的「告警历史」卡片可追溯（实时列表代理 AM，重启/环形截断后查不到，历史不受影响）。

- host.docker.internal 由 compose alertmanager 服务的 `extra_hosts: host-gateway` 解析。
- 平台设置 `ALERT_WEBHOOK_TOKEN` 后要求 `?token=<值>` 或 `Authorization: Bearer <值>`，
  需同步在 alertmanager.yml 的 webhook url 后追加 `?token=<值>`；默认空=公开接收。
- 除鉴权失败/请求体非法外网关恒返回 200（Alertmanager 对非 2xx 会按策略重试轰炸），
  处理失败只记服务端日志。

镜像版本：grafana 13.2.1 / prometheus v3.14.0 / alertmanager v0.34.0 / mysql 8.0.36（Docker Hub 直连超时时用 `docker.m.daocloud.io` 拉取后 tag 回官方名）。

> ⚠️ **Grafana 不要用 `-slim` 镜像**（如 13.2.1-slim）：slim 版不含内置数据源插件，Prometheus 数据源会报 `plugin not registered`，后台补装器虽会从 grafana.com 逐个下载（依赖外网且极慢，本机 18 个插件要十几分钟），不可靠。用完整版；升级后若插件状态异常，删 `grafana-data` 卷重建即可（provisioning 会自动重建数据源与看板）。
