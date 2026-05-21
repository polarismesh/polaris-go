#!/bin/bash
# =============================================================================
# examples/ratelimit 限流端到端测试脚本
#
# 覆盖：
#   [用例 1.1] QPS 限流 - reject（快速失败）：触发上限后请求被立即拒绝
#   [用例 1.2] QPS 限流 - reject 新窗口再次生效：等过窗口后先 1 次放通，再次突发仍能触发限流
#   [用例 2.1] QPS 限流 - unirate（匀速排队）：超出速率的请求会被 SDK 排队，总耗时被拉长但仍 200
#   [用例 2.2] QPS 限流 - unirate 队列丢弃：队列等待超过 maxQueueDelay 时直接 429
#   [用例 2.3] QPS 限流 - unirate 新窗口再生效：旧排队耗尽后再次突发，规则继续按 2.1/2.2 模式生效
#   [用例 3.1] 并发数限流：长耗时并发请求触达上限后超出请求被 reject
#   [用例 3.2] 并发数 Release 归还 + 再次限流：上一批 sleep 完成后新请求放通，紧接突发仍能触发限流
#   [用例 3.3] 并发数低于上限放通：低于阈值的并发请求应全部通过
#   [用例 4.1] 自定义匹配规则全条件命中：HEADER+QUERY+METHOD+CALLER_SERVICE+CALLER_IP+CALLER_METADATA 都匹配 → 限流
#   [用例 4.2] 自定义匹配规则单维不命中：query 故意改成不匹配 → AND 关系下规则跳过 → 全放行
#   [用例 4.3] 自定义匹配规则新窗口再次生效：等过 QPS 窗口后再次发命中请求，仍能触发限流
#
# 流程：
#   1. 通过 Polaris HTTP API 检查并按需创建三条限流规则（QPS reject + QPS unirate + CONCURRENCY）；
#      已存在同名规则则跳过创建，规则一旦创建后**永久保留**，下次跑直接复用.
#   2. 编译并启动 3 组 (provider, consumer)：
#        - provider-qps-reject (18180) + consumer-qps-reject (18190)：QPS reject 策略
#        - provider-qps-unirate (18182) + consumer-qps-unirate (18192)：QPS unirate 策略
#        - provider-concurrency (18181) + consumer-concurrency (18191)：并发数策略
#   3. 链路 = curl → consumer → provider，由 consumer 通过 polaris 服务发现选 provider 并转发请求；
#      provider 调用 LimitAPI.GetQuota 命中规则，返回的状态码（含 429）由 consumer 透传给 curl
#   4. 用 curl 串行/并发打 consumer 端口，统计 200 / 429 比例与预期对比
#   5. 输出"验证结论: ✅/❌/⚠"行供聚合脚本识别
#   6. 退出前停止 provider/consumer；规则不删
#
# 用法:
#   chmod +x verify_ratelimit.sh
#   ./verify_ratelimit.sh                          # 默认本地 polaris (127.0.0.1)
#   ./verify_ratelimit.sh --polaris-server 1.2.3.4 # 指定 polaris 地址
#   ./verify_ratelimit.sh --polaris-token TOKEN    # 配置 token（开启鉴权时必填）
#   ./verify_ratelimit.sh --skip qps               # 跳过 QPS 用例
#   ./verify_ratelimit.sh --skip concurrency       # 跳过并发数用例
#   ./verify_ratelimit.sh --keep                   # 保留 provider 进程和日志（规则始终保留）
#   ./verify_ratelimit.sh --debug                  # 提高 SDK 日志级别
#
# 退出码:
#   0   = 全部用例通过
#   非0 = 失败用例数
# =============================================================================

set -uo pipefail

# ======================== 颜色 ========================
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# ======================== 默认配置 ========================
POLARIS_SERVER="${POLARIS_SERVER:-127.0.0.1}"
POLARIS_TOKEN="${POLARIS_TOKEN:-}"
SKIP_LIST=""
KEEP_RESOURCES=false
DEBUG_MODE=false

NAMESPACE="default"
QPS_SERVICE="QpsRatelimitEchoServer"        # 与 provider-qps 默认值一致；用于 reject（快速失败）验证
UNIRATE_SERVICE="UnirateRatelimitEchoServer" # 复用 provider-qps 二进制，--service 切换；用于 unirate（匀速排队）验证
CONCURRENCY_SERVICE="ConcurrencyEchoServer" # 与 provider-concurrency 默认值一致
CUSTOM_SERVICE="CustomMatchEchoServer"      # 复用 provider-qps 二进制；用于自定义匹配规则（用例 4.x）

# 规则名（用于查询是否已存在；脚本不再删除规则，已存在则跳过创建）
QPS_RULE_NAME="ratelimit-e2e-qps-rule"
UNIRATE_RULE_NAME="ratelimit-e2e-unirate-rule"
CONCURRENCY_RULE_NAME="ratelimit-e2e-concurrency-rule"
CUSTOM_RULE_NAME="ratelimit-e2e-custom-match-rule"

# 端口（避免与其他 demo 冲突）；本端到端测试链路 = curl → consumer → provider
# provider 端口
PORT_PROVIDER_QPS=18180
PORT_PROVIDER_CONCURRENCY=18181
PORT_PROVIDER_UNIRATE=18182
PORT_PROVIDER_CUSTOM=18183
# consumer 端口（每个用例段一个 consumer 实例，--service 指向对应 provider）
PORT_CONSUMER_QPS=18190
PORT_CONSUMER_CONCURRENCY=18191
PORT_CONSUMER_UNIRATE=18192
PORT_CONSUMER_CUSTOM=18193

# QPS reject 规则参数（用例 1.x）
QPS_MAX_AMOUNT=2
QPS_WINDOW_SECOND=1
# 串行打 6 次：单窗口下应限到 4，最坏跨 2 窗口仍能限到 2；判定阈值 expected = TOTAL - 2*MAX = 2.
# 不要降回 5：5 次跨 2 窗口时 limited 只有 1 会偶发触发边界 FAIL（参见 examples/ratelimit/.logs/verify_ratelimit-20260521_155123.log）.
QPS_TOTAL_REQUESTS=6

# QPS unirate 规则参数（用例 2.x）：4 次 / 2 秒 = 2 QPS（每个请求间隔约 500ms）
# - UNIRATE_TOTAL_REQUESTS（3.1）= 3：最大等待 ≈ (3-1)*500ms = 1000ms，<= maxQueueDelay，全部 200，验证"匀速排队不丢弃"语义
# - UNIRATE_BURST_REQUESTS  （3.2/3.3）= 6：最大等待 ≈ (6-1)*500ms = 2500ms，> maxQueueDelay，至少 ~3 个被丢弃为 429
UNIRATE_MAX_AMOUNT=4
UNIRATE_WINDOW_SECOND=2
UNIRATE_TOTAL_REQUESTS=3
UNIRATE_BURST_REQUESTS=6
# maxQueueDelay 必须设得"刚刚够 3.1 的最大等待"，又"不够 3.2 的最大等待"，让两个用例方向相反
UNIRATE_MAX_QUEUE_DELAY_SEC=1

# 并发数规则参数（用例 3.x）
CONCURRENCY_MAX_AMOUNT=2
CONCURRENCY_TOTAL_REQUESTS=5
CONCURRENCY_SLOW_MS=1500   # 业务耗时，需要 > 用例之间的间隔
CONCURRENCY_BELOW_LIMIT_REQ=2

# 自定义匹配规则参数（用例 4.x）—— 5 个 AND 条件:
#   HEADER x-tenant=gold + QUERY region=cn-east + CALLER_SERVICE=default/CustomCallerService
#   + CALLER_IP=10.0.0.1 + CALLER_METADATA env=prod
# 阈值 2/1s 与 reject 一样，便于触发后立即拒绝.
CUSTOM_MAX_AMOUNT=2
CUSTOM_WINDOW_SECOND=1
# 6 次串行请求在 1s 内大概率落入 1 个窗口（限 4 / 通过 2），最差跨 2 窗口（限 2 / 通过 4），
# 都能保证 limited ≥ 2；不要降回 5：5 次跨 2 窗口时 limited 只有 1 会偶发触发边界 FAIL.
CUSTOM_TOTAL_REQUESTS=6
CUSTOM_HEADER_KEY="x-tenant"
CUSTOM_HEADER_VALUE="gold"
CUSTOM_QUERY_KEY="region"
CUSTOM_QUERY_VALUE="cn-east"
CUSTOM_CALLER_SERVICE_NS="default"
CUSTOM_CALLER_SERVICE_SVC="CustomCallerService"
CUSTOM_CALLER_IP="10.0.0.1"
CUSTOM_CALLER_META_KEY="env"
CUSTOM_CALLER_META_VALUE="prod"

# 全局结果
declare -a CASE_NAMES
declare -a CASE_VERDICTS
declare -a CASE_DETAILS
TOTAL_FAIL=0

# ======================== 解析参数 ========================
while [[ $# -gt 0 ]]; do
    case "$1" in
        --polaris-server) POLARIS_SERVER="$2"; shift 2 ;;
        --polaris-token)  POLARIS_TOKEN="$2";  shift 2 ;;
        --skip)           SKIP_LIST="$2";      shift 2 ;;
        --keep)           KEEP_RESOURCES=true; shift ;;
        --debug)          DEBUG_MODE=true;     shift ;;
        -h|--help)
            cat <<EOF
用法: $0 [选项]

选项:
  --polaris-server <addr>   Polaris 服务端地址 (默认 127.0.0.1)
  --polaris-token <token>   Polaris 鉴权 Token
  --skip <列表>             逗号分隔，可选: qps,unirate,concurrency,custom
  --keep                    保留 provider 进程和日志（限流规则始终保留，无需此参数控制）
  --debug                   开启 debug 日志（透传 SDK 日志级别）
  -h, --help                展示帮助
EOF
            exit 0
            ;;
        *) echo -e "${RED}未知参数: $1${NC}"; exit 1 ;;
    esac
done

POLARIS_HTTP_ADDR="http://${POLARIS_SERVER}:8090"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG_DIR="${SCRIPT_DIR}/.logs"
BUILD_DIR="${SCRIPT_DIR}/.build"
mkdir -p "$LOG_DIR" "$BUILD_DIR"

LOG_FILE="${LOG_DIR}/verify_ratelimit-$(date +%Y%m%d_%H%M%S).log"

# 把整个脚本的 stdout / stderr 同时输出到屏幕 + 日志文件（日志中剥离 ANSI 颜色码）.
# 这样无论后续添加什么 echo / printf，都会自动入日志；不依赖每个 helper 单独 tee.
{
    echo "===== examples/ratelimit 验证日志 $(date '+%Y-%m-%d %H:%M:%S') ====="
    echo "Command: $0 $*"
} > "$LOG_FILE"
exec > >(tee >(sed -u 's/\x1b\[[0-9;]*m//g' >> "$LOG_FILE")) 2>&1

