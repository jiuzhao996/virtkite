# Prometheus file_sd 目标目录（平台自动生成，勿手工编辑）

平台后端配置 `FILE_SD_PATH` 指向本目录下的 `targets.json` 时，后台协程每分钟把
「running 且 IP 非空」的虚拟机写入该文件（先写 .tmp 再原子 rename），格式：

```json
[
  {
    "targets": ["192.168.1.10:9100"],
    "labels": {"job": "vm-node", "vm_name": "web-1", "vm_id": "1"}
  }
]
```

- docker-compose 形态：本目录同时挂载进 prometheus 容器 `/etc/prometheus/file_sd`
  与 app 容器 `/app/deploy/file_sd`，两侧路径已在 docker-compose.yml 配好。
- 宿主机原生直跑：在 `.env` 设置 `FILE_SD_PATH=<仓库绝对路径>/deploy/file_sd/targets.json`。
- Prometheus 侧经 `deploy/prometheus.yml` 的 `file_sd_configs` 每 30s 重载该文件。
- 预览当前将生成的内容：登录后 `GET /api/monitor/file-sd`。
- 前提：VM 内需安装并运行 node_exporter（默认 :9100），否则目标 up 但抓取失败。
