#!/bin/bash
# =============================================================================
# examples/route 路由 demo 聚合测试脚本
#
# 一次性调用 examples/route/ 下 4 个路由 demo 的测试脚本：
#   1. lane      泳道路由         lane/lane-test.sh all <polaris>
#   2. metadata  元数据路由       metadata/verify_metadata_route.sh
#   3. nearby    就近路由         nearby/verify_nearby_route.sh
#   4. rule      规则路由 + failoverType  rule/verify_rule_route.sh
#   5. mixed     混合路由 (lane × rule × nearby)  mixed/run.sh all <polaris>
#
# 每个 demo 运行完后，从退出码与输出中的「验证结论」字样判定 PASS/PARTIAL/FAIL，
# 并在末尾输出：
#   - 总数 / 通过数 / 部分通过数 / 失败数
#   - 成功率（PASS / 总数）
#   - 每个 demo 的结论、耗时与日志位置
#   - 失败用例列表
#
# 使用方法:
#   chmod +x run_all_tests.sh
#   ./run_all_tests.sh                          # 默认跑全部 4 个 demo
#   ./run_all_tests.sh --polaris-server 1.2.3.4 # 指定北极星地址
#   ./run_all_tests.sh --only rule,nearby       # 只跑指定 demo
#   ./run_all_tests.sh --skip lane              # 跳过指定 demo
#   ./run_all_tests.sh --continue-on-failure    # 失败不中断，全部跑完再汇总
#   ./run_all_tests.sh --stop-on-first-failure  # 第一个失败即停止（默认行为相反）
#
# 前置条件:
#   - 北极星服务端已启动（默认 127.0.0.1:8091, HTTP 8090）
#   - Go / python3 / curl / lsof 已安装
#
# 退出码:
#   0          = 全部 PASS
#   >0         = 失败的 demo 数量（PARTIAL 计 1，FAIL 计 1）
# =============================================================================

set -uo pipefail

# ======================== 颜色输出 ========================
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# ======================== 默认配置 ========================
POLARIS_SERVER="${POLARIS_SERVER:-127.0.0.1}"
POLARIS_TOKEN="${POLARIS_TOKEN:-}"
ONLY_LIST=""
SKIP_LIST=""
CONTINUE_ON_FAILURE=true   # 默认继续跑完所有 demo
DEBUG_MODE="${DEBUG_MODE:-false}"

# ======================== 解析命令行参数 ========================
while [[ $# -gt 0 ]]; do
    case "$1" in
        --polaris-server)
            POLARIS_SERVER="$2"; shift 2 ;;
        --polaris-token)
            POLARIS_TOKEN="$2"; shift 2 ;;
        --only)
            ONLY_LIST="$2"; shift 2 ;;
        --skip)
            SKIP_LIST="$2"; shift 2 ;;
        --continue-on-failure)
            CONTINUE_ON_FAILURE=true; shift ;;
        --stop-on-first-failure)
            CONTINUE_ON_FAILURE=false; shift ;;
        --debug)
            DEBUG_MODE=true; shift ;;
        -h|--help)
            cat <<EOF
用法: $0 [选项]

选项:
  --polaris-server <地址>       北极星服务端地址 (默认: 127.0.0.1)
  --polaris-token <令牌>        北极星鉴权令牌 (默认: 空)
  --only <列表>                 仅运行指定 demo，逗号分隔 (lane,metadata,nearby,rule)
  --skip <列表>                 跳过指定 demo，逗号分隔
  --continue-on-failure         任何失败都继续跑完其余 demo (默认行为)
  --stop-on-first-failure       遇到第一个失败立即停止
  --debug                       透传 --debug 给子脚本（影响 SDK 日志级别）
  -h, --help                    展示本帮助

示例:
  $0                                     # 跑全部 4 个 demo
  $0 --only rule,nearby                  # 只跑 rule 和 nearby
  $0 --skip lane --polaris-server 1.2.3.4
  $0 --stop-on-first-failure
EOF
            exit 0
            ;;
        *)
            echo -e "${RED}未知参数: $1${NC}"; exit 1 ;;
    esac
done

# ======================== 全局变量 ========================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AGG_LOG_DIR="${SCRIPT_DIR}/.logs"
AGG_LOG_FILE="${AGG_LOG_DIR}/run_all_tests-$(date +%Y%m%d_%H%M%S).log"

mkdir -p "$AGG_LOG_DIR"

# ======================== 日志工具 ========================
log_info()  { echo -e "${GREEN}[INFO]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*"; }

