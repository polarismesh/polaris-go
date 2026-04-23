#!/bin/bash
# =============================================================================
# 规则路由(Rule Route)功能验证脚本
#
# 使用方法:
#   chmod +x verify_rule_route.sh
#   ./verify_rule_route.sh [--polaris-server <地址>] [--polaris-token <令牌>]
#                          [--service <服务名>] [--namespace <命名空间>]
#                          [--request-count <请求次数>]
#
# 前置条件:
#   1. 北极星服务端(Polaris Server)已启动
#   2. Go 环境已安装
#
#   本脚本会自动在北极星服务端检查/创建所需的路由规则（按请求 Query env 匹配）。
#
# 验证原理:
#   规则路由允许 Consumer 在发起服务调用时，根据请求携带的标签（Header/Query），
#   通过北极星控制台配置的路由规则，将请求路由到匹配的 Provider 实例。
#
#   本脚本启动四个 Provider 实例：
#     - provider-dev:  携带标签 env=dev
#     - provider-test: 携带标签 env=test
#     - provider-pre:  携带标签 env=pre
#     - provider-prod: 携带标签 env=prod
#   然后启动一个 Consumer，通过 query 参数 ?env=xxx 指定路由目标。
#
#   验证：Consumer 发出的请求应该只路由到与 query 参数匹配的 Provider 实例。
#   例如 ?env=pre 应只路由到 provider-pre。
#
# 预期的规则路由配置（本脚本自动维护，不需要控制台操作）:
#   规则名称: RouteEchoServer-auto-rule
#   作用服务: RouteEchoServer (default 命名空间)
#   入站规则: 对每个 env ∈ {dev, test, pre, prod}
#     - 源: 请求参数 QUERY env=<env>
#     - 目标: metadata env=<env> 的 Provider 实例（priority=0, weight=100）
#
# 验证流程:
#   1. 环境准备
#   2. 编译 Provider 和 Consumer
#   3. 检查/创建路由规则（通过北极星管理 API）
#   4. 启动 Provider-dev / Provider-test / Provider-pre / Provider-prod
#   5. 启动 Consumer
#   6. 发送请求验证规则路由
#   7. 汇总验证结论
# =============================================================================

set -euo pipefail

# ======================== 默认配置 ========================
POLARIS_SERVER="${POLARIS_SERVER:-127.0.0.1}"
POLARIS_TOKEN="${POLARIS_TOKEN:-}"
SERVICE_NAME="${SERVICE_NAME:-RouteEchoServer}"
SELF_SERVICE="${SELF_SERVICE:-}"
NAMESPACE="${NAMESPACE:-default}"
CONSUMER_PORT="${CONSUMER_PORT:-18080}"
PROVIDER_DEV_PORT="${PROVIDER_DEV_PORT:-28081}"  # 固定端口：同一实例 ID 在 Polaris 中被重复注册时会被覆盖，避免 zombie 实例累积
PROVIDER_TEST_PORT="${PROVIDER_TEST_PORT:-28082}"
PROVIDER_PRE_PORT="${PROVIDER_PRE_PORT:-28083}"
PROVIDER_PROD_PORT="${PROVIDER_PROD_PORT:-28084}"
REQUEST_COUNT="${REQUEST_COUNT:-10}"           # 每个 env 值的验证请求次数
DEBUG_MODE="${DEBUG_MODE:-false}"
SKIP_RULE_CHECK="${SKIP_RULE_CHECK:-false}"    # 跳过规则检查/创建（默认不跳过）
AUTO_CREATE_RULE="${AUTO_CREATE_RULE:-true}"   # 规则不存在时自动创建

# 路由测试的 env 值列表
ROUTE_ENVS="${ROUTE_ENVS:-dev,test,pre,prod}"
# 期望的规则路由名称（由本脚本自动创建/维护）
EXPECTED_RULE_NAME="${EXPECTED_RULE_NAME:-${SERVICE_NAME}-auto-rule}"

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
        --service)
            SERVICE_NAME="$2"; shift 2 ;;
        --self-service)
            SELF_SERVICE="$2"; shift 2 ;;
        --namespace)
            NAMESPACE="$2"; shift 2 ;;
        --consumer-port)
            CONSUMER_PORT="$2"; shift 2 ;;
        --provider-dev-port)
            PROVIDER_DEV_PORT="$2"; shift 2 ;;
        --provider-test-port)
            PROVIDER_TEST_PORT="$2"; shift 2 ;;
        --provider-pre-port)
            PROVIDER_PRE_PORT="$2"; shift 2 ;;
        --provider-prod-port)
            PROVIDER_PROD_PORT="$2"; shift 2 ;;
        --request-count)
            REQUEST_COUNT="$2"; shift 2 ;;
        --route-envs)
            ROUTE_ENVS="$2"; shift 2 ;;
        --rule-name)
            EXPECTED_RULE_NAME="$2"; shift 2 ;;
        --skip-rule-check)
            SKIP_RULE_CHECK="true"; shift ;;
        --no-auto-create)
            AUTO_CREATE_RULE="false"; shift ;;
        --debug)
            DEBUG_MODE="true"; shift ;;
        --help|-h)
            echo "用法: $0 [选项]"
            echo ""
            echo "选项:"
            echo "  --polaris-server <地址>         北极星服务端地址 (默认: 127.0.0.1)"
            echo "  --polaris-token <令牌>          北极星鉴权令牌 (默认: 空)"
            echo "  --service <服务名>              Provider 服务名 (默认: RouteEchoServer)"
            echo "  --self-service <服务名>         Consumer 自身服务名 (默认: 空)"
            echo "  --namespace <命名空间>          命名空间 (默认: default)"
            echo "  --consumer-port <端口>          Consumer HTTP端口 (默认: 18080)"
            echo "  --provider-dev-port <端口>      Provider-dev 端口 (默认: 自动分配)"
            echo "  --provider-test-port <端口>     Provider-test 端口 (默认: 自动分配)"
            echo "  --provider-pre-port <端口>      Provider-pre 端口 (默认: 自动分配)"
            echo "  --provider-prod-port <端口>     Provider-prod 端口 (默认: 自动分配)"
            echo "  --request-count <次数>          每个 env 值的验证请求次数 (默认: 10)"
            echo "  --route-envs <列表>             要测试的 env 值，逗号分隔 (默认: dev,test,pre,prod)"
            echo "  --rule-name <名称>              期望的路由规则名称 (默认: <service>-auto-rule)"
            echo "  --skip-rule-check               跳过服务端规则检查/创建"
            echo "  --no-auto-create                规则不符合预期时不自动创建（仅检查）"
            echo "  --debug                         启用 debug 日志"
            exit 0
            ;;
        *)
            echo -e "${RED}未知参数: $1${NC}"; exit 1 ;;
    esac
