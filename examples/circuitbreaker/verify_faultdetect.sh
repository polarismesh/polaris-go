#!/bin/bash
# =============================================================================
# 熔断主动探测（Fault Detect / Active Health Check）端到端验证脚本
#
# 使用方法:
#   chmod +x verify_faultdetect.sh
#   POLARIS_SERVER=<地址> ./verify_faultdetect.sh [--polaris-token <令牌>]
#                                                  [--namespace <命名空间>] [--debug]
#
# 前置条件:
#   1. 北极星服务端已启动（gRPC <server>:8091 / HTTP 管控 <server>:8090）
#   2. Go 环境（与 examples/circuitbreaker/*/go.mod 兼容）
#   3. python3 / curl / lsof / grep
#   说明：脚本通过 Polaris HTTP API 自动创建/删除「熔断规则」与「主动探测规则
#   (FaultDetectRule)」，无需手动操作控制台。
#
# 验证目标 —— 主动探测完整闭环:
#   业务请求触发服务级熔断 OPEN
#     → SDK 周期性主动探测 provider /echo（provider 仍故障，探测失败 → 维持 OPEN）
#     → 恢复 provider（业务与探测同时转绿）
#     → 主动探测探活成功，推动 HALF_OPEN → CLOSE
#
#   关键设计：探测端点选 /echo（受 provider needErr 开关控制），与业务同源，
#   provider 挂时探测必然失败、恢复时探测必然成功，因果可被稳定验证。
#   （provider /health 固定 200，不能反映健康变化，不可用作探测端点。）
#
# 拓扑:
#   provider-a / provider-b（均注册到 CircuitBreakerCallee） ── Polaris ──
#     consumer(FaultDetectCaller, HTTP 18095) ←── 脚本 curl /echo
#
# 闭环依赖的 SDK 能力（plugin/circuitbreaker/composite）:
#   - 熔断规则 faultDetectConfig.enable=true 作为探测启动门控
#   - ResourceHealthChecker 周期调度（TaskExecutor 注入）
#   - 探测结果走 record=false 上报，参与 HALF_OPEN 恢复判定
# =============================================================================

set -euo pipefail

# ======================== 默认配置 ========================
POLARIS_SERVER="${POLARIS_SERVER:-127.0.0.1}"
POLARIS_HTTP_PORT="${POLARIS_HTTP_PORT:-8090}"
POLARIS_TOKEN="${POLARIS_TOKEN:-}"
NAMESPACE="${NAMESPACE:-default}"
SERVICE_NAME="${SERVICE_NAME:-CircuitBreakerCallee}"
# 主调服务名：独立 caller，避免与 verify_circuitbreaker.sh 的用例规则相互干扰
FD_CALLER="${FD_CALLER:-CircuitBreakerFaultDetectCaller}"

# 端口规划（与 verify_circuitbreaker.sh 共用 provider 端口；consumer 用独立端口段）
PROVIDER_A_PORT="${PROVIDER_A_PORT:-28081}"
PROVIDER_B_PORT="${PROVIDER_B_PORT:-28082}"
FD_CONSUMER_PORT="${FD_CONSUMER_PORT:-18095}"

# 触发服务级熔断的 burst 次数：两实例全 500 时累计错误触发 SERVICE 级 OPEN，
# 50/50 LB 下需足够大保证 CONSECUTIVE_ERROR / ERROR_RATE 命中。
FD_TRIGGER_COUNT="${FD_TRIGGER_COUNT:-30}"
# 维持 OPEN / 恢复阶段的验证 burst 次数
FD_VERIFY_COUNT="${FD_VERIFY_COUNT:-10}"
# 熔断规则连续错误阈值
FD_CONSECUTIVE_ERROR="${FD_CONSECUTIVE_ERROR:-5}"
# 熔断 sleepWindow（秒）：进入半开的等待窗口
FD_SLEEP_WINDOW="${FD_SLEEP_WINDOW:-6}"
# 主动探测间隔（秒）
FD_PROBE_INTERVAL="${FD_PROBE_INTERVAL:-2}"
# 等待 SDK 拉取规则就绪
WAIT_RULE_READY_SECONDS="${WAIT_RULE_READY_SECONDS:-6}"
# 维持 OPEN 阶段等待（> sleepWindow，确认探测失败不恢复）
WAIT_KEEP_OPEN_SECONDS="${WAIT_KEEP_OPEN_SECONDS:-12}"
# 恢复阶段等待（sleepWindow + 数个探测周期，纯靠主动探测推动 CLOSE）
WAIT_RECOVER_SECONDS="${WAIT_RECOVER_SECONDS:-16}"

