# 多阶段构建：golang 构建 → alpine 运行
FROM golang:1.21 AS builder

WORKDIR /build

# 先复制依赖清单，利用 Docker 层缓存
COPY go.mod go.sum ./
RUN go mod download

# 复制源码并构建 Go 二进制
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o vmops ./...

# 运行阶段
FROM alpine:3.19

WORKDIR /app

# 仅复制编译产物与静态资源
COPY --from=builder /build/vmops ./
COPY static ./static
# 前端构建产物（需先在宿主机执行 `cd web && npm run build`）
COPY web/dist ./web/dist

# libvirt 走宿主机，容器内不运行 libvirt
EXPOSE 8080

CMD ["./vmops"]
