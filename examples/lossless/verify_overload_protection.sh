#!/bin/bash
# =============================================================================
# 预热终止保护（Overload Protection）功能验证脚本
#
# 使用方法:
#   chmod +x verify_overload_protection.sh
#   ./verify_overload_protection.sh [--polaris-server <地址>] [--polaris-token <令牌>]
#                                   [--service <服务名>] [--namespace <命名空间>]
#                                   [--observe-seconds <观察秒数>]
#                                   [--request-interval <请求间隔秒数>]
#
# 前置条件:
#   1. 北极星服务端(Polaris Server)已启动
#   2. 已在北极星控制台为目标服务配置 LosslessRule（预热规则），且启用了过载保护
#   3. Go 环境已安装
#
# 验证原理:
#   过载保护的触发条件：预热中的实例占比 >= 阈值（threshold）
#   本脚本同时启动2个新 Provider（都处于预热期），预热实例占比=100%，
#   必然触发过载保护。触发后 WarmupWeightAdjuster 跳过权重调整，
#   所有实例按原始权重均匀分配流量。
#
# 验证流程:
#   1. 编译 provider 和 consumer
#   2. 环境准备
#   3. 启动 consumer
#   4. 获取无损上线规则，确认过载保护已启用
#   5. 同时启动2个新 Provider（都处于预热期）
#   6. 持续发起请求，统计流量在两个实例间的分布
#   7. 验证流量是否均匀分配（过载保护生效 → 均匀；未生效 → 不均匀）
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
OBSERVE_SECONDS="${OBSERVE_SECONDS:-60}"
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
        --observe-seconds)
            OBSERVE_SECONDS="$2"; shift 2 ;;
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
            echo "  --observe-seconds <秒>      观察时长 (默认: 60)"
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
RESULT_FILE="${LOG_DIR}/overload_protection_result.csv"

CONSUMER_PID=""
CONSUMER_REUSED=false
PROVIDER1_PID=""
PROVIDER2_PID=""

