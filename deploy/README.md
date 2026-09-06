# deploy/ 部署目录

本目录集中存放部署相关配置，是唯一事实源。两种部署形态共用这里的文件。

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
| alertmanager.yml | 告警分组/路由（receiver 目前是占位 webhook，通知渠道按需补） |
| grafana-datasource.yml | 预置 Prometheus 数据源 |
| grafana-dashboard-provider.yml | 看板文件 provider |
| grafana-dashboard.json | vmops 私有云监控看板（uid: vmops-overview，9 面板） |

镜像版本：grafana 13.2.1 / prometheus v3.14.0 / alertmanager v0.34.0 / mysql 8.0.36（Docker Hub 直连超时时用 `docker.m.daocloud.io` 拉取后 tag 回官方名）。

> ⚠️ **Grafana 不要用 `-slim` 镜像**（如 13.2.1-slim）：slim 版不含内置数据源插件，Prometheus 数据源会报 `plugin not registered`，后台补装器虽会从 grafana.com 逐个下载（依赖外网且极慢，本机 18 个插件要十几分钟），不可靠。用完整版；升级后若插件状态异常，删 `grafana-data` 卷重建即可（provisioning 会自动重建数据源与看板）。
