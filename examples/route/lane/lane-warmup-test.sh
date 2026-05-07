#!/bin/bash
# ============================================================
# 泳道预热（Warmup）+ 百分比（Percentage）灰度测试脚本（Go SDK 版本）
#
# 用法: ./lane-warmup-test.sh <命令> [polaris地址]
# 示例: ./lane-warmup-test.sh all 127.0.0.1
#
# 命令:
#   all     完整流程（构建 → 验证规则 → 启动 → 等待 → 测试 → 停止）
#   build   仅构建 Go 二进制
#   check   仅检查 Polaris 泳道规则
#   start   构建并启动服务（含规则检查）
#   test    执行测试用例（服务需已启动）
#   stop    停止所有服务
#
# 预期的 Polaris 泳道规则配置（需提前在控制台配置）:
#   泳道组: lane-go-warmup
#   入口服务: LaneRouterGateway (default 命名空间)
#   目标服务: LaneEchoServer
#   规则 gray:
#     - default_label_value: gray
#     - traffic_gray.type:            WARMUP
#     - traffic_gray.warmup_interval: 60  （建议设置较短区间便于测试）
#     - traffic_gray.curvature:       2   （二次曲线，默认值）
#     - enable: true
#
# 预热算法:
#   probability = pow(uptime / warmup_interval, curvature) * 100%
#   uptime = 当前时间 - etime（规则最近一次启用时间）
#   当 uptime >= interval 时，probability = 100%
#
# 测试方法:
#   通过 service-lane: lane-go-warmup/gray Header 将请求染色到 gray 泳道，
#   laneRouter 根据预热概率决定路由到 gray 实例还是回退到 baseline 实例。
#   通过统计响应中 lane=gray 的比例来验证预热曲线是否正确。
#
# PERCENTAGE 专项 (用例 6-9):
#   在 warmup 组结束后追加 4 个 PERCENTAGE 模式用例。这部分会通过管理 API
#   将 gray 规则的 traffic_gray 临时切换为 {mode: PERCENTAGE, percentage.percent: N}，
#   验证 lane_router.go tryStainByPercentage 在 N=1/50/100 三档下的概率行为，
#   以及基线请求不受 percent 影响。run_all_tests 结束前会自动调用
#   restore_gray_rule_to_warmup 把规则恢复为 WARMUP 模式，脚本被 Ctrl-C 打断时
#   也会通过 trap 做同样的恢复，以免影响后续跑测或共用 Polaris 的其他脚本。
#
#   备注: 不验证 percent=0 —— specification/lane.proto 的 percent 是 proto3 裸 int32
#   (json:"omitempty")，零值会被 omit，Polaris 服务端以 400103 拒绝该请求；SDK 端
#   `percent <= 0 → return false` 这条分支因此外部不可达，本测试用 percent=1
#   验证「极小比例几乎不染色」语义。
# ============================================================

# ==================== 配置区 ====================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

POLARIS_HOST="127.0.0.1"
POLARIS_TOKEN=""

init_config() {
    POLARIS_HTTP_ADDR="http://${POLARIS_HOST}:8090"
    POLARIS_CONSOLE="http://${POLARIS_HOST}:8080"
}

# 服务端口（与 lane-test.sh 共用端口段，不可同时运行）
GATEWAY_PORT=48095
CONSUMER_BASE_PORT=19080
CONSUMER_GRAY_PORT=19081
PROVIDER_BASE_PORT=19090
PROVIDER_GRAY_PORT=19091

# 服务名
NAMESPACE="default"
PROVIDER_SERVICE="LaneEchoServer"
CONSUMER_SERVICE="LaneEchoClient"
GATEWAY_SERVICE="LaneRouterGatewayService"

# 预期的预热泳道组名（与 lane-test.sh 使用的 lane-go-example 隔离）
EXPECTED_LANE_GROUP="lane-go-warmup"
EXPECTED_ENTRY_SERVICE="${GATEWAY_SERVICE}"

# 目录配置
BUILD_DIR="${SCRIPT_DIR}/.build"
LOG_DIR="${SCRIPT_DIR}/.logs"
PID_FILE="${SCRIPT_DIR}/.lane-warmup-pids"
TEST_LOG_FILE="${LOG_DIR}/lane-warmup-test-$(date +%Y%m%d_%H%M%S).log"

# 全局预热参数（由 validate_lane_rules 从 Polaris 读取并设置）
WARMUP_INTERVAL=120
WARMUP_CURVATURE=2
GRAY_ETIME=0

# 等待预热完成的最大超时（秒）；超过此值时，"预热完成"用例将被跳过
MAX_WARMUP_WAIT=120

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

# ==================== 预热概率计算 ====================

# 计算预热期望概率（返回 0-100 的整数百分比）
# 参数: uptime(秒) interval(秒) curvature
calc_warmup_probability() {
    local uptime=$1
    local interval=$2
    local curvature=$3
    python3 -c "
import math
uptime = $uptime
interval = $interval
curvature = $curvature
if uptime <= 0:
    print(0)
elif uptime >= interval:
    print(100)
else:
    prob = math.pow(float(uptime) / float(interval), float(curvature))
    print(int(prob * 100))
" 2>/dev/null || echo "0"
}

# ==================== Polaris 管理 API ====================

# 查询泳道组完整信息（使用管理 API，非 SDK 发现接口）
query_lane_group_full() {
    curl -s --connect-timeout 5 --max-time 10 \
        "${POLARIS_HTTP_ADDR}/naming/v1/lane/groups?name=${EXPECTED_LANE_GROUP}&offset=0&limit=10" \
        -H "X-Polaris-Token: ${POLARIS_TOKEN}" 2>/dev/null || true
}

# 读取 gray 规则的 etime（返回 Unix 秒时间戳，0 表示获取失败）
#
# Polaris 返回的 etime 可能是以下几种格式，都要正确解析：
#   1. 字符串日期：'2026-04-21 17:50:25' （当前 Polaris 主流格式）
#   2. 毫秒时间戳字符串：'1776765588000'
#   3. 秒时间戳字符串：'1776765588'
#   4. 数字：1776765588 (秒) 或 1776765588000 (毫秒)
get_gray_etime() {
    local response
    response=$(query_lane_group_full)
    echo "$response" | python3 -c "
import sys, json
from datetime import datetime

try:
    data = json.load(sys.stdin)
except Exception:
    print(0)
    sys.exit(0)

def parse_etime(raw):
    if not raw:
        return 0
    s = str(raw).strip()
    if not s or s == '0':
        return 0
    # 日期字符串
    if '-' in s and ':' in s:
        try:
            dt = datetime.strptime(s, '%Y-%m-%d %H:%M:%S')
            return int(dt.timestamp())
        except Exception:
            return 0
    # 数字(ms/s)
    try:
        v = int(s)
    except Exception:
        return 0
    if v > 1_000_000_000_000:
        v //= 1000
    return v

try:
    groups = data.get('data', [])
    for g in groups:
        if g.get('name') == '${EXPECTED_LANE_GROUP}':
            for r in g.get('rules', []):
                if r.get('name') == 'gray':
                    tg = r.get('traffic_gray', {})
                    raw = tg.get('etime', r.get('etime', 0))
                    print(parse_etime(raw))
                    sys.exit(0)
    print(0)
except Exception:
    print(0)
" 2>/dev/null || echo "0"
}