# ======================== 日志 ========================
# 由于上面 exec 把 stdout 接到 tee，这里所有 echo 都会自动入日志，**不再需要 tee -a**.
log_info()  { echo -e "${GREEN}[INFO]${NC} $(date '+%H:%M:%S') $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $(date '+%H:%M:%S') $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $(date '+%H:%M:%S') $*"; }
log_step() {
    echo ""
    echo -e "${CYAN}═══════════════════════════════════════════════════════════════════${NC}"
    echo -e "${CYAN}  $*${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════════════════════════${NC}"
}

# ======================== 用例与规则说明 helper ========================
# 用浅蓝色框打印"配置/操作/预期"块，让读日志的人一眼看到测试意图.
print_block() {
    local title="$1"
    shift
    echo -e "${BLUE}┌─ ${title} ─────────────────────────────────────────────${NC}"
    while [[ $# -gt 0 ]]; do
        echo -e "${BLUE}│${NC} $1"
        shift
    done
    echo -e "${BLUE}└──────────────────────────────────────────────────────────────${NC}"
}

# ======================== 工具：是否 skip 某用例分类 ========================
is_skipped() {
    local name="$1"
    [[ -z "$SKIP_LIST" ]] && return 1
    local IFS=','
    # shellcheck disable=SC2206
    local arr=($SKIP_LIST)
    for x in "${arr[@]}"; do
        [[ "$(echo "$x" | tr -d ' ')" == "$name" ]] && return 0
    done
    return 1
}

# ======================== Polaris HTTP API：限流规则查询/创建 ========================

# query_rule_id <rule_name> <service> -> 一行一个 ID（查询失败/无结果时空输出）
# 三元组定位：name + service + namespace 共同决定一条规则身份；同名但服务不同的规则
# 不应被误判为"已存在"——这是限流规则的基本身份语义.
query_rule_id() {
    local rule_name="$1"
    local service="$2"
    local resp http_code
    http_code=$(curl -s -o /tmp/_rl_q_$$.tmp -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request GET "${POLARIS_HTTP_ADDR}/naming/v1/ratelimits?name=${rule_name}&service=${service}&namespace=${NAMESPACE}&limit=50" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" 2>/dev/null || echo "000")
    resp=$(cat /tmp/_rl_q_$$.tmp 2>/dev/null || echo "")
    rm -f /tmp/_rl_q_$$.tmp
    if [[ "$http_code" != "200" ]]; then
        return 0
    fi
    # 服务端的 service= 过滤已生效，但仍在 python 侧再校验一次 name & service 完全相等，
    # 防御服务端模糊匹配等边界情况.
    SVC="$service" RULE="$rule_name" python3 -c "
import sys, json, os
svc = os.environ['SVC']
rule = os.environ['RULE']
try:
    data = json.load(sys.stdin)
    for r in data.get('rateLimits', []):
        if r.get('name', '') == rule and r.get('service', '') == svc:
            print(r.get('id', ''))
except Exception:
    pass
" <<< "$resp" 2>/dev/null || true
}

# rule_exists <rule_name> <service>：判断指定 (name, service, namespace) 三元组的规则是否已存在.
# 返回码 0=存在, 1=不存在.
rule_exists() {
    local rule_name="$1"
    local service="$2"
    local ids
    ids=$(query_rule_id "$rule_name" "$service")
    [[ -n "$ids" ]]
}

# query_rule_max_queue_delay：查询已存在 unirate 规则的 max_queue_delay 值（单位秒）.
# 只在该字段存在时输出数值；查询失败/字段缺失输出空串.
# 用于检测控制台规则参数与脚本期望是否一致（避免老规则 max_queue_delay=10s 让 2.2/2.3 边界用例失效）.
query_rule_max_queue_delay() {
    local rule_name="$1"
    local service="$2"
    local resp http_code
    http_code=$(curl -s -o /tmp/_rl_qmqd_$$.tmp -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request GET "${POLARIS_HTTP_ADDR}/naming/v1/ratelimits?name=${rule_name}&service=${service}&namespace=${NAMESPACE}&limit=50" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" 2>/dev/null || echo "000")
    resp=$(cat /tmp/_rl_qmqd_$$.tmp 2>/dev/null || echo "")
    rm -f /tmp/_rl_qmqd_$$.tmp
    if [[ "$http_code" != "200" ]]; then
        return 0
    fi
    SVC="$service" RULE="$rule_name" python3 -c "
import sys, json, os
svc = os.environ['SVC']
rule = os.environ['RULE']
try:
    data = json.load(sys.stdin)
    for r in data.get('rateLimits', []):
        if r.get('name', '') == rule and r.get('service', '') == svc:
            v = r.get('max_queue_delay', r.get('maxQueueDelay', None))
            if v is not None:
                print(v)
            break
except Exception:
    pass
" <<< "$resp" 2>/dev/null || echo ""
}

# update_rule_via_http <body_json>：对一个或多个已有规则做整体替换更新.
# body 必须是 JSON 数组、每项必须包含 id（由调用方 query_rule_id 取到），其它字段语义同 create.
# 用法：
#   body=$(... 拼好 JSON ...)
#   update_rule_via_http "$body"
update_rule_via_http() {
    local body="$1"
    local resp http_code
    http_code=$(curl -s -o /tmp/_rl_u_$$.tmp -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request PUT "${POLARIS_HTTP_ADDR}/naming/v1/ratelimits" \
        --header 'Content-Type: application/json' \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --data "$body" 2>/dev/null || echo "000")
    resp=$(cat /tmp/_rl_u_$$.tmp 2>/dev/null || echo "")
    rm -f /tmp/_rl_u_$$.tmp
    if [[ "$http_code" != "200" ]]; then
        log_error "[update_rule] HTTP=${http_code} body=${resp}"
        return 1
    fi
    return 0
}

# create_qps_rule
# 已存在同名规则时跳过创建，规则保持不变（包括字段差异；如需重置请人工到控制台修改）.
create_qps_rule() {
    local rule_name="$QPS_RULE_NAME"
    print_block "QPS 规则配置 [$rule_name]" \
        "服务/命名空间:    ${QPS_SERVICE} / ${NAMESPACE}" \
        "限流资源类型:     QPS（请求数限流，按 reject 策略）" \
        "限流模式:         LOCAL（单机限流；与远程同步无关）" \
        "method 匹配:      EXACT '/echo' （仅作用于 /echo 接口）" \
        "阈值:             ${QPS_MAX_AMOUNT} 次 / ${QPS_WINDOW_SECOND} 秒" \
        "" \
        "效果:             单机视角下，每 ${QPS_WINDOW_SECOND}s 内最多放过 ${QPS_MAX_AMOUNT} 个 /echo 请求；" \
        "                  超出部分会被 SDK 直接拒绝（HTTP 429）；窗口结束后自动恢复."
    if rule_exists "$rule_name" "$QPS_SERVICE"; then
        log_info "QPS 规则 [$rule_name] 已存在于服务 [$QPS_SERVICE]，跳过创建（如需变更阈值请到控制台调整）"
        return 0
    fi
    local body
    body=$(SVC="$QPS_SERVICE" NS="$NAMESPACE" NAME="$rule_name" \
        AMOUNT="$QPS_MAX_AMOUNT" WINDOW="$QPS_WINDOW_SECOND" \
        python3 -c "
import os, json
print(json.dumps([{
    'name': os.environ['NAME'],
    'service': os.environ['SVC'],
    'namespace': os.environ['NS'],
    'priority': 0,
    'resource': 'QPS',
    'type': 'LOCAL',
    'method': {'type': 'EXACT', 'value': '/echo'},
    'amounts': [{
        'maxAmount': int(os.environ['AMOUNT']),
        'validDuration': '%ss' % os.environ['WINDOW'],
    }],
    'action': 'REJECT',
    'disable': False,
}]))")
    local http_code
    http_code=$(curl -s -o /tmp/_rl_c_$$.tmp -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/naming/v1/ratelimits" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "$body" 2>/dev/null || echo "000")
    local resp
    resp=$(cat /tmp/_rl_c_$$.tmp 2>/dev/null || echo "")
    rm -f /tmp/_rl_c_$$.tmp
    if [[ "$http_code" != "200" ]]; then
        log_error "创建 QPS 规则失败 HTTP=${http_code} resp=${resp}"
        return 1
    fi
    log_info "QPS 规则 [$rule_name] 已创建"
    return 0
}

# create_unirate_rule
# 与 reject 规则的关键差异：action='UNIRATE'、maxQueueDelay 控制最大排队时间.
# 同名规则查重也按 (name, service) 三元组定位.
create_unirate_rule() {
    local rule_name="$UNIRATE_RULE_NAME"
    local effective_qps
    effective_qps=$(awk -v a="$UNIRATE_MAX_AMOUNT" -v w="$UNIRATE_WINDOW_SECOND" \
        'BEGIN { printf "%.1f", a/w }')
    print_block "QPS 规则配置 [$rule_name] (unirate 匀速排队)" \
        "服务/命名空间:    ${UNIRATE_SERVICE} / ${NAMESPACE}" \
        "限流资源类型:     QPS（请求数限流，按 unirate 策略）" \
        "限流模式:         LOCAL（单机限流）" \
        "method 匹配:      EXACT '/echo'" \
        "阈值:             ${UNIRATE_MAX_AMOUNT} 次 / ${UNIRATE_WINDOW_SECOND} 秒（即 ${effective_qps} QPS）" \
        "排队上限:         maxQueueDelay = ${UNIRATE_MAX_QUEUE_DELAY_SEC}s（请求允许等待的最长时间）" \
        "" \
        "效果:             SDK 将请求按匀速放过——每个请求间隔约 $((1000 * UNIRATE_WINDOW_SECOND / UNIRATE_MAX_AMOUNT))ms；" \
        "                  超出速率的请求**不会立即拒绝**，而是被 SDK 排队等待（QuotaFutureImpl.Get() 内部 sleep）；" \
        "                  仅当排队等待超过 ${UNIRATE_MAX_QUEUE_DELAY_SEC}s 时才返回限流（HTTP 429，即用例 2.2 验证的丢弃行为）；" \
        "                  否则最终返回 200，但请求总耗时被拉长——这是与 reject 行为的核心差异."
    if rule_exists "$rule_name" "$UNIRATE_SERVICE"; then
        # 校验关键参数：max_queue_delay 直接决定用例 2.2/2.3 能否触发"队列丢弃"路径，
        # 控制台上的旧规则一旦不一致，2.2/2.3 必然 FAIL（参见 .logs/verify_ratelimit-20260521_155123.log）.
        # 检测到不一致时主动 PUT 更新，避免要求用户手动到控制台改.
        local actual_mqd
        actual_mqd=$(query_rule_max_queue_delay "$rule_name" "$UNIRATE_SERVICE")
        if [[ -n "$actual_mqd" ]] && [[ "$actual_mqd" != "$UNIRATE_MAX_QUEUE_DELAY_SEC" ]]; then
            log_warn "[unirate 规则] 控制台上 max_queue_delay=${actual_mqd}s 与脚本期望 ${UNIRATE_MAX_QUEUE_DELAY_SEC}s 不一致，自动 PUT 更新"
            local rule_id
            rule_id=$(query_rule_id "$rule_name" "$UNIRATE_SERVICE")
            if [[ -z "$rule_id" ]]; then
                log_error "[unirate 规则] 拿不到规则 id，无法自动更新；请手动到控制台调整或删除规则后重跑脚本"
                return 1
            fi
            local body
            body=$(_build_unirate_rule_body "$rule_name" "$rule_id")
            if update_rule_via_http "$body"; then
                log_info "[unirate 规则] 已自动更新：max_queue_delay ${actual_mqd}s → ${UNIRATE_MAX_QUEUE_DELAY_SEC}s"
            else
                log_error "[unirate 规则] PUT 更新失败；请手动到控制台调整或删除规则后重跑"
                return 1
            fi
        else
            log_info "unirate 规则 [$rule_name] 已存在于服务 [$UNIRATE_SERVICE]，参数与脚本期望一致，跳过更新"
        fi
        return 0
    fi
    local body
    body=$(_build_unirate_rule_body "$rule_name" "")
    local http_code resp
    http_code=$(curl -s -o /tmp/_rl_c_$$.tmp -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/naming/v1/ratelimits" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "$body" 2>/dev/null || echo "000")
    resp=$(cat /tmp/_rl_c_$$.tmp 2>/dev/null || echo "")
    rm -f /tmp/_rl_c_$$.tmp
    if [[ "$http_code" != "200" ]]; then
        log_error "创建 unirate 规则失败 HTTP=${http_code} resp=${resp}"
        return 1
    fi
    log_info "unirate 规则 [$rule_name] 已创建"
    return 0
}

# _build_unirate_rule_body <rule_name> <rule_id>
# 复用给 create（rule_id 留空）与 update（带 id）；body 是 JSON 数组形式（同 polaris HTTP API 约定）.
_build_unirate_rule_body() {
    local rule_name="$1"
    local rule_id="$2"
    SVC="$UNIRATE_SERVICE" NS="$NAMESPACE" NAME="$rule_name" \
        AMOUNT="$UNIRATE_MAX_AMOUNT" WINDOW="$UNIRATE_WINDOW_SECOND" \
        MAX_QUEUE_DELAY="$UNIRATE_MAX_QUEUE_DELAY_SEC" \
        RULE_ID="$rule_id" \
        python3 -c "
import os, json
rule = {
    'name': os.environ['NAME'],
    'service': os.environ['SVC'],
    'namespace': os.environ['NS'],
    'priority': 0,
    'resource': 'QPS',
    'type': 'LOCAL',
    'method': {'type': 'EXACT', 'value': '/echo'},
    'amounts': [{
        'maxAmount': int(os.environ['AMOUNT']),
        'validDuration': '%ss' % os.environ['WINDOW'],
    }],
    'action': 'UNIRATE',
    'max_queue_delay': int(os.environ['MAX_QUEUE_DELAY']),
    'disable': False,
}
rid = os.environ.get('RULE_ID', '')
if rid:
    rule['id'] = rid
print(json.dumps([rule]))
"
}

# create_concurrency_rule
# 已存在同名规则时跳过创建，规则保持不变（包括字段差异；如需重置请人工到控制台修改）.
create_concurrency_rule() {
    local rule_name="$CONCURRENCY_RULE_NAME"
    print_block "并发数规则配置 [$rule_name]" \
        "服务/命名空间:    ${CONCURRENCY_SERVICE} / ${NAMESPACE}" \
        "限流资源类型:     CONCURRENCY（并发数限流，由 reject_concurrency 插件实现）" \
        "限流模式:         LOCAL（节点维度纯本地计数；即便控制面下发 GLOBAL 也会被框架强制为本地）" \
        "method 匹配:      EXACT '/slow' （仅作用于 /slow 接口；/slow 是模拟长耗时业务的接口）" \
        "阈值:             concurrencyAmount.maxAmount = ${CONCURRENCY_MAX_AMOUNT}" \
        "" \
        "效果:             同时只允许 ${CONCURRENCY_MAX_AMOUNT} 个 /slow 请求在处理中；超出部分立即拒绝（HTTP 429）；" \
        "                  请求结束后并发计数自动归还，新请求可继续进入." \
        "前提:             provider 必须 defer future.Release() 归还配额（见 main.go），否则计数会泄漏."
    if rule_exists "$rule_name" "$CONCURRENCY_SERVICE"; then
        log_info "并发数规则 [$rule_name] 已存在于服务 [$CONCURRENCY_SERVICE]，跳过创建（如需变更阈值请到控制台调整）"
        return 0
    fi
    local body
    body=$(SVC="$CONCURRENCY_SERVICE" NS="$NAMESPACE" NAME="$rule_name" \
        AMOUNT="$CONCURRENCY_MAX_AMOUNT" \
        python3 -c "
import os, json
# Resource=CONCURRENCY 时使用 concurrencyAmount 字段；method 精准匹配 /slow.
# Type 写 LOCAL 即可（即便写 GLOBAL，SDK 框架也会强制按本地处理，参见 buildRemoteConfigMode）。
print(json.dumps([{
    'name': os.environ['NAME'],
    'service': os.environ['SVC'],
    'namespace': os.environ['NS'],
    'priority': 0,
    'resource': 'CONCURRENCY',
    'type': 'LOCAL',
    'method': {'type': 'EXACT', 'value': '/slow'},
    'concurrencyAmount': {'maxAmount': int(os.environ['AMOUNT'])},
    'disable': False,
}]))")
    local http_code resp
    http_code=$(curl -s -o /tmp/_rl_c_$$.tmp -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/naming/v1/ratelimits" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "$body" 2>/dev/null || echo "000")
    resp=$(cat /tmp/_rl_c_$$.tmp 2>/dev/null || echo "")
    rm -f /tmp/_rl_c_$$.tmp
    if [[ "$http_code" != "200" ]]; then
        log_error "创建并发数规则失败 HTTP=${http_code} resp=${resp}"
        return 1
    fi
    log_info "并发数规则 [$rule_name] 已创建"
    return 0
}

# create_custom_match_rule
# 自定义匹配规则：5 个 AND 条件（HEADER + QUERY + CALLER_SERVICE + CALLER_IP + CALLER_METADATA）.
# 验证 polaris 限流规则的多维度组合匹配——arguments 之间是 AND 关系，任一不匹配则规则不命中.
create_custom_match_rule() {
    local rule_name="$CUSTOM_RULE_NAME"
    print_block "QPS 规则配置 [$rule_name] (自定义多维匹配, AND 关系)" \
        "服务/命名空间:    ${CUSTOM_SERVICE} / ${NAMESPACE}" \
        "限流资源类型:     QPS（reject 策略）" \
        "method 匹配:      EXACT '/echo'" \
        "阈值:             ${CUSTOM_MAX_AMOUNT} 次 / ${CUSTOM_WINDOW_SECOND} 秒" \
        "" \
        "arguments（5 个条件全部 EXACT 匹配，且 AND 关系）：" \
        "  1) HEADER          ${CUSTOM_HEADER_KEY} = ${CUSTOM_HEADER_VALUE}" \
        "  2) QUERY           ${CUSTOM_QUERY_KEY}  = ${CUSTOM_QUERY_VALUE}" \
        "  3) CALLER_SERVICE  ${CUSTOM_CALLER_SERVICE_NS}/${CUSTOM_CALLER_SERVICE_SVC}" \
        "  4) CALLER_IP       ${CUSTOM_CALLER_IP}" \
        "  5) CALLER_METADATA ${CUSTOM_CALLER_META_KEY} = ${CUSTOM_CALLER_META_VALUE}" \
        "" \
        "效果:             只有 5 个条件全部命中时，限流规则才生效（超阈值后 429）；" \
        "                  任一条件不命中（例如 query=cn-west），整条规则跳过，请求全部放行（用例 4.2 反向验证）."
    if rule_exists "$rule_name" "$CUSTOM_SERVICE"; then
        log_info "自定义匹配规则 [$rule_name] 已存在于服务 [$CUSTOM_SERVICE]，跳过创建（如需变更请到控制台调整）"
        return 0
    fi
    local body
    body=$(SVC="$CUSTOM_SERVICE" NS="$NAMESPACE" NAME="$rule_name" \
        AMOUNT="$CUSTOM_MAX_AMOUNT" WINDOW="$CUSTOM_WINDOW_SECOND" \
        H_KEY="$CUSTOM_HEADER_KEY" H_VAL="$CUSTOM_HEADER_VALUE" \
        Q_KEY="$CUSTOM_QUERY_KEY" Q_VAL="$CUSTOM_QUERY_VALUE" \
        CS_NS="$CUSTOM_CALLER_SERVICE_NS" CS_SVC="$CUSTOM_CALLER_SERVICE_SVC" \
        CIP="$CUSTOM_CALLER_IP" \
        CM_KEY="$CUSTOM_CALLER_META_KEY" CM_VAL="$CUSTOM_CALLER_META_VALUE" \
        python3 -c "
import os, json
def m(t, k, v):
    arg = {'type': t, 'value': {'type': 'EXACT', 'value': v}}
    if k:
        arg['key'] = k
    return arg
print(json.dumps([{
    'name': os.environ['NAME'],
    'service': os.environ['SVC'],
    'namespace': os.environ['NS'],
    'priority': 0,
    'resource': 'QPS',
    'type': 'LOCAL',
    'method': {'type': 'EXACT', 'value': '/echo'},
    'arguments': [
        m('HEADER',          os.environ['H_KEY'],  os.environ['H_VAL']),
        m('QUERY',           os.environ['Q_KEY'],  os.environ['Q_VAL']),
        # CALLER_SERVICE：key=主调命名空间，value=主调服务名
        m('CALLER_SERVICE',  os.environ['CS_NS'],  os.environ['CS_SVC']),
        m('CALLER_IP',       '',                   os.environ['CIP']),
        m('CALLER_METADATA', os.environ['CM_KEY'], os.environ['CM_VAL']),
    ],
    'amounts': [{
        'maxAmount': int(os.environ['AMOUNT']),
        'validDuration': '%ss' % os.environ['WINDOW'],
    }],
    'action': 'REJECT',
    'disable': False,
}]))")
    local http_code resp
    http_code=$(curl -s -o /tmp/_rl_c_$$.tmp -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/naming/v1/ratelimits" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "$body" 2>/dev/null) || http_code="000"
    resp=$(cat /tmp/_rl_c_$$.tmp 2>/dev/null || echo "")
    rm -f /tmp/_rl_c_$$.tmp
    if [[ "$http_code" != "200" ]]; then
        log_error "创建自定义匹配规则失败 HTTP=${http_code} resp=${resp}"
        return 1
    fi
    log_info "自定义匹配规则 [$rule_name] 已创建"
    return 0
}

# ======================== Provider / Consumer 进程管理 ========================
PROVIDER_QPS_PID=""
PROVIDER_UNIRATE_PID=""
PROVIDER_CONC_PID=""
PROVIDER_CUSTOM_PID=""
CONSUMER_QPS_PID=""
CONSUMER_UNIRATE_PID=""
CONSUMER_CONC_PID=""
CONSUMER_CUSTOM_PID=""

# build_and_start_binary <subdir> <port> <service> <log_name> <out_pid_var> [extra_args...]
# 适用于本目录下任何按 polaris-go SDK 模式起的 binary（provider-qps / provider-concurrency / consumer 等）.
# - subdir     : 源码所在子目录（如 provider-qps、consumer）
# - log_name   : 实例的逻辑名（如 provider-qps-reject、consumer-qps-reject），用于运行目录与日志文件名
#                允许同一 subdir 起多个实例（不同 log_name + 不同端口 + 不同服务名）
# - extra_args : 透传给 binary 的额外命令行参数（如 consumer 的 --caller-service / --caller-ip / --caller-metadata）
build_and_start_binary() {
    local subdir="$1"
    local port="$2"
    local service="$3"
    local log_name="$4"
    local out_var="$5"
    shift 5
    local extra_args=("$@")
    local src_dir="${SCRIPT_DIR}/${subdir}"
    # 每个 provider 实例拥有自己的 .build 子目录，存放二进制 + polaris.yaml 软链 + 运行时 polaris/ 目录.
    # 用 log_name（而非 subdir）作为运行目录名，让同一 subdir 可以起多个实例（如 provider-qps 起 reject + unirate 两份），
    # 各自的 SDK 日志/缓存互不干扰.
    local run_dir="${BUILD_DIR}/${log_name}"
    local bin="${run_dir}/${subdir}"
    mkdir -p "$run_dir"

    log_info "[build] ${log_name} (源码 ${subdir}) → ${bin}"
    # go build 的 stdout/stderr 走 exec 接管的 tee 流程，自动入日志文件
    ( cd "$src_dir" && go build -o "$bin" . )
    if [[ ! -x "$bin" ]]; then
        log_error "[build] ${log_name} 编译失败，详见 ${LOG_FILE}"
        return 1
    fi

    # polaris.yaml 软链到 run_dir，让 SDK 在 cwd=run_dir 时仍能加载并展开 ${POLARIS_SERVER}/${POLARIS_TOKEN}.
    # 用软链而非复制：源码 yaml 修改会即时生效，便于调试.
    ln -sf "${src_dir}/polaris.yaml" "${run_dir}/polaris.yaml"

    local pid_log="${LOG_DIR}/${log_name}.log"
    log_info "[start] ${log_name} 监听 :${port}, stdout 日志 ${pid_log}, SDK 日志 ${run_dir}/polaris/log"
    # provider 进程在 run_dir 下启动：
    #   1) polaris-go SDK 默认从 cwd 加载 ./polaris.yaml（已通过软链准备好）
    #   2) yaml 中的 ${POLARIS_SERVER}/${POLARIS_TOKEN} 占位符会通过 os.ExpandEnv 展开
    #   3) SDK 默认日志根目录 "./polaris/log" 自然写到 run_dir/polaris/log/ 下
    #
    # 用 pushd/popd 而不是 subshell，确保 $! 拿到的是 bin 自身的 PID
    # （subshell 形式下 $! 会是 subshell 的 PID，stop_provider 杀不到真正的子进程）.
    pushd "$run_dir" >/dev/null
    # ${extra_args[@]+"${extra_args[@]}"}: 兼容 macOS 自带 bash 3.2 在 set -u 下展开空数组会报 unbound variable 的行为
    # --debug 透传：脚本侧 --debug 命中后，所有 binary（provider-qps / provider-concurrency / consumer）
    # 都会通过自身的 --debug flag 调用 polaris.SetLoggersLevel(DebugLog)，让 ratelimit/cache 等 logger 全部下到 DEBUG.
    local debug_args=()
    if [[ "$DEBUG_MODE" == "true" ]]; then
        debug_args+=(--debug)
    fi
    POLARIS_SERVER="$POLARIS_SERVER" POLARIS_TOKEN="$POLARIS_TOKEN" \
        "$bin" --namespace "$NAMESPACE" --service "$service" --port "$port" \
        --token "$POLARIS_TOKEN" \
        ${debug_args[@]+"${debug_args[@]}"} \
        ${extra_args[@]+"${extra_args[@]}"} >"$pid_log" 2>&1 &
    local pid=$!
    popd >/dev/null
    eval "$out_var=\"$pid\""

    # 等启动 + 注册成功
    local i
    for ((i=0; i<30; i++)); do
        if ! kill -0 "$pid" 2>/dev/null; then
            log_error "[start] ${log_name} 进程已退出 (PID=${pid})，详见 ${pid_log}"
            return 1
        fi
        if curl -fsS --connect-timeout 1 --max-time 2 "http://127.0.0.1:${port}/echo" >/dev/null 2>&1; then
            log_info "[ready] ${log_name} (PID=${pid}, port=${port}) 就绪"
            return 0
        fi
        sleep 1
    done
    # /echo 可能由于规则已生效返回 429，也算"就绪"，再做一次 TCP 端口探测兜底
    if (echo > /dev/tcp/127.0.0.1/"$port") >/dev/null 2>&1; then
        log_info "[ready] ${log_name} (PID=${pid}, port=${port}) TCP 端口已打开"
        return 0
    fi
    log_error "[start] ${log_name} 30 秒内未就绪，详见 ${pid_log}"
    return 1
}

stop_provider() {
    local pid="$1"
    [[ -z "$pid" ]] && return 0
    if kill -0 "$pid" 2>/dev/null; then
        kill "$pid" 2>/dev/null || true
        sleep 1
        if kill -0 "$pid" 2>/dev/null; then
            kill -9 "$pid" 2>/dev/null || true
        fi
    fi
}

# ======================== 用例工具 ========================
record_case() {
    local name="$1"
    local verdict="$2"
    local detail="$3"
    CASE_NAMES+=("$name")
    CASE_VERDICTS+=("$verdict")
    CASE_DETAILS+=("$detail")
    if [[ "$verdict" != "PASS" ]]; then
        TOTAL_FAIL=$((TOTAL_FAIL+1))
    fi
    case "$verdict" in
        PASS) echo -e "  ${GREEN}✅ [${name}] PASS${NC} - ${detail}" ;;
        FAIL) echo -e "  ${RED}❌ [${name}] FAIL${NC} - ${detail}" ;;
        WARN) echo -e "  ${YELLOW}⚠️  [${name}] WARN${NC} - ${detail}" ;;
    esac
}

# count_status <port> <path> <total>
# 串行打 N 个请求，统计 200 / 429 / 其他状态码计数
count_status() {
    local port="$1"
    local path="$2"
    local total="$3"
    local ok=0 limited=0 other=0 i
    for ((i=0; i<total; i++)); do
        local code
        code=$(curl -s -o /dev/null --connect-timeout 2 --max-time 5 \
            -w '%{http_code}' "http://127.0.0.1:${port}${path}" 2>/dev/null || echo "000")
        case "$code" in
            200) ok=$((ok+1)) ;;
            429) limited=$((limited+1)) ;;
            *)   other=$((other+1)) ;;
        esac
    done
    echo "${ok} ${limited} ${other}"
}

# count_status_with_header <port> <path> <total> <header_line>
# 同 count_status，但 curl 增加一个 -H 头；用于自定义匹配规则用例 4.x.
count_status_with_header() {
    local port="$1"
    local path="$2"
    local total="$3"
    local header_line="$4"
    local ok=0 limited=0 other=0 i
    for ((i=0; i<total; i++)); do
        local code
        code=$(curl -s -o /dev/null --connect-timeout 2 --max-time 5 \
            -H "${header_line}" \
            -w '%{http_code}' "http://127.0.0.1:${port}${path}" 2>/dev/null || echo "000")
        case "$code" in
            200) ok=$((ok+1)) ;;
            429) limited=$((limited+1)) ;;
            *)   other=$((other+1)) ;;
        esac
    done
    echo "${ok} ${limited} ${other}"
}

# count_status_concurrent <port> <path> <total>
# 并发打 N 个请求，等所有完成后再统计
count_status_concurrent() {
    local port="$1"
    local path="$2"
    local total="$3"
    local tmp
    tmp=$(mktemp -d)
    local i
    for ((i=0; i<total; i++)); do
        (
            code=$(curl -s -o /dev/null --connect-timeout 2 --max-time 10 \
                -w '%{http_code}' "http://127.0.0.1:${port}${path}" 2>/dev/null || echo "000")
            echo "$code" > "${tmp}/code_${i}"
        ) &
    done
    wait
    local ok=0 limited=0 other=0 code
    for f in "${tmp}"/code_*; do
        code=$(cat "$f")
        case "$code" in
            200) ok=$((ok+1)) ;;
            429) limited=$((limited+1)) ;;
            *)   other=$((other+1)) ;;
        esac
    done
    rm -rf "$tmp"
    echo "${ok} ${limited} ${other}"
}

