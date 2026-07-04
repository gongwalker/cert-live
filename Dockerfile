FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY . /app/
ENV CGO_ENABLED=0 \
    GOPROXY=https://goproxy.cn,direct
RUN go build -trimpath -ldflags="-s -w" -o cert-live


FROM alpine:latest
# ca-certificates：HTTPS 探测目标站点校验证书需要根 CA
# tzdata：/usr/share/zoneinfo 时区数据,配合下面的 TZ 让日志走 CST
RUN apk add --no-cache ca-certificates tzdata
ENV TZ=Asia/Shanghai
WORKDIR /app
# 二进制已用 go:embed 内嵌 templates 和 static，无需再 COPY 资源文件
COPY --from=builder /app/cert-live .
VOLUME /app/data
EXPOSE 9527
ENTRYPOINT ["./cert-live", "serve"]
