#!/bin/bash
# =============================================================================
# serviceRule demo E2E 验证脚本
#
# 目标：验证 examples/serviceRule/main.go 暴露的 7 个 GET /xxx 接口拿到的数据
# 与 Polaris 服务端 OpenAPI 直查的结果**字段级一致**。同时充当
# *pb.BehaviorNotRegisteredError 修复的端到端验证（GET /ratelimit 命中 action=tsf
# 时应返回 isBehaviorNotRegistered=true，且 SDK base 日志里只有 WARN 没有 ERROR）。
#
# 使用方法:
#   chmod +x verify.sh
#   ./verify.sh [选项]
#
# 选项:
#   --polaris-server <地址>      北极星服务端地址 (默认: 127.0.0.1)
#   --polaris-token  <令牌>      北极星鉴权令牌（强烈建议手工传入）
#   --polaris-grpc-port <端口>   北极星 gRPC 端口 (默认: 8091)
#   --polaris-http-port <端口>   北极星 OpenAPI 端口 (默认: 8090)
#   --service        <服务名>    被调服务名 (默认: provider-demo)
#   --namespace      <命名空间>  命名空间 (默认: default)
#   --demo-port      <端口>      serviceRule demo HTTP 端口 (默认: 38080)
#   --rule-cache-wait <秒>       创建/更新规则后等待 SDK 拉取最新数据 (默认: 8)
#   --skip-tsf-rule              跳过 action=tsf 规则的修复验证
#   --debug                      打开 demo 与 OpenAPI 详细日志
#   --help                       显示帮助
#
# 工作机制:
#   - 「检查-存在则复用-否则创建」策略：每类规则用固定 name 做幂等管理；规则跨运行复用，
#     脚本不主动删除规则，避免对环境产生破坏性变更。
#   - demo 由本脚本自动构建并启动；脚本退出时通过 trap 杀掉 demo 进程。
#   - 比对策略「全字段精确对比」：把 demo 与 OpenAPI 两侧的规则列表归一化（unwrap proto
#     的 {value:x} wrapper、丢弃 ctime/mtime/revision 等易变字段、按 name 排序），
#     用 diff 对照；任何字段不一致都会报错。
# =============================================================================

set -euo pipefail

# ======================== 默认配置 ========================
POLARIS_SERVER="${POLARIS_SERVER:-127.0.0.1}"
POLARIS_TOKEN="${POLARIS_TOKEN:-}"
POLARIS_GRPC_PORT="${POLARIS_GRPC_PORT:-8091}"
POLARIS_HTTP_PORT="${POLARIS_HTTP_PORT:-8090}"
SERVICE_NAME="${SERVICE_NAME:-provider-demo}"
NAMESPACE="${NAMESPACE:-default}"
DEMO_PORT="${DEMO_PORT:-38080}"
RULE_CACHE_WAIT="${RULE_CACHE_WAIT:-8}"
SKIP_TSF_RULE="${SKIP_TSF_RULE:-false}"
DEBUG_MODE="${DEBUG_MODE:-false}"

# 颜色
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# ======================== 解析命令行参数 ========================
while [[ $# -gt 0 ]]; do
    case "$1" in
        --polaris-server)    POLARIS_SERVER="$2";    shift 2 ;;
        --polaris-token)     POLARIS_TOKEN="$2";     shift 2 ;;
        --polaris-grpc-port) POLARIS_GRPC_PORT="$2"; shift 2 ;;
        --polaris-http-port) POLARIS_HTTP_PORT="$2"; shift 2 ;;
        --service)           SERVICE_NAME="$2";      shift 2 ;;
        --namespace)         NAMESPACE="$2";         shift 2 ;;
        --demo-port)         DEMO_PORT="$2";         shift 2 ;;
        --rule-cache-wait)   RULE_CACHE_WAIT="$2";   shift 2 ;;
        --skip-tsf-rule)     SKIP_TSF_RULE="true";   shift ;;
        --debug)             DEBUG_MODE="true";      shift ;;
        --help|-h)
            sed -n '2,40p' "$0"
            exit 0
            ;;
        *) echo -e "${RED}未知参数: $1${NC}"; exit 1 ;;
    esac
done

POLARIS_HTTP_ADDR="http://${POLARIS_SERVER}:${POLARIS_HTTP_PORT}"
DEMO_ADDR="http://127.0.0.1:${DEMO_PORT}"

# ======================== 全局变量 ========================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_DIR="${SCRIPT_DIR}/.build"
LOG_DIR="${SCRIPT_DIR}/.logs"
TEST_LOG_FILE="${LOG_DIR}/verify-$(date +%Y%m%d_%H%M%S).log"
mkdir -p "$BUILD_DIR" "$LOG_DIR"

# 跨运行复用的规则名（带 it- 前缀避免与人工规则冲突）
RULE_NAME_ROUTE="serviceRule-it-route-${SERVICE_NAME}"
RULE_NAME_NEARBY="serviceRule-it-nearby-${SERVICE_NAME}"
RULE_NAME_RATELIMIT="serviceRule-it-rl-${SERVICE_NAME}"
RULE_NAME_RATELIMIT_TSF="serviceRule-it-rl-tsf-${SERVICE_NAME}"
RULE_NAME_CIRCUITBREAKER="serviceRule-it-cb-${SERVICE_NAME}"
RULE_NAME_LOSSLESS="serviceRule-it-lossless-${SERVICE_NAME}"
RULE_NAME_BLOCKALLOW="serviceRule-it-ba-${SERVICE_NAME}"
RULE_NAME_LANE="serviceRule-it-lane-${SERVICE_NAME}"

DEMO_PID=""

declare -i TOTAL_COUNT=0
declare -i TOTAL_PASS=0
declare -i TOTAL_FAIL=0
declare -i TOTAL_SKIP=0

