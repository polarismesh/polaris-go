#!/bin/bash
# =============================================================================
# 清理脚本：杀掉泳道路由示例残留的 provider / consumer 进程
# 同时支持清理 lane-test.sh 和 lane-warmup-test.sh 启动的进程
#
# 使用方法:
#   chmod +x cleanup.sh
#   ./cleanup.sh            # 默认模式：先展示再确认后清理
#   ./cleanup.sh -f         # 强制模式：直接清理，不需要确认
#   ./cleanup.sh --dry-run  # 仅展示，不执行清理
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

# PID 文件列表（lane-test.sh 和 lane-warmup-test.sh 各自写入的 PID 文件）
PID_FILES=(
    "${SCRIPT_DIR}/.lane-test-pids"
    "${SCRIPT_DIR}/.lane-warmup-pids"
)

# 格式化打印进程信息表（接受多个 ps -ef 输出行作为参数）
print_process_table() {
    printf "  ${CYAN}%-8s %-8s %-22s %s${NC}\n" "PID" "PPID" "启动时间" "命令"
    printf "  %-8s %-8s %-22s %s\n" \
        "--------" "--------" "----------------------" "--------------------------------------------"
    for line in "$@"; do
        pid=$(echo "$line" | awk '{print $2}')
        ppid=$(echo "$line" | awk '{print $3}')
        cmd=$(echo "$line" | awk '{for(j=8;j<=NF;j++) printf "%s ", $j; print ""}')
        start_time=$(ps -p "$pid" -o lstart= 2>/dev/null || echo "N/A")
        if [[ "$start_time" != "N/A" ]] && [[ -n "$start_time" ]]; then
            start_time=$(echo "$start_time" | awk '{printf "%s %s %s %s", $4, $2, $3, $5}')
        fi
        printf "  %-8s %-8s %-22s %s\n" "$pid" "$ppid" "$start_time" "$cmd"
    done
}

# 终止指定 PID 列表中的进程（先 SIGTERM，超时后 SIGKILL）
kill_pids() {
    local killed=0
    local failed=0
    local all_pids=("$@")

    for pid in "${all_pids[@]}"; do
        if kill -0 "$pid" 2>/dev/null; then
            if kill "$pid" 2>/dev/null; then
                echo -e "  ${GREEN}✓${NC} 已发送 SIGTERM 到 PID $pid"
                killed=$((killed + 1))
            else
                echo -e "  ${RED}✗${NC} 无法终止 PID $pid"
                failed=$((failed + 1))
            fi
        else
            echo -e "  ${YELLOW}-${NC} PID $pid 已不存在，跳过"
        fi
    done

    # 等待进程响应 SIGTERM（最多 10s，给 Polaris 反注册留够时间）
    local wait_secs=10
    echo -e "  ${CYAN}等待进程退出（最多 ${wait_secs}s）...${NC}"
    local elapsed=0
    while [[ $elapsed -lt $wait_secs ]]; do
        local still_running=false
        for pid in "${all_pids[@]}"; do
            if kill -0 "$pid" 2>/dev/null; then
                still_running=true
                break
            fi
        done
        [[ "$still_running" == false ]] && break
        sleep 1
        elapsed=$((elapsed + 1))
    done

    # 仍未退出的进程发送 SIGKILL
    local force_killed=0
    for pid in "${all_pids[@]}"; do
        if kill -0 "$pid" 2>/dev/null; then
            echo -e "  ${YELLOW}!${NC} PID $pid 未响应 SIGTERM，发送 SIGKILL..."
            kill -9 "$pid" 2>/dev/null || true
            force_killed=$((force_killed + 1))
        fi
    done

    local summary="  ${GREEN}清理完成:${NC} 终止 ${killed} 个进程"
    [[ $force_killed -gt 0 ]] && summary+="，强制杀掉 ${force_killed} 个"
    [[ $failed -gt 0 ]] && summary+="，${RED}${failed} 个失败${NC}"
    echo -e "$summary"
}

# 清理 PID 文件
cleanup_pid_files() {
    local removed=0
    for pf in "${PID_FILES[@]}"; do
        if [[ -f "$pf" ]]; then
            if [[ "$DRY_RUN" == true ]]; then
                echo -e "  ${YELLOW}[dry-run]${NC} 将删除 PID 文件: $(basename "$pf")"
            else
                rm -f "$pf"
                echo -e "  ${GREEN}✓${NC} 已删除 PID 文件: $(basename "$pf")"
                removed=$((removed + 1))
            fi
        fi
    done
    [[ $removed -eq 0 ]] && [[ "$DRY_RUN" == false ]] && \
        echo -e "  ${CYAN}无 PID 文件需要清理${NC}"
}

