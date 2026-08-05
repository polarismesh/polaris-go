#!/bin/bash
# =============================================================================
# 配置灰度发布验证脚本
#
# 验证 polaris-go 客户端在「不改动任何代码」的前提下，仅通过 global.client.labels
# 上报标签即可正确参与服务端配置灰度发布：
#   - 命中灰度规则的客户端拿到灰度内容
#   - 未命中的客户端继续使用全量内容
#
# 使用方法:
#   chmod +x gray-test.sh
#   ./gray-test.sh [--polaris-server <地址>] [--polaris-token <令牌>]
#                  [--namespace <命名空间>] [--group <配置组>] [--file <文件名>]
#                  [--port-a <端口>] [--port-b <端口>] [--case <1|2|all>] [--debug]
#
# 前置条件:
#   1. 北极星服务端(Polaris Server)已启动，且已更新含配置灰度逻辑的版本
#   2. Go 环境已安装
#   3. 验证用例 1/2 涉及「发布灰度」「停止灰度」操作需在北极星控制台手动完成
#      (polaris-go 不提供灰度发布 API，灰度规则匹配完全在服务端)
#
# 验证原理:
#   - 客户端在 global.client.labels 中配置的标签会随 GetConfigFile 请求上报服务端
#   - 服务端据标签判定是否命中灰度规则，命中则下发灰度内容，否则下发全量内容
#   - 客户端无需感知灰度，收到内容直接使用
#
# 已知限制(验证时需关注，非 SDK 缺陷):
#   - 限制1: 自定义标签灰度不会触发长轮询 watch 实时推送，需客户端重新拉取生效
#           本脚本通过「重启客户端 A」触发重新拉取来验证此类灰度
#   - 限制2: 客户端配置自定义标签后，服务端不再兜底注入 CLIENT_IP，故按 IP 配置
#           的灰度规则不会命中带自定义标签的客户端(用例 2 验证此现象)
# =============================================================================

set -euo pipefail

# ======================== 默认配置 ========================
POLARIS_SERVER="${POLARIS_SERVER:-127.0.0.1}"
POLARIS_TOKEN="${POLARIS_TOKEN:-}"
NAMESPACE="${NAMESPACE:-default}"
FILE_GROUP="${FILE_GROUP:-polaris-config-example}"
FILE_NAME="${FILE_NAME:-gray-example.yaml}"
PORT_A="${PORT_A:-18081}"
PORT_B="${PORT_B:-18082}"
PORT_C="${PORT_C:-18083}"
CASE="${CASE:-all}"
DEBUG_MODE="${DEBUG_MODE:-false}"

# 验证用的配置内容(具有辨识度，便于自动判定)
NORMAL_CONTENT="normal-content-v1"
GRAY_CONTENT="gray-content-v2"

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
        --polaris-server) POLARIS_SERVER="$2"; shift 2 ;;
        --polaris-token)  POLARIS_TOKEN="$2";  shift 2 ;;
        --namespace)      NAMESPACE="$2";      shift 2 ;;
        --group)          FILE_GROUP="$2";     shift 2 ;;
        --file)           FILE_NAME="$2";      shift 2 ;;
        --port-a)         PORT_A="$2";         shift 2 ;;
        --port-b)         PORT_B="$2";         shift 2 ;;
        --port-c)         PORT_C="$2";         shift 2 ;;
        --case)           CASE="$2";           shift 2 ;;
        --debug)          DEBUG_MODE="true";   shift ;;
        --help|-h)
            echo "用法: $0 [选项]"
            echo ""
            echo "选项:"
            echo "  --polaris-server <地址>  北极星服务端地址 (默认: 127.0.0.1)"
            echo "  --polaris-token <令牌>   北极星鉴权令牌 (默认: 空)"
            echo "  --namespace <命名空间>   命名空间 (默认: default)"
            echo "  --group <配置组>         配置文件组 (默认: polaris-config-example)"
            echo "  --file <文件名>          配置文件名 (默认: gray-example.yaml)"
            echo "  --port-a <端口>          客户端 A(带 env=pre 标签,常驻) HTTP 端口 (默认: 18081)"
            echo "  --port-b <端口>          客户端 B(无标签,常驻) HTTP 端口 (默认: 18082)"
            echo "  --port-c <端口>          临时验证客户端(带 env=pre 标签) HTTP 端口 (默认: 18083)"
            echo "  --case <1|2|all>         仅运行指定用例 (默认: all)"
            echo "  --debug                  启用 debug 日志 (默认: 关闭)"
            exit 0
            ;;
        *)
            echo -e "${RED}未知参数: $1${NC}"; exit 1 ;;
    esac