# ======================== 用例 1.x：QPS 限流 ========================
run_qps_cases() {
    log_step "[用例 1.x] QPS 限流 (链路: curl → consumer:${PORT_CONSUMER_QPS} → provider:${PORT_PROVIDER_QPS})"
    if ! create_qps_rule; then
        record_case "1.0 创建 QPS 规则" "FAIL" "HTTP API 调用失败"
        return
    fi
    if ! build_and_start_binary "provider-qps" "$PORT_PROVIDER_QPS" \
        "$QPS_SERVICE" "provider-qps-reject" "PROVIDER_QPS_PID"; then
        record_case "1.0 启动 QPS provider" "FAIL" "provider-qps (reject) 启动失败"
        return
    fi
    # consumer --service 指向同一个 QPS 服务，curl 打到 consumer 后由 SDK 服务发现选 provider 实例转发
    if ! build_and_start_binary "consumer" "$PORT_CONSUMER_QPS" \
        "$QPS_SERVICE" "consumer-qps-reject" "CONSUMER_QPS_PID"; then
        record_case "1.0 启动 QPS consumer" "FAIL" "consumer (reject) 启动失败"
        return
    fi

    # 等规则 push 到 SDK；SDK 的 cache 一般 1-2s 拉一次
    log_info "等待 4s 让 SDK 拉到规则..."
    sleep 4

    # ---------- 用例 1.1：触发限流 ----------
    print_block "[用例 1.1] QPS 限流触发（链路 curl → consumer → provider）" \
        "操作:   串行向 http://127.0.0.1:${PORT_CONSUMER_QPS}/echo 发起 ${QPS_TOTAL_REQUESTS} 次 GET（无并发）" \
        "链路:   curl → consumer:${PORT_CONSUMER_QPS}（服务发现 → ）provider:${PORT_PROVIDER_QPS}/echo" \
        "原理:   provider 内 LimitAPI.GetQuota 命中 QPS 规则；窗口内放过 ${QPS_MAX_AMOUNT} 个，剩余被拒；429 由 provider 透传到 consumer 再到 curl" \
        "预期:   200 状态 ≈ ${QPS_MAX_AMOUNT}（首批落入窗口），429 状态 ≥ $((QPS_TOTAL_REQUESTS - 2 * QPS_MAX_AMOUNT))（其余被限流，容忍 SDK 拉规则 1s 时延）" \
        "判定:   只要 429 ≥ 期望最小值且无其它状态码，即 PASS"
    log_info "[用例 1.1] 串行打 ${QPS_TOTAL_REQUESTS} 次 /echo（上限 ${QPS_MAX_AMOUNT}/${QPS_WINDOW_SECOND}s）"
    local stat ok limited other
    stat=$(count_status "$PORT_CONSUMER_QPS" "/echo" "$QPS_TOTAL_REQUESTS")
    read -r ok limited other <<< "$stat"
    log_info "结果: 200=${ok}  429=${limited}  其他=${other}"

    # 期望：被限流次数 ≥ (total - maxAmount) - 1（容忍 SDK 拉规则有时延）
    local expected_min_limited=$((QPS_TOTAL_REQUESTS - 2 * QPS_MAX_AMOUNT))
    [[ $expected_min_limited -lt 1 ]] && expected_min_limited=1
    if [[ "$other" -gt 0 ]]; then
        record_case "用例 1.1 QPS 限流触发" "FAIL" "出现非 200/429 状态码 (other=${other})"
    elif [[ "$limited" -ge "$expected_min_limited" ]]; then
        record_case "用例 1.1 QPS 限流触发" "PASS" \
            "限流 ${limited} 次 (≥${expected_min_limited})，通过 ${ok} 次"
    else
        record_case "用例 1.1 QPS 限流触发" "FAIL" \
            "限流次数 ${limited} 不足，期望 ≥${expected_min_limited}（200=${ok} 429=${limited}）"
    fi

    # ---------- 用例 1.2：新窗口重新放通 + 再次能触发限流 ----------
    # 跨过 1.1 留下的限流窗口后，QPS 规则配额应被清零；
    # 第一发请求验证"放通"语义，紧接的突发请求验证"规则在新窗口持续生效"——避免出现"用一次就废"的退化.
    print_block "[用例 1.2] QPS 新窗口重新放通 + 再次触发限流" \
        "操作:   等待 ${QPS_WINDOW_SECOND}s+1s（确保跨过 1.1 的限流窗口）→ 先发 1 次 /echo（验证放通）→ 立刻再串行发 ${QPS_TOTAL_REQUESTS} 次（验证再次限流）" \
        "原理:   QPS reject 按时间窗口计数；新窗口配额清零后，第 1 次请求 200；后续突发再次超过阈值 ${QPS_MAX_AMOUNT}/${QPS_WINDOW_SECOND}s → 429" \
        "预期:   单发 == 200；后续突发 ${QPS_TOTAL_REQUESTS} 次中 limited ≥ ${QPS_TOTAL_REQUESTS} - 2*${QPS_MAX_AMOUNT} = $((QPS_TOTAL_REQUESTS - 2 * QPS_MAX_AMOUNT))" \
        "判定:   单发 200 且突发 limited ≥ $((QPS_TOTAL_REQUESTS - 2 * QPS_MAX_AMOUNT)) && other == 0 → PASS"
    log_info "[用例 1.2] 等待 ${QPS_WINDOW_SECOND}s+1s 后先发 1 次 /echo"
    sleep $((QPS_WINDOW_SECOND))
    sleep 1
    local code
    code=$(curl -s -o /dev/null --connect-timeout 2 --max-time 5 \
        -w '%{http_code}' "http://127.0.0.1:${PORT_CONSUMER_QPS}/echo" 2>/dev/null || echo "000")
    if [[ "$code" != "200" ]]; then
        record_case "用例 1.2 QPS 新窗口再次生效" "FAIL" "新窗口首发请求期望 200, 实际 ${code}（窗口未重置？）"
        return
    fi

    log_info "[用例 1.2] 紧接再串行打 ${QPS_TOTAL_REQUESTS} 次 /echo（应再次触发限流）"
    local stat ok limited other
    stat=$(count_status "$PORT_CONSUMER_QPS" "/echo" "$QPS_TOTAL_REQUESTS")
    read -r ok limited other <<< "$stat"
    log_info "结果: 200=${ok}  429=${limited}  其他=${other}"
    local expected_min_limited=$((QPS_TOTAL_REQUESTS - 2 * QPS_MAX_AMOUNT))
    if [[ "$other" -gt 0 ]]; then
        record_case "用例 1.2 QPS 新窗口再次生效" "FAIL" "出现非 200/429 状态码 (other=${other})"
    elif [[ "$limited" -lt "$expected_min_limited" ]]; then
        record_case "用例 1.2 QPS 新窗口再次生效" "FAIL" \
            "新窗口下限流次数 ${limited} 不足 ${expected_min_limited}（规则没继续生效？）"
    else
        record_case "用例 1.2 QPS 新窗口再次生效" "PASS" \
            "新窗口首发 200，后续突发 limited=${limited}（≥${expected_min_limited}），规则持续生效"
    fi
}