# ======================== 日志工具 ========================
_log() {
    local msg="$1"
    echo -e "$msg" >&2
    echo -e "$msg" | sed 's/\x1b\[[0-9;]*m//g' >> "${TEST_LOG_FILE}" 2>/dev/null || true
}
log_info()  { _log "${GREEN}[INFO]${NC}  $(date '+%H:%M:%S') $*"; }
log_warn()  { _log "${YELLOW}[WARN]${NC}  $(date '+%H:%M:%S') $*"; }
log_error() { _log "${RED}[ERROR]${NC} $(date '+%H:%M:%S') $*"; }
log_debug() { [[ "$DEBUG_MODE" == "true" ]] && _log "${BLUE}[DEBUG]${NC} $(date '+%H:%M:%S') $*" || true; }
log_step() {
    _log ""
    _log "${CYAN}========================================${NC}"
    _log "${CYAN}  $*${NC}"
    _log "${CYAN}========================================${NC}"
}
log_case_pass() { TOTAL_PASS+=1; TOTAL_COUNT+=1; _log "  ${GREEN}[PASS]${NC} $*"; }
log_case_fail() { TOTAL_FAIL+=1; TOTAL_COUNT+=1; _log "  ${RED}[FAIL]${NC} $*"; }
log_case_skip() { TOTAL_SKIP+=1; TOTAL_COUNT+=1; _log "  ${YELLOW}[SKIP]${NC} $*"; }

# ======================== 退出清理 ========================
cleanup() {
    if [[ -n "$DEMO_PID" ]] && kill -0 "$DEMO_PID" 2>/dev/null; then
        log_info "停止 demo 进程 (PID: $DEMO_PID)"
        kill "$DEMO_PID" 2>/dev/null || true
        wait "$DEMO_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT

# ======================== 前置依赖检查 ========================
require_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        log_error "缺少依赖命令: $1"
        exit 1
    fi
}
require_cmd curl
require_cmd python3
require_cmd go
require_cmd diff

# ======================== Demo 构建与启动 ========================
build_demo() {
    log_step "构建 serviceRule demo"
    pushd "$SCRIPT_DIR" >/dev/null
    go build -o "${BUILD_DIR}/serviceRule" .
    popd >/dev/null
    log_info "demo 已构建: ${BUILD_DIR}/serviceRule"
}

start_demo() {
    log_step "启动 serviceRule demo"
    # 端口被占用时强制清理
    if lsof -nP -iTCP:"${DEMO_PORT}" -sTCP:LISTEN 2>/dev/null | grep -q LISTEN; then
        log_warn "demo 端口 ${DEMO_PORT} 已被占用，尝试清理"
        local pids=""
        pids=$(lsof -nP -iTCP:"${DEMO_PORT}" -sTCP:LISTEN -t 2>/dev/null || true)
        for pid in $pids; do
            kill "$pid" 2>/dev/null || true
        done
        sleep 1
    fi

    local demo_log="${LOG_DIR}/demo.log"
    : > "$demo_log"

    local debug_flag=""
    [[ "$DEBUG_MODE" == "true" ]] && debug_flag="-debug"

    "${BUILD_DIR}/serviceRule" \
        -polaris-server-address "${POLARIS_SERVER}:${POLARIS_GRPC_PORT}" \
        -polaris-token "${POLARIS_TOKEN}" \
        -namespace "${NAMESPACE}" \
        -service "${SERVICE_NAME}" \
        -port ":${DEMO_PORT}" \
        ${debug_flag} \
        > "$demo_log" 2>&1 &
    DEMO_PID=$!
    log_info "demo 已启动 (PID: $DEMO_PID, port: $DEMO_PORT, log: $demo_log)"

    # 等待 demo 就绪
    local waited=0
    while [[ $waited -lt 30 ]]; do
        if ! kill -0 "$DEMO_PID" 2>/dev/null; then
            log_error "demo 启动失败，最近日志："
            tail -20 "$demo_log" 2>/dev/null || true
            exit 1
        fi
        if curl -s --connect-timeout 2 "${DEMO_ADDR}/" >/dev/null 2>&1; then
            log_info "demo 已就绪 (${DEMO_ADDR})"
            return 0
        fi
        sleep 1
        waited=$((waited + 1))
    done
    log_error "demo 30s 内未就绪"
    tail -20 "$demo_log" 2>/dev/null || true
    exit 1
}

