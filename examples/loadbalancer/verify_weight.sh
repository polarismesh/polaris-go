#!/bin/bash
# =============================================================================
# 负载均衡权重验证脚本
#
# 使用方法:
#   chmod +x verify_weight.sh
#   ./verify_weight.sh [--polaris-server <地址>] [--polaris-token <令牌>]
#                      [--service <服务名>] [--namespace <命名空间>]
#                      [--observe-seconds <观察秒数>]
#                      [--request-interval <请求间隔秒数>]
#
# 前置条件:
#   1. 北极星服务端(Polaris Server)已启动
#   2. Go 环境已安装
#
# 验证流程:
#   1. 编译 provider 和 consumer
#   2. 环境准备
#   3. 启动 consumer
#   4. 启动 Provider1 和 Provider2
#   5. 等待实例注册到北极星
#   6. 持续发起请求，统计流量在两个实例间的分布
#   7. 验证流量分布是否符合权重配置的预期
# =============================================================================

set -euo pipefail

# ======================== 默认配置 ========================
POLARIS_SERVER="${POLARIS_SERVER:-127.0.0.1}"
POLARIS_TOKEN="${POLARIS_TOKEN:-}"
SERVICE_NAME="${SERVICE_NAME:-LoadBalanceEchoServer}"
NAMESPACE="${NAMESPACE:-default}"
CONSUMER_PORT="${CONSUMER_PORT:-17080}"
PROVIDER1_PORT="${PROVIDER1_PORT:-0}"    # 0 表示自动分配
PROVIDER2_PORT="${PROVIDER2_PORT:-0}"    # 0 表示自动分配
OBSERVE_SECONDS="${OBSERVE_SECONDS:-60}"
REQUEST_INTERVAL="${REQUEST_INTERVAL:-1}"
LB_POLICY="${LB_POLICY:-weightedRandom}"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # 无颜色

# ======================== 解析命令行参数 ========================
while [[ $# -gt 0 ]]; do
    case "$1" in
        --polaris-server)
            POLARIS_SERVER="$2"; shift 2 ;;
        --polaris-token)
            POLARIS_TOKEN="$2"; shift 2 ;;
        --service)
            SERVICE_NAME="$2"; shift 2 ;;
        --namespace)
            NAMESPACE="$2"; shift 2 ;;
        --consumer-port)
            CONSUMER_PORT="$2"; shift 2 ;;
        --provider1-port)
            PROVIDER1_PORT="$2"; shift 2 ;;
        --provider2-port)
            PROVIDER2_PORT="$2"; shift 2 ;;
        --observe-seconds)
            OBSERVE_SECONDS="$2"; shift 2 ;;
        --request-interval)
            REQUEST_INTERVAL="$2"; shift 2 ;;
        --lb-policy)
            LB_POLICY="$2"; shift 2 ;;
        --help|-h)
            echo "用法: $0 [选项]"
            echo ""
            echo "选项:"
            echo "  --polaris-server <地址>     北极星服务端地址 (默认: 127.0.0.1)"
            echo "  --polaris-token <令牌>      北极星鉴权令牌 (默认: 空)"
            echo "  --service <服务名>          目标服务名 (默认: LoadBalanceEchoServer)"
            echo "  --namespace <命名空间>      命名空间 (默认: default)"
            echo "  --consumer-port <端口>      Consumer HTTP端口 (默认: 18080)"
            echo "  --provider1-port <端口>     Provider1 端口 (默认: 自动分配)"
            echo "  --provider2-port <端口>     Provider2 端口 (默认: 自动分配)"
            echo "  --observe-seconds <秒>      观察时长 (默认: 60)"
            echo "  --request-interval <秒>     请求间隔 (默认: 1)"
            echo "  --lb-policy <策略>          负载均衡策略 (默认: weightedRandom)"
            exit 0
            ;;
        *)
            echo -e "${RED}未知参数: $1${NC}"; exit 1 ;;
    esac
done

# ======================== 全局变量 ========================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONSUMER_DIR="${SCRIPT_DIR}/consumer"
PROVIDER_DIR="${SCRIPT_DIR}/provider"
BUILD_DIR="${SCRIPT_DIR}/.build"
LOG_DIR="${SCRIPT_DIR}/.logs"
RESULT_FILE="${LOG_DIR}/weight_result.csv"