# ======================== 用例 2.x：QPS 限流 - unirate（匀速排队） ========================
run_unirate_cases() {
    log_step "[用例 2.x] QPS 限流 - unirate（链路: curl → consumer:${PORT_CONSUMER_UNIRATE} → provider:${PORT_PROVIDER_UNIRATE}）"
    if ! create_unirate_rule; then
        record_case "2.0 创建 unirate 规则" "FAIL" "HTTP API 调用失败"
        return
    fi
    # 复用 provider-qps 二进制，但服务名/端口/run_dir 都换一份；这样 reject + unirate 两套 demo
    # 可以并存，互不干扰.
    if ! build_and_start_binary "provider-qps" "$PORT_PROVIDER_UNIRATE" \
        "$UNIRATE_SERVICE" "provider-qps-unirate" "PROVIDER_UNIRATE_PID"; then
        record_case "2.0 启动 unirate provider" "FAIL" "provider-qps (unirate) 启动失败"
        return
    fi
    if ! build_and_start_binary "consumer" "$PORT_CONSUMER_UNIRATE" \
        "$UNIRATE_SERVICE" "consumer-qps-unirate" "CONSUMER_UNIRATE_PID"; then
        record_case "2.0 启动 unirate consumer" "FAIL" "consumer (unirate) 启动失败"
        return
    fi

    log_info "等待 4s 让 SDK 拉到规则..."
    sleep 4

    # ---------- 用例 2.1：匀速排队总耗时 ----------
    # 有效速率 = UNIRATE_MAX_AMOUNT / UNIRATE_WINDOW_SECOND（每秒），
    # 串行 N 个请求时，第 1 个立即放过，后续每个间隔约 (1/rate)s，总耗时 ≈ (N-1)/rate.
    local interval_ms=$((1000 * UNIRATE_WINDOW_SECOND / UNIRATE_MAX_AMOUNT))
    local expected_min_ms=$(((UNIRATE_TOTAL_REQUESTS - 1) * interval_ms * 7 / 10))  # 70% 容忍下限
    local expected_max_ms=$((UNIRATE_TOTAL_REQUESTS * interval_ms * 2))             # 200% 容忍上限（含调度开销）
    print_block "[用例 2.1] unirate 匀速排队总耗时（链路 curl → consumer → provider）" \
        "操作:   串行向 http://127.0.0.1:${PORT_CONSUMER_UNIRATE}/echo 发起 ${UNIRATE_TOTAL_REQUESTS} 次 GET" \
        "链路:   curl → consumer:${PORT_CONSUMER_UNIRATE}（服务发现 → ）provider:${PORT_PROVIDER_UNIRATE}/echo" \
        "原理:   unirate 让 SDK 把超出速率的请求排队等待（QuotaFutureImpl.Get 内部 sleep 直到下个配额到期）；" \
        "        每个请求间隔约 ${interval_ms}ms，串行 ${UNIRATE_TOTAL_REQUESTS} 个的总耗时约 $(((UNIRATE_TOTAL_REQUESTS - 1) * interval_ms))ms" \
        "预期:   全部 200（不会因为速率超限而拒绝），总耗时 ∈ [${expected_min_ms}ms, ${expected_max_ms}ms]" \
        "判定:   200 == ${UNIRATE_TOTAL_REQUESTS} && 429 == 0 && other == 0 && 总耗时 ≥ ${expected_min_ms}ms"

    log_info "[用例 2.1] 串行打 ${UNIRATE_TOTAL_REQUESTS} 次 /echo（速率 ${UNIRATE_MAX_AMOUNT}/${UNIRATE_WINDOW_SECOND}s = $((1000 / interval_ms)) QPS）"
    local start_ms end_ms elapsed_ms
    # 使用 python3 拿毫秒时间戳，避免 BSD date（macOS）不支持 %N 的兼容性问题：
    # macOS 上 `date +%s%3N` 不会失败，会输出形如 "17792676503N" 的错误字符串导致后续算术失败.
    start_ms=$(python3 -c 'import time; print(int(time.time()*1000))')
    local stat ok limited other
    stat=$(count_status "$PORT_CONSUMER_UNIRATE" "/echo" "$UNIRATE_TOTAL_REQUESTS")
    end_ms=$(python3 -c 'import time; print(int(time.time()*1000))')
    elapsed_ms=$((end_ms - start_ms))
    read -r ok limited other <<< "$stat"
    log_info "结果: 200=${ok}  429=${limited}  其他=${other}  总耗时=${elapsed_ms}ms（期望 ≥${expected_min_ms}ms）"

    if [[ "$other" -gt 0 ]] || [[ "$limited" -gt 0 ]]; then
        record_case "用例 2.1 unirate 匀速排队" "FAIL" \
            "期望全部 200（429=${limited} other=${other}），unirate 不应该立刻拒绝"
    elif [[ "$ok" -ne "$UNIRATE_TOTAL_REQUESTS" ]]; then
        record_case "用例 2.1 unirate 匀速排队" "FAIL" \
            "通过数 ${ok} 不等于总请求数 ${UNIRATE_TOTAL_REQUESTS}"
    elif [[ "$elapsed_ms" -lt "$expected_min_ms" ]]; then
        record_case "用例 2.1 unirate 匀速排队" "FAIL" \
            "总耗时 ${elapsed_ms}ms 小于期望下限 ${expected_min_ms}ms（unirate 没生效？请求未被排队）"
    else
        record_case "用例 2.1 unirate 匀速排队" "PASS" \
            "200=${ok}/${UNIRATE_TOTAL_REQUESTS}，总耗时=${elapsed_ms}ms（≥${expected_min_ms}ms）"
    fi

    # ---------- 用例 2.2：超出 maxQueueDelay 后队列丢弃（429） ----------
    # !!! 必须并发发起，串行不会触发丢弃路径 !!!
    # unirate.allocateQuota 用 lastGrantTime 累积；串行场景下，每发完一个 SDK 就 sleep ~costDuration，
    # 等下一次 GetQuota 调用时 currentTime 已追上 expectedTime，waitMs 始终 ≈ costDuration，永远 ≤ maxQueueDelay.
    # 只有"几乎同时"发起 N 个 GetQuota，第 i 次的 expectedTime 才会累积到 (i-1)*costDuration > maxQueueDelay.
    # 修复历史：参见 .build/provider-qps-unirate/polaris/log/ratelimit/polaris-ratelimit.log，
    # 串行 6 次时 waitMs 都在 400~500ms（< 1000ms），全部排队成功为 200，2.2 必然 FAIL.
    # UNIRATE_BURST_REQUESTS=6, costDuration=500ms, maxQueueDelay=1000ms：
    #   i=1 等 0     —— 直通 200
    #   i=2 等 500ms ≤ 1000ms —— 排队 200
    #   i=3 等 1000ms ≤ 1000ms —— 临界 200（实测可能略大被拒）
    #   i=4..6 等 1500/2000/2500ms 全部 > 1000ms —— 拒绝 429
    local burst_min_limited=2  # 至少 i=5,6 必拒；为容忍边界把下界设为 2
    print_block "[用例 2.2] 队列等待超过 maxQueueDelay 触发丢弃（429）" \
        "操作:   并发（同时启动）向 http://127.0.0.1:${PORT_CONSUMER_UNIRATE}/echo 发起 ${UNIRATE_BURST_REQUESTS} 次 GET" \
        "原理:   maxQueueDelay=${UNIRATE_MAX_QUEUE_DELAY_SEC}s=${UNIRATE_MAX_QUEUE_DELAY_SEC}000ms；并发触发让 SDK 在极短时间内连续调 GetQuota，" \
        "        第 i 次的等待时间 = (i-1) * costDuration；超过 maxQueueDelay 后 SDK 立即返回 RateLimit（HTTP 429）；" \
        "        前几个请求仍走匀速排队 200，靠后请求等待超阈值 → 429" \
        "预期:   200 数量在 [1, ${UNIRATE_BURST_REQUESTS}] 之间，且 limited ≥ ${burst_min_limited}，无 other 状态码" \
        "判定:   limited ≥ ${burst_min_limited} && other == 0 && (200 + 429) == ${UNIRATE_BURST_REQUESTS}"

    log_info "[用例 2.2] 并发 ${UNIRATE_BURST_REQUESTS} 次 /echo（速率 ${UNIRATE_MAX_AMOUNT}/${UNIRATE_WINDOW_SECOND}s，maxQueueDelay=${UNIRATE_MAX_QUEUE_DELAY_SEC}s）"
    stat=$(count_status_concurrent "$PORT_CONSUMER_UNIRATE" "/echo" "$UNIRATE_BURST_REQUESTS")
    read -r ok limited other <<< "$stat"
    log_info "结果: 200=${ok}  429=${limited}  其他=${other}"

    if [[ "$other" -gt 0 ]]; then
        record_case "用例 2.2 unirate 队列丢弃" "FAIL" \
            "出现非 200/429 状态码（other=${other}）"
    elif [[ $((ok + limited)) -ne "$UNIRATE_BURST_REQUESTS" ]]; then
        record_case "用例 2.2 unirate 队列丢弃" "FAIL" \
            "200+429=$((ok+limited)) 不等于总请求数 ${UNIRATE_BURST_REQUESTS}"
    elif [[ "$limited" -lt "$burst_min_limited" ]]; then
        record_case "用例 2.2 unirate 队列丢弃" "FAIL" \
            "limited=${limited} 不足 ${burst_min_limited}（maxQueueDelay 没生效？请求都被排队成功了——是否串行调用了？unirate 串行场景下 waitMs 不会累积）"
    else
        record_case "用例 2.2 unirate 队列丢弃" "PASS" \
            "200=${ok} 429=${limited}（≥${burst_min_limited}），队列等待超 ${UNIRATE_MAX_QUEUE_DELAY_SEC}s 被拒"
    fi

    # ---------- 用例 2.3：旧队列消散后，新一轮突发仍按规则限流 ----------
    # 验证 unirate 不是"用一次就废"——上一轮 burst 留下的 lastGrantTime 在 wait 期间会随当前时间被
    # CompareAndSwap 重置；新一轮请求不会因为"旧账"导致全部直通或全部被拒，应当继续按原速率匀速 + 排队 + 丢弃.
    # 等待时间 ≥ (BURST_REQUESTS-1)*interval_ms，让 lastGrantTime 充分回归.
    local cooldown_ms=$(((UNIRATE_BURST_REQUESTS - 1) * interval_ms + 500))
    local cooldown_sec=$(awk -v ms="$cooldown_ms" 'BEGIN{ printf "%.1f", ms/1000.0 }')
    print_block "[用例 2.3] 新窗口（队列消散）后再次触发限流" \
        "操作:   sleep ${cooldown_sec}s 让上一轮排队彻底耗尽，再并发打 ${UNIRATE_BURST_REQUESTS} 次 /echo（与 2.2 同等模式）" \
        "原理:   匀速排队限流器只持有一个 lastGrantTime；冷却后该值已"过期"，新一轮请求会被当作首批进入：" \
        "        i=1 直通 200、i=2..3 排队 200、i=4..6 等待超阈值 → 429。" \
        "        若新窗口未生效，会出现"全部 200"或"全部 429"的异常情况" \
        "预期:   limited ≥ ${burst_min_limited}（与 2.2 一致），200+429 == ${UNIRATE_BURST_REQUESTS}" \
        "判定:   limited ≥ ${burst_min_limited} && other == 0 && ok ≥ 1 → PASS"

    log_info "[用例 2.3] 等待 ${cooldown_sec}s 让排队消散..."
    sleep "$cooldown_sec"
    log_info "[用例 2.3] 再次并发打 ${UNIRATE_BURST_REQUESTS} 次 /echo"
    stat=$(count_status_concurrent "$PORT_CONSUMER_UNIRATE" "/echo" "$UNIRATE_BURST_REQUESTS")
    read -r ok limited other <<< "$stat"
    log_info "结果: 200=${ok}  429=${limited}  其他=${other}"

    if [[ "$other" -gt 0 ]]; then
        record_case "用例 2.3 unirate 新窗口再次限流" "FAIL" \
            "出现非 200/429 状态码（other=${other}）"
    elif [[ $((ok + limited)) -ne "$UNIRATE_BURST_REQUESTS" ]]; then
        record_case "用例 2.3 unirate 新窗口再次限流" "FAIL" \
            "200+429=$((ok+limited)) 不等于总请求数 ${UNIRATE_BURST_REQUESTS}"
    elif [[ "$ok" -lt 1 ]]; then
        record_case "用例 2.3 unirate 新窗口再次限流" "FAIL" \
            "新窗口下应至少有 1 个请求 200（实际 ok=0，可能是 lastGrantTime 没复位）"
    elif [[ "$limited" -lt "$burst_min_limited" ]]; then
        record_case "用例 2.3 unirate 新窗口再次限流" "FAIL" \
            "limited=${limited} 不足 ${burst_min_limited}，规则可能没继续生效"
    else
        record_case "用例 2.3 unirate 新窗口再次限流" "PASS" \
            "冷却后 200=${ok} 429=${limited}（≥${burst_min_limited}），规则持续生效"
    fi
}

