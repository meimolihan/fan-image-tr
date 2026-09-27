# fan-image-tr

基于 FFmpeg 的图片批量处理 Web 工具。单文件二进制 + 内嵌前端，丢到任意一台装了 FFmpeg 的机器上就能用。

- **格式多样**：JPEG / PNG / WebP / AVIF / GIF / HEIC / JPEG 2000 / TIFF / BMP / QOI，按当前 FFmpeg 实际编译情况逐个自检后展示
- **硬件加速**：自动探测 QSV / NVENC / VAAPI，**逐格式实测**可用编码器，而不是看编码器在不在
- **批量队列**：多选提交、进度与阶段、取消、重试、断电后任务快照可恢复
- **预设管理**：内置预设 + 自定义预设，存本地 JSON
- **一套参数多种规格**：通过 API 的 `variants` 一次生成缩略图 / 列表图 / 大图（界面暂未提供入口）
- **可预览**：提交前看体积预估与**真实 FFmpeg 命令行**，参数不对劲能立刻发现
- **无 Node 依赖**：前端直接嵌进 Go 二进制

## 快速开始

```bash
# 1. 编译
make build            # 或 go build -o bin/fan-image-tr .

# 2. 自检 FFmpeg 环境（输出格式与硬件加速能力）
./bin/fan-image-tr check

# 3. 启动
./bin/fan-image-tr -media /path/to/photos -port 8791
```

浏览器打开 <http://127.0.0.1:8791>。

## 安装

### 二进制

```bash
git clone https://github.com/meimolihan/fan-image-tr.git
cd fan-image-tr
make build-all         # 交叉编译到 dist/
```

或从 Releases 下载对应平台的产物，解压即用。

**依赖**：Go 1.25+（编译）、FFmpeg 8+（运行，需 `ffmpeg` 与 `ffprobe`）。

### Docker

```bash
docker compose up -d --build
# 或
docker build -t fan-image-tr .
docker run -d --name fit -p 8791:8791 \
  -v ./data:/data -v ./photos:/photos:ro fan-image-tr
```

### systemd

```bash
sudo useradd -r -s /sbin/nologin fit
sudo mkdir -p /var/lib/fan-image-tr
sudo cp scripts/fan-image-tr.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now fan-image-tr
sudo systemctl status fan-image-tr
journalctl -u fan-image-tr -f
```

按需修改 unit 里的 `FIT_APP_MEDIA_DIR`、`User` 与 `ReadWritePaths`。

## 配置

优先级：**命令行参数 > 环境变量（`FIT_` 前缀） > 配置文件 > 默认值**。

| 命令行 | 环境变量 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `-port` | `FIT_APP_PORT` | `8791` | 监听端口 |
| `-data` | `FIT_APP_DATA_DIR` | `./data` | 任务快照、预设、上传、产物 |
| `-media` | `FIT_APP_MEDIA_DIR` | 当前工作目录 | 图片浏览根目录 |
| `-output` | `FIT_APP_OUTPUT_DIR` | `<data>/output` | 转换产物目录 |
| `-upload` | `FIT_APP_UPLOAD_DIR` | `<data>/uploads` | 上传文件目录 |
| `-worker` | `FIT_APP_WORKER` | `2` | 转换并发数 |
| `-log-level` | —（无环境变量） | `info` | `debug` / `info` / `warn` / `error` |
| `-debug` | `FIT_APP_DEBUG` | `false` | 调试日志 |
| — | `FIT_FFMPEG_PATH` | `ffmpeg` | ffmpeg 可执行文件 |
| — | `FIT_FFMPEG_FFPROBE_PATH` | `ffprobe` | ffprobe 可执行文件 |
| — | `FIT_FFMPEG_ACCEL` | `auto` | `auto` / `qsv` / `nvenc` / `vaapi` / `none` |
| — | `FIT_VERSION` | 编译期注入 | 版本号 |
| `-web` | — | 内嵌资源 | 覆盖前端静态目录（调试用） |

浏览根目录、数据目录、上传目录、输出目录都允许被 API 访问；**其余路径一律拒绝**（`ErrPathNotAllowed` / HTTP 403）。

## 目录约定

