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
#
# 熔断事件上报（2026-06-29）:
#   所有 consumer 启动时均配置 eventReporter.pushgateway（参考 verify_circuitbreaker.sh 用例7）。
#   默认 POLARIS_EVENT_ADDR 为空，SDK 通过 Polaris 服务发现自动解析远端 pushgateway。
#   事件日志落盘到 .build/<name>_run/polaris/log/event/polaris-event.log，
#   可通过 grep "new event:" 查看推送的熔断事件 JSON。
# =============================================================================

set -euo pipefail

# ======================== 默认配置 ========================
POLARIS_SERVER="${POLARIS_SERVER:-127.0.0.1}"
POLARIS_HTTP_PORT="${POLARIS_HTTP_PORT:-8090}"
POLARIS_TOKEN="${POLARIS_TOKEN:-}"
NAMESPACE="${NAMESPACE:-default}"
SERVICE_NAME="${SERVICE_NAME:-CircuitBreakerCallee}"
# 熔断事件上报地址（pushgateway）。默认为空，SDK PushgatewayReporter 在 address 为空时
# 通过 Polaris 服务发现（polaris.pushgateway）自动解析远端 pushgateway 地址。
# 如需指定地址（如本地 mock），通过环境变量设置：POLARIS_EVENT_ADDR=127.0.0.1:19091
POLARIS_EVENT_ADDR="${POLARIS_EVENT_ADDR:-}"
# 三级用例各用独立被调服务，避免共用 CircuitBreakerCallee 时被其它脚本（verify_circuitbreaker.sh
# 用例10 的 source=*/* catch-all 规则）残留的同 destination 规则按 id 字典序抢占，导致探测门控
# 误判 disabled、主动探测无法启动。provider 进程不变，同时注册到这三个服务（见 start_provider）。
FD_SVC_CALLEE="${FD_SVC_CALLEE:-CircuitBreakerFDSvcCallee}"
FD_METHOD_CALLEE="${FD_METHOD_CALLEE:-CircuitBreakerFDMethodCallee}"
FD_INSTANCE_CALLEE="${FD_INSTANCE_CALLEE:-CircuitBreakerFDInstanceCallee}"
# TCP/UDP 探测用例的被调服务（SERVICE 级，业务走 HTTP /echo，探测走独立 TCP/UDP 端口）
FD_TCP_CALLEE="${FD_TCP_CALLEE:-CircuitBreakerFDTcpCallee}"
FD_UDP_CALLEE="${FD_UDP_CALLEE:-CircuitBreakerFDUdpCallee}"
# 主调服务名：独立 caller，避免与 verify_circuitbreaker.sh 的用例规则相互干扰。
# 三个级别用例各用独立 caller + 独立 consumer 端口 + 独立规则名，避免跨用例熔断状态干扰。
FD_CALLER="${FD_CALLER:-CircuitBreakerFaultDetectCaller}"
# METHOD（接口级）用例主调服务
FD_METHOD_CALLER="${FD_METHOD_CALLER:-CircuitBreakerFDMethodCaller}"
# INSTANCE（实例级）用例主调服务
FD_INSTANCE_CALLER="${FD_INSTANCE_CALLER:-CircuitBreakerFDInstanceCaller}"
# TCP / UDP 探测用例主调服务
FD_TCP_CALLER="${FD_TCP_CALLER:-CircuitBreakerFDTcpCaller}"
FD_UDP_CALLER="${FD_UDP_CALLER:-CircuitBreakerFDUdpCaller}"

# 端口规划（与 verify_circuitbreaker.sh 共用 provider 端口；consumer 用独立端口段）
PROVIDER_A_PORT="${PROVIDER_A_PORT:-28081}"
PROVIDER_B_PORT="${PROVIDER_B_PORT:-28082}"
FD_CONSUMER_PORT="${FD_CONSUMER_PORT:-18095}"
# METHOD / INSTANCE 用例各自独立的 consumer 端口
FD_METHOD_CONSUMER_PORT="${FD_METHOD_CONSUMER_PORT:-18096}"
FD_INSTANCE_CONSUMER_PORT="${FD_INSTANCE_CONSUMER_PORT:-18097}"
# TCP / UDP 用例 consumer 端口
FD_TCP_CONSUMER_PORT="${FD_TCP_CONSUMER_PORT:-18098}"
FD_UDP_CONSUMER_PORT="${FD_UDP_CONSUMER_PORT:-18099}"
# TCP / UDP 主动探测端口（仅 provider-a 启动；rule.port 单值，所有实例同端口，故 TCP/UDP 用例只探 a）
FD_TCP_PROBE_PORT="${FD_TCP_PROBE_PORT:-28091}"
FD_UDP_PROBE_PORT="${FD_UDP_PROBE_PORT:-28101}"

# 选跑用例子集（空=全跑）：逗号分隔，取值 service/method/instance/tcp/udp，便于单独调试
RUN_FD_CASES="${RUN_FD_CASES:-service,method,instance,tcp,udp,proto_method}"

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

