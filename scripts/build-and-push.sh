#!/bin/bash
#
# fan-image-tr - 发布脚本（推送 tag 触发 GitHub Actions 自动构建与发版）
# 不在本地编译任何产物：仅同步版本号、推送代码并打 v 开头 tag。
# 推送后由 .github/workflows/ci.yml 的 release job 交叉编译并创建 Release：
#   test    -> gofmt / go vet / 单元测试 / 竞态检测 / 编译 / FFmpeg 能力自检
#   docker  -> buildx 构建镜像 + 冒烟测试（健康检查）
#   release -> 交叉编译 linux/darwin/windows amd64+arm64 并上传 Release 附件
#
# Usage:
#   TAG(必填) 形如 v0.0.1; --yes 免交互; -m "备注" 可选发版说明
#     bash scripts/build-and-push.sh v0.0.1 --yes -m "第一个测试版"
set -euo pipefail

info() { echo -e "${gl_lv}>>> $*${reset}"; }
warn() { echo -e "${gl_huang}!!! $*${reset}"; }
error() { echo -e "${gl_hong}ERROR: $*${reset}"; exit 1; }

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

# 拉取本次 tag 触发的 workflow run：tag push 后 Actions 尚未注册新 run，
# 直接 --limit 1 取最新会取到上一次的陈旧记录。
# 这里按 tag 过滤并轮询等待，且用 headSha 校验确实是本次 push 的 run。
get_gh_run_info() {
    local tag="$1"
    local expect_sha
    expect_sha=$(git rev-parse HEAD 2>/dev/null) || expect_sha=""

    local tries=0
    local max_tries=24
    local run_json=""
    local sha=""
    while (( tries < max_tries )); do
        run_json=$(gh run list --workflow=ci.yml --limit 1 --branch "${tag}" \
            --json status,conclusion,displayTitle,headBranch,event,databaseId,startedAt,headSha 2>/dev/null) || run_json=""
        if [[ -n "$run_json" && "$run_json" != "[]" ]]; then
            sha=$(echo "$run_json" | jq -r '.[0].headSha')
            if [[ -z "$expect_sha" || "$sha" == "$expect_sha" ]]; then
                echo "$run_json"
                return 0
            fi
        fi
        tries=$((tries + 1))
        if (( tries < max_tries )); then
            sleep 5
        fi
    done
    return 1
}

calc_elapsed() {
    local start_iso="$1"
    local start_ts
    start_ts=$(date -d "${start_iso}" +%s 2>/dev/null)
    if [[ -z "${start_ts}" ]]; then
        echo "时间解析失败"
        return
    fi
    local now_ts=$(date +%s)
    local diff=$(( now_ts - start_ts ))

    if (( diff < 60 )); then
        echo "${diff} 秒"
    elif (( diff < 3600 )); then
        echo "$((diff / 60)) 分 $((diff % 60)) 秒"
    else
        echo "$((diff / 3600)) 时 $(((diff % 3600) / 60)) 分"
    fi
}

