# noVNC 控制台中转镜像（docker-compose 一键全容器形态专用）。
# 混合形态（app 原生直跑）请继续用 start.sh 的宿主机 websockify，
# 两者都监听 6080，同时启动会端口冲突。
FROM python:3.11-slim

# novnc 提供 /usr/share/novnc 静态页；websockify 提供 WS→TCP 中转与 JSONTokenApi 插件
RUN apt-get update \
    && apt-get install -y --no-install-recommends novnc websockify \
    && rm -rf /var/lib/apt/lists/*

EXPOSE 6080

# token-source 指向 compose 网络内的 app 服务（与 start.sh 的宿主机参数同一语义）
CMD ["websockify", \
     "--web", "/usr/share/novnc", \
     "--token-plugin", "JSONTokenApi", \
     "--token-source", "http://app:8080/api/vnc/token/%s", \
     "6080"]
