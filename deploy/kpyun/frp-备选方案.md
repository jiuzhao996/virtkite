# frp 备选方案（当前未启用，现役方案是 SSH 反向隧道 + autossh）

当前隧道：`virtkite-tunnel.service`（autossh -R，复用 22 端口与既有密钥，零安全组改动）。
如需切 frp（性能/多路复用更好），除下述配置外**必须在腾讯云控制台放行 7000 端口**。

## 服务端（腾讯云 152.136.110.39）

frps.toml:
```toml
bindAddr = "0.0.0.0"
bindPort = 7000
auth.token = "<openssl rand -hex 24 生成>"
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
