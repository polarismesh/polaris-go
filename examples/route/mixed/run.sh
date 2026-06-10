#!/bin/bash
# ============================================================
# 混合路由 E2E 测试: 泳道路由 × 规则路由 × 就近路由 共存
#
# 详细用例矩阵见 examples/route/mixed/test.md
#
# 用法:
#   ./run.sh <命令> [polaris地址]
#   命令: all | build | check | start | test | stop | cleanup
#
# 环境变量:
#   POLARIS_HOST          Polaris 服务端 IP (默认 127.0.0.1)
#   POLARIS_TOKEN         鉴权 token (可选)
#   DEBUG_MODE            true/false 是否开 SDK debug 日志
#   AUTO_CREATE_RULE      true/false 是否自动创建 Polaris 规则 (默认 true)
# ============================================================

set -u
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ==================== 配置 ====================
POLARIS_HOST="${POLARIS_HOST:-127.0.0.1}"
POLARIS_TOKEN="${POLARIS_TOKEN:-}"
DEBUG_MODE="${DEBUG_MODE:-false}"
AUTO_CREATE_RULE="${AUTO_CREATE_RULE:-true}"

POLARIS_HTTP_ADDR=""
init_config() {
    POLARIS_HTTP_ADDR="http://${POLARIS_HOST}:8090"
}

# 服务名 / 端口
# 命名空间固定为 default, 规则检查后会按下面的策略处理:
#   - 0 份:  自动创建
#   - 1 份:  复用现有规则, 不重复创建
#   - N>1 份: 提示这是 server 端历史残留(SDK 没有 DELETE 权限, 物理无法清理),
#            继续复用第一份, 并在头部输出影响提示, 测试结果可能因 SDK 读到
#            全部 inbounds 而不准确。
NAMESPACE="default"
PROVIDER_SERVICE="MixedRouteEchoServer"
CONSUMER_SERVICE="MixedRouteEchoClient"

CONSUMER_ALL_PORT=18180   # ruleBasedRouter.failoverType=all
CONSUMER_NONE_PORT=18181  # ruleBasedRouter.failoverType=none

PROVIDER_BASE_DEV_PORT=28181   # env=dev
PROVIDER_BASE_TEST_PORT=28182  # env=test
PROVIDER_BASE_PROD_PORT=28183  # env=prod
PROVIDER_GRAY_DEV_PORT=28184   # lane=gray, env=dev
PROVIDER_GRAY_TEST_PORT=28185  # lane=gray, env=test
# 故意不注册 gray-prod, 用于测试 lane × rule 交集为空的 STRICT 503

# 规则名
LANE_GROUP_NAME="mixed-lane-group"
RULE_ROUTE_NAME="mixed-rule-route"
NEARBY_ROUTE_NAME="mixed-nearby-route"

# 目录
BUILD_DIR="${SCRIPT_DIR}/.build"
LOG_DIR="${SCRIPT_DIR}/.logs"
PID_FILE="${SCRIPT_DIR}/.mixed-pids"
mkdir -p "${BUILD_DIR}" "${LOG_DIR}"
TEST_LOG="${LOG_DIR}/mixed-test-$(date +%Y%m%d_%H%M%S).log"

# 颜色
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'

_log()      { local m="$1"; echo -e "$m"; echo -e "$m" | sed 's/\x1b\[[0-9;]*m//g' >> "${TEST_LOG}" 2>/dev/null; }
log_info()  { _log "${BLUE}[INFO]${NC} $*"; }
log_ok()    { _log "${GREEN}[PASS]${NC} $*"; }
log_warn()  { _log "${YELLOW}[WARN]${NC} $*"; }
log_fail()  { _log "${RED}[FAIL]${NC} $*"; }
log_title() { _log "\n${YELLOW}========== $* ==========${NC}"; }
log_raw()   { echo "$1"; echo "$1" >> "${TEST_LOG}" 2>/dev/null; }

TOTAL_PASS=0; TOTAL_FAIL=0
# 标记 server 端是否存在同名规则的"残留" (>1 份), 用于在测试开始前提示用户。
# SDK 端没有 DELETE 权限, 物理清理必须用 admin 账号手工操作。
DUPLICATE_RULE_DETECTED=0
test_pass() { TOTAL_PASS=$((TOTAL_PASS+1)); log_ok "$*"; }
test_fail() { TOTAL_FAIL=$((TOTAL_FAIL+1)); log_fail "$*"; }

# ==================== 构建 ====================
build_binaries() {
    log_title "构建 provider / consumer"
    if (cd "${SCRIPT_DIR}/provider" && go build -o "${BUILD_DIR}/provider" .); then
        log_ok "provider 构建成功"
    else
        log_fail "provider 构建失败"; return 1
    fi
    if (cd "${SCRIPT_DIR}/consumer" && go build -o "${BUILD_DIR}/consumer" .); then
        log_ok "consumer 构建成功"
    else
        log_fail "consumer 构建失败"; return 1
    fi
}

# ==================== 端口清理 ====================
cleanup_ports() {
    local ports=(
        "${CONSUMER_ALL_PORT}" "${CONSUMER_NONE_PORT}"
        "${PROVIDER_BASE_DEV_PORT}" "${PROVIDER_BASE_TEST_PORT}" "${PROVIDER_BASE_PROD_PORT}"
        "${PROVIDER_GRAY_DEV_PORT}" "${PROVIDER_GRAY_TEST_PORT}"
    )
    for port in "${ports[@]}"; do
        local pids
        pids=$(lsof -iTCP:"${port}" -sTCP:LISTEN -t 2>/dev/null || true)
        for pid in $pids; do
            log_warn "端口 ${port} 被 PID=${pid} 占用,kill"
            kill "$pid" 2>/dev/null || true
        done
    done
    sleep 1
}