# ======================== 用例 3.x：并发数限流 ========================
run_concurrency_cases() {
    log_step "[用例 3.x] 并发数限流 (链路: curl → consumer:${PORT_CONSUMER_CONCURRENCY} → provider:${PORT_PROVIDER_CONCURRENCY})"
    if ! create_concurrency_rule; then
        record_case "3.0 创建并发数规则" "FAIL" "HTTP API 调用失败"
        return
    fi
    if ! build_and_start_binary "provider-concurrency" "$PORT_PROVIDER_CONCURRENCY" \
        "$CONCURRENCY_SERVICE" "provider-concurrency" "PROVIDER_CONC_PID"; then
        record_case "3.0 启动并发 provider" "FAIL" "provider-concurrency 启动失败"
        return
    fi
    if ! build_and_start_binary "consumer" "$PORT_CONSUMER_CONCURRENCY" \
        "$CONCURRENCY_SERVICE" "consumer-concurrency" "CONSUMER_CONC_PID"; then
        record_case "3.0 启动并发 consumer" "FAIL" "consumer (concurrency) 启动失败"
        return
    fi

    log_info "等待 4s 让 SDK 拉到规则..."
    sleep 4

    # ---------- 用例 3.1：并发触发限流 ----------
    print_block "[用例 3.1] 并发数触发限流（链路 curl → consumer → provider）" \
        "操作:   并发（同时）向 http://127.0.0.1:${PORT_CONSUMER_CONCURRENCY}/slow?ms=${CONCURRENCY_SLOW_MS} 发起 ${CONCURRENCY_TOTAL_REQUESTS} 次 GET" \
        "链路:   curl → consumer:${PORT_CONSUMER_CONCURRENCY}（服务发现 → ）provider:${PORT_PROVIDER_CONCURRENCY}/slow" \
        "原理:   /slow 接口会 sleep ${CONCURRENCY_SLOW_MS}ms 模拟长耗时业务；上限 ${CONCURRENCY_MAX_AMOUNT} 表示同时只能有 ${CONCURRENCY_MAX_AMOUNT} 个请求处理中" \
        "预期:   恰好 ${CONCURRENCY_MAX_AMOUNT} 个 200（占满并发），$((CONCURRENCY_TOTAL_REQUESTS - CONCURRENCY_MAX_AMOUNT)) 个 429（超出立即拒绝）" \
        "判定:   200 数量 ≤ ${CONCURRENCY_MAX_AMOUNT} 且 429 数量 ≥ $((CONCURRENCY_TOTAL_REQUESTS - CONCURRENCY_MAX_AMOUNT))，无其它状态码"
    log_info "[用例 3.1] 并发 ${CONCURRENCY_TOTAL_REQUESTS} 个 /slow?ms=${CONCURRENCY_SLOW_MS}（上限 ${CONCURRENCY_MAX_AMOUNT}）"
    local stat ok limited other
    stat=$(count_status_concurrent "$PORT_CONSUMER_CONCURRENCY" \
        "/slow?ms=${CONCURRENCY_SLOW_MS}" "$CONCURRENCY_TOTAL_REQUESTS")
    read -r ok limited other <<< "$stat"
    log_info "结果: 200=${ok}  429=${limited}  其他=${other}"

    # 期望：通过数 ≤ maxAmount，限流数 ≥ (total - maxAmount)
    if [[ "$other" -gt 0 ]]; then
        record_case "用例 3.1 并发数触发限流" "FAIL" "出现非 200/429 状态码 (other=${other})"
    elif [[ "$ok" -le "$CONCURRENCY_MAX_AMOUNT" ]] && \
         [[ "$limited" -ge $((CONCURRENCY_TOTAL_REQUESTS - CONCURRENCY_MAX_AMOUNT)) ]]; then
        record_case "用例 3.1 并发数触发限流" "PASS" \
            "200=${ok}≤${CONCURRENCY_MAX_AMOUNT}, 429=${limited}≥$((CONCURRENCY_TOTAL_REQUESTS - CONCURRENCY_MAX_AMOUNT))"
    else
        record_case "用例 3.1 并发数触发限流" "FAIL" \
            "200=${ok}（期望≤${CONCURRENCY_MAX_AMOUNT}）429=${limited}（期望≥$((CONCURRENCY_TOTAL_REQUESTS - CONCURRENCY_MAX_AMOUNT))）"
    fi

    # ---------- 用例 3.2：Release 归还后再次能触发限流（验证规则持续生效） ----------
    # 上一批最长 sleep 时间 + 缓冲，确保所有 in-flight 请求都已 Release
    local recover_wait_ms=$((CONCURRENCY_SLOW_MS + 1500))
    local expected_min_limited_3_2=$((CONCURRENCY_TOTAL_REQUESTS - CONCURRENCY_MAX_AMOUNT))
    print_block "[用例 3.2] Release 归还后放通 + 再次触发限流" \
        "操作:   等待 $((recover_wait_ms / 1000))s（让 3.1 in-flight /slow 自然 Release）→ 先发 1 次 /slow?ms=200（验证放通）→ 再并发 ${CONCURRENCY_TOTAL_REQUESTS} 个 /slow?ms=1500（验证再次限流）" \
        "原理:   provider defer future.Release() 把并发计数 -1；正确实现下计数回到 0；新一轮并发突发再次超过 maxAmount=${CONCURRENCY_MAX_AMOUNT} 时仍应触发 429" \
        "预期:   单发 200；后续并发 ${CONCURRENCY_TOTAL_REQUESTS} 次中 ok ≤ ${CONCURRENCY_MAX_AMOUNT} 且 limited ≥ ${expected_min_limited_3_2}" \
        "判定:   单发 200 且突发 ok ≤ ${CONCURRENCY_MAX_AMOUNT} && limited ≥ ${expected_min_limited_3_2} && other == 0 → PASS（验证 Release 正常 + 规则持续生效）"
    log_info "[用例 3.2] 等待 $((recover_wait_ms / 1000))s 让上一批请求释放配额，先发 1 个 /slow?ms=200"
    sleep $((recover_wait_ms / 1000))
    local code
    code=$(curl -s -o /dev/null --connect-timeout 2 --max-time 5 \
        -w '%{http_code}' "http://127.0.0.1:${PORT_CONSUMER_CONCURRENCY}/slow?ms=200" 2>/dev/null || echo "000")
    if [[ "$code" != "200" ]]; then
        record_case "用例 3.2 Release 后再次限流" "FAIL" \
            "Release 归还后首发期望 200, 实际 ${code}（main.go 漏写 defer 或 SDK 回调链断裂？）"
    else
        # 等首发 200 的请求结束，再做并发突发
        sleep 1
        log_info "[用例 3.2] 紧接并发 ${CONCURRENCY_TOTAL_REQUESTS} 个 /slow?ms=1500（应再次触发限流）"
        local stat ok limited other
        stat=$(count_status_concurrent "$PORT_CONSUMER_CONCURRENCY" \
            "/slow?ms=${CONCURRENCY_SLOW_MS}" "$CONCURRENCY_TOTAL_REQUESTS")
        read -r ok limited other <<< "$stat"
        log_info "结果: 200=${ok}  429=${limited}  其他=${other}"
        if [[ "$other" -gt 0 ]]; then
            record_case "用例 3.2 Release 后再次限流" "FAIL" "出现非 200/429 状态码 (other=${other})"
        elif [[ "$ok" -gt "$CONCURRENCY_MAX_AMOUNT" ]]; then
            record_case "用例 3.2 Release 后再次限流" "FAIL" \
                "新一轮并发 ok=${ok} > maxAmount=${CONCURRENCY_MAX_AMOUNT}（限流没生效？）"
        elif [[ "$limited" -lt "$expected_min_limited_3_2" ]]; then
            record_case "用例 3.2 Release 后再次限流" "FAIL" \
                "新一轮并发 limited=${limited} 不足 ${expected_min_limited_3_2}（规则没继续生效？）"
        else
            record_case "用例 3.2 Release 后再次限流" "PASS" \
                "Release 后首发 200，新一轮并发 ok=${ok}≤${CONCURRENCY_MAX_AMOUNT}, 429=${limited}（≥${expected_min_limited_3_2}），规则持续生效"
        fi
    fi

    # 等到上一批 in-flight /slow 都结束，避免污染 3.3
    sleep $((CONCURRENCY_SLOW_MS / 1000 + 1))

    # ---------- 用例 3.3：低于上限的并发应全部放通 ----------
    print_block "[用例 3.3] 低于上限全放通" \
        "操作:   并发 ${CONCURRENCY_BELOW_LIMIT_REQ} 个 /slow?ms=600（≤ 上限 ${CONCURRENCY_MAX_AMOUNT}），打到 consumer:${PORT_CONSUMER_CONCURRENCY}" \
        "原理:   并发数低于上限时，所有请求都应正常通过；这是反向验证（确保限流不会误伤）" \
        "预期:   全部 200，0 个 429，0 个其它" \
        "判定:   200 数量 == ${CONCURRENCY_BELOW_LIMIT_REQ} 且 429==0 && other==0"
    log_info "[用例 3.3] 并发 ${CONCURRENCY_BELOW_LIMIT_REQ} 个 /slow?ms=600（≤上限 ${CONCURRENCY_MAX_AMOUNT}）"
    stat=$(count_status_concurrent "$PORT_CONSUMER_CONCURRENCY" \
        "/slow?ms=600" "$CONCURRENCY_BELOW_LIMIT_REQ")
    read -r ok limited other <<< "$stat"
    log_info "结果: 200=${ok}  429=${limited}  其他=${other}"
    if [[ "$ok" == "$CONCURRENCY_BELOW_LIMIT_REQ" ]] && [[ "$limited" == "0" ]] && [[ "$other" == "0" ]]; then
        record_case "用例 3.3 低于上限全放通" "PASS" "200=${ok}/${CONCURRENCY_BELOW_LIMIT_REQ}"
    else
        record_case "用例 3.3 低于上限全放通" "FAIL" "200=${ok} 429=${limited} other=${other}"
    fi
}

