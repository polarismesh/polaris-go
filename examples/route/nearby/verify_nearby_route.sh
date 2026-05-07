#!/bin/bash
# =============================================================================
# 就近路由(Nearby Route)功能验证脚本
#
# 使用方法:
#   chmod +x verify_nearby_route.sh
#   ./verify_nearby_route.sh [--polaris-server <地址>] [--polaris-token <令牌>]
#                            [--service <服务名>] [--namespace <命名空间>]
#                            [--request-count <请求次数>]
#
# 前置条件:
#   1. 北极星服务端(Polaris Server)已启动
#   2. Go 环境已安装
#
#   本脚本会自动在北极星服务端检查/创建目标服务的就近路由规则。
#
# 验证原理:
#   就近路由根据实例的地域信息（Region/Zone/Campus）实现按地域就近路由。
#   当 Consumer 和 Provider 在同一 Campus 时，请求优先路由到同 Campus 的实例。
#
#   本脚本启动 3 个 Provider 实例，分别位于不同的 Campus：
#     - provider-1: Region=china, Zone=ap-guangzhou, Campus=ap-guangzhou-1
#     - provider-2: Region=china, Zone=ap-guangzhou, Campus=ap-guangzhou-2
#     - provider-3: Region=china, Zone=ap-guangzhou, Campus=ap-guangzhou-3
#   然后启动一个 Consumer，位于 Campus=ap-guangzhou-1。
#
#   验证：Consumer 发出的所有请求应该只路由到 provider-1（同 Campus），
#   不会路由到 provider-2 或 provider-3。
#
# 预期的就近路由规则（本脚本自动维护）:
#   规则名称: RouteNearbyEchoServer-auto-nearby
#   作用服务: RouteNearbyEchoServer (default 命名空间)
#   routing_policy: NearbyPolicy
#   match_level: CAMPUS (可通过 --rule-match-level 覆盖)
#   max_match_level: UNKNOWN (默认不限制)
#   strict_nearby: false
#   enable: true
#
# 验证流程:
#   1. 环境准备
#   2. 编译 Provider 和 Consumer
#   3. 检查/创建就近路由规则（通过北极星管理 API）
#   4. 启动 Provider-1 / Provider-2 / Provider-3
#   5. 启动 Consumer
#   6. 发送多次请求，验证路由结果
#   7. 汇总验证结论
# =============================================================================

set -euo pipefail

# ======================== 默认配置 ========================
POLARIS_SERVER="${POLARIS_SERVER:-127.0.0.1}"
POLARIS_TOKEN="${POLARIS_TOKEN:-}"
SERVICE_NAME="${SERVICE_NAME:-RouteNearbyEchoServer}"
CONSUMER_SERVICE="${CONSUMER_SERVICE:-RouteNearbyEchoClient}"
NAMESPACE="${NAMESPACE:-default}"
# 两条验证链路的 Consumer HTTP 端口:
#   - simple-consumer (GetOneInstance): SIMPLE_CONSUMER_PORT
#   - consumer        (ProcessRouters): CONSUMER_PORT
SIMPLE_CONSUMER_PORT="${SIMPLE_CONSUMER_PORT:-18080}"
CONSUMER_PORT="${CONSUMER_PORT:-18081}"
PROVIDER_1_PORT="${PROVIDER_1_PORT:-28091}"  # 固定端口：避免随机端口每次生成新实例 ID 导致 zombie 实例累积
PROVIDER_2_PORT="${PROVIDER_2_PORT:-28092}"
PROVIDER_3_PORT="${PROVIDER_3_PORT:-28093}"
REQUEST_COUNT="${REQUEST_COUNT:-20}"      # 验证请求次数
DEBUG_MODE="${DEBUG_MODE:-false}"

# 地域信息配置
REGION="${REGION:-china}"
ZONE="${ZONE:-ap-guangzhou}"
CONSUMER_CAMPUS="${CONSUMER_CAMPUS:-ap-guangzhou-1}"
PROVIDER_1_CAMPUS="${PROVIDER_1_CAMPUS:-ap-guangzhou-1}"
PROVIDER_2_CAMPUS="${PROVIDER_2_CAMPUS:-ap-guangzhou-2}"
PROVIDER_3_CAMPUS="${PROVIDER_3_CAMPUS:-ap-guangzhou-3}"

# 就近路由匹配级别
# SDK 侧配置（consumer polaris.yaml 的 nearbyBasedRouter.matchLevel）
MATCH_LEVEL="${MATCH_LEVEL:-campus}"
# 服务端就近路由规则的 match_level（CAMPUS/ZONE/REGION/ALL/UNKNOWN）
RULE_MATCH_LEVEL="${RULE_MATCH_LEVEL:-CAMPUS}"
RULE_MAX_MATCH_LEVEL="${RULE_MAX_MATCH_LEVEL:-UNKNOWN}"
RULE_STRICT_NEARBY="${RULE_STRICT_NEARBY:-false}"

