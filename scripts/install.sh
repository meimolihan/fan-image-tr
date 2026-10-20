#!/usr/bin/env bash
#
# fan-image-tr - 图片处理服务 安装脚本
# 将构建产物或 Release 二进制安装为 systemd 服务（无 systemd 环境回退后台运行）。
# 可重复执行，升级等同于重新安装（覆盖二进制并按需重建配置并重启服务）。
#
# Usage:
#   交互式安装:
#     bash scripts/install.sh
#   参数静默安装:
#     bash scripts/install.sh -p 8791 -d /var/lib/fan-image-tr -s /volume1/photo -b ./fan-image-tr
#     bash scripts/install.sh -y
#   curl | bash 远程安装:
#     bash -c "$(curl -sSL https://raw.githubusercontent.com/meimolihan/fan-image-tr/main/scripts/install.sh)" -p 8791 -d /var/lib/fan-image-tr -s /volume1/photo

set -euo pipefail

# ================== terminal colors ==================
list_color_init() {
    export gl_hui=$'\033[38;5;59m'
    export gl_hong=$'\033[38;5;9m'
    export gl_lv=$'\033[38;5;10m'
    export gl_huang=$'\033[38;5;11m'
    export gl_lan=$'\033[38;5;32m'
    export gl_bai=$'\033[38;5;15m'
    export gl_zi=$'\033[38;5;13m'
    export gl_bufan=$'\033[38;5;14m'
    export reset=$'\033[0m'
}
list_color_init

sep_line() {
  printf '%s' "$gl_bufan"
  printf '—%.0s' {1..32}
  printf '%s\n' "$reset"
}

section() {
  printf "  %s %s\n" "${gl_zi}▶${reset}" "$1"
}

ok() {
  printf "  %s %s\n" "${gl_lv}>>>${reset}" "$1"
}

skip() {
  printf "  %s %s\n" "${gl_hui}--${reset}" "$1"
}

error() { printf "  %s %s\n" "${gl_hong}[错误]${reset}" "$1" >&2; exit 1; }

# ================== customize me ==================
APP_NAME="fan-image-tr"
DEFAULT_PORT=8791
DEFAULT_INSTALL_DIR="/var/lib/fan-image-tr"
BIN_PATH=""
DATA_DIR=""
MEDIA_DIR=""
RECORD_FILE="/etc/fan-image-tr.conf"
SERVICE_FILE="/etc/systemd/system/fan-image-tr.service"
WRAPPER_FILE="/usr/local/bin/${APP_NAME}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-}")" && pwd)"
DEFAULT_BIN_SRC="${SCRIPT_DIR}/../fan-image-tr"

# ================== GitHub 下载加速镜像 ==================
# 原始 GitHub 地址超时/失败时，按下列顺序依次尝试（末尾必须带斜杠）
GITHUB_MIRRORS=(
  "https://ghfast.top/"
  "https://ghproxy.net/"
  "https://gh.xxooo.cf/"
  "https://githubproxy.cc/"
)

make_url_candidates() {
  local github_url="$1" p
  printf '%s\n' "${github_url}"
  for p in "${GITHUB_MIRRORS[@]}"; do
    printf '%s\n' "${p}${github_url}"
  done
}

# 下载单个文件：候选按序尝试，单链接单次 120s 超时后换源。
# 用法: download_file <URL> <输出文件> [期望魔数hex]
download_file() {
  local url="$1" dst="$2" want="${3:-}" u="" hex=""
  while IFS= read -r u; do
    rm -f "${dst}"
    if command -v curl >/dev/null 2>&1; then
      timeout 120 curl -fsSL --connect-timeout 10 --max-time 120 -o "${dst}" "${u}" 2>/dev/null || continue
    elif command -v wget >/dev/null 2>&1; then
      wget -qO "${dst}" --timeout=120 --tries=1 "${u}" 2>/dev/null || continue
    else
      return 1
    fi
    [ -s "${dst}" ] || continue
    if [ -n "${want}" ]; then
      hex="$(head -c 4 "${dst}" | od -An -tx1 | tr -d ' \n')"
      case "${hex}" in
        "${want}"*) ;;
        *) printf "  %s\n" "${gl_huang}[警告]${reset} 内容非预期(${u})，换源重试。" >&2; continue ;;
      esac
    fi
    return 0
  done < <(make_url_candidates "${url}")
  return 1
}

