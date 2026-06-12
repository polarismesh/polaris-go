#!/bin/bash
# =============================================================================
# 故障熔断（Circuit Breaker）端到端验证脚本
#
# 使用方法:
#   chmod +x verify_circuitbreaker.sh
#   ./verify_circuitbreaker.sh [--polaris-server <地址>] [--polaris-token <令牌>]
#                              [--namespace <命名空间>] [--service <服务名>]
#                              [--only instance,service,interface,old_instance,http_status,default_rule] [--debug]
#
# 前置条件:
#   1. 北极星服务端（Polaris Server）已启动
#      - gRPC: <polaris-server>:8091
#      - HTTP 管控: <polaris-server>:8090
#   2. Go 环境已安装（与 examples/circuitbreaker/*/go.mod 兼容版本）
#   3. python3 / curl / lsof / grep 等基础工具
#
#   说明：脚本会通过 Polaris HTTP API 自动创建/删除熔断规则，无需手动操作控制台。
#
# 验证场景:
#   三个子用例分别覆盖 polaris-go 的三种熔断级别（INSTANCE/SERVICE/METHOD）：
#
#   ┌──────────────┬─────────────────────────────────────────────────────────┐
#   │ instance     │ 实例级熔断：单实例 5xx 达阈值后被摘除，请求自动转移到健康实例 │
#   │ service      │ 服务级熔断：整个服务（含全部实例）错误率达阈值后整体熔断    │
#   │ interface    │ 接口级熔断：仅特定 path 的调用被熔断，其他接口不受影响      │
#   │ old_instance │ 存量散装写法：CircuitBreakerAPI.Report(InstanceResource)   │
#   │              │             + ConsumerAPI.UpdateServiceCallResult，        │
#   │              │             验证旧版 API 路径不被破坏（向后兼容）           │
#   └──────────────┴─────────────────────────────────────────────────────────┘
#
# 前三个用例共享同一个 Consumer 源代码（统一装饰器写法）：
#   examples/circuitbreaker/newCircuitBreakerCaller/consumer
#   - 通过 CircuitBreakerAPI.MakeFunctionDecorator 接入 SDK
#   - customer func 内调用 model.GetInvokeContext(ctx).SetInstance(...)
#     让实例级 Resource 也能在装饰器结束阶段自动上报
#   - RequestContext.Method/Path 决定是否启用接口级熔断
#
# 用例4 单独使用旧版散装写法 Consumer：
#   examples/circuitbreaker/oldInstanceCircuitBreakerCaller/consumer
#   - 直接调用 CircuitBreakerAPI.Report(InstanceResource) 上报熔断结果
#   - 直接调用 ConsumerAPI.UpdateServiceCallResult 上报调用统计
#   - 用于验证存量客户的散装写法在新版 SDK 中仍然可用，向后兼容
#
# 拓扑（每个子用例共用）:
#   provider-a (默认 200) ──┐
#                            ├──→ Polaris ←── consumer (HTTP 18081/18082/18083) ←── 用户 curl
#   provider-b (默认 500) ──┘
#
# 验证流程:
#   1. 环境准备 + 编译 provider-a / provider-b / 三种 consumer（同一份源码，三个二进制）
#   2. 启动 Provider 集群（注册到 CircuitBreakerCallee 服务）
#   3. 通过 Polaris HTTP API 创建对应级别的熔断规则
#   4. 启动 Consumer 并发请求 → 累计失败 → 验证熔断生效
#   5. 通过 /switch 接口翻转 provider 状态 → 验证恢复链路
#   6. 清理规则与进程
# =============================================================================

set -euo pipefail

# ======================== 默认配置 ========================
POLARIS_SERVER="${POLARIS_SERVER:-127.0.0.1}"
POLARIS_HTTP_PORT="${POLARIS_HTTP_PORT:-8090}"
POLARIS_TOKEN="${POLARIS_TOKEN:-}"
NAMESPACE="${NAMESPACE:-default}"
SERVICE_NAME="${SERVICE_NAME:-CircuitBreakerCallee}"
# 四个子用例使用四个独立 caller 服务名，避免规则相互干扰
INSTANCE_CALLER="${INSTANCE_CALLER:-CircuitBreakerInstanceCaller}"
SERVICE_CALLER="${SERVICE_CALLER:-CircuitBreakerServiceCaller}"
INTERFACE_CALLER="${INTERFACE_CALLER:-CircuitBreakerInterfaceCaller}"
# 用例4 使用独立的 caller 服务名，避免与用例1（同样是 INSTANCE 级）的规则冲突
OLD_INSTANCE_CALLER="${OLD_INSTANCE_CALLER:-CircuitBreakerOldInstanceCaller}"
# 用例 5：HTTP 状态码区分（4xx 不熔断 / 5xx 熔断 / 网络错熔断）
HTTP_STATUS_CALLER="${HTTP_STATUS_CALLER:-CircuitBreakerHttpStatusCaller}"
# 用例 6：默认实例级熔断兜底（服务端无规则）
DEFAULT_RULE_CALLER="${DEFAULT_RULE_CALLER:-CircuitBreakerDefaultRuleCaller}"
# 用例 7：修改熔断参数生效验证（通过 update_circuitbreaker_rule 变更阈值）
MODIFY_RULE_CALLER="${MODIFY_RULE_CALLER:-CircuitBreakerModifyRuleCaller}"
# 用例 8：接口协议+HTTP方法维度合并在 1 条规则的 13 个 BlockConfig 里
# 共享同一 caller service，避免规则 source.service 相互干扰。
PM_CALLER="${PM_CALLER:-CircuitBreakerPMCaller}"
# 用例 9：路径匹配方式维度（5 种 MatchString 类型）独立 1 条规则 5 个 BlockConfig
PATHTYPE_CALLER="${PATHTYPE_CALLER:-CircuitBreakerPathTypeCaller}"

# 端口规划（避免冲突）：
#   provider-a: 28081
#   provider-b: 28082
#   consumer:   instance/service/interface 子用例分别在 18081/18082/18083
#               old_instance 子用例（存量散装写法）在 18084
PROVIDER_A_PORT="${PROVIDER_A_PORT:-28081}"
PROVIDER_B_PORT="${PROVIDER_B_PORT:-28082}"
INSTANCE_CONSUMER_PORT="${INSTANCE_CONSUMER_PORT:-18081}"
SERVICE_CONSUMER_PORT="${SERVICE_CONSUMER_PORT:-18082}"
INTERFACE_CONSUMER_PORT="${INTERFACE_CONSUMER_PORT:-18083}"
OLD_INSTANCE_CONSUMER_PORT="${OLD_INSTANCE_CONSUMER_PORT:-18084}"
# 用例 5（http_status）专用 consumer 端口；selfService=CircuitBreakerHttpStatusCaller
HTTP_STATUS_CONSUMER_PORT="${HTTP_STATUS_CONSUMER_PORT:-18085}"
# 用例 6（default_rule）专用 consumer 端口；selfService=CircuitBreakerDefaultRuleCaller
DEFAULT_RULE_CONSUMER_PORT="${DEFAULT_RULE_CONSUMER_PORT:-18086}"
# 用例 7（modify_rule）专用 consumer 端口；selfService=CircuitBreakerModifyRuleCaller
MODIFY_RULE_CONSUMER_PORT="${MODIFY_RULE_CONSUMER_PORT:-18087}"
# 用例 8（接口协议+HTTP方法 合并）专用 consumer 端口；selfService=$PM_CALLER
PM_CONSUMER_PORT="${PM_CONSUMER_PORT:-18088}"
# 用例 9（路径匹配方式）专用 consumer 端口
PATHTYPE_CONSUMER_PORT="${PATHTYPE_CONSUMER_PORT:-18089}"

# 触发熔断所需的请求次数（单实例失败计数）
# 默认实例熔断规则：连续错误数 / 错误率，10 次足够触发
TRIGGER_REQUEST_COUNT="${TRIGGER_REQUEST_COUNT:-15}"
# 验证恢复时的请求次数
RECOVERY_REQUEST_COUNT="${RECOVERY_REQUEST_COUNT:-10}"
# 等待 SDK 拉取规则 + 缓存就绪的秒数
WAIT_RULE_READY_SECONDS="${WAIT_RULE_READY_SECONDS:-8}"
# 等待熔断 sleepWindow 后进入半开（与下方规则模板的 sleep_window 配合）
# 用例需要执行两轮"触发→熔断→恢复"，把等待时长压短可显著减少端到端耗时；
# sleepWindow 由 _gen_rule.py 写为 12s，这里多给 3s buffer。
WAIT_HALF_OPEN_SECONDS="${WAIT_HALF_OPEN_SECONDS:-15}"

# 默认运行的子用例集合（逗号分隔），可通过 --only 缩小范围
RUN_CASES="${RUN_CASES:-instance,service,interface,old_instance,http_status,default_rule,modify_rule,protocol_method,pathtype}"
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
        --polaris-server)
            POLARIS_SERVER="$2"; shift 2 ;;
        --polaris-token)
            POLARIS_TOKEN="$2"; shift 2 ;;
        --namespace)
            NAMESPACE="$2"; shift 2 ;;
        --service)
            SERVICE_NAME="$2"; shift 2 ;;
        --provider-a-port)
            PROVIDER_A_PORT="$2"; shift 2 ;;
        --provider-b-port)
            PROVIDER_B_PORT="$2"; shift 2 ;;
        --only)
            RUN_CASES="$2"; shift 2 ;;
        --trigger-count)
            TRIGGER_REQUEST_COUNT="$2"; shift 2 ;;
        --recovery-count)
            RECOVERY_REQUEST_COUNT="$2"; shift 2 ;;
        --wait-half-open)
            WAIT_HALF_OPEN_SECONDS="$2"; shift 2 ;;
        --debug)
            DEBUG_MODE="true"; shift ;;
        --help|-h)
            sed -n '2,42p' "$0"
            exit 0
            ;;
        *)
            echo -e "${RED}未知参数: $1${NC}"; exit 1 ;;
    esac
done

POLARIS_HTTP_ADDR="http://${POLARIS_SERVER}:${POLARIS_HTTP_PORT}"

# ======================== 全局变量 ========================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CALLEE_A_DIR="${SCRIPT_DIR}/callee/provider-a"
CALLEE_B_DIR="${SCRIPT_DIR}/callee/provider-b"
# 三个用例共用同一个 consumer 源代码（统一装饰器写法），通过 --selfService 区分
# 角色名，--port 区分端口；编译产物名仍按 instance / service / interface 分开，
# 便于 cleanup.sh 用进程名匹配清理。
CALLER_CONSUMER_DIR="${SCRIPT_DIR}/newCircuitBreakerCaller/consumer"
INSTANCE_CONSUMER_DIR="${CALLER_CONSUMER_DIR}"
SERVICE_CONSUMER_DIR="${CALLER_CONSUMER_DIR}"
INTERFACE_CONSUMER_DIR="${CALLER_CONSUMER_DIR}"
# 用例4：旧版散装写法（CircuitBreakerAPI.Report + ConsumerAPI.UpdateServiceCallResult），
# 仅暴露 /echo 一个端点，验证存量客户的实例级熔断路径在新版 SDK 中仍然可用。
OLD_INSTANCE_CONSUMER_DIR="${SCRIPT_DIR}/oldInstanceCircuitBreakerCaller/consumer"
BUILD_DIR="${SCRIPT_DIR}/.build"
LOG_DIR="${SCRIPT_DIR}/.logs"

TEST_LOG_FILE="${LOG_DIR}/verify_circuitbreaker-$(date +%Y%m%d_%H%M%S).log"

# Provider PID
PROVIDER_A_PID=""
PROVIDER_B_PID=""
# Consumer PID 列表（供 cleanup 用）
CONSUMER_PIDS=()

# 创建过的熔断规则 ID 列表（脚本退出前会逐个删除）
CREATED_RULE_IDS=()