# 规则校验/创建开关
SKIP_RULE_CHECK="${SKIP_RULE_CHECK:-false}"
AUTO_CREATE_RULE="${AUTO_CREATE_RULE:-true}"
EXPECTED_RULE_NAME="${EXPECTED_RULE_NAME:-${SERVICE_NAME}-auto-nearby}"

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
        --namespace)
            NAMESPACE="$2"; shift 2 ;;
        --consumer-port)
            CONSUMER_PORT="$2"; shift 2 ;;
        --simple-consumer-port)
            SIMPLE_CONSUMER_PORT="$2"; shift 2 ;;
        --provider-1-port)
            PROVIDER_1_PORT="$2"; shift 2 ;;
        --provider-2-port)
            PROVIDER_2_PORT="$2"; shift 2 ;;
        --provider-3-port)
            PROVIDER_3_PORT="$2"; shift 2 ;;
        --request-count)
            REQUEST_COUNT="$2"; shift 2 ;;
        --region)
            REGION="$2"; shift 2 ;;
        --zone)
            ZONE="$2"; shift 2 ;;
        --consumer-campus)
            CONSUMER_CAMPUS="$2"; shift 2 ;;
        --match-level)
            MATCH_LEVEL="$2"; shift 2 ;;
        --rule-match-level)
            RULE_MATCH_LEVEL="$2"; shift 2 ;;
        --rule-max-match-level)
            RULE_MAX_MATCH_LEVEL="$2"; shift 2 ;;
        --rule-strict-nearby)
            RULE_STRICT_NEARBY="$2"; shift 2 ;;
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
            echo "  --service <服务名>              Provider 服务名 (默认: RouteNearbyEchoServer)"
            echo "  --namespace <命名空间>          命名空间 (默认: default)"
            echo "  --consumer-port <端口>          Consumer(ProcessRouters) HTTP端口 (默认: 18081)"
            echo "  --simple-consumer-port <端口>   Simple-Consumer(GetOneInstance) HTTP端口 (默认: 18080)"
            echo "  --provider-1-port <端口>        Provider-1 端口 (默认: 自动分配)"
            echo "  --provider-2-port <端口>        Provider-2 端口 (默认: 自动分配)"
            echo "  --provider-3-port <端口>        Provider-3 端口 (默认: 自动分配)"
            echo "  --request-count <次数>          验证请求次数 (默认: 20)"
            echo "  --region <大区>                 地域大区 (默认: china)"
            echo "  --zone <区域>                   地域区域 (默认: ap-guangzhou)"
            echo "  --consumer-campus <园区>        Consumer 所在园区 (默认: ap-guangzhou-1)"
            echo "  --match-level <级别>            SDK 就近路由最小匹配级别: region/zone/campus (默认: campus)"
            echo "  --rule-match-level <级别>       服务端规则 match_level (默认: CAMPUS)"
            echo "  --rule-max-match-level <级别>   服务端规则 max_match_level (默认: UNKNOWN)"
            echo "  --rule-strict-nearby <true|false> 服务端规则 strict_nearby (默认: false)"
            echo "  --rule-name <名称>              期望的规则名称 (默认: <service>-auto-nearby)"
            echo "  --skip-rule-check               跳过服务端规则检查/创建"
            echo "  --no-auto-create                规则不符合预期时不自动创建"
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
SIMPLE_CONSUMER_DIR="${SCRIPT_DIR}/simple-consumer"
BUILD_DIR="${SCRIPT_DIR}/.build"
LOG_DIR="${SCRIPT_DIR}/.logs"
POLARIS_HTTP_ADDR="http://${POLARIS_SERVER}:8090"

# 测试总日志文件（同时输出到标准输出和日志文件，参考 lane-test.sh）
TEST_LOG_FILE="${LOG_DIR}/verify_nearby_route-$(date +%Y%m%d_%H%M%S).log"

PROVIDER_1_PID=""
PROVIDER_2_PID=""
PROVIDER_3_PID=""
CONSUMER_PID=""
SIMPLE_CONSUMER_PID=""
PROVIDER_1_ACTUAL_PORT=""
PROVIDER_2_ACTUAL_PORT=""
PROVIDER_3_ACTUAL_PORT=""

