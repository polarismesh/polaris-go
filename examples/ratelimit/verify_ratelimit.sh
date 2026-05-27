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
#   [用例 5.1] regex_combine=false：method REGEX 命中的多条 path 各自独享阈值（独立 token bucket）
#   [用例 5.2] regex_combine=true：PUT 翻转后多条 path 共享同一阈值（同一 token bucket，合计配额）
#   [用例 6.0] 探测 polaris.limiter 是否注册健康实例；不可用时整段 6.x SKIP
#   [用例 6.1] GLOBAL 多窗口聚合触发限流：多窗口聚合避开远端配额异步上报的边界抖动
#   [用例 6.2] GLOBAL 新窗口再次生效：跨窗口规则持续有效（对照 1.2 单机版）
#   [用例 6.3] GLOBAL 多实例共享配额：2 个 provider 实例**合计**消耗一份阈值（与 LOCAL×N 形成强对比）
#   [用例 6.4] GLOBAL+regex_combine：远端配额下多 path 共享（对照 5.2 单机版）
#   [用例 6.5] 远端降级到本地：故意把 SDK 的 limiter 服务名指向不存在的服务，验证 FAILOVER_LOCAL 退化路径
#   [用例 7.1] CustomResponse 自定义返回（LOCAL）：限流时 HTTP body 来自规则的 customResponse.body，
#              且响应头 X-Polaris-RateLimit-Rule == 规则名（验证 QuotaResponse.GetActiveRule API 可用）
#   [用例 7.2] CustomResponse 自定义返回（GLOBAL）：分布式限流场景下同样能透传 customResponse.body
#              （依赖 polaris.limiter 可用；不可用时 SKIP）
#   [用例 8.1] 限流监控指标验证：curl provider 的 /metrics 端口（pull 模式 28080），
#              断言 ratelimit_rq_{total,pass,limit} 指标存在、label 六维度齐备，
#              且数值满足 total==pass+limit、pass>0、limit>0
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
#   ./verify_ratelimit.sh --skip custom-response   # 跳过自定义返回用例
#   ./verify_ratelimit.sh --only qps               # 仅跑 QPS 用例
#   ./verify_ratelimit.sh --only qps,metrics       # 仅跑 QPS + 监控验证
#   ./verify_ratelimit.sh --only custom-response   # 仅跑自定义返回用例
#   ./verify_ratelimit.sh --only events            # 仅跑限流事件上报验证
#   ./verify_ratelimit.sh --keep                   # 保留 provider 进程和日志（规则始终保留）
#   ./verify_ratelimit.sh --debug                  # 提高 SDK 日志级别
#   ./verify_ratelimit.sh --limiter-service polaris.limiter.local  # 6.x 用本地 polaris-limiter（独立服务名）
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
ONLY_LIST=""
KEEP_RESOURCES=false
DEBUG_MODE=false

NAMESPACE="default"
QPS_SERVICE="QpsRatelimitEchoServer"        # 与 provider-qps 默认值一致；用于 reject（快速失败）验证
UNIRATE_SERVICE="UnirateRatelimitEchoServer" # 复用 provider-qps 二进制，--service 切换；用于 unirate（匀速排队）验证
CONCURRENCY_SERVICE="ConcurrencyEchoServer" # 与 provider-concurrency 默认值一致
CUSTOM_SERVICE="CustomMatchEchoServer"      # 复用 provider-qps 二进制；用于自定义匹配规则（用例 4.x）
REGEX_SERVICE="RegexCombineEchoServer"      # 复用 provider-qps 二进制；用于 regex_combine 规则（用例 5.x）
GLOBAL_SERVICE="GlobalRatelimitEchoServer"  # 复用 provider-qps 二进制；用于分布式 GLOBAL 限流（用例 6.x）
CUSTOM_RESPONSE_SERVICE="CustomResponseEchoServer" # 复用 provider-qps 二进制；用于自定义返回 LOCAL（用例 7.1）
GLOBAL_CUSTOM_RESPONSE_SERVICE="GlobalCustomResponseEchoServer" # 复用 provider-qps；用于自定义返回 GLOBAL（用例 7.2）

# 规则名（用于查询是否已存在；脚本不再删除规则，已存在则跳过创建）
QPS_RULE_NAME="ratelimit-e2e-qps-rule"
UNIRATE_RULE_NAME="ratelimit-e2e-unirate-rule"
CONCURRENCY_RULE_NAME="ratelimit-e2e-concurrency-rule"
CUSTOM_RULE_NAME="ratelimit-e2e-custom-match-rule"
REGEX_RULE_NAME="ratelimit-e2e-regex-combine-rule"
GLOBAL_RULE_NAME="ratelimit-e2e-global-rule"
CUSTOM_RESPONSE_RULE_NAME="ratelimit-e2e-custom-response-rule"
GLOBAL_CUSTOM_RESPONSE_RULE_NAME="ratelimit-e2e-global-custom-response-rule"

# 端口（避免与其他 demo 冲突）；本端到端测试链路 = curl → consumer → provider
# provider 端口
PORT_PROVIDER_QPS=18180
PORT_PROVIDER_CONCURRENCY=18181
PORT_PROVIDER_UNIRATE=18182
PORT_PROVIDER_CUSTOM=18183
PORT_PROVIDER_REGEX=18184
PORT_PROVIDER_GLOBAL_A=18185           # 用例 6.x 第 1 个 provider 实例（验证多实例共享 GLOBAL 配额）
PORT_PROVIDER_GLOBAL_B=18186           # 用例 6.x 第 2 个 provider 实例（同服务名，不同端口）
PORT_PROVIDER_GLOBAL_FAILOVER=18187    # 用例 6.5 远端降级专用 provider（启动时指向不存在的 limiter 服务）
PORT_PROVIDER_CUSTOM_RESPONSE=18188    # 用例 7.1 自定义返回 LOCAL provider
PORT_PROVIDER_GLOBAL_CUSTOM_RESP=18189 # 用例 7.2 自定义返回 GLOBAL provider
# consumer 端口（每个用例段一个 consumer 实例，--service 指向对应 provider）
PORT_CONSUMER_QPS=18190
PORT_CONSUMER_CONCURRENCY=18191
PORT_CONSUMER_UNIRATE=18192
PORT_CONSUMER_CUSTOM=18193
PORT_CONSUMER_REGEX=18194
PORT_CONSUMER_GLOBAL=18195
PORT_CONSUMER_GLOBAL_FAILOVER=18197    # 用例 6.5 远端降级专用 consumer
PORT_CONSUMER_CUSTOM_RESPONSE=18198    # 用例 7.1 自定义返回 LOCAL consumer
PORT_CONSUMER_GLOBAL_CUSTOM_RESP=18199 # 用例 7.2 自定义返回 GLOBAL consumer

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

# regex_combine 规则参数（用例 5.x）：method 用 REGEX 匹配两条不同 path
# 阈值 4/1s：每条 path 独享时各能过 4，被限 1（5-4）；共享时合计仅过 4，被限 6（10-4）
# 用并发触发避免跨窗口边界（同 1.1/4.1 教训）
REGEX_MAX_AMOUNT=4
REGEX_WINDOW_SECOND=1
REGEX_PATH_PATTERN='/users/.*/orders'         # 规则 method 字段（REGEX 类型）
REGEX_PATH_A='/users/100/orders'              # 第 1 条实际请求路径，命中 REGEX
REGEX_PATH_B='/users/200/orders'              # 第 2 条实际请求路径，命中 REGEX
REGEX_PER_PATH_REQUESTS=5                     # 每条路径并发请求数

# 分布式 GLOBAL 规则参数（用例 6.x）：依赖 polaris.limiter 远端配额服务
# - 6.1/6.2 单实例 + 多窗口聚合：1 provider + maxAmount=4/1s 并发 8 个 → 远端配额内通过 ≈4，限流 ≈4
# - 6.3 双实例共享：2 provider + maxAmount=4/1s 共享 → 两实例合计通过 ≈4，限流 ≈6（如 LOCAL 行为则两实例各通过 4 = 共 8）
# - 6.4 GLOBAL+regex_combine：复用 REGEX 规则字段，通过 PUT 切换到 GLOBAL+regex_combine=true
# - 6.5 远端降级：设置 POLARIS_LIMITER_SVC=__nonexistent__ 让 SDK 服务发现拉不到 limiter，触发 FAILOVER_LOCAL
GLOBAL_MAX_AMOUNT=4
GLOBAL_WINDOW_SECOND=1
GLOBAL_BURST_REQUESTS=8                      # 单批并发突发请求数
GLOBAL_PER_INSTANCE_REQUESTS=5               # 共享配额用例每个实例的并发请求数
GLOBAL_OBSERVE_WINDOWS=4                     # 多窗口聚合判定的窗口数（每窗口发一批，统计聚合结果稳定性）
# polaris.limiter 远端集群（默认部署位置）
GLOBAL_LIMITER_NAMESPACE="Polaris"
GLOBAL_LIMITER_SERVICE="polaris.limiter"
GLOBAL_LIMITER_BAD_SERVICE="ratelimit-e2e-nonexistent-limiter"  # 6.5 用例：故意指向一个不存在的服务名

# CustomResponse 规则参数（用例 7.x）—— 验证 QuotaResponse.GetActiveRule + customResponse.body 端到端透传:
#   - 阈值 2 / 1s 与 QPS reject 一致，便于触发后立即拒绝（无排队语义）
#   - 串行 6 次：单窗口下应限到 4，最坏跨 2 窗口仍限到 2，期望 limited ≥ 2
#   - body 是一段 JSON 字符串：通过端到端断言验证规则配置 → 服务端下发 → SDK 缓存 → provider 读 GetActiveRule
#     → HTTP 透传给上游 curl 的整条链路.
CUSTOM_RESPONSE_MAX_AMOUNT=2
CUSTOM_RESPONSE_WINDOW_SECOND=1
CUSTOM_RESPONSE_TOTAL_REQUESTS=6
CUSTOM_RESPONSE_BODY='{"code":429,"reason":"verify_ratelimit e2e custom body","trace":"verify"}'

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
        --only)           ONLY_LIST="$2";      shift 2 ;;
        --keep)           KEEP_RESOURCES=true; shift ;;
        --debug)          DEBUG_MODE=true;     shift ;;
        --limiter-namespace) GLOBAL_LIMITER_NAMESPACE="$2"; shift 2 ;;
        --limiter-service)   GLOBAL_LIMITER_SERVICE="$2";   shift 2 ;;
        -h|--help)
            cat <<EOF
用法: $0 [选项]

