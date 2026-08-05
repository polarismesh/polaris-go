#!/bin/bash
# =============================================================================
# 配置灰度验证客户端节点清理脚本
#
# 使用方法:
#   ./clean.sh            # 默认: 展示后确认再清理
#   ./clean.sh -f         # 强制直接清理
#   ./clean.sh --dry-run  # 仅展示，不执行
#
# 清理内容: client.pid 进程(含 ps 兜底) + client.log + polaris/ SDK 日志目录
# =============================================================================

set -euo pipefail

FORCE=false
DRY_RUN=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        -f|--force)  FORCE=true;   shift ;;
        --dry-run)   DRY_RUN=true; shift ;;
        -h|--help)
            echo "用法: $0 [-f|--force] [--dry-run]"
            echo "  -f, --force   强制清理，不交互确认"
            echo "  --dry-run     仅展示待清理项，不执行"
            exit 0
            ;;
        *)
            echo "未知参数: $1"; exit 1 ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PIDFILE="${SCRIPT_DIR}/client.pid"
LOGFILE="${SCRIPT_DIR}/client.log"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# 待清理 PID 列表
declare -a PIDS=()
declare -A SEEN=()

# 第一层: 从 pidfile 读取
if [[ -f "$PIDFILE" ]]; then
    pid=$(cat "$PIDFILE" 2>/dev/null || true)
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null && [[ -z "${SEEN[$pid]:-}" ]]; then
        PIDS+=("$pid")
        SEEN[$pid]=1
    fi
fi

# 第二层: ps 兜底搜索本目录 x86-bin
while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    pid=$(echo "$line" | awk '{print $1}')
    if [[ -n "$pid" ]] && [[ -z "${SEEN[$pid]:-}" ]]; then
        PIDS+=("$pid")
        SEEN[$pid]=1
    fi
done < <(ps -eo pid,args | grep -F "${SCRIPT_DIR}/x86-bin" | grep -v grep || true)

# 待清理产物
declare -a FILES=()
[[ -f "$PIDFILE" ]] && FILES+=("client.pid")
[[ -f "$LOGFILE" ]] && FILES+=("client.log")
[[ -d "${SCRIPT_DIR}/polaris" ]] && FILES+=("polaris/")

echo ""
echo "待清理项:"
echo "  残留进程数:     ${#PIDS[@]}"
if [[ ${#PIDS[@]} -gt 0 ]]; then
    for pid in "${PIDS[@]}"; do
        ps -p "$pid" -o pid,etime,args 2>/dev/null | tail -1 | awk '{printf "    PID=%s 启动时长=%s\n", $1, $2}'
    done
fi
echo "  产物文件/目录:  ${FILES[*]:-无}"
echo ""

if [[ "$DRY_RUN" == "true" ]]; then
    echo -e "${YELLOW}[dry-run] 仅展示，未清理${NC}"
    exit 0
fi

if [[ "$FORCE" != "true" ]]; then
    read -r -p "确认清理以上进程与产物? [y/N] " ans
    [[ "$ans" =~ ^[Yy]$ ]] || { echo "已取消"; exit 0; }
fi

# 终止进程: SIGTERM → 1s → SIGKILL
if [[ ${#PIDS[@]} -gt 0 ]]; then
    for pid in "${PIDS[@]}"; do
        kill "$pid" 2>/dev/null || true
    done
    sleep 1
    for pid in "${PIDS[@]}"; do
        if kill -0 "$pid" 2>/dev/null; then
            kill -9 "$pid" 2>/dev/null || true
        fi
    done
    echo -e "${GREEN}已终止 ${#PIDS[@]} 个进程${NC}"
fi

# 清理产物
[[ -f "$PIDFILE" ]] && rm -f "$PIDFILE"
[[ -f "$LOGFILE" ]] && rm -f "$LOGFILE"
[[ -d "${SCRIPT_DIR}/polaris" ]] && rm -rf "${SCRIPT_DIR}/polaris"
echo -e "${GREEN}已清理产物${NC}"
echo "清理完成。"