# ======================== 清理函数 ========================
cleanup() {
    log_info "清理进程..."
    for pid_var in CONSUMER_PID SIMPLE_CONSUMER_PID PROVIDER_1_PID PROVIDER_2_PID PROVIDER_3_PID; do
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
        echo "===== 就近路由测试日志 $(date '+%Y-%m-%d %H:%M:%S') ====="
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

# ======================== 就近路由规则校验与自动创建 ========================
#
# 设计说明:
#   就近路由规则在北极星商业版中属于 v2 routings 的一种（routing_policy=NearbyPolicy）。
#   Discover 查询使用 type=NEARBY_ROUTE_RULE，服务端返回 nearbyRouteRules 列表。
#   创建使用 /naming/v2/routings POST，body 与普通路由规则一致，routing_config
#   使用 NearbyRoutingConfig。
#
# Discover 查询接口（读）:
#   POST http://${POLARIS_HTTP_ADDR}/v1/Discover
#   Body: {"service":{"name":"<SVC>","namespace":"<NS>"},"type":"NEARBY_ROUTE_RULE"}
#
# v2 创建接口（写）:
#   POST http://${POLARIS_HTTP_ADDR}/naming/v2/routings
#   Body: [{"routing_policy":"NearbyPolicy",
#          "routing_config":{"@type":"type.googleapis.com/v1.NearbyRoutingConfig",...}}]
#   注：顶层 RouteRule 不带 @type；仅 routing_config (google.protobuf.Any) 需要 @type。

# fetch_nearby_rules 读取目标服务当前的就近路由规则。
fetch_nearby_rules() {
    curl -s --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/v1/Discover" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "{\"service\":{\"name\":\"${SERVICE_NAME}\",\"namespace\":\"${NAMESPACE}\"},\"type\":\"NEARBY_ROUTE_RULE\"}" \
        2>/dev/null || true
}

# query_nearby_rule_ids_by_name 从 Discover NEARBY_ROUTE_RULE 响应中提取指定名称规则的 id 列表。
# 说明: naming/v2/routings?name=... 不支持 NearbyPolicy 的按名称筛选（返回空），
# 这里复用 Discover 接口作为去重依据。
query_nearby_rule_ids_by_name() {
    local rule_name="$1"
    fetch_nearby_rules | RULE_NAME="$rule_name" python3 -c "
import os, sys, json
target = os.environ['RULE_NAME']
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
for r in (data.get('nearbyRouteRules') or []):
    if r.get('name') == target:
        print(r.get('id', ''))
" 2>/dev/null || true
}

# validate_nearby_rules 校验是否已有启用状态的就近路由规则。
# 只要存在一条 enable=true 的规则即视为通过（match_level 等可由用户配置）。
# 返回 0: 通过; 1: 不满足
validate_nearby_rules() {
    local response
    response=$(fetch_nearby_rules)

    if [[ -z "$response" ]]; then
        log_error "查询就近路由规则失败，无法连接 Polaris 服务端: ${POLARIS_HTTP_ADDR}"
        return 1
    fi

    local result
    result=$(SERVICE_NAME="$SERVICE_NAME" NAMESPACE="$NAMESPACE" \
        RULE_MATCH_LEVEL="$RULE_MATCH_LEVEL" \
        python3 -c "
import os, sys, json

svc = os.environ['SERVICE_NAME']
ns  = os.environ['NAMESPACE']
want_level = os.environ['RULE_MATCH_LEVEL'].upper()

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

rules = data.get('nearbyRouteRules') or []
enabled = []
for r in rules:
    if not r.get('enable', False):
        continue
    cfg = r.get('routing_config') or {}
    r_svc = cfg.get('service', '')
    r_ns  = cfg.get('namespace', '')
    # 匹配: service 支持 * 或 精确; namespace 必须匹配
    if r_svc not in ('*', svc):
        continue
    if r_ns not in ('', '*', ns):
        continue
    enabled.append({
        'name': r.get('name', ''),
        'match_level': cfg.get('match_level', 'UNKNOWN'),
        'max_match_level': cfg.get('max_match_level', 'UNKNOWN'),
        'strict_nearby': cfg.get('strict_nearby', False),
    })

if not enabled:
    print('MISSING|未找到启用的就近路由规则')
    sys.exit(0)

# 打印已有规则摘要
for e in enabled:
    print(f\"RULE|{e['name']}|{e['match_level']}|{e['max_match_level']}|{e['strict_nearby']}\")
print(f'OK|共 {len(enabled)} 条启用的就近路由规则')
" <<< "$response")

    local has_ok=false
    local has_missing=false
    while IFS='|' read -r kind payload rest; do
        case "$kind" in
            OK)
                log_info "就近路由规则校验通过: ${payload}"
                has_ok=true
                ;;
            MISSING)
                log_warn "就近路由规则校验未通过: ${payload}"
                has_missing=true
                ;;
            ERROR)
                log_error "就近路由规则校验失败: ${payload}"
                has_missing=true
                ;;
            RULE)
                # payload=name, rest="match_level|max_match_level|strict_nearby"
                IFS='|' read -r ml mmml strict <<< "$rest"
                log_info "  规则 ${payload}: match_level=${ml}, max_match_level=${mmml}, strict_nearby=${strict}"
                ;;
        esac
    done <<< "$result"

    if $has_ok && ! $has_missing; then
        return 0
    fi
    return 1
}

