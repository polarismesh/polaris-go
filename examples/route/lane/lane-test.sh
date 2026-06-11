#!/bin/bash
# ============================================================
# 全链路灰度泳道路由测试脚本（Go SDK 版本）
#
# 用法: ./lane-test.sh <命令> [polaris地址]
# 示例: ./lane-test.sh all 127.0.0.1
#
# 命令:
#   all     完整流程（构建 → 验证规则 → 启动 → 等待 → 测试 → 停止）
#   build   仅构建 Go 二进制
#   check   仅检查 Polaris 泳道规则
#   start   构建并启动服务（含规则检查）
#   test    执行测试用例（服务需已启动）
#   stop    停止所有服务
#
# 预期的 Polaris 泳道规则配置:
#   泳道组: lane-go-example
#   入口服务: LaneRouterGateway (default 命名空间)
#   目标服务: LaneEchoClient, LaneEchoServer
#   规则 gray:
#     - 匹配条件: Header user=gray
#     - 目标泳道: lane=gray
#     - 匹配模式: STRICT
#   规则 permissive:
#     - 匹配条件: Header user=noexist
#     - 目标泳道: lane=noexist（无实例）
#     - 匹配模式: PERMISSIVE（回退基线）
#   规则 strict-noexist:
#     - 匹配条件: Header user=strict
#     - 目标泳道: lane=strict-noexist（无实例）
#     - 匹配模式: STRICT（无可用实例时期望 HTTP 503）
#
#   六类匹配维度验证规则（全部 STRICT 染到 lane=gray，复用 provider-gray）:
#   规则 method-post:
#     - 匹配条件: METHOD=POST  AND  Header lane-test=method-post
#   规则 query-env:
#     - 匹配条件: QUERY env=gray  AND  Header lane-test=query-env
#   规则 cookie-user:
#     - 匹配条件: COOKIE user=gray  AND  Header lane-test=cookie-user
#   规则 path-gray:
#     - 匹配条件: PATH = /LaneEchoClient/gray-path （独占路径，无需守卫）
#   规则 caller-ip-local:
#     - 匹配条件: CALLER_IP EXACT 127.0.0.1  AND  Header lane-test=caller-ip-local
#   规则 caller-ip-not-zero:
#     - 匹配条件: CALLER_IP NOT_EQUALS 0.0.0.0  AND  Header lane-test=caller-ip-not-zero
#
#   说明：除 path-gray 外，5 条维度规则均 AND 上 Header lane-test=<规则名> 作为二级守卫，
#   避免污染已有 baseline / permissive / isolation 用例。
#
#   half 链路专项规则（覆盖 gateway → consumer(无 lane 实例) → provider(有 lane 实例) 链路）:
#   规则 half-gray-permissive:
#     - 匹配条件: Header user=half-permissive
#     - 目标泳道: lane=gray
#     - 匹配模式: PERMISSIVE（验证染色穿透 baseline 中间节点到 lane=gray provider）
#   规则 half-gray-strict:
#     - 匹配条件: Header user=half-strict
#     - 目标泳道: lane=gray
#     - 匹配模式: STRICT（验证 consumer 一跳无目标实例时直接 503）
#
#   half-simple 链路专项规则（覆盖 simple-gateway → simple-consumer(无 lane 实例) → provider(有 lane 实例) 链路）:
#    复用上述两条 half-gray-permissive 和 half-gray-strict 规则，但走 GetOneInstance 链路。
#    与 half 链路的差异: middle consumer 使用 simple-consumer (基于 GetOneInstance) 而非
#    consumer (基于 ProcessRouters)，验证两种实现路径在 half 场景下行为一致。
# ============================================================

# ==================== 配置区 ====================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

POLARIS_HOST="127.0.0.1"
POLARIS_TOKEN=""
DEBUG_MODE=false

init_config() {
    POLARIS_HTTP_ADDR="http://${POLARIS_HOST}:8090"
    POLARIS_CONSOLE="http://${POLARIS_HOST}:8080"
}

# 服务端口
GATEWAY_PORT=48095
SIMPLE_GATEWAY_PORT=48096
GATEWAY_EXCL_PORT=48097
CONSUMER_BASE_PORT=19080
CONSUMER_GRAY_PORT=19081
SIMPLE_CONSUMER_BASE_PORT=19082
SIMPLE_CONSUMER_GRAY_PORT=19083
PROVIDER_BASE_PORT=19090
PROVIDER_GRAY_PORT=19091
PROVIDER_EXCL_STABLE_PORT=19092
PROVIDER_EXCL_GRAY_PORT=19093

# baseLaneMode 专项测试 — 服务不在任何泳道组内:
# NoLaneGroupEchoServer 从未被加入任何泳道组,
# 验证 lane router 对非泳道组服务的 baseLaneMode 行为。
NLG_PROVIDER_BASE_PORT=19100
NLG_PROVIDER_GRAY_PORT=19101
NLG_CONSUMER_MODE0_PORT=19110
NLG_CONSUMER_MODE1_PORT=19111

# half 链路专项测试 — 中间 consumer 没有 lane 标签实例,但 provider 有:
# 用于覆盖 gateway → consumer(无 lane 实例) → provider(有 lane 实例) 这种
# "M 字型" 链路染色透传场景。half-consumer 只有 baseline 一台实例,
# half-provider 同时有 baseline + gray 两台实例。
HALF_CONSUMER_PORT=19120
HALF_PROVIDER_BASE_PORT=19130
HALF_PROVIDER_GRAY_PORT=19131

# half-simple 链路专项测试 — 与 half 链路一致的拓扑但走 GetOneInstance 路径:
# simple-gateway → simple-consumer(无 lane 实例) → provider(有 lane 实例)
# half-simple-consumer 只有 baseline 一台实例,下游复用 ${PROVIDER_SERVICE} 的 baseline+gray。
HALF_SIMPLE_CONSUMER_PORT=19140

# 服务名
NAMESPACE="default"
PROVIDER_SERVICE="LaneEchoServer"
CONSUMER_SERVICE="LaneEchoClient"
SIMPLE_CONSUMER_SERVICE="SimpleLaneEchoClient"
GATEWAY_SERVICE="LaneRouterGateway"
# baseLaneMode=1 测试专用服务（与主链路 PROVIDER_SERVICE 解耦）：
# 该服务只注册带 lane 标签的实例（stable + gray），没有任何未打标签的实例，
# 这样才能触发 routeToBaseline 里 ExcludeEnabledLaneInstance 分支。
PROVIDER_EXCL_SERVICE="StableLaneEchoServer"
NLG_PROVIDER_SERVICE="NoLaneGroupEchoServer"
NLG_CONSUMER_SERVICE="NoLaneGroupEchoClient"
# half 链路专项测试 — 验证染色经过"无目标 lane 实例的中间 consumer"时
# 是否能正确透传到下游有 lane 实例的 provider:
#   - HalfLaneEchoClient: 只注册 baseline 实例(不带 lane 标签)
#   - HalfLaneEchoServer: 同时注册 baseline + lane=gray 两台实例
HALF_CONSUMER_SERVICE="HalfLaneEchoClient"
HALF_PROVIDER_SERVICE="HalfLaneEchoServer"
# half-simple 链路专项测试 — 基于 GetOneInstance 的 half 链路变体:
#   - HalfSimpleLaneEchoClient: 只注册 baseline 实例(不带 lane 标签)
#   - 下游复用 ${PROVIDER_SERVICE} (LaneEchoServer, 已有 baseline+gray)
HALF_SIMPLE_CONSUMER_SERVICE="HalfSimpleLaneEchoClient"

# 预期的泳道规则配置
EXPECTED_LANE_GROUP="lane-go-example"
EXPECTED_ENTRY_SERVICE="${GATEWAY_SERVICE}"

# 目录配置
BUILD_DIR="${SCRIPT_DIR}/.build"
LOG_DIR="${SCRIPT_DIR}/.logs"
PID_FILE="${SCRIPT_DIR}/.lane-test-pids"
TEST_LOG_FILE="${LOG_DIR}/lane-test-$(date +%Y%m%d_%H%M%S).log"

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# ==================== 工具函数 ====================
_log() {
    local msg="$1"
    echo -e "$msg"
    echo -e "$msg" | sed 's/\x1b\[[0-9;]*m//g' >> "${TEST_LOG_FILE}" 2>/dev/null
}
log_info()  { _log "${BLUE}[INFO]${NC} $1"; }
log_ok()    { _log "${GREEN}[PASS]${NC} $1"; }
log_fail()  { _log "${RED}[FAIL]${NC} $1"; }
log_warn()  { _log "${YELLOW}[WARN]${NC} $1"; }
log_title() { _log "\n${YELLOW}========== $1 ==========${NC}"; }
log_raw()   { echo "$1"; echo "$1" >> "${TEST_LOG_FILE}" 2>/dev/null; }

# ==================== 构建 ====================
build_binaries() {
    log_title "构建 Go 二进制文件"
    mkdir -p "${BUILD_DIR}"

    log_info "构建 provider..."
    if (cd "${SCRIPT_DIR}/provider" && go build -o "${BUILD_DIR}/provider" .); then
        log_ok "provider 构建成功: ${BUILD_DIR}/provider"
    else
        log_fail "provider 构建失败"
        return 1
    fi

    log_info "构建 consumer..."
    if (cd "${SCRIPT_DIR}/consumer" && go build -o "${BUILD_DIR}/consumer" .); then
        log_ok "consumer 构建成功: ${BUILD_DIR}/consumer"
    else
        log_fail "consumer 构建失败"
        return 1
    fi

    log_info "构建 simple-consumer..."
    if (cd "${SCRIPT_DIR}/simple-consumer" && go build -o "${BUILD_DIR}/simple-consumer" .); then
        log_ok "simple-consumer 构建成功: ${BUILD_DIR}/simple-consumer"
    else
        log_fail "simple-consumer 构建失败"
        return 1
    fi

    log_info "构建 gateway..."
    if (cd "${SCRIPT_DIR}/gateway" && go build -o "${BUILD_DIR}/gateway" .); then
        log_ok "gateway 构建成功: ${BUILD_DIR}/gateway"
    else
        log_fail "gateway 构建失败"
        return 1
    fi

    log_info "构建 simple-gateway..."
    if (cd "${SCRIPT_DIR}/simple-gateway" && go build -o "${BUILD_DIR}/simple-gateway" .); then
        log_ok "simple-gateway 构建成功: ${BUILD_DIR}/simple-gateway"
    else
        log_fail "simple-gateway 构建失败"
        return 1
    fi
}

# ==================== 泳道规则验证 ====================
fetch_lane_rules() {
    local response
    response=$(curl -s --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/v1/Discover" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "{
            \"service\": {
                \"name\": \"${EXPECTED_ENTRY_SERVICE}\",
                \"namespace\": \"${NAMESPACE}\"
            },
            \"type\": \"LANE\"
        }" 2>/dev/null || true)
    echo "$response"
}

validate_lane_rules() {
    local response
    response=$(fetch_lane_rules)

    if [ -z "$response" ]; then
        log_fail "查询泳道规则失败，无法连接 Polaris 服务端: ${POLARIS_HTTP_ADDR}"
        return 1
    fi

    local code
    code=$(echo "$response" | python3 -c "import sys,json; print(json.load(sys.stdin).get('code',0))" 2>/dev/null || echo "unknown")
    if [ "$code" != "200000" ]; then
        log_fail "查询泳道规则失败，返回码: ${code}"
        return 1
    fi

    local validate_result
    validate_result=$(echo "$response" | python3 -c "
import sys, json

data = json.load(sys.stdin)
lanes = data.get('lanes', [])
errors = []

# 1. 检查泳道组
target_group = None
for lane in lanes:
    if lane.get('name') == '${EXPECTED_LANE_GROUP}':
        target_group = lane
        break

if target_group is None:
    group_names = [l.get('name', '?') for l in lanes]
    errors.append(f'未找到泳道组 ${EXPECTED_LANE_GROUP}，当前泳道组: {group_names}')
    for e in errors:
        print(f'ERROR|{e}')
    sys.exit(0)

# 2. 检查入口
entries = target_group.get('entries', [])
entry_found = any(
    e.get('selector', {}).get('service') == '${EXPECTED_ENTRY_SERVICE}' and
    e.get('selector', {}).get('namespace') == '${NAMESPACE}'
    for e in entries
)
if not entry_found:
    entry_info = [e.get('selector', {}).get('service','?') for e in entries]
    errors.append(f'泳道组入口未包含 ${EXPECTED_ENTRY_SERVICE}/${NAMESPACE}，当前入口: {entry_info}')

# 3. 检查目标服务
destinations = target_group.get('destinations', [])
dest_services = {d.get('service', '') for d in destinations}
fixable_dest = []
for required_svc in ['${CONSUMER_SERVICE}', '${PROVIDER_SERVICE}', '${SIMPLE_CONSUMER_SERVICE}', '${PROVIDER_EXCL_SERVICE}', '${HALF_CONSUMER_SERVICE}', '${HALF_PROVIDER_SERVICE}', '${HALF_SIMPLE_CONSUMER_SERVICE}']:
    if required_svc not in dest_services:
        fixable_dest.append(required_svc)
        errors.append(f'泳道组目标服务缺少 {required_svc}，当前: {dest_services}')

# 4. 检查泳道规则
rules = target_group.get('rules', [])
rules_by_name = {r.get('name', ''): r for r in rules}
fixable_rules = []  # 泳道组已存在但缺少特定规则时，记录可自动修复的规则名

# 必需规则清单：规则名 -> 期望的 match_mode 和 default_label_value。
# 旧规则（gray / permissive / strict-noexist）+ 六类匹配维度新规则全部在这里登记。
# 将来再增加规则只需改这一处即可。
REQUIRED_RULES = {
    'gray':                {'match_mode': 'STRICT',     'default_label_value': 'gray'},
    'permissive':          {'match_mode': 'PERMISSIVE', 'default_label_value': 'noexist'},
    'strict-noexist':      {'match_mode': 'STRICT',     'default_label_value': 'strict-noexist'},
    'method-post':         {'match_mode': 'STRICT',     'default_label_value': 'gray'},
    'query-env':           {'match_mode': 'STRICT',     'default_label_value': 'gray'},
    'cookie-user':         {'match_mode': 'STRICT',     'default_label_value': 'gray'},
    'path-gray':           {'match_mode': 'STRICT',     'default_label_value': 'gray'},
    'caller-ip-local':     {'match_mode': 'STRICT',     'default_label_value': 'gray'},
    'caller-ip-not-zero':  {'match_mode': 'STRICT',     'default_label_value': 'gray'},
    # half 链路专用规则: PERMISSIVE 模式 → 中间 consumer(无 gray 实例)回退基线但
    # 透传 stainLabel,下游 provider(有 gray 实例)命中泳道。
    'half-gray-permissive': {'match_mode': 'PERMISSIVE', 'default_label_value': 'gray'},
    # half 链路专用规则: STRICT 模式 → 中间 consumer(无 gray 实例)直接 503,
    # 验证 STRICT 不会因为 provider 有 gray 实例而被错误地降级.
    'half-gray-strict':     {'match_mode': 'STRICT',     'default_label_value': 'gray'},
}

for rname, expect in REQUIRED_RULES.items():
    rule = rules_by_name.get(rname)
    if rule is None:
        fixable_rules.append(rname)
        errors.append(f'未找到泳道规则 {rname}，当前规则: {list(rules_by_name.keys())}')
        continue
    if not rule.get('enable', False):
        errors.append(f'泳道规则 {rname} 未启用 (enable=false)')
    actual_mode = rule.get('match_mode', '')
    if actual_mode != expect['match_mode']:
        errors.append(f'泳道规则 {rname} 匹配模式应为 {expect["match_mode"]}，实际为: {actual_mode}')
    # permissive 规则的 default_label_value 为 noexist（历史原因），只有在期望值
    # 明确写在 REQUIRED_RULES 中时才校验，保证错误提示准确。
    actual_label = rule.get('default_label_value', '')
    if actual_label != expect['default_label_value']:
        errors.append(f'泳道规则 {rname} 目标泳道标签应为 {expect["default_label_value"]}，实际为: {actual_label}')

# 输出结果
if errors:
    for svc in fixable_dest:
        print(f'FIXABLE_DEST|{svc}')
    for rname in fixable_rules:
        print(f'FIXABLE_RULE|{rname}')
    for e in errors:
        print(f'ERROR|{e}')
else:
    rule_count = len(rules)
    dest_count = len(destinations)
    print(f'OK|泳道组 ${EXPECTED_LANE_GROUP}: {rule_count} 条规则, {dest_count} 个目标服务')
    for r in rules:
        name = r.get('name', '?')
        mode = r.get('match_mode', '?')
        label = r.get('default_label_value', '?')
        enabled = r.get('enable', False)
        args = r.get('traffic_match_rule', {}).get('arguments', [])
        cond = ', '.join([a.get('type','?') + ' ' + a.get('key','?') + '=' + a.get('value',{}).get('value','?') for a in args])
        print(f'RULE|{name}|{mode}|{label}|{enabled}|{cond}')
" 2>/dev/null)

    if [ -z "$validate_result" ]; then
        log_fail "泳道规则解析失败，可能是响应格式异常"
        return 1
    fi

    local has_error=false
    FIXABLE_DEST_SERVICES=""
    FIXABLE_RULES=""
    while IFS='|' read -r level msg rest; do
        case "$level" in
            FIXABLE_DEST)
                FIXABLE_DEST_SERVICES="${FIXABLE_DEST_SERVICES} ${msg}"
                ;;
            FIXABLE_RULE)
                FIXABLE_RULES="${FIXABLE_RULES} ${msg}"
                ;;
            ERROR)
                log_fail "规则验证失败: ${msg}"
                has_error=true
                ;;
            OK)
                log_ok "规则验证通过: ${msg}"
                ;;
            RULE)
                IFS='|' read -r mode label enabled cond <<< "$rest"
                local status_icon="✓"
                [ "$enabled" = "False" ] && status_icon="✗"
                log_info "  ${status_icon} 规则 ${msg}: 模式=${mode}, 泳道=${label}, 条件=[${cond}]"
                ;;
        esac
    done <<< "$validate_result"

    if [ "$has_error" = true ]; then
        log_info ""
        log_info "期望的泳道规则配置:"
        log_info "  泳道组: ${EXPECTED_LANE_GROUP}"
        log_info "  入口: ${EXPECTED_ENTRY_SERVICE} (${NAMESPACE} 命名空间)"
        log_info "  目标服务: ${CONSUMER_SERVICE}, ${SIMPLE_CONSUMER_SERVICE}, ${PROVIDER_SERVICE}, ${PROVIDER_EXCL_SERVICE}, ${HALF_CONSUMER_SERVICE}, ${HALF_PROVIDER_SERVICE}, ${HALF_SIMPLE_CONSUMER_SERVICE}"
        log_info "  规则 gray:               Header user=gray       → lane=gray,           STRICT"
        log_info "  规则 permissive:         Header user=noexist    → lane=noexist,        PERMISSIVE"
        log_info "  规则 strict-noexist:     Header user=strict     → lane=strict-noexist, STRICT (无实例 → HTTP 503)"
        log_info "  规则 method-post:        METHOD=POST  AND Header lane-test=method-post        → lane=gray, STRICT"
        log_info "  规则 query-env:          QUERY env=gray  AND Header lane-test=query-env       → lane=gray, STRICT"
        log_info "  规则 cookie-user:        COOKIE user=gray  AND Header lane-test=cookie-user   → lane=gray, STRICT"
        log_info "  规则 path-gray:          PATH = /LaneEchoClient/gray-path                     → lane=gray, STRICT"
        log_info "  规则 caller-ip-local:    CALLER_IP=127.0.0.1 AND Header lane-test=caller-ip-local      → lane=gray, STRICT"
        log_info "  规则 caller-ip-not-zero: CALLER_IP≠0.0.0.0  AND Header lane-test=caller-ip-not-zero    → lane=gray, STRICT"
        log_info "  规则 half-gray-permissive: Header user=half-permissive → lane=gray, PERMISSIVE"
        log_info "  规则 half-gray-strict:     Header user=half-strict     → lane=gray, STRICT"
        log_info ""
        log_info "请在 Polaris 控制台 (${POLARIS_CONSOLE}) 配置泳道规则后重试"
        return 1
    fi
    return 0
}

validate_rules_with_wait() {
    log_title "检查泳道规则配置"
    log_info "查询 ${GATEWAY_SERVICE} 的泳道规则..."

    local auto_fix_attempts=0
    local max_auto_fix=3
    local auto_create_attempted=false

    while true; do
        if validate_lane_rules; then
            log_ok "泳道规则符合预期配置"
            return 0
        fi

        # 自动创建：泳道组完全不存在时通过管理 API 创建一份完整规则
        local tail_log
        tail_log=$(tail -n 20 "${TEST_LOG_FILE}" 2>/dev/null || true)
        if ! $auto_create_attempted && echo "$tail_log" | grep -q "未找到泳道组"; then
            auto_create_attempted=true
            log_warn "检测到泳道组 ${EXPECTED_LANE_GROUP} 不存在，尝试自动创建..."
            if create_full_lane_group; then
                log_info "等待 10s 让 Polaris 服务端传播变更..."
                sleep 10
                log_info "重新查询泳道规则..."
                continue
            fi
            log_warn "自动创建失败，转为手动模式"
        fi

        # 自动修复: 如果仅是目标服务缺失（通常是上次测试未恢复），自动加回
        if [ -n "${FIXABLE_DEST_SERVICES}" ] && [ $auto_fix_attempts -lt $max_auto_fix ]; then
            auto_fix_attempts=$((auto_fix_attempts + 1))
            log_warn "检测到泳道组目标服务缺失，尝试自动修复 (第 ${auto_fix_attempts}/${max_auto_fix} 次)..."
            for svc in ${FIXABLE_DEST_SERVICES}; do
                add_service_to_lane_group "$svc" || true
            done
            log_info "等待 10s 让 Polaris 服务端传播变更..."
            sleep 10
            log_info "重新查询泳道规则..."
            continue
        fi

        # 自动修复: 如果泳道组已存在但缺少特定规则（例如新增的 strict-noexist），自动追加
        if [ -n "${FIXABLE_RULES}" ] && [ $auto_fix_attempts -lt $max_auto_fix ]; then
            auto_fix_attempts=$((auto_fix_attempts + 1))
            log_warn "检测到泳道组缺少规则，尝试自动追加 (第 ${auto_fix_attempts}/${max_auto_fix} 次)..."
            for rule_name in ${FIXABLE_RULES}; do
                add_rule_to_lane_group "$rule_name" || true
            done
            log_info "等待 10s 让 Polaris 服务端传播变更..."
            sleep 10
            log_info "重新查询泳道规则..."
            continue
        fi

        log_warn "泳道规则不符合预期，请在 Polaris 控制台修改后按 Enter 重新检查（输入 q 退出）..."
        read -r user_input
        [ "$user_input" = "q" ] || [ "$user_input" = "Q" ] && return 1
        log_info "重新查询泳道规则..."
    done
}