CONSUMER_PID=""
PROVIDER1_PID=""
PROVIDER2_PID=""

# ======================== 清理函数 ========================
cleanup() {
    log_info "正在清理进程..."
    for pid_var in CONSUMER_PID PROVIDER1_PID PROVIDER2_PID; do
        local pid="${!pid_var}"
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            log_info "停止进程 $pid_var (PID: $pid)"
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
        fi
    done
    log_info "清理完成"
}

trap cleanup EXIT

# ======================== 工具函数 ========================

log_info() {
    echo -e "${GREEN}[INFO]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*"
}

log_step() {
    echo ""
    echo -e "${CYAN}========================================${NC}"
    echo -e "${CYAN}  步骤: $*${NC}"
    echo -e "${CYAN}========================================${NC}"
}

# 检查进程是否存活
check_process_alive() {
    local pid="$1"
    local name="${2:-进程}"
    if ! kill -0 "$pid" 2>/dev/null; then
        log_error "${name} (PID: $pid) 已异常退出"
        wait "$pid" 2>/dev/null
        local exit_code=$?
        if [[ $exit_code -ne 0 ]]; then
            log_error "${name} 退出码: $exit_code"
        fi
        return 1
    fi
    return 0
}

# 等待 HTTP 服务就绪（同时检查进程是否存活）
wait_for_http() {
    local url="$1"
    local max_wait="${2:-30}"
    local desc="${3:-服务}"
    local pid="${4:-}"
    local waited=0

    while [[ $waited -lt $max_wait ]]; do
        if [[ -n "$pid" ]] && ! kill -0 "$pid" 2>/dev/null; then
            log_error "${desc} 进程 (PID: $pid) 已退出，无需继续等待"
            return 1
        fi
        if curl -s --connect-timeout 2 "$url" > /dev/null 2>&1; then
            log_info "${desc} 已就绪 ($url)"
            return 0
        fi
        sleep 1
        waited=$((waited + 1))
    done
    log_error "${desc} 未就绪 ($url)，等待了 ${max_wait}s"
    return 1
}

# 从日志中提取 provider 的实际监听端口
extract_port_from_log() {
    local log_file="$1"
    local max_wait="${2:-15}"
    local waited=0

    while [[ $waited -lt $max_wait ]]; do
        if [[ -f "$log_file" ]]; then
            local port
            port=$(grep 'listen port is' "$log_file" 2>/dev/null | sed 's/.*listen port is \([0-9]*\).*/\1/' | head -1)
            if [[ -n "$port" ]]; then
                echo "$port"
                return 0
            fi
        fi
        sleep 1
        waited=$((waited + 1))
    done
    return 1
}

# ======================== 生成临时 polaris.yaml ========================
generate_polaris_yaml() {
    local target_dir="$1"
    local config_type="$2"
    local yaml_file="${target_dir}/polaris.yaml"

    if [[ "$config_type" == "consumer" ]]; then
        cat > "$yaml_file" <<EOF
global:
  serverConnector:
    addresses:
      - ${POLARIS_SERVER}:8091
    token: ${POLARIS_TOKEN}
  connectTimeout: 5000ms
  api:
    timeout: 5s
    maxRetryTimes: 2
    retryInterval: 1s
  eventReporter:
    enable: true
    chain:
      - pushgateway
  statReporter:
    enable: true
    chain:
      - prometheus
    plugin:
      prometheus:
        address: ${POLARIS_SERVER}:9091
        type: push
consumer:
  loadbalancer:
    type: ${LB_POLICY}
EOF
    elif [[ "$config_type" == "provider" ]]; then
        cat > "$yaml_file" <<EOF
global:
  serverConnector:
    addresses:
      - ${POLARIS_SERVER}:8091
    token: ${POLARIS_TOKEN}
    connectTimeout: 5000ms
  api:
    timeout: 5s
    maxRetryTimes: 2
    retryInterval: 1s
  eventReporter:
    enable: true
    chain:
      - pushgateway
EOF
    fi
    log_info "生成 polaris.yaml -> $yaml_file"
}

# ======================== 主流程 ========================

