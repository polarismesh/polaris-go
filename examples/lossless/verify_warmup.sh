#!/bin/bash
# =============================================================================
# 服务预热(Warmup)功能验证脚本
#
# 使用方法:
#   chmod +x verify_warmup.sh
#   ./verify_warmup.sh [--polaris-server <地址>] [--polaris-token <令牌>]
#                      [--service <服务名>] [--namespace <命名空间>]
#                      [--warmup-seconds <预热观察秒数>]
#                      [--request-interval <请求间隔秒数>]
#
# 前置条件:
#   1. 北极星服务端(Polaris Server)已启动
#   2. 已在北极星控制台为目标服务配置 LosslessRule（预热规则）
#   3. Go 环境已安装
#
# 验证流程:
#   1. 编译 provider 和 consumer
#   2. 环境准备
#   3. 启动 consumer
#   4. 获取无损上线规则，检查被调是否有可用实例
#   场景A（已有可用实例）:
#     5A. 发起基线请求，确认链路正常
#     6A. 启动一个新 provider（新实例，开始预热）
#   场景B（无可用实例）:
#     5B. 启动 provider1（老实例），等待注册和预热结束
#     6B. 启动 provider2（新实例，开始预热）
#   7. 持续发起请求，统计流量在新老实例间的分布
#   8. 验证新实例流量是否符合预热逐步增长的预期
# =============================================================================

set -euo pipefail

# ======================== 默认配置 ========================
POLARIS_SERVER="${POLARIS_SERVER:-127.0.0.1}"
POLARIS_TOKEN="${POLARIS_TOKEN:-}"
SERVICE_NAME="${SERVICE_NAME:-LosslessTimeDelayServer}"
NAMESPACE="${NAMESPACE:-default}"
CONSUMER_PORT="${CONSUMER_PORT:-18080}"
PROVIDER1_PORT="${PROVIDER1_PORT:-0}"    # 0 表示自动分配
PROVIDER2_PORT="${PROVIDER2_PORT:-0}"    # 0 表示自动分配
WARMUP_OBSERVE_SECONDS="${WARMUP_OBSERVE_SECONDS:-0}"  # 0 表示根据预热时长自动计算
REQUEST_INTERVAL="${REQUEST_INTERVAL:-1}"
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
        --provider1-port)
            PROVIDER1_PORT="$2"; shift 2 ;;
        --provider2-port)
            PROVIDER2_PORT="$2"; shift 2 ;;
        --warmup-seconds)
            WARMUP_OBSERVE_SECONDS="$2"; shift 2 ;;
        --request-interval)
            REQUEST_INTERVAL="$2"; shift 2 ;;
        --debug)
            DEBUG_MODE="true"; shift ;;
        --help|-h)
            echo "用法: $0 [选项]"
            echo ""
            echo "选项:"
            echo "  --polaris-server <地址>     北极星服务端地址 (默认: 127.0.0.1)"
            echo "  --polaris-token <令牌>      北极星鉴权令牌 (默认: 空)"
            echo "  --service <服务名>          目标服务名 (默认: LosslessTimeDelayServer)"
            echo "  --namespace <命名空间>      命名空间 (默认: default)"
            echo "  --consumer-port <端口>      Consumer HTTP端口 (默认: 18080)"
            echo "  --provider1-port <端口>     Provider1 端口 (默认: 自动分配)"
            echo "  --provider2-port <端口>     Provider2 端口 (默认: 自动分配)"
            echo "  --warmup-seconds <秒>       预热观察时长 (默认: 120)"
            echo "  --request-interval <秒>     请求间隔 (默认: 1)"
            echo "  --debug                     启用 Consumer debug 日志 (默认: 关闭)"
            exit 0
            ;;
        *)
            echo -e "${RED}未知参数: $1${NC}"; exit 1 ;;
    esac
done

# ======================== 全局变量 ========================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONSUMER_DIR="${SCRIPT_DIR}/consumer"
PROVIDER_DIR="${SCRIPT_DIR}/provider/timeDelay"
BUILD_DIR="${SCRIPT_DIR}/.build"
LOG_DIR="${SCRIPT_DIR}/.logs"
RESULT_FILE="${LOG_DIR}/warmup_result.csv"