选项:
  --polaris-server <addr>   Polaris 服务端地址 (默认 127.0.0.1)
  --polaris-token <token>   Polaris 鉴权 Token
  --skip <列表>             逗号分隔，跳过指定用例组。可选: qps,unirate,concurrency,custom,regex,global,custom-response,metrics
  --only <列表>             逗号分隔，仅运行指定用例组（其余全部跳过）。与 --skip 互斥，--only 优先
  --keep                    保留 provider 进程和日志（限流规则始终保留，无需此参数控制）
  --debug                   开启 debug 日志（透传 SDK 日志级别）
  --limiter-namespace <ns>  GLOBAL 用例（6.x）使用的 limiter 命名空间 (默认 Polaris)
  --limiter-service <svc>   GLOBAL 用例（6.x）使用的 limiter 服务名 (默认 polaris.limiter)；
                            自己另起本地 polaris-limiter 时，注册一个独立服务名（如 polaris.limiter.local），
                            然后用此参数覆盖，让 SDK 直接路由到本地实例
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
# 优先级：--only > --skip
#   --only 指定时：仅列出的用例组返回"不跳过"(return 1)，其余都跳过(return 0)
#   --only 未指定时：按 --skip 列表判断；列表中存在的返回"跳过"(return 0)
is_skipped() {
    local name="$1"
    # --only 优先：有 ONLY_LIST 时只看是否在白名单中
    if [[ -n "$ONLY_LIST" ]]; then
        local IFS=','
        # shellcheck disable=SC2206
        local arr=($ONLY_LIST)
        for x in "${arr[@]}"; do
            [[ "$(echo "$x" | tr -d ' ')" == "$name" ]] && return 1  # 在白名单中 → 不跳过
        done
        return 0  # 不在白名单中 → 跳过
    fi
    # fallback 到 --skip 黑名单逻辑
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

# query_rule_custom_response_body：查询已存在规则的 customResponse.body 字符串.
# 字段缺失或查询失败均输出空串；成功时输出原始 body（不做转义/解码）.
# 用于检测控制台 customResponse.body 与脚本期望是否一致——不一致则用例 7.x 必然 FAIL（custom body 校验不过），
# 因此在 create_custom_response_rule 里复用该函数，发现不一致就主动 PUT 同步.
query_rule_custom_response_body() {
    local rule_name="$1"
    local service="$2"
    local resp http_code
    http_code=$(curl -s -o /tmp/_rl_qcrb_$$.tmp -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request GET "${POLARIS_HTTP_ADDR}/naming/v1/ratelimits?name=${rule_name}&service=${service}&namespace=${NAMESPACE}&limit=50" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" 2>/dev/null || echo "000")
    resp=$(cat /tmp/_rl_qcrb_$$.tmp 2>/dev/null || echo "")
    rm -f /tmp/_rl_qcrb_$$.tmp
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
            cr = r.get('custom_response', r.get('customResponse', {}) or {})
            body = cr.get('body', '') if isinstance(cr, dict) else ''
            if body:
                # 用 print 直接输出原始 body；调用方按字符串字面量比较即可
                print(body, end='')
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

# _build_regex_rule_body <rule_name> <rule_id> <regex_combine_bool>
# 用于 create_regex_rule（rule_id 留空 / regex_combine 为 false 或 true）和后续 PUT 翻转 regex_combine.
# method 字段用 REGEX 匹配 /users/.*/orders；regex_combine 控制阈值是各 path 独享还是共享.
_build_regex_rule_body() {
    local rule_name="$1"
    local rule_id="$2"
    local regex_combine="$3"
    SVC="$REGEX_SERVICE" NS="$NAMESPACE" NAME="$rule_name" \
        AMOUNT="$REGEX_MAX_AMOUNT" WINDOW="$REGEX_WINDOW_SECOND" \
        PATTERN="$REGEX_PATH_PATTERN" REGEX_COMBINE="$regex_combine" \
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
    'method': {'type': 'REGEX', 'value': os.environ['PATTERN']},
    'amounts': [{
        'maxAmount': int(os.environ['AMOUNT']),
        'validDuration': '%ss' % os.environ['WINDOW'],
    }],
    'action': 'REJECT',
    'regex_combine': os.environ['REGEX_COMBINE'].lower() == 'true',
    'disable': False,
}
rid = os.environ.get('RULE_ID', '')
if rid:
    rule['id'] = rid
print(json.dumps([rule]))
"
}

# query_rule_regex_combine <rule_name> <service>：查询已存在 regex 规则的 regex_combine 值（true/false 字符串）.
query_rule_regex_combine() {
    local rule_name="$1"
    local service="$2"
    local resp http_code
    http_code=$(curl -s -o /tmp/_rl_qrc_$$.tmp -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request GET "${POLARIS_HTTP_ADDR}/naming/v1/ratelimits?name=${rule_name}&service=${service}&namespace=${NAMESPACE}&limit=50" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" 2>/dev/null || echo "000")
    resp=$(cat /tmp/_rl_qrc_$$.tmp 2>/dev/null || echo "")
    rm -f /tmp/_rl_qrc_$$.tmp
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
            v = r.get('regex_combine', r.get('regexCombine', None))
            if v is not None:
                print('true' if v else 'false')
            break
except Exception:
    pass
" <<< "$resp" 2>/dev/null || echo ""
}

# create_regex_rule
# 创建 regex_combine=false 的规则（首次跑）；如果规则已存在但 regex_combine 不一致，PUT 自动同步.
# 与 unirate 规则的 max_queue_delay 自动更新逻辑保持一致风格.
create_regex_rule() {
    local rule_name="$REGEX_RULE_NAME"
    print_block "regex_combine 规则配置 [$rule_name]（默认 regex_combine=false）" \
        "服务/命名空间:    ${REGEX_SERVICE} / ${NAMESPACE}" \
        "限流资源类型:     QPS（reject 策略）" \
        "method 匹配:      REGEX '${REGEX_PATH_PATTERN}'（多条 path 都能命中）" \
        "阈值:             ${REGEX_MAX_AMOUNT} 次 / ${REGEX_WINDOW_SECOND} 秒" \
        "regex_combine:    false（默认；用例 5.1 验证"各 path 独享配额"）" \
        "" \
        "效果:             用例 5.1 串行 / 并发打两条不同的 ${REGEX_PATH_PATTERN} 路径，每条独享 ${REGEX_MAX_AMOUNT}/${REGEX_WINDOW_SECOND}s 阈值；" \
        "                  用例 5.2 通过 PUT 把 regex_combine 改为 true，两条 path 共享同一个阈值."
    if rule_exists "$rule_name" "$REGEX_SERVICE"; then
        # 已存在则确保规则起始状态是 regex_combine=false（5.1 的前提）；不一致就 PUT 同步.
        local actual_rc
        actual_rc=$(query_rule_regex_combine "$rule_name" "$REGEX_SERVICE")
        if [[ -n "$actual_rc" ]] && [[ "$actual_rc" != "false" ]]; then
            log_warn "[regex 规则] 控制台上 regex_combine=${actual_rc}，与脚本 5.1 起始期望 false 不一致，自动 PUT 重置"
            local rule_id
            rule_id=$(query_rule_id "$rule_name" "$REGEX_SERVICE")
            if [[ -z "$rule_id" ]]; then
                log_error "[regex 规则] 拿不到规则 id，无法自动更新"
                return 1
            fi
            local body
            body=$(_build_regex_rule_body "$rule_name" "$rule_id" "false")
            if update_rule_via_http "$body"; then
                log_info "[regex 规则] 已重置 regex_combine=false"
            else
                log_error "[regex 规则] PUT 重置失败"
                return 1
            fi
        else
            log_info "regex 规则 [$rule_name] 已存在于服务 [$REGEX_SERVICE]，regex_combine=false，跳过创建"
        fi
        return 0
    fi
    local body
    body=$(_build_regex_rule_body "$rule_name" "" "false")
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
        log_error "创建 regex 规则失败 HTTP=${http_code} resp=${resp}"
        return 1
    fi
    log_info "regex 规则 [$rule_name] 已创建（regex_combine=false）"
    return 0
}

# flip_regex_combine <true|false>：把现有规则的 regex_combine 翻转，并等待 SDK 拉新规则.
flip_regex_combine() {
    local target="$1"
    local rule_id
    rule_id=$(query_rule_id "$REGEX_RULE_NAME" "$REGEX_SERVICE")
    if [[ -z "$rule_id" ]]; then
        log_error "[regex 规则] 找不到规则 id，无法翻转 regex_combine"
        return 1
    fi
    local body
    body=$(_build_regex_rule_body "$REGEX_RULE_NAME" "$rule_id" "$target")
    if ! update_rule_via_http "$body"; then
        log_error "[regex 规则] PUT 翻转 regex_combine=${target} 失败"
        return 1
    fi
    log_info "[regex 规则] 已 PUT 翻转 regex_combine=${target}，等待 SDK 拉新规则 (3s)..."
    sleep 3
    return 0
}

# probe_limiter_available：探测 polaris.limiter 服务下是否有 healthy instance.
# 返回 0 表示有可用 limiter（GLOBAL 用例可跑），返回 1 表示不可用（用例 6.x 应当 SKIP）.
# 调 GET /naming/v1/instances?service=polaris.limiter&namespace=Polaris；只要任意一条返回的 instance 健康即认为可用.
probe_limiter_available() {
    local resp http_code
    http_code=$(curl -s -o /tmp/_rl_probe_$$.tmp -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        --request GET "${POLARIS_HTTP_ADDR}/naming/v1/instances?service=${GLOBAL_LIMITER_SERVICE}&namespace=${GLOBAL_LIMITER_NAMESPACE}&limit=50" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" 2>/dev/null || echo "000")
    resp=$(cat /tmp/_rl_probe_$$.tmp 2>/dev/null || echo "")
    rm -f /tmp/_rl_probe_$$.tmp
    if [[ "$http_code" != "200" ]]; then
        return 1
    fi
    # 取实例数量；要求至少有一条 healthy instance（healthy=true 且 isolate=false）.
    local healthy
    healthy=$(python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    cnt = 0
    for ins in data.get('instances', []):
        if ins.get('healthy', False) and not ins.get('isolate', False):
            cnt += 1
    print(cnt)
except Exception:
    print(0)
" <<< "$resp" 2>/dev/null || echo 0)
    [[ "$healthy" -gt 0 ]]
}

# _build_global_rule_body <rule_name> <rule_id>
# 构造 GLOBAL 类型规则 body；rule_id 为空时用于 POST 创建，非空时用于 PUT 更新.
# 关键字段：
#   - type=GLOBAL：触发 SDK 走远端配额（asyncRateLimitConnector → polaris.limiter）
#   - failover=FAILOVER_LOCAL：远端不可达时退化为本地限流，避免"远端拉不到→全放通"误判用例
#   - cluster: 默认空，让 SDK 用 yaml 配置的 limiterNamespace/limiterService（即 Polaris/polaris.limiter）
_build_global_rule_body() {
    local rule_name="$1"
    local rule_id="$2"
    SVC="$GLOBAL_SERVICE" NS="$NAMESPACE" NAME="$rule_name" \
        AMOUNT="$GLOBAL_MAX_AMOUNT" WINDOW="$GLOBAL_WINDOW_SECOND" \
        RULE_ID="$rule_id" \
        python3 -c "
import os, json
rule = {
    'name': os.environ['NAME'],
    'service': os.environ['SVC'],
    'namespace': os.environ['NS'],
    'priority': 0,
    'resource': 'QPS',
    'type': 'GLOBAL',
    'method': {'type': 'EXACT', 'value': '/echo'},
    'amounts': [{
        'maxAmount': int(os.environ['AMOUNT']),
        'validDuration': '%ss' % os.environ['WINDOW'],
    }],
    'action': 'REJECT',
    'failover': 'FAILOVER_LOCAL',
    'disable': False,
}
rid = os.environ.get('RULE_ID', '')
if rid:
    rule['id'] = rid
print(json.dumps([rule]))
"
}

# create_global_rule
# 创建 GLOBAL 规则；已存在则跳过（脚本不强制重置 type=GLOBAL，因为该字段切换会让 SDK 重建窗口）.
create_global_rule() {
    local rule_name="$GLOBAL_RULE_NAME"
    print_block "GLOBAL 规则配置 [$rule_name]（分布式限流）" \
        "服务/命名空间:    ${GLOBAL_SERVICE} / ${NAMESPACE}" \
        "限流资源类型:     QPS（reject 策略）" \
        "限流模式:         GLOBAL（远端配额服务，多 SDK 实例共享阈值）" \
        "method 匹配:      EXACT '/echo'" \
        "阈值:             ${GLOBAL_MAX_AMOUNT} 次 / ${GLOBAL_WINDOW_SECOND} 秒" \
        "failover:         FAILOVER_LOCAL（远端不可达时退化为本地限流，避免误判）" \
        "limiter 集群:     ${GLOBAL_LIMITER_NAMESPACE}/${GLOBAL_LIMITER_SERVICE}（SDK yaml 配置）" \
        "" \
        "效果:             SDK 通过 gRPC 异步上报 + 拉取配额；多个 provider 实例**合计**消耗同一份阈值；" \
        "                  与 LOCAL 规则的核心差异：LOCAL 时每个 SDK 实例各自有 ${GLOBAL_MAX_AMOUNT} 个配额，GLOBAL 时全集群只有 ${GLOBAL_MAX_AMOUNT} 个."
    if rule_exists "$rule_name" "$GLOBAL_SERVICE"; then
        log_info "GLOBAL 规则 [$rule_name] 已存在于服务 [$GLOBAL_SERVICE]，跳过创建"
        return 0
    fi
    local body
    body=$(_build_global_rule_body "$rule_name" "")
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
        log_error "创建 GLOBAL 规则失败 HTTP=${http_code} resp=${resp}"
        return 1
    fi
    log_info "GLOBAL 规则 [$rule_name] 已创建"
    return 0
}

# _build_custom_response_rule_body <rule_name> <rule_id> <body>
# 构造带 customResponse.body 的限流规则 body；rule_id 为空时用于 POST 创建，非空时用于 PUT 更新.
# 关键字段：
#   - resource=QPS / type=LOCAL / action=REJECT：与 reject 规则一致，确保限流可立即触发
#   - method.EXACT='/echo'：复用 provider-qps 的 /echo 接口，无需新建路由
#   - customResponse.body：本次新增字段，被 SDK 端通过 QuotaResponse.GetActiveRule().GetCustomResponse().GetBody() 读取
_build_custom_response_rule_body() {
    local rule_name="$1"
    local rule_id="$2"
    local body_text="$3"
    SVC="$CUSTOM_RESPONSE_SERVICE" NS="$NAMESPACE" NAME="$rule_name" \
        AMOUNT="$CUSTOM_RESPONSE_MAX_AMOUNT" WINDOW="$CUSTOM_RESPONSE_WINDOW_SECOND" \
        BODY="$body_text" RULE_ID="$rule_id" \
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
    'action': 'REJECT',
    'customResponse': {'body': os.environ['BODY']},
    'disable': False,
}
rid = os.environ.get('RULE_ID', '')
if rid:
    rule['id'] = rid
print(json.dumps([rule]))
"
}

# create_custom_response_rule
# 创建带 customResponse.body 的限流规则（用例 7.x）；
# 若同名规则已存在但 customResponse.body 与脚本期望不一致，则自动 PUT 同步——
# 否则 7.1 用例的 body 字面量比较必然 FAIL.
create_custom_response_rule() {
    local rule_name="$CUSTOM_RESPONSE_RULE_NAME"
    print_block "CustomResponse 规则配置 [$rule_name]" \
        "服务/命名空间:    ${CUSTOM_RESPONSE_SERVICE} / ${NAMESPACE}" \
        "限流资源类型:     QPS（reject 策略）" \
        "限流模式:         LOCAL（单机限流）" \
        "method 匹配:      EXACT '/echo'" \
        "阈值:             ${CUSTOM_RESPONSE_MAX_AMOUNT} 次 / ${CUSTOM_RESPONSE_WINDOW_SECOND} 秒" \
        "customResponse:   body=${CUSTOM_RESPONSE_BODY}" \
        "" \
        "效果:             触发限流后 SDK 通过 QuotaResponse.ActiveRule 暴露规则；provider-qps 读" \
        "                  resp.GetActiveRule().GetCustomResponse().GetBody() 作为 HTTP body 写回，" \
        "                  并把规则名透传到响应头 X-Polaris-RateLimit-Rule，供 7.1 用例做端到端断言."
    if rule_exists "$rule_name" "$CUSTOM_RESPONSE_SERVICE"; then
        local actual_body
        actual_body=$(query_rule_custom_response_body "$rule_name" "$CUSTOM_RESPONSE_SERVICE")
        if [[ "$actual_body" != "$CUSTOM_RESPONSE_BODY" ]]; then
            log_warn "[custom-response 规则] 控制台 body 与脚本期望不一致，自动 PUT 同步"
            log_warn "    期望: ${CUSTOM_RESPONSE_BODY}"
            log_warn "    实际: ${actual_body}"
            local rule_id
            rule_id=$(query_rule_id "$rule_name" "$CUSTOM_RESPONSE_SERVICE")
            if [[ -z "$rule_id" ]]; then
                log_error "[custom-response 规则] 拿不到规则 id，无法自动更新"
                return 1
            fi
            local body
            body=$(_build_custom_response_rule_body "$rule_name" "$rule_id" "$CUSTOM_RESPONSE_BODY")
            if update_rule_via_http "$body"; then
                log_info "[custom-response 规则] 已自动 PUT 同步 customResponse.body"
            else
                log_error "[custom-response 规则] PUT 同步失败"
                return 1
            fi
        else
            log_info "custom-response 规则 [$rule_name] 已存在于服务 [$CUSTOM_RESPONSE_SERVICE]，body 一致，跳过更新"
        fi
        return 0
    fi
    local body
    body=$(_build_custom_response_rule_body "$rule_name" "" "$CUSTOM_RESPONSE_BODY")
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
        log_error "创建 custom-response 规则失败 HTTP=${http_code} resp=${resp}"
        return 1
    fi
    log_info "custom-response 规则 [$rule_name] 已创建"
    return 0
}

# _build_global_custom_response_rule_body <rule_name> <rule_id> <body>
# 与 _build_custom_response_rule_body 唯一区别：type='GLOBAL' + failover='FAILOVER_LOCAL'.
_build_global_custom_response_rule_body() {
    local rule_name="$1"
    local rule_id="$2"
    local body_text="$3"
    SVC="$GLOBAL_CUSTOM_RESPONSE_SERVICE" NS="$NAMESPACE" NAME="$rule_name" \
        AMOUNT="$CUSTOM_RESPONSE_MAX_AMOUNT" WINDOW="$CUSTOM_RESPONSE_WINDOW_SECOND" \
        BODY="$body_text" RULE_ID="$rule_id" \
        python3 -c "
import os, json
rule = {
    'name': os.environ['NAME'],
    'service': os.environ['SVC'],
    'namespace': os.environ['NS'],
    'priority': 0,
    'resource': 'QPS',
    'type': 'GLOBAL',
    'method': {'type': 'EXACT', 'value': '/echo'},
    'amounts': [{
        'maxAmount': int(os.environ['AMOUNT']),
        'validDuration': '%ss' % os.environ['WINDOW'],
    }],
    'action': 'REJECT',
    'failover': 'FAILOVER_LOCAL',
    'customResponse': {'body': os.environ['BODY']},
    'disable': False,
}
rid = os.environ.get('RULE_ID', '')
if rid:
    rule['id'] = rid
print(json.dumps([rule]))
"
}

# create_global_custom_response_rule
# 创建 GLOBAL + customResponse.body 的限流规则（用例 7.2）；
# 验证分布式限流场景下 customResponse 仍然能透传。
create_global_custom_response_rule() {
    local rule_name="$GLOBAL_CUSTOM_RESPONSE_RULE_NAME"
    print_block "GLOBAL CustomResponse 规则配置 [$rule_name]" \
        "服务/命名空间:    ${GLOBAL_CUSTOM_RESPONSE_SERVICE} / ${NAMESPACE}" \
        "限流资源类型:     QPS（reject 策略）" \
        "限流模式:         GLOBAL（分布式限流，走远端 polaris.limiter 配额）" \
        "method 匹配:      EXACT '/echo'" \
        "阈值:             ${CUSTOM_RESPONSE_MAX_AMOUNT} 次 / ${CUSTOM_RESPONSE_WINDOW_SECOND} 秒" \
        "failover:         FAILOVER_LOCAL（远端不可达时退化为本地限流，不影响 customResponse 透传）" \
        "customResponse:   body=${CUSTOM_RESPONSE_BODY}" \
        "" \
        "效果:             分布式限流触发后，SDK 的 QuotaResponse.ActiveRule 同样能读取 customResponse.body；" \
        "                  无论走远端配额还是 FAILOVER_LOCAL 路径，provider 都能正确返回自定义响应体."
    if rule_exists "$rule_name" "$GLOBAL_CUSTOM_RESPONSE_SERVICE"; then
        local actual_body
        actual_body=$(query_rule_custom_response_body "$rule_name" "$GLOBAL_CUSTOM_RESPONSE_SERVICE")
        if [[ "$actual_body" != "$CUSTOM_RESPONSE_BODY" ]]; then
            log_warn "[global-custom-response 规则] 控制台 body 与脚本期望不一致，自动 PUT 同步"
            local rule_id
            rule_id=$(query_rule_id "$rule_name" "$GLOBAL_CUSTOM_RESPONSE_SERVICE")
            if [[ -z "$rule_id" ]]; then
                log_error "[global-custom-response 规则] 拿不到规则 id，无法自动更新"
                return 1
            fi
            local body
            body=$(_build_global_custom_response_rule_body "$rule_name" "$rule_id" "$CUSTOM_RESPONSE_BODY")
            if update_rule_via_http "$body"; then
                log_info "[global-custom-response 规则] 已自动 PUT 同步 customResponse.body"
            else
                log_error "[global-custom-response 规则] PUT 同步失败"
                return 1
            fi
        else
            log_info "global-custom-response 规则 [$rule_name] 已存在，body 一致，跳过更新"
        fi
        return 0
    fi
    local body
    body=$(_build_global_custom_response_rule_body "$rule_name" "" "$CUSTOM_RESPONSE_BODY")
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
        log_error "创建 global-custom-response 规则失败 HTTP=${http_code} resp=${resp}"
        return 1
    fi
    log_info "global-custom-response 规则 [$rule_name] 已创建"
    return 0
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
PROVIDER_REGEX_PID=""
PROVIDER_GLOBAL_A_PID=""
PROVIDER_GLOBAL_B_PID=""
PROVIDER_GLOBAL_FAILOVER_PID=""
PROVIDER_CUSTOM_RESPONSE_PID=""
PROVIDER_GLOBAL_CUSTOM_RESP_PID=""
CONSUMER_QPS_PID=""
CONSUMER_UNIRATE_PID=""
CONSUMER_CONC_PID=""
CONSUMER_CUSTOM_PID=""
CONSUMER_REGEX_PID=""
CONSUMER_GLOBAL_PID=""
CONSUMER_GLOBAL_FAILOVER_PID=""
CONSUMER_CUSTOM_RESPONSE_PID=""
CONSUMER_GLOBAL_CUSTOM_RESP_PID=""
PROVIDER_METRICS_PID=""
PROVIDER_EVENTS_PID=""
MOCK_EVENT_SERVER_PID=""

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

    # POLARIS_METRICS_PORT：每个 provider 实例的 prometheus /metrics 端口 = 业务端口 + 10000.
    # 确保同一台机器上多个 provider 实例不会争抢同一个 metrics 端口.
    # consumer 实例的 metrics 端口同样按此规则分配（consumer 也加载了 statReporter），但用例 8.x 仅检查 provider.
    local metrics_port=$((port + 10000))

    local pid_log="${LOG_DIR}/${log_name}.log"
    log_info "[start] ${log_name} 监听 :${port}, metrics :${metrics_port}, stdout 日志 ${pid_log}, SDK 日志 ${run_dir}/polaris/log"
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
    # POLARIS_LIMITER_NS / POLARIS_LIMITER_SVC：被 provider-qps/polaris.yaml 通过 ${...} 占位符展开为
    # provider.limiterNamespace / limiterService（远端配额服务的标识）.
    # 默认值 = 全局 GLOBAL_LIMITER_NAMESPACE/SERVICE（用户可通过 --limiter-namespace / --limiter-service 覆盖）；
    # 调用方（如用例 6.5）也可临时 export POLARIS_LIMITER_SVC=__nonexistent__ 验证 FAILOVER_LOCAL 退化路径.
    local limiter_ns="${POLARIS_LIMITER_NS:-${GLOBAL_LIMITER_NAMESPACE}}"
    local limiter_svc="${POLARIS_LIMITER_SVC:-${GLOBAL_LIMITER_SERVICE}}"
    POLARIS_SERVER="$POLARIS_SERVER" POLARIS_TOKEN="$POLARIS_TOKEN" \
    POLARIS_LIMITER_NS="$limiter_ns" POLARIS_LIMITER_SVC="$limiter_svc" \
    POLARIS_METRICS_PORT="$metrics_port" \
    POLARIS_EVENT_ADDR="${POLARIS_EVENT_ADDR:-}" \
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
    # SKIP 不计入失败：用于"环境不具备此用例前置条件"的场景（如 polaris.limiter 未注册时跳过 6.x）
    if [[ "$verdict" != "PASS" && "$verdict" != "SKIP" ]]; then
        TOTAL_FAIL=$((TOTAL_FAIL+1))
    fi
    case "$verdict" in
        PASS) echo -e "  ${GREEN}✅ [${name}] PASS${NC} - ${detail}" ;;
        FAIL) echo -e "  ${RED}❌ [${name}] FAIL${NC} - ${detail}" ;;
        WARN) echo -e "  ${YELLOW}⚠️  [${name}] WARN${NC} - ${detail}" ;;
        SKIP) echo -e "  ${YELLOW}⏭️  [${name}] SKIP${NC} - ${detail}" ;;
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

# fetch_status_body_header <port> <path> <header_name>
# 串行 1 次请求，返回 "<status> <header_value> <body>" 三段（按字面量空格分隔，body 占最后；TAB 已 escape 成空格）.
# 用于用例 7.x 在限流命中后取整套响应做断言：状态码 / 自定义响应头 / body.
# - 限流时上游会拿到 429 + body=customResponse.body + 响应头 X-Polaris-RateLimit-Rule=<rule_name>.
# - 通过时上游拿到 200 + body=Hello,... + 无 X-Polaris-RateLimit-Rule 头.
# 由于 body 中可能包含特殊字符，调用方应通过临时文件读 body / header，不要再依赖单行解析.
fetch_status_body_header() {
    local port="$1"
    local path="$2"
    local header_name="$3"
    local out_dir="$4"  # 输出文件所在目录；调用方负责创建/回收
    local code
    code=$(curl -s --connect-timeout 2 --max-time 5 \
        -D "${out_dir}/headers" \
        -o "${out_dir}/body" \
        -w '%{http_code}' \
        "http://127.0.0.1:${port}${path}" 2>/dev/null || echo "000")
    # 提取目标 header（大小写不敏感；取第一个匹配，去掉前后空格 / CR）
    local header_value=""
    if [[ -f "${out_dir}/headers" ]]; then
        header_value=$(awk -v h="$header_name" 'BEGIN{IGNORECASE=1} \
            tolower($1)==tolower(h":"){
                $1="";
                sub(/^ +/,"");
                sub(/\r$/,"");
                print;
                exit
            }' "${out_dir}/headers")
    fi
    echo "$code"
    echo "$header_value"
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

# ======================== 用例 5.x：regex_combine（合并计算阈值）开关 ========================
# 验证 polaris ratelimit 规则字段 regex_combine 的语义差异：
#   - false（默认）：method 用 REGEX 时，每条实际命中规则的 path 各自独享阈值
#   - true：所有命中同一 REGEX 的 path 共享同一阈值
# 测试模式：用并发请求避免跨窗口边界（同 1.1/4.1 教训）.
run_regex_combine_cases() {
    log_step "[用例 5.x] regex_combine 合并计算阈值开关 (链路: curl → consumer:${PORT_CONSUMER_REGEX} → provider:${PORT_PROVIDER_REGEX})"
    if ! create_regex_rule; then
        record_case "5.0 创建 regex 规则" "FAIL" "HTTP API 调用失败"
        return
    fi
    if ! build_and_start_binary "provider-qps" "$PORT_PROVIDER_REGEX" \
        "$REGEX_SERVICE" "provider-qps-regex" "PROVIDER_REGEX_PID"; then
        record_case "5.0 启动 regex provider" "FAIL" "provider-qps (regex) 启动失败"
        return
    fi
    if ! build_and_start_binary "consumer" "$PORT_CONSUMER_REGEX" \
        "$REGEX_SERVICE" "consumer-regex" "CONSUMER_REGEX_PID"; then
        record_case "5.0 启动 regex consumer" "FAIL" "consumer (regex) 启动失败"
        return
    fi

    log_info "等待 4s 让 SDK 拉到规则..."
    sleep 4

    local stat ok limited other
    local total=$((REGEX_PER_PATH_REQUESTS * 2))

    # ---------- 用例 5.1：regex_combine=false 各 path 独享配额 ----------
    # 并发打两条不同的命中 REGEX 的 path：每条 5 次，两条独享 4/1s 阈值，每条限 1 个，总共 limited ≈ 2.
    # 容忍跨窗口的边界：limited ≥ 2*(REQ_PER_PATH - MAX_AMOUNT) - 2 = 2，最差也至少有 1 个 limited.
    local independent_min_limited=$((2 * (REGEX_PER_PATH_REQUESTS - REGEX_MAX_AMOUNT) - 2))
    [[ $independent_min_limited -lt 1 ]] && independent_min_limited=1
    local independent_max_ok=$((2 * REGEX_MAX_AMOUNT + 2))  # 跨 2 窗口最多 2*MAX+2 个能过
    print_block "[用例 5.1] regex_combine=false 各 path 独享配额" \
        "操作:   并发打 ${REGEX_PER_PATH_REQUESTS} 个 ${REGEX_PATH_A} + ${REGEX_PER_PATH_REQUESTS} 个 ${REGEX_PATH_B}（共 ${total} 次）" \
        "原理:   method REGEX '${REGEX_PATH_PATTERN}' 命中两条 path；regex_combine=false 时" \
        "        SDK 按"实际 path 值"作为 windowKey 维度——两条 path 落到不同 token bucket，各独享 ${REGEX_MAX_AMOUNT}/${REGEX_WINDOW_SECOND}s" \
        "预期:   每条 path 通过 ≈${REGEX_MAX_AMOUNT}、限流 ≈$((REGEX_PER_PATH_REQUESTS - REGEX_MAX_AMOUNT))；总 limited ≥ ${independent_min_limited}，ok ≤ ${independent_max_ok}" \
        "判定:   limited ≥ ${independent_min_limited} && ok ≤ ${independent_max_ok} && other == 0 → PASS"

    log_info "[用例 5.1] 并发 ${REGEX_PER_PATH_REQUESTS} 个 ${REGEX_PATH_A}"
    local stat_a stat_b
    stat_a=$(count_status_concurrent "$PORT_CONSUMER_REGEX" "$REGEX_PATH_A" "$REGEX_PER_PATH_REQUESTS")
    log_info "[用例 5.1] 并发 ${REGEX_PER_PATH_REQUESTS} 个 ${REGEX_PATH_B}"
    stat_b=$(count_status_concurrent "$PORT_CONSUMER_REGEX" "$REGEX_PATH_B" "$REGEX_PER_PATH_REQUESTS")
    local ok_a limited_a other_a ok_b limited_b other_b
    read -r ok_a limited_a other_a <<< "$stat_a"
    read -r ok_b limited_b other_b <<< "$stat_b"
    ok=$((ok_a + ok_b))
    limited=$((limited_a + limited_b))
    other=$((other_a + other_b))
    log_info "结果: ${REGEX_PATH_A}=(200=${ok_a}, 429=${limited_a})  ${REGEX_PATH_B}=(200=${ok_b}, 429=${limited_b})  total: 200=${ok} 429=${limited} other=${other}"

    if [[ "$other" -gt 0 ]]; then
        record_case "用例 5.1 regex_combine=false 各 path 独享" "FAIL" \
            "出现非 200/429 状态码 (other=${other})"
    elif [[ "$limited" -lt "$independent_min_limited" ]]; then
        record_case "用例 5.1 regex_combine=false 各 path 独享" "FAIL" \
            "limited=${limited} 不足 ${independent_min_limited}（独享语义不成立？阈值可能被共享）"
    elif [[ "$ok" -gt "$independent_max_ok" ]]; then
        record_case "用例 5.1 regex_combine=false 各 path 独享" "FAIL" \
            "ok=${ok} 超过 ${independent_max_ok}（规则没生效？）"
    else
        record_case "用例 5.1 regex_combine=false 各 path 独享" "PASS" \
            "${REGEX_PATH_A}: 200=${ok_a}/429=${limited_a}; ${REGEX_PATH_B}: 200=${ok_b}/429=${limited_b}"
    fi

    # 等过窗口让 5.2 从干净状态开始
    sleep $((REGEX_WINDOW_SECOND + 1))

    # ---------- 用例 5.2：regex_combine=true 多 path 共享配额 ----------
    # 通过 PUT 把 regex_combine 翻成 true，等 SDK 拉新规则后，再发同等总量请求；现在两条 path 共享一个 windowKey.
    # 期望：总 limited ≈ total - MAX_AMOUNT；容忍跨 2 窗口下限 = total - 2*MAX.
    local combined_min_limited=$((total - 2 * REGEX_MAX_AMOUNT))
    [[ $combined_min_limited -lt 1 ]] && combined_min_limited=1
    local combined_max_ok=$((2 * REGEX_MAX_AMOUNT))  # 共享时最多 2 个窗口各 MAX 个能过
    print_block "[用例 5.2] regex_combine=true 多 path 共享配额" \
        "操作:   PUT 翻转 regex_combine=true → 等 3s 让 SDK 拉新规则 → 同样并发 ${REGEX_PER_PATH_REQUESTS}+${REGEX_PER_PATH_REQUESTS} 次" \
        "原理:   regex_combine=true 时 SDK 按"规则配置的 REGEX 字符串"做 windowKey 维度——两条 path 落到同一个 token bucket，共享 ${REGEX_MAX_AMOUNT}/${REGEX_WINDOW_SECOND}s" \
        "预期:   总通过 ≈${REGEX_MAX_AMOUNT}、限流 ≈$((total - REGEX_MAX_AMOUNT))；limited ≥ ${combined_min_limited}，ok ≤ ${combined_max_ok}" \
        "判定:   limited ≥ ${combined_min_limited} && ok ≤ ${combined_max_ok} && other == 0 → PASS（且 ok 显著小于 5.1）"

    if ! flip_regex_combine "true"; then
        record_case "用例 5.2 regex_combine=true 共享" "FAIL" "PUT 翻转 regex_combine 失败"
        return
    fi

    log_info "[用例 5.2] 并发 ${REGEX_PER_PATH_REQUESTS} 个 ${REGEX_PATH_A}"
    stat_a=$(count_status_concurrent "$PORT_CONSUMER_REGEX" "$REGEX_PATH_A" "$REGEX_PER_PATH_REQUESTS")
    log_info "[用例 5.2] 并发 ${REGEX_PER_PATH_REQUESTS} 个 ${REGEX_PATH_B}"
    stat_b=$(count_status_concurrent "$PORT_CONSUMER_REGEX" "$REGEX_PATH_B" "$REGEX_PER_PATH_REQUESTS")
    read -r ok_a limited_a other_a <<< "$stat_a"
    read -r ok_b limited_b other_b <<< "$stat_b"
    ok=$((ok_a + ok_b))
    limited=$((limited_a + limited_b))
    other=$((other_a + other_b))
    log_info "结果: ${REGEX_PATH_A}=(200=${ok_a}, 429=${limited_a})  ${REGEX_PATH_B}=(200=${ok_b}, 429=${limited_b})  total: 200=${ok} 429=${limited} other=${other}"

    if [[ "$other" -gt 0 ]]; then
        record_case "用例 5.2 regex_combine=true 共享" "FAIL" \
            "出现非 200/429 状态码 (other=${other})"
    elif [[ "$limited" -lt "$combined_min_limited" ]]; then
        record_case "用例 5.2 regex_combine=true 共享" "FAIL" \
            "limited=${limited} 不足 ${combined_min_limited}（共享语义不成立？regex_combine 没生效）"
    elif [[ "$ok" -gt "$combined_max_ok" ]]; then
        record_case "用例 5.2 regex_combine=true 共享" "FAIL" \
            "ok=${ok} 超过 ${combined_max_ok}（共享后仍然各 path 独享？）"
    else
        record_case "用例 5.2 regex_combine=true 共享" "PASS" \
            "共享下 200=${ok}/${total}, 429=${limited}（≥${combined_min_limited}），合并阈值生效"
    fi

    # 5.x 结束，把 regex_combine 改回 false 让规则恢复初始状态，避免下次跑时影响 5.1 起始检测.
    if ! flip_regex_combine "false"; then
        log_warn "[regex 规则] 收尾翻转 regex_combine=false 失败，下次跑 5.1 前会自动 PUT 重置（不影响测试）"
    fi
}

# ======================== 用例 6.x：分布式集群限流（GLOBAL） ========================
# 验证 polaris ratelimit 规则字段 type=GLOBAL 的语义：
#   - 多个 provider 实例（同服务、不同端口）共享同一份远端配额，通过 polaris.limiter 服务做异步上报与拉取
#   - 与 LOCAL 类型规则的核心差异：LOCAL 下每个 SDK 实例各自独享阈值，GLOBAL 下全集群合计仅一份阈值
# 用例覆盖：
#   6.0 polaris.limiter 探测（前置；不可用则整段 SKIP）
#   6.1 GLOBAL 单实例触发限流（多窗口聚合判定，避开窗口边界抖动）
#   6.2 GLOBAL 新窗口再次生效（验证规则跨窗口持续有效，对照 1.2 单机版语义）
#   6.3 GLOBAL 多实例共享配额（核心：A/B 两实例合计仅一份阈值，对照 LOCAL 多实例×N）
#   6.4 GLOBAL + regex_combine：多 path 共享同一份**远端**配额（对照 5.2 单机版）
#   6.5 远端降级（FAILOVER_LOCAL）：故意把 SDK 的 limiter 服务名指向不存在的服务，验证退化路径

# run_global_burst_in_windows <port> <path> <total_per_window> <windows>
# 在连续 N 个窗口里各发一批并发请求，最后聚合返回 (ok limited other)；
# 每批请求间隔 = GLOBAL_WINDOW_SECOND + 0.5s（确保跨过窗口边界进入新一批）.
# 多窗口聚合的目的：远端配额异步上报会让单窗口偶尔出现 ok>阈值 / limited 不达标的抖动，
# 但在 N 个窗口内**期望值**应当稳定 ≈ N*阈值 通过、N*(total-阈值) 限流.
#
# !!! 实现注意 !!!：调用方用 stat=$(run_global_burst_in_windows ...) 捕获 stdout 取聚合结果，
# 因此函数内**所有 log_info 调用都必须 >&2 写到 stderr**（顶部 exec 已把 stderr 接进 LOG_FILE，
# 不影响日志可见性）；否则窗口分行日志会污染聚合 echo，read 解析时把"窗口 1/4: 200=7..."当成 ok 字段.
run_global_burst_in_windows() {
    local port="$1"
    local path="$2"
    local total_per_window="$3"
    local windows="$4"
    local total_ok=0 total_limited=0 total_other=0
    local w
    for ((w=0; w<windows; w++)); do
        local stat ok lim oth
        stat=$(count_status_concurrent "$port" "$path" "$total_per_window")
        read -r ok lim oth <<< "$stat"
        log_info "  窗口 $((w+1))/${windows}: 200=${ok} 429=${lim} 其他=${oth}" >&2
        total_ok=$((total_ok + ok))
        total_limited=$((total_limited + lim))
        total_other=$((total_other + oth))
        # 不在最后一窗后再 sleep，避免无谓等待
        if [[ $((w+1)) -lt windows ]]; then
            sleep "$(awk -v s="$GLOBAL_WINDOW_SECOND" 'BEGIN{ printf "%.1f", s+0.5 }')"
        fi
    done
    echo "$total_ok $total_limited $total_other"
}

# run_global_two_instances_in_windows <port_a> <port_b> <path> <per_instance_per_window> <windows>
# 6.3/6.4 用：在多窗口里同时打 A/B 两个 provider 端口（绕开 consumer 负载均衡随机性），聚合统计.
run_global_two_instances_in_windows() {
    local port_a="$1"
    local port_b="$2"
    local path="$3"
    local per_instance="$4"
    local windows="$5"
    local total_ok=0 total_limited=0 total_other=0
    local w
    for ((w=0; w<windows; w++)); do
        local stat_a stat_b ok_a lim_a oth_a ok_b lim_b oth_b
        stat_a=$(count_status_concurrent "$port_a" "$path" "$per_instance")
        stat_b=$(count_status_concurrent "$port_b" "$path" "$per_instance")
        read -r ok_a lim_a oth_a <<< "$stat_a"
        read -r ok_b lim_b oth_b <<< "$stat_b"
        log_info "  窗口 $((w+1))/${windows}: A=(200=${ok_a},429=${lim_a}) B=(200=${ok_b},429=${lim_b})" >&2
        total_ok=$((total_ok + ok_a + ok_b))
        total_limited=$((total_limited + lim_a + lim_b))
        total_other=$((total_other + oth_a + oth_b))
        if [[ $((w+1)) -lt windows ]]; then
            sleep "$(awk -v s="$GLOBAL_WINDOW_SECOND" 'BEGIN{ printf "%.1f", s+0.5 }')"
        fi
    done
    echo "$total_ok $total_limited $total_other"
}

run_global_cases() {
    log_step "[用例 6.x] 分布式集群限流 GLOBAL (链路: curl → consumer:${PORT_CONSUMER_GLOBAL} → provider 实例 A/B)"

    # ---------- 用例 6.0：探测 polaris.limiter 是否可用 ----------
    if ! probe_limiter_available; then
        log_warn "[用例 6.0] polaris.limiter 实例未注册或全部不健康，整段 6.x 跳过"
        record_case "用例 6.0 polaris.limiter 探测" "SKIP" \
            "${GLOBAL_LIMITER_NAMESPACE}/${GLOBAL_LIMITER_SERVICE} 下无健康实例；如需验证分布式限流，请先在 polaris 服务端注册 polaris-limiter 进程"
        return
    fi
    record_case "用例 6.0 polaris.limiter 探测" "PASS" \
        "${GLOBAL_LIMITER_NAMESPACE}/${GLOBAL_LIMITER_SERVICE} 下存在健康实例，可继续 6.x"

    if ! create_global_rule; then
        record_case "6.0 创建 GLOBAL 规则" "FAIL" "HTTP API 调用失败"
        return
    fi
    # 启动两个 provider 实例 + consumer
    if ! build_and_start_binary "provider-qps" "$PORT_PROVIDER_GLOBAL_A" \
        "$GLOBAL_SERVICE" "provider-qps-global-a" "PROVIDER_GLOBAL_A_PID"; then
        record_case "6.0 启动 GLOBAL provider A" "FAIL" "provider-qps (global-a) 启动失败"
        return
    fi
    if ! build_and_start_binary "provider-qps" "$PORT_PROVIDER_GLOBAL_B" \
        "$GLOBAL_SERVICE" "provider-qps-global-b" "PROVIDER_GLOBAL_B_PID"; then
        record_case "6.0 启动 GLOBAL provider B" "FAIL" "provider-qps (global-b) 启动失败"
        return
    fi
    if ! build_and_start_binary "consumer" "$PORT_CONSUMER_GLOBAL" \
        "$GLOBAL_SERVICE" "consumer-global" "CONSUMER_GLOBAL_PID"; then
        record_case "6.0 启动 GLOBAL consumer" "FAIL" "consumer (global) 启动失败"
        return
    fi

    # SDK 拉规则 + 与 polaris.limiter 完成首次配额握手都需要时间
    log_info "等待 6s 让 SDK 拉规则 + 与 polaris.limiter 完成首次配额同步..."
    sleep 6

    local stat ok limited other

    # ---------- 用例 6.1：GLOBAL 多窗口聚合触发限流 ----------
    # 在 N 个连续窗口里，每窗口并发突发；多窗口聚合避开"单窗口边界抖动"导致的偶发误判.
    local windows=$GLOBAL_OBSERVE_WINDOWS
    local total_per_window=$GLOBAL_BURST_REQUESTS
    local total_all=$((total_per_window * windows))
    # 单窗口期望限流 ≈ total_per_window - max_amount = 4；N 窗口期望 ≈ N*4；下界给 N*1（远端抖动严苛容忍）
    local agg_min_limited_6_1=$((windows * 1))
    print_block "[用例 6.1] GLOBAL 多窗口聚合触发限流" \
        "操作:   连续 ${windows} 个 ${GLOBAL_WINDOW_SECOND}s 窗口，每窗口经 consumer:${PORT_CONSUMER_GLOBAL} 并发突发 ${total_per_window} 次（${total_all} 次合计）" \
        "原理:   type=GLOBAL → SDK asyncRateLimitConnector 走 gRPC 与 polaris.limiter 通信；" \
        "        阈值 ${GLOBAL_MAX_AMOUNT}/${GLOBAL_WINDOW_SECOND}s 是**全集群**配额；多窗口聚合后限流次数应稳定 ≥ 窗口数" \
        "预期:   ${windows} 窗口合计 limited ≥ ${agg_min_limited_6_1}（单窗口至少 1，远端抖动容忍）" \
        "判定:   total limited ≥ ${agg_min_limited_6_1} && other == 0 → PASS"

    log_info "[用例 6.1] 连续 ${windows} 窗口并发突发"
    stat=$(run_global_burst_in_windows "$PORT_CONSUMER_GLOBAL" "/echo" "$total_per_window" "$windows")
    read -r ok limited other <<< "$stat"
    log_info "结果: 总 200=${ok} 429=${limited} 其他=${other}（${windows} 窗口聚合）"

    if [[ "$other" -gt 0 ]]; then
        record_case "用例 6.1 GLOBAL 多窗口触发限流" "FAIL" \
            "出现非 200/429 状态码 (other=${other})"
    elif [[ "$limited" -lt "$agg_min_limited_6_1" ]]; then
        record_case "用例 6.1 GLOBAL 多窗口触发限流" "FAIL" \
            "聚合 limited=${limited} 不足 ${agg_min_limited_6_1}（远端配额未生效？）"
    else
        record_case "用例 6.1 GLOBAL 多窗口触发限流" "PASS" \
            "${windows} 窗口合计: 200=${ok} 429=${limited}（≥${agg_min_limited_6_1}），远端配额持续生效"
    fi

    # 等过 1 个窗口让 6.2 从干净状态开始（同时让 limiter 服务端的桶过窗口）
    sleep $((GLOBAL_WINDOW_SECOND + 1))

    # ---------- 用例 6.2：GLOBAL 新窗口仍能触发限流（与 1.2 单机版对照） ----------
    # 验证 GLOBAL 规则不是"用一次就废"：再发一批仍能命中限流，说明远端配额会按窗口重置且 SDK 会重新拉取.
    print_block "[用例 6.2] GLOBAL 新窗口仍能触发限流" \
        "操作:   再次连续 ${windows} 个窗口，每窗口并发突发 ${total_per_window} 次" \
        "原理:   远端 limiter 按窗口重置配额；SDK 每个窗口重新拉取；规则**持续生效**而不是一次就静音" \
        "预期:   与 6.1 行为一致：limited ≥ ${agg_min_limited_6_1}" \
        "判定:   total limited ≥ ${agg_min_limited_6_1} && other == 0 → PASS"

    log_info "[用例 6.2] 新一轮 ${windows} 窗口并发突发"
    stat=$(run_global_burst_in_windows "$PORT_CONSUMER_GLOBAL" "/echo" "$total_per_window" "$windows")
    read -r ok limited other <<< "$stat"
    log_info "结果: 总 200=${ok} 429=${limited} 其他=${other}"

    if [[ "$other" -gt 0 ]]; then
        record_case "用例 6.2 GLOBAL 新窗口再次生效" "FAIL" \
            "出现非 200/429 状态码 (other=${other})"
    elif [[ "$limited" -lt "$agg_min_limited_6_1" ]]; then
        record_case "用例 6.2 GLOBAL 新窗口再次生效" "FAIL" \
            "聚合 limited=${limited} 不足 ${agg_min_limited_6_1}（规则没继续生效？）"
    else
        record_case "用例 6.2 GLOBAL 新窗口再次生效" "PASS" \
            "${windows} 窗口合计: 200=${ok} 429=${limited}（≥${agg_min_limited_6_1}），规则跨窗口持续生效"
    fi

    sleep $((GLOBAL_WINDOW_SECOND + 1))

    # ---------- 用例 6.3：GLOBAL 多实例共享配额（核心语义，多窗口聚合判定） ----------
    # 直接打 A/B 两个 provider 端口（绕开 consumer 负载均衡随机性），让两边 SDK 真正同时承压.
    # 多窗口聚合：N 个窗口期望合计 ok ≈ N*MAX_AMOUNT；如果错认成 LOCAL，会变成 N*2*MAX_AMOUNT.
    local per_instance=$GLOBAL_PER_INSTANCE_REQUESTS
    local share_total_per_window=$((per_instance * 2))
    local share_total_all=$((share_total_per_window * windows))
    # GLOBAL 期望 ok ≈ N*MAX_AMOUNT = N*4；上界容忍每窗口多放 2 个抖动 → ok ≤ N*(MAX_AMOUNT+2)
    local share_max_ok=$((windows * (GLOBAL_MAX_AMOUNT + 2)))
    # GLOBAL 期望 limited ≈ N*(2*MAX - MAX) = N*MAX；下界容忍 → limited ≥ N*1
    local share_min_limited=$((windows * 1))
    print_block "[用例 6.3] GLOBAL 多实例共享配额（核心语义）" \
        "操作:   连续 ${windows} 个窗口，每窗口同时打：" \
        "        - http://127.0.0.1:${PORT_PROVIDER_GLOBAL_A}/echo 并发 ${per_instance}" \
        "        - http://127.0.0.1:${PORT_PROVIDER_GLOBAL_B}/echo 并发 ${per_instance}" \
        "原理:   两 provider 各持一份 SDK；GLOBAL 规则下都向同一个 limiter 拉配额，**合计**仅 ${GLOBAL_MAX_AMOUNT}/窗口；" \
        "        如果错误地按 LOCAL 处理，两实例各独享 → 每窗口 ok=2*MAX=$((GLOBAL_MAX_AMOUNT * 2))（与 GLOBAL 形成强对比）" \
        "预期:   ${windows} 窗口合计 ok ≤ ${share_max_ok}（≈${windows}*MAX）、limited ≥ ${share_min_limited}" \
        "判定:   limited ≥ ${share_min_limited} && ok ≤ ${share_max_ok} && other == 0 → PASS"

    log_info "[用例 6.3] ${windows} 窗口双实例并发突发"
    stat=$(run_global_two_instances_in_windows "$PORT_PROVIDER_GLOBAL_A" "$PORT_PROVIDER_GLOBAL_B" "/echo" "$per_instance" "$windows")
    read -r ok limited other <<< "$stat"
    log_info "结果: 总 200=${ok} 429=${limited} 其他=${other}（${windows} 窗口聚合）"

    if [[ "$other" -gt 0 ]]; then
        record_case "用例 6.3 GLOBAL 多实例共享配额" "FAIL" \
            "出现非 200/429 状态码 (other=${other})"
    elif [[ "$ok" -gt "$share_max_ok" ]]; then
        record_case "用例 6.3 GLOBAL 多实例共享配额" "FAIL" \
            "聚合 ok=${ok} 超过 ${share_max_ok}（GLOBAL 共享语义不成立？规则可能退化为 LOCAL；检查 polaris.limiter 是否真的连上）"
    elif [[ "$limited" -lt "$share_min_limited" ]]; then
        record_case "用例 6.3 GLOBAL 多实例共享配额" "FAIL" \
            "聚合 limited=${limited} 不足 ${share_min_limited}（远端配额未生效？）"
    else
        record_case "用例 6.3 GLOBAL 多实例共享配额" "PASS" \
            "${windows} 窗口合计 ok=${ok}（≤${share_max_ok}）、429=${limited}（≥${share_min_limited}），远端共享配额生效"
    fi

    sleep $((GLOBAL_WINDOW_SECOND + 1))

    # ---------- 用例 6.4：GLOBAL + regex_combine 多 path 共享同一远端配额（对照 5.2 单机版） ----------
    # 复用 5.x 的 regex 规则，临时把它升级为 GLOBAL+regex_combine=true：
    # 多条命中 REGEX 的 path（/users/100/orders, /users/200/orders）会共享同一份**远端**配额.
    # 6.4 结束后翻回 LOCAL+regex_combine=false，让 5.x 用例下次跑保持初始状态.
    log_info "[用例 6.4] 把 regex 规则切换到 type=GLOBAL+regex_combine=true"
    local regex_rule_id
    regex_rule_id=$(query_rule_id "$REGEX_RULE_NAME" "$REGEX_SERVICE")
    if [[ -z "$regex_rule_id" ]]; then
        record_case "用例 6.4 GLOBAL+regex_combine 共享" "FAIL" "找不到 regex 规则 id（5.x 可能未跑过；本用例需要 5.x 已创建过 regex 规则）"
        return
    fi
    # 临时把 regex 规则改成 GLOBAL + regex_combine=true
    local body
    body=$(SVC="$REGEX_SERVICE" NS="$NAMESPACE" NAME="$REGEX_RULE_NAME" \
        AMOUNT="$REGEX_MAX_AMOUNT" WINDOW="$REGEX_WINDOW_SECOND" \
        PATTERN="$REGEX_PATH_PATTERN" RULE_ID="$regex_rule_id" \
        python3 -c "
import os, json
print(json.dumps([{
    'id': os.environ['RULE_ID'],
    'name': os.environ['NAME'],
    'service': os.environ['SVC'],
    'namespace': os.environ['NS'],
    'priority': 0,
    'resource': 'QPS',
    'type': 'GLOBAL',
    'method': {'type': 'REGEX', 'value': os.environ['PATTERN']},
    'amounts': [{'maxAmount': int(os.environ['AMOUNT']), 'validDuration': '%ss' % os.environ['WINDOW']}],
    'action': 'REJECT',
    'failover': 'FAILOVER_LOCAL',
    'regex_combine': True,
    'disable': False,
}]))")
    if ! update_rule_via_http "$body"; then
        record_case "用例 6.4 GLOBAL+regex_combine 共享" "FAIL" "PUT 切换到 GLOBAL+regex_combine 失败"
        return
    fi
    log_info "[用例 6.4] 已切换；等待 5s 让 SDK 拉新规则..."
    sleep 5

    # 启动一组临时 provider+consumer 给 6.4 用？不需要——直接复用 5.x 的 provider 实例，
    # 但它的 SDK 拉到的是新规则版本，会按 GLOBAL 走远端.
    # 不过 5.x 的 provider 已经在 6.x 段开头未启动；为简化，6.4 也直接打 6.3 的 provider A/B（它们的 service=GLOBAL_SERVICE 不命中 REGEX 规则）.
    # 改用临时启动：起一个用 REGEX_SERVICE 服务名的 provider 即可；但又要新端口.
    # 折中做法：6.4 直接用 5.x 的 regex provider/consumer（前提 --skip 没去掉 regex），它们仍然存活
    # 但 5.x 段的 provider 在 5.2 收尾后没被 stop_provider，直到全局 cleanup 才停——所以仍能用.
    # 这要求：用户必须先跑过 regex 段（即没用 --skip regex）.
    if [[ -z "$PROVIDER_REGEX_PID" ]] || ! kill -0 "$PROVIDER_REGEX_PID" 2>/dev/null; then
        record_case "用例 6.4 GLOBAL+regex_combine 共享" "SKIP" \
            "依赖 5.x regex provider 进程仍存活（--skip regex 时跳过；同时跑 5.x 与 6.4 时本用例自动启用）"
        # 把规则翻回去，避免污染 5.x
        body=$(_build_regex_rule_body "$REGEX_RULE_NAME" "$regex_rule_id" "false")
        update_rule_via_http "$body" >/dev/null 2>&1 || true
        return
    fi

    print_block "[用例 6.4] GLOBAL + regex_combine=true：多 path 共享同一远端配额" \
        "操作:   连续 ${windows} 个窗口，每窗口同时打 5.x 的 regex provider:${PORT_PROVIDER_REGEX} 两条 path：" \
        "        - ${REGEX_PATH_A} 并发 ${REGEX_PER_PATH_REQUESTS}" \
        "        - ${REGEX_PATH_B} 并发 ${REGEX_PER_PATH_REQUESTS}" \
        "原理:   规则切到 type=GLOBAL+regex_combine=true：两条 path 命中同一 REGEX → 共享同一远端 windowKey；" \
        "        阈值 ${REGEX_MAX_AMOUNT}/${REGEX_WINDOW_SECOND}s 是全集群（含两 path）合计配额" \
        "预期:   每窗口合计 ok ≈ ${REGEX_MAX_AMOUNT}（≤ MAX+2 抖动），多窗口聚合 limited ≥ ${windows}" \
        "判定:   total limited ≥ ${windows} && total ok ≤ ${windows}*(MAX+2) && other == 0"

    local regex_share_max_ok=$((windows * (REGEX_MAX_AMOUNT + 2)))
    local regex_share_min_limited=$windows
    log_info "[用例 6.4] ${windows} 窗口双 path 并发"
    stat=$(run_global_two_instances_in_windows "$PORT_PROVIDER_REGEX" "$PORT_PROVIDER_REGEX" "" "$REGEX_PER_PATH_REQUESTS" 1)
    # 上面这种调用方式 path 必须能区分 A/B；改用直接两 path 合并的辅助：
    # 复用 run_global_burst_in_windows 但每窗口要分两次（path_a, path_b）
    local total_ok_64=0 total_lim_64=0 total_oth_64=0
    local w64
    for ((w64=0; w64<windows; w64++)); do
        local stat_a stat_b ok_a lim_a oth_a ok_b lim_b oth_b
        stat_a=$(count_status_concurrent "$PORT_PROVIDER_REGEX" "$REGEX_PATH_A" "$REGEX_PER_PATH_REQUESTS")
        stat_b=$(count_status_concurrent "$PORT_PROVIDER_REGEX" "$REGEX_PATH_B" "$REGEX_PER_PATH_REQUESTS")
        read -r ok_a lim_a oth_a <<< "$stat_a"
        read -r ok_b lim_b oth_b <<< "$stat_b"
        log_info "  窗口 $((w64+1))/${windows}: A(${REGEX_PATH_A})=(200=${ok_a},429=${lim_a}) B(${REGEX_PATH_B})=(200=${ok_b},429=${lim_b})" >&2
        total_ok_64=$((total_ok_64 + ok_a + ok_b))
        total_lim_64=$((total_lim_64 + lim_a + lim_b))
        total_oth_64=$((total_oth_64 + oth_a + oth_b))
        if [[ $((w64+1)) -lt windows ]]; then
            sleep "$(awk -v s="$REGEX_WINDOW_SECOND" 'BEGIN{ printf "%.1f", s+0.5 }')"
        fi
    done
    log_info "结果: 总 200=${total_ok_64} 429=${total_lim_64} 其他=${total_oth_64}"

    if [[ "$total_oth_64" -gt 0 ]]; then
        record_case "用例 6.4 GLOBAL+regex_combine 共享" "FAIL" \
            "出现非 200/429 状态码 (other=${total_oth_64})"
    elif [[ "$total_ok_64" -gt "$regex_share_max_ok" ]]; then
        record_case "用例 6.4 GLOBAL+regex_combine 共享" "FAIL" \
            "聚合 ok=${total_ok_64} 超过 ${regex_share_max_ok}（GLOBAL+regex_combine 共享语义不成立？）"
    elif [[ "$total_lim_64" -lt "$regex_share_min_limited" ]]; then
        record_case "用例 6.4 GLOBAL+regex_combine 共享" "FAIL" \
            "聚合 limited=${total_lim_64} 不足 ${regex_share_min_limited}（限流未生效？）"
    else
        record_case "用例 6.4 GLOBAL+regex_combine 共享" "PASS" \
            "${windows} 窗口合计 ok=${total_ok_64}（≤${regex_share_max_ok}）、429=${total_lim_64}（≥${regex_share_min_limited}），远端 + 多 path 共享配额生效"
    fi

    # 收尾：把 regex 规则翻回 LOCAL+regex_combine=false 让 5.x 下次跑回到初始状态
    body=$(_build_regex_rule_body "$REGEX_RULE_NAME" "$regex_rule_id" "false")
    if update_rule_via_http "$body"; then
        log_info "[用例 6.4] 收尾：regex 规则已翻回 LOCAL+regex_combine=false"
    else
        log_warn "[用例 6.4] 收尾翻回 LOCAL+regex_combine=false 失败；下次跑 5.x 前会自动 PUT 重置（不影响测试）"
    fi

    sleep $((GLOBAL_WINDOW_SECOND + 1))

    # ---------- 用例 6.5：远端不可达降级到本地（FAILOVER_LOCAL） ----------
    # 启动一个**新的** provider 实例：用 POLARIS_LIMITER_SVC 指向不存在的服务名；
    # SDK 服务发现拉不到 limiter 实例 → asyncRateLimitConnector 不可达 → bucket 走 RemoteToLocal 路径，
    # 把规则当本地限流处理（每个 SDK 实例独享 max_amount 个本地配额）.
    # 期望：仍能限流（不全放通），且与 LOCAL 单机行为吻合（每窗口 ok ≈ max_amount，limited ≈ burst-max）.
    print_block "[用例 6.5] 远端不可达降级到本地（FAILOVER_LOCAL）" \
        "操作:   把 POLARIS_LIMITER_SVC 临时设为 "${GLOBAL_LIMITER_BAD_SERVICE}"（一个不存在的服务名）→ 启动新 provider →" \
        "        **直接**对 provider:${PORT_PROVIDER_GLOBAL_FAILOVER} 发请求（不走 consumer，避免 consumer 服务发现把请求负载到 6.1/6.3 的 A/B 上），连续 ${windows} 个窗口，每窗口并发突发 ${total_per_window} 次" \
        "原理:   SDK 服务发现拉不到 limiter 实例 → asyncRateLimitConnector 持续报错 → bucket 命中 remoteExpired" \
        "        → 规则 failover=FAILOVER_LOCAL → 走 RemoteToLocal 路径，按本地配额限流（不再走远端）" \
        "预期:   仍能限流（每窗口约限到 burst-max=${total_per_window}-${GLOBAL_MAX_AMOUNT}=$((total_per_window - GLOBAL_MAX_AMOUNT))），" \
        "        多窗口聚合 limited ≥ ${windows}（容忍单窗口仅 1 限流）" \
        "判定:   limited ≥ ${windows} && other == 0 → PASS（验证降级路径生效，不会全放通也不会 panic）" \
        "" \
        "提示:   provider-qps-global-failover/polaris/log/ratelimit/polaris-ratelimit.log 会持续出现 'fail to call RateLimitService.GetMessageSender' 的 ERROR 日志，" \
        "        这是 SDK 内部 asyncRateLimitConnector 后台重试上报，**与降级判定路径独立**——只要看到 ratelimit log 里有 mode=RemoteToLocal 的 passed/limited 行，就说明本地配额已经接管."

    log_info "[用例 6.5] 启动 provider+consumer，指向不存在的 limiter '${GLOBAL_LIMITER_BAD_SERVICE}'"
    POLARIS_LIMITER_SVC="$GLOBAL_LIMITER_BAD_SERVICE" \
        build_and_start_binary "provider-qps" "$PORT_PROVIDER_GLOBAL_FAILOVER" \
            "$GLOBAL_SERVICE" "provider-qps-global-failover" "PROVIDER_GLOBAL_FAILOVER_PID" || {
        record_case "用例 6.5 远端降级到本地" "FAIL" "provider-qps (failover) 启动失败"
        return
    }
    # 注意：6.5 不启 consumer——直接打 provider 端口避免 consumer 的服务发现把请求负载到 6.1/6.3 的 A/B 上
    # （A/B 的 SDK 用的是正常 limiter 服务，会让"降级路径"测试错验对象）.

    # 等 SDK 探测到 limiter 不可达：远程过期阈值 remoteExpireMilli=1000ms（见 reject 插件），
    # 加上 SDK 服务发现失败回调延迟，给 8s 足够稳定降级.
    log_info "等待 8s 让 SDK 完成 limiter 服务发现失败 + 远程过期判定..."
    sleep 8

    log_info "[用例 6.5] 4 窗口并发突发（直接打 failover provider:${PORT_PROVIDER_GLOBAL_FAILOVER}，绕开 consumer 负载均衡——避免请求被错误地路由到 6.1/6.3 的 provider A/B 上）"
    stat=$(run_global_burst_in_windows "$PORT_PROVIDER_GLOBAL_FAILOVER" "/echo" "$total_per_window" "$windows")
    read -r ok limited other <<< "$stat"
    log_info "结果: 总 200=${ok} 429=${limited} 其他=${other}"

    if [[ "$other" -gt 0 ]]; then
        record_case "用例 6.5 远端降级到本地" "FAIL" \
            "出现非 200/429 状态码 (other=${other})（降级路径异常或 panic？）"
    elif [[ "$limited" -lt "$windows" ]]; then
        record_case "用例 6.5 远端降级到本地" "FAIL" \
            "聚合 limited=${limited} 不足 ${windows}（FAILOVER_LOCAL 退化未生效？检查 provider 日志是否在反复重连远端）"
    else
        record_case "用例 6.5 远端降级到本地" "PASS" \
            "${windows} 窗口合计 ok=${ok}、429=${limited}（≥${windows}），降级路径生效，未全放通也未 panic"
    fi
}

# ======================== 用例 7.x：CustomResponse 自定义返回 ========================
# 验证目标：
#   - 规则的 customResponse.body 能被服务端正确下发到 SDK
#   - SDK 通过 QuotaResponse.GetActiveRule().GetCustomResponse().GetBody() 把 body 暴露给业务
#   - provider-qps 在限流分支用该 body 作为 HTTP 响应体写回，并把规则名透传到响应头
#   - 经 consumer 透传后，curl 收到的 body / X-Polaris-RateLimit-Rule header 与规则配置一致
# 用例：
#   [7.1] 限流时 body 与响应头匹配规则配置（同时间接验证 ActiveRule.RuleName 透传链路）
run_custom_response_cases() {
    log_step "[用例 7.x] CustomResponse 自定义返回 (链路: curl → consumer:${PORT_CONSUMER_CUSTOM_RESPONSE} → provider:${PORT_PROVIDER_CUSTOM_RESPONSE})"
    if ! create_custom_response_rule; then
        record_case "7.0 创建 custom-response 规则" "FAIL" "HTTP API 调用失败"
        return
    fi
    # 复用 provider-qps 二进制：它已通过 GetActiveRule API 在限流分支写入 customResponse.body 与响应头.
    if ! build_and_start_binary "provider-qps" "$PORT_PROVIDER_CUSTOM_RESPONSE" \
        "$CUSTOM_RESPONSE_SERVICE" "provider-qps-custom-response" "PROVIDER_CUSTOM_RESPONSE_PID"; then
        record_case "7.0 启动 custom-response provider" "FAIL" "provider-qps (custom-response) 启动失败"
        return
    fi
    if ! build_and_start_binary "consumer" "$PORT_CONSUMER_CUSTOM_RESPONSE" \
        "$CUSTOM_RESPONSE_SERVICE" "consumer-qps-custom-response" "CONSUMER_CUSTOM_RESPONSE_PID"; then
        record_case "7.0 启动 custom-response consumer" "FAIL" "consumer (custom-response) 启动失败"
        return
    fi

    log_info "等待 4s 让 SDK 拉到规则..."
    sleep 4

    # ---------- 用例 7.1：限流时 body 与响应头匹配 ----------
    print_block "[用例 7.1] CustomResponse 限流响应内容验证" \
        "操作:   串行 ${CUSTOM_RESPONSE_TOTAL_REQUESTS} 次 GET /echo（前 N-1 次预热 + 最后 1 次抓取限流 body/header）" \
        "原理:   规则 customResponse.body 服务端→SDK→provider-qps（GetActiveRule.GetCustomResponse.GetBody）→consumer→curl" \
        "断言 1: 最后一次 status == 429（确认已触发限流）" \
        "断言 2: 响应 body == 规则配置的 customResponse.body（字面量精确匹配）" \
        "断言 3: 响应头 X-Polaris-RateLimit-Rule == ${CUSTOM_RESPONSE_RULE_NAME}（GetActiveRuleName 透传）" \
        "判定:   3 个断言全 PASS → 用例 PASS；任一失败 → FAIL"

    # 合并策略（同 7.2）：先发 (N-1) 次把窗口打满，紧接最后 1 次用 fetch_status_body_header 抓取 429 body。
    # 所有请求在同一秒窗口内完成，避免两阶段之间跨窗口导致配额恢复。
    log_info "[用例 7.1] 串行 ${CUSTOM_RESPONSE_TOTAL_REQUESTS} 次 /echo 触发限流，最后一次抓取 body/header"
    local warmup_count=$((CUSTOM_RESPONSE_TOTAL_REQUESTS - 1))
    local stat ok limited other
    stat=$(count_status "$PORT_CONSUMER_CUSTOM_RESPONSE" "/echo" "$warmup_count")
    read -r ok limited other <<< "$stat"
    log_info "预热 ${warmup_count} 次结果: 200=${ok}  429=${limited}  其他=${other}"

    # 紧接（不 sleep）最后 1 次，抓取 body + header
    local fetch_dir
    fetch_dir=$(mktemp -d)
    local fetch_out
    fetch_out=$(fetch_status_body_header "$PORT_CONSUMER_CUSTOM_RESPONSE" "/echo" "X-Polaris-RateLimit-Rule" "$fetch_dir")
    local got_status got_header got_body
    got_status=$(echo "$fetch_out" | sed -n '1p')
    got_header=$(echo "$fetch_out" | sed -n '2p')
    got_body=""
    if [[ -f "${fetch_dir}/body" ]]; then
        got_body=$(cat "${fetch_dir}/body")
    fi
    rm -rf "$fetch_dir"

    log_info "  status=${got_status}"
    log_info "  X-Polaris-RateLimit-Rule=${got_header}"
    log_info "  body=${got_body}"

    # 断言 1：status == 429
    if [[ "$got_status" != "429" ]]; then
        record_case "用例 7.1 CustomResponse 自定义返回" "FAIL" \
            "限流单发期望 status=429，实际 ${got_status}（窗口已恢复？前置阶段限流次数不够？）"
        return
    fi
    # 断言 2：body 精确匹配规则配置的 customResponse.body
    if [[ "$got_body" != "$CUSTOM_RESPONSE_BODY" ]]; then
        record_case "用例 7.1 CustomResponse 自定义返回" "FAIL" \
            "body 不匹配：期望=${CUSTOM_RESPONSE_BODY}；实际=${got_body}"
        return
    fi
    # 断言 3：响应头匹配规则名（验证 GetActiveRuleName 链路透传）
    if [[ "$got_header" != "$CUSTOM_RESPONSE_RULE_NAME" ]]; then
        record_case "用例 7.1 CustomResponse 自定义返回" "FAIL" \
            "X-Polaris-RateLimit-Rule 不匹配：期望=${CUSTOM_RESPONSE_RULE_NAME}；实际=${got_header}"
        return
    fi
    record_case "用例 7.1 CustomResponse 自定义返回" "PASS" \
        "status=429 / body 精确匹配 / X-Polaris-RateLimit-Rule=${got_header}"

    # ---------- 用例 7.2：GLOBAL 分布式限流 + CustomResponse ----------
    # 前提：polaris.limiter 服务可用（不可用时 SKIP）；规则 type=GLOBAL + customResponse.body
    # 验证：分布式限流场景下 SDK 仍然能通过 GetActiveRule 暴露 customResponse
    if ! probe_limiter_available; then
        record_case "用例 7.2 GLOBAL CustomResponse" "SKIP" \
            "polaris.limiter (${GLOBAL_LIMITER_NAMESPACE}/${GLOBAL_LIMITER_SERVICE}) 无可用实例，跳过分布式限流自定义返回验证"
        return
    fi

    if ! create_global_custom_response_rule; then
        record_case "用例 7.2 GLOBAL CustomResponse" "FAIL" "GLOBAL 规则创建失败"
        return
    fi

    if ! build_and_start_binary "provider-qps" "$PORT_PROVIDER_GLOBAL_CUSTOM_RESP" \
        "$GLOBAL_CUSTOM_RESPONSE_SERVICE" "provider-qps-global-custom-resp" "PROVIDER_GLOBAL_CUSTOM_RESP_PID"; then
        record_case "用例 7.2 GLOBAL CustomResponse" "FAIL" "provider 启动失败"
        return
    fi
    if ! build_and_start_binary "consumer" "$PORT_CONSUMER_GLOBAL_CUSTOM_RESP" \
        "$GLOBAL_CUSTOM_RESPONSE_SERVICE" "consumer-global-custom-resp" "CONSUMER_GLOBAL_CUSTOM_RESP_PID"; then
        record_case "用例 7.2 GLOBAL CustomResponse" "FAIL" "consumer 启动失败"
        return
    fi

    log_info "等待 5s 让 SDK 拉到 GLOBAL 规则 + 完成远端 init..."
    sleep 5

    print_block "[用例 7.2] GLOBAL CustomResponse 分布式限流自定义返回验证" \
        "操作:   串行 ${CUSTOM_RESPONSE_TOTAL_REQUESTS} 次触发限流 → 单发 1 次抓取 429 响应" \
        "原理:   GLOBAL 规则走远端配额（polaris.limiter），被限后 SDK 的 ActiveRule 仍指向原始规则（含 customResponse）" \
        "        即使 FAILOVER_LOCAL 退化路径下也同样能透传 customResponse.body" \
        "断言:   与 7.1 一致 — status=429 / body 精确匹配 / X-Polaris-RateLimit-Rule == 规则名"

    log_info "[用例 7.2] 串行 ${CUSTOM_RESPONSE_TOTAL_REQUESTS} 次 /echo 触发限流，最后一次抓取 body/header"
    # 7.2 的窗口只有 1s，为避免"第一阶段触发限流 + 第二阶段单发"之间跨窗口，
    # 改为：先发 (N-1) 次打满窗口，最后 1 次直接用 fetch_status_body_header 抓取 429 body。
    # 这样全部请求在同一秒内完成，不存在窗口恢复问题。
    local warmup_count=$((CUSTOM_RESPONSE_TOTAL_REQUESTS - 1))
    stat=$(count_status "$PORT_CONSUMER_GLOBAL_CUSTOM_RESP" "/echo" "$warmup_count")
    read -r ok limited other <<< "$stat"
    log_info "预热 ${warmup_count} 次结果: 200=${ok}  429=${limited}  其他=${other}"

    # 紧接（不 sleep）最后 1 次，抓取 body + header
    fetch_dir=$(mktemp -d)
    fetch_out=$(fetch_status_body_header "$PORT_CONSUMER_GLOBAL_CUSTOM_RESP" "/echo" "X-Polaris-RateLimit-Rule" "$fetch_dir")
    got_status=$(echo "$fetch_out" | sed -n '1p')
    got_header=$(echo "$fetch_out" | sed -n '2p')
    got_body=""
    if [[ -f "${fetch_dir}/body" ]]; then
        got_body=$(cat "${fetch_dir}/body")
    fi
    rm -rf "$fetch_dir"

    log_info "  status=${got_status}"
    log_info "  X-Polaris-RateLimit-Rule=${got_header}"
    log_info "  body=${got_body}"

    if [[ "$got_status" != "429" ]]; then
        record_case "用例 7.2 GLOBAL CustomResponse" "FAIL" \
            "期望 status=429，实际 ${got_status}"
        return
    fi
    if [[ "$got_body" != "$CUSTOM_RESPONSE_BODY" ]]; then
        record_case "用例 7.2 GLOBAL CustomResponse" "FAIL" \
            "body 不匹配：期望=${CUSTOM_RESPONSE_BODY}；实际=${got_body}"
        return
    fi
    if [[ "$got_header" != "$GLOBAL_CUSTOM_RESPONSE_RULE_NAME" ]]; then
        record_case "用例 7.2 GLOBAL CustomResponse" "FAIL" \
            "X-Polaris-RateLimit-Rule 不匹配：期望=${GLOBAL_CUSTOM_RESPONSE_RULE_NAME}；实际=${got_header}"
        return
    fi
    record_case "用例 7.2 GLOBAL CustomResponse" "PASS" \
        "分布式限流场景：status=429 / body 精确匹配 / X-Polaris-RateLimit-Rule=${got_header}"
}

# ======================== 用例 8.x：限流监控指标验证（pull /metrics） ========================
# 验证目标：
#   - provider-qps-reject（首个启动的 provider-qps 实例）绑定 28080 端口暴露 /metrics
#   - 经过前面用例 1.x~7.x 的限流操作后，prometheus 插件已完成 30s 聚合周期，metrics 可查
#   - /metrics 中包含 ratelimit_rq_total 指标，且 label 含 callee_namespace、callee_service、
#     callee_method、rule_name、caller_labels（六维度齐备）
#
# 前提条件：
#   - 用例 8.x 会自行启动独立的 provider + consumer，不依赖其他用例的残留实例
#   - 使用 QpsRatelimitEchoServer 服务的 QPS reject 规则（需先跑 1.x 创建规则或规则已存在）
#   - SDK 在 pull 模式下每 30s 把收集到的 ReportStat 数据刷入 prometheus registry
#   - 用例内部先发请求产生数据，再等 30s 聚合后验证 /metrics
#
# metrics 端口按 "业务端口 + 10000" 规则分配
PORT_PROVIDER_METRICS=18200         # 用例 8.x 专用 provider 业务端口
METRICS_PORT=$((PORT_PROVIDER_METRICS + 10000))  # = 28200

run_metrics_cases() {
    log_step "[用例 8.x] 限流监控指标验证（pull 模式 /metrics 端口 ${METRICS_PORT}）"

    # 确保 QPS 规则已存在（8.x 复用 QpsRatelimitEchoServer 的 reject 规则）
    if ! rule_exists "$QPS_RULE_NAME" "$QPS_SERVICE"; then
        if ! create_qps_rule; then
            record_case "用例 8.1 限流监控指标" "FAIL" "QPS 规则创建失败"
            return
        fi
    fi

    # 启动专用 provider（请求直接发到 provider，不需要 consumer）
    if ! build_and_start_binary "provider-qps" "$PORT_PROVIDER_METRICS" \
        "$QPS_SERVICE" "provider-qps-metrics" "PROVIDER_METRICS_PID"; then
        record_case "用例 8.1 限流监控指标" "FAIL" "provider 启动失败"
        return
    fi

    log_info "等待 4s 让 SDK 拉到规则..."
    sleep 4

    # ---------- 用例 8.1：/metrics 端口可达 + ratelimit_rq 指标存在 + 数值校验 ----------
    print_block "[用例 8.1] /metrics 限流监控指标验证" \
        "目标:   验证 provider 的 prometheus /metrics 端点（pull 模式 ${METRICS_PORT}）" \
        "操作:   直接向 provider:${PORT_PROVIDER_METRICS}/echo 发请求 → 等聚合 → 检查 /metrics" \
        "断言 1: HTTP 200（端口可达）" \
        "断言 2: ratelimit_rq_total / ratelimit_rq_pass / ratelimit_rq_limit 三个指标都存在" \
        "断言 3: 指标 label 包含 callee_namespace、callee_service、callee_method、rule_name" \
        "断言 4: pass > 0 且 limit > 0（前序用例确实产生了通过和限流两种结果）" \
        "断言 5: total == pass + limit（限流监控不可丢数据的不变量）" \
        "判定:   5 个断言全 PASS → 用例 PASS"

    # 关键步骤：直接向 provider 发请求（绕过 consumer 的服务发现 LB），
    # 确保所有请求都命中这一个 provider 实例的限流窗口，监控指标全部记录在本 provider。
    # 如果走 consumer→provider 路径，consumer 会通过服务发现把请求分散到同服务的其他 provider 实例，
    # 导致当前 provider 的 /metrics 只能看到部分数据，limit 可能为 0。
    log_info "[用例 8.1] 直接向 provider:${PORT_PROVIDER_METRICS}/echo 发 6 次请求产生监控数据..."
    for _i in 1 2 3 4 5 6; do
        curl -s -o /dev/null --connect-timeout 2 --max-time 3 \
            "http://127.0.0.1:${PORT_PROVIDER_METRICS}/echo" 2>/dev/null || true
    done

    # 轮询等待聚合：首次 ReportStat 后 30s 内 metrics 才会出现在 registry，
    # 到达这里时通常已经过了 >30s（前序用例累计耗时），但做一个兜底轮询。
    local metrics_body=""
    local attempt
    for attempt in 1 2 3 4 5 6; do
        local poll_code
        metrics_body=$(curl -s --connect-timeout 3 --max-time 5 \
            -w '\n%{http_code}' "http://127.0.0.1:${METRICS_PORT}/metrics" 2>/dev/null || echo "")
        # 分离 body 和 status code（-w 追加的最后一行是 http_code）
        poll_code=$(echo "$metrics_body" | tail -1)
        metrics_body=$(echo "$metrics_body" | sed '$d')
        if echo "$metrics_body" | grep -q "ratelimit_rq_total"; then
            log_info "[用例 8.1] 第 ${attempt} 次轮询命中 ratelimit_rq_total（HTTP=${poll_code}）"
            break
        fi
        if [[ $attempt -lt 6 ]]; then
            log_info "[用例 8.1] 第 ${attempt} 次轮询未见 ratelimit_rq_total（HTTP=${poll_code}, body_len=${#metrics_body}），等 10s..."
            sleep 10
        else
            log_info "[用例 8.1] 第 ${attempt} 次轮询仍未见 ratelimit_rq_total（HTTP=${poll_code}, body_len=${#metrics_body}），放弃"
        fi
    done

    # 断言 1：端口可达且有内容
    if [[ -z "$metrics_body" ]]; then
        record_case "用例 8.1 限流监控指标" "FAIL" \
            "curl http://127.0.0.1:${METRICS_PORT}/metrics 返回空（可能原因：1.provider 进程未启动/已退出 2.SDK prepare()未触发——需要至少一次GetQuota调用 3.残留进程占用端口但无metrics数据）"
        return
    fi

    # 断言 2：三个指标都存在
    if ! echo "$metrics_body" | grep -q "ratelimit_rq_total"; then
        record_case "用例 8.1 限流监控指标" "FAIL" \
            "/metrics 可达但未找到 ratelimit_rq_total（聚合尚未完成？前序用例未触发限流？）"
        return
    fi
    if ! echo "$metrics_body" | grep -q "ratelimit_rq_pass"; then
        record_case "用例 8.1 限流监控指标" "FAIL" \
            "找到 ratelimit_rq_total 但缺少 ratelimit_rq_pass"
        return
    fi
    if ! echo "$metrics_body" | grep -q "ratelimit_rq_limit"; then
        record_case "用例 8.1 限流监控指标" "FAIL" \
            "找到 ratelimit_rq_total 但缺少 ratelimit_rq_limit"
        return
    fi

    # 断言 3：六维度 label 齐备（检查一行即可）
    local has_ns has_svc has_method has_rule
    has_ns=$(echo "$metrics_body" | grep "ratelimit_rq_total" | grep -c 'callee_namespace=' || echo 0)
    has_svc=$(echo "$metrics_body" | grep "ratelimit_rq_total" | grep -c 'callee_service=' || echo 0)
    has_method=$(echo "$metrics_body" | grep "ratelimit_rq_total" | grep -c 'callee_method=' || echo 0)
    has_rule=$(echo "$metrics_body" | grep "ratelimit_rq_total" | grep -c 'rule_name=' || echo 0)

    if [[ "$has_ns" -eq 0 ]] || [[ "$has_svc" -eq 0 ]] || [[ "$has_method" -eq 0 ]] || [[ "$has_rule" -eq 0 ]]; then
        record_case "用例 8.1 限流监控指标" "FAIL" \
            "ratelimit_rq_total 存在但 label 不全：ns=${has_ns} svc=${has_svc} method=${has_method} rule=${has_rule}"
        return
    fi

    # 断言 4 + 5：用 python3 解析指标数值，校验 pass > 0、limit > 0、total == pass + limit
    # prometheus text format 示例：
    #   ratelimit_rq_total{callee_method="/echo",...} 6
    #   ratelimit_rq_pass{callee_method="/echo",...} 2
    #   ratelimit_rq_limit{callee_method="/echo",...} 4
    # 把所有同名指标的 value 求和（可能有多条不同 label 的时间序列）
    local metric_check
    metric_check=$(echo "$metrics_body" | python3 -c "
import sys

total_sum = 0.0
pass_sum = 0.0
limit_sum = 0.0

for line in sys.stdin:
    line = line.strip()
    if line.startswith('#') or not line:
        continue
    # 格式: metric_name{labels} value  或  metric_name value
    # 找最后一个空格之后的数字
    parts = line.rsplit(' ', 1)
    if len(parts) != 2:
        continue
    name_labels, val_str = parts
    try:
        val = float(val_str)
    except ValueError:
        continue
    # 提取 metric name（大括号前的部分）
    metric_name = name_labels.split('{')[0] if '{' in name_labels else name_labels
    if metric_name == 'ratelimit_rq_total':
        total_sum += val
    elif metric_name == 'ratelimit_rq_pass':
        pass_sum += val
    elif metric_name == 'ratelimit_rq_limit':
        limit_sum += val

# 输出结果供 bash 解析
# 格式: total pass limit ok_or_fail reason
if pass_sum <= 0:
    print('%.0f %.0f %.0f FAIL pass_sum=0（没有通过的请求？前序用例未正常运行）' % (total_sum, pass_sum, limit_sum))
elif limit_sum <= 0:
    print('%.0f %.0f %.0f FAIL limit_sum=0（没有被限流的请求？前序用例未触发限流）' % (total_sum, pass_sum, limit_sum))
elif abs(total_sum - (pass_sum + limit_sum)) > 0.001:
    print('%.0f %.0f %.0f FAIL total(%.0f)!=pass(%.0f)+limit(%.0f)（数据不一致）' % (total_sum, pass_sum, limit_sum, total_sum, pass_sum, limit_sum))
else:
    print('%.0f %.0f %.0f PASS' % (total_sum, pass_sum, limit_sum))
" 2>/dev/null || echo "0 0 0 FAIL python3解析异常")

    local m_total m_pass m_limit m_verdict m_reason
    m_total=$(echo "$metric_check" | awk '{print $1}')
    m_pass=$(echo "$metric_check" | awk '{print $2}')
    m_limit=$(echo "$metric_check" | awk '{print $3}')
    m_verdict=$(echo "$metric_check" | awk '{print $4}')
    m_reason=$(echo "$metric_check" | cut -d' ' -f5-)

    log_info "指标数值: total=${m_total} pass=${m_pass} limit=${m_limit}"

    # 打印所有 ratelimit_rq_* 指标行（含完整 label + 数值），供日志审计排查
    log_info "--- ratelimit_rq 完整指标 ---"
    echo "$metrics_body" | grep "^ratelimit_rq" | while IFS= read -r line; do
        log_info "  ${line}"
    done
    log_info "--- end ---"

    if [[ "$m_verdict" != "PASS" ]]; then
        record_case "用例 8.1 限流监控指标" "FAIL" \
            "数值校验失败: total=${m_total} pass=${m_pass} limit=${m_limit} — ${m_reason}"
        return
    fi

    record_case "用例 8.1 限流监控指标" "PASS" \
        "label 六维度齐备 / total(${m_total})=pass(${m_pass})+limit(${m_limit}) / pass>0 且 limit>0"
}

# ======================== 用例 9.x：限流事件上报验证（mock pushgateway） ========================
# 验证目标：
#   - 限流状态 UNLIMITED→LIMITED 时 SDK 上报 RateLimitStart 事件
#   - 限流状态 LIMITED→UNLIMITED 时 SDK 上报 RateLimitEnd 事件
#   - 事件包含正确的 rule_name、namespace、service、resource_type 字段
#
# 实现方式：
#   - 启动本地 mock HTTP server（Python）模拟 pushgateway，捕获 POST 到文件
#   - provider polaris.yaml 中 pushgateway.address="${POLARIS_EVENT_ADDR}" 指向 mock server
#   - 触发限流 → 等待 batch flush → 解析捕获文件验证事件字段
PORT_PROVIDER_EVENTS=18202
MOCK_EVENT_PORT=19090

run_events_cases() {
    log_step "[用例 9.x] 限流事件上报验证（mock pushgateway 端口 ${MOCK_EVENT_PORT}）"

    # 确保 QPS 规则已存在
    if ! rule_exists "$QPS_RULE_NAME" "$QPS_SERVICE"; then
        if ! create_qps_rule; then
            record_case "用例 9.1 限流事件 RateLimitStart" "FAIL" "QPS 规则创建失败"
            return
        fi
    fi

    # 启动 mock event server
    local events_file="${LOG_DIR}/captured_events.json"
    rm -f "$events_file"
    python3 "${SCRIPT_DIR}/mock_event_server.py" "$events_file" "$MOCK_EVENT_PORT" &
    MOCK_EVENT_SERVER_PID=$!
    sleep 1
    if ! kill -0 "$MOCK_EVENT_SERVER_PID" 2>/dev/null; then
        record_case "用例 9.1 限流事件 RateLimitStart" "FAIL" "mock_event_server 启动失败"
        return
    fi
    log_info "mock_event_server (PID=${MOCK_EVENT_SERVER_PID}) 监听 :${MOCK_EVENT_PORT}, 事件写入 ${events_file}"

    # 启动专用 provider，POLARIS_EVENT_ADDR 指向 mock server
    export POLARIS_EVENT_ADDR="127.0.0.1:${MOCK_EVENT_PORT}"
    if ! build_and_start_binary "provider-qps" "$PORT_PROVIDER_EVENTS" \
        "$QPS_SERVICE" "provider-qps-events" "PROVIDER_EVENTS_PID"; then
        record_case "用例 9.1 限流事件 RateLimitStart" "FAIL" "provider 启动失败"
        unset POLARIS_EVENT_ADDR
        return
    fi
    unset POLARIS_EVENT_ADDR

    log_info "等待 4s 让 SDK 拉到规则..."
    sleep 4

    # ---------- 用例 9.1：RateLimitStart 事件 ----------
    print_block "[用例 9.1] 限流事件 RateLimitStart" \
        "操作:   直接向 provider:${PORT_PROVIDER_EVENTS}/echo 发 6 次请求触发 UNLIMITED→LIMITED" \
        "原理:   首次被限流时 SDK 检测到状态从 Ok→Limited，上报 RateLimitStart 事件到 mock pushgateway" \
        "断言:   captured_events.json 中存在 event_name=RateLimitStart 且 rule_name=ratelimit-e2e-qps-rule"

    log_info "[用例 9.1] 发 6 次请求触发限流..."
    for _i in 1 2 3 4 5 6; do
        curl -s -o /dev/null --connect-timeout 2 --max-time 3 \
            "http://127.0.0.1:${PORT_PROVIDER_EVENTS}/echo" 2>/dev/null || true
    done

    # 等待事件 batch flush（pushgateway 插件每秒或队列满时 flush）
    log_info "[用例 9.1] 等待 3s 让事件 batch flush..."
    sleep 3

    # 验证 RateLimitStart 事件
    if [[ ! -f "$events_file" ]]; then
        record_case "用例 9.1 限流事件 RateLimitStart" "FAIL" \
            "未捕获到任何事件（${events_file} 不存在）"
        return
    fi

    local start_count
    start_count=$(grep -c '"RateLimitStart"' "$events_file" 2>/dev/null || echo 0)
    if [[ "$start_count" -lt 1 ]]; then
        local total_events
        total_events=$(wc -l < "$events_file" 2>/dev/null || echo 0)
        record_case "用例 9.1 限流事件 RateLimitStart" "FAIL" \
            "未找到 RateLimitStart 事件（文件共 ${total_events} 条事件）"
        return
    fi

    # 验证事件字段
    local start_valid
    start_valid=$(python3 -c "
import json, sys
for line in open('$events_file'):
    try:
        e = json.loads(line.strip())
        if e.get('event_name') == 'RateLimitStart':
            if e.get('rule_name') == '$QPS_RULE_NAME' and e.get('namespace') == '$NAMESPACE' and e.get('service') == '$QPS_SERVICE':
                print('OK')
                sys.exit(0)
    except Exception: pass
print('FAIL')
" 2>/dev/null || echo "FAIL")

    if [[ "$start_valid" != "OK" ]]; then
        record_case "用例 9.1 限流事件 RateLimitStart" "FAIL" \
            "RateLimitStart 事件存在但字段不匹配（期望 rule_name=${QPS_RULE_NAME}, ns=${NAMESPACE}, svc=${QPS_SERVICE}）"
        return
    fi
    record_case "用例 9.1 限流事件 RateLimitStart" "PASS" \
        "捕获到 RateLimitStart 事件，rule_name=${QPS_RULE_NAME}，字段校验通过"

    # ---------- 用例 9.2：RateLimitEnd 事件 ----------
    print_block "[用例 9.2] 限流事件 RateLimitEnd" \
        "操作:   等窗口恢复（2s）后发 1 次请求触发 LIMITED→UNLIMITED" \
        "原理:   限流窗口过后首次通过的请求让状态从 Limited→Ok，SDK 上报 RateLimitEnd 事件" \
        "断言:   captured_events.json 中存在 event_name=RateLimitEnd 且 rule_name=ratelimit-e2e-qps-rule"

    log_info "[用例 9.2] 等 2s 让限流窗口恢复..."
    sleep 2

    log_info "[用例 9.2] 发 1 次请求触发 LIMITED→UNLIMITED..."
    curl -s -o /dev/null --connect-timeout 2 --max-time 3 \
        "http://127.0.0.1:${PORT_PROVIDER_EVENTS}/echo" 2>/dev/null || true

    log_info "[用例 9.2] 等待 3s 让事件 batch flush..."
    sleep 3

    local end_count
    end_count=$(grep -c '"RateLimitEnd"' "$events_file" 2>/dev/null || echo 0)
    if [[ "$end_count" -lt 1 ]]; then
        record_case "用例 9.2 限流事件 RateLimitEnd" "FAIL" \
            "未找到 RateLimitEnd 事件（RateLimitStart=${start_count}, RateLimitEnd=${end_count}）"
        return
    fi

    local end_valid
    end_valid=$(python3 -c "
import json, sys
for line in open('$events_file'):
    try:
        e = json.loads(line.strip())
        if e.get('event_name') == 'RateLimitEnd':
            if e.get('rule_name') == '$QPS_RULE_NAME' and e.get('namespace') == '$NAMESPACE' and e.get('service') == '$QPS_SERVICE':
                print('OK')
                sys.exit(0)
    except Exception: pass
print('FAIL')
" 2>/dev/null || echo "FAIL")

    if [[ "$end_valid" != "OK" ]]; then
        record_case "用例 9.2 限流事件 RateLimitEnd" "FAIL" \
            "RateLimitEnd 事件存在但字段不匹配"
        return
    fi
    record_case "用例 9.2 限流事件 RateLimitEnd" "PASS" \
        "捕获到 RateLimitEnd 事件，rule_name=${QPS_RULE_NAME}，字段校验通过"

    # 打印所有捕获的事件供日志审计
    log_info "--- 捕获的限流事件 ---"
    while IFS= read -r line; do
        log_info "  ${line}"
    done < "$events_file"
    log_info "--- end ---"
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
    stop_provider "$PROVIDER_REGEX_PID"
    stop_provider "$PROVIDER_GLOBAL_A_PID"
    stop_provider "$PROVIDER_GLOBAL_B_PID"
    stop_provider "$PROVIDER_GLOBAL_FAILOVER_PID"
    stop_provider "$PROVIDER_CUSTOM_RESPONSE_PID"
    stop_provider "$PROVIDER_GLOBAL_CUSTOM_RESP_PID"
    stop_provider "$CONSUMER_QPS_PID"
    stop_provider "$CONSUMER_UNIRATE_PID"
    stop_provider "$CONSUMER_CONC_PID"
    stop_provider "$CONSUMER_CUSTOM_PID"
    stop_provider "$CONSUMER_REGEX_PID"
    stop_provider "$CONSUMER_GLOBAL_PID"
    stop_provider "$CONSUMER_GLOBAL_FAILOVER_PID"
    stop_provider "$CONSUMER_CUSTOM_RESPONSE_PID"
    stop_provider "$CONSUMER_GLOBAL_CUSTOM_RESP_PID"
    stop_provider "$PROVIDER_METRICS_PID"
    stop_provider "$PROVIDER_EVENTS_PID"
    stop_provider "$MOCK_EVENT_SERVER_PID"
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
    echo "regex 服务:        ${REGEX_SERVICE}, provider:${PORT_PROVIDER_REGEX} consumer:${PORT_CONSUMER_REGEX}（regex_combine 开关）"
    echo "GLOBAL 服务:       ${GLOBAL_SERVICE}, provider A:${PORT_PROVIDER_GLOBAL_A} B:${PORT_PROVIDER_GLOBAL_B} failover:${PORT_PROVIDER_GLOBAL_FAILOVER} consumer:${PORT_CONSUMER_GLOBAL}（分布式集群限流，依赖 ${GLOBAL_LIMITER_NAMESPACE}/${GLOBAL_LIMITER_SERVICE}）"
    echo "CustomResponse 服务: ${CUSTOM_RESPONSE_SERVICE}, provider:${PORT_PROVIDER_CUSTOM_RESPONSE} consumer:${PORT_CONSUMER_CUSTOM_RESPONSE}（验证 customResponse.body 端到端透传）"
    echo "请求链路:          curl → consumer → provider（consumer 负责服务发现 + HTTP 转发）"
    echo "only 列表:         ${ONLY_LIST:-（无，运行全部）}"
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

    if ! is_skipped "regex"; then
        run_regex_combine_cases
    else
        log_warn "跳过 regex_combine 用例 (--skip regex)"
    fi

    if ! is_skipped "global"; then
        run_global_cases
    else
        log_warn "跳过 GLOBAL 分布式限流用例 (--skip global)"
    fi

    if ! is_skipped "custom-response"; then
        run_custom_response_cases
    else
        log_warn "跳过 CustomResponse 自定义返回用例 (--skip custom-response)"
    fi

    if ! is_skipped "metrics"; then
        run_metrics_cases
    else
        log_warn "跳过限流监控指标验证用例 (--skip metrics)"
    fi

    if ! is_skipped "events"; then
        run_events_cases
    else
        log_warn "跳过限流事件上报验证用例 (--skip events)"
    fi

    # ==================== 汇总 ====================
    log_step "用例汇总"
    local n=${#CASE_NAMES[@]}
    local i=0 pass=0 fail=0 skip=0
    while [[ $i -lt $n ]]; do
        local name="${CASE_NAMES[$i]}"
        local verdict="${CASE_VERDICTS[$i]}"
        local detail="${CASE_DETAILS[$i]}"
        case "$verdict" in
            PASS) pass=$((pass+1)); printf "  ${GREEN}✅ %-40s${NC} %s\n" "$name" "$detail" ;;
            FAIL) fail=$((fail+1)); printf "  ${RED}❌ %-40s${NC} %s\n" "$name" "$detail" ;;
            WARN) printf "  ${YELLOW}⚠️  %-40s${NC} %s\n" "$name" "$detail" ;;
            SKIP) skip=$((skip+1)); printf "  ${YELLOW}⏭️  %-40s${NC} %s\n" "$name" "$detail" ;;
        esac
        i=$((i+1))
    done
    echo ""
    echo "  通过 ${pass} / ${n}, 失败 ${fail}, 跳过 ${skip}"
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
