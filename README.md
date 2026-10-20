<div align="center">

# fan-image-tr

_✨ 基于 FFmpeg 的图片批量处理 Web 工具（单文件二进制 + 内嵌前端）✨_

</div>

<p align="center">
  <a href="https://github.com/meimolihan/fan-image-tr/releases/latest">
    <img src="https://img.shields.io/github/v/release/meimolihan/fan-image-tr?color=brightgreen" alt="release">
  </a>
  <a href="https://github.com/meimolihan/fan-image-tr/actions">
    <img src="https://img.shields.io/github/actions/workflow/status/meimolihan/fan-image-tr/release.yml?branch=main" alt="release status">
  </a>
  <a href="https://hub.docker.com/repository/docker/mobufan/fan-image-tr">
    <img src="https://img.shields.io/docker/pulls/mobufan/fan-image-tr?color=brightgreen" alt="docker pull">
  </a>
  <a href="https://github.com/meimolihan/fan-image-tr/releases/latest">
    <img src="https://img.shields.io/github/downloads/meimolihan/fan-image-tr/total?color=brightgreen&include_prereleases" alt="downloads">
  </a>
</p>

<p align="center">
  <a href="#功能">功能</a>
  ·
  <a href="#快速开始">快速开始</a>
  ·
  <a href="#项目部署">项目部署</a>
  ·
  <a href="#管理命令">管理命令</a>
  ·
  <a href="#配置">配置</a>
  ·
  <a href="#http-api">HTTP API</a>
  ·
  <a href="#硬件加速说明">硬件加速说明</a>
  ·
  <a href="#开发">开发</a>
</p>

## 功能

+ [x] 基于 FFmpeg 的**图片批量处理**，单文件二进制 + 内嵌前端，装了 FFmpeg 就能用
+ [x] **格式多样**：JPEG / PNG / WebP / AVIF / GIF / HEIC / JPEG 2000 / TIFF / BMP / QOI，按当前 FFmpeg 实际编译情况逐个自检后展示
+ [x] **硬件加速**：自动探测 QSV / NVENC / VAAPI，**逐格式实测**可用编码器，而不是看编码器在不在
+ [x] **批量队列**：多选提交、进度与阶段、取消、重试、断电后任务快照可恢复
+ [x] **预设管理**：内置预设 + 自定义预设，存本地 JSON
+ [x] **一套参数多种规格**：通过 API 的 `variants` 一次生成缩略图 / 列表图 / 大图（界面暂未提供入口）
+ [x] **可预览**：提交前看体积预估与**真实 FFmpeg 命令行**，参数不对劲能立刻发现
+ [x] **无 Node 依赖**：前端直接嵌进 Go 二进制

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

## 项目部署

默认端口：**8791**；默认安装目录：**/var/lib/fan-image-tr**。

**依赖**：Go 1.25+（编译）、FFmpeg 8+（运行，需 `ffmpeg` 与 `ffprobe`）。

### 一键脚本安装（systemd，推荐）

```bash
# 交互式安装
bash scripts/install.sh

# 参数静默安装（端口 / 安装目录 / 图片浏览根目录）
bash scripts/install.sh -p 8791 -d /var/lib/fan-image-tr -s /volume1/photo -y

# curl | bash 远程安装
bash -c "$(curl -sSL https://raw.githubusercontent.com/meimolihan/fan-image-tr/main/scripts/install.sh)" -p 8791 -d /var/lib/fan-image-tr
```

安装脚本会：检测/安装 FFmpeg → 落地二进制到 `${INSTALL_DIR}/fan-image-tr` → 建立 `/usr/local/bin/fan-image-tr` 软链 →
写安装记录 `/etc/fan-image-tr.conf` → 生成并启动 `fan-image-tr.service`（无 systemd 环境回退后台运行）。

### Docker

```bash
docker compose up -d --build
# 或
docker build -t fan-image-tr .
docker run -d --name fit -p 8791:8791 \
  -v ./data:/data -v ./photos:/photos:ro fan-image-tr
```

多架构镜像（linux/amd64 + linux/arm64）：

```bash
docker pull mobufan/fan-image-tr:latest
docker pull ghcr.io/meimolihan/fan-image-tr:latest
```

### systemd（手动）

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

### 二进制

```bash
git clone https://github.com/meimolihan/fan-image-tr.git
cd fan-image-tr
make release           # 本地构建发布产物到 dist/（linux amd64 + arm64 + sha256）
```

或从 [Releases](https://github.com/meimolihan/fan-image-tr/releases/latest) 下载对应平台的产物（`fan-image-tr_linux_amd64` / `fan-image-tr_linux_arm64`），解压即用。

## 管理命令

二进制内置 systemd / 进程管理子命令（需 root 权限）：

```bash
fan-image-tr status                       # 查看运行方式、PID、端口、访问地址、运行时长、内存、健康检查
fan-image-tr start | stop | restart        # 启动 / 停止 / 重启服务（systemd 优先，其次直接管理进程）
fan-image-tr check                         # 检查 FFmpeg 环境与格式 / 硬件加速能力
fan-image-tr uninstall -y                  # 免确认卸载，保留数据目录
fan-image-tr uninstall -y --purge          # 免确认卸载，并删除数据目录
fan-image-tr version | help                # 版本 / 帮助
```

系统级安装也可用脚本卸载：

```bash
bash scripts/uninstall.sh                  # 交互式（数据目录默认保留）
bash scripts/uninstall.sh -y               # 免确认
bash scripts/uninstall.sh -y --purge       # 同时删除数据目录
```

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

安装记录文件 `/etc/fan-image-tr.conf` 由 `install.sh` 生成，记录 `INSTALL_DIR` / `BIN_PATH` / `PORT` / `DATA_DIR` / `MEDIA_DIR`。

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
make check     # FFmpeg 能力自检
make release   # 本地构建发布产物（dist/）
make docker    # 构建镜像
make push TAG=v0.0.1   # 发版并触发 GitHub Actions
```

前端源码在 `internal/embedded/web/`，改完直接 `make build` 重新内嵌即可（无需 Node 构建）。

测试需要 FFmpeg 与 `ffprobe`；`go test ./...` 会跑真实转码用例。

### 工程结构

```
.github/workflows/
├── ci.yml          常规检查（tag 触发）：单元测试 + FFmpeg 自检 + 镜像冒烟
└── release.yml     发布流水线（workflow_dispatch）：build-binaries → publish-release → build-docker → sync-cnb

scripts/
├── install.sh      系统级安装（systemd / 后台运行）
├── uninstall.sh    系统级卸载
├── build-release.sh 本地构建发布产物（dist/）
└── build-and-push.sh 发版（bump 版本 → 打 tag → 触发 release.yml）
```

## 许可

见 [LICENSE](LICENSE)。