log_step() {
    echo ""
    echo -e "${CYAN}═══════════════════════════════════════════════════════════════════${NC}"
    echo -e "${CYAN}  $*${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════════════════════════${NC}"
}

# 把 stdout/stderr 同时写到聚合日志（保留颜色，日志里剥离 ANSI）
{
    echo "===== examples/route 聚合测试日志 $(date '+%Y-%m-%d %H:%M:%S') ====="
    echo "Command: $0 $*"
    echo ""
} > "$AGG_LOG_FILE"
exec > >(tee >(sed -u 's/\x1b\[[0-9;]*m//g' >> "$AGG_LOG_FILE")) 2>&1

# ======================== demo 定义 ========================
# 每个 demo 一个字符串（pipe 分隔）：name|script_path|args
#
# 注意 POLARIS_TOKEN 的处理：
#   子 verify_*.sh 用 set -u，解析 `--polaris-token "$2"` 时如果 $2 是空串仍可跑，
#   但如果 `--polaris-token` 后面没有任何 token (例如 shell 自身 IFS 把空串吃掉)，
#   `shift 2` 会触发 unbound variable 报错导致整个子脚本立刻退出。
#   这里仅当 POLARIS_TOKEN 非空时才拼接 `--polaris-token <值>` 参数，空则省略。
_TOKEN_ARG=""
if [[ -n "$POLARIS_TOKEN" ]]; then
    _TOKEN_ARG="--polaris-token ${POLARIS_TOKEN}"
fi

DEMOS=(
    "lane|${SCRIPT_DIR}/lane/lane-test.sh|all ${POLARIS_SERVER}"
    "metadata|${SCRIPT_DIR}/metadata/verify_metadata_route.sh|--polaris-server ${POLARIS_SERVER} ${_TOKEN_ARG}"
    "nearby|${SCRIPT_DIR}/nearby/verify_nearby_route.sh|--polaris-server ${POLARIS_SERVER} ${_TOKEN_ARG}"
    "rule|${SCRIPT_DIR}/rule/verify_rule_route.sh|--polaris-server ${POLARIS_SERVER} ${_TOKEN_ARG}"
    "mixed|${SCRIPT_DIR}/mixed/run.sh|all ${POLARIS_SERVER}"
)

# 是否运行某个 demo（依据 --only / --skip）
should_run_demo() {
    local name="$1"
    if [[ -n "$ONLY_LIST" ]]; then
        # 只保留白名单内的
        local found=false
        local IFS=','
        # shellcheck disable=SC2206
        local arr=($ONLY_LIST)
        for x in "${arr[@]}"; do
            [[ "$(echo "$x" | tr -d ' ')" == "$name" ]] && found=true && break
        done
        [[ "$found" == "true" ]] || return 1
    fi
    if [[ -n "$SKIP_LIST" ]]; then
        local IFS=','
        # shellcheck disable=SC2206
        local arr=($SKIP_LIST)
        for x in "${arr[@]}"; do
            [[ "$(echo "$x" | tr -d ' ')" == "$name" ]] && return 1
        done
    fi
    return 0
}

# 从子脚本输出提取验证结论；返回值写入全局 DEMO_VERDICT
#   PASS   = 成功
#   PARTIAL= 部分通过
#   FAIL   = 失败
#   UNKNOWN= 无法识别（通常是脚本崩溃或无 conclusion 输出）
extract_verdict() {
    local exit_code="$1"
    local sub_log="$2"

    # lane-test.sh 不打印 "验证结论" 行，用退出码判定
    # 其它 verify_*.sh 以 "验证结论: ✅/❌/⚠️" 字样表示结论
    if grep -qE "验证结论.*✅" "$sub_log" 2>/dev/null; then
        DEMO_VERDICT="PASS"
        return
    fi
    if grep -qE "验证结论.*❌" "$sub_log" 2>/dev/null; then
        DEMO_VERDICT="FAIL"
        return
    fi
    if grep -qE "验证结论.*⚠" "$sub_log" 2>/dev/null; then
        DEMO_VERDICT="PARTIAL"
        return
    fi

    # lane-test.sh 以 "所有泳道路由测试用例通过" / "有 N 个测试用例失败" 收尾
    if grep -qE "所有泳道路由测试用例通过" "$sub_log" 2>/dev/null; then
        DEMO_VERDICT="PASS"
        return
    fi
    if grep -qE "有 [0-9]+ 个测试用例失败" "$sub_log" 2>/dev/null; then
        DEMO_VERDICT="FAIL"
        return
    fi

    # 未找到任何结论行：用退出码兜底
    if [[ "$exit_code" == "0" ]]; then
        DEMO_VERDICT="PASS"
    else
        DEMO_VERDICT="FAIL"
    fi
}

