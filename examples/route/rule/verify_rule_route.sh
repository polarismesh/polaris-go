#!/bin/bash
# =============================================================================
# 规则路由(Rule Route)功能验证脚本
#
# 使用方法:
#   chmod +x verify_rule_route.sh
#   ./verify_rule_route.sh [--polaris-server <地址>] [--polaris-token <令牌>]
#                          [--service <服务名>] [--namespace <命名空间>]
#                          [--request-count <请求次数>]
#                          [--failover-request-count <次数>]
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
#
#   然后启动 4 个 Consumer 实例，覆盖两条 SDK 调用链路与两种 failoverType:
#     - simple-consumer (GetOneInstance)        × failoverType=all
#     - consumer        (ProcessRouters + LB)   × failoverType=all
#     - simple-consumer (GetOneInstance)        × failoverType=none
#     - consumer        (ProcessRouters + LB)   × failoverType=none
#
#   验证场景：
#     场景 1 - matched env  : 通过 query 参数 ?env=dev|test|pre|prod 验证规则路由
#                              （4 条链路 × 4 个 env，应全部命中匹配的 Provider）
#     场景 2a - 失败分流 nomatch : ?env=nomatch（不在规则中的 env 值）
#     场景 2b - 失败分流 空值   : ?env=（空 query value）
#       - failoverType=all 链路: 期望 SDK 兜底返回任意 Provider（HTTP 200）
#       - failoverType=none 链路: 期望 SDK 返回空实例列表（HTTP 5xx 或逻辑失败）
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
#   2. 编译 Provider / simple-consumer / consumer
#   3. 检查/创建路由规则（通过北极星管理 API）
#   4-7. 启动 Provider-dev / Provider-test / Provider-pre / Provider-prod
#   8. 启动 4 个 Consumer (simple/full × all/none)
#   9. 场景 1 - matched env (4 链路 × 4 env)
#  10. 场景 2a - failover (?env=nomatch)
#  11. 场景 2b - failover (?env=)
#  12. 汇总 4 链路 × 3 场景结果矩阵
# =============================================================================

set -euo pipefail

# ======================== 默认配置 ========================
POLARIS_SERVER="${POLARIS_SERVER:-127.0.0.1}"
POLARIS_TOKEN="${POLARIS_TOKEN:-}"
SERVICE_NAME="${SERVICE_NAME:-RouteEchoServer}"
SELF_SERVICE="${SELF_SERVICE:-}"
CONSUMER_SERVICE="${CONSUMER_SERVICE:-RouteEchoClient}"   # 默认主调方服务名（可被 --self-service 覆盖）
NAMESPACE="${NAMESPACE:-default}"
# 4 条 Consumer 链路的端口（CONSUMER_PORT 保留为向后兼容别名，等价于 CONSUMER_ALL_PORT）
CONSUMER_PORT="${CONSUMER_PORT:-18080}"
CONSUMER_ALL_PORT="${CONSUMER_ALL_PORT:-${CONSUMER_PORT}}"            # consumer + failoverType=all
SIMPLE_CONSUMER_ALL_PORT="${SIMPLE_CONSUMER_ALL_PORT:-18081}"          # simple-consumer + failoverType=all
CONSUMER_NONE_PORT="${CONSUMER_NONE_PORT:-18082}"                      # consumer + failoverType=none
SIMPLE_CONSUMER_NONE_PORT="${SIMPLE_CONSUMER_NONE_PORT:-18083}"        # simple-consumer + failoverType=none
PROVIDER_DEV_PORT="${PROVIDER_DEV_PORT:-28081}"  # 固定端口：同一实例 ID 在 Polaris 中被重复注册时会被覆盖，避免 zombie 实例累积
PROVIDER_TEST_PORT="${PROVIDER_TEST_PORT:-28082}"
PROVIDER_PRE_PORT="${PROVIDER_PRE_PORT:-28083}"
PROVIDER_PROD_PORT="${PROVIDER_PROD_PORT:-28084}"
REQUEST_COUNT="${REQUEST_COUNT:-10}"           # 每个 env 值的验证请求次数（matched 场景）
FAILOVER_REQUEST_COUNT="${FAILOVER_REQUEST_COUNT:-10}"   # 失败分流场景每条链路的请求次数
CONSUMER_TIMES="${CONSUMER_TIMES:-1}"          # 透传 consumer/main.go 的 --times 参数
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
            SELF_SERVICE="$2"; CONSUMER_SERVICE="$2"; shift 2 ;;
        --namespace)
            NAMESPACE="$2"; shift 2 ;;
        --consumer-port)
            # 向后兼容：等价于 --consumer-all-port
            CONSUMER_PORT="$2"; CONSUMER_ALL_PORT="$2"; shift 2 ;;
        --consumer-all-port)
            CONSUMER_ALL_PORT="$2"; shift 2 ;;
        --simple-consumer-all-port)
            SIMPLE_CONSUMER_ALL_PORT="$2"; shift 2 ;;
        --consumer-none-port)
            CONSUMER_NONE_PORT="$2"; shift 2 ;;
        --simple-consumer-none-port)
            SIMPLE_CONSUMER_NONE_PORT="$2"; shift 2 ;;
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
        --failover-request-count)
            FAILOVER_REQUEST_COUNT="$2"; shift 2 ;;
        --consumer-times)
            CONSUMER_TIMES="$2"; shift 2 ;;
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
            echo "  --polaris-server <地址>            北极星服务端地址 (默认: 127.0.0.1)"
            echo "  --polaris-token <令牌>             北极星鉴权令牌 (默认: 空)"
            echo "  --service <服务名>                 Provider 服务名 (默认: RouteEchoServer)"
            echo "  --self-service <服务名>            Consumer 自身服务名 (默认: 空，等价于 RouteEchoClient)"
            echo "  --namespace <命名空间>             命名空间 (默认: default)"
            echo "  --consumer-port <端口>             [兼容] 等价于 --consumer-all-port (默认: 18080)"
            echo "  --consumer-all-port <端口>         consumer + failoverType=all 端口 (默认: 18080)"
            echo "  --simple-consumer-all-port <端口>  simple-consumer + failoverType=all 端口 (默认: 18081)"
            echo "  --consumer-none-port <端口>        consumer + failoverType=none 端口 (默认: 18082)"
            echo "  --simple-consumer-none-port <端口> simple-consumer + failoverType=none 端口 (默认: 18083)"
            echo "  --provider-dev-port <端口>         Provider-dev 端口 (默认: 28081)"
            echo "  --provider-test-port <端口>        Provider-test 端口 (默认: 28082)"
            echo "  --provider-pre-port <端口>         Provider-pre 端口 (默认: 28083)"
            echo "  --provider-prod-port <端口>        Provider-prod 端口 (默认: 28084)"
            echo "  --request-count <次数>             每个 env 值的验证请求次数 (默认: 10)"
            echo "  --failover-request-count <次数>    失败分流场景每条链路的请求次数 (默认: 10)"
            echo "  --consumer-times <次数>            consumer/main.go --times 参数透传 (默认: 1)"
            echo "  --route-envs <列表>                要测试的 env 值，逗号分隔 (默认: dev,test,pre,prod)"
            echo "  --rule-name <名称>                 期望的路由规则名称 (默认: <service>-auto-rule)"
            echo "  --skip-rule-check                  跳过服务端规则检查/创建"
            echo "  --no-auto-create                   规则不符合预期时不自动创建（仅检查）"
            echo "  --debug                            启用 debug 日志（仅作用于 simple-consumer）"
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
SIMPLE_CONSUMER_DIR="${SCRIPT_DIR}/simple-consumer"
BUILD_DIR="${SCRIPT_DIR}/.build"
LOG_DIR="${SCRIPT_DIR}/.logs"
POLARIS_HTTP_ADDR="http://${POLARIS_SERVER}:8090"