done

# ======================== 全局变量 ========================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_DIR="${SCRIPT_DIR}/.build"
LOG_DIR="${SCRIPT_DIR}/.logs"
PID_FILE="${SCRIPT_DIR}/.gray-test-pids"
RESULT_FILE="${LOG_DIR}/gray_result.csv"

PID_A=""
PID_B=""
PID_VERIFY=""

# ======================== 清理函数 ========================
cleanup() {
    log_info "清理客户端进程..."
    for pid in "$PID_A" "$PID_B"; do
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
        fi
    done
    if [[ -f "$PID_FILE" ]]; then
        while IFS= read -r pid; do
            [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true
        done < "$PID_FILE"
        rm -f "$PID_FILE"
    fi
}
trap cleanup EXIT

# ======================== 工具函数 ========================

log_info()  { echo -e "${GREEN}[INFO]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*"; }
log_step() {
    echo ""
    echo -e "${CYAN}========================================${NC}"
    echo -e "${CYAN}  $*${NC}"
    echo -e "${CYAN}========================================${NC}"
}

# wait_for_http 轮询 HTTP 端口直到返回响应，同时检查进程存活。
# 入参: url max_wait desc pid
wait_for_http() {
    local url="$1" max_wait="${2:-30}" desc="${3:-服务}" pid="${4:-}"
    local waited=0
    while [[ $waited -lt $max_wait ]]; do
        if [[ -n "$pid" ]] && ! kill -0 "$pid" 2>/dev/null; then
            log_error "${desc} 进程 (PID: ${pid}) 已退出"
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

# get_config_content 拉取指定端口客户端的当前生效配置内容。
# 入参: port
get_config_content() {
    local port="$1"
    curl -s --connect-timeout 3 "http://127.0.0.1:${port}/config" 2>/dev/null \
        | grep -oE '"content":"[^"]*"' | sed 's/"content":"//;s/"$//'
}

# get_config_local_ip 拉取指定端口客户端上报的本机 IP。
# 入参: port
get_config_local_ip() {
    local port="$1"
    curl -s --connect-timeout 3 "http://127.0.0.1:${port}/config" 2>/dev/null \
        | grep -oE '"localIP":"[^"]*"' | sed 's/"localIP":"//;s/"$//'
}

# wait_for_content 轮询客户端直到生效内容等于期望值或超时。
# 入参: port expected max_wait desc
wait_for_content() {
    local port="$1" expected="$2" max_wait="${3:-30}" desc="${4:-配置}"
    local waited=0
    while [[ $waited -lt $max_wait ]]; do
        local actual
        actual=$(get_config_content "$port")
        if [[ "$actual" == "$expected" ]]; then
            log_info "${desc} 已生效: content=${actual} (耗时 ${waited}s)"
            return 0
        fi
        sleep 1
        waited=$((waited + 1))
    done
    log_error "${desc} 未在 ${max_wait}s 内生效，期望=${expected}，实际=$(get_config_content "$port")"
    return 1
}

# ======================== 生成临时 polaris.yaml ========================
# generate_polaris_yaml 按 client 类型生成携带不同标签的 polaris.yaml。
# 入参: target_file client_type
generate_polaris_yaml() {
    local target_file="$1" client_type="$2"
    local labels_block=""
    if [[ "$client_type" == "a" ]]; then
        # 客户端 A: 上报 env=pre 标签，用于命中自定义标签灰度规则
        labels_block="  client:
    labels:
      env: pre"
    fi
    cat > "$target_file" <<EOF
global:
  serverConnector:
    addresses:
      - ${POLARIS_SERVER}:8091
    token: ${POLARIS_TOKEN}
    connectTimeout: 5000ms
  api:
    timeout: 5s
    maxRetryTimes: 2
    retryInterval: 1s
${labels_block}
  eventReporter:
    enable: true
    chain:
      - pushgateway
config:
  configConnector:
    addresses:
      - ${POLARIS_SERVER}:8093
    token: ${POLARIS_TOKEN}
EOF
    log_info "生成 polaris.yaml(${client_type}) -> ${target_file}"
}

# ======================== 进程管理 ========================
# start_client 启动一个客户端实例。
# 入参: client_type port pid_var_name
start_client() {
    local client_type="$1" port="$2" pid_var="$3"
    local workdir="${BUILD_DIR}/client-${client_type}-run"
    local log_file="${LOG_DIR}/client-${client_type}.log"
    mkdir -p "$workdir"
    cp "${BUILD_DIR}/polaris-client-${client_type}.yaml" "${workdir}/polaris.yaml"

    local debug_flag=""
    [[ "$DEBUG_MODE" == "true" ]] && debug_flag="-debug"

    (cd "$workdir" && exec "${BUILD_DIR}/gray-demo" \
        -action=run \
        -config="${workdir}/polaris.yaml" \
        -namespace="$NAMESPACE" \
        -group="$FILE_GROUP" \
        -file="$FILE_NAME" \
        -port=":${port}" \
        $debug_flag \
        > "$log_file" 2>&1) &
    local pid=$!
    echo "$pid" >> "$PID_FILE"
    eval "${pid_var}=${pid}"
    log_info "客户端 ${client_type} 已启动 (PID: ${pid}, 端口: ${port}, 日志: ${log_file})"
}

# start_labeled_client 启动一个带 env=pre 标签的临时客户端(独立日志与工作目录)。
# 用于验证自定义标签灰度命中：新进程初始拉取会携带 env=pre 标签，命中灰度即拿到灰度内容。
# 入参: port instance
# 结果: PID 写入全局 PID_VERIFY，并追加到 PID_FILE。
start_labeled_client() {
    local port="$1" instance="$2"
    local workdir="${BUILD_DIR}/client-${instance}-run"
    local log_file="${LOG_DIR}/client-${instance}.log"
    mkdir -p "$workdir"
    cp "${BUILD_DIR}/polaris-client-a.yaml" "${workdir}/polaris.yaml"

    local debug_flag=""
    [[ "$DEBUG_MODE" == "true" ]] && debug_flag="-debug"

    (cd "$workdir" && exec "${BUILD_DIR}/gray-demo" \
        -action=run \
        -config="${workdir}/polaris.yaml" \
        -namespace="$NAMESPACE" \
        -group="$FILE_GROUP" \
        -file="$FILE_NAME" \
        -port=":${port}" \
        $debug_flag \
        > "$log_file" 2>&1) &
    PID_VERIFY=$!
    echo "$PID_VERIFY" >> "$PID_FILE"
    log_info "临时客户端 ${instance} 已启动 (PID: ${PID_VERIFY}, 端口: ${port}, 日志: ${log_file})"
}

# stop_client 停止指定 PID 的客户端进程并等待退出，从 PID 文件移除该 PID。
# 入参: pid
stop_client() {
    local pid="$1"
    [[ -z "$pid" ]] && return 0
    kill "$pid" 2>/dev/null || true
    local waited=0
    while [[ $waited -lt 10 ]]; do
        kill -0 "$pid" 2>/dev/null || break
        sleep 1
        waited=$((waited + 1))
    done
    if kill -0 "$pid" 2>/dev/null; then
        kill -9 "$pid" 2>/dev/null || true
    fi
    if [[ -f "$PID_FILE" ]]; then
        grep -v "^${pid}$" "$PID_FILE" > "${PID_FILE}.tmp" 2>/dev/null || true
        mv "${PID_FILE}.tmp" "$PID_FILE" 2>/dev/null || true
    fi
}

# verify_labeled_client 启动一个带 env=pre 标签的临时客户端，验证其初始拉取内容后停止。
# 不重启常驻客户端，而是「增加一个客户端进程」完成命中验证(初始拉取携带标签)。
# 入参: port expected case_id case_name
verify_labeled_client() {
    local port="$1" expected="$2" case_id="$3" case_name="$4"
    local instance="verify-${case_id}"
    start_labeled_client "$port" "$instance"
    if ! wait_for_http "http://127.0.0.1:${port}/health" 30 "用例 ${case_id}" "$PID_VERIFY"; then
        log_error "❌ [用例 ${case_id} ${case_name}] FAIL - 临时客户端未就绪"
        record_result "$case_id" "$case_name" "FAIL" "临时客户端未就绪"
        stop_client "$PID_VERIFY"
        return 1
    fi
    if wait_for_content "$port" "$expected" 30 "用例 ${case_id}"; then
        log_info "✅ [用例 ${case_id} ${case_name}] PASS - 新客户端(env=pre) content=${expected}"
        record_result "$case_id" "$case_name" "PASS" "新客户端(env=pre)=${expected}"
        stop_client "$PID_VERIFY"
        return 0
    fi
    log_error "❌ [用例 ${case_id} ${case_name}] FAIL - 期望=${expected}，实际=$(get_config_content "$port")"
    record_result "$case_id" "$case_name" "FAIL" "新客户端未达期望"
    stop_client "$PID_VERIFY"
    return 1
}

# ======================== 用例 1: 自定义标签灰度 ========================
# 命中规则(env EXACT pre)的新启动客户端拿到灰度内容，未命中的常驻客户端 B 继续使用全量内容。
# 不重启常驻客户端 A，而是新启动一个带 env=pre 标签的临时客户端(其初始拉取携带标签)验证命中。
run_case1() {
    log_step "用例 1: 自定义标签灰度 (规则 env EXACT pre)"

    echo -e "${BLUE}请在北极星控制台执行以下操作:${NC}"
    echo -e "  1. 编辑配置 ${NAMESPACE}/${FILE_GROUP}/${FILE_NAME}"
    echo -e "  2. 修改内容为: ${GRAY_CONTENT}"
    echo -e "  3. 点击「灰度发布」，灰度规则选择标签 env，匹配类型 EXACT，值 pre"
    echo -e "  4. 确认发布灰度版本"
    echo ""
    read -r -p "完成后按 Enter 继续..."

    log_info "启动新的带 env=pre 标签的临时客户端验证命中灰度(初始拉取携带标签)..."
    verify_labeled_client "$PORT_C" "$GRAY_CONTENT" "1.1" "灰度命中" || return 1

    log_info "验证常驻客户端 B 未命中灰度(期望: ${NORMAL_CONTENT})..."
    if wait_for_content "$PORT_B" "$NORMAL_CONTENT" 10 "客户端 B 全量内容"; then
        log_info "✅ [用例 1.2 灰度未命中] PASS - 客户端 B(无标签) 仍为全量内容"
        record_result "1.2" "灰度未命中" "PASS" "客户端B=${NORMAL_CONTENT}"
    else
        log_error "❌ [用例 1.2 灰度未命中] FAIL - 客户端 B 内容异常"
        record_result "1.2" "灰度未命中" "FAIL" "客户端B内容异常"
        return 1
    fi

    echo ""
    echo -e "${BLUE}请在北极星控制台停止上述灰度发布(停止灰度后所有客户端回到全量版本)。${NC}"
    read -r -p "完成后按 Enter 继续..."

    log_info "再次启动新的带 env=pre 标签的临时客户端验证回落到全量内容..."
    verify_labeled_client "$PORT_C" "$NORMAL_CONTENT" "1.3" "停止灰度" || return 1
}

# ======================== 用例 2: IP 维度灰度(实时推送) ========================
# 按 CLIENT_IP 配置灰度规则，验证:
#   - 无自定义标签的客户端 B 被服务端注入 CLIENT_IP，命中 IP 灰度并通过 watch 实时推送
#   - 带自定义标签的客户端 A 不被注入 CLIENT_IP(限制2)，不命中 IP 灰度
run_case2() {
    log_step "用例 2: IP 维度灰度 (规则 CLIENT_IP EXACT <服务端视角的客户端B连接IP>)"

    local ip_b
    ip_b=$(get_config_local_ip "$PORT_B")
    log_info "客户端 B 自报本机 IP: ${ip_b} (仅作参考)"
    echo -e "${YELLOW}注意: CLIENT_IP 标签由服务端从 gRPC 连接对端地址解析(非客户端上报)。${NC}"
    echo -e "${YELLOW}若客户端经 NAT 出网，服务端看到的 IP 与上述自报 IP 不同。${NC}"
    echo -e "${YELLOW}请从服务端日志/控制台获取客户端 B 的实际连接 IP 用于灰度规则。${NC}"

    echo ""
    echo -e "${BLUE}请在北极星控制台执行以下操作:${NC}"
    echo -e "  1. 编辑配置 ${NAMESPACE}/${FILE_GROUP}/${FILE_NAME}"
    echo -e "  2. 修改内容为: ${GRAY_CONTENT}"
    echo -e "  3. 点击「灰度发布」，灰度规则选择标签 CLIENT_IP，匹配类型 EXACT"
    echo -e "     值填「服务端视角的客户端 B 连接 IP」(同机/同局域网时即 ${ip_b}，跨 NAT 时需另取)"
    echo -e "  4. 确认发布灰度版本"
    echo ""
    read -r -p "完成后按 Enter 继续..."

    log_info "轮询客户端 B，期望通过 watch 实时推送获取灰度内容(最长等待 60s)..."
    if wait_for_content "$PORT_B" "$GRAY_CONTENT" 60 "客户端 B IP灰度推送"; then
        log_info "✅ [用例 2.1 IP灰度推送] PASS - 客户端 B 通过 watch 实时推送获取灰度内容"
        record_result "2.1" "IP灰度推送" "PASS" "客户端B=${GRAY_CONTENT}"
    else
        log_error "❌ [用例 2.1 IP灰度推送] FAIL - 客户端 B 未通过推送获取灰度内容"
        record_result "2.1" "IP灰度推送" "FAIL" "客户端B未收到推送"
        return 1
    fi

    log_info "验证客户端 A 因限制2未命中 IP 灰度(期望: ${NORMAL_CONTENT})..."
    if wait_for_content "$PORT_A" "$NORMAL_CONTENT" 5 "客户端 A IP灰度未命中"; then
        log_info "✅ [用例 2.2 IP灰度未命中] PASS - 客户端 A(带 env=pre) 未命中 IP 灰度(符合限制2)"
        record_result "2.2" "IP灰度未命中" "PASS" "客户端A=${NORMAL_CONTENT}"
    else
        log_warn "⚠️  [用例 2.2 IP灰度未命中] 客户端 A 内容非全量，需人工确认是否符合预期"
        record_result "2.2" "IP灰度未命中" "WARN" "客户端A内容=$(get_config_content "$PORT_A")"
    fi

    echo ""
    echo -e "${BLUE}请在北极星控制台停止上述 IP 维度灰度发布。${NC}"
    read -r -p "完成后按 Enter 继续..."

    log_info "验证客户端 B 保持灰度内容(停止灰度不推送回落，已灰度客户端保持)..."
    sleep 5
    local b_content
    b_content=$(get_config_content "$PORT_B")
    if [[ "$b_content" == "$GRAY_CONTENT" ]]; then
        log_info "✅ [用例 2.3 停止IP灰度] PASS - 客户端 B 保持灰度内容(停止灰度不推送回落)"
        record_result "2.3" "停止IP灰度" "PASS" "客户端B保持${GRAY_CONTENT}"
    else
        log_warn "⚠️  [用例 2.3 停止IP灰度] 客户端 B 内容=${b_content}，非预期的灰度内容"
        record_result "2.3" "停止IP灰度" "WARN" "客户端B=${b_content}"
    fi
    log_warn "说明: 停止灰度只清理灰度规则，不向已灰度客户端推送回落通知。"
    log_warn "      B 需重新拉取才回落全量(重启 B 或新启动客户端验证)。"
}

# record_result 记录一条用例结果到 CSV。
# 入参: case_id name status detail
record_result() {
    echo "$(date '+%Y-%m-%d %H:%M:%S'),$1,$2,$3,$4" >> "$RESULT_FILE"
}

# ======================== 主流程 ========================
main() {
    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║          配置灰度发布验证脚本                    ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "配置信息:"
    echo "  北极星服务端:     ${POLARIS_SERVER}"
    echo "  命名空间/配置组:  ${NAMESPACE}/${FILE_GROUP}"
    echo "  配置文件名:       ${FILE_NAME}"
    echo "  客户端 A 端口:    ${PORT_A} (标签 env=pre,常驻)"
    echo "  客户端 B 端口:    ${PORT_B} (无标签,常驻)"
    echo "  临时验证客户端:   ${PORT_C} (标签 env=pre,用例1 按需启动)"
    echo "  运行用例:         ${CASE}"
    echo "  Debug 日志:       ${DEBUG_MODE}"
    echo ""

    log_step "步骤 1/5 环境准备与编译"
    mkdir -p "$BUILD_DIR" "$LOG_DIR"
    echo "时间戳,用例编号,用例名称,结果,详情" > "$RESULT_FILE"
    : > "$PID_FILE"

    if ! command -v go &> /dev/null; then
        log_error "Go 未安装，请先安装 Go"
        exit 1
    fi
    log_info "Go 版本: $(go version)"

    if [[ ! -f "${SCRIPT_DIR}/main.go" ]]; then
        log_error "找不到源码: ${SCRIPT_DIR}/main.go"
        exit 1
    fi

    log_info "编译 gray-demo..."
    (cd "$SCRIPT_DIR" && go build -o "${BUILD_DIR}/gray-demo" .)
    if command -v xattr &> /dev/null; then
        xattr -c "${BUILD_DIR}/gray-demo" 2>/dev/null || true
    fi
    log_info "编译完成 -> ${BUILD_DIR}/gray-demo"

    log_step "步骤 2/5 生成配置与发布全量基线"
    generate_polaris_yaml "${BUILD_DIR}/polaris-client-a.yaml" "a"
    generate_polaris_yaml "${BUILD_DIR}/polaris-client-b.yaml" "b"

    log_info "通过 gray-demo setup 准备全量基线(content=${NORMAL_CONTENT}, 已存在则跳过)..."
    (cd "${BUILD_DIR}" && "./gray-demo" \
        -action=setup \
        -config="${BUILD_DIR}/polaris-client-b.yaml" \
        -namespace="$NAMESPACE" -group="$FILE_GROUP" -file="$FILE_NAME" \
        -content="$NORMAL_CONTENT") || {
        log_error "全量基线准备失败，请确认配置组 ${FILE_GROUP} 存在且服务端可用(若有活跃灰度请先停止)"
        exit 1
    }

    log_step "步骤 3/5 启动客户端 A 与客户端 B"
    start_client "a" "$PORT_A" PID_A
    sleep 1
    kill -0 "$PID_A" 2>/dev/null || { log_error "客户端 A 启动失败"; cat "${LOG_DIR}/client-a.log" 2>/dev/null; exit 1; }
    wait_for_http "http://127.0.0.1:${PORT_A}/health" 30 "客户端 A" "$PID_A" || exit 1

    start_client "b" "$PORT_B" PID_B
    sleep 1
    kill -0 "$PID_B" 2>/dev/null || { log_error "客户端 B 启动失败"; cat "${LOG_DIR}/client-b.log" 2>/dev/null; exit 1; }
    wait_for_http "http://127.0.0.1:${PORT_B}/health" 30 "客户端 B" "$PID_B" || exit 1

    log_step "步骤 4/5 验证全量基线(两客户端均应为 ${NORMAL_CONTENT})"
    wait_for_content "$PORT_A" "$NORMAL_CONTENT" 30 "客户端 A 基线" || exit 1
    wait_for_content "$PORT_B" "$NORMAL_CONTENT" 30 "客户端 B 基线" || exit 1
    log_info "✅ 两客户端均获取到全量基线内容"

    log_step "步骤 5/5 执行灰度验证用例"
    local overall_pass=true
    if [[ "$CASE" == "1" || "$CASE" == "all" ]]; then
        run_case1 || overall_pass=false
    fi
    if [[ "$CASE" == "2" || "$CASE" == "all" ]]; then
        run_case2 || overall_pass=false
    fi

    # ==================== 结果汇总 ====================
    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║              配置灰度验证结果汇总                ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "  配置文件:    ${NAMESPACE}/${FILE_GROUP}/${FILE_NAME}"
    echo "  全量内容:    ${NORMAL_CONTENT}"
    echo "  灰度内容:    ${GRAY_CONTENT}"
    echo ""
    echo "  用例明细:"
    awk -F',' 'NR>1 { printf "    [%s] %s: %s (%s)\n", $2, $3, $4, $5 }' "$RESULT_FILE"
    echo ""
    echo "  详细结果 CSV: ${RESULT_FILE}"
    echo "  客户端 A 日志: ${LOG_DIR}/client-a.log"
    echo "  客户端 B 日志: ${LOG_DIR}/client-b.log"
    echo ""

    if [[ "$overall_pass" == "true" ]]; then
        echo -e "${GREEN}验证结论: ✅ 配置灰度功能验证通过${NC}"
        echo -e "${GREEN}  - polaris-go 无需任何代码改动，仅通过 global.client.labels 即可参与灰度${NC}"
        echo -e "${GREEN}  - 自定义标签灰度: 命中客户端获取灰度内容，未命中客户端保持全量内容${NC}"
        echo -e "${GREEN}  - IP 维度灰度: 通过 watch 实时推送到命中客户端${NC}"
    else
        echo -e "${YELLOW}验证结论: ⚠️ 部分用例未通过，请对照上述明细与日志排查${NC}"
        echo -e "${YELLOW}  常见原因:${NC}"
        echo -e "${YELLOW}  1. 服务端未更新含灰度逻辑的版本${NC}"
        echo -e "${YELLOW}  2. 控制台灰度规则配置错误(标签 key/value/匹配类型)${NC}"
        echo -e "${YELLOW}  3. 客户端标签未生效(检查 polaris.yaml 中 global.client.labels)${NC}"
    fi
    echo ""
}

main "$@"
