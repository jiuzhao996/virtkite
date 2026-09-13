# 系统架构图

> 整体部署形态：本机 Ubuntu 26.04（KVM 宿主机）运行平台与监控栈，云服务器 kpyun.fun 经 frp 隧道提供公网访问与远程指标存储。

```mermaid
flowchart TB
    subgraph user["用户端（浏览器）"]
        pc["管理员 / 操作员 / 只读用户<br/>REST + WebSocket + noVNC"]
    end

    subgraph cloud["云服务器 kpyun.fun"]
        nginx["Nginx TLS 反代<br/>Web 入口 + /grafana 子路径"]
        frps["frps（隧道服务端）"]
        vm["VictoriaMetrics<br/>指标远程存储（:8081 Basic Auth）"]
    end

    subgraph host["本机 · Ubuntu 26.04 桌面版（KVM 宿主机）"]
        frpc["frpc（隧道客户端）"]
        app["鸢航 VirtKite 单二进制 :8080<br/>REST API + 前端静态托管<br/>三级角色 + 资产授权 + 审计<br/>异步任务 + /metrics exporter"]
        ws["websockify :6080<br/>noVNC + JSONTokenApi"]
        fsd["file_sd 目录<br/>（deploy/file_sd）"]
        subgraph kvm["虚拟化层 libvirt · qemu:///system"]
            vms["虚拟机 × N（Master / node / Ceph / ELK …）<br/>内装 node_exporter :9100"]
        end
        subgraph compose["Docker Compose：监控栈 + MySQL"]
            mysql[("MySQL :3306")]
            prom["Prometheus :9090<br/>9 条告警规则"]
            am["Alertmanager :9093"]
            graf["Grafana :3000"]
        end
    end

    pc ==>|"HTTPS www.kpyun.fun"| nginx
    nginx --> frps
    frps ==>|"frp 隧道"| frpc
    frpc --> app
    pc -.->|"局域网直连 :8080"| app
    app --> mysql
    app ==>|"unix socket：域管理/快照/卷"| vms
    app -.->|"SSH 终端 / 串口 PTY"| vms
    app -->|"签发一次性 VNC token"| ws
    ws ==>|"VNC :5900"| vms
    app -->|"30s 原子写 file_sd"| fsd
    fsd -.->|"服务发现"| prom
    prom -->|"抓取 /metrics（METRICS_TOKEN）"| app
    prom -->|"抓取 node_exporter"| vms
    prom -->|"告警规则触发"| am
    am -->|"webhook ?token= 入库"| app
    app -.->|"query_range 历史曲线"| prom
    prom ==>|"remote_write Basic Auth"| vm
    graf -.-> prom
    pc -.->|"iframe 嵌入 /grafana"| graf
```

## 层次说明

| 层 | 组件 | 职责 |
|---|---|---|
| 用户端 | 浏览器 | 三角色访问 REST API / WebSocket 终端 / noVNC 控制台 |
| 云服务器 | Nginx TLS + frps + VictoriaMetrics | 公网入口与隧道中转；业务指标远程长存 |
| 平台层 | 鸢航 VirtKite 单二进制 | API、静态前端、任务系统、授权审计、内建 exporter |
| 控制台链路 | websockify + noVNC | 一次性 token 换 VNC 连接 |
| 虚拟化层 | libvirt / qemu | 15 台虚拟机的真实生命周期 |
| 监控栈 | Prometheus / Alertmanager / Grafana | 指标采集、告警、看板；历史曲线以 Prometheus 为库 |
