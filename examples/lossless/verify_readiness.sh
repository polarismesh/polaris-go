#!/bin/bash
# =============================================================================
# 就绪检查(Readiness Probe)功能验证脚本
#
# 使用方法:
#   chmod +x verify_readiness.sh
#   ./verify_readiness.sh [--polaris-server <地址>] [--polaris-token <令牌>]
#                         [--service <服务名>] [--namespace <命名空间>]
#                         [--admin-port <Admin端口>]
#                         [--observe-seconds <观察秒数>]
#                         [--check-interval <检查间隔秒数>]
#
# 前置条件:
#   1. 北极星服务端(Polaris Server)已启动
#   2. 已在北极星控制台为目标服务配置 LosslessRule（启用延迟注册 + 就绪检查）
#   3. Go 环境已安装
#
# 验证原理:
#   无损上线的就绪检查探针注册在 Admin 服务上（默认端口 28080），路径为 /readiness。
#   - 当实例尚未注册到北极星时，/readiness 返回 HTTP 503 + "UNREGISTERED"
#   - 当实例已注册到北极星后，/readiness 返回 HTTP 200 + "REGISTERED"
#
#   本脚本启动一个 Provider（配置了延迟注册），在延迟注册期间持续轮询 /readiness，
#   验证就绪检查探针在注册前后的状态变化是否符合预期。
#
# 验证流程:
#   1. 编译 provider 和 consumer
#   2. 环境准备
#   3. 启动 consumer（用于获取无损上线规则）
#   4. 获取无损上线规则，确认延迟注册和就绪检查已启用
#   5. 启动 provider（触发延迟注册 + 就绪检查）
#   6. 持续轮询 /readiness 接口，记录状态变化
#   7. 验证结果：注册前应为 UNREGISTERED，注册后应为 REGISTERED
# =============================================================================

set -euo pipefail

# ======================== 默认配置 ========================
POLARIS_SERVER="${POLARIS_SERVER:-127.0.0.1}"
POLARIS_TOKEN="${POLARIS_TOKEN:-}"
SERVICE_NAME="${SERVICE_NAME:-LosslessHealthDelayServer}"
NAMESPACE="${NAMESPACE:-default}"
CONSUMER_PORT="${CONSUMER_PORT:-18080}"
PROVIDER_PORT="${PROVIDER_PORT:-0}"       # 0 表示自动分配
ADMIN_PORT="${ADMIN_PORT:-28080}"         # Admin 服务端口（就绪检查探针所在端口）
OBSERVE_SECONDS="${OBSERVE_SECONDS:-0}"   # 0 表示根据延迟注册时长自动计算
CHECK_INTERVAL="${CHECK_INTERVAL:-1}"     # 就绪检查轮询间隔（秒）
DEBUG_MODE="${DEBUG_MODE:-false}"

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
        --provider-port)
            PROVIDER_PORT="$2"; shift 2 ;;
        --admin-port)
            ADMIN_PORT="$2"; shift 2 ;;
        --observe-seconds)
            OBSERVE_SECONDS="$2"; shift 2 ;;
        --check-interval)
            CHECK_INTERVAL="$2"; shift 2 ;;
        --debug)
            DEBUG_MODE="true"; shift ;;
        --help|-h)
            echo "用法: $0 [选项]"
            echo ""
            echo "选项:"
            echo "  --polaris-server <地址>     北极星服务端地址 (默认: 127.0.0.1)"
            echo "  --polaris-token <令牌>      北极星鉴权令牌 (默认: 空)"
            echo "  --service <服务名>          目标服务名 (默认: LosslessHealthDelayServer)"
            echo "  --namespace <命名空间>      命名空间 (默认: default)"
            echo "  --consumer-port <端口>      Consumer HTTP端口 (默认: 18080)"
            echo "  --provider-port <端口>      Provider 端口 (默认: 自动分配)"
            echo "  --admin-port <端口>         Admin 服务端口 (默认: 28080)"
            echo "  --observe-seconds <秒>      观察时长 (默认: 根据延迟注册时长自动计算)"
            echo "  --check-interval <秒>       就绪检查轮询间隔 (默认: 1)"
            echo "  --debug                     启用 debug 日志 (默认: 关闭)"
            exit 0
            ;;
        *)
            echo -e "${RED}未知参数: $1${NC}"; exit 1 ;;
    esac
