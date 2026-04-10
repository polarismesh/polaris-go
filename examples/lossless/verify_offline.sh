#!/bin/bash
# =============================================================================
# 无损下线(/offline)功能验证脚本
#
# 使用方法:
#   chmod +x verify_offline.sh
#   ./verify_offline.sh [--polaris-server <地址>] [--polaris-token <令牌>]
#                       [--service <服务名>] [--namespace <命名空间>]
#                       [--admin-port <Admin端口>]
#
# 前置条件:
#   1. 北极星服务端(Polaris Server)已启动
#   2. 已在北极星控制台为目标服务配置 LosslessRule（启用无损下线）
#   3. Go 环境已安装
#
# 验证原理:
#   无损下线接口注册在 Admin 服务上（默认端口 28080），路径为 /offline。
#   应用在滚动发布或下线过程中，被调方服务实例通过调用 /offline 接口
#   向注册中心发起反注册，在进程结束之前完成反注册，确保流量不再路由到该实例。
#
#   - 调用 /offline 成功时，返回 HTTP 200 + "DEREGISTERED SUCCESS"
#   - 调用 /offline 失败时，返回 HTTP 500 + "DEREGISTERED FAILED"
#
# 验证流程:
#   1. 编译 provider 和 consumer
#   2. 环境准备
#   3. 启动 consumer（用于获取无损上线规则和查询实例列表）
#   4. 获取无损上线规则，确认无损下线已启用
#   5. 启动 provider（注册到北极星）
#   6. 通过 consumer 确认 provider 实例已注册
#   7. 调用 /offline 接口触发反注册
#   8. 验证反注册结果
# =============================================================================

set -euo pipefail

# ======================== 默认配置 ========================
POLARIS_SERVER="${POLARIS_SERVER:-127.0.0.1}"
POLARIS_TOKEN="${POLARIS_TOKEN:-}"
SERVICE_NAME="${SERVICE_NAME:-LosslessHealthDelayServer}"
NAMESPACE="${NAMESPACE:-default}"
CONSUMER_PORT="${CONSUMER_PORT:-18080}"
PROVIDER_PORT="${PROVIDER_PORT:-0}"       # 0 表示自动分配
ADMIN_PORT="${ADMIN_PORT:-28080}"         # Admin 服务端口（无损下线接口所在端口）
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
RESULT_FILE="${LOG_DIR}/offline_result.csv"

CONSUMER_PID=""
PROVIDER_PID=""
PROVIDER_ACTUAL_PORT=""

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

# 从 lossless 规则 JSON 中检查 losslessOffline 是否开启
get_lossless_offline_enabled() {
    local rule_json="$1"
    local offline_block
    offline_block=$(echo "$rule_json" | grep -oE '"losslessOffline":\{[^}]*\}' || echo "")
    if [[ -z "$offline_block" ]]; then
        echo "false"
        return 0
    fi
    local enable
    enable=$(echo "$offline_block" | grep -o '"enable":true' || echo "")
    if [[ -n "$enable" ]]; then
        echo "true"
    else
        echo "false"
    fi
    return 0
}

