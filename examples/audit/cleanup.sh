#!/bin/bash
# =============================================================================
# 清理脚本：杀掉 examples/audit 示例残留的 provider / consumer / 集成测试进程，
# 并清理编译产物与日志目录（.build / .logs），覆盖 audit 远程版与 local mock 版。
#
# 使用方法:
#   chmod +x cleanup.sh
#   ./cleanup.sh          # 默认模式：先展示再确认后清理
#   ./cleanup.sh -f       # 强制模式：直接清理，不需要确认
#   ./cleanup.sh --dry-run # 仅展示，不执行清理
# =============================================================================

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

FORCE=false
DRY_RUN=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        -f|--force)  FORCE=true;   shift ;;
        --dry-run)   DRY_RUN=true; shift ;;
        -h|--help)
            echo "用法: $0 [-f|--force] [--dry-run]"
            echo "  -f, --force    直接清理，不需要确认"
            echo "  --dry-run      仅展示匹配的进程/目录，不执行清理"
            exit 0
            ;;
        *) echo -e "${RED}未知参数: $1${NC}"; exit 1 ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo ""
echo -e "${CYAN}========================================${NC}"
echo -e "${CYAN}  callAuditLog 示例进程/目录清理工具${NC}"
echo -e "${CYAN}========================================${NC}"
echo ""

# ======================== 收集残留进程 ========================
declare -a PIDS=()
declare -a DESCS=()
while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    pid=$(echo "$line" | awk '{print $2}')
    PIDS+=("$pid")
    DESCS+=("$line")
done < <(ps -ef \
    | grep -E 'examples/audit/.*/(audit_provider|audit_consumer|audit_test)\b|\.build/(audit_provider|audit_consumer|audit_test)\b' \
    | grep -v grep)

kill_pids() {
    local killed=0 force_killed=0
    for pid in "$@"; do
        if kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null && { echo -e "  ${GREEN}✓${NC} 已终止 PID $pid (SIGTERM)"; killed=$((killed+1)); }
        else
            echo -e "  ${YELLOW}-${NC} PID $pid 已不存在，跳过"
        fi
    done
    sleep 1
    for pid in "$@"; do
        if kill -0 "$pid" 2>/dev/null; then
            echo -e "  ${YELLOW}!${NC} PID $pid 未响应 SIGTERM，发送 SIGKILL..."
            kill -9 "$pid" 2>/dev/null || true
            force_killed=$((force_killed+1))
        fi
    done
    echo -e "  ${GREEN}进程清理完成:${NC} 终止 ${killed} 个$( [[ $force_killed -gt 0 ]] && echo ", 强制杀掉 ${force_killed} 个" )"
}

if [[ ${#PIDS[@]} -gt 0 ]]; then
    echo -e "${YELLOW}发现 ${#PIDS[@]} 个残留进程:${NC}"
    for line in "${DESCS[@]}"; do echo "  $line"; done
    echo ""
    if [[ "$DRY_RUN" == true ]]; then
        echo -e "${YELLOW}[dry-run] 仅展示进程，未执行清理。${NC}"
    elif [[ "$FORCE" == true ]]; then
        kill_pids "${PIDS[@]}"
    else
        read -r -p "确认清理以上进程? [y/N] " response
        case "$response" in
            [yY]|[yY][eE][sS]) kill_pids "${PIDS[@]}" ;;
            *) echo -e "${YELLOW}跳过进程清理。${NC}" ;;
        esac
    fi
    echo ""
else
    echo -e "${GREEN}未发现残留的 provider/consumer/集成测试进程。${NC}"
    echo ""
fi

# ======================== 清理 .build / .logs 目录 ========================
# 覆盖 audit 根目录与 local 子目录
declare -a TARGET_DIRS=(
    "${SCRIPT_DIR}/.build"
    "${SCRIPT_DIR}/.logs"
    "${SCRIPT_DIR}/local/.build"
    "${SCRIPT_DIR}/local/.logs"
)

FOUND_DIR=false
for d in "${TARGET_DIRS[@]}"; do
    [[ -d "$d" ]] && FOUND_DIR=true
done

if [[ "$FOUND_DIR" != true ]]; then
    echo -e "${GREEN}未发现 .build/.logs 目录，无需清理。${NC}"
    echo ""
    exit 0
fi

echo -e "${YELLOW}发现构建/日志目录:${NC}"
for d in "${TARGET_DIRS[@]}"; do
    if [[ -d "$d" ]]; then
        size=$(du -sh "$d" 2>/dev/null | awk '{print $1}')
        echo -e "  ${d#"${SCRIPT_DIR}/"}  (${size})"
    fi
done
echo ""

if [[ "$DRY_RUN" == true ]]; then
    echo -e "${YELLOW}[dry-run] 仅展示目录，未执行清理。${NC}"
    echo ""
    exit 0
fi

if [[ "$FORCE" != true ]]; then
    read -r -p "是否清理以上目录? [y/N] " dir_response
    case "$dir_response" in
        [yY]|[yY][eE][sS]) ;;
        *) echo -e "${YELLOW}跳过目录清理。${NC}"; echo ""; exit 0 ;;
    esac
fi

for d in "${TARGET_DIRS[@]}"; do
    if [[ -d "$d" ]]; then
        rm -rf "$d"
        echo -e "  ${GREEN}✓${NC} 已清理: ${d#"${SCRIPT_DIR}/"}"
    fi
done
echo ""
echo -e "${GREEN}清理完成。${NC}"
echo ""