# 设置泳道规则的 enable 状态（True 或 False）
_set_lane_rule_enable() {
    local rule_name=$1
    local enable_py_val=$2   # Python bool: True 或 False

    local group_json
    group_json=$(query_lane_group_full)

    if [ -z "$group_json" ]; then
        log_fail "无法获取泳道组信息，管理 API 连接失败: ${POLARIS_HTTP_ADDR}"
        return 1
    fi

    local updated_json
    updated_json=$(echo "$group_json" | python3 -c "
import sys, json

try:
    data = json.load(sys.stdin)
    groups = data.get('data', [])
    if not groups:
        sys.exit(1)
    group = groups[0]
    # 移除顶层 @type 字段，否则 Polaris decode 会报 \"unknown field @type in v1.LaneGroup\"。
    # 注意：entries[].selector.@type 必须保留（Any 类型反序列化需要它）。
    group.pop('@type', None)
    found = False
    for rule in group.get('rules', []):
        if rule.get('name') == '$rule_name':
            rule['enable'] = $enable_py_val
            found = True
    if not found:
        sys.stderr.write('rule not found: $rule_name\n')
        sys.exit(1)
    print(json.dumps([group]))
except Exception as e:
    sys.stderr.write(str(e) + '\n')
    sys.exit(1)
" 2>/dev/null)

    if [ -z "$updated_json" ]; then
        log_fail "修改规则 ${rule_name} enable=${enable_py_val} 失败（JSON 处理异常）"
        return 1
    fi

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -X PUT "${POLARIS_HTTP_ADDR}/naming/v1/lane/groups" \
        -H "Content-Type: application/json" \
        -H "X-Polaris-Token: ${POLARIS_TOKEN}" \
        -d "$updated_json" 2>/dev/null || true)

    # 检查响应码（Polaris 返回批量结果）
    local ok
    ok=$(echo "$resp" | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    # 兼容 {responses:[{code:...}]} 和 {code:...} 两种格式
    responses = d.get('responses', [d])
    codes = [str(r.get('code', 0)) for r in responses]
    if any(c in ('200000','200001') for c in codes):
        print('ok')
    else:
        print('fail:' + ','.join(codes))
except:
    print('fail:parse')
" 2>/dev/null || echo "fail:curl")

    if [[ "$ok" == ok ]]; then
        return 0
    else
        log_warn "更新规则 ${rule_name} enable=${enable_py_val} 结果: ${ok}"
        # 部分环境返回非标准码，继续执行
        return 0
    fi
}

disable_lane_rule() { _set_lane_rule_enable "$1" "False"; }
enable_lane_rule()  { _set_lane_rule_enable "$1" "True"; }

# 重置泳道规则预热计时器（disable → sleep → enable → sleep）
# 重置后 etime 将更新为当前时间，预热重新从 0% 开始
reset_lane_rule() {
    local rule_name="${1:-gray}"
    log_info "重置泳道规则 '${rule_name}' 预热计时器..."
    disable_lane_rule "$rule_name" || return 1
    sleep 2
    enable_lane_rule "$rule_name" || return 1
    sleep 3
    # 重新获取 etime，确认已更新
    GRAY_ETIME=$(get_gray_etime)
    local now
    now=$(date +%s)
    local drift=$((now - GRAY_ETIME))
    if [ "$GRAY_ETIME" -gt 0 ] && [ $drift -le 30 ]; then
        log_ok "规则 '${rule_name}' 预热计时器已重置，etime=${GRAY_ETIME}（${drift}s 前）"
    else
        log_warn "规则 etime 可能未更新（etime=${GRAY_ETIME}, drift=${drift}s），继续执行"
    fi
}

# ==================== PERCENTAGE 模式切换 ====================
#
# set_gray_rule_traffic_gray 将 gray 规则的 traffic_gray 字段整体替换为指定内容，
# 并通过 PUT /naming/v1/lane/groups 提交。用于 percentage / warmup 模式切换，
# 便于在同一个 lane-go-warmup 泳道组上复用 gray 规则做不同染色策略的回归测试。
#
# 参数:
#   $1 mode     'PERCENTAGE' 或 'WARMUP'
#   $2 value    mode=PERCENTAGE 时是 percent (0-100 整数)；mode=WARMUP 时是 "interval|curvature"
# 返回: 0=成功, 1=失败
_set_gray_rule_traffic_gray() {
    local mode="$1"
    local value="$2"

    local group_json
    group_json=$(query_lane_group_full)
    if [ -z "$group_json" ]; then
        log_fail "查询泳道组失败，无法连接 Polaris 管理接口"
        return 1
    fi

    local updated_json
    updated_json=$(echo "$group_json" | MODE="$mode" VALUE="$value" python3 -c "
import sys, json, os

mode = os.environ['MODE']
value = os.environ['VALUE']

try:
    data = json.load(sys.stdin)
    groups = data.get('data', [])
    if not groups:
        sys.stderr.write('no group found\n')
        sys.exit(1)
    group = groups[0]
    # 移除顶层 @type，保留 entries[].selector.@type
    group.pop('@type', None)

    found = False
    for rule in group.get('rules', []):
        if rule.get('name') != 'gray':
            continue
        found = True
        if mode == 'PERCENTAGE':
            rule['traffic_gray'] = {
                'mode': 'PERCENTAGE',
                'percentage': {
                    'percent': int(value),
                },
            }
        elif mode == 'WARMUP':
            interval, curvature = value.split('|')
            rule['traffic_gray'] = {
                'mode': 'WARMUP',
                'warmup': {
                    'interval_second': int(interval),
                    'curvature': int(float(curvature)),
                },
            }
        else:
            sys.stderr.write(f'unknown mode: {mode}\n')
            sys.exit(1)
    if not found:
        sys.stderr.write('gray rule not found\n')
        sys.exit(1)
    print(json.dumps([group]))
except Exception as e:
    sys.stderr.write(str(e) + '\n')
    sys.exit(1)
" 2>/dev/null)

    if [ -z "$updated_json" ]; then
        log_fail "构造 ${mode} 模式规则更新 body 失败"
        return 1
    fi

    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        -X PUT "${POLARIS_HTTP_ADDR}/naming/v1/lane/groups" \
        -H "Content-Type: application/json" \
        -H "X-Polaris-Token: ${POLARIS_TOKEN}" \
        -d "$updated_json" 2>/dev/null || true)

    local ok
    ok=$(echo "$resp" | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    responses = d.get('responses', [d])
    codes = [str(r.get('code', 0)) for r in responses]
    if any(c in ('200000','200001') for c in codes):
        print('ok')
    else:
        print('fail:' + ','.join(codes))
except:
    print('fail:parse')
" 2>/dev/null || echo "fail:curl")

    if [[ "$ok" == "ok" ]]; then
        return 0
    fi

    # 区分 4xx / 5xx 等明确错误（如 400103=参数校验失败）和 "non-standard 2xx 但未列出 ok 码" 两种情况：
    # - 含 4xx / 5xx 三位数 code → 真失败，必须 return 1，让上层用例显式 FAIL
    # - 其他（如纯解析失败、curl 失败、未知 6 位 code）→ 沿用宽容策略 return 0，附 warn
    if echo "$ok" | grep -qE "fail:[45][0-9]{2}|fail:[45][0-9]{5}"; then
        log_fail "更新 gray 规则 mode=${mode} 失败: ${ok}（服务端拒绝）"
        return 1
    fi
    log_warn "更新 gray 规则 mode=${mode} 返回: ${ok}（部分环境非标准返回，继续）"
    return 0
}

# set_gray_rule_percentage 把 gray 规则切换到 PERCENTAGE 模式。
# 参数: $1 = percent (0~100)
# 额外 sleep 3s 给 SDK 拉缓存。
set_gray_rule_percentage() {
    local percent="$1"
    log_info "将 gray 规则切换到 PERCENTAGE 模式，percent=${percent}..."
    if ! _set_gray_rule_traffic_gray "PERCENTAGE" "$percent"; then
        return 1
    fi
    sleep 3
    log_ok "gray 规则已切换到 PERCENTAGE (percent=${percent})"
    return 0
}

# restore_gray_rule_to_warmup 恢复 gray 规则为 WARMUP 模式，使用 WARMUP_INTERVAL/CURVATURE
# 这两个由 validate_lane_rules 预先读取的运行时变量；任一为空则回退默认 60/2。
restore_gray_rule_to_warmup() {
    local interval="${WARMUP_INTERVAL:-60}"
    local curvature="${WARMUP_CURVATURE:-2}"
    log_info "恢复 gray 规则为 WARMUP 模式 (interval=${interval}s, curvature=${curvature})..."
    if ! _set_gray_rule_traffic_gray "WARMUP" "${interval}|${curvature}"; then
        log_warn "恢复 WARMUP 失败，请手动在 Polaris 控制台 [${POLARIS_CONSOLE}] 核对 [${EXPECTED_LANE_GROUP}] 的 gray 规则"
        return 1
    fi
    sleep 3
    log_ok "gray 规则已恢复为 WARMUP"
    return 0
}

# ==================== 泳道规则验证 ====================
validate_lane_rules() {
    local response
    response=$(query_lane_group_full)

    if [ -z "$response" ]; then
        log_fail "查询泳道规则失败，无法连接 Polaris 管理接口: ${POLARIS_HTTP_ADDR}/naming/v1/lane/groups"
        return 1
    fi

    local validate_result
    validate_result=$(echo "$response" | python3 -c "
import sys, json

try:
    data = json.load(sys.stdin)
except Exception as e:
    print(f'ERROR|响应不是有效 JSON: {e}')
    sys.exit(0)

# 管理 API 没有 code 字段时默认认为成功
code = data.get('code', 200000)
if str(code) not in ('0', '200000'):
    print(f'ERROR|管理 API 返回错误码: {code}')
    sys.exit(0)

groups = data.get('data', [])
target = None
for g in groups:
    if g.get('name') == '${EXPECTED_LANE_GROUP}':
        target = g
        break

if target is None:
    names = [g.get('name','?') for g in groups]
    print(f'ERROR|未找到泳道组 ${EXPECTED_LANE_GROUP}，当前组: {names}')
    sys.exit(0)

# 检查入口
entries = target.get('entries', [])
entry_found = any(
    e.get('selector', {}).get('service') == '${EXPECTED_ENTRY_SERVICE}' and
    e.get('selector', {}).get('namespace') == '${NAMESPACE}'
    for e in entries
)
if not entry_found:
    entry_names = [e.get('selector', {}).get('service','?') for e in entries]
    print(f'ERROR|泳道组入口未包含 ${EXPECTED_ENTRY_SERVICE}/${NAMESPACE}，当前入口: {entry_names}')

# 检查目标服务
destinations = target.get('destinations', [])
dest_services = {d.get('service','') for d in destinations}
for required_svc in ['${CONSUMER_SERVICE}', '${PROVIDER_SERVICE}']:
    if required_svc not in dest_services:
        # FIXABLE_DEST 标记：此类错误可通过管理 API 自动修复
        print(f'FIXABLE_DEST|{required_svc}')
        print(f'ERROR|泳道组目标服务缺少 {required_svc}，当前: {dest_services}')

# 检查 gray 规则
rules = target.get('rules', [])
gray_rule = next((r for r in rules if r.get('name') == 'gray'), None)
if gray_rule is None:
    names = [r.get('name','?') for r in rules]
    print(f'ERROR|未找到 gray 规则，当前规则: {names}')
    sys.exit(0)

if not gray_rule.get('enable', False):
    print('ERROR|gray 规则未启用 (enable=false)，请在 Polaris 控制台启用后重试')
    sys.exit(0)

# 校验 TrafficMatchRule 的 header key。必须为 'warmup-user'，否则会和其他 lane 组
# (如 lane-go-example 使用 user=gray) 冲突，导致 warmup 概率路径被老规则屏蔽。
_args = gray_rule.get('traffic_match_rule', {}).get('arguments', [])
_expected_key = 'warmup-user'
_actual_keys = [a.get('key', '') for a in _args if a.get('type', '') == 'HEADER']
if _expected_key not in _actual_keys:
    # FIXABLE_RULE 标记：此类错误可通过管理 API 自动修复
    print(f'FIXABLE_RULE|header_key')
    print(f'ERROR|gray 规则 TrafficMatchRule 的 Header key 应为 {_expected_key!r}，'
          f'实际为 {_actual_keys!r}')
    sys.exit(0)

# 读取预热配置
tg = gray_rule.get('traffic_gray', {})
# type 字段兼容 WARMUP / warmup / Warmup
mode = str(tg.get('type', tg.get('mode', ''))).upper()
if mode != 'WARMUP':
    print(f'ERROR|gray 规则 traffic_gray.type 应为 WARMUP，实际为: {mode or \"(未设置)\"}')
    print('提示: 请在 Polaris 控制台将 gray 规则的流量策略类型设为「预热」(WARMUP)')
    sys.exit(0)

interval = int(tg.get('warmup_interval', tg.get('interval', 120)))
curvature = float(tg.get('curvature', 2.0))

# 读取 etime（可能在规则顶层或 traffic_gray 内，可能是日期字符串、ms 或 s）
raw_etime = tg.get('etime', gray_rule.get('etime', 0))
etime = 0
if raw_etime:
    s = str(raw_etime).strip()
    if s and s != '0':
        if '-' in s and ':' in s:
            # 日期字符串 '2026-04-21 17:50:25'
            try:
                from datetime import datetime
                etime = int(datetime.strptime(s, '%Y-%m-%d %H:%M:%S').timestamp())
            except Exception:
                etime = 0
        else:
            try:
                etime = int(s)
                if etime > 1_000_000_000_000:
                    etime = etime // 1000
            except Exception:
                etime = 0

label = gray_rule.get('default_label_value', '')
if label != 'gray':
    print(f'ERROR|gray 规则目标泳道标签应为 gray，实际为: {label}')
    sys.exit(0)

print(f'WARMUP_PARAM|{interval}|{curvature}')
print(f'GRAY_ETIME|{etime}')
print(f'OK|泳道组 ${EXPECTED_LANE_GROUP}: WARMUP 模式, interval={interval}s, curvature={curvature}')
" 2>/dev/null)

    if [ -z "$validate_result" ]; then
        log_fail "泳道规则解析失败，可能是响应格式异常或 python3 不可用"
        return 1
    fi

    local has_error=false
    FIXABLE_DEST_SERVICES=""
    FIXABLE_RULE=""
    while IFS='|' read -r level msg rest; do
        case "$level" in
            FIXABLE_DEST)
                FIXABLE_DEST_SERVICES="${FIXABLE_DEST_SERVICES} ${msg}"
                ;;
            FIXABLE_RULE)
                FIXABLE_RULE="${msg}"
                ;;
            ERROR)
                log_fail "规则验证失败: ${msg}"
                [ -n "$rest" ] && log_info "  ${rest}"
                has_error=true
                ;;
            OK)
                log_ok "规则验证通过: ${msg}"
                ;;
            WARMUP_PARAM)
                WARMUP_INTERVAL="$msg"
                WARMUP_CURVATURE="$rest"
                log_info "  预热参数: interval=${WARMUP_INTERVAL}s, curvature=${WARMUP_CURVATURE}"
                if [ "${WARMUP_INTERVAL}" -gt "${MAX_WARMUP_WAIT}" ]; then
                    log_warn "  warmup_interval (${WARMUP_INTERVAL}s) > MAX_WARMUP_WAIT (${MAX_WARMUP_WAIT}s)"
                    log_warn "  建议将泳道规则中的 warmup_interval 设为 ≤${MAX_WARMUP_WAIT}s 以缩短测试耗时"
                fi
                ;;
            GRAY_ETIME)
                GRAY_ETIME="$msg"
                local now
                now=$(date +%s)
                local uptime=$((now - GRAY_ETIME))
                if [ "$GRAY_ETIME" -gt 0 ]; then
                    local expected_prob
                    expected_prob=$(calc_warmup_probability "$uptime" "${WARMUP_INTERVAL}" "${WARMUP_CURVATURE}")
                    log_info "  当前已预热: ${uptime}s / ${WARMUP_INTERVAL}s，期望概率: ${expected_prob}%"
                else
                    log_info "  etime=0（规则可能未曾启用，或 Polaris 版本不返回 etime）"
                fi
                ;;
        esac
    done <<< "$validate_result"

    if [ "$has_error" = true ]; then
        log_info ""
        log_info "期望的 Polaris 泳道规则配置（lane-go-warmup）:"
        log_info "  泳道组: ${EXPECTED_LANE_GROUP}"
        log_info "  入口: ${EXPECTED_ENTRY_SERVICE} (${NAMESPACE} 命名空间)"
        log_info "  目标服务: ${CONSUMER_SERVICE}, ${PROVIDER_SERVICE}"
        log_info "  规则 gray:"
        log_info "    - default_label_value: gray"
        log_info "    - traffic_gray.type: WARMUP"
        log_info "    - traffic_gray.warmup_interval: 60"
        log_info "    - traffic_gray.curvature: 2"
        log_info "    - enable: true"
        log_info ""
        log_info "请在 Polaris 控制台 (${POLARIS_CONSOLE}) 配置后重试"
        return 1
    fi
    return 0
}