# 经 curl|bash 远程执行时，SCRIPT_DIR 指向临时目录，本地产物需按常见目录回退探测
resolve_local_src() {
  local candidates=(
    "${SCRIPT_DIR:-}/fan-image-tr"
    "${SCRIPT_DIR:-}/../fan-image-tr"
    "$(pwd)/fan-image-tr"
    "$(pwd)/../fan-image-tr"
  )
  for c in "${candidates[@]}"; do
    if [ -e "${c}" ]; then
      printf '%s' "${c}"
      return 0
    fi
  done
  return 1
}

# ================== 参数解析 ==================
PORT=""
INSTALL_DIR=""
BIN_SRC=""
BIN_SRC_EXPLICIT=0
INSTALL_YES=0

# ---- bootstrap: support `bash -c "$(curl ...)" -p ... -d ... -s ...` ----
case "$0" in
  -*) set -- "$0" "$@" ;;
esac

while [ "$#" -gt 0 ]; do
  case "$1" in
    -p|--port)
      shift; [ -n "${1:-}" ] || error "缺少 -p/--port 的值"
      PORT="$1" ;;
    -d|--dir)
      shift; [ -n "${1:-}" ] || error "缺少 -d/--dir 的值"
      INSTALL_DIR="$1" ;;
    -s|--media)
      shift; [ -n "${1:-}" ] || error "缺少 -s/--media 的值"
      MEDIA_DIR="$1" ;;
    -b|--bin)
      shift; [ -n "${1:-}" ] || error "缺少 -b/--bin 的值"
      BIN_SRC="$1"; BIN_SRC_EXPLICIT=1 ;;
    -y|--yes)
      INSTALL_YES=1 ;;
    -h|--help)
      printf "%s\n" "${gl_lan}fan-image-tr${reset} - ${gl_bai}图片处理工具 安装脚本${reset}"
      printf "  %-13s %s\n" "${gl_bai}用法:${reset}" "bash scripts/install.sh [-p PORT] [-d INSTALL_DIR] [-s MEDIA_DIR] [-b BIN] [-y]"
      printf "  %-13s %s\n" "${gl_bai}-p, --port${reset}" "监听端口（默认 ${gl_lan}${DEFAULT_PORT}${reset}）"
      printf "  %-13s %s\n" "${gl_bai}-d, --dir${reset}" "安装目录（二进制+数据都装于此，默认 ${gl_lan}${DEFAULT_INSTALL_DIR}${reset}）"
      printf "  %-13s %s\n" "${gl_bai}-s, --media${reset}" "图片浏览根目录（可选；指定后浏览器默认从该目录浏览，留空为当前工作目录）"
      printf "  %-13s %s\n" "${gl_bai}-b, --bin${reset}" "二进制源路径（默认 ${gl_lan}${DEFAULT_BIN_SRC}${reset} 或仓库根目录 fan-image-tr）"
      printf "  %-13s %s\n" "${gl_bai}-y, --yes${reset}" "免交互，未指定项全部使用默认值"
      printf "  %-13s %s\n" "${gl_bai}-h, --help${reset}" "显示本帮助"
      printf "%s\n" "${gl_hui}未指定 -b 且本地无构建产物时，自动从 GitHub Release 下载对应架构二进制。${reset}"
      printf "%s\n" "${gl_hui}依赖检查：本服务内置 FFmpeg 调用，若系统未安装 ffmpeg/ffprobe 将自动尝试安装。${reset}"
      exit 0 ;;
    *)
      error "未知参数: $1（使用 -h 查看帮助）" ;;
  esac
  shift
done