# ======================== 清理函数 ========================
cleanup() {
    log_info "正在清理进程..."
    for pid_var in CONSUMER_PID PROVIDER1_PID PROVIDER2_PID; do
        # 复用的 Consumer 不清理
        if [[ "$pid_var" == "CONSUMER_PID" ]] && [[ "$CONSUMER_REUSED" == "true" ]]; then
            log_info "Consumer 为复用的已有进程，跳过清理"
            continue
        fi
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

# 从 lossless 规则 JSON 中提取预热终止保护是否启用
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

# 从 lossless 规则 JSON 中提取延迟注册时长秒数
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

# 从 lossless 规则 JSON 中提取预热终止保护阈值（百分比）
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

# 通过 Consumer 的 /instances 接口获取当前实例列表
get_instance_list() {
    local consumer_url="http://127.0.0.1:${CONSUMER_PORT}/instances"
    local resp
    resp=$(curl -s --connect-timeout 5 "$consumer_url" 2>/dev/null || echo "")
    if [[ -z "$resp" ]]; then
        return 1
    fi
    local tmp_hosts="/tmp/_op_hosts_$$.tmp"
    local tmp_ports="/tmp/_op_ports_$$.tmp"
    echo "$resp" | grep -o '"host":"[^"]*"' | sed 's/"host":"\([^"]*\)"/\1/' > "$tmp_hosts"
    echo "$resp" | grep -o '"port":[0-9]*' | sed 's/"port"://' > "$tmp_ports"
    paste -d':' "$tmp_hosts" "$tmp_ports"
    rm -f "$tmp_hosts" "$tmp_ports"
    return 0
}

# 等待至少有指定数量的实例注册到北极星
wait_for_instance_count() {
    local expected_count="$1"
    local max_wait="${2:-60}"
    local waited=0

    while [[ $waited -lt $max_wait ]]; do
        local instance_list
        instance_list=$(get_instance_list 2>/dev/null || echo "")
        if [[ -n "$instance_list" ]]; then
            local count
            count=$(echo "$instance_list" | wc -l | tr -d ' ')
            if [[ $count -ge $expected_count ]]; then
                log_info "检测到 ${count} 个已注册实例（期望 >= ${expected_count}）"
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
    log_error "等待实例注册超时（${max_wait}s），期望 ${expected_count} 个实例"
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
    echo -e "${BLUE}╔══════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║   预热终止保护(Overload Protection)功能验证脚本         ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "配置信息:"
    echo "  北极星服务端:     ${POLARIS_SERVER}:8091"
    echo "  服务名:           ${SERVICE_NAME}"
    echo "  命名空间:         ${NAMESPACE}"
    echo "  Consumer端口:     ${CONSUMER_PORT}"
    echo "  Debug日志:        ${DEBUG_MODE}"
    echo "  观察时长:         ${OBSERVE_SECONDS}s"
    echo "  请求间隔:         ${REQUEST_INTERVAL}s"
    echo ""
    echo -e "${YELLOW}验证原理:${NC}"
    echo "  同时启动2个新 Provider（都处于预热期），预热实例占比=100%"
    echo "  如果过载保护已启用且阈值 <= 100%，则必然触发过载保护"
    echo "  触发后 WarmupWeightAdjuster 跳过权重调整，流量应均匀分配"
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

    if command -v xattr &> /dev/null; then
        xattr -c "${BUILD_DIR}/provider" 2>/dev/null || true
        xattr -c "${BUILD_DIR}/consumer" 2>/dev/null || true
        log_info "已清除二进制文件的 macOS quarantine 属性"
    fi

    generate_polaris_yaml "$BUILD_DIR" "provider"
    cp "${BUILD_DIR}/polaris.yaml" "${BUILD_DIR}/polaris_provider.yaml"
    generate_polaris_yaml "$BUILD_DIR" "consumer"
    cp "${BUILD_DIR}/polaris.yaml" "${BUILD_DIR}/polaris_consumer.yaml"

    # ==================== 步骤3: 启动 Consumer ====================
    log_step "3/7 启动 Consumer"

    local consumer_log="${LOG_DIR}/consumer.log"

    # 检查是否已有 Consumer 在运行（通过端口检测）
    if curl -s --connect-timeout 2 "http://127.0.0.1:${CONSUMER_PORT}/instances" > /dev/null 2>&1; then
        log_info "检测到端口 ${CONSUMER_PORT} 上已有 Consumer 在运行，直接复用"
        CONSUMER_REUSED=true
        # 尝试获取已有 Consumer 的 PID（用于后续清理判断）
        local existing_pid
        existing_pid=$(lsof -ti :"$CONSUMER_PORT" 2>/dev/null | head -1 || echo "")
        if [[ -n "$existing_pid" ]]; then
            CONSUMER_PID="$existing_pid"
            log_info "已有 Consumer PID: $CONSUMER_PID"
        else
            log_warn "无法获取已有 Consumer 的 PID，退出时不会自动清理该进程"
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

        sleep 1
        check_process_alive "$CONSUMER_PID" "Consumer" || {
            log_error "Consumer 启动失败，请检查日志: $consumer_log"
            cat "$consumer_log" 2>/dev/null || true
            exit 1
        }

        wait_for_http "http://127.0.0.1:${CONSUMER_PORT}/instances" 20 "Consumer" "$CONSUMER_PID" || exit 1
    fi

    # ==================== 步骤4: 获取无损上线规则，确认过载保护配置 ====================
    log_step "4/7 获取无损上线规则，确认过载保护配置"

    local lossless_rule_json=""
    local rule_warmup_seconds=0
    local overload_protection_enabled="false"
    local overload_protection_threshold=0
    local delay_register_seconds=0

    lossless_rule_json=$(get_lossless_rule 2>/dev/null || echo "")
    if [[ -n "$lossless_rule_json" ]]; then
        log_info "获取到无损上线规则: ${lossless_rule_json}"

        rule_warmup_seconds=$(get_warmup_seconds "$lossless_rule_json")
        overload_protection_enabled=$(get_overload_protection_enabled "$lossless_rule_json")
        overload_protection_threshold=$(get_overload_protection_threshold "$lossless_rule_json")
        delay_register_seconds=$(get_delay_register_seconds "$lossless_rule_json")

        log_info "预热时长配置: ${rule_warmup_seconds}s"
        if [[ $delay_register_seconds -gt 0 ]]; then
            log_info "延迟注册配置: ${delay_register_seconds}s"
        fi

        if [[ "$overload_protection_enabled" == "true" ]]; then
            log_info "预热终止保护: 已启用（阈值: ${overload_protection_threshold}%）"
        else
            log_warn "预热终止保护: 未启用"
            log_warn "本脚本用于验证过载保护功能，请先在北极星控制台启用过载保护"
            log_warn "继续执行，但验证结果可能不符合预期..."
        fi
    else
        log_warn "无法获取无损上线规则"
        log_warn "继续执行，但验证结果可能不符合预期..."
    fi

    # 检查被调服务是否已有可用实例
    local existing_instances=""
    existing_instances=$(get_instance_list 2>/dev/null || echo "")
    local existing_count=0
    if [[ -n "$existing_instances" ]]; then
        existing_count=$(echo "$existing_instances" | wc -l | tr -d ' ')
    fi

    if [[ $existing_count -gt 0 ]]; then
        log_warn "检测到被调服务已有 ${existing_count} 个可用实例:"
        while IFS= read -r inst; do
            log_warn "  - $inst"
        done <<< "$existing_instances"
        log_warn "已有实例可能影响过载保护的触发判断"
        log_warn "建议先清理已有实例后再运行本脚本（使用 cleanup.sh）"
        echo ""
        echo -e "${YELLOW}是否继续？(y/N)${NC}"
        read -r confirm
        if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
            log_info "用户取消，退出"
            exit 0
        fi
    fi

    # ==================== 步骤5: 同时启动2个新 Provider ====================
    log_step "5/7 同时启动2个新 Provider（都处于预热期）"

    log_info "关键: 两个 Provider 同时启动，都是新实例，预热实例占比=100%"
    if [[ $existing_count -gt 0 ]]; then
        local expected_warmup_pct=$((2 * 100 / (existing_count + 2)))
        log_info "考虑已有 ${existing_count} 个实例，预热实例占比预计为: 2/${existing_count}+2 = ${expected_warmup_pct}%"
        if [[ $expected_warmup_pct -lt $overload_protection_threshold ]]; then
            log_warn "预热实例占比(${expected_warmup_pct}%) < 阈值(${overload_protection_threshold}%)，过载保护可能不会触发"
        fi
    fi

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

    wait_for_http "http://127.0.0.1:${p1_port}/echo" 20 "Provider1" "$PROVIDER1_PID" || exit 1
    wait_for_http "http://127.0.0.1:${p2_port}/echo" 20 "Provider2" "$PROVIDER2_PID" || exit 1

    # 等待两个实例都注册到北极星
    log_info "等待两个 Provider 注册到北极星..."
    local expected_total=$((existing_count + 2))
    local register_wait=120
    if [[ $delay_register_seconds -gt 0 ]]; then
        register_wait=$((delay_register_seconds + 10))
        log_info "检测到延迟注册配置（${delay_register_seconds}s），等待时间设为 ${register_wait}s"
    fi
    wait_for_instance_count "$expected_total" "$register_wait" || {
        log_error "两个 Provider 未能在 ${register_wait}s 内全部注册到北极星"
        exit 1
    }

    # 打印当前完整实例列表
    log_info "当前完整实例列表:"
    local current_all
    current_all=$(get_instance_list 2>/dev/null || echo "")
    if [[ -n "$current_all" ]]; then
        while IFS= read -r inst; do
            log_info "  - $inst"
        done <<< "$current_all"
    fi

    # 发起基线请求验证链路
    log_info "发起基线请求（验证链路正常）..."
    local baseline_ok=0
    for i in $(seq 1 5); do
        local http_code resp
        http_code=$(curl -s -o /tmp/_op_resp_$$.tmp -w '%{http_code}' --connect-timeout 5 \
            "http://127.0.0.1:${CONSUMER_PORT}/echo" 2>/dev/null || echo "000")
        resp=$(cat /tmp/_op_resp_$$.tmp 2>/dev/null || echo "FAILED")
        rm -f /tmp/_op_resp_$$.tmp
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
        exit 1
    fi
    log_info "基线请求通过 (${baseline_ok}/5 成功)，链路正常"

    # ==================== 步骤6: 持续请求并统计流量分布 ====================
    log_step "6/7 持续请求并统计流量分布（观察期: ${OBSERVE_SECONDS}s）"

    log_info "Provider1 端口: ${p1_port}, Provider2 端口: ${p2_port}"
    if [[ "$overload_protection_enabled" == "true" ]]; then
        log_info "过载保护已启用（阈值: ${overload_protection_threshold}%），预期流量均匀分配"
    else
        log_info "过载保护未启用，预期新实例流量被预热限制"
    fi

    # 初始化结果文件
    echo "时间戳,已运行秒数,Provider1(${p1_port})计数,Provider2(${p2_port})计数,P1占比(%),P2占比(%)" > "$RESULT_FILE"

    # 计算 echo-loop 参数
    local loop_interval_ms
    loop_interval_ms=$(awk "BEGIN {printf \"%d\", $REQUEST_INTERVAL * 1000}")
    local loop_count
    loop_count=$(awk "BEGIN {printf \"%d\", $OBSERVE_SECONDS / $REQUEST_INTERVAL}")
    if [[ $loop_count -lt 1 ]]; then
        loop_count=1
    fi

    log_info "echo-loop 参数: count=${loop_count}, interval=${loop_interval_ms}ms"

    local p1_count=0
    local p2_count=0
    local other_count=0
    local total_requests=0
    local fail_count=0

    echo ""
    printf "${BLUE}%-8s | %-14s | %-14s | %-12s | %-12s | %-18s | %-18s${NC}\n" \
        "已运行" "Provider1" "Provider2" "P1占比" "P2占比" "区间P1占比" "区间P2占比"
    printf "%-8s-+-%-14s-+-%-14s-+-%-12s-+-%-12s-+-%-18s-+-%-18s\n" \
        "--------" "--------------" "--------------" "------------" "------------" "------------------" "------------------"

    local start_ts
    start_ts=$(date +%s)

    local max_time=$((OBSERVE_SECONDS + 60))
    local sse_tmp="${LOG_DIR}/echo_loop_op_sse_$$.tmp"

    curl -s -N --no-buffer --max-time "$max_time" \
        "http://127.0.0.1:${CONSUMER_PORT}/echo-loop?count=${loop_count}&interval=${loop_interval_ms}" \
        2>/dev/null | while IFS= read -r line; do

        if [[ "$line" == data:* ]]; then
            local json_data="${line#data: }"

            local tag total fail
            tag=$(echo "$json_data" | grep -o '"tag":"[^"]*"' | sed 's/"tag":"\([^"]*\)"/\1/' || echo "")
            total=$(echo "$json_data" | grep -o '"total":[0-9]*' | sed 's/"total"://' || echo "0")
            fail=$(echo "$json_data" | grep -o '"fail":[0-9]*' | sed 's/"fail"://' || echo "0")

            total_requests=$total
            fail_count=$fail

            local inst_p1_count=0
            local inst_p2_count=0
            local inst_p1_interval=0
            local inst_p2_interval=0
            local inst_other_count=0

            local instances_json
            instances_json=$(echo "$json_data" | grep -o '"instances":\[[^]]*\]' || echo "")

            if [[ -n "$instances_json" ]]; then
                local addrs counts interval_counts
                addrs=$(echo "$instances_json" | grep -o '"addr":"[^"]*"' | sed 's/"addr":"\([^"]*\)"/\1/')
                counts=$(echo "$instances_json" | grep -o '"count":[0-9]*' | sed 's/"count"://')
                interval_counts=$(echo "$instances_json" | grep -o '"intervalCount":[0-9]*' | sed 's/"intervalCount"://')

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

                    if echo "$addr" | grep -q ":${p1_port}$"; then
                        inst_p1_count=$cnt
                        inst_p1_interval=$icnt
                    elif echo "$addr" | grep -q ":${p2_port}$"; then
                        inst_p2_count=$cnt
                        inst_p2_interval=$icnt
                    else
                        inst_other_count=$((inst_other_count + cnt))
                    fi
                done
            fi

            p1_count=$inst_p1_count
            p2_count=$inst_p2_count

            local p1_ratio="0.0" p2_ratio="0.0"
            if [[ $total -gt 0 ]]; then
                p1_ratio=$(awk "BEGIN {printf \"%.1f\", $inst_p1_count * 100.0 / $total}")
                p2_ratio=$(awk "BEGIN {printf \"%.1f\", $inst_p2_count * 100.0 / $total}")
            fi

            local interval_total=$((inst_p1_interval + inst_p2_interval))
            local ip1_ratio="0.0" ip2_ratio="0.0"
            if [[ $interval_total -gt 0 ]]; then
                ip1_ratio=$(awk "BEGIN {printf \"%.1f\", $inst_p1_interval * 100.0 / $interval_total}")
                ip2_ratio=$(awk "BEGIN {printf \"%.1f\", $inst_p2_interval * 100.0 / $interval_total}")
            fi

            local now_ts
            now_ts=$(date +%s)
            local elapsed=$((now_ts - start_ts))

            printf "%-8s | %-14s | %-14s | %-12s | %-12s | %-18s | %-18s\n" \
                "${elapsed}s" "$inst_p1_count" "$inst_p2_count" "${p1_ratio}%" "${p2_ratio}%" "${ip1_ratio}%" "${ip2_ratio}%"

            echo "$(date '+%Y-%m-%d %H:%M:%S'),${elapsed},${inst_p1_count},${inst_p2_count},${p1_ratio},${p2_ratio}" >> "$RESULT_FILE"

            if [[ "$tag" == "final" ]]; then
                echo "${total}|${fail}|${inst_p1_count}|${inst_p2_count}|${inst_other_count}" > "$sse_tmp"
            fi
        fi
    done

    # 从临时文件读取最终统计
    if [[ -f "$sse_tmp" ]]; then
        IFS='|' read -r total_requests fail_count p1_count p2_count other_count < "$sse_tmp"
        rm -f "$sse_tmp"
    else
        log_warn "未收到 echo-loop 的最终统计事件，使用默认值"
    fi

    # ==================== 步骤7: 验证结果汇总 ====================
    log_step "7/7 验证结果汇总"

    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║            预热终止保护验证结果汇总                      ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "  总请求数:          ${total_requests}"
    echo "  请求失败:          ${fail_count}"
    echo "  Provider1 (端口${p1_port}): ${p1_count} 次"
    echo "  Provider2 (端口${p2_port}): ${p2_count} 次"
    echo "  其他/未匹配:       ${other_count} 次"

    local final_p1_ratio="0.0" final_p2_ratio="0.0"
    if [[ $total_requests -gt 0 ]]; then
        final_p1_ratio=$(awk "BEGIN {printf \"%.1f\", $p1_count * 100.0 / $total_requests}")
        final_p2_ratio=$(awk "BEGIN {printf \"%.1f\", $p2_count * 100.0 / $total_requests}")
        echo "  Provider1 流量占比: ${final_p1_ratio}%"
        echo "  Provider2 流量占比: ${final_p2_ratio}%"
    fi

    echo ""
    echo "  流量分布详情 CSV:  ${RESULT_FILE}"
    echo "  Consumer 日志:     ${consumer_log}"
    echo "  Provider1 日志:    ${provider1_log}"
    echo "  Provider2 日志:    ${provider2_log}"

    # ==================== 过载保护配置信息 ====================
    echo ""
    echo -e "${BLUE}── 过载保护配置 ──${NC}"
    if [[ "$overload_protection_enabled" == "true" ]]; then
        echo "  过载保护:          已启用"
        echo "  触发阈值:          ${overload_protection_threshold}%"
        echo "  预热时长:          ${rule_warmup_seconds}s"
        echo "  预热实例数:        2（两个 Provider 都是新启动的）"
        local total_inst_count=$((existing_count + 2))
        local warmup_pct=$((2 * 100 / total_inst_count))
        echo "  总实例数:          ${total_inst_count}"
        echo "  预热实例占比:      2/${total_inst_count} = ${warmup_pct}%"

        if [[ $warmup_pct -ge $overload_protection_threshold ]]; then
            echo ""
            log_info "预热实例占比(${warmup_pct}%) >= 阈值(${overload_protection_threshold}%)，过载保护应已触发"
        else
            echo ""
            log_warn "预热实例占比(${warmup_pct}%) < 阈值(${overload_protection_threshold}%)，过载保护未触发"
        fi
    else
        echo "  过载保护:          未启用"
        echo "  预热时长:          ${rule_warmup_seconds}s"
    fi

    # ==================== 验证过载保护效果 ====================
    echo ""
    echo -e "${BLUE}── 过载保护效果验证 ──${NC}"

    if [[ $total_requests -eq 0 ]]; then
        log_error "没有成功的请求，无法验证"
        echo -e "${RED}验证结论: ❌ 验证失败，没有成功的请求${NC}"
    elif [[ $p1_count -eq 0 ]] || [[ $p2_count -eq 0 ]]; then
        log_warn "有一个实例未收到任何请求"
        log_warn "可能原因:"
        log_warn "  1. 实例未成功注册到北极星"
        log_warn "  2. 观察时间不足（当前: ${OBSERVE_SECONDS}s）"
        echo ""
        echo -e "${YELLOW}验证结论: ⚠️ 流量分布异常，请检查配置${NC}"
    else
        # 计算两个实例的流量差异
        local diff
        diff=$(awk "BEGIN {d = $final_p1_ratio - $final_p2_ratio; if (d < 0) d = -d; printf \"%.1f\", d}")

        if [[ "$overload_protection_enabled" == "true" ]]; then
            local total_inst_count=$((existing_count + 2))
            local warmup_pct=$((2 * 100 / total_inst_count))

            if [[ $warmup_pct -ge $overload_protection_threshold ]]; then
                # 过载保护应该触发，流量应该均匀
                echo ""
                log_info "过载保护应已触发（预热占比 ${warmup_pct}% >= 阈值 ${overload_protection_threshold}%）"
                log_info "两个实例的流量差异: ${diff}%"

                if awk "BEGIN {exit !($diff <= 20.0)}"; then
                    echo ""
                    log_info "流量差异 ${diff}% <= 20%，两个实例流量基本均匀"
                    echo ""
                    echo -e "${GREEN}验证结论: ✅ 过载保护功能正常！${NC}"
                    echo -e "${GREEN}  过载保护触发后，WarmupWeightAdjuster 跳过了权重调整，${NC}"
                    echo -e "${GREEN}  两个新实例按原始权重均匀分配流量（P1: ${final_p1_ratio}%, P2: ${final_p2_ratio}%）${NC}"
                else
                    echo ""
                    log_warn "流量差异 ${diff}% > 20%，两个实例流量不均匀"
                    log_warn "可能原因:"
                    log_warn "  1. 过载保护未正确触发"
                    log_warn "  2. 负载均衡策略本身的波动"
                    log_warn "  3. 观察时间不足"
                    echo ""
                    echo -e "${YELLOW}验证结论: ⚠️ 流量分布偏差较大，过载保护可能未正确生效${NC}"
                fi
            else
                # 过载保护不应触发，流量应该不均匀（预热生效）
                echo ""
                log_info "过载保护未触发（预热占比 ${warmup_pct}% < 阈值 ${overload_protection_threshold}%）"
                log_info "预热应正常生效，两个新实例都应承接较少流量"
                echo ""
                echo -e "${YELLOW}验证结论: 过载保护未触发（已有实例过多），请减少已有实例后重试${NC}"
            fi
        else
            # 过载保护未启用，作为对照组
            echo ""
            log_info "过载保护未启用（对照组）"
            log_info "两个实例的流量差异: ${diff}%"

            if awk "BEGIN {exit !($diff <= 20.0)}"; then
                log_info "流量基本均匀（差异 ${diff}% <= 20%）"
                log_info "由于两个实例都是新实例且权重相同，即使预热生效，它们之间也可能均匀分配"
                echo ""
                echo -e "${YELLOW}验证结论: 过载保护未启用，无法验证过载保护功能${NC}"
                echo -e "${YELLOW}  请在北极星控制台启用过载保护后重新运行本脚本${NC}"
            else
                log_info "流量不均匀（差异 ${diff}% > 20%）"
                echo ""
                echo -e "${YELLOW}验证结论: 过载保护未启用，流量分布不均匀${NC}"
                echo -e "${YELLOW}  请在北极星控制台启用过载保护后重新运行本脚本进行对比${NC}"
            fi
        fi
    fi

    # ==================== Consumer 日志中的过载保护记录 ====================
    echo ""
    echo -e "${BLUE}── Consumer 日志中的过载保护记录 ──${NC}"
    local polaris_sdk_log="${BUILD_DIR}/consumer_run/polaris/log/lossless/polaris-lossless.log"
    if [[ ! -f "$polaris_sdk_log" ]]; then
        log_warn "未找到 Polaris SDK 日志文件: $polaris_sdk_log"
        log_warn "Consumer 可能未正确初始化 Polaris SDK"
    else
        local op_logs
        op_logs=$(grep 'overload protection triggered' "$polaris_sdk_log" 2>/dev/null | tail -5 || echo "")
        if [[ -n "$op_logs" ]]; then
            log_info "检测到过载保护触发日志:"
            while IFS= read -r logline; do
                echo "  $logline"
            done <<< "$op_logs"
        else
            log_info "未检测到过载保护触发日志"
            log_info "检查 overload check 日志:"
            local check_logs
            check_logs=$(grep 'overload check' "$polaris_sdk_log" 2>/dev/null | tail -5 || echo "")
            if [[ -n "$check_logs" ]]; then
                while IFS= read -r logline; do
                    echo "  $logline"
                done <<< "$check_logs"
            else
                log_warn "未找到任何过载保护相关日志"
            fi
        fi
    fi

    echo ""
    local polaris_sdk_log="${BUILD_DIR}/consumer_run/polaris/log/lossless/polaris-lossless.log"
    echo -e "${BLUE}提示: 查看 Consumer 的 DEBUG 日志可以看到详细的过载保护判断过程:${NC}"
    echo -e "${BLUE}  grep 'overload' ${polaris_sdk_log} | tail -20${NC}"
    echo ""
}

main "$@"