done

# ======================== 全局变量 ========================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROVIDER_DIR="${SCRIPT_DIR}/provider"
CONSUMER_DIR="${SCRIPT_DIR}/consumer"
BUILD_DIR="${SCRIPT_DIR}/.build"
LOG_DIR="${SCRIPT_DIR}/.logs"
POLARIS_HTTP_ADDR="http://${POLARIS_SERVER}:8090"

PROVIDER_DEV_PID=""
PROVIDER_TEST_PID=""
PROVIDER_PRE_PID=""
PROVIDER_PROD_PID=""
CONSUMER_PID=""
PROVIDER_DEV_ACTUAL_PORT=""
PROVIDER_TEST_ACTUAL_PORT=""
PROVIDER_PRE_ACTUAL_PORT=""
PROVIDER_PROD_ACTUAL_PORT=""

# ======================== 清理函数 ========================
cleanup() {
    log_info "清理进程..."
    for pid_var in CONSUMER_PID PROVIDER_DEV_PID PROVIDER_TEST_PID PROVIDER_PRE_PID PROVIDER_PROD_PID; do
        local pid="${!pid_var}"
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
            log_info "已停止进程 (PID: $pid)"
        fi
    done
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

# cleanup_zombies_on_ports 清理本测试使用的固定端口上残留的僵尸进程
# 上一次测试被 Ctrl-C 中断或 cleanup 没命中全部 PID 时，会导致 bind: address already in use。
cleanup_zombies_on_ports() {
    local ports=("$CONSUMER_PORT" "$PROVIDER_DEV_PORT" "$PROVIDER_TEST_PORT" "$PROVIDER_PRE_PORT" "$PROVIDER_PROD_PORT")
    local any=false
    for port in "${ports[@]}"; do
        [[ -z "$port" ]] && continue
        local pids
        pids=$(lsof -iTCP:"${port}" -sTCP:LISTEN -t 2>/dev/null || true)
        [[ -z "$pids" ]] && continue
        for pid in $pids; do
            log_warn "端口 ${port} 被残留进程 PID=${pid} 占用，先杀掉..."
            kill "$pid" 2>/dev/null || true
            any=true
        done
    done
    [[ "$any" == false ]] && return 0
    sleep 2
    # 硬杀剩余
    for port in "${ports[@]}"; do
        [[ -z "$port" ]] && continue
        local pids
        pids=$(lsof -iTCP:"${port}" -sTCP:LISTEN -t 2>/dev/null || true)
        [[ -z "$pids" ]] && continue
        for pid in $pids; do
            log_warn "端口 ${port} 上 PID=${pid} 未响应，发送 SIGKILL"
            kill -9 "$pid" 2>/dev/null || true
        done
    done
    sleep 1
}

# ======================== 规则路由规则校验与自动创建 ========================
#
# 设计说明:
#   规则路由在北极星商业版中通过 v2 routings 接口维护。我们先用 Discover 接口
#   查询目标服务当前的规则路由，看是否已经存在一条覆盖所有 env 分组的规则；
#   若不存在则通过 /naming/v2/routings POST 自动创建。
#
# 规则语义:
#   对每个 env ∈ ROUTE_ENVS：
#     请求参数 QUERY env=<env>  →  Provider(metadata env=<env>, weight=100)
#
# Discover 查询接口（读）:
#   POST http://${POLARIS_HTTP_ADDR}/v1/Discover
#   Body: {"service":{"name":"<SERVICE>","namespace":"<NS>"},"type":"ROUTING"}
#
# v2 创建接口（写）:
#   POST http://${POLARIS_HTTP_ADDR}/naming/v2/routings
#   Body: [{"routing_policy":"RulePolicy", "routing_config":{"@type":"...","rules":[...]}}]
#   注：顶层 RouteRule 不带 @type；仅 routing_config (google.protobuf.Any) 需要 @type。

# fetch_routing_rules 读取目标服务当前的规则路由。
fetch_routing_rules() {
    curl -s --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/v1/Discover" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "{\"service\":{\"name\":\"${SERVICE_NAME}\",\"namespace\":\"${NAMESPACE}\"},\"type\":\"ROUTING\"}" \
        2>/dev/null || true
}

# query_routing_rule_by_name 从 v2 routings 查询指定名称的规则，返回 JSON（无结果为空）。
# 用于确认规则是否需要新建。
query_routing_rule_by_name() {
    local rule_name="$1"
    # 使用 URL 编码简单替代
    local encoded
    encoded=$(python3 -c "import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1]))" "$rule_name")
    curl -s --connect-timeout 5 --max-time 10 \
        "${POLARIS_HTTP_ADDR}/naming/v2/routings?name=${encoded}&service=${SERVICE_NAME}&namespace=${NAMESPACE}&offset=0&limit=20" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        2>/dev/null || true
}

# validate_routing_rules 校验当前路由规则是否满足预期（按 env 分组路由到匹配 metadata）
# 返回 0: 已存在且覆盖所有期望 env
#     1: 规则不存在或缺少期望的 env 分组
validate_routing_rules() {
    local response
    response=$(fetch_routing_rules)

    if [[ -z "$response" ]]; then
        log_error "查询规则路由失败，无法连接 Polaris 服务端: ${POLARIS_HTTP_ADDR}"
        return 1
    fi

    local result
    result=$(ROUTE_ENVS="$ROUTE_ENVS" SERVICE_NAME="$SERVICE_NAME" NAMESPACE="$NAMESPACE" \
        python3 -c "
import os, sys, json

envs = [e.strip() for e in os.environ['ROUTE_ENVS'].split(',') if e.strip()]
svc = os.environ['SERVICE_NAME']
ns  = os.environ['NAMESPACE']

try:
    data = json.load(sys.stdin)
except Exception as e:
    print(f'ERROR|Discover 响应解析失败: {e}')
    sys.exit(0)

code = data.get('code', 0)
if code != 200000:
    info = data.get('info', '')
    print(f'ERROR|Discover 返回码: {code}, info: {info}')
    sys.exit(0)

routing = data.get('routing') or {}
inbounds = routing.get('inbounds') or []

# 收集已有 (source metadata key=env, value=<e> -> dest metadata env=<e>) 的分组。
# 说明: Polaris v2 的 arguments 类型在 v1 Discover 响应中会被降级为 source.metadata:
#   - type=QUERY  → key: \$query.<k>
#   - type=HEADER → key: \$header.<k>
#   - type=CUSTOM → key: <k>
# polaris-go SDK 的 RuleBasedRouter.matchSourceMetadata 直接用 SourceService.Metadata[key]
# 做匹配，因此必须要求 source 使用 type=CUSTOM（降级后为 \"env\" 纯 key），否则 SDK 中
# 请求侧需要显式把 \"env\" 塞到 SourceService.Metadata 里才能匹配上，可移植性差。
# 本脚本自动创建的规则使用 type=CUSTOM，这里只接受 'env' 纯 key 作为 Covered 判定；
# 发现其它前缀形态（\$query.env 等）时记为 unfriendly，促使重新创建规则。
covered = set()
unfriendly = set()
for entry in inbounds:
    sources = entry.get('sources') or []
    dests = entry.get('destinations') or []
    for src in sources:
        md = src.get('metadata') or {}
        for k, v in md.items():
            if k == 'env':
                key_kind = 'custom'
            elif k in ('\$query.env', '\$header.env'):
                key_kind = 'prefixed'
            else:
                continue
            value = v.get('value') if isinstance(v, dict) else None
            if not value or value not in envs:
                continue
            # 目标 metadata 需要命中同名 env
            dst_ok = False
            for d in dests:
                ds = d.get('service', '')
                dns = d.get('namespace', '')
                if ds not in ('*', svc) or dns not in ('*', ns):
                    continue
                dmd = d.get('metadata') or {}
                dv = dmd.get('env')
                if isinstance(dv, dict) and dv.get('value') == value:
                    dst_ok = True
                    break
            if not dst_ok:
                continue
            if key_kind == 'custom':
                covered.add(value)
            else:
                unfriendly.add(value)

missing = [e for e in envs if e not in covered]
if missing and unfriendly:
    print(f'MISSING|缺少 CUSTOM 类型的 env 分组: {missing}（发现 \$query./\$header. 前缀的规则不兼容 SDK 约定）')
elif missing:
    print(f'MISSING|{\",\".join(missing)}')
else:
    print(f'OK|所有期望 env 已覆盖: {envs}')
" <<< "$response")

    local kind payload
    IFS='|' read -r kind payload <<< "$result"
    case "$kind" in
        OK)
            log_info "规则路由校验通过: ${payload}"
            return 0
            ;;
        MISSING)
            log_warn "规则路由缺少以下 env 分组: ${payload}"
            return 1
            ;;
        ERROR|*)
            log_error "规则路由校验失败: ${payload}"
            return 1
            ;;
    esac
}