# 测试总日志文件（同时输出到标准输出和日志文件，参考 lane-test.sh）
TEST_LOG_FILE="${LOG_DIR}/verify_rule_route-$(date +%Y%m%d_%H%M%S).log"

PROVIDER_DEV_PID=""
PROVIDER_TEST_PID=""
PROVIDER_PRE_PID=""
PROVIDER_PROD_PID=""
# 4 条 Consumer 链路 PID
CONSUMER_ALL_PID=""
SIMPLE_CONSUMER_ALL_PID=""
CONSUMER_NONE_PID=""
SIMPLE_CONSUMER_NONE_PID=""
PROVIDER_DEV_ACTUAL_PORT=""
PROVIDER_TEST_ACTUAL_PORT=""
PROVIDER_PRE_ACTUAL_PORT=""
PROVIDER_PROD_ACTUAL_PORT=""

# ======================== 清理函数 ========================
cleanup() {
    log_info "清理进程..."
    for pid_var in CONSUMER_ALL_PID SIMPLE_CONSUMER_ALL_PID CONSUMER_NONE_PID SIMPLE_CONSUMER_NONE_PID \
                   PROVIDER_DEV_PID PROVIDER_TEST_PID PROVIDER_PRE_PID PROVIDER_PROD_PID; do
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

# setup_test_log 初始化测试总日志文件，并把后续所有 stdout/stderr 同时输出到终端和日志文件
# 参考 examples/route/lane/lane-test.sh 的做法
setup_test_log() {
    mkdir -p "${LOG_DIR}"
    {
        echo "===== 规则路由测试日志 $(date '+%Y-%m-%d %H:%M:%S') ====="
        echo "Command: $0 $*"
    } > "${TEST_LOG_FILE}"
    # 使用 process substitution + sed 去除 ANSI 颜色码后写入日志文件，
    # 终端仍然保留颜色输出。
    exec > >(tee >(sed -u 's/\x1b\[[0-9;]*m//g' >> "${TEST_LOG_FILE}")) 2>&1
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
    local ports=(
        "$CONSUMER_ALL_PORT" "$SIMPLE_CONSUMER_ALL_PORT"
        "$CONSUMER_NONE_PORT" "$SIMPLE_CONSUMER_NONE_PORT"
        "$PROVIDER_DEV_PORT" "$PROVIDER_TEST_PORT" "$PROVIDER_PRE_PORT" "$PROVIDER_PROD_PORT"
    )
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

# 收集已有 (source metadata key=\$query.env, value=<e> -> dest metadata env=<e>) 的分组。
# 说明: Polaris v2 的 arguments 类型在 v1 Discover 响应中会被降级为 source.metadata:
#   - type=QUERY  → key: \$query.<k>
#   - type=HEADER → key: \$header.<k>
#   - type=CUSTOM → key: <k>
# polaris-go SDK 的 RuleBasedRouter.matchSourceMetadata 直接用 SourceService.Metadata[key]
# 做匹配，QUERY 类型 Argument 通过 ToLabels 在客户端侧被写入 SourceService.Metadata
# 时也会带上 \$query. 前缀（见 pkg/model/argument.go:177）——这是 simple-consumer 与
# consumer 两类客户端都会产生的 key 形态，因此规则必须使用 type=QUERY，Discover 响应
# 中的 key 为 \$query.env，才能被两类客户端都正确匹配。
# 这里只接受 \$query.env 作为 Covered 判定；发现 CUSTOM (env) 或 \$header.env 形态时
# 记为 unfriendly，促使重新创建规则。
covered = set()
unfriendly = set()
for entry in inbounds:
    sources = entry.get('sources') or []
    dests = entry.get('destinations') or []
    for src in sources:
        md = src.get('metadata') or {}
        for k, v in md.items():
            if k == '\$query.env':
                key_kind = 'query'
            elif k == 'env' or k == '\$header.env':
                key_kind = 'incompat'
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
            if key_kind == 'query':
                covered.add(value)
            else:
                unfriendly.add(value)

missing = [e for e in envs if e not in covered]
if missing and unfriendly:
    print(f'MISSING|缺少 QUERY 类型的 env 分组: {missing}（发现 CUSTOM env 或 \$header.env 形态的旧规则，不兼容 simple-consumer 的 QUERY 上报）')
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

    # 构造 create body；每个 env 一个子规则（rules[]），源 QUERY env=<e>，目标 metadata env=<e>。
    # 说明：type=QUERY 在服务端 v1 Discover 响应中降级为 source.metadata["$query.env"]；
    # polaris-go SDK 两类客户端（simple-consumer/consumer）在把 HTTP query 参数通过
    # BuildQueryArgument 转成路由 Argument 时，也会在 SourceService.Metadata 中写入
    # "$query.env" key（见 pkg/model/argument.go:177），因此 QUERY 类型能被两类客户端
    # 都正确匹配上。老版本使用 CUSTOM 的规则对 simple-consumer 无效（后者不上报 CUSTOM）。
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
                'type': 'QUERY',
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
    local failover_type="${2:-all}"
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
        # all: 规则全部匹配失败时返回所有实例（兜底）；
        # none: 规则全部匹配失败时返回空实例列表（严格匹配）
        failoverType: ${failover_type}
EOF
    log_info "生成 Consumer polaris.yaml -> $yaml_file (failoverType: ${failover_type})"
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

# ======================== 启动 Consumer 通用函数 ========================
# 参数:
#   $1 display_name    展示名 (如 "Simple-Consumer (failoverType=all)")
#   $2 bin_name        已编译好的二进制名 ("simple_consumer" / "consumer")，位于 ${BUILD_DIR} 下
#   $3 workdir_name    工作目录子目录名（每条链路必须不同，确保 polaris.yaml 隔离）
#   $4 listen_port     HTTP 监听端口
#   $5 log_file        日志文件绝对路径
#   $6 pid_var         PID 全局变量名（eval 写回）
#   $7 failover_type   "all" / "none"，注入到生成的 polaris.yaml
#   $8 use_times_flag  "true": 使用 consumer 二进制（支持 --times，无 --debug）
#                       "false": 使用 simple-consumer 二进制（支持 --debug，无 --times）
start_consumer() {
    local display_name="$1"
    local bin_name="$2"
    local workdir_name="$3"
    local listen_port="$4"
    local log_file="$5"
    local pid_var="$6"
    local failover_type="$7"
    local use_times_flag="$8"

    local workdir="${BUILD_DIR}/${workdir_name}"
    mkdir -p "$workdir"
    generate_consumer_polaris_yaml "$workdir" "$failover_type"

    # 端口占用检查
    if curl -s --connect-timeout 2 "http://127.0.0.1:${listen_port}/echo" > /dev/null 2>&1; then
        log_warn "端口 ${listen_port} 已被占用，尝试终止已有进程..."
        local existing_pid
        existing_pid=$(lsof -ti :"$listen_port" 2>/dev/null | head -1 || echo "")
        if [[ -n "$existing_pid" ]]; then
            kill "$existing_pid" 2>/dev/null || true
            sleep 2
        fi
    fi

    # 通用 CLI 参数（两类二进制都支持）
    local args=(
        --namespace "$NAMESPACE"
        --service "$SERVICE_NAME"
        --selfNamespace "$NAMESPACE"
        --selfService "$CONSUMER_SERVICE"
        --port "$listen_port"
        --token "$POLARIS_TOKEN"
    )
    if [[ "$use_times_flag" == "true" ]]; then
        # consumer/main.go: 支持 --times，但没有 --debug（强制 DEBUG）
        args+=(--times "$CONSUMER_TIMES")
    else
        # simple-consumer/main.go: 支持 --debug，没有 --times
        args+=(--debug="$DEBUG_MODE")
    fi

    (cd "$workdir" && "${BUILD_DIR}/${bin_name}" "${args[@]}" > "$log_file" 2>&1) &
    local pid=$!
    eval "${pid_var}=${pid}"
    log_info "${display_name} 已启动 (PID: $pid, port: ${listen_port}, failoverType: ${failover_type})"

    sleep 1
    check_process_alive "$pid" "${display_name}" || {
        log_error "${display_name} 启动失败，请检查日志: $log_file"
        cat "$log_file" 2>/dev/null || true
        exit 1
    }
    wait_for_http "http://127.0.0.1:${listen_port}/echo" 30 "${display_name}" "$pid" || exit 1
}

# ======================== 验证单条链路通用函数 ========================
# 参数:
#   $1 link_name       链路展示名（用于日志/CSV）
#   $2 listen_port     Consumer HTTP 端口
#   $3 target_env      请求 ?env=<value>（"" 表示空 query value）
#   $4 request_count   请求次数
#   $5 csv_file        CSV 文件路径（追加写入；调用方提前写表头）
#   $6 expect_success  "true": 期望 HTTP 200 + 路由命中目标 env
#                       "false": 期望非 200 或逻辑失败（用于 failoverType=none 失败分流场景）
# 输出（全局变量）:
#   LINK_TOTAL / LINK_SUCCESS / LINK_FAIL
#   LINK_ROUTE_DEV / LINK_ROUTE_TEST / LINK_ROUTE_PRE / LINK_ROUTE_PROD / LINK_ROUTE_UNKNOWN
#   LINK_CORRECT       命中 target_env 的次数
verify_link() {
    local link_name="$1"
    local listen_port="$2"
    local target_env="$3"
    local request_count="$4"
    local csv_file="$5"
    local expect_success="$6"

    local label_env="$target_env"
    [[ -z "$label_env" ]] && label_env="<empty>"
    log_step "验证: ${link_name} | ?env='${label_env}' | expect_success=${expect_success}"

    LINK_TOTAL=0; LINK_SUCCESS=0; LINK_FAIL=0
    LINK_ROUTE_DEV=0; LINK_ROUTE_TEST=0; LINK_ROUTE_PRE=0; LINK_ROUTE_PROD=0; LINK_ROUTE_UNKNOWN=0
    LINK_CORRECT=0

    echo ""
    printf "  ${CYAN}%-6s %-12s %-32s %-8s %s${NC}\n" "序号" "状态" "路由目标" "命中?" "响应摘要"
    printf "  %-6s %-12s %-32s %-8s %s\n" "------" "------------" "--------------------------------" "--------" "----------------------------------------"

    local url="http://127.0.0.1:${listen_port}/echo?env=${target_env}"

    for i in $(seq 1 "$request_count"); do
        LINK_TOTAL=$((LINK_TOTAL + 1))
        local resp http_code
        http_code=$(curl -s -o /tmp/_rule_resp_$$.tmp -w '%{http_code}' --connect-timeout 5 \
            "$url" 2>/dev/null || echo "000")
        resp=$(cat /tmp/_rule_resp_$$.tmp 2>/dev/null || echo "")
        rm -f /tmp/_rule_resp_$$.tmp

        local route_target="未知" status_icon="${YELLOW}?${NC}" hit_marker="-"
        # consumer/main.go 在 SDK 失败时仍写 200 + 错误文本（rw.WriteHeader(http.StatusOK)），
        # 这里把这种「逻辑失败」识别出来计入 LINK_FAIL，否则 failoverType=none 链路在
        # consumer 上的 PASS 判定会失效。simple-consumer 在 SDK 失败时返回 503，无需特判。
        local is_logical_fail=false
        if echo "$resp" | grep -qE "fail to (processRouters|processLoadBalance|getOneInstance|getAllInstances)|no available instance"; then
            is_logical_fail=true
        fi

        if [[ "$http_code" == "200" ]] && [[ "$is_logical_fail" != "true" ]]; then
            LINK_SUCCESS=$((LINK_SUCCESS + 1))
            local matched=""
            for e in dev test pre prod; do
                if echo "$resp" | grep -q "env=${e}"; then matched="$e"; break; fi
            done
            case "$matched" in
                dev)  LINK_ROUTE_DEV=$((LINK_ROUTE_DEV + 1));   route_target="Provider-dev (env=dev)" ;;
                test) LINK_ROUTE_TEST=$((LINK_ROUTE_TEST + 1)); route_target="Provider-test (env=test)" ;;
                pre)  LINK_ROUTE_PRE=$((LINK_ROUTE_PRE + 1));   route_target="Provider-pre (env=pre)" ;;
                prod) LINK_ROUTE_PROD=$((LINK_ROUTE_PROD + 1)); route_target="Provider-prod (env=prod)" ;;
                *)    LINK_ROUTE_UNKNOWN=$((LINK_ROUTE_UNKNOWN + 1));  route_target="未知 Provider" ;;
            esac
            if [[ -n "$matched" && "$matched" == "$target_env" ]]; then
                LINK_CORRECT=$((LINK_CORRECT + 1))
                status_icon="${GREEN}✓${NC}"; hit_marker="✓"
            else
                # matched env 与请求不一致：在 failoverType=all + failover 场景下属于兜底正常
                status_icon="${YELLOW}→${NC}"; hit_marker="→"
            fi
        else
            LINK_FAIL=$((LINK_FAIL + 1))
            if [[ "$is_logical_fail" == "true" ]]; then
                route_target="逻辑失败 (HTTP 200, body=fail/no instance)"
            else
                route_target="请求失败 (HTTP ${http_code})"
            fi
            status_icon="${RED}✗${NC}"; hit_marker="✗"
        fi

        local resp_summary
        resp_summary=$(echo "$resp" | head -1 | cut -c1-80)
        printf "  %-6s ${status_icon} %-10s %-32s %-8s %s\n" "$i" "HTTP $http_code" "$route_target" "$hit_marker" "$resp_summary"
        echo "${link_name},${label_env},${i},${http_code},${route_target},${hit_marker},${resp}" >> "$csv_file"
        sleep 0.1
    done

    echo ""
}

