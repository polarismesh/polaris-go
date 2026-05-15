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
#   --debug                     启用 debug 模式：脚本自身打印更详细的诊断（OpenAPI 响应、curl
#                                响应体），同时给 provider / consumer 进程透传 --debug=true，
#                                让 polaris-go SDK 把日志级别切到 debug（写到 ./polaris/log/auth/
#                                polaris-auth.log 等），便于排查规则评估细节。生产排查鉴权问题
#                                时建议带上此参数。
#   --help                      显示帮助
#
# 工作机制:
#   1. 启动 provider（开启鉴权链 chain=[blockAllowList]）；
#   2. 服务复用：用例 3-5 用 SERVICE_NAME-norule（不建规则），6-28 用 SERVICE_NAME（17 条规则常驻）；
#   3. 规则跨运行复用：固定规则名 + ensure_rule 幂等创建；同 service 下多条规则按 EXACT api.path 互不干扰；
#   4. provider 进程复用：仅在「polaris.yaml 配置变化（用例 3/4/5）」或「--register-metadata 变化
#      （用例 12 入端切 env=prod、用例 14 入端切回无 metadata）」时重启 provider，其他用例只 ensure_rule
#      + wait_rule_cache 让 SDK LocalRegistry 拉到新规则；全程仅 ~5 次重启而非 26 次；
#   5. 校验维度：HTTP 状态码 + 响应头 X-Auth-Result（ok / forbidden）。
#
# 用例（共 26 条，按顺序执行）:
#   用例 3-5 走 SERVICE_NAME-norule（service 下无任何规则），验证 SDK 配置侧"放行"语义；
#   用例 6-28 走 SERVICE_NAME，下面常驻 17 条规则（10 ALLOW_LIST + 7 BLOCK_LIST）；每条带
#   EXACT api.path 限定，请求经 matchMethod 过滤后只剩 1 条进入 matchArguments。
#   containsAllowList 在 matchMethod 之前置位（与 polaris-java 一致）→ service 下存在 ALLOW_LIST
#   即让所有未命中规则的请求落入 return !containsAllowList = false → 403。
#
#   [用例 3]  enable=false                                          → 200（鉴权未启用，零开销路径）
#   [用例 4]  enable=true, chain=[ba-list], service 无规则           → 200（getBlockAllowListRules 返回空 → 直接放行）
#   [用例 5]  enable=true, chain=[]                                  → 200（authenticators 切片为空，等同未加载）
#   [用例 6]  path=/echo-allow-vip,    matchMethod 命中 ALLOW user=vip 规则，args 命中           → 200
#   [用例 7]  path=/echo-allow-vip,    matchMethod 命中 ALLOW user=vip 规则，args 不命中（混合）  → 403
#   [用例 8]  path=/echo-block-bad,    matchMethod 命中 BLOCK user=bad 规则，args 命中           → 403
#   [用例 9]  path=/echo-block-bad,    matchMethod 命中 BLOCK user=bad 规则，args 不命中（混合）  → 403
#   [用例 10]  path=/echo-allow-trusted, matchMethod 命中 ALLOW CALLER_SERVICE 规则，args 命中    → 200
#   [用例 11]  path=/echo-allow-trusted, matchMethod 命中 ALLOW CALLER_SERVICE 规则，args 不命中  → 403
#   [用例 12] path=/echo-callee-prod,  matchMethod 命中 ALLOW CALLEE_METADATA env=prod 规则，本端 metadata 命中 → 200
#   [用例 13] path=/echo-callee-dev,   matchMethod 命中 ALLOW CALLEE_METADATA env=dev 规则，本端 metadata 不命中 → 403
#   [用例 14] path=/echo-custom-v1,    matchMethod 命中 ALLOW CUSTOM biz=v1 规则，args 命中     → 200
#   [用例 15] path=/echo-custom-v2,    matchMethod 命中 ALLOW CUSTOM biz=v2 规则，args 不命中   → 403
#   --- 用例 16-20：MatchString 6 种类型端到端覆盖（EXACT 已由 6-15 验证；其余 5 种全部 BLOCK_LIST + 命中 → 403）
#   [用例 16] path=/echo-mt-regex,        HEADER user REGEX ^bad-.*$，请求 user=bad-foo  → 403
#   [用例 17] path=/echo-mt-not-equals,   HEADER user NOT_EQUALS admin，请求 user=guest  → 403
#   [用例 18] path=/echo-mt-in,           HEADER user IN bad1,bad2,bad3，请求 user=bad2  → 403
#   [用例 19] path=/echo-mt-not-in,       HEADER user NOT_IN admin,operator，请求 user=guest → 403
#   [用例 20] path=/echo-mt-range,        HEADER user RANGE 100~200，请求 user=150        → 403
#   --- 用例 21-26：维度补齐（QUERY / CALLER_IP / CALLER_METADATA），让 7 种 MatchArgument 端到端全覆盖
#   [用例 21] path=/echo-query-cn,        ALLOW QUERY region=cn，请求 ?region=cn          → 200
#   [用例 22] path=/echo-query-cn,        ALLOW QUERY region=cn，请求 ?region=us          → 403
#   [用例 23] path=/echo-caller-ip-block, BLOCK CALLER_IP loopback REGEX，本机 curl       → 403
#   [用例 24] path=/echo-caller-ip-allow, ALLOW CALLER_IP loopback REGEX，本机 curl       → 200
#   [用例 25] path=/echo-caller-meta-gold, ALLOW CALLER_METADATA tier=gold，主调带 tier=gold → 200
#   [用例 26] path=/echo-caller-meta-gold, ALLOW CALLER_METADATA tier=gold，主调带 tier=silver → 403
#   --- 用例 27-28：单规则多 MatchArgument AND 关系（matchArguments 任一不满足即整体不命中）
#   [用例 27] path=/echo-and-both,        ALLOW [HEADER user=vip ∧ QUERY region=cn]，两条都满足 → 200
#   [用例 28] path=/echo-and-both,        ALLOW [HEADER user=vip ∧ QUERY region=cn]，仅 HEADER 满足 → 403
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
            sed -n '2,60p' "$0"
            exit 0
            ;;
        *) echo -e "${RED}未知参数: $1${NC}"; exit 1 ;;
    esac
done

POLARIS_HTTP_ADDR="http://${POLARIS_SERVER}:${POLARIS_HTTP_PORT}"

# ======================== 全局变量 ========================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROVIDER_DIR="${SCRIPT_DIR}/provider"
CONSUMER_DIR="${SCRIPT_DIR}/consumer"
BUILD_DIR="${SCRIPT_DIR}/.build"
LOG_DIR="${SCRIPT_DIR}/.logs"
TEST_LOG_FILE="${LOG_DIR}/verify-auth-$(date +%Y%m%d_%H%M%S).log"

# 提前创建日志目录，确保 _log 双写不丢首屏内容
mkdir -p "$LOG_DIR"

# CURRENT_SERVICE 当前用例使用的被调服务名。
# 用例 3-5 使用 SERVICE_NAME_NORULE（不建规则，验证放行场景）；
# 用例 6-28 使用 SERVICE_NAME，17 条规则常驻该服务下，按 EXACT api.path 互不干扰。
# 服务名跨运行复用，避免不断生成新 service。
CURRENT_SERVICE="$SERVICE_NAME"

# set_current_service <svc>: 切换当前被调服务名（规则与 provider 都指向该服务）
set_current_service() {
    CURRENT_SERVICE="$1"
    log_info "切换被调服务 → ${CURRENT_SERVICE}"
}

PROVIDER_PID=""
PROVIDER_ACTUAL_PORT=""

# Consumer 进程状态（仅用例 1/2 需要；跑完立即释放）
CONSUMER_PID=""
CONSUMER_PORT="${CONSUMER_PORT:-38080}"
# Consumer demo 自身服务名（X-Polaris-Caller-Service 透传值），与 provider 服务名分离。
# consumer/main.go 已支持 --selfService 参数，默认就是 AuthEchoClient。
CONSUMER_SELF_SERVICE="${CONSUMER_SELF_SERVICE:-AuthEchoClient}"

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
    if [[ -n "$CONSUMER_PID" ]] && kill -0 "$CONSUMER_PID" 2>/dev/null; then
        log_info "停止 Consumer (PID: $CONSUMER_PID)"
        kill "$CONSUMER_PID" 2>/dev/null || true
        wait "$CONSUMER_PID" 2>/dev/null || true
    fi
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
    log_warn "继续执行（用例 6-28 的规则创建可能失败 → 后续用例 SKIP）"
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

# list_rule_names_for_service <service>：获取指定 service 下所有规则的 name 列表（每行一个）。
# 与 list_rules_for_service 的差别：可以指定任意 service（不依赖 CURRENT_SERVICE 全局变量），
# 提取 name 字段而非 id，专供 precheck_existing_rules 在脚本启动早期做卫生检查使用。
#
# 注意 set -e + pipefail 兼容性：内部用 `|| true` 兜底，确保 grep / sed 在无匹配时
# 不会让外层调用方因退出码非 0 触发 set -e 退出。
list_rule_names_for_service() {
    local svc="$1"
    local url="${POLARIS_HTTP_ADDR}${RULE_API_PATH}?namespace=${NAMESPACE}&service=${svc}&offset=0&limit=200"
    local resp
    resp=$(curl -s --connect-timeout 5 -H "X-Polaris-Token: ${POLARIS_TOKEN}" "$url" 2>/dev/null || echo "")
    # grep 无匹配时返回 1，会被 pipefail 上抛——这里用 || true 兜底，让无规则的 service
    # 静默返回空字符串而非异常退出
    echo "$resp" | grep -oE '"name"[[:space:]]*:[[:space:]]*"[^"]+"' 2>/dev/null \
        | sed -E 's/"name"[[:space:]]*:[[:space:]]*"([^"]*)"/\1/g' || true
}