# ======================== 用例 4.x：自定义多维匹配规则（AND） ========================
run_custom_match_cases() {
    log_step "[用例 4.x] 自定义多维匹配规则 (链路: curl → consumer:${PORT_CONSUMER_CUSTOM} → provider:${PORT_PROVIDER_CUSTOM})"
    if ! create_custom_match_rule; then
        record_case "4.0 创建自定义匹配规则" "FAIL" "HTTP API 调用失败"
        return
    fi
    # 复用 provider-qps 二进制，--service 指向 CustomMatchEchoServer
    if ! build_and_start_binary "provider-qps" "$PORT_PROVIDER_CUSTOM" \
        "$CUSTOM_SERVICE" "provider-qps-custom" "PROVIDER_CUSTOM_PID"; then
        record_case "4.0 启动 custom provider" "FAIL" "provider-qps (custom) 启动失败"
        return
    fi
    # consumer 启动时把"主调身份"通过 --caller-* 注入：
    #   --caller-service: ns/svc 形式
    #   --caller-ip:      固定 IP（覆盖默认的 RemoteAddr）
    #   --caller-metadata: 多个 k=v
    if ! build_and_start_binary "consumer" "$PORT_CONSUMER_CUSTOM" \
        "$CUSTOM_SERVICE" "consumer-custom" "CONSUMER_CUSTOM_PID" \
        --caller-service "${CUSTOM_CALLER_SERVICE_NS}/${CUSTOM_CALLER_SERVICE_SVC}" \
        --caller-ip "${CUSTOM_CALLER_IP}" \
        --caller-metadata "${CUSTOM_CALLER_META_KEY}=${CUSTOM_CALLER_META_VALUE}"; then
        record_case "4.0 启动 custom consumer" "FAIL" "consumer (custom) 启动失败"
        return
    fi

    log_info "等待 4s 让 SDK 拉到规则..."
    sleep 4

    # ---------- 用例 4.1：5 条件全部命中，触发限流 ----------
    print_block "[用例 4.1] 自定义匹配规则全部命中触发限流" \
        "操作:   串行向 consumer:${PORT_CONSUMER_CUSTOM}/echo?${CUSTOM_QUERY_KEY}=${CUSTOM_QUERY_VALUE} 发 ${CUSTOM_TOTAL_REQUESTS} 次，并带 H '${CUSTOM_HEADER_KEY}: ${CUSTOM_HEADER_VALUE}'" \
        "原理:   curl 提供 HEADER+QUERY；consumer 注入 CALLER_SERVICE/IP/METADATA → provider buildQuotaRequest 还原 5 类维度 → 全部命中规则" \
        "预期:   limit ≥ ${CUSTOM_TOTAL_REQUESTS} - 2*${CUSTOM_MAX_AMOUNT}（容忍 SDK 拉规则 1s 时延），无非 200/429 状态码" \
        "判定:   429 ≥ $((CUSTOM_TOTAL_REQUESTS - 2 * CUSTOM_MAX_AMOUNT)) 即 PASS"
    log_info "[用例 4.1] 串行打 ${CUSTOM_TOTAL_REQUESTS} 次（全条件匹配）"
    local stat ok limited other
    stat=$(count_status_with_header "$PORT_CONSUMER_CUSTOM" \
        "/echo?${CUSTOM_QUERY_KEY}=${CUSTOM_QUERY_VALUE}" "$CUSTOM_TOTAL_REQUESTS" \
        "${CUSTOM_HEADER_KEY}: ${CUSTOM_HEADER_VALUE}")
    read -r ok limited other <<< "$stat"
    log_info "结果: 200=${ok}  429=${limited}  其他=${other}"

    local expected_min_limited=$((CUSTOM_TOTAL_REQUESTS - 2 * CUSTOM_MAX_AMOUNT))
    [[ $expected_min_limited -lt 1 ]] && expected_min_limited=1
    if [[ "$other" -gt 0 ]]; then
        record_case "用例 4.1 全条件命中触发限流" "FAIL" "出现非 200/429 状态码 (other=${other})"
    elif [[ "$limited" -ge "$expected_min_limited" ]]; then
        record_case "用例 4.1 全条件命中触发限流" "PASS" \
            "限流 ${limited} 次 (≥${expected_min_limited})，通过 ${ok} 次"
    else
        record_case "用例 4.1 全条件命中触发限流" "FAIL" \
            "限流次数 ${limited} 不足，期望 ≥${expected_min_limited}（200=${ok} 429=${limited}）"
    fi

    # 等过 1 个 QPS 窗口，让 4.2 从干净状态开始
    sleep $((CUSTOM_WINDOW_SECOND + 1))

    # ---------- 用例 4.2：query 不匹配，规则跳过 ----------
    print_block "[用例 4.2] 单维不匹配则规则不命中（反向验证 AND 语义）" \
        "操作:   串行向 consumer:${PORT_CONSUMER_CUSTOM}/echo?${CUSTOM_QUERY_KEY}=cn-west 发 ${CUSTOM_TOTAL_REQUESTS} 次（query 故意不匹配，其它 4 维度仍然匹配）" \
        "原理:   规则的 5 个 arguments 是 AND 关系，任一不命中整条规则跳过；预期结果是请求全部放行" \
        "预期:   全部 200，无 429" \
        "判定:   200 == ${CUSTOM_TOTAL_REQUESTS} && 429 == 0 → PASS（说明 AND 语义正确）"
    log_info "[用例 4.2] 串行打 ${CUSTOM_TOTAL_REQUESTS} 次（query 故意不匹配）"
    stat=$(count_status_with_header "$PORT_CONSUMER_CUSTOM" \
        "/echo?${CUSTOM_QUERY_KEY}=cn-west" "$CUSTOM_TOTAL_REQUESTS" \
        "${CUSTOM_HEADER_KEY}: ${CUSTOM_HEADER_VALUE}")
    read -r ok limited other <<< "$stat"
    log_info "结果: 200=${ok}  429=${limited}  其他=${other}"
    if [[ "$ok" == "$CUSTOM_TOTAL_REQUESTS" ]] && [[ "$limited" == "0" ]] && [[ "$other" == "0" ]]; then
        record_case "用例 4.2 单维不匹配规则跳过" "PASS" "200=${ok}/${CUSTOM_TOTAL_REQUESTS}"
    else
        record_case "用例 4.2 单维不匹配规则跳过" "FAIL" "200=${ok} 429=${limited} other=${other}（期望全 200）"
    fi

    # 等过 1 个 QPS 窗口，让 4.3 从干净状态开始
    sleep $((CUSTOM_WINDOW_SECOND + 1))

    # ---------- 用例 4.3：新窗口下规则仍按相同模式触发限流 ----------
    # 验证自定义匹配规则在 QPS 窗口重置后持续生效——与用例 1.2/3.2 形成完整对照（每种限流方案都有"新窗口再次生效"语义）.
    print_block "[用例 4.3] 自定义匹配规则在新窗口仍能触发限流" \
        "操作:   等待 ${CUSTOM_WINDOW_SECOND}s+1s 跨过 4.1 的限流窗口，再次串行向 consumer:${PORT_CONSUMER_CUSTOM}/echo?${CUSTOM_QUERY_KEY}=${CUSTOM_QUERY_VALUE} 发 ${CUSTOM_TOTAL_REQUESTS} 次（同 4.1 的全命中模式）" \
        "原理:   自定义匹配规则底层走 QPS reject 限流，按时间窗口计数；新窗口配额清零后，命中规则的请求继续按 ${CUSTOM_MAX_AMOUNT}/${CUSTOM_WINDOW_SECOND}s 限流" \
        "预期:   429 ≥ ${CUSTOM_TOTAL_REQUESTS} - 2*${CUSTOM_MAX_AMOUNT} = $((CUSTOM_TOTAL_REQUESTS - 2 * CUSTOM_MAX_AMOUNT))，无非 200/429 状态码" \
        "判定:   429 ≥ $((CUSTOM_TOTAL_REQUESTS - 2 * CUSTOM_MAX_AMOUNT)) && other == 0 → PASS（验证规则持续生效，不会"用一次就废"）"
    log_info "[用例 4.3] 串行打 ${CUSTOM_TOTAL_REQUESTS} 次（全条件匹配，验证新窗口）"
    stat=$(count_status_with_header "$PORT_CONSUMER_CUSTOM" \
        "/echo?${CUSTOM_QUERY_KEY}=${CUSTOM_QUERY_VALUE}" "$CUSTOM_TOTAL_REQUESTS" \
        "${CUSTOM_HEADER_KEY}: ${CUSTOM_HEADER_VALUE}")
    read -r ok limited other <<< "$stat"
    log_info "结果: 200=${ok}  429=${limited}  其他=${other}"
    local custom_min_limited_4_3=$((CUSTOM_TOTAL_REQUESTS - 2 * CUSTOM_MAX_AMOUNT))
    [[ $custom_min_limited_4_3 -lt 1 ]] && custom_min_limited_4_3=1
    if [[ "$other" -gt 0 ]]; then
        record_case "用例 4.3 新窗口再次触发限流" "FAIL" "出现非 200/429 状态码 (other=${other})"
    elif [[ "$limited" -ge "$custom_min_limited_4_3" ]]; then
        record_case "用例 4.3 新窗口再次触发限流" "PASS" \
            "新窗口限流 ${limited} 次 (≥${custom_min_limited_4_3})，通过 ${ok} 次，规则持续生效"
    else
        record_case "用例 4.3 新窗口再次触发限流" "FAIL" \
            "新窗口限流次数 ${limited} 不足，期望 ≥${custom_min_limited_4_3}（200=${ok} 429=${limited}）"
    fi
}