# ======================== 单条链路结论判定 ========================
# 参数:
#   $1 link_name
#   $2 scenario      "matched" / "failover"
#   $3 target_env    场景=matched 时为 dev/test/pre/prod；场景=failover 时为标签字符串
#   $4 expect_success "true" / "false"
# 输出: LINK_RESULT="PASS" / "PARTIAL" / "FAIL"
summarize_link() {
    local link_name="$1"
    local scenario="$2"
    local target_env="$3"
    local expect_success="$4"

    echo -e "  ${CYAN}── [${link_name}] 场景=${scenario} target='${target_env}' expect_success=${expect_success} ──${NC}"
    echo "  总请求: ${LINK_TOTAL}, HTTP 200 命中: ${LINK_SUCCESS}, 失败(非200/逻辑失败): ${LINK_FAIL}"
    echo "  路由分布: dev=${LINK_ROUTE_DEV}, test=${LINK_ROUTE_TEST}, pre=${LINK_ROUTE_PRE}, prod=${LINK_ROUTE_PROD}, unknown=${LINK_ROUTE_UNKNOWN}"

    if [[ "$scenario" == "matched" ]]; then
        # 期望: 全部 200 + 全部命中 target_env
        if [[ "$LINK_SUCCESS" -eq "$LINK_TOTAL" ]] && [[ "$LINK_CORRECT" -eq "$LINK_TOTAL" ]] && [[ "$LINK_TOTAL" -gt 0 ]]; then
            log_info "✅ 路由命中正确：所有 ${LINK_TOTAL} 个请求均路由到 Provider-${target_env}"
            LINK_RESULT="PASS"
        elif [[ "$LINK_CORRECT" -gt 0 ]] && [[ "$LINK_SUCCESS" -eq "$LINK_TOTAL" ]]; then
            log_warn "⚠️ 路由部分命中：${LINK_CORRECT}/${LINK_TOTAL} 命中目标 env=${target_env}"
            LINK_RESULT="PARTIAL"
        else
            log_error "❌ 路由未通过：命中=${LINK_CORRECT}/${LINK_TOTAL}，失败=${LINK_FAIL}"
            LINK_RESULT="FAIL"
        fi
    else
        # failover 场景
        if [[ "$expect_success" == "true" ]]; then
            # failoverType=all：期望全部 200（路由到任意 Provider 都行，验证兜底语义）
            if [[ "$LINK_SUCCESS" -eq "$LINK_TOTAL" ]] && [[ "$LINK_TOTAL" -gt 0 ]]; then
                log_info "✅ failoverType=all 兜底生效：全部 ${LINK_TOTAL} 个请求成功（返回任意 Provider）"
                LINK_RESULT="PASS"
            elif [[ "$LINK_SUCCESS" -gt 0 ]]; then
                log_warn "⚠️ failoverType=all 仅 ${LINK_SUCCESS}/${LINK_TOTAL} 成功"
                LINK_RESULT="PARTIAL"
            else
                log_error "❌ failoverType=all 全部失败，未达兜底语义"
                LINK_RESULT="FAIL"
            fi
        else
            # failoverType=none：期望全部失败（无可用实例）
            if [[ "$LINK_FAIL" -eq "$LINK_TOTAL" ]] && [[ "$LINK_TOTAL" -gt 0 ]]; then
                log_info "✅ failoverType=none 严格匹配生效：全部 ${LINK_TOTAL} 个请求被拒绝（无可用实例）"
                LINK_RESULT="PASS"
            elif [[ "$LINK_FAIL" -gt 0 ]]; then
                log_warn "⚠️ failoverType=none 仅 ${LINK_FAIL}/${LINK_TOTAL} 被拒绝；其余 ${LINK_SUCCESS} 个意外返回 200"
                LINK_RESULT="PARTIAL"
            else
                log_error "❌ failoverType=none 未生效：${LINK_SUCCESS} 个请求仍然返回 200"
                LINK_RESULT="FAIL"
            fi
        fi
    fi
}