```
data/
├── tasks.json     任务快照（重启后未完成的任务回到队列）
├── profiles.json  自定义预设（内置预设不可改）
├── uploads/       上传的文件
└── output/        转换产物
```

## HTTP API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/health` | 健康检查 |
| GET | `/api/version` | 版本、构建信息 |
| GET | `/api/capabilities` | 格式字典 + 硬件加速自检结果 |
| GET | `/api/options` | 缩放 / 滤镜 / 颜色模式 / 水印等选项字典 |
| GET | `/api/media/dir?path=` | 目录浏览（`is_dir` / `mod_time`） |
| GET | `/api/media/info?path=` | 单图探测信息 |
| POST | `/api/media/probe` | 批量探测（`{paths:[...]}`） |
| GET | `/api/media/thumb?path=&size=` | 缩略图 |
| GET | `/api/media/raw?path=` | 原图（仅图片类型） |
| GET | `/api/media/download?path=` | 下载（仅图片类型） |
| POST | `/api/media/rename` | 重命名（`{path, new_name}`，不允许跨目录） |
| POST | `/api/media/delete` | 删除文件（不允许删目录） |
| POST | `/api/media/mkdir` | 新建目录 |
| POST | `/api/media/upload` | 上传（multipart） |
| POST | `/api/estimate` | 体积预估 |
| POST | `/api/preview` | 返回将要执行的 FFmpeg 命令 |
| GET/POST/DELETE | `/api/tasks[/:id]` | 任务列表 / 创建 / 清理 |
| GET | `/api/tasks/stats` | 任务计数 |
| POST | `/api/tasks/:id/cancel` `/retry` | 取消 / 重试 |
| GET/POST/DELETE | `/api/presets[/:name]` | 预设列表 / 保存 / 删除 |

### 状态码约定

| 码 | 场景 |
| --- | --- |
| 400 | 参数非法（格式、缩放方式拼错、请求体错误） |
| 403 | 路径不在允许根目录内 |
| 404 | 任务 / 预设 / 文件不存在 |
| 409 | 任务状态不允许该操作（取消已完成任务、重试进行中任务…） |
| 503 | FFmpeg 探测失败或能力不可用 |

### 提交任务的例子

```bash
curl -X POST http://127.0.0.1:8791/api/tasks \
  -H 'Content-Type: application/json' \
  -d '{
    "inputs": ["IMG_0001.jpg", "IMG_0002.png"],
    "output_dir": "output",
    "options": {
      "format": "webp",
      "quality": 82,
      "resize": "long_edge",
      "size": 1920,
      "adapt": "fit",
      "strip_metadata": true
    },
    "variants": [
      {"name": "thumb", "resize": "long_edge", "size": 320},
      {"name": "list",  "resize": "long_edge", "size": 640, "quality": 75}
    ]
  }'
```

> **变体语义**：一旦提供 `variants`，`options` 描述的主产物**不再单独输出**，变体列表就是全部产物，
> 其中第一个变体作为任务卡片展示的主规格。

变体字段是**扁平**的（`name` / `format` / `quality` / `resize` / `size` / `custom_width` / `custom_height` / `adapt` / `allow_upscale` / `strip_metadata`），留空则沿用主参数。

`quality` 统一是 **1–100** 的界面量纲（越大越好），服务内部按各编码器换算成 qscale / CRF。

## 硬件加速说明

`/api/capabilities` 里的 `hw_formats` 是**逐个格式实际编码一张测试图**得出的结果，而不是"编码器存在就算可用"。原因是硬编码流水线经常在特定格式上缺少像素格式转换器。

QSV 的 JPEG 有一个坑：`mjpeg_qsv` 输出 `yuvj420p` 时按 limited range 解释，纯白会被解码成 235。程序检测到「硬件编码器 + `yuvj*` 像素格式」时会自动加 `-color_range pc`，避免白底发灰。

## 开发

```bash
make help      # 所有目标
make fmt       # 格式化
make test      # go vet + go test
make race      # 竞态检测
make cover     # 覆盖率报告
make docker    # 构建镜像
```

前端源码在 `internal/embedded/web/`，改完直接 `make build` 重新内嵌即可（无需 Node 构建）。

测试需要 FFmpeg 与 `ffprobe`；`go test ./...` 会跑真实转码用例。

## 许可

见 [LICENSE](LICENSE)。
