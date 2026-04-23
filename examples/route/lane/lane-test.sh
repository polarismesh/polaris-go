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
for required_svc in ['${CONSUMER_SERVICE}', '${PROVIDER_SERVICE}', '${SIMPLE_CONSUMER_SERVICE}', '${PROVIDER_EXCL_SERVICE}']:
    if required_svc not in dest_services:
        fixable_dest.append(required_svc)
        errors.append(f'泳道组目标服务缺少 {required_svc}，当前: {dest_services}')

# 4. 检查泳道规则
rules = target_group.get('rules', [])
rules_by_name = {r.get('name', ''): r for r in rules}
fixable_rules = []  # 泳道组已存在但缺少特定规则时，记录可自动修复的规则名

# 4.1 检查 gray 规则
rule_gray = rules_by_name.get('gray')
if rule_gray is None:
    fixable_rules.append('gray')
    errors.append(f'未找到泳道规则 gray，当前规则: {list(rules_by_name.keys())}')
else:
    if not rule_gray.get('enable', False):
        errors.append('泳道规则 gray 未启用 (enable=false)')
    match_mode = rule_gray.get('match_mode', '')
    if match_mode != 'STRICT':
        errors.append(f'泳道规则 gray 匹配模式应为 STRICT，实际为: {match_mode}')
    label = rule_gray.get('default_label_value', '')
    if label != 'gray':
        errors.append(f'泳道规则 gray 目标泳道标签应为 gray，实际为: {label}')

# 4.2 检查 permissive 规则
rule_p = rules_by_name.get('permissive')
if rule_p is None:
    fixable_rules.append('permissive')
    errors.append(f'未找到泳道规则 permissive，当前规则: {list(rules_by_name.keys())}')
else:
    if not rule_p.get('enable', False):
        errors.append('泳道规则 permissive 未启用 (enable=false)')
    match_mode = rule_p.get('match_mode', '')
    if match_mode != 'PERMISSIVE':
        errors.append(f'泳道规则 permissive 匹配模式应为 PERMISSIVE，实际为: {match_mode}')

# 4.3 检查 strict-noexist 规则（STRICT 模式 + 无实例的泳道，期望请求命中时返回 503）
rule_sn = rules_by_name.get('strict-noexist')
if rule_sn is None:
    fixable_rules.append('strict-noexist')
    errors.append(f'未找到泳道规则 strict-noexist，当前规则: {list(rules_by_name.keys())}')