DEBUG_MODE="${DEBUG_MODE:-false}"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# ======================== 解析命令行参数 ========================
while [[ $# -gt 0 ]]; do
    case "$1" in
        --polaris-server) POLARIS_SERVER="$2"; shift 2 ;;
        --polaris-token)  POLARIS_TOKEN="$2"; shift 2 ;;
        --namespace)      NAMESPACE="$2"; shift 2 ;;
        --service)        SERVICE_NAME="$2"; shift 2 ;;
        --debug)          DEBUG_MODE="true"; shift ;;
        -h|--help)
            grep -E '^#( |$)' "$0" | sed 's/^# \{0,1\}//'
            exit 0
            ;;
        *) echo "未知参数: $1"; exit 1 ;;
    esac
done

POLARIS_HTTP_ADDR="http://${POLARIS_SERVER}:${POLARIS_HTTP_PORT}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROVIDER_A_DIR="${SCRIPT_DIR}/callee/provider-a"
PROVIDER_B_DIR="${SCRIPT_DIR}/callee/provider-b"
CONSUMER_DIR="${SCRIPT_DIR}/newCircuitBreakerCaller/consumer"
BUILD_DIR="${SCRIPT_DIR}/.build"
LOG_DIR="${SCRIPT_DIR}/.logs"
TEST_LOG_FILE="${LOG_DIR}/verify_faultdetect-$(date +%Y%m%d_%H%M%S).log"

PROVIDER_A_PID=""
PROVIDER_B_PID=""
CONSUMER_PIDS=()
# 已创建的熔断规则 / 探测规则 id（退出时清理）
CREATED_CB_RULE_IDS=()
CREATED_FD_RULE_IDS=()