beautify_gh_run() {
    local tag="${1:-}"
    echo -e ""
    echo -e "${gl_zi}>>> GitHub Actions 流水线信息${gl_bai}"
    echo -e "${gl_bufan}————————————————————————————————————————————————${gl_bai}"

    local json_data
    json_data=$(get_gh_run_info "${tag}") || true
    if [[ -z "$json_data" || "$json_data" == "[]" ]]; then
        echo -e "${gl_hong}[错误] 未获取到本次(${tag})流水线运行记录${reset}"
        echo -e "${gl_bai}可能原因：GitHub Actions 尚未注册该 run，可稍后用以下命令手动查看：${reset}"
        echo -e "${gl_lv}gh run list --workflow=ci.yml --branch ${tag}${reset}"
        echo -e "${gl_bufan}————————————————————————————————————————————————${gl_bai}"
        return 1
    fi

    local status title branch event run_id started_at
    status=$(echo "$json_data" | jq -r '.[0].status')
    title=$(echo "$json_data" | jq -r '.[0].displayTitle')
    branch=$(echo "$json_data" | jq -r '.[0].headBranch')
    event=$(echo "$json_data" | jq -r '.[0].event')
    run_id=$(echo "$json_data" | jq -r '.[0].databaseId')
    started_at=$(echo "$json_data" | jq -r '.[0].startedAt')

    local elapsed status_text
    elapsed=$(calc_elapsed "$started_at")

    case "$status" in
        in_progress)
            status_text="${gl_huang}运行中${reset}"
            ;;
        completed)
            local conclusion
            conclusion=$(echo "$json_data" | jq -r '.[0].conclusion // ""')
            case "$conclusion" in
                success) status_text="${gl_lv}成功${reset}" ;;
                failure) status_text="${gl_hong}失败${reset}" ;;
                cancelled) status_text="${gl_hui}已取消${reset}" ;;
                skipped) status_text="${gl_huang}已跳过${reset}" ;;
                *) status_text="${gl_huang}已完成(${conclusion})${reset}" ;;
            esac
            ;;
        *)
            status_text="${gl_hui}${status}${reset}"
            ;;
    esac

    printf "%-14s%s\n" "${gl_hui}[运行状态]：${reset}" "$status_text"
    printf "%-14s%s\n" "${gl_hui}[提交标题]：${reset}" "${gl_huang}$title${reset}"
    printf "%-14s%s\n" "${gl_hui}[触发分支]：${reset}" "${gl_lan}$branch${reset}"
    printf "%-14s%s\n" "${gl_hui}[触发事件]：${reset}" "${gl_bai}$event${reset}"
    printf "%-14s%s\n" "${gl_hui}[Run ID]：${reset}" "${gl_bufan}$run_id${reset}"
    printf "%-14s%s\n" "${gl_hui}[已耗时]：${reset}" "${gl_bai}$elapsed${reset}"

    echo -e "${gl_bufan}————————————————————————————————————————————————${gl_bai}"
    echo -e ""
    echo -e "${gl_huang}>>> 快捷操作命令${gl_bai}"
    echo -e "${gl_bufan}————————————————————————————————————————————————${gl_bai}"
    echo -e "${gl_lv}实时跟踪流水线：${reset}gh run watch $run_id"
    echo -e "${gl_lv}查看详细信息：${reset}gh run view $run_id"
    echo -e "${gl_lv}查看完整日志：${reset}gh run view $run_id --log"
    echo -e "${gl_lv}查看失败日志：${reset}gh run view $run_id --log-failed"
    echo -e "${gl_lv}取消本次构建：${reset}gh run cancel $run_id"
    echo -e "${gl_lv}重新运行流水线：${reset}gh run rerun $run_id"
    echo -e "${gl_bufan}————————————————————————————————————————————————${gl_bai}"
    return 0
}

YES_MODE=0
TAG=""
MSG=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --yes) YES_MODE=1; shift ;;
        -m|--message)
            shift
            [ -n "${1:-}" ] || error "缺少 -m/--message 的备注内容"
            MSG="$1"
            shift
            ;;
        *) TAG="$1"; shift ;;
    esac
done

[ -z "${TAG}" ] && error "缺少 TAG 参数，用法: bash scripts/build-and-push.sh v0.0.1 --yes -m \"备注\""
TARGET_VER="${TAG#v}"
VER_RE='^v[0-9]+\.[0-9]+\.[0-9]+$'
[[ "${TAG}" =~ ${VER_RE} ]] || error "TAG 必须形如 vX.Y.Z（当前: ${TAG}）"

cd "$(dirname "$0")/.."

command -v git >/dev/null 2>&1 || error "未安装 git"
command -v python3 >/dev/null 2>&1 || error "未安装 python3（用于改写版本号）"

if [ "${YES_MODE}" = "0" ]; then
    read -r -p "即将发布 ${TAG}，将更新 internal/version/version.go 版本号并推送 main + tag，确认? [y/N] " ans
    [[ "${ans}" =~ ^[Yy]$ ]] || { echo "已取消"; exit 1; }
fi

if [[ "$(git status --porcelain)" =~ .[MADR] ]]; then
    warn "工作区存在未提交改动:"
    git status --short
    error "请先提交或 stash 后再发布"
fi