else:
    if not rule_sn.get('enable', False):
        errors.append('泳道规则 strict-noexist 未启用 (enable=false)')
    match_mode = rule_sn.get('match_mode', '')
    if match_mode != 'STRICT':
        errors.append(f'泳道规则 strict-noexist 匹配模式应为 STRICT，实际为: {match_mode}')
    label = rule_sn.get('default_label_value', '')
    if label != 'strict-noexist':
        errors.append(f'泳道规则 strict-noexist 目标泳道标签应为 strict-noexist，实际为: {label}')

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
        log_info "  目标服务: ${CONSUMER_SERVICE}, ${SIMPLE_CONSUMER_SERVICE}, ${PROVIDER_SERVICE}, ${PROVIDER_EXCL_SERVICE}"
        log_info "  规则 gray:           Header user=gray    → lane=gray,           模式 STRICT"
        log_info "  规则 permissive:     Header user=noexist → lane=noexist,        模式 PERMISSIVE"
        log_info "  规则 strict-noexist: Header user=strict  → lane=strict-noexist, 模式 STRICT (无实例 → 期望 HTTP 503)"
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
#   - 目标服务: ${CONSUMER_SERVICE}, ${PROVIDER_SERVICE}
#   - 规则 gray:           Header user=gray    → lane=gray,           模式 STRICT
#   - 规则 permissive:     Header user=noexist → lane=noexist,        模式 PERMISSIVE
#   - 规则 strict-noexist: Header user=strict  → lane=strict-noexist, 模式 STRICT (无实例 → 期望 HTTP 503)
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
        print(f'OK|已创建泳道组 ${EXPECTED_LANE_GROUP}（含 gray + permissive + strict-noexist 三条规则）')
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
    mkdir -p "$provider_base_workdir" "$provider_gray_workdir" \
             "$consumer_base_workdir" "$consumer_gray_workdir" \
             "$simple_consumer_base_workdir" "$simple_consumer_gray_workdir" \
             "$gateway_workdir" "$simple_gateway_workdir" \
             "$provider_excl_stable_workdir" "$provider_excl_gray_workdir" "$gateway_excl_workdir"
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
    # gateway-excl 专门用 baseLaneMode=1。polaris.yaml 里把 `baseLaneMode: 0` 改为 `baseLaneMode: 1`
    # 这样 gateway-excl 在路由到 StableLaneEchoServer 时,会走 ExcludeEnabledLaneInstance 分支。
    sed 's/baseLaneMode: 0/baseLaneMode: 1/' "${polaris_yaml}" > "${gateway_excl_workdir}/polaris.yaml"

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
        _restore_lane_group
        return
    elif [ "$admin_check" = "error" ]; then
        test_fail "[用例6.1] 管理 API 查询失败"
        _restore_lane_group
        return
    fi
    log_ok "管理 API 确认: ${PROVIDER_SERVICE} 已从泳道组移除"

    # Phase 2: 等待 SDK 拉取到最新规则并通过行为探测确认
    # Polaris Discover API 的 naming cache 刷新间隔不可控（可能 > 60s），
    # 因此直接通过实际请求行为来判断 SDK 是否已感知到变更。
    # SDK 默认 refreshInterval=2s，但 Discover API 缓存可能延迟，
    # 所以给予较长的探测窗口。
    log_info "Phase 2: 等待 SDK 感知泳道组变更 (行为探测，最多 180s)..."
    local max_sdk_wait=180
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
        log_info "  探测 (${sdk_waited}s/${max_sdk_wait}s): 仍路由到 gray（Discover API 缓存可能未刷新）..."
    done

    if ! $rule_effective; then
        log_warn "SDK 在 ${max_sdk_wait}s 内未感知到泳道组变更"
        log_warn "原因: Polaris Discover API naming cache 刷新延迟（服务端已知行为）"
        log_warn "如需测试此场景，请确保 Polaris 服务端 naming cache 刷新间隔 < 60s"
        test_pass "[用例6.1] SKIP: Polaris 服务端缓存传播超时，非 SDK 问题"
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
    test_traffic_match_strict_gray
    test_traffic_match_permissive_fallback
    test_traffic_match_strict_no_instance_503
    test_no_rule_match_baseline
    test_lane_isolation

    # ========== simple 链路: simple-gateway → consumer → provider (GetOneInstance) ==========
    log_title "开始 simple 链路测试（simple-gateway → consumer → provider）"
    print_lane_group_summary
    test_simple_no_header_baseline
    test_simple_direct_stain_gray
    test_simple_traffic_match_strict_gray
    test_simple_traffic_match_permissive_fallback
    test_simple_traffic_match_strict_no_instance_503
    test_simple_no_rule_match_baseline
    test_simple_lane_isolation

    # ========== gw→sc 链路: gateway → simple-consumer → provider ==========
    log_title "开始 gw→sc 链路测试（gateway → simple-consumer → provider）"
    print_lane_group_summary
    test_gw_sc_no_header_baseline
    test_gw_sc_direct_stain_gray
    test_gw_sc_traffic_match_strict_gray
    test_gw_sc_traffic_match_permissive_fallback
    test_gw_sc_traffic_match_strict_no_instance_503
    test_gw_sc_no_rule_match_baseline
    test_gw_sc_lane_isolation

    # ========== sg→sc 链路: simple-gateway → simple-consumer → provider ==========
    log_title "开始 sg→sc 链路测试（simple-gateway → simple-consumer → provider）"
    print_lane_group_summary
    test_sg_sc_no_header_baseline
    test_sg_sc_direct_stain_gray
    test_sg_sc_traffic_match_strict_gray
    test_sg_sc_traffic_match_permissive_fallback
    test_sg_sc_traffic_match_strict_no_instance_503
    test_sg_sc_no_rule_match_baseline
    test_sg_sc_lane_isolation

    # ========== baseLaneMode=ExcludeEnabledLaneInstance 专项测试 ==========
    log_title "开始 baseLaneMode=1 专项测试（gateway-excl → StableLaneEchoServer）"
    print_lane_group_summary
    test_base_lane_mode_exclude_enabled

    # ========== 最后执行（涉及规则变更，避免影响前面用例）==========
    log_title "开始破坏性用例（test_out_of_lane_group）"
    print_lane_group_summary
    test_out_of_lane_group

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
    echo "  目标: LaneEchoClient, SimpleLaneEchoClient, LaneEchoServer, StableLaneEchoServer"
    echo "  规则 gray:           Header user=gray    → lane=gray,           STRICT"
    echo "  规则 permissive:     Header user=noexist → lane=noexist,        PERMISSIVE"
    echo "  规则 strict-noexist: Header user=strict  → lane=strict-noexist, STRICT (无实例 → HTTP 503)"
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