# ======================== 执行单个 demo ========================
# 输出：填充 RESULT_* 全局变量
run_demo() {
    local name="$1"
    local script="$2"
    local args="$3"

    log_step "运行 demo: ${name}"
    log_info "入口脚本: ${script}"
    log_info "参数:     ${args}"

    if [[ ! -f "$script" ]] || [[ ! -x "$script" ]]; then
        log_error "找不到或不可执行: ${script}"
        DEMO_VERDICT="FAIL"
        DEMO_DURATION="0"
        DEMO_SUBLOG=""
        return 1
    fi

    # demo 专用的聚合子日志
    local sub_log="${AGG_LOG_DIR}/${name}-$(date +%Y%m%d_%H%M%S).log"

    local start_ts end_ts duration exit_code
    start_ts=$(date +%s)

    # 以子 shell 运行脚本；输出同时落到屏幕 + 子日志 + 聚合总日志。
    # bash -c 解决脚本可能用 "exec >" 重定向的干扰；--debug 透传由调用方预先处理。
    set +e
    ( # shellcheck disable=SC2086
      bash "$script" $args ) 2>&1 | tee "$sub_log"
    exit_code=${PIPESTATUS[0]}
    set -e 2>/dev/null || true

    end_ts=$(date +%s)
    duration=$((end_ts - start_ts))

    extract_verdict "$exit_code" "$sub_log"

    case "$DEMO_VERDICT" in
        PASS)    log_info  "demo [${name}] 结论: ${GREEN}PASS${NC} (exit=${exit_code}, 耗时 ${duration}s)" ;;
        PARTIAL) log_warn  "demo [${name}] 结论: ${YELLOW}PARTIAL${NC} (exit=${exit_code}, 耗时 ${duration}s)" ;;
        FAIL)    log_error "demo [${name}] 结论: ${RED}FAIL${NC} (exit=${exit_code}, 耗时 ${duration}s)" ;;
    esac

    DEMO_DURATION="$duration"
    DEMO_SUBLOG="$sub_log"
    DEMO_EXIT_CODE="$exit_code"
}

