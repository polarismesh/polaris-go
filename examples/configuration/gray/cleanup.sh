#!/bin/bash
# =============================================================================
# 清理脚本：杀掉配置灰度验证示例残留进程，清理构建/日志目录
#
# 使用方法:
#   ./cleanup.sh            # 默认模式：先展示再确认后清理
#   ./cleanup.sh -f         # 强制模式：直接清理
#   ./cleanup.sh --dry-run  # 仅展示，不执行清理
# =============================================================================

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
PID_FILE="${SCRIPT_DIR}/.gray-test-pids"
BUILD_DIR="${SCRIPT_DIR}/.build"
LOG_DIR="${SCRIPT_DIR}/.logs"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# 待终止的 PID 列表
declare -a TARGET_PIDS=()

# 第一层：从 PID 文件读取
if [[ -f "$PID_FILE" ]]; then
    while IFS= read -r pid; do
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            TARGET_PIDS+=("$pid")
        fi
    done < "$PID_FILE"
fi

# 第二层：ps 兜底搜索残留 gray-demo 进程
while IFS= read -r line; do
    pid=$(echo "$line" | awk '{print $2}')
    [[ -n "$pid" ]] && TARGET_PIDS+=("$pid")
done < <(ps -ef | grep -E "${SCRIPT_DIR}/\.build/gray-demo" | grep -v grep || true)

# 去重
declare -A SEEN
UNIQUE_PIDS=()
for pid in "${TARGET_PIDS[@]}"; do
    if [[ -z "${SEEN[$pid]:-}" ]]; then
        SEEN[$pid]=1
        UNIQUE_PIDS+=("$pid")
    fi
done

echo ""
echo "待清理项:"
echo "  PID 文件:       ${PID_FILE} ($([[ -f "$PID_FILE" ]] && echo 存在 || echo 不存在))"
echo "  构建目录:       ${BUILD_DIR} ($([[ -d "$BUILD_DIR" ]] && echo 存在 || echo 不存在))"
echo "  日志目录:       ${LOG_DIR} ($([[ -d "$LOG_DIR" ]] && echo 存在 || echo 不存在))"
echo "  残留进程数:     ${#UNIQUE_PIDS[@]}"
if [[ ${#UNIQUE_PIDS[@]} -gt 0 ]]; then
    echo "  残留进程明细:"
    for pid in "${UNIQUE_PIDS[@]}"; do
        ps -p "$pid" -o pid,ppid,etime,args 2>/dev/null | tail -1 | awk '{printf "    PID=%s PPID=%s 启动时长=%s\n", $1, $2, $3}'
    done
fi
echo ""

if [[ "$DRY_RUN" == "true" ]]; then
    echo -e "${YELLOW}--dry-run 模式，不执行清理${NC}"
    exit 0
fi

if [[ "$FORCE" != "true" ]]; then
    read -r -p "确认清理以上进程与目录? [y/N] " ans
    [[ "$ans" =~ ^[Yy]$ ]] || { echo "已取消"; exit 0; }
fi

# 终止进程：先 SIGTERM，等待后 SIGKILL
if [[ ${#UNIQUE_PIDS[@]} -gt 0 ]]; then
    echo "终止残留进程..."
    for pid in "${UNIQUE_PIDS[@]}"; do
        kill "$pid" 2>/dev/null || true
    done
    sleep 3
    for pid in "${UNIQUE_PIDS[@]}"; do
        if kill -0 "$pid" 2>/dev/null; then
            kill -9 "$pid" 2>/dev/null || true
        fi
    done
    echo -e "${GREEN}已终止 ${#UNIQUE_PIDS[@]} 个进程${NC}"
fi

# 清理 PID 文件与构建/日志目录
rm -f "$PID_FILE"
rm -rf "$BUILD_DIR" "$LOG_DIR"
# 清理各工作目录下可能残留的 polaris SDK 日志目录
rm -rf "${SCRIPT_DIR}/.build" 2>/dev/null || true
echo -e "${GREEN}已清理 PID 文件与构建/日志目录${NC}"
echo "清理完成。"
