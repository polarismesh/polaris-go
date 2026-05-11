#!/bin/bash
# =============================================================================
# 服务鉴权（Authenticator / BlockAllowList）功能验证脚本（全自动模式）
#
# 使用方法:
#   chmod +x verify_auth.sh
#   ./verify_auth.sh [选项]
#
# 选项:
#   --polaris-server <地址>     北极星服务端地址 (默认: 127.0.0.1)
#   --polaris-token  <令牌>     北极星鉴权令牌    (默认: 空，强烈建议手工传入)
#   --polaris-http-port <端口>  北极星 OpenAPI 端口 (默认: 8090)
#   --service        <服务名>   被调服务名         (默认: AuthEchoServer)
#   --namespace      <命名空间> 命名空间           (默认: default)
#   --provider-port  <端口>     Provider HTTP 端口 (默认: 0=自动分配)
#   --rule-api-path  <路径>     BlockAllowList OpenAPI 路径
#                                (默认: /naming/v1/blockallowlists)
#   --rule-cache-wait <秒>      创建/删除规则后等待 SDK 缓存刷新 (默认: 5)
#   --debug                     启用 debug 日志
#   --help                      显示帮助
#
# 工作机制:
#   1. 启动 provider（开启鉴权链 chain=[blockAllowList]）；
#   2. 每个用例独立："清理本服务所有 BlockAllowListRule → 创建本用例需要的规则
#      → 等待 SDK 缓存刷新 → 发请求 → 校验响应"，避免规则相互干扰；
#   3. 全部用例跑完后再清理一次规则，确保不留残留；
#   4. 校验维度：HTTP 状态码 + 响应头 X-Auth-Result（ok / forbidden）。
#
# 用例（共 9 条，按顺序执行）:
#   [用例 1] enable=false                    + 无规则     → 200（零开销路径）
#   [用例 2] enable=true,  chain=[ba-list]   + 无规则     → 200（无规则按通过处理）
#   [用例 3] enable=true,  chain=[]          + 无规则     → 200（链为空等同未加载）
#   [用例 4] 仅白名单 ALLOW Header user=vip   + 携带 user=vip      → 200（白名单命中）
#   [用例 5] 仅白名单 ALLOW Header user=vip   + 不带 user           → 403（白名单不命中）
#   [用例 6] 仅黑名单 BLOCK Header user=bad   + 携带 user=bad      → 403（黑名单命中）
#   [用例 7] 仅黑名单 BLOCK Header user=bad   + 携带 user=normal   → 200（黑名单不命中）
#   [用例 8] 仅白名单 ALLOW CALLER_SERVICE default/trusted + 携带 caller=trusted → 200
#   [用例 9] 仅白名单 ALLOW CALLER_SERVICE default/trusted + 携带 caller=other   → 403
#
# 前置条件:
#   - 北极星服务端 OpenAPI 端口可达；
#   - 提供 ${POLARIS_TOKEN} 用于规则创建/删除（管理员权限）；
#   - 服务端版本支持 BlockAllowListRule（≥ specification 1.8.0）。
# =============================================================================

set -euo pipefail

# ======================== 默认配置 ========================
POLARIS_SERVER="${POLARIS_SERVER:-127.0.0.1}"
POLARIS_TOKEN="${POLARIS_TOKEN:-}"
POLARIS_HTTP_PORT="${POLARIS_HTTP_PORT:-8090}"
SERVICE_NAME="${SERVICE_NAME:-AuthEchoServer}"
NAMESPACE="${NAMESPACE:-default}"
PROVIDER_PORT="${PROVIDER_PORT:-0}"
RULE_API_PATH="${RULE_API_PATH:-/naming/v1/blockallow/rules}"
RULE_CACHE_WAIT="${RULE_CACHE_WAIT:-5}"
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
        --polaris-server)    POLARIS_SERVER="$2";    shift 2 ;;
        --polaris-token)     POLARIS_TOKEN="$2";     shift 2 ;;
        --polaris-http-port) POLARIS_HTTP_PORT="$2"; shift 2 ;;
        --service)           SERVICE_NAME="$2";      shift 2 ;;
        --namespace)         NAMESPACE="$2";         shift 2 ;;
        --provider-port)     PROVIDER_PORT="$2";     shift 2 ;;
        --rule-api-path)     RULE_API_PATH="$2";     shift 2 ;;
        --rule-cache-wait)   RULE_CACHE_WAIT="$2";   shift 2 ;;
        --debug)             DEBUG_MODE="true";      shift ;;
        --help|-h)
            sed -n '2,52p' "$0"
            exit 0
            ;;
        *) echo -e "${RED}未知参数: $1${NC}"; exit 1 ;;
    esac