# ---- read previous install record to prefill defaults (reinstall/upgrade) ----
read_record() {
  [ -f "${RECORD_FILE}" ] || return 0
  while IFS='=' read -r KEY VALUE; do
    KEY=$(printf '%s' "$KEY" | tr -d ' ')
    VALUE=$(printf '%s' "$VALUE" | tr -d '\r')
    case "$KEY" in
      INSTALL_DIR) [ -n "$VALUE" ] && [ -z "$INSTALL_DIR" ] && INSTALL_DIR="$VALUE" ;;
      PORT) [ -n "$VALUE" ] && [ -z "$PORT" ] && PORT="$VALUE" ;;
      DATA_DIR) [ -n "$VALUE" ] && [ -z "$DATA_DIR" ] && DATA_DIR="$VALUE" ;;
      MEDIA_DIR) [ -n "$VALUE" ] && [ -z "$MEDIA_DIR" ] && MEDIA_DIR="$VALUE" ;;
    esac
  done < "${RECORD_FILE}"
  return 0
}

[ "$(id -u)" != "0" ] && error "请以 root 身份运行（例如 sudo bash scripts/install.sh）"

read_record
INSTALL_DIR="${INSTALL_DIR:-$DEFAULT_INSTALL_DIR}"
DATA_DIR="${DATA_DIR:-${INSTALL_DIR}/data}"

# ---- silent install detection ----
SILENT="n"
[ -n "${PORT}" ] && SILENT="y"
[ -n "${INSTALL_DIR}" ] && SILENT="y"
[ -n "${MEDIA_DIR}" ] && SILENT="y"
[ -n "${BIN_SRC}" ] && SILENT="y"
[ "$INSTALL_YES" = "1" ] && SILENT="y"
[ ! -t 0 ] && SILENT="y"

section "配置参数"
if [ -z "${PORT}" ]; then
  if [ "$SILENT" = "y" ]; then
    PORT="${DEFAULT_PORT}"
  else
    while :; do
      read -r -p "${gl_bai}请输入监听端口${reset} ${gl_hui}[默认: ${DEFAULT_PORT}]${reset}: " PORT
      PORT="${PORT:-$DEFAULT_PORT}"
      case "$PORT" in
        ''|*[!0-9]*) printf "  %s\n" "${gl_huang}端口无效，请重新输入。${reset}" ;;
        *)
          [ "$PORT" -ge 1 ] && [ "$PORT" -le 65535 ] && break
          printf "  %s\n" "${gl_huang}端口超出范围（1‑65535），请重新输入。${reset}" ;;
      esac
    done
  fi
fi
PORT="${PORT:-$DEFAULT_PORT}"

if [ -z "${MEDIA_DIR}" ] && [ "$SILENT" != "y" ]; then
  read -r -p "${gl_bai}请输入图片浏览根目录${reset} ${gl_hui}[可选，留空浏览当前工作目录: ]${reset}: " MEDIA_DIR
  MEDIA_DIR="${MEDIA_DIR// /}"
fi

MEDIA_ARGS=""
[ -n "${MEDIA_DIR}" ] && MEDIA_ARGS="-media ${MEDIA_DIR}"

# ------------------ 1. 依赖检查：ffmpeg / ffprobe ------------------
ensure_ffmpeg() {
  if command -v ffmpeg >/dev/null 2>&1 && command -v ffprobe >/dev/null 2>&1; then
    ok "已检测到 ffmpeg / ffprobe（$(ffmpeg -version 2>/dev/null | head -1 | awk '{print $3}')）"
    return 0
  fi

  printf "  %s\n" "${gl_huang}[提示]${reset} 未检测到 ffmpeg / ffprobe，尝试自动安装 ..."
  if command -v apt-get >/dev/null 2>&1; then
    apt-get update -y >/dev/null 2>&1 || true
    DEBIAN_FRONTEND=noninteractive apt-get install -y ffmpeg >/dev/null 2>&1 || { error "apt-get 安装 ffmpeg 失败，请手动安装: apt install ffmpeg"; }
  elif command -v apk >/dev/null 2>&1; then
    apk add --no-cache ffmpeg >/dev/null 2>&1 || { error "apk 安装 ffmpeg 失败，请手动安装: apk add ffmpeg"; }
  elif command -v yum >/dev/null 2>&1; then
    yum install -y ffmpeg >/dev/null 2>&1 || { error "yum 安装 ffmpeg 失败，请手动安装: yum install ffmpeg"; }
  else
    error "无法自动安装 ffmpeg。本服务图片转换依赖 ffmpeg + ffprobe，请先手动安装（Debian/Ubuntu: apt install ffmpeg ｜ Alpine: apk add ffmpeg ｜ CentOS: yum install ffmpeg）"
  fi
  command -v ffmpeg >/dev/null 2>&1 && command -v ffprobe >/dev/null 2>&1 || error "ffmpeg 安装后仍不可用，请手动检查"
  ok "ffmpeg / ffprobe 安装完成"
}