# ==================== Polaris 规则: 泳道组 ====================
fetch_lane_groups() {
    curl -s --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/v1/Discover" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "{\"service\":{\"name\":\"${CONSUMER_SERVICE}\",\"namespace\":\"${NAMESPACE}\"},\"type\":\"LANE\"}" \
        2>/dev/null || true
}

ensure_lane_group() {
    log_info "检查泳道组 ${LANE_GROUP_NAME}..."
    local resp
    resp=$(fetch_lane_groups)
    local found
    found=$(echo "$resp" | LANE_NAME="$LANE_GROUP_NAME" python3 -c "
import os, sys, json
target = os.environ['LANE_NAME']
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
for g in (data.get('lanes') or []):
    if g.get('name') == target:
        print('yes')
        break
" 2>/dev/null)
    if [[ "$found" == "yes" ]]; then
        log_ok "泳道组 ${LANE_GROUP_NAME} 已存在"
        return 0
    fi

    if [[ "$AUTO_CREATE_RULE" != "true" ]]; then
        log_fail "泳道组 ${LANE_GROUP_NAME} 不存在且未启用自动创建"
        return 1
    fi

    log_info "自动创建泳道组 ${LANE_GROUP_NAME}..."
    local result
    result=$(LANE_NAME="$LANE_GROUP_NAME" \
        CONSUMER_SVC="$CONSUMER_SERVICE" PROVIDER_SVC="$PROVIDER_SERVICE" \
        NAMESPACE="$NAMESPACE" \
        POLARIS_HTTP_ADDR="$POLARIS_HTTP_ADDR" POLARIS_TOKEN="$POLARIS_TOKEN" \
        python3 -c "
import os, json, urllib.request, urllib.error

group = {
    'name': os.environ['LANE_NAME'],
    'description': 'auto-created by mixed/run.sh',
    'entries': [{
        'type': 'polarismesh.cn/service',
        'selector': {
            '@type': 'type.googleapis.com/v1.ServiceSelector',
            'namespace': os.environ['NAMESPACE'],
            'service': os.environ['CONSUMER_SVC'],
        },
    }],
    'destinations': [
        {'service': os.environ['PROVIDER_SVC'], 'namespace': os.environ['NAMESPACE']},
    ],
    'rules': [
        {
            'name': 'gray',
            'enable': True,
            'match_mode': 'STRICT',
            'default_label_value': 'gray',
            'traffic_match_rule': {
                'arguments': [{
                    'type': 'HEADER', 'key': 'user',
                    'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'gray'},
                }],
                'match_mode': 'AND',
            },
        },
        {
            'name': 'strict-noexist',
            'enable': True,
            'match_mode': 'STRICT',
            'default_label_value': 'strict-noexist',
            'traffic_match_rule': {
                'arguments': [{
                    'type': 'HEADER', 'key': 'user',
                    'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'strict'},
                }],
                'match_mode': 'AND',
            },
        },
        {
            'name': 'permissive-noexist',
            'enable': True,
            'match_mode': 'PERMISSIVE',
            'default_label_value': 'noexist',
            'traffic_match_rule': {
                'arguments': [{
                    'type': 'HEADER', 'key': 'user',
                    'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'permissive'},
                }],
                'match_mode': 'AND',
            },
        },
    ],
}

req = urllib.request.Request(
    f\"{os.environ['POLARIS_HTTP_ADDR']}/naming/v1/lane/groups\",
    data=json.dumps([group]).encode('utf-8'),
    method='POST',
    headers={'Content-Type': 'application/json', 'X-Polaris-Token': os.environ['POLARIS_TOKEN']},
)
try:
    resp = urllib.request.urlopen(req, timeout=10)
    rd = json.loads(resp.read().decode('utf-8'))
    code = rd.get('code', 0)
    if code in (200000, 200001):
        print('OK')
    else:
        print(f\"ERR|{code}|{rd.get('info','')}\")
except urllib.error.HTTPError as e:
    body = e.read().decode('utf-8', errors='replace')
    print(f'ERR|{e.code}|{body[:200]}')
except Exception as e:
    print(f'ERR|0|{e}')
" 2>/dev/null)

    if [[ "${result:0:2}" == "OK" ]]; then
        log_ok "泳道组 ${LANE_GROUP_NAME} 创建成功"
        return 0
    fi
    log_fail "泳道组创建失败: ${result}"
    return 1
}

# ==================== Polaris 规则: 规则路由 ====================
fetch_routings_by_name() {
    local name="$1"
    curl -s --connect-timeout 5 --max-time 10 \
        --request GET "${POLARIS_HTTP_ADDR}/naming/v2/routings?name=${name}&offset=0&limit=10" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" 2>/dev/null || true
}

ensure_rule_route() {
    log_info "检查规则路由 ${RULE_ROUTE_NAME}..."
    local resp
    resp=$(fetch_routings_by_name "${RULE_ROUTE_NAME}")
    local existing_ids
    existing_ids=$(echo "$resp" | NAME="$RULE_ROUTE_NAME" python3 -c "
import os, sys, json
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
for item in (data.get('data') or []):
    if item.get('name') == os.environ['NAME']:
        print(item.get('id', ''))
" 2>/dev/null)

    if [[ -n "$existing_ids" ]]; then
        # 规则已存在,直接复用,避免反复删除重建导致 server 端积累
        # 多份同名规则 (SDK 会读到全部 inbounds, 进而干扰 failoverType=none 等
        # 依赖 dstRuleFail 分支的语义判断)。
        local n
        n=$(echo "$existing_ids" | grep -c . || true)
        if [[ "$n" -eq 1 ]]; then
            log_ok "规则路由 ${RULE_ROUTE_NAME} 已存在 (复用, id=$(echo "$existing_ids" | head -1))"
            return 0
        fi
        # N>1 份: 历史上反复 delete+recreate (delete API 失败或异步) 导致
        # server 端残留, SDK 端无 DELETE 权限, 这里不再尝试清理, 也不再
        # 追加新规则, 直接复用第一份。SDK 会读到全部 inbounds, 用例 7/12/14/16
        # 会受影响; 若需要干净状态, 请用 server 端 admin 账号手工删除
        # 命名空间 default 下的同名规则。
        log_warn "规则路由 ${RULE_ROUTE_NAME} 已存在 ${n} 份 (server 历史残留), SDK 会读到全部, 复用第一份 (id=$(echo "$existing_ids" | head -1))"
        log_warn "    → 受影响用例: 7/12/14/16 (rule×lane 交集判定与 failoverType=none 行为)。需用 admin 清理后才能稳定通过。"
        DUPLICATE_RULE_DETECTED=1
        return 0
    fi

    if [[ "$AUTO_CREATE_RULE" != "true" ]]; then
        log_warn "未启用自动创建,跳过规则路由创建"
        return 0
    fi

    log_info "自动创建规则路由 ${RULE_ROUTE_NAME}..."
    local create_body
    create_body=$(NAME="$RULE_ROUTE_NAME" SERVICE_NAME="$PROVIDER_SERVICE" NAMESPACE="$NAMESPACE" \
        python3 -c "
import os, json
svc = os.environ['SERVICE_NAME']
ns  = os.environ['NAMESPACE']
name = os.environ['NAME']
rules = []
for e in ['dev', 'test', 'prod']:
    rules.append({
        'name': f'env-{e}',
        'sources': [{
            'service': '*', 'namespace': '*',
            'arguments': [{
                'type': 'QUERY', 'key': 'env',
                'value': {'type': 'EXACT', 'value': e, 'value_type': 'TEXT'},
            }],
        }],
        'destinations': [{
            'service': svc, 'namespace': ns,
            'labels': {'env': {'type': 'EXACT', 'value': e, 'value_type': 'TEXT'}},
            'priority': 0, 'weight': 100, 'name': f'dest-{e}',
        }],
    })
print(json.dumps([{
    'name': name, 'namespace': ns,
    'enable': True,
    'routing_policy': 'RulePolicy',
    'routing_config': {
        '@type': 'type.googleapis.com/v1.RuleRoutingConfig',
        'rules': rules,
    },
    'priority': 0,
    'description': 'auto-created by mixed/run.sh',
}]))
" 2>/dev/null)

    local resp_body http_code code
    http_code=$(curl -s -o /tmp/_mixed_rule_$$.tmp -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/naming/v2/routings" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "${create_body}" 2>/dev/null || echo "000")
    resp_body=$(cat /tmp/_mixed_rule_$$.tmp 2>/dev/null || echo "")
    rm -f /tmp/_mixed_rule_$$.tmp
    code=$(echo "$resp_body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('code',0))" 2>/dev/null || echo "?")

    if [[ "$http_code" == "200" ]] && [[ "$code" =~ ^20000[01]$ ]]; then
        log_ok "规则路由创建成功"
        return 0
    fi
    log_fail "规则路由创建失败 (HTTP=${http_code}, code=${code})"
    log_warn "响应: ${resp_body:0:300}"
    return 1
}

# ==================== Polaris 规则: 就近路由 ====================
ensure_nearby_route() {
    log_info "检查就近路由 ${NEARBY_ROUTE_NAME}..."

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/v1/Discover" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "{\"service\":{\"name\":\"${PROVIDER_SERVICE}\",\"namespace\":\"${NAMESPACE}\"},\"type\":\"NEARBY_ROUTE_RULE\"}" \
        2>/dev/null || true)

    local existing_ids
    existing_ids=$(echo "$resp" | NAME="$NEARBY_ROUTE_NAME" python3 -c "
import os, sys, json
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
for r in (data.get('nearbyRouteRules') or []):
    if r.get('name') == os.environ['NAME']:
        print(r.get('id', ''))
" 2>/dev/null)

    if [[ -n "$existing_ids" ]]; then
        # 规则已存在,直接复用,避免反复删除重建导致 server 端积累
        # 多份同名规则。
        local n
        n=$(echo "$existing_ids" | grep -c . || true)
        if [[ "$n" -eq 1 ]]; then
            log_ok "就近路由规则 ${NEARBY_ROUTE_NAME} 已存在 (复用, id=$(echo "$existing_ids" | head -1))"
            return 0
        fi
        # N>1 份: 历史上反复 delete+recreate 失败导致 server 端残留,
        # SDK 端无 DELETE 权限, 这里不再尝试清理, 也不再追加新规则,
        # 直接复用第一份。
        log_warn "就近路由规则 ${NEARBY_ROUTE_NAME} 已存在 ${n} 份 (server 历史残留), 复用第一份 (id=$(echo "$existing_ids" | head -1))"
        DUPLICATE_RULE_DETECTED=1
        return 0
    fi

    if [[ "$AUTO_CREATE_RULE" != "true" ]]; then
        log_warn "未启用自动创建,跳过就近路由创建"
        return 0
    fi

    local create_body
    create_body=$(NAME="$NEARBY_ROUTE_NAME" SERVICE_NAME="$PROVIDER_SERVICE" NAMESPACE="$NAMESPACE" \
        python3 -c "
import os, json
print(json.dumps([{
    'name': os.environ['NAME'],
    'namespace': os.environ['NAMESPACE'],
    'enable': True,
    'routing_policy': 'NearbyPolicy',
    'routing_config': {
        '@type': 'type.googleapis.com/v1.NearbyRoutingConfig',
        'service': os.environ['SERVICE_NAME'],
        'namespace': os.environ['NAMESPACE'],
        # 本地测试环境实例无地域信息(region/zone 为空), nearby router 默认会
        # 全量回退; 这里 ZONE + max=ALL 是合法且最常见的配置, 不强制就近。
        'match_level': 'ZONE',
        'max_match_level': 'ALL',
        'strict_nearby': False,
    },
    'priority': 0,
    'description': 'auto-created by mixed/run.sh (no geo info → 全量回退)',
}]))
" 2>/dev/null)

    local resp_body http_code code
    http_code=$(curl -s -o /tmp/_mixed_nearby_$$.tmp -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/naming/v2/routings" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "${create_body}" 2>/dev/null || echo "000")
    resp_body=$(cat /tmp/_mixed_nearby_$$.tmp 2>/dev/null || echo "")
    rm -f /tmp/_mixed_nearby_$$.tmp
    code=$(echo "$resp_body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('code',0))" 2>/dev/null || echo "?")

    if [[ "$http_code" == "200" ]] && [[ "$code" =~ ^20000[01]$ ]]; then
        log_ok "就近路由规则创建成功"
        return 0
    fi
    log_fail "就近路由规则创建失败 (HTTP=${http_code}, code=${code})"
    log_warn "响应: ${resp_body:0:300}"
    return 1
}

ensure_all_rules() {
    log_title "校验/创建 Polaris 三类规则"
    ensure_lane_group   || return 1
    ensure_rule_route   || return 1
    ensure_nearby_route || return 1
    # 规则准备完毕后再做一次全局体检: 多份同名规则会让 SDK 读到 N 倍 inbounds,
    # 进而干扰 failoverType=none 等依赖 dstRuleFail 分支的判定 (routebase 走
    # failoverType 时输出空 cluster, 但后续 nearby router 用全量 clusters 重新
    # 过滤, 不消费上游空 cluster, 导致用例 12/14/16 等期望 503 的场景拿不到 503)。
    check_rule_accumulation || return 1
    log_info "等待 Polaris naming cache 刷新 (10s)..."
    sleep 10
}

# ==================== 规则存量体检 ====================
# 服务端 Polaris 不会因为 run.sh 重跑而物理删除同名路由 (naming/v2/routings/delete
# 需要 admin 权限, 默认 token 没有), 旧版 delete+recreate 逻辑在 delete 失败时
# 会让同名规则逐次累积, SDK 端会读到多份规则的 inbounds。
#
# 本函数在三类规则都准备完之后, 主动探测服务端是否存在多余 1 份的同名规则,
# 若有则打印可执行的清理命令清单, 提示用户用有 DELETE 权限的 POLARIS_TOKEN 清理。
check_rule_accumulation() {
    local issues=0

    local lane_count
    lane_count=$(fetch_lane_groups | LANE_NAME="$LANE_GROUP_NAME" python3 -c "
import os, sys, json
target = os.environ['LANE_NAME']
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
print(sum(1 for g in (data.get('lanes') or []) if g.get('name') == target))
" 2>/dev/null)
    if [[ "${lane_count:-0}" -gt 1 ]]; then
        issues=$((issues+1))
        log_warn "泳道组 ${LANE_GROUP_NAME} 存在 ${lane_count} 份 (SDK 按 name 区分, 多份并存会同时生效)"
    fi

    local rule_count
    rule_count=$(fetch_routings_by_name "${RULE_ROUTE_NAME}" | NAME="$RULE_ROUTE_NAME" python3 -c "
import os, sys, json
target = os.environ['NAME']
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
print(sum(1 for it in (data.get('data') or []) if it.get('name') == target))
" 2>/dev/null)
    if [[ "${rule_count:-0}" -gt 1 ]]; then
        issues=$((issues+1))
        log_warn "规则路由 ${RULE_ROUTE_NAME} 存在 ${rule_count} 份 (SDK 一次读到所有 inbounds, 干扰 dstRuleFail 判定)"
    fi

    local nearby_count
    nearby_count=$(curl -s --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/v1/Discover" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "{\"service\":{\"name\":\"${PROVIDER_SERVICE}\",\"namespace\":\"${NAMESPACE}\"},\"type\":\"NEARBY_ROUTE_RULE\"}" \
        2>/dev/null | NAME="$NEARBY_ROUTE_NAME" python3 -c "
import os, sys, json
target = os.environ['NAME']
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
print(sum(1 for r in (data.get('nearbyRouteRules') or []) if r.get('name') == target))
" 2>/dev/null)
    if [[ "${nearby_count:-0}" -gt 1 ]]; then
        issues=$((issues+1))
        log_warn "就近路由规则 ${NEARBY_ROUTE_NAME} 存在 ${nearby_count} 份"
    fi

    if [[ "$issues" -gt 0 ]]; then
        log_warn "服务端存在历史残留同名规则, 继续运行可能让用例 7/12/14/16 误判"
        log_warn "推荐两种处理方式 (任选其一):"
        log_warn "  1) 用有 delete 权限的 POLARIS_TOKEN 跑一次清理 (例: 假设 token 在 \$ADMIN_TOKEN):"
        log_warn "       ADMIN_TOKEN=... bash /Users/evelynwei/work/git_repo/polarismesh/polaris-go/examples/route/mixed/cleanup_rules.sh"
        log_warn "  2) 先在 Polaris 控制台手动删除多余规则, 再重跑 ./run.sh all"
        # 即使有警告也继续 (脚本已经会复用现有 1 份规则, 只是多余那份无法被复用避免)
        return 0
    fi
    return 0
}

# ==================== 启动服务 ====================
write_polaris_yaml() {
    local target_file="$1"
    local failover_type="$2"
    cat > "${target_file}" <<EOF
global:
  serverConnector:
    addresses:
      - ${POLARIS_HOST}:8091
    connectTimeout: 3s
consumer:
  serviceRouter:
    chain:
      - ruleBasedRouter
      - nearbyBasedRouter
    plugin:
      laneRouter:
        baseLaneMode: 0
      ruleBasedRouter:
        failoverType: ${failover_type}
      nearbyBasedRouter:
        # matchLevel 必须是 region / zone / campus 之一; 本地测试环境实例无地域
        # 信息(region/zone 为空) → 走 matchLocation 时空字符串等价"匹配任意",
        # 等价于不强制就近; maxMatchLevel 不设置(默认 "")即 AllLevel, 允许降级到所有级别。
        matchLevel: zone
        maxMatchLevel: ""
        strictNearby: false
EOF
}

start_services() {
    log_title "启动服务实例"
    cleanup_ports
    : > "${PID_FILE}"
    if [ ! -x "${BUILD_DIR}/provider" ] || [ ! -x "${BUILD_DIR}/consumer" ]; then
        build_binaries || return 1
    fi

    local debug_flag=""
    [ "${DEBUG_MODE}" = "true" ] && debug_flag="-debug"

    start_provider() {
        local label="$1" port="$2" md="$3"
        local workdir="${BUILD_DIR}/${label}-run"
        mkdir -p "${workdir}"
        write_polaris_yaml "${workdir}/polaris.yaml" "all"
        log_info "启动 ${label} (port=${port}, metadata=${md})..."
        (cd "${workdir}" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
            "${BUILD_DIR}/provider" \
            -namespace="${NAMESPACE}" -service="${PROVIDER_SERVICE}" \
            -port="${port}" -metadata="${md}" \
            -token="${POLARIS_TOKEN}" \
            ${debug_flag} \
            > "${LOG_DIR}/${label}.log" 2>&1) &
        echo $! >> "${PID_FILE}"
        log_info "${label} PID=$!"
    }

    start_provider "provider-base-dev"  "${PROVIDER_BASE_DEV_PORT}"  "env=dev"
    start_provider "provider-base-test" "${PROVIDER_BASE_TEST_PORT}" "env=test"
    start_provider "provider-base-prod" "${PROVIDER_BASE_PROD_PORT}" "env=prod"
    start_provider "provider-gray-dev"  "${PROVIDER_GRAY_DEV_PORT}"  "lane=gray&env=dev"
    start_provider "provider-gray-test" "${PROVIDER_GRAY_TEST_PORT}" "lane=gray&env=test"

    start_consumer() {
        local label="$1" port="$2" failover="$3"
        local workdir="${BUILD_DIR}/${label}-run"
        mkdir -p "${workdir}"
        write_polaris_yaml "${workdir}/polaris.yaml" "${failover}"
        log_info "启动 ${label} (port=${port}, failoverType=${failover})..."
        (cd "${workdir}" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
            "${BUILD_DIR}/consumer" \
            -namespace="${NAMESPACE}" -service="${PROVIDER_SERVICE}" \
            -selfNamespace="${NAMESPACE}" -selfService="${CONSUMER_SERVICE}" \
            -port="${port}" \
            -token="${POLARIS_TOKEN}" \
            ${debug_flag} \
            > "${LOG_DIR}/${label}.log" 2>&1) &
        echo $! >> "${PID_FILE}"
        log_info "${label} PID=$!"
    }

    start_consumer "consumer-all"  "${CONSUMER_ALL_PORT}"  "all"
    start_consumer "consumer-none" "${CONSUMER_NONE_PORT}" "none"
}

wait_for_services() {
    log_title "等待服务就绪"
    local max_wait=60 elapsed=0
    while [ $elapsed -lt $max_wait ]; do
        local code
        code=$(curl -s -o /dev/null -w '%{http_code}' --connect-timeout 3 --max-time 5 \
            "http://127.0.0.1:${CONSUMER_ALL_PORT}/echo?env=dev" 2>/dev/null || echo "000")
        if [[ "$code" == "200" ]]; then
            log_ok "consumer-all 就绪 (耗时 ${elapsed}s)"
            # 同步等待 consumer-none
            for i in $(seq 1 10); do
                local c2
                c2=$(curl -s -o /dev/null -w '%{http_code}' --connect-timeout 3 --max-time 5 \
                    "http://127.0.0.1:${CONSUMER_NONE_PORT}/echo?env=dev" 2>/dev/null || echo "000")
                if [[ "$c2" == "200" ]]; then
                    log_ok "consumer-none 就绪"
                    return 0
                fi
                sleep 1
            done
            log_warn "consumer-none 检查超时,继续测试"
            return 0
        fi
        sleep 2; elapsed=$((elapsed+2))
        log_info "等待 consumer-all... (${elapsed}s/${max_wait}s, HTTP=${code})"
    done
    log_fail "服务等待超时"
    return 1
}

# ==================== 用例 ====================
# probe_one CONSUMER_PORT HEADER_USER QUERY_ENV → 输出 "<http_code>|<body>"
probe_one() {
    local port="$1" user="$2" env="$3"
    local url="http://127.0.0.1:${port}/echo"
    [[ -n "$env" ]] && url="${url}?env=${env}"

    local tmp=$(mktemp -t mixed-resp.XXXXXX) http_code body
    if [[ -n "$user" ]]; then
        http_code=$(curl -s --connect-timeout 5 --max-time 10 \
            -H "user: ${user}" \
            -o "$tmp" -w "%{http_code}" "$url" 2>/dev/null || echo "000")
    else
        http_code=$(curl -s --connect-timeout 5 --max-time 10 \
            -o "$tmp" -w "%{http_code}" "$url" 2>/dev/null || echo "000")
    fi
    body=$(cat "$tmp" 2>/dev/null || echo "")
    rm -f "$tmp"
    echo "${http_code}|${body}"
}

# extract_callee_port BODY → 提取 ":<port>" 中的 port,失败返回空
extract_callee_port() {
    local body="$1"
    echo "$body" | grep -oE 'callee=[^:]+:[0-9]+' | head -1 | grep -oE '[0-9]+$' || true
}

# 用例 1-16: 见 test.md
# 通用断言: run_case <编号> <consumer-port> <header-user> <query-env> <expect_kind> <expect_arg>
#   expect_kind:
#     PORT      期望命中端口 == expect_arg (10 次全命中)
#     ANY_BASE  期望命中任一 base 实例 (≠ gray-*)
#     HTTP_503  期望 HTTP 503

# print_case_brief 输出用例的背景、生效规则、预期路由链路
# 参数: <consumer-name> <user-header> <env-query> <expect_kind> <expect_arg>
print_case_brief() {
    local consumer="$1" user="$2" env="$3" kind="$4" expect="$5"

    # ---- 背景: 当前请求语义 ----
    local bg
    case "${user}" in
        "")            bg="无染色 Header (基线流量)" ;;
        gray)          bg="user=gray → 命中泳道组的 gray 规则 (STRICT)" ;;
        strict)        bg="user=strict → 命中 strict-noexist (STRICT, default_label_value 在实例端不存在)" ;;
        permissive)    bg="user=permissive → 命中 permissive-noexist (PERMISSIVE, default_label_value 在实例端不存在)" ;;
        *)             bg="user=${user}" ;;
    esac
    case "${env}" in
        dev|test|prod) bg="${bg}; ?env=${env} → 命中规则路由 env-${env} (要求实例 metadata env=${env})" ;;
        nomatch)       bg="${bg}; ?env=nomatch → 不匹配任何 rule inbound" ;;
        *)             bg="${bg}; env=${env}" ;;
    esac
    log_raw "  [背景] ${bg}"

    # ---- 生效规则: 三类规则在本用例的输出 ----
    local lane_out rule_out nearby_out
    case "${user}" in
        "")
            lane_out="laneRouter: 无规则匹配, baseLaneMode=0 → 输出 baseline cluster (instanceFilter 排除带 lane key 的实例)" ;;
        gray)
            lane_out="laneRouter: 命中 gray (STRICT) → 输出 cluster {Metadata: lane=gray}, 限定到 gray-dev/gray-test 两实例" ;;
        strict)
            lane_out="laneRouter: 命中 strict-noexist (STRICT) + 无对应实例 → 返回空 cluster + IgnoreFilterOnlyOnEndChain=true (短路主链)" ;;
        permissive)
            lane_out="laneRouter: 命中 permissive-noexist (PERMISSIVE) + 无对应实例 → 回退基线 (instanceFilter 排除 lane key)" ;;
    esac
    log_raw "  [lane]   ${lane_out}"

    case "${env}" in
        dev|test|prod)
            rule_out="ruleBasedRouter: 命中 env-${env} 规则 → 叠加 metadata env=${env}, 在上游 cluster 内按 env 过滤" ;;
        nomatch)
            local fo_label="all"
            [[ "${consumer}" == "none" ]] && fo_label="none"
            if [[ "${fo_label}" == "all" ]]; then
                rule_out="ruleBasedRouter: 无规则匹配, failoverType=all → 兜底返回上游 cluster (透传, 不收敛)"
            else
                rule_out="ruleBasedRouter: 无规则匹配, failoverType=none → 返回空 cluster + IgnoreFilterOnlyOnEndChain=true"
            fi
            ;;
    esac
    # 当 lane 已经短路时, rule 不会执行, 提示这一点
    if [[ "${user}" == "strict" ]]; then
        rule_out="(skipped: laneRouter 已短路主链)"
    fi
    log_raw "  [rule]   ${rule_out}"

    nearby_out="nearbyBasedRouter: matchLevel=zone, maxMatchLevel=ALL, 实例 zone 为空 → 等价 noop (透传上游 cluster)"
    if [[ "${user}" == "strict" ]] || \
       ( [[ "${env}" == "nomatch" ]] && [[ "${consumer}" == "none" ]] ) || \
       ( [[ "${user}" == "gray" ]] && [[ "${env}" == "prod" ]] ); then
        nearby_out="(skipped: 上游 cluster 实例为 0, processServiceRouters 短路退出)"
    fi
    log_raw "  [nearby] ${nearby_out}"

    # ---- 预期 ----
    local expect_text
    case "${kind}" in
        PORT)
            local label="实例"
            case "${expect}" in
                "${PROVIDER_BASE_DEV_PORT}")  label="provider-base-dev (env=dev)" ;;
                "${PROVIDER_BASE_TEST_PORT}") label="provider-base-test (env=test)" ;;
                "${PROVIDER_BASE_PROD_PORT}") label="provider-base-prod (env=prod)" ;;
                "${PROVIDER_GRAY_DEV_PORT}")  label="provider-gray-dev (lane=gray, env=dev)" ;;
                "${PROVIDER_GRAY_TEST_PORT}") label="provider-gray-test (lane=gray, env=test)" ;;
            esac
            expect_text="HTTP 200, 命中 ${label} (port ${expect})"
            ;;
        ANY_BASE)
            expect_text="HTTP 200, 命中任一 base 实例 (28181/28182/28183), 非 gray" ;;
        HTTP_503)
            expect_text="HTTP 503 (空 cluster → LB 报 ErrCodeAPIInstanceNotFound)" ;;
    esac
    log_raw "  [预期]   ${expect_text}"
}

