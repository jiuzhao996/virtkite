# frp（✅ 现役方案，2026-09-11 起）

两端 systemd 服务：云端 `frps.service`（/usr/local/frp/）+ 家里 `virtkite-frpc.service`（/usr/local/frp/frpc.toml）。
已验证：断线自动重连（frp 内置心跳）；`proxyBindAddr=127.0.0.1` 保证 18080/16081 不裸奔公网、只能走 nginx TLS。

## 已弃用的旧方案（保留文档）

SSH 反向隧道 + autossh（`virtkite-tunnel.service`，已 disable，unit 文件留存可随时切回）。
弃用原因：用户偏好 frp 的自动重连语义；frp 还有流量仪表盘与多路复用优势。

## 服务端（腾讯云 152.136.110.39，/usr/local/frp/frps.toml）

```toml
bindAddr = "0.0.0.0"
bindPort = 7000
proxyBindAddr = "127.0.0.1"
auth.token = "<实机值，家里 deploy/tls/.frp_token.secret 同步>"
```

## 客户端（家里宿主机）

frpc.toml:
```toml
serverAddr = "152.136.110.39"
serverPort = 7000
auth.token = "<与服务端一致>"

[[proxies]]
name = "virtkite-web"
type = "tcp"
localIP = "127.0.0.1"
localPort = 8080
remotePort = 18080

[[proxies]]
name = "virtkite-novnc"
type = "tcp"
localIP = "127.0.0.1"
localPort = 6080
remotePort = 16081
```

nginx vhost（deploy/kpyun/kpyun.conf）无需任何改动——frp 与 SSH 隧道在云侧落点是相同的 18080/16081。
