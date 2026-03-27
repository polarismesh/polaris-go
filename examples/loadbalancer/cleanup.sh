#!/bin/bash
# =============================================================================
# 清理脚本：杀掉 loadbalancer 示例残留的 provider / consumer 进程
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
            echo "  --dry-run      仅展示匹配的进程，不执行清理"
            exit 0
            ;;
        *) echo -e "${RED}未知参数: $1${NC}"; exit 1 ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# 清理 .build 和 .logs 目录
cleanup_dirs() {
    local has_dir=false
    for dir_name in .build .logs; do
        target_dir="${SCRIPT_DIR}/${dir_name}"
        if [[ -d "$target_dir" ]]; then
            has_dir=true
            break
        fi
    done

    if [[ "$has_dir" == false ]]; then
        return
    fi

    echo ""
    echo -e "${YELLOW}发现构建/日志目录:${NC}"
    for dir_name in .build .logs; do
        target_dir="${SCRIPT_DIR}/${dir_name}"
        if [[ -d "$target_dir" ]]; then
            local dir_size
            dir_size=$(du -sh "$target_dir" 2>/dev/null | awk '{print $1}')
            echo -e "  ${dir_name}/  (${dir_size})"
        fi
    done

    if [[ "$DRY_RUN" == true ]]; then
        echo -e "${YELLOW}[dry-run] 仅展示，未清理目录。${NC}"
        return
    fi

    if [[ "$FORCE" != true ]]; then
        read -r -p "是否清理以上目录? [y/N] " dir_response
        case "$dir_response" in
            [yY]|[yY][eE][sS]) ;;
            *)
                echo -e "${YELLOW}跳过目录清理。${NC}"
                return
                ;;
        esac
    fi

    for dir_name in .build .logs; do
        target_dir="${SCRIPT_DIR}/${dir_name}"
        if [[ -d "$target_dir" ]]; then
            rm -rf "$target_dir"
            echo -e "  ${GREEN}✓${NC} 已清理目录: ${dir_name}/"
        fi
    done
}

echo ""
echo -e "${CYAN}========================================${NC}"
echo -e "${CYAN}  LoadBalancer 示例进程清理工具${NC}"
echo -e "${CYAN}========================================${NC}"
echo ""

# 收集需要清理的 PID
declare -a PIDS_TO_KILL=()
declare -a PID_DESCS=()

# 查找 provider 进程（匹配 loadbalancer/.build 下的 provider 进程）
while IFS= read -r line; do
    pid=$(echo "$line" | awk '{print $2}')
    PIDS_TO_KILL+=("$pid")
    PID_DESCS+=("$line")
done < <(ps -ef | grep -E 'loadbalancer/\.build/(provider[0-9]*/)?provider\b|/provider\s+--namespace\s+\S+\s+--service\s+LoadBalance' | grep -v grep | grep -v fileproviderd)

# 查找 consumer 进程（匹配 loadbalancer/.build 下的 consumer 进程）
while IFS= read -r line; do
    pid=$(echo "$line" | awk '{print $2}')
    PIDS_TO_KILL+=("$pid")
    PID_DESCS+=("$line")
done < <(ps -ef | grep -E 'loadbalancer/\.build/(consumer_run/)?consumer\b|/consumer\s+--namespace\s+\S+\s+--service\s+LoadBalance' | grep -v grep)

# 展示结果
if [[ ${#PIDS_TO_KILL[@]} -eq 0 ]]; then
    echo -e "${GREEN}未发现残留的 provider/consumer 进程，无需清理。${NC}"
    cleanup_dirs
    echo ""
    exit 0
fi

echo -e "${YELLOW}发现 ${#PIDS_TO_KILL[@]} 个残留进程:${NC}"
echo ""
printf "  ${CYAN}%-8s %-8s %-22s %s${NC}\n" "PID" "PPID" "启动时间" "命令"
printf "  %-8s %-8s %-22s %s\n" "--------" "--------" "----------------------" "--------------------------------------------"
for i in "${!PID_DESCS[@]}"; do
    pid=$(echo "${PID_DESCS[$i]}" | awk '{print $2}')
    ppid=$(echo "${PID_DESCS[$i]}" | awk '{print $3}')
    cmd=$(echo "${PID_DESCS[$i]}" | awk '{for(j=8;j<=NF;j++) printf "%s ", $j; print ""}')
    # 获取进程启动时间
    start_time=$(ps -p "$pid" -o lstart= 2>/dev/null || echo "N/A")
    # 格式化启动时间：将 "Thu Mar 26 11:20:01 2026" 转为更紧凑的格式
    if [[ "$start_time" != "N/A" ]] && [[ -n "$start_time" ]]; then
        start_time=$(echo "$start_time" | awk '{printf "%s %s %s %s", $4, $2, $3, $5}')
    fi
    printf "  %-8s %-8s %-22s %s\n" "$pid" "$ppid" "$start_time" "$cmd"
done
echo ""

# dry-run 模式仅展示
if [[ "$DRY_RUN" == true ]]; then
    echo -e "${YELLOW}[dry-run] 仅展示，未执行清理。${NC}"
    cleanup_dirs
    echo ""
    exit 0
fi

# 非强制模式下需确认
if [[ "$FORCE" != true ]]; then
    read -r -p "确认清理以上进程? [y/N] " response
    case "$response" in
        [yY]|[yY][eE][sS]) ;;
        *)
            echo -e "${YELLOW}已取消进程清理。${NC}"
            cleanup_dirs
            exit 0
            ;;
    esac
fi

# 执行清理
killed=0
failed=0
for pid in "${PIDS_TO_KILL[@]}"; do
    if kill -0 "$pid" 2>/dev/null; then
        if kill "$pid" 2>/dev/null; then
            echo -e "  ${GREEN}✓${NC} 已终止 PID $pid (SIGTERM)"
            killed=$((killed + 1))
        else
            echo -e "  ${RED}✗${NC} 无法终止 PID $pid"
            failed=$((failed + 1))
        fi
    else
        echo -e "  ${YELLOW}-${NC} PID $pid 已不存在，跳过"
    fi
done

# 等待一小会，检查是否有顽固进程需要 SIGKILL
sleep 1
force_killed=0
for pid in "${PIDS_TO_KILL[@]}"; do
    if kill -0 "$pid" 2>/dev/null; then
        echo -e "  ${YELLOW}!${NC} PID $pid 未响应 SIGTERM，发送 SIGKILL..."
        kill -9 "$pid" 2>/dev/null || true
        force_killed=$((force_killed + 1))
    fi
done

echo ""
echo -e "${GREEN}清理完成:${NC} 终止 ${killed} 个进程" \
    "$( [[ $force_killed -gt 0 ]] && echo ", 强制杀掉 ${force_killed} 个" )" \
    "$( [[ $failed -gt 0 ]] && echo -e ", ${RED}${failed} 个失败${NC}" )"

cleanup_dirs

echo ""