# create_nearby_rule 创建就近路由规则；已有同名规则时尝试删除再创建（删除失败容忍）
create_nearby_rule() {
    log_info "尝试在北极星创建就近路由规则 [${EXPECTED_RULE_NAME}]..."

    # 先查同名规则（通过 Discover，兼容 NEARBY_ROUTE_RULE 读路径）
    local existing_ids
    existing_ids=$(query_nearby_rule_ids_by_name "${EXPECTED_RULE_NAME}")

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
            del_http=$(curl -s -o /tmp/_nearby_del_$$.tmp -w '%{http_code}' \
                --connect-timeout 5 --max-time 10 \
                --request POST "${POLARIS_HTTP_ADDR}/naming/v2/routings/delete" \
                --header "X-Polaris-Token:${POLARIS_TOKEN}" \
                --header 'Content-Type: application/json' \
                --data-raw "${del_body}" 2>/dev/null || echo "000")
            del_resp=$(cat /tmp/_nearby_del_$$.tmp 2>/dev/null || echo "")
            rm -f /tmp/_nearby_del_$$.tmp
            del_code=$(echo "$del_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('code',0))" 2>/dev/null || echo "?")
            if [[ "$del_code" =~ ^20000[01]$ ]]; then
                log_info "旧规则已删除"
            else
                # 服务端开启鉴权时删除可能失败（403002），此时继续创建一条新规则即可。
                # Polaris 允许同名规则共存，新建的规则会与旧规则并存（并不会相互覆盖）。
                log_warn "删除旧规则失败 (HTTP=${del_http}, code=${del_code})；将直接新增一条规则，同名规则可能共存"
            fi
        fi
    fi

    # 构造 create body
    local create_body
    create_body=$(SERVICE_NAME="$SERVICE_NAME" NAMESPACE="$NAMESPACE" \
        EXPECTED_RULE_NAME="$EXPECTED_RULE_NAME" \
        RULE_MATCH_LEVEL="$RULE_MATCH_LEVEL" \
        RULE_MAX_MATCH_LEVEL="$RULE_MAX_MATCH_LEVEL" \
        RULE_STRICT_NEARBY="$RULE_STRICT_NEARBY" \
        python3 -c "
import os, json

svc = os.environ['SERVICE_NAME']
ns  = os.environ['NAMESPACE']
name = os.environ['EXPECTED_RULE_NAME']
match_level = os.environ['RULE_MATCH_LEVEL'].upper()
max_match_level = os.environ['RULE_MAX_MATCH_LEVEL'].upper()
strict = os.environ['RULE_STRICT_NEARBY'].lower() == 'true'

payload = [{
    'name': name,
    'namespace': ns,
    'enable': True,
    'routing_policy': 'NearbyPolicy',
    'routing_config': {
        '@type': 'type.googleapis.com/v1.NearbyRoutingConfig',
        'service': svc,
        'namespace': ns,
        'match_level': match_level,
        'max_match_level': max_match_level,
        'strict_nearby': strict,
    },
    'priority': 0,
    'description': 'auto-created by verify_nearby_route.sh',
}]
print(json.dumps(payload))
" 2>/dev/null)

    if [[ -z "${create_body}" ]]; then
        log_error "构造就近路由规则 body 失败"
        return 1
    fi

    local resp http_code
    http_code=$(curl -s -o /tmp/_nearby_create_resp_$$.tmp -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/naming/v2/routings" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "${create_body}" 2>/dev/null || echo "000")
    resp=$(cat /tmp/_nearby_create_resp_$$.tmp 2>/dev/null || echo "")
    rm -f /tmp/_nearby_create_resp_$$.tmp

    local code info
    code=$(echo "$resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('code',0))" 2>/dev/null || echo "?")
    info=$(echo "$resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('info',''))" 2>/dev/null || echo "?")

    if [[ "$http_code" == "200" ]] && [[ "$code" =~ ^20000[01]$ ]]; then
        log_info "✅ 就近路由规则创建成功 (code=${code})"
        enable_nearby_rule || true
        return 0
    fi

    log_error "就近路由规则创建失败 (HTTP=${http_code}, code=${code}, info=${info})"
    log_error "响应: ${resp:0:500}"
    return 1
}

# enable_nearby_rule 确保规则是启用状态（兜底）
enable_nearby_rule() {
    local existing_ids
    existing_ids=$(query_nearby_rule_ids_by_name "${EXPECTED_RULE_NAME}")
    local enable_body
    enable_body=$(echo "${existing_ids}" | EXPECTED_RULE_NAME="$EXPECTED_RULE_NAME" python3 -c "
import os, sys, json
name = os.environ['EXPECTED_RULE_NAME']
ids = [l.strip() for l in sys.stdin.read().splitlines() if l.strip()]
out = [{'id': i, 'name': name, 'enable': True} for i in ids]
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

# ensure_nearby_rules 规则不存在/未启用时自动创建
ensure_nearby_rules() {
    log_step "检查/创建就近路由规则"
    if [[ "$SKIP_RULE_CHECK" == "true" ]]; then
        log_warn "已通过 --skip-rule-check 跳过规则检查"
        return 0
    fi

    if validate_nearby_rules; then
        log_info "就近路由规则已符合预期，无需创建"
        return 0
    fi

    if [[ "$AUTO_CREATE_RULE" != "true" ]]; then
        log_error "就近路由规则不符合预期，且已通过 --no-auto-create 禁用自动创建"
        log_error "请在北极星控制台手动为 ${NAMESPACE}/${SERVICE_NAME} 开启就近路由后重试"
        exit 1
    fi

    if ! create_nearby_rule; then
        log_error "自动创建就近路由规则失败"
        exit 1
    fi

    log_info "等待 6s 让 Polaris 服务端传播规则变更..."
    sleep 6

    if ! validate_nearby_rules; then
        log_error "就近路由规则创建后仍未通过校验，请检查北极星日志"
        exit 1
    fi
    return 0
}

# ======================== 生成 polaris.yaml ========================
generate_provider_polaris_yaml() {
    local target_dir="$1"
    local campus="$2"
    local yaml_file="${target_dir}/polaris.yaml"

    cat > "$yaml_file" <<EOF
global:
  serverConnector:
    addresses:
      - ${POLARIS_SERVER}:8091
    token: ${POLARIS_TOKEN}
    # 默认 connectTimeout=500ms / messageTimeout=1s 对外网北极星偏小：
    # 首次 TCP 握手/服务发现容易 context deadline exceeded，
    # 参考 .logs/consumer.log 里 20 条 Polaris-1004 的现象。
    connectTimeout: 3s
    messageTimeout: 3s
  # 地址提供插件，用于获取当前SDK所在的地域信息
  location:
    providers:
      - type: local
        options:
          region: ${REGION}
          zone: ${ZONE}
          campus: ${campus}
EOF
    log_info "生成 Provider polaris.yaml -> $yaml_file (campus: ${campus})"
}

generate_consumer_polaris_yaml() {
    local target_dir="$1"
    local campus="$2"
    local yaml_file="${target_dir}/polaris.yaml"

    cat > "$yaml_file" <<EOF
global:
  serverConnector:
    addresses:
      - ${POLARIS_SERVER}:8091
    # 同 generate_provider_polaris_yaml：放宽 serverConnector 默认超时，
    # 避免 consumer 在 GetAllInstances 首包冷启动阶段因 500ms 被击穿。
    connectTimeout: 3s
    messageTimeout: 3s
  # 地址提供插件，用于获取当前SDK所在的地域信息
  location:
    providers:
      - type: local
        options:
          region: ${REGION}
          zone: ${ZONE}
          campus: ${campus}
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
    # 服务路由链
    chain:
      # 自定义路由策略
      - ruleBasedRouter
      # 就近路由策略
      - nearbyBasedRouter
    # 服务路由插件的配置
    plugin:
      nearbyBasedRouter:
        # 就近路由的最小匹配级别
        matchLevel: ${MATCH_LEVEL}
EOF
    log_info "生成 Consumer polaris.yaml -> $yaml_file (campus: ${campus}, matchLevel: ${MATCH_LEVEL})"
}

# ======================== 启动 Provider 通用函数 ========================
start_provider() {
    local provider_name="$1"
    local campus="$2"
    local port_var="$3"
    local pid_var="$4"
    local actual_port_var="$5"

    local provider_workdir="${BUILD_DIR}/${provider_name}"
    mkdir -p "$provider_workdir"
    generate_provider_polaris_yaml "$provider_workdir" "$campus"

    local provider_log="${LOG_DIR}/${provider_name}.log"

    # 设置地域环境变量并启动
    (cd "$provider_workdir" && \
        REGION="${REGION}" ZONE="${ZONE}" CAMPUS="${campus}" \
        "${BUILD_DIR}/provider" \
        --namespace "$NAMESPACE" \
        --service "$SERVICE_NAME" \
        --token "$POLARIS_TOKEN" \
        --port "${!port_var}" \
        > "$provider_log" 2>&1) &
    eval "${pid_var}=$!"
    local pid="${!pid_var}"
    log_info "${provider_name} 已启动 (PID: $pid, campus: ${campus})"

    sleep 1
    check_process_alive "$pid" "${provider_name}" || {
        log_error "${provider_name} 启动失败，请检查日志: $provider_log"
        cat "$provider_log" 2>/dev/null || true
        exit 1
    }

    # 获取实际端口
    local actual_port
    if [[ "${!port_var}" != "0" ]]; then
        actual_port="${!port_var}"
    else
        actual_port=$(extract_port_from_log "$provider_log" 20) || {
            log_error "无法获取 ${provider_name} 端口，请检查日志: $provider_log"
            cat "$provider_log" 2>/dev/null || true
            exit 1
        }
    fi
    eval "${actual_port_var}=${actual_port}"
    log_info "${provider_name} 实际监听端口: ${actual_port}"

    wait_for_http "http://127.0.0.1:${actual_port}/echo" 20 "${provider_name}" "$pid" || exit 1
}

# ======================== 启动 Consumer 通用函数 ========================
# 参数:
#   $1 display_name    展示名称, 如 "Simple-Consumer" / "Consumer"
#   $2 bin_name        已编译好的二进制名 (位于 ${BUILD_DIR} 下), 如 "simple_consumer" / "consumer"
#   $3 workdir_name    工作目录子目录名, 如 "simple_consumer_run" / "consumer_run"
#   $4 port            HTTP 监听端口
#   $5 log_file        日志文件绝对路径
#   $6 pid_var         PID 全局变量名 (eval 赋值)
start_consumer() {
    local display_name="$1"
    local bin_name="$2"
    local workdir_name="$3"
    local listen_port="$4"
    local log_file="$5"
    local pid_var="$6"

    local workdir="${BUILD_DIR}/${workdir_name}"
    mkdir -p "$workdir"
    generate_consumer_polaris_yaml "$workdir" "$CONSUMER_CAMPUS"

    # 检查端口是否已被占用
    if curl -s --connect-timeout 2 "http://127.0.0.1:${listen_port}/echo" > /dev/null 2>&1; then
        log_warn "端口 ${listen_port} 已被占用，尝试终止已有进程..."
        local existing_pid
        existing_pid=$(lsof -ti :"$listen_port" 2>/dev/null | head -1 || echo "")
        if [[ -n "$existing_pid" ]]; then
            kill "$existing_pid" 2>/dev/null || true
            sleep 2
        fi
    fi

    (cd "$workdir" && \
        REGION="${REGION}" ZONE="${ZONE}" CAMPUS="${CONSUMER_CAMPUS}" \
        "${BUILD_DIR}/${bin_name}" \
        --namespace "$NAMESPACE" \
        --service "$SERVICE_NAME" \
        --selfNamespace "$NAMESPACE" \
        --selfService "$CONSUMER_SERVICE" \
        --port "$listen_port" \
        --token "$POLARIS_TOKEN" \
        --debug=$DEBUG_MODE \
        > "$log_file" 2>&1) &
    local pid=$!
    eval "${pid_var}=${pid}"
    log_info "${display_name} 已启动 (PID: $pid, port: ${listen_port}, campus: ${CONSUMER_CAMPUS})"

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
#   $1 link_name      链路展示名, 如 "simple-consumer → provider"
#   $2 listen_port    Consumer HTTP 端口
#   $3 csv_file       结果 CSV 文件路径
# 输出(通过 eval 写回的全局变量):
#   LINK_TOTAL / LINK_SUCCESS / LINK_FAIL
#   LINK_ROUTE_1 / LINK_ROUTE_2 / LINK_ROUTE_3 / LINK_ROUTE_UNKNOWN
verify_link() {
    local link_name="$1"
    local listen_port="$2"
    local csv_file="$3"

    log_step "验证链路: ${link_name} (port=${listen_port})"

    local total_requests=0
    local success_requests=0
    local fail_requests=0
    local route_to_1=0
    local route_to_2=0
    local route_to_3=0
    local route_to_unknown=0

    echo "序号,HTTP状态码,路由目标,响应内容" > "$csv_file"

    echo ""
    printf "  ${CYAN}%-6s %-12s %-30s %s${NC}\n" "序号" "状态" "路由目标" "响应摘要"
    printf "  %-6s %-12s %-30s %s\n" "------" "------------" "------------------------------" "--------------------------------------------"

    for i in $(seq 1 "$REQUEST_COUNT"); do
        total_requests=$((total_requests + 1))

        local resp http_code
        http_code=$(curl -s -o /tmp/_nearby_resp_$$.tmp -w '%{http_code}' --connect-timeout 5 \
            "http://127.0.0.1:${listen_port}/echo" 2>/dev/null || echo "000")
        resp=$(cat /tmp/_nearby_resp_$$.tmp 2>/dev/null || echo "")
        rm -f /tmp/_nearby_resp_$$.tmp

        local route_target="unknown"
        local status_icon=""

        if [[ "$http_code" == "200" ]]; then
            success_requests=$((success_requests + 1))

            if echo "$resp" | grep -q "\"Campus\":\"${PROVIDER_1_CAMPUS}\""; then
                route_to_1=$((route_to_1 + 1))
                route_target="Provider-1 (${PROVIDER_1_CAMPUS})"
                if [[ "$CONSUMER_CAMPUS" == "$PROVIDER_1_CAMPUS" ]]; then
                    status_icon="${GREEN}✓${NC}"
                else
                    status_icon="${YELLOW}→${NC}"
                fi
            elif echo "$resp" | grep -q "\"Campus\":\"${PROVIDER_2_CAMPUS}\""; then
                route_to_2=$((route_to_2 + 1))
                route_target="Provider-2 (${PROVIDER_2_CAMPUS})"
                if [[ "$CONSUMER_CAMPUS" == "$PROVIDER_2_CAMPUS" ]]; then
                    status_icon="${GREEN}✓${NC}"
                else
                    status_icon="${RED}✗${NC}"
                fi
            elif echo "$resp" | grep -q "\"Campus\":\"${PROVIDER_3_CAMPUS}\""; then
                route_to_3=$((route_to_3 + 1))
                route_target="Provider-3 (${PROVIDER_3_CAMPUS})"
                if [[ "$CONSUMER_CAMPUS" == "$PROVIDER_3_CAMPUS" ]]; then
                    status_icon="${GREEN}✓${NC}"
                else
                    status_icon="${RED}✗${NC}"
                fi
            else
                route_to_unknown=$((route_to_unknown + 1))
                route_target="未知"
                status_icon="${YELLOW}?${NC}"
            fi
        else
            fail_requests=$((fail_requests + 1))
            route_target="请求失败 (HTTP ${http_code})"
            status_icon="${RED}✗${NC}"
        fi

        local resp_summary
        resp_summary=$(echo "$resp" | head -1 | cut -c1-80)

        printf "  %-6s ${status_icon} %-10s %-30s %s\n" "$i" "HTTP $http_code" "$route_target" "$resp_summary"
        echo "${i},${http_code},${route_target},${resp}" >> "$csv_file"

        sleep 0.1
    done

    echo ""

    # 结果通过全局变量返回
    LINK_TOTAL=$total_requests
    LINK_SUCCESS=$success_requests
    LINK_FAIL=$fail_requests
    LINK_ROUTE_1=$route_to_1
    LINK_ROUTE_2=$route_to_2
    LINK_ROUTE_3=$route_to_3
    LINK_ROUTE_UNKNOWN=$route_to_unknown
}

# ======================== 单条链路结论判定 ========================
# 参数:
#   $1 link_name
#   各项计数(全局 LINK_*)
# 返回值:
#   LINK_RESULT="PASS" / "PARTIAL" / "FAIL"  (全局变量)
summarize_link() {
    local link_name="$1"

    echo ""
    echo -e "  ${CYAN}── 链路 [${link_name}] 结果 ──${NC}"
    echo "  总请求数:            ${LINK_TOTAL}"
    echo "  成功请求:            ${LINK_SUCCESS}"
    echo "  失败请求:            ${LINK_FAIL}"
    echo "  路由到 Provider-1:   ${LINK_ROUTE_1}  (${PROVIDER_1_CAMPUS})"
    echo "  路由到 Provider-2:   ${LINK_ROUTE_2}  (${PROVIDER_2_CAMPUS})"
    echo "  路由到 Provider-3:   ${LINK_ROUTE_3}  (${PROVIDER_3_CAMPUS})"
    echo "  路由到未知目标:      ${LINK_ROUTE_UNKNOWN}"

    # 期望路由的 Provider
    local expected_count=0
    local expected_target=""
    if [[ "$CONSUMER_CAMPUS" == "$PROVIDER_1_CAMPUS" ]]; then
        expected_target="Provider-1 (${PROVIDER_1_CAMPUS})"
        expected_count=$LINK_ROUTE_1
    elif [[ "$CONSUMER_CAMPUS" == "$PROVIDER_2_CAMPUS" ]]; then
        expected_target="Provider-2 (${PROVIDER_2_CAMPUS})"
        expected_count=$LINK_ROUTE_2
    elif [[ "$CONSUMER_CAMPUS" == "$PROVIDER_3_CAMPUS" ]]; then
        expected_target="Provider-3 (${PROVIDER_3_CAMPUS})"
        expected_count=$LINK_ROUTE_3
    fi

    local non_nearby_count=$((LINK_SUCCESS - expected_count))

    if [[ "$LINK_SUCCESS" -eq "$LINK_TOTAL" ]] \
       && [[ "$expected_count" -eq "$LINK_SUCCESS" ]] \
       && [[ "$LINK_SUCCESS" -gt 0 ]]; then
        log_info "✅ [${link_name}] 就近路由功能正常生效"
        log_info "   所有 ${LINK_SUCCESS} 个请求均路由到 ${expected_target}"
        LINK_RESULT="PASS"
    elif [[ "$non_nearby_count" -eq 0 ]] && [[ "$LINK_SUCCESS" -gt 0 ]]; then
        log_warn "⚠️  [${link_name}] 就近路由基本正常，但有 ${LINK_FAIL} 个请求失败"
        LINK_RESULT="PARTIAL"
    else
        log_error "❌ [${link_name}] 就近路由验证未通过"
        if [[ "$non_nearby_count" -gt 0 ]]; then
            log_error "   有 ${non_nearby_count} 个请求路由到了非同 Campus 的 Provider"
        fi
        if [[ "$LINK_FAIL" -gt 0 ]]; then
            log_error "   有 ${LINK_FAIL} 个请求失败"
        fi
        LINK_RESULT="FAIL"
    fi
}

# ======================== 主流程 ========================

main() {
    # 初始化测试总日志（stdout/stderr 同时输出到终端和日志文件）
    setup_test_log "$@"

    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║       就近路由(Nearby Route)功能验证脚本         ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "配置信息:"
    echo "  北极星服务端:       ${POLARIS_SERVER}:8091 (HTTP: ${POLARIS_HTTP_ADDR})"
    echo "  服务名:             ${SERVICE_NAME}"
    echo "  命名空间:           ${NAMESPACE}"
    echo "  Simple-Consumer端口: ${SIMPLE_CONSUMER_PORT}  (GetOneInstance)"
    echo "  Consumer端口:       ${CONSUMER_PORT}  (ProcessRouters + ProcessLoadBalance)"
    echo "  SDK 匹配级别:       ${MATCH_LEVEL}"
    echo "  规则 match_level:   ${RULE_MATCH_LEVEL} / max=${RULE_MAX_MATCH_LEVEL} / strict=${RULE_STRICT_NEARBY}"
    echo "  期望规则名称:       ${EXPECTED_RULE_NAME}"
    echo "  跳过规则检查:       ${SKIP_RULE_CHECK}"
    echo "  自动创建规则:       ${AUTO_CREATE_RULE}"
    echo "  请求次数:           ${REQUEST_COUNT}"
    echo "  测试日志文件:       ${TEST_LOG_FILE}"
    echo ""
    echo "地域信息:"
    echo "  Region:             ${REGION}"
    echo "  Zone:               ${ZONE}"
    echo "  Provider-1 Campus:  ${PROVIDER_1_CAMPUS}"
    echo "  Provider-2 Campus:  ${PROVIDER_2_CAMPUS}"
    echo "  Provider-3 Campus:  ${PROVIDER_3_CAMPUS}"
    echo "  Consumer Campus:    ${CONSUMER_CAMPUS}"
    echo ""

    # ==================== 步骤1: 环境准备 ====================
    log_step "1/9 环境准备"

    mkdir -p "$BUILD_DIR" "$LOG_DIR"

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
    log_step "2/9 编译 Provider / Simple-Consumer / Consumer"

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

    # ==================== 步骤3: 就近路由规则检查/创建 ====================
    log_step "3/9 就近路由规则校验与自动创建"
    ensure_nearby_rules

    # ==================== 步骤4: 启动 Provider-1 (ap-guangzhou-1) ====================
    log_step "4/9 启动 Provider-1 (${PROVIDER_1_CAMPUS})"
    start_provider "provider_1" "$PROVIDER_1_CAMPUS" "PROVIDER_1_PORT" "PROVIDER_1_PID" "PROVIDER_1_ACTUAL_PORT"

    # ==================== 步骤5: 启动 Provider-2 (ap-guangzhou-2) ====================
    log_step "5/9 启动 Provider-2 (${PROVIDER_2_CAMPUS})"
    start_provider "provider_2" "$PROVIDER_2_CAMPUS" "PROVIDER_2_PORT" "PROVIDER_2_PID" "PROVIDER_2_ACTUAL_PORT"

    # ==================== 步骤6: 启动 Provider-3 (ap-guangzhou-3) ====================
    log_step "6/9 启动 Provider-3 (${PROVIDER_3_CAMPUS})"
    start_provider "provider_3" "$PROVIDER_3_CAMPUS" "PROVIDER_3_PORT" "PROVIDER_3_PID" "PROVIDER_3_ACTUAL_PORT"

    # ==================== 步骤7: 启动 Simple-Consumer + Consumer ====================
    log_step "7/9 启动 Simple-Consumer 和 Consumer (${CONSUMER_CAMPUS})"

    local simple_consumer_log="${LOG_DIR}/simple_consumer.log"
    local consumer_log="${LOG_DIR}/consumer.log"

    start_consumer "Simple-Consumer(GetOneInstance)" \
        "simple_consumer" "simple_consumer_run" \
        "$SIMPLE_CONSUMER_PORT" "$simple_consumer_log" "SIMPLE_CONSUMER_PID"

    start_consumer "Consumer(ProcessRouters)" \
        "consumer" "consumer_run" \
        "$CONSUMER_PORT" "$consumer_log" "CONSUMER_PID"

    # 等待服务发现缓存刷新
    log_info "等待 5s，确保服务发现缓存已刷新..."
    sleep 5

    # ==================== 步骤8: 验证两条链路 ====================
    local simple_csv="${LOG_DIR}/nearby_route_simple_consumer.csv"
    local full_csv="${LOG_DIR}/nearby_route_consumer.csv"

    log_step "8/9 验证链路: simple-consumer(GetOneInstance) → provider"
    verify_link "simple-consumer(GetOneInstance) → provider" \
        "$SIMPLE_CONSUMER_PORT" "$simple_csv"
    local s_total=$LINK_TOTAL s_success=$LINK_SUCCESS s_fail=$LINK_FAIL
    local s_r1=$LINK_ROUTE_1 s_r2=$LINK_ROUTE_2 s_r3=$LINK_ROUTE_3 s_ru=$LINK_ROUTE_UNKNOWN

    log_step "验证链路: consumer(ProcessRouters) → provider"
    verify_link "consumer(ProcessRouters) → provider" \
        "$CONSUMER_PORT" "$full_csv"
    local c_total=$LINK_TOTAL c_success=$LINK_SUCCESS c_fail=$LINK_FAIL
    local c_r1=$LINK_ROUTE_1 c_r2=$LINK_ROUTE_2 c_r3=$LINK_ROUTE_3 c_ru=$LINK_ROUTE_UNKNOWN

    # ==================== 步骤9: 汇总结论 ====================
    log_step "9/9 汇总结论"

    echo ""
    echo -e "${BLUE}╔═════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║          就近路由验证结果汇总（两条链路）           ║${NC}"
    echo -e "${BLUE}╚═════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "  服务名:              ${SERVICE_NAME}"
    echo "  命名空间:            ${NAMESPACE}"
    echo "  SDK 匹配级别:        ${MATCH_LEVEL}"
    echo "  使用规则名称:        ${EXPECTED_RULE_NAME}"
    echo ""
    echo -e "  ${CYAN}── 地域信息 ──${NC}"
    echo "  Region:              ${REGION}"
    echo "  Zone:                ${ZONE}"
    echo "  Consumer Campus:     ${CONSUMER_CAMPUS}"
    echo "  Provider-1 Campus:   ${PROVIDER_1_CAMPUS} (端口: ${PROVIDER_1_ACTUAL_PORT})"
    echo "  Provider-2 Campus:   ${PROVIDER_2_CAMPUS} (端口: ${PROVIDER_2_ACTUAL_PORT})"
    echo "  Provider-3 Campus:   ${PROVIDER_3_CAMPUS} (端口: ${PROVIDER_3_ACTUAL_PORT})"

    # 链路A: simple-consumer
    LINK_TOTAL=$s_total LINK_SUCCESS=$s_success LINK_FAIL=$s_fail \
    LINK_ROUTE_1=$s_r1 LINK_ROUTE_2=$s_r2 LINK_ROUTE_3=$s_r3 LINK_ROUTE_UNKNOWN=$s_ru
    summarize_link "simple-consumer(GetOneInstance) → provider"
    local simple_result=$LINK_RESULT

    # 链路B: consumer
    LINK_TOTAL=$c_total LINK_SUCCESS=$c_success LINK_FAIL=$c_fail \
    LINK_ROUTE_1=$c_r1 LINK_ROUTE_2=$c_r2 LINK_ROUTE_3=$c_r3 LINK_ROUTE_UNKNOWN=$c_ru
    summarize_link "consumer(ProcessRouters) → provider"
    local full_result=$LINK_RESULT

    echo ""
    echo "  详细结果 CSV:"
    echo "    simple-consumer: ${simple_csv}"
    echo "    consumer:        ${full_csv}"
    echo "  Provider-1 日志:     ${LOG_DIR}/provider_1.log"
    echo "  Provider-2 日志:     ${LOG_DIR}/provider_2.log"
    echo "  Provider-3 日志:     ${LOG_DIR}/provider_3.log"
    echo "  Simple-Consumer 日志: ${simple_consumer_log}"
    echo "  Consumer 日志:        ${consumer_log}"
    echo ""

    # ==================== 最终结论 ====================
    echo -e "${BLUE}── 最终结论 ──${NC}"
    echo ""
    if [[ "$simple_result" == "PASS" ]] && [[ "$full_result" == "PASS" ]]; then
        echo -e "${GREEN}╔═════════════════════════════════════════════════╗${NC}"
        echo -e "${GREEN}║  验证结论: ✅ 两条链路就近路由全部正常生效！        ║${NC}"
        echo -e "${GREEN}╚═════════════════════════════════════════════════╝${NC}"
        echo ""
        echo -e "${GREEN}  ✓ simple-consumer(GetOneInstance) → provider：请求均路由到同 Campus${NC}"
        echo -e "${GREEN}  ✓ consumer(ProcessRouters) → provider：请求均路由到同 Campus${NC}"
    elif [[ "$simple_result" == "FAIL" ]] || [[ "$full_result" == "FAIL" ]]; then
        echo -e "${RED}╔═════════════════════════════════════════════════╗${NC}"
        echo -e "${RED}║  验证结论: ❌ 至少一条链路验证未通过              ║${NC}"
        echo -e "${RED}╚═════════════════════════════════════════════════╝${NC}"
        echo ""
        echo -e "${RED}  simple-consumer: ${simple_result}${NC}"
        echo -e "${RED}  consumer:        ${full_result}${NC}"
        echo ""
        echo -e "${RED}  请检查以下配置:${NC}"
        echo -e "${RED}  1. 北极星控制台是否为服务 [${SERVICE_NAME}] 开启了就近路由${NC}"
        echo -e "${RED}  2. Consumer 的 polaris.yaml 是否配置了 nearbyBasedRouter 路由链${NC}"
        echo -e "${RED}  3. Provider 和 Consumer 的地域信息(Region/Zone/Campus)是否正确设置${NC}"
        echo -e "${RED}  4. 就近路由匹配级别(matchLevel: ${MATCH_LEVEL})是否正确${NC}"
    else
        echo -e "${YELLOW}验证结论: ⚠️  两条链路就近路由基本正常，但存在部分失败请求${NC}"
        echo -e "${YELLOW}  simple-consumer: ${simple_result}${NC}"
        echo -e "${YELLOW}  consumer:        ${full_result}${NC}"
        echo -e "${YELLOW}  请检查网络连接和服务状态${NC}"
    fi

    echo ""
    echo -e "${BLUE}提示: 查看详细日志:${NC}"
    echo -e "${BLUE}  测试总日志:      cat ${TEST_LOG_FILE}${NC}"
    echo -e "${BLUE}  Provider-1:      cat ${LOG_DIR}/provider_1.log${NC}"
    echo -e "${BLUE}  Provider-2:      cat ${LOG_DIR}/provider_2.log${NC}"
    echo -e "${BLUE}  Provider-3:      cat ${LOG_DIR}/provider_3.log${NC}"
    echo -e "${BLUE}  Simple-Consumer: cat ${simple_consumer_log}${NC}"
    echo -e "${BLUE}  Consumer:        cat ${consumer_log}${NC}"
    echo ""
}

main "$@"