# create_routing_rule 通过 v2 routings 接口创建规则（若同名规则已存在则先删除再新建）
create_routing_rule() {
    log_info "尝试在北极星创建规则路由 [${EXPECTED_RULE_NAME}]..."

    # 先查同名规则是否已存在；存在则先删除，避免 DuplicateRoutingName
    local existing
    existing=$(query_routing_rule_by_name "${EXPECTED_RULE_NAME}")
    local existing_ids
    existing_ids=$(echo "${existing}" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
for item in (data.get('data') or []):
    if item.get('name') == '${EXPECTED_RULE_NAME}':
        print(item.get('id', ''))
" 2>/dev/null || true)

    if [[ -n "${existing_ids}" ]]; then
        log_info "检测到同名规则已存在 (ids=$(echo "${existing_ids}" | tr '\n' ' '))，尝试先删除再重建..."
        local del_body
        del_body=$(echo "${existing_ids}" | python3 -c "
import sys, json
ids = [l.strip() for l in sys.stdin.read().splitlines() if l.strip()]
out = [{'id':i,'name':'${EXPECTED_RULE_NAME}'} for i in ids]
print(json.dumps(out))
" 2>/dev/null)
        if [[ -n "${del_body}" ]]; then
            local del_resp del_http del_code
            del_http=$(curl -s -o /tmp/_rule_del_$$.tmp -w '%{http_code}' \
                --connect-timeout 5 --max-time 10 \
                --request POST "${POLARIS_HTTP_ADDR}/naming/v2/routings/delete" \
                --header "X-Polaris-Token:${POLARIS_TOKEN}" \
                --header 'Content-Type: application/json' \
                --data-raw "${del_body}" 2>/dev/null || echo "000")
            del_resp=$(cat /tmp/_rule_del_$$.tmp 2>/dev/null || echo "")
            rm -f /tmp/_rule_del_$$.tmp
            del_code=$(echo "$del_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('code',0))" 2>/dev/null || echo "?")
            if [[ "$del_code" =~ ^20000[01]$ ]]; then
                log_info "旧规则已删除"
            else
                # 鉴权场景下删除可能返回 403002；此时继续新建，Polaris 允许同名规则共存。
                log_warn "删除旧规则失败 (HTTP=${del_http}, code=${del_code})；将直接新增一条规则，同名规则可能共存"
            fi
        fi
    fi

    # 构造 create body；每个 env 一个子规则（rules[]），源 CUSTOM env=<e>，目标 metadata env=<e>。
    # 说明：type=CUSTOM 会在服务端 v1 Discover 响应中降级为 source.metadata["env"] (无 $query./$header. 前缀)，
    # 与 polaris-go SDK 中 RuleBasedRouter.matchSourceMetadata 读取 SourceService.Metadata["env"] 的约定一致，
    # 这是 polaris-go 示例工程(pkg test/testdata/route_rule/*.json)中标准的写法。
    local create_body
    create_body=$(ROUTE_ENVS="$ROUTE_ENVS" SERVICE_NAME="$SERVICE_NAME" \
        NAMESPACE="$NAMESPACE" EXPECTED_RULE_NAME="$EXPECTED_RULE_NAME" \
        python3 -c "
import os, json

envs = [e.strip() for e in os.environ['ROUTE_ENVS'].split(',') if e.strip()]
svc = os.environ['SERVICE_NAME']
ns  = os.environ['NAMESPACE']
name = os.environ['EXPECTED_RULE_NAME']

rules = []
for e in envs:
    rules.append({
        'name': f'env-{e}',
        'sources': [{
            'service': '*',
            'namespace': '*',
            'arguments': [{
                'type': 'CUSTOM',
                'key': 'env',
                'value': {
                    'type': 'EXACT',
                    'value': e,
                    'value_type': 'TEXT',
                }
            }]
        }],
        'destinations': [{
            'service': svc,
            'namespace': ns,
            'labels': {
                'env': {
                    'type': 'EXACT',
                    'value': e,
                    'value_type': 'TEXT',
                }
            },
            'priority': 0,
            'weight': 100,
            'name': f'dest-{e}',
        }]
    })

payload = [{
    'name': name,
    'namespace': ns,
    'enable': True,
    'routing_policy': 'RulePolicy',
    'routing_config': {
        '@type': 'type.googleapis.com/v1.RuleRoutingConfig',
        'rules': rules,
    },
    'priority': 0,
    'description': 'auto-created by verify_rule_route.sh',
}]
print(json.dumps(payload))
" 2>/dev/null)

    if [[ -z "${create_body}" ]]; then
        log_error "构造规则路由创建 body 失败"
        return 1
    fi

    local resp http_code
    http_code=$(curl -s -o /tmp/_rule_create_resp_$$.tmp -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/naming/v2/routings" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "${create_body}" 2>/dev/null || echo "000")
    resp=$(cat /tmp/_rule_create_resp_$$.tmp 2>/dev/null || echo "")
    rm -f /tmp/_rule_create_resp_$$.tmp

    local code info
    code=$(echo "$resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('code',0))" 2>/dev/null || echo "?")
    info=$(echo "$resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('info',''))" 2>/dev/null || echo "?")

    # 商业版 Polaris: 200000/200001 均视为创建成功
    if [[ "$http_code" == "200" ]] && [[ "$code" =~ ^20000[01]$ ]]; then
        log_info "✅ 规则路由创建成功 (code=${code})"
        # 启用规则（etime 设置）
        enable_routing_rule || true
        return 0
    fi

    log_error "规则路由创建失败 (HTTP=${http_code}, code=${code}, info=${info})"
    log_error "响应: ${resp:0:500}"
    return 1
}

# enable_routing_rule 确保规则是启用状态（新建后 enable 已为 true，这里做一次幂等兜底）
enable_routing_rule() {
    local existing
    existing=$(query_routing_rule_by_name "${EXPECTED_RULE_NAME}")
    local enable_body
    enable_body=$(echo "${existing}" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
out = []
for item in (data.get('data') or []):
    if item.get('name') != '${EXPECTED_RULE_NAME}':
        continue
    out.append({
        'id': item.get('id', ''),
        'name': item.get('name', ''),
        'enable': True,
    })
print(json.dumps(out) if out else '')
" 2>/dev/null)

    if [[ -z "${enable_body}" ]] || [[ "${enable_body}" == "[]" ]]; then
        return 0
    fi

    curl -s --connect-timeout 5 --max-time 10 \
        --request PUT "${POLARIS_HTTP_ADDR}/naming/v2/routings/enable" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "${enable_body}" > /dev/null 2>&1 || true
}

# ensure_routing_rules 规则不存在/不完整时自动创建；控制整体流程
ensure_routing_rules() {
    log_step "检查/创建规则路由"
    if [[ "$SKIP_RULE_CHECK" == "true" ]]; then
        log_warn "已通过 --skip-rule-check 跳过规则检查，直接进入后续流程"
        return 0
    fi

    if validate_routing_rules; then
        log_info "规则路由已符合预期，无需创建"
        return 0
    fi

    if [[ "$AUTO_CREATE_RULE" != "true" ]]; then
        log_error "规则路由不符合预期，且已通过 --no-auto-create 禁用自动创建"
        log_error "请在北极星控制台手动配置以下规则后重试："
        log_error "  规则: 请求参数 QUERY env=<e>  →  Provider metadata env=<e>   (e ∈ ${ROUTE_ENVS})"
        exit 1
    fi

    if ! create_routing_rule; then
        log_error "自动创建规则路由失败"
        exit 1
    fi

    log_info "等待 6s 让 Polaris 服务端传播规则变更..."
    sleep 6

    if ! validate_routing_rules; then
        log_error "规则路由创建后仍未通过校验，请检查北极星日志"
        exit 1
    fi
    return 0
}

# ======================== 生成 polaris.yaml ========================
generate_provider_polaris_yaml() {
    local target_dir="$1"
    local yaml_file="${target_dir}/polaris.yaml"

    cat > "$yaml_file" <<EOF
global:
  serverConnector:
    addresses:
      - ${POLARIS_SERVER}:8091
    token: ${POLARIS_TOKEN}
EOF
    log_info "生成 Provider polaris.yaml -> $yaml_file"
}

generate_consumer_polaris_yaml() {
    local target_dir="$1"
    local yaml_file="${target_dir}/polaris.yaml"

    cat > "$yaml_file" <<EOF
global:
  serverConnector:
    addresses:
      - ${POLARIS_SERVER}:8091
  statReporter:
    enable: true
    chain:
      - prometheus
    plugin:
      prometheus:
        type: push
        address: ${POLARIS_SERVER}:9091
        interval: 10s
consumer:
  serviceRouter:
    chain:
      # 启用规则路由
      - ruleBasedRouter
    plugin:
      ruleBasedRouter:
        failoverType: all
EOF
    log_info "生成 Consumer polaris.yaml -> $yaml_file"
}

# 启动一个 Provider 实例
# 参数: $1=名称 $2=metadata $3=指定端口 $4=日志文件
start_provider() {
    local name="$1"
    local meta="$2"
    local port="$3"
    local log_file="$4"

    local workdir="${BUILD_DIR}/${name}"
    mkdir -p "$workdir"
    generate_provider_polaris_yaml "$workdir"

    (cd "$workdir" && "${BUILD_DIR}/provider" \
        --namespace "$NAMESPACE" \
        --service "$SERVICE_NAME" \
        --token "$POLARIS_TOKEN" \
        --port "$port" \
        --metadata "$meta" \
        > "$log_file" 2>&1) &
    local pid=$!
    log_info "${name} 已启动 (PID: $pid, metadata: ${meta})"

    sleep 1
    check_process_alive "$pid" "$name" || {
        log_error "${name} 启动失败，请检查日志: $log_file"
        cat "$log_file" 2>/dev/null || true
        exit 1
    }

    # 获取实际端口
    local actual_port
    if [[ "$port" != "0" ]]; then
        actual_port="$port"
    else
        actual_port=$(extract_port_from_log "$log_file" 20) || {
            log_error "无法获取 ${name} 端口，请检查日志: $log_file"
            cat "$log_file" 2>/dev/null || true
            exit 1
        }
    fi
    log_info "${name} 实际监听端口: ${actual_port}"

    wait_for_http "http://127.0.0.1:${actual_port}/echo" 20 "$name" "$pid" || exit 1

    # 通过全局变量返回 PID 和端口
    _STARTED_PID="$pid"
    _STARTED_PORT="$actual_port"
}

# ======================== 主流程 ========================

main() {
    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║        规则路由(Rule Route)功能验证脚本          ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "配置信息:"
    echo "  北极星服务端:       ${POLARIS_SERVER}:8091 (HTTP: ${POLARIS_HTTP_ADDR})"
    echo "  服务名:             ${SERVICE_NAME}"
    echo "  命名空间:           ${NAMESPACE}"
    echo "  Consumer端口:       ${CONSUMER_PORT}"
    echo "  Consumer自身服务:   ${SELF_SERVICE:-（未设置）}"
    echo "  测试 env 值:        ${ROUTE_ENVS}"
    echo "  每个 env 请求次数:  ${REQUEST_COUNT}"
    echo "  期望规则名称:       ${EXPECTED_RULE_NAME}"
    echo "  跳过规则检查:       ${SKIP_RULE_CHECK}"
    echo "  自动创建规则:       ${AUTO_CREATE_RULE}"
    echo ""

    # ==================== 步骤1: 环境准备 ====================
    log_step "1/9 环境准备"

    mkdir -p "$BUILD_DIR" "$LOG_DIR"

    # 清理上次运行可能残留在固定端口上的僵尸进程（bind: address already in use 防御）
    cleanup_zombies_on_ports

    # 检查 Go 环境
    if ! command -v go &> /dev/null; then
        log_error "Go 未安装，请先安装 Go"
        exit 1
    fi
    log_info "Go 版本: $(go version)"

    # 检查 python3 (用于 JSON 解析和构造 body)
    if ! command -v python3 &> /dev/null; then
        log_error "python3 未安装；本脚本依赖 python3 解析/构造 Polaris API JSON"
        exit 1
    fi

    # 检查源码目录
    if [[ ! -f "${PROVIDER_DIR}/main.go" ]]; then
        log_error "找不到 Provider 源码: ${PROVIDER_DIR}/main.go"
        exit 1
    fi
    if [[ ! -f "${CONSUMER_DIR}/main.go" ]]; then
        log_error "找不到 Consumer 源码: ${CONSUMER_DIR}/main.go"
        exit 1
    fi
    log_info "源码检查通过"

    # ==================== 步骤2: 编译 ====================
    log_step "2/9 编译 Provider 和 Consumer"

    log_info "编译 Provider..."
    (cd "$PROVIDER_DIR" && go build -o "${BUILD_DIR}/provider" .)
    log_info "Provider 编译完成 -> ${BUILD_DIR}/provider"

    log_info "编译 Consumer..."
    (cd "$CONSUMER_DIR" && go build -o "${BUILD_DIR}/consumer" .)
    log_info "Consumer 编译完成 -> ${BUILD_DIR}/consumer"

    # macOS Gatekeeper 处理
    if command -v xattr &> /dev/null; then
        xattr -c "${BUILD_DIR}/provider" 2>/dev/null || true
        xattr -c "${BUILD_DIR}/consumer" 2>/dev/null || true
        log_info "已清除二进制文件的 macOS quarantine 属性"
    fi

    # ==================== 步骤3: 规则路由检查/创建 ====================
    log_step "3/9 规则路由规则校验与自动创建"
    ensure_routing_rules

    # ==================== 步骤4: 启动 Provider-dev ====================
    log_step "4/9 启动 Provider-dev (env=dev)"
    start_provider "provider_dev" "env=dev" "$PROVIDER_DEV_PORT" "${LOG_DIR}/provider_dev.log"
    PROVIDER_DEV_PID="$_STARTED_PID"
    PROVIDER_DEV_ACTUAL_PORT="$_STARTED_PORT"

    # ==================== 步骤5: 启动 Provider-test ====================
    log_step "5/9 启动 Provider-test (env=test)"
    start_provider "provider_test" "env=test" "$PROVIDER_TEST_PORT" "${LOG_DIR}/provider_test.log"
    PROVIDER_TEST_PID="$_STARTED_PID"
    PROVIDER_TEST_ACTUAL_PORT="$_STARTED_PORT"

    # ==================== 步骤6: 启动 Provider-pre ====================
    log_step "6/9 启动 Provider-pre (env=pre)"
    start_provider "provider_pre" "env=pre" "$PROVIDER_PRE_PORT" "${LOG_DIR}/provider_pre.log"
    PROVIDER_PRE_PID="$_STARTED_PID"
    PROVIDER_PRE_ACTUAL_PORT="$_STARTED_PORT"

    # ==================== 步骤7: 启动 Provider-prod ====================
    log_step "7/9 启动 Provider-prod (env=prod)"
    start_provider "provider_prod" "env=prod" "$PROVIDER_PROD_PORT" "${LOG_DIR}/provider_prod.log"
    PROVIDER_PROD_PID="$_STARTED_PID"
    PROVIDER_PROD_ACTUAL_PORT="$_STARTED_PORT"

    # ==================== 步骤8: 启动 Consumer ====================
    log_step "8/9 启动 Consumer"

    local consumer_workdir="${BUILD_DIR}/consumer_run"
    mkdir -p "$consumer_workdir"
    generate_consumer_polaris_yaml "$consumer_workdir"

    local consumer_log="${LOG_DIR}/consumer.log"

    # 检查端口是否已被占用
    if curl -s --connect-timeout 2 "http://127.0.0.1:${CONSUMER_PORT}/echo" > /dev/null 2>&1; then
        log_warn "端口 ${CONSUMER_PORT} 已被占用，尝试终止已有进程..."
        local existing_pid
        existing_pid=$(lsof -ti :"$CONSUMER_PORT" 2>/dev/null | head -1 || echo "")
        if [[ -n "$existing_pid" ]]; then
            kill "$existing_pid" 2>/dev/null || true
            sleep 2
        fi
    fi

    local consumer_args=(
        --namespace "$NAMESPACE"
        --service "$SERVICE_NAME"
        --port "$CONSUMER_PORT"
        --token "$POLARIS_TOKEN"
    )
    if [[ -n "$SELF_SERVICE" ]]; then
        consumer_args+=(--selfService "$SELF_SERVICE" --selfNamespace "$NAMESPACE")
    fi

    (cd "$consumer_workdir" && "${BUILD_DIR}/consumer" "${consumer_args[@]}" \
        > "$consumer_log" 2>&1) &
    CONSUMER_PID=$!
    log_info "Consumer 已启动 (PID: $CONSUMER_PID)"

    sleep 1
    check_process_alive "$CONSUMER_PID" "Consumer" || {
        log_error "Consumer 启动失败，请检查日志: $consumer_log"
        cat "$consumer_log" 2>/dev/null || true
        exit 1
    }

    wait_for_http "http://127.0.0.1:${CONSUMER_PORT}/echo" 30 "Consumer" "$CONSUMER_PID" || exit 1

    # 等待服务发现缓存刷新
    log_info "等待 5s，确保服务发现缓存已刷新..."
    sleep 5

    # ==================== 步骤9: 发送请求验证路由 ====================
    log_step "9/9 发送请求验证规则路由"

    # 将 env 列表转为数组
    IFS=',' read -ra ENV_ARRAY <<< "$ROUTE_ENVS"

    local total_requests=0
    local total_success=0
    local total_fail=0
    local total_correct_route=0
    local total_wrong_route=0
    local total_error=0

    # 结果文件
    local result_file="${LOG_DIR}/rule_route_result.csv"
    echo "序号,env值,HTTP状态码,路由目标,是否正确,响应内容" > "$result_file"

    # 按 env 值分组测试
    for target_env in "${ENV_ARRAY[@]}"; do
        echo ""
        echo -e "${BLUE}── 测试 env=${target_env} (期望路由到 Provider-${target_env}) ──${NC}"
        echo ""
        printf "  ${CYAN}%-6s %-12s %-30s %-8s %s${NC}\n" "序号" "状态" "路由目标" "正确?" "响应摘要"
        printf "  %-6s %-12s %-30s %-8s %s\n" "------" "------------" "------------------------------" "--------" "--------------------------------------------"

        local env_correct=0
        local env_wrong=0
        local env_error=0

        for i in $(seq 1 "$REQUEST_COUNT"); do
            total_requests=$((total_requests + 1))

            local resp
            local http_code
            http_code=$(curl -s -o /tmp/_rule_resp_$$.tmp -w '%{http_code}' --connect-timeout 5 \
                "http://127.0.0.1:${CONSUMER_PORT}/echo?env=${target_env}" 2>/dev/null || echo "000")
            resp=$(cat /tmp/_rule_resp_$$.tmp 2>/dev/null || echo "")
            rm -f /tmp/_rule_resp_$$.tmp

            local route_target="unknown"
            local is_correct="?"
            local status_icon=""

            if [[ "$http_code" == "200" ]]; then
                total_success=$((total_success + 1))

                # 判断路由到了哪个 Provider（通过响应中的 metadata 信息）
                if echo "$resp" | grep -q "env=${target_env}"; then
                    route_target="Provider-${target_env} (env=${target_env})"
                    is_correct="✓"
                    status_icon="${GREEN}✓${NC}"
                    env_correct=$((env_correct + 1))
                    total_correct_route=$((total_correct_route + 1))
                else
                    # 尝试识别实际路由到了哪个 env
                    local actual_env=""
                    for check_env in dev test pre prod; do
                        if echo "$resp" | grep -q "env=${check_env}"; then
                            actual_env="$check_env"
                            break
                        fi
                    done
                    if [[ -n "$actual_env" ]]; then
                        route_target="Provider-${actual_env} (env=${actual_env})"
                    else
                        route_target="未知实例"
                    fi
                    is_correct="✗"
                    status_icon="${RED}✗${NC}"
                    env_wrong=$((env_wrong + 1))
                    total_wrong_route=$((total_wrong_route + 1))
                fi
            else
                total_fail=$((total_fail + 1))
                env_error=$((env_error + 1))
                total_error=$((total_error + 1))
                route_target="请求失败 (HTTP ${http_code})"
                is_correct="✗"
                status_icon="${RED}✗${NC}"
            fi

            # 截取响应摘要（最多80字符）
            local resp_summary
            resp_summary=$(echo "$resp" | head -1 | cut -c1-80)

            printf "  %-6s ${status_icon} %-10s %-30s %-8s %s\n" "$i" "HTTP $http_code" "$route_target" "$is_correct" "$resp_summary"
            echo "${total_requests},${target_env},${http_code},${route_target},${is_correct},${resp}" >> "$result_file"

            sleep 0.1
        done

        # 单个 env 小结
        echo ""
        if [[ "$env_correct" -eq "$REQUEST_COUNT" ]]; then
            echo -e "  ${GREEN}env=${target_env}: 全部正确 (${env_correct}/${REQUEST_COUNT})${NC}"
        elif [[ "$env_error" -eq "$REQUEST_COUNT" ]]; then
            echo -e "  ${RED}env=${target_env}: 全部失败 (错误: ${env_error}/${REQUEST_COUNT})${NC}"
        else
            echo -e "  ${YELLOW}env=${target_env}: 正确=${env_correct}, 错误路由=${env_wrong}, 请求失败=${env_error} (共 ${REQUEST_COUNT})${NC}"
        fi
    done

    echo ""

    # ==================== 验证结果汇总 ====================
    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║           规则路由验证结果汇总                   ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "  服务名:              ${SERVICE_NAME}"
    echo "  命名空间:            ${NAMESPACE}"
    echo "  测试 env 值:         ${ROUTE_ENVS}"
    echo "  使用规则名称:        ${EXPECTED_RULE_NAME}"
    echo ""
    echo -e "  ${CYAN}── Provider 实例 ──${NC}"
    echo "  Provider-dev:        env=dev  (端口: ${PROVIDER_DEV_ACTUAL_PORT})"
    echo "  Provider-test:       env=test (端口: ${PROVIDER_TEST_ACTUAL_PORT})"
    echo "  Provider-pre:        env=pre  (端口: ${PROVIDER_PRE_ACTUAL_PORT})"
    echo "  Provider-prod:       env=prod (端口: ${PROVIDER_PROD_ACTUAL_PORT})"
    echo ""
    echo -e "  ${CYAN}── 请求统计 ──${NC}"
    echo "  总请求数:            ${total_requests}"
    echo "  成功请求:            ${total_success}"
    echo "  失败请求:            ${total_fail}"
    echo ""
    echo -e "  ${CYAN}── 路由准确性 ──${NC}"
    echo "  路由正确:            ${total_correct_route}"
    echo "  路由错误:            ${total_wrong_route}"
    echo "  请求错误:            ${total_error}"
    echo ""
    echo "  详细结果 CSV:        ${result_file}"
    echo "  Provider-dev 日志:   ${LOG_DIR}/provider_dev.log"
    echo "  Provider-test 日志:  ${LOG_DIR}/provider_test.log"
    echo "  Provider-pre 日志:   ${LOG_DIR}/provider_pre.log"
    echo "  Provider-prod 日志:  ${LOG_DIR}/provider_prod.log"
    echo "  Consumer 日志:       ${LOG_DIR}/consumer.log"
    echo ""

    # ==================== 验证结论 ====================
    echo -e "${BLUE}── 规则路由效果验证 ──${NC}"
    echo ""

    local test_passed=true

    # 验证1: 请求是否全部成功
    if [[ "$total_success" -eq "$total_requests" ]]; then
        log_info "✅ 所有请求均成功 (${total_success}/${total_requests})"
    elif [[ "$total_success" -gt 0 ]]; then
        log_warn "⚠️  部分请求失败 (成功: ${total_success}/${total_requests})"
    else
        log_error "❌ 所有请求均失败"
        test_passed=false
    fi

    # 验证2: 路由是否全部正确
    if [[ "$total_correct_route" -eq "$total_success" ]] && [[ "$total_success" -gt 0 ]]; then
        log_info "✅ 所有成功请求均路由到正确的 Provider 实例"
        log_info "   说明: 请求参数 ?env=xxx 通过规则路由正确匹配到对应 metadata 的 Provider"
    else
        if [[ "$total_wrong_route" -gt 0 ]]; then
            log_error "❌ 有 ${total_wrong_route} 个请求路由到了错误的 Provider 实例"
            log_error "   说明: 规则路由未正确匹配，请检查北极星控制台的路由规则配置"
            test_passed=false
        fi
    fi

    echo ""
    if [[ "$test_passed" == "true" ]] && [[ "$total_success" -eq "$total_requests" ]]; then
        echo -e "${GREEN}╔══════════════════════════════════════════════════╗${NC}"
        echo -e "${GREEN}║  验证结论: ✅ 规则路由功能正常生效！              ║${NC}"
        echo -e "${GREEN}╚══════════════════════════════════════════════════╝${NC}"
        echo ""
        echo -e "${GREEN}  - 请求参数 ?env=dev  → 路由到 Provider-dev  (env=dev)${NC}"
        echo -e "${GREEN}  - 请求参数 ?env=test → 路由到 Provider-test (env=test)${NC}"
        echo -e "${GREEN}  - 请求参数 ?env=pre  → 路由到 Provider-pre  (env=pre)${NC}"
        echo -e "${GREEN}  - 请求参数 ?env=prod → 路由到 Provider-prod (env=prod)${NC}"
        echo -e "${GREEN}  - 规则路由按预期工作：请求根据路由规则调度到匹配的节点${NC}"
    elif [[ "$test_passed" == "true" ]]; then
        echo -e "${YELLOW}验证结论: ⚠️  规则路由基本正常，但部分请求失败${NC}"
        echo -e "${YELLOW}  请检查网络连接和服务状态${NC}"
    else
        echo -e "${RED}╔══════════════════════════════════════════════════╗${NC}"
        echo -e "${RED}║  验证结论: ❌ 规则路由功能验证未通过              ║${NC}"
        echo -e "${RED}╚══════════════════════════════════════════════════╝${NC}"
        echo ""
        echo -e "${RED}  请检查以下配置:${NC}"
        echo -e "${RED}  1. 北极星控制台的规则路由是否包含 env 分组（${ROUTE_ENVS}）${NC}"
        echo -e "${RED}  2. Consumer 的 polaris.yaml 是否配置了 ruleBasedRouter 路由链${NC}"
        echo -e "${RED}  3. Provider 实例是否正确注册了 env 元数据标签${NC}"
        echo -e "${RED}  4. 路由规则的匹配条件是否正确（请求参数类型 QUERY，EXACT 匹配）${NC}"
    fi

    echo ""
    echo -e "${BLUE}提示: 查看详细日志:${NC}"
    echo -e "${BLUE}  Provider-dev:  cat ${LOG_DIR}/provider_dev.log${NC}"
    echo -e "${BLUE}  Provider-test: cat ${LOG_DIR}/provider_test.log${NC}"
    echo -e "${BLUE}  Provider-pre:  cat ${LOG_DIR}/provider_pre.log${NC}"
    echo -e "${BLUE}  Provider-prod: cat ${LOG_DIR}/provider_prod.log${NC}"
    echo -e "${BLUE}  Consumer:      cat ${LOG_DIR}/consumer.log${NC}"
    echo ""
}

main "$@"