run_case() {
    local idx="$1" port="$2" user="$3" env="$4" kind="$5" expect="$6"
    local consumer_name
    consumer_name=$([ "${port}" = "${CONSUMER_ALL_PORT}" ] && echo all || echo none)
    local label="用例${idx}: consumer=${consumer_name} user=${user:-(none)} env=${env:-(none)}"
    log_info "--- ${label} (期望: ${kind}=${expect}) ---"
    print_case_brief "${consumer_name}" "${user}" "${env}" "${kind}" "${expect}"

    local rounds=10 ok=0 distrib_dev_b=0 distrib_test_b=0 distrib_prod_b=0 distrib_dev_g=0 distrib_test_g=0 other=0
    for i in $(seq 1 ${rounds}); do
        local res http_code body
        res=$(probe_one "${port}" "${user}" "${env}")
        http_code="${res%%|*}"
        body="${res#*|}"
        log_raw "  [${i}] HTTP=${http_code}, body=${body:0:140}"

        case "$kind" in
            PORT)
                if [[ "$http_code" == "200" ]]; then
                    local p
                    p=$(extract_callee_port "$body")
                    if [[ "$p" == "$expect" ]]; then ok=$((ok+1)); fi
                fi
                ;;
            ANY_BASE)
                if [[ "$http_code" == "200" ]]; then
                    local p
                    p=$(extract_callee_port "$body")
                    case "$p" in
                        "${PROVIDER_BASE_DEV_PORT}"|"${PROVIDER_BASE_TEST_PORT}"|"${PROVIDER_BASE_PROD_PORT}")
                            ok=$((ok+1)) ;;
                    esac
                fi
                ;;
            HTTP_503)
                if [[ "$http_code" == "503" ]]; then ok=$((ok+1)); fi
                ;;
        esac

        # 分布统计 (用于诊断)
        if [[ "$http_code" == "200" ]]; then
            local p
            p=$(extract_callee_port "$body")
            case "$p" in
                "${PROVIDER_BASE_DEV_PORT}")  distrib_dev_b=$((distrib_dev_b+1)) ;;
                "${PROVIDER_BASE_TEST_PORT}") distrib_test_b=$((distrib_test_b+1)) ;;
                "${PROVIDER_BASE_PROD_PORT}") distrib_prod_b=$((distrib_prod_b+1)) ;;
                "${PROVIDER_GRAY_DEV_PORT}")  distrib_dev_g=$((distrib_dev_g+1)) ;;
                "${PROVIDER_GRAY_TEST_PORT}") distrib_test_g=$((distrib_test_g+1)) ;;
                *) other=$((other+1)) ;;
            esac
        fi

        sleep 0.15
    done

    log_info "  分布: base-dev=${distrib_dev_b}, base-test=${distrib_test_b}, base-prod=${distrib_prod_b}, gray-dev=${distrib_dev_g}, gray-test=${distrib_test_g}, other=${other}; 命中预期=${ok}/${rounds}"

    if [[ "$kind" == "HTTP_503" ]]; then
        if [[ $ok -ge 8 ]]; then
            test_pass "[${label}] 多数返回 503 (${ok}/${rounds})"
        else
            test_fail "[${label}] 期望 503 但只有 ${ok}/${rounds} 次返回 503"
        fi
    else
        if [[ $ok -eq $rounds ]]; then
            test_pass "[${label}] 全部命中预期 (${ok}/${rounds})"
        elif [[ $ok -ge 8 ]]; then
            test_pass "[${label}] 多数命中预期 (${ok}/${rounds})"
        else
            test_fail "[${label}] 命中数不足 (${ok}/${rounds})"
        fi
    fi
}