# precheck_existing_rules：脚本启动早期对涉及的 service 做卫生检查。
# 背景：若上次脚本运行后或人工在控制台留下了非 auth-it-* 命名的规则（如 path=*、name=test 等
# 通配规则），会让 path 隔离的用例语义失效——因为这类规则会让 service 下其他 path 的请求也被
# 误命中。例如曾遇到 service=AuthEchoServer 下 OpenAPI 看不见但 Discover 能拉到的 name=test
# path=* HEADER user=vip ALLOW_LIST 规则，导致用例 28（AND 部分命中）被误放行成 200。
#
# 此函数对每个相关 service 拉一次规则列表，提示所有非脚本命名规约的规则名。仅打印告警，
# 不阻断脚本——允许用户在已知脏规则存在的情况下继续跑，便于一边排查一边迭代。
#
# 命名规约：
#   - SERVICE_NAME 下：合法规则全部以 `auth-it-` 开头（脚本创建并维护）；
#     另允许历史名 `auth-smoke-allow-caller-service`（旧版独立 smoke 脚本残留，路径与新规则不冲突）。
#   - SERVICE_NAME_NORULE 下：用例 3-5 期望此 service 下「无任何规则」，发现任何规则都告警。
precheck_existing_rules() {
    log_info "扫描 ${SERVICE_NAME} 与 ${SERVICE_NAME_NORULE} 下的现有规则，识别非脚本管理的脏数据..."
    local found_dirty=false

    # 1) ${SERVICE_NAME}：脚本自己创建并维护的规则全部以 `auth-it-` 开头，
    #    历史名 `auth-smoke-allow-caller-service` 也属于脚本管理范围（旧版独立 smoke 脚本残留，
    #    与新规则路径不冲突）。除此之外的规则全部视为非脚本管理（疑似脏数据）。
    local svc_names
    svc_names=$(list_rule_names_for_service "$SERVICE_NAME")
    local managed_count=0
    local dirty_count=0
    local dirty_list=""
    if [[ -n "$svc_names" ]]; then
        while IFS= read -r name; do
            [[ -z "$name" ]] && continue
            if [[ "$name" == auth-it-* ]] || [[ "$name" == "auth-smoke-allow-caller-service" ]]; then
                managed_count=$((managed_count + 1))
            else
                dirty_count=$((dirty_count + 1))
                dirty_list+="    - ${name}"$'\n'
                found_dirty=true
            fi
        done <<< "$svc_names"
    fi
    log_info "${SERVICE_NAME}: 脚本管理 ${managed_count} 条（auth-it-* / auth-smoke-*），不计入下方统计"
    if [[ $dirty_count -gt 0 ]]; then
        log_warn "${SERVICE_NAME}: 发现 ${dirty_count} 条非脚本管理的规则（疑似脏数据）："
        log_raw "$dirty_list"
        log_warn "  这些规则可能干扰用例 6-28 的 path 隔离语义（特别是 path=* / 通配匹配规则），"
        log_warn "  例如曾遇到 path=* + ALLOW_LIST + HEADER user=vip 的脏规则让用例 28（AND 部分"
        log_warn "  命中）被误放行成 200。建议先去 Polaris 控制台清理这些规则后再跑。"
    fi

    # 2) ${SERVICE_NAME_NORULE}：用例 3-5 依赖此 service 下「无任何规则」才能验证放行语义。
    #    脚本运行期间不在此 service 下创建任何规则，所以这里**任何**规则都视为脏数据。
    local norule_names
    norule_names=$(list_rule_names_for_service "$SERVICE_NAME_NORULE")
    local norule_dirty=0
    if [[ -n "$norule_names" ]]; then
        while IFS= read -r n; do
            [[ -z "$n" ]] && continue
            norule_dirty=$((norule_dirty + 1))
        done <<< "$norule_names"
    fi
    if [[ $norule_dirty -gt 0 ]]; then
        log_warn "${SERVICE_NAME_NORULE}: 发现 ${norule_dirty} 条规则（应当为空）："
        echo "$norule_names" | while IFS= read -r n; do [[ -n "$n" ]] && log_raw "    - $n"; done
        log_warn "  用例 3-5 依赖此 service 无规则才能验证'鉴权未启用 / chain=[] / 无规则'三种放行场景。"
        log_warn "  建议先去 Polaris 控制台清理这些规则后再跑。"
        found_dirty=true
    fi

    if [[ "$found_dirty" == "true" ]]; then
        log_warn "脚本继续执行，但若后续用例失败请先清理上述脏规则。"
    else
        log_info "规则集干净（仅含脚本管理的规则），可继续执行。"
    fi
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

# update_rule <rule_json>: PUT 更新一条规则
# 用例 2（smoke 不命中场景）需要把规则的 caller_service 改成「不匹配」的值，
# 才能验证「主调身份不命中 → 拒绝」分支；用 PUT 更新而不是删除重建，避开删除权限。
update_rule() {
    local rule_json="$1"
    local resp
    resp=$(curl -s --connect-timeout 5 -X PUT \
        -H "X-Polaris-Token: ${POLARIS_TOKEN}" \
        -H "Content-Type: application/json" \
        -d "[$rule_json]" \
        "${POLARIS_HTTP_ADDR}${RULE_API_PATH}" 2>/dev/null || echo "")
    log_debug "update_rule resp: $(echo "$resp" | head -c 300)"
    if echo "$resp" | grep -qE '"code"[[:space:]]*:[[:space:]]*200000\b'; then
        return 0
    fi
    log_warn "update_rule 返回非成功码: $(echo "$resp" | head -c 200)"
    return 1
}

# 工具函数：构造 JSON 字符串（仅简单转义双引号）
json_escape() {
    local s="$1"
    echo "${s//\"/\\\"}"
}

# build_api_path_block <path>: 构造规则 JSON 中 api 段的 path 限定子句。
# path 为空字符串时返回空（不限定 path）；非空时限定为 EXACT 匹配，
# 这样同 service 下不同用例的规则可以靠 path 互不干扰，无需删除/重建。
build_api_path_block() {
    local p="$1"
    if [[ -z "$p" ]]; then
        echo ""
        return
    fi
    cat <<EOF
"api": {
        "protocol": "HTTP",
        "path": {"type": "EXACT", "value_type": "TEXT", "value": "$(json_escape "$p")"}
      },
      
EOF
}

# rule_with_header_match <name> <policy> <header_key> <header_value> <path> [match_type]
# path 用于限定本规则只对该 path 生效；同 service 下不同 path 的规则互不干扰。
# match_type 可选，缺省 EXACT；支持 EXACT / REGEX / NOT_EQUALS / IN / NOT_IN / RANGE
# （与 spec/MatchString 6 种类型对齐，由 pkg/algorithm/match/match.go::MatchString 解释）。
rule_with_header_match() {
    local name="$1" policy="$2" hk="$3" hv="$4" path="$5"
    local mt="${6:-EXACT}"
    local api_block
    api_block="$(build_api_path_block "$path")"
    cat <<EOF
{
  "name": "$(json_escape "$name")",
  "namespace": "$(json_escape "$NAMESPACE")",
  "service": "$(json_escape "$CURRENT_SERVICE")",
  "enable": true,
  "block_allow_config": [
    {
      ${api_block}"block_allow_policy": "${policy}",
      "arguments": [
        {
          "type": "HEADER",
          "key": "$(json_escape "$hk")",
          "value": {"type": "${mt}", "value_type": "TEXT", "value": "$(json_escape "$hv")"}
        }
      ]
    }
  ]
}
EOF
}

# rule_with_caller_service_match <name> <policy> <caller_ns> <caller_svc> <path> [match_type]
rule_with_caller_service_match() {
    local name="$1" policy="$2" cns="$3" csvc="$4" path="$5"
    local mt="${6:-EXACT}"
    local api_block
    api_block="$(build_api_path_block "$path")"
    cat <<EOF
{
  "name": "$(json_escape "$name")",
  "namespace": "$(json_escape "$NAMESPACE")",
  "service": "$(json_escape "$CURRENT_SERVICE")",
  "enable": true,
  "block_allow_config": [
    {
      ${api_block}"block_allow_policy": "${policy}",
      "arguments": [
        {
          "type": "CALLER_SERVICE",
          "key": "$(json_escape "$cns")",
          "value": {"type": "${mt}", "value_type": "TEXT", "value": "$(json_escape "$csvc")"}
        }
      ]
    }
  ]
}
EOF
}

# rule_with_callee_metadata_match <name> <policy> <meta_key> <meta_value> <path> [match_type]
# CALLEE_METADATA 维度的规则：匹配的数据源是 polaris-go SDK 本端通过 RegisterInstance
# 登记的实例 metadata，因此只有当 provider 以 --register-metadata <key>=<value>
# 启动时才能命中。
rule_with_callee_metadata_match() {
    local name="$1" policy="$2" mk="$3" mv="$4" path="$5"
    local mt="${6:-EXACT}"
    local api_block
    api_block="$(build_api_path_block "$path")"
    cat <<EOF
{
  "name": "$(json_escape "$name")",
  "namespace": "$(json_escape "$NAMESPACE")",
  "service": "$(json_escape "$CURRENT_SERVICE")",
  "enable": true,
  "block_allow_config": [
    {
      ${api_block}"block_allow_policy": "${policy}",
      "arguments": [
        {
          "type": "CALLEE_METADATA",
          "key": "$(json_escape "$mk")",
          "value": {"type": "${mt}", "value_type": "TEXT", "value": "$(json_escape "$mv")"}
        }
      ]
    }
  ]
}
EOF
}

# rule_with_custom_match <name> <policy> <custom_key> <custom_value> <path> [match_type]
# CUSTOM 维度的规则：数据源是请求中的自定义 Argument（provider 约定通过
# X-Custom-Arg-<Key>: <Value> Header 透传并调用 BuildCustomArgument 上报）。
rule_with_custom_match() {
    local name="$1" policy="$2" ck="$3" cv="$4" path="$5"
    local mt="${6:-EXACT}"
    local api_block
    api_block="$(build_api_path_block "$path")"
    cat <<EOF
{
  "name": "$(json_escape "$name")",
  "namespace": "$(json_escape "$NAMESPACE")",
  "service": "$(json_escape "$CURRENT_SERVICE")",
  "enable": true,
  "block_allow_config": [
    {
      ${api_block}"block_allow_policy": "${policy}",
      "arguments": [
        {
          "type": "CUSTOM",
          "key": "$(json_escape "$ck")",
          "value": {"type": "${mt}", "value_type": "TEXT", "value": "$(json_escape "$cv")"}
        }
      ]
    }
  ]
}
EOF
}

# rule_with_query_match <name> <policy> <query_key> <query_value> <path> [match_type]
# QUERY 维度的规则：数据源是请求 URL 的 query 参数（provider 通过 r.URL.Query() 转 BuildQueryArgument 上报）。
rule_with_query_match() {
    local name="$1" policy="$2" qk="$3" qv="$4" path="$5"
    local mt="${6:-EXACT}"
    local api_block
    api_block="$(build_api_path_block "$path")"
    cat <<EOF
{
  "name": "$(json_escape "$name")",
  "namespace": "$(json_escape "$NAMESPACE")",
  "service": "$(json_escape "$CURRENT_SERVICE")",
  "enable": true,
  "block_allow_config": [
    {
      ${api_block}"block_allow_policy": "${policy}",
      "arguments": [
        {
          "type": "QUERY",
          "key": "$(json_escape "$qk")",
          "value": {"type": "${mt}", "value_type": "TEXT", "value": "$(json_escape "$qv")"}
        }
      ]
    }
  ]
}
EOF
}

# rule_with_caller_ip_match <name> <policy> <ip_value> <path> [match_type]
# CALLER_IP 维度的规则：数据源是 provider 通过 BuildCallerIPArgument 上报的远端 IP（来自 r.RemoteAddr）。
# key 在 SDK 取值时被忽略（findArgumentValue 第二个参数用 ""），这里固定写空串。
rule_with_caller_ip_match() {
    local name="$1" policy="$2" ipv="$3" path="$4"
    local mt="${5:-EXACT}"
    local api_block
    api_block="$(build_api_path_block "$path")"
    cat <<EOF
{
  "name": "$(json_escape "$name")",
  "namespace": "$(json_escape "$NAMESPACE")",
  "service": "$(json_escape "$CURRENT_SERVICE")",
  "enable": true,
  "block_allow_config": [
    {
      ${api_block}"block_allow_policy": "${policy}",
      "arguments": [
        {
          "type": "CALLER_IP",
          "key": "",
          "value": {"type": "${mt}", "value_type": "TEXT", "value": "$(json_escape "$ipv")"}
        }
      ]
    }
  ]
}
EOF
}

# rule_with_caller_metadata_match <name> <policy> <meta_key> <meta_value> <path> [match_type]
# CALLER_METADATA 维度的规则：数据源是 SourceService.Metadata（即主调实例 metadata）。
# 示例 provider 约定主调通过 X-Polaris-Caller-Service + X-Caller-Meta-<Key> Header 透传，
# 没有 X-Polaris-Caller-Service 时 SourceService 为空，CALLER_METADATA 永远取空字符串。
rule_with_caller_metadata_match() {
    local name="$1" policy="$2" mk="$3" mv="$4" path="$5"
    local mt="${6:-EXACT}"
    local api_block
    api_block="$(build_api_path_block "$path")"
    cat <<EOF
{
  "name": "$(json_escape "$name")",
  "namespace": "$(json_escape "$NAMESPACE")",
  "service": "$(json_escape "$CURRENT_SERVICE")",
  "enable": true,
  "block_allow_config": [
    {
      ${api_block}"block_allow_policy": "${policy}",
      "arguments": [
        {
          "type": "CALLER_METADATA",
          "key": "$(json_escape "$mk")",
          "value": {"type": "${mt}", "value_type": "TEXT", "value": "$(json_escape "$mv")"}
        }
      ]
    }
  ]
}
EOF
}

# rule_with_two_args_header_query <name> <policy> <hdr_key> <hdr_value> <query_key> <query_value> <path>
# 单条规则两个 MatchArgument（HEADER + QUERY），全部 EXACT。matchArguments 是 AND 关系：
# 必须两个 MatchArgument 都满足才整体命中；任一不满足整条规则不命中。
rule_with_two_args_header_query() {
    local name="$1" policy="$2" hk="$3" hv="$4" qk="$5" qv="$6" path="$7"
    local api_block
    api_block="$(build_api_path_block "$path")"
    cat <<EOF
{
  "name": "$(json_escape "$name")",
  "namespace": "$(json_escape "$NAMESPACE")",
  "service": "$(json_escape "$CURRENT_SERVICE")",
  "enable": true,
  "block_allow_config": [
    {
      ${api_block}"block_allow_policy": "${policy}",
      "arguments": [
        {
          "type": "HEADER",
          "key": "$(json_escape "$hk")",
          "value": {"type": "EXACT", "value_type": "TEXT", "value": "$(json_escape "$hv")"}
        },
        {
          "type": "QUERY",
          "key": "$(json_escape "$qk")",
          "value": {"type": "EXACT", "value_type": "TEXT", "value": "$(json_escape "$qv")"}
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
    # 可选：第 3 个参数透传 register 时上报的 metadata（格式 k1=v1,k2=v2）。
    # 没提供就走默认（注册不带 metadata），用于用例 3-11 / 14-15 保持"不带 metadata"的默认行为。
    local register_metadata="${3:-}"

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
        --register-metadata "$register_metadata" \
        --debug=$DEBUG_MODE \
        > "$provider_log" 2>&1) &
    PROVIDER_PID=$!
    log_info "Provider 已启动 (PID: $PROVIDER_PID, log: $provider_log, register-metadata='${register_metadata}')"

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

# ======================== Consumer 启停（仅 smoke 用例 1/2） ========================
# wait_for_tcp <host> <port> <max_wait> <desc> [pid]
# TCP 连通性探活：consumer demo 没有专门的 health endpoint，/echo 调用会触发完整 SDK 链路，
# 直接探 /echo 会消耗一次"真请求"，并且 consumer 此时还没拉到规则，请求结果不可控。
# 这里只探 TCP 连通就当 ready；后面 do_consumer_call 才发真请求。
wait_for_tcp() {
    local host="$1"
    local port="$2"
    local max_wait="${3:-30}"
    local desc="${4:-服务}"
    local pid="${5:-}"
    local waited=0
    while [[ $waited -lt $max_wait ]]; do
        if [[ -n "$pid" ]] && ! kill -0 "$pid" 2>/dev/null; then
            log_error "${desc} 进程 (PID: $pid) 已退出"
            return 1
        fi
        if (echo > "/dev/tcp/${host}/${port}") 2>/dev/null; then
            log_info "${desc} TCP 已就绪 (${host}:${port})"
            return 0
        fi
        sleep 1
        waited=$((waited + 1))
    done
    log_error "${desc} TCP 未就绪 (${host}:${port})，等待了 ${max_wait}s"
    return 1
}

# start_consumer：启动 consumer 进程（service=被调名 SERVICE_NAME，selfService=主调名
# CONSUMER_SELF_SERVICE）。consumer 通过 polaris-go SDK 走 GetOneInstance 选址 + 自动添加
# X-Polaris-Caller-* / X-Caller-Meta-* Header，用于验证 SDK 主调侧透传链路。
start_consumer() {
    # Consumer 端口被占用时尝试自动清理（常见原因：上一次脚本被 Ctrl+C 没走 trap，
    # 残留 auth_consumer 进程占住 38080）。先 SIGTERM，留 1s 让其优雅退出；仍不释放则 SIGKILL。
    if lsof -nP -iTCP:"${CONSUMER_PORT}" -sTCP:LISTEN 2>/dev/null | grep -q LISTEN; then
        log_warn "Consumer 端口 ${CONSUMER_PORT} 已被占用，尝试自动清理占用进程..."
        lsof -nP -iTCP:"${CONSUMER_PORT}" -sTCP:LISTEN 2>/dev/null || true
        local occupant_pids
        occupant_pids=$(lsof -nP -iTCP:"${CONSUMER_PORT}" -sTCP:LISTEN -t 2>/dev/null || true)
        if [[ -n "$occupant_pids" ]]; then
            for pid in $occupant_pids; do
                kill "$pid" 2>/dev/null && log_info "  已发送 SIGTERM 给 PID=$pid" || true
            done
            sleep 1
            for pid in $occupant_pids; do
                if kill -0 "$pid" 2>/dev/null; then
                    kill -9 "$pid" 2>/dev/null && log_warn "  PID=$pid 未响应 SIGTERM，已 SIGKILL" || true
                fi
            done
            sleep 1
        fi
        if lsof -nP -iTCP:"${CONSUMER_PORT}" -sTCP:LISTEN 2>/dev/null | grep -q LISTEN; then
            log_error "清理后端口 ${CONSUMER_PORT} 仍被占用，请手工排查（或用 CONSUMER_PORT 环境变量换一个）"
            lsof -nP -iTCP:"${CONSUMER_PORT}" -sTCP:LISTEN 2>/dev/null || true
            exit 1
        fi
        log_info "端口 ${CONSUMER_PORT} 已释放，继续启动 Consumer"
    fi

    local consumer_workdir="${BUILD_DIR}/consumer_run"
    mkdir -p "$consumer_workdir"
    rm -rf "${consumer_workdir}/polaris" 2>/dev/null || true

    cat > "${consumer_workdir}/polaris.yaml" <<EOF
global:
  serverConnector:
    addresses:
      - ${POLARIS_SERVER}:8091
    token: ${POLARIS_TOKEN}
    connectTimeout: 3s
EOF

    local consumer_log="${LOG_DIR}/consumer.log"
    : > "$consumer_log"

    (cd "$consumer_workdir" && "${BUILD_DIR}/auth_consumer" \
        --namespace "$NAMESPACE" \
        --service "$SERVICE_NAME" \
        --selfNamespace "$NAMESPACE" \
        --selfService "$CONSUMER_SELF_SERVICE" \
        --port "$CONSUMER_PORT" \
        --debug=$DEBUG_MODE \
        > "$consumer_log" 2>&1) &
    CONSUMER_PID=$!
    log_info "Consumer 已启动 (PID: $CONSUMER_PID, port: $CONSUMER_PORT, log: $consumer_log)"
    log_info "  consumer 主调身份 = ${NAMESPACE}/${CONSUMER_SELF_SERVICE}（X-Polaris-Caller-Service Header）"
    log_info "  consumer 调用目标 = ${NAMESPACE}/${SERVICE_NAME}（GetOneInstance 选址）"

    sleep 1
    check_process_alive "$CONSUMER_PID" "Consumer" || {
        log_error "Consumer 启动失败，最近日志："
        tail -30 "$consumer_log" 2>/dev/null || true
        exit 1
    }

    wait_for_tcp 127.0.0.1 "$CONSUMER_PORT" 20 "Consumer" "$CONSUMER_PID" || exit 1
    sleep 1
}

# stop_consumer：用例 1/2 跑完立即关 consumer，释放 38080 端口；后续用例 3-28 不再用 consumer。
stop_consumer() {
    if [[ -n "$CONSUMER_PID" ]] && kill -0 "$CONSUMER_PID" 2>/dev/null; then
        log_info "停止 Consumer (PID: $CONSUMER_PID)"
        kill "$CONSUMER_PID" 2>/dev/null || true
        wait "$CONSUMER_PID" 2>/dev/null || true
        CONSUMER_PID=""
    fi
}

# build_smoke_rule_caller_service <caller_svc>: 构造 smoke 用例规则的 JSON
# CALLER_SERVICE EXACT 匹配 + path=/echo + ALLOW_LIST。caller_service 的 key=NAMESPACE。
build_smoke_rule_caller_service() {
    local caller_svc="$1"
    rule_with_caller_service_match \
        "$RULE_NAME_SMOKE_ALLOW_CALLER_CLIENT" \
        "ALLOW_LIST" \
        "$NAMESPACE" \
        "$caller_svc" \
        "$PATH_CASE_1_2"
}

# ensure_smoke_rule_with_caller_service <caller_svc>：确保 smoke 规则存在且 caller_service=指定值
# 不存在则 POST 创建；存在但内容可能是上一次 smoke 跑剩的不同 caller_svc，所以无脑 PUT 更新一次保证一致。
ensure_smoke_rule_with_caller_service() {
    local caller_svc="$1"
    local rule_json
    rule_json="$(build_smoke_rule_caller_service "$caller_svc")"
    if ! rule_exists_by_name "$RULE_NAME_SMOKE_ALLOW_CALLER_CLIENT"; then
        log_info "smoke 规则 ${RULE_NAME_SMOKE_ALLOW_CALLER_CLIENT} 不存在，POST 创建（caller_service=${caller_svc}）"
        create_rule "$rule_json"
        return $?
    fi
    log_info "smoke 规则 ${RULE_NAME_SMOKE_ALLOW_CALLER_CLIENT} 已存在，PUT 更新为 caller_service=${caller_svc}"
    update_rule "$rule_json"
}

# do_consumer_call <expect_http_code> <case_label>
# 通过 consumer 的 /echo 转发到 provider 鉴权链路；返回 consumer 自己的 HTTP 状态码。
# consumer demo 已修复成「透传 provider 状态码」，所以：
# - provider 鉴权放行 → consumer 200 + 透传 provider 响应体（"Hello, ..."）
# - provider 鉴权拒绝（403）→ consumer 透传 403 + provider 响应体（"forbidden: ..."）+ X-Auth-Result Header
do_consumer_call() {
    local expect_code="$1"
    local case_label="$2"
    local url="http://127.0.0.1:${CONSUMER_PORT}/echo"
    local tmp_resp
    tmp_resp=$(mktemp)
    local http_code
    http_code=$(curl -s -o "$tmp_resp" -w '%{http_code}' --connect-timeout 5 --max-time 15 "$url" || echo "000")
    local body
    body=$(head -c 200 "$tmp_resp" 2>/dev/null || echo "")
    if [[ "$http_code" == "$expect_code" ]]; then
        log_case_pass "${case_label}: HTTP=${http_code}  body=${body:0:120}"
        rm -f "$tmp_resp"
        return 0
    else
        log_case_fail "${case_label}: 期望 HTTP=${expect_code}, 实际=${http_code}  body=${body:0:200}"
        rm -f "$tmp_resp"
        return 1
    fi
}

# verify_provider_caller_log <expected_caller_service>：检查 provider 日志包含期望的主调身份
# 用于确认 polaris-go SDK 透传 X-Polaris-Caller-Service Header 成功。
verify_provider_caller_log() {
    local expected="$1"
    local provider_log="${LOG_DIR}/provider.log"
    if grep -q "caller service:.*service=\"${expected}\"" "$provider_log"; then
        log_case_pass "[smoke 透传校验] provider 日志包含 caller service=\"${expected}\"（SDK 主调侧透传成功）"
        return 0
    fi
    log_case_fail "[smoke 透传校验] provider 日志未发现 caller service=\"${expected}\""
    log_raw "    最近 5 行 [CALLER] 日志："
    grep "\[CALLER\]" "$provider_log" 2>/dev/null | tail -5 | while read -r line; do log_raw "      $line"; done
    return 1
}

# ======================== HTTP 请求工具 ========================
# do_request <expect_code> <case_label> <path> [curl 额外参数...]
# path 与规则中的 api.path 一致，用于在同 service 下让规则按 path 隔离。
do_request() {
    local expect_code="$1"
    local case_label="$2"
    local path="$3"
    shift 3

    local url="http://127.0.0.1:${PROVIDER_ACTUAL_PORT}${path}"
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

# 服务复用策略：
#   - SERVICE_NAME_NORULE：用例 3-5 专用，不创建任何规则。每次脚本都用同一个名字（不带 RUN_ID），
#     依赖"被调下没有任何规则"这一前提，让"鉴权未启用 / chain 为空 / 无规则"三种放行场景成立。
#     如服务端残留有人手动加的 BlockAllowListRule，需先在控制台清掉再跑。
#   - SERVICE_NAME：用例 6-28 公用一个服务名，17 条规则在它下面常驻。
#     规则通过各自的 EXACT api.path 限定（PATH_CASE_*）实现互不干扰：
#     * matchMethod 会按 protocol+method+path 过滤掉不属于本用例的规则；
#     * 只有本用例对应的那条规则进入 matchArguments；
#     * 不需要删除/更新规则，跨运行天然幂等（ensure_rule 已存在则复用）。
SERVICE_NAME_NORULE="${SERVICE_NAME}-norule"

# 用例 6-15 的 7 条业务规则固定名（不带 RUN_ID），第一次 ensure_rule 创建，之后跨运行复用。
RULE_NAME_ALLOW_VIP="auth-it-allow-vip"
RULE_NAME_BLOCK_BAD="auth-it-block-bad"
RULE_NAME_ALLOW_TRUSTED="auth-it-allow-trusted"
RULE_NAME_ALLOW_CALLEE_ENV_PROD="auth-it-allow-callee-env-prod"
RULE_NAME_ALLOW_CALLEE_ENV_DEV="auth-it-allow-callee-env-dev"
RULE_NAME_ALLOW_CUSTOM_BIZ_V1="auth-it-allow-custom-biz-v1"
RULE_NAME_ALLOW_CUSTOM_BIZ_V2="auth-it-allow-custom-biz-v2"
# 用例 16-20：5 种 MatchString 类型的端到端覆盖（HEADER 维度，BLOCK_LIST + 命中场景）。
# 选 BLOCK_LIST + 命中：service 下已有 6 条业务 ALLOW_LIST 规则，新增 ALLOW 规则会被混合策略掩盖；
# BLOCK 命中能直接 return false，结果干净，跟 service 全局白名单状态无关。
RULE_NAME_BLOCK_REGEX="auth-it-block-regex"
RULE_NAME_BLOCK_NOT_EQUALS="auth-it-block-not-equals"
RULE_NAME_BLOCK_IN="auth-it-block-in"
RULE_NAME_BLOCK_NOT_IN="auth-it-block-not-in"
RULE_NAME_BLOCK_RANGE="auth-it-block-range"
# 用例 21-28：维度补齐（QUERY / CALLER_IP / CALLER_METADATA）+ 多 MatchArgument AND 关系
RULE_NAME_ALLOW_QUERY_CN="auth-it-allow-query-cn"
RULE_NAME_BLOCK_CALLER_IP_LOOPBACK="auth-it-block-caller-ip-loopback"
RULE_NAME_ALLOW_CALLER_IP_LOOPBACK="auth-it-allow-caller-ip-loopback"
RULE_NAME_ALLOW_CALLER_META_GOLD="auth-it-allow-caller-meta-gold"
RULE_NAME_ALLOW_AND_HEADER_QUERY="auth-it-allow-and-header-query"

# 用例 1/2：consumer ↔ provider 端到端 smoke（验证 polaris-go SDK 主调侧 X-Polaris-Caller-*
# Header 透传链路），用一条规则在 caller_service 命中/未命中两种场景下分别得到 200 / 403。
# 注意：consumer demo 在 v? 之后已修复成「透传 provider 状态码 + X-Auth-Result/X-Auth-Info Header」，
# 所以 provider 鉴权拒绝时（403）curl 直接拿到 403，而不是被包装成 500。
# path 用 "/echo"（consumer 内部硬编码），与用例 6-28 的所有 path（/echo-XXX）按 EXACT 互不干扰。
RULE_NAME_SMOKE_ALLOW_CALLER_CLIENT="auth-it-smoke-allow-caller-client"
PATH_CASE_1_2="/echo"

# 各用例对应的 path 与规则严格 1:1 绑定；同 service 下靠 path 互不干扰。
# 用例 6/7 共用同一条规则与 path（同一规则两种入参验证命中/不命中），8/9、10/11 同理。
# 12/13、14/15 用不同 path 隔离不同规则，避免任一白名单命中导致"不命中"用例失败。
# 注意：以下 PATH_CASE_X_Y 变量名中的数字是历史编号（用例编号未右移之前），
# 现已用例编号整体 +2，但 path 字符串本身保持不变（path 与变量名是字符串绑定，与"用例编号"
# 是间接对应——比如 PATH_CASE_4_5 现在对应用例 6/7，依此类推）。变量名不重命名是为了
# 避免大量改动；实际语义看 PATH 字符串本身（"/echo-allow-vip" 等）。
PATH_CASE_4_5="/echo-allow-vip"
PATH_CASE_6_7="/echo-block-bad"
PATH_CASE_8_9="/echo-allow-trusted"
PATH_CASE_10="/echo-callee-prod"
PATH_CASE_11="/echo-callee-dev"
PATH_CASE_12="/echo-custom-v1"
PATH_CASE_13="/echo-custom-v2"
# 用例 16-20：每种 MatchString 类型独立 path
PATH_CASE_14="/echo-mt-regex"
PATH_CASE_15="/echo-mt-not-equals"
PATH_CASE_16="/echo-mt-in"
PATH_CASE_17="/echo-mt-not-in"
PATH_CASE_18="/echo-mt-range"
# 用例 21-28：每个用例（或一对正反用例）独立 path
PATH_CASE_19_20="/echo-query-cn"
PATH_CASE_21="/echo-caller-ip-block"
PATH_CASE_22="/echo-caller-ip-allow"
PATH_CASE_23_24="/echo-caller-meta-gold"
PATH_CASE_25_26="/echo-and-both"

# log_service_rules_overview 在用例 6 第一次进入 SERVICE_NAME 之前打印一次
# 该 service 下 18 条常驻规则的概览（含用例 1/2 创建的 1 条 smoke 规则），便于阅读后续用例
# 描述时建立全局视图。
# 1 条 smoke 规则（1/2）+ 7 条业务规则（6-15）+ 5 条 MatchString 类型规则（16-20）
#   + 5 条维度补齐与 AND 规则（21-28）= 合计 11 条 ALLOW_LIST + 7 条 BLOCK_LIST = 18 条
# 所有规则都带 EXACT api.path 限定，因此同一 service 下不同 path 的请求经 matchMethod 过滤
# 只剩 1 条进入 matchArguments；但 containsAllowList 在 matchMethod 之前置位（与 polaris-java
# 一致），意味着只要本 service 下存在任意一条 ALLOW_LIST 规则，未命中任何规则的请求就会落入
# return !containsAllowList = false → 403。
log_service_rules_overview() {
    log_info "─── ${SERVICE_NAME} 下常驻规则全景（18 条，每条带 EXACT api.path 限定）───"
    log_raw "  -- 用例 1-2：consumer 主调身份 smoke 规则（用例 2 跑完 caller_service 是 NotConsumer 状态）"
    log_raw "   *) ${RULE_NAME_SMOKE_ALLOW_CALLER_CLIENT} ALLOW_LIST  path=${PATH_CASE_1_2}              CALLER_SERVICE ${NAMESPACE}/<NotConsumer> EXACT"
    log_raw "  -- 用例 6-15：7 条业务规则（HEADER/CALLER_SERVICE/CALLEE_METADATA/CUSTOM 维度，全部 value EXACT 匹配）"
    log_raw "   1) ${RULE_NAME_ALLOW_VIP}                ALLOW_LIST  path=${PATH_CASE_4_5}     HEADER       user EXACT vip"
    log_raw "   2) ${RULE_NAME_BLOCK_BAD}                BLOCK_LIST  path=${PATH_CASE_6_7}     HEADER       user EXACT bad"
    log_raw "   3) ${RULE_NAME_ALLOW_TRUSTED}            ALLOW_LIST  path=${PATH_CASE_8_9}    CALLER_SERVICE default/trusted EXACT"
    log_raw "   4) ${RULE_NAME_ALLOW_CALLEE_ENV_PROD}    ALLOW_LIST  path=${PATH_CASE_10}      CALLEE_METADATA env EXACT prod"
    log_raw "   5) ${RULE_NAME_ALLOW_CALLEE_ENV_DEV}     ALLOW_LIST  path=${PATH_CASE_11}      CALLEE_METADATA env EXACT dev"
    log_raw "   6) ${RULE_NAME_ALLOW_CUSTOM_BIZ_V1}      ALLOW_LIST  path=${PATH_CASE_12}      CUSTOM       biz EXACT v1"
    log_raw "   7) ${RULE_NAME_ALLOW_CUSTOM_BIZ_V2}      ALLOW_LIST  path=${PATH_CASE_13}      CUSTOM       biz EXACT v2"
    log_raw "  -- 用例 16-20：5 种 MatchString 类型覆盖（HEADER 维度，全部 BLOCK_LIST + 命中 → 403）"
    log_raw "   8) ${RULE_NAME_BLOCK_REGEX}              BLOCK_LIST  path=${PATH_CASE_14}        HEADER user REGEX        ^bad-.*\$"
    log_raw "   9) ${RULE_NAME_BLOCK_NOT_EQUALS}         BLOCK_LIST  path=${PATH_CASE_15}   HEADER user NOT_EQUALS   admin（admin 通过，其它都拒）"
    log_raw "  10) ${RULE_NAME_BLOCK_IN}                 BLOCK_LIST  path=${PATH_CASE_16}           HEADER user IN           bad1,bad2,bad3"
    log_raw "  11) ${RULE_NAME_BLOCK_NOT_IN}             BLOCK_LIST  path=${PATH_CASE_17}       HEADER user NOT_IN       admin,operator（不在白名单的都拒）"
    log_raw "  12) ${RULE_NAME_BLOCK_RANGE}              BLOCK_LIST  path=${PATH_CASE_18}        HEADER user RANGE        100~200（仅整数）"
    log_raw "  -- 用例 21-26：维度补齐（QUERY / CALLER_IP / CALLER_METADATA），让 7 种 MatchArgument 全覆盖"
    log_raw "  13) ${RULE_NAME_ALLOW_QUERY_CN}           ALLOW_LIST  path=${PATH_CASE_19_20}        QUERY        region EXACT cn"
    log_raw "  14) ${RULE_NAME_BLOCK_CALLER_IP_LOOPBACK} BLOCK_LIST  path=${PATH_CASE_21}  CALLER_IP    REGEX ^(127\\.0\\.0\\.1|::1)\$"
    log_raw "  15) ${RULE_NAME_ALLOW_CALLER_IP_LOOPBACK} ALLOW_LIST  path=${PATH_CASE_22}  CALLER_IP    REGEX ^(127\\.0\\.0\\.1|::1)\$"
    log_raw "  16) ${RULE_NAME_ALLOW_CALLER_META_GOLD}   ALLOW_LIST  path=${PATH_CASE_23_24}  CALLER_METADATA tier EXACT gold"
    log_raw "  -- 用例 27-28：单规则多 MatchArgument AND 关系（任一不满足整体不命中）"
    log_raw "  17) ${RULE_NAME_ALLOW_AND_HEADER_QUERY}   ALLOW_LIST  path=${PATH_CASE_25_26}        [HEADER user EXACT vip] AND [QUERY region EXACT cn]"
    log_raw "  ★ ALLOW_LIST 11 条 + BLOCK_LIST 7 条 → 任意请求 containsAllowList 均被置 true。"
    log_raw "  ★ 每个用例请求 path 只命中 1 条规则；matchMethod 过滤后只有该规则进入 matchArguments。"
    log_raw "  ★ 未命中任一规则时按混合策略落入 return !containsAllowList = false → 403。"
}

# 后续 6-20 的标准 chain 配置
ENABLED_CHAIN_BLOCK="    chain:
      - blockAllowList
    plugin:
      blockAllowList: {}"

# CURRENT_PROVIDER_METADATA 记录当前 provider 进程实际启动时使用的 --register-metadata，
# 用于让 prepare_case_with_rule 判断「本用例期望的 metadata」与「provider 当前 metadata」
# 是否一致：一致则不重启（仅 wait_rule_cache 让 SDK 拉新规则），不一致才重启。
# 默认空串 = 进程未启动 / 启动时不带 metadata。
CURRENT_PROVIDER_METADATA=""

# CURRENT_PROVIDER_CHAIN_BLOCK 记录当前 provider 进程的 polaris.yaml chain_block 段。
# 用例 3/4/5 会把它切到 "false-no-chain" / "$ENABLED_CHAIN_BLOCK" / "    chain: []"，
# 进入用例 6 之前 prepare_case_with_rule 必须确保它回到 "$ENABLED_CHAIN_BLOCK"（含
# blockAllowList 鉴权链），否则后续用例 6-28 会跑在错误的鉴权配置下（chain=[] 时鉴权链
# 完全未加载，所有请求都被放行 → 期望 403 的用例会失败）。
CURRENT_PROVIDER_CHAIN_BLOCK=""

# CURRENT_PROVIDER_ENABLE 记录 provider.auth.enable 当前值（"true" / "false"）
CURRENT_PROVIDER_ENABLE=""

# ensure_enabled_provider 切回标准的 enable=true + chain=[blockAllowList] 配置（不带 metadata）
# 用例 3（enable=false）/ 2（chain=[blockAllowList]）/ 3（chain=[]）需要切 polaris.yaml；
# 这 3 个属于「SDK 启动期配置」，polaris-go 不支持热加载，必须重启 provider。
ensure_enabled_provider() {
    local register_metadata="${1:-}"
    restart_provider_with_config "true" "$ENABLED_CHAIN_BLOCK" "$register_metadata"
    CURRENT_PROVIDER_ENABLE="true"
    CURRENT_PROVIDER_CHAIN_BLOCK="$ENABLED_CHAIN_BLOCK"
    CURRENT_PROVIDER_METADATA="$register_metadata"
}

# prepare_case_with_rule <rule_name> <rule_json> [register_metadata]：
# 每个用例独立运行的标准前置流程：
#   1) ensure_rule：同名规则存在则复用，不存在则创建（跨运行幂等）；
#   2) 当下列任一项与当前 provider 进程不一致时，重启 provider：
#      - polaris.yaml 的 enable 段（用例 3 留下 enable=false）；
#      - polaris.yaml 的 chain 段（用例 4 留下标准 chain；用例 5 留下 chain=[]）；
#      - --register-metadata（用例 12/13 用 env=prod；用例 14 之后回到空）。
#      其中 enable/chain/metadata 全是 SDK 启动期配置，polaris-go 不支持热加载。
#      用例 6-28 全部期望「enable=true + chain=[blockAllowList]」，所以从用例 5
#      （chain=[]）进入用例 6 时必触发一次重启，但只重启一次，后续 6-11 复用同一进程。
#   3) 等待 SDK 拉到最新规则（wait_rule_cache）。
# 返回 0=就绪可发请求；返回 1=创建规则失败（用例应 SKIP）。
prepare_case_with_rule() {
    local rule_name="$1"
    local rule_json="$2"
    local register_metadata="${3:-}"
    if ! ensure_rule "$rule_name" "$rule_json"; then
        return 1
    fi
    local need_restart=false
    local reason=""
    if [[ "$CURRENT_PROVIDER_ENABLE" != "true" ]]; then
        need_restart=true
        reason="enable 当前=${CURRENT_PROVIDER_ENABLE}，需要切到 true"
    elif [[ "$CURRENT_PROVIDER_CHAIN_BLOCK" != "$ENABLED_CHAIN_BLOCK" ]]; then
        need_restart=true
        reason="chain 不是标准 [blockAllowList]，需要切回"
    elif [[ "$register_metadata" != "$CURRENT_PROVIDER_METADATA" ]]; then
        need_restart=true
        reason="register_metadata 从 '${CURRENT_PROVIDER_METADATA}' → '${register_metadata}'"
    fi
    if [[ "$need_restart" == "true" ]]; then
        log_info "重启 provider（原因：${reason}）"
        restart_provider_with_config "true" "$ENABLED_CHAIN_BLOCK" "$register_metadata"
        CURRENT_PROVIDER_ENABLE="true"
        CURRENT_PROVIDER_CHAIN_BLOCK="$ENABLED_CHAIN_BLOCK"
        CURRENT_PROVIDER_METADATA="$register_metadata"
    else
        log_debug "provider 状态未变化（enable=true, chain=blockAllowList, metadata='${register_metadata}'），复用现有进程"
    fi
    wait_rule_cache
    return 0
}

# ============================================================================
# 用例 1-2：consumer ↔ provider 端到端 smoke
# ----------------------------------------------------------------------------
# verify_auth.sh 用例 6-28 全部用 curl 直接构造 Header 模拟主调，**不走 polaris-go SDK
# 主调侧的代码路径**。用例 1/2 用真实的 polaris-go consumer demo（examples/auth/consumer/）
# 验证「consumer 通过 SDK 发出的请求 → provider 鉴权链能正确识别主调身份」这条端到端
# SDK 透传链路。
#
# 链路：
#   curl ──► consumer:${CONSUMER_PORT}/echo
#                 │
#                 ├─ ConsumerAPI.GetOneInstance(${SERVICE_NAME})  // 选址
#                 ├─ 自动加 Header X-Polaris-Caller-Service: ${CONSUMER_SELF_SERVICE}
#                 ├─ 自动加 Header X-Caller-Meta-env=dev / version=1.0.0（consumer demo 硬编码）
#                 ▼
#         provider:${PROVIDER_PORT}/echo
#                 ├─ AuthAPI.Authenticate(req, srcService=AuthEchoClient)
#                 └─ 命中 ALLOW_LIST(CALLER_SERVICE=${CONSUMER_SELF_SERVICE}) → 200 / 不命中 → 403
#
# 用例 1：规则 caller_service=${CONSUMER_SELF_SERVICE}（命中）→ provider 200 → consumer 透传 200
# 用例 2：规则 caller_service=Other（不命中）→ provider 403 → consumer 透传 403 给 curl
#
# 跑完用例 1/2 立即 stop_consumer 释放 38080 端口，后续用例 3-28 不需要 consumer。
# ============================================================================

# [用例 1] CALLER_SERVICE 命中 → consumer 拿到 200
test_case_1_consumer_smoke_hit() {
    log_step "[用例 1] consumer 主调身份 ${CONSUMER_SELF_SERVICE} 命中 ALLOW_LIST → consumer 200"
    set_current_service "$SERVICE_NAME"
    if ! ensure_smoke_rule_with_caller_service "${CONSUMER_SELF_SERVICE}"; then
        log_case_skip "[用例 1] 创建/更新 smoke 规则失败，跳过 consumer smoke 用例 1/2"
        log_case_skip "[用例 2] 因用例 1 跳过而连带跳过"
        return 1
    fi
    # ensure_enabled_provider 把 provider 切到「enable=true + chain=[blockAllowList] + 无 metadata」
    # 标准状态。用例 1 是脚本进入后第 1 次启 provider；后续用例 3/4/5 还会切 enable/chain，但
    # CURRENT_PROVIDER_* 状态变量会跟踪，最终用例 6 进入时再切回标准状态。
    ensure_enabled_provider ""
    start_consumer
    wait_rule_cache
    log_info "  规则：${RULE_NAME_SMOKE_ALLOW_CALLER_CLIENT} ALLOW_LIST path=${PATH_CASE_1_2}"
    log_info "        CALLER_SERVICE key=${NAMESPACE} value=${CONSUMER_SELF_SERVICE} EXACT"
    log_info "  期望：consumer 透传 X-Polaris-Caller-Service: ${CONSUMER_SELF_SERVICE} → 命中 ALLOW → consumer 200"
    do_consumer_call 200 "[用例 1] consumer 命中 ALLOW_LIST → consumer 200"
    verify_provider_caller_log "${CONSUMER_SELF_SERVICE}"
}

# [用例 2] CALLER_SERVICE 不命中 → consumer 透传 provider 403 给 curl
# consumer demo 已修复成「透传 provider 状态码」，所以 provider 鉴权拒绝时 curl 直接拿 403。
test_case_2_consumer_smoke_miss() {
    log_step "[用例 2] 把规则改成 CALLER_SERVICE=NotConsumer → consumer 主调不命中 → consumer 403"
    set_current_service "$SERVICE_NAME"
    if ! ensure_smoke_rule_with_caller_service "NotConsumer"; then
        log_case_skip "[用例 2] PUT 更新 smoke 规则失败（可能 token 权限不足），跳过"
        stop_consumer
        return 1
    fi
    wait_rule_cache
    log_info "  规则更新：${RULE_NAME_SMOKE_ALLOW_CALLER_CLIENT} 的 caller_service 改成 NotConsumer"
    log_info "  期望：consumer 透传 X-Polaris-Caller-Service: ${CONSUMER_SELF_SERVICE}（≠ NotConsumer）"
    log_info "        → ALLOW_LIST 未命中 → containsAllowList=true → provider 403 → consumer 透传 403 给 curl"
    do_consumer_call 403 "[用例 2] consumer 不命中 ALLOW_LIST → consumer 403"
    # 用例 1/2 跑完立即释放 consumer 进程与 38080 端口；后续用例 3-28 不再需要 consumer
    stop_consumer
}

# [用例 3] enable=false → 任何请求放行
# 用例 3-5 复用 SERVICE_NAME_NORULE：该 service 下不创建任何规则，
# 测试"鉴权未启用 / chain 为空 / 无规则"三种放行场景。
test_case_3_disabled() {
    log_step "[用例 3] enable=false → SyncAuthenticate 直接返回 Ok（零开销路径）"
    log_info "  service=${SERVICE_NAME_NORULE} 下规则集：<空>（用例 3-5 专用 service 不建任何规则）"
    log_info "  SDK 配置：provider.auth.enable=false → authenticators 不加载 → 鉴权链未生效"
    set_current_service "$SERVICE_NAME_NORULE"
    restart_provider_with_config "false" ""
    CURRENT_PROVIDER_ENABLE="false"
    CURRENT_PROVIDER_CHAIN_BLOCK=""
    CURRENT_PROVIDER_METADATA=""
    do_request 200 "[用例 3] enable=false → 200" "/echo"
}

# [用例 4] enable=true + 无规则 → 放行
test_case_4_enabled_no_rule() {
    log_step "[用例 4] enable=true + chain=[blockAllowList] 但 service 下无规则 → 200"
    log_info "  service=${SERVICE_NAME_NORULE} 下规则集：<空>"
    log_info "  SDK 配置：enable=true，chain=[blockAllowList]，规则集为空 → getBlockAllowListRules 返回 nil"
    log_info "  Authenticate 直接返回 Ok（与 polaris-java 一致）"
    set_current_service "$SERVICE_NAME_NORULE"
    restart_provider_with_config "true" "$ENABLED_CHAIN_BLOCK"
    CURRENT_PROVIDER_ENABLE="true"
    CURRENT_PROVIDER_CHAIN_BLOCK="$ENABLED_CHAIN_BLOCK"
    CURRENT_PROVIDER_METADATA=""
    wait_rule_cache
    do_request 200 "[用例 4] enable=true 但 service 下无规则 → 200" "/echo"
}

# [用例 5] chain=[] → 等同未加载
test_case_5_enabled_empty_chain() {
    log_step "[用例 5] enable=true 但 chain=[] → 等同未加载，200"
    log_info "  service=${SERVICE_NAME_NORULE} 下规则集：<空>"
    log_info "  SDK 配置：enable=true，但 chain=[] → loadAuthenticators 因链为空提前 return"
    log_info "  authenticators 切片为空 → SyncAuthenticate 直接返回 Ok"
    set_current_service "$SERVICE_NAME_NORULE"
    restart_provider_with_config "true" "    chain: []"
    CURRENT_PROVIDER_ENABLE="true"
    CURRENT_PROVIDER_CHAIN_BLOCK="    chain: []"
    CURRENT_PROVIDER_METADATA=""
    do_request 200 "[用例 5] enable=true + chain=[] → 200" "/echo"
}

# 用例 6-28 全部使用 SERVICE_NAME 这一个固定服务名，下面常驻 17 条规则（首次跑由 ensure_rule
# 创建，之后跨运行复用）。每条规则带 EXACT api.path 限定，请求经 matchMethod 过滤后只剩一条
# 进入 matchArguments；containsAllowList 在 matchMethod 之前置位（与 polaris-java 一致），
# 因此 17 条里 10 条 ALLOW_LIST 会让所有请求的 containsAllowList=true → 未命中任何规则即拒绝。

# log_case_context 在用例描述中打印"本 service 当前规则集 → matchMethod 后剩什么 → 期望裁决"
# 三段视角，避免读者把"仅一条规则"的简化描述误以为是真实规则集。
# 参数：$1=请求 path，$2=本 path 命中的那条规则的描述，$3=本用例 matchArguments 是否命中（hit/miss），
# $4=期望裁决文字描述（如 "ALLOW_LIST 命中 → return true → 200"）
log_case_context() {
    local req_path="$1" matched_rule="$2" arg_state="$3" verdict="$4"
    log_info "  service=${SERVICE_NAME} 下当前规则集：见前述 17 条全景；包含 10 ALLOW_LIST + 7 BLOCK_LIST"
    log_info "  请求 path=${req_path} → matchMethod 仅命中规则：${matched_rule}"
    log_info "  matchArguments 结果：${arg_state}；containsAllowList=true（service 下存在 ALLOW_LIST 规则）"
    log_info "  期望裁决：${verdict}"
}

# [用例 6] path=/echo-allow-vip + Header user=vip → 命中 ALLOW_LIST → 200
test_case_6_allow_header_match() {
    log_step "[用例 6] path 命中 ALLOW_LIST(HEADER user=vip) 且 args 命中 → 200"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_ALLOW_VIP" \
        "$(rule_with_header_match "$RULE_NAME_ALLOW_VIP" "ALLOW_LIST" "user" "vip" "$PATH_CASE_4_5")"; then
        log_case_context "$PATH_CASE_4_5" \
            "${RULE_NAME_ALLOW_VIP}（ALLOW_LIST, HEADER user=vip）" \
            "命中（请求 Header user=vip 与规则 EXACT 匹配）" \
            "ALLOW_LIST 规则命中 → return true → 200"
        do_request 200 "[用例 6] ALLOW_LIST 命中 → 200" "$PATH_CASE_4_5" -H "user: vip"
    else
        log_case_skip "[用例 6] 创建规则失败，跳过"
    fi
}

# [用例 7] path=/echo-allow-vip + 不带 user → ALLOW_LIST 不命中 → 混合策略拒绝 → 403
test_case_7_allow_header_not_match() {
    log_step "[用例 7] path 命中 ALLOW_LIST(HEADER user=vip) 但 args 不命中 → 403"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_ALLOW_VIP" \
        "$(rule_with_header_match "$RULE_NAME_ALLOW_VIP" "ALLOW_LIST" "user" "vip" "$PATH_CASE_4_5")"; then
        log_case_context "$PATH_CASE_4_5" \
            "${RULE_NAME_ALLOW_VIP}（ALLOW_LIST, HEADER user=vip）" \
            "未命中（请求未携带 user header）" \
            "白名单规则未命中 + containsAllowList=true → return false → 403"
        do_request 403 "[用例 7] ALLOW_LIST 未命中 → 403" "$PATH_CASE_4_5"
    else
        log_case_skip "[用例 7] 创建规则失败，跳过"
    fi
}

# [用例 8] path=/echo-block-bad + user=bad → BLOCK_LIST 命中 → 403
test_case_8_block_header_match() {
    log_step "[用例 8] path 命中 BLOCK_LIST(HEADER user=bad) 且 args 命中 → 403"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_BLOCK_BAD" \
        "$(rule_with_header_match "$RULE_NAME_BLOCK_BAD" "BLOCK_LIST" "user" "bad" "$PATH_CASE_6_7")"; then
        log_case_context "$PATH_CASE_6_7" \
            "${RULE_NAME_BLOCK_BAD}（BLOCK_LIST, HEADER user=bad）" \
            "命中（请求 Header user=bad 与规则 EXACT 匹配）" \
            "BLOCK_LIST 规则命中 → return false → 403"
        do_request 403 "[用例 8] BLOCK_LIST 命中 → 403" "$PATH_CASE_6_7" -H "user: bad"
    else
        log_case_skip "[用例 8] 创建规则失败，跳过"
    fi
}

# [用例 9] path=/echo-block-bad + user=normal → BLOCK_LIST 不命中 + 同 service 存在白名单 → 403
# 这是混合场景的典型表现：本 path 规则虽然是 BLOCK_LIST，但 containsAllowList 由 service 全局
# 视角统计，service 下另有 6 条 ALLOW_LIST 规则 → containsAllowList=true → return false → 403。
# 注意：polaris-java 与 polaris-go 行为一致；"纯黑名单 + 不命中 → 200" 的纯净语义需要用 service
# 下只有 BLOCK 规则的场景才能验证，由单测 TestCheckAllow_OnlyBlockList_NotMatch_ShouldPass 覆盖。
test_case_9_block_header_not_match() {
    log_step "[用例 9] path 命中 BLOCK_LIST(HEADER user=bad) 但 args 不命中 + 混合策略 → 403"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_BLOCK_BAD" \
        "$(rule_with_header_match "$RULE_NAME_BLOCK_BAD" "BLOCK_LIST" "user" "bad" "$PATH_CASE_6_7")"; then
        log_case_context "$PATH_CASE_6_7" \
            "${RULE_NAME_BLOCK_BAD}（BLOCK_LIST, HEADER user=bad）" \
            "未命中（请求 user=normal ≠ 规则 user=bad）" \
            "黑名单未命中，但 service 全局存在 ALLOW_LIST → containsAllowList=true → return false → 403"
        do_request 403 "[用例 9] BLOCK_LIST 未命中 + 混合策略 → 403" "$PATH_CASE_6_7" -H "user: normal"
    else
        log_case_skip "[用例 9] 创建规则失败，跳过"
    fi
}

# [用例 10] path=/echo-allow-trusted + caller=trusted → ALLOW_LIST 命中 → 200
test_case_10_allow_caller_service_match() {
    log_step "[用例 10] path 命中 ALLOW_LIST(CALLER_SERVICE default/trusted) 且 args 命中 → 200"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_ALLOW_TRUSTED" \
        "$(rule_with_caller_service_match "$RULE_NAME_ALLOW_TRUSTED" "ALLOW_LIST" "default" "trusted" "$PATH_CASE_8_9")"; then
        log_case_context "$PATH_CASE_8_9" \
            "${RULE_NAME_ALLOW_TRUSTED}（ALLOW_LIST, CALLER_SERVICE key=default value=trusted）" \
            "命中（X-Polaris-Caller-Service=trusted 且命名空间 default 匹配）" \
            "ALLOW_LIST 规则命中 → return true → 200"
        do_request 200 "[用例 10] CALLER_SERVICE 命中 → 200" "$PATH_CASE_8_9" \
            -H "X-Polaris-Caller-Namespace: default" -H "X-Polaris-Caller-Service: trusted"
    else
        log_case_skip "[用例 10] 创建规则失败，跳过"
    fi
}

# [用例 11] path=/echo-allow-trusted + caller=other → ALLOW_LIST 不命中 → 403
test_case_11_allow_caller_service_not_match() {
    log_step "[用例 11] path 命中 ALLOW_LIST(CALLER_SERVICE default/trusted) 但 args 不命中 → 403"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_ALLOW_TRUSTED" \
        "$(rule_with_caller_service_match "$RULE_NAME_ALLOW_TRUSTED" "ALLOW_LIST" "default" "trusted" "$PATH_CASE_8_9")"; then
        log_case_context "$PATH_CASE_8_9" \
            "${RULE_NAME_ALLOW_TRUSTED}（ALLOW_LIST, CALLER_SERVICE key=default value=trusted）" \
            "未命中（X-Polaris-Caller-Service=other ≠ trusted）" \
            "白名单规则未命中 + containsAllowList=true → return false → 403"
        do_request 403 "[用例 11] CALLER_SERVICE 未命中 → 403" "$PATH_CASE_8_9" \
            -H "X-Polaris-Caller-Namespace: default" -H "X-Polaris-Caller-Service: other"
    else
        log_case_skip "[用例 11] 创建规则失败，跳过"
    fi
}

# [用例 12] path=/echo-callee-prod + provider env=prod → CALLEE_METADATA 命中 → 200
test_case_12_allow_callee_metadata_match() {
    log_step "[用例 12] path 命中 ALLOW_LIST(CALLEE_METADATA env=prod) 且本端 metadata 命中 → 200"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_ALLOW_CALLEE_ENV_PROD" \
        "$(rule_with_callee_metadata_match "$RULE_NAME_ALLOW_CALLEE_ENV_PROD" "ALLOW_LIST" "env" "prod" "$PATH_CASE_10")" \
        "env=prod"; then
        log_case_context "$PATH_CASE_10" \
            "${RULE_NAME_ALLOW_CALLEE_ENV_PROD}（ALLOW_LIST, CALLEE_METADATA env=prod）" \
            "命中（provider 以 --register-metadata env=prod 启动，Engine.GetLocalInstanceMetadata 返回 {env:prod}）" \
            "ALLOW_LIST 规则命中 → return true → 200"
        do_request 200 "[用例 12] CALLEE_METADATA 命中 → 200" "$PATH_CASE_10"
    else
        log_case_skip "[用例 12] 创建规则失败，跳过"
    fi
}

# [用例 13] path=/echo-callee-dev + provider 仍 env=prod → CALLEE_METADATA 不命中 → 403
test_case_13_allow_callee_metadata_not_match() {
    log_step "[用例 13] path 命中 ALLOW_LIST(CALLEE_METADATA env=dev) 但本端 metadata 不命中 → 403"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_ALLOW_CALLEE_ENV_DEV" \
        "$(rule_with_callee_metadata_match "$RULE_NAME_ALLOW_CALLEE_ENV_DEV" "ALLOW_LIST" "env" "dev" "$PATH_CASE_11")" \
        "env=prod"; then
        log_case_context "$PATH_CASE_11" \
            "${RULE_NAME_ALLOW_CALLEE_ENV_DEV}（ALLOW_LIST, CALLEE_METADATA env=dev）" \
            "未命中（本端实例 metadata 只有 env=prod，规则要求 env=dev）" \
            "白名单规则未命中 + containsAllowList=true → return false → 403"
        do_request 403 "[用例 13] CALLEE_METADATA 未命中 → 403" "$PATH_CASE_11"
    else
        log_case_skip "[用例 13] 创建规则失败，跳过"
    fi
}

# [用例 14] path=/echo-custom-v1 + biz=v1 → CUSTOM 命中 → 200
test_case_14_allow_custom_match() {
    log_step "[用例 14] path 命中 ALLOW_LIST(CUSTOM biz=v1) 且 args 命中 → 200"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_ALLOW_CUSTOM_BIZ_V1" \
        "$(rule_with_custom_match "$RULE_NAME_ALLOW_CUSTOM_BIZ_V1" "ALLOW_LIST" "biz" "v1" "$PATH_CASE_12")"; then
        log_case_context "$PATH_CASE_12" \
            "${RULE_NAME_ALLOW_CUSTOM_BIZ_V1}（ALLOW_LIST, CUSTOM biz=v1）" \
            "命中（X-Custom-Arg-biz=v1 经 BuildCustomArgument 上报，与规则 EXACT 匹配）" \
            "ALLOW_LIST 规则命中 → return true → 200"
        do_request 200 "[用例 14] CUSTOM 命中 → 200" "$PATH_CASE_12" -H "X-Custom-Arg-biz: v1"
    else
        log_case_skip "[用例 14] 创建规则失败，跳过"
    fi
}

# [用例 15] path=/echo-custom-v2 + biz=v1 → CUSTOM 不命中 → 403
test_case_15_allow_custom_not_match() {
    log_step "[用例 15] path 命中 ALLOW_LIST(CUSTOM biz=v2) 但 args 不命中 → 403"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_ALLOW_CUSTOM_BIZ_V2" \
        "$(rule_with_custom_match "$RULE_NAME_ALLOW_CUSTOM_BIZ_V2" "ALLOW_LIST" "biz" "v2" "$PATH_CASE_13")"; then
        log_case_context "$PATH_CASE_13" \
            "${RULE_NAME_ALLOW_CUSTOM_BIZ_V2}（ALLOW_LIST, CUSTOM biz=v2）" \
            "未命中（请求 biz=v1 ≠ 规则 biz=v2）" \
            "白名单规则未命中 + containsAllowList=true → return false → 403"
        do_request 403 "[用例 15] CUSTOM 未命中 → 403" "$PATH_CASE_13" -H "X-Custom-Arg-biz: v1"
    else
        log_case_skip "[用例 15] 创建规则失败，跳过"
    fi
}

# ============================================================================
# 用例 16-20：MatchString 6 种类型的端到端覆盖（EXACT 已由 6-15 覆盖）
# 5 个用例都用 BLOCK_LIST + 命中场景，结果是 403：BLOCK 命中直接 return false，
# 不受 service 全局 containsAllowList 影响，便于断言匹配类型本身工作正常。
# 期望 200 的"匹配类型工作正常 + 不命中"语义请看 pkg/algorithm/match/match_test.go。
# ============================================================================

# [用例 16] REGEX：HEADER user 匹配正则 ^bad-.*$ → user=bad-foo 命中 → 403
test_case_16_match_regex() {
    log_step "[用例 16] MatchString_REGEX：HEADER user ~ ^bad-.*\$，请求 user=bad-foo → 403"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_BLOCK_REGEX" \
        "$(rule_with_header_match "$RULE_NAME_BLOCK_REGEX" "BLOCK_LIST" "user" "^bad-.*\$" "$PATH_CASE_14" "REGEX")"; then
        log_case_context "$PATH_CASE_14" \
            "${RULE_NAME_BLOCK_REGEX}（BLOCK_LIST, HEADER user REGEX ^bad-.*\$）" \
            "命中（请求 user=bad-foo 与正则 ^bad-.*\$ 匹配）" \
            "BLOCK_LIST 规则命中 → return false → 403"
        do_request 403 "[用例 16] REGEX 命中 → 403" "$PATH_CASE_14" -H "user: bad-foo"
    else
        log_case_skip "[用例 16] 创建规则失败，跳过"
    fi
}

# [用例 17] NOT_EQUALS：HEADER user != admin → user=guest 命中（≠admin） → 403
# 注意：value 不能写 "*"，否则 IsMatchAll 短路会让 NOT_EQUALS 直接 return true（变成放行）。
test_case_17_match_not_equals() {
    log_step "[用例 17] MatchString_NOT_EQUALS：HEADER user != admin，请求 user=guest → 403"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_BLOCK_NOT_EQUALS" \
        "$(rule_with_header_match "$RULE_NAME_BLOCK_NOT_EQUALS" "BLOCK_LIST" "user" "admin" "$PATH_CASE_15" "NOT_EQUALS")"; then
        log_case_context "$PATH_CASE_15" \
            "${RULE_NAME_BLOCK_NOT_EQUALS}（BLOCK_LIST, HEADER user NOT_EQUALS admin）" \
            "命中（请求 user=guest ≠ admin → 满足 NOT_EQUALS）" \
            "BLOCK_LIST 规则命中 → return false → 403"
        do_request 403 "[用例 17] NOT_EQUALS 命中 → 403" "$PATH_CASE_15" -H "user: guest"
    else
        log_case_skip "[用例 17] 创建规则失败，跳过"
    fi
}

# [用例 18] IN：HEADER user 在 bad1,bad2,bad3 中 → user=bad2 命中 → 403
test_case_18_match_in() {
    log_step "[用例 18] MatchString_IN：HEADER user ∈ {bad1,bad2,bad3}，请求 user=bad2 → 403"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_BLOCK_IN" \
        "$(rule_with_header_match "$RULE_NAME_BLOCK_IN" "BLOCK_LIST" "user" "bad1,bad2,bad3" "$PATH_CASE_16" "IN")"; then
        log_case_context "$PATH_CASE_16" \
            "${RULE_NAME_BLOCK_IN}（BLOCK_LIST, HEADER user IN bad1,bad2,bad3）" \
            "命中（请求 user=bad2 在集合 {bad1,bad2,bad3} 中）" \
            "BLOCK_LIST 规则命中 → return false → 403"
        do_request 403 "[用例 18] IN 命中 → 403" "$PATH_CASE_16" -H "user: bad2"
    else
        log_case_skip "[用例 18] 创建规则失败，跳过"
    fi
}

# [用例 19] NOT_IN：HEADER user 不在 admin,operator 中 → user=guest 命中（不在白名单） → 403
test_case_19_match_not_in() {
    log_step "[用例 19] MatchString_NOT_IN：HEADER user ∉ {admin,operator}，请求 user=guest → 403"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_BLOCK_NOT_IN" \
        "$(rule_with_header_match "$RULE_NAME_BLOCK_NOT_IN" "BLOCK_LIST" "user" "admin,operator" "$PATH_CASE_17" "NOT_IN")"; then
        log_case_context "$PATH_CASE_17" \
            "${RULE_NAME_BLOCK_NOT_IN}（BLOCK_LIST, HEADER user NOT_IN admin,operator）" \
            "命中（请求 user=guest 不在集合 {admin,operator} 中 → 满足 NOT_IN）" \
            "BLOCK_LIST 规则命中 → return false → 403"
        do_request 403 "[用例 19] NOT_IN 命中 → 403" "$PATH_CASE_17" -H "user: guest"
    else
        log_case_skip "[用例 19] 创建规则失败，跳过"
    fi
}

# [用例 20] RANGE：HEADER user 在 [100,200] 中 → user=150 命中 → 403
# RANGE 仅支持整数；非整数输入（如 "abc"、"1.5"）会让 ParseInt 失败 → 该规则视为不匹配。
test_case_20_match_range() {
    log_step "[用例 20] MatchString_RANGE：HEADER user ∈ [100,200]，请求 user=150 → 403"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_BLOCK_RANGE" \
        "$(rule_with_header_match "$RULE_NAME_BLOCK_RANGE" "BLOCK_LIST" "user" "100~200" "$PATH_CASE_18" "RANGE")"; then
        log_case_context "$PATH_CASE_18" \
            "${RULE_NAME_BLOCK_RANGE}（BLOCK_LIST, HEADER user RANGE 100~200）" \
            "命中（请求 user=150 ∈ [100,200]）" \
            "BLOCK_LIST 规则命中 → return false → 403"
        do_request 403 "[用例 20] RANGE 命中 → 403" "$PATH_CASE_18" -H "user: 150"
    else
        log_case_skip "[用例 20] 创建规则失败，跳过"
    fi
}

# ============================================================================
# 用例 21-26：维度补齐（QUERY / CALLER_IP / CALLER_METADATA）
# 已有用例覆盖 HEADER / CALLER_SERVICE / CALLEE_METADATA / CUSTOM 共 4 维度，
# 加上这 3 个维度，BlockAllowConfig.MatchArgument 7 种类型全部端到端覆盖。
# ============================================================================

# [用例 21] QUERY：path=/echo-query-cn + ?region=cn → 命中 ALLOW_LIST → 200
test_case_21_allow_query_match() {
    log_step "[用例 21] path 命中 ALLOW_LIST(QUERY region=cn) 且 args 命中 → 200"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_ALLOW_QUERY_CN" \
        "$(rule_with_query_match "$RULE_NAME_ALLOW_QUERY_CN" "ALLOW_LIST" "region" "cn" "$PATH_CASE_19_20")"; then
        log_case_context "$PATH_CASE_19_20" \
            "${RULE_NAME_ALLOW_QUERY_CN}（ALLOW_LIST, QUERY region=cn）" \
            "命中（请求 ?region=cn 经 BuildQueryArgument 上报，与规则 EXACT 匹配）" \
            "ALLOW_LIST 规则命中 → return true → 200"
        do_request 200 "[用例 21] QUERY 命中 → 200" "${PATH_CASE_19_20}?region=cn"
    else
        log_case_skip "[用例 21] 创建规则失败，跳过"
    fi
}

# [用例 22] QUERY：path=/echo-query-cn + ?region=us → 不命中 → 混合策略 → 403
test_case_22_allow_query_not_match() {
    log_step "[用例 22] path 命中 ALLOW_LIST(QUERY region=cn) 但 args 不命中 → 403"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_ALLOW_QUERY_CN" \
        "$(rule_with_query_match "$RULE_NAME_ALLOW_QUERY_CN" "ALLOW_LIST" "region" "cn" "$PATH_CASE_19_20")"; then
        log_case_context "$PATH_CASE_19_20" \
            "${RULE_NAME_ALLOW_QUERY_CN}（ALLOW_LIST, QUERY region=cn）" \
            "未命中（请求 ?region=us ≠ 规则 cn）" \
            "白名单规则未命中 + containsAllowList=true → return false → 403"
        do_request 403 "[用例 22] QUERY 未命中 → 403" "${PATH_CASE_19_20}?region=us"
    else
        log_case_skip "[用例 22] 创建规则失败，跳过"
    fi
}

# [用例 23] CALLER_IP：path=/echo-caller-ip-block + BLOCK_LIST CALLER_IP REGEX 命中 loopback → 403
# 用 REGEX ^(127\.0\.0\.1|::1)$ 兜底 IPv4/IPv6 loopback 差异。
test_case_23_block_caller_ip_match() {
    log_step "[用例 23] path 命中 BLOCK_LIST(CALLER_IP loopback) 且 args 命中 → 403"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_BLOCK_CALLER_IP_LOOPBACK" \
        "$(rule_with_caller_ip_match "$RULE_NAME_BLOCK_CALLER_IP_LOOPBACK" "BLOCK_LIST" "^(127\\\\.0\\\\.0\\\\.1|::1)\$" "$PATH_CASE_21" "REGEX")"; then
        log_case_context "$PATH_CASE_21" \
            "${RULE_NAME_BLOCK_CALLER_IP_LOOPBACK}（BLOCK_LIST, CALLER_IP REGEX ^(127\\.0\\.0\\.1|::1)\$）" \
            "命中（curl 默认连本机 → r.RemoteAddr 为 127.0.0.1 或 ::1，BuildCallerIPArgument 上报后正则匹配）" \
            "BLOCK_LIST 规则命中 → return false → 403"
        do_request 403 "[用例 23] CALLER_IP 命中 → 403" "$PATH_CASE_21"
    else
        log_case_skip "[用例 23] 创建规则失败，跳过"
    fi
}

# [用例 24] CALLER_IP：path=/echo-caller-ip-allow + ALLOW_LIST CALLER_IP REGEX 命中 loopback → 200
test_case_24_allow_caller_ip_match() {
    log_step "[用例 24] path 命中 ALLOW_LIST(CALLER_IP loopback) 且 args 命中 → 200"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_ALLOW_CALLER_IP_LOOPBACK" \
        "$(rule_with_caller_ip_match "$RULE_NAME_ALLOW_CALLER_IP_LOOPBACK" "ALLOW_LIST" "^(127\\\\.0\\\\.0\\\\.1|::1)\$" "$PATH_CASE_22" "REGEX")"; then
        log_case_context "$PATH_CASE_22" \
            "${RULE_NAME_ALLOW_CALLER_IP_LOOPBACK}（ALLOW_LIST, CALLER_IP REGEX ^(127\\.0\\.0\\.1|::1)\$）" \
            "命中（curl 本机请求 → CALLER_IP=127.0.0.1/::1 命中正则）" \
            "ALLOW_LIST 规则命中 → return true → 200"
        do_request 200 "[用例 24] CALLER_IP 命中 → 200" "$PATH_CASE_22"
    else
        log_case_skip "[用例 24] 创建规则失败，跳过"
    fi
}

# [用例 25] CALLER_METADATA：path=/echo-caller-meta-gold + tier=gold → ALLOW_LIST 命中 → 200
# 注意：必须带 X-Polaris-Caller-Service Header，否则 provider 不构造 SourceService，
# CALLER_METADATA 永远取空值 → 不命中。
test_case_25_allow_caller_metadata_match() {
    log_step "[用例 25] path 命中 ALLOW_LIST(CALLER_METADATA tier=gold) 且 args 命中 → 200"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_ALLOW_CALLER_META_GOLD" \
        "$(rule_with_caller_metadata_match "$RULE_NAME_ALLOW_CALLER_META_GOLD" "ALLOW_LIST" "tier" "gold" "$PATH_CASE_23_24")"; then
        log_case_context "$PATH_CASE_23_24" \
            "${RULE_NAME_ALLOW_CALLER_META_GOLD}（ALLOW_LIST, CALLER_METADATA tier=gold）" \
            "命中（X-Polaris-Caller-Service=app + X-Caller-Meta-tier=gold → SourceService.Metadata={tier:gold}）" \
            "ALLOW_LIST 规则命中 → return true → 200"
        do_request 200 "[用例 25] CALLER_METADATA 命中 → 200" "$PATH_CASE_23_24" \
            -H "X-Polaris-Caller-Namespace: default" \
            -H "X-Polaris-Caller-Service: app" \
            -H "X-Caller-Meta-tier: gold"
    else
        log_case_skip "[用例 25] 创建规则失败，跳过"
    fi
}

# [用例 26] CALLER_METADATA：path=/echo-caller-meta-gold + tier=silver → 不命中 → 403
test_case_26_allow_caller_metadata_not_match() {
    log_step "[用例 26] path 命中 ALLOW_LIST(CALLER_METADATA tier=gold) 但 args 不命中 → 403"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_ALLOW_CALLER_META_GOLD" \
        "$(rule_with_caller_metadata_match "$RULE_NAME_ALLOW_CALLER_META_GOLD" "ALLOW_LIST" "tier" "gold" "$PATH_CASE_23_24")"; then
        log_case_context "$PATH_CASE_23_24" \
            "${RULE_NAME_ALLOW_CALLER_META_GOLD}（ALLOW_LIST, CALLER_METADATA tier=gold）" \
            "未命中（X-Caller-Meta-tier=silver ≠ 规则 gold）" \
            "白名单规则未命中 + containsAllowList=true → return false → 403"
        do_request 403 "[用例 26] CALLER_METADATA 未命中 → 403" "$PATH_CASE_23_24" \
            -H "X-Polaris-Caller-Namespace: default" \
            -H "X-Polaris-Caller-Service: app" \
            -H "X-Caller-Meta-tier: silver"
    else
        log_case_skip "[用例 26] 创建规则失败，跳过"
    fi
}

# ============================================================================
# 用例 27-28：多 MatchArgument AND 关系
# 单条规则 arguments 数组里两个 MatchArgument（HEADER user=vip + QUERY region=cn），
# matchArguments 函数遍历 args 数组，任一不满足即整体不命中（AND 关系）。
# 用例 27 同时满足两条 → ALLOW 命中 → 200；
# 用例 28 只满足 HEADER 那条 → matchArguments 返回 false → 白名单未命中 → 403。
# ============================================================================

# [用例 27] AND 全命中：HEADER user=vip ∧ QUERY region=cn 都满足 → 200
test_case_27_multi_args_and_all_match() {
    log_step "[用例 27] 多 MatchArgument AND 全命中：HEADER user=vip ∧ QUERY region=cn → 200"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_ALLOW_AND_HEADER_QUERY" \
        "$(rule_with_two_args_header_query "$RULE_NAME_ALLOW_AND_HEADER_QUERY" "ALLOW_LIST" \
            "user" "vip" "region" "cn" "$PATH_CASE_25_26")"; then
        log_case_context "$PATH_CASE_25_26" \
            "${RULE_NAME_ALLOW_AND_HEADER_QUERY}（ALLOW_LIST, [HEADER user=vip, QUERY region=cn]）" \
            "命中（两个 MatchArgument 同时满足，AND 关系成立）" \
            "ALLOW_LIST 规则命中 → return true → 200"
        do_request 200 "[用例 27] AND 全命中 → 200" "${PATH_CASE_25_26}?region=cn" -H "user: vip"
    else
        log_case_skip "[用例 27] 创建规则失败，跳过"
    fi
}

# [用例 28] AND 部分命中：HEADER user=vip 命中 + QUERY region 缺失 → matchArguments=false → 403
test_case_28_multi_args_and_partial_miss() {
    log_step "[用例 28] 多 MatchArgument AND 部分命中：HEADER user=vip ∧ QUERY 缺失 → matchArguments=false → 403"
    set_current_service "$SERVICE_NAME"
    if prepare_case_with_rule "$RULE_NAME_ALLOW_AND_HEADER_QUERY" \
        "$(rule_with_two_args_header_query "$RULE_NAME_ALLOW_AND_HEADER_QUERY" "ALLOW_LIST" \
            "user" "vip" "region" "cn" "$PATH_CASE_25_26")"; then
        log_case_context "$PATH_CASE_25_26" \
            "${RULE_NAME_ALLOW_AND_HEADER_QUERY}（ALLOW_LIST, [HEADER user=vip, QUERY region=cn]）" \
            "未命中（HEADER user=vip 满足，但请求未带 ?region=cn → 第 2 个 MatchArgument 取空 ≠ cn → AND 失败）" \
            "白名单规则未命中（AND 关系拆穿一条即整体 false）+ containsAllowList=true → return false → 403"
        do_request 403 "[用例 28] AND 部分命中 → 403" "$PATH_CASE_25_26" -H "user: vip"
    else
        log_case_skip "[用例 28] 创建规则失败，跳过"
    fi
}

# ======================== 编译 ========================
build_binaries() {
    log_step "0/4 编译 Provider + Consumer"
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
    if [[ ! -f "${CONSUMER_DIR}/main.go" ]]; then
        log_error "找不到 Consumer 源码: ${CONSUMER_DIR}/main.go"
        exit 1
    fi

    log_info "编译 Provider..."
    (cd "$PROVIDER_DIR" && go build -o "${BUILD_DIR}/auth_provider" .)
    log_info "Provider 编译完成 -> ${BUILD_DIR}/auth_provider"

    log_info "编译 Consumer..."
    (cd "$CONSUMER_DIR" && go build -o "${BUILD_DIR}/auth_consumer" .)
    log_info "Consumer 编译完成 -> ${BUILD_DIR}/auth_consumer"

    if command -v xattr &> /dev/null; then
        xattr -c "${BUILD_DIR}/auth_provider" 2>/dev/null || true
        xattr -c "${BUILD_DIR}/auth_consumer" 2>/dev/null || true
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
    log_raw "  服务名（复用，不带 RUN_ID）:"
    log_raw "    用例 1-2  使用: ${SERVICE_NAME}（consumer 主调=${CONSUMER_SELF_SERVICE}，被调=${SERVICE_NAME}）"
    log_raw "    用例 3-5  使用: ${SERVICE_NAME_NORULE}（不创建规则，验证放行场景）"
    log_raw "    用例 6-28 使用: ${SERVICE_NAME}（17 条规则常驻，按 path 隔离）"
    log_raw "  Consumer 端口:  ${CONSUMER_PORT}（仅用例 1-2 使用，跑完立即释放）"
    log_raw "  各用例 path（path 隔离 + 规则 EXACT api.path 限定）:"
    log_raw "    用例 1/2:  ${PATH_CASE_1_2}（consumer 内部硬编码 /echo）"
    log_raw "    用例 6/7:  ${PATH_CASE_4_5}"
    log_raw "    用例 8/9:  ${PATH_CASE_6_7}"
    log_raw "    用例 10/11:  ${PATH_CASE_8_9}"
    log_raw "    用例 12:   ${PATH_CASE_10}"
    log_raw "    用例 13:   ${PATH_CASE_11}"
    log_raw "    用例 14:   ${PATH_CASE_12}"
    log_raw "    用例 15:   ${PATH_CASE_13}"
    log_raw "    用例 16:   ${PATH_CASE_14}"
    log_raw "    用例 17:   ${PATH_CASE_15}"
    log_raw "    用例 18:   ${PATH_CASE_16}"
    log_raw "    用例 19:   ${PATH_CASE_17}"
    log_raw "    用例 20:   ${PATH_CASE_18}"
    log_raw "    用例 21/22: ${PATH_CASE_19_20}"
    log_raw "    用例 23:   ${PATH_CASE_21}"
    log_raw "    用例 24:   ${PATH_CASE_22}"
    log_raw "    用例 25/26: ${PATH_CASE_23_24}"
    log_raw "    用例 27/28: ${PATH_CASE_25_26}"
    log_raw "  命名空间:       ${NAMESPACE}"
    log_raw "  Provider 端口:  ${PROVIDER_PORT} (0=自动)"
    log_raw "  规则缓存等待:   ${RULE_CACHE_WAIT}s"
    log_raw "  Token:          $([[ -n "$POLARIS_TOKEN" ]] && echo "<已设置>" || echo "<未设置>")"
    log_raw "  规则名:         auth-it-*（固定名，跨运行复用；同名规则存在即跳过创建）"
    log_raw "  清理策略:       不主动删除规则；检查同名规则，存在则复用，不存在则创建"
    log_raw "  Debug 日志:     ${DEBUG_MODE}（同时控制脚本诊断输出 + provider/consumer SDK debug 日志）"
    log_raw "  日志文件:       ${TEST_LOG_FILE}"
    _log ""

    if [[ -z "$POLARIS_TOKEN" ]]; then
        log_warn "未设置 POLARIS_TOKEN：将以匿名身份调用 OpenAPI（创建规则通常允许匿名）"
    fi

    build_binaries

    log_step "0/4 OpenAPI 端点探测"
    probe_open_api || true

    log_step "0/4 卫生检查：扫描已有规则识别脏数据"
    precheck_existing_rules

    log_step "1/4 用例 1-2：consumer ↔ provider 端到端 smoke（验证 SDK 主调身份透传）"
    test_case_1_consumer_smoke_hit
    test_case_2_consumer_smoke_miss

    log_step "1/4 用例 3：enable=false（鉴权未启用）"
    test_case_3_disabled

    log_step "2/4 用例 4-5：enable=true + 无规则 / chain=[]"
    test_case_4_enabled_no_rule
    test_case_5_enabled_empty_chain

    log_step "3/4 用例 6-28：自动建规则 + 黑白名单匹配验证（provider 进程按需重启）"
    log_service_rules_overview
    test_case_6_allow_header_match
    test_case_7_allow_header_not_match
    test_case_8_block_header_match
    test_case_9_block_header_not_match
    test_case_10_allow_caller_service_match
    test_case_11_allow_caller_service_not_match
    test_case_12_allow_callee_metadata_match
    test_case_13_allow_callee_metadata_not_match
    test_case_14_allow_custom_match
    test_case_15_allow_custom_not_match
    test_case_16_match_regex
    test_case_17_match_not_equals
    test_case_18_match_in
    test_case_19_match_not_in
    test_case_20_match_range
    test_case_21_allow_query_match
    test_case_22_allow_query_not_match
    test_case_23_block_caller_ip_match
    test_case_24_allow_caller_ip_match
    test_case_25_allow_caller_metadata_match
    test_case_26_allow_caller_metadata_not_match
    test_case_27_multi_args_and_all_match
    test_case_28_multi_args_and_partial_miss

    log_step "4/4 全部跑完，退出 trap 会再清理一遍规则"

    _log ""
    _log "${CYAN}========================================${NC}"
    _log "${CYAN}  验证结果汇总${NC}"
    _log "${CYAN}========================================${NC}"
    log_raw "  TOTAL: $TOTAL_COUNT  PASS: $TOTAL_PASS  FAIL: $TOTAL_FAIL  SKIP: $TOTAL_SKIP"
    log_raw "  日志文件: ${TEST_LOG_FILE}"

    if [[ $TOTAL_FAIL -gt 0 ]]; then
        _log "${RED}存在失败用例，详见上方日志。${NC}"
        _log ""
        _log "${YELLOW}排查建议：${NC}"
        log_raw "  1. 加上 --debug 参数重跑，让 polaris-go SDK 写 debug 级日志到："
        log_raw "       ${SCRIPT_DIR}/.build/provider_run/polaris/log/auth/polaris-auth.log"
        log_raw "     文件里 [BlockAllow] 前缀的日志能逐条还原 checkAllow 决策路径："
        log_raw "       - total_rules=N：SDK 实际拉到几条规则"
        log_raw "       - rules[i=NAME].cfg[j] policy=... api_path=... method_match=..."
        log_raw "       - args[k] type=... expect=... actual=... matched=..."
        log_raw "       - HIT → final_allow=...  或  no rule hit, containsAllowList=..."
        log_raw "  2. 若 rules[i] 中出现陌生规则（比如非 auth-it-* 命名、path=*），说明 service 下"
        log_raw "     存在脏数据。注意：商业版 polaris-server 的 OpenAPI GET 与 Discover RPC 数据"
        log_raw "     可能不一致——precheck 卫生检查走 OpenAPI 看不到的脏数据，需以 SDK debug 日志"
        log_raw "     为准。可用 Polaris 控制台或 Discover 调试工具核对。"
        log_raw "  3. 用例 1/2 失败：检查 ${LOG_DIR}/consumer.log 与 provider.log 的 [CALLER] 行，"
        log_raw "     确认 X-Polaris-Caller-Service Header 是否被 SDK 正确透传。"
        exit 1
    fi
    _log "${GREEN}全部通过。${NC}"
}

main "$@"