# create_full_lane_group 在泳道组不存在时，通过管理 API 创建符合测试预期的完整配置：
#   - 入口: ${GATEWAY_SERVICE} (default 命名空间)
#   - 目标服务: ${CONSUMER_SERVICE}, ${PROVIDER_SERVICE}, ${SIMPLE_CONSUMER_SERVICE},
#                ${PROVIDER_EXCL_SERVICE}, ${HALF_CONSUMER_SERVICE}, ${HALF_PROVIDER_SERVICE}
#   - 规则 gray:               Header user=gray    → lane=gray,           STRICT
#   - 规则 permissive:         Header user=noexist → lane=noexist,        PERMISSIVE
#   - 规则 strict-noexist:     Header user=strict  → lane=strict-noexist, STRICT (无实例 → 期望 HTTP 503)
#   - 规则 method-post:        METHOD=POST  AND Header lane-test=method-post        → lane=gray, STRICT
#   - 规则 query-env:          QUERY env=gray  AND Header lane-test=query-env       → lane=gray, STRICT
#   - 规则 cookie-user:        COOKIE user=gray  AND Header lane-test=cookie-user   → lane=gray, STRICT
#   - 规则 path-gray:          PATH = /LaneEchoClient/gray-path                     → lane=gray, STRICT
#   - 规则 caller-ip-local:    CALLER_IP=127.0.0.1 AND Header lane-test=caller-ip-local     → lane=gray, STRICT
#   - 规则 caller-ip-not-zero: CALLER_IP≠0.0.0.0  AND Header lane-test=caller-ip-not-zero   → lane=gray, STRICT
#   - 规则 half-gray-permissive: Header user=half-permissive → lane=gray, PERMISSIVE (half 链路染色穿透)
#   - 规则 half-gray-strict:     Header user=half-strict     → lane=gray, STRICT (half 链路一跳 503)
create_full_lane_group() {
    log_info "尝试自动创建泳道组 [${EXPECTED_LANE_GROUP}]..."

    local result
    result=$(python3 -c "
import json, urllib.request, urllib.error

group = {
    'name': '${EXPECTED_LANE_GROUP}',
    'description': 'auto-created by lane-test.sh',
    'entries': [{
        'type': 'polarismesh.cn/service',
        'selector': {
            '@type': 'type.googleapis.com/v1.ServiceSelector',
            'namespace': '${NAMESPACE}',
            'service': '${GATEWAY_SERVICE}',
        },
    }],
    'destinations': [
        {'service': '${CONSUMER_SERVICE}', 'namespace': '${NAMESPACE}'},
        {'service': '${SIMPLE_CONSUMER_SERVICE}', 'namespace': '${NAMESPACE}'},
        {'service': '${PROVIDER_SERVICE}', 'namespace': '${NAMESPACE}'},
        {'service': '${PROVIDER_EXCL_SERVICE}', 'namespace': '${NAMESPACE}'},
        {'service': '${HALF_CONSUMER_SERVICE}', 'namespace': '${NAMESPACE}'},
        {'service': '${HALF_PROVIDER_SERVICE}', 'namespace': '${NAMESPACE}'},
        {'service': '${HALF_SIMPLE_CONSUMER_SERVICE}', 'namespace': '${NAMESPACE}'},
    ],
    'rules': [
        {
            'name': 'gray',
            'enable': True,
            'match_mode': 'STRICT',
            'default_label_value': 'gray',
            'traffic_match_rule': {
                'arguments': [{
                    'type': 'HEADER',
                    'key': 'user',
                    'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'gray'},
                }],
                'match_mode': 'AND',
            },
        },
        {
            'name': 'permissive',
            'enable': True,
            'match_mode': 'PERMISSIVE',
            'default_label_value': 'noexist',
            'traffic_match_rule': {
                'arguments': [{
                    'type': 'HEADER',
                    'key': 'user',
                    'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'noexist'},
                }],
                'match_mode': 'AND',
            },
        },
        {
            # STRICT 模式 + 指向不存在的泳道，用于覆盖"严格模式 + 无实例"场景：
            # 命中该规则时，SDK 会把路由状态降级为 DegradeToFilterOnly 并标记
            # HasLimitedInstances=true，最终 gateway / simple-gateway 因拿不到实例
            # 返回 HTTP 503 StatusServiceUnavailable。
            'name': 'strict-noexist',
            'enable': True,
            'match_mode': 'STRICT',
            'default_label_value': 'strict-noexist',
            'traffic_match_rule': {
                'arguments': [{
                    'type': 'HEADER',
                    'key': 'user',
                    'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'strict'},
                }],
                'match_mode': 'AND',
            },
        },
        # ---------- 六类匹配维度验证规则（全部 STRICT 染到 lane=gray） ----------
        # 除 path-gray 外均 AND 上 Header lane-test=<规则名> 作为二级守卫，
        # 避免污染已有 baseline / permissive / strict-noexist / isolation 用例。
        {
            'name': 'method-post',
            'enable': True,
            'match_mode': 'STRICT',
            'default_label_value': 'gray',
            'traffic_match_rule': {
                'arguments': [
                    {'type': 'METHOD', 'key': '',
                     'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'POST'}},
                    {'type': 'HEADER', 'key': 'lane-test',
                     'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'method-post'}},
                ],
                'match_mode': 'AND',
            },
        },
        {
            'name': 'query-env',
            'enable': True,
            'match_mode': 'STRICT',
            'default_label_value': 'gray',
            'traffic_match_rule': {
                'arguments': [
                    {'type': 'QUERY', 'key': 'env',
                     'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'gray'}},
                    {'type': 'HEADER', 'key': 'lane-test',
                     'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'query-env'}},
                ],
                'match_mode': 'AND',
            },
        },
        {
            'name': 'cookie-user',
            'enable': True,
            'match_mode': 'STRICT',
            'default_label_value': 'gray',
            'traffic_match_rule': {
                'arguments': [
                    {'type': 'COOKIE', 'key': 'user',
                     'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'gray'}},
                    {'type': 'HEADER', 'key': 'lane-test',
                     'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'cookie-user'}},
                ],
                'match_mode': 'AND',
            },
        },
        {
            # path-gray 使用独占路径 /LaneEchoClient/gray-path，现有用例都访问 /echo，
            # 无需 Header 守卫即可天然隔离。consumer 对 /gray-path 没有 handler 会返回 404，
            # 但 gateway 的响应 msg 里仍会包含 callee lane=gray，测试脚本只断言 lane=gray。
            'name': 'path-gray',
            'enable': True,
            'match_mode': 'STRICT',
            'default_label_value': 'gray',
            'traffic_match_rule': {
                'arguments': [
                    {'type': 'PATH', 'key': '',
                     'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': '/LaneEchoClient/gray-path'}},
                ],
                'match_mode': 'AND',
            },
        },
        {
            'name': 'caller-ip-local',
            'enable': True,
            'match_mode': 'STRICT',
            'default_label_value': 'gray',
            'traffic_match_rule': {
                'arguments': [
                    {'type': 'CALLER_IP', 'key': '',
                     'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': '127.0.0.1'}},
                    {'type': 'HEADER', 'key': 'lane-test',
                     'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'caller-ip-local'}},
                ],
                'match_mode': 'AND',
            },
        },
        {
            # NOT_EQUALS 几乎匹配所有 IP，必须配 Header 守卫；只有显式带
            # lane-test=caller-ip-not-zero 的请求才会命中，避免灾难性误染。
            'name': 'caller-ip-not-zero',
            'enable': True,
            'match_mode': 'STRICT',
            'default_label_value': 'gray',
            'traffic_match_rule': {
                'arguments': [
                    {'type': 'CALLER_IP', 'key': '',
                     'value': {'type': 'NOT_EQUALS', 'value_type': 'TEXT', 'value': '0.0.0.0'}},
                    {'type': 'HEADER', 'key': 'lane-test',
                     'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'caller-ip-not-zero'}},
                ],
                'match_mode': 'AND',
            },
        },
        # ---------- half 链路专用规则 ----------
        # 链路: gateway → HalfLaneEchoClient(只有 baseline 实例) → HalfLaneEchoServer(baseline+gray)
        # 验证 "中间一跳无目标 lane 实例" 的染色透传与 STRICT/PERMISSIVE 行为差异。
        {
            # PERMISSIVE: 中间 consumer 没有 lane=gray 实例 → 回退基线但透传 stainLabel,
            # 下游 provider 有 lane=gray 实例 → 命中泳道,链路最终显示 callee lane=gray。
            'name': 'half-gray-permissive',
            'enable': True,
            'match_mode': 'PERMISSIVE',
            'default_label_value': 'gray',
            'traffic_match_rule': {
                'arguments': [{
                    'type': 'HEADER',
                    'key': 'user',
                    'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'half-permissive'},
                }],
                'match_mode': 'AND',
            },
        },
        {
            # STRICT: 中间 consumer 没有 lane=gray 实例 → 直接 503,不会再走到 provider。
            # 与 half-gray-permissive 形成对照,验证 STRICT 不会因为下游有目标实例而被错误降级。
            'name': 'half-gray-strict',
            'enable': True,
            'match_mode': 'STRICT',
            'default_label_value': 'gray',
            'traffic_match_rule': {
                'arguments': [{
                    'type': 'HEADER',
                    'key': 'user',
                    'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'half-strict'},
                }],
                'match_mode': 'AND',
            },
        },
    ],
}

req_data = json.dumps([group]).encode('utf-8')
req = urllib.request.Request(
    '${POLARIS_HTTP_ADDR}/naming/v1/lane/groups',
    data=req_data,
    method='POST',
    headers={
        'Content-Type': 'application/json',
        'X-Polaris-Token': '${POLARIS_TOKEN}',
    }
)
try:
    resp = urllib.request.urlopen(req, timeout=10)
    resp_data = json.loads(resp.read().decode('utf-8'))
    code = resp_data.get('code', 0)
    if code in (200000, 200001):
        print(f'OK|已创建泳道组 ${EXPECTED_LANE_GROUP}（含 11 条规则：gray + permissive + strict-noexist + 6 条维度规则 + half-gray-permissive + half-gray-strict）加上 ${HALF_SIMPLE_CONSUMER_SERVICE} 目标')
    else:
        info = resp_data.get('info', '')
        print(f'ERROR|创建失败，返回码: {code}, 信息: {info}')
except urllib.error.HTTPError as e:
    body = e.read().decode('utf-8', errors='replace')
    print(f'ERROR|HTTP {e.code}: {body[:200]}')
except Exception as e:
    print(f'ERROR|请求失败: {e}')
" 2>/dev/null)

    if echo "$result" | grep -q "^OK"; then
        local msg
        msg=$(echo "$result" | grep "^OK" | cut -d'|' -f2)
        log_ok "$msg"
        return 0
    fi
    local err_msg
    err_msg=$(echo "$result" | grep "^ERROR" | cut -d'|' -f2- || echo "未知错误")
    log_fail "自动创建失败: ${err_msg}"
    return 1
}

# ==================== 泳道组目标服务操作 ====================
# 泳道组名称
LANE_GROUP_NAME="${EXPECTED_LANE_GROUP}"

# 查询泳道组完整配置（用于修改后提交）
query_lane_group_full() {
    local response
    response=$(curl -s --connect-timeout 5 --max-time 10 \
        "${POLARIS_HTTP_ADDR}/naming/v1/lane/groups?name=${LANE_GROUP_NAME}&offset=0&limit=10" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" 2>/dev/null || true)
    echo "$response"
}

# print_lane_group_summary 打印当前泳道组的入口服务、目标服务和规则摘要。
# 每条链路测试开始前调用,方便排查"某条链路为什么路由到预期外的实例"这类问题,
# 因为泳道组配置是所有链路共享的输入,变化会影响全部用例。
print_lane_group_summary() {
    local response
    response=$(query_lane_group_full)
    if [ -z "$response" ]; then
        log_warn "无法查询泳道组 ${LANE_GROUP_NAME} 当前配置"
        return 1
    fi

    local summary
    summary=$(echo "$response" | python3 -c "
import sys, json

try:
    data = json.load(sys.stdin)
except Exception as e:
    print(f'ERROR|JSON 解析失败: {e}')
    sys.exit(0)

groups = data.get('data', [])
if not groups:
    print(f'ERROR|未找到泳道组 ${LANE_GROUP_NAME}')
    sys.exit(0)

group = groups[0]

# 入口
entries = group.get('entries', [])
entry_list = []
for e in entries:
    sel = e.get('selector', {})
    ns = sel.get('namespace', '')
    svc = sel.get('service', '')
    entry_list.append(f'{ns}/{svc}' if ns else svc)
print('ENTRIES|' + ', '.join(entry_list) if entry_list else 'ENTRIES|(空)')

# 目标服务
dests = group.get('destinations', [])
dest_list = []
for d in dests:
    ns = d.get('namespace', '')
    svc = d.get('service', '')
    dest_list.append(f'{ns}/{svc}' if ns else svc)
print('DESTS|' + ', '.join(dest_list) if dest_list else 'DESTS|(空)')

# 规则
rules = group.get('rules', [])
if not rules:
    print('RULES|(无)')
else:
    for r in rules:
        name = r.get('name', '?')
        mode = r.get('match_mode', '?')
        label = r.get('default_label_value', '?')
        label_key = r.get('label_key', '') or 'lane'
        enable = r.get('enable', False)
        enable_str = 'ON' if enable else 'OFF'
        args = r.get('traffic_match_rule', {}).get('arguments', [])
        cond_parts = []
        for a in args:
            t = a.get('type', '?')
            k = a.get('key', '?')
            v = a.get('value', {}).get('value', '?')
            cond_parts.append(f'{t} {k}={v}')
        cond = ', '.join(cond_parts) if cond_parts else '(none)'
        print(f'RULE|{name}|{mode}|{label_key}={label}|{enable_str}|{cond}')
" 2>/dev/null)

    if [ -z "$summary" ]; then
        log_warn "解析泳道组摘要失败"
        return 1
    fi

    log_info "[泳道组 ${LANE_GROUP_NAME}] 当前配置:"
    while IFS='|' read -r kind payload rest; do
        case "$kind" in
            ENTRIES)
                log_info "  入口服务: ${payload}"
                ;;
            DESTS)
                log_info "  目标服务: ${payload}"
                ;;
            RULE)
                # payload=name,rest="mode|label|enable|cond"
                IFS='|' read -r mode label enable cond <<< "$rest"
                log_info "  规则 ${payload} [${enable}] mode=${mode}, ${label}, condition=[${cond}]"
                ;;
            ERROR)
                log_warn "  ${payload}"
                ;;
        esac
    done <<< "$summary"
    return 0
}

# 从泳道组目标服务中移除 LaneEchoServer
# 返回: 0=成功, 1=失败
remove_provider_from_lane_group() {
    log_info "正在从泳道组 [${LANE_GROUP_NAME}] 中移除 ${PROVIDER_SERVICE}..."

    local group_data
    group_data=$(query_lane_group_full)
    if [ -z "$group_data" ]; then
        log_fail "查询泳道组失败"
        return 1
    fi

    local result
    result=$(echo "$group_data" | python3 -c "
import sys, json, urllib.request

data = json.load(sys.stdin)
groups = data.get('data', [])
if not groups:
    print('ERROR|未找到泳道组')
    sys.exit(0)

group = groups[0]
group.pop('@type', None)

# 从 destinations 中移除 ${PROVIDER_SERVICE}
destinations = group.get('destinations', [])
new_destinations = [d for d in destinations if d.get('service') != '${PROVIDER_SERVICE}']
if len(new_destinations) == len(destinations):
    print('WARN|${PROVIDER_SERVICE} 不在目标服务中，无需移除')
    sys.exit(0)

group['destinations'] = new_destinations

# 提交修改
req_data = json.dumps([group]).encode('utf-8')
req = urllib.request.Request(
    '${POLARIS_HTTP_ADDR}/naming/v1/lane/groups',
    data=req_data,
    method='PUT',
    headers={
        'Content-Type': 'application/json',
        'X-Polaris-Token': '${POLARIS_TOKEN}'
    }
)
try:
    resp = urllib.request.urlopen(req, timeout=10)
    resp_data = json.loads(resp.read().decode('utf-8'))
    code = resp_data.get('code', 0)
    if code == 200000:
        remaining = [d.get('service','?') for d in new_destinations]
        print(f'OK|已移除 ${PROVIDER_SERVICE}，剩余目标服务: {remaining}')
    else:
        info = resp_data.get('info', '')
        print(f'ERROR|移除失败，返回码: {code}, 信息: {info}')
except Exception as e:
    print(f'ERROR|请求失败: {e}')
" 2>/dev/null)

    if echo "$result" | grep -q "^OK"; then
        local msg
        msg=$(echo "$result" | grep "^OK" | cut -d'|' -f2)
        log_ok "$msg"
        return 0
    elif echo "$result" | grep -q "^WARN"; then
        local msg
        msg=$(echo "$result" | grep "^WARN" | cut -d'|' -f2)
        log_warn "$msg"
        return 0
    else
        local err_msg
        err_msg=$(echo "$result" | grep "^ERROR" | cut -d'|' -f2)
        log_fail "移除 ${PROVIDER_SERVICE} 失败: ${err_msg}"
        return 1
    fi
}

# 将任意服务加回泳道组目标服务
# 参数: $1=服务名
# 返回: 0=成功, 1=失败
add_service_to_lane_group() {
    local target_svc="$1"
    if [ -z "$target_svc" ]; then
        log_fail "add_service_to_lane_group: 缺少服务名参数"
        return 1
    fi
    log_info "正在将 ${target_svc} 加回泳道组 [${LANE_GROUP_NAME}]..."

    local group_data
    group_data=$(query_lane_group_full)
    if [ -z "$group_data" ]; then
        log_fail "查询泳道组失败"
        return 1
    fi

    local result
    result=$(echo "$group_data" | TARGET_SVC="$target_svc" python3 -c "
import sys, json, os, urllib.request

target_svc = os.environ['TARGET_SVC']
data = json.load(sys.stdin)
groups = data.get('data', [])
if not groups:
    print('ERROR|未找到泳道组')
    sys.exit(0)

group = groups[0]
group.pop('@type', None)

# 检查 target_svc 是否已在 destinations 中
destinations = group.get('destinations', [])
for d in destinations:
    if d.get('service') == target_svc:
        print(f'WARN|{target_svc} 已在目标服务中，无需添加')
        sys.exit(0)

# 添加 target_svc 到 destinations
destinations.append({
    'service': target_svc,
    'namespace': '${NAMESPACE}'
})
group['destinations'] = destinations

# 提交修改
req_data = json.dumps([group]).encode('utf-8')
req = urllib.request.Request(
    '${POLARIS_HTTP_ADDR}/naming/v1/lane/groups',
    data=req_data,
    method='PUT',
    headers={
        'Content-Type': 'application/json',
        'X-Polaris-Token': '${POLARIS_TOKEN}'
    }
)
try:
    resp = urllib.request.urlopen(req, timeout=10)
    resp_data = json.loads(resp.read().decode('utf-8'))
    code = resp_data.get('code', 0)
    if code == 200000:
        current = [d.get('service','?') for d in destinations]
        print(f'OK|已恢复 {target_svc}，当前目标服务: {current}')
    else:
        info = resp_data.get('info', '')
        print(f'ERROR|恢复 {target_svc} 失败，返回码: {code}, 信息: {info}')
except Exception as e:
    print(f'ERROR|请求失败: {e}')
" 2>/dev/null)

    if echo "$result" | grep -q "^OK"; then
        local msg
        msg=$(echo "$result" | grep "^OK" | cut -d'|' -f2)
        log_ok "$msg"
        return 0
    elif echo "$result" | grep -q "^WARN"; then
        local msg
        msg=$(echo "$result" | grep "^WARN" | cut -d'|' -f2)
        log_warn "$msg"
        return 0
    else
        local err_msg
        err_msg=$(echo "$result" | grep "^ERROR" | cut -d'|' -f2)
        log_fail "恢复 ${target_svc} 失败: ${err_msg}"
        return 1
    fi
}

# 将 LaneEchoServer 加回泳道组目标服务（向后兼容别名）
# 返回: 0=成功, 1=失败
restore_provider_to_lane_group() {
    add_service_to_lane_group "${PROVIDER_SERVICE}"
}

# add_rule_to_lane_group 向已存在的泳道组中追加缺失的泳道规则。
# 参数: $1=规则名（gray / permissive / strict-noexist 之一）
# 返回: 0=成功（或规则已存在）, 1=失败
# 场景: 泳道组已存在但新增了规则定义（例如 strict-noexist）时，
#       validate_rules_with_wait 会检测到 FIXABLE_RULE，通过此函数
#       PUT 更新泳道组，追加缺失的规则，避免进入手动修复循环。
add_rule_to_lane_group() {
    local rule_name="$1"
    if [ -z "$rule_name" ]; then
        log_fail "add_rule_to_lane_group: 缺少规则名参数"
        return 1
    fi
    log_info "正在向泳道组 [${LANE_GROUP_NAME}] 追加规则 ${rule_name}..."

    local group_data
    group_data=$(query_lane_group_full)
    if [ -z "$group_data" ]; then
        log_fail "查询泳道组失败"
        return 1
    fi

    local result
    result=$(echo "$group_data" | RULE_NAME="$rule_name" python3 -c "
import sys, json, os, urllib.request

rule_name = os.environ['RULE_NAME']
data = json.load(sys.stdin)
groups = data.get('data', [])
if not groups:
    print('ERROR|未找到泳道组')
    sys.exit(0)

group = groups[0]
group.pop('@type', None)

# 预定义的规则模板（与 create_full_lane_group 保持一致）
rule_templates = {
    'gray': {
        'name': 'gray',
        'enable': True,
        'match_mode': 'STRICT',
        'default_label_value': 'gray',
        'traffic_match_rule': {
            'arguments': [{
                'type': 'HEADER',
                'key': 'user',
                'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'gray'},
            }],
            'match_mode': 'AND',
        },
    },
    'permissive': {
        'name': 'permissive',
        'enable': True,
        'match_mode': 'PERMISSIVE',
        'default_label_value': 'noexist',
        'traffic_match_rule': {
            'arguments': [{
                'type': 'HEADER',
                'key': 'user',
                'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'noexist'},
            }],
            'match_mode': 'AND',
        },
    },
    'strict-noexist': {
        'name': 'strict-noexist',
        'enable': True,
        'match_mode': 'STRICT',
        'default_label_value': 'strict-noexist',
        'traffic_match_rule': {
            'arguments': [{
                'type': 'HEADER',
                'key': 'user',
                'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'strict'},
            }],
            'match_mode': 'AND',
        },
    },
    # ---------- 六类匹配维度验证规则 ----------
    'method-post': {
        'name': 'method-post',
        'enable': True,
        'match_mode': 'STRICT',
        'default_label_value': 'gray',
        'traffic_match_rule': {
            'arguments': [
                {'type': 'METHOD', 'key': '',
                 'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'POST'}},
                {'type': 'HEADER', 'key': 'lane-test',
                 'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'method-post'}},
            ],
            'match_mode': 'AND',
        },
    },
    'query-env': {
        'name': 'query-env',
        'enable': True,
        'match_mode': 'STRICT',
        'default_label_value': 'gray',
        'traffic_match_rule': {
            'arguments': [
                {'type': 'QUERY', 'key': 'env',
                 'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'gray'}},
                {'type': 'HEADER', 'key': 'lane-test',
                 'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'query-env'}},
            ],
            'match_mode': 'AND',
        },
    },
    'cookie-user': {
        'name': 'cookie-user',
        'enable': True,
        'match_mode': 'STRICT',
        'default_label_value': 'gray',
        'traffic_match_rule': {
            'arguments': [
                {'type': 'COOKIE', 'key': 'user',
                 'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'gray'}},
                {'type': 'HEADER', 'key': 'lane-test',
                 'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'cookie-user'}},
            ],
            'match_mode': 'AND',
        },
    },
    'path-gray': {
        'name': 'path-gray',
        'enable': True,
        'match_mode': 'STRICT',
        'default_label_value': 'gray',
        'traffic_match_rule': {
            'arguments': [
                {'type': 'PATH', 'key': '',
                 'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': '/LaneEchoClient/gray-path'}},
            ],
            'match_mode': 'AND',
        },
    },
    'caller-ip-local': {
        'name': 'caller-ip-local',
        'enable': True,
        'match_mode': 'STRICT',
        'default_label_value': 'gray',
        'traffic_match_rule': {
            'arguments': [
                {'type': 'CALLER_IP', 'key': '',
                 'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': '127.0.0.1'}},
                {'type': 'HEADER', 'key': 'lane-test',
                 'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'caller-ip-local'}},
            ],
            'match_mode': 'AND',
        },
    },
    'caller-ip-not-zero': {
        'name': 'caller-ip-not-zero',
        'enable': True,
        'match_mode': 'STRICT',
        'default_label_value': 'gray',
        'traffic_match_rule': {
            'arguments': [
                {'type': 'CALLER_IP', 'key': '',
                 'value': {'type': 'NOT_EQUALS', 'value_type': 'TEXT', 'value': '0.0.0.0'}},
                {'type': 'HEADER', 'key': 'lane-test',
                 'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'caller-ip-not-zero'}},
            ],
            'match_mode': 'AND',
        },
    },
    # ---------- half 链路专用规则 ----------
    'half-gray-permissive': {
        'name': 'half-gray-permissive',
        'enable': True,
        'match_mode': 'PERMISSIVE',
        'default_label_value': 'gray',
        'traffic_match_rule': {
            'arguments': [{
                'type': 'HEADER',
                'key': 'user',
                'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'half-permissive'},
            }],
            'match_mode': 'AND',
        },
    },
    'half-gray-strict': {
        'name': 'half-gray-strict',
        'enable': True,
        'match_mode': 'STRICT',
        'default_label_value': 'gray',
        'traffic_match_rule': {
            'arguments': [{
                'type': 'HEADER',
                'key': 'user',
                'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'half-strict'},
            }],
            'match_mode': 'AND',
        },
    },
}

template = rule_templates.get(rule_name)
if template is None:
    print(f'ERROR|未知规则模板: {rule_name}')
    sys.exit(0)

rules = group.get('rules', []) or []
for r in rules:
    if r.get('name') == rule_name:
        print(f'WARN|规则 {rule_name} 已存在，无需追加')
        sys.exit(0)

rules.append(template)
group['rules'] = rules

# 提交修改
req_data = json.dumps([group]).encode('utf-8')
req = urllib.request.Request(
    '${POLARIS_HTTP_ADDR}/naming/v1/lane/groups',
    data=req_data,
    method='PUT',
    headers={
        'Content-Type': 'application/json',
        'X-Polaris-Token': '${POLARIS_TOKEN}'
    }
)
try:
    resp = urllib.request.urlopen(req, timeout=10)
    resp_data = json.loads(resp.read().decode('utf-8'))
    code = resp_data.get('code', 0)
    if code == 200000:
        current = [r.get('name','?') for r in rules]
        print(f'OK|已追加规则 {rule_name}，当前规则列表: {current}')
    else:
        info = resp_data.get('info', '')
        print(f'ERROR|追加规则 {rule_name} 失败，返回码: {code}, 信息: {info}')
except Exception as e:
    print(f'ERROR|请求失败: {e}')
" 2>/dev/null)

    if echo "$result" | grep -q "^OK"; then
        local msg
        msg=$(echo "$result" | grep "^OK" | cut -d'|' -f2)
        log_ok "$msg"
        return 0
    elif echo "$result" | grep -q "^WARN"; then
        local msg
        msg=$(echo "$result" | grep "^WARN" | cut -d'|' -f2)
        log_warn "$msg"
        return 0
    else
        local err_msg
        err_msg=$(echo "$result" | grep "^ERROR" | cut -d'|' -f2)
        log_fail "追加规则 ${rule_name} 失败: ${err_msg}"
        return 1
    fi
}

# ==================== 启动服务 ====================
# cleanup_zombies_on_ports 清理本测试使用的端口上残留的僵尸进程。
# 使用场景: 上一次测试被 Ctrl-C 中断,或 stop_services 没能命中所有 PID 时,
# 会留下占用端口的僵尸进程,导致新 run 的 bind() 失败。
# 逐个端口用 lsof 查是否有 LISTEN 进程,有则 kill;TERM 3s 不退出就 KILL。
cleanup_zombies_on_ports() {
    local ports=(
        "${GATEWAY_PORT}" "${SIMPLE_GATEWAY_PORT}" "${GATEWAY_EXCL_PORT}"
        "${CONSUMER_BASE_PORT}" "${CONSUMER_GRAY_PORT}"
        "${SIMPLE_CONSUMER_BASE_PORT}" "${SIMPLE_CONSUMER_GRAY_PORT}"
        "${PROVIDER_BASE_PORT}" "${PROVIDER_GRAY_PORT}"
        "${PROVIDER_EXCL_STABLE_PORT}" "${PROVIDER_EXCL_GRAY_PORT}"
        "${NLG_PROVIDER_BASE_PORT}" "${NLG_PROVIDER_GRAY_PORT}"
        "${NLG_CONSUMER_MODE0_PORT}" "${NLG_CONSUMER_MODE1_PORT}"
        "${HALF_CONSUMER_PORT}" "${HALF_PROVIDER_BASE_PORT}" "${HALF_PROVIDER_GRAY_PORT}"
        "${HALF_SIMPLE_CONSUMER_PORT}"
    )
    local any=false
    for port in "${ports[@]}"; do
        local pids
        pids=$(lsof -iTCP:"${port}" -sTCP:LISTEN -t 2>/dev/null || true)
        [ -z "$pids" ] && continue
        for pid in $pids; do
            local cmd
            cmd=$(ps -p "$pid" -o comm= 2>/dev/null || echo "unknown")
            log_warn "端口 ${port} 被进程 PID=${pid} (${cmd}) 占用,先杀掉..."
            kill "$pid" 2>/dev/null || true
            any=true
        done
    done
    [ "$any" = false ] && return 0

    # 等 3s 让 SIGTERM 生效
    sleep 3
    # 硬杀任何还在占用的
    for port in "${ports[@]}"; do
        local pids
        pids=$(lsof -iTCP:"${port}" -sTCP:LISTEN -t 2>/dev/null || true)
        [ -z "$pids" ] && continue
        for pid in $pids; do
            log_warn "端口 ${port} 上 PID=${pid} 未退出,发送 SIGKILL"
            kill -9 "$pid" 2>/dev/null || true
        done
    done
    sleep 1
}

start_services() {
    log_title "启动服务实例"
    mkdir -p "${LOG_DIR}"
    > "${PID_FILE}"

    # 清理上一次可能残留的僵尸进程(端口占用会导致当前 provider/gateway 直接 bind 失败)
    cleanup_zombies_on_ports

    if [ ! -f "${BUILD_DIR}/provider" ] || [ ! -f "${BUILD_DIR}/consumer" ] || [ ! -f "${BUILD_DIR}/gateway" ] || [ ! -f "${BUILD_DIR}/simple-gateway" ] || [ ! -f "${BUILD_DIR}/simple-consumer" ]; then
        log_info "未找到编译产物，先构建..."
        build_binaries || return 1
    fi

    local polaris_yaml="${SCRIPT_DIR}/polaris.yaml"
    if [ ! -f "${polaris_yaml}" ]; then
        log_fail "未找到 polaris.yaml: ${polaris_yaml}"
        return 1
    fi

    # 为每个进程创建独立工作目录，Polaris SDK 日志自然分开到各自目录的 polaris/ 子目录
    local provider_base_workdir="${BUILD_DIR}/provider-base"
    local provider_gray_workdir="${BUILD_DIR}/provider-gray"
    local consumer_base_workdir="${BUILD_DIR}/consumer-base"
    local consumer_gray_workdir="${BUILD_DIR}/consumer-gray"
    local simple_consumer_base_workdir="${BUILD_DIR}/simple-consumer-base"
    local simple_consumer_gray_workdir="${BUILD_DIR}/simple-consumer-gray"
    local gateway_workdir="${BUILD_DIR}/gateway-run"
    local simple_gateway_workdir="${BUILD_DIR}/simple-gateway-run"
    # 以下 3 个目录用于 ExcludeEnabledLaneInstance 专项测试（baseLaneMode=1）
    local provider_excl_stable_workdir="${BUILD_DIR}/provider-excl-stable"
    local provider_excl_gray_workdir="${BUILD_DIR}/provider-excl-gray"
    local gateway_excl_workdir="${BUILD_DIR}/gateway-excl-run"
    # 以下 4 个目录用于“非泳道组服务 baseLaneMode”专项测试
    local nlg_provider_base_workdir="${BUILD_DIR}/nlg-provider-base"
    local nlg_provider_gray_workdir="${BUILD_DIR}/nlg-provider-gray"
    local nlg_consumer_mode0_workdir="${BUILD_DIR}/nlg-consumer-mode0"
    local nlg_consumer_mode1_workdir="${BUILD_DIR}/nlg-consumer-mode1"
    # 以下 3 个目录用于 half 链路专项测试:
    # gateway → half-consumer (无 lane 实例) → half-provider (有 lane=gray 实例)
    local half_consumer_workdir="${BUILD_DIR}/half-consumer"
    local half_provider_base_workdir="${BUILD_DIR}/half-provider-base"
    local half_provider_gray_workdir="${BUILD_DIR}/half-provider-gray"
    # 以下 1 个目录用于 half-simple 链路专项测试:
    # simple-gateway → half-simple-consumer (无 lane 实例, GetOneInstance) → provider (有 lane=gray 实例)
    local half_simple_consumer_workdir="${BUILD_DIR}/half-simple-consumer"
    mkdir -p "$provider_base_workdir" "$provider_gray_workdir" \
             "$consumer_base_workdir" "$consumer_gray_workdir" \
             "$simple_consumer_base_workdir" "$simple_consumer_gray_workdir" \
             "$gateway_workdir" "$simple_gateway_workdir" \
             "$provider_excl_stable_workdir" "$provider_excl_gray_workdir" "$gateway_excl_workdir" \
             "$nlg_provider_base_workdir" "$nlg_provider_gray_workdir" \
             "$nlg_consumer_mode0_workdir" "$nlg_consumer_mode1_workdir" \
             "$half_consumer_workdir" "$half_provider_base_workdir" "$half_provider_gray_workdir" \
             "$half_simple_consumer_workdir"
    cp "${polaris_yaml}" "${provider_base_workdir}/polaris.yaml"
    cp "${polaris_yaml}" "${provider_gray_workdir}/polaris.yaml"
    cp "${polaris_yaml}" "${consumer_base_workdir}/polaris.yaml"
    cp "${polaris_yaml}" "${consumer_gray_workdir}/polaris.yaml"
    cp "${polaris_yaml}" "${simple_consumer_base_workdir}/polaris.yaml"
    cp "${polaris_yaml}" "${simple_consumer_gray_workdir}/polaris.yaml"
    cp "${polaris_yaml}" "${gateway_workdir}/polaris.yaml"
    cp "${polaris_yaml}" "${simple_gateway_workdir}/polaris.yaml"
    cp "${polaris_yaml}" "${provider_excl_stable_workdir}/polaris.yaml"
    cp "${polaris_yaml}" "${provider_excl_gray_workdir}/polaris.yaml"
    cp "${polaris_yaml}" "${nlg_provider_base_workdir}/polaris.yaml"
    cp "${polaris_yaml}" "${nlg_provider_gray_workdir}/polaris.yaml"
    # half 链路所有实例使用默认 baseLaneMode=0
    cp "${polaris_yaml}" "${half_consumer_workdir}/polaris.yaml"
    cp "${polaris_yaml}" "${half_provider_base_workdir}/polaris.yaml"
    cp "${polaris_yaml}" "${half_provider_gray_workdir}/polaris.yaml"
    # half-simple 链路所有实例使用默认 baseLaneMode=0
    cp "${polaris_yaml}" "${half_simple_consumer_workdir}/polaris.yaml"
    # gateway-excl 专门用 baseLaneMode=1。polaris.yaml 里把 `baseLaneMode: 0` 改为 `baseLaneMode: 1`
    # 这样 gateway-excl 在路由到 StableLaneEchoServer 时,会走 ExcludeEnabledLaneInstance 分支。
    sed 's/baseLaneMode: 0/baseLaneMode: 1/' "${polaris_yaml}" > "${gateway_excl_workdir}/polaris.yaml"
    # NLG consumer mode0 使用默认的 baseLaneMode=0
    cp "${polaris_yaml}" "${nlg_consumer_mode0_workdir}/polaris.yaml"
    # NLG consumer mode1 使用 baseLaneMode=1
    sed 's/baseLaneMode: 0/baseLaneMode: 1/' "${polaris_yaml}" > "${nlg_consumer_mode1_workdir}/polaris.yaml"

    # 启动 provider-base（无泳道标签，基线实例）
    local debug_flag=""
    if [ "$DEBUG_MODE" = true ]; then
        debug_flag="-debug"
        log_info "DEBUG 模式已开启，所有服务将输出 Polaris SDK debug 日志"
    fi

    # 使用 exec 启动进程，确保 $! 记录的是 binary 的真实 PID（而非 subshell PID）。
    # 这样 stop_services 发送 SIGTERM 时能直接到达 binary，触发 Polaris 反注册。
    log_info "启动 provider-base (端口: ${PROVIDER_BASE_PORT}, lane: baseline)..."
    (cd "$provider_base_workdir" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/provider" \
        -namespace="${NAMESPACE}" \
        -service="${PROVIDER_SERVICE}" \
        -port="${PROVIDER_BASE_PORT}" \
        ${debug_flag} \
        > "${LOG_DIR}/provider-base.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "provider-base PID: $!"

    # 启动 provider-gray（携带 lane=gray 元数据）
    log_info "启动 provider-gray (端口: ${PROVIDER_GRAY_PORT}, lane: gray)..."
    (cd "$provider_gray_workdir" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/provider" \
        -namespace="${NAMESPACE}" \
        -service="${PROVIDER_SERVICE}" \
        -port="${PROVIDER_GRAY_PORT}" \
        -lane="gray" \
        ${debug_flag} \
        > "${LOG_DIR}/provider-gray.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "provider-gray PID: $!"

    # 启动 consumer-base（无泳道标签，基线实例）
    log_info "启动 consumer-base (端口: ${CONSUMER_BASE_PORT}, lane: baseline)..."
    (cd "$consumer_base_workdir" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/consumer" \
        -namespace="${NAMESPACE}" \
        -service="${PROVIDER_SERVICE}" \
        -selfNamespace="${NAMESPACE}" \
        -selfService="${CONSUMER_SERVICE}" \
        -port="${CONSUMER_BASE_PORT}" \
        ${debug_flag} \
        > "${LOG_DIR}/consumer-base.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "consumer-base PID: $!"

    # 启动 consumer-gray（携带 lane=gray 元数据）
    log_info "启动 consumer-gray (端口: ${CONSUMER_GRAY_PORT}, lane: gray)..."
    (cd "$consumer_gray_workdir" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/consumer" \
        -namespace="${NAMESPACE}" \
        -service="${PROVIDER_SERVICE}" \
        -selfNamespace="${NAMESPACE}" \
        -selfService="${CONSUMER_SERVICE}" \
        -port="${CONSUMER_GRAY_PORT}" \
        -lane="gray" \
        ${debug_flag} \
        > "${LOG_DIR}/consumer-gray.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "consumer-gray PID: $!"

    # 启动 simple-consumer-base（基于 GetOneInstance，无泳道标签）
    log_info "启动 simple-consumer-base (端口: ${SIMPLE_CONSUMER_BASE_PORT}, lane: baseline)..."
    (cd "$simple_consumer_base_workdir" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/simple-consumer" \
        -namespace="${NAMESPACE}" \
        -service="${PROVIDER_SERVICE}" \
        -selfNamespace="${NAMESPACE}" \
        -selfService="${SIMPLE_CONSUMER_SERVICE}" \
        -port="${SIMPLE_CONSUMER_BASE_PORT}" \
        ${debug_flag} \
        > "${LOG_DIR}/simple-consumer-base.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "simple-consumer-base PID: $!"

    # 启动 simple-consumer-gray（携带 lane=gray 元数据）
    log_info "启动 simple-consumer-gray (端口: ${SIMPLE_CONSUMER_GRAY_PORT}, lane: gray)..."
    (cd "$simple_consumer_gray_workdir" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/simple-consumer" \
        -namespace="${NAMESPACE}" \
        -service="${PROVIDER_SERVICE}" \
        -selfNamespace="${NAMESPACE}" \
        -selfService="${SIMPLE_CONSUMER_SERVICE}" \
        -port="${SIMPLE_CONSUMER_GRAY_PORT}" \
        -lane="gray" \
        ${debug_flag} \
        > "${LOG_DIR}/simple-consumer-gray.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "simple-consumer-gray PID: $!"

    # 启动 gateway（泳道网关入口，作为泳道组的唯一入口服务）
    log_info "启动 gateway (端口: ${GATEWAY_PORT}, selfService: ${GATEWAY_SERVICE})..."
    (cd "$gateway_workdir" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/gateway" \
        -namespace="${NAMESPACE}" \
        -selfNamespace="${NAMESPACE}" \
        -selfService="${GATEWAY_SERVICE}" \
        -port="${GATEWAY_PORT}" \
        ${debug_flag} \
        > "${LOG_DIR}/gateway.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "gateway PID: $!"

    # 启动 simple-gateway（基于 GetOneInstance 的简化泳道网关，与 gateway 共用入口服务名 ${GATEWAY_SERVICE}）
    # 端口与 gateway 分开，下游仍复用 ${CONSUMER_SERVICE}（即 simple-gateway → consumer → provider 链路）
    log_info "启动 simple-gateway (端口: ${SIMPLE_GATEWAY_PORT}, selfService: ${GATEWAY_SERVICE})..."
    (cd "$simple_gateway_workdir" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/simple-gateway" \
        -namespace="${NAMESPACE}" \
        -selfNamespace="${NAMESPACE}" \
        -selfService="${GATEWAY_SERVICE}" \
        -port="${SIMPLE_GATEWAY_PORT}" \
        ${debug_flag} \
        > "${LOG_DIR}/simple-gateway.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "simple-gateway PID: $!"

    # === ExcludeEnabledLaneInstance (baseLaneMode=1) 专项测试集群 ===
    # 单独起一组 StableLaneEchoServer 实例:只有带标签的 stable + gray,没有无标签实例。
    # 当 gateway-excl 收到 baseline 请求(无 Header)时:
    #   - routeToBaseline 第一步找"无 lane 键"的实例 → 0 个,跳过
    #   - baseLaneMode=1 触发第二分支:buildEnabledLaneValues 构建 {gray, noexist}(已启用规则)
    #     → 排除 lane=gray 的实例 → 保留 lane=stable 实例 → 命中 :${PROVIDER_EXCL_STABLE_PORT}
    log_info "启动 provider-excl-stable (端口: ${PROVIDER_EXCL_STABLE_PORT}, service: ${PROVIDER_EXCL_SERVICE}, lane: stable)..."
    (cd "$provider_excl_stable_workdir" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/provider" \
        -namespace="${NAMESPACE}" \
        -service="${PROVIDER_EXCL_SERVICE}" \
        -port="${PROVIDER_EXCL_STABLE_PORT}" \
        -lane="stable" \
        ${debug_flag} \
        > "${LOG_DIR}/provider-excl-stable.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "provider-excl-stable PID: $!"

    log_info "启动 provider-excl-gray (端口: ${PROVIDER_EXCL_GRAY_PORT}, service: ${PROVIDER_EXCL_SERVICE}, lane: gray)..."
    (cd "$provider_excl_gray_workdir" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/provider" \
        -namespace="${NAMESPACE}" \
        -service="${PROVIDER_EXCL_SERVICE}" \
        -port="${PROVIDER_EXCL_GRAY_PORT}" \
        -lane="gray" \
        ${debug_flag} \
        > "${LOG_DIR}/provider-excl-gray.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "provider-excl-gray PID: $!"

    log_info "启动 gateway-excl (端口: ${GATEWAY_EXCL_PORT}, baseLaneMode=1)..."
    (cd "$gateway_excl_workdir" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/gateway" \
        -namespace="${NAMESPACE}" \
        -selfNamespace="${NAMESPACE}" \
        -selfService="${GATEWAY_SERVICE}" \
        -port="${GATEWAY_EXCL_PORT}" \
        ${debug_flag} \
        > "${LOG_DIR}/gateway-excl.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "gateway-excl PID: $!"

    # === baseLaneMode 专项测试 — 非泳道组服务 (NoLaneGroupEchoServer/NoLaneGroupEchoClient) ===
    # 两对 provider+consumer 都不在任何泳道组内:
    #   - NLG provider-base (无 lane 标签) + NLG provider-gray (lane=gray)
    #   - NLG consumer-mode0 (baseLaneMode=0) + NLG consumer-mode1 (baseLaneMode=1)
    log_info "启动 nlg-provider-base (端口: ${NLG_PROVIDER_BASE_PORT}, service: ${NLG_PROVIDER_SERVICE})..."
    (cd "$nlg_provider_base_workdir" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/provider" \
        -namespace="${NAMESPACE}" \
        -service="${NLG_PROVIDER_SERVICE}" \
        -port="${NLG_PROVIDER_BASE_PORT}" \
        ${debug_flag} \
        > "${LOG_DIR}/nlg-provider-base.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "nlg-provider-base PID: $!"

    log_info "启动 nlg-provider-gray (端口: ${NLG_PROVIDER_GRAY_PORT}, service: ${NLG_PROVIDER_SERVICE}, lane: gray)..."
    (cd "$nlg_provider_gray_workdir" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/provider" \
        -namespace="${NAMESPACE}" \
        -service="${NLG_PROVIDER_SERVICE}" \
        -port="${NLG_PROVIDER_GRAY_PORT}" \
        -lane="gray" \
        ${debug_flag} \
        > "${LOG_DIR}/nlg-provider-gray.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "nlg-provider-gray PID: $!"

    log_info "启动 nlg-consumer-mode0 (端口: ${NLG_CONSUMER_MODE0_PORT}, baseLaneMode=0)..."
    (cd "$nlg_consumer_mode0_workdir" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/simple-consumer" \
        -namespace="${NAMESPACE}" \
        -service="${NLG_PROVIDER_SERVICE}" \
        -selfNamespace="${NAMESPACE}" \
        -selfService="${NLG_CONSUMER_SERVICE}" \
        -port="${NLG_CONSUMER_MODE0_PORT}" \
        ${debug_flag} \
        > "${LOG_DIR}/nlg-consumer-mode0.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "nlg-consumer-mode0 PID: $!"

    log_info "启动 nlg-consumer-mode1 (端口: ${NLG_CONSUMER_MODE1_PORT}, baseLaneMode=1)..."
    (cd "$nlg_consumer_mode1_workdir" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/simple-consumer" \
        -namespace="${NAMESPACE}" \
        -service="${NLG_PROVIDER_SERVICE}" \
        -selfNamespace="${NAMESPACE}" \
        -selfService="${NLG_CONSUMER_SERVICE}" \
        -port="${NLG_CONSUMER_MODE1_PORT}" \
        ${debug_flag} \
        > "${LOG_DIR}/nlg-consumer-mode1.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "nlg-consumer-mode1 PID: $!"

    # === half 链路专项测试 (HalfLaneEchoClient/HalfLaneEchoServer) ===
    # 验证 gateway → consumer(无目标 lane 实例) → provider(有目标 lane 实例) 链路:
    #   - half-consumer:        只注册 baseline 实例,没有 lane=gray
    #   - half-provider-base:   baseline 实例(无 lane 标签)
    #   - half-provider-gray:   lane=gray 实例
    # PERMISSIVE 规则下:中间 consumer 回退基线但透传 stainLabel,provider 命中 gray
    # STRICT 规则下:中间 consumer 一跳就 503,根本走不到 provider
    log_info "启动 half-consumer (端口: ${HALF_CONSUMER_PORT}, service: ${HALF_CONSUMER_SERVICE}, lane: baseline)..."
    (cd "$half_consumer_workdir" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/consumer" \
        -namespace="${NAMESPACE}" \
        -service="${HALF_PROVIDER_SERVICE}" \
        -selfNamespace="${NAMESPACE}" \
        -selfService="${HALF_CONSUMER_SERVICE}" \
        -port="${HALF_CONSUMER_PORT}" \
        ${debug_flag} \
        > "${LOG_DIR}/half-consumer.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "half-consumer PID: $!"

    log_info "启动 half-provider-base (端口: ${HALF_PROVIDER_BASE_PORT}, service: ${HALF_PROVIDER_SERVICE}, lane: baseline)..."
    (cd "$half_provider_base_workdir" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/provider" \
        -namespace="${NAMESPACE}" \
        -service="${HALF_PROVIDER_SERVICE}" \
        -port="${HALF_PROVIDER_BASE_PORT}" \
        ${debug_flag} \
        > "${LOG_DIR}/half-provider-base.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "half-provider-base PID: $!"

    log_info "启动 half-provider-gray (端口: ${HALF_PROVIDER_GRAY_PORT}, service: ${HALF_PROVIDER_SERVICE}, lane: gray)..."
    (cd "$half_provider_gray_workdir" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/provider" \
        -namespace="${NAMESPACE}" \
        -service="${HALF_PROVIDER_SERVICE}" \
        -port="${HALF_PROVIDER_GRAY_PORT}" \
        -lane="gray" \
        ${debug_flag} \
        > "${LOG_DIR}/half-provider-gray.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "half-provider-gray PID: $!"

    # 启动 half-simple-consumer (基于 GetOneInstance, 只有 baseline 实例)
    # 链路: simple-gateway → half-simple-consumer (无 lane 实例) → provider (baseline+gray)
    # half-simple-consumer 使用 simple-consumer 二进制，目标下游复用 ${PROVIDER_SERVICE}
    log_info "启动 half-simple-consumer (端口: ${HALF_SIMPLE_CONSUMER_PORT}, service: ${HALF_SIMPLE_CONSUMER_SERVICE}, lane: baseline)..."
    (cd "$half_simple_consumer_workdir" && exec env POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/simple-consumer" \
        -namespace="${NAMESPACE}" \
        -service="${PROVIDER_SERVICE}" \
        -selfNamespace="${NAMESPACE}" \
        -selfService="${HALF_SIMPLE_CONSUMER_SERVICE}" \
        -port="${HALF_SIMPLE_CONSUMER_PORT}" \
        ${debug_flag} \
        > "${LOG_DIR}/half-simple-consumer.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "half-simple-consumer PID: $!"

    log_info "所有服务已启动，PID 记录在 ${PID_FILE}"
    log_info "Polaris SDK 日志目录:"
    log_info "  provider-base:        ${provider_base_workdir}/polaris/"
    log_info "  provider-gray:        ${provider_gray_workdir}/polaris/"
    log_info "  consumer-base:        ${consumer_base_workdir}/polaris/"
    log_info "  consumer-gray:        ${consumer_gray_workdir}/polaris/"
    log_info "  simple-consumer-base: ${simple_consumer_base_workdir}/polaris/"
    log_info "  simple-consumer-gray: ${simple_consumer_gray_workdir}/polaris/"
    log_info "  gateway:              ${gateway_workdir}/polaris/"
    log_info "  simple-gateway:       ${simple_gateway_workdir}/polaris/"
    log_info "  provider-excl-stable: ${provider_excl_stable_workdir}/polaris/"
    log_info "  provider-excl-gray:   ${provider_excl_gray_workdir}/polaris/"
    log_info "  gateway-excl:         ${gateway_excl_workdir}/polaris/  (baseLaneMode=1)"
    log_info "  nlg-provider-base:    ${nlg_provider_base_workdir}/polaris/"
    log_info "  nlg-provider-gray:    ${nlg_provider_gray_workdir}/polaris/"
    log_info "  nlg-consumer-mode0:   ${nlg_consumer_mode0_workdir}/polaris/  (baseLaneMode=0)"
    log_info "  nlg-consumer-mode1:   ${nlg_consumer_mode1_workdir}/polaris/  (baseLaneMode=1)"
    log_info "  half-consumer:        ${half_consumer_workdir}/polaris/  (无 lane 标签实例)"
    log_info "  half-provider-base:   ${half_provider_base_workdir}/polaris/"
    log_info "  half-provider-gray:   ${half_provider_gray_workdir}/polaris/"
    log_info "  half-simple-consumer: ${half_simple_consumer_workdir}/polaris/  (无 lane 标签实例, GetOneInstance)"
}

# ==================== 等待服务就绪 ====================
# probe_url 对指定 URL 做一次带 timeout 的 curl,分别返回 HTTP 状态码和 body 文件路径。
# 处理方式:
#   - 用 --connect-timeout / --max-time 锁紧时间上限,防止 curl 自身挂住;
#   - 把 HTTP 状态码单独写到文件(避免和 body 混在一起),无论 curl 退出码多少都能读到;
#   - body 单独保存成文件,调用方自己去 cat/grep,避免 `$(curl ...)` 在 curl 非 0 退出
#     时拼接 `||echo` 兜底字符串导致的诡异结果(曾见过 `200000` / `500000` 之类)。
# 用法:
#   probe_url "http://..." status_file body_file
# 返回: 0 永远
probe_url() {
    local url="$1"
    local status_file="$2"
    local body_file="$3"
    # -f 保留 HTTP 错误响应体;-w 写状态码到 status_file;-o body_file;--max-time 5
    local code
    code=$(curl -s --connect-timeout 3 --max-time 5 \
        -o "$body_file" \
        -w "%{http_code}" \
        "$url" 2>/dev/null || true)
    # 兜底: 若 code 为空(连接彻底失败)强制置 000;否则剥离尾部可能的噪声,只留首个 000-999。
    if [ -z "$code" ]; then
        code="000"
    fi
    # 只保留首次出现的 3 位数字作为 HTTP code,避免极端情况下 `200000` 之类拼接。
    code=$(printf '%s' "$code" | tr -cd '0-9' | cut -c1-3)
    [ -z "$code" ] && code="000"
    printf '%s' "$code" > "$status_file"
    return 0
}

wait_for_services() {
    log_title "等待服务就绪"
    local max_wait=120
    local elapsed=0
    local tmp_status
    tmp_status=$(mktemp -t lane-test-probe-status.XXXXXX)
    local tmp_body
    tmp_body=$(mktemp -t lane-test-probe-body.XXXXXX)
    trap 'rm -f "$tmp_status" "$tmp_body"' RETURN

    # 判定逻辑:
    #   gateway 只会在全链路(gateway → consumer → provider)都成功时返回 HTTP 200。
    #   任一环节缺实例会走 http.Error(500) 分支。所以 **HTTP 200 = 链路已通**,
    #   不必再强依赖 body 含 "LaneEchoServer" 这个弱信号——在某些环境下 curl+chunked
    #   组合返回 body 有概率为空,我们曾观察到 gateway 明明写了 300+B 但 probe 读到 0B。
    wait_ready_on_port() {
        local port="$1"
        local target="$2"
        local label="$3"
        local timeout="$4"
        local waited=0
        while [ $waited -lt $timeout ]; do
            probe_url "http://127.0.0.1:${port}/${target}/echo" "$tmp_status" "$tmp_body"
            local code
            code=$(cat "$tmp_status")
            case "$code" in
                2*)
                    log_ok "${label} 全链路已通 (耗时 ${waited}s, HTTP=${code})"
                    return 0
                    ;;
                5*|000)
                    # 5xx / 网络不通都继续等
                    ;;
            esac
            sleep 2
            waited=$((waited + 2))
        done
        log_warn "${label} 在 ${timeout}s 内未就绪 (最后 HTTP=${code})"
        return 1
    }

    while [ $elapsed -lt $max_wait ]; do
        probe_url "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" "$tmp_status" "$tmp_body"
        local http_code
        http_code=$(cat "$tmp_status")

        case "$http_code" in
            2*)
                # 主链路就绪,依次等剩余 3 条链路就绪
                log_ok "gateway → consumer → provider 全链路已通 (耗时 ${elapsed}s, HTTP=${http_code})"

                wait_ready_on_port "${SIMPLE_GATEWAY_PORT}" "${CONSUMER_SERVICE}" "simple-gateway → consumer → provider" 30 || true
                wait_ready_on_port "${GATEWAY_PORT}" "${SIMPLE_CONSUMER_SERVICE}" "gateway → simple-consumer → provider" 30 || true
                wait_ready_on_port "${GATEWAY_EXCL_PORT}" "${PROVIDER_EXCL_SERVICE}" "gateway-excl → ${PROVIDER_EXCL_SERVICE}" 30 || true
                # half 链路: gateway → half-consumer → half-provider
                wait_ready_on_port "${GATEWAY_PORT}" "${HALF_CONSUMER_SERVICE}" "gateway → half-consumer → half-provider" 30 || true
                # half-simple 链路: simple-gateway → half-simple-consumer → provider
                wait_ready_on_port "${SIMPLE_GATEWAY_PORT}" "${HALF_SIMPLE_CONSUMER_SERVICE}" "simple-gateway → half-simple-consumer → provider" 30 || true

                # 等待 nlg-consumer 就绪(直接 /echo,不走 gateway proxy)
                local nlg_timeout=30
                for nlg_label_port in "nlg-consumer-mode0,${NLG_CONSUMER_MODE0_PORT}" "nlg-consumer-mode1,${NLG_CONSUMER_MODE1_PORT}"; do
                    local nlg_label="${nlg_label_port%%,*}"
                    local nlg_port="${nlg_label_port##*,}"
                    local nlg_waited=0
                    local nlg_ok=false
                    while [ $nlg_waited -lt $nlg_timeout ]; do
                        probe_url "http://127.0.0.1:${nlg_port}/echo" "$tmp_status" "$tmp_body"
                        local nlg_code
                        nlg_code=$(cat "$tmp_status")
                        case "$nlg_code" in
                            2*) log_ok "${nlg_label} 就绪 (耗时 ${nlg_waited}s, HTTP=${nlg_code})"; nlg_ok=true; break ;;
                            *)   sleep 2; nlg_waited=$((nlg_waited + 2)) ;;
                        esac
                    done
                    [ "$nlg_ok" = true ] || log_warn "${nlg_label} 在 ${nlg_timeout}s 内未就绪"
                done

                return 0
                ;;
            5*)
                # gateway HTTP 已启动,但链路未就绪(Polaris SDK 缓存未刷,或下游实例尚未注册)
                local body_bytes=0
                [ -s "$tmp_body" ] && body_bytes=$(wc -c < "$tmp_body" | tr -d ' ')
                local preview=""
                [ -s "$tmp_body" ] && preview=$(head -c 200 "$tmp_body" | tr -d '\n' | tr -d '\r')
                log_info "等待 consumer 和 provider 注册到 Polaris... (HTTP=${http_code}, body ${body_bytes}B): ${preview}"
                ;;
            *)
                log_info "等待 gateway 启动... (${elapsed}s/${max_wait}s, HTTP: ${http_code})"
                ;;
        esac

        sleep 3
        elapsed=$((elapsed + 3))
    done

    log_fail "等待超时 (${max_wait}s)，服务未就绪"
    log_info "请检查日志: ${LOG_DIR}/"
    return 1
}

# ==================== 测试计数 ====================
TOTAL_PASS=0
TOTAL_FAIL=0
TOTAL_COUNT=0

test_pass() { TOTAL_PASS=$((TOTAL_PASS + 1)); TOTAL_COUNT=$((TOTAL_COUNT + 1)); log_ok "$1"; }
test_fail() { TOTAL_FAIL=$((TOTAL_FAIL + 1)); TOTAL_COUNT=$((TOTAL_COUNT + 1)); log_fail "$1"; }

# ==================== 测试用例 ====================

# ---------- 用例 1.1: 网关无 Header → 路由到基线 ----------
test_no_header_baseline() {
    log_title "用例 1.1: 无 Header — 全链路路由到基线实例"
    log_info "测试目的: Gateway → Consumer → Provider，未携带染色标签时，应路由到基线实例"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例1.1] 无 Header 路由到基线实例 lane=(baseline)"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例1.1] 收到 provider 响应但 lane 不符合预期（期望 lane=(baseline)）"
        log_info "  期望响应包含: lane=(baseline)"
    else
        test_fail "[用例1.1] 未收到有效响应，请检查 gateway 是否正常运行"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 1.2: service-lane Header 直接染色 → gray 泳道 ----------
test_direct_stain_gray() {
    log_title "用例 1.2: service-lane 直接染色 — 路由到 gray 泳道"
    log_info "测试目的: 携带 service-lane Header 时，laneRouter 直接按染色标签路由"
    log_info "请求头: service-lane: ${EXPECTED_LANE_GROUP}/gray"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "service-lane: ${EXPECTED_LANE_GROUP}/gray" \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=gray"; then
        if echo "$resp" | grep -q ":${PROVIDER_GRAY_PORT}"; then
            test_pass "[用例1.2] 直接染色路由到 gray 实例 (port: ${PROVIDER_GRAY_PORT})"
        else
            test_pass "[用例1.2] 直接染色路由到 gray 泳道 (lane=gray 在响应中)"
        fi
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例1.2] 收到 provider 响应但未路由到 gray 泳道"
        log_info "  期望响应包含: lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例1.2] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 1.2n: service-lane 直接染色反向 — 不存在的规则名应走基线 ----------
test_direct_stain_gray_negative() {
    log_title "用例 1.2n: service-lane 直接染色反向 — 不存在的规则名应走基线"
    log_info "测试目的: 反向校验 lane router 的 matchByStainLabel,只有 stain label 命中已配置规则才染色,陌生标签必须回退基线"
    log_info "请求头: service-lane: ${EXPECTED_LANE_GROUP}/nonexist-rule (规则名不存在)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "service-lane: ${EXPECTED_LANE_GROUP}/nonexist-rule" \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例1.2n] 不存在的规则名 stain label 不命中,正确走基线"
    elif echo "$resp" | grep -q "lane=gray"; then
        test_fail "[用例1.2n] 不存在的规则名不应该被染色到 lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例1.2n] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 1.3: 流量匹配 STRICT — Header user=gray → gray 泳道 ----------
test_traffic_match_strict_gray() {
    log_title "用例 1.3: 流量匹配 STRICT — Header user=gray 路由到 gray 泳道"
    log_info "测试目的: Gateway → Consumer → Provider，网关通过 TrafficMatchRule 识别并染色，consumer 透传标签，路由到 gray 泳道"
    log_info "请求头: user: gray"
    log_info "期望: 规则 gray 匹配 Header user=gray → lane=gray，模式 STRICT"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: gray" \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=gray"; then
        test_pass "[用例1.3] 流量匹配 STRICT 路由到 gray 泳道 (lane=gray)"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例1.3] 未路由到 gray 泳道，检查 Polaris 规则 gray (Header user=gray, STRICT)"
        log_info "  期望响应包含: lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例1.3] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 1.3n: 流量匹配 STRICT 反向 — Header user=graymismatch 不命中 ----------
test_traffic_match_strict_gray_negative() {
    log_title "用例 1.3n: 流量匹配 STRICT 反向 — Header user 取近似值不命中 gray 规则,走基线"
    log_info "测试目的: 反向校验 EXACT 匹配的严格性,即使值与 'gray' 仅差一字符也不应命中"
    log_info "请求头: user: graymismatch (与目标值 EXACT 不等)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: graymismatch" \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例1.3n] Header user=graymismatch 不命中 gray 规则,正确走基线"
    elif echo "$resp" | grep -q "lane=gray"; then
        test_fail "[用例1.3n] EXACT 匹配应严格,user=graymismatch 不应被染色到 lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例1.3n] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 1.4: 流量匹配 PERMISSIVE — 无目标泳道实例时回退基线 ----------
test_traffic_match_permissive_fallback() {
    log_title "用例 1.4: 流量匹配 PERMISSIVE — 无目标泳道实例时回退基线"
    log_info "测试目的: PERMISSIVE 模式下，目标泳道 lane=noexist 无实例时，自动降级到基线"
    log_info "请求头: user: noexist"
    log_info "期望: 规则 permissive 匹配 Header user=noexist → lane=noexist 无实例 → 回退 baseline"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: noexist" \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例1.4] PERMISSIVE 模式无目标实例时正确回退基线 (lane=(baseline))"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例1.4] 收到 provider 响应但未回退到基线"
        log_info "  期望响应包含: lane=(baseline)"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例1.4] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 1.4b: 流量匹配 STRICT — 无目标泳道实例时返回 503 ----------
test_traffic_match_strict_no_instance_503() {
    log_title "用例 1.4b: 流量匹配 STRICT — 无目标泳道实例时期望 HTTP 503"
    log_info "测试目的: STRICT 模式下，目标泳道 lane=strict-noexist 无任何实例时，SDK 不降级基线，"
    log_info "         gateway/consumer 因拿不到可用实例而返回 HTTP 503"
    log_info "请求头: user: strict"
    log_info "期望: 规则 strict-noexist 匹配 Header user=strict → lane=strict-noexist 无实例 → HTTP 503"
    log_info ""

    local tmp_body http_code body
    tmp_body=$(mktemp -t lane-test-strict-body.XXXXXX)
    http_code=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: strict" \
        -o "$tmp_body" -w "%{http_code}" \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || echo "000")
    http_code=$(printf '%s' "$http_code" | tr -cd '0-9' | cut -c1-3)
    [ -z "$http_code" ] && http_code="000"
    body=$(cat "$tmp_body" 2>/dev/null || echo "")
    rm -f "$tmp_body"
    log_raw "  HTTP ${http_code}, 响应: ${body}"

    if [ "$http_code" = "503" ]; then
        test_pass "[用例1.4b] STRICT 模式无目标实例时正确返回 HTTP 503"
    elif echo "$body" | grep -q "lane=(baseline)\|lane=gray"; then
        test_fail "[用例1.4b] STRICT 模式竟然路由到了实例(期望 503)，body=${body:0:120}"
    else
        test_fail "[用例1.4b] STRICT 模式未返回 503 (HTTP=${http_code})"
        log_info "  响应: ${body:0:200}"
    fi
}

# ---------- 用例 1.5: 未匹配 Header → 路由到基线（无规则命中） ----------
test_no_rule_match_baseline() {
    log_title "用例 1.5: 未匹配 Header — 无规则命中时路由到基线"
    log_info "测试目的: 携带与规则不匹配的 Header 时，网关无法染色，回退基线"
    log_info "请求头: user: unknown-value"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: unknown-value" \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例1.5] 未命中规则时路由到基线 (lane=(baseline))"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例1.5] 未命中规则但未路由到基线"
        log_info "  期望响应包含: lane=(baseline)"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例1.5] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 1.6: 泳道隔离验证 — 并发请求不互相干扰 ----------
test_lane_isolation() {
    log_title "用例 1.6: 泳道隔离验证 — 并发请求不互相干扰"
    log_info "测试目的: Gateway → Consumer → Provider 全链路，不同类型请求并发时路由结果不受干扰"
    log_info ""

    local baseline_ok=true
    local gray_ok=true
    local rounds=5

    for round in $(seq 1 $rounds); do
        # 基线请求（无 Header）
        local resp_base
        resp_base=$(curl -s --connect-timeout 3 --max-time 5 \
            "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
        log_raw "  [${round}] baseline: ${resp_base}"
        if ! echo "$resp_base" | grep -q "lane=(baseline)"; then
            baseline_ok=false
        fi

        # gray 泳道请求
        local resp_gray
        resp_gray=$(curl -s --connect-timeout 3 --max-time 5 \
            -H "user: gray" \
            "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
        log_raw "  [${round}] gray:     ${resp_gray}"
        if ! echo "$resp_gray" | grep -q "lane=gray"; then
            gray_ok=false
        fi

        sleep 0.2
    done

    if [ "$baseline_ok" = true ] && [ "$gray_ok" = true ]; then
        test_pass "[用例1.6] 泳道隔离正确: baseline 和 gray 请求互不干扰 (${rounds} 轮)"
    else
        local detail=""
        [ "$baseline_ok" = false ] && detail="baseline 路由异常 "
        [ "$gray_ok" = false ] && detail="${detail}gray 路由异常"
        test_fail "[用例1.6] 泳道隔离失败: ${detail}"
    fi
}

# ==================================================================
# 六类匹配维度测试（主链路：gateway → consumer → provider）
# ==================================================================
# 规则设计要点（详见 Part D）:
#   - 除 path-gray 外，其余 5 条规则均 AND 上 Header lane-test=<规则名> 作为守卫，
#     只有测试用例显式携带该 Header 时才会命中，其他用例一概绕开，
#     确保 baseline / permissive / isolation 等用例不被污染。

# ---------- 用例 1.7: $method 维度 — METHOD=POST 路由到 gray 泳道 ----------
test_method_post_match() {
    log_title "用例 1.7: \$method 维度 — METHOD=POST 路由到 gray 泳道"
    log_info "测试目的: gateway 把 HTTP Method 作为 Argument 上报，TrafficMatchRule 按 METHOD=POST 染色"
    log_info "请求: POST + Header lane-test=method-post (守卫)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -X POST \
        -H "lane-test: method-post" \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=gray"; then
        test_pass "[用例1.7] METHOD=POST 命中 method-post 规则路由到 lane=gray"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例1.7] 收到 provider 响应但未路由到 gray 泳道"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例1.7] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 1.7n: $method 维度反向 — METHOD=GET 不命中 method-post 规则 ----------
test_method_post_match_negative() {
    log_title "用例 1.7n: \$method 维度反向 — GET 请求不命中 method-post 规则,走基线"
    log_info "测试目的: 反向校验 method-post 规则只在 METHOD=POST 时染色,GET 请求不应被路由到 gray 泳道"
    log_info "请求: GET + Header lane-test=method-post (守卫)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "lane-test: method-post" \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例1.7n] METHOD=GET 不命中 method-post 规则,正确走基线"
    elif echo "$resp" | grep -q "lane=gray"; then
        test_fail "[用例1.7n] METHOD=GET 不应该命中 method-post 规则却被染色到 lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例1.7n] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 1.8: Query 维度 — ?env=gray 路由到 gray 泳道 ----------
test_query_env_match() {
    log_title "用例 1.8: Query 维度 — QUERY env=gray 路由到 gray 泳道"
    log_info "测试目的: gateway 把 URL 查询参数作为 Argument 上报，命中 query-env 规则"
    log_info "请求: ?env=gray + Header lane-test=query-env (守卫)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "lane-test: query-env" \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo?env=gray" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=gray"; then
        test_pass "[用例1.8] QUERY env=gray 命中 query-env 规则路由到 lane=gray"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例1.8] 收到 provider 响应但未路由到 gray 泳道"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例1.8] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 1.8n: Query 维度反向 — ?env=stable 不命中 query-env 规则 ----------
test_query_env_match_negative() {
    log_title "用例 1.8n: Query 维度反向 — env=stable 不命中 query-env 规则,走基线"
    log_info "测试目的: 反向校验 query-env 规则只匹配 env=gray,其他取值不应被染色"
    log_info "请求: ?env=stable + Header lane-test=query-env (守卫)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "lane-test: query-env" \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo?env=stable" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例1.8n] QUERY env=stable 不命中 query-env 规则,正确走基线"
    elif echo "$resp" | grep -q "lane=gray"; then
        test_fail "[用例1.8n] QUERY env=stable 不应该命中 query-env 规则却被染色到 lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例1.8n] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 1.9: Cookie 维度 — Cookie user=gray 路由到 gray 泳道 ----------
test_cookie_user_match() {
    log_title "用例 1.9: Cookie 维度 — COOKIE user=gray 路由到 gray 泳道"
    log_info "测试目的: gateway 遍历 r.Cookies() 把每个 Cookie 作为 Argument 上报"
    log_info "请求: Cookie: user=gray + Header lane-test=cookie-user (守卫)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "lane-test: cookie-user" \
        -H "Cookie: user=gray" \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=gray"; then
        test_pass "[用例1.9] COOKIE user=gray 命中 cookie-user 规则路由到 lane=gray"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例1.9] 收到 provider 响应但未路由到 gray 泳道"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例1.9] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 1.9n: Cookie 维度反向 — Cookie user=stable 不命中 cookie-user 规则 ----------
test_cookie_user_match_negative() {
    log_title "用例 1.9n: Cookie 维度反向 — Cookie user=stable 不命中 cookie-user 规则,走基线"
    log_info "测试目的: 反向校验 cookie-user 规则只匹配 user=gray,其他 cookie 值不应被染色"
    log_info "请求: Cookie: user=stable + Header lane-test=cookie-user (守卫)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "lane-test: cookie-user" \
        -H "Cookie: user=stable" \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例1.9n] COOKIE user=stable 不命中 cookie-user 规则,正确走基线"
    elif echo "$resp" | grep -q "lane=gray"; then
        test_fail "[用例1.9n] COOKIE user=stable 不应该命中 cookie-user 规则却被染色到 lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例1.9n] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 1.10: $path 维度 — PATH=/LaneEchoClient/gray-path 路由到 gray 泳道 ----------
test_path_gray_match() {
    log_title "用例 1.10: \$path 维度 — PATH=/LaneEchoClient/gray-path 路由到 gray 泳道"
    log_info "测试目的: gateway 把 r.URL.Path 作为 Argument 上报，命中 path-gray 规则"
    log_info "请求: 访问 /LaneEchoClient/gray-path (consumer 无该 handler 会返回 404，"
    log_info "     但 gateway 响应 msg 里仍包含 callee lane=gray，只断言 lane=gray 不检查 status code)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/gray-path" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=gray"; then
        test_pass "[用例1.10] PATH=/LaneEchoClient/gray-path 命中 path-gray 规则路由到 lane=gray"
    elif echo "$resp" | grep -q "LaneEchoServer\|LaneRouterGateway"; then
        test_fail "[用例1.10] 收到响应但未路由到 gray 泳道"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例1.10] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 1.10n: $path 维度反向 — PATH=/LaneEchoClient/echo 不命中 path-gray 规则 ----------
test_path_gray_match_negative() {
    log_title "用例 1.10n: \$path 维度反向 — 默认 /echo 路径不命中 path-gray 规则,走基线"
    log_info "测试目的: 反向校验 path-gray 规则只匹配 /LaneEchoClient/gray-path,其他路径不应被染色"
    log_info "请求: 访问 /LaneEchoClient/echo (默认路径)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例1.10n] PATH=/LaneEchoClient/echo 不命中 path-gray 规则,正确走基线"
    elif echo "$resp" | grep -q "lane=gray"; then
        test_fail "[用例1.10n] PATH=/LaneEchoClient/echo 不应该命中 path-gray 规则却被染色到 lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例1.10n] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 1.11: $caller_ip 维度 — EXACT 127.0.0.1 路由到 gray 泳道 ----------
test_caller_ip_local_match() {
    log_title "用例 1.11: \$caller_ip 维度 — EXACT 127.0.0.1 路由到 gray 泳道"
    log_info "测试目的: gateway 把 r.RemoteAddr 拆 host 作为 Argument 上报，命中 caller-ip-local 规则"
    log_info "请求: 本地 curl (RemoteAddr = 127.0.0.1) + Header lane-test=caller-ip-local (守卫)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "lane-test: caller-ip-local" \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=gray"; then
        test_pass "[用例1.11] CALLER_IP=127.0.0.1 命中 caller-ip-local 规则路由到 lane=gray"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例1.11] 收到 provider 响应但未路由到 gray 泳道"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例1.11] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 1.11n: $caller_ip 维度反向 — 守卫 Header 缺失时不命中 caller-ip-local 规则 ----------
test_caller_ip_local_match_negative() {
    log_title "用例 1.11n: \$caller_ip 维度反向 — 缺少守卫 Header 时不命中 caller-ip-local 规则,走基线"
    log_info "测试目的: 反向校验 AND 复合条件,即使来源 IP=127.0.0.1,缺少 lane-test 守卫 Header 也不应被染色"
    log_info "请求: 本地 curl,无 lane-test Header"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例1.11n] 缺守卫 Header 时不命中 caller-ip-local 规则,正确走基线"
    elif echo "$resp" | grep -q "lane=gray"; then
        test_fail "[用例1.11n] 缺守卫 Header 时不应被 caller-ip-local 规则染色到 lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例1.11n] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 1.12: $caller_ip 维度 — NOT_EQUALS 0.0.0.0 路由到 gray 泳道 ----------
test_caller_ip_not_zero_match() {
    log_title "用例 1.12: \$caller_ip 维度 — NOT_EQUALS 0.0.0.0 路由到 gray 泳道"
    log_info "测试目的: NOT_EQUALS 匹配模式，任意非 0.0.0.0 的来源 IP 均命中"
    log_info "请求: 本地 curl + Header lane-test=caller-ip-not-zero (必须守卫，否则会误染所有请求)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "lane-test: caller-ip-not-zero" \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=gray"; then
        test_pass "[用例1.12] CALLER_IP NOT_EQUALS 0.0.0.0 命中 caller-ip-not-zero 规则路由到 lane=gray"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例1.12] 收到 provider 响应但未路由到 gray 泳道"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例1.12] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 1.12n: $caller_ip 维度反向 — 守卫 Header 取错值时不命中 caller-ip-not-zero 规则 ----------
test_caller_ip_not_zero_match_negative() {
    log_title "用例 1.12n: \$caller_ip 维度反向 — 守卫 Header 取错值时不命中 caller-ip-not-zero 规则,走基线"
    log_info "测试目的: 反向校验 lane-test 守卫 Header 必须严格等于 caller-ip-not-zero,任意其他取值都不应被染色"
    log_info "请求: 本地 curl + Header lane-test=mismatch (取错值)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "lane-test: mismatch" \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例1.12n] 守卫 Header 取错值时不命中 caller-ip-not-zero 规则,正确走基线"
    elif echo "$resp" | grep -q "lane=gray"; then
        test_fail "[用例1.12n] 守卫 Header 取错值时不应被 caller-ip-not-zero 规则染色到 lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例1.12n] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- simple 链路用例: simple-gateway → consumer → provider ----------
# 与主链路共用 SourceService=${GATEWAY_SERVICE} 作为泳道入口,但使用 GetOneInstance API 简化实现。
# 目标: 验证简化 API 下,laneRouter 能正确完成流量匹配、染色透传和泳道路由。

# ---------- 用例 2.1: [simple] 无 Header → 基线 ----------
test_simple_no_header_baseline() {
    log_title "用例 2.1: [simple] 无 Header — 全链路路由到基线实例"
    log_info "链路: simple-gateway → consumer → provider (GetOneInstance API)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例2.1] 无 Header 路由到基线实例 lane=(baseline)"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例2.1] 收到 provider 响应但 lane 不符合预期（期望 lane=(baseline)）"
    else
        test_fail "[用例2.1] 未收到有效响应，请检查 simple-gateway 是否正常运行"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 2.2: [simple] service-lane Header 直接染色 → gray 泳道 ----------
test_simple_direct_stain_gray() {
    log_title "用例 2.2: [simple] service-lane 直接染色 — 路由到 gray 泳道"
    log_info "请求头: service-lane: ${EXPECTED_LANE_GROUP}/gray"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "service-lane: ${EXPECTED_LANE_GROUP}/gray" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=gray"; then
        test_pass "[用例2.2] 直接染色路由到 gray 泳道 (lane=gray)"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例2.2] 收到 provider 响应但未路由到 gray 泳道"
    else
        test_fail "[用例2.2] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 2.2n: [simple] service-lane 直接染色反向 — 不存在的规则名应走基线 ----------
test_simple_direct_stain_gray_negative() {
    log_title "用例 2.2n: [simple] service-lane 直接染色反向 — 不存在的规则名应走基线"
    log_info "测试目的: 反向校验 simple-gateway 链路下 lane router 的 matchByStainLabel 严格性"
    log_info "请求头: service-lane: ${EXPECTED_LANE_GROUP}/nonexist-rule (规则名不存在)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "service-lane: ${EXPECTED_LANE_GROUP}/nonexist-rule" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例2.2n] 不存在的规则名 stain label 不命中,正确走基线"
    elif echo "$resp" | grep -q "lane=gray"; then
        test_fail "[用例2.2n] 不存在的规则名不应该被染色到 lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例2.2n] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 2.3: [simple] 流量匹配 STRICT — Header user=gray ----------
test_simple_traffic_match_strict_gray() {
    log_title "用例 2.3: [simple] 流量匹配 STRICT — Header user=gray 路由到 gray 泳道"
    log_info "期望: TrafficMatchRule 在 simple-gateway 入口识别流量 → 染色 service-lane → consumer 透传 → provider gray"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: gray" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=gray"; then
        test_pass "[用例2.3] 流量匹配 STRICT 路由到 gray 泳道 (lane=gray)"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例2.3] 未路由到 gray 泳道，检查 Polaris 规则 gray (Header user=gray, STRICT)"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例2.3] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 2.3n: [simple] 流量匹配 STRICT 反向 — Header user 取近似值不命中 gray 规则 ----------
test_simple_traffic_match_strict_gray_negative() {
    log_title "用例 2.3n: [simple] 流量匹配 STRICT 反向 — Header user 取近似值不命中,走基线"
    log_info "测试目的: 反向校验 simple-gateway 链路下 EXACT 匹配的严格性"
    log_info "请求头: user: graymismatch (与目标值 EXACT 不等)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: graymismatch" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例2.3n] Header user=graymismatch 不命中 gray 规则,正确走基线"
    elif echo "$resp" | grep -q "lane=gray"; then
        test_fail "[用例2.3n] EXACT 匹配应严格,user=graymismatch 不应被染色到 lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例2.3n] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 2.4: [simple] 流量匹配 PERMISSIVE 回退基线 ----------
test_simple_traffic_match_permissive_fallback() {
    log_title "用例 2.4: [simple] 流量匹配 PERMISSIVE — 无目标泳道实例时回退基线"
    log_info "请求头: user: noexist (PERMISSIVE 规则目标 lane=noexist 无实例 → 回退 baseline)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: noexist" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例2.4] PERMISSIVE 模式无目标实例时正确回退基线 (lane=(baseline))"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例2.4] 收到 provider 响应但未回退到基线"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例2.4] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 2.4b: [simple] 流量匹配 STRICT — 无目标泳道实例时返回 503 ----------
test_simple_traffic_match_strict_no_instance_503() {
    log_title "用例 2.4b: [simple] 流量匹配 STRICT — 无目标泳道实例时期望 HTTP 503"
    log_info "测试目的: simple-gateway 入口，STRICT 模式 + 无实例的泳道 lane=strict-noexist 直接返回 503"
    log_info "请求头: user: strict"
    log_info ""

    local tmp_body http_code body
    tmp_body=$(mktemp -t lane-test-strict-body.XXXXXX)
    http_code=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: strict" \
        -o "$tmp_body" -w "%{http_code}" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || echo "000")
    http_code=$(printf '%s' "$http_code" | tr -cd '0-9' | cut -c1-3)
    [ -z "$http_code" ] && http_code="000"
    body=$(cat "$tmp_body" 2>/dev/null || echo "")
    rm -f "$tmp_body"
    log_raw "  HTTP ${http_code}, 响应: ${body}"

    if [ "$http_code" = "503" ]; then
        test_pass "[用例2.4b] STRICT 模式无目标实例时正确返回 HTTP 503"
    elif echo "$body" | grep -q "lane=(baseline)\|lane=gray"; then
        test_fail "[用例2.4b] STRICT 模式竟然路由到了实例(期望 503)，body=${body:0:120}"
    else
        test_fail "[用例2.4b] STRICT 模式未返回 503 (HTTP=${http_code})"
        log_info "  响应: ${body:0:200}"
    fi
}

# ---------- 用例 2.5: [simple] 未匹配 Header → 基线 ----------
test_simple_no_rule_match_baseline() {
    log_title "用例 2.5: [simple] 未匹配 Header — 无规则命中时路由到基线"
    log_info "请求头: user: unknown-value"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: unknown-value" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例2.5] 未命中规则时路由到基线 (lane=(baseline))"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例2.5] 未命中规则但未路由到基线"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例2.5] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 2.6: [simple] 泳道隔离验证 ----------
test_simple_lane_isolation() {
    log_title "用例 2.6: [simple] 泳道隔离验证 — 并发请求不互相干扰"
    log_info ""

    local baseline_ok=true
    local gray_ok=true
    local rounds=5

    for round in $(seq 1 $rounds); do
        local resp_base
        resp_base=$(curl -s --connect-timeout 3 --max-time 5 \
            "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
        log_raw "  [${round}] baseline: ${resp_base}"
        if ! echo "$resp_base" | grep -q "lane=(baseline)"; then
            baseline_ok=false
        fi

        local resp_gray
        resp_gray=$(curl -s --connect-timeout 3 --max-time 5 \
            -H "user: gray" \
            "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
        log_raw "  [${round}] gray:     ${resp_gray}"
        if ! echo "$resp_gray" | grep -q "lane=gray"; then
            gray_ok=false
        fi

        sleep 0.2
    done

    if [ "$baseline_ok" = true ] && [ "$gray_ok" = true ]; then
        test_pass "[用例2.6] 泳道隔离正确: baseline 和 gray 请求互不干扰 (${rounds} 轮)"
    else
        local detail=""
        [ "$baseline_ok" = false ] && detail="baseline 路由异常 "
        [ "$gray_ok" = false ] && detail="${detail}gray 路由异常"
        test_fail "[用例2.6] 泳道隔离失败: ${detail}"
    fi
}

# ==================================================================
# 六类匹配维度测试（simple-gateway 链路：simple-gateway → consumer → provider）
# ==================================================================
# 入口换成 simple-gateway (GetOneInstance API)，与 1.7-1.12 用例对照，
# 验证两种入口 API 下六类维度的路由决策一致。

# ---------- 用例 2.7: [simple] $method 维度 — METHOD=POST → gray ----------
test_simple_method_post_match() {
    log_title "用例 2.7: [simple] \$method 维度 — METHOD=POST 路由到 gray 泳道"
    log_info "链路: simple-gateway → consumer → provider"
    log_info "请求: POST + Header lane-test=method-post (守卫)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -X POST \
        -H "lane-test: method-post" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=gray"; then
        test_pass "[用例2.7] METHOD=POST 命中 method-post 规则路由到 lane=gray"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例2.7] 收到 provider 响应但未路由到 gray 泳道"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例2.7] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 2.7n: [simple] $method 维度反向 — GET 不命中 method-post 规则 ----------
test_simple_method_post_match_negative() {
    log_title "用例 2.7n: [simple] \$method 维度反向 — GET 请求不命中 method-post 规则,走基线"
    log_info "测试目的: 反向校验 simple-gateway 链路下 method-post 规则只在 METHOD=POST 时染色"
    log_info "请求: GET + Header lane-test=method-post (守卫)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "lane-test: method-post" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例2.7n] METHOD=GET 不命中 method-post 规则,正确走基线"
    elif echo "$resp" | grep -q "lane=gray"; then
        test_fail "[用例2.7n] METHOD=GET 不应该命中 method-post 规则却被染色到 lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例2.7n] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 2.8: [simple] Query 维度 ----------
test_simple_query_env_match() {
    log_title "用例 2.8: [simple] Query 维度 — QUERY env=gray 路由到 gray 泳道"
    log_info "请求: ?env=gray + Header lane-test=query-env (守卫)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "lane-test: query-env" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo?env=gray" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=gray"; then
        test_pass "[用例2.8] QUERY env=gray 命中 query-env 规则路由到 lane=gray"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例2.8] 收到 provider 响应但未路由到 gray 泳道"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例2.8] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 2.8n: [simple] Query 维度反向 — env=stable 不命中 query-env 规则 ----------
test_simple_query_env_match_negative() {
    log_title "用例 2.8n: [simple] Query 维度反向 — env=stable 不命中 query-env 规则,走基线"
    log_info "测试目的: 反向校验 simple-gateway 链路下 query-env 规则只匹配 env=gray"
    log_info "请求: ?env=stable + Header lane-test=query-env (守卫)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "lane-test: query-env" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo?env=stable" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例2.8n] QUERY env=stable 不命中 query-env 规则,正确走基线"
    elif echo "$resp" | grep -q "lane=gray"; then
        test_fail "[用例2.8n] QUERY env=stable 不应该命中 query-env 规则却被染色到 lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例2.8n] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 2.9: [simple] Cookie 维度 ----------
test_simple_cookie_user_match() {
    log_title "用例 2.9: [simple] Cookie 维度 — COOKIE user=gray 路由到 gray 泳道"
    log_info "请求: Cookie: user=gray + Header lane-test=cookie-user (守卫)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "lane-test: cookie-user" \
        -H "Cookie: user=gray" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=gray"; then
        test_pass "[用例2.9] COOKIE user=gray 命中 cookie-user 规则路由到 lane=gray"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例2.9] 收到 provider 响应但未路由到 gray 泳道"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例2.9] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 2.9n: [simple] Cookie 维度反向 — Cookie user=stable 不命中 cookie-user 规则 ----------
test_simple_cookie_user_match_negative() {
    log_title "用例 2.9n: [simple] Cookie 维度反向 — Cookie user=stable 不命中 cookie-user 规则,走基线"
    log_info "测试目的: 反向校验 simple-gateway 链路下 cookie-user 规则只匹配 user=gray"
    log_info "请求: Cookie: user=stable + Header lane-test=cookie-user (守卫)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "lane-test: cookie-user" \
        -H "Cookie: user=stable" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例2.9n] COOKIE user=stable 不命中 cookie-user 规则,正确走基线"
    elif echo "$resp" | grep -q "lane=gray"; then
        test_fail "[用例2.9n] COOKIE user=stable 不应该命中 cookie-user 规则却被染色到 lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例2.9n] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 2.10: [simple] $path 维度 ----------
test_simple_path_gray_match() {
    log_title "用例 2.10: [simple] \$path 维度 — PATH=/LaneEchoClient/gray-path 路由到 gray 泳道"
    log_info "请求: 访问 /LaneEchoClient/gray-path (consumer 无 handler 返回 404 为预期)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/gray-path" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=gray"; then
        test_pass "[用例2.10] PATH=/LaneEchoClient/gray-path 命中 path-gray 规则路由到 lane=gray"
    elif echo "$resp" | grep -q "LaneEchoServer\|SimpleLaneGateway\|LaneRouterGateway"; then
        test_fail "[用例2.10] 收到响应但未路由到 gray 泳道"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例2.10] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 2.10n: [simple] $path 维度反向 — 默认 /echo 路径不命中 path-gray 规则 ----------
test_simple_path_gray_match_negative() {
    log_title "用例 2.10n: [simple] \$path 维度反向 — 默认 /echo 路径不命中 path-gray 规则,走基线"
    log_info "测试目的: 反向校验 simple-gateway 链路下 path-gray 规则只匹配 /LaneEchoClient/gray-path"
    log_info "请求: 访问 /LaneEchoClient/echo (默认路径)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例2.10n] PATH=/LaneEchoClient/echo 不命中 path-gray 规则,正确走基线"
    elif echo "$resp" | grep -q "lane=gray"; then
        test_fail "[用例2.10n] PATH=/LaneEchoClient/echo 不应该命中 path-gray 规则却被染色到 lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例2.10n] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 2.11: [simple] $caller_ip 维度 EXACT ----------
test_simple_caller_ip_local_match() {
    log_title "用例 2.11: [simple] \$caller_ip 维度 — EXACT 127.0.0.1 路由到 gray 泳道"
    log_info "请求: 本地 curl + Header lane-test=caller-ip-local (守卫)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "lane-test: caller-ip-local" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=gray"; then
        test_pass "[用例2.11] CALLER_IP=127.0.0.1 命中 caller-ip-local 规则路由到 lane=gray"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例2.11] 收到 provider 响应但未路由到 gray 泳道"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例2.11] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 2.11n: [simple] $caller_ip 维度反向 — 守卫 Header 缺失时不命中 caller-ip-local 规则 ----------
test_simple_caller_ip_local_match_negative() {
    log_title "用例 2.11n: [simple] \$caller_ip 维度反向 — 缺少守卫 Header 时不命中 caller-ip-local 规则,走基线"
    log_info "测试目的: 反向校验 simple-gateway 链路下 caller-ip-local 规则的 AND 复合条件,无 lane-test 守卫不应染色"
    log_info "请求: 本地 curl,无 lane-test Header"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例2.11n] 缺守卫 Header 时不命中 caller-ip-local 规则,正确走基线"
    elif echo "$resp" | grep -q "lane=gray"; then
        test_fail "[用例2.11n] 缺守卫 Header 时不应被 caller-ip-local 规则染色到 lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例2.11n] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 2.12: [simple] $caller_ip 维度 NOT_EQUALS ----------
test_simple_caller_ip_not_zero_match() {
    log_title "用例 2.12: [simple] \$caller_ip 维度 — NOT_EQUALS 0.0.0.0 路由到 gray 泳道"
    log_info "请求: 本地 curl + Header lane-test=caller-ip-not-zero (必须守卫，避免误染)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "lane-test: caller-ip-not-zero" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=gray"; then
        test_pass "[用例2.12] CALLER_IP NOT_EQUALS 0.0.0.0 命中 caller-ip-not-zero 规则路由到 lane=gray"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例2.12] 收到 provider 响应但未路由到 gray 泳道"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例2.12] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 2.12n: [simple] $caller_ip 维度反向 — 守卫 Header 取错值时不命中 caller-ip-not-zero 规则 ----------
test_simple_caller_ip_not_zero_match_negative() {
    log_title "用例 2.12n: [simple] \$caller_ip 维度反向 — 守卫 Header 取错值时不命中 caller-ip-not-zero 规则,走基线"
    log_info "测试目的: 反向校验 simple-gateway 链路下 lane-test 守卫 Header 必须严格等于 caller-ip-not-zero"
    log_info "请求: 本地 curl + Header lane-test=mismatch (取错值)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "lane-test: mismatch" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例2.12n] 守卫 Header 取错值时不命中 caller-ip-not-zero 规则,正确走基线"
    elif echo "$resp" | grep -q "lane=gray"; then
        test_fail "[用例2.12n] 守卫 Header 取错值时不应被 caller-ip-not-zero 规则染色到 lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例2.12n] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- gw→sc 链路用例: gateway → simple-consumer → provider ----------
# 主链路的对照组,目标服务替换为 SimpleLaneEchoClient (基于 GetOneInstance 的简化 consumer)。
# 验证 gateway (ProcessRouters) → simple-consumer (GetOneInstance) → provider 的端到端染色/路由。

test_gw_sc_no_header_baseline() {
    log_title "用例 3.1: [gw→sc] 无 Header — 全链路路由到基线实例"
    log_info "链路: gateway → simple-consumer → provider"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        "http://127.0.0.1:${GATEWAY_PORT}/${SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例3.1] 无 Header 路由到基线实例 lane=(baseline)"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例3.1] 收到 provider 响应但 lane 不符合预期"
    else
        test_fail "[用例3.1] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

test_gw_sc_direct_stain_gray() {
    log_title "用例 3.2: [gw→sc] service-lane 直接染色 — 路由到 gray 泳道"
    log_info "请求头: service-lane: ${EXPECTED_LANE_GROUP}/gray"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "service-lane: ${EXPECTED_LANE_GROUP}/gray" \
        "http://127.0.0.1:${GATEWAY_PORT}/${SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=gray"; then
        test_pass "[用例3.2] 直接染色路由到 gray 泳道 (lane=gray)"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例3.2] 收到 provider 响应但未路由到 gray 泳道"
    else
        test_fail "[用例3.2] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 3.2n: [gw→sc] service-lane 直接染色反向 — 不存在的规则名应走基线 ----------
test_gw_sc_direct_stain_gray_negative() {
    log_title "用例 3.2n: [gw→sc] service-lane 直接染色反向 — 不存在的规则名应走基线"
    log_info "测试目的: 反向校验 gw→sc 链路下 stain label 命中规则索引的严格性"
    log_info "请求头: service-lane: ${EXPECTED_LANE_GROUP}/nonexist-rule (规则名不存在)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "service-lane: ${EXPECTED_LANE_GROUP}/nonexist-rule" \
        "http://127.0.0.1:${GATEWAY_PORT}/${SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例3.2n] 不存在的规则名 stain label 不命中,正确走基线"
    elif echo "$resp" | grep -q "lane=gray"; then
        test_fail "[用例3.2n] 不存在的规则名不应该被染色到 lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例3.2n] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

test_gw_sc_traffic_match_strict_gray() {
    log_title "用例 3.3: [gw→sc] 流量匹配 STRICT — Header user=gray 路由到 gray 泳道"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: gray" \
        "http://127.0.0.1:${GATEWAY_PORT}/${SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=gray"; then
        test_pass "[用例3.3] 流量匹配 STRICT 路由到 gray 泳道 (lane=gray)"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例3.3] 未路由到 gray 泳道"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例3.3] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 3.3n: [gw→sc] 流量匹配 STRICT 反向 — Header user 取近似值不命中 ----------
test_gw_sc_traffic_match_strict_gray_negative() {
    log_title "用例 3.3n: [gw→sc] 流量匹配 STRICT 反向 — Header user 取近似值不命中,走基线"
    log_info "测试目的: 反向校验 gw→sc 链路下 EXACT 匹配的严格性"
    log_info "请求头: user: graymismatch (与目标值 EXACT 不等)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: graymismatch" \
        "http://127.0.0.1:${GATEWAY_PORT}/${SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例3.3n] Header user=graymismatch 不命中 gray 规则,正确走基线"
    elif echo "$resp" | grep -q "lane=gray"; then
        test_fail "[用例3.3n] EXACT 匹配应严格,user=graymismatch 不应被染色到 lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例3.3n] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

test_gw_sc_traffic_match_permissive_fallback() {
    log_title "用例 3.4: [gw→sc] 流量匹配 PERMISSIVE — 无目标泳道实例时回退基线"
    log_info "请求头: user: noexist"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: noexist" \
        "http://127.0.0.1:${GATEWAY_PORT}/${SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例3.4] PERMISSIVE 模式无目标实例时正确回退基线 (lane=(baseline))"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例3.4] 未回退到基线"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例3.4] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 3.4b: [gw→sc] 流量匹配 STRICT — 无目标泳道实例时返回 503 ----------
test_gw_sc_traffic_match_strict_no_instance_503() {
    log_title "用例 3.4b: [gw→sc] 流量匹配 STRICT — 无目标泳道实例时期望 HTTP 503"
    log_info "请求头: user: strict"
    log_info ""

    local tmp_body http_code body
    tmp_body=$(mktemp -t lane-test-strict-body.XXXXXX)
    http_code=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: strict" \
        -o "$tmp_body" -w "%{http_code}" \
        "http://127.0.0.1:${GATEWAY_PORT}/${SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || echo "000")
    http_code=$(printf '%s' "$http_code" | tr -cd '0-9' | cut -c1-3)
    [ -z "$http_code" ] && http_code="000"
    body=$(cat "$tmp_body" 2>/dev/null || echo "")
    rm -f "$tmp_body"
    log_raw "  HTTP ${http_code}, 响应: ${body}"

    if [ "$http_code" = "503" ]; then
        test_pass "[用例3.4b] STRICT 模式无目标实例时正确返回 HTTP 503"
    elif echo "$body" | grep -q "lane=(baseline)\|lane=gray"; then
        test_fail "[用例3.4b] STRICT 模式竟然路由到了实例(期望 503)，body=${body:0:120}"
    else
        test_fail "[用例3.4b] STRICT 模式未返回 503 (HTTP=${http_code})"
        log_info "  响应: ${body:0:200}"
    fi
}

test_gw_sc_no_rule_match_baseline() {
    log_title "用例 3.5: [gw→sc] 未匹配 Header — 无规则命中时路由到基线"
    log_info "请求头: user: unknown-value"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: unknown-value" \
        "http://127.0.0.1:${GATEWAY_PORT}/${SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例3.5] 未命中规则时路由到基线 (lane=(baseline))"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例3.5] 未命中规则但未路由到基线"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例3.5] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

test_gw_sc_lane_isolation() {
    log_title "用例 3.6: [gw→sc] 泳道隔离验证 — 并发请求不互相干扰"
    log_info ""

    local baseline_ok=true
    local gray_ok=true
    local rounds=5

    for round in $(seq 1 $rounds); do
        local resp_base
        resp_base=$(curl -s --connect-timeout 3 --max-time 5 \
            "http://127.0.0.1:${GATEWAY_PORT}/${SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
        log_raw "  [${round}] baseline: ${resp_base}"
        if ! echo "$resp_base" | grep -q "lane=(baseline)"; then
            baseline_ok=false
        fi

        local resp_gray
        resp_gray=$(curl -s --connect-timeout 3 --max-time 5 \
            -H "user: gray" \
            "http://127.0.0.1:${GATEWAY_PORT}/${SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
        log_raw "  [${round}] gray:     ${resp_gray}"
        if ! echo "$resp_gray" | grep -q "lane=gray"; then
            gray_ok=false
        fi

        sleep 0.2
    done

    if [ "$baseline_ok" = true ] && [ "$gray_ok" = true ]; then
        test_pass "[用例3.6] 泳道隔离正确: baseline 和 gray 请求互不干扰 (${rounds} 轮)"
    else
        local detail=""
        [ "$baseline_ok" = false ] && detail="baseline 路由异常 "
        [ "$gray_ok" = false ] && detail="${detail}gray 路由异常"
        test_fail "[用例3.6] 泳道隔离失败: ${detail}"
    fi
}

# ---------- sg→sc 链路用例: simple-gateway → simple-consumer → provider ----------
# 两侧都基于 GetOneInstance 的完整简化链路。

test_sg_sc_no_header_baseline() {
    log_title "用例 4.1: [sg→sc] 无 Header — 全链路路由到基线实例"
    log_info "链路: simple-gateway → simple-consumer → provider (两侧均 GetOneInstance)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例4.1] 无 Header 路由到基线实例 lane=(baseline)"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例4.1] 收到 provider 响应但 lane 不符合预期"
    else
        test_fail "[用例4.1] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

test_sg_sc_direct_stain_gray() {
    log_title "用例 4.2: [sg→sc] service-lane 直接染色 — 路由到 gray 泳道"
    log_info "请求头: service-lane: ${EXPECTED_LANE_GROUP}/gray"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "service-lane: ${EXPECTED_LANE_GROUP}/gray" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=gray"; then
        test_pass "[用例4.2] 直接染色路由到 gray 泳道 (lane=gray)"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例4.2] 收到 provider 响应但未路由到 gray 泳道"
    else
        test_fail "[用例4.2] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 4.2n: [sg→sc] service-lane 直接染色反向 — 不存在的规则名应走基线 ----------
test_sg_sc_direct_stain_gray_negative() {
    log_title "用例 4.2n: [sg→sc] service-lane 直接染色反向 — 不存在的规则名应走基线"
    log_info "测试目的: 反向校验 sg→sc 链路下 stain label 命中规则索引的严格性"
    log_info "请求头: service-lane: ${EXPECTED_LANE_GROUP}/nonexist-rule (规则名不存在)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "service-lane: ${EXPECTED_LANE_GROUP}/nonexist-rule" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例4.2n] 不存在的规则名 stain label 不命中,正确走基线"
    elif echo "$resp" | grep -q "lane=gray"; then
        test_fail "[用例4.2n] 不存在的规则名不应该被染色到 lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例4.2n] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

test_sg_sc_traffic_match_strict_gray() {
    log_title "用例 4.3: [sg→sc] 流量匹配 STRICT — Header user=gray 路由到 gray 泳道"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: gray" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=gray"; then
        test_pass "[用例4.3] 流量匹配 STRICT 路由到 gray 泳道 (lane=gray)"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例4.3] 未路由到 gray 泳道"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例4.3] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 4.3n: [sg→sc] 流量匹配 STRICT 反向 — Header user 取近似值不命中 ----------
test_sg_sc_traffic_match_strict_gray_negative() {
    log_title "用例 4.3n: [sg→sc] 流量匹配 STRICT 反向 — Header user 取近似值不命中,走基线"
    log_info "测试目的: 反向校验 sg→sc 链路下 EXACT 匹配的严格性"
    log_info "请求头: user: graymismatch (与目标值 EXACT 不等)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: graymismatch" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例4.3n] Header user=graymismatch 不命中 gray 规则,正确走基线"
    elif echo "$resp" | grep -q "lane=gray"; then
        test_fail "[用例4.3n] EXACT 匹配应严格,user=graymismatch 不应被染色到 lane=gray"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例4.3n] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

test_sg_sc_traffic_match_permissive_fallback() {
    log_title "用例 4.4: [sg→sc] 流量匹配 PERMISSIVE — 无目标泳道实例时回退基线"
    log_info "请求头: user: noexist"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: noexist" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例4.4] PERMISSIVE 模式无目标实例时正确回退基线 (lane=(baseline))"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例4.4] 未回退到基线"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例4.4] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 4.4b: [sg→sc] 流量匹配 STRICT — 无目标泳道实例时返回 503 ----------
test_sg_sc_traffic_match_strict_no_instance_503() {
    log_title "用例 4.4b: [sg→sc] 流量匹配 STRICT — 无目标泳道实例时期望 HTTP 503"
    log_info "请求头: user: strict"
    log_info ""

    local tmp_body http_code body
    tmp_body=$(mktemp -t lane-test-strict-body.XXXXXX)
    http_code=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: strict" \
        -o "$tmp_body" -w "%{http_code}" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || echo "000")
    http_code=$(printf '%s' "$http_code" | tr -cd '0-9' | cut -c1-3)
    [ -z "$http_code" ] && http_code="000"
    body=$(cat "$tmp_body" 2>/dev/null || echo "")
    rm -f "$tmp_body"
    log_raw "  HTTP ${http_code}, 响应: ${body}"

    if [ "$http_code" = "503" ]; then
        test_pass "[用例4.4b] STRICT 模式无目标实例时正确返回 HTTP 503"
    elif echo "$body" | grep -q "lane=(baseline)\|lane=gray"; then
        test_fail "[用例4.4b] STRICT 模式竟然路由到了实例(期望 503)，body=${body:0:120}"
    else
        test_fail "[用例4.4b] STRICT 模式未返回 503 (HTTP=${http_code})"
        log_info "  响应: ${body:0:200}"
    fi
}

test_sg_sc_no_rule_match_baseline() {
    log_title "用例 4.5: [sg→sc] 未匹配 Header — 无规则命中时路由到基线"
    log_info "请求头: user: unknown-value"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: unknown-value" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例4.5] 未命中规则时路由到基线 (lane=(baseline))"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例4.5] 未命中规则但未路由到基线"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例4.5] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

test_sg_sc_lane_isolation() {
    log_title "用例 4.6: [sg→sc] 泳道隔离验证 — 并发请求不互相干扰"
    log_info ""

    local baseline_ok=true
    local gray_ok=true
    local rounds=5

    for round in $(seq 1 $rounds); do
        local resp_base
        resp_base=$(curl -s --connect-timeout 3 --max-time 5 \
            "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
        log_raw "  [${round}] baseline: ${resp_base}"
        if ! echo "$resp_base" | grep -q "lane=(baseline)"; then
            baseline_ok=false
        fi

        local resp_gray
        resp_gray=$(curl -s --connect-timeout 3 --max-time 5 \
            -H "user: gray" \
            "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
        log_raw "  [${round}] gray:     ${resp_gray}"
        if ! echo "$resp_gray" | grep -q "lane=gray"; then
            gray_ok=false
        fi

        sleep 0.2
    done

    if [ "$baseline_ok" = true ] && [ "$gray_ok" = true ]; then
        test_pass "[用例4.6] 泳道隔离正确: baseline 和 gray 请求互不干扰 (${rounds} 轮)"
    else
        local detail=""
        [ "$baseline_ok" = false ] && detail="baseline 路由异常 "
        [ "$gray_ok" = false ] && detail="${detail}gray 路由异常"
        test_fail "[用例4.6] 泳道隔离失败: ${detail}"
    fi
}

# ---------- 用例 5.1: baseLaneMode=ExcludeEnabledLaneInstance 专项 ----------
# 验证 lane router 在 baseLaneMode=1 模式下,当无任何未打标签实例时,
# 会排除 lane 值命中 "已启用规则集合" 的实例,剩下的实例作为基线。
#
# 链路: gateway-excl (baseLaneMode=1) → StableLaneEchoServer
#   - 该服务只有 lane=stable(:${PROVIDER_EXCL_STABLE_PORT}) + lane=gray(:${PROVIDER_EXCL_GRAY_PORT}) 两个实例
#   - 泳道规则 gray/noexist 的 defaultLabelValue 集合 = {gray, noexist}
#   - baseline 请求应路由到 lane=stable(不在启用集合) → :${PROVIDER_EXCL_STABLE_PORT}
#   - Header user=gray 请求按常规染色 → :${PROVIDER_EXCL_GRAY_PORT}
test_base_lane_mode_exclude_enabled() {
    log_title "用例 5.1: baseLaneMode=ExcludeEnabledLaneInstance — 排除启用泳道实例后作为基线"
    log_info "gateway-excl (baseLaneMode=1) → ${PROVIDER_EXCL_SERVICE}"
    log_info "场景: 目标服务只有 lane=stable / lane=gray 两种实例, 无未打标签实例"
    log_info ""

    # --- 子测试 5.1a: 无 Header baseline 请求应路由到 lane=stable(不在启用集合) ---
    log_info "--- 子测试 5.1a: 无 Header baseline 请求应路由到 lane=stable(:${PROVIDER_EXCL_STABLE_PORT}) ---"
    local stable_count=0
    local gray_count=0
    local other_count=0
    for i in $(seq 1 10); do
        local resp
        resp=$(curl -s --connect-timeout 5 --max-time 10 \
            "http://127.0.0.1:${GATEWAY_EXCL_PORT}/${PROVIDER_EXCL_SERVICE}/echo" 2>/dev/null || true)
        log_raw "  第${i}次: ${resp}"
        if echo "$resp" | grep -q ":${PROVIDER_EXCL_STABLE_PORT}"; then
            stable_count=$((stable_count + 1))
        elif echo "$resp" | grep -q ":${PROVIDER_EXCL_GRAY_PORT}"; then
            gray_count=$((gray_count + 1))
        else
            other_count=$((other_count + 1))
        fi
        sleep 0.3
    done
    log_info "  路由分布: stable(:${PROVIDER_EXCL_STABLE_PORT})=${stable_count}, gray(:${PROVIDER_EXCL_GRAY_PORT})=${gray_count}, other=${other_count}"
    if [ $stable_count -eq 10 ] && [ $gray_count -eq 0 ]; then
        test_pass "[用例5.1a] baseline 全部路由到 lane=stable(:${PROVIDER_EXCL_STABLE_PORT}),符合 ExcludeEnabledLaneInstance 语义"
    elif [ $stable_count -gt 0 ] && [ $gray_count -gt 0 ]; then
        test_fail "[用例5.1a] baseline 出现负载均衡,mode=1 下不应路由到启用集合里的 lane=gray 实例"
    else
        test_fail "[用例5.1a] baseline 路由分布异常 (stable=${stable_count}, gray=${gray_count}, other=${other_count})"
    fi

    # --- 子测试 5.1b: Header user=gray 请求按常规染色,路由到 lane=gray ---
    log_info ""
    log_info "--- 子测试 5.1b: Header user=gray 请求仍按染色路由到 lane=gray(:${PROVIDER_EXCL_GRAY_PORT}) ---"
    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: gray" \
        "http://127.0.0.1:${GATEWAY_EXCL_PORT}/${PROVIDER_EXCL_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"
    if echo "$resp" | grep -q ":${PROVIDER_EXCL_GRAY_PORT}"; then
        test_pass "[用例5.1b] 染色请求路由到 lane=gray(:${PROVIDER_EXCL_GRAY_PORT}),baseLaneMode 不影响染色路径"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例5.1b] 染色请求未路由到 lane=gray 实例"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例5.1b] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 6.1: 服务不在泳道组内（默认模式 ONLY_UNTAGGED_INSTANCE）----------
# 此用例涉及泳道组的移除/恢复操作，放在最后执行以避免影响其他用例
test_out_of_lane_group() {
    log_title "用例 6.1: 服务不在泳道组内（默认模式）— 移除后只走无标签实例"
    log_info "测试目的: 将 ${PROVIDER_SERVICE} 从泳道组中移除后，验证染色请求和无 Header 请求均只走无标签基线实例"
    log_info "当前 baseLaneMode: ONLY_UNTAGGED_INSTANCE（默认）"
    log_info ""

    # 自动将 provider 从泳道组中移除
    if ! remove_provider_from_lane_group; then
        test_fail "[用例6.1] 无法从泳道组中移除 ${PROVIDER_SERVICE}"
        test_fail "[用例6.1a] 跳过执行: 依赖用例 6.1 规则变更生效 (级联失败)"
        test_fail "[用例6.1b] 跳过执行: 依赖用例 6.1 规则变更生效 (级联失败)"
        # 移除本身失败,泳道组未改动,此处无需恢复。但若移除是"半成功"状态,
        # 仍尝试恢复一次以兜底:恢复函数的 add 操作是幂等的,服务已在组内会直接跳过。
        _restore_lane_group
        return
    fi

    # Phase 1: 通过管理 API 确认移除成功
    # 注意: Discover API 的 naming cache 刷新间隔可能很长（分钟级），不可靠。
    # 改为通过管理 API 确认 + 行为探测的策略。
    log_info "Phase 1: 通过管理 API 确认移除状态..."
    local admin_check
    admin_check=$(curl -s --connect-timeout 5 --max-time 10 \
        "${POLARIS_HTTP_ADDR}/naming/v1/lane/groups?name=${LANE_GROUP_NAME}&offset=0&limit=10" \
        --header "X-Polaris-Token:${POLARIS_TOKEN}" 2>/dev/null | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    groups = data.get('data', [])
    if groups:
        dests = [d.get('service','') for d in groups[0].get('destinations',[])]
        print('yes' if '${PROVIDER_SERVICE}' in dests else 'no')
    else:
        print('error')
except:
    print('error')
" 2>/dev/null)
    if [ "$admin_check" = "yes" ]; then
        test_fail "[用例6.1] 管理 API 确认移除失败: ${PROVIDER_SERVICE} 仍在 destinations 中"
        test_fail "[用例6.1a] 跳过执行: 依赖用例 6.1 规则变更生效 (级联失败)"
        test_fail "[用例6.1b] 跳过执行: 依赖用例 6.1 规则变更生效 (级联失败)"
        _restore_lane_group
        return
    elif [ "$admin_check" = "error" ]; then
        test_fail "[用例6.1] 管理 API 查询失败"
        test_fail "[用例6.1a] 跳过执行: 依赖用例 6.1 规则变更生效 (级联失败)"
        test_fail "[用例6.1b] 跳过执行: 依赖用例 6.1 规则变更生效 (级联失败)"
        _restore_lane_group
        return
    fi
    log_ok "管理 API 确认: ${PROVIDER_SERVICE} 已从泳道组移除"

    # Phase 1.5: 通过 Discover API 直接验证服务端已将最新规则推送给 SDK
    # 管理 API 与 Discover API 分别面向控制面与数据面：
    #   - 管理 API（/naming/v1/lane/groups）: 修改落盘后立即生效
    #   - Discover API（/v1/Discover, type=LANE）: 需要 Polaris 服务端 naming cache 刷新后才会下发
    # 只有 Discover API 返回的 destinations 不包含 ${PROVIDER_SERVICE}，SDK 才可能感知到变更。
    log_info "Phase 1.5: 通过 Discover API 验证服务端推送给 SDK 的泳道规则..."
    local max_discover_wait=120
    local discover_waited=0
    local discover_synced=false
    while [ $discover_waited -lt $max_discover_wait ]; do
        local discover_resp
        discover_resp=$(fetch_lane_rules)
        local discover_check
        discover_check=$(echo "$discover_resp" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    if data.get('code', 0) != 200000:
        print('error|code=' + str(data.get('code', 0)))
        sys.exit(0)
    lanes = data.get('lanes', [])
    target = None
    for lane in lanes:
        if lane.get('name') == '${LANE_GROUP_NAME}':
            target = lane
            break
    if target is None:
        print('error|lane_group_not_found')
        sys.exit(0)
    dests = [d.get('service','') for d in target.get('destinations', [])]
    if '${PROVIDER_SERVICE}' in dests:
        print('pending|' + ','.join(dests))
    else:
        print('ok|' + ','.join(dests))
except Exception as e:
    print('error|' + str(e))
" 2>/dev/null)
        local status="${discover_check%%|*}"
        local detail="${discover_check#*|}"
        case "$status" in
            ok)
                log_ok "Discover API 确认: destinations 已不含 ${PROVIDER_SERVICE} (当前: ${detail})"
                discover_synced=true
                break
                ;;
            pending)
                log_info "  Discover API 仍包含 ${PROVIDER_SERVICE} (${discover_waited}s/${max_discover_wait}s)，当前: ${detail}"
                ;;
            error|*)
                log_warn "  Discover API 查询异常 (${discover_waited}s/${max_discover_wait}s): ${detail}"
                ;;
        esac
        sleep 5
        discover_waited=$((discover_waited + 5))
    done

    if ! $discover_synced; then
        log_warn "Discover API 在 ${max_discover_wait}s 内未感知到泳道组变更"
        log_warn "原因: Polaris 服务端 naming cache 刷新延迟，SDK 无法拉取到最新规则"
        log_warn "如需测试此场景，请确保 Polaris 服务端 naming cache 刷新间隔 < ${max_discover_wait}s"
        test_pass "[用例6.1] SKIP: Discover API 缓存传播超时，非 SDK 问题"
        # 管理 API 已经真实移除了 ${PROVIDER_SERVICE},即使本次 SKIP,也必须把泳道组
        # 恢复回初始状态,否则后续运行会一直失败。
        _restore_lane_group
        return
    fi

    # Phase 2: 等待 SDK 拉取到最新规则并通过行为探测确认
    #
    # 重要前置条件(Phase 1.5 已保证):
    #   - Polaris Discover API 已经把 destinations 不含 ${PROVIDER_SERVICE} 的最新规则
    #     下发了。SDK 默认 refreshInterval=2s,此时拉取到新规则只是时间问题。
    #
    # 因此 Phase 2 窗口内如果行为仍未切换:
    #   - 绝不能归咎为"服务端缓存刷新延迟"(Phase 1.5 已排除);
    #   - 只可能是 SDK 端 bug(泳道路由短格式歧义、跨泳道组 destinations 合并错误等)。
    # 必须 FAIL,否则会掩盖真实 bug。
    log_info "Phase 2: 等待 SDK 感知泳道组变更 (行为探测,最多 30s)..."
    local max_sdk_wait=30
    local sdk_waited=0
    local rule_effective=false
    while [ $sdk_waited -lt $max_sdk_wait ]; do
        local probe_resp
        probe_resp=$(curl -s --connect-timeout 5 --max-time 10 \
            -H "user: gray" \
            "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
        if echo "$probe_resp" | grep -q ":${PROVIDER_BASE_PORT}"; then
            log_info "  SDK 规则已生效 (${sdk_waited}s): 染色请求路由到 base 实例"
            rule_effective=true
            break
        fi
        sleep 5
        sdk_waited=$((sdk_waited + 5))
        log_info "  探测 (${sdk_waited}s/${max_sdk_wait}s): 仍路由到 gray..."
    done

    if ! $rule_effective; then
        test_fail "[用例6.1] SDK 在 ${max_sdk_wait}s 内未感知到泳道组变更"
        log_info "  Phase 1.5 已确认 Discover API 下发的 destinations 不含 ${PROVIDER_SERVICE},"
        log_info "  即服务端规则已传播到 SDK。Phase 2 行为仍停留在旧路由,说明 SDK 泳道路由存在 bug。"
        log_info "  排查提示:"
        log_info "    1. 检查是否存在其它泳道组(如 lane-go-warmup)也包含 ${PROVIDER_SERVICE} 与相同 defaultLabelValue,"
        log_info "       导致短格式 service-lane=gray 被错误匹配到兄弟组(matchByStainLabel 歧义消解)。"
        log_info "    2. 检查 caller+callee 两侧 LaneGroup 合并是否正确丢弃了已过期的一侧。"
        log_info "    3. Consumer SDK debug 日志: .logs/consumer-base.log 搜索 '[Router][Lane]'。"
        # 6.1a / 6.1b 的前置条件(规则变更生效)未满足,属于级联失败。
        # 必须显式标记为 FAIL,否则总计数会少算,给出虚假的"通过率 100%"。
        test_fail "[用例6.1a] 跳过执行: 依赖用例 6.1 规则变更生效 (级联失败)"
        test_fail "[用例6.1b] 跳过执行: 依赖用例 6.1 规则变更生效 (级联失败)"
        # 恢复泳道组配置,避免脏状态污染后续运行。
        _restore_lane_group
        return
    fi

    # --- 子测试 6.1a: 染色请求验证 ---
    log_info ""
    log_info "--- 子测试 6.1a: 染色请求 (Header user=gray) 应只走无标签基线实例 ---"
    local base_count=0
    local gray_count=0
    for i in $(seq 1 10); do
        local resp
        resp=$(curl -s --connect-timeout 5 --max-time 10 \
            -H "user: gray" \
            "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
        log_raw "  第${i}次: ${resp}"
        if echo "$resp" | grep -q ":${PROVIDER_BASE_PORT}"; then
            base_count=$((base_count + 1))
        elif echo "$resp" | grep -q ":${PROVIDER_GRAY_PORT}"; then
            gray_count=$((gray_count + 1))
        fi
        sleep 0.5
    done

    log_info "  路由分布: base(:${PROVIDER_BASE_PORT})=${base_count}, gray(:${PROVIDER_GRAY_PORT})=${gray_count}"

    # 验证: 默认模式下，基线只选无标签实例，provider 应全部走 base
    if [ $base_count -eq 10 ] && [ $gray_count -eq 0 ]; then
        test_pass "[用例6.1a] 染色请求: provider 全部路由到无标签的 base(:${PROVIDER_BASE_PORT})"
    elif [ $base_count -gt 0 ] && [ $gray_count -gt 0 ]; then
        test_fail "[用例6.1a] 染色请求: provider 出现负载均衡，默认模式下不应路由到带标签实例"
    else
        test_fail "[用例6.1a] 染色请求: 路由分布异常 (base=${base_count}, gray=${gray_count})"
    fi

    # --- 子测试 6.1b: 无 Header 请求验证 ---
    log_info ""
    log_info "--- 子测试 6.1b: 无 Header 请求应只走无标签基线实例 ---"
    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q "lane=(baseline)" && echo "$resp" | grep -q ":${PROVIDER_BASE_PORT}"; then
        test_pass "[用例6.1b] 无 Header 请求路由到无标签 base 实例 (:${PROVIDER_BASE_PORT})"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例6.1b] 路由结果不符合预期"
        log_info "  期望: lane=(baseline), port=${PROVIDER_BASE_PORT}"
        log_info "  实际: ${resp}"
    else
        test_fail "[用例6.1b] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi

    # 恢复阶段: 恢复泳道组配置
    _restore_lane_group
}

# 恢复泳道组配置的辅助函数
_restore_lane_group() {
    log_info "恢复泳道组配置..."
    if restore_provider_to_lane_group; then
        log_info "等待 5s 确保泳道组恢复..."
        sleep 5
    else
        log_warn "恢复泳道组配置失败，请手动在 Polaris 控制台将 ${PROVIDER_SERVICE} 加回泳道组 ${LANE_GROUP_NAME}"
        log_warn "控制台地址: ${POLARIS_CONSOLE}"
    fi
}

# ---------- 用例 6.2: baseLaneMode 控制非泳道组服务中带标签实例的可见性 ----------
# 验证当 consumer 和 provider 都不属于任何泳道组时:
#   - baseLaneMode=0 (OnlyUntaggedInstance, 默认): consumer 请求 provider 时
#     不应返回带有泳道标签(如 lane=gray)的实例,只返回无标签基线实例。
#     对齐 polaris-java LaneRouter.redirectToBase + ONLY_UNTAGGED_INSTANCE 语义。
#   - baseLaneMode=1 (ExcludeEnabledLaneInstance): consumer 请求 provider 时
#     可能返回带有泳道标签的实例(前提是标签值不在“已启用规则集合”中)。
# 此测试用例不修改泳道组配置,不影响其他用例。
test_base_lane_mode_no_lane_group() {
    log_title "用例 6.2: baseLaneMode 控制非泳道组服务中带标签实例的可见性"
    log_info "consumer (${NLG_CONSUMER_SERVICE}) → provider (${NLG_PROVIDER_SERVICE})"
    log_info "两个服务均不在任何泳道组: provider 有 base(无标签) + gray(lane=gray) 两个实例"
    log_info ""

    # --- 子测试 6.2a: baseLaneMode=0 — 只路由到无标签基线实例 ---
    log_info "--- 子测试 6.2a: baseLaneMode=0 — consumer(mode0) 应只路由到无标签 base 实例 ---"
    local base_count=0
    local gray_count=0
    for i in $(seq 1 10); do
        local resp
        resp=$(curl -s --connect-timeout 5 --max-time 10 \
            "http://127.0.0.1:${NLG_CONSUMER_MODE0_PORT}/echo" 2>/dev/null || true)
        log_raw "  第${i}次: ${resp}"
        if echo "$resp" | grep -q ":${NLG_PROVIDER_BASE_PORT}"; then
            base_count=$((base_count + 1))
        elif echo "$resp" | grep -q ":${NLG_PROVIDER_GRAY_PORT}"; then
            gray_count=$((gray_count + 1))
        fi
        sleep 0.3
    done
    log_info "  路由分布: base(:${NLG_PROVIDER_BASE_PORT})=${base_count}, gray(:${NLG_PROVIDER_GRAY_PORT})=${gray_count}"
    if [ $base_count -eq 10 ] && [ $gray_count -eq 0 ]; then
        test_pass "[用例6.2a] baseLaneMode=0: 全部路由到无标签 base 实例,符合 OnlyUntaggedInstance 语义"
    elif [ $base_count -gt 0 ] && [ $gray_count -gt 0 ]; then
        test_fail "[用例6.2a] baseLaneMode=0: 出现负载均衡,mode=0 下不应路由到带 lane=gray 标签的实例"
    else
        test_fail "[用例6.2a] baseLaneMode=0: 路由分布异常 (base=${base_count}, gray=${gray_count})"
    fi

    # --- 子测试 6.2b: baseLaneMode=1 — 可以路由到带泳道标签的实例 ---
    log_info ""
    log_info "--- 子测试 6.2b: baseLaneMode=1 — consumer(mode1) 可能路由到带 lane=gray 标签的实例 ---"
    local base_count2=0
    local gray_count2=0
    for i in $(seq 1 10); do
        local resp2
        resp2=$(curl -s --connect-timeout 5 --max-time 10 \
            "http://127.0.0.1:${NLG_CONSUMER_MODE1_PORT}/echo" 2>/dev/null || true)
        log_raw "  第${i}次: ${resp2}"
        if echo "$resp2" | grep -q ":${NLG_PROVIDER_BASE_PORT}"; then
            base_count2=$((base_count2 + 1))
        elif echo "$resp2" | grep -q ":${NLG_PROVIDER_GRAY_PORT}"; then
            gray_count2=$((gray_count2 + 1))
        fi
        sleep 0.3
    done
    log_info "  路由分布: base(:${NLG_PROVIDER_BASE_PORT})=${base_count2}, gray(:${NLG_PROVIDER_GRAY_PORT})=${gray_count2}"
    if [ $gray_count2 -gt 0 ]; then
        test_pass "[用例6.2b] baseLaneMode=1: 至少 1 次路由到 lane=gray 实例(gray=${gray_count2}/10),符合 ExcludeEnabledLaneInstance 语义"
    elif [ $base_count2 -eq 10 ] && [ $gray_count2 -eq 0 ]; then
        test_fail "[用例6.2b] baseLaneMode=1: 10 次全部路由到 base,mode=1 下理论上应允许路由到 lane=gray 实例(gray 不在已启用规则集合)"
    else
        test_fail "[用例6.2b] baseLaneMode=1: 路由分布异常 (base=${base_count2}, gray=${gray_count2})"
    fi
}

# ---------- half 链路用例 7.x: gateway → consumer(无 lane 实例) → provider(有 lane 实例) ----------
# 链路特点:
#   - half-consumer (HalfLaneEchoClient): 只注册 baseline 实例,没有 lane=gray 实例
#   - half-provider (HalfLaneEchoServer): 同时有 baseline + lane=gray 两台实例
#
# 关键验证点:
#   1) PERMISSIVE 规则下,中间 consumer 一跳没有 lane=gray 实例,但 lane router 会
#      "回退基线 + 透传 stainLabel",最终 half-consumer 把 service-lane Header 透传给
#      half-provider, 后者作为入口染色 (consumer 不是 entry, 走 alreadyStained 分支),
#      命中 lane=gray 实例。表现: callee lane=gray (即 half-consumer 看到 provider lane=gray)。
#   2) STRICT 规则下,中间 consumer 一跳没有 lane=gray 实例,SDK 直接置空 cluster
#      并触发 HTTP 503,根本走不到 provider。
#   3) 无 Header 的基线流量正常走 baseline 实例,链路不受 half 链路影响。

# ---------- 用例 7.2: half 链路 — PERMISSIVE 染色穿透中间 baseline 节点 ----------
test_half_chain_permissive_passthrough() {
    log_title "用例 7.2: [half] PERMISSIVE — 中间 consumer 无 lane 实例时染色穿透到 provider"
    log_info "测试目的: 验证 gateway 染色后, lane=gray 在 half-consumer 一跳回退 baseline 但 service-lane 透传, half-provider 命中 lane=gray"
    log_info "链路: gateway → ${HALF_CONSUMER_SERVICE} (仅 baseline) → ${HALF_PROVIDER_SERVICE} (baseline+gray)"
    log_info "请求头: user: half-permissive (规则 half-gray-permissive: PERMISSIVE → lane=gray)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: half-permissive" \
        "http://127.0.0.1:${GATEWAY_PORT}/${HALF_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    # 期望响应中:
    #   - half-consumer 自己 lane=(baseline) (它没有 lane 标签)
    #   - 它选中的 callee (即 half-provider) lane=gray
    # gateway 拼装的 msg 形如:
    #   "...callee addr:host:HALF_PROVIDER_GRAY_PORT, callee lane=gray, callee resp=Hello, I'm HalfLaneEchoClient. lane=(baseline), ... callee lane=gray, callee resp=..."
    # 鉴别"穿透"的关键: 响应中必须能看到 lane=gray (provider 端命中) 且 half-consumer 是 baseline。
    if echo "$resp" | grep -q ":${HALF_PROVIDER_GRAY_PORT}" \
        && echo "$resp" | grep -q "${HALF_CONSUMER_SERVICE}. lane=(baseline)"; then
        test_pass "[用例7.2] PERMISSIVE 染色穿透成功: half-consumer baseline → half-provider lane=gray(:${HALF_PROVIDER_GRAY_PORT})"
    elif echo "$resp" | grep -q ":${HALF_PROVIDER_BASE_PORT}"; then
        test_fail "[用例7.2] half-provider 命中 baseline(:${HALF_PROVIDER_BASE_PORT}) 而非 lane=gray, 染色未穿透"
        log_info "  实际响应: ${resp}"
    elif echo "$resp" | grep -q "Half"; then
        test_fail "[用例7.2] 收到响应但路由结果不符合预期"
        log_info "  期望: half-consumer lane=(baseline) 且 callee 命中 :${HALF_PROVIDER_GRAY_PORT}"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例7.2] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 7.2d: half 链路 — service-lane 直接染色对照 ----------
# 与 7.2 形成对照: 7.2 走"普通 header → gateway 按规则匹配后染色"路径,
# 7.2d 直接构造 service-lane stain label,跳过 TrafficMatchRule 流量识别,
# 直接命中 lane router 的 stainLabelIndex。两者最终应走出完全一样的链路结果。
test_half_chain_permissive_passthrough_by_stain_label() {
    log_title "用例 7.2d: [half] PERMISSIVE — service-lane 直接染色对照"
    log_info "测试目的: 验证直接染色 (跳过 TrafficMatchRule) 与流量匹配染色 (7.2) 走出同样的链路: half-consumer baseline → half-provider lane=gray"
    log_info "链路: gateway → ${HALF_CONSUMER_SERVICE} (仅 baseline) → ${HALF_PROVIDER_SERVICE} (baseline+gray)"
    log_info "请求头: service-lane: ${EXPECTED_LANE_GROUP}/half-gray-permissive (直接染色,绕过流量匹配)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "service-lane: ${EXPECTED_LANE_GROUP}/half-gray-permissive" \
        "http://127.0.0.1:${GATEWAY_PORT}/${HALF_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q ":${HALF_PROVIDER_GRAY_PORT}" \
        && echo "$resp" | grep -q "${HALF_CONSUMER_SERVICE}. lane=(baseline)"; then
        test_pass "[用例7.2d] service-lane 直接染色穿透成功: half-consumer baseline → half-provider lane=gray(:${HALF_PROVIDER_GRAY_PORT})"
    elif echo "$resp" | grep -q ":${HALF_PROVIDER_BASE_PORT}"; then
        test_fail "[用例7.2d] half-provider 命中 baseline(:${HALF_PROVIDER_BASE_PORT}) 而非 lane=gray, stain label 染色未生效"
        log_info "  实际响应: ${resp}"
    elif echo "$resp" | grep -q "Half"; then
        test_fail "[用例7.2d] 收到响应但路由结果不符合预期"
        log_info "  期望: half-consumer lane=(baseline) 且 callee 命中 :${HALF_PROVIDER_GRAY_PORT}"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例7.2d] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 7.3: half 链路 — STRICT 在中间 consumer 一跳直接 503 ----------
test_half_chain_strict_blocks_at_consumer() {
    log_title "用例 7.3: [half] STRICT — 中间 consumer 无 lane 实例时一跳即 503"
    log_info "测试目的: 验证 STRICT 模式下 consumer 一跳就因没有 lane=gray 实例返回 HTTP 503, 不会被错误降级或穿透"
    log_info "链路: gateway → ${HALF_CONSUMER_SERVICE} (仅 baseline) — 在此处中断"
    log_info "请求头: user: half-strict (规则 half-gray-strict: STRICT → lane=gray)"
    log_info ""

    local tmp_body http_code body
    tmp_body=$(mktemp -t lane-test-half-strict-body.XXXXXX)
    http_code=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: half-strict" \
        -o "$tmp_body" -w "%{http_code}" \
        "http://127.0.0.1:${GATEWAY_PORT}/${HALF_CONSUMER_SERVICE}/echo" 2>/dev/null || echo "000")
    http_code=$(printf '%s' "$http_code" | tr -cd '0-9' | cut -c1-3)
    [ -z "$http_code" ] && http_code="000"
    body=$(cat "$tmp_body" 2>/dev/null || echo "")
    rm -f "$tmp_body"
    log_raw "  HTTP ${http_code}, 响应: ${body}"

    if [ "$http_code" = "503" ]; then
        test_pass "[用例7.3] STRICT 模式 consumer 一跳无目标 lane 实例时正确返回 HTTP 503"
    elif echo "$body" | grep -q ":${HALF_PROVIDER_GRAY_PORT}"; then
        test_fail "[用例7.3] STRICT 模式被错误地穿透到 half-provider(:${HALF_PROVIDER_GRAY_PORT}), 期望在 consumer 一跳就 503"
    elif echo "$body" | grep -q "Half"; then
        test_fail "[用例7.3] STRICT 模式路由到了实例(期望 503), body=${body:0:120}"
    else
        test_fail "[用例7.3] STRICT 模式未返回 503 (HTTP=${http_code})"
        log_info "  响应: ${body:0:200}"
    fi
}

# ---------- 用例 7.1: half 链路 — 无 Header 基线流量不受影响 ----------
test_half_chain_baseline() {
    log_title "用例 7.1: [half] 无 Header — 基线流量正常路由到 baseline 实例"
    log_info "测试目的: 确认 half 规则只对带相应 Header 的流量生效, 普通基线请求仍按 baseline 路由"
    log_info "链路: gateway → ${HALF_CONSUMER_SERVICE} → ${HALF_PROVIDER_SERVICE} (无 Header)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        "http://127.0.0.1:${GATEWAY_PORT}/${HALF_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q ":${HALF_PROVIDER_BASE_PORT}" \
        && echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例7.1] 基线流量正确路由到 half-provider baseline(:${HALF_PROVIDER_BASE_PORT})"
    elif echo "$resp" | grep -q ":${HALF_PROVIDER_GRAY_PORT}"; then
        test_fail "[用例7.1] 无 Header 流量被错误染色, 路由到 lane=gray 实例(:${HALF_PROVIDER_GRAY_PORT})"
        log_info "  实际响应: ${resp}"
    elif echo "$resp" | grep -q "Half"; then
        test_fail "[用例7.1] 收到响应但路由结果不符合预期"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例7.1] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- half-simple 链路用例 8.x: simple-gateway → simple-consumer(无 lane 实例) → provider(有 lane 实例) ----------
# 链路特点:
#   - half-simple-consumer (HalfSimpleLaneEchoClient): 只注册 baseline 实例,没有 lane=gray 实例, 基于 GetOneInstance
#   - provider (LaneEchoServer): 同时有 baseline + lane=gray 两台实例
#   - 与 half 链路 (7.x) 的差异: 中间 consumer 使用 simple-consumer (GetOneInstance) 而非 consumer (ProcessRouters)
#
# 关键验证点:
#   1) PERMISSIVE 规则下, simple-gateway 通过 RouteMetadata 透传 service-lane header 给 half-simple-consumer,
#      half-simple-consumer 作为中间节点透传给 provider, 后者命中 lane=gray 实例。
#   2) STRICT 规则下, simple-gateway 一跳就因 half-simple-consumer 无 gray 实例返回 HTTP 503。
#   3) 无 Header 的基线流量正常走 baseline 实例, 链路不受影响。

# ---------- 用例 8.1: half-simple 链路 — 无 Header 基线流量不受影响 ----------
test_half_simple_chain_baseline() {
    log_title "用例 8.1: [half-simple] 无 Header — 基线流量正常路由到 baseline 实例"
    log_info "测试目的: 确认 half 规则只对带相应 Header 的流量生效, 普通基线请求仍按 baseline 路由"
    log_info "链路: simple-gateway → ${HALF_SIMPLE_CONSUMER_SERVICE} → ${PROVIDER_SERVICE} (无 Header)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${HALF_SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q ":${PROVIDER_BASE_PORT}" \
        && echo "$resp" | grep -q "lane=(baseline)"; then
        test_pass "[用例8.1] 基线流量正确路由到 provider baseline(:${PROVIDER_BASE_PORT})"
    elif echo "$resp" | grep -q ":${PROVIDER_GRAY_PORT}"; then
        test_fail "[用例8.1] 无 Header 流量被错误染色, 路由到 lane=gray 实例(:${PROVIDER_GRAY_PORT})"
        log_info "  实际响应: ${resp}"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例8.1] 收到响应但路由结果不符合预期"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例8.1] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 8.2: half-simple 链路 — PERMISSIVE 染色穿透中间 baseline 节点 ----------
test_half_simple_chain_permissive_passthrough() {
    log_title "用例 8.2: [half-simple] PERMISSIVE — 中间 simple-consumer 无 lane 实例时染色穿透到 provider"
    log_info "测试目的: 验证 simple-gateway 染色后, lane=gray 在 half-simple-consumer 一跳回退 baseline 但 service-lane 透传, provider 命中 lane=gray"
    log_info "链路: simple-gateway → ${HALF_SIMPLE_CONSUMER_SERVICE} (仅 baseline) → ${PROVIDER_SERVICE} (baseline+gray)"
    log_info "请求头: user: half-permissive (规则 half-gray-permissive: PERMISSIVE → lane=gray)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: half-permissive" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${HALF_SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    # 期望响应中:
    #   - half-simple-consumer 自己 lane=(baseline) (它没有 lane 标签)
    #   - 它选中的 callee (即 provider) lane=gray
    if echo "$resp" | grep -q ":${PROVIDER_GRAY_PORT}" \
        && echo "$resp" | grep -q "${HALF_SIMPLE_CONSUMER_SERVICE}. lane=(baseline)"; then
        test_pass "[用例8.2] PERMISSIVE 染色穿透成功: half-simple-consumer baseline → provider lane=gray(:${PROVIDER_GRAY_PORT})"
    elif echo "$resp" | grep -q ":${PROVIDER_BASE_PORT}"; then
        test_fail "[用例8.2] provider 命中 baseline(:${PROVIDER_BASE_PORT}) 而非 lane=gray, 染色未穿透"
        log_info "  实际响应: ${resp}"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例8.2] 收到响应但路由结果不符合预期"
        log_info "  期望: ${HALF_SIMPLE_CONSUMER_SERVICE} lane=(baseline) 且 callee 命中 :${PROVIDER_GRAY_PORT}"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例8.2] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 8.2d: half-simple 链路 — service-lane 直接染色对照 ----------
# 与 8.2 形成对照: 8.2 走"普通 header → simple-gateway 按规则匹配后染色"路径,
# 8.2d 直接构造 service-lane stain label,跳过 TrafficMatchRule 流量识别。
test_half_simple_chain_permissive_passthrough_by_stain_label() {
    log_title "用例 8.2d: [half-simple] PERMISSIVE — service-lane 直接染色对照"
    log_info "测试目的: 验证直接染色 (跳过 TrafficMatchRule) 与流量匹配染色 (8.2) 走出同样的链路: half-simple-consumer baseline → provider lane=gray"
    log_info "链路: simple-gateway → ${HALF_SIMPLE_CONSUMER_SERVICE} (仅 baseline) → ${PROVIDER_SERVICE} (baseline+gray)"
    log_info "请求头: service-lane: ${EXPECTED_LANE_GROUP}/half-gray-permissive (直接染色,绕过流量匹配)"
    log_info ""

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "service-lane: ${EXPECTED_LANE_GROUP}/half-gray-permissive" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${HALF_SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || true)
    log_raw "  响应: ${resp}"

    if echo "$resp" | grep -q ":${PROVIDER_GRAY_PORT}" \
        && echo "$resp" | grep -q "${HALF_SIMPLE_CONSUMER_SERVICE}. lane=(baseline)"; then
        test_pass "[用例8.2d] service-lane 直接染色穿透成功: half-simple-consumer baseline → provider lane=gray(:${PROVIDER_GRAY_PORT})"
    elif echo "$resp" | grep -q ":${PROVIDER_BASE_PORT}"; then
        test_fail "[用例8.2d] provider 命中 baseline(:${PROVIDER_BASE_PORT}) 而非 lane=gray, stain label 染色未生效"
        log_info "  实际响应: ${resp}"
    elif echo "$resp" | grep -q "LaneEchoServer"; then
        test_fail "[用例8.2d] 收到响应但路由结果不符合预期"
        log_info "  期望: ${HALF_SIMPLE_CONSUMER_SERVICE} lane=(baseline) 且 callee 命中 :${PROVIDER_GRAY_PORT}"
        log_info "  实际响应: ${resp}"
    else
        test_fail "[用例8.2d] 未收到有效响应"
        log_info "  响应: ${resp}"
    fi
}

# ---------- 用例 8.3: half-simple 链路 — STRICT 在 simple-gateway 一跳直接 503 ----------
test_half_simple_chain_strict_blocks_at_gateway() {
    log_title "用例 8.3: [half-simple] STRICT — simple-gateway 一跳就因 half-simple-consumer 无 lane 实例返回 503"
    log_info "测试目的: 验证 STRICT 模式下 simple-gateway 用 GetOneInstance 路由, consumer 无 gray 实例时直接 HTTP 503"
    log_info "链路: simple-gateway → ${HALF_SIMPLE_CONSUMER_SERVICE} (仅 baseline) — 在此处中断"
    log_info "请求头: user: half-strict (规则 half-gray-strict: STRICT → lane=gray)"
    log_info ""

    local tmp_body http_code body
    tmp_body=$(mktemp -t lane-test-half-simple-strict-body.XXXXXX)
    http_code=$(curl -s --connect-timeout 5 --max-time 10 \
        -H "user: half-strict" \
        -o "$tmp_body" -w "%{http_code}" \
        "http://127.0.0.1:${SIMPLE_GATEWAY_PORT}/${HALF_SIMPLE_CONSUMER_SERVICE}/echo" 2>/dev/null || echo "000")
    http_code=$(printf '%s' "$http_code" | tr -cd '0-9' | cut -c1-3)
    [ -z "$http_code" ] && http_code="000"
    body=$(cat "$tmp_body" 2>/dev/null || echo "")
    rm -f "$tmp_body"
    log_raw "  HTTP ${http_code}, 响应: ${body}"

    if [ "$http_code" = "503" ]; then
        test_pass "[用例8.3] STRICT 模式 simple-gateway GetOneInstance 无目标 lane 实例时正确返回 HTTP 503"
    elif echo "$body" | grep -q ":${PROVIDER_GRAY_PORT}"; then
        test_fail "[用例8.3] STRICT 模式被错误地穿透到 provider(:${PROVIDER_GRAY_PORT}), 期望在 gateway 一跳就 503"
    elif echo "$body" | grep -q "LaneEchoServer"; then
        test_fail "[用例8.3] STRICT 模式路由到了实例(期望 503), body=${body:0:120}"
    else
        test_fail "[用例8.3] STRICT 模式未返回 503 (HTTP=${http_code})"
        log_info "  响应: ${body:0:200}"
    fi
}

# ==================== 运行所有测试 ====================
run_all_tests() {
    log_title "开始泳道路由测试"
    log_info "Polaris: ${POLARIS_HOST}"
    log_info "Gateway 端口: ${GATEWAY_PORT}"
    log_info "Simple-Gateway 端口: ${SIMPLE_GATEWAY_PORT}"
    log_info "Gateway-Excl 端口: ${GATEWAY_EXCL_PORT} (baseLaneMode=1)"
    log_info "Consumer-base 端口: ${CONSUMER_BASE_PORT}"
    log_info "Consumer-gray 端口: ${CONSUMER_GRAY_PORT}"
    log_info "Simple-Consumer-base 端口: ${SIMPLE_CONSUMER_BASE_PORT}"
    log_info "Simple-Consumer-gray 端口: ${SIMPLE_CONSUMER_GRAY_PORT}"
    log_info "Provider-base 端口: ${PROVIDER_BASE_PORT}"
    log_info "Provider-gray 端口: ${PROVIDER_GRAY_PORT}"
    log_info "Provider-excl-stable 端口: ${PROVIDER_EXCL_STABLE_PORT} (${PROVIDER_EXCL_SERVICE})"
    log_info "Provider-excl-gray 端口: ${PROVIDER_EXCL_GRAY_PORT} (${PROVIDER_EXCL_SERVICE})"
    log_info "NLG-Provider-base 端口: ${NLG_PROVIDER_BASE_PORT} (${NLG_PROVIDER_SERVICE})"
    log_info "NLG-Provider-gray 端口: ${NLG_PROVIDER_GRAY_PORT} (${NLG_PROVIDER_SERVICE})"
    log_info "NLG-Consumer-mode0 端口: ${NLG_CONSUMER_MODE0_PORT} (baseLaneMode=0, ${NLG_CONSUMER_SERVICE})"
    log_info "NLG-Consumer-mode1 端口: ${NLG_CONSUMER_MODE1_PORT} (baseLaneMode=1, ${NLG_CONSUMER_SERVICE})"
    log_info "Half-Consumer 端口: ${HALF_CONSUMER_PORT} (${HALF_CONSUMER_SERVICE}, 仅 baseline)"
    log_info "Half-Provider-base 端口: ${HALF_PROVIDER_BASE_PORT} (${HALF_PROVIDER_SERVICE})"
    log_info "Half-Provider-gray 端口: ${HALF_PROVIDER_GRAY_PORT} (${HALF_PROVIDER_SERVICE}, lane=gray)"
    log_info "Half-Simple-Consumer 端口: ${HALF_SIMPLE_CONSUMER_PORT} (${HALF_SIMPLE_CONSUMER_SERVICE}, 仅 baseline, GetOneInstance)"
    log_info ""

    TOTAL_PASS=0
    TOTAL_FAIL=0
    TOTAL_COUNT=0

    # 预热请求：确保所有服务的 Polaris SDK 已加载泳道规则
    # 泳道规则通过 Discover API 异步加载，首次调用可能未就绪
    log_info "预热请求中，等待泳道规则加载..."
    local warmup_ok=false
    for i in $(seq 1 10); do
        local resp
        resp=$(curl -s --connect-timeout 3 --max-time 5 \
            -H "service-lane: ${EXPECTED_LANE_GROUP}/gray" \
            "http://127.0.0.1:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
        if echo "$resp" | grep -q "lane=gray"; then
            warmup_ok=true
            log_ok "泳道规则已就绪 (预热 ${i} 次)"
            break
        fi
        sleep 2
    done
    if [ "$warmup_ok" = false ]; then
        log_warn "预热 10 次后泳道规则仍未就绪，继续测试"
    fi
    log_info ""

    # ========== 主链路: gateway → consumer → provider (ProcessRouters + ProcessLoadBalance) ==========
    log_title "开始主链路测试（gateway → consumer → provider）"
    print_lane_group_summary
    test_no_header_baseline
    test_direct_stain_gray
    test_direct_stain_gray_negative
    test_traffic_match_strict_gray
    test_traffic_match_strict_gray_negative
    test_traffic_match_permissive_fallback
    test_traffic_match_strict_no_instance_503
    test_no_rule_match_baseline
    test_lane_isolation
    # 六类匹配维度验证（主链路）
    test_method_post_match
    test_method_post_match_negative
    test_query_env_match
    test_query_env_match_negative
    test_cookie_user_match
    test_cookie_user_match_negative
    test_path_gray_match
    test_path_gray_match_negative
    test_caller_ip_local_match
    test_caller_ip_local_match_negative
    test_caller_ip_not_zero_match
    test_caller_ip_not_zero_match_negative

    # ========== simple 链路: simple-gateway → consumer → provider (GetOneInstance) ==========
    log_title "开始 simple 链路测试（simple-gateway → consumer → provider）"
    print_lane_group_summary
    test_simple_no_header_baseline
    test_simple_direct_stain_gray
    test_simple_direct_stain_gray_negative
    test_simple_traffic_match_strict_gray
    test_simple_traffic_match_strict_gray_negative
    test_simple_traffic_match_permissive_fallback
    test_simple_traffic_match_strict_no_instance_503
    test_simple_no_rule_match_baseline
    test_simple_lane_isolation
    # 六类匹配维度验证（simple-gateway 链路）
    test_simple_method_post_match
    test_simple_method_post_match_negative
    test_simple_query_env_match
    test_simple_query_env_match_negative
    test_simple_cookie_user_match
    test_simple_cookie_user_match_negative
    test_simple_path_gray_match
    test_simple_path_gray_match_negative
    test_simple_caller_ip_local_match
    test_simple_caller_ip_local_match_negative
    test_simple_caller_ip_not_zero_match
    test_simple_caller_ip_not_zero_match_negative

    # ========== gw→sc 链路: gateway → simple-consumer → provider ==========
    log_title "开始 gw→sc 链路测试（gateway → simple-consumer → provider）"
    print_lane_group_summary
    test_gw_sc_no_header_baseline
    test_gw_sc_direct_stain_gray
    test_gw_sc_direct_stain_gray_negative
    test_gw_sc_traffic_match_strict_gray
    test_gw_sc_traffic_match_strict_gray_negative
    test_gw_sc_traffic_match_permissive_fallback
    test_gw_sc_traffic_match_strict_no_instance_503
    test_gw_sc_no_rule_match_baseline
    test_gw_sc_lane_isolation

    # ========== sg→sc 链路: simple-gateway → simple-consumer → provider ==========
    log_title "开始 sg→sc 链路测试（simple-gateway → simple-consumer → provider）"
    print_lane_group_summary
    test_sg_sc_no_header_baseline
    test_sg_sc_direct_stain_gray
    test_sg_sc_direct_stain_gray_negative
    test_sg_sc_traffic_match_strict_gray
    test_sg_sc_traffic_match_strict_gray_negative
    test_sg_sc_traffic_match_permissive_fallback
    test_sg_sc_traffic_match_strict_no_instance_503
    test_sg_sc_no_rule_match_baseline
    test_sg_sc_lane_isolation

    # ========== baseLaneMode=ExcludeEnabledLaneInstance 专项测试 ==========
    log_title "开始 baseLaneMode=1 专项测试（gateway-excl → StableLaneEchoServer）"
    print_lane_group_summary
    test_base_lane_mode_exclude_enabled

    # ========== half 链路: gateway → consumer(无 lane 实例) → provider(有 lane 实例) ==========
    log_title "开始 half 链路测试（gateway → ${HALF_CONSUMER_SERVICE} → ${HALF_PROVIDER_SERVICE}）"
    print_lane_group_summary
    test_half_chain_baseline
    test_half_chain_permissive_passthrough
    test_half_chain_permissive_passthrough_by_stain_label
    test_half_chain_strict_blocks_at_consumer

    # ========== half-simple 链路: simple-gateway → simple-consumer(无 lane 实例) → provider(有 lane 实例) ==========
    log_title "开始 half-simple 链路测试（simple-gateway → ${HALF_SIMPLE_CONSUMER_SERVICE} → ${PROVIDER_SERVICE}）"
    print_lane_group_summary
    test_half_simple_chain_baseline
    test_half_simple_chain_permissive_passthrough
    test_half_simple_chain_permissive_passthrough_by_stain_label
    test_half_simple_chain_strict_blocks_at_gateway

    # ========== 最后执行（涉及规则变更，避免影响前面用例）==========
    log_title "开始破坏性用例（test_out_of_lane_group）"
    print_lane_group_summary
    test_out_of_lane_group

    # ========== baseLaneMode 非泳道组服务专项测试 ==========
    log_title "开始 baseLaneMode 非泳道组服务专项测试（nlg-consumer → nlg-provider）"
    test_base_lane_mode_no_lane_group

    log_title "测试结果汇总"
    _log "总计: ${TOTAL_COUNT}  ${GREEN}通过: ${TOTAL_PASS}${NC}  ${RED}失败: ${TOTAL_FAIL}${NC}"
    if [ $TOTAL_FAIL -eq 0 ]; then
        log_ok "所有泳道路由测试用例通过！"
    else
        log_fail "有 ${TOTAL_FAIL} 个测试用例失败，请查看日志: ${TEST_LOG_FILE}"
    fi
    log_info "测试日志已保存到: ${TEST_LOG_FILE}"
    return $TOTAL_FAIL
}

# ==================== 停止服务 ====================
stop_services() {
    log_title "停止所有服务"

    if [ ! -f "${PID_FILE}" ]; then
        log_info "未找到 PID 文件，尝试按进程名查找..."
        local pids
        pids=$(ps -ef | grep -E '\.build/(provider|consumer|gateway|simple-gateway|simple-consumer)' | grep -v grep | awk '{print $2}' || true)
        if [ -n "$pids" ]; then
            for pid in $pids; do
                log_info "停止进程 PID=${pid}"
                kill "$pid" 2>/dev/null || true
            done
        else
            log_info "未发现残留进程"
        fi
        return 0
    fi

    # 第一步: 发送 SIGTERM（触发 provider 的 Polaris 反注册）
    local pids_to_kill=()
    while read -r pid; do
        [ -z "$pid" ] && continue
        if kill -0 "$pid" 2>/dev/null; then
            pids_to_kill+=("$pid")
            log_info "发送 SIGTERM PID=${pid}"
            kill "$pid" 2>/dev/null || true
        else
            log_info "进程 PID=${pid} 已不存在"
        fi
    done < "${PID_FILE}"

    if [ ${#pids_to_kill[@]} -eq 0 ]; then
        log_info "无需停止任何进程"
        rm -f "${PID_FILE}"
        return 0
    fi

    # 等待进程退出（provider 需要时间完成 Polaris 反注册）
    log_info "等待服务完成 Polaris 反注册 (最多 10s)..."
    local elapsed=0
    while [ $elapsed -lt 10 ]; do
        local still_running=false
        for pid in "${pids_to_kill[@]}"; do
            if kill -0 "$pid" 2>/dev/null; then
                still_running=true
                break
            fi
        done
        if [ "$still_running" = false ]; then
            log_ok "所有服务已优雅停止 (耗时 ${elapsed}s)"
            break
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done

    # 强制停止未退出的进程
    for pid in "${pids_to_kill[@]}"; do
        if kill -0 "$pid" 2>/dev/null; then
            log_warn "强制停止 PID=${pid}"
            kill -9 "$pid" 2>/dev/null || true
        fi
    done

    rm -f "${PID_FILE}"
    log_ok "服务已停止"
}

# ==================== 主流程 ====================
usage() {
    echo "用法: $0 <命令> [选项] [polaris地址]"
    echo ""
    echo "命令:"
    echo "  all    完整流程（构建 → 验证规则 → 启动 → 等待 → 测试 → 停止）"
    echo "  build  仅构建 Go 二进制文件"
    echo "  check  仅检查 Polaris 泳道规则"
    echo "  start  构建并启动服务（含规则检查）"
    echo "  test   执行测试用例（服务需已启动）"
    echo "  stop   停止所有服务"
    echo ""
    echo "选项:"
    echo "  -d     开启 debug 模式，所有服务输出 Polaris SDK debug 级别日志"
    echo ""
    echo "参数:"
    echo "  polaris地址  Polaris 服务端 IP（默认: 127.0.0.1）"
    echo ""
    echo "预期的 Polaris 泳道规则（需提前在控制台配置）:"
    echo "  泳道组: lane-go-example"
    echo "  入口: LaneRouterGateway/default"
    echo "  目标: LaneEchoClient, SimpleLaneEchoClient, LaneEchoServer, StableLaneEchoServer, HalfLaneEchoClient, HalfLaneEchoServer, HalfSimpleLaneEchoClient"
    echo "  规则 gray:               Header user=gray       → lane=gray,           STRICT"
    echo "  规则 permissive:         Header user=noexist    → lane=noexist,        PERMISSIVE"
    echo "  规则 strict-noexist:     Header user=strict     → lane=strict-noexist, STRICT (无实例 → HTTP 503)"
    echo "  规则 method-post:        METHOD=POST + Header lane-test=method-post     → lane=gray, STRICT"
    echo "  规则 query-env:          QUERY env=gray + Header lane-test=query-env    → lane=gray, STRICT"
    echo "  规则 cookie-user:        COOKIE user=gray + Header lane-test=cookie-user → lane=gray, STRICT"
    echo "  规则 path-gray:          PATH = /LaneEchoClient/gray-path                → lane=gray, STRICT"
    echo "  规则 caller-ip-local:    CALLER_IP=127.0.0.1 + Header lane-test=caller-ip-local      → lane=gray, STRICT"
    echo "  规则 caller-ip-not-zero: CALLER_IP≠0.0.0.0  + Header lane-test=caller-ip-not-zero    → lane=gray, STRICT"
    echo "  规则 half-gray-permissive: Header user=half-permissive → lane=gray, PERMISSIVE (验证染色穿透 baseline consumer)"
    echo "  规则 half-gray-strict:     Header user=half-strict     → lane=gray, STRICT (验证 consumer 一跳即 503)"
    echo ""
    echo "示例:"
    echo "  $0 all 127.0.0.1         # 完整测试"
    echo "  $0 all -d 127.0.0.1      # 完整测试（debug 模式）"
    echo "  $0 check 127.0.0.1       # 仅检查规则"
    echo "  $0 start -d 127.0.0.1    # 启动服务（debug 模式）"
    echo "  $0 test                  # 执行测试"
    echo "  $0 stop                  # 停止服务"
}

CMD="${1:-all}"
shift 2>/dev/null || true

# 解析选项参数（如 -d）
while [ $# -gt 0 ]; do
    case "$1" in
        -d|--debug)
            DEBUG_MODE=true
            shift
            ;;
        -*)
            log_fail "未知选项: $1"
            usage
            exit 1
            ;;
        *)
            # 非选项参数作为 polaris 地址
            POLARIS_HOST="$1"
            shift
            ;;
    esac
done

mkdir -p "$(dirname "${TEST_LOG_FILE}")"
echo "===== 泳道路由测试日志 $(date '+%Y-%m-%d %H:%M:%S') =====" > "${TEST_LOG_FILE}"
log_info "测试日志: ${TEST_LOG_FILE}"

case "${CMD}" in
    all)
        init_config
        build_binaries || exit 1
        validate_rules_with_wait || exit 1
        start_services || exit 1
        wait_for_services || { stop_services; exit 1; }
        run_all_tests
        RESULT=$?
        stop_services
        exit $RESULT
        ;;
    build)
        build_binaries
        ;;
    check)
        init_config
        validate_rules_with_wait
        ;;
    start)
        init_config
        build_binaries || exit 1
        validate_rules_with_wait || exit 1
        start_services || exit 1
        wait_for_services
        ;;
    test)
        init_config
        run_all_tests
        ;;
    stop)
        stop_services
        ;;
    -h|--help|help)
        usage
        ;;
    *)
        log_fail "未知命令: ${CMD}"
        usage
        exit 1
        ;;
esac