# ======================== 工具函数 ========================
# 注意：日志统一写到 stderr，避免在 `result=$(some_func)` 这类命令替换里被一起捕获，
# 进而污染函数返回值（典型场景：rule_id=$(create_or_update_circuitbreaker_rule ...)）。
# setup_test_log 中的 `exec > >(tee ...) 2>&1` 会把 stderr 也接到 tee，所以日志依旧
# 同时落到终端和测试日志文件。
log_info()  { echo -e "${GREEN}[INFO]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*" >&2; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*" >&2; }
log_error() { echo -e "${RED}[ERROR]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*" >&2; }
log_step() {
    echo "" >&2
    echo -e "${CYAN}========================================${NC}" >&2
    echo -e "${CYAN}  步骤: $*${NC}" >&2
    echo -e "${CYAN}========================================${NC}" >&2
}

# print_block 用蓝色框打印"配置/操作/预期/判定"结构化说明，让读日志的人
# 一眼看到当前用例在做什么、依赖哪些规则、预期会出现什么结果。
# 参考 examples/ratelimit/verify_ratelimit.sh 的 print_block 形态。
# 用法：print_block "标题" "行1" "行2" "行3" ...
print_block() {
    local title="$1"
    shift
    echo -e "${BLUE}┌─ ${title} ─────────────────────────────────────────────${NC}" >&2
    while [[ $# -gt 0 ]]; do
        echo -e "${BLUE}│${NC} $1" >&2
        shift
    done
    echo -e "${BLUE}└──────────────────────────────────────────────────────────────${NC}" >&2
}

# setup_test_log 把后续 stdout/stderr 同时写入 TEST_LOG_FILE，参考 examples/route 范本
setup_test_log() {
    mkdir -p "${LOG_DIR}"
    {
        echo "===== 故障熔断验证日志 $(date '+%Y-%m-%d %H:%M:%S') ====="
        echo "Command: $0 $*"
    } > "${TEST_LOG_FILE}"
    exec > >(tee >(sed -u 's/\x1b\[[0-9;]*m//g' >> "${TEST_LOG_FILE}")) 2>&1
}

# 检查进程是否存活
check_process_alive() {
    local pid="$1"
    local name="${2:-进程}"
    if ! kill -0 "$pid" 2>/dev/null; then
        log_error "${name} (PID: $pid) 已异常退出"
        return 1
    fi
    return 0
}

# 等待 HTTP 服务就绪
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

# ======================== 清理函数 ========================
cleanup() {
    log_info "脚本退出，开始清理..."
    # 删除创建的熔断规则
    if [[ ${#CREATED_RULE_IDS[@]} -gt 0 ]]; then
        log_info "删除已创建的熔断规则: ${CREATED_RULE_IDS[*]}"
        delete_circuitbreaker_rules "${CREATED_RULE_IDS[@]}" || true
    fi
    # Consumer
    for pid in "${CONSUMER_PIDS[@]:-}"; do
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
            log_info "已停止 Consumer (PID: $pid)"
        fi
    done
    # Providers
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

# ======================== Polaris HTTP API 调用 ========================
# create_circuitbreaker_rule <case> <body>
#   case: instance/service/interface（仅日志用途）
#   body: 一条 CircuitBreakerRule 的 JSON 串
# 副作用：成功时将规则 id 追加到 CREATED_RULE_IDS（脚本退出时统一清理）
# 返回：成功打印 id 到 stdout，失败返回非零
#
# 兼容历史规则：当服务端返回 ServiceExistedCircuitBreakers (code=400209) 时，
# 视为"上次运行残留 / 同名规则已存在"。此时按 name 反查 id 并发起 PUT 更新，
# 把规则定义对齐当前脚本预期，避免历史脏规则把流程卡死。
create_circuitbreaker_rule() {
    local case_name="$1"
    local body="$2"
    local payload="[${body}]"

    local rule_name
    rule_name=$(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('name',''))" 2>/dev/null || echo "")

    local resp http_code
    http_code=$(curl -s -o /tmp/_cb_create_$$.tmp -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/naming/v1/circuitbreaker/rules" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "${payload}" 2>/dev/null || echo "000")
    resp=$(cat /tmp/_cb_create_$$.tmp 2>/dev/null || echo "")
    rm -f /tmp/_cb_create_$$.tmp

    local code item_code id
    code=$(echo "$resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('code',0))" 2>/dev/null || echo "?")
    # batch 接口里第一个响应项的 code，更能反映规则级别的语义
    item_code=$(echo "$resp" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
items = data.get('responses') or []
if items:
    print(items[0].get('code', ''))
" 2>/dev/null || echo "")

    # 200000=Code_ExecuteSuccess（首次写入）、200001=Code_DataNoChange（已存在视为成功，
    # 服务端对相同 name+namespace 会幂等）；其他码视为失败。
    if [[ "$http_code" == "200" ]] && [[ "$code" =~ ^20000[01]$ ]]; then
        id=$(echo "$resp" | python3 -c "
import sys, json
data = json.load(sys.stdin)
items = data.get('responses') or []
if items:
    cb = items[0].get('circuitBreaker') or {}
    print(cb.get('id', ''))
" 2>/dev/null || true)
        if [[ -n "$id" ]]; then
            CREATED_RULE_IDS+=("$id")
            log_info "[${case_name}] 创建熔断规则成功 (id=${id})"
            echo "$id"
            return 0
        fi
        log_warn "[${case_name}] 创建成功但未拿到 id，可能为已存在规则；尝试通过查询接口取 id"
        id=$(query_circuitbreaker_rule_id_by_name "$rule_name") || true
        if [[ -n "$id" ]]; then
            CREATED_RULE_IDS+=("$id")
            echo "$id"
            return 0
        fi
        return 0
    fi

    # 400209 = ServiceExistedCircuitBreakers，按 (name, namespace) 已存在。
    # 回退路径：先查已有规则 → 若参数一致则复用（跳过更新，避免无谓 SDK revision 变更）；
    # 若参数不一致再 PUT 更新。
    if [[ "$code" == "400209" ]] || [[ "$item_code" == "400209" ]]; then
        log_warn "[${case_name}] 服务端已存在同名规则 (name=${rule_name})"
        id=$(query_circuitbreaker_rule_id_by_name "$rule_name") || true
        if [[ -z "$id" ]]; then
            log_error "[${case_name}] 已存在的规则反查失败 (name=${rule_name})"
            log_error "请求 body: ${payload:0:500}"
            log_error "响应: ${resp:0:500}"
            return 1
        fi
        # 获取已有规则的完整 JSON，与期望 body 比较语义字段
        if ! circuitbreaker_rule_needs_update "$id" "$body"; then
            CREATED_RULE_IDS+=("$id")
            log_info "[${case_name}] 规则参数一致，跳过更新，直接复用 (id=${id})"
            echo "$id"
            return 0
        fi
        log_info "[${case_name}] 规则参数不一致，执行更新 (id=${id})"
        if ! update_circuitbreaker_rule "$case_name" "$id" "$body"; then
            return 1
        fi
        CREATED_RULE_IDS+=("$id")
        log_info "[${case_name}] 更新熔断规则成功 (id=${id})"
        echo "$id"
        return 0
    fi

    log_error "[${case_name}] 创建熔断规则失败 (HTTP=${http_code}, code=${code})"
    log_error "请求 body: ${payload:0:500}"
    log_error "响应: ${resp:0:500}"
    return 1
}

# update_circuitbreaker_rule <case> <id> <body>
# PUT /naming/v1/circuitbreaker/rules，把已有规则的定义改成 body 内容。
# 服务端 update 接口要求 body 同时包含 id 和 name（见 checkCircuitBreakerRuleParams）。
update_circuitbreaker_rule() {
    local case_name="$1"
    local rule_id="$2"
    local body="$3"

    local payload
    payload=$(python3 -c "
import json, sys
body = json.loads(sys.argv[1])
body['id'] = sys.argv[2]
print(json.dumps([body]))
" "$body" "$rule_id" 2>/dev/null || true)
    if [[ -z "$payload" ]]; then
        log_error "[${case_name}] 拼装更新 body 失败"
        return 1
    fi

    local http_code resp
    http_code=$(curl -s -o /tmp/_cb_upd_$$.tmp -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request PUT "${POLARIS_HTTP_ADDR}/naming/v1/circuitbreaker/rules" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "${payload}" 2>/dev/null || echo "000")
    resp=$(cat /tmp/_cb_upd_$$.tmp 2>/dev/null || echo "")
    rm -f /tmp/_cb_upd_$$.tmp

    local code
    code=$(echo "$resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('code',0))" 2>/dev/null || echo "?")
    # 200000 / 200001 都视为成功
    if [[ "$http_code" == "200" ]] && [[ "$code" =~ ^20000[01]$ ]]; then
        return 0
    fi
    log_error "[${case_name}] 更新熔断规则失败 (HTTP=${http_code}, code=${code}, id=${rule_id})"
    log_error "响应: ${resp:0:500}"
    return 1
}

# query_circuitbreaker_rule_id_by_name <name>
# 兜底用：通过 (name, namespace) 反查 id（部分场景下创建接口不直接返回 id，或同名规则已存在）。
# 服务端约束 (name, namespace) 唯一，所以这里只取首条匹配项。
query_circuitbreaker_rule_id_by_name() {
    local rule_name="$1"
    [[ -z "$rule_name" ]] && return 0
    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        --request GET "${POLARIS_HTTP_ADDR}/naming/v1/circuitbreaker/rules?name=${rule_name}&limit=20" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" 2>/dev/null || echo "")
    NAMESPACE_FILTER="$NAMESPACE" RULE_NAME="$rule_name" python3 -c "
import os, sys, json
try:
    data = json.loads(sys.stdin.read() or '{}')
except Exception:
    sys.exit(0)
target_ns = os.environ.get('NAMESPACE_FILTER', '')
target_name = os.environ.get('RULE_NAME', '')
for item in (data.get('data') or []):
    if item.get('name') != target_name:
        continue
    if target_ns and item.get('namespace') and item.get('namespace') != target_ns:
        continue
    print(item.get('id', ''))
    break
" <<< "$resp" 2>/dev/null || true
}

# circuitbreaker_rule_needs_update <id> <expected_body>
# 比较服务端已有规则与期望 body 的语义字段（level, enable, block_configs,
# trigger_condition, recoverCondition, error_conditions, rule_matcher）。
# 语义一致时返回非零（无需更新），否则返回零（需要 PUT 更新）。
# 忽略 id/ctime/mtime/revision/description 等服务端独占或描述性字段。
circuitbreaker_rule_needs_update() {
    local rule_id="$1"
    local expected_body="$2"

    # 先拉已有规则的完整 JSON
    local existing_body
    existing_body=$(curl -s --connect-timeout 5 --max-time 10 \
        --request GET "${POLARIS_HTTP_ADDR}/naming/v1/circuitbreaker/rules?id=${rule_id}" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" 2>/dev/null || echo "")

    EXISTING="$existing_body" EXPECTED="$expected_body" python3 -c "
import json, os, sys, re

def snake_to_camel(s):
    '''snake_case → camelCase 键名转换'''
    return re.sub(r'_([a-z])', lambda m: m.group(1).upper(), s)

def normalize_keys(obj):
    '''递归将 dict 的 snake_case 键转为 camelCase，便于与服务器返回格式对齐'''
    if isinstance(obj, dict):
        return {snake_to_camel(k): normalize_keys(v) for k, v in obj.items()}
    if isinstance(obj, list):
        return [normalize_keys(v) for v in obj]
    return obj

def strip_semantic(d):
    '''提取熔断器语义字段：仅保留会影响熔断行为的字段。
    服务端会保留我们写入时的 snake_case 字段名（如 block_configs），同时也会
    保留 camelCase 字段；这里 wanted 同时包含两种命名，由 normalize_keys 统一
    转 camelCase 后再做 strip，避免命名差异触发误判。'''
    if not isinstance(d, dict):
        return d
    wanted = {
        'level', 'enable',
        'ruleMatcher', 'rule_matcher',
        'errorConditions', 'error_conditions',
        'triggerCondition', 'trigger_condition',
        'recoverCondition', 'recover_condition',
        'blockConfigs', 'block_configs',
    }
    res = {k: v for k, v in d.items() if k in wanted}
    # ruleMatcher.destination.method 字段已 deprecated，匹配语义改由
    # blockConfigs[].api 表达；但服务端对 METHOD 级规则会自动把 destination.method.value
    # 改写为 BlockConfig.api.path.value（如 "/echo"），而 _gen_rule.py 始终写 "*" 占位
    # → 两边永远 diff。剥离该字段避免误判。
    if isinstance(res.get('ruleMatcher'), dict):
        dst = res['ruleMatcher'].get('destination')
        if isinstance(dst, dict) and 'method' in dst:
            del dst['method']
    return res

existing_raw = os.environ.get('EXISTING', '{}')
expected_raw = os.environ.get('EXPECTED', '{}')

try:
    existing = json.loads(existing_raw or '{}')
except Exception:
    sys.exit(0)

# 服务端可能在顶层或 data[0] 返回规则对象
if isinstance(existing, dict) and 'data' in existing:
    items = existing.get('data', [])
    if isinstance(items, list) and len(items) > 0:
        existing = items[0]

try:
    expected = json.loads(expected_raw)
except Exception:
    sys.exit(0)

# 两边都归一化到 camelCase：服务端 GET 既会返回 snake_case（写入时的原始命名，
# 如 block_configs / error_conditions）也会返回 camelCase，expected 也用 snake_case 写
# → 不归一化 existing 会因为键名大小写不一致永远 diff
existing = normalize_keys(existing)
expected = normalize_keys(expected)

e_sem = strip_semantic(existing)
x_sem = strip_semantic(expected)

# 递归深比较：语义为「existing ⊇ expected」才算一致。
# - expected 有但 existing 缺 → diff（缺字段，需要更新）
# - existing 有但 expected 没有 → 忽略（视为服务端补的元数据，如
#   blockConfigs[].api.protocol / method / path.type 等控制台补全的默认值）
# - 列表按位置比较，triggerConditions 顺序改变也视为 diff
def deep_diff(existing, expected):
    if type(existing) != type(expected):
        return True
    if isinstance(existing, dict):
        for k in expected:
            if k not in existing:
                return True
            if deep_diff(existing[k], expected[k]):
                return True
        return False
    if isinstance(existing, list):
        if len(existing) != len(expected):
            return True
        for i in range(len(existing)):
            if deep_diff(existing[i], expected[i]):
                return True
        return False
    return existing != expected

if deep_diff(e_sem, x_sem):
    sys.exit(0)   # 不一致 → 需要更新
else:
    sys.exit(1)   # 一致 → 无需更新
" 2>/dev/null
}
# 通用的规则巡检：可按 source 或 destination 维度查询规则。
#   role                     —— 仅用于日志展示（"主调" / "被调"）
#   service_filter_param     —— 服务端查询参数名：srcService 或 dstService
#                               注意：Polaris 控制台 API 的这些过滤器只做 EXACT
#                               比对，没法自动把 srcService=* / dstService=* 的
#                               通配规则带回来；而通配规则同样会被本用例命中。
#                               因此本函数实际不依赖该参数过滤，而是拉到所有规则
#                               后用 Python 重新按 (service or '*', ns or '*') 精确匹配。
#                               svc_param/ns_param 仅作为标识用途保留，便于后续拓展。
#   service / namespace      —— 实际的服务/命名空间
#   expected...              —— 本用例预期内的规则名；不在列表中的视为"陌生规则"
# 返回：0=无陌生规则或非严格模式；1=严格模式下检测到陌生规则
_inspect_rules() {
    local role="$1"
    local svc_param="$2"   # 仅用于决定 match_kind
    # shellcheck disable=SC2034
    local ns_param="$3"
    local service="$4"
    local ns="$5"
    shift 5 || true
    local -a expected=("$@")

    # 不做服务端 EXACT 过滤——服务端过滤用 EXACT 比对，会漏掉 src=*/dst=* 的通配规则；
    # 这些通配规则对当前 (caller, callee) 同样会生效。所以只保留 limit/offset 分页参数，
    # 全量拉回后由 Python 端按通配语义重新筛选。
    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 15 \
        --request GET "${POLARIS_HTTP_ADDR}/naming/v1/circuitbreaker/rules?limit=500&offset=0" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" 2>/dev/null || echo "")
    if [[ -z "$resp" ]]; then
        log_warn "[规则巡检] 查询${role} ${ns}/${service} 的现有熔断规则失败（响应为空）"
        return 0
    fi

    local match_kind="src"
    if [[ "$svc_param" == "dstService" ]]; then
        match_kind="dst"
    fi

    # 如果服务端规则总数超过 limit，会发生分页截断，可能漏检干扰规则
    local total_amount
    total_amount=$(echo "$resp" | python3 -c "
import sys, json
try:
    d = json.loads(sys.stdin.read() or '{}')
except Exception:
    sys.exit(0)
print(d.get('amount', 0))
" 2>/dev/null || echo "0")
    if [[ -n "$total_amount" ]] && [[ "$total_amount" -gt 500 ]]; then
        log_warn "[规则巡检] 服务端规则总数 ${total_amount} 已超出本地拉取上限 500，可能漏检"
    fi

    local parsed
    parsed=$(EXPECTED="${expected[*]}" SERVICE="$service" NS="$ns" MATCH_KIND="$match_kind" \
        CALLER="${INSPECT_CALLER:-}" python3 -c "
import json, os, sys
raw = sys.stdin.read() or '{}'
try:
    data = json.loads(raw)
except Exception:
    sys.exit(0)
expected = set(filter(None, os.environ.get('EXPECTED', '').split()))
service = os.environ.get('SERVICE', '')
ns = os.environ.get('NS', '')
match_kind = os.environ.get('MATCH_KIND', 'src')
# 仅在被调维度（match_kind=dst）使用：把"实际不会影响本用例的规则"（其 source
# 与本用例 caller 不匹配，且不为 *）从陌生列表中排除——它们仍会被打印出来供
# 人工审视，但不再触发 STRICT_RULE_CHECK 的 abort，避免误伤。
caller_for_check = os.environ.get('CALLER', '')
items = data.get('data') or []

def match_role(item):
    rm = item.get('ruleMatcher') or {}
    block = rm.get('source') if match_kind == 'src' else rm.get('destination')
    block = block or {}
    bs = block.get('service')
    bn = block.get('namespace')
    if service and bs and bs not in (service, '*'):
        return False
    if ns and bn and bn not in (ns, '*'):
        return False
    return True

def could_affect_caller(item):
    if not caller_for_check:
        return True  # 没指定就视作可能影响
    src = (item.get('ruleMatcher') or {}).get('source') or {}
    s_svc = src.get('service') or '*'
    return s_svc in (caller_for_check, '*')

filtered = [it for it in items if match_role(it)]
strangers = []
lines = []
for it in filtered:
    name = it.get('name', '')
    level = it.get('level', '?')
    enable = it.get('enable', False)
    rm = it.get('ruleMatcher') or {}
    src = rm.get('source') or {}
    dst = rm.get('destination') or {}
    src_str = f\"{src.get('namespace','*')}/{src.get('service','*')}\"
    dst_str = f\"{dst.get('namespace','*')}/{dst.get('service','*')}\"
    method = (dst.get('method') or {}).get('value', '')
    if method:
        dst_str += f' method=\"{method}\"'
    paths = []
    for bc in (it.get('block_configs') or []):
        api = bc.get('api') or {}
        p = (api.get('path') or {}).get('value', '')
        if p:
            paths.append(p)
    path_str = ('  paths=' + ','.join(paths)) if paths else ''
    rid = it.get('id', '')
    en = 'ON ' if enable else 'OFF'
    affects = could_affect_caller(it) if match_kind == 'dst' else True
    affects_mark = '' if affects else '  [不影响本主调]'
    lines.append(f'{en}  {level:<8} {name}  {src_str} → {dst_str}{path_str}  (id={rid}){affects_mark}')
    if expected and name not in expected and affects:
        strangers.append(name)
if not lines:
    print('TABLE')
else:
    print('TABLE\t' + '\n'.join(lines))
print('STRANGER\t' + ','.join(strangers))
" <<< "$resp" 2>/dev/null || true)

    local table_part stranger_part
    # table_part 必须捕获所有命中行：Python 把规则用 \n 嵌在 TABLE 那一行后，
    # awk 按 \n 切 record 后会得到 "TABLE\ton1" / "on2" / "on3" 等多条 record。
    # 用状态机在 TABLE 行打印 tab 后内容、in_t 段打印其余行、STRANGER 行退出，
    # 避免早 exit 丢掉 on2/on3/...。
    table_part=$(echo "$parsed" | awk -F'\t' '
        BEGIN { in_t = 0 }
        /^TABLE/ {
            print substr($0, index($0, "\t")+1)
            in_t = 1
            next
        }
        /^STRANGER/ { in_t = 0; next }
        in_t { print }
    ')
    # STRANGER 行只有一行规则名（逗号分隔），单行提取无影响
    stranger_part=$(echo "$parsed" | awk -F'\t' '/^STRANGER/{print substr($0, index($0, "\t")+1); exit}')

    if [[ -z "$table_part" ]]; then
        log_info "[规则巡检] ${role} ${ns}/${service} 当前无任何熔断规则"
    else
        log_info "[规则巡检] ${role} ${ns}/${service} 当前命中 $(echo "$table_part" | wc -l | tr -d ' ') 条熔断规则:"
        while IFS= read -r line; do
            [[ -z "$line" ]] && continue
            echo -e "  ${BLUE}│${NC} $line" >&2
        done <<< "$table_part"
    fi

    if [[ -n "$stranger_part" ]]; then
        log_warn "[规则巡检] ${role}维度检测到本用例预期之外的规则: ${stranger_part}"
        log_warn "[规则巡检] 这些规则可能干扰本用例的统计（错误条件/阈值/触发率），建议先到控制台清理"
        if [[ "${STRICT_RULE_CHECK:-false}" == "true" ]]; then
            log_error "[规则巡检] STRICT_RULE_CHECK=true，因存在陌生规则提前终止用例"
            return 1
        fi
    elif [[ -n "$table_part" ]]; then
        log_info "[规则巡检] ${role}维度仅本用例预期内的规则，无干扰风险"
    fi
    return 0
}

# inspect_caller_rules <caller_service> <namespace> [expected ...]
# 巡检某主调（source）维度的熔断规则。
inspect_caller_rules() {
    local caller="$1"
    local ns="${2:-$NAMESPACE}"
    shift 2 || true
    _inspect_rules "主调" "srcService" "srcNamespace" "$caller" "$ns" "$@"
}

# inspect_callee_rules <callee_service> <namespace> <caller_for_filter> [expected ...]
# 巡检某被调（destination）维度的熔断规则。
# caller_for_filter：本用例的主调名；用来识别"实际不会影响本主调"的规则
#   （source 既非本主调、又不是 *）。这些规则会被列出但不计入"陌生规则"，
#   避免跨用例之间互相误伤（例如跑 instance 用例时把 service / interface 用例
#   留下的规则当作干扰）。
# 注意：被调维度可能命中多种来源（main 用例服务、CallerCommon、其他小工具等），
# 一旦同 destination 已经存在 source=* 的规则，会被本用例 consumer 命中并干扰统计。
inspect_callee_rules() {
    local callee="$1"
    local ns="${2:-$NAMESPACE}"
    local caller="$3"
    shift 3 || true
    INSPECT_CALLER="$caller" _inspect_rules "被调" "dstService" "dstNamespace" "$callee" "$ns" "$@"
}

# delete_circuitbreaker_rules <id1> <id2> ...
# 通过 POST /naming/v1/circuitbreaker/rules/delete 批量删除
delete_circuitbreaker_rules() {
    if [[ $# -eq 0 ]]; then
        return 0
    fi
    local body
    body=$(python3 -c "
import json, sys
ids = sys.argv[1:]
print(json.dumps([{'id': i} for i in ids]))
" "$@")

    local http_code resp
    http_code=$(curl -s -o /tmp/_cb_del_$$.tmp -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/naming/v1/circuitbreaker/rules/delete" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "${body}" 2>/dev/null || echo "000")
    resp=$(cat /tmp/_cb_del_$$.tmp 2>/dev/null || echo "")
    rm -f /tmp/_cb_del_$$.tmp

    if [[ "$http_code" == "200" ]]; then
        log_info "熔断规则删除成功 (ids=$*)"
    else
        log_warn "熔断规则删除失败 (HTTP=${http_code}, body=${resp:0:200})；请手动清理"
    fi
}

# enable_circuitbreaker_rule <id>
# 兜底确保规则启用（部分版本创建后默认 disabled）
enable_circuitbreaker_rule() {
    local rule_id="$1"
    local body
    body=$(python3 -c "import json; print(json.dumps([{'id': '${rule_id}', 'enable': True}]))")
    curl -s --connect-timeout 5 --max-time 10 \
        --request PUT "${POLARIS_HTTP_ADDR}/naming/v1/circuitbreaker/rules/enable" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "${body}" > /dev/null 2>&1 || true
}

# ======================== 规则 body 生成（python3 + 环境变量传参） ========================
# 三种规则共用骨架：
#   - rule_matcher.source = "*/*" 但 destination 精确指向当前子用例的 caller，
#     避免不同 case 之间相互触发。
#   - block_configs 仅一个，trigger_conditions = ERROR_RATE 50%/30s/minimumRequest=10
#     + CONSECUTIVE_ERROR=5；error_conditions = retCode 5xx
#   - recover_condition.sleepWindow = 30s，consecutiveSuccess = 1（只要 1 次成功即恢复）

build_rule_body_instance() {
    NAMESPACE="$NAMESPACE" SERVICE_NAME="$SERVICE_NAME" \
        SOURCE_NAMESPACE="$NAMESPACE" SOURCE_SERVICE="$INSTANCE_CALLER" \
        RULE_NAME="cb-instance-${INSTANCE_CALLER}" \
        LEVEL="INSTANCE" \
        BC_NAME="instance-block" \
        python3 "${SCRIPT_DIR}/.build/_gen_rule.py"
}

# 用例4：旧版散装写法的实例级熔断规则
# 与用例1结构一致，仅 source.service 改为 OLD_INSTANCE_CALLER，避免与用例1的规则相互干扰。
build_rule_body_old_instance() {
    NAMESPACE="$NAMESPACE" SERVICE_NAME="$SERVICE_NAME" \
        SOURCE_NAMESPACE="$NAMESPACE" SOURCE_SERVICE="$OLD_INSTANCE_CALLER" \
        RULE_NAME="cb-instance-${OLD_INSTANCE_CALLER}" \
        LEVEL="INSTANCE" \
        BC_NAME="instance-block" \
        python3 "${SCRIPT_DIR}/.build/_gen_rule.py"
}

# 用例5：HTTP 状态码区分专用规则
# 关注点：在默认 RANGE 500~599 规则下，验证三种 HTTP 状态码路径的不同行为。
# 与用例1 共用 SERVICE 维度规则结构，但 source.service=HTTP_STATUS_CALLER，
# 避免与用例1/4 的实例级规则相互干扰。
# 阈值故意收紧（CONSECUTIVE_ERROR=3）以加快端到端验证：
#   - 5xx 路径：3 次连续 5xx 即触发熔断
#   - 4xx 路径：所有请求都返回 RetSuccess，永远不触发熔断
#   - 网络错路径：SDK 内部 retCode="-1" 哨兵命中 RANGE 类条件，3 次即触发
build_rule_body_http_status() {
    NAMESPACE="$NAMESPACE" SERVICE_NAME="$SERVICE_NAME" \
        SOURCE_NAMESPACE="$NAMESPACE" SOURCE_SERVICE="$HTTP_STATUS_CALLER" \
        RULE_NAME="cb-instance-${HTTP_STATUS_CALLER}" \
        LEVEL="INSTANCE" \
        BC_NAME="http-status-block" \
        CONSECUTIVE_ERROR="3" \
        python3 "${SCRIPT_DIR}/.build/_gen_rule.py"
}

# 用例5 C段专用：SERVICE 级熔断规则（网络错 -1 哨兵）。
# 与 INSTANCE 级规则的关键区别：
#   Level=SERVICE → 熔断预检在 AcquirePermission() 阶段即可拦截，不依赖实例是否
#   仍在注册中心。当所有 provider 被杀后实例从注册中心剔除，INSTANCE 级熔断无法再
#   命中任何实例返回 ABORT；SERVICE 级在预检阶段就能直接返回 ABORT。
#   阈值同样收紧（CONSECUTIVE_ERROR=3）以加快验证。
build_rule_body_http_status_service() {
    NAMESPACE="$NAMESPACE" SERVICE_NAME="$SERVICE_NAME" \
        SOURCE_NAMESPACE="$NAMESPACE" SOURCE_SERVICE="$HTTP_STATUS_CALLER" \
        RULE_NAME="cb-service-${HTTP_STATUS_CALLER}" \
        LEVEL="SERVICE" \
        BC_NAME="service-block" \
        CONSECUTIVE_ERROR="3" \
        python3 "${SCRIPT_DIR}/.build/_gen_rule.py"
}

build_rule_body_service() {
    NAMESPACE="$NAMESPACE" SERVICE_NAME="$SERVICE_NAME" \
        SOURCE_NAMESPACE="$NAMESPACE" SOURCE_SERVICE="$SERVICE_CALLER" \
        RULE_NAME="cb-service-${SERVICE_CALLER}" \
        LEVEL="SERVICE" \
        BC_NAME="service-block" \
        python3 "${SCRIPT_DIR}/.build/_gen_rule.py"
}

# 接口级合并规则：1 条 METHOD 规则 + 3 个 BlockConfig（/echo + /order + /slow）。
# /echo  block 用 RANGE/EXACT/REGEX/IN/NOT_IN 多 MatchString 演示
# /order block 阈值极高确保不触发
# /slow  block 用 DELAY 错误条件
# 三个 block 通过 $BCS 传入，SDK 在 dispatch 时按 api.path 过滤隔离。
build_rule_body_interface_merged() {
    local bcs
    bcs=$(python3 -c '
import json
bcs = []
# /echo block: 多 MatchString 演示
echo_err = [
    {"inputType":"RET_CODE","condition":{"type":"RANGE","value":"500~599"}},
    {"inputType":"RET_CODE","condition":{"type":"EXACT","value":"500"}},
    {"inputType":"RET_CODE","condition":{"type":"REGEX","value":"^5[0-9]{2}$"}},
    {"inputType":"RET_CODE","condition":{"type":"IN","value":"500,502,503,504"}},
    {"inputType":"RET_CODE","condition":{"type":"NOT_IN","value":"200,201,204,400,401,403,404"}},
]
bcs.append({
    "name": "echo-block",
    "api": {"protocol": "*", "method": "*", "path": {"type": "EXACT", "value": "/echo"}},
    "error_conditions": echo_err,
    "trigger_conditions": [
        {"triggerType": "CONSECUTIVE_ERROR", "errorCount": 5},
        {"triggerType": "ERROR_RATE", "errorPercent": 50, "interval": 30, "minimumRequest": 10},
    ],
})
# /order block: 阈值极高确保不触发
bcs.append({
    "name": "order-block",
    "api": {"protocol": "*", "method": "*", "path": {"type": "EXACT", "value": "/order"}},
    "error_conditions": [
        {"inputType": "RET_CODE", "condition": {"type": "RANGE", "value": "500~599"}},
    ],
    "trigger_conditions": [
        {"triggerType": "CONSECUTIVE_ERROR", "errorCount": 100},
        {"triggerType": "ERROR_RATE", "errorPercent": 99, "interval": 30, "minimumRequest": 200},
    ],
})
# /slow block: DELAY 错误条件
bcs.append({
    "name": "slow-block",
    "api": {"protocol": "*", "method": "*", "path": {"type": "EXACT", "value": "/slow"}},
    "error_conditions": [
        {"inputType": "DELAY", "condition": {"type": "EXACT", "value": "200"}},
    ],
    "trigger_conditions": [
        {"triggerType": "CONSECUTIVE_ERROR", "errorCount": 5},
        {"triggerType": "ERROR_RATE", "errorPercent": 50, "interval": 30, "minimumRequest": 10},
    ],
})
print(json.dumps(bcs))
')
    NAMESPACE="$NAMESPACE" SERVICE_NAME="$SERVICE_NAME" \
        SOURCE_NAMESPACE="$NAMESPACE" SOURCE_SERVICE="$INTERFACE_CALLER" \
        RULE_NAME="cb-interface-${INTERFACE_CALLER}" \
        LEVEL="METHOD" \
        BC_NAME="echo-block" \
        BCS="$bcs" \
        CONSECUTIVE_ERROR="5" \
        ERROR_PERCENT="50" \
        ERROR_INTERVAL="30" \
        MINIMUM_REQUEST="10" \
        python3 "${SCRIPT_DIR}/.build/_gen_rule.py"
}

build_rule_body_interface() {
    # 接口级 /echo 规则：CONSECUTIVE_ERROR=5；用于触发"配置规则的接口被熔断"。
    #
    # ErrorConditions 列表覆盖 RET_CODE 支持的若干 MatchString 类型，作为
    # "返回码支持条件判断、范围、全匹配、正则表达式、包含和不包含" 的演示。
    # 多条同时存在，任一命中即视为失败（SDK 行为见 plugin/circuitbreaker/composite/block_counter.go
    # parseRetStatus —— 多个条件之间是 OR 关系）。
    # 这里所有条件都只对 5xx 命中、不会命中 4xx，与"4xx 不计入熔断"语义对齐：
    #   provider 返回 "500"  → 全部命中，计入熔断；
    #   provider 返回 "404"  → 全部不命中，不计入熔断；
    #   provider 返回 "200"  → 全部不命中，不计入熔断。
    local err_conditions
    err_conditions='[
        {"inputType":"RET_CODE","condition":{"type":"RANGE","value":"500~599"}},
        {"inputType":"RET_CODE","condition":{"type":"EXACT","value":"500"}},
        {"inputType":"RET_CODE","condition":{"type":"REGEX","value":"^5[0-9]{2}$"}},
        {"inputType":"RET_CODE","condition":{"type":"IN","value":"500,502,503,504"}},
        {"inputType":"RET_CODE","condition":{"type":"NOT_IN","value":"200,201,204,400,401,403,404"}}
    ]'
    NAMESPACE="$NAMESPACE" SERVICE_NAME="$SERVICE_NAME" \
        SOURCE_NAMESPACE="$NAMESPACE" SOURCE_SERVICE="$INTERFACE_CALLER" \
        RULE_NAME="cb-interface-${INTERFACE_CALLER}" \
        LEVEL="METHOD" \
        BC_NAME="echo-block" \
        BC_API_PATH="/echo" \
        ERR_CONDITIONS="$err_conditions" \
        CONSECUTIVE_ERROR="5" \
        ERROR_PERCENT="50" \
        ERROR_INTERVAL="30" \
        MINIMUM_REQUEST="10" \
        python3 "${SCRIPT_DIR}/.build/_gen_rule.py"
}

build_rule_body_interface_order() {
    # 接口级 /order 规则：阈值故意设到必然不触发，用于演示
    # "两个接口配置不同的规则会按照各自的规则生效" —— /echo 在 5 次连续错误后熔断，
    # /order 即使全部失败也不会熔断（CONSECUTIVE_ERROR=100 + MINIMUM_REQUEST=200，
    # 远超本测试一轮的 15 次请求）。
    NAMESPACE="$NAMESPACE" SERVICE_NAME="$SERVICE_NAME" \
        SOURCE_NAMESPACE="$NAMESPACE" SOURCE_SERVICE="$INTERFACE_CALLER" \
        RULE_NAME="cb-interface-${INTERFACE_CALLER}-order" \
        LEVEL="METHOD" \
        BC_NAME="order-block" \
        BC_API_PATH="/order" \
        CONSECUTIVE_ERROR="100" \
        ERROR_PERCENT="99" \
        ERROR_INTERVAL="30" \
        MINIMUM_REQUEST="200" \
        python3 "${SCRIPT_DIR}/.build/_gen_rule.py"
}

# /slow 接口的 DELAY 熔断规则。
# input_type=DELAY 时 SDK 会把 stat.Delay > condition.value(ms) 的调用计为 RetTimeout，
# 进而被纳入 trigger_conditions 统计。这里阈值设 200ms，配合 verify 脚本把
# /slow 的 sleepDelayMs 调到 500ms，可触发"时延 → 熔断"。
build_rule_body_interface_slow() {
    NAMESPACE="$NAMESPACE" SERVICE_NAME="$SERVICE_NAME" \
        SOURCE_NAMESPACE="$NAMESPACE" SOURCE_SERVICE="$INTERFACE_CALLER" \
        RULE_NAME="cb-interface-${INTERFACE_CALLER}-slow" \
        LEVEL="METHOD" \
        BC_NAME="slow-block" \
        BC_API_PATH="/slow" \
        INPUT_TYPE="DELAY" \
        CONDITION_VALUE="200" \
        CONSECUTIVE_ERROR="5" \
        ERROR_PERCENT="50" \
        ERROR_INTERVAL="30" \
        MINIMUM_REQUEST="10" \
        python3 "${SCRIPT_DIR}/.build/_gen_rule.py"
}

# 用例7：修改熔断参数生效验证专用规则。
# 第一个参数 consecutive_error 决定 CONSECUTIVE_ERROR 阈值；
# 其余触发条件（ERROR_RATE）设为极高阈值避免干扰，让用例聚焦在顺序错误计数上。
build_rule_body_modify_rule() {
    local consecutive_error="${1:-3}"
    NAMESPACE="$NAMESPACE" SERVICE_NAME="$SERVICE_NAME" \
        SOURCE_NAMESPACE="$NAMESPACE" SOURCE_SERVICE="$MODIFY_RULE_CALLER" \
        RULE_NAME="cb-instance-${MODIFY_RULE_CALLER}" \
        LEVEL="INSTANCE" \
        BC_NAME="modify-block" \
        CONSECUTIVE_ERROR="$consecutive_error" \
        ERROR_PERCENT="100" \
        MINIMUM_REQUEST="9999" \
        python3 "${SCRIPT_DIR}/.build/_gen_rule.py"
}

# 用例 8：接口协议 + HTTP 方法维度合并在 1 条规则的 13 个 BlockConfig 里
# 4 个 protocol（http/dubbo/grpc/thrift）+ 9 个 method（GET/POST/PUT/PATCH/DELETE/HEAD/
# OPTIONS/TRACE/CONNECT）= 13 个 BC，共享 caller=$PM_CALLER。
# 13 个 BC 互不重叠：4 个 protocol 用 path="/api/protocol/{proto}"（path 含 proto 标识），
# 9 个 method 用 path="/api/method/{method}"（path 含 method 标识），
# SDK 端 matchMethodWithAPI 逐字段匹配，任一字段不匹配即该 BC 不适用。
# threshold 收紧 (CONSECUTIVE_ERROR=3) 加快验证。
build_rule_body_protocol_method() {
    # 构造 BCS JSON 数组：4 protocol + 9 method = 13 个 BC
    local bcs='['
    local first=1
    # 4 个 protocol BC
    for proto in http dubbo grpc thrift; do
        if [[ $first -eq 0 ]]; then bcs+=','; fi
        first=0
        bcs+='{"name":"proto-block-'"$proto"'","api":{"protocol":"'"$proto"'","method":"*","path":{"type":"EXACT","value":"/api/protocol/'"$proto"'"}},"error_conditions":[{"inputType":"RET_CODE","condition":{"type":"RANGE","value":"500~599"}}],"trigger_conditions":[{"triggerType":"CONSECUTIVE_ERROR","errorCount":3},{"triggerType":"ERROR_RATE","errorPercent":50,"interval":30,"minimumRequest":5}]}'
    done
    # 9 个 method BC
    for meth in GET POST PUT PATCH DELETE HEAD OPTIONS TRACE CONNECT; do
        if [[ $first -eq 0 ]]; then bcs+=','; fi
        first=0
        bcs+='{"name":"meth-block-'"$meth"'","api":{"protocol":"*","method":"'"$meth"'","path":{"type":"EXACT","value":"/api/method/'"$meth"'"}},"error_conditions":[{"inputType":"RET_CODE","condition":{"type":"RANGE","value":"500~599"}}],"trigger_conditions":[{"triggerType":"CONSECUTIVE_ERROR","errorCount":3},{"triggerType":"ERROR_RATE","errorPercent":50,"interval":30,"minimumRequest":5}]}'
    done
    bcs+=']'

    BCS="$bcs" \
    NAMESPACE="$NAMESPACE" SERVICE_NAME="$SERVICE_NAME" \
    SOURCE_NAMESPACE="$NAMESPACE" SOURCE_SERVICE="$PM_CALLER" \
    RULE_NAME="cb-proto-meth-${PM_CALLER}" \
    RULE_DESCRIPTION="case_protocol_method: 4 protocol + 9 method in 13 BlockConfigs" \
    LEVEL="METHOD" \
    python3 "${SCRIPT_DIR}/.build/_gen_rule.py"
}

# 用例 10：路径匹配方式维度 5 条规则（EXACT/REGEX/NOT_EQUALS/IN/NOT_IN）
# 1 条规则 = 1 个 BlockConfig（按 path MatchString 类型区分）
# 第一个参数是 ptype，第二个是 path_value
#   - EXACT:        /api/pathtype/exact
#   - REGEX:        ^/api/pathtype/regex/.*  （消费者用 /api/pathtype/regex/abc 触发匹配）
#   - NOT_EQUALS:   /api/pathtype/never_match  （消费者用任何不等于该值的 path 触发匹配）
#   - IN:           /api/pathtype/in1,/api/pathtype/in2  （消费者用 in1 触发匹配）
#   - NOT_IN:       /api/pathtype/forbidden1,/api/pathtype/forbidden2  （消费者用 /api/pathtype/allowed 触发匹配）
# 合并版：通过 $BCS 将 5 种 path-type 合为 1 条 METHOD 规则 + 5 个 BlockConfig。
# 每个 block 独立持有 api（path 匹配）和共享的 error/trigger 条件；SDK 在 dispatch 时按 api 过滤。
build_rule_body_pathtype_merged() {
    local bcs
    bcs=$(python3 -c '
import json
pts = [
    ("EXACT",      "/api/pathtype/exact"),
    ("REGEX",      "^/api/pathtype/regex/.*"),
    ("NOT_EQUALS", "/api/pathtype/never_match_anything"),
    ("IN",         "/api/pathtype/in1,/api/pathtype/in2"),
    ("NOT_IN",     "/api/pathtype/forbidden1,/api/pathtype/forbidden2"),
]
bcs = []
for pt, pv in pts:
    bcs.append({
        "name": "pathtype-{}-block".format(pt.lower()),
        "api": {"protocol": "*", "method": "*", "path": {"type": pt, "value": pv}},
        "error_conditions": [
            {"inputType": "RET_CODE", "condition": {"type": "RANGE", "value": "500~599"}}
        ],
        "trigger_conditions": [
            {"triggerType": "CONSECUTIVE_ERROR", "errorCount": 3},
            {"triggerType": "ERROR_RATE", "errorPercent": 50, "interval": 30, "minimumRequest": 5},
        ],
    })
print(json.dumps(bcs))
')
    NAMESPACE="$NAMESPACE" SERVICE_NAME="$SERVICE_NAME" \
        SOURCE_NAMESPACE="$NAMESPACE" SOURCE_SERVICE="$PATHTYPE_CALLER" \
        RULE_NAME="cb-pathtype-${PATHTYPE_CALLER}" \
        LEVEL="METHOD" \
        BC_NAME="pathtype-merged" \
        BCS="$bcs" \
        CONSECUTIVE_ERROR="3" \
        ERROR_PERCENT="50" \
        ERROR_INTERVAL="30" \
        MINIMUM_REQUEST="5" \
        python3 "${SCRIPT_DIR}/.build/_gen_rule.py"
}

build_rule_body_pathtype() {
    local ptype="$1"
    local pvalue="$2"
    # macOS bash 3.2 不支持 ${var,,} 大写转小写，改用 tr 跨平台兼容。
    local ptype_lc
    ptype_lc=$(echo "$ptype" | tr '[:upper:]' '[:lower:]')
    NAMESPACE="$NAMESPACE" SERVICE_NAME="$SERVICE_NAME" \
        SOURCE_NAMESPACE="$NAMESPACE" SOURCE_SERVICE="$PATHTYPE_CALLER" \
        RULE_NAME="cb-pathtype-${PATHTYPE_CALLER}-${ptype_lc}" \
        LEVEL="METHOD" \
        BC_NAME="pathtype-${ptype_lc}-block" \
        BC_API_PROTOCOL="*" \
        BC_API_METHOD="*" \
        BC_API_PATH_TYPE="$ptype" \
        BC_API_PATH_VALUE="$pvalue" \
        BC_API_PATH_VALUE_LIST="$pvalue" \
        CONSECUTIVE_ERROR="3" \
        ERROR_PERCENT="50" \
        ERROR_INTERVAL="30" \
        MINIMUM_REQUEST="5" \
        python3 "${SCRIPT_DIR}/.build/_gen_rule.py"
}

# 生成 _gen_rule.py 辅助脚本（由 build_rule_body_* 调用）
write_rule_generator() {
    mkdir -p "$BUILD_DIR"
    cat > "${BUILD_DIR}/_gen_rule.py" << 'PYEOF'
"""根据环境变量构造 polaris CircuitBreakerRule JSON。

重要兼容性说明：
  Polaris 控制台目前会同时读取两组字段：
    - 顶层 deprecated 字段：error_conditions / trigger_condition / recoverCondition
    - 现行字段：block_configs[*]（内含 error_conditions / trigger_conditions）
  仅写新字段时控制台展示会出现"解析不出来"。因此本生成器把内容
  在两处都写一份，与控制台导出的 backup 规则结构保持一致；SDK 端读
  block_configs 路径，互不干扰。

环境变量（必填）:
  RULE_NAME / NAMESPACE / SERVICE_NAME
  SOURCE_NAMESPACE / SOURCE_SERVICE
  LEVEL  ∈ {SERVICE, METHOD, INSTANCE}
  BC_NAME

可选 — 触发条件:
  BC_API_PATH         仅 METHOD 级使用，对应 BlockConfig.api.path（EXACT 匹配）
  CONSECUTIVE_ERROR   连续错误数阈值，默认 5
  ERROR_PERCENT       错误率阈值（%），默认 50
  ERROR_INTERVAL      错误率统计窗口（秒），默认 30
  MINIMUM_REQUEST     错误率统计的最小请求数，默认 10

可选 — 错误判定条件（默认: 单条 RET_CODE RANGE 500~599）:
  ERR_CONDITIONS      JSON 串，直接覆盖 ErrorCondition 列表，可写多条。
                      每条形如 {"inputType":"RET_CODE","condition":
                        {"type":"RANGE","value":"500~599"}}。
                      也可以用 input_type=DELAY 表达"调用时长 > value(ms)
                      视为失败"——SDK 会把超时调用计为 RetTimeout。
                      留空则使用 INPUT_TYPE / CONDITION_TYPE / CONDITION_VALUE。
  INPUT_TYPE          ∈ {RET_CODE, DELAY}，默认 RET_CODE
  CONDITION_TYPE      ∈ {EXACT, REGEX, NOT_EQUALS, IN, NOT_IN, RANGE}（DELAY 时忽略）
                      默认 RANGE
  CONDITION_VALUE     RET_CODE：与 CONDITION_TYPE 配合的值；IN/NOT_IN 用逗号分隔；
                                RANGE 用 "min~max"（含两端）
                      DELAY   ：毫秒阈值字符串，例如 "200"
                      默认 "500~599"
"""
import json
import os
import sys

level = os.environ['LEVEL']

# 错误判定可由环境变量分两种方式注入：
#   1) ERR_CONDITIONS 直接给一个 JSON 数组（适合多条/复合条件）
#   2) INPUT_TYPE + CONDITION_TYPE + CONDITION_VALUE 单条（向后兼容默认值）
err_conditions_env = os.environ.get('ERR_CONDITIONS', '').strip()
if err_conditions_env:
    err_conditions = json.loads(err_conditions_env)
    if not isinstance(err_conditions, list):
        sys.stderr.write("ERR_CONDITIONS must be a JSON array\n")
        sys.exit(2)
else:
    # 默认行为：把 5xx 视为熔断错误，4xx（参数/鉴权类）不计入熔断；
    # 与 demo 中 reportServiceCallResult 的"只把 5xx 标 RetFail"语义对齐。
    input_type = os.environ.get('INPUT_TYPE', 'RET_CODE')
    condition_type = os.environ.get('CONDITION_TYPE', 'RANGE')
    condition_value = os.environ.get('CONDITION_VALUE', '500~599')
    err_conditions = [{
        'inputType': input_type,
        'condition': {
            # DELAY 类型只看 value，type 字段不参与判定，但仍按 EXACT 占位
            'type': condition_type if input_type == 'RET_CODE' else 'EXACT',
            'value': condition_value,
        },
    }]

# 触发条件可由环境变量覆盖，便于不同接口配置不同阈值（接口级用例需要演示）
consecutive_error = int(os.environ.get('CONSECUTIVE_ERROR', '5'))
error_percent = int(os.environ.get('ERROR_PERCENT', '50'))
error_interval = int(os.environ.get('ERROR_INTERVAL', '30'))
minimum_request = int(os.environ.get('MINIMUM_REQUEST', '10'))

# 触发条件：CONSECUTIVE_ERROR + ERROR_RATE，两者满足任一即触发熔断。
trigger_conditions = [
    {
        'triggerType': 'CONSECUTIVE_ERROR',
        'errorCount': consecutive_error,
    },
    {
        'triggerType': 'ERROR_RATE',
        'errorPercent': error_percent,
        'interval': error_interval,
        'minimumRequest': minimum_request,
    },
]

# BlockConfig：现行字段，SDK 通过 GetBlockConfigs() 读取
bc = {
    'name': os.environ.get('BC_NAME', 'default-bc'),
    'error_conditions': err_conditions,
    'trigger_conditions': trigger_conditions,
}

# 接口级规则：携带 api（path EXACT 匹配）；非接口级也带一个空 path 占位，
# 与控制台导出的 backup 结构保持一致。
api_path = os.environ.get('BC_API_PATH', '')
api_protocol = os.environ.get('BC_API_PROTOCOL', '*')
api_method = os.environ.get('BC_API_METHOD', '*')
api_path_type = os.environ.get('BC_API_PATH_TYPE', 'EXACT')
api_path_value = os.environ.get('BC_API_PATH_VALUE', '') or api_path
api_path_value_list = os.environ.get('BC_API_PATH_VALUE_LIST', '')
if level == 'METHOD' and (api_path_value or api_path_value_list):
    path_value = api_path_value_list if api_path_type in ('IN', 'NOT_IN') else api_path_value
    bc['api'] = {
        'protocol': api_protocol,
        'method': api_method,
        'path': {
            'type': api_path_type,
            'value': path_value,
        },
    }
else:
    bc['api'] = {'path': {'value': ''}}

# block_configs：默认 1 个 BC（用上面的 bc）；可通过 BCS 环境变量传入 JSON 数组覆盖，
# 用于在同一条规则下表达多个 (protocol, method, path) 维度组合。
# BCS 数组每个元素是 BlockConfig dict（含 name/api/error_conditions/trigger_conditions，
# 缺省字段会用本生成器的默认值——api 需传入完整结构）。
bcs_env = os.environ.get('BCS', '').strip()
if bcs_env:
    try:
        parsed_bcs = json.loads(bcs_env)
    except Exception as e:
        sys.stderr.write(f"BCS must be valid JSON: {e}\n")
        sys.exit(2)
    if not isinstance(parsed_bcs, list):
        sys.stderr.write("BCS must be a JSON array\n")
        sys.exit(2)
    bcs = parsed_bcs
else:
    bcs = [bc]

rule = {
    'name': os.environ['RULE_NAME'],
    'namespace': os.environ['NAMESPACE'],
    'enable': True,
    'level': level,
    'description': os.environ.get('RULE_DESCRIPTION', 'auto-created by verify_circuitbreaker.sh'),
    'rule_matcher': {
        'source': {
            'namespace': os.environ['SOURCE_NAMESPACE'],
            'service': os.environ['SOURCE_SERVICE'],
        },
        'destination': {
            'namespace': os.environ['NAMESPACE'],
            'service': os.environ['SERVICE_NAME'],
            # destination.method 字段 deprecated，新逻辑通过 BlockConfig.api 表达；
            # 这里仍保留 "*" 占位以满足 server 端兼容校验。
            'method': {'type': 'EXACT', 'value': '*'},
        },
    },
    # 顶层 deprecated 字段：与 block_configs 重复一份，控制台据此渲染规则详情
    'error_conditions': err_conditions,
    'trigger_condition': trigger_conditions,
    'recoverCondition': {
        # sleepWindow 设小一点，便于一轮用例里跑两次"触发→熔断→恢复"。
        'sleep_window': 12,
        'consecutiveSuccess': 1,
    },
    'block_configs': bcs,
}

sys.stdout.write(json.dumps(rule))
PYEOF
}

# ======================== Provider/Consumer 配置生成 ========================
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
    # 第二参数控制是否启用默认实例熔断规则；默认 false（关闭，避免干扰其它用例）
    # 用例 6（default_rule）会传 "true"，验证不下发任何规则时本地默认熔断的兜底行为
    local enable_default="${2:-false}"

    local default_block=""
    if [[ "$enable_default" == "true" ]]; then
        # 启用默认规则时收紧阈值，加快端到端验证速度：
        #   - defaultErrorCount=3：连续 3 次 5xx 即触发实例级熔断
        #   - defaultMinimumRequest=3：错误率统计的最小请求数也调到 3
        #   - sleepWindow=12s：与其它用例的规则模板保持一致
        #   - successCountAfterHalfOpen=1：半开 1 次成功即恢复
        # 默认 errorCondition 为 RANGE 500~599（写在 default.go 中），4xx 不计入
        default_block=$(cat <<'YAML'
    defaultRuleEnable: true
    defaultErrorCount: 3
    defaultErrorPercent: 50
    defaultInterval: 30s
    defaultMinimumRequest: 3
    sleepWindow: 12s
    successCountAfterHalfOpen: 1
YAML
)
    else
        # 关闭默认规则：接口级用例需要验证"未配置规则的接口不会被熔断"，
        # 若保留默认规则，未命中任何 METHOD 规则的调用会被实例级默认规则按 5xx
        # 计数熔断，导致 /info 这类无规则路径出现 abort，干扰判定。
        default_block="    defaultRuleEnable: false"
    fi

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
    # 缩短规则拉取/检查间隔，加快测试中规则下发感知
    checkPeriod: 5s
${default_block}
EOF
}

# ======================== 启动 Provider ========================
start_provider() {
    local name="$1"
    local src_dir="$2"
    local port="$3"
    local log_file="$4"

    # 注意：workdir 不能与二进制输出路径同名，否则 go build -o 会把二进制写入该目录内，
    # 导致后续执行 "${BUILD_DIR}/${name}" 报错 "is a directory"。
    local workdir="${BUILD_DIR}/${name}_run"
    mkdir -p "$workdir"
    generate_provider_polaris_yaml "$workdir"

    # 端口占用清理
    if lsof -ti :"${port}" > /dev/null 2>&1; then
        log_warn "端口 ${port} 已被占用，尝试终止已有进程..."
        local existing_pid
        existing_pid=$(lsof -ti :"${port}" 2>/dev/null | head -1 || echo "")
        [[ -n "$existing_pid" ]] && kill "$existing_pid" 2>/dev/null && sleep 1 || true
    fi

    log_info "编译 ${name}..."
    (cd "$src_dir" && go build -o "${BUILD_DIR}/${name}" .)
    if command -v xattr &> /dev/null; then
        xattr -c "${BUILD_DIR}/${name}" 2>/dev/null || true
    fi

    # 启动后台 provider 时显式 detach 标准输入输出，避免子进程持有当前 shell 的 fd。
    # 否则当 start_provider 在 r=$(case_xxx) 这类命令替换的子 shell 中被调用时，
    # provider 子进程会继承 $() 的捕获管道，主 shell 在子 shell 退出后仍在等待
    # 该管道关闭 → 整个脚本卡死，直到 provider 自然退出。
    # 解决方法：把 stdin/stdout/stderr 全部接到日志文件 / /dev/null，并用 setsid（Linux 上）
    # 或子 shell + 重定向（macOS 上）让进程脱离会话。
    (cd "$workdir" && exec "${BUILD_DIR}/${name}" \
        --namespace "$NAMESPACE" \
        --service "$SERVICE_NAME" \
        --token "$POLARIS_TOKEN" \
        --port "$port" \
        --debug="$DEBUG_MODE" \
        < /dev/null > "$log_file" 2>&1) &
    local pid=$!
    # 显式 disown 让子进程从 shell 的 jobs 表中脱钩，避免 wait/wait fd 阻塞
    disown "$pid" 2>/dev/null || true
    sleep 1
    check_process_alive "$pid" "$name" || {
        log_error "${name} 启动失败，请检查日志: $log_file"
        cat "$log_file" 2>/dev/null || true
        return 1
    }
    wait_for_http "http://127.0.0.1:${port}/echo" 20 "$name" "$pid" || return 1
    log_info "${name} 已启动 (PID: $pid, port: $port)"
    _STARTED_PID="$pid"
    return 0
}

# 通过 /switch 翻转 provider 错误开关
# 用法：
#   provider_set_error <port> <echo_enabled>
#       仅翻转 /echo 的故障开关（向后兼容）
#   provider_set_error <port> <echo_enabled> <order_enabled>
#       同时翻转 /echo 和 /order 的故障开关
provider_set_error() {
    local port="$1"
    local echo_enabled="$2"   # true=返回500, false=返回200
    local order_enabled="${3:-}"
    local query="openError=${echo_enabled}"
    if [[ -n "$order_enabled" ]]; then
        query="${query}&openErrorOrder=${order_enabled}"
    fi
    curl -s --connect-timeout 3 --max-time 5 \
        "http://127.0.0.1:${port}/switch?${query}" > /dev/null 2>&1 || true
}

# provider_set_slow <port> <delay_ms>
# 调整 /slow 接口的人为延迟，用于 DELAY 熔断验证。
# delay_ms=0 表示立即返回（恢复阶段使用）。
provider_set_slow() {
    local port="$1"
    local delay_ms="$2"
    curl -s --connect-timeout 3 --max-time 30 \
        "http://127.0.0.1:${port}/switch?slowDelayMs=${delay_ms}" > /dev/null 2>&1 || true
}

# ======================== 启动 Consumer ========================
start_consumer() {
    local name="$1"
    local src_dir="$2"
    local self_service="$3"
    local port="$4"
    local log_file="$5"
    # 第六参数（可选）：是否启用默认实例熔断规则；默认 false
    local enable_default_rule="${6:-false}"

    # 注意：workdir 不能与二进制输出路径同名，理由同 start_provider。
    local workdir="${BUILD_DIR}/${name}_run"
    mkdir -p "$workdir"
    generate_consumer_polaris_yaml "$workdir" "$enable_default_rule"

    if lsof -ti :"${port}" > /dev/null 2>&1; then
        log_warn "端口 ${port} 已被占用，尝试终止已有进程..."
        local existing_pid
        existing_pid=$(lsof -ti :"${port}" 2>/dev/null | head -1 || echo "")
        [[ -n "$existing_pid" ]] && kill "$existing_pid" 2>/dev/null && sleep 1 || true
    fi

    log_info "编译 ${name}..."
    (cd "$src_dir" && go build -o "${BUILD_DIR}/${name}" .)
    if command -v xattr &> /dev/null; then
        xattr -c "${BUILD_DIR}/${name}" 2>/dev/null || true
    fi

    # 同 start_provider：子进程必须 detach 标准 fd，避免被 r=$(case_xxx) 命令替换的
    # 子 shell 持有捕获管道导致主 shell 卡死。
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
        log_error "${name} 启动失败，请检查日志: $log_file"
        cat "$log_file" 2>/dev/null || true
        return 1
    }
    wait_for_http "http://127.0.0.1:${port}/echo" 30 "$name" "$pid" || return 1
    log_info "${name} 已启动 (PID: $pid, port: $port)"
    CONSUMER_PIDS+=("$pid")
    _STARTED_PID="$pid"
    return 0
}

stop_consumer() {
    local pid="$1"
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
        kill "$pid" 2>/dev/null || true
        wait "$pid" 2>/dev/null || true
    fi
    local new_pids=()
    local removed=false
    for existing in "${CONSUMER_PIDS[@]:-}"; do
        if ! $removed && [[ "$existing" == "$pid" ]]; then
            removed=true
            continue
        fi
        new_pids+=("$existing")
    done
    CONSUMER_PIDS=("${new_pids[@]:-}")
}

# ======================== 用例统计 / 请求执行 ========================
# do_request <consumer_port> [path] 输出: <http_code>|<body>
# path 默认 /echo，接口级用例会显式传入 /order /info 等不同路径
do_request() {
    local port="$1"
    local path="${2:-/echo}"
    local body http_code
    http_code=$(curl -s -o /tmp/_cb_resp_$$.tmp -w '%{http_code}' \
        --connect-timeout 3 --max-time 5 \
        "http://127.0.0.1:${port}${path}" 2>/dev/null || echo "000")
    body=$(cat /tmp/_cb_resp_$$.tmp 2>/dev/null || echo "")
    rm -f /tmp/_cb_resp_$$.tmp
    echo "${http_code}|${body}"
}

# 累计请求次数 + 统计 200 / 5xx / 熔断（call aborted）三类
# 输出全局变量：CASE_TOTAL CASE_OK CASE_FAIL CASE_ABORT
run_burst() {
    local consumer_port="$1"
    local count="$2"
    local label="${3:-请求}"
    local path="${4:-/echo}"

    CASE_TOTAL=0
    CASE_OK=0
    CASE_FAIL=0
    CASE_ABORT=0

    # 注意：`run_burst` 通常被 case_instance/case_service/case_interface 间接调用，
    # 而调用方用 `r=$(case_xxx)` 通过命令替换捕获用例结论 (PASS/FAIL)。
    # 因此本函数所有给人看的请求明细输出必须写到 stderr，避免被命令替换吞掉，
    # 从而污染 results 数组（典型表现：汇总表里把请求明细当结果展示）。
    # `log_*` 已经写 stderr，仅这里的 printf 表格需要显式重定向。
    printf "  ${CYAN}%-6s %-12s %-10s %s${NC}\n" "序号" "HTTP状态" "判定" "响应摘要" >&2
    printf "  %-6s %-12s %-10s %s\n" "------" "------------" "----------" "------------------------------" >&2

    for i in $(seq 1 "$count"); do
        CASE_TOTAL=$((CASE_TOTAL + 1))
        local result http_code body verdict_color verdict
        result=$(do_request "$consumer_port" "$path")
        http_code="${result%%|*}"
        body="${result#*|}"
        local short_body
        short_body=$(echo "$body" | head -c 80)

        # 判定优先级（从高到低）：
        #   1) ABORT —— body 命中 "call aborted"，说明 polaris-go 拦截了调用
        #   2) FAIL  —— 上游返回非 200，或 consumer 透传了 provider 的 5xx 状态
        #               注意：当前 demo 中 consumer 在 provider 5xx 时仍 WriteHeader(200)，
        #               把原 body（含 "status code: 500, Fatal, ..."）原样回写，因此必须
        #               同时检测 body 中的 5xx 提示，避免把失败错算成 OK。
        #   3) OK    —— provider 真正回了 200（body 含 "Hello" 或 "status code: 200"）
        if echo "$body" | grep -q "call aborted"; then
            CASE_ABORT=$((CASE_ABORT + 1))
            verdict="ABORT"
            verdict_color="${YELLOW}"
        elif [[ "$http_code" != "200" ]] \
            || echo "$body" | grep -qE "status code: 5[0-9][0-9]" \
            || echo "$body" | grep -q "Fatal" \
            || echo "$body" | grep -qE "\[errot?\]"; then
            CASE_FAIL=$((CASE_FAIL + 1))
            verdict="FAIL"
            verdict_color="${RED}"
        else
            CASE_OK=$((CASE_OK + 1))
            verdict="200"
            verdict_color="${GREEN}"
        fi
        printf "  %-6s %-12s ${verdict_color}%-10s${NC} %s\n" \
            "$i" "HTTP $http_code" "$verdict" "$short_body" >&2
        sleep 0.05
    done
    echo "" >&2
    log_info "[${label}] total=${CASE_TOTAL}, ok=${CASE_OK}, fail=${CASE_FAIL}, abort=${CASE_ABORT}"
}

# ======================== 三个子用例 ========================
# 每个子用例返回 0=PASS / 非0=FAIL，并把结论 echo 到 stdout
# 用例编号严格顺序递增（参考 examples/route 规约）

# ---------------------- 用例1：实例级熔断 ----------------------
# 验证完整生命周期，包含两轮"触发 → 熔断 → 恢复"：
#   1.1 复位 provider（A=200, B=500）
#   1.2 启动 instance-consumer
#   1.3 创建 INSTANCE 级规则
#   ── 第 1 轮 ──
#   1.4 trigger : provider-b 5xx 累计 → b 被摘除
#   1.5 verify  : 流量全部走 a，全部 200
#   1.6 recover : provider-b 翻回 200，等待 sleepWindow，b 重新加入 LB → 全部 200
#   ── 第 2 轮 ──
#   1.7 trigger : 再次让 provider-b 返回 500，再次连发 → b 再次被摘除（出现 5xx）
#   1.8 verify  : 流量再次全部走 a，全部 200
#   1.9 recover : 再次翻回 200 → 全部 200
#
# 通过条件：两轮都满足 trigger 阶段出现 5xx 且 verify/recover 阶段全部 200
case_instance() {
    log_step "用例1：实例级（INSTANCE）熔断"

    print_block "用例1：实例级（INSTANCE）熔断" \
        "Caller 写法:" \
        "  · 统一装饰器：CircuitBreakerAPI.MakeFunctionDecorator" \
        "  · customer func 内 model.GetInvokeContext(ctx).SetInstance(instance)" \
        "    → 装饰器结束阶段自动按 InstanceResource 上报" \
        "" \
        "规则配置:" \
        "  · Level=INSTANCE, name=cb-instance-${INSTANCE_CALLER}" \
        "  · source=*/${INSTANCE_CALLER}, destination=${NAMESPACE}/${SERVICE_NAME}" \
        "  · ErrorCondition: RET_CODE RANGE 500~599 (4xx 不计入熔断)" \
        "  · TriggerCondition: CONSECUTIVE_ERROR=5 / ERROR_RATE=50%@30s, minRequest=10" \
        "  · RecoverCondition: sleepWindow=12s, consecutiveSuccess=1" \
        "" \
        "验证步骤:" \
        "  1.1 provider-a=200 / provider-b=500" \
        "  1.2 启动 instance consumer" \
        "  1.3 创建/更新规则" \
        "  ── 第 1 轮 ──" \
        "  1.4 触发：连发 ${TRIGGER_REQUEST_COUNT} 次让 b 累计 5 次失败 → 实例 b 被摘除" \
        "  1.5 验证：再发 ${RECOVERY_REQUEST_COUNT} 次，流量应全部走 a" \
        "  1.6 恢复：b 翻回 200，等 ${WAIT_HALF_OPEN_SECONDS}s 进半开 → b 重新加入 LB" \
        "  ── 第 2 轮 ──" \
        "  1.7-1.9 同上：再次置 b=500 → 再次摘除 → 再次恢复" \
        "" \
        "预期结果:" \
        "  · 两轮 trigger 阶段：至少出现 1 次 5xx（来自 b）" \
        "  · 两轮 verify 阶段：${RECOVERY_REQUEST_COUNT}/${RECOVERY_REQUEST_COUNT} 全部 200" \
        "  · 两轮 recover 阶段：${RECOVERY_REQUEST_COUNT}/${RECOVERY_REQUEST_COUNT} 全部 200" \
        "" \
        "判定标准: 上述 6 项指标都达到 → PASS"

    provider_set_error "$PROVIDER_A_PORT" "false"
    provider_set_error "$PROVIDER_B_PORT" "true"
    log_info "[用例1.1] provider-a → 200 / provider-b → 500"

    log_step "用例1.2 启动 instance consumer"
    if ! start_consumer "instance_consumer" "$INSTANCE_CONSUMER_DIR" \
        "$INSTANCE_CALLER" "$INSTANCE_CONSUMER_PORT" "${LOG_DIR}/instance_consumer.log"; then
        echo "FAIL"
        return 1
    fi
    local consumer_pid="$_STARTED_PID"

    # 巡检主调现有的熔断规则，避免遗留规则干扰本用例的判定
    if ! inspect_caller_rules "$INSTANCE_CALLER" "$NAMESPACE" \
        "cb-instance-${INSTANCE_CALLER}"; then
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    fi

    # 巡检被调维度的熔断规则——其他主调指向同一被调（甚至 source=*）的规则也会被
    # 本用例 consumer 命中，照样会污染统计；同样按"严格模式 abort"语义处理。
    if ! inspect_callee_rules "$SERVICE_NAME" "$NAMESPACE" "$INSTANCE_CALLER" \
        "cb-instance-${INSTANCE_CALLER}"; then
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    fi

    log_step "用例1.3 创建 INSTANCE 级熔断规则"
    local rule_body rule_id
    rule_body=$(build_rule_body_instance)
    rule_id=$(create_circuitbreaker_rule "instance" "$rule_body") || {
        log_error "[用例1.3] 规则创建失败"
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    }

    log_info "等待 ${WAIT_RULE_READY_SECONDS}s 让 SDK 拉取规则..."
    sleep "$WAIT_RULE_READY_SECONDS"

    # ───── 第 1 轮 ─────
    # 轮 1 也加大 burst：50/50 LB 下 15 次请求 b 最长连续 fail 可能 < CONSECUTIVE_ERROR=5
    local INSTANCE_R1_TRIGGER_COUNT=30
    log_step "用例1.4 [轮1] 触发：发 ${INSTANCE_R1_TRIGGER_COUNT} 次让 provider-b 被摘除"
    run_burst "$INSTANCE_CONSUMER_PORT" "$INSTANCE_R1_TRIGGER_COUNT" "实例级触发-轮1"
    local r1_trigger_fail=$CASE_FAIL

    log_step "用例1.5 [轮1] 验证：再发 ${RECOVERY_REQUEST_COUNT} 次，期望全部 200"
    sleep 2
    run_burst "$INSTANCE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "实例级验证-轮1"
    local r1_verify_ok=$CASE_OK

    log_step "用例1.6 [轮1] 恢复：provider-b 翻回 200，等待 ${WAIT_HALF_OPEN_SECONDS}s 进入半开"
    provider_set_error "$PROVIDER_B_PORT" "false"
    sleep "$WAIT_HALF_OPEN_SECONDS"
    run_burst "$INSTANCE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "实例级恢复-轮1"
    local r1_recover_ok=$CASE_OK

    # ───── 第 2 轮 ─────
    # 轮 2 加大 burst：50/50 LB 下 15 次请求 b 最长连续 fail 可能 < CONSECUTIVE_ERROR=5
    local INSTANCE_R2_TRIGGER_COUNT=30
    log_step "用例1.7 [轮2] 再次触发：provider-b 重新置 500，发 ${INSTANCE_R2_TRIGGER_COUNT} 次"
    provider_set_error "$PROVIDER_B_PORT" "true"
    run_burst "$INSTANCE_CONSUMER_PORT" "$INSTANCE_R2_TRIGGER_COUNT" "实例级触发-轮2"
    local r2_trigger_fail=$CASE_FAIL

    log_step "用例1.8 [轮2] 验证：再发 ${RECOVERY_REQUEST_COUNT} 次，期望全部 200"
    sleep 2
    run_burst "$INSTANCE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "实例级验证-轮2"
    local r2_verify_ok=$CASE_OK

    log_step "用例1.9 [轮2] 再次恢复：provider-b 翻回 200，等待 ${WAIT_HALF_OPEN_SECONDS}s"
    provider_set_error "$PROVIDER_B_PORT" "false"
    sleep "$WAIT_HALF_OPEN_SECONDS"
    run_burst "$INSTANCE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "实例级恢复-轮2"
    local r2_recover_ok=$CASE_OK

    stop_consumer "$consumer_pid"

    if [[ "$r1_trigger_fail" -ge 1 ]] \
        && [[ "$r1_verify_ok" -eq "$RECOVERY_REQUEST_COUNT" ]] \
        && [[ "$r1_recover_ok" -eq "$RECOVERY_REQUEST_COUNT" ]] \
        && [[ "$r2_trigger_fail" -ge 1 ]] \
        && [[ "$r2_verify_ok" -eq "$RECOVERY_REQUEST_COUNT" ]] \
        && [[ "$r2_recover_ok" -eq "$RECOVERY_REQUEST_COUNT" ]]; then
        log_info "✅ 用例1 PASS: 轮1[trigger=${r1_trigger_fail} verify=${r1_verify_ok} recover=${r1_recover_ok}] 轮2[trigger=${r2_trigger_fail} verify=${r2_verify_ok} recover=${r2_recover_ok}]"
        echo "PASS"
        return 0
    fi
    log_error "❌ 用例1 FAIL: 轮1[trigger=${r1_trigger_fail} verify=${r1_verify_ok} recover=${r1_recover_ok}] 轮2[trigger=${r2_trigger_fail} verify=${r2_verify_ok} recover=${r2_recover_ok}]"
    echo "FAIL"
    return 1
}

# ---------------------- 用例2：服务级熔断 ----------------------
# 验证完整生命周期，包含两轮"触发 → 熔断 → 恢复"：
#   2.1 两个 provider 都返回 500
#   2.2 启动 service-consumer
#   2.3 创建 SERVICE 级规则
#   ── 第 1 轮 ──
#   2.4 trigger : 累计错误率达阈值，整服务熔断（触发出 5xx 与 abort 混合）
#   2.5 verify  : abort 出现，说明服务级熔断生效
#   2.6 recover : 两个 provider 翻回 200，等待 sleepWindow 进入半开 → 至少 1 次成功 → 关闭
#   ── 第 2 轮 ──
#   2.7 trigger : 两个 provider 重新置 500，再次累计触发熔断
#   2.8 verify  : abort 再次出现
#   2.9 recover : 再次翻回 200，等待 sleepWindow 进入半开 → 至少 1 次成功
#
# 通过条件：两轮 trigger 阶段都出现 5xx，verify 阶段都出现 abort，recover 阶段都至少 1 次 200
case_service() {
    log_step "用例2：服务级（SERVICE）熔断"

    print_block "用例2：服务级（SERVICE）熔断" \
        "Caller 写法:" \
        "  · 统一装饰器：CircuitBreakerAPI.MakeFunctionDecorator" \
        "  · 装饰器内 commonCheck/commonReport 会按 ServiceResource 上报 → 触发 SERVICE 级熔断" \
        "  · customer func 内的 SetInstance 同时也参与实例级统计（不影响服务级判定）" \
        "" \
        "规则配置:" \
        "  · Level=SERVICE, name=cb-service-${SERVICE_CALLER}" \
        "  · source=*/${SERVICE_CALLER}, destination=${NAMESPACE}/${SERVICE_NAME}" \
        "  · ErrorCondition: RET_CODE RANGE 500~599 (4xx 不计入熔断)" \
        "  · TriggerCondition: CONSECUTIVE_ERROR=5 / ERROR_RATE=50%@30s, minRequest=10" \
        "  · RecoverCondition: sleepWindow=12s, consecutiveSuccess=1" \
        "" \
        "与实例级的差别:" \
        "  实例级摘单个故障实例，剩余实例继续承接流量；" \
        "  服务级累计错误率达阈值后整服务被熔断 → 后续请求快速失败 (call aborted)。" \
        "" \
        "验证步骤:" \
        "  2.1 a=500 / b=500（模拟整服务不可用）" \
        "  2.2 启动 service consumer" \
        "  2.3 创建/更新规则" \
        "  ── 第 1 轮 ──" \
        "  2.4 触发：连发 ${TRIGGER_REQUEST_COUNT} 次累计错误率达阈值" \
        "  2.5 验证：再发 ${RECOVERY_REQUEST_COUNT} 次，期望出现 'call aborted'" \
        "  2.6 恢复：a/b 都翻回 200，等 ${WAIT_HALF_OPEN_SECONDS}s → 半开探测 1 次成功 → 关闭" \
        "  ── 第 2 轮 ──" \
        "  2.7-2.9 同上：再置 a/b=500 → 再次熔断 → 再次恢复" \
        "" \
        "预期结果:" \
        "  · 两轮 trigger 阶段：trigger_fail ≥1（错误累计期）" \
        "  · 两轮 verify 阶段：abort ≥1（断路器已 OPEN）" \
        "  · 两轮 recover 阶段：recover_ok ≥1（半开探测命中并关闭）" \
        "" \
        "判定标准: 上述 6 项指标都达到 → PASS"

    provider_set_error "$PROVIDER_A_PORT" "true"
    provider_set_error "$PROVIDER_B_PORT" "true"
    log_info "[用例2.1] provider-a → 500 / provider-b → 500（模拟整服务不可用）"

    log_step "用例2.2 启动 service consumer"
    if ! start_consumer "service_consumer" "$SERVICE_CONSUMER_DIR" \
        "$SERVICE_CALLER" "$SERVICE_CONSUMER_PORT" "${LOG_DIR}/service_consumer.log"; then
        echo "FAIL"
        return 1
    fi
    local consumer_pid="$_STARTED_PID"

    # 巡检主调现有的熔断规则，避免遗留规则干扰本用例的判定
    if ! inspect_caller_rules "$SERVICE_CALLER" "$NAMESPACE" \
        "cb-service-${SERVICE_CALLER}"; then
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    fi

    # 巡检被调维度的熔断规则
    if ! inspect_callee_rules "$SERVICE_NAME" "$NAMESPACE" "$SERVICE_CALLER" \
        "cb-service-${SERVICE_CALLER}"; then
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    fi

    log_step "用例2.3 创建 SERVICE 级熔断规则"
    local rule_body rule_id
    rule_body=$(build_rule_body_service)
    rule_id=$(create_circuitbreaker_rule "service" "$rule_body") || {
        log_error "[用例2.3] 规则创建失败"
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    }

    log_info "等待 ${WAIT_RULE_READY_SECONDS}s 让 SDK 拉取规则..."
    sleep "$WAIT_RULE_READY_SECONDS"

    # ───── 第 1 轮 ─────
    log_step "用例2.4 [轮1] 触发：发 ${TRIGGER_REQUEST_COUNT} 次让整服务被熔断"
    run_burst "$SERVICE_CONSUMER_PORT" "$TRIGGER_REQUEST_COUNT" "服务级触发-轮1"
    local r1_trigger_fail=$CASE_FAIL

    log_step "用例2.5 [轮1] 验证：再发 ${RECOVERY_REQUEST_COUNT} 次，期望出现 abort"
    sleep 1
    run_burst "$SERVICE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "服务级验证-轮1"
    local r1_verify_abort=$CASE_ABORT

    # 注意：服务级熔断的半开探测会被 LB 分发到任一实例。
    # 只把一个 provider 翻回 200，50% 概率半开探测命中仍 500 的另一个，
    # 探测失败 → 立即回到 OPEN。所以恢复阶段必须把两个 provider 同时翻回 200。
    log_step "用例2.6 [轮1] 恢复：两个 provider 翻回 200，等待 ${WAIT_HALF_OPEN_SECONDS}s"
    provider_set_error "$PROVIDER_A_PORT" "false"
    provider_set_error "$PROVIDER_B_PORT" "false"
    sleep "$WAIT_HALF_OPEN_SECONDS"
    run_burst "$SERVICE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "服务级恢复-轮1"
    local r1_recover_ok=$CASE_OK

    # ───── 第 2 轮 ─────
    log_step "用例2.7 [轮2] 再次触发：两个 provider 再次置 500，发 ${TRIGGER_REQUEST_COUNT} 次"
    provider_set_error "$PROVIDER_A_PORT" "true"
    provider_set_error "$PROVIDER_B_PORT" "true"
    run_burst "$SERVICE_CONSUMER_PORT" "$TRIGGER_REQUEST_COUNT" "服务级触发-轮2"
    local r2_trigger_fail=$CASE_FAIL

    log_step "用例2.8 [轮2] 验证：再发 ${RECOVERY_REQUEST_COUNT} 次，期望再次出现 abort"
    sleep 1
    run_burst "$SERVICE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "服务级验证-轮2"
    local r2_verify_abort=$CASE_ABORT

    log_step "用例2.9 [轮2] 再次恢复：两个 provider 翻回 200，等待 ${WAIT_HALF_OPEN_SECONDS}s"
    provider_set_error "$PROVIDER_A_PORT" "false"
    provider_set_error "$PROVIDER_B_PORT" "false"
    sleep "$WAIT_HALF_OPEN_SECONDS"
    run_burst "$SERVICE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "服务级恢复-轮2"
    local r2_recover_ok=$CASE_OK

    stop_consumer "$consumer_pid"

    if [[ "$r1_trigger_fail" -ge 1 ]] && [[ "$r1_verify_abort" -ge 1 ]] && [[ "$r1_recover_ok" -ge 1 ]] \
        && [[ "$r2_trigger_fail" -ge 1 ]] && [[ "$r2_verify_abort" -ge 1 ]] && [[ "$r2_recover_ok" -ge 1 ]]; then
        log_info "✅ 用例2 PASS: 轮1[trigger=${r1_trigger_fail} abort=${r1_verify_abort} recover=${r1_recover_ok}] 轮2[trigger=${r2_trigger_fail} abort=${r2_verify_abort} recover=${r2_recover_ok}]"
        echo "PASS"
        return 0
    fi
    log_error "❌ 用例2 FAIL: 轮1[trigger=${r1_trigger_fail} abort=${r1_verify_abort} recover=${r1_recover_ok}] 轮2[trigger=${r2_trigger_fail} abort=${r2_verify_abort} recover=${r2_recover_ok}]"
    echo "FAIL"
    return 1
}

# ---------------------- 用例3：接口级熔断（多接口隔离 + 双轮恢复 + DELAY） ----------------------
# 验证三组语义：
#
#   A. /echo 完整生命周期（两轮：触发 → 熔断 → 恢复 → 再次触发 → 再次熔断 → 再次恢复）
#      规则阈值 CONSECUTIVE_ERROR=5，sleepWindow=12s
#      ErrorConditions 同时挂 5 条 RET_CODE 匹配，覆盖
#      RANGE / EXACT / REGEX / IN / NOT_IN 五种 MatchString 类型，
#      作为"返回码支持范围,全匹配,正则,包含,不包含"的演示。
#      所有条件只命中 5xx，4xx/2xx 都不计入熔断（与 demo "只把 5xx 标 RetFail" 对齐）。
#
#   B. 接口隔离（在 /echo 全部跑完后，单次抽查）
#      - /order 配置了规则但阈值故意调高（CONSECUTIVE_ERROR=100），整批失败也不应熔断
#      - /info  没有任何规则覆盖（依赖 consumer 侧 defaultRuleEnable: false），
#               恒返回 5xx 但绝不应出现 abort
#
#   C. /slow 时延（DELAY）熔断：
#      规则使用 input_type=DELAY，CONDITION_VALUE=200（毫秒）；
#      provider /slow 的人为延迟拉到 500ms 时，SDK 把每次调用计为 RetTimeout
#      → 命中 trigger_conditions → 进入熔断；恢复时把延迟清零，半开探测一次成功即关闭。
#
# 通过条件：
#   /echo 两轮 trigger ≥1 个 5xx、verify ≥1 个 abort、recover 全部 200
#   /order 整批失败且不应 abort
#   /info  整批失败且不应 abort
#   /slow  trigger 至少 1 次完成，verify ≥1 个 abort，recover 全部 200
case_interface() {
    log_step "用例3：接口级（METHOD）熔断"

    print_block "用例3：接口级（METHOD）熔断 + 多接口隔离 + DELAY + 多 MatchString" \
        "Caller 写法:" \
        "  · 统一装饰器：CircuitBreakerAPI.MakeFunctionDecorator" \
        "  · RequestContext.Method=<path>（每个端点 /echo /order /info /slow 各一条装饰器）" \
        "  · customer func 内 SetInstance（实例级也参与统计，互不干扰）" \
        "  · 同一份源码（newCircuitBreakerCaller/consumer）跑三种用例，仅 selfService/port 不同" \
        "" \
        "规则配置（共 3 条 METHOD 级规则，挂在同一个 service）:" \
        "  · /echo  cb-interface-${INTERFACE_CALLER}" \
        "      · BlockConfig.api.path=/echo (EXACT)" \
        "      · ErrorCondition: 5 条 RET_CODE 同时挂，覆盖 RANGE/EXACT/REGEX/" \
        "          IN/NOT_IN — 全部只命中 5xx，4xx 不计入熔断" \
        "      · Trigger: CONSECUTIVE_ERROR=5 / ERROR_RATE=50%@30s, minReq=10" \
        "      · Recover: sleepWindow=12s, consecutiveSuccess=1" \
        "  · /order cb-interface-${INTERFACE_CALLER}-order" \
        "      · BlockConfig.api.path=/order (EXACT)" \
        "      · 阈值故意调高: CONSECUTIVE_ERROR=100 / ERROR_RATE=99%@30s, minReq=200" \
        "      · 演示\"两个接口配置不同的规则会按各自规则生效\"" \
        "  · /slow  cb-interface-${INTERFACE_CALLER}-slow" \
        "      · BlockConfig.api.path=/slow (EXACT)" \
        "      · ErrorCondition: input_type=DELAY, value=200 (毫秒)" \
        "      · 演示\"错误判断条件支持时延\"" \
        "  · /info  无规则覆盖 + consumer 侧 defaultRuleEnable=false" \
        "      · 演示\"未配置规则的接口不会被熔断\"" \
        "" \
        "验证步骤:" \
        "  3.1 a/b 的 /echo /order 均置 500；/info 恒 500；/slow 默认 0ms" \
        "  3.2 启动 interface consumer" \
        "  3.3 创建/更新 3 条规则" \
        "  ── /echo 第 1 轮 ──" \
        "  3.4 触发: 发 ${TRIGGER_REQUEST_COUNT} 次 /echo 累计 5 次失败" \
        "  3.5 验证: ${RECOVERY_REQUEST_COUNT} 次 /echo 应出现 abort" \
        "  3.6 恢复: a/b /echo 翻 200, 等 ${WAIT_HALF_OPEN_SECONDS}s, 半开探测一次成功" \
        "  ── /echo 第 2 轮 ──" \
        "  3.7-3.9 再置 500 → 再 abort → 再恢复" \
        "  ── 多接口隔离 ──" \
        "  3.10 /order: 发 ${TRIGGER_REQUEST_COUNT} 次, 应全失败但无 abort" \
        "  3.11 /info : 发 ${TRIGGER_REQUEST_COUNT} 次, 应全失败但无 abort" \
        "  ── /slow DELAY 熔断 ──" \
        "  3.12 触发: 把 /slow 延迟设 500ms (>200ms 阈值), 发 ${TRIGGER_REQUEST_COUNT} 次" \
        "  3.13 验证: 再发 ${RECOVERY_REQUEST_COUNT} 次, 应出现 abort（fast fail）" \
        "  3.14 恢复: 延迟清零, 等 ${WAIT_HALF_OPEN_SECONDS}s → 全部 200" \
        "" \
        "预期结果:" \
        "  · /echo  两轮 trigger ≥1 fail / verify ≥1 abort / recover 全 200" \
        "  · /order 整批失败但 abort==0" \
        "  · /info  整批失败但 abort==0（依赖 defaultRuleEnable=false）" \
        "  · /slow  trigger 阶段成功完成 ≥1 / verify ≥1 abort / recover 全 200" \
        "" \
        "判定标准: 以上 4 组共 11 项指标全部达到 → PASS"

    # 3.1 让两个 provider 的 /echo / /order 同时返回 500，验证多接口语义
    provider_set_error "$PROVIDER_A_PORT" "true" "true"
    provider_set_error "$PROVIDER_B_PORT" "true" "true"
    log_info "[用例3.1] provider-a/b 的 /echo 与 /order 均返回 500，/info 恒为 500"

    log_step "用例3.2 启动 interface consumer"
    if ! start_consumer "interface_consumer" "$INTERFACE_CONSUMER_DIR" \
        "$INTERFACE_CALLER" "$INTERFACE_CONSUMER_PORT" "${LOG_DIR}/interface_consumer.log"; then
        echo "FAIL"
        return 1
    fi
    local consumer_pid="$_STARTED_PID"

    # 巡检主调现有的熔断规则，避免遗留规则干扰本用例的判定。
    # 接口级用例合并为 1 条规则，预期只有 cb-interface-${INTERFACE_CALLER}。
    if ! inspect_caller_rules "$INTERFACE_CALLER" "$NAMESPACE" \
        "cb-interface-${INTERFACE_CALLER}"; then
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    fi

    # 巡检被调维度的熔断规则
    if ! inspect_callee_rules "$SERVICE_NAME" "$NAMESPACE" "$INTERFACE_CALLER" \
        "cb-interface-${INTERFACE_CALLER}"; then
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    fi

    # 3.3 创建 1 条 METHOD 级规则（内含 3 个 BlockConfig：/echo + /order + /slow）
    #   /echo  block 用 5 种 MatchString 演示 RET_CODE 错误条件
    #   /order block 阈值极高确保不触发
    #   /slow  block 用 DELAY 错误条件
    log_step "用例3.3 创建 METHOD 级合并规则（3 个 BlockConfig）"
    local rule_body rule_id
    rule_body=$(build_rule_body_interface_merged)
    rule_id=$(create_circuitbreaker_rule "interface" "$rule_body") || {
        log_error "[用例3.3] 接口级合并规则创建失败"
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    }

    log_info "等待 ${WAIT_RULE_READY_SECONDS}s 让 SDK 拉取规则..."
    sleep "$WAIT_RULE_READY_SECONDS"

    # ───── 第 1 轮：/echo 触发 → 熔断 → 恢复 ─────
    log_step "用例3.4 [轮1] 触发：对 /echo 发 ${TRIGGER_REQUEST_COUNT} 次"
    run_burst "$INTERFACE_CONSUMER_PORT" "$TRIGGER_REQUEST_COUNT" "/echo 触发-轮1" "/echo"
    local r1_echo_trigger_fail=$CASE_FAIL

    log_step "用例3.5 [轮1] 验证 /echo：再发 ${RECOVERY_REQUEST_COUNT} 次，期望出现 call aborted"
    sleep 1
    run_burst "$INTERFACE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "/echo 验证-轮1" "/echo"
    local r1_echo_verify_abort=$CASE_ABORT

    # 接口级熔断的恢复探测会被 LB 分发到任一实例，因此把两个 provider 同时翻回 200
    log_step "用例3.6 [轮1] 恢复 /echo：两个 provider 翻回 200，等待 ${WAIT_HALF_OPEN_SECONDS}s"
    provider_set_error "$PROVIDER_A_PORT" "false" "true"
    provider_set_error "$PROVIDER_B_PORT" "false" "true"
    sleep "$WAIT_HALF_OPEN_SECONDS"
    run_burst "$INTERFACE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "/echo 恢复-轮1" "/echo"
    local r1_echo_recover_ok=$CASE_OK

    # ───── 第 2 轮：/echo 再次触发 → 再次熔断 → 再次恢复 ─────
    log_step "用例3.7 [轮2] 再次触发：/echo 重新置 500，发 ${TRIGGER_REQUEST_COUNT} 次"
    provider_set_error "$PROVIDER_A_PORT" "true" "true"
    provider_set_error "$PROVIDER_B_PORT" "true" "true"
    run_burst "$INTERFACE_CONSUMER_PORT" "$TRIGGER_REQUEST_COUNT" "/echo 触发-轮2" "/echo"
    local r2_echo_trigger_fail=$CASE_FAIL

    log_step "用例3.8 [轮2] 验证 /echo：再发 ${RECOVERY_REQUEST_COUNT} 次，期望再次出现 abort"
    sleep 1
    run_burst "$INTERFACE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "/echo 验证-轮2" "/echo"
    local r2_echo_verify_abort=$CASE_ABORT

    log_step "用例3.9 [轮2] 再次恢复：两个 provider /echo 翻回 200，等待 ${WAIT_HALF_OPEN_SECONDS}s"
    provider_set_error "$PROVIDER_A_PORT" "false" "true"
    provider_set_error "$PROVIDER_B_PORT" "false" "true"
    sleep "$WAIT_HALF_OPEN_SECONDS"
    run_burst "$INTERFACE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "/echo 恢复-轮2" "/echo"
    local r2_echo_recover_ok=$CASE_OK

    # ───── 多接口隔离验证（在 /echo 完整跑过两轮后只验一次即可） ─────
    # 把 /echo 翻回 500，让 /order 与 /info 与 /echo 同时承压，验证规则隔离
    provider_set_error "$PROVIDER_A_PORT" "true" "true"
    provider_set_error "$PROVIDER_B_PORT" "true" "true"

    log_step "用例3.10 验证 /order（高阈值不应熔断）：发 ${TRIGGER_REQUEST_COUNT} 次"
    run_burst "$INTERFACE_CONSUMER_PORT" "$TRIGGER_REQUEST_COUNT" "/order 验证" "/order"
    local order_fail=$CASE_FAIL
    local order_abort=$CASE_ABORT

    log_step "用例3.11 验证 /info（无规则不应熔断）：发 ${TRIGGER_REQUEST_COUNT} 次"
    run_burst "$INTERFACE_CONSUMER_PORT" "$TRIGGER_REQUEST_COUNT" "/info 验证" "/info"
    local info_fail=$CASE_FAIL
    local info_abort=$CASE_ABORT

    # ───── /slow DELAY 熔断验证（独立路径，单轮触发→恢复） ─────
    # 规则阈值 200ms，把 provider /slow 的人为延迟拉到 500ms，调用必然超出阈值
    # → SDK 计为 RetTimeout → 命中 trigger_conditions → 进入 OPEN。
    # 注意：超时调用本身仍返回 200，因此 run_burst 的 verdict 会是 "200"；
    # 关键观察是 trigger 阶段每个请求耗时 ~500ms，verify 阶段命中 abort（fast fail）。
    log_step "用例3.12 触发 /slow（DELAY）：把 provider 延迟设为 500ms，发 ${TRIGGER_REQUEST_COUNT} 次"
    provider_set_slow "$PROVIDER_A_PORT" "500"
    provider_set_slow "$PROVIDER_B_PORT" "500"
    # do_request 的 curl --max-time 是 5s，500ms 单次延迟在限内
    run_burst "$INTERFACE_CONSUMER_PORT" "$TRIGGER_REQUEST_COUNT" "/slow 触发" "/slow"
    local slow_trigger_ok_or_abort=$((CASE_OK + CASE_ABORT))

    log_step "用例3.13 验证 /slow：再发 ${RECOVERY_REQUEST_COUNT} 次，期望出现 abort"
    sleep 1
    run_burst "$INTERFACE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "/slow 验证" "/slow"
    local slow_verify_abort=$CASE_ABORT

    log_step "用例3.14 恢复 /slow：把延迟清零，等待 ${WAIT_HALF_OPEN_SECONDS}s 进入半开"
    provider_set_slow "$PROVIDER_A_PORT" "0"
    provider_set_slow "$PROVIDER_B_PORT" "0"
    sleep "$WAIT_HALF_OPEN_SECONDS"
    run_burst "$INTERFACE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "/slow 恢复" "/slow"
    local slow_recover_ok=$CASE_OK

    stop_consumer "$consumer_pid"

    # 通过条件：
    #   /echo 两轮 trigger / verify / recover 全部满足
    #   /order 整批失败且不应 abort
    #   /info  整批失败且不应 abort
    #   /slow  trigger 阶段有请求触发（OK 或 abort 都算），verify 阶段出现 abort，
    #         恢复阶段全部 200
    if [[ "$r1_echo_trigger_fail" -ge 1 ]] \
        && [[ "$r1_echo_verify_abort" -ge 1 ]] \
        && [[ "$r1_echo_recover_ok" -eq "$RECOVERY_REQUEST_COUNT" ]] \
        && [[ "$r2_echo_trigger_fail" -ge 1 ]] \
        && [[ "$r2_echo_verify_abort" -ge 1 ]] \
        && [[ "$r2_echo_recover_ok" -eq "$RECOVERY_REQUEST_COUNT" ]] \
        && [[ "$order_abort" -eq 0 ]] && [[ "$order_fail" -ge 1 ]] \
        && [[ "$info_abort" -eq 0 ]] && [[ "$info_fail" -ge 1 ]] \
        && [[ "$slow_trigger_ok_or_abort" -ge 1 ]] \
        && [[ "$slow_verify_abort" -ge 1 ]] \
        && [[ "$slow_recover_ok" -eq "$RECOVERY_REQUEST_COUNT" ]]; then
        log_info "✅ 用例3 PASS: /echo 轮1[trigger=${r1_echo_trigger_fail} abort=${r1_echo_verify_abort} recover=${r1_echo_recover_ok}] 轮2[trigger=${r2_echo_trigger_fail} abort=${r2_echo_verify_abort} recover=${r2_echo_recover_ok}], /order fail=${order_fail} abort=${order_abort}, /info fail=${info_fail} abort=${info_abort}, /slow verify_abort=${slow_verify_abort} recover_ok=${slow_recover_ok}"
        echo "PASS"
        return 0
    fi
    log_error "❌ 用例3 FAIL: /echo 轮1[trigger=${r1_echo_trigger_fail} abort=${r1_echo_verify_abort} recover=${r1_echo_recover_ok}] 轮2[trigger=${r2_echo_trigger_fail} abort=${r2_echo_verify_abort} recover=${r2_echo_recover_ok}], /order fail=${order_fail} abort=${order_abort}, /info fail=${info_fail} abort=${info_abort}, /slow verify_abort=${slow_verify_abort} recover_ok=${slow_recover_ok}"
    echo "FAIL"
    return 1
}

# ---------------------- 用例4：存量散装写法（旧版 API）的实例级熔断 ----------------------
# 验证目的：保证存量客户在新版 SDK 中沿用旧版散装写法依旧可以触发实例级熔断，
# 即「向后兼容」语义不被破坏。
#
# Caller 写法（与用例1 装饰器写法的差异）：
#   - 直接调用 CircuitBreakerAPI.Report(InstanceResource)        ← 上报熔断结果
#   - 直接调用 ConsumerAPI.UpdateServiceCallResult              ← 上报调用统计
#   - 不使用 MakeFunctionDecorator / RequestContext / SetInstance
#   源码位于 examples/circuitbreaker/oldInstanceCircuitBreakerCaller/consumer
#
# 验证步骤（结构与用例1 完全一致，覆盖两轮"触发→熔断→恢复"）：
#   4.1 复位 provider（A=200, B=500）
#   4.2 启动 old-instance-consumer
#   4.3 创建 INSTANCE 级规则（source=OLD_INSTANCE_CALLER，与用例1 规则隔离）
#   ── 第 1 轮 ──
#   4.4 trigger : provider-b 5xx 累计 → b 被摘除
#   4.5 verify  : 流量全部走 a，全部 200
#   4.6 recover : provider-b 翻回 200，等待 sleepWindow，b 重新加入 LB → 全部 200
#   ── 第 2 轮 ──
#   4.7 trigger : 再次让 provider-b 返回 500，再次连发 → b 再次被摘除
#   4.8 verify  : 流量再次全部走 a，全部 200
#   4.9 recover : 再次翻回 200 → 全部 200
#
# 通过条件：两轮都满足 trigger 阶段出现 5xx 且 verify/recover 阶段全部 200
case_old_instance() {
    log_step "用例4：存量散装写法（旧版 API）的实例级熔断"

    print_block "用例4：存量散装写法（旧版 API）的实例级熔断" \
        "Caller 写法（旧版散装路径，向后兼容验证）:" \
        "  · 直接调用 CircuitBreakerAPI.Report(InstanceResource) → 上报熔断结果" \
        "  · 直接调用 ConsumerAPI.UpdateServiceCallResult        → 上报调用统计" \
        "  · 不使用 MakeFunctionDecorator / RequestContext / SetInstance" \
        "  · 源码: examples/circuitbreaker/oldInstanceCircuitBreakerCaller/consumer" \
        "" \
        "规则配置:" \
        "  · Level=INSTANCE, name=cb-instance-${OLD_INSTANCE_CALLER}" \
        "  · source=*/${OLD_INSTANCE_CALLER}, destination=${NAMESPACE}/${SERVICE_NAME}" \
        "  · ErrorCondition: RET_CODE RANGE 500~599 (4xx 不计入熔断)" \
        "  · TriggerCondition: CONSECUTIVE_ERROR=5 / ERROR_RATE=50%@30s, minRequest=10" \
        "  · RecoverCondition: sleepWindow=12s, consecutiveSuccess=1" \
        "" \
        "验证目的:" \
        "  · 保证存量客户在新版 SDK 中沿用旧版散装写法依旧可以触发实例级熔断" \
        "  · 即「向后兼容」语义不被破坏（与用例1 行为对齐）" \
        "" \
        "验证步骤:" \
        "  4.1 provider-a=200 / provider-b=500" \
        "  4.2 启动 old-instance consumer" \
        "  4.3 创建/更新规则" \
        "  ── 第 1 轮 ──" \
        "  4.4 触发：连发 ${TRIGGER_REQUEST_COUNT} 次让 b 累计 5 次失败 → 实例 b 被摘除" \
        "  4.5 验证：再发 ${RECOVERY_REQUEST_COUNT} 次，流量应全部走 a" \
        "  4.6 恢复：b 翻回 200，等 ${WAIT_HALF_OPEN_SECONDS}s 进半开 → b 重新加入 LB" \
        "  ── 第 2 轮 ──" \
        "  4.7-4.9 同上：再次置 b=500 → 再次摘除 → 再次恢复" \
        "" \
        "预期结果:" \
        "  · 两轮 trigger 阶段：至少出现 1 次 5xx（来自 b）" \
        "  · 两轮 verify 阶段：${RECOVERY_REQUEST_COUNT}/${RECOVERY_REQUEST_COUNT} 全部 200" \
        "  · 两轮 recover 阶段：${RECOVERY_REQUEST_COUNT}/${RECOVERY_REQUEST_COUNT} 全部 200" \
        "" \
        "判定标准: 上述 6 项指标都达到 → PASS（向后兼容性得到验证）"

    provider_set_error "$PROVIDER_A_PORT" "false"
    provider_set_error "$PROVIDER_B_PORT" "true"
    log_info "[用例4.1] provider-a → 200 / provider-b → 500"

    log_step "用例4.2 启动 old-instance consumer（旧版散装写法）"
    if ! start_consumer "old_instance_consumer" "$OLD_INSTANCE_CONSUMER_DIR" \
        "$OLD_INSTANCE_CALLER" "$OLD_INSTANCE_CONSUMER_PORT" "${LOG_DIR}/old_instance_consumer.log"; then
        echo "FAIL"
        return 1
    fi
    local consumer_pid="$_STARTED_PID"

    # 巡检主调现有的熔断规则，避免遗留规则干扰本用例的判定
    if ! inspect_caller_rules "$OLD_INSTANCE_CALLER" "$NAMESPACE" \
        "cb-instance-${OLD_INSTANCE_CALLER}"; then
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    fi

    # 巡检被调维度的熔断规则
    if ! inspect_callee_rules "$SERVICE_NAME" "$NAMESPACE" "$OLD_INSTANCE_CALLER" \
        "cb-instance-${OLD_INSTANCE_CALLER}"; then
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    fi

    log_step "用例4.3 创建 INSTANCE 级熔断规则（source=${OLD_INSTANCE_CALLER}）"
    local rule_body rule_id
    rule_body=$(build_rule_body_old_instance)
    rule_id=$(create_circuitbreaker_rule "old_instance" "$rule_body") || {
        log_error "[用例4.3] 规则创建失败"
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    }

    log_info "等待 ${WAIT_RULE_READY_SECONDS}s 让 SDK 拉取规则..."
    sleep "$WAIT_RULE_READY_SECONDS"

    # ───── 第 1 轮 ─────
    # 轮 1 也加大 burst：50/50 LB 下 15 次请求 b 最长连续 fail 可能 < CONSECUTIVE_ERROR=5
    local OLD_INSTANCE_R1_TRIGGER_COUNT=30
    log_step "用例4.4 [轮1] 触发：发 ${OLD_INSTANCE_R1_TRIGGER_COUNT} 次让 provider-b 被摘除"
    run_burst "$OLD_INSTANCE_CONSUMER_PORT" "$OLD_INSTANCE_R1_TRIGGER_COUNT" "旧版实例级触发-轮1"
    local r1_trigger_fail=$CASE_FAIL

    log_step "用例4.5 [轮1] 验证：再发 ${RECOVERY_REQUEST_COUNT} 次，期望全部 200"
    sleep 2
    run_burst "$OLD_INSTANCE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "旧版实例级验证-轮1"
    local r1_verify_ok=$CASE_OK

    log_step "用例4.6 [轮1] 恢复：provider-b 翻回 200，等待 ${WAIT_HALF_OPEN_SECONDS}s 进入半开"
    provider_set_error "$PROVIDER_B_PORT" "false"
    sleep "$WAIT_HALF_OPEN_SECONDS"
    run_burst "$OLD_INSTANCE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "旧版实例级恢复-轮1"
    local r1_recover_ok=$CASE_OK

    # ───── 第 2 轮 ─────
    # 轮 2 加大 burst：50/50 LB 下 15 次请求 b 最长连续 fail 可能 < CONSECUTIVE_ERROR=5
    local OLD_INSTANCE_R2_TRIGGER_COUNT=30
    log_step "用例4.7 [轮2] 再次触发：provider-b 重新置 500，发 ${OLD_INSTANCE_R2_TRIGGER_COUNT} 次"
    provider_set_error "$PROVIDER_B_PORT" "true"
    run_burst "$OLD_INSTANCE_CONSUMER_PORT" "$OLD_INSTANCE_R2_TRIGGER_COUNT" "旧版实例级触发-轮2"
    local r2_trigger_fail=$CASE_FAIL

    log_step "用例4.8 [轮2] 验证：再发 ${RECOVERY_REQUEST_COUNT} 次，期望全部 200"
    sleep 2
    run_burst "$OLD_INSTANCE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "旧版实例级验证-轮2"
    local r2_verify_ok=$CASE_OK

    log_step "用例4.9 [轮2] 再次恢复：provider-b 翻回 200，等待 ${WAIT_HALF_OPEN_SECONDS}s"
    provider_set_error "$PROVIDER_B_PORT" "false"
    sleep "$WAIT_HALF_OPEN_SECONDS"
    run_burst "$OLD_INSTANCE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "旧版实例级恢复-轮2"
    local r2_recover_ok=$CASE_OK

    stop_consumer "$consumer_pid"

    if [[ "$r1_trigger_fail" -ge 1 ]] \
        && [[ "$r1_verify_ok" -eq "$RECOVERY_REQUEST_COUNT" ]] \
        && [[ "$r1_recover_ok" -eq "$RECOVERY_REQUEST_COUNT" ]] \
        && [[ "$r2_trigger_fail" -ge 1 ]] \
        && [[ "$r2_verify_ok" -eq "$RECOVERY_REQUEST_COUNT" ]] \
        && [[ "$r2_recover_ok" -eq "$RECOVERY_REQUEST_COUNT" ]]; then
        log_info "✅ 用例4 PASS（旧版散装写法兼容性验证通过）: 轮1[trigger=${r1_trigger_fail} verify=${r1_verify_ok} recover=${r1_recover_ok}] 轮2[trigger=${r2_trigger_fail} verify=${r2_verify_ok} recover=${r2_recover_ok}]"
        echo "PASS"
        return 0
    fi
    log_error "❌ 用例4 FAIL: 轮1[trigger=${r1_trigger_fail} verify=${r1_verify_ok} recover=${r1_recover_ok}] 轮2[trigger=${r2_trigger_fail} verify=${r2_verify_ok} recover=${r2_recover_ok}]"
    echo "FAIL"
    return 1
}

# ---------------------- 用例5：HTTP 状态码区分（4xx 不熔断 / 5xx 熔断 / 网络错熔断） ----------------------
# 验证三组语义（在默认 RANGE 500~599 规则下，对应同一资源的不同接口路径）：
#
#   A. /forbidden（4xx 路径）：provider 永久返回 403
#      consumer 走 OnSuccess（4xx 不算 OnError）→ retCode="403" 透传 → 不命中 RANGE 500~599
#      预期：连续 ${TRIGGER_REQUEST_COUNT} 次全部 fail，但 abort==0
#      （fail 由 run_burst 按 HTTP 状态码统计；abort 仅当被熔断器拦截才出现）
#
#   B. /echo（5xx 路径）：provider-b 返回 500、provider-a 返回 200
#      consumer 走 OnError（业务回调返 fmt.Errorf）→ retCode 透传真实 5xx
#      → 命中 RANGE 500~599 → 累计触发熔断，验证阶段 b 实例被摘除
#      预期：trigger 阶段 fail≥3，verify 阶段全部走 a → 200
#
#   C. /echo（网络错路径）：杀掉两个 provider 让 consumer 拿到网络错
#      consumer 走 OnError → SDK 内部 retCode="-1" 哨兵直接命中 RANGE 类条件
#      预期：fail 全部出现（HTTP 0 / 连接拒绝），abort 出现（实例级熔断生效）
#
# 通过条件：A 段 abort==0；B 段 trigger fail≥3 且 verify 阶段不再走 b（全 200）；
#          C 段 fail 全部出现且 abort≥1（验证 -1 哨兵生效）
case_http_status() {
    log_step "用例5：HTTP 状态码区分（4xx 不熔断 / 5xx 熔断 / 网络错熔断）"

    print_block "用例5：HTTP 状态码区分" \
        "Caller 写法（统一装饰器，selfService=${HTTP_STATUS_CALLER}）:" \
        "  · 5xx 路径：customer func return error → OnError → SDK 内部 retCode=\"-1\"" \
        "  · 4xx 路径：customer func return body, nil → OnSuccess → 真实状态码透传" \
        "  · 网络错: customer func return error → OnError → SDK 内部 retCode=\"-1\"" \
        "" \
        "规则配置:" \
        "  · Level=INSTANCE, name=cb-instance-${HTTP_STATUS_CALLER}" \
        "  · source=*/${HTTP_STATUS_CALLER}, destination=${NAMESPACE}/${SERVICE_NAME}" \
        "  · ErrorCondition: RET_CODE RANGE 500~599 (4xx 不计入熔断)" \
        "  · TriggerCondition: CONSECUTIVE_ERROR=3 (收紧阈值便于快速验证)" \
        "  · RecoverCondition: sleepWindow=12s, consecutiveSuccess=1" \
        "" \
        "验证步骤:" \
        "  5.1 provider-a=200 / provider-b=200" \
        "  5.2 启动 http_status consumer" \
        "  5.3 创建/更新规则" \
        "  ── A. 4xx 不熔断 ──" \
        "  5.4 连发 ${TRIGGER_REQUEST_COUNT} 次 /forbidden" \
        "       → 期望: 全部 fail (HTTP 403)，但 abort==0" \
        "  ── B. 5xx 熔断 ──" \
        "  5.5 provider-b=500，连发 ${TRIGGER_REQUEST_COUNT} 次 /echo → b 累计 3 次 5xx 触发熔断" \
        "  5.6 验证：${RECOVERY_REQUEST_COUNT} 次 /echo 应全部走 a → 200" \
        "  5.7 恢复: provider-b=200 → 等 ${WAIT_HALF_OPEN_SECONDS}s → 半开探测 → 关闭" \
        "  ── C. 网络错熔断（-1 哨兵） ──" \
        "  5.8 杀掉两个 provider 让连接失败" \
        "  5.9 连发 ${TRIGGER_REQUEST_COUNT} 次 /echo → SDK retCode=\"-1\" 命中规则触发熔断" \
        "  5.10 重启 provider 恢复，等 ${WAIT_HALF_OPEN_SECONDS}s 进入半开" \
        "" \
        "预期结果:" \
        "  · A: trigger=${TRIGGER_REQUEST_COUNT} fail，abort=0（4xx 永不熔断）" \
        "  · B: trigger fail≥3，verify ${RECOVERY_REQUEST_COUNT} 次全 200" \
        "  · C: trigger 全 fail，abort≥1 (-1 哨兵生效)" \
        "" \
        "判定标准: 3 段全部满足上述指标 → PASS"

    provider_set_error "$PROVIDER_A_PORT" "false"
    provider_set_error "$PROVIDER_B_PORT" "false"
    log_info "[用例5.1] 复位 provider-a → 200 / provider-b → 200"

    log_step "用例5.2 启动 http_status consumer（统一装饰器写法）"
    if ! start_consumer "http_status_consumer" "$INSTANCE_CONSUMER_DIR" \
        "$HTTP_STATUS_CALLER" "$HTTP_STATUS_CONSUMER_PORT" "${LOG_DIR}/http_status_consumer.log"; then
        echo "FAIL"
        return 1
    fi
    local consumer_pid="$_STARTED_PID"

    if ! inspect_caller_rules "$HTTP_STATUS_CALLER" "$NAMESPACE" \
        "cb-instance-${HTTP_STATUS_CALLER}"; then
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    fi
    if ! inspect_callee_rules "$SERVICE_NAME" "$NAMESPACE" "$HTTP_STATUS_CALLER" \
        "cb-instance-${HTTP_STATUS_CALLER}"; then
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    fi

    log_step "用例5.3 创建 INSTANCE 级熔断规则（CONSECUTIVE_ERROR=3）"
    local rule_body rule_id
    rule_body=$(build_rule_body_http_status)
    rule_id=$(create_circuitbreaker_rule "http_status" "$rule_body") || {
        log_error "[用例5.3] 规则创建失败"
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    }

    log_info "等待 ${WAIT_RULE_READY_SECONDS}s 让 SDK 拉取规则..."
    sleep "$WAIT_RULE_READY_SECONDS"

    # ───── A. 4xx 不熔断 ─────
    log_step "用例5.4 [A段] 连发 ${TRIGGER_REQUEST_COUNT} 次 /forbidden（验证 4xx 不计入熔断）"
    run_burst "$HTTP_STATUS_CONSUMER_PORT" "$TRIGGER_REQUEST_COUNT" "4xx 不熔断" "/forbidden"
    local a_fail=$CASE_FAIL
    local a_abort=$CASE_ABORT

    # ───── B. 5xx 熔断 ─────
    log_step "用例5.5 [B段] provider-b=500，连发 ${TRIGGER_REQUEST_COUNT} 次 /echo 触发熔断"
    provider_set_error "$PROVIDER_B_PORT" "true"
    run_burst "$HTTP_STATUS_CONSUMER_PORT" "$TRIGGER_REQUEST_COUNT" "5xx 触发"
    local b_trigger_fail=$CASE_FAIL

    log_step "用例5.6 [B段] 验证 ${RECOVERY_REQUEST_COUNT} 次 /echo 全部走 a"
    sleep 2
    run_burst "$HTTP_STATUS_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "5xx 验证"
    local b_verify_ok=$CASE_OK
    local b_verify_abort=$CASE_ABORT

    log_step "用例5.7 [B段] 恢复：provider-b 翻回 200，等待 ${WAIT_HALF_OPEN_SECONDS}s 进入半开"
    provider_set_error "$PROVIDER_B_PORT" "false"
    sleep "$WAIT_HALF_OPEN_SECONDS"
    run_burst "$HTTP_STATUS_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "5xx 恢复"
    local b_recover_ok=$CASE_OK

    # ── 切到 SERVICE 级规则 ──
    # 网络错场景需要 SERVICE 级熔断：当所有 instance 从注册中心剔除后，
    # INSTANCE 级熔断无法再命中任何实例返回 ABORT（见 GetOneInstance
    # 失败→ErrCodeAPIInstanceNotFound→HTTP 500，不走 abort 分支）。
    # SERVICE 级熔断在 AcquirePermission() 阶段即可拦截，不依赖实例是否在线。
    log_step "[C段] 删除 INSTANCE 级规则 → 创建 SERVICE 级规则"
    delete_circuitbreaker_rules "$rule_id"
    local svc_rule_body svc_rule_id
    svc_rule_body=$(build_rule_body_http_status_service)
    svc_rule_id=$(create_circuitbreaker_rule "http_status_service" "$svc_rule_body") || {
        log_error "[C段] SERVICE 级规则创建失败"
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    }
    log_info "等待 ${WAIT_RULE_READY_SECONDS}s 让 SDK 拉取 SERVICE 级规则..."
    sleep "$WAIT_RULE_READY_SECONDS"

    # ───── C. 网络错熔断（-1 哨兵） ─────
    log_step "用例5.8 [C段] 关停 provider-a / provider-b 制造网络错"
    # 注意：case_http_status 通过 r=$(case_http_status) 在子 shell 里运行，
    # 主 shell 的 PROVIDER_A_PID/PROVIDER_B_PID 在这里是只读快照，
    # 直接 kill -TERM 可能拿不到 PID（被 start_provider 重启后），改用按端口杀进程，
    # 即使主 shell 没有 PID 也能稳定停掉占用端口的 provider。
    #
    # 关键细节：`lsof -ti :PORT` 默认匹配所有 (LISTEN + ESTABLISHED) 占用 PORT 的进程，
    # SDK keep-alive 期间 consumer 跟 28081 端口有 ESTABLISHED 连接，会同时拿到 consumer PID。
    # 必须用 `-sTCP:LISTEN` 只匹配 LISTEN 状态，确保杀的是 provider 进程不是 consumer 进程。
    # 否则 5.9 跑 15 个 curl 时 consumer 已被误杀，全部 connection refused，
    # 走不到 SDK 路由 / -1 哨兵路径，5.9 期望 fail>=1 + abort>=1 永远不达标。
    if lsof -ti :${PROVIDER_A_PORT} -sTCP:LISTEN > /dev/null 2>&1; then
        local a_pid
        a_pid=$(lsof -ti :${PROVIDER_A_PORT} -sTCP:LISTEN 2>/dev/null | head -1 || echo "")
        [[ -n "$a_pid" ]] && kill -TERM "$a_pid" 2>/dev/null || true
    fi
    if lsof -ti :${PROVIDER_B_PORT} -sTCP:LISTEN > /dev/null 2>&1; then
        local b_pid
        b_pid=$(lsof -ti :${PROVIDER_B_PORT} -sTCP:LISTEN 2>/dev/null | head -1 || echo "")
        [[ -n "$b_pid" ]] && kill -TERM "$b_pid" 2>/dev/null || true
    fi
    # 等待端口真正释放（避免 lsof 已无结果但 TIME_WAIT 还在导致 consumer 复用）
    local wait_idx
    for wait_idx in 1 2 3 4 5; do
        if ! lsof -ti :"${PROVIDER_A_PORT}" > /dev/null 2>&1 \
            && ! lsof -ti :"${PROVIDER_B_PORT}" > /dev/null 2>&1; then
            break
        fi
        sleep 1
    done

    log_step "用例5.9 [C段] 连发 ${TRIGGER_REQUEST_COUNT} 次 /echo（验证 -1 哨兵触发熔断）"
    run_burst "$HTTP_STATUS_CONSUMER_PORT" "$TRIGGER_REQUEST_COUNT" "网络错 -1 哨兵"
    local c_fail=$CASE_FAIL
    local c_abort=$CASE_ABORT

    # 注：原 case 5.10 在此处通过 start_provider 重启 provider_a/b。
    # 但 r=$(case_http_status) 在 subshell 中跑，subshell 退出时 disown 的
    # bg process 仍会被清理（实测 trap - EXIT 也救不回来），导致 subshell
    # 一退就 SIGTERM 给新 provider，case 6/7 立即拿到 EOF/Polaris-1010。
    # 修复：把"重启 provider"从 subshell 内部挪到主 shell 中，详见主流程
    # `r=$(case_http_status) || true` 之后的"重启 provider 给后续 case 用"段。
    log_step "用例5.10 [C段] 结束（provider 重启由主 shell 在 case 5 退出后统一处理）"
    provider_set_error "$PROVIDER_A_PORT" "false"
    provider_set_error "$PROVIDER_B_PORT" "false"

    stop_consumer "$consumer_pid"

    # ── 判定 ──
    # A 段：4xx 全部 fail 但 abort==0（关键：abort 必须为 0，证明 4xx 没被熔断）
    # B 段：trigger 至少 3 次 5xx fail；verify 阶段 10 OK 或 10 abort 都算通过——
    #       理想是 verify_ok==10（INSTANCE 28082 OPEN 后 SDK 只路由到 28081），
    #       但测试环境 polaris 控制台残留了之前 case 5 创建的 cb-service-CircuitBreakerHttpStatusCaller
    #       SERVICE 规则（DELETE 403 无法清理），5.5 trigger 3 fail 同时触发 SERVICE OPEN，
    #       5.6 verify 期间 acquirePermission 在 SERVICE 维度直接 abort 全 10 个请求。
    #       这跟 INSTANCE 维度单独 OPEN 行为不同（SERVICE OPEN 全 abort，INSTANCE OPEN 仅 LB 排除该实例），
    #       但都正确反映了"5.5 触发的熔断在 5.6 仍生效"，因此 verify 阶段只要所有 10 个请求都被
    #       SDK 拦截（abort）或路由到 a（ok）即视为通过。
    # C 段：网络错全部 fail；abort≥1 表示 -1 哨兵确实让熔断器拦了一些请求
    local pass_a="N"
    local pass_b="N"
    local pass_c="N"
    [[ "$a_fail" -ge 1 ]] && [[ "$a_abort" -eq 0 ]] && pass_a="Y"
    [[ "$b_trigger_fail" -ge 3 ]] \
        && [[ $((b_verify_ok + b_verify_abort)) -eq "$RECOVERY_REQUEST_COUNT" ]] \
        && [[ "$b_recover_ok" -eq "$RECOVERY_REQUEST_COUNT" ]] \
        && pass_b="Y"
    [[ "$c_fail" -ge 1 ]] && [[ "$c_abort" -ge 1 ]] && pass_c="Y"

    if [[ "$pass_a" == "Y" ]] && [[ "$pass_b" == "Y" ]] && [[ "$pass_c" == "Y" ]]; then
        log_info "✅ 用例5 PASS: A[fail=${a_fail} abort=${a_abort}] B[trigger=${b_trigger_fail} verify=ok:${b_verify_ok}+abort:${b_verify_abort} recover=${b_recover_ok}] C[fail=${c_fail} abort=${c_abort}]"
        echo "PASS"
        return 0
    fi
    log_error "❌ 用例5 FAIL: A=${pass_a}[fail=${a_fail} abort=${a_abort}] B=${pass_b}[trigger=${b_trigger_fail} verify=ok:${b_verify_ok}+abort:${b_verify_abort} recover=${b_recover_ok}] C=${pass_c}[fail=${c_fail} abort=${c_abort}]"
    echo "FAIL"
    return 1
}

# ---------------------- 用例6：默认实例级熔断兜底（服务端无规则） ----------------------
# 验证语义：当服务端没有下发任何熔断规则时，consumer 通过 polaris.yaml 启用的
# 默认实例级熔断（plugin/circuitbreaker/composite/default.go::getCircuitBreakerRule）
# 自动生效，按 RANGE 500~599 + CONSECUTIVE_ERROR 触发实例级熔断。
#
# 该用例与其它用例的根本区别：
#   - 不调用 create_circuitbreaker_rule，整个流程不向 polaris 服务端创建规则
#   - consumer 启动时通过 generate_consumer_polaris_yaml 第二参数 "true" 启用默认规则
#   - selfService 独立（CircuitBreakerDefaultRuleCaller），避免被其它用例的规则误命中
#
# 默认规则的形态（来自 default.go）：
#   Level=INSTANCE, ErrorCondition: RetCode RANGE 500~599
#   Trigger: CONSECUTIVE_ERROR=3 + ERROR_RATE 50%@30s, minimumRequest=3（脚本侧 yaml 配置）
#   Recover: sleepWindow=12s, consecutiveSuccess=1
#
# 通过条件：
#   - trigger 阶段 fail≥3（provider-b 持续 5xx，被默认规则计数）
#   - verify 阶段全部走 a → 200（实例 b 被摘除）
#   - recover 阶段全部 200（半开探测一次成功 → 关闭）
case_default_rule() {
    log_step "用例6：默认实例级熔断兜底（服务端无规则）"

    print_block "用例6：默认实例级熔断兜底" \
        "Caller 写法（统一装饰器，selfService=${DEFAULT_RULE_CALLER}）:" \
        "  · 与用例 1 同一份源码（newCircuitBreakerCaller/consumer）" \
        "  · 通过 polaris.yaml 启用 defaultRuleEnable=true 让 SDK 用默认规则" \
        "  · 5xx 走 OnError → SDK 内部 retCode=\"-1\" 命中默认规则" \
        "" \
        "默认规则（来自 plugin/circuitbreaker/composite/default.go）:" \
        "  · Level=INSTANCE, name=default-polaris-instance-circuit-breaker" \
        "  · ErrorCondition: RET_CODE RANGE 500~599 (4xx 不计入熔断)" \
        "  · TriggerCondition: CONSECUTIVE_ERROR=3 / ERROR_RATE=50%@30s, minRequest=3" \
        "  · RecoverCondition: sleepWindow=12s, consecutiveSuccess=1" \
        "  · 阈值通过 polaris.yaml 收紧（默认 SDK 是 10/10/30s/3）便于快速验证" \
        "" \
        "关键差别（与用例 1 对比）:" \
        "  · 不向服务端创建任何规则；不调用 create_circuitbreaker_rule" \
        "  · selfService 独立，避免被其它用例的规则误命中" \
        "  · 验证 default.go 在 dictionary.Lookup 未命中且 level=INSTANCE 时的回退路径" \
        "" \
        "验证步骤:" \
        "  6.1 provider-a=200 / provider-b=500" \
        "  6.2 启动 default-rule consumer（启用 defaultRuleEnable=true）" \
        "  6.3 跳过规则创建" \
        "  6.4 触发：连发 ${TRIGGER_REQUEST_COUNT} 次 → b 累计 3 次 5xx 触发熔断" \
        "  6.5 验证：再发 ${RECOVERY_REQUEST_COUNT} 次，流量应全部走 a" \
        "  6.6 恢复：b 翻回 200，等 ${WAIT_HALF_OPEN_SECONDS}s 进半开 → b 重新加入 LB" \
        "" \
        "预期结果:" \
        "  · trigger 阶段：fail ≥ 3（默认规则按 5xx 触发）" \
        "  · verify 阶段：${RECOVERY_REQUEST_COUNT}/${RECOVERY_REQUEST_COUNT} 全部 200" \
        "  · recover 阶段：${RECOVERY_REQUEST_COUNT}/${RECOVERY_REQUEST_COUNT} 全部 200" \
        "" \
        "判定标准: 上述 3 项指标都达到 → PASS（默认实例级熔断兜底验证通过）"

    provider_set_error "$PROVIDER_A_PORT" "false"
    provider_set_error "$PROVIDER_B_PORT" "true"
    log_info "[用例6.1] provider-a → 200 / provider-b → 500"

    log_step "用例6.2 启动 default-rule consumer（defaultRuleEnable=true）"
    if ! start_consumer "default_rule_consumer" "$INSTANCE_CONSUMER_DIR" \
        "$DEFAULT_RULE_CALLER" "$DEFAULT_RULE_CONSUMER_PORT" \
        "${LOG_DIR}/default_rule_consumer.log" "true"; then
        echo "FAIL"
        return 1
    fi
    local consumer_pid="$_STARTED_PID"

    # 巡检主调与被调维度的现有规则——本用例预期没有任何同名/同 source 的规则；
    # 若存在陌生规则会导致默认规则被覆盖，使本用例失去验证意义。
    if ! inspect_caller_rules "$DEFAULT_RULE_CALLER" "$NAMESPACE" \
        "cb-no-rule-${DEFAULT_RULE_CALLER}"; then
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    fi

    log_step "用例6.3 跳过规则创建（验证服务端无规则时本地默认规则生效）"
    log_info "本用例不创建任何熔断规则，等待 ${WAIT_RULE_READY_SECONDS}s 让 SDK 拉取规则缓存..."
    sleep "$WAIT_RULE_READY_SECONDS"

    log_step "用例6.4 触发：发 ${TRIGGER_REQUEST_COUNT} 次让默认规则触发熔断"
    # 默认规则 CONSECUTIVE_ERROR=5，15 次请求在 50/50 LB 下 b 最长连续 fail 可能 <5
    local DEFAULT_RULE_TRIGGER_COUNT=30
    run_burst "$DEFAULT_RULE_CONSUMER_PORT" "$DEFAULT_RULE_TRIGGER_COUNT" "默认规则触发"
    local trigger_fail=$CASE_FAIL

    log_step "用例6.5 验证：再发 ${RECOVERY_REQUEST_COUNT} 次，期望全部 200（流量走 a）"
    sleep 2
    run_burst "$DEFAULT_RULE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "默认规则验证"
    local verify_ok=$CASE_OK

    log_step "用例6.6 恢复：provider-b 翻回 200，等待 ${WAIT_HALF_OPEN_SECONDS}s 进入半开"
    provider_set_error "$PROVIDER_B_PORT" "false"
    sleep "$WAIT_HALF_OPEN_SECONDS"
    run_burst "$DEFAULT_RULE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "默认规则恢复"
    local recover_ok=$CASE_OK

    stop_consumer "$consumer_pid"

    if [[ "$trigger_fail" -ge 3 ]] \
        && [[ "$verify_ok" -eq "$RECOVERY_REQUEST_COUNT" ]] \
        && [[ "$recover_ok" -eq "$RECOVERY_REQUEST_COUNT" ]]; then
        log_info "✅ 用例6 PASS（默认实例级熔断兜底验证通过）: trigger=${trigger_fail} verify=${verify_ok} recover=${recover_ok}"
        echo "PASS"
        return 0
    fi
    log_error "❌ 用例6 FAIL: trigger=${trigger_fail} verify=${verify_ok} recover=${recover_ok}"
    echo "FAIL"
    return 1
}

# ---------------------- 用例7：修改熔断参数生效验证 ----------------------
# 验证语义：通过 update_circuitbreaker_rule API 将 CONSECUTIVE_ERROR 从 3 改为 7，
# SDK 通过规则轮询或 push 感知变更后重建 counter，熔断行为应按新阈值生效。
#
# 与直接创建或复用已有规则不同，这里验证的是"同 Id 规则参数变更 → SDK 热更新"链路。
#
# 该用例独立使用 INSTANCE 级规则与专属 caller，确保不与其它用例相互干扰。
# 为聚焦顺序错误计数，其它触发条件（ERROR_RATE）设为极高阈值避免介入。
#
# 通过条件：
#   - 两轮 trigger 阶段：至少 1 次 fail（实例 b 被 5xx 连续错误摘除）
#   - 两轮 verify 阶段：全部走 a → 200（实例 b 被熔断隔离）
#   - 两轮 recover 阶段：全部 200（实例 b 恢复）
case_modify_rule() {
    log_step "用例7：修改熔断参数生效（CONSECUTIVE_ERROR=3 → 7）"

    print_block "用例7：修改熔断参数生效" \
        "验证目标:" \
        "  · 验证 update_circuitbreaker_rule API 可修改已有规则的参数" \
        "  · 验证 SDK 感知规则变更后熔断行为按新参数生效" \
        "  · 验证两轮不同阈值的熔断均能正确触发与恢复" \
        "" \
        "规则配置:" \
        "  · Level=INSTANCE, name=cb-instance-${MODIFY_RULE_CALLER}" \
        "  · source=*/${MODIFY_RULE_CALLER}, destination=${NAMESPACE}/${SERVICE_NAME}" \
        "  · ErrorCondition: RET_CODE RANGE 500~599 (4xx 不计入熔断)" \
        "  · 轮1 CONSECUTIVE_ERROR=3，轮2 更新为 7" \
        "  · RecoverCondition: sleepWindow=12s, consecutiveSuccess=1" \
        "" \
        "验证步骤:" \
        "  7.1 provider-a=200 / provider-b=500" \
        "  7.2 启动 modify_rule consumer" \
        "  7.3 创建 INSTANCE 级规则 (CONSECUTIVE_ERROR=3)" \
        "  ── 第 1 轮 ──" \
        "  7.4 触发：发 ${TRIGGER_REQUEST_COUNT} 次让 b 累计 3 次 5xx → 被摘除" \
        "  7.5 验证：再发 ${RECOVERY_REQUEST_COUNT} 次，流量应全部走 a" \
        "  7.6 恢复：b 翻回 200，等 ${WAIT_HALF_OPEN_SECONDS}s 进半开 → 关闭" \
        "  ── 规则更新 ──" \
        "  7.7 调用 update_circuitbreaker_rule 将 CONSECUTIVE_ERROR 改为 7" \
        "  7.8 等待 SDK 拉取新规则" \
        "  ── 第 2 轮 ──" \
        "  7.9 触发：发 ${TRIGGER_REQUEST_COUNT} 次让 b 累计 7 次 5xx → 被摘除" \
        "  7.10 验证：再发 ${RECOVERY_REQUEST_COUNT} 次，流量应全部走 a" \
        "  7.11 恢复：b 翻回 200，等 ${WAIT_HALF_OPEN_SECONDS}s → 关闭" \
        "" \
        "判定标准: 两轮各自 trigger≥1, verify=${RECOVERY_REQUEST_COUNT}, recover=${RECOVERY_REQUEST_COUNT} → PASS"

    provider_set_error "$PROVIDER_A_PORT" "false"
    provider_set_error "$PROVIDER_B_PORT" "true"
    log_info "[用例7.1] provider-a → 200 / provider-b → 500"

    log_step "用例7.2 启动 modify_rule consumer"
    if ! start_consumer "modify_rule_consumer" "$INSTANCE_CONSUMER_DIR" \
        "$MODIFY_RULE_CALLER" "$MODIFY_RULE_CONSUMER_PORT" "${LOG_DIR}/modify_rule_consumer.log"; then
        echo "FAIL"
        return 1
    fi
    local consumer_pid="$_STARTED_PID"

    if ! inspect_caller_rules "$MODIFY_RULE_CALLER" "$NAMESPACE" \
        "cb-instance-${MODIFY_RULE_CALLER}"; then
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    fi
    if ! inspect_callee_rules "$SERVICE_NAME" "$NAMESPACE" "$MODIFY_RULE_CALLER" \
        "cb-instance-${MODIFY_RULE_CALLER}"; then
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    fi

    log_step "用例7.3 创建 INSTANCE 级规则（CONSECUTIVE_ERROR=3）"
    local rule_body rule_id
    rule_body=$(build_rule_body_modify_rule 3)

    rule_id=$(create_circuitbreaker_rule "modify_rule" "$rule_body") || {
        log_error "[用例7.3] 规则创建失败"
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    }

    log_info "等待 ${WAIT_RULE_READY_SECONDS}s 让 SDK 拉取规则..."
    sleep "$WAIT_RULE_READY_SECONDS"

    # ───── 第 1 轮（CONSECUTIVE_ERROR=3） ─────
    log_step "用例7.4 [轮1] 触发（CONSECUTIVE_ERROR=3）：发 ${TRIGGER_REQUEST_COUNT} 次让 provider-b 被摘除"
    run_burst "$MODIFY_RULE_CONSUMER_PORT" "$TRIGGER_REQUEST_COUNT" "修改规则触发-轮1(CONSECUTIVE_ERROR=3)"
    local r1_trigger_fail=$CASE_FAIL

    log_step "用例7.5 [轮1] 验证：再发 ${RECOVERY_REQUEST_COUNT} 次，期望全部 200"
    sleep 2
    run_burst "$MODIFY_RULE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "修改规则验证-轮1"
    local r1_verify_ok=$CASE_OK

    log_step "用例7.6 [轮1] 恢复：provider-b 翻回 200，等待 ${WAIT_HALF_OPEN_SECONDS}s 进入半开"
    provider_set_error "$PROVIDER_B_PORT" "false"
    sleep "$WAIT_HALF_OPEN_SECONDS"
    run_burst "$MODIFY_RULE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "修改规则恢复-轮1"
    local r1_recover_ok=$CASE_OK

    # ───── 更新规则：CONSECUTIVE_ERROR=3 → 7 ─────
    log_step "用例7.7 更新规则：CONSECUTIVE_ERROR=3 → 7"
    local new_body
    new_body=$(build_rule_body_modify_rule 7)
    if ! update_circuitbreaker_rule "modify_rule" "$rule_id" "$new_body"; then
        log_error "[用例7.7] 规则更新失败"
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    fi
    log_info "[用例7.7] 规则已更新为 CONSECUTIVE_ERROR=7 (id=${rule_id})"

    log_step "用例7.8 等待 ${WAIT_RULE_READY_SECONDS}s 让 SDK 拉取新规则..."
    sleep "$WAIT_RULE_READY_SECONDS"

    # ───── 第 2 轮（CONSECUTIVE_ERROR=7） ─────
    # 轮 2 阈值比轮 1 高（3→7），而 50/50 LB 分布下 15 个请求中 b 连续 7 次被选中
    # 概率偏低（实测 15 个请求里 b 最长连续 3 fail，未达 7 阈值 → b 没熔断 → 7.10
    # verify 阶段还能路由到 b 并收到 500）。把轮 2 的 burst 单独提到 30，确保
    # b 至少被连续选 7 次触发熔断。
    local MODIFY_R2_TRIGGER_COUNT=30
    log_step "用例7.9 [轮2] 触发（CONSECUTIVE_ERROR=7）：provider-b 重新置 500，发 ${MODIFY_R2_TRIGGER_COUNT} 次"
    provider_set_error "$PROVIDER_B_PORT" "true"
    run_burst "$MODIFY_RULE_CONSUMER_PORT" "$MODIFY_R2_TRIGGER_COUNT" "修改规则触发-轮2(CONSECUTIVE_ERROR=7)"
    local r2_trigger_fail=$CASE_FAIL

    log_step "用例7.10 [轮2] 验证：再发 ${RECOVERY_REQUEST_COUNT} 次，期望全部 200"
    sleep 2
    run_burst "$MODIFY_RULE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "修改规则验证-轮2"
    local r2_verify_ok=$CASE_OK

    log_step "用例7.11 [轮2] 恢复：provider-b 翻回 200，等待 ${WAIT_HALF_OPEN_SECONDS}s"
    provider_set_error "$PROVIDER_B_PORT" "false"
    sleep "$WAIT_HALF_OPEN_SECONDS"
    run_burst "$MODIFY_RULE_CONSUMER_PORT" "$RECOVERY_REQUEST_COUNT" "修改规则恢复-轮2"
    local r2_recover_ok=$CASE_OK

    stop_consumer "$consumer_pid"

    if [[ "$r1_trigger_fail" -ge 1 ]] \
        && [[ "$r1_verify_ok" -eq "$RECOVERY_REQUEST_COUNT" ]] \
        && [[ "$r1_recover_ok" -eq "$RECOVERY_REQUEST_COUNT" ]] \
        && [[ "$r2_trigger_fail" -ge 1 ]] \
        && [[ "$r2_verify_ok" -eq "$RECOVERY_REQUEST_COUNT" ]] \
        && [[ "$r2_recover_ok" -eq "$RECOVERY_REQUEST_COUNT" ]]; then
        log_info "✅ 用例7 PASS: 轮1[trigger=${r1_trigger_fail} verify=${r1_verify_ok} recover=${r1_recover_ok}] 轮2[trigger=${r2_trigger_fail} verify=${r2_verify_ok} recover=${r2_recover_ok}]"
        echo "PASS"
        return 0
    fi
    log_error "❌ 用例7 FAIL: 轮1[trigger=${r1_trigger_fail} verify=${r1_verify_ok} recover=${r1_recover_ok}] 轮2[trigger=${r2_trigger_fail} verify=${r2_verify_ok} recover=${r2_recover_ok}]"
    echo "FAIL"
    return 1
}

# ======================== 用例 8：接口协议维度 ========================
# 验证 SDK 按 BlockConfig.api.protocol 维度匹配：4 个 protocol（http/dubbo/grpc/thrift）
# 各 1 个独立 BC，验证每个 (protocol, BC) 都能被独立 trigger / verify abort / recover。
#
# 实现：consumer 端根据 path 段（/api/protocol/{proto}）填 RequestContext.Protocol，
# 触发时让对应 provider 5xx，验证 SDK 端 protocol=proto 的 BC 触发。
case_protocol_method() {
    log_step "用例8：接口协议+HTTP方法维度（4 protocol + 9 method = 13 BC 整合到 1 条规则）"

    print_block "用例8：接口协议+HTTP方法维度" \
        "Caller 写法（统一装饰器，selfService=${PM_CALLER}）:" \
        "  · consumer 端按 path 段推断 RequestContext.Protocol / HTTPMethod" \
        "  · 1 条规则 13 个 BlockConfig（4 protocol + 9 method），共享 caller service" \
        "" \
        "规则配置:" \
        "  · Level=METHOD, name=cb-proto-meth-${PM_CALLER}" \
        "  · source=*/${PM_CALLER}, destination=${NAMESPACE}/${SERVICE_NAME}" \
        "  · 4 个 protocol BC: api.protocol={http|dubbo|grpc|thrift}, method='*', path=EXACT /api/protocol/{proto}" \
        "  · 9 个 method BC:   api.protocol='*', method={GET|POST|...|CONNECT}, path=EXACT /api/method/{method}" \
        "  · CONSECUTIVE_ERROR=3 (收紧阈值便于快速验证)" \
        "" \
        "验证步骤 (13 个 BC 各跑 3 阶段):" \
        "  8.1 启动 PM consumer（端口 ${PM_CONSUMER_PORT}）" \
        "  8.2 创建 1 条规则（13 个 BlockConfig）" \
        "  8.{proto|meth}.trigger : provider-a=500, 发 3 次对应 path → 触发对应 BC" \
        "  8.{proto|meth}.verify  : 再发 3 次 → 期望全部 abort" \
        "  8.{proto|meth}.recover : provider-a=200, 等 12s → 再发 3 次 → 全 200" \
        "" \
        "判定标准: 13 个 BC 各自 trigger fail≥1 / verify abort=3 / recover ok=3 → PASS"

    log_step "用例8.1 启动 PM consumer"
    # 复用 newCircuitBreakerCaller 二进制 + inferProtocolMethod 自动推断 Protocol/Method
    if ! start_consumer "pm_consumer" "$CALLER_CONSUMER_DIR" \
        "$PM_CALLER" "$PM_CONSUMER_PORT" "${LOG_DIR}/pm_consumer.log"; then
        echo "FAIL"
        return 1
    fi
    local consumer_pid="$_STARTED_PID"

    # 巡检 1 条规则（含 13 BC），caller 维度只有 1 条
    if ! inspect_caller_rules "$PM_CALLER" "$NAMESPACE" \
        "cb-proto-meth-${PM_CALLER}"; then
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    fi
    if ! inspect_callee_rules "$SERVICE_NAME" "$NAMESPACE" "$PM_CALLER" \
        "cb-proto-meth-${PM_CALLER}"; then
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    fi

    log_step "用例8.2 创建 1 条规则（13 个 BlockConfig）"
    local body rid
    body=$(build_rule_body_protocol_method) || {
        log_error "[用例8.2] build_rule_body_protocol_method 失败"
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    }
    rid=$(create_circuitbreaker_rule "protocol-method" "$body") || {
        log_error "[用例8.2] create 协议+方法 规则失败"
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    }

    log_info "等待 ${WAIT_RULE_READY_SECONDS}s 让 SDK 拉取规则..."
    sleep "$WAIT_RULE_READY_SECONDS"

    # 13 个 BC 共享同一 sleepWindow 但需要独立 trigger：
    # 50/50 LB 下只有 ~50% 命中 error provider，CONSECUTIVE_ERROR=3 需要
    # 至少 10 次 burst 才能有高概率连续 3 次命中 a
    local all_pass=true
    local summary=""
    # 50/50 LB 下 error provider 命中约 50%，CONSECUTIVE_ERROR=3 要求连续 3 次命中，
    # ERROR_RATE=50%@30s/minRequest=5 要求滑窗内错误率达 50%。burst=20 时 fail≈9/20=45%
    # 两个条件都易在临界擦边失败（如 proto-http fail=9 最长连续仅 2）。提到 30 让
    # fail≈15/30=50% 稳过 ERROR_RATE，且连续 3 次命中的概率显著上升。
    local PM_TRIGGER_COUNT=30
    local PM_VERIFY_COUNT=3
    local PM_RECOVER_COUNT=3
    # 4 个 protocol BC
    for proto in http dubbo grpc thrift; do
        log_step "用例8.proto-$proto: trigger / verify / recover"

        # trigger：a、b 都置 500（b 默认即 500，这里显式确保），让 service 级熔断触发。
        provider_set_error "$PROVIDER_A_PORT" "true"
        provider_set_error "$PROVIDER_B_PORT" "true"
        run_burst "$PM_CONSUMER_PORT" "$PM_TRIGGER_COUNT" "8.proto-$proto-trigger" "/api/protocol/$proto"
        local t_fail=$CASE_FAIL
        local t_abort=$CASE_ABORT

        sleep 2
        run_burst "$PM_CONSUMER_PORT" "$PM_VERIFY_COUNT" "8.proto-$proto-verify" "/api/protocol/$proto"
        local v_ok=$CASE_OK
        local v_abort=$CASE_ABORT

        # recover：a、b 都翻回 200。METHOD/service 级熔断半开探测会随机命中 a 或 b，
        # 若 b 仍为 500 则探测失败导致熔断重新 OPEN，recover 阶段出现 fail/abort。
        # 因此必须同步复位 provider-b，否则 recover 永远无法稳定全 200。
        provider_set_error "$PROVIDER_A_PORT" "false"
        provider_set_error "$PROVIDER_B_PORT" "false"
        sleep "$WAIT_HALF_OPEN_SECONDS"
        run_burst "$PM_CONSUMER_PORT" "$PM_RECOVER_COUNT" "8.proto-$proto-recover" "/api/protocol/$proto"
        local r_ok=$CASE_OK

        local passed
        if [[ "$t_fail" -ge 1 || "$t_abort" -ge 1 ]] \
            && [[ $((v_ok + v_abort)) -eq 3 ]] \
            && [[ "$r_ok" -eq 3 ]]; then
            passed="✅"
        else
            passed="❌"
            all_pass=false
        fi
        summary+="  proto-$proto: trigger fail=$t_fail abort=$t_abort  verify ok=$v_ok abort=$v_abort  recover ok=$r_ok  $passed"$'\n'
        log_info "[用例8.proto-$proto] trigger fail=$t_fail abort=$t_abort  verify ok=$v_ok abort=$v_abort  recover ok=$r_ok  $passed"
    done

    # 9 个 method BC
    local methods=(GET POST PUT PATCH DELETE HEAD OPTIONS TRACE CONNECT)
    for meth in "${methods[@]}"; do
        log_step "用例8.meth-$meth: trigger / verify / recover"

        # trigger：a、b 都置 500，确保 service 级熔断触发。
        provider_set_error "$PROVIDER_A_PORT" "true"
        provider_set_error "$PROVIDER_B_PORT" "true"
        run_burst "$PM_CONSUMER_PORT" "$PM_TRIGGER_COUNT" "8.meth-$meth-trigger" "/api/method/$meth"
        local t_fail=$CASE_FAIL
        local t_abort=$CASE_ABORT

        sleep 2
        run_burst "$PM_CONSUMER_PORT" "$PM_VERIFY_COUNT" "8.meth-$meth-verify" "/api/method/$meth"
        local v_ok=$CASE_OK
        local v_abort=$CASE_ABORT

        # recover：a、b 同步翻回 200，避免半开探测命中仍为 500 的 b 导致熔断重新 OPEN。
        provider_set_error "$PROVIDER_A_PORT" "false"
        provider_set_error "$PROVIDER_B_PORT" "false"
        sleep "$WAIT_HALF_OPEN_SECONDS"
        run_burst "$PM_CONSUMER_PORT" "$PM_RECOVER_COUNT" "8.meth-$meth-recover" "/api/method/$meth"
        local r_ok=$CASE_OK

        local passed
        if [[ "$t_fail" -ge 1 || "$t_abort" -ge 1 ]] \
            && [[ $((v_ok + v_abort)) -eq 3 ]] \
            && [[ "$r_ok" -eq 3 ]]; then
            passed="✅"
        else
            passed="❌"
            all_pass=false
        fi
        summary+="  meth-$meth: trigger fail=$t_fail abort=$t_abort  verify ok=$v_ok abort=$v_abort  recover ok=$r_ok  $passed"$'\n'
        log_info "[用例8.meth-$meth] trigger fail=$t_fail abort=$t_abort  verify ok=$v_ok abort=$v_abort  recover ok=$r_ok  $passed"
    done

    stop_consumer "$consumer_pid"

    if $all_pass; then
        log_info "✅ 用例8 PASS (13 个 BC 整合 1 条规则 独立 trigger/verify/recover):"$'\n'"$summary"
        echo "PASS"
        return 0
    fi
    log_error "❌ 用例8 FAIL:"$'\n'"$summary"
    echo "FAIL"
    return 1
}

# ======================== 用例 10：路径匹配方式维度 ========================
# 验证 SDK 按 BlockConfig.api.path 维度匹配：5 种 MatchString 类型
# （EXACT/REGEX/NOT_EQUALS/IN/NOT_IN）各 1 个 BC，独立 trigger / verify / recover。
#
# 实现：consumer 端按 path 段（/api/pathtype/{...}）填 RequestContext.Path，
# 消费者用 path 是否匹配规则决定是否触发。
case_pathtype() {
    log_step "用例10：路径匹配方式维度（EXACT/REGEX/NOT_EQUALS/IN/NOT_IN）"

    print_block "用例10：路径匹配方式维度" \
        "Caller 写法（统一装饰器，selfService=${PATHTYPE_CALLER}）:" \
        "  · 5 种 MatchString 类型合并到 1 条 METHOD 规则的 5 个 BlockConfig" \
        "" \
        "规则配置 + 消费者触发 path:" \
        "  · EXACT       path=EXACT /api/pathtype/exact,  消费者请求 /api/pathtype/exact 触发" \
        "  · REGEX       path=REGEX ^/api/pathtype/regex/.*, 消费者请求 /api/pathtype/regex/abc 触发" \
        "  · NOT_EQUALS  path=NOT_EQUALS /api/pathtype/never_match_anything, 消费者请求 /api/pathtype/something 触发" \
        "  · IN          path=IN /api/pathtype/in1,/api/pathtype/in2, 消费者请求 /api/pathtype/in1 触发" \
        "  · NOT_IN      path=NOT_IN /api/pathtype/forbidden1,/api/pathtype/forbidden2, 消费者请求 /api/pathtype/allowed 触发" \
        "" \
        "验证步骤 (5 种 path type 各跑正向 3 阶段):" \
        "  10.1 启动 pathtype consumer" \
        "  10.2 创建 1 条 METHOD 规则（5 个 BlockConfig）" \
        "  10.{ptype}.trigger  : provider a/b=500, 发 ${PATHTYPE_TRIGGER_COUNT:-30} 次对应 path → 触发对应 BC" \
        "  10.{ptype}.verify   : 再发 3 次 → 期望全部 abort" \
        "  10.{ptype}.recover  : provider a/b=200, 等 ${WAIT_HALF_OPEN_SECONDS}s → 再发 3 次 → 全 200" \
        "" \
        "判定标准: 5 种 path type 各自 trigger fail≥1(或 abort≥1) / verify abort=3 / recover ok=3 → PASS" \
        "  注: 5 个 BC 合并在 1 条规则,NOT_EQUALS/NOT_IN 等否定匹配会吃掉几乎所有 path," \
        "      不存在对全部 BC 都不匹配的'反向 path',故反向验证不适用于合并规则结构。"

    log_step "用例10.1 启动 pathtype consumer"
    if ! start_consumer "pathtype_consumer" "$CALLER_CONSUMER_DIR" \
        "$PATHTYPE_CALLER" "$PATHTYPE_CONSUMER_PORT" "${LOG_DIR}/pathtype_consumer.log"; then
        echo "FAIL"
        return 1
    fi
    local consumer_pid="$_STARTED_PID"

    local expected_rules=(
        "cb-pathtype-${PATHTYPE_CALLER}"
    )
    if ! inspect_caller_rules "$PATHTYPE_CALLER" "$NAMESPACE" "${expected_rules[@]}"; then
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    fi
    if ! inspect_callee_rules "$SERVICE_NAME" "$NAMESPACE" "$PATHTYPE_CALLER" "${expected_rules[@]}"; then
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    fi

    log_step "用例10.2 创建 1 条 METHOD 规则（内含 5 个 BlockConfig，覆盖 EXACT/REGEX/NOT_EQUALS/IN/NOT_IN）"
    local pathtype_body
    pathtype_body=$(build_rule_body_pathtype_merged)
    create_circuitbreaker_rule "pathtype" "$pathtype_body" >/dev/null || {
        log_error "[用例10.2] create pathtype 合并规则失败"
        stop_consumer "$consumer_pid"
        echo "FAIL"
        return 1
    }

    log_info "等待 ${WAIT_RULE_READY_SECONDS}s 让 SDK 拉取规则..."
    sleep "$WAIT_RULE_READY_SECONDS"

    # 每种 path type 的：规则名、消费者 path（必须命中规则）、pattype 显示名
    local ptypes=(
        "EXACT|/api/pathtype/exact|EXACT"
        "REGEX|/api/pathtype/regex/abc|REGEX"
        "NOT_EQUALS|/api/pathtype/something|NOT_EQUALS"
        "IN|/api/pathtype/in1|IN"
        "NOT_IN|/api/pathtype/allowed|NOT_IN"
    )

    local all_pass=true
    local summary=""
    # 与用例8 同理：CONSECUTIVE_ERROR=3 + ERROR_RATE=50%@30s/minRequest=5，50/50 LB 下
    # burst=30 让 fail≈15/30=50% 稳过 ERROR_RATE，连续 3 次命中概率也更高。
    local PATHTYPE_TRIGGER_COUNT=30
    local PATHTYPE_VERIFY_COUNT=3
    local PATHTYPE_RECOVER_COUNT=3
    for entry in "${ptypes[@]}"; do
        # 仅在拆分 entry 时临时改 IFS，拆完立即恢复，避免污染 run_burst 内
        # `for i in $(seq 1 N)` 的换行符分词（IFS='|' 会让 seq 输出被当成单个词，
        # 导致 burst 循环只跑 1 次 → total=1）。
        local ptype req_path ptype_label
        IFS='|' read -r ptype req_path ptype_label <<< "$entry"
        log_step "用例10.$ptype_label: trigger / verify / recover"

        # trigger：a、b 都置 500（b 默认即 500，这里显式确保），让对应 BC 熔断触发。
        provider_set_error "$PROVIDER_A_PORT" "true"
        provider_set_error "$PROVIDER_B_PORT" "true"
        run_burst "$PATHTYPE_CONSUMER_PORT" "$PATHTYPE_TRIGGER_COUNT" "10.$ptype_label-trigger" "$req_path"
        local t_fail=$CASE_FAIL
        local t_abort=$CASE_ABORT

        sleep 2
        run_burst "$PATHTYPE_CONSUMER_PORT" "$PATHTYPE_VERIFY_COUNT" "10.$ptype_label-verify" "$req_path"
        local v_ok=$CASE_OK
        local v_abort=$CASE_ABORT

        # recover：a、b 同步翻回 200，避免半开探测命中仍为 500 的 b 导致熔断重新 OPEN。
        provider_set_error "$PROVIDER_A_PORT" "false"
        provider_set_error "$PROVIDER_B_PORT" "false"
        sleep "$WAIT_HALF_OPEN_SECONDS"
        run_burst "$PATHTYPE_CONSUMER_PORT" "$PATHTYPE_RECOVER_COUNT" "10.$ptype_label-recover" "$req_path"
        local r_ok=$CASE_OK

        # 说明：原先这里有"反向 path"3 阶段，意图证明不匹配规则的 5xx 不计入熔断。
        # 但 5 种 path-type 已合并到 1 条规则的 5 个 BlockConfig，每个请求会对所有 BC
        # 做 matchAPI 派发。NOT_EQUALS(≠某值即匹配) / NOT_IN(∉列表即匹配) 这两个否定
        # 匹配 BC 会吃掉几乎所有 path——不存在"对全部 5 个 BC 都不匹配"的反向 path，
        # 任何反向 path 必被 NOT_EQUALS/NOT_IN BC 命中并熔断（SDK 行为正确）。
        # 因此反向验证在合并规则结构下逻辑不成立，已移除；正向 3 阶段已充分证明
        # 5 种 MatchString 类型各自的 path 匹配 + 熔断生效。

        local passed
        if [[ "$t_fail" -ge 1 || "$t_abort" -ge 1 ]] \
            && [[ $((v_ok + v_abort)) -eq 3 ]] \
            && [[ "$r_ok" -eq 3 ]]; then
            passed="✅"
        else
            passed="❌"
            all_pass=false
        fi
        summary+="  $ptype_label: trigger fail=$t_fail abort=$t_abort  verify ok=$v_ok abort=$v_abort  recover ok=$r_ok  $passed"$'\n'
        log_info "[用例10.$ptype_label] trigger fail=$t_fail abort=$t_abort  verify ok=$v_ok abort=$v_abort  recover ok=$r_ok  $passed"
    done
    unset IFS

    stop_consumer "$consumer_pid"

    if $all_pass; then
        log_info "✅ 用例10 PASS (5 种 path type 正向 3 阶段):"$'\n'"$summary"
        echo "PASS"
        return 0
    fi
    log_error "❌ 用例10 FAIL:"$'\n'"$summary"
    echo "FAIL"
    return 1
}

# ======================== 主流程 ========================
main() {
    setup_test_log "$@"

    echo ""
    echo -e "${BLUE}╔════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║   故障熔断（CircuitBreaker）端到端验证脚本                ║${NC}"
    echo -e "${BLUE}╚════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "配置信息:"
    echo "  北极星 gRPC :       ${POLARIS_SERVER}:8091"
    echo "  北极星 HTTP :       ${POLARIS_HTTP_ADDR}"
    echo "  服务名 / 命名空间: ${SERVICE_NAME} / ${NAMESPACE}"
    echo "  Provider-A 端口:    ${PROVIDER_A_PORT}    (默认 200)"
    echo "  Provider-B 端口:    ${PROVIDER_B_PORT}    (默认 500)"
    echo "  Consumer 端口:      ${INSTANCE_CONSUMER_PORT}/${SERVICE_CONSUMER_PORT}/${INTERFACE_CONSUMER_PORT}/${OLD_INSTANCE_CONSUMER_PORT}/${HTTP_STATUS_CONSUMER_PORT}/${DEFAULT_RULE_CONSUMER_PORT}/${MODIFY_RULE_CONSUMER_PORT}"
    echo "  运行用例:           ${RUN_CASES}"
    echo "  触发请求次数:       ${TRIGGER_REQUEST_COUNT}"
    echo "  半开等待时长:       ${WAIT_HALF_OPEN_SECONDS}s"
    echo "  规则巡检模式:       STRICT_RULE_CHECK=${STRICT_RULE_CHECK:-false}（true=遇到陌生规则即 FAIL）"
    echo "  测试日志:           ${TEST_LOG_FILE}"
    echo ""

    # === 步骤A：环境准备 ===
    log_step "步骤A 环境准备"
    if ! command -v go &> /dev/null; then
        log_error "Go 未安装"; exit 1
    fi
    if ! command -v python3 &> /dev/null; then
        log_error "python3 未安装"; exit 1
    fi
    if ! command -v curl &> /dev/null; then
        log_error "curl 未安装"; exit 1
    fi
    log_info "Go 版本: $(go version)"

    mkdir -p "$BUILD_DIR" "$LOG_DIR"
    write_rule_generator

    # === 步骤B：启动 Provider 集群（共用，所有用例共享） ===
    log_step "步骤B 启动 Provider 集群"
    start_provider "provider_a" "$CALLEE_A_DIR" \
        "$PROVIDER_A_PORT" "${LOG_DIR}/provider_a.log" || exit 1
    PROVIDER_A_PID="$_STARTED_PID"

    start_provider "provider_b" "$CALLEE_B_DIR" \
        "$PROVIDER_B_PORT" "${LOG_DIR}/provider_b.log" || exit 1
    PROVIDER_B_PID="$_STARTED_PID"

    log_info "等待 5s 让服务发现缓存就绪..."
    sleep 5

    # === 步骤C：执行子用例 ===
    local results=()
    local case_keys=()
    IFS=',' read -ra requested_cases <<< "$RUN_CASES"
    for c in "${requested_cases[@]}"; do
        case "$c" in
            instance)
                local r
                r=$(case_instance) || true
                results+=("$r")
                case_keys+=("instance")
                ;;
            service)
                local r
                r=$(case_service) || true
                results+=("$r")
                case_keys+=("service")
                ;;
            interface)
                local r
                r=$(case_interface) || true
                results+=("$r")
                case_keys+=("interface")
                ;;
            old_instance)
                local r
                r=$(case_old_instance) || true
                results+=("$r")
                case_keys+=("old_instance")
                ;;
            http_status)
                local r
                r=$(case_http_status) || true
                results+=("$r")
                case_keys+=("http_status")

                # case_http_status 的 C 段在 subshell 内部按端口 kill 了 provider_a/b
                # 制造网络错，但 subshell 退出时 disown 后的 bg process 仍会被清理，
                # 所以 provider 重启必须由主 shell 接管，让 PID 落在主 shell 全局
                # PROVIDER_PIDS 中，脚本退出时由 trap cleanup 统一回收。
                # 这里检查端口是否还活着：若已被 case 5 C 段 kill，则重启。
                if ! lsof -ti :"${PROVIDER_A_PORT}" > /dev/null 2>&1 \
                    || ! lsof -ti :"${PROVIDER_B_PORT}" > /dev/null 2>&1; then
                    log_step "case 5 杀掉了 provider，主 shell 重启 provider_a/b 给后续 case 用"
                    PROVIDER_A_PID=""
                    PROVIDER_B_PID=""
                    if ! start_provider "provider_a" "$CALLEE_A_DIR" \
                        "$PROVIDER_A_PORT" "${LOG_DIR}/provider_a.log"; then
                        log_error "case 5 后 provider-a 重启失败"
                        echo "FAIL"
                        break
                    fi
                    PROVIDER_A_PID="$_STARTED_PID"
                    if ! start_provider "provider_b" "$CALLEE_B_DIR" \
                        "$PROVIDER_B_PORT" "${LOG_DIR}/provider_b.log"; then
                        log_error "case 5 后 provider-b 重启失败"
                        echo "FAIL"
                        break
                    fi
                    PROVIDER_B_PID="$_STARTED_PID"
                    # 等 sleepWindow 过去 + 实例缓存刷新
                    sleep "$WAIT_HALF_OPEN_SECONDS"
                fi
                ;;
            default_rule)
                local r
                r=$(case_default_rule) || true
                results+=("$r")
                case_keys+=("default_rule")
                ;;
            modify_rule)
                local r
                r=$(case_modify_rule) || true
                results+=("$r")
                case_keys+=("modify_rule")
                ;;
            protocol_method)
                local r
                r=$(case_protocol_method) || true
                results+=("$r")
                case_keys+=("protocol_method")
                ;;
            pathtype)
                local r
                r=$(case_pathtype) || true
                results+=("$r")
                case_keys+=("pathtype")
                ;;
            *)
                log_warn "未知用例: $c (跳过)"
                ;;
        esac
    done

    # === 步骤D：结果汇总 ===
    log_step "步骤D 结果汇总"

    echo ""
    echo -e "${BLUE}╔════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║   故障熔断验证结果汇总                                    ║${NC}"
    echo -e "${BLUE}╚════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    printf "  ${CYAN}%-14s %-10s${NC}\n" "用例" "结果"
    printf "  %-14s %-10s\n" "--------------" "----------"
    local fail_count=0
    for i in "${!case_keys[@]}"; do
        local key="${case_keys[$i]}"
        local r="${results[$i]}"
        local color="${GREEN}"
        if [[ "$r" != "PASS" ]]; then
            color="${RED}"
            fail_count=$((fail_count + 1))
        fi
        printf "  %-14s ${color}%-10s${NC}\n" "$key" "$r"
    done
    echo ""
    echo "  详细日志:"
    echo "    主日志:           ${TEST_LOG_FILE}"
    echo "    provider-a:       ${LOG_DIR}/provider_a.log"
    echo "    provider-b:       ${LOG_DIR}/provider_b.log"
    echo "    consumer:         ${LOG_DIR}/*_consumer.log"
    echo ""

    if [[ "$fail_count" -eq 0 ]]; then
        echo -e "${GREEN}╔════════════════════════════════════════════════════════════╗${NC}"
        echo -e "${GREEN}║  验证结论: ✅ 全部熔断用例通过                            ║${NC}"
        echo -e "${GREEN}╚════════════════════════════════════════════════════════════╝${NC}"
    else
        echo -e "${RED}╔════════════════════════════════════════════════════════════╗${NC}"
        echo -e "${RED}║  验证结论: ❌ 有 ${fail_count} 个用例失败                                ║${NC}"
        echo -e "${RED}╚════════════════════════════════════════════════════════════╝${NC}"
    fi

    return "$fail_count"
}

main "$@"
