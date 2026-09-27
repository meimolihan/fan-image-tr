# 多阶段构建：编译阶段只带 Go 工具链，运行阶段只带二进制与 FFmpeg。
# 前端已内嵌进二进制（internal/embedded/web），无需 Node 构建。

# ---------- 构建阶段 ----------
FROM golang:1.25-bookworm AS builder

ARG VERSION=dev

WORKDIR /src

# 先拉依赖，利用镜像层缓存
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 产出静态二进制，可直接放进最小运行镜像
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags "-s -w -X github.com/meimolihan/fan-image-tr/internal/version.Version=${VERSION}" \
    -o /out/fan-image-tr .

# ---------- 运行阶段 ----------
FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ffmpeg \
        ca-certificates \
        curl \
        tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && useradd -r -u 1000 -d /data -s /sbin/nologin fit

COPY --from=builder /out/fan-image-tr /usr/local/bin/fan-image-tr

ENV FIT_APP_PORT=8791 \
    FIT_APP_DATA_DIR=/data \
    FIT_APP_MEDIA_DIR=/photos \
    FIT_FFMPEG_PATH=/usr/bin/ffmpeg \
    FIT_FFMPEG_FFPROBE_PATH=/usr/bin/ffprobe \
    TZ=Asia/Shanghai

# 数据目录（任务快照 / 预设 / 上传 / 产物）与图片根目录都作为卷挂出
RUN mkdir -p /data /photos && chown -R fit:fit /data /photos
VOLUME ["/data", "/photos"]

USER fit
WORKDIR /data

EXPOSE 8791

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -fsS "http://127.0.0.1:${FIT_APP_PORT}/api/health" >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/fan-image-tr"]