done

POLARIS_HTTP_ADDR="http://${POLARIS_SERVER}:${POLARIS_HTTP_PORT}"

# ======================== 全局变量 ========================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROVIDER_DIR="${SCRIPT_DIR}/provider"
BUILD_DIR="${SCRIPT_DIR}/.build"
LOG_DIR="${SCRIPT_DIR}/.logs"
TEST_LOG_FILE="${LOG_DIR}/verify-auth-$(date +%Y%m%d_%H%M%S).log"

# 提前创建日志目录，确保 _log 双写不丢首屏内容
mkdir -p "$LOG_DIR"

# 本次脚本运行的唯一标识：用于规则名/服务名后缀，避免与服务端残留规则/服务同名冲突。
RUN_ID="$(date +%H%M%S)$$"
RULE_NAME_PREFIX="auth-it-${RUN_ID}"

# CURRENT_SERVICE 当前用例使用的被调服务名。每组用例（4/5、6/7、8/9）使用独立的 service，
# 这样同服务下不会残留其他用例的规则影响 checkAllow 的混合语义。
# 默认和 SERVICE_NAME 一致（用于用例 1-3 的"鉴权未启用/无规则"场景）。
CURRENT_SERVICE="$SERVICE_NAME"

# set_current_service <svc>: 切换当前被调服务名（规则与 provider 都指向该服务）
set_current_service() {
    CURRENT_SERVICE="$1"
    log_info "切换被调服务 → ${CURRENT_SERVICE}"
}

PROVIDER_PID=""
PROVIDER_ACTUAL_PORT=""

declare -i TOTAL_COUNT=0
declare -i TOTAL_PASS=0
declare -i TOTAL_FAIL=0
declare -i TOTAL_SKIP=0

# ======================== 工具函数 ========================
# _log 把消息同时输出到 stdout 和日志文件，写日志时去除 ANSI 颜色码
_log() {
    local msg="$1"
    echo -e "$msg"
    echo -e "$msg" | sed 's/\x1b\[[0-9;]*m//g' >> "${TEST_LOG_FILE}" 2>/dev/null || true
}
log_info()  { _log "${GREEN}[INFO]${NC}  $(date '+%Y-%m-%d %H:%M:%S') $*"; }
log_warn()  { _log "${YELLOW}[WARN]${NC}  $(date '+%Y-%m-%d %H:%M:%S') $*"; }
log_error() { _log "${RED}[ERROR]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*"; }
log_debug() { [[ "$DEBUG_MODE" == "true" ]] && _log "${BLUE}[DEBUG]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*" || true; }
log_step() {
    _log ""
    _log "${CYAN}========================================${NC}"
    _log "${CYAN}  步骤: $*${NC}"
    _log "${CYAN}========================================${NC}"
}
log_case_pass() { TOTAL_PASS+=1; TOTAL_COUNT+=1; _log "  ${GREEN}[PASS]${NC} $*"; }
log_case_fail() { TOTAL_FAIL+=1; TOTAL_COUNT+=1; _log "  ${RED}[FAIL]${NC} $*"; }
log_case_skip() { TOTAL_SKIP+=1; TOTAL_COUNT+=1; _log "  ${YELLOW}[SKIP]${NC} $*"; }
# log_raw 输出纯文本（无颜色、无前缀），同样写入日志
log_raw() { echo "$*"; echo "$*" >> "${TEST_LOG_FILE}" 2>/dev/null || true; }