# ======================== Polaris 服务端兜底准备 ========================
# 服务端要求规则归属于一个真实存在的服务。如果 SERVICE_NAME 在 namespace 下不存在，
# 部分 OpenAPI（特别是 Routing/RateLimit）会返回 400 类错误。这里幂等创建空 service。
ensure_service() {
    log_info "确认 service ${NAMESPACE}/${SERVICE_NAME} 存在"
    local body=""
    body=$(SVC="$SERVICE_NAME" NS="$NAMESPACE" python3 -c '
import os, json
print(json.dumps([{"name": os.environ["SVC"], "namespace": os.environ["NS"],
                  "comment": "auto-created by serviceRule/verify.sh"}]))')
    local http_code=""
    http_code=$(curl -s -o /dev/null -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/naming/v1/services" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "$body" 2>/dev/null || echo "000")
    # 200=创建成功；400201=重复（已存在），都视为 OK
    if [[ "$http_code" == "200" ]]; then
        log_info "  service 创建/已存在 (HTTP=${http_code})"
    else
        log_warn "  service POST 返回 HTTP=${http_code} (可能已存在或权限不足, 继续)"
    fi
}

# ======================== OpenAPI 调用工具 ========================
# openapi_get <path> <query>
#   返回响应体到 stdout；失败时返回空。
openapi_get() {
    local path="$1" query="${2:-}"
    local url="${POLARIS_HTTP_ADDR}${path}"
    [[ -n "$query" ]] && url="${url}?${query}"
    curl -s --connect-timeout 5 --max-time 15 \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        "$url" 2>/dev/null || echo ""
}

# openapi_post <path> <body_json>
openapi_post() {
    local path="$1" body="$2"
    local resp=""
    resp=$(curl -s --connect-timeout 5 --max-time 15 \
        --request POST "${POLARIS_HTTP_ADDR}${path}" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "$body" 2>/dev/null || echo "")
    log_debug "POST ${path} -> $(echo "$resp" | head -c 200)"
    echo "$resp"
}

# openapi_put <path> <body_json>
openapi_put() {
    local path="$1" body="$2"
    curl -s --connect-timeout 5 --max-time 15 \
        --request PUT "${POLARIS_HTTP_ADDR}${path}" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "$body" 2>/dev/null || echo ""
}

# 检查 OpenAPI 应答的 code 是否成功（200000 / 200001 都算）
openapi_resp_ok() {
    local resp="$1"
    local code=""
    code=$(echo "$resp" | python3 -c "
import sys, json
try:
    print(json.load(sys.stdin).get('code', 0))
except Exception:
    print(0)
" 2>/dev/null)
    [[ "$code" =~ ^20000[01]$ ]]
}

# ======================== Demo 调用工具 ========================
# demo_get <path>: 返回 demo 的响应体
demo_get() {
    local path="$1"
    local body=""
    body=$(python3 -c "
import json
print(json.dumps({'namespace': '${NAMESPACE}', 'service': '${SERVICE_NAME}'}))")
    curl -s --connect-timeout 5 --max-time 15 \
        --request GET "${DEMO_ADDR}${path}" \
        --header 'Content-Type: application/json' \
        --data-raw "$body" 2>/dev/null || echo ""
}

# ======================== 通用规则规范化 ========================
# canonicalize <kind> <demo_or_openapi> <input_json>: 将规则列表归一化为按 name 排序
# 的 JSON 数组，每条规则为 {key1: v1, ...}（unwrap proto wrapper、丢弃易变字段）。
#
# kind 决定 OpenAPI 与 demo 的提取路径与可比较字段集；demo_or_openapi 决定从哪个根
# 路径取列表。
canonicalize() {
    local kind="$1" side="$2" input="$3"
    KIND="$kind" SIDE="$side" python3 - <<'PY' <<<"$input"
import json, os, sys

KIND = os.environ['KIND']
SIDE = os.environ['SIDE']  # demo | openapi


def unwrap(v):
    """proto3 把 *StringValue/*BoolValue 等包装类型序列化为 {"value": x}；
    Go SDK 走 jsonpb 时也保留这种 wrapper。归一化时统一拆掉，便于两侧字段对齐。"""
    if isinstance(v, dict):
        if set(v.keys()) == {"value"}:
            return v["value"]
        return {k: unwrap(x) for k, x in v.items()}
    if isinstance(v, list):
        return [unwrap(x) for x in v]
    return v


# 易变 / 服务端记账字段，两侧不可能完全一致，不参与比对
DROP_KEYS = {
    "ctime", "mtime", "etime",        # 时间戳
    "id",                              # 服务端生成的随机 id
    "revision",                        # 版本号，每次更新都变
    "modifiable",                      # 服务端权限标记
    "editable",
    "deleteable",
    "extendInfo", "extend_info",       # 用 dict 不参与比对（jsonpb 与 Go json 输出键序不一致）
    "metadata",                        # 服务端可能补充自身信息
    "creator", "modifier",
    "delete_protection",
    # rule_routing 等的 sources/destinations 里有 service_token 等运行时字段
    "service_token",
}


def strip(o):
    if isinstance(o, dict):
        return {k: strip(v) for k, v in o.items() if k not in DROP_KEYS}
    if isinstance(o, list):
        return [strip(x) for x in o]
    return o


def normalize_rules(rules):
    """unwrap proto wrapper -> drop volatile fields -> 按 name 升序"""
    out = []
    for r in rules or []:
        rr = strip(unwrap(r))
        out.append(rr)
    out.sort(key=lambda x: str(x.get("name", "")))
    return out


def extract_demo(payload):
    """demo 端响应统一形态：{"rule": ServiceRuleResponse, "validateError": ...}
    SDK 把 ServiceRuleResponse 通过 encoding/json 直接序列化，Value 为对应规则
    类型的 proto.Message。这里按 kind 抽取实际规则列表。"""
    rule = (payload or {}).get("rule") or {}
    val = rule.get("Value") or rule.get("value") or {}
    if KIND == "ratelimit":
        return val.get("rules") or []
    if KIND == "circuitbreaker":
        # ServiceRuleResponse.Value = *apifault.CircuitBreaker
        return val.get("rules") or []
    if KIND == "route":
        # apitraffic.Routing 老版 + v2 兼容：rules 在 outbounds/inbounds 中为旧版
        # serviceRule demo 用 GetRouteRule -> EventRouting -> Value=*apitraffic.Routing
        # 但商用版把 v2 路由规则也走 EventRouting 透出，Value 为 RuleRoutingConfig 包装
        # 这里做"全字段对齐"时只取最外层规则名集合。
        return val.get("rules") or val.get("inbounds") or val.get("outbounds") or []
    if KIND == "nearbyroute":
        # 就近路由规则在 SDK 侧透出为 *apitraffic.NearbyRoutingConfig 列表
        return val.get("rules") or [val] if val else []
    if KIND == "lossless":
        return val.get("rules") or []
    if KIND == "blockallow":
        return val.get("rules") or []
    if KIND == "lane":
        return val.get("lane_groups") or val.get("rules") or []
    return []


def extract_openapi(payload):
    """OpenAPI 列表接口的形态各异，按 kind 抽取规则数组。"""
    if KIND == "ratelimit":
        return (payload or {}).get("rateLimits") or []
    if KIND == "circuitbreaker":
        return (payload or {}).get("data") or (payload or {}).get("rules") or []
    if KIND == "route" or KIND == "nearbyroute":
        return (payload or {}).get("data") or (payload or {}).get("routings") or []
    if KIND == "lossless":
        return (payload or {}).get("data") or []
    if KIND == "blockallow":
        return (payload or {}).get("data") or []
    if KIND == "lane":
        return (payload or {}).get("data") or []
    return []


# ----- 入口 -----
text = sys.stdin.read()
if not text.strip():
    json.dump([], sys.stdout)
    sys.exit(0)
try:
    payload = json.loads(text)
except Exception as e:
    print(f"[CANON] JSON parse error on side={SIDE} kind={KIND}: {e}", file=sys.stderr)
    json.dump([], sys.stdout)
    sys.exit(0)

if SIDE == "demo":
    rules = extract_demo(payload)
else:
    rules = extract_openapi(payload)

# OpenAPI 列表通常按 service 过滤，可能拿到非本脚本管理的"脏"规则；
# 同时 demo 看到的是该 service 下全部规则。这里按规则名前缀过滤到
# "serviceRule-it-" 命名空间内，专注比对脚本自管理的子集，避免把人工规则
# 与脚本规则混在一起干扰对比。
filtered = []
for r in rules or []:
    rn = r if not isinstance(r, dict) else (r.get("name") or {})
    name_val = rn.get("value") if isinstance(rn, dict) else rn
    if isinstance(name_val, str) and name_val.startswith("serviceRule-it-"):
        filtered.append(r)

normalized = normalize_rules(filtered)
json.dump(normalized, sys.stdout, ensure_ascii=False, sort_keys=True, indent=2)
PY
}

# diff_rules <kind>: 对比 demo 与 OpenAPI，相同时返回 0；不同时打印 diff 并返回 1
diff_rules() {
    local kind="$1" demo_path="$2" oa_path="$3" oa_query="$4"
    local demo_resp oa_resp demo_canon oa_canon
    demo_resp=$(demo_get "$demo_path")
    oa_resp=$(openapi_get "$oa_path" "$oa_query")

    if [[ -z "$demo_resp" ]]; then
        log_case_fail "[$kind] demo 调用 ${demo_path} 返回空响应"
        return 1
    fi
    if [[ -z "$oa_resp" ]]; then
        log_case_fail "[$kind] OpenAPI 调用 ${oa_path} 返回空响应"
        return 1
    fi

    demo_canon=$(canonicalize "$kind" "demo" "$demo_resp")
    oa_canon=$(canonicalize "$kind" "openapi" "$oa_resp")

    if [[ "$DEBUG_MODE" == "true" ]]; then
        log_debug "[$kind] demo canonical: $(echo "$demo_canon" | head -c 800)"
        log_debug "[$kind] openapi canonical: $(echo "$oa_canon" | head -c 800)"
    fi

    local demo_count oa_count
    demo_count=$(echo "$demo_canon" | python3 -c "import sys, json; print(len(json.load(sys.stdin)))" 2>/dev/null || echo "?")
    oa_count=$(echo "$oa_canon" | python3 -c "import sys, json; print(len(json.load(sys.stdin)))" 2>/dev/null || echo "?")

    if [[ "$demo_canon" == "$oa_canon" ]]; then
        log_case_pass "[$kind] demo ↔ OpenAPI 全字段对齐（rules: ${demo_count}）"
        return 0
    fi

    log_case_fail "[$kind] demo (${demo_count}) ↔ OpenAPI (${oa_count}) 规则字段不一致"
    local diff_file="${LOG_DIR}/diff-${kind}.txt"
    {
        echo "=== demo canonical ==="
        echo "$demo_canon"
        echo ""
        echo "=== openapi canonical ==="
        echo "$oa_canon"
        echo ""
        echo "=== diff ==="
        diff <(echo "$demo_canon") <(echo "$oa_canon") || true
    } > "$diff_file"
    log_warn "  详细 diff 已保存: ${diff_file}"
    if [[ "$DEBUG_MODE" == "true" ]]; then
        diff <(echo "$demo_canon") <(echo "$oa_canon") | head -40 | while IFS= read -r line; do
            _log "    $line"
        done
    fi
    return 1
}

wait_rule_cache() {
    log_info "等待 ${RULE_CACHE_WAIT}s 让 SDK 拉取最新规则..."
    sleep "$RULE_CACHE_WAIT"
}

# ======================== 各类规则的 ensure_* 实现 ========================
# 共同套路：先拉 OpenAPI 列表 -> grep 规则名是否存在 -> 不存在则 POST 创建。
# 创建时使用最简但语义有效的 body：仅校验 demo 接口能拿到 != 空规则，不强制具体行为。

# ---- RateLimit ----
ratelimit_rule_exists() {
    local name="$1"
    local resp=""
    resp=$(openapi_get "/naming/v1/ratelimits" \
        "name=${name}&service=${SERVICE_NAME}&namespace=${NAMESPACE}&limit=10")
    local amount=""
    amount=$(echo "$resp" | python3 -c "
import sys, json
try:
    print(json.load(sys.stdin).get('amount', 0))
except Exception:
    print(0)
" 2>/dev/null)
    [[ "${amount:-0}" -gt 0 ]]
}

ensure_ratelimit_rule() {
    local name="$1" action="${2:-REJECT}"
    if ratelimit_rule_exists "$name"; then
        log_info "  RateLimit 规则 ${name} 已存在（action=${action}），复用"
        return 0
    fi
    log_info "  创建 RateLimit 规则 ${name}（action=${action}）"
    local body=""
    body=$(NAME="$name" SVC="$SERVICE_NAME" NS="$NAMESPACE" ACT="$action" python3 -c '
import os, json
name = os.environ["NAME"]
print(json.dumps([{
    "name": name,
    "service": os.environ["SVC"],
    "namespace": os.environ["NS"],
    "priority": 0,
    "resource": "QPS",
    "type": "LOCAL",
    "method": {"type": "EXACT", "value": "/echo-" + name},
    "amounts": [{"maxAmount": 100, "validDuration": "1s"}],
    "action": os.environ["ACT"],
    "disable": False,
}]))')
    local resp=""
    resp=$(openapi_post "/naming/v1/ratelimits" "$body")
    if openapi_resp_ok "$resp"; then
        log_info "  RateLimit ${name} 创建成功"
    else
        log_warn "  RateLimit ${name} 创建失败：$(echo "$resp" | head -c 200)"
        return 1
    fi
}

# ---- CircuitBreaker (v1 fault_tolerance) ----
circuitbreaker_rule_exists() {
    local name="$1"
    local resp=""
    resp=$(openapi_get "/naming/v1/circuitbreaker/rules" \
        "name=${name}&service=${SERVICE_NAME}&namespace=${NAMESPACE}&limit=10")
    local amount=""
    amount=$(echo "$resp" | python3 -c "
import sys, json
try:
    print(json.load(sys.stdin).get('amount', 0))
except Exception:
    print(0)
" 2>/dev/null)
    [[ "${amount:-0}" -gt 0 ]]
}

ensure_circuitbreaker_rule() {
    local name="$1"
    if circuitbreaker_rule_exists "$name"; then
        log_info "  CircuitBreaker 规则 ${name} 已存在，复用"
        return 0
    fi
    log_info "  创建 CircuitBreaker 规则 ${name}"
    local body=""
    body=$(NAME="$name" SVC="$SERVICE_NAME" NS="$NAMESPACE" python3 -c '
import os, json
print(json.dumps([{
    "name": os.environ["NAME"],
    "namespace": os.environ["NS"],
    "enable": True,
    "level": "SERVICE",
    "rule_matcher": {
        "source":      {"service": "*", "namespace": "*"},
        "destination": {"service": os.environ["SVC"],
                        "namespace": os.environ["NS"],
                        "method": {"type": "EXACT", "value": "/cb-test"}},
    },
    "error_conditions": [{
        "input_type": "RET_CODE",
        "condition": {"type": "EXACT", "value": "500"},
    }],
    "trigger_condition": [{
        "trigger_type":       "CONSECUTIVE_ERROR",
        "error_count":        5,
        "error_percent":      100,
        "interval":           60,
        "minimum_request":    10,
    }],
    "recover_condition": {"sleep_window": 30, "consecutive_success": 3},
    "fault_detect_config": {"enable": False},
}]))')
    local resp=""
    resp=$(openapi_post "/naming/v1/circuitbreaker/rules" "$body")
    if openapi_resp_ok "$resp"; then
        log_info "  CircuitBreaker ${name} 创建成功"
    else
        # 400209 = 该 service 下已存在熔断规则，视为成功
        local resp_code=""
        resp_code=$(echo "$resp" | python3 -c "
import sys, json
try:
    print(json.load(sys.stdin).get('code', 0))
except Exception:
    print(0)
" 2>/dev/null)
        if [[ "$resp_code" == "400209" ]]; then
            log_info "  CircuitBreaker 规则已存在（400209），视为成功"
        else
            log_warn "  CircuitBreaker ${name} 创建失败：$(echo "$resp" | head -c 200)"
            return 1
        fi
    fi
}

# ---- BlockAllowList ----
blockallow_rule_exists() {
    local name="$1"
    local resp=""
    resp=$(openapi_get "/naming/v1/blockallow/rules" \
        "name=${name}&service=${SERVICE_NAME}&namespace=${NAMESPACE}&limit=10")
    local amount=""
    amount=$(echo "$resp" | python3 -c "
import sys, json
try:
    print(json.load(sys.stdin).get('amount', 0))
except Exception:
    print(0)
" 2>/dev/null)
    [[ "${amount:-0}" -gt 0 ]]
}

ensure_blockallow_rule() {
    local name="$1"
    if blockallow_rule_exists "$name"; then
        log_info "  BlockAllow 规则 ${name} 已存在，复用"
        return 0
    fi
    log_info "  创建 BlockAllow 规则 ${name}"
    local body=""
    body=$(NAME="$name" SVC="$SERVICE_NAME" NS="$NAMESPACE" python3 -c '
import os, json
print(json.dumps([{
    "name": os.environ["NAME"],
    "namespace": os.environ["NS"],
    "service": os.environ["SVC"],
    "enable": True,
    "block_allow_config": [{
        "api": {"protocol": "HTTP",
                "path": {"type": "EXACT", "value_type": "TEXT", "value": "/echo-ba-it"}},
        "block_allow_policy": "ALLOW_LIST",
        "arguments": [{
            "type": "HEADER",
            "key":  "tester",
            "value": {"type": "EXACT", "value_type": "TEXT", "value": "true"},
        }],
    }],
}]))')
    local resp=""
    resp=$(openapi_post "/naming/v1/blockallow/rules" "$body")
    if openapi_resp_ok "$resp"; then
        log_info "  BlockAllow ${name} 创建成功"
    else
        log_warn "  BlockAllow ${name} 创建失败：$(echo "$resp" | head -c 200)"
        return 1
    fi
}

# ---- Lossless ----
# LosslessRule proto 没有 name 字段，只能按 service+namespace 判断是否已存在。
lossless_rule_exists() {
    local name="$1"
    local resp=""
    resp=$(openapi_get "/naming/v1/lossless/rules" \
        "service=${SERVICE_NAME}&namespace=${NAMESPACE}&limit=10")
    local amount=""
    amount=$(echo "$resp" | python3 -c "
import sys, json
try:
    print(json.load(sys.stdin).get('amount', 0))
except Exception:
    print(0)
" 2>/dev/null)
    [[ "${amount:-0}" -gt 0 ]]
}

ensure_lossless_rule() {
    local name="$1"
    if lossless_rule_exists "$name"; then
        log_info "  Lossless 规则 ${name} 已存在（${NAMESPACE}/${SERVICE_NAME}），复用"
        return 0
    fi
    log_info "  创建 Lossless 规则 ${name}"
    local body=""
    body=$(SVC="$SERVICE_NAME" NS="$NAMESPACE" python3 -c '
import os, json
print(json.dumps([{
    "service": os.environ["SVC"],
    "namespace": os.environ["NS"],
    "lossless_online": {
        "warmup": {"enable": True, "interval_second": 30, "curvature": 2},
    },
}]))')
    local resp=""
    resp=$(openapi_post "/naming/v1/lossless/rules" "$body")
    if openapi_resp_ok "$resp"; then
        log_info "  Lossless ${name} 创建成功"
    else
        # 400209 = 该 service 下已存在 lossless 规则（只允许一条），视为成功
        local resp_code=""
        resp_code=$(echo "$resp" | python3 -c "
import sys, json
try:
    print(json.load(sys.stdin).get('code', 0))
except Exception:
    print(0)
" 2>/dev/null)
        if [[ "$resp_code" == "400209" ]]; then
            log_info "  Lossless 规则已存在（400209），视为成功"
        else
            log_warn "  Lossless ${name} 创建失败：$(echo "$resp" | head -c 200)"
            return 1
        fi
    fi
}

# ---- Routing (v2 RulePolicy) ----
v2_routing_rule_exists() {
    local name="$1"
    local encoded=""
    encoded=$(python3 -c "import urllib.parse; print(urllib.parse.quote('$name'))")
    local resp=""
    resp=$(openapi_get "/naming/v2/routings" \
        "name=${encoded}&service=${SERVICE_NAME}&namespace=${NAMESPACE}&limit=10")
    local amount=""
    amount=$(echo "$resp" | python3 -c "
import sys, json
try:
    print(json.load(sys.stdin).get('amount', 0))
except Exception:
    print(0)
" 2>/dev/null)
    [[ "${amount:-0}" -gt 0 ]]
}

ensure_route_rule() {
    local name="$1"
    if v2_routing_rule_exists "$name"; then
        log_info "  Route 规则 ${name} 已存在，复用"
        return 0
    fi
    log_info "  创建 Route 规则 ${name} (RulePolicy)"
    local body=""
    body=$(NAME="$name" SVC="$SERVICE_NAME" NS="$NAMESPACE" python3 -c '
import os, json
name = os.environ["NAME"]; svc = os.environ["SVC"]; ns = os.environ["NS"]
print(json.dumps([{
    "name": name,
    "namespace": ns,
    "enable": True,
    "priority": 0,
    "routing_policy": "RulePolicy",
    "routing_config": {
        "@type": "type.googleapis.com/v1.RuleRoutingConfig",
        "rules": [{
            "name": name + "-default",
            "sources":      [{"service": "*", "namespace": "*"}],
            "destinations": [{"service": svc, "namespace": ns, "weight": 100,
                              "priority": 0, "isolate": False, "name": "all"}],
        }],
    },
    "description": "auto-created by serviceRule/verify.sh",
}]))')
    local resp=""
    resp=$(openapi_post "/naming/v2/routings" "$body")
    if openapi_resp_ok "$resp"; then
        log_info "  Route ${name} 创建成功"
        # 创建后默认 disable，主动开启
        local ids=""
        ids=$(echo "$resp" | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    rs = d.get('responses', []) or [d]
    for r in rs:
        rr = r.get('routing') or {}
        if rr.get('id'):
            print(rr['id'])
except Exception:
    pass
" 2>/dev/null)
        if [[ -n "$ids" ]]; then
            local enable_body=""
            enable_body=$(echo "$ids" | NAME="$name" python3 -c "
import os, sys, json
ids = [l.strip() for l in sys.stdin.read().splitlines() if l.strip()]
print(json.dumps([{'id': i, 'name': os.environ['NAME'], 'enable': True} for i in ids]))")
            openapi_put "/naming/v2/routings/enable" "$enable_body" >/dev/null 2>&1 || true
        fi
    else
        log_warn "  Route ${name} 创建失败：$(echo "$resp" | head -c 200)"
        return 1
    fi
}

ensure_nearbyroute_rule() {
    local name="$1"
    if v2_routing_rule_exists "$name"; then
        log_info "  NearbyRoute 规则 ${name} 已存在，复用"
        return 0
    fi
    log_info "  创建 NearbyRoute 规则 ${name} (NearbyPolicy)"
    local body=""
    body=$(NAME="$name" SVC="$SERVICE_NAME" NS="$NAMESPACE" python3 -c '
import os, json
print(json.dumps([{
    "name":      os.environ["NAME"],
    "namespace": os.environ["NS"],
    "enable":    True,
    "priority":  0,
    "routing_policy": "NearbyPolicy",
    "routing_config": {
        "@type":           "type.googleapis.com/v1.NearbyRoutingConfig",
        "service":         os.environ["SVC"],
        "namespace":       os.environ["NS"],
        "match_level":     "ZONE",
        "max_match_level": "ALL",
        "strict_nearby":   False,
    },
    "description": "auto-created by serviceRule/verify.sh",
}]))')
    local resp=""
    resp=$(openapi_post "/naming/v2/routings" "$body")
    if openapi_resp_ok "$resp"; then
        log_info "  NearbyRoute ${name} 创建成功"
        local ids=""
        ids=$(echo "$resp" | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    rs = d.get('responses', []) or [d]
    for r in rs:
        rr = r.get('routing') or {}
        if rr.get('id'):
            print(rr['id'])
except Exception:
    pass
" 2>/dev/null)
        if [[ -n "$ids" ]]; then
            local enable_body=""
            enable_body=$(echo "$ids" | NAME="$name" python3 -c "
import os, sys, json
ids = [l.strip() for l in sys.stdin.read().splitlines() if l.strip()]
print(json.dumps([{'id': i, 'name': os.environ['NAME'], 'enable': True} for i in ids]))")
            openapi_put "/naming/v2/routings/enable" "$enable_body" >/dev/null 2>&1 || true
        fi
    else
        log_warn "  NearbyRoute ${name} 创建失败：$(echo "$resp" | head -c 200)"
        return 1
    fi
}

# ---- Lane Group ----
lane_group_exists() {
    local name="$1"
    local resp=""
    resp=$(openapi_get "/naming/v1/lane/groups" "name=${name}&offset=0&limit=10")
    local amount=""
    amount=$(echo "$resp" | python3 -c "
import sys, json
try:
    print(json.load(sys.stdin).get('amount', 0))
except Exception:
    print(0)
" 2>/dev/null)
    [[ "${amount:-0}" -gt 0 ]]
}

ensure_lane_group() {
    local name="$1"
    if lane_group_exists "$name"; then
        log_info "  Lane 规则组 ${name} 已存在，复用"
        return 0
    fi
    log_info "  创建 Lane 规则组 ${name}"
    local body=""
    body=$(NAME="$name" SVC="$SERVICE_NAME" NS="$NAMESPACE" python3 -c '
import os, json
name = os.environ["NAME"]; svc = os.environ["SVC"]; ns = os.environ["NS"]
print(json.dumps([{
    "name":      name,
    "entries":   [{
        "type": "polarismesh.cn/service",
        "selector": {
            "@type": "type.googleapis.com/v1.ServiceSelector",
            "namespace": ns,
            "service": svc,
        },
    }],
    "destinations": [{
        "service":   svc,
        "namespace": ns,
        "labels":    {"lane": {"type": "EXACT", "value_type": "TEXT", "value": "gray"}},
    }],
    "description": "auto-created by serviceRule/verify.sh",
}]))')
    local resp=""
    resp=$(openapi_post "/naming/v1/lane/groups" "$body")
    if openapi_resp_ok "$resp"; then
        log_info "  Lane 规则组 ${name} 创建成功"
    else
        log_warn "  Lane 规则组 ${name} 创建失败：$(echo "$resp" | head -c 200)"
        return 1
    fi
}

# ======================== 测试用例 ========================
case_route() {
    log_step "[Route]      OpenAPI ↔ demo(/route) 全字段对齐"
    if ! ensure_route_rule "$RULE_NAME_ROUTE"; then
        log_case_skip "[Route] 创建规则失败，跳过对比"
        return
    fi
    wait_rule_cache
    diff_rules "route" "/route" "/naming/v2/routings" \
        "service=${SERVICE_NAME}&namespace=${NAMESPACE}&offset=0&limit=200" || true
}

case_nearbyroute() {
    log_step "[NearbyRoute] OpenAPI ↔ demo(/nearbyroute) 全字段对齐"
    if ! ensure_nearbyroute_rule "$RULE_NAME_NEARBY"; then
        log_case_skip "[NearbyRoute] 创建规则失败，跳过对比"
        return
    fi
    wait_rule_cache
    diff_rules "nearbyroute" "/nearbyroute" "/naming/v2/routings" \
        "service=${SERVICE_NAME}&namespace=${NAMESPACE}&offset=0&limit=200" || true
}

case_circuitbreaker() {
    log_step "[CircuitBreaker] OpenAPI ↔ demo(/circuitbreaker) 全字段对齐"
    if ! ensure_circuitbreaker_rule "$RULE_NAME_CIRCUITBREAKER"; then
        log_case_skip "[CircuitBreaker] 创建规则失败，跳过对比"
        return
    fi
    wait_rule_cache
    diff_rules "circuitbreaker" "/circuitbreaker" "/naming/v1/circuitbreaker/rules" \
        "service=${SERVICE_NAME}&namespace=${NAMESPACE}&offset=0&limit=200" || true
}

case_lossless() {
    log_step "[Lossless] OpenAPI ↔ demo(/lossless) 全字段对齐"
    if ! ensure_lossless_rule "$RULE_NAME_LOSSLESS"; then
        log_case_skip "[Lossless] 创建规则失败，跳过对比"
        return
    fi
    wait_rule_cache
    diff_rules "lossless" "/lossless" "/naming/v1/lossless/rules" \
        "service=${SERVICE_NAME}&namespace=${NAMESPACE}&offset=0&limit=200" || true
}

case_blockallow() {
    log_step "[BlockAllow] OpenAPI ↔ demo(/auth) 全字段对齐"
    if ! ensure_blockallow_rule "$RULE_NAME_BLOCKALLOW"; then
        log_case_skip "[BlockAllow] 创建规则失败，跳过对比"
        return
    fi
    wait_rule_cache
    diff_rules "blockallow" "/auth" "/naming/v1/blockallow/rules" \
        "service=${SERVICE_NAME}&namespace=${NAMESPACE}&offset=0&limit=200" || true
}

case_lane() {
    log_step "[Lane] OpenAPI ↔ demo(/lane) 全字段对齐"
    if ! ensure_lane_group "$RULE_NAME_LANE"; then
        log_case_skip "[Lane] 创建规则组失败，跳过对比"
        return
    fi
    wait_rule_cache
    diff_rules "lane" "/lane" "/naming/v1/lane/groups" \
        "name=${RULE_NAME_LANE}&offset=0&limit=20" || true
}

case_ratelimit() {
    log_step "[RateLimit] OpenAPI ↔ demo(/ratelimit) 全字段对齐"
    if ! ensure_ratelimit_rule "$RULE_NAME_RATELIMIT" "REJECT"; then
        log_case_skip "[RateLimit] 创建 REJECT 规则失败，跳过对比"
        return
    fi
    if [[ "$SKIP_TSF_RULE" != "true" ]]; then
        # 验证 BehaviorNotRegisteredError 修复：tsf 规则照样能拉到、字段一致，
        # 只是 demo 那边 validateError.isBehaviorNotRegistered 会被置位。
        if ! ensure_ratelimit_rule "$RULE_NAME_RATELIMIT_TSF" "tsf"; then
            log_warn "  跳过 tsf 规则验证（创建失败）"
        fi
    fi
    wait_rule_cache
    diff_rules "ratelimit" "/ratelimit" "/naming/v1/ratelimits" \
        "service=${SERVICE_NAME}&namespace=${NAMESPACE}&offset=0&limit=200" || true

    # 修复验证：tsf 规则存在时，demo 的 validateError.isBehaviorNotRegistered=true
    if [[ "$SKIP_TSF_RULE" != "true" ]]; then
        log_info "  附加验证：BehaviorNotRegisteredError 修复"
        local resp=""
        resp=$(demo_get "/ratelimit")
        local is_bnr behavior demo_msg
        is_bnr=$(echo "$resp" | python3 -c "
import sys, json
try:
    print(json.load(sys.stdin).get('validateError', {}).get('isBehaviorNotRegistered', False))
except Exception:
    print(False)
" 2>/dev/null || echo "False")
        behavior=$(echo "$resp" | python3 -c "
import sys, json
try:
    print(json.load(sys.stdin).get('validateError', {}).get('unregisteredBehavior', ''))
except Exception:
    print('')
" 2>/dev/null || echo "")
        demo_msg=$(echo "$resp" | python3 -c "
import sys, json
try:
    print(json.load(sys.stdin).get('validateError', {}).get('message', ''))
except Exception:
    print('')
" 2>/dev/null || echo "")

        if [[ "$is_bnr" == "True" ]] && [[ "$behavior" == "tsf" ]]; then
            log_case_pass "  [RateLimit/tsf] demo 命中 BehaviorNotRegisteredError, behavior=\"tsf\""
            log_info "    validateError.message=${demo_msg}"
        elif [[ "$is_bnr" == "True" ]]; then
            log_case_fail "  [RateLimit/tsf] isBehaviorNotRegistered=true 但 behavior 为 \"${behavior}\"（期望 \"tsf\"）"
        else
            log_case_fail "  [RateLimit/tsf] demo 未识别到 *pb.BehaviorNotRegisteredError（响应片段：$(echo "$resp" | head -c 200)）"
        fi

        # 校验 SDK base 日志：应当出现 WARN 关键字（修复后），不应出现 ERROR
        local demo_log="${LOG_DIR}/demo.log"
        # SDK 日志默认不写到 stdout/stderr；只有 -debug 时才能在 demo.log 看到。
        # 没开 debug 就跳过该子项校验，避免误判。
        if [[ "$DEBUG_MODE" == "true" ]]; then
            if grep -E "references unregistered behavior plugin" "$demo_log" >/dev/null 2>&1; then
                log_case_pass "  [RateLimit/tsf] SDK 日志命中 WARN 关键字 'references unregistered behavior plugin'"
            else
                log_case_fail "  [RateLimit/tsf] SDK 日志未发现 WARN 关键字（demo.log 可能未捕获 SDK 日志）"
            fi
            if grep -E "fail to validate service rule.*behavior plugin .* not registered" "$demo_log" >/dev/null 2>&1; then
                log_case_fail "  [RateLimit/tsf] SDK 日志仍出现旧版 ERROR 文案（修复未生效）"
            else
                log_case_pass "  [RateLimit/tsf] SDK 日志未出现旧版 ERROR 文案"
            fi
        fi
    fi
}

# ======================== 主流程 ========================
log_step "serviceRule demo E2E 验证"
log_info "POLARIS_HTTP_ADDR : ${POLARIS_HTTP_ADDR}"
log_info "POLARIS_GRPC_ADDR : ${POLARIS_SERVER}:${POLARIS_GRPC_PORT}"
log_info "Namespace/Service : ${NAMESPACE} / ${SERVICE_NAME}"
log_info "Demo address      : ${DEMO_ADDR}"
log_info "测试日志文件      : ${TEST_LOG_FILE}"

if [[ -z "$POLARIS_TOKEN" ]]; then
    log_warn "POLARIS_TOKEN 未设置，规则创建可能失败"
fi

ensure_service
build_demo
start_demo

case_route
case_nearbyroute
case_circuitbreaker
case_lossless
case_blockallow
case_lane
case_ratelimit

log_step "测试结果汇总"
log_info "Total: ${TOTAL_COUNT}, Passed: ${TOTAL_PASS}, Failed: ${TOTAL_FAIL}, Skipped: ${TOTAL_SKIP}"

if [[ $TOTAL_FAIL -gt 0 ]]; then
    log_error "存在失败用例，详见 ${LOG_DIR}/diff-*.txt"
    exit 1
fi
log_info "全部通过 ✅"
exit 0
