#!/bin/bash
# =============================================================================
# 清理脚本：杀掉 examples/serviceRule 残留的 demo 进程，
# 删除 verify.sh 创建的 .build / .logs 目录及构建产物。
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
            echo "  --dry-run      仅展示匹配的进程和目录，不执行清理"
            exit 0
            ;;
        *) echo -e "${RED}未知参数: $1${NC}"; exit 1 ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ======================== 目录清理 ========================
cleanup_dirs() {
    local -a targets=(
        ".build"
        ".logs"
        "bin"
        "x86-bin"
        "polaris"
    )

    local -a present_targets=()
    for dir_name in "${targets[@]}"; do
        target="${SCRIPT_DIR}/${dir_name}"
        if [[ -e "$target" ]]; then
            present_targets+=("$dir_name")
        fi
    done

    if [[ ${#present_targets[@]} -eq 0 ]]; then
        return
    fi

    echo ""
    echo -e "${YELLOW}发现构建/日志产物:${NC}"
    for dir_name in "${present_targets[@]}"; do
        target="${SCRIPT_DIR}/${dir_name}"
        if [[ -d "$target" ]]; then
            local dir_size
            dir_size=$(du -sh "$target" 2>/dev/null | awk '{print $1}')
            echo -e "  ${dir_name}/  (${dir_size})"
        else
            local file_size
            file_size=$(ls -lh "$target" 2>/dev/null | awk '{print $5}')
            echo -e "  ${dir_name}  (${file_size})"
        fi
    done

    if [[ "$DRY_RUN" == true ]]; then
        echo -e "${YELLOW}[dry-run] 仅展示，未清理。${NC}"
        return
    fi

    if [[ "$FORCE" != true ]]; then
        read -r -p "是否清理以上产物? [y/N] " response
        case "$response" in
            [yY]|[yY][eE][sS]) ;;
            *)
                echo -e "${YELLOW}跳过目录清理。${NC}"
                return
                ;;
        esac
    fi

    for dir_name in "${present_targets[@]}"; do
        target="${SCRIPT_DIR}/${dir_name}"
        rm -rf "$target"
        echo -e "  ${GREEN}✓${NC} 已清理: ${dir_name}"
    done
}

# ======================== 进程清理 ========================
echo ""
echo -e "${CYAN}========================================${NC}"
echo -e "${CYAN}  serviceRule 示例清理工具${NC}"
echo -e "${CYAN}========================================${NC}"
echo ""

# 收集需要清理的 PID
declare -a DEMO_PIDS=()
declare -a DEMO_DESCS=()

# 匹配 serviceRule demo 进程：
#   - ./bin（Makefile 产出）
#   - .build/serviceRule（verify.sh 产出）
#   - serviceRule（go build 产出）
# 匹配条件：命令行包含 examples/serviceRule 路径下的二进制，或包含 -port :38080（demo 默认端口）
while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    pid=$(echo "$line" | awk '{print $2}')
    DEMO_PIDS+=("$pid")
    DEMO_DESCS+=("$line")
done < <(ps -ef | grep -E "examples/serviceRule/(bin|\.build/serviceRule|serviceRule)" | grep -v grep 2>/dev/null || true)

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

# 展示并清理进程
if [[ ${#DEMO_PIDS[@]} -eq 0 ]]; then
    echo -e "${GREEN}未发现残留的 serviceRule demo 进程，无需清理。${NC}"
else
    echo -e "${YELLOW}发现 ${#DEMO_PIDS[@]} 个残留 demo 进程:${NC}"
    echo ""
    print_process_table "${DEMO_DESCS[@]}"
    echo ""

    if [[ "$DRY_RUN" == true ]]; then
        echo -e "${YELLOW}[dry-run] 仅展示进程，未执行清理。${NC}"
    elif [[ "$FORCE" == true ]]; then
        kill_pids "${DEMO_PIDS[@]}"
    else
        read -r -p "确认清理以上进程? [y/N] " response
        case "$response" in
            [yY]|[yY][eE][sS]) kill_pids "${DEMO_PIDS[@]}" ;;
            *) echo -e "${YELLOW}跳过进程清理。${NC}" ;;
        esac
    fi
    echo ""
fi

if [[ "$DRY_RUN" == true ]]; then
    echo -e "${YELLOW}[dry-run] 以上为展示结果，未执行任何清理操作。${NC}"
else
    echo -e "${GREEN}进程清理流程完成。${NC}"
fi

cleanup_dirs

echo ""