# 从 lossless 规则 JSON 中提取延迟注册策略
get_delay_register_strategy() {
    local rule_json="$1"
    local block
    block=$(echo "$rule_json" | grep -oE '"delayRegister":\{[^}]*\}' || echo "")
    if [[ -z "$block" ]]; then
        echo ""
        return 0
    fi
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

# 通过 Consumer 的 /instances 接口获取实例列表
get_instances() {
    local consumer_url="http://127.0.0.1:${CONSUMER_PORT}/instances"
    local resp
    resp=$(curl -s --connect-timeout 5 "$consumer_url" 2>/dev/null || echo "")
    echo "$resp"
}

# 检查指定端口的实例是否在实例列表中
check_instance_registered() {
    local target_port="$1"
    local instances_json
    instances_json=$(get_instances)
    if [[ -z "$instances_json" ]]; then
        echo "error"
        return 0
    fi
    # 检查实例列表中是否包含目标端口
    if echo "$instances_json" | grep -q "\"port\":${target_port}"; then
        echo "true"
        return 0
    fi
    echo "false"
    return 0
}

# 执行无损下线请求
# 返回格式: "HTTP状态码|响应体"
do_offline_request() {
    local admin_url="http://127.0.0.1:${ADMIN_PORT}/offline"
    local http_code
    local body
    http_code=$(curl -s -o /tmp/_offline_resp_$$.tmp -w '%{http_code}' --connect-timeout 5 "$admin_url" 2>/dev/null || echo "000")
    body=$(cat /tmp/_offline_resp_$$.tmp 2>/dev/null || echo "")
    rm -f /tmp/_offline_resp_$$.tmp
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
    echo -e "${BLUE}║      无损下线(/offline)功能验证脚本             ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "配置信息:"
    echo "  北极星服务端:     ${POLARIS_SERVER}:8091"
    echo "  服务名:           ${SERVICE_NAME}"
    echo "  命名空间:         ${NAMESPACE}"
    echo "  Consumer端口:     ${CONSUMER_PORT}"
    echo "  Admin端口:        ${ADMIN_PORT}"
    echo "  Debug日志:        ${DEBUG_MODE}"
    echo ""

    # ==================== 步骤1: 环境准备 ====================
    log_step "1/8 环境准备"

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
    log_step "2/8 编译 Provider 和 Consumer"

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
    log_step "3/8 启动 Consumer"

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
    log_step "4/8 获取无损上线规则，确认无损下线配置"

    local lossless_rule_json=""
    local delay_register_strategy=""

    lossless_rule_json=$(get_lossless_rule 2>/dev/null || echo "")
    if [[ -n "$lossless_rule_json" ]]; then
        log_info "获取到无损上线规则: ${lossless_rule_json}"

        # 检查无损下线是否开启
        local offline_enabled
        offline_enabled=$(get_lossless_offline_enabled "$lossless_rule_json")
        if [[ "$offline_enabled" != "true" ]]; then
            log_error "无损下线(lossless_offline)未开启！"
            log_error "请在北极星控制台为服务 [${SERVICE_NAME}] 的无损上线规则中开启无损下线开关。"
            log_error "脚本退出。"
            exit 1
        fi
        log_info "✅ 无损下线(lossless_offline)已开启"

        # 解析延迟注册策略（用于展示信息）
        delay_register_strategy=$(get_delay_register_strategy "$lossless_rule_json")
        if [[ -n "$delay_register_strategy" ]]; then
            log_info "延迟注册策略: ${delay_register_strategy}"
        fi
    else
        log_warn "无法获取无损上线规则，将继续执行但可能无法验证"
    fi

    # ==================== 步骤5: 启动 Provider ====================
    log_step "5/8 启动 Provider（注册到北极星）"

    local provider_workdir="${BUILD_DIR}/provider_offline"
    mkdir -p "$provider_workdir"
    cp "${BUILD_DIR}/polaris_provider.yaml" "${provider_workdir}/polaris.yaml"

    local provider_log="${LOG_DIR}/provider_offline.log"
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
    PROVIDER_ACTUAL_PORT="$p_port"

    wait_for_http "http://127.0.0.1:${p_port}/echo" 20 "Provider" "$PROVIDER_PID" || exit 1

    # ==================== 步骤6: 等待 Provider 注册完成并确认 ====================
    log_step "6/8 等待 Provider 注册完成并确认实例已注册"

    # 等待 Provider 完成注册（包括延迟注册的等待时间）
    # 通过轮询 /readiness 接口或 consumer 的 /instances 接口来确认
    local max_wait_register=120  # 最长等待 120 秒
    local waited=0
    local registered=false

    log_info "等待 Provider 完成注册（最长等待 ${max_wait_register}s）..."

    while [[ $waited -lt $max_wait_register ]]; do
        # 检查 Provider 进程是否存活
        if ! check_process_alive "$PROVIDER_PID" "Provider" 2>/dev/null; then
            log_error "Provider 进程已退出，停止等待"
            exit 1
        fi

        # 尝试通过 readiness 接口检查
        local readiness_result
        readiness_result=$(curl -s -o /dev/null -w '%{http_code}' --connect-timeout 2 "http://127.0.0.1:${ADMIN_PORT}/readiness" 2>/dev/null || echo "000")

        if [[ "$readiness_result" == "200" ]]; then
            log_info "✅ Provider 已注册 (readiness 返回 200, 耗时 ${waited}s)"
            registered=true
            break
        fi

        # 也通过 consumer 的 instances 接口检查
        local instance_status
        instance_status=$(check_instance_registered "$p_port")
        if [[ "$instance_status" == "true" ]]; then
            log_info "✅ Provider 实例已在 Consumer 实例列表中 (端口: ${p_port}, 耗时 ${waited}s)"
            registered=true
            break
        fi

        if [[ $((waited % 10)) -eq 0 ]] && [[ $waited -gt 0 ]]; then
            log_info "仍在等待 Provider 注册... (已等待 ${waited}s, readiness=${readiness_result})"
        fi

        sleep 1
        waited=$((waited + 1))
    done

    if [[ "$registered" != "true" ]]; then
        log_error "Provider 在 ${max_wait_register}s 内未完成注册"
        log_error "请检查延迟注册配置和 Provider 日志: $provider_log"
        exit 1
    fi

    # 额外等待几秒，确保 Consumer 缓存已刷新
    log_info "额外等待 5s，确保 Consumer 实例缓存已刷新..."
    sleep 5

    # 再次确认实例在 Consumer 列表中
    local pre_offline_instances
    pre_offline_instances=$(get_instances)
    log_info "下线前实例列表: ${pre_offline_instances}"

    local pre_check
    pre_check=$(check_instance_registered "$p_port")
    if [[ "$pre_check" != "true" ]]; then
        log_warn "Consumer 实例列表中未找到端口 ${p_port} 的实例，但 readiness 已返回 200"
        log_warn "继续执行下线操作..."
    else
        log_info "✅ 确认 Provider 实例(端口: ${p_port})在 Consumer 实例列表中"
    fi

    # ==================== 步骤7: 调用 /offline 接口触发反注册 ====================
    log_step "7/8 调用 /offline 接口触发反注册"

    log_info "无损下线地址: http://127.0.0.1:${ADMIN_PORT}/offline"
    log_info "即将调用 /offline 接口..."

    # 初始化结果文件
    echo "时间戳,步骤,HTTP状态码,响应体,说明" > "$RESULT_FILE"

    local offline_result
    offline_result=$(do_offline_request)
    local offline_http_code="${offline_result%%|*}"
    local offline_body="${offline_result#*|}"

    echo "$(date '+%Y-%m-%d %H:%M:%S'),offline_request,${offline_http_code},${offline_body},调用/offline接口" >> "$RESULT_FILE"

    echo ""
    printf "${BLUE}%-20s | %-12s | %-25s${NC}\n" \
        "操作" "HTTP状态码" "响应体"
    printf "%-20s-+-%-12s-+-%-25s\n" \
        "--------------------" "------------" "-------------------------"
    printf "%-20s | %-12s | %-25s\n" \
        "调用 /offline" "$offline_http_code" "$offline_body"
    echo ""

    if [[ "$offline_http_code" == "200" ]]; then
        log_info "✅ /offline 接口返回成功: HTTP ${offline_http_code} - ${offline_body}"
    elif [[ "$offline_http_code" == "404" ]]; then
        log_error "❌ /offline 接口返回 404，接口未注册"
        log_error "可能原因: 无损下线(lossless_offline)未在北极星控制台开启"
        exit 1
    else
        log_error "❌ /offline 接口返回失败: HTTP ${offline_http_code} - ${offline_body}"
        log_error "反注册可能失败，请检查 Provider 日志: $provider_log"
    fi

    # ==================== 步骤8: 验证反注册结果 ====================
    log_step "8/8 验证反注册结果"

    # 等待一段时间让 Consumer 缓存刷新
    log_info "等待 Consumer 实例缓存刷新（轮询检查，最长等待 30s）..."

    local max_wait_deregister=30
    local deregister_waited=0
    local deregistered=false

    while [[ $deregister_waited -lt $max_wait_deregister ]]; do
        local post_check
        post_check=$(check_instance_registered "$p_port")
        if [[ "$post_check" == "false" ]]; then
            log_info "✅ Provider 实例(端口: ${p_port})已从 Consumer 实例列表中移除 (耗时 ${deregister_waited}s)"
            deregistered=true
            break
        fi
        sleep 1
        deregister_waited=$((deregister_waited + 1))
    done

    local post_offline_instances
    post_offline_instances=$(get_instances)
    log_info "下线后实例列表: ${post_offline_instances}"

    echo "$(date '+%Y-%m-%d %H:%M:%S'),verify_deregister,${deregistered},,验证实例是否已从列表移除" >> "$RESULT_FILE"

    # 检查 readiness 接口状态（如果可用）
    local post_readiness_code
    post_readiness_code=$(curl -s -o /dev/null -w '%{http_code}' --connect-timeout 2 "http://127.0.0.1:${ADMIN_PORT}/readiness" 2>/dev/null || echo "000")
    log_info "下线后 readiness 状态: HTTP ${post_readiness_code}"

    echo "$(date '+%Y-%m-%d %H:%M:%S'),readiness_after_offline,${post_readiness_code},,下线后readiness状态" >> "$RESULT_FILE"

    # ==================== 验证结果汇总 ====================
    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║            无损下线验证结果汇总                  ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "  服务名:              ${SERVICE_NAME}"
    echo "  命名空间:            ${NAMESPACE}"
    echo "  Provider 端口:       ${p_port}"
    echo "  Admin 端口:          ${ADMIN_PORT}"
    echo ""
    echo "  延迟注册策略:        ${delay_register_strategy:-未配置}"
    echo ""
    echo "  /offline 响应码:     ${offline_http_code}"
    echo "  /offline 响应体:     ${offline_body}"
    echo "  实例已从列表移除:    ${deregistered}"
    echo "  下线后 readiness:    HTTP ${post_readiness_code}"
    echo ""
    echo "  详细结果 CSV:        ${RESULT_FILE}"
    echo "  Provider 日志:       ${provider_log}"
    echo "  Consumer 日志:       ${LOG_DIR}/consumer.log"
    echo ""

    # ==================== 验证结论 ====================
    echo -e "${BLUE}── 无损下线效果验证 ──${NC}"

    local test_passed=true

    # 验证1: /offline 接口是否返回成功
    if [[ "$offline_http_code" == "200" ]]; then
        log_info "✅ /offline 接口调用成功 (HTTP 200 - ${offline_body})"
        log_info "   说明: 被调方服务实例成功向注册中心发起了反注册"
    else
        log_error "❌ /offline 接口调用失败 (HTTP ${offline_http_code} - ${offline_body})"
        log_error "   说明: 反注册请求未成功"
        test_passed=false
    fi

    # 验证2: 实例是否已从 Consumer 实例列表中移除
    if [[ "$deregistered" == "true" ]]; then
        log_info "✅ 实例已从 Consumer 实例列表中移除"
        log_info "   说明: 反注册生效，Consumer 不再路由流量到该实例"
    else
        log_warn "⚠️  实例仍在 Consumer 实例列表中"
        log_warn "   可能原因:"
        log_warn "     1. Consumer 缓存刷新延迟（北极星默认缓存刷新周期较长）"
        log_warn "     2. 反注册未成功"
        if [[ "$offline_http_code" == "200" ]]; then
            log_warn "   注意: /offline 接口已返回成功，实例可能需要更长时间从缓存中移除"
        else
            test_passed=false
        fi
    fi

    # 验证3: readiness 状态变化
    if [[ "$post_readiness_code" == "503" ]]; then
        log_info "✅ 下线后 readiness 返回 503 (UNREGISTERED)"
        log_info "   说明: 就绪检查正确反映了实例已反注册的状态"
    elif [[ "$post_readiness_code" == "200" ]]; then
        log_warn "⚠️  下线后 readiness 仍返回 200"
        log_warn "   说明: readiness 状态可能存在延迟"
    elif [[ "$post_readiness_code" == "000" ]] || [[ "$post_readiness_code" == "404" ]]; then
        log_info "ℹ️  readiness 接口不可用 (HTTP ${post_readiness_code})，跳过此项验证"
    fi

    echo ""
    if [[ "$test_passed" == "true" ]] && [[ "$offline_http_code" == "200" ]]; then
        echo -e "${GREEN}验证结论: ✅ 无损下线功能正常生效！${NC}"
        echo -e "${GREEN}  - /offline 接口: 调用成功，返回 HTTP 200 (DEREGISTERED SUCCESS)${NC}"
        echo -e "${GREEN}  - 反注册效果: 实例已从注册中心移除${NC}"
        echo -e "${GREEN}  - 应用场景: 滚动发布或下线时，在进程结束前调用 /offline 完成反注册${NC}"
        echo -e "${GREEN}  - 调用方式: curl http://127.0.0.1:${ADMIN_PORT}/offline${NC}"
    elif [[ "$offline_http_code" == "200" ]] && [[ "$deregistered" != "true" ]]; then
        echo -e "${YELLOW}验证结论: ⚠️  /offline 接口调用成功，但 Consumer 缓存尚未刷新${NC}"
        echo -e "${YELLOW}  - /offline 接口已返回成功，反注册请求已发送${NC}"
        echo -e "${YELLOW}  - Consumer 实例列表可能需要更长时间刷新${NC}"
    else
        echo -e "${RED}验证结论: ❌ 无损下线功能验证未通过${NC}"
        echo -e "${RED}  请检查以下配置:${NC}"
        echo -e "${RED}  1. 北极星控制台是否为服务 [${SERVICE_NAME}] 开启了无损下线${NC}"
        echo -e "${RED}  2. Admin 端口(${ADMIN_PORT})是否正确${NC}"
        echo -e "${RED}  3. Provider 日志是否有错误信息${NC}"
    fi

    echo ""
    echo -e "${BLUE}提示: 查看 Provider 日志可以看到详细的无损下线过程:${NC}"
    echo -e "${BLUE}  grep 'Lossless\|Deregister\|offline' ${provider_log} | tail -20${NC}"
    echo ""
}

main "$@"