CONSUMER_PID=""
PROVIDER1_PID=""
PROVIDER2_PID=""
EXTRA_PROVIDER_PIDS=()

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
    # 清理额外的老实例进程
    if [[ ${#EXTRA_PROVIDER_PIDS[@]} -gt 0 ]]; then
        for extra_pid in "${EXTRA_PROVIDER_PIDS[@]}"; do
            if [[ -n "$extra_pid" ]] && kill -0 "$extra_pid" 2>/dev/null; then
                log_info "停止额外老实例进程 (PID: $extra_pid)"
                kill "$extra_pid" 2>/dev/null || true
                wait "$extra_pid" 2>/dev/null || true
            fi
        done
    fi
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
        # 尝试获取退出码
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
    local pid="${4:-}"  # 可选：关联的后台进程 PID
    local waited=0

    while [[ $waited -lt $max_wait ]]; do
        # 如果提供了 PID，先检查进程是否还活着
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
# 返回 JSON 格式的规则信息，包含延迟注册和预热配置
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

# 从 lossless 规则 JSON 中提取延迟注册的等待秒数
# 如果延迟注册未启用或不存在，返回 0
get_delay_register_seconds() {
    local rule_json="$1"
    # 检查是否启用了延迟注册
    local enable
    enable=$(echo "$rule_json" | grep -o '"delayRegister":{[^}]*}' | grep -o '"enable":true' || echo "")
    if [[ -z "$enable" ]]; then
        echo "0"
        return 0
    fi
    # 提取 intervalSeconds
    local interval
    interval=$(echo "$rule_json" | grep -o '"delayRegister":{[^}]*}' | grep -o '"intervalSeconds":[0-9]*' | sed 's/"intervalSeconds"://' || echo "0")
    if [[ -z "$interval" ]] || [[ "$interval" == "0" ]]; then
        echo "0"
        return 0
    fi
    echo "$interval"
    return 0
}

# 从 lossless 规则 JSON 中提取预热时长秒数
get_warmup_seconds() {
    local rule_json="$1"
    local enable
    enable=$(echo "$rule_json" | grep -o '"warmup":{[^}]*}' | grep -o '"enable":true' || echo "")
    if [[ -z "$enable" ]]; then
        echo "0"
        return 0
    fi
    local interval
    interval=$(echo "$rule_json" | grep -o '"warmup":{[^}]*}' | grep -o '"intervalSeconds":[0-9]*' | sed 's/"intervalSeconds"://' || echo "0")
    echo "${interval:-0}"
    return 0
}

# 从 lossless 规则 JSON 中提取预热终止保护（过载保护）是否启用
# 返回 "true" 或 "false"
get_overload_protection_enabled() {
    local rule_json="$1"
    local warmup_block
    warmup_block=$(echo "$rule_json" | grep -o '"warmup":{[^}]*}' || echo "")
    if [[ -z "$warmup_block" ]]; then
        echo "false"
        return 0
    fi
    local enabled
    enabled=$(echo "$warmup_block" | grep -o '"enableOverloadProtection":true' || echo "")
    if [[ -n "$enabled" ]]; then
        echo "true"
    else
        echo "false"
    fi
    return 0
}

# 从 lossless 规则 JSON 中提取预热终止保护阈值（百分比）
# 返回阈值数字，未配置则返回 0
get_overload_protection_threshold() {
    local rule_json="$1"
    local warmup_block
    warmup_block=$(echo "$rule_json" | grep -o '"warmup":{[^}]*}' || echo "")
    if [[ -z "$warmup_block" ]]; then
        echo "0"
        return 0
    fi
    local threshold
    threshold=$(echo "$warmup_block" | grep -o '"overloadProtectionThreshold":[0-9]*' | sed 's/"overloadProtectionThreshold"://' || echo "0")
    echo "${threshold:-0}"
    return 0
}

# 从 lossless 规则 JSON 中提取预热曲线系数（curvature）
# 返回曲线系数数字，未配置或为0则返回默认值2
get_warmup_curvature() {
    local rule_json="$1"
    local warmup_block
    warmup_block=$(echo "$rule_json" | grep -o '"warmup":{[^}]*}' || echo "")
    if [[ -z "$warmup_block" ]]; then
        echo "2"
        return 0
    fi
    local curvature
    curvature=$(echo "$warmup_block" | grep -o '"curvature":[0-9]*' | sed 's/"curvature"://' || echo "0")
    if [[ -z "$curvature" ]] || [[ "$curvature" == "0" ]]; then
        echo "2"
        return 0
    fi
    echo "$curvature"
    return 0
}

# 等待至少有一个实例注册到北极星（通过 /instances 接口检测）
wait_for_instance_registered() {
    local max_wait="${1:-60}"
    local waited=0

    while [[ $waited -lt $max_wait ]]; do
        local instance_list
        instance_list=$(get_instance_list 2>/dev/null || echo "")
        if [[ -n "$instance_list" ]]; then
            local count
            count=$(echo "$instance_list" | wc -l | tr -d ' ')
            if [[ $count -gt 0 ]]; then
                log_info "检测到 ${count} 个已注册实例"
                echo "$instance_list"
                return 0
            fi
        fi
        sleep 1
        waited=$((waited + 1))
        if [[ $((waited % 5)) -eq 0 ]]; then
            log_info "等待实例注册中... 已等待 ${waited}s / ${max_wait}s"
        fi
    done
    log_error "等待实例注册超时（${max_wait}s）"
    return 1
}

# 通过 Consumer 的 /instances 接口获取当前实例列表（返回 host:port 列表，每行一个）
get_instance_list() {
    local consumer_url="http://127.0.0.1:${CONSUMER_PORT}/instances"
    local resp
    resp=$(curl -s --connect-timeout 5 "$consumer_url" 2>/dev/null || echo "")
    if [[ -z "$resp" ]]; then
        return 1
    fi
    # 从 JSON 数组中提取 host:port，兼容 macOS（不依赖 jq）
    # 使用 $$ (当前脚本 PID) 作为临时文件后缀，避免多实例并发冲突
    local tmp_hosts="/tmp/_warmup_hosts_$$.tmp"
    local tmp_ports="/tmp/_warmup_ports_$$.tmp"
    echo "$resp" | grep -o '"host":"[^"]*"' | sed 's/"host":"\([^"]*\)"/\1/' > "$tmp_hosts"
    echo "$resp" | grep -o '"port":[0-9]*' | sed 's/"port"://' > "$tmp_ports"
    paste -d':' "$tmp_hosts" "$tmp_ports"
    rm -f "$tmp_hosts" "$tmp_ports"
    return 0
}

# 等待新实例出现在实例列表中（对比基线列表）
wait_for_new_instance() {
    local baseline_file="$1"
    local max_wait="${2:-30}"
    local waited=0

    while [[ $waited -lt $max_wait ]]; do
        local current_list
        current_list=$(get_instance_list 2>/dev/null || echo "")
        if [[ -n "$current_list" ]]; then
            # 找出不在基线中的新实例
            local new_instances
            new_instances=$(echo "$current_list" | grep -v -F -f "$baseline_file" 2>/dev/null || echo "")
            if [[ -n "$new_instances" ]]; then
                echo "$new_instances"
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
    echo -e "${BLUE}║      服务预热(Warmup)功能验证脚本                ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "配置信息:"
    echo "  北极星服务端:     ${POLARIS_SERVER}:8091"
    echo "  服务名:           ${SERVICE_NAME}"
    echo "  命名空间:         ${NAMESPACE}"
    echo "  Consumer端口:     ${CONSUMER_PORT}"
    echo "  Debug日志:        ${DEBUG_MODE}"
    echo "  预热观察时长:     ${WARMUP_OBSERVE_SECONDS}s"
    echo "  请求间隔:         ${REQUEST_INTERVAL}s"
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

    log_info "编译 Provider..."
    (cd "$PROVIDER_DIR" && go build -o "${BUILD_DIR}/provider" .)
    log_info "Provider 编译完成 -> ${BUILD_DIR}/provider"

    log_info "编译 Consumer..."
    (cd "$CONSUMER_DIR" && go build -o "${BUILD_DIR}/consumer" .)
    log_info "Consumer 编译完成 -> ${BUILD_DIR}/consumer"

    # macOS Gatekeeper 可能会阻止执行未签名的二进制文件，清除 quarantine 属性
    if command -v xattr &> /dev/null; then
        xattr -c "${BUILD_DIR}/provider" 2>/dev/null || true
        xattr -c "${BUILD_DIR}/consumer" 2>/dev/null || true
        log_info "已清除二进制文件的 macOS quarantine 属性"
    fi

    # 生成 polaris.yaml（到 build 目录）
    generate_polaris_yaml "$BUILD_DIR" "provider"
    cp "${BUILD_DIR}/polaris.yaml" "${BUILD_DIR}/polaris_provider.yaml"
    generate_polaris_yaml "$BUILD_DIR" "consumer"
    cp "${BUILD_DIR}/polaris.yaml" "${BUILD_DIR}/polaris_consumer.yaml"

    # ==================== 步骤3: 启动 Consumer ====================
    log_step "3/8 启动 Consumer"

    local consumer_log="${LOG_DIR}/consumer.log"

    # 检查是否已有 Consumer 在运行（通过端口检测）
    if curl -s --connect-timeout 2 "http://127.0.0.1:${CONSUMER_PORT}/instances" > /dev/null 2>&1; then
        log_info "检测到端口 ${CONSUMER_PORT} 上已有 Consumer 在运行，直接复用"
        # 尝试获取已有 Consumer 的 PID
        local existing_pid
        existing_pid=$(lsof -ti :"$CONSUMER_PORT" 2>/dev/null | head -1 || echo "")
        if [[ -n "$existing_pid" ]]; then
            CONSUMER_PID="$existing_pid"
            log_info "已有 Consumer PID: $CONSUMER_PID"
        else
            log_warn "无法获取已有 Consumer 的 PID"
            CONSUMER_PID=""
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

        # 短暂等待后检查进程是否存活
        sleep 1
        check_process_alive "$CONSUMER_PID" "Consumer" || {
            log_error "Consumer 启动失败，请检查日志: $consumer_log"
            cat "$consumer_log" 2>/dev/null || true
            exit 1
        }

        # 等待 Consumer HTTP 就绪（使用 /instances 接口而非 /echo，因为此时可能没有可用实例）
        wait_for_http "http://127.0.0.1:${CONSUMER_PORT}/instances" 20 "Consumer" "$CONSUMER_PID" || exit 1
    fi

    # ==================== 步骤4: 获取无损上线规则并检查可用实例 ====================
    log_step "4/8 获取无损上线规则，检查被调是否有可用实例"

    local lossless_rule_json=""
    local delay_register_seconds=0
    local rule_warmup_seconds=0
    local overload_protection_enabled="false"
    local overload_protection_threshold=0
    local warmup_curvature=2

    lossless_rule_json=$(get_lossless_rule 2>/dev/null || echo "")
    if [[ -n "$lossless_rule_json" ]]; then
        log_info "获取到无损上线规则: ${lossless_rule_json}"

        delay_register_seconds=$(get_delay_register_seconds "$lossless_rule_json")
        rule_warmup_seconds=$(get_warmup_seconds "$lossless_rule_json")
        overload_protection_enabled=$(get_overload_protection_enabled "$lossless_rule_json")
        overload_protection_threshold=$(get_overload_protection_threshold "$lossless_rule_json")
        warmup_curvature=$(get_warmup_curvature "$lossless_rule_json")

        log_info "延迟注册配置: ${delay_register_seconds}s"
        log_info "预热时长配置: ${rule_warmup_seconds}s"
        log_info "预热曲线系数: ${warmup_curvature}（权重公式: weight = ceil(ratio^${warmup_curvature} * baseWeight)）"
        if [[ "$overload_protection_enabled" == "true" ]]; then
            log_info "预热终止保护: 已启用（阈值: ${overload_protection_threshold}%）"
            log_info "  说明: 当预热中的实例占比 >= ${overload_protection_threshold}% 时，预热权重调整将被终止，所有实例按原始权重分配流量"
        else
            log_info "预热终止保护: 未启用"
        fi
    else
        log_warn "无法获取无损上线规则"
    fi

    # 检查被调服务是否已有可用实例
    local existing_instances=""
    existing_instances=$(get_instance_list 2>/dev/null || echo "")
    local existing_count=0
    if [[ -n "$existing_instances" ]]; then
        existing_count=$(echo "$existing_instances" | wc -l | tr -d ' ')
    fi

    # 用于后续流量统计的端口变量
    local old_port=""       # 老实例端口（场景B中使用，单个端口）
    local old_ports=""      # 老实例端口列表（场景A中使用，可能有多个）
    local new_port=""       # 新实例端口（用于流量统计中的"新实例"列）
    local provider1_log="${LOG_DIR}/provider1.log"
    local provider2_log="${LOG_DIR}/provider2.log"
    local baseline_file="${LOG_DIR}/baseline_instances.txt"
    local baseline_instances=""
    local needed_old_count=1  # 场景B中需要的老实例数量（场景A中不使用）

    if [[ $existing_count -gt 0 ]]; then
        # ==================== 场景A: 被调已有可用实例 ====================
        log_info "检测到被调服务已有 ${existing_count} 个可用实例:"
        while IFS= read -r inst; do
            log_info "  - $inst"
        done <<< "$existing_instances"

        log_info "场景A: 已有可用实例，仅需启动一个新 Provider 验证预热"

        # 记录基线实例列表
        baseline_instances="$existing_instances"
        echo "$baseline_instances" > "$baseline_file"

        # 提取所有老实例端口（用于日志展示）
        old_ports=$(echo "$existing_instances" | sed 's/.*:\([0-9]*\)/\1/' | paste -sd',' -)
        old_port=$(echo "$existing_instances" | head -1 | sed 's/.*:\([0-9]*\)/\1/')
        log_info "老实例端口（用于流量统计）: ${old_ports}"

        # ==================== 步骤5A: 发起基线请求 ====================
        log_step "5/8 发起基线请求（验证链路正常）"

        local baseline_ok=0
        for i in $(seq 1 5); do
            local http_code
            local resp
            http_code=$(curl -s -o /tmp/_warmup_resp_$$.tmp -w '%{http_code}' --connect-timeout 5 "http://127.0.0.1:${CONSUMER_PORT}/echo" 2>/dev/null || echo "000")
            resp=$(cat /tmp/_warmup_resp_$$.tmp 2>/dev/null || echo "FAILED")
            rm -f /tmp/_warmup_resp_$$.tmp
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
            exit 1
        fi
        log_info "基线请求通过 (${baseline_ok}/5 成功)，链路正常"

        # 场景A: 过载保护预判
        if [[ "$overload_protection_enabled" == "true" ]] && [[ $overload_protection_threshold -gt 0 ]]; then
            local predicted_total=$((existing_count + 1))
            local predicted_pct=$((1 * 100 / predicted_total))
            if [[ $predicted_pct -ge $overload_protection_threshold ]]; then
                log_warn "⚠️  过载保护预判: 启动新实例后，预热实例占比 = 1/${predicted_total} = ${predicted_pct}% >= 阈值 ${overload_protection_threshold}%"
                log_warn "预热终止保护可能会被触发，导致预热效果不明显"
                log_warn "建议增加更多老实例或调高过载保护阈值"
            else
                log_info "过载保护预判: 启动新实例后，预热实例占比 = 1/${predicted_total} = ${predicted_pct}% < 阈值 ${overload_protection_threshold}%，不会触发"
            fi
        fi

        # ==================== 步骤6A: 启动新 Provider ====================
        log_step "6/8 启动新 Provider（新实例，开始预热）"

        local new_provider_workdir="${BUILD_DIR}/provider2"
        mkdir -p "$new_provider_workdir"
        cp "${BUILD_DIR}/polaris_provider.yaml" "${new_provider_workdir}/polaris.yaml"

        provider2_log="${LOG_DIR}/provider2.log"
        (cd "$new_provider_workdir" && "${BUILD_DIR}/provider" \
            --namespace "$NAMESPACE" \
            --service "$SERVICE_NAME" \
            --token "$POLARIS_TOKEN" \
            --port "$PROVIDER2_PORT" \
            > "$provider2_log" 2>&1) &
        PROVIDER2_PID=$!
        log_info "新 Provider 已启动 (PID: $PROVIDER2_PID)"

        sleep 1
        check_process_alive "$PROVIDER2_PID" "新Provider" || {
            log_error "新 Provider 启动失败，请检查日志: $provider2_log"
            cat "$provider2_log" 2>/dev/null || true
            exit 1
        }

        local np_port
        if [[ "$PROVIDER2_PORT" != "0" ]]; then
            np_port="$PROVIDER2_PORT"
            log_info "新 Provider 使用指定端口: ${np_port}"
        else
            np_port=$(extract_port_from_log "$provider2_log" 20) || {
                log_error "无法获取新 Provider 端口，请检查日志: $provider2_log"
                cat "$provider2_log" 2>/dev/null || true
                exit 1
            }
            log_info "新 Provider 实际监听端口: ${np_port}"
        fi

        wait_for_http "http://127.0.0.1:${np_port}/echo" 20 "新Provider" "$PROVIDER2_PID" || exit 1

        # 等待新 Provider 注册并出现在实例列表中
        log_info "等待新 Provider 注册到北极星..."
        if [[ -f "$baseline_file" ]]; then
            local new_inst
            new_inst=$(wait_for_new_instance "$baseline_file" 30) || {
            log_warn "未检测到新实例加入，继续等待 2s..."
                sleep 2
            }
            if [[ -n "$new_inst" ]]; then
                log_info "检测到新实例加入:"
                while IFS= read -r inst; do
                    log_info "  - $inst"
                    local detected_port
                    detected_port=$(echo "$inst" | sed 's/.*:\([0-9]*\)/\1/')
                    if [[ -n "$detected_port" ]]; then
                        np_port="$detected_port"
                        log_info "新 Provider 端口已通过实例列表确认: ${np_port}"
                    fi
                done <<< "$new_inst"
            fi
        else
            sleep 2
        fi

        new_port="$np_port"

        # 打印当前完整实例列表
        log_info "当前完整实例列表:"
        local current_all
        current_all=$(get_instance_list 2>/dev/null || echo "")
        if [[ -n "$current_all" ]]; then
            while IFS= read -r inst; do
                log_info "  - $inst"
            done <<< "$current_all"
        fi

    else
        # ==================== 场景B: 被调没有可用实例 ====================
        log_info "被调服务当前没有可用实例"
        log_info "场景B: 需要先启动老实例，等待预热结束后再启动新实例（Provider2）"

        # ==================== 预判过载保护：计算需要的老实例数量 ====================
        needed_old_count=1  # 默认至少1个老实例
        if [[ "$overload_protection_enabled" == "true" ]] && [[ $overload_protection_threshold -gt 0 ]]; then
            # 过载保护触发条件: 预热实例占比 >= 阈值
            # 新实例数=1，需要老实例数 N 使得: 1/(N+1)*100 < 阈值
            # 即 N+1 > 100/阈值，N > 100/阈值 - 1
            # 使用 awk 计算 ceil(100/threshold)
            local min_total
            min_total=$(awk "BEGIN {t=$overload_protection_threshold; printf \"%d\", (100 % t == 0) ? 100/t + 1 : int(100/t) + 1}")
            needed_old_count=$((min_total - 1))
            if [[ $needed_old_count -lt 1 ]]; then
                needed_old_count=1
            fi

            local predicted_pct=$((1 * 100 / (needed_old_count + 1)))
            if [[ $needed_old_count -gt 1 ]]; then
                log_warn "⚠️  预热终止保护预判："
                log_warn "  过载保护阈值: ${overload_protection_threshold}%"
                log_warn "  如果只启动1个老实例 + 1个新实例，预热实例占比 = 50% >= ${overload_protection_threshold}%，会触发过载保护！"
                log_warn "  自动调整: 将启动 ${needed_old_count} 个老实例，使预热实例占比 = 1/${min_total} = ${predicted_pct}% < ${overload_protection_threshold}%"
            else
                log_info "过载保护预判: 1个老实例 + 1个新实例，预热实例占比 = 50%，阈值 = ${overload_protection_threshold}%，不会触发"
            fi
        fi

        # ==================== 步骤5B: 启动老实例 ====================
        log_step "5/8 启动老实例（共 ${needed_old_count} 个）"

        # --- 启动 Provider1（第一个老实例） ---
        local provider1_workdir="${BUILD_DIR}/provider1"
        mkdir -p "$provider1_workdir"
        cp "${BUILD_DIR}/polaris_provider.yaml" "${provider1_workdir}/polaris.yaml"

        provider1_log="${LOG_DIR}/provider1.log"
        (cd "$provider1_workdir" && "${BUILD_DIR}/provider" \
            --namespace "$NAMESPACE" \
            --service "$SERVICE_NAME" \
            --token "$POLARIS_TOKEN" \
            --port "$PROVIDER1_PORT" \
            > "$provider1_log" 2>&1) &
        PROVIDER1_PID=$!
        log_info "Provider1（老实例）已启动 (PID: $PROVIDER1_PID)"

        sleep 1
        check_process_alive "$PROVIDER1_PID" "Provider1" || {
            log_error "Provider1 启动失败，请检查日志: $provider1_log"
            cat "$provider1_log" 2>/dev/null || true
            exit 1
        }

        local p1_port
        if [[ "$PROVIDER1_PORT" != "0" ]]; then
            p1_port="$PROVIDER1_PORT"
            log_info "Provider1 使用指定端口: ${p1_port}"
        else
            p1_port=$(extract_port_from_log "$provider1_log" 20) || {
                log_error "无法获取 Provider1 端口，请检查日志: $provider1_log"
                cat "$provider1_log" 2>/dev/null || true
                exit 1
            }
            log_info "Provider1 实际监听端口: ${p1_port}"
        fi

        wait_for_http "http://127.0.0.1:${p1_port}/echo" 20 "Provider1" "$PROVIDER1_PID" || exit 1

        # --- 启动额外的老实例（如果需要） ---
        if [[ $needed_old_count -gt 1 ]]; then
            log_info "需要额外启动 $((needed_old_count - 1)) 个老实例以避免触发过载保护..."
            for extra_idx in $(seq 2 $needed_old_count); do
                local extra_workdir="${BUILD_DIR}/provider_extra_${extra_idx}"
                mkdir -p "$extra_workdir"
                cp "${BUILD_DIR}/polaris_provider.yaml" "${extra_workdir}/polaris.yaml"

                local extra_log="${LOG_DIR}/provider_extra_${extra_idx}.log"
                (cd "$extra_workdir" && "${BUILD_DIR}/provider" \
                    --namespace "$NAMESPACE" \
                    --service "$SERVICE_NAME" \
                    --token "$POLARIS_TOKEN" \
                    --port 0 \
                    > "$extra_log" 2>&1) &
                local extra_pid=$!
                EXTRA_PROVIDER_PIDS+=("$extra_pid")
                log_info "额外老实例 #${extra_idx} 已启动 (PID: $extra_pid)"

                sleep 1
                check_process_alive "$extra_pid" "额外老实例#${extra_idx}" || {
                    log_error "额外老实例 #${extra_idx} 启动失败，请检查日志: $extra_log"
                    cat "$extra_log" 2>/dev/null || true
                    exit 1
                }

                local extra_port
                extra_port=$(extract_port_from_log "$extra_log" 20) || {
                    log_error "无法获取额外老实例 #${extra_idx} 端口，请检查日志: $extra_log"
                    cat "$extra_log" 2>/dev/null || true
                    exit 1
                }
                log_info "额外老实例 #${extra_idx} 实际监听端口: ${extra_port}"
                wait_for_http "http://127.0.0.1:${extra_port}/echo" 20 "额外老实例#${extra_idx}" "$extra_pid" || exit 1
            done
            log_info "所有 ${needed_old_count} 个老实例已启动"
        fi

        # 等待所有老实例注册到北极星
        if [[ $delay_register_seconds -gt 0 ]]; then
            log_warn "检测到延迟注册配置（${delay_register_seconds}s），老实例需要延迟注册"
            local total_wait=$((delay_register_seconds + 10))
            log_info "等待老实例完成延迟注册并同步到北极星（最长 ${total_wait}s）..."
            local registered_instances
            registered_instances=$(wait_for_instance_registered "$total_wait") || {
                log_error "老实例未能在 ${total_wait}s 内注册到北极星"
                log_error "可能原因: 延迟注册时长(${delay_register_seconds}s)过长，或老实例注册失败"
                log_error "Provider1 日志: $provider1_log"
                exit 1
            }
            log_info "老实例已注册到北极星:"
            while IFS= read -r inst; do
                log_info "  - $inst"
            done <<< "$registered_instances"
        else
            log_info "等待老实例注册到北极星（2s）..."
            sleep 2
        fi

        # 发起基线请求验证链路
        log_info "发起基线请求（验证链路正常）..."
        local baseline_ok=0
        for i in $(seq 1 5); do
            local http_code
            local resp
            http_code=$(curl -s -o /tmp/_warmup_resp_$$.tmp -w '%{http_code}' --connect-timeout 5 "http://127.0.0.1:${CONSUMER_PORT}/echo" 2>/dev/null || echo "000")
            resp=$(cat /tmp/_warmup_resp_$$.tmp 2>/dev/null || echo "FAILED")
            rm -f /tmp/_warmup_resp_$$.tmp
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
            exit 1
        fi
        log_info "基线请求通过 (${baseline_ok}/5 成功)，链路正常"

        # 等待所有老实例预热结束（确保它们成为"老实例"）
        if [[ $rule_warmup_seconds -gt 0 ]]; then
            local warmup_wait=$((rule_warmup_seconds + 10))
            log_step "5.5/8 等待老实例预热结束（${warmup_wait}s），使其成为老实例"
            log_info "老实例需要经过预热期（${rule_warmup_seconds}s）+ 缓冲（10s）才能成为老实例"
            log_info "等待中..."

            local warmup_waited=0
            while [[ $warmup_waited -lt $warmup_wait ]]; do
                sleep 10
                warmup_waited=$((warmup_waited + 10))
                if [[ $warmup_waited -lt $warmup_wait ]]; then
                    log_info "老实例预热等待中... ${warmup_waited}s / ${warmup_wait}s"
                    # 定期检查进程存活
                    check_process_alive "$PROVIDER1_PID" "Provider1" || {
                        log_error "Provider1 在预热等待期间异常退出"
                        exit 1
                    }
                    check_process_alive "$CONSUMER_PID" "Consumer" || {
                        log_error "Consumer 在预热等待期间异常退出"
                        exit 1
                    }
                    if [[ ${#EXTRA_PROVIDER_PIDS[@]} -gt 0 ]]; then
                        for extra_pid in "${EXTRA_PROVIDER_PIDS[@]}"; do
                            check_process_alive "$extra_pid" "额外老实例" || {
                                log_error "额外老实例 (PID: $extra_pid) 在预热等待期间异常退出"
                                exit 1
                            }
                        done
                    fi
                fi
            done
            log_info "老实例预热等待完成（${warmup_wait}s），现在所有老实例已完成预热"
        else
            log_warn "未获取到预热时长配置，默认等待 30s 让老实例完成预热"
            sleep 30
        fi

        # 记录基线实例列表
        baseline_instances=$(get_instance_list 2>/dev/null || echo "")
        if [[ -n "$baseline_instances" ]]; then
            echo "$baseline_instances" > "$baseline_file"
            log_info "基线实例列表（Provider2 启动前，共 ${needed_old_count} 个老实例）:"
            while IFS= read -r inst; do
                log_info "  - $inst"
            done <<< "$baseline_instances"
        fi

        old_port="$p1_port"

        # ==================== 步骤6B: 启动 Provider2（新实例） ====================
        log_step "6/8 启动 Provider2（新实例，开始预热）"

        local provider2_workdir="${BUILD_DIR}/provider2"
        mkdir -p "$provider2_workdir"
        cp "${BUILD_DIR}/polaris_provider.yaml" "${provider2_workdir}/polaris.yaml"

        provider2_log="${LOG_DIR}/provider2.log"
        (cd "$provider2_workdir" && "${BUILD_DIR}/provider" \
            --namespace "$NAMESPACE" \
            --service "$SERVICE_NAME" \
            --token "$POLARIS_TOKEN" \
            --port "$PROVIDER2_PORT" \
            > "$provider2_log" 2>&1) &
        PROVIDER2_PID=$!
        log_info "Provider2 已启动 (PID: $PROVIDER2_PID)"

        sleep 1
        check_process_alive "$PROVIDER2_PID" "Provider2" || {
            log_error "Provider2 启动失败，请检查日志: $provider2_log"
            cat "$provider2_log" 2>/dev/null || true
            exit 1
        }

        local p2_port
        if [[ "$PROVIDER2_PORT" != "0" ]]; then
            p2_port="$PROVIDER2_PORT"
            log_info "Provider2 使用指定端口: ${p2_port}"
        else
            p2_port=$(extract_port_from_log "$provider2_log" 20) || {
                log_error "无法获取 Provider2 端口，请检查日志: $provider2_log"
                cat "$provider2_log" 2>/dev/null || true
                exit 1
            }
            log_info "Provider2 实际监听端口: ${p2_port}"
        fi

        wait_for_http "http://127.0.0.1:${p2_port}/echo" 20 "Provider2" "$PROVIDER2_PID" || exit 1

        # 等待 Provider2 注册并出现在实例列表中
        log_info "等待 Provider2 注册到北极星..."
        if [[ -n "$baseline_instances" ]] && [[ -f "$baseline_file" ]]; then
            local new_inst
            new_inst=$(wait_for_new_instance "$baseline_file" 30) || {
            log_warn "未检测到新实例加入，继续等待 2s..."
                sleep 2
            }
            if [[ -n "$new_inst" ]]; then
                log_info "检测到新实例加入:"
                while IFS= read -r inst; do
                    log_info "  - $inst"
                    local detected_port
                    detected_port=$(echo "$inst" | sed 's/.*:\([0-9]*\)/\1/')
                    if [[ -n "$detected_port" ]] && [[ "$detected_port" != "$p1_port" ]]; then
                        p2_port="$detected_port"
                        log_info "Provider2 端口已通过实例列表确认: ${p2_port}"
                    fi
                done <<< "$new_inst"
            fi
        else
            log_info "等待 Provider2 注册到北极星（2s）..."
            sleep 2
        fi

        new_port="$p2_port"

        # 打印当前完整实例列表
        log_info "当前完整实例列表:"
        local current_all
        current_all=$(get_instance_list 2>/dev/null || echo "")
        if [[ -n "$current_all" ]]; then
            while IFS= read -r inst; do
                log_info "  - $inst"
            done <<< "$current_all"
        fi
    fi

    # ==================== 步骤7: 持续请求并统计流量分布 ====================

    # 根据规则中的预热时长自动计算观察时长（预热时长 + 30s 缓冲）
    if [[ $rule_warmup_seconds -gt 0 ]]; then
        local auto_observe=$((rule_warmup_seconds + 30))
        if [[ $WARMUP_OBSERVE_SECONDS -eq 0 ]]; then
            log_info "根据预热时长(${rule_warmup_seconds}s)自动设置观察时长为 ${auto_observe}s"
            WARMUP_OBSERVE_SECONDS=$auto_observe
        elif [[ $WARMUP_OBSERVE_SECONDS -lt $auto_observe ]]; then
            log_warn "预热观察时长(${WARMUP_OBSERVE_SECONDS}s) < 规则预热时长(${rule_warmup_seconds}s) + 30s缓冲"
            log_warn "自动调整预热观察时长为 ${auto_observe}s"
            WARMUP_OBSERVE_SECONDS=$auto_observe
        fi
    fi
    # 如果仍然为0（未获取到规则），使用默认值120s
    if [[ $WARMUP_OBSERVE_SECONDS -eq 0 ]]; then
        WARMUP_OBSERVE_SECONDS=120
        log_warn "未获取到预热时长规则，使用默认观察时长 120s"
    fi

    log_step "7/8 通过 echo-loop 持续请求并统计流量分布（预热观察期: ${WARMUP_OBSERVE_SECONDS}s）"

    if [[ -n "$old_ports" ]]; then
        log_info "老实例端口: ${old_ports}, 新实例端口: ${new_port}"
    else
        log_info "老实例端口: ${old_port}, 新实例端口: ${new_port}"
    fi

    # 初始化结果文件
    echo "时间戳,已运行秒数,老实例(非新实例)计数,新实例(${new_port})计数,新实例流量占比(%)" > "$RESULT_FILE"

    # 计算 echo-loop 的请求参数
    # REQUEST_INTERVAL 单位为秒，echo-loop 的 interval 单位为毫秒
    local loop_interval_ms
    loop_interval_ms=$(awk "BEGIN {printf \"%d\", $REQUEST_INTERVAL * 1000}")
    # 根据观察时长和请求间隔计算总请求次数
    local loop_count
    loop_count=$(awk "BEGIN {printf \"%d\", $WARMUP_OBSERVE_SECONDS / $REQUEST_INTERVAL}")
    if [[ $loop_count -lt 1 ]]; then
        loop_count=1
    fi

    log_info "echo-loop 参数: count=${loop_count}, interval=${loop_interval_ms}ms"

    local old_count=0
    local new_count=0
    local other_count=0
    local total_requests=0
    local fail_count=0

    echo ""
    printf "${BLUE}%-8s | %-12s | %-12s | %-14s | %-20s${NC}\n" \
        "已运行" "老实例" "新实例" "新实例占比" "区间新实例占比"
    printf "%-8s-+-%-12s-+-%-12s-+-%-14s-+-%-20s\n" \
        "--------" "------------" "------------" "--------------" "--------------------"

    local start_ts
    start_ts=$(date +%s)

    # 使用 curl 以 SSE 模式读取 echo-loop 的事件流
    # --no-buffer 确保实时输出，--max-time 设置超时防止无限等待
    local max_time=$((WARMUP_OBSERVE_SECONDS + 60))
    local sse_tmp="${LOG_DIR}/echo_loop_sse_$$.tmp"

    curl -s -N --no-buffer --max-time "$max_time" \
        "http://127.0.0.1:${CONSUMER_PORT}/echo-loop?count=${loop_count}&interval=${loop_interval_ms}" \
        2>/dev/null | while IFS= read -r line; do

        # SSE 格式: "data: {json}\n\n"
        if [[ "$line" == data:* ]]; then
            local json_data="${line#data: }"

            # 解析 JSON 中的统计数据
            local tag total fail
            tag=$(echo "$json_data" | grep -o '"tag":"[^"]*"' | sed 's/"tag":"\([^"]*\)"/\1/' || echo "")
            total=$(echo "$json_data" | grep -o '"total":[0-9]*' | sed 's/"total"://' || echo "0")
            fail=$(echo "$json_data" | grep -o '"fail":[0-9]*' | sed 's/"fail"://' || echo "0")

            total_requests=$total
            fail_count=$fail

            # 解析每个实例的统计
            # 从 instances 数组中提取各实例的 count 和 intervalCount
            local inst_old_count=0
            local inst_new_count=0
            local inst_old_interval=0
            local inst_new_interval=0
            local inst_other_count=0

            # 提取所有实例的 addr 和 count
            # 使用 grep 逐个提取实例块
            local instances_json
            instances_json=$(echo "$json_data" | grep -o '"instances":\[[^]]*\]' || echo "")

            if [[ -n "$instances_json" ]]; then
                # 提取每个实例的 addr, count, intervalCount
                local addrs counts interval_counts
                addrs=$(echo "$instances_json" | grep -o '"addr":"[^"]*"' | sed 's/"addr":"\([^"]*\)"/\1/')
                counts=$(echo "$instances_json" | grep -o '"count":[0-9]*' | sed 's/"count"://')
                interval_counts=$(echo "$instances_json" | grep -o '"intervalCount":[0-9]*' | sed 's/"intervalCount"://')

                # 逐行匹配
                local addr_arr=()
                local count_arr=()
                local icount_arr=()
                while IFS= read -r a; do addr_arr+=("$a"); done <<< "$addrs"
                while IFS= read -r c; do count_arr+=("$c"); done <<< "$counts"
                while IFS= read -r ic; do icount_arr+=("$ic"); done <<< "$interval_counts"

                for idx in "${!addr_arr[@]}"; do
                    local addr="${addr_arr[$idx]}"
                    local cnt="${count_arr[$idx]:-0}"
                    local icnt="${icount_arr[$idx]:-0}"

                    if [[ -n "$new_port" ]] && echo "$addr" | grep -q ":${new_port}$"; then
                        inst_new_count=$cnt
                        inst_new_interval=$icnt
                    else
                        # 不是新实例的流量全部归为老实例（支持多个老实例）
                        inst_old_count=$((inst_old_count + cnt))
                        inst_old_interval=$((inst_old_interval + icnt))
                    fi
                done
            fi

            old_count=$inst_old_count
            new_count=$inst_new_count

            # 计算比例
            local new_ratio="0.0"
            if [[ $total -gt 0 ]]; then
                new_ratio=$(awk "BEGIN {printf \"%.1f\", $inst_new_count * 100.0 / $total}")
            fi

            local interval_total=$((inst_new_interval + inst_old_interval))
            local interval_new_ratio="0.0"
            if [[ $interval_total -gt 0 ]]; then
                interval_new_ratio=$(awk "BEGIN {printf \"%.1f\", $inst_new_interval * 100.0 / $interval_total}")
            fi

            local now_ts
            now_ts=$(date +%s)
            local elapsed=$((now_ts - start_ts))

            printf "%-8s | %-12s | %-12s | %-14s | %-20s\n" \
                "${elapsed}s" "$inst_old_count" "$inst_new_count" "${new_ratio}%" "${interval_new_ratio}%"

            # 写入 CSV
            echo "$(date '+%Y-%m-%d %H:%M:%S'),${elapsed},${inst_old_count},${inst_new_count},${new_ratio}" >> "$RESULT_FILE"

            # 如果是 final 事件，保存最终统计到临时文件供后续汇总使用
            if [[ "$tag" == "final" ]]; then
                echo "${total}|${fail}|${inst_old_count}|${inst_new_count}|0" > "$sse_tmp"
            fi
        fi
    done

    # 从临时文件读取最终统计（因为 while 在子 shell 中，变量不会传递回来）
    if [[ -f "$sse_tmp" ]]; then
        IFS='|' read -r total_requests fail_count old_count new_count other_count < "$sse_tmp"
        rm -f "$sse_tmp"
    else
        log_warn "未收到 echo-loop 的最终统计事件，使用默认值"
    fi

    # ==================== 步骤8: 验证结果汇总 ====================
    log_step "8/8 验证结果汇总"

    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║                  验证结果汇总                    ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
    if [[ $existing_count -gt 0 ]]; then
        echo "  运行模式:          场景A（被调已有可用实例，仅启动一个新 Provider）"
    else
        echo "  运行模式:          场景B（被调无可用实例，启动 ${needed_old_count} 个老实例等预热结束再启动新实例）"
    fi
    echo "  总请求数:          ${total_requests}"
    if [[ -n "$old_ports" ]]; then
        echo "  老实例 (端口${old_ports}): ${old_count} 次"
    else
        echo "  老实例 (端口${old_port}): ${old_count} 次"
    fi
    echo "  新实例 (端口${new_port}): ${new_count} 次"
    echo "  请求失败:          ${fail_count} 次"
    if [[ ${other_count:-0} -gt 0 ]]; then
        echo "  其他/未匹配:       ${other_count} 次"
    fi

    if [[ $total_requests -gt 0 ]]; then
        local final_old_ratio
        final_old_ratio=$(awk "BEGIN {printf \"%.1f\", $old_count * 100.0 / $total_requests}")
        local final_new_ratio
        final_new_ratio=$(awk "BEGIN {printf \"%.1f\", $new_count * 100.0 / $total_requests}")
        echo "  老实例流量占比:    ${final_old_ratio}%"
        echo "  新实例流量占比:    ${final_new_ratio}%"
    fi

    echo ""
    echo "  流量分布详情 CSV:  ${RESULT_FILE}"
    echo "  Consumer 日志:     ${consumer_log}"
    if [[ -n "$PROVIDER1_PID" ]]; then
        echo "  Provider1 日志:    ${provider1_log}"
    fi
    echo "  Provider2 日志:    ${provider2_log}"
    echo ""

    # ==================== 预热终止保护检查 ====================
    if [[ "$overload_protection_enabled" == "true" ]]; then
        echo ""
        echo -e "${BLUE}── 预热终止保护（Overload Protection）检查 ──${NC}"
        echo "  预热终止保护:      已启用"
        echo "  触发阈值:          ${overload_protection_threshold}%"

        # 根据实际实例数计算预热实例占比
        local total_instance_count=0
        local warmup_instance_count=1  # 新实例始终是1个
        if [[ $existing_count -gt 0 ]]; then
            # 场景A: 已有实例 + 1个新实例
            total_instance_count=$((existing_count + 1))
        else
            # 场景B: needed_old_count 个老实例 + 1个新实例
            total_instance_count=$((needed_old_count + 1))
        fi
        local warmup_percentage=$((warmup_instance_count * 100 / total_instance_count))
        echo "  当前预热实例占比:  ${warmup_instance_count}/${total_instance_count} = ${warmup_percentage}%"

        if [[ $warmup_percentage -ge $overload_protection_threshold ]]; then
            echo ""
            log_warn "⚠️  预热终止保护可能已触发！"
            log_warn "预热实例占比(${warmup_percentage}%) >= 阈值(${overload_protection_threshold}%)"
            log_warn "当预热终止保护触发时，WarmupWeightAdjuster 将跳过权重调整，"
            log_warn "所有实例按原始权重均匀分配流量，预热效果将不会体现。"
            echo ""
            log_info "解决方案:"
            log_info "  1. 在北极星控制台调高预热终止保护阈值（当前: ${overload_protection_threshold}%）"
            log_info "  2. 关闭预热终止保护（enableOverloadProtection 设为 false）"
            log_info "  3. 增加老实例数量，降低预热实例占比"
        else
            log_info "预热实例占比(${warmup_percentage}%) < 阈值(${overload_protection_threshold}%)，预热终止保护未触发"
        fi
    fi

    # ==================== 验证预热效果 ====================
    echo ""
    echo -e "${BLUE}── 预热效果验证 ──${NC}"
    if [[ $new_count -eq 0 ]]; then
        log_warn "新实例未收到任何请求"
        log_warn "可能原因:"
        log_warn "  1. 北极星控制台未配置 LosslessRule 预热规则"
        log_warn "  2. 预热观察时间不足（当前: ${WARMUP_OBSERVE_SECONDS}s）"
        log_warn "  3. Consumer 的 weightAdjust 未正确启用"
        echo ""
        echo -e "${YELLOW}验证结论: 未能确认预热效果，请检查配置${NC}"
    elif [[ $new_count -lt $old_count ]]; then
        log_info "新实例流量少于老实例"
        log_info "这符合预热预期：新实例在预热期间承接较少流量"
        echo ""
        echo -e "${GREEN}验证结论: ✅ 预热功能正常生效！新实例流量被成功限制${NC}"
    else
        log_info "新实例流量 >= 老实例"
        if [[ "$overload_protection_enabled" == "true" ]]; then
            local check_total=0
            local check_warmup=1
            if [[ $existing_count -gt 0 ]]; then
                check_total=$((existing_count + 1))
            else
                check_total=$((needed_old_count + 1))
            fi
            local check_pct=$((check_warmup * 100 / check_total))
            if [[ $check_pct -ge $overload_protection_threshold ]]; then
                log_warn "这很可能是预热终止保护触发导致的（预热实例占比 ${check_pct}% >= 阈值 ${overload_protection_threshold}%）"
                log_warn "预热终止保护触发后，所有实例按原始权重均匀分配流量"
                echo ""
                echo -e "${YELLOW}验证结论: ⚠️  预热终止保护已触发，预热权重调整被跳过${NC}"
            else
                log_info "这可能表示:"
                log_info "  1. 预热已完成（观察时间超过预热时长）"
                log_info "  2. 预热规则未正确配置"
                echo ""
                echo -e "${YELLOW}验证结论: 新实例流量未被限制，请检查预热配置和时长${NC}"
            fi
        else
            log_info "这可能表示:"
            log_info "  1. 预热已完成（观察时间超过预热时长）"
            log_info "  2. 预热规则未正确配置"
            echo ""
            echo -e "${YELLOW}验证结论: 新实例流量未被限制，请检查预热配置和时长${NC}"
        fi
    fi

    echo ""
    echo -e "${BLUE}提示: 查看 Consumer 的 DEBUG 日志可以看到详细的权重计算过程:${NC}"
    echo -e "${BLUE}  grep 'WarmupWeightAdjuster' ${consumer_log} | tail -20${NC}"
    echo ""
}

main "$@"