# ======================== 工具函数 ========================
# 日志统一写 stderr，避免被 r=$(func) 命令替换捕获污染返回值。
log_info()  { echo -e "${GREEN}[INFO]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*" >&2; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*" >&2; }
log_error() { echo -e "${RED}[ERROR]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*" >&2; }
log_step() {
    echo "" >&2
    echo -e "${CYAN}========================================${NC}" >&2
    echo -e "${CYAN}  步骤: $*${NC}" >&2
    echo -e "${CYAN}========================================${NC}" >&2
}
print_block() {
    local title="$1"; shift
    echo -e "${BLUE}┌- ${title} -----------------------------------------${NC}" >&2
    while [[ $# -gt 0 ]]; do
        echo -e "${BLUE}|${NC} $1" >&2
        shift
    done
    echo -e "${BLUE}+----------------------------------------------------------${NC}" >&2
}

setup_test_log() {
    mkdir -p "${LOG_DIR}"
    {
        echo "===== 熔断主动探测验证日志 $(date '+%Y-%m-%d %H:%M:%S') ====="
        echo "Command: $0 $*"
    } > "${TEST_LOG_FILE}"
    exec > >(tee >(sed -u 's/\x1b\[[0-9;]*m//g' >> "${TEST_LOG_FILE}")) 2>&1
}

check_process_alive() {
    local pid="$1"
    local name="${2:-进程}"
    if ! kill -0 "$pid" 2>/dev/null; then
        log_error "${name} (PID: $pid) 已异常退出"
        return 1
    fi
    return 0
}

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

# 倒计时等待，便于读日志观察探测/状态切换进度
wait_seconds() {
    local secs="$1"
    local reason="${2:-等待}"
    log_info "${reason}：等待 ${secs}s ..."
    sleep "$secs"
}

# ======================== 清理函数 ========================
cleanup() {
    log_info "脚本退出，开始清理..."
    if [[ ${#CREATED_FD_RULE_IDS[@]} -gt 0 ]]; then
        log_info "删除已创建的探测规则: ${CREATED_FD_RULE_IDS[*]}"
        delete_fault_detect_rules "${CREATED_FD_RULE_IDS[@]}" || true
    fi
    if [[ ${#CREATED_CB_RULE_IDS[@]} -gt 0 ]]; then
        log_info "删除已创建的熔断规则: ${CREATED_CB_RULE_IDS[*]}"
        delete_circuitbreaker_rules "${CREATED_CB_RULE_IDS[@]}" || true
    fi
    for pid in "${CONSUMER_PIDS[@]:-}"; do
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
            log_info "已停止 Consumer (PID: $pid)"
        fi
    done
    for pid_var in PROVIDER_A_PID PROVIDER_B_PID; do
        local pid="${!pid_var}"
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
            log_info "已停止 ${pid_var} (PID: $pid)"
        fi
    done
}
trap cleanup EXIT

# ======================== Polaris HTTP API：熔断规则 ========================
# build_circuitbreaker_rule_body
#   生成一条 SERVICE 级熔断规则 JSON（含 faultDetectConfig.enable=true 作为探测门控）。
#   stdout 输出单个 JSON 对象。
build_circuitbreaker_rule_body() {
    python3 -c "
import json, os
consecutive = int(os.environ.get('FD_CONSECUTIVE_ERROR', '5'))
sleep_window = int(os.environ.get('FD_SLEEP_WINDOW', '6'))
err_conditions = [{'inputType': 'RET_CODE', 'condition': {'type': 'RANGE', 'value': '500~599'}}]
trigger_conditions = [
    {'triggerType': 'CONSECUTIVE_ERROR', 'errorCount': consecutive},
    {'triggerType': 'ERROR_RATE', 'errorPercent': 50, 'interval': 30, 'minimumRequest': 10},
]
bc = {
    'name': 'fault-detect-bc',
    'api': {'path': {'value': ''}},
    'error_conditions': err_conditions,
    'trigger_conditions': trigger_conditions,
}
rule = {
    'name': os.environ['RULE_NAME'],
    'namespace': os.environ['NAMESPACE'],
    'enable': True,
    'level': 'SERVICE',
    'description': 'auto-created by verify_faultdetect.sh',
    'rule_matcher': {
        'source': {'namespace': os.environ['SOURCE_NAMESPACE'], 'service': os.environ['SOURCE_SERVICE']},
        'destination': {
            'namespace': os.environ['NAMESPACE'],
            'service': os.environ['SERVICE_NAME'],
            'method': {'type': 'EXACT', 'value': '*'},
        },
    },
    'error_conditions': err_conditions,
    'trigger_condition': trigger_conditions,
    'recoverCondition': {'sleep_window': sleep_window, 'consecutiveSuccess': 1},
    'block_configs': [bc],
    # 主动探测门控：仅当 faultDetectConfig.enable=true 且存在匹配 FaultDetectRule，
    # SDK 才会启动 ResourceHealthChecker。
    'faultDetectConfig': {'enable': True},
}
print(json.dumps(rule))
"
}

# create_circuitbreaker_rule <body> -> stdout: rule id
create_circuitbreaker_rule() {
    local body="$1"
    local payload="[${body}]"
    local rule_name
    rule_name=$(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('name',''))" 2>/dev/null || echo "")

    local resp http_code
    http_code=$(curl -s -o "/tmp/_fd_cb_create_$$.tmp" -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/naming/v1/circuitbreaker/rules" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "${payload}" 2>/dev/null || echo "000")
    resp=$(cat "/tmp/_fd_cb_create_$$.tmp" 2>/dev/null || echo "")
    rm -f "/tmp/_fd_cb_create_$$.tmp"

    local code id
    code=$(echo "$resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('code',0))" 2>/dev/null || echo "?")
    if [[ "$http_code" == "200" ]] && [[ "$code" =~ ^20000[01]$ ]]; then
        id=$(echo "$resp" | python3 -c "
import sys, json
data = json.load(sys.stdin)
items = data.get('responses') or []
if items:
    cb = items[0].get('circuitBreaker') or {}
    print(cb.get('id', ''))
" 2>/dev/null || true)
        [[ -z "$id" ]] && id=$(query_circuitbreaker_rule_id_by_name "$rule_name")
    else
        # 已存在（400209）等场景：按 name 反查 id 复用，PUT 更新对齐定义
        id=$(query_circuitbreaker_rule_id_by_name "$rule_name") || true
        if [[ -n "$id" ]]; then
            update_circuitbreaker_rule "$id" "$body" || true
        fi
    fi
    if [[ -z "$id" ]]; then
        log_error "创建/复用熔断规则失败 (HTTP=${http_code}, code=${code})"
        log_error "响应: ${resp:0:400}"
        return 1
    fi
    CREATED_CB_RULE_IDS+=("$id")
    log_info "熔断规则就绪 (name=${rule_name}, id=${id})"
    echo "$id"
}

update_circuitbreaker_rule() {
    local rule_id="$1"
    local body="$2"
    local payload
    payload=$(python3 -c "
import json, sys
body = json.loads(sys.argv[1]); body['id'] = sys.argv[2]
print(json.dumps([body]))
" "$body" "$rule_id" 2>/dev/null || true)
    [[ -z "$payload" ]] && return 1
    curl -s -o /dev/null --connect-timeout 5 --max-time 10 \
        --request PUT "${POLARIS_HTTP_ADDR}/naming/v1/circuitbreaker/rules" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "${payload}" 2>/dev/null || true
}

query_circuitbreaker_rule_id_by_name() {
    local name="$1"
    [[ -z "$name" ]] && { echo ""; return 0; }
    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        "${POLARIS_HTTP_ADDR}/naming/v1/circuitbreaker/rules?name=${name}&namespace=${NAMESPACE}&limit=50&offset=0" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" 2>/dev/null || echo "")
    echo "$resp" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
except Exception:
    print(''); sys.exit(0)
for r in (data.get('data') or []):
    if r.get('name') == '${name}':
        print(r.get('id', '')); break
" 2>/dev/null || echo ""
}

delete_circuitbreaker_rules() {
    local ids=("$@")
    [[ ${#ids[@]} -eq 0 ]] && return 0
    local payload
    payload=$(python3 -c "
import json, sys
print(json.dumps([{'id': i} for i in sys.argv[1:]]))
" "${ids[@]}" 2>/dev/null || echo "[]")
    curl -s -o /dev/null --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/naming/v1/circuitbreaker/rules/delete" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "${payload}" 2>/dev/null || true
}

# ======================== Polaris HTTP API：主动探测规则 ========================
# build_fault_detect_rule_body
#   生成一条 HTTP 探测规则 JSON：探测目标服务的 /echo，间隔 FD_PROBE_INTERVAL 秒。
#   port=0 表示使用被探测实例自身端口（provider 注册的 28081/28082）。
#   stdout 输出单个 JSON 对象。
build_fault_detect_rule_body() {
    python3 -c "
import json, os
interval = int(os.environ.get('FD_PROBE_INTERVAL', '2'))
rule = {
    'name': os.environ['RULE_NAME'],
    'namespace': os.environ['NAMESPACE'],
    'description': 'auto-created by verify_faultdetect.sh',
    'target_service': {
        'service': os.environ['SERVICE_NAME'],
        'namespace': os.environ['NAMESPACE'],
        'method': {'type': 'EXACT', 'value': '*'},
    },
    'interval': interval,
    'timeout': 1000,
    'port': 0,
    'protocol': 1,
    'http_config': {'method': 'GET', 'url': '/echo', 'headers': []},
}
print(json.dumps(rule))
"
}

# create_fault_detect_rule <body> -> stdout: rule id
create_fault_detect_rule() {
    local body="$1"
    local payload="[${body}]"
    local rule_name
    rule_name=$(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('name',''))" 2>/dev/null || echo "")

    local resp http_code
    http_code=$(curl -s -o "/tmp/_fd_create_$$.tmp" -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/naming/v1/faultdetectors" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "${payload}" 2>/dev/null || echo "000")
    resp=$(cat "/tmp/_fd_create_$$.tmp" 2>/dev/null || echo "")
    rm -f "/tmp/_fd_create_$$.tmp"

    local code id
    code=$(echo "$resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('code',0))" 2>/dev/null || echo "?")
    if [[ "$http_code" == "200" ]] && [[ "$code" =~ ^20000[01]$ ]]; then
        id=$(echo "$resp" | python3 -c "
import sys, json
data = json.load(sys.stdin)
items = data.get('responses') or []
if items:
    fd = items[0].get('faultDetector') or items[0].get('faultDetectRule') or {}
    print(fd.get('id', ''))
" 2>/dev/null || true)
        [[ -z "$id" ]] && id=$(query_fault_detect_rule_id_by_name "$rule_name")
    else
        id=$(query_fault_detect_rule_id_by_name "$rule_name") || true
        if [[ -n "$id" ]]; then
            update_fault_detect_rule "$id" "$body" || true
        fi
    fi
    if [[ -z "$id" ]]; then
        log_error "创建/复用探测规则失败 (HTTP=${http_code}, code=${code})"
        log_error "响应: ${resp:0:400}"
        return 1
    fi
    CREATED_FD_RULE_IDS+=("$id")
    log_info "探测规则就绪 (name=${rule_name}, id=${id})"
    echo "$id"
}

update_fault_detect_rule() {
    local rule_id="$1"
    local body="$2"
    local payload
    payload=$(python3 -c "
import json, sys
body = json.loads(sys.argv[1]); body['id'] = sys.argv[2]
print(json.dumps([body]))
" "$body" "$rule_id" 2>/dev/null || true)
    [[ -z "$payload" ]] && return 1
    curl -s -o /dev/null --connect-timeout 5 --max-time 10 \
        --request PUT "${POLARIS_HTTP_ADDR}/naming/v1/faultdetectors" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "${payload}" 2>/dev/null || true
}

query_fault_detect_rule_id_by_name() {
    local name="$1"
    [[ -z "$name" ]] && { echo ""; return 0; }
    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        "${POLARIS_HTTP_ADDR}/naming/v1/faultdetectors?name=${name}&namespace=${NAMESPACE}&limit=50&offset=0" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" 2>/dev/null || echo "")
    echo "$resp" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
except Exception:
    print(''); sys.exit(0)
for r in (data.get('data') or []):
    if r.get('name') == '${name}':
        print(r.get('id', '')); break
" 2>/dev/null || echo ""
}

delete_fault_detect_rules() {
    local ids=("$@")
    [[ ${#ids[@]} -eq 0 ]] && return 0
    local payload
    payload=$(python3 -c "
import json, sys
print(json.dumps([{'id': i} for i in sys.argv[1:]]))
" "${ids[@]}" 2>/dev/null || echo "[]")
    curl -s -o /dev/null --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/naming/v1/faultdetectors/delete" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "${payload}" 2>/dev/null || true
}

# ======================== Provider 错误开关 ========================
provider_set_error() {
    local port="$1"
    local echo_enabled="$2"   # true=返回500, false=返回200
    curl -s --connect-timeout 3 --max-time 5 \
        "http://127.0.0.1:${port}/switch?openError=${echo_enabled}" > /dev/null 2>&1 || true
}

# ======================== polaris.yaml 生成 ========================
generate_provider_polaris_yaml() {
    local target_dir="$1"
    cat > "${target_dir}/polaris.yaml" <<EOF
global:
  serverConnector:
    addresses:
      - ${POLARIS_SERVER}:8091
    token: ${POLARIS_TOKEN}
    connectTimeout: 3s
EOF
}

generate_consumer_polaris_yaml() {
    local target_dir="$1"
    cat > "${target_dir}/polaris.yaml" <<EOF
global:
  serverConnector:
    addresses:
      - ${POLARIS_SERVER}:8091
    token: ${POLARIS_TOKEN}
    connectTimeout: 3s
consumer:
  circuitBreaker:
    enable: true
    checkPeriod: 5s
    defaultRuleEnable: false
  serviceRouter:
    enableRecoverAll: true
EOF
}

# ======================== 启动 Provider / Consumer ========================
# 子进程显式 detach 标准 fd（< /dev/null + exec + disown），避免在 r=$(func)
# 命令替换的子 shell 中继承捕获管道导致主 shell 卡死。
start_provider() {
    local name="$1"
    local src_dir="$2"
    local port="$3"
    local log_file="$4"

    local workdir="${BUILD_DIR}/${name}_run"
    mkdir -p "$workdir"
    generate_provider_polaris_yaml "$workdir"

    if lsof -ti :"${port}" -sTCP:LISTEN > /dev/null 2>&1; then
        log_warn "端口 ${port} 已被监听进程占用，尝试终止..."
        local existing_pid
        existing_pid=$(lsof -ti :"${port}" -sTCP:LISTEN 2>/dev/null | head -1 || echo "")
        [[ -n "$existing_pid" ]] && kill "$existing_pid" 2>/dev/null && sleep 1 || true
    fi

    log_info "编译 ${name}..."
    (cd "$src_dir" && go build -o "${BUILD_DIR}/${name}" .)
    command -v xattr &> /dev/null && xattr -c "${BUILD_DIR}/${name}" 2>/dev/null || true

    (cd "$workdir" && exec "${BUILD_DIR}/${name}" \
        --namespace "$NAMESPACE" \
        --service "$SERVICE_NAME" \
        --token "$POLARIS_TOKEN" \
        --port "$port" \
        --debug="$DEBUG_MODE" \
        < /dev/null > "$log_file" 2>&1) &
    local pid=$!
    disown "$pid" 2>/dev/null || true
    sleep 1
    check_process_alive "$pid" "$name" || {
        log_error "${name} 启动失败，日志: $log_file"
        cat "$log_file" 2>/dev/null || true
        return 1
    }
    wait_for_http "http://127.0.0.1:${port}/echo" 20 "$name" "$pid" || return 1
    log_info "${name} 已启动 (PID: $pid, port: $port)"
    _STARTED_PID="$pid"
    return 0
}

start_consumer() {
    local name="$1"
    local self_service="$2"
    local port="$3"
    local log_file="$4"

    local workdir="${BUILD_DIR}/${name}_run"
    mkdir -p "$workdir"
    generate_consumer_polaris_yaml "$workdir"

    if lsof -ti :"${port}" -sTCP:LISTEN > /dev/null 2>&1; then
        log_warn "端口 ${port} 已被监听进程占用，尝试终止..."
        local existing_pid
        existing_pid=$(lsof -ti :"${port}" -sTCP:LISTEN 2>/dev/null | head -1 || echo "")
        [[ -n "$existing_pid" ]] && kill "$existing_pid" 2>/dev/null && sleep 1 || true
    fi

    log_info "编译 ${name}..."
    (cd "$CONSUMER_DIR" && go build -o "${BUILD_DIR}/${name}" .)
    command -v xattr &> /dev/null && xattr -c "${BUILD_DIR}/${name}" 2>/dev/null || true

    (cd "$workdir" && exec "${BUILD_DIR}/${name}" \
        --selfNamespace "$NAMESPACE" \
        --selfService "$self_service" \
        --selfRegister=false \
        --calleeNamespace "$NAMESPACE" \
        --calleeService "$SERVICE_NAME" \
        --token "$POLARIS_TOKEN" \
        --port "$port" \
        --debug="$DEBUG_MODE" \
        < /dev/null > "$log_file" 2>&1) &
    local pid=$!
    disown "$pid" 2>/dev/null || true
    sleep 1
    check_process_alive "$pid" "$name" || {
        log_error "${name} 启动失败，日志: $log_file"
        cat "$log_file" 2>/dev/null || true
        return 1
    }
    wait_for_http "http://127.0.0.1:${port}/echo" 30 "$name" "$pid" || return 1
    log_info "${name} 已启动 (PID: $pid, port: $port)"
    CONSUMER_PIDS+=("$pid")
    _STARTED_PID="$pid"
    return 0
}

# ======================== 请求 / 压测 ========================
do_request() {
    local port="$1"
    local path="${2:-/echo}"
    local body http_code
    http_code=$(curl -s -o "/tmp/_fd_resp_$$.tmp" -w '%{http_code}' \
        --connect-timeout 3 --max-time 5 \
        "http://127.0.0.1:${port}${path}" 2>/dev/null || echo "000")
    body=$(cat "/tmp/_fd_resp_$$.tmp" 2>/dev/null || echo "")
    rm -f "/tmp/_fd_resp_$$.tmp"
    echo "${http_code}|${body}"
}

# run_burst <consumer_port> <count> <label>
# 统计 CASE_OK / CASE_FAIL / CASE_ABORT（判定逻辑与 verify_circuitbreaker.sh 一致）
run_burst() {
    local consumer_port="$1"
    local count="$2"
    local label="${3:-请求}"

    CASE_TOTAL=0; CASE_OK=0; CASE_FAIL=0; CASE_ABORT=0
    printf "  ${CYAN}%-6s %-12s %-10s %s${NC}\n" "序号" "HTTP状态" "判定" "响应摘要" >&2
    printf "  %-6s %-12s %-10s %s\n" "------" "------------" "----------" "------------------------------" >&2

    local i
    for i in $(seq 1 "$count"); do
        CASE_TOTAL=$((CASE_TOTAL + 1))
        local result http_code body short_body verdict verdict_color
        result=$(do_request "$consumer_port" "/echo")
        http_code="${result%%|*}"
        body="${result#*|}"
        short_body=$(echo "$body" | head -c 80)
        if echo "$body" | grep -qE "call aborted|circuit breaker open"; then
            CASE_ABORT=$((CASE_ABORT + 1)); verdict="ABORT"; verdict_color="${YELLOW}"
        elif [[ "$http_code" != "200" ]] \
            || echo "$body" | grep -qE "status code: 5[0-9][0-9]" \
            || echo "$body" | grep -q "Fatal"; then
            CASE_FAIL=$((CASE_FAIL + 1)); verdict="FAIL"; verdict_color="${RED}"
        else
            CASE_OK=$((CASE_OK + 1)); verdict="200"; verdict_color="${GREEN}"
        fi
        printf "  %-6s %-12s ${verdict_color}%-10s${NC} %s\n" \
            "$i" "HTTP $http_code" "$verdict" "$short_body" >&2
        sleep 0.05
    done
    echo "" >&2
    log_info "[${label}] total=${CASE_TOTAL}, ok=${CASE_OK}, fail=${CASE_FAIL}, abort=${CASE_ABORT}"
}

# ======================== 主动探测验证用例 ========================
# case_fault_detect 验证完整闭环：OPEN -> 探测维持 OPEN -> 探活 -> HALF_OPEN -> CLOSE
# 结果写入全局变量 _CASE_RESULT（PASS/FAIL）而非 echo 到 stdout。
# 必须在主 shell 直接调用（不可用 r=$(case_fault_detect) 命令替换），否则函数内
# 对 CREATED_*_RULE_IDS / CONSUMER_PIDS 的 append 发生在子 shell 中、不回传主 shell，
# 导致 trap cleanup 拿到空数组，规则与进程无法清理（在共享环境残留规则）。
case_fault_detect() {
    _CASE_RESULT="FAIL"
    local consumer_log="${LOG_DIR}/fault_detect_consumer.log"

    print_block "主动探测验证（SERVICE 级）" \
        "目标服务 : ${SERVICE_NAME}" \
        "主调服务 : ${FD_CALLER}" \
        "熔断规则 : SERVICE 级, CONSECUTIVE_ERROR=${FD_CONSECUTIVE_ERROR}, sleepWindow=${FD_SLEEP_WINDOW}s, faultDetectConfig.enable=true" \
        "探测规则 : HTTP GET /echo, interval=${FD_PROBE_INTERVAL}s, port=0(实例端口)" \
        "预期闭环 : 业务触发 OPEN -> provider 仍故障探测失败维持 OPEN -> 恢复 provider 探活成功 -> CLOSE"

    # 用例 主动探测 步骤1：环境复位（provider 全部正常）
    log_step "用例 主动探测 [1] 环境复位：provider-a/b 均返回 200"
    provider_set_error "$PROVIDER_A_PORT" "false"
    provider_set_error "$PROVIDER_B_PORT" "false"

    # 用例 主动探测 步骤2：启动 consumer + 下发熔断规则 + 探测规则
    log_step "用例 主动探测 [2] 启动 consumer 并下发规则"
    start_consumer "fault_detect_consumer" "$FD_CALLER" "$FD_CONSUMER_PORT" "$consumer_log" || {
        _CASE_RESULT="FAIL"; return
    }

    local cb_body fd_body
    cb_body=$(RULE_NAME="cb-faultdetect-${FD_CALLER}" NAMESPACE="$NAMESPACE" SERVICE_NAME="$SERVICE_NAME" \
        SOURCE_NAMESPACE="$NAMESPACE" SOURCE_SERVICE="$FD_CALLER" \
        FD_CONSECUTIVE_ERROR="$FD_CONSECUTIVE_ERROR" FD_SLEEP_WINDOW="$FD_SLEEP_WINDOW" \
        build_circuitbreaker_rule_body)
    create_circuitbreaker_rule "$cb_body" > /dev/null || { _CASE_RESULT="FAIL"; return; }

    fd_body=$(RULE_NAME="fd-faultdetect-${FD_CALLER}" NAMESPACE="$NAMESPACE" SERVICE_NAME="$SERVICE_NAME" \
        FD_PROBE_INTERVAL="$FD_PROBE_INTERVAL" \
        build_fault_detect_rule_body)
    create_fault_detect_rule "$fd_body" > /dev/null || { _CASE_RESULT="FAIL"; return; }

    wait_seconds "$WAIT_RULE_READY_SECONDS" "用例 主动探测 [2] 等待 SDK 拉取熔断/探测规则就绪"

    # 用例 主动探测 步骤3：触发服务级熔断 OPEN
    log_step "用例 主动探测 [3] 触发熔断：provider 全部置 500，burst ${FD_TRIGGER_COUNT} 次"
    provider_set_error "$PROVIDER_A_PORT" "true"
    provider_set_error "$PROVIDER_B_PORT" "true"
    run_burst "$FD_CONSUMER_PORT" "$FD_TRIGGER_COUNT" "用例 主动探测 触发-OPEN"
    local trigger_abort="$CASE_ABORT"

    # 用例 主动探测 步骤4：维持 OPEN —— provider 仍故障，探测打 /echo 也失败，
    # 半开探测失败立即回 OPEN，不应恢复
    log_step "用例 主动探测 [4] 维持 OPEN：provider 保持 500，等待 > sleepWindow 后验证仍熔断"
    wait_seconds "$WAIT_KEEP_OPEN_SECONDS" "用例 主动探测 [4] 等待（探测持续失败，应维持 OPEN）"
    run_burst "$FD_CONSUMER_PORT" "$FD_VERIFY_COUNT" "用例 主动探测 维持-OPEN"
    local keep_abort="$CASE_ABORT"

    # 用例 主动探测 步骤5：恢复 provider，纯靠主动探测探活推动 HALF_OPEN -> CLOSE
    log_step "用例 主动探测 [5] 探活恢复：provider 全部置 200，仅等待主动探测推动恢复（不发业务请求）"
    provider_set_error "$PROVIDER_A_PORT" "false"
    provider_set_error "$PROVIDER_B_PORT" "false"
    wait_seconds "$WAIT_RECOVER_SECONDS" "用例 主动探测 [5] 等待主动探测探活并推动 CLOSE（sleepWindow + 多个探测周期）"
    run_burst "$FD_CONSUMER_PORT" "$FD_VERIFY_COUNT" "用例 主动探测 恢复-CLOSE"
    local recover_ok="$CASE_OK"

    # 用例 主动探测 步骤6：日志佐证探测真实运行
    log_step "用例 主动探测 [6] 校验 consumer 日志中的探测调度与状态切换"
    local has_schedule="no" has_statechg="no"
    if grep -qE "\[FaultDetect\]|health check|schedule task" "$consumer_log" 2>/dev/null; then
        has_schedule="yes"
    fi
    if grep -qE "status change.*HalfOpen|HalfOpen -> Close|status change.*Close" "$consumer_log" 2>/dev/null; then
        has_statechg="yes"
    fi
    log_info "用例 主动探测 [6] 日志佐证：探测调度=${has_schedule}, 状态切换=${has_statechg}"

    # 用例 主动探测 判定
    print_block "用例 主动探测 判定指标" \
        "触发 OPEN  : abort=${trigger_abort} (期望 >=1)" \
        "维持 OPEN  : abort=${keep_abort} (期望 >=1，探测失败不恢复)" \
        "探活 CLOSE : ok=${recover_ok}/${FD_VERIFY_COUNT} (期望 == ${FD_VERIFY_COUNT})" \
        "日志佐证   : 探测调度=${has_schedule}"

    if [[ "$trigger_abort" -ge 1 ]] && [[ "$keep_abort" -ge 1 ]] \
        && [[ "$recover_ok" -eq "$FD_VERIFY_COUNT" ]]; then
        _CASE_RESULT="PASS"
    else
        _CASE_RESULT="FAIL"
    fi
}

# ======================== 主流程 ========================
main() {
    setup_test_log "$@"
    mkdir -p "$BUILD_DIR" "$LOG_DIR"

    if [[ -z "$POLARIS_TOKEN" ]]; then
        log_warn "POLARIS_TOKEN 为空，若服务端开启鉴权将无法创建规则"
    fi

    print_block "熔断主动探测端到端验证" \
        "Polaris   : ${POLARIS_HTTP_ADDR} (gRPC ${POLARIS_SERVER}:8091)" \
        "命名空间  : ${NAMESPACE}" \
        "被调服务  : ${SERVICE_NAME}" \
        "探测端点  : HTTP GET /echo (随 provider 故障开关变化)" \
        "DEBUG     : ${DEBUG_MODE}"

    # 启动 provider 集群
    log_step "启动 Provider 集群"
    start_provider "fd_provider_a" "$PROVIDER_A_DIR" "$PROVIDER_A_PORT" "${LOG_DIR}/fd_provider_a.log" \
        && PROVIDER_A_PID="$_STARTED_PID" || { log_error "provider-a 启动失败"; exit 1; }
    start_provider "fd_provider_b" "$PROVIDER_B_DIR" "$PROVIDER_B_PORT" "${LOG_DIR}/fd_provider_b.log" \
        && PROVIDER_B_PID="$_STARTED_PID" || { log_error "provider-b 启动失败"; exit 1; }

    # 运行主动探测用例
    local result
    result=$(case_fault_detect)

    log_step "验证结果"
    print_block "结果汇总" "主动探测闭环（OPEN -> 探测维持 -> 探活 -> CLOSE）: ${result}"

    if [[ "$result" == "PASS" ]]; then
        log_info "主动探测验证通过 ✅"
        exit 0
    else
        log_error "主动探测验证失败 ❌（详见日志 ${TEST_LOG_FILE} 与 ${LOG_DIR}/fault_detect_consumer.log）"
        exit 1
    fi
}

main "$@"