run_tests() {
    log_title "运行混合路由测试用例"
    TOTAL_PASS=0; TOTAL_FAIL=0

    # 如果检查规则时发现 server 端有同名规则残留 (N>1), 在用例开始前醒目提示
    if [[ "${DUPLICATE_RULE_DETECTED}" == "1" ]]; then
        log_warn "==========================================================="
        log_warn "Server 端检测到同名规则残留 (SDK 没有 DELETE 权限, 已自动跳过创建)"
        log_warn "SDK 会把残留的全部 inbounds 一起读进来, 部分用例可能不稳定"
        log_warn "清理方法 (二选一):"
        log_warn "  1) 提供 admin token 重跑:    POLARIS_TOKEN=xxx ./run.sh all"
        log_warn "  2) 手工调 PUT 端点把残留规则 enable=false:"
        log_warn "     curl -X PUT '${POLARIS_HTTP_ADDR}/naming/v2/routings/enable' \\"
        log_warn "          -H 'Content-Type: application/json' \\"
        log_warn "          -d '[{\"id\":\"<id>\",\"name\":\"mixed-rule-route\",\"enable\":false}]'"
        log_warn "==========================================================="
    fi

    # ===== consumer-all (failoverType=all) =====
    log_title "consumer-all (failoverType=all)"
    log_raw "  [组背景] consumer 端 ruleBasedRouter.failoverType=all"
    log_raw "           → 主调路由规则全部失败时, ruleBasedRouter 兜底返回上游 cluster (透传 lane 输出)"
    log_raw "           → 链路: laneRouter (beforeChain) → ruleBasedRouter → nearbyBasedRouter → filterOnly (afterChain)"
    log_raw "           → 5 个 provider: base-dev/test/prod (28181-28183), gray-dev/test (28184/28185); 故意不注册 gray-prod"
    run_case 1  "${CONSUMER_ALL_PORT}" "" "dev"     PORT     "${PROVIDER_BASE_DEV_PORT}"
    run_case 2  "${CONSUMER_ALL_PORT}" "" "test"    PORT     "${PROVIDER_BASE_TEST_PORT}"
    run_case 3  "${CONSUMER_ALL_PORT}" "" "prod"    PORT     "${PROVIDER_BASE_PROD_PORT}"
    run_case 4  "${CONSUMER_ALL_PORT}" "" "nomatch" ANY_BASE ""
    run_case 5  "${CONSUMER_ALL_PORT}" "gray"       "dev"  PORT "${PROVIDER_GRAY_DEV_PORT}"
    run_case 6  "${CONSUMER_ALL_PORT}" "gray"       "test" PORT "${PROVIDER_GRAY_TEST_PORT}"
    run_case 7  "${CONSUMER_ALL_PORT}" "gray"       "prod" HTTP_503 ""
    run_case 8  "${CONSUMER_ALL_PORT}" "strict"     "dev"  HTTP_503 ""
    run_case 9  "${CONSUMER_ALL_PORT}" "permissive" "dev"  PORT "${PROVIDER_BASE_DEV_PORT}"
    run_case 10 "${CONSUMER_ALL_PORT}" "permissive" "test" PORT "${PROVIDER_BASE_TEST_PORT}"

    # ===== consumer-none (failoverType=none) =====
    log_title "consumer-none (failoverType=none)"
    log_raw "  [组背景] consumer 端 ruleBasedRouter.failoverType=none"
    log_raw "           → 主调路由规则全部失败时, ruleBasedRouter 返回空 cluster + 抑制 filterOnly 兜底 → 触发 503"
    log_raw "           → 与 consumer-all 共用同一组泳道组/规则路由/就近路由规则"
    run_case 11 "${CONSUMER_NONE_PORT}" "" "dev"     PORT "${PROVIDER_BASE_DEV_PORT}"
    run_case 12 "${CONSUMER_NONE_PORT}" "" "nomatch" HTTP_503 ""
    run_case 13 "${CONSUMER_NONE_PORT}" "gray"       "dev"     PORT "${PROVIDER_GRAY_DEV_PORT}"
    run_case 14 "${CONSUMER_NONE_PORT}" "gray"       "nomatch" HTTP_503 ""
    run_case 15 "${CONSUMER_NONE_PORT}" "strict"     "dev"     HTTP_503 ""
    run_case 16 "${CONSUMER_NONE_PORT}" "permissive" "nomatch" HTTP_503 ""

    log_title "测试结果汇总"
    _log "总计: $((TOTAL_PASS+TOTAL_FAIL))  ${GREEN}通过: ${TOTAL_PASS}${NC}  ${RED}失败: ${TOTAL_FAIL}${NC}"
    return ${TOTAL_FAIL}
}