# 清理 .build、.logs 和 polaris 日志目录
cleanup_dirs() {
    # 收集所有需要清理的目录
    local -a dirs_to_clean=()

    # 顶层 .build、.logs 和 polaris 日志目录
    for dir_name in .build .logs polaris; do
        [[ -d "${SCRIPT_DIR}/${dir_name}" ]] && dirs_to_clean+=("${SCRIPT_DIR}/${dir_name}")
    done

    if [[ ${#dirs_to_clean[@]} -eq 0 ]]; then
        return
    fi

    echo ""
    echo -e "${YELLOW}发现构建/日志目录:${NC}"
    for target_dir in "${dirs_to_clean[@]}"; do
        local display_path="${target_dir#${SCRIPT_DIR}/}"
        local dir_size
        dir_size=$(du -sh "$target_dir" 2>/dev/null | awk '{print $1}')
        echo -e "  ${display_path}/  (${dir_size})"
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

    for target_dir in "${dirs_to_clean[@]}"; do
        local display_path="${target_dir#${SCRIPT_DIR}/}"
        if [[ -d "$target_dir" ]]; then
            rm -rf "$target_dir"
            echo -e "  ${GREEN}✓${NC} 已清理目录: ${display_path}/"
        fi
    done
}

# =============================================================================
# 主流程
# =============================================================================

echo ""
echo -e "${CYAN}========================================${NC}"
echo -e "${CYAN}  泳道路由示例进程清理工具${NC}"
echo -e "${CYAN}========================================${NC}"
echo ""

# 收集需要清理的 PID（分别收集 provider、consumer 和 gateway）
declare -a PROVIDER_PIDS=()
declare -a PROVIDER_DESCS=()
declare -a CONSUMER_PIDS=()
declare -a CONSUMER_DESCS=()
declare -a GATEWAY_PIDS=()
declare -a GATEWAY_DESCS=()

# 1. 从 PID 文件中读取已知 PID
for pf in "${PID_FILES[@]}"; do
    if [[ -f "$pf" ]]; then
        while IFS= read -r pid; do
            [[ -z "$pid" ]] && continue
            if kill -0 "$pid" 2>/dev/null; then
                cmd=$(ps -p "$pid" -o comm= 2>/dev/null || echo "")
                line=$(ps -ef | awk -v p="$pid" '$2 == p' | head -1)
                case "$cmd" in
                    provider*)
                        PROVIDER_PIDS+=("$pid")
                        PROVIDER_DESCS+=("$line")
                        ;;
                    consumer*)
                        CONSUMER_PIDS+=("$pid")
                        CONSUMER_DESCS+=("$line")
                        ;;
                    gateway*)
                        GATEWAY_PIDS+=("$pid")
                        GATEWAY_DESCS+=("$line")
                        ;;
                    *)
                        # 未知类型，按 provider 处理
                        PROVIDER_PIDS+=("$pid")
                        PROVIDER_DESCS+=("$line")
                        ;;
                esac
            fi
        done < "$pf"
    fi
done

# 2. 兜底：通过 ps 搜索残留进程（防止 PID 文件丢失的情况）
while IFS= read -r line; do
    pid=$(echo "$line" | awk '{print $2}')
    # 去重：如果 PID 已在列表中则跳过
    local_found=false
    for known in "${PROVIDER_PIDS[@]}" "${CONSUMER_PIDS[@]}" "${GATEWAY_PIDS[@]}"; do
        [[ "$known" == "$pid" ]] && local_found=true && break
    done
    [[ "$local_found" == true ]] && continue
    PROVIDER_PIDS+=("$pid")
    PROVIDER_DESCS+=("$line")
done < <(ps -ef | grep -E "${SCRIPT_DIR}/\.build/provider|\.build/provider" | grep -v grep)

while IFS= read -r line; do
    pid=$(echo "$line" | awk '{print $2}')
    local_found=false
    for known in "${CONSUMER_PIDS[@]}"; do
        [[ "$known" == "$pid" ]] && local_found=true && break
    done
    [[ "$local_found" == true ]] && continue
    CONSUMER_PIDS+=("$pid")
    CONSUMER_DESCS+=("$line")
done < <(ps -ef | grep -E "${SCRIPT_DIR}/\.build/consumer|\.build/consumer" | grep -v grep)