main() {
    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║        负载均衡权重验证脚本                      ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "配置信息:"
    echo "  北极星服务端:     ${POLARIS_SERVER}:8091"
    echo "  服务名:           ${SERVICE_NAME}"
    echo "  命名空间:         ${NAMESPACE}"
    echo "  Consumer端口:     ${CONSUMER_PORT}"
    echo "  负载均衡策略:     ${LB_POLICY}"
    echo "  观察时长:         ${OBSERVE_SECONDS}s"
    echo "  请求间隔:         ${REQUEST_INTERVAL}s"
    echo ""

    # ==================== 步骤1: 环境准备 ====================
    log_step "1/7 环境准备"

    mkdir -p "$BUILD_DIR" "$LOG_DIR"

    if ! command -v go &> /dev/null; then
        log_error "Go 未安装，请先安装 Go"
        exit 1
    fi
    log_info "Go 版本: $(go version)"

    if [[ ! -f "${CONSUMER_DIR}/main.go" ]]; then
        log_error "找不到 Consumer 源码: ${CONSUMER_DIR}/main.go"
        exit 1
    fi
    if [[ ! -f "${PROVIDER_DIR}/main.go" ]]; then
        log_error "找不到 Provider 源码: ${PROVIDER_DIR}/main.go"
        exit 1
    fi
    log_info "源码检查通过"

    # ==================== 步骤2: 编译 ====================
    log_step "2/7 编译 Provider 和 Consumer"

    log_info "编译 Provider..."
    (cd "$PROVIDER_DIR" && go build -o "${BUILD_DIR}/provider" .)
    log_info "Provider 编译完成 -> ${BUILD_DIR}/provider"

    log_info "编译 Consumer..."
    (cd "$CONSUMER_DIR" && go build -o "${BUILD_DIR}/consumer" .)
    log_info "Consumer 编译完成 -> ${BUILD_DIR}/consumer"

    # macOS Gatekeeper 可能会阻止执行未签名的二进制文件
    if command -v xattr &> /dev/null; then
        xattr -c "${BUILD_DIR}/provider" 2>/dev/null || true
        xattr -c "${BUILD_DIR}/consumer" 2>/dev/null || true
        log_info "已清除二进制文件的 macOS quarantine 属性"
    fi

    # 生成 polaris.yaml
    generate_polaris_yaml "$BUILD_DIR" "provider"
    cp "${BUILD_DIR}/polaris.yaml" "${BUILD_DIR}/polaris_provider.yaml"
    generate_polaris_yaml "$BUILD_DIR" "consumer"
    cp "${BUILD_DIR}/polaris.yaml" "${BUILD_DIR}/polaris_consumer.yaml"

    # ==================== 步骤3: 启动 Consumer ====================
    log_step "3/7 启动 Consumer"

    local consumer_workdir="${BUILD_DIR}/consumer_run"
    mkdir -p "$consumer_workdir"
    cp "${BUILD_DIR}/polaris_consumer.yaml" "${consumer_workdir}/polaris.yaml"

    local consumer_log="${LOG_DIR}/consumer.log"
    (cd "$consumer_workdir" && "${BUILD_DIR}/consumer" \
        --namespace "$NAMESPACE" \
        --service "$SERVICE_NAME" \
        --port "$CONSUMER_PORT" \
        --lbPolicy "$LB_POLICY" \
        --config "./polaris.yaml" \
        > "$consumer_log" 2>&1) &
    CONSUMER_PID=$!
    log_info "Consumer 已启动 (PID: $CONSUMER_PID)"

    sleep 1
    check_process_alive "$CONSUMER_PID" "Consumer" || {
        log_error "Consumer 启动失败，请检查日志: $consumer_log"
        cat "$consumer_log" 2>/dev/null || true
        exit 1
    }

    # ==================== 步骤4: 启动 Provider1 和 Provider2 ====================
    log_step "4/7 启动 Provider1 和 Provider2"

    # --- Provider1 ---
    local provider1_workdir="${BUILD_DIR}/provider1"
    mkdir -p "$provider1_workdir"
    cp "${BUILD_DIR}/polaris_provider.yaml" "${provider1_workdir}/polaris.yaml"

    local provider1_log="${LOG_DIR}/provider1.log"
    (cd "$provider1_workdir" && "${BUILD_DIR}/provider" \
        --namespace "$NAMESPACE" \
        --service "$SERVICE_NAME" \
        --token "$POLARIS_TOKEN" \
        --port "$PROVIDER1_PORT" \
        --config "./polaris.yaml" \
        > "$provider1_log" 2>&1) &
    PROVIDER1_PID=$!
    log_info "Provider1 已启动 (PID: $PROVIDER1_PID)"

    # --- Provider2 ---
    local provider2_workdir="${BUILD_DIR}/provider2"
    mkdir -p "$provider2_workdir"
    cp "${BUILD_DIR}/polaris_provider.yaml" "${provider2_workdir}/polaris.yaml"

    local provider2_log="${LOG_DIR}/provider2.log"
    (cd "$provider2_workdir" && "${BUILD_DIR}/provider" \
        --namespace "$NAMESPACE" \
        --service "$SERVICE_NAME" \
        --token "$POLARIS_TOKEN" \
        --port "$PROVIDER2_PORT" \
        --config "./polaris.yaml" \
        > "$provider2_log" 2>&1) &
    PROVIDER2_PID=$!
    log_info "Provider2 已启动 (PID: $PROVIDER2_PID)"

    sleep 1
    check_process_alive "$PROVIDER1_PID" "Provider1" || {
        log_error "Provider1 启动失败，请检查日志: $provider1_log"
        cat "$provider1_log" 2>/dev/null || true
        exit 1
    }
    check_process_alive "$PROVIDER2_PID" "Provider2" || {
        log_error "Provider2 启动失败，请检查日志: $provider2_log"
        cat "$provider2_log" 2>/dev/null || true
        exit 1
    }

    # 获取实际端口
    local p1_port p2_port

    if [[ "$PROVIDER1_PORT" != "0" ]]; then
        p1_port="$PROVIDER1_PORT"
    else
        p1_port=$(extract_port_from_log "$provider1_log" 20) || {
            log_error "无法获取 Provider1 端口，请检查日志: $provider1_log"
            cat "$provider1_log" 2>/dev/null || true
            exit 1
        }
    fi
    log_info "Provider1 实际监听端口: ${p1_port}"

    if [[ "$PROVIDER2_PORT" != "0" ]]; then
        p2_port="$PROVIDER2_PORT"
    else
        p2_port=$(extract_port_from_log "$provider2_log" 20) || {
            log_error "无法获取 Provider2 端口，请检查日志: $provider2_log"
            cat "$provider2_log" 2>/dev/null || true
            exit 1
        }
    fi
    log_info "Provider2 实际监听端口: ${p2_port}"

    # 等待 Provider HTTP 就绪
    wait_for_http "http://127.0.0.1:${p1_port}/echo" 20 "Provider1" "$PROVIDER1_PID" || exit 1
    wait_for_http "http://127.0.0.1:${p2_port}/echo" 20 "Provider2" "$PROVIDER2_PID" || exit 1

    # ==================== 步骤5: 等待实例注册到北极星 ====================
    log_step "5/7 等待实例注册到北极星并验证链路"

    log_info "等待实例注册同步（10s）..."
    sleep 10

    # 等待 Consumer 就绪
    wait_for_http "http://127.0.0.1:${CONSUMER_PORT}/echo" 30 "Consumer" "$CONSUMER_PID" || exit 1

    # 发起基线请求验证链路
    log_info "发起基线请求（验证链路正常）..."
    local baseline_ok=0
    for i in $(seq 1 5); do
        local http_code resp
        http_code=$(curl -s -o /tmp/_weight_resp_$$.tmp -w '%{http_code}' --connect-timeout 5 \
            "http://127.0.0.1:${CONSUMER_PORT}/echo" 2>/dev/null || echo "000")
        resp=$(cat /tmp/_weight_resp_$$.tmp 2>/dev/null || echo "FAILED")
        rm -f /tmp/_weight_resp_$$.tmp
        if [[ "$http_code" == "200" ]]; then
            log_info "基线请求 #${i}: [HTTP ${http_code}] ${resp}"
            baseline_ok=$((baseline_ok + 1))
        else
            log_warn "基线请求 #${i} 失败: [HTTP ${http_code}] ${resp}"
        fi
        sleep 0.5
    done

    if [[ $baseline_ok -eq 0 ]]; then
        log_error "所有基线请求失败，请检查配置和日志"
        log_error "Consumer 日志: $consumer_log"
        log_error "Provider1 日志: $provider1_log"
        log_error "Provider2 日志: $provider2_log"
        exit 1
    fi
    log_info "基线请求通过 (${baseline_ok}/5 成功)，链路正常"

    # ==================== 步骤6: 持续请求并统计流量分布 ====================
    log_step "6/7 持续请求并统计流量分布（观察期: ${OBSERVE_SECONDS}s）"

    log_info "Provider1 端口: ${p1_port}, Provider2 端口: ${p2_port}"

    # 初始化结果文件
    echo "时间戳,已运行秒数,Provider1(${p1_port})计数,Provider2(${p2_port})计数,Provider1占比(%),Provider2占比(%)" > "$RESULT_FILE"

    # 统计变量
    local p1_count=0
    local p2_count=0
    local other_count=0
    local total_count=0
    local fail_count=0

    # 区间统计（每5秒输出一次）
    local interval_p1=0
    local interval_p2=0
    local interval_total=0

    local start_ts
    start_ts=$(date +%s)
    local last_stat_ts=$start_ts

    echo ""
    printf "${BLUE}%-8s | %-14s | %-14s | %-12s | %-12s | %-18s | %-18s${NC}\n" \
        "已运行" "Provider1" "Provider2" "P1占比" "P2占比" "区间P1占比" "区间P2占比"
    printf "%-8s-+-%-14s-+-%-14s-+-%-12s-+-%-12s-+-%-18s-+-%-18s\n" \
        "--------" "--------------" "--------------" "------------" "------------" "------------------" "------------------"

    local end_ts=$((start_ts + OBSERVE_SECONDS))

    while true; do
        local now_ts
        now_ts=$(date +%s)
        if [[ $now_ts -ge $end_ts ]]; then
            break
        fi

        # 检查进程存活
        check_process_alive "$CONSUMER_PID" "Consumer" || { log_error "Consumer 异常退出"; break; }
        check_process_alive "$PROVIDER1_PID" "Provider1" || { log_error "Provider1 异常退出"; break; }
        check_process_alive "$PROVIDER2_PID" "Provider2" || { log_error "Provider2 异常退出"; break; }

        # 发起请求
        local http_code resp
        http_code=$(curl -s -o /tmp/_weight_resp_$$.tmp -w '%{http_code}' --connect-timeout 5 \
            "http://127.0.0.1:${CONSUMER_PORT}/echo" 2>/dev/null || echo "000")
        resp=$(cat /tmp/_weight_resp_$$.tmp 2>/dev/null || echo "")
        rm -f /tmp/_weight_resp_$$.tmp

        total_count=$((total_count + 1))
        interval_total=$((interval_total + 1))

        if [[ "$http_code" == "200" ]]; then
            # 从响应中解析实例端口
            # 响应格式: "Hello, I'm LoadBalanceEchoServer Provider, My host : <host>:<port>"
            local resp_port
            resp_port=$(echo "$resp" | grep -o ':[0-9]*$' | sed 's/://' | tr -d '[:space:]')

            if [[ "$resp_port" == "$p1_port" ]]; then
                p1_count=$((p1_count + 1))
                interval_p1=$((interval_p1 + 1))
            elif [[ "$resp_port" == "$p2_port" ]]; then
                p2_count=$((p2_count + 1))
                interval_p2=$((interval_p2 + 1))
            else
                other_count=$((other_count + 1))
            fi
        else
            fail_count=$((fail_count + 1))
        fi

        # 每10秒输出一次统计
        now_ts=$(date +%s)
        if [[ $((now_ts - last_stat_ts)) -ge 10 ]]; then
            local elapsed=$((now_ts - start_ts))

            local p1_ratio="0.0" p2_ratio="0.0"
            if [[ $total_count -gt 0 ]]; then
                p1_ratio=$(awk "BEGIN {printf \"%.1f\", $p1_count * 100.0 / $total_count}")
                p2_ratio=$(awk "BEGIN {printf \"%.1f\", $p2_count * 100.0 / $total_count}")
            fi

            local ip1_ratio="0.0" ip2_ratio="0.0"
            if [[ $interval_total -gt 0 ]]; then
                ip1_ratio=$(awk "BEGIN {printf \"%.1f\", $interval_p1 * 100.0 / $interval_total}")
                ip2_ratio=$(awk "BEGIN {printf \"%.1f\", $interval_p2 * 100.0 / $interval_total}")
            fi

            printf "%-8s | %-14s | %-14s | %-12s | %-12s | %-18s | %-18s\n" \
                "${elapsed}s" "$p1_count" "$p2_count" "${p1_ratio}%" "${p2_ratio}%" "${ip1_ratio}%" "${ip2_ratio}%"

            # 写入 CSV
            echo "$(date '+%Y-%m-%d %H:%M:%S'),${elapsed},${p1_count},${p2_count},${p1_ratio},${p2_ratio}" >> "$RESULT_FILE"

            # 重置区间统计
            interval_p1=0
            interval_p2=0
            interval_total=0
            last_stat_ts=$now_ts
        fi

        sleep "$REQUEST_INTERVAL"
    done

    # 最终统计输出
    local final_elapsed=$(($(date +%s) - start_ts))
    local final_p1_ratio="0.0" final_p2_ratio="0.0"
    if [[ $total_count -gt 0 ]]; then
        final_p1_ratio=$(awk "BEGIN {printf \"%.1f\", $p1_count * 100.0 / $total_count}")
        final_p2_ratio=$(awk "BEGIN {printf \"%.1f\", $p2_count * 100.0 / $total_count}")
    fi

    printf "%-8s | %-14s | %-14s | %-12s | %-12s | %-18s | %-18s\n" \
        "${final_elapsed}s" "$p1_count" "$p2_count" "${final_p1_ratio}%" "${final_p2_ratio}%" "---" "---"
    echo "$(date '+%Y-%m-%d %H:%M:%S'),${final_elapsed},${p1_count},${p2_count},${final_p1_ratio},${final_p2_ratio}" >> "$RESULT_FILE"

    # ==================== 步骤7: 验证结果汇总 ====================
    log_step "7/7 验证结果汇总"

    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║                  验证结果汇总                    ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "  负载均衡策略:      ${LB_POLICY}"
    echo "  总请求数:          ${total_count}"
    echo "  请求失败:          ${fail_count}"
    echo "  Provider1 (端口${p1_port}): ${p1_count} 次 (${final_p1_ratio}%)"
    echo "  Provider2 (端口${p2_port}): ${p2_count} 次 (${final_p2_ratio}%)"
    echo "  其他/未匹配:       ${other_count} 次"
    echo ""
    echo "  流量分布详情 CSV:  ${RESULT_FILE}"
    echo "  Consumer 日志:     ${consumer_log}"
    echo "  Provider1 日志:    ${provider1_log}"
    echo "  Provider2 日志:    ${provider2_log}"
    echo ""

    # ==================== 验证权重效果 ====================
    if [[ $total_count -eq 0 ]]; then
        log_error "没有成功的请求，无法验证权重分布"
        echo -e "${RED}验证结论: ❌ 验证失败，没有成功的请求${NC}"
    elif [[ $p1_count -eq 0 ]] || [[ $p2_count -eq 0 ]]; then
        log_warn "有一个实例未收到任何请求"
        log_warn "可能原因:"
        log_warn "  1. 实例未成功注册到北极星"
        log_warn "  2. 负载均衡策略配置问题"
        log_warn "  3. 观察时间不足（当前: ${OBSERVE_SECONDS}s）"
        echo ""
        echo -e "${YELLOW}验证结论: ⚠️ 流量分布不均匀，请检查配置${NC}"
    else
        # 检查是否大致均匀（默认权重相同时，两个实例应各约50%）
        local diff
        diff=$(awk "BEGIN {d = $final_p1_ratio - $final_p2_ratio; if (d < 0) d = -d; printf \"%.1f\", d}")
        if awk "BEGIN {exit !($diff <= 20.0)}"; then
            log_info "两个实例的流量差异为 ${diff}%，在合理范围内（<=20%）"
            echo -e "${GREEN}验证结论: ✅ 负载均衡正常！流量在两个实例间合理分布${NC}"
        else
            log_warn "两个实例的流量差异为 ${diff}%，偏差较大（>20%）"
            log_warn "如果两个实例权重相同，这可能表示负载均衡存在问题"
            log_warn "如果两个实例权重不同，请根据权重比例判断是否符合预期"
            echo ""
            echo -e "${YELLOW}验证结论: ⚠️ 流量分布偏差较大，请检查权重配置${NC}"
        fi
    fi

    echo ""
}

main "$@"