# ==================== 停止 ====================
stop_services() {
    log_title "停止所有服务"
    if [ -f "${PID_FILE}" ]; then
        while read -r pid; do
            [ -z "$pid" ] && continue
            if kill -0 "$pid" 2>/dev/null; then
                kill "$pid" 2>/dev/null || true
            fi
        done < "${PID_FILE}"
        sleep 3
        while read -r pid; do
            [ -z "$pid" ] && continue
            if kill -0 "$pid" 2>/dev/null; then
                kill -9 "$pid" 2>/dev/null || true
            fi
        done < "${PID_FILE}"
        rm -f "${PID_FILE}"
    fi
    cleanup_ports
}

cleanup_workspace() {
    log_title "清理工作目录"
    rm -rf "${BUILD_DIR}" "${LOG_DIR}"
    log_ok "已清理 ${BUILD_DIR} 和 ${LOG_DIR}"
}

# ==================== 入口 ====================
usage() {
    cat <<EOF
用法: $0 <命令> [polaris地址]

命令:
  all      完整流程: build → check → start → wait → test → stop
  build    仅构建二进制
  check    仅校验/创建 Polaris 规则 (lane-group + rule-route + nearby-route)
  start    构建 + 校验规则 + 启动服务
  test     执行测试用例 (要求服务已启动)
  stop     停止 provider/consumer
  cleanup  清理 .build / .logs

环境变量:
  POLARIS_HOST          Polaris IP (默认 127.0.0.1)
  POLARIS_TOKEN         鉴权 token (可选)
  DEBUG_MODE            true/false 是否开 SDK debug
  AUTO_CREATE_RULE      true/false 自动创建规则 (默认 true)

示例:
  $0 all                                # 默认 127.0.0.1
  $0 all 1.2.3.4                        # 指定 Polaris IP
  POLARIS_TOKEN=xxx $0 all 1.2.3.4
  AUTO_CREATE_RULE=false $0 check 1.2.3.4

详细用例矩阵: $(realpath "${SCRIPT_DIR}/test.md")
EOF
}

CMD="${1:-all}"
[ -n "${2:-}" ] && POLARIS_HOST="$2"
init_config

echo "===== 混合路由 E2E 测试日志 $(date '+%Y-%m-%d %H:%M:%S') =====" > "${TEST_LOG}"
log_info "测试日志: ${TEST_LOG}"
log_info "Polaris: ${POLARIS_HTTP_ADDR}"

case "${CMD}" in
    all)
        build_binaries || exit 1
        ensure_all_rules || { log_warn "规则准备失败,但仍会启动服务尝试; 部分用例可能失败"; }
        start_services || { stop_services; exit 1; }
        wait_for_services || { stop_services; exit 1; }
        run_tests; rc=$?
        stop_services
        exit $rc
        ;;
    build)   build_binaries ;;
    check)   ensure_all_rules ;;
    start)   build_binaries && ensure_all_rules && start_services && wait_for_services ;;
    test)    run_tests ;;
    stop)    stop_services ;;
    cleanup) cleanup_workspace ;;
    -h|--help|help) usage ;;
    *) log_fail "未知命令: ${CMD}"; usage; exit 1 ;;
esac
