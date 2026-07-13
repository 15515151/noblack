# syntax=docker/dockerfile:1

# ============ 构建阶段 ============
# 静态编译 (CGO_ENABLED=0), 产出一个不依赖 libc 的单文件二进制。
FROM golang:1.25.12-alpine AS build

WORKDIR /src

# 先拷依赖清单并下载, 利用 Docker 层缓存 (源码变动时无需重新拉依赖)。
COPY go.mod go.sum ./
RUN go mod download

# 再拷源码并编译。
COPY . .
# -ldflags "-s -w" 去掉符号表与调试信息, 减小体积。
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/noblack ./cmd/server

# ============ 运行阶段 ============
# alpine 体积小 (~7MB) 且自带 sh, 方便入口脚本做首次初始化与排障。
FROM alpine:3.20

# 时区数据 (可选, 便于日志时间正确); ca-certificates 备将来外连用。
RUN apk add --no-cache tzdata ca-certificates

WORKDIR /app

# 拷入二进制、内置默认词库 (首次启动用来初始化数据卷)、入口脚本。
COPY --from=build /out/noblack /app/noblack
COPY words.json /app/words.default.json
COPY docker-entrypoint.sh /app/entrypoint.sh

# /data 为持久化目录（词库 + 统计）；容器以 root 运行，避免绑定挂载权限不一致。
RUN chmod +x /app/entrypoint.sh \
    && mkdir -p /data


# 通过环境变量配置, 入口脚本会翻译成命令行参数。
ENV NB_ADDR=":8080" \
    NB_WORDS="/data/words.json" \
    NB_STATS="/data/stats.json" \
    NB_TOKEN="" \
    NB_CI="false" \
    NB_WATCH="true"

# 显式使用 root，确保绑定挂载的持久化目录可写。
USER root

EXPOSE 8080
VOLUME ["/data"]

# 简单健康检查: /health 返回 200 即健康。
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/health >/dev/null 2>&1 || exit 1

ENTRYPOINT ["/app/entrypoint.sh"]