# D 段热更新验证：修改探测间隔的目标值（秒）
FD_PROBE_INTERVAL_UPDATED="${FD_PROBE_INTERVAL_UPDATED:-5}"
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
    # 探测规则（FaultDetectRule）创建后不删除：跨运行复用，下次运行检测到已存在则 PUT 更新复用
    # （见 create_fault_detect_rule 的幂等策略）。仅打印保留的规则 id 供排查，不调用 delete。
    if [[ ${#CREATED_FD_RULE_IDS[@]} -gt 0 ]]; then
        log_info "保留探测规则（不删除，下次运行复用）: ${CREATED_FD_RULE_IDS[*]}"
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
#   生成一条熔断规则 JSON（含 faultDetectConfig.enable=true 作为探测门控）。
#   级别与匹配维度通过环境变量参数化，默认 SERVICE 级（保持原有 SERVICE 用例零影响）：
#     FD_RULE_LEVEL        规则级别 SERVICE / METHOD / INSTANCE（默认 SERVICE）
#     FD_RULE_METHOD_TYPE  destination.method.type（默认 EXACT）
#     FD_RULE_METHOD_VALUE destination.method.value（默认 *；METHOD 级用例传具体路径如 /echo）
#     FD_BC_API_PATH       BlockConfig.api.path.value（默认空串；METHOD 级用例传 /echo）
#   stdout 输出单个 JSON 对象。
build_circuitbreaker_rule_body() {
    python3 -c "
import json, os
consecutive = int(os.environ.get('FD_CONSECUTIVE_ERROR', '5'))
sleep_window = int(os.environ.get('FD_SLEEP_WINDOW', '6'))
level = os.environ.get('FD_RULE_LEVEL', 'SERVICE')
method_type = os.environ.get('FD_RULE_METHOD_TYPE', 'EXACT')
method_value = os.environ.get('FD_RULE_METHOD_VALUE', '*')
bc_api_path = os.environ.get('FD_BC_API_PATH', '')
err_conditions = [{'inputType': 'RET_CODE', 'condition': {'type': 'RANGE', 'value': '500~599'}}]
# 主动探测闭环验证仅用 CONSECUTIVE_ERROR：连续失败触发熔断、连续成功(consecutiveSuccess=1)恢复，
# 探测推动 half-open->close 后立即生效。
# 不能叠加 ERROR_RATE：它用 30s 滑动窗口统计 failRatio，close 后窗口内仍残留 OPEN 期间探测失败的
# 记录，高频(2s)探测成功来不及把 failRatio 稀释到阈值(50%)以下，导致 close 后立刻重新 CloseToOpen
# 抖动（reqCount≈26/failCount≈13=50% 命中），恢复阶段业务请求全 abort，验证假性失败。
trigger_conditions = [
    {'triggerType': 'CONSECUTIVE_ERROR', 'errorCount': consecutive},
]
bc = {
    'name': 'fault-detect-bc',
    'api': {'path': {'value': bc_api_path}},
    'error_conditions': err_conditions,
    'trigger_conditions': trigger_conditions,
}
rule = {
    'name': os.environ['RULE_NAME'],
    'namespace': os.environ['NAMESPACE'],
    'enable': True,
    'level': level,
    'description': 'auto-created by verify_faultdetect.sh',
    'rule_matcher': {
        'source': {'namespace': os.environ['SOURCE_NAMESPACE'], 'service': os.environ['SOURCE_SERVICE']},
        'destination': {
            'namespace': os.environ['NAMESPACE'],
            'service': os.environ['SERVICE_NAME'],
            'method': {'type': method_type, 'value': method_value},
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

# build_circuitbreaker_rule_body_disable
#   与 build_circuitbreaker_rule_body 相同但 faultDetectConfig.enable=false，
#   用于 D 段 enable toggle 验证中关闭探测。接受相同的环境变量参数。
build_circuitbreaker_rule_body_disable() {
    python3 -c "
import json, os
consecutive = int(os.environ.get('FD_CONSECUTIVE_ERROR', '5'))
sleep_window = int(os.environ.get('FD_SLEEP_WINDOW', '6'))
level = os.environ.get('FD_RULE_LEVEL', 'SERVICE')
method_type = os.environ.get('FD_RULE_METHOD_TYPE', 'EXACT')
method_value = os.environ.get('FD_RULE_METHOD_VALUE', '*')
bc_api_path = os.environ.get('FD_BC_API_PATH', '')
err_conditions = [{'inputType': 'RET_CODE', 'condition': {'type': 'RANGE', 'value': '500~599'}}]
trigger_conditions = [
    {'triggerType': 'CONSECUTIVE_ERROR', 'errorCount': consecutive},
]
bc = {
    'name': 'fault-detect-bc',
    'api': {'path': {'value': bc_api_path}},
    'error_conditions': err_conditions,
    'trigger_conditions': trigger_conditions,
}
rule = {
    'name': os.environ['RULE_NAME'],
    'namespace': os.environ['NAMESPACE'],
    'enable': True,
    'level': level,
    'description': 'auto-created by verify_faultdetect.sh (D段-disable)',
    'rule_matcher': {
        'source': {'namespace': os.environ['SOURCE_NAMESPACE'], 'service': os.environ['SOURCE_SERVICE']},
        'destination': {
            'namespace': os.environ['NAMESPACE'],
            'service': os.environ['SERVICE_NAME'],
            'method': {'type': method_type, 'value': method_value},
        },
    },
    'error_conditions': err_conditions,
    'trigger_condition': trigger_conditions,
    'recoverCondition': {'sleep_window': sleep_window, 'consecutiveSuccess': 1},
    'block_configs': [bc],
    # D 段关闭探测：faultDetectConfig.enable=false，触发 SDK realRefreshHealthCheck
    # 门控不通过 -> stop checker -> 日志 [FaultDetect] health check ... is disabled, now stop
    'faultDetectConfig': {'enable': False},
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
# targetService.method 参数化：默认 EXACT/*（SERVICE/INSTANCE 级用例匹配全部方法）；
# METHOD 级用例传 EXACT//echo，使探测规则与熔断规则、业务请求三者方法维度同源。
probe_method_type = os.environ.get('FD_PROBE_METHOD_TYPE', 'EXACT')
probe_method_value = os.environ.get('FD_PROBE_METHOD_VALUE', '*')
# 探测协议参数化：1=HTTP（默认）/2=TCP/3=UDP。
#   FD_PROBE_PORT  探测端口，0=用被探测实例自身注册端口（HTTP 用例）；TCP/UDP 用例传独立探测端口。
#   FD_PROBE_SEND/FD_PROBE_RECEIVE  TCP/UDP 探测发送内容与期望响应（receive 命中才算探测成功）。
protocol = int(os.environ.get('FD_PROBE_PROTOCOL', '1'))
probe_port = int(os.environ.get('FD_PROBE_PORT', '0'))
probe_send = os.environ.get('FD_PROBE_SEND', '')
probe_receive = os.environ.get('FD_PROBE_RECEIVE', '')
# HTTP 探测 headers 参数化：FD_HTTP_HEADERS 为 JSON 数组字符串，如 '[{"key":"X-Probe","value":"1"}]'。
# 默认空字符串表示空数组 []（向后兼容）。
http_headers_raw = os.environ.get('FD_HTTP_HEADERS', '')
if http_headers_raw:
    http_headers = json.loads(http_headers_raw)
else:
    http_headers = []
rule = {
    'name': os.environ['RULE_NAME'],
    'namespace': os.environ['NAMESPACE'],
    'description': 'auto-created by verify_faultdetect.sh',
    'target_service': {
        'service': os.environ['SERVICE_NAME'],
        'namespace': os.environ['NAMESPACE'],
        'method': {'type': probe_method_type, 'value': probe_method_value},
    },
    'interval': interval,
    'timeout': 1000,
    'port': probe_port,
    'protocol': protocol,
}
if protocol == 2:
    # TCP 探测：连上后发送 send、读响应、与 receive 任一匹配才算成功
    rule['tcp_config'] = {'send': probe_send, 'receive': [probe_receive] if probe_receive else []}
elif protocol == 3:
    # UDP 探测：发送 send、读响应、与 receive 匹配才算成功（必须配 send/receive，否则恒成功）
    rule['udp_config'] = {'send': probe_send, 'receive': [probe_receive] if probe_receive else []}
else:
    # HTTP 探测（默认）
    rule['http_config'] = {'method': 'GET', 'url': '/echo', 'headers': http_headers}
print(json.dumps(rule))
"
}

# create_fault_detect_rule <body> -> stdout: rule id
# 幂等复用策略（2026-06-22）：探测规则创建后不再删除（cleanup 不删），改为跨运行复用。
# 先按 name 查规则是否已存在：
#   - 已存在：PUT 更新复用（刷新 interval/timeout/http_config 等定义，对齐本次 body），保留同一 id；
#   - 不存在：POST 新建。
# FaultDetectRule 无 enable 字段，无法 PUT enable=false 关闭；探测的真正启停由其依附的熔断规则
# faultDetectConfig.enable 门控（见 build_circuitbreaker_rule_body）。因此这里"存在则打开"等价于
# "存在则 PUT 复用并确保定义最新"，不存在则创建。
create_fault_detect_rule() {
    local body="$1"
    local rule_name
    rule_name=$(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('name',''))" 2>/dev/null || echo "")

    local id
    # 先查：规则已存在则 PUT 更新复用（不删不重建），保留原 id
    id=$(query_fault_detect_rule_id_by_name "$rule_name") || true
    if [[ -n "$id" ]]; then
        update_fault_detect_rule "$id" "$body" || true
        log_info "探测规则已存在，PUT 更新复用 (name=${rule_name}, id=${id})"
        echo "$id"
        return 0
    fi

    # 不存在则 POST 新建
    local payload="[${body}]"
    local resp http_code
    http_code=$(curl -s -o "/tmp/_fd_create_$$.tmp" -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/naming/v1/faultdetectors" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "${payload}" 2>/dev/null || echo "000")
    resp=$(cat "/tmp/_fd_create_$$.tmp" 2>/dev/null || echo "")
    rm -f "/tmp/_fd_create_$$.tmp"

    local code
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
        # 并发/竞态下 POST 与查 id 之间可能被他处创建，回退查 id + PUT 复用
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
    log_info "探测规则新建就绪 (name=${rule_name}, id=${id})"
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

# delete_fault_detect_rules 保留备用：当前流程不再在 cleanup 中删除探测规则（改为跨运行复用），
# 如需手动清理探测规则可调用本函数。
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

# provider_set_switch <http_port> <switch_param> <true|false>
# 通用 provider 开关翻转：switch_param 取 openError / openTcpError / openUdpError 等。
# 用于 TCP/UDP 探测用例单独控制探测端口的故障开关（与业务 /echo 的 openError 解耦）。
provider_set_switch() {
    local port="$1"
    local switch_param="$2"
    local enabled="$3"
    curl -s --connect-timeout 3 --max-time 5 \
        "http://127.0.0.1:${port}/switch?${switch_param}=${enabled}" > /dev/null 2>&1 || true
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
  eventReporter:
    enable: true
    chain:
      - pushgateway
    plugin:
      pushgateway:
        address: '\${POLARIS_EVENT_ADDR}'
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

    # 同时注册到三级用例各自的被调服务（逗号分隔，provider 支持多服务注册）。
    # TCP/UDP 探测端口仅由 provider-a 启动（rule.port 单值 → 所有实例同端口 → 同机两进程不能绑同端口，
    # 故 TCP/UDP 用例只探 provider-a）。provider-b 不传探测端口（默认 0 不启动）。
    local probe_port_args=""
    if [[ "$name" == "fd_provider_a" ]]; then
        probe_port_args="--tcp-probe-port ${FD_TCP_PROBE_PORT} --udp-probe-port ${FD_UDP_PROBE_PORT}"
    fi
    # 注册到全部被调服务（HTTP 三级 + TCP/UDP 两级共用同一组 provider 实例）。
    (cd "$workdir" && exec "${BUILD_DIR}/${name}" \
        --namespace "$NAMESPACE" \
        --service "${FD_SVC_CALLEE},${FD_METHOD_CALLEE},${FD_INSTANCE_CALLEE},${FD_TCP_CALLEE},${FD_UDP_CALLEE}" \
        --token "$POLARIS_TOKEN" \
        --port "$port" \
        ${probe_port_args} \
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
    # 第 5 个参数：被调服务名（默认全局 SERVICE_NAME）。三级用例各传自己的独立被调服务。
    local callee_service="${5:-$SERVICE_NAME}"

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

    # POLARIS_EVENT_ADDR 在子 shell 内 export，供 yaml 中 ${POLARIS_EVENT_ADDR} 占位符经 SDK os.ExpandEnv 展开。
    # 默认为空字符串，SDK PushgatewayReporter 在 address 为空时通过 Polaris 服务发现自动解析远端 pushgateway。
    (cd "$workdir" \
        && export POLARIS_EVENT_ADDR="${POLARIS_EVENT_ADDR:-}" \
        && exec "${BUILD_DIR}/${name}" \
        --selfNamespace "$NAMESPACE" \
        --selfService "$self_service" \
        --selfRegister=false \
        --calleeNamespace "$NAMESPACE" \
        --calleeService "$callee_service" \
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
    log_info "${name} 已启动 (PID: $pid, port: $port, callee: $callee_service)"
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
# _probe_log_evidence <sdk_cb_log> <out_schedule_var> <out_statechg_var>
#   校验 SDK 熔断日志中探测调度与状态切换的佐证（带落盘重试）。结果写入指定变量名。
#   注意：SDK 内部熔断日志不写到 consumer 应用日志（stdout），而是落在独立文件
#   ${BUILD_DIR}/<name>_run/polaris/log/circuitbreaker/polaris-circuitbreaker.log，
#   因此必须 grep 该文件而非 consumer_log，否则探测调度/状态切换永远匹配不到（误报 no）。
#   关键字大小写需与真实日志一致：调度日志为 "[CircuitBreaker] schedule task"，
#   状态切换为小写 "status change: half-open -> close"（不是驼峰 HalfOpen/Close）。
#   SDK 日志（zap+lumberjack）带写缓冲，consumer 运行中关键字可能尚未落盘，故带超时重试
#   （12 次 * 0.5s，最多约 6s）等待落盘。
_probe_log_evidence() {
    local sdk_cb_log="$1"
    local out_schedule="$2"
    local out_statechg="$3"
    # 内部工作变量必须用不会与调用方传入变量名（has_schedule/has_statechg）撞名的名字：
    # bash 动态作用域下，函数内 local 同名变量会遮蔽调用方变量，printf -v "$out_schedule"
    # （= printf -v has_schedule）会写到本函数的 local 而非调用方的变量，导致调用方读到空值。
    local _sched="no" _stchg="no" _retry
    for _retry in $(seq 1 12); do
        # 探测调度铁证只认 "schedule task"（composite/checker.go:100，探测周期任务真正注册）。
        # 不能用宽松的 [FaultDetect]/health check：关停日志
        # "[FaultDetect] health check for ... is disabled, now stop the previous checker" 同时含
        # [FaultDetect] 和 health check，会把"探测被关停"误判为"探测调度=yes"。
        if [[ "$_sched" == "no" ]] && \
            grep -qE "\[CircuitBreaker\] schedule task" "$sdk_cb_log" 2>/dev/null; then
            _sched="yes"
        fi
        if [[ "$_stchg" == "no" ]] && \
            grep -qiE "status change.*half-open -> close|status change.*open -> half-open" "$sdk_cb_log" 2>/dev/null; then
            _stchg="yes"
        fi
        [[ "$_sched" == "yes" && "$_stchg" == "yes" ]] && break
        sleep 0.5
    done
    printf -v "$out_schedule" '%s' "$_sched"
    printf -v "$out_statechg" '%s' "$_stchg"
}

# _run_probe_abort_case 验证 SERVICE/METHOD 级主动探测完整闭环：
#   OPEN -> 探测维持 OPEN -> 探活 -> HALF_OPEN -> CLOSE
# SERVICE 与 METHOD 级都经 AcquirePermission，OPEN 时业务请求 abort，闭环结构一致，故共用本函数。
# 参数：
#   $1 级别标签（用于日志/打印，如 "服务级(SERVICE)" / "接口级(METHOD)"）
#   $2 case 标签前缀（用于 log_step，如 "服务级" / "接口级"）
#   $3 caller 服务名（主调，决定规则 source 与 consumer selfService）
#   $4 consumer 名（决定 .build/<name>_run 工作目录与 SDK 日志路径）
#   $5 consumer 端口
#   $6 规则级别 FD_RULE_LEVEL（SERVICE / METHOD）
#   $7 destination.method.value 与 BlockConfig.api.path（SERVICE 传 ""，METHOD 传 /echo）
# 结果写入全局变量 _CASE_RESULT（PASS/FAIL）而非 echo 到 stdout。
# 必须在主 shell 直接调用（不可用 r=$(...) 命令替换），否则函数内对 CREATED_*_RULE_IDS /
# CONSUMER_PIDS 的 append 发生在子 shell 中、不回传主 shell，导致 trap cleanup 拿到空数组，
# 规则与进程无法清理（在共享环境残留规则）。
_run_probe_abort_case() {
    local level_label="$1" case_tag="$2" caller="$3" consumer_name="$4" consumer_port="$5"
    local rule_level="$6" method_path="$7"
    # $8 被调服务名（独立于其它级别用例，避免共享 destination 被残留规则抢占）
    local callee_service="${8:-$SERVICE_NAME}"
    # $9 D 段类型（可选）：toggle=enable toggle 验证, interval=规则参数热更新验证, 空=不增加 D 段
    local d_phase="${9:-}"
    # ${10} 用例编号前缀（如 "1"/"2"），用于日志中标识所属用例
    local case_num="${10:-}"
    local step_prefix=""
    [[ -n "$case_num" ]] && step_prefix="用例${case_num} "
    _CASE_RESULT="FAIL"
    local consumer_log="${LOG_DIR}/${consumer_name}.log"

    # METHOD 级：destination.method 与 BlockConfig.api.path 限定到 method_path，探测规则
    # targetService.method 同步限定，使熔断规则、探测规则、业务请求三者方法维度同源。
    # SERVICE 级：method_path 为空 → 用默认 EXACT/* 通配。
    local rule_method_value="*" bc_api_path="" probe_method_value="*"
    if [[ -n "$method_path" ]]; then
        rule_method_value="$method_path"; bc_api_path="$method_path"; probe_method_value="$method_path"
    fi

    print_block "主动探测验证（${level_label}）" \
        "目标服务 : ${callee_service}" \
        "主调服务 : ${caller}" \
        "熔断规则 : ${rule_level} 级, CONSECUTIVE_ERROR=${FD_CONSECUTIVE_ERROR}, sleepWindow=${FD_SLEEP_WINDOW}s, faultDetectConfig.enable=true" \
        "探测规则 : HTTP GET /echo, interval=${FD_PROBE_INTERVAL}s, port=0(实例端口)" \
        "预期闭环 : 业务触发 OPEN -> provider 仍故障探测失败维持 OPEN -> 恢复 provider 探活成功 -> CLOSE"

    # 步骤1：环境复位（provider 全部正常）
    log_step "${step_prefix}${case_tag} [1] 环境复位：provider-a/b 均返回 200"
    provider_set_error "$PROVIDER_A_PORT" "false"
    provider_set_error "$PROVIDER_B_PORT" "false"

    # 步骤2：启动 consumer + 下发熔断规则 + 探测规则
    log_step "${step_prefix}${case_tag} [2] 启动 consumer 并下发规则"
    start_consumer "$consumer_name" "$caller" "$consumer_port" "$consumer_log" "$callee_service" || {
        _CASE_RESULT="FAIL"; return
    }

    local cb_body fd_body cb_rule_name cb_rule_id fd_rule_name fd_rule_id
    cb_rule_name="cb-fd-${rule_level}-${caller}"
    cb_body=$(RULE_NAME="$cb_rule_name" NAMESPACE="$NAMESPACE" SERVICE_NAME="$callee_service" \
        SOURCE_NAMESPACE="$NAMESPACE" SOURCE_SERVICE="$caller" \
        FD_CONSECUTIVE_ERROR="$FD_CONSECUTIVE_ERROR" FD_SLEEP_WINDOW="$FD_SLEEP_WINDOW" \
        FD_RULE_LEVEL="$rule_level" FD_RULE_METHOD_VALUE="$rule_method_value" FD_BC_API_PATH="$bc_api_path" \
        build_circuitbreaker_rule_body)
    cb_rule_id=$(create_circuitbreaker_rule "$cb_body" 2>/dev/null) || { _CASE_RESULT="FAIL"; return; }

    fd_rule_name="fd-fd-${rule_level}-${caller}"
    fd_body=$(RULE_NAME="$fd_rule_name" NAMESPACE="$NAMESPACE" SERVICE_NAME="$callee_service" \
        FD_PROBE_INTERVAL="$FD_PROBE_INTERVAL" FD_PROBE_METHOD_VALUE="$probe_method_value" \
        build_fault_detect_rule_body)
    fd_rule_id=$(create_fault_detect_rule "$fd_body" 2>/dev/null) || { _CASE_RESULT="FAIL"; return; }
    log_info "${step_prefix}${case_tag} 预期生效的主动探测规则: ${fd_rule_name}（protocol=HTTP, method=${probe_method_value}, interval=${FD_PROBE_INTERVAL}s, port=0）"

    wait_seconds "$WAIT_RULE_READY_SECONDS" "${step_prefix}${case_tag} [2] 等待 SDK 拉取熔断/探测规则就绪"

    # 步骤3：触发熔断 OPEN（a/b 全 500）
    log_step "${step_prefix}${case_tag} [3] 触发熔断：provider 全部置 500，burst ${FD_TRIGGER_COUNT} 次"
    provider_set_error "$PROVIDER_A_PORT" "true"
    provider_set_error "$PROVIDER_B_PORT" "true"
    run_burst "$consumer_port" "$FD_TRIGGER_COUNT" "${step_prefix}${case_tag} 触发-OPEN"
    local trigger_abort="$CASE_ABORT"

    # 步骤4：维持 OPEN —— provider 仍故障，探测打 /echo 也失败，半开探测失败立即回 OPEN，不应恢复
    log_step "${step_prefix}${case_tag} [4] 维持 OPEN：provider 保持 500，等待 > sleepWindow 后验证仍熔断"
    wait_seconds "$WAIT_KEEP_OPEN_SECONDS" "${step_prefix}${case_tag} [4] 等待（探测持续失败，应维持 OPEN）"
    run_burst "$consumer_port" "$FD_VERIFY_COUNT" "${step_prefix}${case_tag} 维持-OPEN"
    local keep_abort="$CASE_ABORT"

    # 步骤5：恢复 provider，纯靠主动探测探活推动 HALF_OPEN -> CLOSE
    log_step "${step_prefix}${case_tag} [5] 探活恢复：provider 全部置 200，仅等待主动探测推动恢复（不发业务请求）"
    provider_set_error "$PROVIDER_A_PORT" "false"
    provider_set_error "$PROVIDER_B_PORT" "false"
    wait_seconds "$WAIT_RECOVER_SECONDS" "${step_prefix}${case_tag} [5] 等待主动探测探活并推动 CLOSE（sleepWindow + 多个探测周期）"
    run_burst "$consumer_port" "$FD_VERIFY_COUNT" "${step_prefix}${case_tag} 恢复-CLOSE"
    local recover_ok="$CASE_OK"

    # 步骤6：日志佐证探测真实运行
    log_step "${step_prefix}${case_tag} [6] 校验 SDK 熔断日志中的探测调度与状态切换"
    local sdk_cb_log="${BUILD_DIR}/${consumer_name}_run/polaris/log/circuitbreaker/polaris-circuitbreaker.log"
    local has_schedule has_statechg
    _probe_log_evidence "$sdk_cb_log" has_schedule has_statechg
    log_info "${step_prefix}${case_tag} [6] 日志佐证：探测调度=${has_schedule}, 状态切换=${has_statechg} (来源 ${sdk_cb_log})"

    # ── D 段：规则动态启停 / 热更新验证 ──
    local d_disabled_stop="-" d_resume_schedule="-" d_new_interval="-"

    # D 段 - toggle：enable toggle 验证（关闭→重新开启探测）
    if [[ "$d_phase" == "toggle" ]]; then
        # 记录步骤6时刻的 schedule task 行数（用于步骤8判断恢复后是否新增）
        local schedule_before_d
        schedule_before_d=$(grep -cE "\[CircuitBreaker\] schedule task" "$sdk_cb_log" 2>/dev/null || echo "0")

        # 步骤7：PUT 熔断规则 faultDetectConfig.enable=false，验证探测停止
        log_step "${step_prefix}${case_tag} [7] D段-toggle：关闭探测（PUT faultDetectConfig.enable=false）"
        local cb_body_disabled
        cb_body_disabled=$(RULE_NAME="$cb_rule_name" NAMESPACE="$NAMESPACE" SERVICE_NAME="$callee_service" \
            SOURCE_NAMESPACE="$NAMESPACE" SOURCE_SERVICE="$caller" \
            FD_CONSECUTIVE_ERROR="$FD_CONSECUTIVE_ERROR" FD_SLEEP_WINDOW="$FD_SLEEP_WINDOW" \
            FD_RULE_LEVEL="$rule_level" FD_RULE_METHOD_VALUE="$rule_method_value" FD_BC_API_PATH="$bc_api_path" \
            build_circuitbreaker_rule_body_disable)
        update_circuitbreaker_rule "$cb_rule_id" "$cb_body_disabled"
        wait_seconds "$WAIT_RULE_READY_SECONDS" "${step_prefix}${case_tag} [7] 等待 SDK 感知规则变更（探测应被停止）"
        if grep -qE "\[FaultDetect\] health check for resource=.*is disabled, now stop" "$sdk_cb_log" 2>/dev/null; then
            d_disabled_stop="yes"
        else
            d_disabled_stop="no"
        fi
        log_info "${step_prefix}${case_tag} [7] 探测停止佐证：${d_disabled_stop} (来源 ${sdk_cb_log})"

        # 步骤8：PUT 熔断规则 faultDetectConfig.enable=true，验证探测重新启动
        log_step "${step_prefix}${case_tag} [8] D段-toggle：重新开启探测（PUT faultDetectConfig.enable=true）"
        update_circuitbreaker_rule "$cb_rule_id" "$cb_body"
        wait_seconds "$WAIT_RULE_READY_SECONDS" "${step_prefix}${case_tag} [8] 等待 SDK 感知规则变更（探测应重新启动）"
        local schedule_after_d
        schedule_after_d=$(grep -cE "\[CircuitBreaker\] schedule task" "$sdk_cb_log" 2>/dev/null || echo "0")
        if [[ "$schedule_after_d" -gt "$schedule_before_d" ]]; then
            d_resume_schedule="yes"
        else
            d_resume_schedule="no"
        fi
        log_info "${step_prefix}${case_tag} [8] 探测恢复佐证：schedule task 行数 ${schedule_before_d}→${schedule_after_d} (${d_resume_schedule})"
    fi

    # D 段 - interval：规则参数热更新验证（修改探测间隔）
    if [[ "$d_phase" == "interval" ]]; then
        log_step "${step_prefix}${case_tag} [7] D段-interval：修改探测间隔 interval=${FD_PROBE_INTERVAL}s→${FD_PROBE_INTERVAL_UPDATED}s"
        local fd_body_updated
        fd_body_updated=$(RULE_NAME="$fd_rule_name" NAMESPACE="$NAMESPACE" SERVICE_NAME="$callee_service" \
            FD_PROBE_INTERVAL="$FD_PROBE_INTERVAL_UPDATED" FD_PROBE_METHOD_VALUE="$probe_method_value" \
            build_fault_detect_rule_body)
        update_fault_detect_rule "$fd_rule_id" "$fd_body_updated"
        wait_seconds "$WAIT_RULE_READY_SECONDS" "${step_prefix}${case_tag} [7] 等待 SDK 感知探测规则变更（应重建 checker）"
        # 验证 SDK 日志中出现 interval=${FD_PROBE_INTERVAL_UPDATED}s 的 schedule task
        if grep -qE "\[CircuitBreaker\] schedule task.*interval=${FD_PROBE_INTERVAL_UPDATED}s" "$sdk_cb_log" 2>/dev/null; then
            d_new_interval="yes"
        else
            d_new_interval="no"
        fi
        log_info "${step_prefix}${case_tag} [7] 探测间隔更新佐证：interval=${FD_PROBE_INTERVAL_UPDATED}s schedule task 出现=${d_new_interval} (来源 ${sdk_cb_log})"
    fi

    # 判定（含 D 段指标）
    print_block "${step_prefix}${case_tag} 判定指标" \
        "触发 OPEN  : abort=${trigger_abort} (期望 >=1)" \
        "维持 OPEN  : abort=${keep_abort} (期望 >=1，探测失败不恢复)" \
        "探活 CLOSE : ok=${recover_ok}/${FD_VERIFY_COUNT} (期望 == ${FD_VERIFY_COUNT})" \
        "日志佐证   : 探测调度=${has_schedule}, 状态切换=${has_statechg} (期望均为 yes)" \
        "D段-关闭   : disabled_stop=${d_disabled_stop} (期望 yes，toggle 用例; 非 toggle 用例=- 忽略)" \
        "D段-恢复   : resume_schedule=${d_resume_schedule} (期望 yes，toggle 用例; 非 toggle 用例=- 忽略)" \
        "D段-间隔   : new_interval=${d_new_interval} (期望 yes，interval 用例; 非 interval 用例=- 忽略)"

    local abc_pass="no"
    if [[ "$trigger_abort" -ge 1 ]] && [[ "$keep_abort" -ge 1 ]] \
        && [[ "$recover_ok" -eq "$FD_VERIFY_COUNT" ]] \
        && [[ "$has_schedule" == "yes" ]] && [[ "$has_statechg" == "yes" ]]; then
        abc_pass="yes"
    fi

    local d_pass="yes"
    if [[ "$d_phase" == "toggle" ]]; then
        [[ "$d_disabled_stop" != "yes" ]] && d_pass="no"
        [[ "$d_resume_schedule" != "yes" ]] && d_pass="no"
    elif [[ "$d_phase" == "interval" ]]; then
        [[ "$d_new_interval" != "yes" ]] && d_pass="no"
    fi

    if [[ "$abc_pass" == "yes" ]] && [[ "$d_pass" == "yes" ]]; then
        _CASE_RESULT="PASS"
    else
        _CASE_RESULT="FAIL"
    fi
}

# case_fault_detect 服务级（SERVICE）主动探测验证。被调服务用独立 FD_SVC_CALLEE。
# D 段增加 enable toggle 验证：关闭探测(faultDetectConfig.enable=false)→验证停止→重新开启→验证恢复。
case_fault_detect() {
    _run_probe_abort_case "服务级(SERVICE)" "服务级" "$FD_CALLER" \
        "fault_detect_consumer" "$FD_CONSUMER_PORT" "SERVICE" "" "$FD_SVC_CALLEE" "toggle" "1"
}

# case_fault_detect_method 接口级（METHOD）主动探测验证。被调服务用独立 FD_METHOD_CALLEE。
# 与 SERVICE 级闭环一致（都经 AcquirePermission，OPEN 时 abort），区别仅在规则 level=METHOD、
# destination.method 与 BlockConfig.api.path 限定 /echo、探测规则 targetService.method=/echo。
# D 段增加规则参数热更新验证：修改探测间隔 interval 2s→5s→验证 schedule task 日志间隔变化。
case_fault_detect_method() {
    _run_probe_abort_case "接口级(METHOD)" "接口级" "$FD_METHOD_CALLER" \
        "fd_method_consumer" "$FD_METHOD_CONSUMER_PORT" "METHOD" "/echo" "$FD_METHOD_CALLEE" "interval" "2"
}

# _run_probe_tcpudp_case 验证 TCP / UDP 协议主动探测闭环（SERVICE 级，abort 型）。
# 与 HTTP 用例的关键区别：
#   - 探测协议为 TCP(2)/UDP(3)，探测打 provider-a 的独立探测端口（probe_port），靠 send/receive 内容匹配判健康；
#   - 业务故障（触发熔断 OPEN）仍用 HTTP /echo 的 openError 开关；
#   - 探测故障（维持 OPEN）用 openTcpError/openUdpError 开关让 TCP/UDP 探测失败；
#   - 恢复阶段两个开关都翻回，探测探活成功推动 close。
#   - TCP/UDP 探测端口只在 provider-a 启动，故探测只探 a（SERVICE 级闭环不依赖多实例）。
# 参数：$1 级别标签 $2 case 标签 $3 caller $4 consumer 名 $5 consumer 端口 $6 被调服务
#       $7 探测协议(2/3) $8 探测端口 $9 send ${10} receive ${11} 探测故障开关参数名(openTcpError/openUdpError)
#       ${12} 用例编号前缀（如 "4"/"5"）
_run_probe_tcpudp_case() {
    local level_label="$1" case_tag="$2" caller="$3" consumer_name="$4" consumer_port="$5"
    local callee_service="$6" probe_protocol="$7" probe_port="$8" probe_send="$9" probe_receive="${10}"
    local probe_err_switch="${11}"
    local case_num="${12:-}"
    local step_prefix=""
    [[ -n "$case_num" ]] && step_prefix="用例${case_num} "
    _CASE_RESULT="FAIL"
    local consumer_log="${LOG_DIR}/${consumer_name}.log"

    print_block "主动探测验证（${level_label}）" \
        "目标服务 : ${callee_service}" \
        "主调服务 : ${caller}" \
        "熔断规则 : SERVICE 级, CONSECUTIVE_ERROR=${FD_CONSECUTIVE_ERROR}, sleepWindow=${FD_SLEEP_WINDOW}s, faultDetectConfig.enable=true" \
        "探测规则 : protocol=${probe_protocol}(2=TCP/3=UDP), port=${probe_port}, send=${probe_send}, receive=${probe_receive}, interval=${FD_PROBE_INTERVAL}s" \
        "预期闭环 : 业务(/echo)触发 OPEN -> ${probe_err_switch} 置故障探测失败维持 OPEN -> 恢复探测探活 -> CLOSE"

    # 步骤1：环境复位（业务 /echo 与探测端口均正常）
    log_step "${step_prefix}${case_tag} [1] 环境复位：provider 业务/探测端口均正常"
    provider_set_error "$PROVIDER_A_PORT" "false"
    provider_set_error "$PROVIDER_B_PORT" "false"
    provider_set_switch "$PROVIDER_A_PORT" "$probe_err_switch" "false"

    # 步骤2：启动 consumer + 下发 SERVICE 级熔断规则 + TCP/UDP 探测规则
    log_step "${step_prefix}${case_tag} [2] 启动 consumer 并下发规则"
    start_consumer "$consumer_name" "$caller" "$consumer_port" "$consumer_log" "$callee_service" || {
        _CASE_RESULT="FAIL"; return
    }

    local cb_body fd_body
    cb_body=$(RULE_NAME="cb-fd-SERVICE-${caller}" NAMESPACE="$NAMESPACE" SERVICE_NAME="$callee_service" \
        SOURCE_NAMESPACE="$NAMESPACE" SOURCE_SERVICE="$caller" \
        FD_CONSECUTIVE_ERROR="$FD_CONSECUTIVE_ERROR" FD_SLEEP_WINDOW="$FD_SLEEP_WINDOW" \
        FD_RULE_LEVEL="SERVICE" FD_RULE_METHOD_VALUE="*" FD_BC_API_PATH="" \
        build_circuitbreaker_rule_body)
    create_circuitbreaker_rule "$cb_body" > /dev/null || { _CASE_RESULT="FAIL"; return; }

    fd_body=$(RULE_NAME="fd-fd-${probe_protocol}-${caller}" NAMESPACE="$NAMESPACE" SERVICE_NAME="$callee_service" \
        FD_PROBE_INTERVAL="$FD_PROBE_INTERVAL" \
        FD_PROBE_PROTOCOL="$probe_protocol" FD_PROBE_PORT="$probe_port" \
        FD_PROBE_SEND="$probe_send" FD_PROBE_RECEIVE="$probe_receive" \
        build_fault_detect_rule_body)
    create_fault_detect_rule "$fd_body" > /dev/null || { _CASE_RESULT="FAIL"; return; }
    log_info "${step_prefix}${case_tag} 预期生效的主动探测规则: fd-fd-${probe_protocol}-${caller}（protocol=${probe_protocol}, port=${probe_port}, send=${probe_send}, receive=${probe_receive}, interval=${FD_PROBE_INTERVAL}s）"

    wait_seconds "$WAIT_RULE_READY_SECONDS" "${step_prefix}${case_tag} [2] 等待 SDK 拉取熔断/探测规则就绪"

    # 步骤3：触发 SERVICE 级熔断 OPEN（业务 /echo 全 500）
    log_step "${step_prefix}${case_tag} [3] 触发熔断：provider /echo 置 500，burst ${FD_TRIGGER_COUNT} 次"
    provider_set_error "$PROVIDER_A_PORT" "true"
    provider_set_error "$PROVIDER_B_PORT" "true"
    run_burst "$consumer_port" "$FD_TRIGGER_COUNT" "${step_prefix}${case_tag} 触发-OPEN"
    local trigger_abort="$CASE_ABORT"

    # 步骤4：维持 OPEN —— 业务 /echo 保持 500（持续触发）+ 探测端口置故障（探测失败）。
    # 注意：不能在此恢复业务 /echo 为 200——SERVICE 级熔断进 half-open 态会放行业务请求探活，
    # 业务请求成功会抢先把熔断 close 掉，导致探测维持 OPEN 验证失效（实测 abort=0）。
    # 与 HTTP 用例一致：维持阶段业务保持 500，探测也故障，OPEN 才稳定。
    log_step "${step_prefix}${case_tag} [4] 维持 OPEN：业务 /echo 保持 500 + 探测端口故障，等待 > sleepWindow 验证仍熔断"
    provider_set_switch "$PROVIDER_A_PORT" "$probe_err_switch" "true"
    wait_seconds "$WAIT_KEEP_OPEN_SECONDS" "${step_prefix}${case_tag} [4] 等待（${probe_err_switch} 探测持续失败，应维持 OPEN）"
    run_burst "$consumer_port" "$FD_VERIFY_COUNT" "${step_prefix}${case_tag} 维持-OPEN"
    local keep_abort="$CASE_ABORT"

    # 步骤5：探活恢复 —— 业务 /echo 与探测端口都恢复正常，主动探测探活推动 CLOSE
    log_step "${step_prefix}${case_tag} [5] 探活恢复：业务 /echo 与 ${probe_err_switch} 均置正常，等待主动探测推动 CLOSE"
    provider_set_error "$PROVIDER_A_PORT" "false"
    provider_set_error "$PROVIDER_B_PORT" "false"
    provider_set_switch "$PROVIDER_A_PORT" "$probe_err_switch" "false"
    wait_seconds "$WAIT_RECOVER_SECONDS" "${step_prefix}${case_tag} [5] 等待主动探测探活并推动 CLOSE（sleepWindow + 多个探测周期）"
    run_burst "$consumer_port" "$FD_VERIFY_COUNT" "${step_prefix}${case_tag} 恢复-CLOSE"
    local recover_ok="$CASE_OK"

    # 步骤6：日志佐证探测真实运行
    log_step "${step_prefix}${case_tag} [6] 校验 SDK 熔断日志中的探测调度与状态切换"
    local sdk_cb_log="${BUILD_DIR}/${consumer_name}_run/polaris/log/circuitbreaker/polaris-circuitbreaker.log"
    local has_schedule has_statechg
    _probe_log_evidence "$sdk_cb_log" has_schedule has_statechg
    log_info "${step_prefix}${case_tag} [6] 日志佐证：探测调度=${has_schedule}, 状态切换=${has_statechg} (来源 ${sdk_cb_log})"

    # 判定
    print_block "${step_prefix}${case_tag} 判定指标" \
        "触发 OPEN  : abort=${trigger_abort} (期望 >=1)" \
        "维持 OPEN  : abort=${keep_abort} (期望 >=1，探测失败不恢复)" \
        "探活 CLOSE : ok=${recover_ok}/${FD_VERIFY_COUNT} (期望 == ${FD_VERIFY_COUNT})" \
        "日志佐证   : 探测调度=${has_schedule}, 状态切换=${has_statechg} (期望均为 yes)"

    if [[ "$trigger_abort" -ge 1 ]] && [[ "$keep_abort" -ge 1 ]] \
        && [[ "$recover_ok" -eq "$FD_VERIFY_COUNT" ]] \
        && [[ "$has_schedule" == "yes" ]] && [[ "$has_statechg" == "yes" ]]; then
        _CASE_RESULT="PASS"
    else
        _CASE_RESULT="FAIL"
    fi
}

# case_fault_detect_tcp TCP 协议主动探测验证（探 provider-a 的 28091 端口，send=ping/receive=tcp-ok）
case_fault_detect_tcp() {
    _run_probe_tcpudp_case "TCP 探测" "TCP" "$FD_TCP_CALLER" \
        "fd_tcp_consumer" "$FD_TCP_CONSUMER_PORT" "$FD_TCP_CALLEE" \
        "2" "$FD_TCP_PROBE_PORT" "ping" "tcp-ok" "openTcpError" "4"
}

# case_fault_detect_udp UDP 协议主动探测验证（探 provider-a 的 28101 端口，send=ping/receive=udp-ok）
case_fault_detect_udp() {
    _run_probe_tcpudp_case "UDP 探测" "UDP" "$FD_UDP_CALLER" \
        "fd_udp_consumer" "$FD_UDP_CONSUMER_PORT" "$FD_UDP_CALLEE" \
        "3" "$FD_UDP_PROBE_PORT" "ping" "udp-ok" "openUdpError" "5"
}

# case_fault_detect_proto_method 验证探测规则 target_service.method 和 http_config.headers 维度。
# 分三段：
#   A) SERVICE 级 method 过滤：验证已有 SERVICE 探测规则（method=* 通配）被 selectFaultDetectRules 接受；
#      非通配拒绝的结论由段 B.2（METHOD 级 nonmatch 被拒绝）覆盖——两者共享 matchMethodWithAPI 路径。
#   B) METHOD 级 method 过滤 + 完整闭环：验证精确匹配 method.value 的规则被选中并走完熔断闭环。
#   C) HTTP headers：验证 http_config.headers 被正确设置到探测请求头。
# 复用已有 SERVICE consumer（18095）和 METHOD consumer（18096），不新增进程。
# ⚠️ A 段不创建新规则：SDK 的 selectFaultDetectRules 对同 protocol 只取第一条匹配规则，
#   SERVICE 用例的 fd-fd-SERVICE-*（method=*）已占用 HTTP 协议 slot，新规则不会被选中。
case_fault_detect_proto_method() {
    _CASE_RESULT="FAIL"
    local case_tag="协议方法"
    local case_num="6"
    local step_prefix="用例${case_num} "
    local a_pass="no" b_pass="no" c_pass="no"

    # ── 段 A：SERVICE 级 method 过滤 ──
    # 复用 SERVICE consumer（18095）和 FD_SVC_CALLEE。
    # SERVICE 用例已创建探测规则 fd-fd-SERVICE-CircuitBreakerFaultDetectCaller（method=* 通配），
    # 若该规则被 schedule task 选中，即验证了"SERVICE 级接受通配 method"。
    # 非通配拒绝验证由段 B.2（METHOD 级 nonmatch 被拒绝）等价覆盖：
    #   selectFaultDetectRules → matchMethodWithAPI → IsMatchAll 对非通配返回 false，
    #   SERVICE 级与 METHOD 级共享同一代码路径，METHOD 级拒绝 → SERVICE 级同理。
    local svc_sdk_cb_log="${BUILD_DIR}/fault_detect_consumer_run/polaris/log/circuitbreaker/polaris-circuitbreaker.log"
    local svc_probe_rule="fd-fd-SERVICE-${FD_CALLER}"
    local has_a1="yes"

    log_info "${step_prefix}${case_tag} [A] 预期生效的主动探测规则: ${svc_probe_rule}（复用 SERVICE 用例已有规则，method=*, interval=${FD_PROBE_INTERVAL}s, port=0）"
    log_step "${step_prefix}${case_tag} [A] 段A-通配：验证已有 SERVICE 探测规则（method=*）被 schedule task 选中"
    # 验证：已有 SERVICE 探测规则 method=* 已被 schedule task 选中。
    # selectFaultDetectRules 对 SERVICE 级用 IsMatchAll 判断 method.value="*" → 返回 true → 规则被选中。
    # 铁证：schedule task 中出现 SERVICE 探测规则名（不含 wildcard 后缀，即 SERVICE 用例创建的规则）。
    # 使用 \b 边界匹配确保精确匹配规则名（不与 -wildcard 等后缀规则混淆）。
    local has_a2="no"
    if grep -qE "\[CircuitBreaker\] schedule task.*${svc_probe_rule}\b" "$svc_sdk_cb_log" 2>/dev/null; then
        has_a2="yes"
    fi
    log_info "${step_prefix}${case_tag} [A] SERVICE 级接受通配 method（已有规则 ${svc_probe_rule}）：schedule task 含该规则=$(grep -cE "schedule task.*${svc_probe_rule}\b" "$svc_sdk_cb_log" 2>/dev/null || echo 0) (期望 >0, got ${has_a2})"

    if [[ "$has_a1" == "yes" && "$has_a2" == "yes" ]]; then
        a_pass="yes"
    fi

    # ── 段 B：METHOD 级 method 过滤 + 完整闭环 ──
    # /echo 匹配复用用例二已创建的 fd-fd-METHOD-CircuitBreakerFDMethodCaller（method=/echo，
    # 与本段所需规则完全等价），不再新建冗余的 -echo 规则；仅新建 -nonmatch 验证"非匹配 method 被拒绝"。
    log_step "${step_prefix}${case_tag} [B.1] 段B：复用 /echo 探测规则 + 新建 /api/protocol/http 探测规则（METHOD 级只应选中 /echo）"
    local method_consumer_log="${LOG_DIR}/fd_method_consumer.log"
    local method_sdk_cb_log="${BUILD_DIR}/fd_method_consumer_run/polaris/log/circuitbreaker/polaris-circuitbreaker.log"
    local method_probe_rule="fd-fd-METHOD-${FD_METHOD_CALLER}"

    local sched_before_b
    sched_before_b=$(grep -cE "\[CircuitBreaker\] schedule task" "$method_sdk_cb_log" 2>/dev/null || echo "0")

    # 创建 method.value=/api/protocol/http 的探测规则（应被 METHOD 级 selectFaultDetectRules 拒绝）。
    # method.value=/echo 的探测规则由用例二的 fd-fd-METHOD-CircuitBreakerFDMethodCaller 提供，无需重建。
    local fd_body_b2
    fd_body_b2=$(RULE_NAME="fd-fd-METHOD-${FD_METHOD_CALLER}-nonmatch" NAMESPACE="$NAMESPACE" SERVICE_NAME="$FD_METHOD_CALLEE" \
        FD_PROBE_INTERVAL="$FD_PROBE_INTERVAL" FD_PROBE_METHOD_VALUE="/api/protocol/http" \
        build_fault_detect_rule_body)
    create_fault_detect_rule "$fd_body_b2" > /dev/null 2>&1 || true
    log_info "${step_prefix}${case_tag} [B] 预期生效的主动探测规则: ${method_probe_rule}（复用用例二已有规则，应被选中，method=/echo）; fd-fd-METHOD-${FD_METHOD_CALLER}-nonmatch（应被拒绝，method=/api/protocol/http）"

    wait_seconds "$WAIT_RULE_READY_SECONDS" "${step_prefix}${case_tag} [B.1] 等待 SDK 拉取探测规则"

    local has_b_filter
    # 验证：/echo 的 schedule task 出现（METHOD 级只应选中与 resource.path 匹配的规则）。
    # 复用的 fd-fd-METHOD-* 规则 method=/echo，被选中后 schedule task 含 path=/echo；
    # -nonmatch（method=/api/protocol/http）不匹配 resource.path=/echo，不会出现 schedule task。
    if grep -qE "\[CircuitBreaker\] schedule task.*path=/echo" "$method_sdk_cb_log" 2>/dev/null; then
        has_b_filter="yes"
    else
        has_b_filter="no"
    fi
    log_info "${step_prefix}${case_tag} [B.1] METHOD 级 method 过滤：/echo match=${has_b_filter}（复用规则 ${method_probe_rule}）"

    # 段 B 闭环验证（复用已有 METHOD consumer，触发 /echo 路径熔断）
    log_step "${step_prefix}${case_tag} [B.2] 段B-闭环：触发熔断 OPEN"
    provider_set_error "$PROVIDER_A_PORT" "true"
    provider_set_error "$PROVIDER_B_PORT" "true"
    run_burst "$FD_METHOD_CONSUMER_PORT" "$FD_TRIGGER_COUNT" "${step_prefix}${case_tag} B-trigger"
    local b_trigger_abort="$CASE_ABORT"

    log_step "${step_prefix}${case_tag} [B.3] 段B-闭环：维持 OPEN"
    wait_seconds "$WAIT_KEEP_OPEN_SECONDS" "${step_prefix}${case_tag} [B.3] 等待（探测持续失败，应维持 OPEN）"
    run_burst "$FD_METHOD_CONSUMER_PORT" "$FD_VERIFY_COUNT" "${step_prefix}${case_tag} B-maintain"
    local b_keep_abort="$CASE_ABORT"

    log_step "${step_prefix}${case_tag} [B.4] 段B-闭环：探活恢复"
    provider_set_error "$PROVIDER_A_PORT" "false"
    provider_set_error "$PROVIDER_B_PORT" "false"
    wait_seconds "$WAIT_RECOVER_SECONDS" "${step_prefix}${case_tag} [B.4] 等待主动探测推动 CLOSE"
    run_burst "$FD_METHOD_CONSUMER_PORT" "$FD_VERIFY_COUNT" "${step_prefix}${case_tag} B-recover"
    local b_recover_ok="$CASE_OK"

    log_step "${step_prefix}${case_tag} [B.5] 段B-闭环：日志佐证"
    local b_has_schedule b_has_statechg
    _probe_log_evidence "$method_sdk_cb_log" b_has_schedule b_has_statechg
    log_info "${step_prefix}${case_tag} [B.5] 日志佐证：探测调度=${b_has_schedule}, 状态切换=${b_has_statechg}"

    if [[ "$has_b_filter" == "yes" ]] \
        && [[ "$b_trigger_abort" -ge 1 ]] && [[ "$b_keep_abort" -ge 1 ]] \
        && [[ "$b_recover_ok" -eq "$FD_VERIFY_COUNT" ]] \
        && [[ "$b_has_schedule" == "yes" ]] && [[ "$b_has_statechg" == "yes" ]]; then
        b_pass="yes"
    fi

    # ── 段 C：HTTP Headers 验证 ──
    # 策略：PUT 更新已有探测规则加 X-Health-Probe header。
    # 通过 HTTP API 确认规则 body 含 headers（主验证），provider 日志作为辅助（依赖 SDK revision 感知）。
    log_step "${step_prefix}${case_tag} [C.1] 段C：PUT 更新已有探测规则加 X-Health-Probe header"
    local c_rule_name="fd-fd-SERVICE-${FD_CALLER}"
    log_info "${step_prefix}${case_tag} [C] 预期生效的主动探测规则: ${c_rule_name}（复用 SERVICE 用例已有规则，PUT 更新加 X-Health-Probe header, method=*, interval=${FD_PROBE_INTERVAL}s）"
    local c_rule_id
    c_rule_id=$(query_fault_detect_rule_id_by_name "$c_rule_name") || true
    if [[ -z "$c_rule_id" ]]; then
        log_error "${step_prefix}${case_tag} [C.1] 未找到 SERVICE 探测规则 ${c_rule_name}，无法验证 headers"
        return
    fi

    # 构造带 header 的探测规则 body 并 PUT 更新
    local fd_body_c
    fd_body_c=$(RULE_NAME="$c_rule_name" NAMESPACE="$NAMESPACE" SERVICE_NAME="$FD_SVC_CALLEE" \
        FD_PROBE_INTERVAL="$FD_PROBE_INTERVAL" FD_PROBE_METHOD_VALUE="*" \
        FD_HTTP_HEADERS='[{"key":"X-Health-Probe","value":"true"}]' \
        build_fault_detect_rule_body)
    update_fault_detect_rule "$c_rule_id" "$fd_body_c"
    wait_seconds "$WAIT_RULE_READY_SECONDS" "${step_prefix}${case_tag} [C.1] 等待规则更新生效"

    # 验证探测调度仍存在
    local c_has_schedule="no"
    if grep -qE "\[CircuitBreaker\] schedule task.*${c_rule_name}" "$svc_sdk_cb_log" 2>/dev/null; then
        c_has_schedule="yes"
    fi
    log_info "${step_prefix}${case_tag} [C.1] header 探测规则调度：${c_has_schedule}"

    # 通过 HTTP API 查询规则 body，确认 headers 已写入
    log_step "${step_prefix}${case_tag} [C.2] 段C：HTTP API 查询确认规则含 X-Health-Probe header"
    local c_api_has_header="no"
    local c_resp
    c_resp=$(curl -s --connect-timeout 5 --max-time 10 \
        "${POLARIS_HTTP_ADDR}/naming/v1/faultdetectors?id=${c_rule_id}&namespace=${NAMESPACE}&limit=1&offset=0" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" 2>/dev/null || echo "")
    if echo "$c_resp" | grep -q "X-Health-Probe"; then
        c_api_has_header="yes"
    fi
    log_info "${step_prefix}${case_tag} [C.2] HTTP API 确认规则含 header：${c_api_has_header}"

    # 等待探测周期，验证 provider 日志（辅助验证，依赖 SDK revision 感知）
    log_step "${step_prefix}${case_tag} [C.3] 段C：等待探测周期，验证 provider 日志含 X-Health-Probe header"
    wait_seconds "$((FD_PROBE_INTERVAL * 3 + 2))" "${step_prefix}${case_tag} [C.3] 等待探测请求到达 provider"
    local c_has_header="no"
    if grep -q "X-Health-Probe=true" "${LOG_DIR}/fd_provider_a.log" 2>/dev/null; then
        c_has_header="yes"
    fi
    log_info "${step_prefix}${case_tag} [C.3] provider 日志含 X-Health-Probe header：${c_has_header}"

    # PUT 恢复：去掉 headers
    log_step "${step_prefix}${case_tag} [C.4] 段C：PUT 恢复探测规则（去掉 X-Health-Probe header）"
    local fd_body_c_restore
    fd_body_c_restore=$(RULE_NAME="$c_rule_name" NAMESPACE="$NAMESPACE" SERVICE_NAME="$FD_SVC_CALLEE" \
        FD_PROBE_INTERVAL="$FD_PROBE_INTERVAL" FD_PROBE_METHOD_VALUE="*" \
        build_fault_detect_rule_body)
    update_fault_detect_rule "$c_rule_id" "$fd_body_c_restore"

    # 判定：API 确认 headers + 探测调度存在 = PASS
    # provider 日志验证为辅助，不作为硬性条件
    if [[ "$c_has_schedule" == "yes" && "$c_api_has_header" == "yes" ]]; then
        c_pass="yes"
    fi

    # ── 汇总判定 ──
    print_block "${step_prefix}${case_tag} 判定指标" \
        "段A-SERVICE过滤 : 拒绝非通配(段B.2等价覆盖)=${has_a1} (期望 yes), 接受通配=${has_a2} (期望 yes)" \
        "段B-METHOD过滤  : /echo匹配=${has_b_filter} (期望 yes)" \
        "段B-闭环        : trigger_abort=${b_trigger_abort}, keep_abort=${b_keep_abort}, recover_ok=${b_recover_ok}/${FD_VERIFY_COUNT}" \
        "段B-日志佐证    : 调度=${b_has_schedule}, 状态切换=${b_has_statechg}" \
        "段C-headers     : 调度=${c_has_schedule}, provider_header=${c_has_header}"

    if [[ "$a_pass" == "yes" && "$b_pass" == "yes" && "$c_pass" == "yes" ]]; then
        _CASE_RESULT="PASS"
    else
        _CASE_RESULT="FAIL"
    fi
}

# case_fault_detect_instance 实例级（INSTANCE）主动探测验证（单实例故障方案）
# INSTANCE 级与 SERVICE/METHOD 级语义不同：INSTANCE 级熔断**不经过 AcquirePermission**，某实例
# OPEN 时由服务路由层（FilterOnlyRouter）将其从可选列表摘除、LB 不再选它，业务请求落到其余健康
# 实例（返回 200），而**不是** abort 503。因此判定逻辑独立于 abort 型用例：
#   - 只把 provider-b 置 500（a 保持 200），触发 b 实例的 INSTANCE 级熔断 OPEN
#   - 维持 OPEN：b 仍 500，业务请求应全部落到 a → ok=10/10（证明 b 被摘除、业务无感）
#   - 探活恢复：b 恢复 200，纯靠主动探测探活 b，使 b 实例 half-open->close 重新可选
#   - 铁证：provider-b 收到不带 X-Request-Id 的探测请求 + SDK 日志出现 INSTANCE 级 half-open->close
# 结果写入全局变量 _CASE_RESULT；必须主 shell 直接调用（理由同 _run_probe_abort_case）。
case_fault_detect_instance() {
    _CASE_RESULT="FAIL"
    local case_tag="实例级"
    local case_num="3"
    local step_prefix="用例${case_num} "
    local caller="$FD_INSTANCE_CALLER"
    local callee_service="$FD_INSTANCE_CALLEE"
    local consumer_name="fd_instance_consumer"
    local consumer_port="$FD_INSTANCE_CONSUMER_PORT"
    local consumer_log="${LOG_DIR}/${consumer_name}.log"

    print_block "主动探测验证（实例级(INSTANCE)）" \
        "目标服务 : ${callee_service}" \
        "主调服务 : ${caller}" \
        "熔断规则 : INSTANCE 级, CONSECUTIVE_ERROR=${FD_CONSECUTIVE_ERROR}, sleepWindow=${FD_SLEEP_WINDOW}s, faultDetectConfig.enable=true" \
        "探测规则 : HTTP GET /echo, interval=${FD_PROBE_INTERVAL}s, port=0(实例端口)" \
        "预期闭环 : 仅 b 故障触发 b 实例 OPEN -> b 被路由摘除业务落 a(ok) -> 恢复 b 探测探活 -> b 实例 CLOSE 重新可选"

    log_info "${step_prefix}${case_tag} 预期生效的主动探测规则: fd-fd-INSTANCE-${caller}（protocol=HTTP, method=*, interval=${FD_PROBE_INTERVAL}s, port=0）"

    # 步骤1：环境复位（a/b 均 200）
    log_step "${step_prefix}${case_tag} [1] 环境复位：provider-a/b 均返回 200"
    provider_set_error "$PROVIDER_A_PORT" "false"
    provider_set_error "$PROVIDER_B_PORT" "false"

    # 步骤2：启动 consumer + 下发 INSTANCE 级熔断规则 + 探测规则
    log_step "${step_prefix}${case_tag} [2] 启动 consumer 并下发规则"
    start_consumer "$consumer_name" "$caller" "$consumer_port" "$consumer_log" "$callee_service" || {
        _CASE_RESULT="FAIL"; return
    }

    local cb_body fd_body
    cb_body=$(RULE_NAME="cb-fd-INSTANCE-${caller}" NAMESPACE="$NAMESPACE" SERVICE_NAME="$callee_service" \
        SOURCE_NAMESPACE="$NAMESPACE" SOURCE_SERVICE="$caller" \
        FD_CONSECUTIVE_ERROR="$FD_CONSECUTIVE_ERROR" FD_SLEEP_WINDOW="$FD_SLEEP_WINDOW" \
        FD_RULE_LEVEL="INSTANCE" FD_RULE_METHOD_VALUE="*" FD_BC_API_PATH="" \
        build_circuitbreaker_rule_body)
    create_circuitbreaker_rule "$cb_body" > /dev/null || { _CASE_RESULT="FAIL"; return; }

    fd_body=$(RULE_NAME="fd-fd-INSTANCE-${caller}" NAMESPACE="$NAMESPACE" SERVICE_NAME="$callee_service" \
        FD_PROBE_INTERVAL="$FD_PROBE_INTERVAL" FD_PROBE_METHOD_VALUE="*" \
        build_fault_detect_rule_body)
    create_fault_detect_rule "$fd_body" > /dev/null || { _CASE_RESULT="FAIL"; return; }

    wait_seconds "$WAIT_RULE_READY_SECONDS" "${step_prefix}${case_tag} [2] 等待 SDK 拉取熔断/探测规则就绪"

    # 步骤3：仅 b 置 500 触发 b 实例 OPEN（a 保持 200）
    log_step "${step_prefix}${case_tag} [3] 触发 b 实例熔断：仅 provider-b 置 500，burst ${FD_TRIGGER_COUNT} 次"
    provider_set_error "$PROVIDER_A_PORT" "false"
    provider_set_error "$PROVIDER_B_PORT" "true"
    run_burst "$consumer_port" "$FD_TRIGGER_COUNT" "${step_prefix}${case_tag} 触发-OPEN"
    local trigger_fail="$CASE_FAIL"

    # 步骤4：维持 OPEN —— b 仍 500，b 被路由摘除，业务请求应（绝大多数）落 a 返回 200。
    # 容忍 1 次抖动：b 刚 OPEN 的瞬间可能有 1 个在途请求仍命中 b（500），故判定用 ok >= N-1。
    log_step "${step_prefix}${case_tag} [4] 维持 OPEN：b 保持 500，等待 > sleepWindow，业务请求应全落 a(200)"
    wait_seconds "$WAIT_KEEP_OPEN_SECONDS" "${step_prefix}${case_tag} [4] 等待（b 探测持续失败，b 维持 OPEN 被摘除）"
    run_burst "$consumer_port" "$FD_VERIFY_COUNT" "${step_prefix}${case_tag} 维持-OPEN(b被摘除)"
    local keep_ok="$CASE_OK"

    # 步骤5：恢复 b，等待恢复推动 b 实例 half-open->close 重新可选。
    # 注意（实测）：INSTANCE 级 b 实例进入 half-open 态后，SDK 会放行业务请求去探活 b，
    # 因此 b 的最终 close 通常由 half-open 态业务请求探活推动；主动探测在 b OPEN 期间也持续
    # 探测 b（探测调度=yes 佐证），两者共同保障恢复。本步等待足够长（sleepWindow + 多个探测/
    # 半开周期）后发 burst，验证 b 已重新可选（业务请求能再次落到 b）。
    log_step "${step_prefix}${case_tag} [5] 探活恢复：provider-b 置 200，等待恢复推动 b 实例 CLOSE 重新可选"
    provider_set_error "$PROVIDER_B_PORT" "false"
    wait_seconds "$WAIT_RECOVER_SECONDS" "${step_prefix}${case_tag} [5] 等待 b 实例 half-open->close 恢复（sleepWindow + 多个周期）"
    run_burst "$consumer_port" "$FD_VERIFY_COUNT" "${step_prefix}${case_tag} 恢复-CLOSE(a/b均可选)"
    local recover_ok="$CASE_OK"

    # 步骤6：日志佐证 —— 探测调度在跑 + 出现 INSTANCE 级 b 实例 half-open->close。
    # 不再用"provider-b 探测请求增量"作铁证：provider 为三个用例共享，探测计数会串扰；且 INSTANCE
    # 级 b 的恢复实际由 half-open 业务请求探活推动（探测探活请求不一定落在恢复窗口），增量判定不可靠。
    log_step "${step_prefix}${case_tag} [6] 校验 SDK 熔断日志（探测调度 + INSTANCE 级状态切换）"
    local sdk_cb_log="${BUILD_DIR}/${consumer_name}_run/polaris/log/circuitbreaker/polaris-circuitbreaker.log"
    local has_schedule="no" has_inst_statechg="no" _retry
    for _retry in $(seq 1 12); do
        # 探测调度铁证只认 "schedule task"（不用宽松 [FaultDetect]/health check，避免被
        # "is disabled, now stop the previous checker" 关停日志误判为 yes）。
        if [[ "$has_schedule" == "no" ]] && \
            grep -qE "\[CircuitBreaker\] schedule task" "$sdk_cb_log" 2>/dev/null; then
            has_schedule="yes"
        fi
        # INSTANCE 级状态切换：日志含 level=INSTANCE 的 half-open->close（b 实例恢复）
        if [[ "$has_inst_statechg" == "no" ]] && \
            grep -aiE "status change.*half-open -> close" "$sdk_cb_log" 2>/dev/null | grep -qi "INSTANCE"; then
            has_inst_statechg="yes"
        fi
        [[ "$has_schedule" == "yes" && "$has_inst_statechg" == "yes" ]] && break
        sleep 0.5
    done
    log_info "${step_prefix}${case_tag} [6] 日志佐证：探测调度=${has_schedule}, INSTANCE状态切换=${has_inst_statechg} (来源 ${sdk_cb_log})"

    # 判定（INSTANCE 级专属指标；维持/探活 ok 容忍 1 次抖动用 >= N-1）
    local ok_threshold=$((FD_VERIFY_COUNT - 1))
    print_block "${step_prefix}${case_tag} 判定指标" \
        "触发 b OPEN : fail=${trigger_fail} (期望 >=1，b 被选中时 500)" \
        "维持 OPEN   : ok=${keep_ok}/${FD_VERIFY_COUNT} (期望 >=${ok_threshold}，b 被摘除业务落 a)" \
        "探活 CLOSE  : ok=${recover_ok}/${FD_VERIFY_COUNT} (期望 >=${ok_threshold}，b 恢复可选)" \
        "日志佐证    : 探测调度=${has_schedule}, INSTANCE状态切换=${has_inst_statechg} (期望均为 yes)"

    if [[ "$trigger_fail" -ge 1 ]] && [[ "$keep_ok" -ge "$ok_threshold" ]] \
        && [[ "$recover_ok" -ge "$ok_threshold" ]] \
        && [[ "$has_schedule" == "yes" ]] && [[ "$has_inst_statechg" == "yes" ]]; then
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
        "被调服务  : SERVICE=${FD_SVC_CALLEE} / METHOD=${FD_METHOD_CALLEE} / INSTANCE=${FD_INSTANCE_CALLEE}（各级独立，provider 同时注册）" \
        "探测端点  : HTTP GET /echo (随 provider 故障开关变化)" \
        "DEBUG     : ${DEBUG_MODE}"

    # 启动 provider 集群
    log_step "启动 Provider 集群"
    start_provider "fd_provider_a" "$PROVIDER_A_DIR" "$PROVIDER_A_PORT" "${LOG_DIR}/fd_provider_a.log" \
        && PROVIDER_A_PID="$_STARTED_PID" || { log_error "provider-a 启动失败"; exit 1; }
    start_provider "fd_provider_b" "$PROVIDER_B_DIR" "$PROVIDER_B_PORT" "${LOG_DIR}/fd_provider_b.log" \
        && PROVIDER_B_PID="$_STARTED_PID" || { log_error "provider-b 启动失败"; exit 1; }

    # 依次运行三个级别的主动探测用例（SERVICE / METHOD / INSTANCE）。必须在主 shell 直接调用：
    # 各 case 把结果写入全局变量 _CASE_RESULT，且函数内对 CREATED_*_RULE_IDS / CONSUMER_PIDS
    # 的 append 也依赖主 shell 上下文；若用 result=$(case_xxx) 命令替换，函数体会在子 shell 执行，
    # _CASE_RESULT 与各全局数组的修改都不回传主 shell，导致结果恒为空且 trap cleanup 拿不到规则/进程。
    # RUN_FD_CASES 控制选跑子集（逗号分隔 service/method/instance/tcp/udp/proto_method），未选中的用例标记为 SKIP。
    local svc_result="SKIP" method_result="SKIP" instance_result="SKIP" tcp_result="SKIP" udp_result="SKIP" proto_method_result="SKIP"
    if [[ ",${RUN_FD_CASES}," == *",service,"* ]]; then
        case_fault_detect;          svc_result="$_CASE_RESULT"
    fi
    if [[ ",${RUN_FD_CASES}," == *",method,"* ]]; then
        case_fault_detect_method;   method_result="$_CASE_RESULT"
    fi
    if [[ ",${RUN_FD_CASES}," == *",instance,"* ]]; then
        case_fault_detect_instance; instance_result="$_CASE_RESULT"
    fi
    if [[ ",${RUN_FD_CASES}," == *",tcp,"* ]]; then
        case_fault_detect_tcp;      tcp_result="$_CASE_RESULT"
    fi
    if [[ ",${RUN_FD_CASES}," == *",udp,"* ]]; then
        case_fault_detect_udp;      udp_result="$_CASE_RESULT"
    fi
    if [[ ",${RUN_FD_CASES}," == *",proto_method,"* ]]; then
        case_fault_detect_proto_method; proto_method_result="$_CASE_RESULT"
    fi

    log_step "验证结果"
    print_block "结果汇总（主动探测闭环 OPEN -> 探测维持 -> 探活 -> CLOSE）" \
        "服务级(SERVICE)  : ${svc_result}" \
        "接口级(METHOD)   : ${method_result}" \
        "实例级(INSTANCE) : ${instance_result}" \
        "TCP 探测         : ${tcp_result}" \
        "UDP 探测         : ${udp_result}" \
        "协议方法         : ${proto_method_result}"

    # 任一被选中执行的用例 FAIL 即整体失败；SKIP 不计入失败。
    local overall="PASS" r
    for r in "$svc_result" "$method_result" "$instance_result" "$tcp_result" "$udp_result" "$proto_method_result"; do
        [[ "$r" == "FAIL" ]] && overall="FAIL"
    done

    local summary="service=${svc_result} method=${method_result} instance=${instance_result} tcp=${tcp_result} udp=${udp_result} proto_method=${proto_method_result}"
    if [[ "$overall" == "PASS" ]]; then
        log_info "主动探测验证通过 ✅（${summary}）"
        exit 0
    else
        log_error "主动探测验证失败 ❌（${summary}；详见日志 ${TEST_LOG_FILE} 与 ${LOG_DIR}/）"
        exit 1
    fi
}

main "$@"
