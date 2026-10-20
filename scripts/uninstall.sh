#!/usr/bin/env bash
#
# fan-image-tr - 图片处理工具 卸载脚本
# 停止服务、移除 systemd 单元、二进制与安装记录。
# 数据目录默认保留，-y/--purge 或交互确认后才会清除。
#
# Usage:
#   fan-image-tr 自带子命令:  fan-image-tr uninstall
#   本脚本(移除系统级安装):   bash scripts/uninstall.sh [-y] [--purge]
set -euo pipefail

# ================== terminal colors ==================
list_color_init() {
    export gl_hui=$'\033[38;5;59m'
    export gl_hong=$'\033[38;5;9m'
    export gl_lv=$'\033[38;5;10m'
    export gl_huang=$'\033[38;5;11m'
    export gl_lan=$'\033[38;5;32m'
    export gl_bai=$'\033[38;5;15m'
    export reset=$'\033[0m'
}
list_color_init

error() { printf "  %s %s\n" "${gl_hong}[错误]${reset}" "$1" >&2; exit 1; }
ok()    { printf "  %s %s\n" "${gl_lv}>>>${reset}" "$1"; }
skip()  { printf "  %s %s\n" "${gl_hui}--${reset}" "$1"; }

APP_NAME="fan-image-tr"
BIN_PATH=""
DATA_DIR=""
MEDIA_DIR=""
INSTALL_DIR=""
RECORD_FILE="/etc/fan-image-tr.conf"
SERVICE_FILE="/etc/systemd/system/fan-image-tr.service"
WRAPPER_FILE="/usr/local/bin/${APP_NAME}"
DEFAULT_INSTALL_DIR="/var/lib/fan-image-tr"
DEFAULT_DATA_DIR="/var/lib/fan-image-tr/data"
PID_FILE="/var/run/fan-image-tr.pid"

PURGE=0
YES=0
NOOP=0
for arg in "$@"; do
  case "$arg" in
    -y|--yes) YES=1 ;;
    --purge|--delete-data) PURGE=1 ;;
    --keep-data) NOOP=1 ;;
    -h|--help)
      printf "%s\n" "${gl_lan}fan-image-tr${reset} 卸载脚本"
      printf "  %-14s %s\n" "-y, --yes" "免交互（未指定清除数据目录）"
      printf "  %-14s %s\n" "--purge" "同时清除数据目录（不可恢复）"
      printf "  %-14s %s\n" "--keep-data" "卸载时保留数据目录"
      exit 0 ;;
    *) error "未知参数: $arg（-h 查看帮助）" ;;
  esac
done

if [ "${PURGE}" = "1" ] && [ "${NOOP}" = "1" ]; then
  error "--purge 与 --keep-data 不能同时使用"
fi

DATA_DIR="${DEFAULT_DATA_DIR}"
INSTALL_DIR="${DEFAULT_INSTALL_DIR}"
if [ -f "${RECORD_FILE}" ]; then
  while IFS='=' read -r KEY VALUE; do
    KEY=$(printf '%s' "$KEY" | tr -d ' ')
    VALUE=$(printf '%s' "$VALUE" | tr -d '\r')
    case "$KEY" in
      INSTALL_DIR) [ -n "$VALUE" ] && INSTALL_DIR="$VALUE" ;;
      BIN_PATH) [ -n "$VALUE" ] && BIN_PATH="$VALUE" ;;
      DATA_DIR) [ -n "$VALUE" ] && DATA_DIR="$VALUE" ;;
      MEDIA_DIR) [ -n "$VALUE" ] && MEDIA_DIR="$VALUE" ;;
    esac
  done < "${RECORD_FILE}"
fi
[ -z "${BIN_PATH}" ] && BIN_PATH="${INSTALL_DIR}/fan-image-tr"

printf "正在卸载 ${gl_bai}${APP_NAME}${reset} ...\n"

# 1. 停止服务
if command -v systemctl >/dev/null 2>&1; then
  systemctl stop "${APP_NAME}.service" >/dev/null 2>&1 || true
  systemctl disable "${APP_NAME}.service" >/dev/null 2>&1 || true
  rm -f "${SERVICE_FILE}"
  systemctl daemon-reload >/dev/null 2>&1 || true
  ok "已停止并移除 systemd 服务"
else
  if [ -f "${PID_FILE}" ]; then
    kill "$(cat "${PID_FILE}")" >/dev/null 2>&1 || true
    rm -f "${PID_FILE}"
    ok "已停止残留进程"
  else
    pkill -x "${APP_NAME}" >/dev/null 2>&1 || true
    ok "已尝试终止 ${APP_NAME} 进程"
  fi
fi

# 2. 移除二进制
if [ -f "${BIN_PATH}" ]; then
  rm -f "${BIN_PATH}"
  ok "已移除二进制 ${gl_bai}${BIN_PATH}${reset}"
else
  skip "未发现二进制 ${BIN_PATH}"
fi

# 2b. 移除命令行入口软链
if [ -L "${WRAPPER_FILE}" ]; then
  rm -f "${WRAPPER_FILE}"
  ok "已移除命令行入口 ${gl_bai}${WRAPPER_FILE}${reset}"
else
  skip "未发现命令行入口 ${WRAPPER_FILE}"
fi

# 3. 移除安装记录
rm -f "${RECORD_FILE}"
ok "已移除安装记录 ${gl_bai}${RECORD_FILE}${reset}"

# 4. 数据目录
if [ -d "${DATA_DIR}" ]; then
  if [ "${NOOP}" = "1" ]; then
    skip "数据目录已保留（--keep-data）: ${DATA_DIR}"
  elif [ "${PURGE}" = "1" ]; then
    rm -rf "${DATA_DIR}"
    ok "已清除数据目录 ${gl_bai}${DATA_DIR}${reset}"
  elif [ "${YES}" = "1" ]; then
    skip "数据目录已保留（指定 --purge 可清除）: ${DATA_DIR}"
  else
    printf "  %s\n" "${gl_huang}[确认]${reset} 是否清除数据目录 ${gl_bai}${DATA_DIR}${reset}？（此操作不可恢复）"
    read -r -p "  输入 y 清除，其余保留: " ans
    if [ "$ans" = "y" ] || [ "$ans" = "Y" ]; then
      rm -rf "${DATA_DIR}"
      ok "已清除数据目录"
    else
      skip "数据目录已保留: ${DATA_DIR}"
    fi
  fi
else
  skip "数据目录不存在: ${DATA_DIR}"
fi

# 5. 安装目录（若为空则顺手移除）
if [ -d "${INSTALL_DIR}" ] && [ "${INSTALL_DIR}" != "${DATA_DIR}" ]; then
  if [ "$PURGE" = "1" ] || [ -z "$(ls -A "${INSTALL_DIR}" 2>/dev/null || true)" ]; then
    rmdir "${INSTALL_DIR}" 2>/dev/null || true
    [ -d "${INSTALL_DIR}" ] && skip "安装目录非空，已保留: ${INSTALL_DIR}" || ok "已移除空安装目录 ${gl_bai}${INSTALL_DIR}${reset}"
  else
    skip "安装目录非空，已保留: ${INSTALL_DIR}"
  fi
fi

if [ -n "${MEDIA_DIR}" ]; then
  skip "图片目录已保留（未改动源目录）: ${MEDIA_DIR}"
fi

printf "  %s\n" "${gl_lv}✔ ${APP_NAME} 卸载完成。${reset}"