# ======================== 主流程 ========================

main() {
    # 初始化测试总日志（stdout/stderr 同时输出到终端和日志文件）
    setup_test_log "$@"

    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║   规则路由(Rule Route) + failoverType 验证脚本   ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "配置信息:"
    echo "  北极星服务端:                 ${POLARIS_SERVER}:8091 (HTTP: ${POLARIS_HTTP_ADDR})"
    echo "  服务名:                       ${SERVICE_NAME}"
    echo "  命名空间:                     ${NAMESPACE}"
    echo "  Consumer 自身服务:            ${CONSUMER_SERVICE}"
    echo "  consumer (failover=all)端口:  ${CONSUMER_ALL_PORT}"
    echo "  simple-c (failover=all)端口:  ${SIMPLE_CONSUMER_ALL_PORT}"
    echo "  consumer (failover=none)端口: ${CONSUMER_NONE_PORT}"
    echo "  simple-c (failover=none)端口: ${SIMPLE_CONSUMER_NONE_PORT}"
    echo "  测试 env 值:                  ${ROUTE_ENVS}"
    echo "  matched 场景每 env 请求次数:  ${REQUEST_COUNT}"
    echo "  failover 场景每链路请求次数:  ${FAILOVER_REQUEST_COUNT}"
    echo "  期望规则名称:                 ${EXPECTED_RULE_NAME}"
    echo "  跳过规则检查:                 ${SKIP_RULE_CHECK}"
    echo "  自动创建规则:                 ${AUTO_CREATE_RULE}"
    echo "  测试日志文件:                 ${TEST_LOG_FILE}"
    echo ""

    # ==================== 步骤1: 环境准备 ====================
    log_step "1/12 环境准备"

    mkdir -p "$BUILD_DIR" "$LOG_DIR"

    # 清理上次运行可能残留在固定端口上的僵尸进程（bind: address already in use 防御）
    cleanup_zombies_on_ports

    # 检查 Go 环境
    if ! command -v go &> /dev/null; then
        log_error "Go 未安装，请先安装 Go"
        exit 1
    fi
    log_info "Go 版本: $(go version)"

    # 检查 python3
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
        log_error "找不到 Consumer (ProcessRouters) 源码: ${CONSUMER_DIR}/main.go"
        exit 1
    fi
    if [[ ! -f "${SIMPLE_CONSUMER_DIR}/main.go" ]]; then
        log_error "找不到 Simple-Consumer (GetOneInstance) 源码: ${SIMPLE_CONSUMER_DIR}/main.go"
        exit 1
    fi
    log_info "源码检查通过"

    # ==================== 步骤2: 编译 ====================
    log_step "2/12 编译 Provider / simple-consumer / consumer"

    log_info "编译 Provider..."
    (cd "$PROVIDER_DIR" && go build -o "${BUILD_DIR}/provider" .)
    log_info "Provider 编译完成 -> ${BUILD_DIR}/provider"

    log_info "编译 Simple-Consumer (GetOneInstance)..."
    (cd "$SIMPLE_CONSUMER_DIR" && go build -o "${BUILD_DIR}/simple_consumer" .)
    log_info "Simple-Consumer 编译完成 -> ${BUILD_DIR}/simple_consumer"

    log_info "编译 Consumer (ProcessRouters)..."
    (cd "$CONSUMER_DIR" && go build -o "${BUILD_DIR}/consumer" .)
    log_info "Consumer 编译完成 -> ${BUILD_DIR}/consumer"

    # macOS Gatekeeper 处理
    if command -v xattr &> /dev/null; then
        xattr -c "${BUILD_DIR}/provider" 2>/dev/null || true
        xattr -c "${BUILD_DIR}/simple_consumer" 2>/dev/null || true
        xattr -c "${BUILD_DIR}/consumer" 2>/dev/null || true
        log_info "已清除二进制文件的 macOS quarantine 属性"
    fi

    # ==================== 步骤3: 规则路由检查/创建 ====================
    log_step "3/12 规则路由规则校验与自动创建"
    ensure_routing_rules

    # ==================== 步骤4-7: 启动 4 个 Provider ====================
    log_step "4/12 启动 Provider-dev (env=dev)"
    start_provider "provider_dev" "env=dev" "$PROVIDER_DEV_PORT" "${LOG_DIR}/provider_dev.log"
    PROVIDER_DEV_PID="$_STARTED_PID"
    PROVIDER_DEV_ACTUAL_PORT="$_STARTED_PORT"

    log_step "5/12 启动 Provider-test (env=test)"
    start_provider "provider_test" "env=test" "$PROVIDER_TEST_PORT" "${LOG_DIR}/provider_test.log"
    PROVIDER_TEST_PID="$_STARTED_PID"
    PROVIDER_TEST_ACTUAL_PORT="$_STARTED_PORT"

    log_step "6/12 启动 Provider-pre (env=pre)"
    start_provider "provider_pre" "env=pre" "$PROVIDER_PRE_PORT" "${LOG_DIR}/provider_pre.log"
    PROVIDER_PRE_PID="$_STARTED_PID"
    PROVIDER_PRE_ACTUAL_PORT="$_STARTED_PORT"

    log_step "7/12 启动 Provider-prod (env=prod)"
    start_provider "provider_prod" "env=prod" "$PROVIDER_PROD_PORT" "${LOG_DIR}/provider_prod.log"
    PROVIDER_PROD_PID="$_STARTED_PID"
    PROVIDER_PROD_ACTUAL_PORT="$_STARTED_PORT"

    # ==================== 步骤8: 启动 4 个 Consumer ====================
    log_step "8/12 启动 4 个 Consumer (simple/full × failover=all/none)"

    local simple_all_log="${LOG_DIR}/simple_consumer_all.log"
    local consumer_all_log="${LOG_DIR}/consumer_all.log"
    local simple_none_log="${LOG_DIR}/simple_consumer_none.log"
    local consumer_none_log="${LOG_DIR}/consumer_none.log"

    start_consumer "Simple-Consumer (failoverType=all)" \
        "simple_consumer" "simple_all_run" \
        "$SIMPLE_CONSUMER_ALL_PORT" "$simple_all_log" "SIMPLE_CONSUMER_ALL_PID" \
        "all" "false"

    start_consumer "Consumer (failoverType=all)" \
        "consumer" "consumer_all_run" \
        "$CONSUMER_ALL_PORT" "$consumer_all_log" "CONSUMER_ALL_PID" \
        "all" "true"

    start_consumer "Simple-Consumer (failoverType=none)" \
        "simple_consumer" "simple_none_run" \
        "$SIMPLE_CONSUMER_NONE_PORT" "$simple_none_log" "SIMPLE_CONSUMER_NONE_PID" \
        "none" "false"

    start_consumer "Consumer (failoverType=none)" \
        "consumer" "consumer_none_run" \
        "$CONSUMER_NONE_PORT" "$consumer_none_log" "CONSUMER_NONE_PID" \
        "none" "true"

    # 等待服务发现缓存刷新
    log_info "等待 5s，确保服务发现缓存已刷新..."
    sleep 5

    # ==================== 准备 4 个链路 CSV ====================
    local csv_simple_all="${LOG_DIR}/rule_route_simple_all.csv"
    local csv_consumer_all="${LOG_DIR}/rule_route_consumer_all.csv"
    local csv_simple_none="${LOG_DIR}/rule_route_simple_none.csv"
    local csv_consumer_none="${LOG_DIR}/rule_route_consumer_none.csv"
    local csv_header="链路,场景env,序号,HTTP状态码,路由目标,命中标记,响应内容"
    echo "$csv_header" > "$csv_simple_all"
    echo "$csv_header" > "$csv_consumer_all"
    echo "$csv_header" > "$csv_simple_none"
    echo "$csv_header" > "$csv_consumer_none"

    # 链路展示名 / 端口 / CSV / failoverType / expect_success(failover) 5 元组
    local LINKS=(
        "simple-consumer(GetOneInstance) failoverType=all|${SIMPLE_CONSUMER_ALL_PORT}|${csv_simple_all}|all|true"
        "consumer(ProcessRouters)        failoverType=all|${CONSUMER_ALL_PORT}|${csv_consumer_all}|all|true"
        "simple-consumer(GetOneInstance) failoverType=none|${SIMPLE_CONSUMER_NONE_PORT}|${csv_simple_none}|none|false"
        "consumer(ProcessRouters)        failoverType=none|${CONSUMER_NONE_PORT}|${csv_consumer_none}|none|false"
    )

    # 收集 12 个 (链路 × 场景) 结果，用于最终矩阵汇总
    # 行索引: 0=simple_all, 1=consumer_all, 2=simple_none, 3=consumer_none
    # 列索引: 0=matched, 1=nomatch, 2=empty
    declare -a RESULT_MATRIX
    for i in 0 1 2 3; do
        for j in 0 1 2; do
            RESULT_MATRIX[$((i*3+j))]="N/A"
        done
    done

    # ==================== 步骤9: 场景 1 - matched env ====================
    log_step "9/12 场景 1: matched env (4 链路 × ${ROUTE_ENVS})"

    IFS=',' read -ra ENV_ARRAY <<< "$ROUTE_ENVS"

    local link_idx=0
    for link in "${LINKS[@]}"; do
        IFS='|' read -r link_name listen_port csv_file failover_type _ <<< "$link"
        echo ""
        echo -e "${BLUE}── 链路 [${link_name}] : matched env 验证 ──${NC}"

        local matched_pass_count=0
        local matched_total=0
        for target_env in "${ENV_ARRAY[@]}"; do
            verify_link "$link_name" "$listen_port" "$target_env" "$REQUEST_COUNT" "$csv_file" "true"
            summarize_link "$link_name" "matched" "$target_env" "true"
            matched_total=$((matched_total + 1))
            [[ "$LINK_RESULT" == "PASS" ]] && matched_pass_count=$((matched_pass_count + 1))
        done

        # 链路在 matched 场景下的聚合结论：4 个 env 全 PASS 才算整体 PASS
        if [[ "$matched_pass_count" -eq "$matched_total" ]]; then
            RESULT_MATRIX[$((link_idx*3+0))]="PASS"
        elif [[ "$matched_pass_count" -gt 0 ]]; then
            RESULT_MATRIX[$((link_idx*3+0))]="PARTIAL (${matched_pass_count}/${matched_total})"
        else
            RESULT_MATRIX[$((link_idx*3+0))]="FAIL"
        fi
        link_idx=$((link_idx + 1))
    done

    # ==================== 步骤10: 场景 2a - failover (?env=nomatch) ====================
    log_step "10/12 场景 2a: 失败分流 (?env=nomatch)"

    link_idx=0
    for link in "${LINKS[@]}"; do
        IFS='|' read -r link_name listen_port csv_file failover_type expect_success <<< "$link"
        echo ""
        echo -e "${BLUE}── 链路 [${link_name}] : ?env=nomatch (failover) ──${NC}"
        verify_link "$link_name" "$listen_port" "nomatch" "$FAILOVER_REQUEST_COUNT" "$csv_file" "$expect_success"
        summarize_link "$link_name" "failover" "nomatch" "$expect_success"
        RESULT_MATRIX[$((link_idx*3+1))]="$LINK_RESULT"
        link_idx=$((link_idx + 1))
    done

    # ==================== 步骤11: 场景 2b - failover (?env=) ====================
    log_step "11/12 场景 2b: 失败分流 (?env= 空 query value)"

    link_idx=0
    for link in "${LINKS[@]}"; do
        IFS='|' read -r link_name listen_port csv_file failover_type expect_success <<< "$link"
        echo ""
        echo -e "${BLUE}── 链路 [${link_name}] : ?env= 空值 (failover) ──${NC}"
        verify_link "$link_name" "$listen_port" "" "$FAILOVER_REQUEST_COUNT" "$csv_file" "$expect_success"
        summarize_link "$link_name" "failover" "<empty>" "$expect_success"
        RESULT_MATRIX[$((link_idx*3+2))]="$LINK_RESULT"
        link_idx=$((link_idx + 1))
    done

    # ==================== 步骤12: 汇总结论 ====================
    log_step "12/12 汇总结论"

    echo ""
    echo -e "${BLUE}╔════════════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║         规则路由 + failoverType 验证结果（4 链路 × 3 场景）        ║${NC}"
    echo -e "${BLUE}╚════════════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "  服务名:              ${SERVICE_NAME}"
    echo "  命名空间:            ${NAMESPACE}"
    echo "  Consumer 自身服务:   ${CONSUMER_SERVICE}"
    echo "  测试 env 值:         ${ROUTE_ENVS}"
    echo "  使用规则名称:        ${EXPECTED_RULE_NAME}"
    echo ""
    echo -e "  ${CYAN}── Provider 实例 ──${NC}"
    echo "  Provider-dev:        env=dev  (端口: ${PROVIDER_DEV_ACTUAL_PORT})"
    echo "  Provider-test:       env=test (端口: ${PROVIDER_TEST_ACTUAL_PORT})"
    echo "  Provider-pre:        env=pre  (端口: ${PROVIDER_PRE_ACTUAL_PORT})"
    echo "  Provider-prod:       env=prod (端口: ${PROVIDER_PROD_ACTUAL_PORT})"
    echo ""
    echo -e "  ${CYAN}── 链路结果矩阵 ──${NC}"
    printf "  %-50s | %-12s | %-15s | %-15s\n" "链路" "matched env" "?env=nomatch" "?env= (空值)"
    printf "  %-50s-+-%-12s-+-%-15s-+-%-15s\n" "--------------------------------------------------" "------------" "---------------" "---------------"
    local i=0
    for link in "${LINKS[@]}"; do
        IFS='|' read -r link_name _ _ _ _ <<< "$link"
        printf "  %-50s | %-12s | %-15s | %-15s\n" \
            "$link_name" \
            "${RESULT_MATRIX[$((i*3+0))]}" \
            "${RESULT_MATRIX[$((i*3+1))]}" \
            "${RESULT_MATRIX[$((i*3+2))]}"
        i=$((i + 1))
    done
    echo ""
    echo "  注: 对于 failoverType=none 链路，?env=nomatch / ?env= 场景下 PASS 表示请求被正确拒绝（无可用实例）。"
    echo ""
    echo "  详细结果 CSV:"
    echo "    simple-consumer (failover=all):  ${csv_simple_all}"
    echo "    consumer        (failover=all):  ${csv_consumer_all}"
    echo "    simple-consumer (failover=none): ${csv_simple_none}"
    echo "    consumer        (failover=none): ${csv_consumer_none}"
    echo ""

    # ==================== 最终结论聚合 ====================
    local final_pass=0
    local final_partial=0
    local final_fail=0
    for r in "${RESULT_MATRIX[@]}"; do
        case "$r" in
            PASS) final_pass=$((final_pass + 1)) ;;
            PARTIAL*) final_partial=$((final_partial + 1)) ;;
            FAIL) final_fail=$((final_fail + 1)) ;;
        esac
    done
    local total_cells=${#RESULT_MATRIX[@]}

    echo -e "${BLUE}── 最终结论 ──${NC}"
    echo ""
    echo "  PASS=${final_pass}, PARTIAL=${final_partial}, FAIL=${final_fail}, 共 ${total_cells} 格"
    echo ""

    if [[ "$final_fail" -eq 0 ]] && [[ "$final_partial" -eq 0 ]] && [[ "$final_pass" -eq "$total_cells" ]]; then
        echo -e "${GREEN}╔════════════════════════════════════════════════════════════════════╗${NC}"
        echo -e "${GREEN}║  验证结论: ✅ 规则路由 + failoverType 行为全部符合预期             ║${NC}"
        echo -e "${GREEN}╚════════════════════════════════════════════════════════════════════╝${NC}"
        echo ""
        echo -e "${GREEN}  ✓ 4 条链路在 matched env 场景下全部精确路由${NC}"
        echo -e "${GREEN}  ✓ failoverType=all 链路在失败分流场景下兜底返回任意 Provider${NC}"
        echo -e "${GREEN}  ✓ failoverType=none 链路在失败分流场景下严格拒绝请求${NC}"
    elif [[ "$final_fail" -gt 0 ]]; then
        echo -e "${RED}╔════════════════════════════════════════════════════════════════════╗${NC}"
        echo -e "${RED}║  验证结论: ❌ 至少 ${final_fail} 个 (链路×场景) 验证未通过                  ║${NC}"
        echo -e "${RED}╚════════════════════════════════════════════════════════════════════╝${NC}"
        echo ""
        echo -e "${RED}  请检查以下配置:${NC}"
        echo -e "${RED}  1. 北极星控制台的规则路由是否包含 env 分组（${ROUTE_ENVS}）${NC}"
        echo -e "${RED}  2. Consumer 的 polaris.yaml 是否配置了 ruleBasedRouter 路由链${NC}"
        echo -e "${RED}  3. Provider 实例是否正确注册了 env 元数据标签${NC}"
        echo -e "${RED}  4. 路由规则的匹配条件是否正确（请求参数类型 CUSTOM，EXACT 匹配）${NC}"
        echo -e "${RED}  5. failoverType=none 链路：SDK 是否正确返回空实例列表（plugin/servicerouter/rulebase/rule.go:192）${NC}"
    else
        echo -e "${YELLOW}╔════════════════════════════════════════════════════════════════════╗${NC}"
        echo -e "${YELLOW}║  验证结论: ⚠️  ${final_partial} 个 (链路×场景) 部分通过，请检查详细日志            ║${NC}"
        echo -e "${YELLOW}╚════════════════════════════════════════════════════════════════════╝${NC}"
    fi

    echo ""
    echo -e "${BLUE}提示: 查看详细日志:${NC}"
    echo -e "${BLUE}  测试总日志:                    cat ${TEST_LOG_FILE}${NC}"
    echo -e "${BLUE}  Provider-dev:                  cat ${LOG_DIR}/provider_dev.log${NC}"
    echo -e "${BLUE}  Provider-test:                 cat ${LOG_DIR}/provider_test.log${NC}"
    echo -e "${BLUE}  Provider-pre:                  cat ${LOG_DIR}/provider_pre.log${NC}"
    echo -e "${BLUE}  Provider-prod:                 cat ${LOG_DIR}/provider_prod.log${NC}"
    echo -e "${BLUE}  Simple-Consumer (failover=all): cat ${simple_all_log}${NC}"
    echo -e "${BLUE}  Consumer        (failover=all): cat ${consumer_all_log}${NC}"
    echo -e "${BLUE}  Simple-Consumer (failover=none):cat ${simple_none_log}${NC}"
    echo -e "${BLUE}  Consumer        (failover=none):cat ${consumer_none_log}${NC}"
    echo ""
}

main "$@"