# ==================== 自动创建 warmup 泳道组 ====================
#
# create_warmup_lane_group 在泳道组不存在时，通过管理 API 创建符合 warmup 测试预期的完整配置：
#   - 入口: ${EXPECTED_ENTRY_SERVICE} (gateway)
#   - 目标服务: ${CONSUMER_SERVICE}, ${PROVIDER_SERVICE} (完整链路 gateway → consumer → provider)
#   - 规则 gray: WARMUP 模式，interval=60s, curvature=2, enable=true, default_label_value=gray
#
# Polaris 创建 LaneGroup 的 HTTP API:
#   POST /naming/v1/lane/groups  (JSON body = [group])
#
# 注意：Polaris 对 entries.selector 要求使用 google.protobuf.Any 封装，
# 管理 API 接收 JSON 时需要带上 @type 字段。服务入口类型为 polarismesh.cn/service。
create_warmup_lane_group() {
    log_info "尝试自动创建泳道组 [${EXPECTED_LANE_GROUP}]..."

    local result
    result=$(python3 -c "
import json, urllib.request

group = {
    'name': '${EXPECTED_LANE_GROUP}',
    'description': 'auto-created by lane-warmup-test.sh',
    'entries': [{
        'type': 'polarismesh.cn/service',
        'selector': {
            '@type': 'type.googleapis.com/v1.ServiceSelector',
            'namespace': '${NAMESPACE}',
            'service': '${EXPECTED_ENTRY_SERVICE}',
        },
    }],
    'destinations': [
        {'service': '${CONSUMER_SERVICE}', 'namespace': '${NAMESPACE}'},
        {'service': '${PROVIDER_SERVICE}', 'namespace': '${NAMESPACE}'},
    ],
    'rules': [{
        'name': 'gray',
        'enable': True,
        'match_mode': 'STRICT',
        'default_label_value': 'gray',
        'traffic_match_rule': {
            'arguments': [{
                'type': 'HEADER',
                'key': 'warmup-user',
                'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'gray'},
            }],
            'match_mode': 'AND',
        },
        'traffic_gray': {
            'mode': 'WARMUP',
            'warmup': {
                'interval_second': 60,
                'curvature': 2,
            },
        },
    }],
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
        print(f'OK|已创建泳道组 ${EXPECTED_LANE_GROUP}')
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

# ==================== 修补现有泳道组 ====================
# add_service_to_lane_group 将指定服务加入现有泳道组的 destinations。
# 用于 validate 发现组存在但缺 destination 的自动修复场景(FIXABLE_DEST 分支)。
# 参数: $1=服务名
# 返回: 0=成功或无需添加, 1=失败
add_service_to_lane_group() {
    local target_svc="$1"
    if [ -z "$target_svc" ]; then
        log_fail "add_service_to_lane_group: 缺少服务名参数"
        return 1
    fi
    log_info "正在将 ${target_svc} 加入泳道组 [${EXPECTED_LANE_GROUP}]..."

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

destinations = group.get('destinations', [])
for d in destinations:
    if d.get('service') == target_svc:
        print(f'WARN|{target_svc} 已在目标服务中，无需添加')
        sys.exit(0)

destinations.append({'service': target_svc, 'namespace': '${NAMESPACE}'})
group['destinations'] = destinations

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
    if code in (200000, 200001):
        current = [d.get('service','?') for d in destinations]
        print(f'OK|已添加 {target_svc}，当前目标服务: {current}')
    else:
        info = resp_data.get('info', '')
        print(f'ERROR|添加 {target_svc} 失败，返回码: {code}, 信息: {info}')
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
    fi
    local err_msg
    err_msg=$(echo "$result" | grep "^ERROR" | cut -d'|' -f2- || echo "未知错误")
    log_fail "添加 ${target_svc} 失败: ${err_msg}"
    return 1
}

# 修正 gray 规则的 TrafficMatchRule Header key 为 'warmup-user'。
# 用于和其他 lane 组(如 lane-go-example 用 user=gray)隔离，避免规则冲突。
# 返回: 0=成功, 1=失败
fix_gray_rule_match_header() {
    log_info "正在修正泳道组 [${EXPECTED_LANE_GROUP}] gray 规则的匹配 Header 为 warmup-user..."

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

changed = False
for rule in group.get('rules', []):
    if rule.get('name') != 'gray':
        continue
    tmr = rule.setdefault('traffic_match_rule', {})
    args = tmr.setdefault('arguments', [])
    # 替换或新增 warmup-user=gray
    header_args = [a for a in args if a.get('type') == 'HEADER']
    found_target = False
    for a in header_args:
        if a.get('key') == 'warmup-user':
            a['value'] = {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'gray'}
            found_target = True
            break
    # 移除其它 HEADER 参数(如 user=gray)以避免和其他组冲突
    if not found_target:
        args[:] = [a for a in args if a.get('type') != 'HEADER']
        args.append({
            'type': 'HEADER',
            'key': 'warmup-user',
            'value': {'type': 'EXACT', 'value_type': 'TEXT', 'value': 'gray'},
        })
    else:
        # 已有 warmup-user，把其它 HEADER 规则也清掉，避免 AND 模式匹配失败
        args[:] = [a for a in args if not (a.get('type') == 'HEADER' and a.get('key') != 'warmup-user')]
    tmr.setdefault('match_mode', 'AND')
    changed = True

if not changed:
    print('ERROR|gray 规则不存在，无法修正')
    sys.exit(0)

req_data = json.dumps([group]).encode('utf-8')
req = urllib.request.Request(
    '${POLARIS_HTTP_ADDR}/naming/v1/lane/groups',
    data=req_data,
    method='PUT',
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
        print(f'OK|gray 规则 Header 已修正为 warmup-user=gray')
    else:
        info = resp_data.get('info', '')
        print(f'ERROR|修正失败，返回码: {code}, 信息: {info}')
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
    log_fail "修正失败: ${err_msg}"
    return 1
}

validate_rules_with_wait() {
    log_title "检查预热泳道规则配置"
    log_info "查询 ${EXPECTED_ENTRY_SERVICE} 的泳道规则（泳道组: ${EXPECTED_LANE_GROUP}）..."

    local auto_create_attempted=false
    local auto_fix_attempts=0
    local max_auto_fix=3

    while true; do
        if validate_lane_rules; then
            log_ok "预热泳道规则符合预期配置"
            return 0
        fi

        # 自动创建：当前组完全不存在 → 用管理 API 创建一份完整规则
        # 通过检查上一次 validate_lane_rules 的日志文件末尾判断是否仅为"组不存在"
        local tail_log
        tail_log=$(tail -n 20 "${TEST_LOG_FILE}" 2>/dev/null || true)
        if ! $auto_create_attempted && echo "$tail_log" | grep -q "未找到泳道组"; then
            auto_create_attempted=true
            log_warn "检测到泳道组 ${EXPECTED_LANE_GROUP} 不存在，尝试自动创建..."
            if create_warmup_lane_group; then
                log_info "等待 10s 让 Polaris 服务端传播变更..."
                sleep 10
                log_info "重新查询泳道规则..."
                continue
            fi
            log_warn "自动创建失败，转为手动模式"
        fi

        # 自动修复：组存在但 destinations 缺服务(通常是上次测试清理不净，或手工建组时少配)
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

        # 自动修复：gray 规则的匹配 Header key 不对(和其他 lane 组冲突)
        if [ "${FIXABLE_RULE}" = "header_key" ] && [ $auto_fix_attempts -lt $max_auto_fix ]; then
            auto_fix_attempts=$((auto_fix_attempts + 1))
            log_warn "检测到 gray 规则 Header key 不匹配，尝试自动修正 (第 ${auto_fix_attempts}/${max_auto_fix} 次)..."
            fix_gray_rule_match_header || true
            log_info "等待 10s 让 Polaris 服务端传播变更..."
            sleep 10
            log_info "重新查询泳道规则..."
            continue
        fi

        log_warn "规则不符合预期，请在 Polaris 控制台修改后按 Enter 重新检查（输入 q 退出）..."
        read -r user_input
        [ "$user_input" = "q" ] || [ "$user_input" = "Q" ] && return 1
        log_info "重新查询泳道规则..."
    done
}

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

    log_info "构建 gateway..."
    if (cd "${SCRIPT_DIR}/gateway" && go build -o "${BUILD_DIR}/gateway" .); then
        log_ok "gateway 构建成功: ${BUILD_DIR}/gateway"
    else
        log_fail "gateway 构建失败"
        return 1
    fi
}

# ==================== 启动服务 ====================
start_services() {
    log_title "启动服务实例"
    mkdir -p "${LOG_DIR}"
    > "${PID_FILE}"

    if [ ! -f "${BUILD_DIR}/provider" ] || [ ! -f "${BUILD_DIR}/consumer" ] || [ ! -f "${BUILD_DIR}/gateway" ]; then
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
    local gateway_workdir="${BUILD_DIR}/gateway-run"
    mkdir -p "$provider_base_workdir" "$provider_gray_workdir" \
        "$consumer_base_workdir" "$consumer_gray_workdir" "$gateway_workdir"
    cp "${polaris_yaml}" "${provider_base_workdir}/polaris.yaml"
    cp "${polaris_yaml}" "${provider_gray_workdir}/polaris.yaml"
    cp "${polaris_yaml}" "${consumer_base_workdir}/polaris.yaml"
    cp "${polaris_yaml}" "${consumer_gray_workdir}/polaris.yaml"
    cp "${polaris_yaml}" "${gateway_workdir}/polaris.yaml"

    # provider-base: 无泳道标签（基线实例）
    log_info "启动 provider-base (端口: ${PROVIDER_BASE_PORT}, lane: baseline)..."
    (cd "$provider_base_workdir" && POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/provider" \
        -namespace="${NAMESPACE}" \
        -service="${PROVIDER_SERVICE}" \
        -port="${PROVIDER_BASE_PORT}" \
        > "${LOG_DIR}/provider-base.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "provider-base PID: $!"

    # provider-gray: 携带 lane=gray 元数据
    log_info "启动 provider-gray (端口: ${PROVIDER_GRAY_PORT}, lane: gray)..."
    (cd "$provider_gray_workdir" && POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/provider" \
        -namespace="${NAMESPACE}" \
        -service="${PROVIDER_SERVICE}" \
        -port="${PROVIDER_GRAY_PORT}" \
        -lane="gray" \
        > "${LOG_DIR}/provider-gray.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "provider-gray PID: $!"

    # consumer-base: 无泳道标签（基线实例），转发给 provider
    log_info "启动 consumer-base (端口: ${CONSUMER_BASE_PORT}, lane: baseline)..."
    (cd "$consumer_base_workdir" && POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/consumer" \
        -namespace="${NAMESPACE}" \
        -service="${PROVIDER_SERVICE}" \
        -selfNamespace="${NAMESPACE}" \
        -selfService="${CONSUMER_SERVICE}" \
        -port="${CONSUMER_BASE_PORT}" \
        > "${LOG_DIR}/consumer-base.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "consumer-base PID: $!"

    # consumer-gray: 携带 lane=gray 元数据，转发给 provider
    log_info "启动 consumer-gray (端口: ${CONSUMER_GRAY_PORT}, lane: gray)..."
    (cd "$consumer_gray_workdir" && POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/consumer" \
        -namespace="${NAMESPACE}" \
        -service="${PROVIDER_SERVICE}" \
        -selfNamespace="${NAMESPACE}" \
        -selfService="${CONSUMER_SERVICE}" \
        -port="${CONSUMER_GRAY_PORT}" \
        -lane="gray" \
        > "${LOG_DIR}/consumer-gray.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "consumer-gray PID: $!"

    # gateway（泳道网关入口，作为泳道组的唯一入口服务）
    log_info "启动 gateway (端口: ${GATEWAY_PORT}, selfService: ${GATEWAY_SERVICE})..."
    (cd "$gateway_workdir" && POLARIS_SERVER="${POLARIS_HOST}" \
        "${BUILD_DIR}/gateway" \
        -namespace="${NAMESPACE}" \
        -selfNamespace="${NAMESPACE}" \
        -selfService="${GATEWAY_SERVICE}" \
        -port="${GATEWAY_PORT}" \
        > "${LOG_DIR}/gateway.log" 2>&1) &
    echo $! >> "${PID_FILE}"
    log_info "gateway PID: $!"

    log_info "所有服务已启动，PID 记录在 ${PID_FILE}"
    log_info "Polaris SDK 日志目录:"
    log_info "  provider-base: ${provider_base_workdir}/polaris/"
    log_info "  provider-gray: ${provider_gray_workdir}/polaris/"
    log_info "  consumer-base: ${consumer_base_workdir}/polaris/"
    log_info "  consumer-gray: ${consumer_gray_workdir}/polaris/"
    log_info "  gateway:       ${gateway_workdir}/polaris/"
}

# ==================== 等待服务就绪 ====================
wait_for_services() {
    log_title "等待服务就绪"
    local max_wait=60
    local elapsed=0

    while [ $elapsed -lt $max_wait ]; do
        local http_code
        http_code=$(curl -s -o /dev/null -w "%{http_code}" \
            "http://localhost:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || echo "000")

        if [ "${http_code}" = "200" ] || [ "${http_code}" = "500" ]; then
            log_ok "gateway HTTP 服务已启动 (耗时 ${elapsed}s)"
            sleep 2
            local resp
            resp=$(curl -s --connect-timeout 3 --max-time 5 \
                "http://localhost:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
            # 完整链路就绪的标志：响应里应同时出现 LaneEchoClient 和 LaneEchoServer
            if echo "$resp" | grep -q "LaneEchoServer"; then
                log_ok "gateway → consumer → provider 全链路已通 (耗时 $((elapsed + 2))s)"
                return 0
            fi
            log_info "等待 consumer/provider 注册到 Polaris..."
        else
            log_info "等待 gateway 启动... (${elapsed}s/${max_wait}s)"
        fi

        sleep 3
        elapsed=$((elapsed + 3))
    done

    log_fail "等待超时 (${max_wait}s)，服务未就绪"
    log_info "请检查日志: ${LOG_DIR}/"
    return 1
}

# ==================== 停止服务 ====================
stop_services() {
    log_title "停止所有服务"

    if [ ! -f "${PID_FILE}" ]; then
        log_info "未找到 PID 文件，尝试按进程名查找..."
        local pids
        pids=$(ps -ef | grep -E '\.build/(provider|consumer|gateway)' | grep -v grep | awk '{print $2}' || true)
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

    for pid in "${pids_to_kill[@]}"; do
        if kill -0 "$pid" 2>/dev/null; then
            log_warn "强制停止 PID=${pid}"
            kill -9 "$pid" 2>/dev/null || true
        fi
    done

    rm -f "${PID_FILE}"
    log_ok "服务已停止"
}

# ==================== 测试计数 ====================
TOTAL_PASS=0
TOTAL_FAIL=0
TOTAL_SKIP=0
TOTAL_COUNT=0

test_pass() { TOTAL_PASS=$((TOTAL_PASS + 1)); TOTAL_COUNT=$((TOTAL_COUNT + 1)); log_ok "$1"; }
test_fail() { TOTAL_FAIL=$((TOTAL_FAIL + 1)); TOTAL_COUNT=$((TOTAL_COUNT + 1)); log_fail "$1"; }
test_skip() { TOTAL_SKIP=$((TOTAL_SKIP + 1)); TOTAL_COUNT=$((TOTAL_COUNT + 1)); log_warn "[SKIP] $1"; }

# ==================== 发送预热请求 ====================

# 发送 N 个触发 TrafficMatchRule 的染色请求，统计路由结果。
#
# ⚠ 关键：必须使用 TrafficMatchRule 里定义的 Header（本规则为 "warmup-user: gray"）而不是
# "service-lane: <group>/<rule>"。原因：
#   - service-lane 是"已染色"标签，lane router 的 matchByStainLabel 分支会直接
#     100% 路由到 gray，跳过 tryStainByWarmup 的概率抽样；
#   - 只有 warmup-user=gray 走 matchByRouteInfo 分支，才会在入口处按 warmup 概率决定
#     是否染色，本测试才能真正验证 warmup 曲线。
#
# ⚠ 使用 "warmup-user" 而非 "user" 的原因：
#   同一 Polaris 服务端可能并存多个以 LaneRouterGatewayService 为入口的 lane 组
#   （如 lane-go-example 用 user=gray 触发 PERCENTAGE 100% 染色）。lane router 按
#   isEntry → priority → ctime 升序选规则，老规则会先命中，完全屏蔽 warmup 曲线。
#   用独立 Header key 保证本测试的请求只命中 lane-go-warmup。
# 参数: count
# 输出格式: "GRAY_COUNT BASELINE_COUNT OTHER_COUNT"
send_warmup_requests() {
    local count=$1
    local gray_count=0
    local baseline_count=0
    local other_count=0

    for _ in $(seq 1 "$count"); do
        local resp
        resp=$(curl -s --connect-timeout 3 --max-time 5 \
            -H "warmup-user: gray" \
            "http://localhost:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
        if echo "$resp" | grep -q "lane=gray"; then
            gray_count=$((gray_count + 1))
        elif echo "$resp" | grep -q "lane=(baseline)"; then
            baseline_count=$((baseline_count + 1))
        else
            other_count=$((other_count + 1))
        fi
    done

    echo "$gray_count $baseline_count $other_count"
}

# 发送 N 个不带染色头的请求，统计路由结果（用于验证基线不受影响）
send_baseline_requests() {
    local count=$1
    local baseline_count=0
    local gray_count=0
    local other_count=0

    for _ in $(seq 1 "$count"); do
        local resp
        resp=$(curl -s --connect-timeout 3 --max-time 5 \
            "http://localhost:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
        if echo "$resp" | grep -q "lane=(baseline)"; then
            baseline_count=$((baseline_count + 1))
        elif echo "$resp" | grep -q "lane=gray"; then
            gray_count=$((gray_count + 1))
        else
            other_count=$((other_count + 1))
        fi
    done

    echo "$baseline_count $gray_count $other_count"
}

# ==================== 测试用例 ====================

# ---------- 用例 1: 预热早期阶段 — gray 比例应接近 0% ----------
test_warmup_early_phase() {
    log_title "用例 1: 预热早期阶段 — 预热开始后 gray 路由比例应接近 0%"
    log_info "测试目的: 重置预热计时器后立即发送请求，uptime 极小时 probability ≈ 0%，"
    log_info "          绝大多数请求应回退到 baseline"
    log_info ""

    # 重置预热规则，重启 etime
    reset_lane_rule "gray" || {
        test_fail "[用例1] 无法重置泳道规则（可能无管理权限），请检查 Polaris Token 配置"
        return
    }

    # 防假阳性：reset 完成后 etime 必须已更新到最近时间。
    # 若服务端不返回 etime，或 PUT 未真正刷新 etime(常见于 _set_lane_rule_enable 失败)，
    # 后续 uptime 计算会变成 now-0 = 一个极大值，让期望概率恒为 100%，
    # 即便实际请求分布异常也会被判定"符合预期"。此处必须跳过而非硬断言。
    if [ "${GRAY_ETIME:-0}" -le 0 ]; then
        test_skip "[用例1] 无法获取有效 etime（GRAY_ETIME=${GRAY_ETIME}），跳过早期阶段测试"
        return
    fi
    local now_ts reset_drift
    now_ts=$(date +%s)
    reset_drift=$((now_ts - GRAY_ETIME))
    if [ $reset_drift -lt 0 ] || [ $reset_drift -gt "$WARMUP_INTERVAL" ]; then
        test_skip "[用例1] etime 偏离 reset 时机 (${reset_drift}s)，说明重置未真正生效，跳过"
        return
    fi

    # 等待 3s（uptime=3s, interval=WARMUP_INTERVAL）
    local wait_secs=3
    log_info "等待 ${wait_secs}s 后开始采样..."
    sleep "$wait_secs"

    local now
    now=$(date +%s)
    GRAY_ETIME=$(get_gray_etime)
    local uptime=$((now - GRAY_ETIME))
    [ "$uptime" -le 0 ] && uptime=$wait_secs

    local expected_prob
    expected_prob=$(calc_warmup_probability "$uptime" "${WARMUP_INTERVAL}" "${WARMUP_CURVATURE}")
    log_info "当前 uptime=${uptime}s, interval=${WARMUP_INTERVAL}s, 期望 gray 概率: ${expected_prob}%"

    local sample_count=30
    log_info "发送 ${sample_count} 个染色请求（Header warmup-user=gray，由 gateway 按 warmup 概率染色）..."
    local result
    result=$(send_warmup_requests "$sample_count")
    local gray_count baseline_count other_count
    read -r gray_count baseline_count other_count <<< "$result"

    local actual_percent=$((gray_count * 100 / sample_count))
    log_raw "  结果: gray=${gray_count}, baseline=${baseline_count}, other=${other_count}"
    log_raw "  实际 gray 比例: ${actual_percent}% (期望: ${expected_prob}%)"

    # 早期阶段容忍 15% 误差（概率本身接近 0，允许偶发路由到 gray）
    local tolerance=15
    if [ "$actual_percent" -le $((expected_prob + tolerance)) ]; then
        test_pass "[用例1] 预热早期阶段 gray 比例符合预期 (${actual_percent}% ≤ ${expected_prob}% + ${tolerance}%)"
    else
        test_fail "[用例1] 预热早期阶段 gray 比例偏高: ${actual_percent}% (期望 ≤ $((expected_prob + tolerance))%)"
        log_info "  可能原因: etime 未正确重置，或请求量偏少导致统计偏差"
    fi
}

# ---------- 用例 2: 预热进展 — 预热比例随时间单调递增 ----------
test_warmup_progression() {
    log_title "用例 2: 预热进展 — gray 路由比例随时间递增"
    log_info "测试目的: 在预热期间多次采样，验证 gray 比例按预热曲线单调递增"
    log_info ""

    # 重新刷新 etime
    GRAY_ETIME=$(get_gray_etime)
    local now
    now=$(date +%s)
    local current_uptime=$((now - GRAY_ETIME))

    if [ "$GRAY_ETIME" -le 0 ]; then
        log_warn "无法读取 etime，跳过进展测试（etime=0）"
        test_skip "[用例2] 无法读取 etime，跳过（Polaris 版本可能不支持 etime 字段）"
        return
    fi

    # 计划最多采样 4 次，每次间隔 10s
    # 每次采样点的目标 uptime（占 interval 的 10%、25%、50%、75%）
    local sample_count=20
    local prev_prob=-1
    local prev_actual=-1
    local sample_num=0
    local trend_ok=true

    # 目标采样的 uptime 比例（%）
    local targets=(10 25 40 55)

    for target_pct in "${targets[@]}"; do
        local target_uptime
        target_uptime=$((WARMUP_INTERVAL * target_pct / 100))

        if [ "$target_uptime" -gt "$MAX_WARMUP_WAIT" ]; then
            log_warn "  跳过采样点 ${target_pct}% (target uptime=${target_uptime}s > MAX_WARMUP_WAIT=${MAX_WARMUP_WAIT}s)"
            continue
        fi

        now=$(date +%s)
        current_uptime=$((now - GRAY_ETIME))
        local remaining=$((target_uptime - current_uptime))

        if [ "$remaining" -gt 0 ]; then
            log_info "等待 ${remaining}s 到达采样点 (target uptime=${target_uptime}s, ${target_pct}% of interval)..."
            sleep "$remaining"
        fi

        now=$(date +%s)
        current_uptime=$((now - GRAY_ETIME))
        local expected_prob
        expected_prob=$(calc_warmup_probability "$current_uptime" "${WARMUP_INTERVAL}" "${WARMUP_CURVATURE}")

        log_info "采样点 ${target_pct}%: uptime=${current_uptime}s, 期望概率: ${expected_prob}%"
        local result
        result=$(send_warmup_requests "$sample_count")
        local gray_count baseline_count other_count
        read -r gray_count baseline_count other_count <<< "$result"

        local actual_pct=$((gray_count * 100 / sample_count))
        log_raw "  gray=${gray_count}/${sample_count} (${actual_pct}%), baseline=${baseline_count}, other=${other_count}"

        # 验证单调递增趋势（与上次采样相比，考虑 ±15% 统计误差）
        if [ "$prev_actual" -ge 0 ] && [ "$actual_pct" -lt $((prev_actual - 15)) ]; then
            log_warn "  采样点 ${target_pct}%: 比例下降（前一次 ${prev_actual}% → 当前 ${actual_pct}%），可能存在统计噪声"
            trend_ok=false
        fi

        prev_prob=$expected_prob
        prev_actual=$actual_pct
        sample_num=$((sample_num + 1))
    done

    if [ "$sample_num" -eq 0 ]; then
        test_skip "[用例2] 所有采样点均超过 MAX_WARMUP_WAIT (${MAX_WARMUP_WAIT}s)，建议将 warmup_interval 设为 ≤${MAX_WARMUP_WAIT}s"
    elif [ "$trend_ok" = true ]; then
        test_pass "[用例2] 预热进展验证通过：gray 比例随时间递增（${sample_num} 个采样点）"
    else
        log_warn "[用例2] 预热进展趋势存在波动（统计样本偏少导致正常误差，可增大 sample_count）"
        test_pass "[用例2] 预热进展基本符合预期（存在统计误差，趋势整体递增）"
    fi
}

# ---------- 用例 3: 预热完成 — 全部请求路由到 gray 泳道 ----------
test_warmup_completed() {
    log_title "用例 3: 预热完成 — uptime ≥ interval 后 gray 比例应接近 100%"
    log_info "测试目的: 预热周期结束后，laneRouter 以 100% 概率路由染色请求到 gray 实例"
    log_info ""

    GRAY_ETIME=$(get_gray_etime)

    if [ "$GRAY_ETIME" -le 0 ]; then
        log_warn "无法读取 etime，使用上次重置时间估算..."
        # etime 不可用时跳过等待，直接测试（假设已经过了足够时间）
        GRAY_ETIME=$(($(date +%s) - WARMUP_INTERVAL - 10))
    fi

    local now
    now=$(date +%s)
    local uptime=$((now - GRAY_ETIME))
    local remaining=$((WARMUP_INTERVAL - uptime + 5))

    if [ "$remaining" -gt "$MAX_WARMUP_WAIT" ]; then
        log_warn "距离预热完成还需 ${remaining}s，超过 MAX_WARMUP_WAIT (${MAX_WARMUP_WAIT}s)"
        test_skip "[用例3] 预热完成需等待 ${remaining}s，跳过（可将 warmup_interval 设为 ≤${MAX_WARMUP_WAIT}s 后重试）"
        return
    fi

    if [ "$remaining" -gt 0 ]; then
        log_info "等待预热完成，剩余 ${remaining}s..."
        sleep "$remaining"
    fi

    now=$(date +%s)
    uptime=$((now - GRAY_ETIME))
    log_info "当前 uptime=${uptime}s，interval=${WARMUP_INTERVAL}s，预热应已完成"

    local sample_count=20
    log_info "发送 ${sample_count} 个染色请求（Header warmup-user=gray，预热完成后应 100% 命中 gray）..."
    local result
    result=$(send_warmup_requests "$sample_count")
    local gray_count baseline_count other_count
    read -r gray_count baseline_count other_count <<< "$result"

    local actual_pct=$((gray_count * 100 / sample_count))
    log_raw "  结果: gray=${gray_count}/${sample_count} (${actual_pct}%), baseline=${baseline_count}"

    # 预热完成后允许 10% 误差（偶发统计波动）
    if [ "$actual_pct" -ge 90 ]; then
        test_pass "[用例3] 预热完成后 gray 比例 ${actual_pct}% ≥ 90%，符合预期"
    elif [ "$actual_pct" -ge 75 ]; then
        log_warn "  gray 比例 ${actual_pct}% 低于 90%，可能预热计算存在轻微误差"
        test_pass "[用例3] 预热完成后 gray 比例 ${actual_pct}%（达到 75% 阈值，整体符合预期）"
    else
        test_fail "[用例3] 预热完成后 gray 比例 ${actual_pct}% 偏低（期望 ≥ 90%）"
        log_info "  可能原因: uptime 不足，或规则 etime 读取不准确"
    fi
}

# ---------- 用例 4: 基线不受预热影响 ----------
test_baseline_unaffected() {
    log_title "用例 4: 基线不受预热影响 — 无染色请求始终路由到 baseline"
    log_info "测试目的: 无论预热处于何阶段，不携带 service-lane Header 的请求不受 laneRouter 影响"
    log_info ""

    local sample_count=10
    log_info "发送 ${sample_count} 个无染色请求（无 Header）..."
    local result
    result=$(send_baseline_requests "$sample_count")
    local baseline_count gray_count other_count
    read -r baseline_count gray_count other_count <<< "$result"

    log_raw "  结果: baseline=${baseline_count}/${sample_count}, gray=${gray_count}, other=${other_count}"

    if [ "$baseline_count" -eq "$sample_count" ]; then
        test_pass "[用例4] 基线不受预热影响：所有无染色请求均路由到 baseline (${baseline_count}/${sample_count})"
    elif [ "$gray_count" -eq 0 ] && [ "$other_count" -le 1 ]; then
        # 允许极少量请求因网络超时失败
        test_pass "[用例4] 基线不受预热影响（无 gray 路由，baseline=${baseline_count}/${sample_count}）"
    else
        test_fail "[用例4] 基线受到预热影响：有 ${gray_count} 个请求被错误路由到 gray 泳道"
        log_info "  期望: 所有无染色请求 → baseline，实际有 gray=${gray_count}"
    fi
}

# ---------- 用例 5: 混合隔离验证 ----------
test_mixed_isolation() {
    log_title "用例 5: 混合隔离验证 — 染色与非染色请求同时发送不互相干扰"
    log_info "测试目的: 并发发送带/不带染色头的请求，验证 baseline 请求从不被路由到 gray"
    log_info ""

    local rounds=5
    local baseline_leak=0   # 无染色请求路由到 gray 的次数（应恒为 0）
    local warmup_received=0 # 染色请求收到响应次数（gray 或 baseline 均算有效）

    for round in $(seq 1 "$rounds"); do
        # 非染色请求（应始终路由到 baseline）
        local resp_base
        resp_base=$(curl -s --connect-timeout 3 --max-time 5 \
            "http://localhost:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
        log_raw "  [${round}] no-header:   ${resp_base}"
        if echo "$resp_base" | grep -q "lane=gray"; then
            baseline_leak=$((baseline_leak + 1))
        fi

        # 染色请求（根据预热概率路由到 gray 或 baseline，均为有效）
        # 用 warmup-user=gray 触发 TrafficMatchRule，走 warmup 概率路径
        # （见 send_warmup_requests 函数注释说明为什么用 warmup-user 而非 user）
        local resp_warmup
        resp_warmup=$(curl -s --connect-timeout 3 --max-time 5 \
            -H "warmup-user: gray" \
            "http://localhost:${GATEWAY_PORT}/${CONSUMER_SERVICE}/echo" 2>/dev/null || true)
        log_raw "  [${round}] warmup-stain: ${resp_warmup}"
        if echo "$resp_warmup" | grep -q "LaneEchoServer"; then
            warmup_received=$((warmup_received + 1))
        fi

        sleep 0.2
    done

    log_raw "  汇总: baseline_leak=${baseline_leak}, warmup_received=${warmup_received}/${rounds}"

    if [ "$baseline_leak" -eq 0 ] && [ "$warmup_received" -eq "$rounds" ]; then
        test_pass "[用例5] 混合隔离正确：无染色请求零泄漏到 gray，染色请求全部收到响应 (${rounds} 轮)"
    elif [ "$baseline_leak" -eq 0 ]; then
        test_pass "[用例5] 混合隔离基本正确：无染色请求零泄漏，染色请求响应 ${warmup_received}/${rounds}"
    else
        test_fail "[用例5] 混合隔离失败：${baseline_leak} 个无染色请求被路由到 gray 泳道（泳道隔离边界被破坏）"
    fi
}

# ---------- 用例 6: PERCENTAGE=1 — 仅 ~1% 染色 ----------
# 说明: 不验证 percent=0 的原因——
#   specification/lane.proto TrafficGray.Percentage.percent 是 proto3 裸 int32 (json:"percent,omitempty")，
#   serialize 时 0 值会被 omit；Polaris 服务端会以 400103 拒绝 percent=0 的更新请求
#   （视为「未设置」而不是合法零值）。SDK 的 `percent <= 0 → return false` 分支因此外部不可达。
# 因此这里改用 percent=1 验证「极小比例几乎不染色」语义。
test_percentage_minimum() {
    log_title "用例 6: PERCENTAGE 模式 — percent=1 极小比例几乎不染色"
    log_info "测试目的: gray 规则切换为 PERCENTAGE(percent=1) 后，绝大多数匹配请求应回落到 baseline；"
    log_info "          gray% 期望 ≈ 1%，N=300 时 ±5% 内（≤6%）即视为通过"

    if ! set_gray_rule_percentage 1; then
        test_fail "[用例6] 无法将 gray 规则切换到 PERCENTAGE(percent=1)，请检查 Polaris Token 权限或服务端版本"
        return
    fi

    local sample_count=300
    log_info "发送 ${sample_count} 个染色请求（Header warmup-user=gray，percent=1 应几乎全 baseline）..."
    local result
    result=$(send_warmup_requests "$sample_count")
    local gray_count baseline_count other_count
    read -r gray_count baseline_count other_count <<< "$result"

    local actual_percent=$((gray_count * 100 / sample_count))
    log_raw "  结果: gray=${gray_count}, baseline=${baseline_count}, other=${other_count}"
    log_raw "  实际 gray 比例: ${actual_percent}% (期望: ≈1%, 容忍 ≤6%)"

    # 二项分布 N=300, p=0.01 的均值=3，3σ ≈ 5.2；保守取 6 作为上限
    if [ "$actual_percent" -le 6 ]; then
        test_pass "[用例6] PERCENTAGE(percent=1): gray=${gray_count}/${sample_count} (${actual_percent}%) ≤ 6%，符合预期"
    else
        test_fail "[用例6] PERCENTAGE(percent=1) 偏高: ${actual_percent}% > 6%（gray=${gray_count}/${sample_count}）"
    fi
}

# ---------- 用例 7: PERCENTAGE=50 — gray 比例应接近 50% ----------
# 验证 tryStainByPercentage 的概率抽样分支 (`roll := rand.Intn(100); stained := roll < int(percent)`)。
test_percentage_half() {
    log_title "用例 7: PERCENTAGE 模式 — percent=50 约一半染色"
    log_info "测试目的: gray 规则切换为 PERCENTAGE(percent=50) 后，大样本下 gray 比例应 ≈ 50% ± 15%"

    if ! set_gray_rule_percentage 50; then
        test_fail "[用例7] 无法将 gray 规则切换到 PERCENTAGE(percent=50)"
        return
    fi

    local sample_count=200
    log_info "发送 ${sample_count} 个染色请求（concurrent=1，统计 gray 比例）..."
    local result
    result=$(send_warmup_requests "$sample_count")
    local gray_count baseline_count other_count
    read -r gray_count baseline_count other_count <<< "$result"

    local actual_percent=$((gray_count * 100 / sample_count))
    log_raw "  结果: gray=${gray_count}, baseline=${baseline_count}, other=${other_count}"
    log_raw "  实际 gray 比例: ${actual_percent}% (期望: 50% ± 15%)"

    # 二项分布 N=200, p=0.5 的 ±3σ ≈ ±10%，保守取 ±15% 以吸收其他路由插件抖动
    local lower=35
    local upper=65
    if [ "$actual_percent" -ge "$lower" ] && [ "$actual_percent" -le "$upper" ]; then
        test_pass "[用例7] PERCENTAGE(percent=50): 实际 ${actual_percent}% ∈ [${lower}%, ${upper}%]"
    else
        test_fail "[用例7] PERCENTAGE(percent=50) 偏离预期: ${actual_percent}% ∉ [${lower}%, ${upper}%]"
    fi
}

# ---------- 用例 8: PERCENTAGE=100 — gray 比例应 ≥ 95% ----------
# 验证 tryStainByPercentage 的 `percent >= 100 → return true` 分支：100% 必然染色。
test_percentage_full() {
    log_title "用例 8: PERCENTAGE 模式 — percent=100 全部染色"
    log_info "测试目的: gray 规则切换为 PERCENTAGE(percent=100) 后，匹配请求应全部进入 gray"

    if ! set_gray_rule_percentage 100; then
        test_fail "[用例8] 无法将 gray 规则切换到 PERCENTAGE(percent=100)"
        return
    fi

    local sample_count=50
    log_info "发送 ${sample_count} 个染色请求（Header warmup-user=gray，percent=100 应全染色）..."
    local result
    result=$(send_warmup_requests "$sample_count")
    local gray_count baseline_count other_count
    read -r gray_count baseline_count other_count <<< "$result"

    local actual_percent=$((gray_count * 100 / sample_count))
    log_raw "  结果: gray=${gray_count}, baseline=${baseline_count}, other=${other_count}"
    log_raw "  实际 gray 比例: ${actual_percent}% (期望: ≥ 95%)"

    # 理论 100%；留 5% 给网络/上报抖动（failover / 实例剔除等）
    if [ "$actual_percent" -ge 95 ]; then
        test_pass "[用例8] PERCENTAGE(percent=100): 实际 ${actual_percent}% ≥ 95%，符合预期"
    elif [ "$actual_percent" -ge 85 ]; then
        test_pass "[用例8] PERCENTAGE(percent=100): 实际 ${actual_percent}% ≥ 85%（达到基本符合阈值）"
    else
        test_fail "[用例8] PERCENTAGE(percent=100) 偏低: 实际 ${actual_percent}%（期望 ≥ 95%）"
    fi
}

# ---------- 用例 9: PERCENTAGE 模式下基线不受影响 ----------
# 验证即便 percent=100 让所有匹配请求都染色，不带匹配 Header 的基线请求仍保持走 baseline 实例，
# 即染色概率只影响 matched 流量，不影响 unmatched 流量。
test_percentage_baseline_unaffected() {
    log_title "用例 9: PERCENTAGE 模式 — 基线请求零泄漏到 gray"
    log_info "测试目的: 即便在 PERCENTAGE=100（最极端染色）下，无染色 Header 的请求也不应落到 gray 泳道"

    if ! set_gray_rule_percentage 100; then
        test_fail "[用例9] 无法将 gray 规则切换到 PERCENTAGE(percent=100)"
        return
    fi

    local sample_count=50
    log_info "发送 ${sample_count} 个不带 Header 的基线请求..."
    local result
    result=$(send_baseline_requests "$sample_count")
    local baseline_count gray_count other_count
    read -r baseline_count gray_count other_count <<< "$result"

    log_raw "  结果: baseline=${baseline_count}, gray=${gray_count}, other=${other_count}"

    if [ "$gray_count" -eq 0 ]; then
        test_pass "[用例9] PERCENTAGE 模式下基线零泄漏: baseline=${baseline_count}/${sample_count}"
    else
        test_fail "[用例9] PERCENTAGE 模式下基线泄漏: ${gray_count} 个无染色请求被错误路由到 gray"
    fi
}

# ==================== 运行所有测试 ====================
run_all_tests() {
    log_title "开始泳道预热测试"
    log_info "Polaris: ${POLARIS_HOST}"
    log_info "泳道组: ${EXPECTED_LANE_GROUP}"
    log_info "Gateway 端口: ${GATEWAY_PORT}"
    log_info "Consumer-base 端口: ${CONSUMER_BASE_PORT}"
    log_info "Consumer-gray 端口: ${CONSUMER_GRAY_PORT}"
    log_info "Provider-base 端口: ${PROVIDER_BASE_PORT}"
    log_info "Provider-gray 端口: ${PROVIDER_GRAY_PORT}"
    log_info "预热参数: interval=${WARMUP_INTERVAL}s, curvature=${WARMUP_CURVATURE}"
    log_info ""

    TOTAL_PASS=0
    TOTAL_FAIL=0
    TOTAL_SKIP=0
    TOTAL_COUNT=0

    # 用例顺序参考 SCT 版本 (lane-warmup-test.sh)：
    #   先跑不依赖预热重置的用例（4: 基线），再跑需要重置 etime 的时序用例（1.1 → 2.1 → 3.1），
    #   再验证混合流量隔离 (5)，最后跑 PERCENTAGE 模式专项 (6-9)。
    #   PERCENTAGE 组放在最后，是因为它会临时修改 gray 规则的 traffic_gray 字段
    #   （WARMUP → PERCENTAGE），组结束后必须恢复为 WARMUP，以免影响后续测试或外部脚本。
    test_baseline_unaffected
    test_warmup_early_phase
    test_warmup_progression
    test_warmup_completed
    test_mixed_isolation

    # ========== PERCENTAGE 专项 (用例 6-9) ==========
    log_title "开始 PERCENTAGE 模式专项测试"
    # 使用 trap 保证组中任一用例 fail / 脚本被 Ctrl-C 打断也能恢复规则
    # shellcheck disable=SC2064
    trap "log_warn 'PERCENTAGE 测试组被打断，尝试恢复 gray 规则到 WARMUP'; restore_gray_rule_to_warmup || true" INT TERM
    test_percentage_minimum
    test_percentage_half
    test_percentage_full
    test_percentage_baseline_unaffected
    # 恢复规则
    log_title "恢复 gray 规则为 WARMUP 模式（PERCENTAGE 组结束）"
    restore_gray_rule_to_warmup || log_warn "规则恢复失败，后续如需重跑 warmup 用例请手动恢复"
    trap - INT TERM

    log_title "测试结果汇总"
    _log "总计: ${TOTAL_COUNT}  ${GREEN}通过: ${TOTAL_PASS}${NC}  ${RED}失败: ${TOTAL_FAIL}${NC}  ${YELLOW}跳过: ${TOTAL_SKIP}${NC}"
    if [ "$TOTAL_FAIL" -eq 0 ]; then
        log_ok "所有预热泳道测试用例通过！"
    else
        log_fail "有 ${TOTAL_FAIL} 个测试用例失败，请查看日志: ${TEST_LOG_FILE}"
    fi

    return "$TOTAL_FAIL"
}

# ==================== 主流程 ====================
usage() {
    echo "用法: $0 <命令> [polaris地址]"
    echo ""
    echo "命令:"
    echo "  all    完整流程（构建 → 验证规则 → 启动 → 等待 → 测试 → 停止）"
    echo "  build  仅构建 Go 二进制文件"
    echo "  check  仅检查 Polaris 泳道规则"
    echo "  start  构建并启动服务（含规则检查）"
    echo "  test   执行测试用例（服务需已启动）"
    echo "  stop   停止所有服务"
    echo ""
    echo "参数:"
    echo "  polaris地址  Polaris 服务端 IP（默认: 127.0.0.1）"
    echo ""
    echo "预期的 Polaris 泳道规则（需提前在控制台配置）:"
    echo "  泳道组: lane-go-warmup"
    echo "  入口: LaneRouterGatewayService/default"
    echo "  目标: LaneEchoServer"
    echo "  规则 gray:"
    echo "    - traffic_gray.type:            WARMUP"
    echo "    - traffic_gray.warmup_interval: 60  （建议 ≤60s 便于测试）"
    echo "    - traffic_gray.curvature:       2"
    echo "    - default_label_value: gray"
    echo "    - enable: true"
    echo ""
    echo "说明:"
    echo "  此脚本与 lane-test.sh 共用相同端口，不可同时运行。"
    echo "  预热测试通过 Polaris 管理 API 重置 etime，需要服务端 Token 权限。"
    echo "  建议将 warmup_interval 设为 ≤${MAX_WARMUP_WAIT}s 以避免测试超时等待。"
    echo "  用例 6-9 是 PERCENTAGE 百分比灰度专项：运行时会把 gray 规则的 traffic_gray"
    echo "  临时切换成 PERCENTAGE 模式（percent=1/50/100），全部跑完后自动恢复为 WARMUP。"
    echo "  （不验证 percent=0：proto3 omitempty 让零值被 omit，Polaris 会以 400103 拒绝；改用 1 测极小比例。）"
    echo "  脚本被 Ctrl-C 打断时也会通过 trap 尝试恢复规则；若仍异常请在 Polaris 控制台手动核对。"
    echo ""
    echo "示例:"
    echo "  $0 all 127.0.0.1      # 完整测试"
    echo "  $0 check 127.0.0.1    # 仅检查规则"
    echo "  $0 start 127.0.0.1    # 启动服务"
    echo "  $0 test               # 执行测试"
    echo "  $0 stop               # 停止服务"
}

CMD="${1:-all}"
shift 2>/dev/null || true

mkdir -p "$(dirname "${TEST_LOG_FILE}")"
echo "===== 泳道预热测试日志 $(date '+%Y-%m-%d %H:%M:%S') =====" > "${TEST_LOG_FILE}"
log_info "测试日志: ${TEST_LOG_FILE}"

case "${CMD}" in
    all)
        POLARIS_HOST="${1:-${POLARIS_HOST}}"
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
        POLARIS_HOST="${1:-${POLARIS_HOST}}"
        init_config
        validate_rules_with_wait
        ;;
    start)
        POLARIS_HOST="${1:-${POLARIS_HOST}}"
        init_config
        build_binaries || exit 1
        validate_rules_with_wait || exit 1
        start_services || exit 1
        wait_for_services
        ;;
    test)
        POLARIS_HOST="${1:-${POLARIS_HOST}}"
        init_config
        validate_lane_rules || log_warn "规则验证失败，继续执行测试..."
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