# ======================== 收尾清理 ========================
# 仅停止脚本启动的 provider 进程；限流规则不删除（设计上规则永久保留，复用即可）.
cleanup() {
    if [[ "$KEEP_RESOURCES" == "true" ]]; then
        log_warn "--keep 已开启，保留 provider 进程和日志"
        return
    fi
    log_step "清理资源（仅停止 provider/consumer 进程；规则保留）"
    stop_provider "$PROVIDER_QPS_PID"
    stop_provider "$PROVIDER_UNIRATE_PID"
    stop_provider "$PROVIDER_CONC_PID"
    stop_provider "$PROVIDER_CUSTOM_PID"
    stop_provider "$CONSUMER_QPS_PID"
    stop_provider "$CONSUMER_UNIRATE_PID"
    stop_provider "$CONSUMER_CONC_PID"
    stop_provider "$CONSUMER_CUSTOM_PID"
    log_info "已停止 provider/consumer；限流规则保留以供下次复用"
}
trap cleanup EXIT

# ======================== 依赖检查 ========================
require_cmd() {
    local cmd="$1"
    if ! command -v "$cmd" >/dev/null 2>&1; then
        log_error "缺少依赖命令: $cmd"
        exit 1
    fi
}
require_cmd go
require_cmd python3
require_cmd curl

