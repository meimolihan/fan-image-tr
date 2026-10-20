#!/usr/bin/env bash
#
# fan-image-tr - 本地发布构建脚本
# 在本机构建自包含的 linux/amd64 与 linux/arm64 二进制（前端资源已内嵌），
# 生成 sha256 校验和，并把 install/uninstall 脚本、示例配置与发版说明一并归档到 dist/。
# 产物命名与 install.sh 的 Release 下载地址一致：dist/fan-image-tr_linux_amd64 / _arm64
#
# Usage:
#   bash scripts/build-release.sh            # 构建 amd64 + arm64
#   bash scripts/build-release.sh amd64      # 仅构建指定架构
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

VERSION="$(sed -n 's/^var Version = "\([0-9.]*\)"/\1/p' "${ROOT_DIR}/internal/version/version.go" | head -1)"
if [ -z "${VERSION}" ]; then
  echo "ERROR: 无法从 internal/version/version.go 读取版本号" >&2
  exit 1
fi

COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
PKG="github.com/meimolihan/fan-image-tr"
LD_FLAGS="-s -w -X ${PKG}/internal/version.Version=${VERSION} -X ${PKG}/internal/version.Commit=${COMMIT} -X ${PKG}/internal/version.BuildTime=${BUILD_TIME}"

# 架构选择：默认 amd64 + arm64，也可传入单个/多个架构
ARCHES=()
if [ "$#" -gt 0 ]; then
  ARCHES=("$@")
else
  ARCHES=(amd64 arm64)
fi

DIST="${ROOT_DIR}/dist"
rm -rf "${DIST}"
mkdir -p "${DIST}"

command -v go >/dev/null 2>&1 || { echo "ERROR: 未找到 go 工具链" >&2; exit 1; }
if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
  echo "ERROR: 未找到 sha256sum" >&2
  exit 1
fi

echo ">>> fan-image-tr 本地发布构建 ${VERSION}"

for arch in "${ARCHES[@]}"; do
  case "${arch}" in
    amd64|arm64) ;;
    *) echo "ERROR: 不支持的架构: ${arch}（支持 amd64 / arm64）" >&2; exit 1 ;;
  esac
  bin="${DIST}/fan-image-tr_linux_${arch}"
  echo "--- 构建 linux/${arch} -> ${bin} （version=${VERSION}）"
  (cd "${ROOT_DIR}" && CGO_ENABLED=0 GOOS=linux GOARCH="${arch}" \
    go build -trimpath -ldflags="${LD_FLAGS}" -o "${bin}" .)
done

# sha256 校验和
SHA_CMD="sha256sum"
command -v sha256sum >/dev/null 2>&1 || SHA_CMD="shasum -a 256"
( cd "${DIST}" && ${SHA_CMD} fan-image-tr_linux_* | tee sha256sums.txt )

# 归档附属文件
cp -f "${SCRIPT_DIR}/install.sh" "${DIST}/install.sh"
cp -f "${SCRIPT_DIR}/uninstall.sh" "${DIST}/uninstall.sh"
[ -f "${ROOT_DIR}/config.example.yaml" ] && cp -f "${ROOT_DIR}/config.example.yaml" "${DIST}/config.example.yaml"
[ -f "${ROOT_DIR}/RELEASE_NOTES.md" ] && cp -f "${ROOT_DIR}/RELEASE_NOTES.md" "${DIST}/RELEASE_NOTES.md"
chmod +x "${DIST}/install.sh" "${DIST}/uninstall.sh"

echo ">>> 发布产物已就绪："
ls -lh "${DIST}"
echo ">>> 生成 sha256：${DIST}/sha256sums.txt"
cat "${DIST}/sha256sums.txt"
