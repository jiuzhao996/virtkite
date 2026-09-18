# Loki 日志栈部署（deploy/docker-compose.loki.yml）

补齐平台日志能力：**Loki + Promtail**（Grafana 原生支持，单机内存占用小，替代 ELK）。
容器日志与宿主机日志进 Loki 后，在 Grafana Explore 交互查询，也可经平台后端代理接口程序化查询。

```
宿主机 /var/log/*.log ─┐
Docker 容器日志 ────────┼─→ promtail（采集+打标）→ loki（存储/索引/7d 保留）→ Grafana Explore
systemd journal（可选）─┘                                              └→ 平台后端 /api/monitor/loki/* 代理
```

本文件是独立增量 compose，**不并入主 docker-compose.yml**——日志栈可独立启停/升级，
不需要日志能力的环境不部署即可（平台对 Loki 不可达只返回 502 提示，其余功能不受影响）。

## 前置条件

- 主栈（`docker-compose.yml`）已启动过至少一次：日志栈复用其默认网络 `vmops_default`（external 引用）。
  若网络不存在：`docker network create vmops_default`，或先 `docker compose up -d` 主栈。
- Grafana 需在同一网络（主栈 grafana 服务满足）。

## 部署

```bash
docker compose -f deploy/docker-compose.loki.yml up -d
# 验证就绪（健康检查通过后）：
curl -s http://127.0.0.1:3100/ready        # → ready
docker logs vmops-promtail --tail 20       # 无 config error、推送无重试报错
```

端口说明：Loki 的 3100 **只绑定宿主机 127.0.0.1**——宿主机原生直跑的 vmops 后端（LOKI_URL
默认 `http://127.0.0.1:3100`）经它访问，不向局域网暴露；容器间（Grafana）走网络内 `http://loki:3100`。

⚠️ **权限提示**：promtail 容器以 root 运行（官方镜像默认），且挂载了 `/var/run/docker.sock`
（等价宿主机 root 权限）。日志栈只应部署在可信管理节点上。

## ✅ 本机已验证（2026-09，实测通过）

- `loki-config.yml`：`docker run --rm -v ...:/etc/loki/local-config.yaml grafana/loki:2.9.14
  -config.file=... -verify-config=true` → `config is valid`。
  **坑**：上报开关字段是 `analytics.reporting_enabled`（不是 report_enabled，写错 2.9 拒绝启动）。
- `promtail-config.yml`：短跑实测 `/var/log` 目标挂载成功（tailer started）、docker_sd 发现容器、
  推送地址 `http://loki:3100/loki/api/v1/push` 正确。
- 全链路：`docker compose -f deploy/docker-compose.loki.yml up -d` 后
  `{container=~"vmops-.*"}` 返回 vmops-loki / vmops-promtail 真实日志（container 标签齐全），
  `{job="varlogs"}` 采到 /var/log/syslog。

## Grafana 添加 Loki 数据源

编辑 `deploy/grafana-datasource.yml`（provisioning 挂载，改完 `docker restart vmops-grafana` 生效），
在 datasources 列表追加：

```yaml
apiVersion: 1
datasources:
  - name: Prometheus
    type: prometheus
    access: proxy
    url: http://prometheus:9090
    isDefault: true
  # ── 追加以下片段 ──
  - name: Loki
    type: loki
    access: proxy
    url: http://loki:3100
```

之后打开 Grafana → Explore → 选 Loki 数据源即可查询（admin/admin，首次登录改密）。

## LogQL 速查

```logql
# 平台容器全部日志（vmops-* 为容器名前缀，标签由 promtail docker_sd 自动附加）
{container=~"vmops-.*"}

# 过滤错误行（行过滤运算符）
{container="vmops-app"} |= "error"

# 宿主机 /var/log（job=varlogs）
{job="varlogs"} |= "sshd"

# 按 compose 服务分组（docker compose 标签自动打上）
{compose_service="mysql"}

# 指标查询：vmops-app 每 5 分钟错误行速率
sum(rate({container="vmops-app"} |= "level=error" [5m]))

# 容器日志字节量 Top（promtail 对 docker 日志附带 stream 标签）
topk(5, sum by (container) (count_over_time({container=~".+"}[$__range])))
```

## 平台后端代理接口（监控中心用）

| 接口 | 说明 |
|------|------|
| `GET /api/monitor/loki/query?query=<LogQL>&limit=100&since=1h` | 代理 Loki query_range，响应 `{code:200,data:<Loki原始JSON>}` |
| `GET /api/monitor/loki/labels` | 标签名列表（选择器补全） |

- 参数：`limit` 默认 100（1~5000）；`since` 支持 Go duration（`30m`/`2h`），非法回退 1h，
  上限 168h（与保留期对齐）。
- Loki 未部署/不可达 → 502「Loki 未部署或不可达（参考 deploy/README-loki.md 部署）」。
- 地址来源：env `LOKI_URL`（建议加入 config + main 注入），宿主机直跑默认 `127.0.0.1:3100`，
  compose 内配 `LOKI_URL: http://loki:3100`。
- 权限：与监控中心同口径（NonViewerMiddleware，viewer 403）。

## 保留与磁盘占用

- 保留期 **7 天（168h）**：`limits_config.retention_period`，过期由 compactor 在压实期删除
  （`retention_enabled: true` + `delete_request_store: filesystem`，二者缺一 2.9 启动即报错）。
- 数据落在命名卷 `vmops-loki_loki-data`（chunks + tsdb 索引 + compactor 工作目录）。
  日志量异常膨胀时：`docker system df -v | grep loki` 看占用，调整保留期后重启 vmops-loki。
- promtail 断点记录在 `vmops-loki_promtail-positions` 卷（重启不重采不丢采）。

## 可选：采集 systemd journal

promtail-config.yml 里 journal 段默认注释（依赖宿主机 journal 持久化目录）。启用：

1. compose 的 promtail 服务追加挂载：
   `/var/log/journal:/var/log/journal:ro`、`/run/log/journal:/run/log/journal:ro`、
   `/etc/machine-id:/etc/machine-id:ro`
2. 取消 `scrape_configs` 里 journal job 的注释
3. `docker compose -f deploy/docker-compose.loki.yml up -d --force-recreate promtail`

## 可选：不用 docker.sock 的纯文件采集

无法挂载 docker.sock 的环境，启用 promtail-config.yml 末尾注释的 `docker-files` job
（解析容器原始 json 日志，pipeline: json→timestamp→labels→output）。注意：文件名只有容器 ID
拿不到容器名，且必须停用 docker job 避免双份采集。

## 卸载

```bash
docker compose -f deploy/docker-compose.loki.yml down      # 保留数据卷
docker compose -f deploy/docker-compose.loki.yml down -v   # 连日志数据一起清掉
```
