# 多阶段构建：golang 构建 → alpine 运行
# 构建：docker build -t vmops:latest .
# 基础镜像必须满足 go.mod 的 `go 1.25.0`（golang:1.21 会直接报版本不足）
FROM golang:1.25-alpine AS builder

WORKDIR /build

# 模块代理：默认走国内镜像（proxy.golang.org 在国内网络常不可达，会让 go mod download 挂死）；
# 海外/内网环境可 docker build --build-arg GOPROXY=https://proxy.golang.org,direct 覆盖
ARG GOPROXY=https://goproxy.cn,direct

# 先复制依赖清单，利用 Docker 层缓存
COPY go.mod go.sum ./
RUN go mod download

# 复制源码并构建 Go 二进制
COPY . .
# 构建参数说明（两项均已实测）：
#   -o vmops .        主包在仓库根目录；写 `./...` 会匹配到 11 个包，
#                     go build 报 "cannot write multiple packages to non-directory"
#   -tags timetzdata  把时区库嵌进二进制：alpine 无 /usr/share/zoneinfo，
#                     否则 DSN 的 loc=Local 退化为 UTC，审计/任务时间差 8 小时
RUN CGO_ENABLED=0 GOOS=linux go build -tags timetzdata -o vmops .

# web/dist 被 .gitignore 排除，干净克隆里可能不存在：先兜底建空目录，
# 保证运行阶段的 COPY --from 不会因源目录缺失而整个构建失败
RUN mkdir -p /build/web/dist

# 运行阶段（alpine 3.19 已停止维护，跟随 builder 的 alpine 大版本）
FROM alpine:3.24

WORKDIR /app

# 容器内时区：时区库已随 -tags timetzdata 编入二进制，无需 apk 安装 tzdata
ENV TZ=Asia/Shanghai

# 仅复制编译产物与静态资源。WORKDIR 与目录布局须对齐 main.go 的 locateWebRoot 探测顺序
# （<exeDir>/web/dist → <exeDir>/static → <cwd>/web/dist → <cwd>/static）：
# 二进制在 /app，故 /app/web/dist 命中第一候选，/app/static 是第二候选兜底。
COPY --from=builder /build/vmops ./
COPY static ./static
# 前端构建产物（需先在宿主机执行 `cd web && npm run build`）；
# 未构建时这里是空目录，main.go 会退到 static 或降级为「仅 API」模式，不会退出进程
COPY --from=builder /build/web/dist ./web/dist

# libvirt 走宿主机，容器内不运行 libvirt
EXPOSE 8080

CMD ["./vmops"]
