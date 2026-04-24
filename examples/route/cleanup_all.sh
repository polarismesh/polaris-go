#!/bin/bash
# =============================================================================
# examples/route 路由 demo 聚合清理脚本
#
# 功能：
#   1. 依次调用 4 个子目录下的 cleanup.sh，清理各 demo 的 provider/consumer 进程
#      及 .build / .logs 目录
#   2. 额外兜底：杀掉 examples/route 目录树下所有 provider / consumer / gateway /
#      simple-consumer / simple-gateway 残留进程
#   3. 删除聚合脚本自己生成的 .logs（run_all_tests 的聚合日志）
#
# 使用方法:
#   chmod +x cleanup_all.sh
#   ./cleanup_all.sh               # 展示将清理的内容，要求确认
#   ./cleanup_all.sh -f            # 强制清理，不询问
#   ./cleanup_all.sh --dry-run     # 仅展示残留项，不执行任何清理
#   ./cleanup_all.sh --skip lane   # 跳过指定 demo 的 cleanup
# =============================================================================

set -uo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

FORCE=false
DRY_RUN=false
SKIP_LIST=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        -f|--force)  FORCE=true;   shift ;;
        --dry-run)   DRY_RUN=true; shift ;;
        --skip)      SKIP_LIST="$2"; shift 2 ;;
        -h|--help)
            cat <<EOF
用法: $0 [-f|--force] [--dry-run] [--skip <demo,...>]

选项:
  -f, --force     直接清理，不需要确认
  --dry-run       仅展示匹配的进程与目录，不执行清理
  --skip <列表>   跳过指定 demo 的子清理，逗号分隔 (lane,metadata,nearby,rule)
  -h, --help      展示本帮助

示例:
  $0                  # 交互式清理（子脚本各自询问）
  $0 -f               # 一键强制清理全部
  $0 --dry-run        # 仅展示残留项
  $0 -f --skip lane   # 强制清理但跳过 lane
EOF
            exit 0
            ;;
        *) echo -e "${RED}未知参数: $1${NC}"; exit 1 ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

log_info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }

log_step() {
    echo ""
    echo -e "${CYAN}═══════════════════════════════════════════════════════════════════${NC}"
    echo -e "${CYAN}  $*${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════════════════════════${NC}"
}

# ======================== 判断子目录是否 skip ========================
is_skipped() {
    local name="$1"
    [[ -z "$SKIP_LIST" ]] && return 1
    local IFS=','
    # shellcheck disable=SC2206
    local arr=($SKIP_LIST)
    for x in "${arr[@]}"; do
        [[ "$(echo "$x" | tr -d ' ')" == "$name" ]] && return 0
    done
    return 1
}

# ======================== 调用单个子 cleanup ========================
run_sub_cleanup() {
    local name="$1"
    local script="${SCRIPT_DIR}/${name}/cleanup.sh"

    if is_skipped "$name"; then
        log_info "跳过子清理: ${name}"
        return 0
    fi

    if [[ ! -f "$script" ]]; then
        log_warn "找不到 ${script}，跳过"
        return 0
    fi
    if [[ ! -x "$script" ]]; then
        chmod +x "$script" 2>/dev/null || true
    fi

    log_step "子清理: ${name}/cleanup.sh"
    local args=()
    if [[ "$DRY_RUN" == "true" ]]; then
        args+=(--dry-run)
    elif [[ "$FORCE" == "true" ]]; then
        args+=(-f)
    fi
    # 让子脚本原样输出到终端
    if "$script" "${args[@]}"; then
        log_info "子清理 [${name}] 完成"
    else
        log_warn "子清理 [${name}] 返回非零退出码，继续下一个"
    fi
}