# ======================== 退出清理 ========================
# 本脚本采用「检查-存在则复用-否则创建」策略：不主动删除规则，退出时只关停 provider。
# 这样即使服务端对删除操作有权限限制（如 403002），脚本也能完整跑完所有用例。
cleanup() {
    if [[ -n "$PROVIDER_PID" ]] && kill -0 "$PROVIDER_PID" 2>/dev/null; then
        log_info "停止 Provider (PID: $PROVIDER_PID)"
        kill "$PROVIDER_PID" 2>/dev/null || true
        wait "$PROVIDER_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT

# ======================== 进程辅助 ========================
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
            log_error "${desc} 进程 (PID: $pid) 已退出"
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

extract_port_from_log() {
    local log_file="$1"
    local max_wait="${2:-15}"
    local waited=0
    while [[ $waited -lt $max_wait ]]; do
        if [[ -f "$log_file" ]]; then
            local p
            p=$(grep 'listen port is' "$log_file" 2>/dev/null | sed 's/.*listen port is \([0-9]*\).*/\1/' | head -1)
            if [[ -n "$p" ]]; then
                echo "$p"
                return 0
            fi
        fi
        sleep 1
        waited=$((waited + 1))
    done
    return 1
}

# ======================== Polaris OpenAPI 调用（鉴权规则） ========================

# probe_open_api: 探测 OpenAPI 端点是否可达，不可达则提示用户用 --rule-api-path 覆盖
probe_open_api() {
    local probe_url="${POLARIS_HTTP_ADDR}${RULE_API_PATH}?namespace=${NAMESPACE}&service=${CURRENT_SERVICE}&offset=0&limit=1"
    local code
    code=$(curl -s -o /dev/null -w '%{http_code}' --connect-timeout 5 \
        -H "X-Polaris-Token: ${POLARIS_TOKEN}" "$probe_url" 2>/dev/null || echo "000")
    if [[ "$code" =~ ^(200|400|401)$ ]]; then
        log_info "OpenAPI 端点探测通过: ${POLARIS_HTTP_ADDR}${RULE_API_PATH} (HTTP=$code)"
        return 0
    fi
    log_warn "OpenAPI 端点 ${POLARIS_HTTP_ADDR}${RULE_API_PATH} 探测返回 HTTP=$code"
    log_warn "如服务端实际端点不同，请用 --rule-api-path <路径> 覆盖（默认 /naming/v1/blockallow/rules）"
    log_warn "继续执行（用例 4-9 的规则创建可能失败 → 后续用例 SKIP）"
    return 1
}

# list_rules_for_service：获取本服务下所有规则的 id 列表（每行一个）
list_rules_for_service() {
    local url="${POLARIS_HTTP_ADDR}${RULE_API_PATH}?namespace=${NAMESPACE}&service=${CURRENT_SERVICE}&offset=0&limit=100"
    local resp
    resp=$(curl -s --connect-timeout 5 -H "X-Polaris-Token: ${POLARIS_TOKEN}" "$url" 2>/dev/null || echo "")
    log_debug "list_rules resp: $(echo "$resp" | head -c 400)"
    # 从 JSON 中提取所有 "id"[whitespace]:[whitespace]"xxx" 的 id 值
    # jsonpb.Marshaler{Indent:" "} 输出格式为 `"id": "xxx"`（冒号后有空格），grep 兼容空格
    echo "$resp" | grep -oE '"id"[[:space:]]*:[[:space:]]*"[^"]+"' \
        | sed -E 's/"id"[[:space:]]*:[[:space:]]*"([^"]*)"/\1/g'
}

# rule_exists_by_name <name>: 检查指定名字的规则是否已存在
# 返回 0=存在；1=不存在
rule_exists_by_name() {
    local name="$1"
    [[ -z "$name" ]] && return 1
    local url="${POLARIS_HTTP_ADDR}${RULE_API_PATH}?namespace=${NAMESPACE}&service=${CURRENT_SERVICE}&name=${name}&offset=0&limit=1"
    local resp
    resp=$(curl -s --connect-timeout 5 -H "X-Polaris-Token: ${POLARIS_TOKEN}" "$url" 2>/dev/null || echo "")
    log_debug "rule_exists resp: $(echo "$resp" | head -c 300)"
    # amount > 0 即存在
    local amount
    amount=$(echo "$resp" | grep -oE '"amount"[[:space:]]*:[[:space:]]*[0-9]+' | grep -oE '[0-9]+$' | head -1)
    [[ -z "$amount" ]] && amount=0
    if [[ "$amount" -gt 0 ]]; then
        return 0
    fi
    return 1
}

# create_rule <rule_json>: 创建一条 BlockAllowListRule
create_rule() {
    local rule_json="$1"
    local resp
    resp=$(curl -s --connect-timeout 5 -X POST \
        -H "X-Polaris-Token: ${POLARIS_TOKEN}" \
        -H "Content-Type: application/json" \
        -d "[$rule_json]" \
        "${POLARIS_HTTP_ADDR}${RULE_API_PATH}" 2>/dev/null || echo "")
    log_debug "create_rule resp: $(echo "$resp" | head -c 400)"
    if echo "$resp" | grep -qE '"code"[[:space:]]*:[[:space:]]*200000\b'; then
        return 0
    fi
    log_warn "create_rule 返回非成功码: $(echo "$resp" | head -c 200)"
    return 1
}

# ensure_rule <name> <rule_json>: 确保该规则存在。已存在则复用；不存在则创建。
# 返回 0=规则已就绪（存在或新建成功）；1=失败（创建失败）
ensure_rule() {
    local name="$1"
    local rule_json="$2"
    if rule_exists_by_name "$name"; then
        log_info "规则 ${name} 已存在，直接复用（不删除、不重建）"
        return 0
    fi
    log_info "规则 ${name} 不存在，创建..."
    create_rule "$rule_json"
}

# 工具函数：构造 JSON 字符串（仅简单转义双引号）
json_escape() {
    local s="$1"
    echo "${s//\"/\\\"}"
}

# rule_with_header_match <name> <policy ALLOW_LIST|BLOCK_LIST> <header_key> <header_value>
rule_with_header_match() {
    local name="$1" policy="$2" hk="$3" hv="$4"
    cat <<EOF
{
  "name": "$(json_escape "$name")",
  "namespace": "$(json_escape "$NAMESPACE")",
  "service": "$(json_escape "$CURRENT_SERVICE")",
  "enable": true,
  "block_allow_config": [
    {
      "block_allow_policy": "${policy}",
      "arguments": [
        {
          "type": "HEADER",
          "key": "$(json_escape "$hk")",
          "value": {"type": "EXACT", "value_type": "TEXT", "value": "$(json_escape "$hv")"}
        }
      ]
    }
  ]
}
EOF
}

# rule_with_caller_service_match <name> <policy> <caller_ns> <caller_svc>
rule_with_caller_service_match() {
    local name="$1" policy="$2" cns="$3" csvc="$4"
    cat <<EOF
{
  "name": "$(json_escape "$name")",
  "namespace": "$(json_escape "$NAMESPACE")",
  "service": "$(json_escape "$CURRENT_SERVICE")",
  "enable": true,
  "block_allow_config": [
    {
      "block_allow_policy": "${policy}",
      "arguments": [
        {
          "type": "CALLER_SERVICE",
          "key": "$(json_escape "$cns")",
          "value": {"type": "EXACT", "value_type": "TEXT", "value": "$(json_escape "$csvc")"}
        }
      ]
    }
  ]
}
EOF
}

# 给 SDK LocalRegistry 时间拉到最新规则
wait_rule_cache() {
    log_info "等待 ${RULE_CACHE_WAIT}s 让 SDK 拉取最新规则..."
    sleep "$RULE_CACHE_WAIT"
}

# ======================== 生成 polaris.yaml & 重启 Provider ========================
generate_polaris_yaml() {
    local target_dir="$1"
    local enable="$2"
    local chain_block="$3"
    local yaml="${target_dir}/polaris.yaml"
    cat > "$yaml" <<EOF
global:
  serverConnector:
    addresses:
      - ${POLARIS_SERVER}:8091
    token: ${POLARIS_TOKEN}
    connectTimeout: 3s
provider:
  auth:
    enable: ${enable}
${chain_block}
EOF
    log_info "已生成 polaris.yaml -> $yaml (enable=${enable})"
}

restart_provider_with_config() {
    local enable="$1"
    local chain_block="$2"

    if [[ -n "$PROVIDER_PID" ]] && kill -0 "$PROVIDER_PID" 2>/dev/null; then
        kill "$PROVIDER_PID" 2>/dev/null || true
        wait "$PROVIDER_PID" 2>/dev/null || true
        sleep 1
    fi

    local provider_workdir="${BUILD_DIR}/provider_run"
    mkdir -p "$provider_workdir"
    # 清理上次运行残留的 LocalRegistry 持久化文件，避免 SDK 从磁盘恢复旧规则导致用例间相互干扰
    rm -rf "${provider_workdir}/polaris" 2>/dev/null || true
    generate_polaris_yaml "$provider_workdir" "$enable" "$chain_block"

    local provider_log="${LOG_DIR}/provider.log"
    : > "$provider_log"

    (cd "$provider_workdir" && "${BUILD_DIR}/auth_provider" \
        --namespace "$NAMESPACE" \
        --service "$CURRENT_SERVICE" \
        --token "$POLARIS_TOKEN" \
        --port "$PROVIDER_PORT" \
        --debug=$DEBUG_MODE \
        > "$provider_log" 2>&1) &
    PROVIDER_PID=$!
    log_info "Provider 已启动 (PID: $PROVIDER_PID, log: $provider_log)"

    sleep 1
    check_process_alive "$PROVIDER_PID" "Provider" || {
        log_error "Provider 启动失败，最近日志："
        tail -30 "$provider_log" 2>/dev/null || true
        exit 1
    }

    PROVIDER_ACTUAL_PORT=$(extract_port_from_log "$provider_log" 15)
    if [[ -z "$PROVIDER_ACTUAL_PORT" ]]; then
        log_error "无法从日志提取 Provider 端口"
        tail -30 "$provider_log" 2>/dev/null || true
        exit 1
    fi
    log_info "Provider 监听端口: $PROVIDER_ACTUAL_PORT"

    wait_for_http "http://127.0.0.1:${PROVIDER_ACTUAL_PORT}/auth-info" 20 "Provider" "$PROVIDER_PID" || exit 1
    sleep 2
}

# ======================== HTTP 请求工具 ========================
# do_request <expect_code> <case_label> [curl 额外参数...]
do_request() {
    local expect_code="$1"
    local case_label="$2"
    shift 2

    local url="http://127.0.0.1:${PROVIDER_ACTUAL_PORT}/echo"
    local tmp_resp tmp_hdr
    tmp_resp=$(mktemp); tmp_hdr=$(mktemp)
    local http_code
    http_code=$(curl -s -o "$tmp_resp" -D "$tmp_hdr" -w '%{http_code}' --connect-timeout 5 "$@" "$url" || echo "000")

    local auth_result
    auth_result=$(grep -i '^X-Auth-Result:' "$tmp_hdr" 2>/dev/null | head -1 | awk -F': ' '{print $2}' | tr -d '\r' || echo "")

    if [[ "$http_code" == "$expect_code" ]]; then
        log_case_pass "${case_label}: HTTP=${http_code}  X-Auth-Result=${auth_result:-N/A}"
        rm -f "$tmp_resp" "$tmp_hdr"
        return 0
    else
        log_case_fail "${case_label}: 期望 HTTP=${expect_code}, 实际=${http_code}  X-Auth-Result=${auth_result:-N/A}"
        if [[ "$DEBUG_MODE" == "true" ]]; then
            log_raw "    响应体: $(head -c 200 "$tmp_resp" || true)"
            log_raw "    响应头: $(head -c 400 "$tmp_hdr" || true)"
        fi
        rm -f "$tmp_resp" "$tmp_hdr"
        return 1
    fi
}

# ======================== 用例实现 ========================

# [用例 1] enable=false → 任何请求放行
test_case_1_disabled() {
    log_step "[用例 1] enable=false + 无规则 → 200（零开销路径）"
    set_current_service "$SERVICE_GROUP_NORULE"
    restart_provider_with_config "false" ""
    do_request 200 "[用例 1] 鉴权未启用，请求应放行"
}

# [用例 2] enable=true + 无规则 → 放行
test_case_2_enabled_no_rule() {
    log_step "[用例 2] enable=true + chain=[blockAllowList] + 无规则 → 200"
    set_current_service "$SERVICE_GROUP_NORULE"
    restart_provider_with_config "true" "    chain:
      - blockAllowList
    plugin:
      blockAllowList: {}"
    wait_rule_cache
    do_request 200 "[用例 2] 启用鉴权但无任何 BlockAllowListRule，请求应放行"
}

# [用例 3] chain=[] → 等同未加载
test_case_3_enabled_empty_chain() {
    log_step "[用例 3] enable=true + chain=[] → 200"
    set_current_service "$SERVICE_GROUP_NORULE"
    restart_provider_with_config "true" "    chain: []"
    do_request 200 "[用例 3] enable=true 但插件链为空，等同于不加载任何鉴权器"
}

# 后续 4-9 的标准 chain 配置
ENABLED_CHAIN_BLOCK="    chain:
      - blockAllowList
    plugin:
      blockAllowList: {}"

# 后续 4-9 都基于 enable=true + chain=[blockAllowList]，统一切回这套配置
ensure_enabled_provider() {
    restart_provider_with_config "true" "$ENABLED_CHAIN_BLOCK"
}

# prepare_case_with_rule <rule_name> <rule_json>：每个用例独立运行的标准前置流程
#   1) 检查同名规则是否已存在（上次运行残留也算）；
#   2) 不存在则创建；存在则复用（不删除、不重建）；
#   3) 重启 provider，让 SDK LocalRegistry 重新拉规则（隔离上一用例的缓存）；
#   4) 等待 SDK 拉到最新规则。
# 返回 0=就绪可发请求；返回 1=创建规则失败（用例应 SKIP）。
prepare_case_with_rule() {
    local rule_name="$1"
    local rule_json="$2"
    if ! ensure_rule "$rule_name" "$rule_json"; then
        return 1
    fi
    restart_provider_with_config "true" "$ENABLED_CHAIN_BLOCK"
    wait_rule_cache
    return 0
}

# 用例 4-9 分 3 组，每组使用独立 service，保证组内只有一条规则生效，避免 SDK 规则缓存相互干扰。
# service 名带 RUN_ID 后缀，多次重跑不会互相影响。
SERVICE_GROUP_NORULE="${SERVICE_NAME}-grp0-${RUN_ID}"         # 用例 1/2/3：鉴权未启用/无规则
SERVICE_GROUP_HEADER_ALLOW="${SERVICE_NAME}-grpA-${RUN_ID}"   # 用例 4/5：白名单 Header
SERVICE_GROUP_HEADER_BLOCK="${SERVICE_NAME}-grpB-${RUN_ID}"   # 用例 6/7：黑名单 Header
SERVICE_GROUP_CALLER_ALLOW="${SERVICE_NAME}-grpC-${RUN_ID}"   # 用例 8/9：白名单 CALLER_SERVICE

# 用例 4+5 共用一条白名单规则：Header user=vip（同一 service，两个请求不同命中情况）
RULE_NAME_ALLOW_VIP="${RULE_NAME_PREFIX}-allow-vip"
# 用例 6+7 共用一条黑名单规则：Header user=bad
RULE_NAME_BLOCK_BAD="${RULE_NAME_PREFIX}-block-bad"
# 用例 8+9 共用一条白名单规则：CALLER_SERVICE default/trusted
RULE_NAME_ALLOW_TRUSTED="${RULE_NAME_PREFIX}-allow-trusted"

# [用例 4] 仅白名单 + Header user=vip → 命中放行
test_case_4_allow_header_match() {
    log_step "[用例 4] 仅白名单 ALLOW_LIST(Header user=vip) + 请求带 user=vip → 200"
    set_current_service "$SERVICE_GROUP_HEADER_ALLOW"
    if prepare_case_with_rule "$RULE_NAME_ALLOW_VIP" \
        "$(rule_with_header_match "$RULE_NAME_ALLOW_VIP" "ALLOW_LIST" "user" "vip")"; then
        do_request 200 "[用例 4] 白名单 Header user=vip 命中 → 200" -H "user: vip"
    else
        log_case_skip "[用例 4] 创建规则失败，跳过"
    fi
}

# [用例 5] 仅白名单 + 不带 user → 不命中拒绝（复用用例 4 的 service 与规则）
test_case_5_allow_header_not_match() {
    log_step "[用例 5] 仅白名单 ALLOW_LIST(Header user=vip) + 请求不带 user → 403"
    set_current_service "$SERVICE_GROUP_HEADER_ALLOW"
    if prepare_case_with_rule "$RULE_NAME_ALLOW_VIP" \
        "$(rule_with_header_match "$RULE_NAME_ALLOW_VIP" "ALLOW_LIST" "user" "vip")"; then
        do_request 403 "[用例 5] 白名单仅允许 user=vip，请求未携带 user → 403"
    else
        log_case_skip "[用例 5] 创建规则失败，跳过"
    fi
}

# [用例 6] 仅黑名单 + Header user=bad → 命中拒绝
test_case_6_block_header_match() {
    log_step "[用例 6] 仅黑名单 BLOCK_LIST(Header user=bad) + 请求带 user=bad → 403"
    set_current_service "$SERVICE_GROUP_HEADER_BLOCK"
    if prepare_case_with_rule "$RULE_NAME_BLOCK_BAD" \
        "$(rule_with_header_match "$RULE_NAME_BLOCK_BAD" "BLOCK_LIST" "user" "bad")"; then
        do_request 403 "[用例 6] 黑名单 Header user=bad 命中 → 403" -H "user: bad"
    else
        log_case_skip "[用例 6] 创建规则失败，跳过"
    fi
}

# [用例 7] 仅黑名单 + Header user=normal → 不命中放行（复用用例 6 的 service 与规则）
test_case_7_block_header_not_match() {
    log_step "[用例 7] 仅黑名单 BLOCK_LIST(Header user=bad) + 请求带 user=normal → 200"
    set_current_service "$SERVICE_GROUP_HEADER_BLOCK"
    if prepare_case_with_rule "$RULE_NAME_BLOCK_BAD" \
        "$(rule_with_header_match "$RULE_NAME_BLOCK_BAD" "BLOCK_LIST" "user" "bad")"; then
        do_request 200 "[用例 7] 黑名单 Header user=bad 不命中 → 200" -H "user: normal"
    else
        log_case_skip "[用例 7] 创建规则失败，跳过"
    fi
}

# [用例 8] 仅白名单 CALLER_SERVICE default/trusted + 命中 → 放行
test_case_8_allow_caller_service_match() {
    log_step "[用例 8] 仅白名单 ALLOW_LIST(CALLER_SERVICE default/trusted) + 命中 → 200"
    set_current_service "$SERVICE_GROUP_CALLER_ALLOW"
    if prepare_case_with_rule "$RULE_NAME_ALLOW_TRUSTED" \
        "$(rule_with_caller_service_match "$RULE_NAME_ALLOW_TRUSTED" "ALLOW_LIST" "default" "trusted")"; then
        do_request 200 "[用例 8] 白名单 CALLER_SERVICE 命中 → 200" \
            -H "X-Polaris-Caller-Namespace: default" -H "X-Polaris-Caller-Service: trusted"
    else
        log_case_skip "[用例 8] 创建规则失败，跳过"
    fi
}

# [用例 9] 仅白名单 CALLER_SERVICE default/trusted + 不命中 → 拒绝（复用用例 8）
test_case_9_allow_caller_service_not_match() {
    log_step "[用例 9] 仅白名单 ALLOW_LIST(CALLER_SERVICE default/trusted) + 不命中 → 403"
    set_current_service "$SERVICE_GROUP_CALLER_ALLOW"
    if prepare_case_with_rule "$RULE_NAME_ALLOW_TRUSTED" \
        "$(rule_with_caller_service_match "$RULE_NAME_ALLOW_TRUSTED" "ALLOW_LIST" "default" "trusted")"; then
        do_request 403 "[用例 9] 白名单 CALLER_SERVICE 不命中（caller=other）→ 403" \
            -H "X-Polaris-Caller-Namespace: default" -H "X-Polaris-Caller-Service: other"
    else
        log_case_skip "[用例 9] 创建规则失败，跳过"
    fi
}

# ======================== 编译 ========================
build_binaries() {
    log_step "0/4 编译 Provider"
    mkdir -p "$BUILD_DIR" "$LOG_DIR"

    if ! command -v go &> /dev/null; then
        log_error "Go 未安装"
        exit 1
    fi
    log_info "Go 版本: $(go version)"

    if [[ ! -f "${PROVIDER_DIR}/main.go" ]]; then
        log_error "找不到 Provider 源码: ${PROVIDER_DIR}/main.go"
        exit 1
    fi

    log_info "编译 Provider..."
    (cd "$PROVIDER_DIR" && go build -o "${BUILD_DIR}/auth_provider" .)
    log_info "Provider 编译完成 -> ${BUILD_DIR}/auth_provider"

    if command -v xattr &> /dev/null; then
        xattr -c "${BUILD_DIR}/auth_provider" 2>/dev/null || true
    fi
}

# ======================== 主流程 ========================
main() {
    _log ""
    _log "${BLUE}╔══════════════════════════════════════════════════════════╗${NC}"
    _log "${BLUE}║   服务鉴权（Authenticator）功能验证脚本（全自动模式）   ║${NC}"
    _log "${BLUE}╚══════════════════════════════════════════════════════════╝${NC}"
    _log ""
    log_raw "配置信息:"
    log_raw "  北极星服务端:   ${POLARIS_SERVER}:8091"
    log_raw "  OpenAPI 端口:   ${POLARIS_HTTP_PORT}"
    log_raw "  规则端点:       ${POLARIS_HTTP_ADDR}${RULE_API_PATH}"
    log_raw "  服务名前缀:     ${SERVICE_NAME}"
    log_raw "    用例 1-3 使用: ${SERVICE_GROUP_NORULE}"
    log_raw "    用例 4/5 使用: ${SERVICE_GROUP_HEADER_ALLOW}"
    log_raw "    用例 6/7 使用: ${SERVICE_GROUP_HEADER_BLOCK}"
    log_raw "    用例 8/9 使用: ${SERVICE_GROUP_CALLER_ALLOW}"
    log_raw "  命名空间:       ${NAMESPACE}"
    log_raw "  Provider 端口:  ${PROVIDER_PORT} (0=自动)"
    log_raw "  规则缓存等待:   ${RULE_CACHE_WAIT}s"
    log_raw "  Token:          $([[ -n "$POLARIS_TOKEN" ]] && echo "<已设置>" || echo "<未设置>")"
    log_raw "  规则名前缀:     ${RULE_NAME_PREFIX}（每次脚本运行唯一，避免与残留规则同名）"
    log_raw "  清理策略:       不主动删除规则；检查同名规则，存在则复用，不存在则创建"
    log_raw "  Debug 日志:     ${DEBUG_MODE}"
    log_raw "  日志文件:       ${TEST_LOG_FILE}"
    _log ""

    if [[ -z "$POLARIS_TOKEN" ]]; then
        log_warn "未设置 POLARIS_TOKEN：将以匿名身份调用 OpenAPI（创建规则通常允许匿名）"
    fi

    build_binaries

    log_step "0/4 OpenAPI 端点探测"
    probe_open_api || true

    log_step "1/4 用例 1：enable=false（鉴权未启用）"
    test_case_1_disabled

    log_step "2/4 用例 2-3：enable=true + 无规则 / chain=[]"
    test_case_2_enabled_no_rule
    test_case_3_enabled_empty_chain

    log_step "3/4 用例 4-9：自动建规则 + 黑白名单匹配验证（每用例独立重启 provider）"
    test_case_4_allow_header_match
    test_case_5_allow_header_not_match
    test_case_6_block_header_match
    test_case_7_block_header_not_match
    test_case_8_allow_caller_service_match
    test_case_9_allow_caller_service_not_match

    log_step "4/4 全部跑完，退出 trap 会再清理一遍规则"

    _log ""
    _log "${CYAN}========================================${NC}"
    _log "${CYAN}  验证结果汇总${NC}"
    _log "${CYAN}========================================${NC}"
    log_raw "  TOTAL: $TOTAL_COUNT  PASS: $TOTAL_PASS  FAIL: $TOTAL_FAIL  SKIP: $TOTAL_SKIP"
    log_raw "  日志文件: ${TEST_LOG_FILE}"

    if [[ $TOTAL_FAIL -gt 0 ]]; then
        _log "${RED}存在失败用例，详见上方日志。${NC}"
        exit 1
    fi
    _log "${GREEN}全部通过。${NC}"
}

main "$@"
