#!/bin/bash
# =============================================================================
# 清理脚本：杀掉 examples/ratelimit 残留的 provider/consumer 进程，
# 删除 verify_ratelimit.sh 创建的 .build / .logs 目录。
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

# 清理 .build 和 .logs 目录，以及 provider 子目录中可能因手动 `go run .` 产生的 polaris/ 残留.
# verify_ratelimit.sh 现在会让 SDK 写到 .build/<provider>/polaris/，源码子目录默认不会再被污染；
# 此处的 provider-*/polaris/ 清理是兜底，处理"用户手动跑 main.go"产生的旧痕迹.
cleanup_dirs() {
    # 清理目标：相对 SCRIPT_DIR 的子路径
    local -a targets=(
        ".build"
        ".logs"
        "provider-qps/polaris"
        "provider-concurrency/polaris"
        "consumer/polaris"
    )

    local -a present_targets=()
    for dir_name in "${targets[@]}"; do
        target_dir="${SCRIPT_DIR}/${dir_name}"
        if [[ -d "$target_dir" ]]; then
            present_targets+=("$dir_name")
        fi
    done

    if [[ ${#present_targets[@]} -eq 0 ]]; then
        return
    fi

    echo ""
    echo -e "${YELLOW}发现构建/日志目录:${NC}"
    for dir_name in "${present_targets[@]}"; do
        target_dir="${SCRIPT_DIR}/${dir_name}"
        local dir_size
        dir_size=$(du -sh "$target_dir" 2>/dev/null | awk '{print $1}')
        echo -e "  ${dir_name}/  (${dir_size})"
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

    for dir_name in "${present_targets[@]}"; do
        target_dir="${SCRIPT_DIR}/${dir_name}"
        rm -rf "$target_dir"
        echo -e "  ${GREEN}✓${NC} 已清理目录: ${dir_name}/"
    done
}

echo ""
echo -e "${CYAN}========================================${NC}"
echo -e "${CYAN}  Ratelimit 示例进程清理工具${NC}"
echo -e "${CYAN}========================================${NC}"
echo ""

# 收集需要清理的 PID
declare -a PROVIDER_PIDS=()
declare -a PROVIDER_DESCS=()
declare -a CONSUMER_PIDS=()
declare -a CONSUMER_DESCS=()

# 匹配 examples/ratelimit 子目录下 provider 产物：
#   - provider-qps/bin、provider-concurrency/bin（Makefile 产出，与源码同级）
#   - .build/<instance>/(provider-qps|provider-concurrency)
#     （verify_ratelimit.sh 产出，<instance> 如 provider-qps-reject、provider-qps-unirate、provider-concurrency 等）
while IFS= read -r line; do
    pid=$(echo "$line" | awk '{print $2}')
    PROVIDER_PIDS+=("$pid")
    PROVIDER_DESCS+=("$line")
done < <(ps -ef | grep -E 'examples/ratelimit/((provider-qps|provider-concurrency)/bin|\.build/[a-zA-Z0-9_-]+/(provider-qps|provider-concurrency))\b' | grep -v grep)

# 匹配 consumer 产物（独立分组便于按角色清理）：
#   - consumer/bin（Makefile）
#   - .build/<instance>/consumer（verify_ratelimit.sh 产出，<instance> 如 consumer-qps-reject 等）
while IFS= read -r line; do
    pid=$(echo "$line" | awk '{print $2}')
    CONSUMER_PIDS+=("$pid")
    CONSUMER_DESCS+=("$line")
done < <(ps -ef | grep -E 'examples/ratelimit/(consumer/bin|\.build/[a-zA-Z0-9_-]+/consumer)\b' | grep -v grep)

# 格式化打印进程信息
print_process_table() {
    printf "  ${CYAN}%-8s %-8s %-22s %s${NC}\n" "PID" "PPID" "启动时间" "命令"
    printf "  %-8s %-8s %-22s %s\n" "--------" "--------" "----------------------" "--------------------------------------------"
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

# 执行清理指定 PID 列表
kill_pids() {
    local killed=0
    local failed=0
    local all_pids=("$@")
    for pid in "${all_pids[@]}"; do
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
    sleep 1
    local force_killed=0
    for pid in "${all_pids[@]}"; do
        if kill -0 "$pid" 2>/dev/null; then
            echo -e "  ${YELLOW}!${NC} PID $pid 未响应 SIGTERM，发送 SIGKILL..."
            kill -9 "$pid" 2>/dev/null || true
            force_killed=$((force_killed + 1))
        fi
    done
    echo -e "  ${GREEN}清理完成:${NC} 终止 ${killed} 个进程" \
        "$( [[ $force_killed -gt 0 ]] && echo ", 强制杀掉 ${force_killed} 个" )" \
        "$( [[ $failed -gt 0 ]] && echo -e ", ${RED}${failed} 个失败${NC}" )"
}

# 展示结果
if [[ ${#PROVIDER_PIDS[@]} -eq 0 ]] && [[ ${#CONSUMER_PIDS[@]} -eq 0 ]]; then
    echo -e "${GREEN}未发现残留的 ratelimit provider/consumer 进程，无需清理。${NC}"
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

# --- Consumer 进程清理 ---
if [[ ${#CONSUMER_PIDS[@]} -gt 0 ]]; then
    echo -e "${YELLOW}发现 ${#CONSUMER_PIDS[@]} 个残留 Consumer 进程:${NC}"
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

if [[ "$DRY_RUN" == true ]]; then
    echo -e "${YELLOW}[dry-run] 以上为进程展示结果，未执行任何清理操作。${NC}"
else
    echo -e "${GREEN}进程清理流程完成。${NC}"
fi

cleanup_dirs

echo ""