# ------------------ 2. 二进制来源 ------------------
BIN_SRC="${BIN_SRC:-$DEFAULT_BIN_SRC}"
if [ ! -f "${BIN_SRC}" ]; then
  if [ "${BIN_SRC_EXPLICIT}" != "1" ]; then
    DISCOVERED_BIN="$(resolve_local_src)" || true
    if [ -n "${DISCOVERED_BIN:-}" ]; then
      ok "已发现本地产物 ${gl_bai}${DISCOVERED_BIN}${reset}"
      BIN_SRC="${DISCOVERED_BIN}"
    fi
  fi
fi
if [ ! -f "${BIN_SRC}" ]; then
  if [ "${BIN_SRC_EXPLICIT}" = "1" ]; then
    error "未找到二进制文件 ${BIN_SRC}（-b 显式指定）"
  fi
  REL_ARCH=""
  case "$(uname -m)" in
    x86_64|amd64) REL_ARCH="amd64" ;;
    aarch64|arm64) REL_ARCH="arm64" ;;
    *) error "不支持的架构: $(uname -m)，请先本地构建或使用 -b 指定" ;;
  esac
  REL_URL="https://github.com/meimolihan/fan-image-tr/releases/latest/download/fan-image-tr_linux_${REL_ARCH}"
  ok "本地无构建产物，尝试从 GitHub Release 下载 ${gl_bai}${REL_URL}${reset}"
  TMP_BIN="$(mktemp)"
  if ! download_file "${REL_URL}" "${TMP_BIN}" "7f454c46"; then
    error "下载 Release 二进制失败（${REL_URL}），请先本地构建或使用 -b 指定"
  fi
  chmod +x "${TMP_BIN}"
  BIN_SRC="${TMP_BIN}"
  ok "已从 GitHub Release 下载二进制（${gl_bai}$(du -h "${TMP_BIN}" | cut -f1)${reset}）"
fi

if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  USE_SYSTEMD="y"
else
  USE_SYSTEMD="n"
  printf "  %s\n" "${gl_huang}[警告]${reset} 未检测到 systemd（容器或受限环境），回退为后台运行模式。${reset}"
fi

# ------------------ 3. 安装程序 ------------------
sep_line
section "安装程序"

ensure_ffmpeg

BIN_PATH="${INSTALL_DIR}/fan-image-tr"
DATA_DIR="${DATA_DIR:-${INSTALL_DIR}/data}"

ok "创建安装目录 ${gl_lan}${INSTALL_DIR}${reset}"
mkdir -p "${INSTALL_DIR}"

ok "安装二进制 ${gl_bai}${BIN_PATH}${reset}"
cp -f "${BIN_SRC}" "${BIN_PATH}"
chmod +x "${BIN_PATH}"
ok "已安装二进制至 ${gl_bai}${BIN_PATH}${reset}"

ok "创建命令行入口 ${gl_bai}${WRAPPER_FILE}${reset}"
mkdir -p "$(dirname "${WRAPPER_FILE}")"
ln -sfn "${BIN_PATH}" "${WRAPPER_FILE}"

ok "创建数据目录 ${gl_lan}${DATA_DIR}${reset}"
mkdir -p "${DATA_DIR}"
chmod 755 "${DATA_DIR}"

# 转换输出 / 上传 子目录：与应用内 OutputDir()/UploadDir() 一致（默认均位于数据目录下）
ok "创建转换输出目录 ${gl_lan}${DATA_DIR}/output${reset}"
mkdir -p "${DATA_DIR}/output"
chmod 755 "${DATA_DIR}/output"
ok "创建上传目录 ${gl_lan}${DATA_DIR}/uploads${reset}"
mkdir -p "${DATA_DIR}/uploads"
chmod 755 "${DATA_DIR}/uploads"