while IFS= read -r line; do
    pid=$(echo "$line" | awk '{print $2}')
    local_found=false
    for known in "${GATEWAY_PIDS[@]}"; do
        [[ "$known" == "$pid" ]] && local_found=true && break
    done
    [[ "$local_found" == true ]] && continue
    GATEWAY_PIDS+=("$pid")
    GATEWAY_DESCS+=("$line")
done < <(ps -ef | grep -E "${SCRIPT_DIR}/\.build/gateway|\.build/gateway" | grep -v grep)

# =============================================================================
# 展示 & 清理进程
# =============================================================================

if [[ ${#PROVIDER_PIDS[@]} -eq 0 ]] && [[ ${#CONSUMER_PIDS[@]} -eq 0 ]] && [[ ${#GATEWAY_PIDS[@]} -eq 0 ]]; then
    echo -e "${GREEN}未发现残留的 provider/consumer/gateway 进程，无需清理。${NC}"
    echo ""
    echo -e "${CYAN}--- 清理 PID 文件 ---${NC}"
    cleanup_pid_files
    cleanup_dirs
    echo ""
    exit 0
fi

# --- Provider 进程清理 ---
if [[ ${#PROVIDER_PIDS[@]} -gt 0 ]]; then
    echo -e "${YELLOW}发现 ${#PROVIDER_PIDS[@]} 个残留 Provider 进程:${NC}"
    echo ""
    print_process_table "${PROVIDER_DESCS[@]}"
    echo ""

    if [[ "$DRY_RUN" == true ]]; then
        echo -e "${YELLOW}[dry-run] 仅展示 Provider 进程，未执行清理。${NC}"
    elif [[ "$FORCE" == true ]]; then
        kill_pids "${PROVIDER_PIDS[@]}"
    else
        read -r -p "确认清理以上 Provider 进程? [y/N] " response
        case "$response" in
            [yY]|[yY][eE][sS]) kill_pids "${PROVIDER_PIDS[@]}" ;;
            *) echo -e "${YELLOW}跳过 Provider 进程清理。${NC}" ;;
        esac
    fi
    echo ""
fi

# --- Consumer 进程清理（单独询问） ---
if [[ ${#CONSUMER_PIDS[@]} -gt 0 ]]; then
    echo -e "${YELLOW}发现 ${#CONSUMER_PIDS[@]} 个残留 Consumer 进程:${NC}"
    echo -e "${YELLOW}（注意: Consumer 可能正在被其他验证脚本复用）${NC}"
    echo ""
    print_process_table "${CONSUMER_DESCS[@]}"
    echo ""

    if [[ "$DRY_RUN" == true ]]; then
        echo -e "${YELLOW}[dry-run] 仅展示 Consumer 进程，未执行清理。${NC}"
    elif [[ "$FORCE" == true ]]; then
        kill_pids "${CONSUMER_PIDS[@]}"
    else
        read -r -p "确认清理以上 Consumer 进程? [y/N] " response
        case "$response" in
            [yY]|[yY][eE][sS]) kill_pids "${CONSUMER_PIDS[@]}" ;;
            *) echo -e "${YELLOW}跳过 Consumer 进程清理。${NC}" ;;
        esac
    fi
    echo ""
fi

# --- Gateway 进程清理 ---
if [[ ${#GATEWAY_PIDS[@]} -gt 0 ]]; then
    echo -e "${YELLOW}发现 ${#GATEWAY_PIDS[@]} 个残留 Gateway 进程:${NC}"
    echo ""
    print_process_table "${GATEWAY_DESCS[@]}"
    echo ""

    if [[ "$DRY_RUN" == true ]]; then
        echo -e "${YELLOW}[dry-run] 仅展示 Gateway 进程，未执行清理。${NC}"
    elif [[ "$FORCE" == true ]]; then
        kill_pids "${GATEWAY_PIDS[@]}"
    else
        read -r -p "确认清理以上 Gateway 进程? [y/N] " response
        case "$response" in
            [yY]|[yY][eE][sS]) kill_pids "${GATEWAY_PIDS[@]}" ;;
            *) echo -e "${YELLOW}跳过 Gateway 进程清理。${NC}" ;;
        esac
    fi
    echo ""
fi

# --- 清理 PID 文件 ---
echo -e "${CYAN}--- 清理 PID 文件 ---${NC}"
cleanup_pid_files
echo ""

if [[ "$DRY_RUN" == true ]]; then
    echo -e "${YELLOW}[dry-run] 以上为进程展示结果，未执行任何清理操作。${NC}"
else
    echo -e "${GREEN}进程清理流程完成。${NC}"
fi

cleanup_dirs

echo ""