# ======================== 主流程 ========================
main() {
    echo ""
    echo -e "${BLUE}╔═══════════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║           examples/route 路由 demo 聚合测试脚本                  ║${NC}"
    echo -e "${BLUE}╚═══════════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "配置:"
    echo "  北极星服务端:       ${POLARIS_SERVER}"
    echo "  北极星 Token:       ${POLARIS_TOKEN:-（未设置）}"
    echo "  only 列表:          ${ONLY_LIST:-（全部）}"
    echo "  skip 列表:          ${SKIP_LIST:-（无）}"
    echo "  失败时继续:         ${CONTINUE_ON_FAILURE}"
    echo "  聚合日志:           ${AGG_LOG_FILE}"
    echo ""

    # 依赖检查
    if ! command -v go &> /dev/null; then
        log_error "Go 未安装"
        exit 1
    fi
    if ! command -v python3 &> /dev/null; then
        log_error "python3 未安装（子脚本依赖）"
        exit 1
    fi

    # ==================== 按序执行 ====================
    local -a RUN_NAMES
    local -a RUN_VERDICTS
    local -a RUN_DURATIONS
    local -a RUN_LOGS
    local -a RUN_EXITS

    for entry in "${DEMOS[@]}"; do
        IFS='|' read -r name script args <<< "$entry"

        if ! should_run_demo "$name"; then
            log_info "跳过 demo: ${name}（未在 --only 中，或被 --skip 排除）"
            RUN_NAMES+=("$name")
            RUN_VERDICTS+=("SKIP")
            RUN_DURATIONS+=("0")
            RUN_LOGS+=("-")
            RUN_EXITS+=("-")
            continue
        fi

        DEMO_VERDICT=""
        DEMO_DURATION=""
        DEMO_SUBLOG=""
        DEMO_EXIT_CODE=""
        run_demo "$name" "$script" "$args"

        RUN_NAMES+=("$name")
        RUN_VERDICTS+=("$DEMO_VERDICT")
        RUN_DURATIONS+=("$DEMO_DURATION")
        RUN_LOGS+=("$DEMO_SUBLOG")
        RUN_EXITS+=("$DEMO_EXIT_CODE")

        if [[ "$DEMO_VERDICT" == "FAIL" ]] && [[ "$CONTINUE_ON_FAILURE" != "true" ]]; then
            log_warn "demo [${name}] FAIL 且未开启 --continue-on-failure，提前终止"
            break
        fi
    done

    # ==================== 汇总 ====================
    log_step "汇总结果"

    local total=0 pass=0 partial=0 fail=0 skip=0
    for v in "${RUN_VERDICTS[@]}"; do
        case "$v" in
            PASS)    pass=$((pass+1));    total=$((total+1)) ;;
            PARTIAL) partial=$((partial+1)); total=$((total+1)) ;;
            FAIL)    fail=$((fail+1));    total=$((total+1)) ;;
            SKIP)    skip=$((skip+1)) ;;
        esac
    done

    # 成功率（不含 SKIP）
    local success_rate="N/A"
    if [[ "$total" -gt 0 ]]; then
        success_rate=$(awk -v p="$pass" -v t="$total" 'BEGIN { printf "%.1f%%", (p*100.0)/t }')
    fi

    echo ""
    echo -e "${BLUE}╔═══════════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║                    examples/route 测试结果汇总                   ║${NC}"
    echo -e "${BLUE}╚═══════════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    printf "  %-10s %-12s %-10s %-10s %s\n" "Demo" "结论" "退出码" "耗时(秒)" "日志"
    printf "  %-10s %-12s %-10s %-10s %s\n" "----------" "------------" "----------" "----------" "----------------------------------------"
    local i=0
    local n=${#RUN_NAMES[@]}
    while [[ $i -lt $n ]]; do
        local name="${RUN_NAMES[$i]}"
        local verdict="${RUN_VERDICTS[$i]}"
        local dur="${RUN_DURATIONS[$i]}"
        local sublog="${RUN_LOGS[$i]}"
        local ec="${RUN_EXITS[$i]}"
        local color="$NC"
        case "$verdict" in
            PASS)    color="$GREEN" ;;
            PARTIAL) color="$YELLOW" ;;
            FAIL)    color="$RED" ;;
            SKIP)    color="$CYAN" ;;
        esac
        printf "  %-10s ${color}%-12s${NC} %-10s %-10s %s\n" \
            "$name" "$verdict" "$ec" "$dur" "$sublog"
        i=$((i+1))
    done
    echo ""
    echo "  总数(不含 SKIP): ${total},  PASS=${pass},  PARTIAL=${partial},  FAIL=${fail},  SKIP=${skip}"
    echo "  成功率:          ${success_rate}"
    echo "  聚合日志:        ${AGG_LOG_FILE}"
    echo ""

    # ==================== 失败用例列表 ====================
    if [[ "$fail" -gt 0 ]] || [[ "$partial" -gt 0 ]]; then
        echo -e "${YELLOW}── 失败/部分通过 demo 列表 ──${NC}"
        i=0
        while [[ $i -lt $n ]]; do
            local v="${RUN_VERDICTS[$i]}"
            if [[ "$v" == "FAIL" ]] || [[ "$v" == "PARTIAL" ]]; then
                echo -e "  - ${RUN_NAMES[$i]}: ${v}  (查看: cat ${RUN_LOGS[$i]})"
            fi
            i=$((i+1))
        done
        echo ""
    fi

    # ==================== 最终结论 ====================
    if [[ "$total" -eq 0 ]]; then
        echo -e "${YELLOW}未运行任何 demo，请检查 --only / --skip 配置${NC}"
        exit 0
    fi

    if [[ "$fail" -eq 0 ]] && [[ "$partial" -eq 0 ]]; then
        echo -e "${GREEN}╔═══════════════════════════════════════════════════════════════════╗${NC}"
        echo -e "${GREEN}║  最终结论: ✅ 全部 ${total} 个路由 demo 验证通过！                           ║${NC}"
        echo -e "${GREEN}╚═══════════════════════════════════════════════════════════════════╝${NC}"
        exit 0
    elif [[ "$fail" -gt 0 ]]; then
        echo -e "${RED}╔═══════════════════════════════════════════════════════════════════╗${NC}"
        echo -e "${RED}║  最终结论: ❌ ${fail} 个 demo 失败，${partial} 个部分通过                           ║${NC}"
        echo -e "${RED}╚═══════════════════════════════════════════════════════════════════╝${NC}"
        # 退出码 = 失败数量 + 部分通过数量（让 CI 能快速识别）
        exit $((fail + partial))
    else
        echo -e "${YELLOW}╔═══════════════════════════════════════════════════════════════════╗${NC}"
        echo -e "${YELLOW}║  最终结论: ⚠️  ${partial} 个 demo 部分通过，请检查详细日志                       ║${NC}"
        echo -e "${YELLOW}╚═══════════════════════════════════════════════════════════════════╝${NC}"
        exit $partial
    fi
}

main "$@"
