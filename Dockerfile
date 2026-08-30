# ── Stage 1: build ──────────────────────────────────────────────
FROM golang:1.25 AS builder

WORKDIR /app

# 先单独拷贝依赖文件，利用 Docker 层缓存
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o obs-api .

# ── Stage 2: runtime ─────────────────────────────────────────────
FROM alpine:3.21

# 时区 + CA 证书
RUN apk --no-cache add tzdata ca-certificates && \
    cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo "Asia/Shanghai" > /etc/timezone

WORKDIR /app
COPY --from=builder /app/obs-api .

EXPOSE 8081

ENTRYPOINT ["./obs-api"]