# ======================== 主流程 ========================
main() {
    echo ""
    echo -e "${BLUE}╔═══════════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║         examples/ratelimit 限流端到端验证脚本                    ║${NC}"
    echo -e "${BLUE}╚═══════════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "Polaris 服务端:    ${POLARIS_SERVER} (HTTP=${POLARIS_HTTP_ADDR})"
    echo "命名空间:          ${NAMESPACE}"
    echo "QPS 服务:          ${QPS_SERVICE}, provider:${PORT_PROVIDER_QPS} consumer:${PORT_CONSUMER_QPS}（reject 策略）"
    echo "Unirate 服务:      ${UNIRATE_SERVICE}, provider:${PORT_PROVIDER_UNIRATE} consumer:${PORT_CONSUMER_UNIRATE}（unirate 策略）"
    echo "并发服务:          ${CONCURRENCY_SERVICE}, provider:${PORT_PROVIDER_CONCURRENCY} consumer:${PORT_CONSUMER_CONCURRENCY}"
    echo "自定义匹配服务:    ${CUSTOM_SERVICE}, provider:${PORT_PROVIDER_CUSTOM} consumer:${PORT_CONSUMER_CUSTOM}（多维 AND 匹配）"
    echo "请求链路:          curl → consumer → provider（consumer 负责服务发现 + HTTP 转发）"
    echo "skip 列表:         ${SKIP_LIST:-（无）}"
    echo "保留资源:          ${KEEP_RESOURCES}"
    echo "日志文件:          ${LOG_FILE}"
    echo ""

    # --debug 透传由 build_and_start_binary 完成（追加 --debug flag），此处无需额外处理.

    if ! is_skipped "qps"; then
        run_qps_cases
    else
        log_warn "跳过 QPS 用例 (--skip qps)"
    fi

    if ! is_skipped "unirate"; then
        run_unirate_cases
    else
        log_warn "跳过 unirate 用例 (--skip unirate)"
    fi

    if ! is_skipped "concurrency"; then
        run_concurrency_cases
    else
        log_warn "跳过并发数用例 (--skip concurrency)"
    fi

    if ! is_skipped "custom"; then
        run_custom_match_cases
    else
        log_warn "跳过自定义匹配规则用例 (--skip custom)"
    fi

    # ==================== 汇总 ====================
    log_step "用例汇总"
    local n=${#CASE_NAMES[@]}
    local i=0 pass=0 fail=0
    while [[ $i -lt $n ]]; do
        local name="${CASE_NAMES[$i]}"
        local verdict="${CASE_VERDICTS[$i]}"
        local detail="${CASE_DETAILS[$i]}"
        case "$verdict" in
            PASS) pass=$((pass+1)); printf "  ${GREEN}✅ %-40s${NC} %s\n" "$name" "$detail" ;;
            FAIL) fail=$((fail+1)); printf "  ${RED}❌ %-40s${NC} %s\n" "$name" "$detail" ;;
            WARN) printf "  ${YELLOW}⚠️  %-40s${NC} %s\n" "$name" "$detail" ;;
        esac
        i=$((i+1))
    done
    echo ""
    echo "  通过 ${pass} / ${n}, 失败 ${fail}"
    echo ""

    if [[ "$n" -eq 0 ]]; then
        echo -e "${YELLOW}验证结论: ⚠️  未运行任何用例（请检查 --skip）${NC}"
        exit 0
    fi
    if [[ "$fail" -eq 0 ]]; then
        echo -e "${GREEN}验证结论: ✅ 全部 ${n} 个用例通过${NC}"
        exit 0
    fi
    echo -e "${RED}验证结论: ❌ ${fail} 个用例失败${NC}"
    exit "$fail"
}

main "$@"