# ======================== 全局兜底清理 ========================
# 匹配 examples/route/*/.build/ 下的二进制名：
#   provider / consumer / simple_consumer / gateway / simple-gateway / simple-consumer
# 端口范围覆盖 4 个 demo 当前使用的端口（18080-18199 + 28080-28099 + 28180-28199）。
#
# 说明：子清理脚本已经按各自目录做了进程+端口清理；这里只是兜底防止某个 demo 目录
# 被用户改名/移除后仍有同名进程遗留。
global_sweep() {
    log_step "全局兜底清理 examples/route 下的残留进程"

    # 匹配 examples/route/<demo>/.build/<bin> 为前缀的进程命令行
    local grep_pattern="examples/route/.*/\\.build/(provider|consumer|simple_consumer|gateway|simple-gateway|simple-consumer)"

    local matches
    matches=$(ps -ef 2>/dev/null | grep -E "$grep_pattern" | grep -v grep || true)
    if [[ -z "$matches" ]]; then
        log_info "未发现全局残留进程"
        return 0
    fi

    echo "发现以下残留进程:"
    echo "$matches" | awk '{printf "  PID=%s CMD=%s %s %s %s %s %s %s\n", $2, $8, $9, $10, $11, $12, $13, $14}'

    if [[ "$DRY_RUN" == "true" ]]; then
        log_info "--dry-run: 不执行清理"
        return 0
    fi

    if [[ "$FORCE" != "true" ]]; then
        read -r -p "是否终止以上进程? [y/N] " ans
        if [[ ! "$ans" =~ ^[yY]$ ]]; then
            log_info "已取消全局兜底清理"
            return 0
        fi
    fi

    local pids
    pids=$(echo "$matches" | awk '{print $2}')
    for pid in $pids; do
        [[ -z "$pid" ]] && continue
        if kill -0 "$pid" 2>/dev/null; then
            log_info "SIGTERM PID=${pid}"
            kill "$pid" 2>/dev/null || true
        fi
    done
    sleep 2
    # SIGKILL 兜底
    for pid in $pids; do
        [[ -z "$pid" ]] && continue
        if kill -0 "$pid" 2>/dev/null; then
            log_warn "SIGKILL PID=${pid}"
            kill -9 "$pid" 2>/dev/null || true
        fi
    done
}

# ======================== 清理聚合脚本自己的 .logs ========================
cleanup_aggregate_logs() {
    local agg_log_dir="${SCRIPT_DIR}/.logs"
    if [[ ! -d "$agg_log_dir" ]]; then
        return 0
    fi
    log_step "清理聚合脚本日志: ${agg_log_dir}"
    local size
    size=$(du -sh "$agg_log_dir" 2>/dev/null | awk '{print $1}')
    echo "  ${agg_log_dir}  (${size})"

    if [[ "$DRY_RUN" == "true" ]]; then
        log_info "--dry-run: 不执行清理"
        return 0
    fi
    if [[ "$FORCE" != "true" ]]; then
        read -r -p "是否删除 ${agg_log_dir}? [y/N] " ans
        if [[ ! "$ans" =~ ^[yY]$ ]]; then
            log_info "已取消聚合日志清理"
            return 0
        fi
    fi
    rm -rf "$agg_log_dir"
    log_info "已删除 ${agg_log_dir}"
}

# ======================== 主流程 ========================
main() {
    echo ""
    echo -e "${BLUE}╔═══════════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║           examples/route 路由 demo 聚合清理脚本                  ║${NC}"
    echo -e "${BLUE}╚═══════════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "参数:"
    echo "  force:    ${FORCE}"
    echo "  dry-run:  ${DRY_RUN}"
    echo "  skip:     ${SKIP_LIST:-（无）}"
    echo ""

    # 调用 4 个子清理
    for name in lane metadata nearby rule; do
        run_sub_cleanup "$name"
    done

    # 全局兜底
    global_sweep

    # 聚合日志
    cleanup_aggregate_logs

    echo ""
    echo -e "${GREEN}╔═══════════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${GREEN}║  聚合清理完成                                                    ║${NC}"
    echo -e "${GREEN}╚═══════════════════════════════════════════════════════════════════╝${NC}"
    echo ""
}

main "$@"