done

# ======================== 全局变量 ========================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONSUMER_DIR="${SCRIPT_DIR}/consumer"
PROVIDER_DIR="${SCRIPT_DIR}/provider/healthCheckDelay"
BUILD_DIR="${SCRIPT_DIR}/.build"
LOG_DIR="${SCRIPT_DIR}/.logs"
RESULT_FILE="${LOG_DIR}/readiness_result.csv"

CONSUMER_PID=""
PROVIDER_PID=""

# ======================== 清理函数 ========================
cleanup() {
    log_info "清理进程..."
    if [[ -n "$PROVIDER_PID" ]] && kill -0 "$PROVIDER_PID" 2>/dev/null; then
        kill "$PROVIDER_PID" 2>/dev/null || true
        wait "$PROVIDER_PID" 2>/dev/null || true
        log_info "Provider 已停止 (PID: $PROVIDER_PID)"
    fi
    # Consumer 不在此处清理（可能是复用的已有实例）
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

# 通过 Consumer 的 /lossless-rule 接口获取被调服务的无损上线规则
get_lossless_rule() {
    local consumer_url="http://127.0.0.1:${CONSUMER_PORT}/lossless-rule"
    local resp
    resp=$(curl -s --connect-timeout 5 "$consumer_url" 2>/dev/null || echo "")
    if [[ -z "$resp" ]]; then
        return 1
    fi
    echo "$resp"
    return 0
}

# 从 lossless 规则 JSON 中提取 delayRegister 块的内容
# 注意: Consumer /lossless-rule 接口返回的 JSON 使用驼峰格式字段名（如 delayRegister, intervalSeconds, healthCheckIntervalSeconds）
get_delay_register_block() {
    local rule_json="$1"
    # 提取 delayRegister 块（支持嵌套的花括号不深于一层）
    echo "$rule_json" | grep -oE '"delayRegister":\{[^}]*\}' || echo ""
}

# 从 lossless 规则 JSON 中提取延迟注册策略
get_delay_register_strategy() {
    local rule_json="$1"
    local block
    block=$(get_delay_register_block "$rule_json")
    if [[ -z "$block" ]]; then
        echo ""
        return 0
    fi
    # 检查是否启用
    local enable
    enable=$(echo "$block" | grep -o '"enable":true' || echo "")
    if [[ -z "$enable" ]]; then
        echo ""
        return 0
    fi
    local strategy
    strategy=$(echo "$block" | grep -oE '"strategy":"[^"]+"' | sed 's/"strategy":"\([^"]*\)"/\1/' || echo "")
    echo "$strategy"
    return 0
}

# 从 lossless 规则 JSON 中提取延迟注册的等待秒数（DELAY_BY_TIME 策略使用 intervalSeconds）
get_delay_register_seconds() {
    local rule_json="$1"
    local block
    block=$(get_delay_register_block "$rule_json")
    if [[ -z "$block" ]]; then
        echo "0"
        return 0
    fi
    local enable
    enable=$(echo "$block" | grep -o '"enable":true' || echo "")
    if [[ -z "$enable" ]]; then
        echo "0"
        return 0
    fi
    local interval
    interval=$(echo "$block" | grep -oE '"intervalSeconds":[0-9]+' | sed 's/"intervalSeconds"://' || echo "0")
    if [[ -z "$interval" ]]; then
        interval="0"
    fi
    echo "$interval"
    return 0
}

# 从 lossless 规则 JSON 中提取接口探测延迟的健康检查间隔秒数（DELAY_BY_HEALTH_CHECK 策略）
get_health_check_interval_seconds() {
    local rule_json="$1"
    local block
    block=$(get_delay_register_block "$rule_json")
    if [[ -z "$block" ]]; then
        echo "0"
        return 0
    fi
    local interval
    # healthCheckIntervalSeconds 的值可能是数字或带引号的字符串
    interval=$(echo "$block" | grep -oE '"healthCheckIntervalSeconds":"?[0-9]+"?' | grep -oE '[0-9]+' || echo "0")
    if [[ -z "$interval" ]]; then
        interval="0"
    fi
    echo "$interval"
    return 0
}

# 从 lossless 规则 JSON 中提取健康检查路径
get_health_check_path() {
    local rule_json="$1"
    local block
    block=$(get_delay_register_block "$rule_json")
    if [[ -z "$block" ]]; then
        echo ""
        return 0
    fi
    local path
    path=$(echo "$block" | grep -oE '"healthCheckPath":"[^"]+"' | sed 's/"healthCheckPath":"\([^"]*\)"/\1/' || echo "")
    echo "$path"
    return 0
}

# 从 lossless 规则 JSON 中提取健康检查协议
get_health_check_protocol() {
    local rule_json="$1"
    local block
    block=$(get_delay_register_block "$rule_json")
    if [[ -z "$block" ]]; then
        echo ""
        return 0
    fi
    local protocol
    protocol=$(echo "$block" | grep -oE '"healthCheckProtocol":"[^"]+"' | sed 's/"healthCheckProtocol":"\([^"]*\)"/\1/' || echo "")
    echo "$protocol"
    return 0
}

# 从 lossless 规则 JSON 中提取健康检查方法
get_health_check_method() {
    local rule_json="$1"
    local block
    block=$(get_delay_register_block "$rule_json")
    if [[ -z "$block" ]]; then
        echo ""
        return 0
    fi
    local method
    method=$(echo "$block" | grep -oE '"healthCheckMethod":"[^"]+"' | sed 's/"healthCheckMethod":"\([^"]*\)"/\1/' || echo "")
    echo "$method"
    return 0
}

# 从 lossless 规则 JSON 中检查 readiness 是否开启
get_readiness_enabled() {
    local rule_json="$1"
    local readiness_block
    readiness_block=$(echo "$rule_json" | grep -oE '"readiness":\{[^}]*\}' || echo "")
    if [[ -z "$readiness_block" ]]; then
        echo "false"
        return 0
    fi
    local enable
    enable=$(echo "$readiness_block" | grep -o '"enable":true' || echo "")
    if [[ -n "$enable" ]]; then
        echo "true"
    else
        echo "false"
    fi
    return 0
}

# 执行就绪检查探针请求
# 返回格式: "HTTP状态码|响应体"
do_readiness_check() {
    local admin_url="http://127.0.0.1:${ADMIN_PORT}/readiness"
    local http_code
    local body
    http_code=$(curl -s -o /tmp/_readiness_resp_$$.tmp -w '%{http_code}' --connect-timeout 3 "$admin_url" 2>/dev/null || echo "000")
    body=$(cat /tmp/_readiness_resp_$$.tmp 2>/dev/null || echo "")
    rm -f /tmp/_readiness_resp_$$.tmp
    echo "${http_code}|${body}"
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
# 主调方配置
consumer:
  weightAdjust:
    enable: true
    chain:
      - warmup
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
  admin:
    port: ${ADMIN_PORT}
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
# 被调方配置
provider:
  lossless:
    enable: true
EOF
    fi
    log_info "生成 polaris.yaml -> $yaml_file"
}

# ======================== 主流程 ========================

main() {
    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║    就绪检查(Readiness Probe)功能验证脚本        ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "配置信息:"
    echo "  北极星服务端:     ${POLARIS_SERVER}:8091"
    echo "  服务名:           ${SERVICE_NAME}"
    echo "  命名空间:         ${NAMESPACE}"
    echo "  Consumer端口:     ${CONSUMER_PORT}"
    echo "  Admin端口:        ${ADMIN_PORT}"
    echo "  Debug日志:        ${DEBUG_MODE}"
    echo "  观察时长:         ${OBSERVE_SECONDS}s (0=自动计算)"
    echo "  检查间隔:         ${CHECK_INTERVAL}s"
    echo ""

    # ==================== 步骤1: 环境准备 ====================
    log_step "1/7 环境准备"

    mkdir -p "$BUILD_DIR" "$LOG_DIR"

    # 检查 Go 环境
    if ! command -v go &> /dev/null; then
        log_error "Go 未安装，请先安装 Go"
        exit 1
    fi
    log_info "Go 版本: $(go version)"

    # 检查源码目录
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

    log_info "编译 Provider (healthCheckDelay)..."
    (cd "$PROVIDER_DIR" && go build -o "${BUILD_DIR}/provider_hcd" .)
    log_info "Provider 编译完成 -> ${BUILD_DIR}/provider_hcd"

    log_info "编译 Consumer..."
    (cd "$CONSUMER_DIR" && go build -o "${BUILD_DIR}/consumer" .)
    log_info "Consumer 编译完成 -> ${BUILD_DIR}/consumer"

    # macOS Gatekeeper 处理
    if command -v xattr &> /dev/null; then
        xattr -c "${BUILD_DIR}/provider_hcd" 2>/dev/null || true
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

    local consumer_log="${LOG_DIR}/consumer.log"

    if curl -s --connect-timeout 2 "http://127.0.0.1:${CONSUMER_PORT}/instances" > /dev/null 2>&1; then
        log_info "检测到端口 ${CONSUMER_PORT} 上已有 Consumer 在运行，直接复用"
        local existing_pid
        existing_pid=$(lsof -ti :"$CONSUMER_PORT" 2>/dev/null | head -1 || echo "")
        if [[ -n "$existing_pid" ]]; then
            CONSUMER_PID="$existing_pid"
            log_info "已有 Consumer PID: $CONSUMER_PID"
        fi
    else
        log_info "端口 ${CONSUMER_PORT} 上未检测到已有 Consumer，启动新实例..."

        local consumer_workdir="${BUILD_DIR}/consumer_run"
        mkdir -p "$consumer_workdir"
        cp "${BUILD_DIR}/polaris_consumer.yaml" "${consumer_workdir}/polaris.yaml"

        (cd "$consumer_workdir" && "${BUILD_DIR}/consumer" \
            --calleeNamespace "$NAMESPACE" \
            --calleeService "$SERVICE_NAME" \
            --port "$CONSUMER_PORT" \
            --selfRegister=false \
            --debug=$DEBUG_MODE \
            > "$consumer_log" 2>&1) &
        CONSUMER_PID=$!
        log_info "Consumer 已启动 (PID: $CONSUMER_PID)"

        sleep 1
        check_process_alive "$CONSUMER_PID" "Consumer" || {
            log_error "Consumer 启动失败，请检查日志: $consumer_log"
            cat "$consumer_log" 2>/dev/null || true
            exit 1
        }

        wait_for_http "http://127.0.0.1:${CONSUMER_PORT}/instances" 20 "Consumer" "$CONSUMER_PID" || exit 1
    fi

    # ==================== 步骤4: 获取无损上线规则 ====================
    log_step "4/7 获取无损上线规则，确认延迟注册和就绪检查配置"

    local lossless_rule_json=""
    local delay_register_seconds=0
    local delay_register_strategy=""
    local hc_interval_seconds=0
    local hc_path=""
    local hc_protocol=""
    local hc_method=""

    lossless_rule_json=$(get_lossless_rule 2>/dev/null || echo "")
    if [[ -n "$lossless_rule_json" ]]; then
        log_info "获取到无损上线规则: ${lossless_rule_json}"

        # 优先检查 readiness 是否开启，未开启则直接退出
        local readiness_enabled
        readiness_enabled=$(get_readiness_enabled "$lossless_rule_json")
        if [[ "$readiness_enabled" != "true" ]]; then
            log_error "就绪检查(readiness)未开启！"
            # 尝试解析延迟注册策略，在提示中展示当前已有的配置
            local tmp_strategy
            tmp_strategy=$(get_delay_register_strategy "$lossless_rule_json")
            if [[ -n "$tmp_strategy" ]]; then
                log_error "当前延迟注册策略已配置为: ${tmp_strategy}，但就绪检查未启用。"
            fi
            log_error "请在北极星控制台为服务 [${SERVICE_NAME}] 的无损上线规则中开启就绪检查开关。"
            log_error "脚本退出。"
            exit 1
        fi
        log_info "✅ 就绪检查(readiness)已开启"

        # 解析延迟注册配置
        delay_register_strategy=$(get_delay_register_strategy "$lossless_rule_json")
        delay_register_seconds=$(get_delay_register_seconds "$lossless_rule_json")
        hc_interval_seconds=$(get_health_check_interval_seconds "$lossless_rule_json")
        hc_path=$(get_health_check_path "$lossless_rule_json")
        hc_protocol=$(get_health_check_protocol "$lossless_rule_json")
        hc_method=$(get_health_check_method "$lossless_rule_json")

        log_info "延迟注册策略: ${delay_register_strategy:-未配置}"

        if [[ "$delay_register_strategy" == "DELAY_BY_TIME" ]]; then
            log_info "延迟注册类型: 时长延迟"
            log_info "延迟注册时长: ${delay_register_seconds}s"
        elif [[ "$delay_register_strategy" == "DELAY_BY_HEALTH_CHECK" ]]; then
            log_info "延迟注册类型: 接口探测延迟"
            log_info "健康检查间隔: ${hc_interval_seconds}s"
            log_info "健康检查路径: ${hc_path:-未配置}"
            log_info "健康检查协议: ${hc_protocol:-未配置}"
            log_info "健康检查方法: ${hc_method:-未配置}"
            log_info "说明: Provider 将每隔 ${hc_interval_seconds}s 探测一次 ${hc_path}，直到返回成功后才注册"
        fi

        if [[ -z "$delay_register_strategy" ]]; then
            log_warn "未检测到延迟注册配置，就绪检查可能无法观察到 UNREGISTERED 状态"
            log_warn "建议在北极星控制台配置延迟注册（DELAY_BY_TIME 或 DELAY_BY_HEALTH_CHECK）"
        fi
    else
        log_warn "无法获取无损上线规则，将使用默认观察时长"
    fi

    # 自动计算观察时长
    if [[ $OBSERVE_SECONDS -eq 0 ]]; then
        if [[ "$delay_register_strategy" == "DELAY_BY_TIME" ]] && [[ $delay_register_seconds -gt 0 ]]; then
            # 时长延迟: 观察时长 = 延迟时长 + 30s 缓冲
            OBSERVE_SECONDS=$((delay_register_seconds + 30))
            log_info "根据时长延迟(${delay_register_seconds}s)自动设置观察时长为 ${OBSERVE_SECONDS}s"
        elif [[ "$delay_register_strategy" == "DELAY_BY_HEALTH_CHECK" ]] && [[ $hc_interval_seconds -gt 0 ]]; then
            # 接口探测延迟: 观察时长 = 健康检查间隔 * 5（预留多次探测的时间） + 30s 缓冲
            OBSERVE_SECONDS=$((hc_interval_seconds * 5 + 30))
            log_info "根据接口探测间隔(${hc_interval_seconds}s)自动设置观察时长为 ${OBSERVE_SECONDS}s"
        else
            OBSERVE_SECONDS=60
            log_warn "未获取到延迟注册时长，使用默认观察时长 ${OBSERVE_SECONDS}s"
        fi
    fi

    # ==================== 步骤5: 启动 Provider ====================
    log_step "5/7 启动 Provider（触发延迟注册 + 就绪检查）"

    local provider_workdir="${BUILD_DIR}/provider_readiness"
    mkdir -p "$provider_workdir"
    cp "${BUILD_DIR}/polaris_provider.yaml" "${provider_workdir}/polaris.yaml"

    local provider_log="${LOG_DIR}/provider_readiness.log"
    (cd "$provider_workdir" && "${BUILD_DIR}/provider_hcd" \
        --namespace "$NAMESPACE" \
        --service "$SERVICE_NAME" \
        --token "$POLARIS_TOKEN" \
        --port "$PROVIDER_PORT" \
        > "$provider_log" 2>&1) &
    PROVIDER_PID=$!
    log_info "Provider 已启动 (PID: $PROVIDER_PID)"

    sleep 1
    check_process_alive "$PROVIDER_PID" "Provider" || {
        log_error "Provider 启动失败，请检查日志: $provider_log"
        cat "$provider_log" 2>/dev/null || true
        exit 1
    }

    local p_port
    if [[ "$PROVIDER_PORT" != "0" ]]; then
        p_port="$PROVIDER_PORT"
        log_info "Provider 使用指定端口: ${p_port}"
    else
        p_port=$(extract_port_from_log "$provider_log" 20) || {
            log_error "无法获取 Provider 端口，请检查日志: $provider_log"
            cat "$provider_log" 2>/dev/null || true
            exit 1
        }
        log_info "Provider 实际监听端口: ${p_port}"
    fi

    wait_for_http "http://127.0.0.1:${p_port}/echo" 20 "Provider" "$PROVIDER_PID" || exit 1

    # ==================== 步骤6: 持续轮询就绪检查接口 ====================
    log_step "6/7 持续轮询就绪检查接口（最长观察: ${OBSERVE_SECONDS}s，READY 后再确认 10 次即结束）"

    log_info "就绪检查地址: http://127.0.0.1:${ADMIN_PORT}/readiness"
    log_info "轮询间隔: ${CHECK_INTERVAL}s"

    # 初始化结果文件
    echo "时间戳,已运行秒数,HTTP状态码,响应体,就绪状态" > "$RESULT_FILE"

    echo ""
    printf "${BLUE}%-8s | %-12s | %-15s | %-12s${NC}\n" \
        "已运行" "HTTP状态码" "响应体" "就绪状态"
    printf "%-8s-+-%-12s-+-%-15s-+-%-12s\n" \
        "--------" "------------" "---------------" "------------"

    local start_ts
    start_ts=$(date +%s)

    local unregistered_count=0
    local registered_count=0
    local error_count=0
    local first_registered_ts=0
    local last_unregistered_ts=0
    local state_transitions=""

    local prev_status=""
    local checked=0
    local ready_extra_max=10       # 检测到 READY 后再额外请求的次数
    local ready_extra_counter=0    # 已额外请求的计数
    local ready_reached=false      # 是否已检测到 READY 状态

    while true; do
        local now_ts
        now_ts=$(date +%s)
        local elapsed=$((now_ts - start_ts))

        if [[ $elapsed -ge $OBSERVE_SECONDS ]]; then
            break
        fi

        # 检测到 READY 后再额外请求 ready_extra_max 次即退出
        if [[ "$ready_reached" == "true" ]]; then
            ready_extra_counter=$((ready_extra_counter + 1))
            if [[ $ready_extra_counter -gt $ready_extra_max ]]; then
                log_info "已检测到 READY 状态并额外确认 ${ready_extra_max} 次，提前结束观察"
                break
            fi
        fi

        # 检查 Provider 进程是否存活
        if ! check_process_alive "$PROVIDER_PID" "Provider" 2>/dev/null; then
            log_error "Provider 进程已退出，停止检查"
            break
        fi

        # 执行就绪检查
        local result
        result=$(do_readiness_check)
        local http_code="${result%%|*}"
        local body="${result#*|}"

        local status=""
        if [[ "$http_code" == "200" ]]; then
            status="READY"
            registered_count=$((registered_count + 1))
            if [[ $first_registered_ts -eq 0 ]]; then
                first_registered_ts=$elapsed
            fi
            if [[ "$ready_reached" != "true" ]]; then
                ready_reached=true
                log_info "✅ 检测到 READY 状态，再额外确认 ${ready_extra_max} 次后结束"
            fi
        elif [[ "$http_code" == "503" ]]; then
            status="NOT_READY"
            unregistered_count=$((unregistered_count + 1))
            last_unregistered_ts=$elapsed
        else
            status="ERROR"
            error_count=$((error_count + 1))
        fi

        # 检测状态变化
        if [[ -n "$prev_status" ]] && [[ "$status" != "$prev_status" ]]; then
            state_transitions="${state_transitions}${elapsed}s: ${prev_status} -> ${status}\n"
            log_info "🔄 状态变化: ${prev_status} -> ${status} (${elapsed}s)"
        fi
        prev_status="$status"

        # 输出当前状态
        local status_color=""
        if [[ "$status" == "READY" ]]; then
            status_color="${GREEN}${status}${NC}"
        elif [[ "$status" == "NOT_READY" ]]; then
            status_color="${YELLOW}${status}${NC}"
        else
            status_color="${RED}${status}${NC}"
        fi

        printf "%-8s | %-12s | %-15s | ${status_color}\n" \
            "${elapsed}s" "$http_code" "$body"

        # 写入 CSV
        echo "$(date '+%Y-%m-%d %H:%M:%S'),${elapsed},${http_code},${body},${status}" >> "$RESULT_FILE"

        checked=$((checked + 1))
        sleep "$CHECK_INTERVAL"
    done

    # ==================== 步骤7: 验证结果汇总 ====================
    log_step "7/7 验证结果汇总"

    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║              就绪检查验证结果汇总                ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "  服务名:              ${SERVICE_NAME}"
    echo "  命名空间:            ${NAMESPACE}"
    echo "  Provider 端口:       ${p_port}"
    echo "  Admin 端口:          ${ADMIN_PORT}"
    echo "  观察时长:            ${OBSERVE_SECONDS}s"
    echo "  检查次数:            ${checked}"
    echo ""
    echo "  延迟注册策略:        ${delay_register_strategy:-未配置}"
    if [[ "$delay_register_strategy" == "DELAY_BY_TIME" ]]; then
        echo "  延迟注册时长:        ${delay_register_seconds}s"
    elif [[ "$delay_register_strategy" == "DELAY_BY_HEALTH_CHECK" ]]; then
        echo "  健康检查间隔:        ${hc_interval_seconds}s"
        echo "  健康检查路径:        ${hc_path:-未配置}"
        echo "  健康检查协议:        ${hc_protocol:-未配置} ${hc_method:-}"
    fi
    echo ""
    echo "  NOT_READY 次数:      ${unregistered_count}"
    echo "  READY 次数:          ${registered_count}"
    echo "  ERROR 次数:          ${error_count}"
    echo ""

    if [[ $first_registered_ts -gt 0 ]]; then
        echo "  首次 READY 时间:     ${first_registered_ts}s"
    fi
    if [[ $last_unregistered_ts -gt 0 ]]; then
        echo "  最后 NOT_READY 时间: ${last_unregistered_ts}s"
    fi

    if [[ -n "$state_transitions" ]]; then
        echo ""
        echo "  状态变化记录:"
        echo -e "$state_transitions" | while IFS= read -r line; do
            if [[ -n "$line" ]]; then
                echo "    $line"
            fi
        done
    fi

    echo ""
    echo "  详细结果 CSV:        ${RESULT_FILE}"
    echo "  Provider 日志:       ${provider_log}"
    echo "  Consumer 日志:       ${LOG_DIR}/consumer.log"
    echo ""

    # ==================== 验证就绪检查效果 ====================
    echo -e "${BLUE}── 就绪检查效果验证 ──${NC}"

    local test_passed=true

    # 验证1: 是否观察到 NOT_READY 状态
    if [[ $unregistered_count -gt 0 ]]; then
        log_info "✅ 观察到 NOT_READY 状态 (${unregistered_count} 次)"
        log_info "   说明: Provider 在延迟注册期间，就绪检查正确返回了未就绪状态"
    else
        log_warn "⚠️  未观察到 NOT_READY 状态"
        log_warn "   可能原因:"
        log_warn "     1. 北极星控制台未配置延迟注册规则"
        log_warn "     2. 北极星控制台未启用就绪检查(readiness)"
        log_warn "     3. 延迟注册时间太短，脚本未能捕获到"
        log_warn "     4. Admin 端口(${ADMIN_PORT})不正确"
        test_passed=false
    fi

    # 验证2: 是否观察到 READY 状态
    if [[ $registered_count -gt 0 ]]; then
        log_info "✅ 观察到 READY 状态 (${registered_count} 次)"
        log_info "   说明: Provider 注册完成后，就绪检查正确返回了就绪状态"
    else
        log_warn "⚠️  未观察到 READY 状态"
        log_warn "   可能原因:"
        log_warn "     1. 观察时长不足，Provider 尚未完成注册"
        log_warn "     2. Provider 注册失败"
        log_warn "     3. Admin 端口(${ADMIN_PORT})不正确"
        test_passed=false
    fi

    # 验证3: 状态变化是否符合预期（NOT_READY -> READY）
    if [[ $unregistered_count -gt 0 ]] && [[ $registered_count -gt 0 ]]; then
        if [[ $first_registered_ts -gt $last_unregistered_ts ]] || [[ $last_unregistered_ts -lt $first_registered_ts ]]; then
            log_info "✅ 状态变化符合预期: NOT_READY -> READY"
            log_info "   说明: 就绪检查探针正确反映了实例的注册状态变化"
        fi
    fi

    echo ""
    if [[ "$test_passed" == "true" ]] && [[ $unregistered_count -gt 0 ]] && [[ $registered_count -gt 0 ]]; then
        echo -e "${GREEN}验证结论: ✅ 就绪检查功能正常生效！${NC}"
        if [[ "$delay_register_strategy" == "DELAY_BY_TIME" ]]; then
            echo -e "${GREEN}  - 延迟注册策略: 时长延迟 (DELAY_BY_TIME, ${delay_register_seconds}s)${NC}"
        elif [[ "$delay_register_strategy" == "DELAY_BY_HEALTH_CHECK" ]]; then
            echo -e "${GREEN}  - 延迟注册策略: 接口探测延迟 (DELAY_BY_HEALTH_CHECK, 间隔${hc_interval_seconds}s)${NC}"
        fi
        echo -e "${GREEN}  - 延迟注册期间: 就绪检查返回 503 (UNREGISTERED)${NC}"
        echo -e "${GREEN}  - 注册完成后:   就绪检查返回 200 (REGISTERED)${NC}"
    elif [[ $registered_count -gt 0 ]] && [[ $unregistered_count -eq 0 ]]; then
        echo -e "${YELLOW}验证结论: ⚠️  就绪检查接口可用，但未观察到 NOT_READY 状态${NC}"
        echo -e "${YELLOW}  请检查延迟注册配置是否正确启用${NC}"
    elif [[ $error_count -eq $checked ]]; then
        echo -e "${RED}验证结论: ❌ 就绪检查接口不可用${NC}"
        echo -e "${RED}  请检查 Admin 端口(${ADMIN_PORT})是否正确，以及就绪检查是否已启用${NC}"
    else
        echo -e "${YELLOW}验证结论: ⚠️  未能完全确认就绪检查效果，请检查配置${NC}"
    fi

    echo ""
    echo -e "${BLUE}提示: 查看 Provider 日志可以看到详细的无损上线过程:${NC}"
    echo -e "${BLUE}  grep 'Lossless' ${provider_log} | tail -20${NC}"
    echo ""
}

main "$@"
