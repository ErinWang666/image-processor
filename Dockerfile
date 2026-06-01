# ---- 构建阶段 ----
# 用完整的 Go 镜像来编译代码
FROM golang:1.24-alpine AS builder

WORKDIR /app

# 先只复制依赖文件，利用 Docker 层缓存
# 只要 go.mod/go.sum 没变，这一层就不会重新跑
COPY go.mod go.sum ./
RUN go mod download

# 再复制所有源码
COPY . .

# 编译成单一二进制文件
# CGO_ENABLED=0 关掉 CGO，让二进制文件在 alpine 镜像里能跑
# -o /app/server 指定输出路径
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/server ./cmd/server/main.go

# ---- 运行阶段 ----
# 用极小的 alpine 镜像，只放编译好的二进制文件
# 最终镜像大小约 20MB，而不是 Go 完整镜像的 500MB+
FROM alpine:3.19

WORKDIR /app

# 从构建阶段只复制编译好的二进制
COPY --from=builder /app/server .

EXPOSE 8080

CMD ["./server"]