# ---- write install record ----
mkdir -p "$(dirname "${RECORD_FILE}")"
cat > "${RECORD_FILE}" <<EOF
# ${APP_NAME} 安装记录（由 install.sh 生成，请勿手动修改）
INSTALL_DIR=${INSTALL_DIR}
BIN_PATH=${BIN_PATH}
PORT=${PORT}
DATA_DIR=${DATA_DIR}
MEDIA_DIR=${MEDIA_DIR}
EOF
chmod 0644 "${RECORD_FILE}"
ok "已写入安装记录 ${gl_bai}${RECORD_FILE}${reset}"

# ---- systemd unit ----
sep_line
section "启动服务"
if [ "${USE_SYSTEMD}" = "y" ]; then
  cat > "${SERVICE_FILE}" <<UNIT
[Unit]
Description=fan-image-tr - 图片处理工具 (FFmpeg 图片转换 + 格式转换)
After=network-online.target local-fs.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${BIN_PATH} -port ${PORT} -data ${DATA_DIR} ${MEDIA_ARGS}
WorkingDirectory=${DATA_DIR}
Environment=TZ=Asia/Shanghai
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
UNIT

  systemctl daemon-reload
  systemctl enable "${APP_NAME}" >/dev/null 2>&1 || true
  systemctl restart "${APP_NAME}"
  sleep 2
  if systemctl is-active "${APP_NAME}" >/dev/null 2>&1; then
    ok "${gl_bai}${APP_NAME}${reset} 服务已启动。"
    systemctl status "${APP_NAME}" --no-pager || true
  else
    printf "  %s\n" "${gl_hong}[错误]${reset} 服务启动失败，请检查：${gl_bai}journalctl -u ${APP_NAME} -n 50${reset}" >&2
    exit 1
  fi
else
  if command -v pgrep >/dev/null 2>&1 && pgrep -x "${APP_NAME}" >/dev/null 2>&1; then
    printf "  %s\n" "${gl_huang}[警告]${reset} 检测到 ${APP_NAME} 进程可能已在运行，跳过拉起。${reset}"
  else
    nohup "${BIN_PATH}" -port "${PORT}" -data "${DATA_DIR}" ${MEDIA_ARGS} >> "${DATA_DIR}/${APP_NAME}.log" 2>&1 &
    ok "${APP_NAME} 已在后台启动，pid: ${gl_bai}$!${reset}"
  fi
fi

IP=$(hostname -I 2>/dev/null | awk '{print $1}')
[ -z "${IP}" ] && IP="<服务器IP>"

sep_line
printf "  %s\n" "${gl_lv}✔ ${APP_NAME} 安装成功！${reset}"
printf "  %-14s %s\n" "${gl_lan}访问地址${reset}" "${gl_bai}http://${IP}:${PORT}${reset}"
printf "  %-14s %s\n" "${gl_lan}安装目录${reset}" "${gl_bai}${INSTALL_DIR}${reset}"
printf "  %-14s %s\n" "${gl_lan}数据目录${reset}" "${gl_bai}${DATA_DIR}${reset}"
if [ -n "${MEDIA_DIR}" ]; then
  printf "  %-14s %s\n" "${gl_lan}图片目录${reset}" "${gl_bai}${MEDIA_DIR}${reset}"
fi
printf "  %-14s %s\n" "${gl_lan}配置文件${reset}" "${gl_bai}${RECORD_FILE}${reset}"
if [ "${USE_SYSTEMD}" = "y" ]; then
  printf "  %s\n" "  ${gl_hui}常用命令：systemctl status/restart/stop ${APP_NAME} ｜ journalctl -u ${APP_NAME} -f${reset}"
else
  printf "  %s\n" "  ${gl_huang}注意：${reset}后台运行模式在系统重启后不会自动恢复。"
fi
printf "  %s\n" "  ${gl_hui}管理命令：${APP_NAME} status/restart/uninstall ｜ ${APP_NAME} help 查看全部${reset}"
sep_line