# ===================== 同步远端 =====================
info "fetch 远端状态"
git fetch origin
LOCAL_HASH=$(git rev-parse @)
REMOTE_HASH=$(git rev-parse @{u} 2>/dev/null || echo "")
if [ -n "${REMOTE_HASH}" ] && [ "${LOCAL_HASH}" != "${REMOTE_HASH}" ]; then
    # 本地领先远端是正常的（待推送）；只有远端领先或分叉才需要先 pull
    BEHIND=$(git rev-list --count "HEAD..@{u}" 2>/dev/null || echo 0)
    AHEAD=$(git rev-list --count "@{u}..HEAD" 2>/dev/null || echo 0)
    if [ "${BEHIND}" -gt 0 ]; then
        error "远端有 ${BEHIND} 个本地没有的提交（本地领先 ${AHEAD}），请先 git pull --rebase 后再发布"
    fi
    info "本地领先远端 ${AHEAD} 个提交，发布时一并推送"
fi

# ===================== 更新 internal/version/version.go 版本号 =====================
info "更新版本号 -> ${TAG}"
python3 - "${TARGET_VER}" <<'PY'
import re, sys
want = sys.argv[1]
p = 'internal/version/version.go'
s = open(p, encoding='utf-8').read()
new, n = re.subn(r'^(var Version = ")[^"]*(")',
                 lambda m: m.group(1) + want + m.group(2), s, count=1, flags=re.M)
if n == 0:
    print('ERROR: internal/version/version.go 中未找到 var Version 声明'); sys.exit(1)
open(p, 'w', encoding='utf-8').write(new)
print('internal/version/version.go Version -> ' + want)
PY

git diff --stat

# ===================== 写发版备注 =====================
info "写入发版备注 RELEASE_NOTES.md"
{
  if [ -n "${MSG}" ]; then
    printf '# fan-image-tr %s\n\n' "${TAG}"
    printf '%s\n' "${MSG}"
    printf '\n'
  fi
  printf '## 下载\n'
  printf '```bash\n'
  printf '# 二进制（linux amd64 / arm64）\n'
  printf 'curl -fsSL -o fan-image-tr "https://github.com/meimolihan/fan-image-tr/releases/latest/download/fan-image-tr-linux-%s"\n' "$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')"
  printf 'chmod +x fan-image-tr\n'
  printf '```\n'
  printf '\n'
  printf '## 变更\n'
  git log --pretty=format:'- %h %s' "${REMOTE_HASH:-HEAD}"..HEAD 2>/dev/null || true
  printf '\n'
} > RELEASE_NOTES.md

# ===================== Git 提交 & Tag =====================
info "提交版本变更"
git add internal/version/version.go RELEASE_NOTES.md
git commit -q -m "chore: bump version to ${TAG}" || warn "无变更可提交？"
info "推送 main"
git push origin main
if git rev-parse -q --verify "refs/tags/${TAG}" >/dev/null; then
    if [ "${YES_MODE}" = "0" ]; then
        read -r -p "远端已存在 tag ${TAG}，删除并重建? [y/N] " ans
        [[ "${ans}" =~ ^[Yy]$ ]] || error "已取消（tag ${TAG} 已存在）"
    fi
    warn "tag ${TAG} 已存在，删除并重建"
    git tag -d "${TAG}"
    git push origin ":refs/tags/${TAG}" --force
fi
info "打 tag ${TAG}"
git tag -a "${TAG}" -m "${TAG}"
info "推送 tag ${TAG}（触发 ci.yml: test -> docker -> release）"
git push origin "refs/tags/${TAG}"

# ===================== 跟踪 GitHub Actions 流水线 =====================
info "等待 GitHub Actions 注册本次流水线 ..."
if ! command -v gh >/dev/null 2>&1; then
    warn "未安装 gh CLI，无法自动跟踪流水线。"
    warn "请手动查看: https://github.com/meimolihan/fan-image-tr/actions"
    exit 0
fi

if ! beautify_gh_run "${TAG}"; then
    warn "未能自动获取流水线信息，但 tag ${TAG} 已推送成功。"
    warn "请手动查看: gh run list --workflow=ci.yml --branch ${TAG}"
    exit 0
fi

info "查看发布结果: gh release view ${TAG}"
echo -e "${gl_bai}远程安装命令（systemd）：${gl_lv}bash <(curl -sL gitee.com/meimolihan/cmdbox/raw/master/sh/fan-image-tr_menu.sh)${gl_bai}"
echo -e "${gl_bai}远程安装命令（Compose）：${gl_lv}bash <(curl -sL gitee.com/meimolihan/cmdbox/raw/master/sh/dc_inst_fan-image-tr.sh) 8791 /vol1/1000/compose/fan-image-tr${gl_bai}"
