FROM golang:1.20.4-alpine3.18 AS builder

WORKDIR /src

#国内服务器可以取消以下注释
#RUN go env -w GO111MODULE=on && \
#    go env -w GOPROXY=https://goproxy.cn,direct

# 先拷贝模块定义文件并下载依赖，充分利用 Docker 缓存
COPY go.mod go.sum ./
RUN go mod download

# 再拷贝源码并构建
COPY . .
RUN go build -ldflags "-s -w" -o ./bin/rss-reader .

FROM alpine

# 安装运行时需要的工具
RUN apk add --no-cache tzdata jq bash
ENV TZ=Asia/Shanghai

WORKDIR /app

# 从构建阶段拷贝编译好的二进制文件和配置
COPY --from=builder /src/bin/rss-reader /app/rss-reader
COPY --from=builder /src/config.json /app/config.json

EXPOSE 8080

ENTRYPOINT ["./rss-reader"]
