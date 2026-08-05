#!/bin/bash
# =============================================================================
# 配置灰度云上验证编排脚本
#
# 在能访问三个客户端节点的机器上执行，curl 各节点 /config 对比生效内容，
# 并引导在北极星控制台发布/停止灰度、在各节点 client.sh start/stop。
#
# 使用方法:
#   ./verify-cloud.sh --a <A节点ip:port> --b <B节点ip:port> --c <C节点ip:port> \
#                    [--polaris-server <地址>] [--case <1|2|all>] [--debug]
#
# 前置:
#   1. 已用 build-materials.sh 生成物料并部署到 3 个节点
#   2. client-b 节点已执行 ./client.sh setup --polaris-server <地址> 发布全量基线
#   3. client-a / client-b 已 ./client.sh start 常驻运行
#   4. client-c 待命(用例1 时按提示 start)
# =============================================================================

set -euo pipefail

# ======================== 默认配置 ========================
ADDR_A=""
ADDR_B=""
ADDR_C=""
POLARIS_SERVER="${POLARIS_SERVER:-}"
CASE="${CASE:-all}"

NORMAL_CONTENT="normal-content-v1"
GRAY_CONTENT="gray-content-v2"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# ======================== 解析参数 ========================
while [[ $# -gt 0 ]]; do
    case "$1" in
        --a)             ADDR_A="$2"; shift 2 ;;
        --b)             ADDR_B="$2"; shift 2 ;;
        --c)             ADDR_C="$2"; shift 2 ;;
        --polaris-server) POLARIS_SERVER="$2"; shift 2 ;;
        --case)          CASE="$2"; shift 2 ;;
        --debug)         DEBUG_MODE=true; shift ;;
        -h|--help)
            echo "用法: $0 --a <ip:port> --b <ip:port> --c <ip:port> [选项]"
            echo ""
            echo "选项:"
            echo "  --a <ip:port>            客户端 A(带 env=pre) 地址"
            echo "  --b <ip:port>            客户端 B(无标签) 地址"
            echo "  --c <ip:port>            客户端 C(带 env=pre,临时) 地址"
            echo "  --polaris-server <地址>  北极星服务端地址(仅用于提示)"
            echo "  --case <1|2|all>         仅运行指定用例 (默认: all)"
            echo "  --debug                  详细输出"
            exit 0
            ;;
        *)
            echo -e "${RED}未知参数: $1${NC}"; exit 1 ;;
    esac
done

[[ -n "$ADDR_A" && -n "$ADDR_B" && -n "$ADDR_C" ]] || {
    echo -e "${RED}需要 --a --b --c 三个节点地址${NC}"; exit 1
}

# ======================== 工具函数 ========================
log_info()  { echo -e "${GREEN}[INFO]${NC} $(date '+%H:%M:%S') $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $(date '+%H:%M:%S') $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $(date '+%H:%M:%S') $*"; }
log_step()  { echo ""; echo -e "${CYAN}========================================${NC}"; echo -e "${CYAN}  $*${NC}"; echo -e "${CYAN}========================================${NC}"; }

# get_content 拉取节点当前生效配置内容。
# 入参: addr
get_content() {
    curl -s --connect-timeout 3 --max-time 5 "http://$1/config" 2>/dev/null \
        | grep -oE '"content":"[^"]*"' | sed 's/"content":"//;s/"$//'
}

# get_local_ip 拉取节点自报本机 IP(仅参考)。
get_local_ip() {
    curl -s --connect-timeout 3 --max-time 5 "http://$1/config" 2>/dev/null \
        | grep -oE '"localIP":"[^"]*"' | sed 's/"localIP":"//;s/"$//'
}

# wait_for_content 轮询节点直到生效内容等于期望或超时。
# 入参: addr expected max_wait desc
wait_for_content() {
    local addr="$1" expected="$2" max_wait="${3:-30}" desc="${4:-配置}"
    local waited=0
    while [[ $waited -lt $max_wait ]]; do
        local actual
        actual=$(get_content "$addr")
        if [[ "$actual" == "$expected" ]]; then
            log_info "${desc} 已生效: content=${actual} (耗时 ${waited}s)"
            return 0
        fi
        sleep 1
        waited=$((waited + 1))
    done
    log_error "${desc} 未在 ${max_wait}s 内生效，期望=${expected}，实际=$(get_content "$addr" || echo '(不可达)')"
    return 1
}

# ======================== 用例 1: 自定义标签灰度 ========================
run_case1() {
    log_step "用例 1: 自定义标签灰度 (规则 env EXACT pre)"

    log_info "验证基线: A、B 均应为 ${NORMAL_CONTENT}"
    wait_for_content "$ADDR_A" "$NORMAL_CONTENT" 15 "客户端 A 基线" || { log_error "A 基线异常"; return 1; }
    wait_for_content "$ADDR_B" "$NORMAL_CONTENT" 15 "客户端 B 基线" || { log_error "B 基线异常"; return 1; }
    log_info "✅ 基线正常"

    echo ""
    echo -e "${BLUE}请在北极星控制台执行以下操作:${NC}"
    echo -e "  1. 编辑配置文件，修改内容为: ${GRAY_CONTENT}"
    echo -e "  2. 点击「灰度发布」，灰度规则选择标签 env，匹配类型 EXACT，值 pre"
    echo -e "  3. 确认发布灰度版本"
    echo ""
    read -r -p "完成后按 Enter 继续..."

    echo ""
    echo -e "${BLUE}请在 C 节点执行(启动带 env=pre 标签的临时客户端，验证初始拉取命中灰度):${NC}"
    echo -e "  cd client-c && ./client.sh start --polaris-server ${POLARIS_SERVER:-<地址>} --port 18083"
    echo ""
    read -r -p "C 节点启动完成后按 Enter 继续..."

    log_info "验证 C 命中灰度(期望: ${GRAY_CONTENT})..."
    if wait_for_content "$ADDR_C" "$GRAY_CONTENT" 30 "客户端 C 灰度内容"; then
        log_info "✅ [用例 1.1 灰度命中] PASS - C(env=pre) 初始拉取命中灰度"
    else
        log_error "❌ [用例 1.1 灰度命中] FAIL"
        return 1
    fi

    log_info "验证 A 未命中(期望: ${NORMAL_CONTENT}, 限制1: 自定义标签灰度不推送)..."
    if wait_for_content "$ADDR_A" "$NORMAL_CONTENT" 5 "客户端 A 未命中"; then
        log_info "✅ [用例 1.2 灰度未命中] PASS - A 常驻未收到推送，保持全量"
    else
        log_warn "⚠️  [用例 1.2 灰度未命中] A 内容非全量，需人工确认"
    fi

    echo ""
    echo -e "${BLUE}请在北极星控制台停止上述灰度发布。${NC}"
    read -r -p "完成后按 Enter 继续..."

    echo ""
    echo -e "${BLUE}请在 C 节点重启客户端验证回落(停止灰度后初始拉取应得全量):${NC}"
    echo -e "  cd client-c && ./client.sh stop && ./client.sh start --polaris-server ${POLARIS_SERVER:-<地址>} --port 18083"
    echo ""
    read -r -p "C 节点重启完成后按 Enter 继续..."

    log_info "验证 C 回落(期望: ${NORMAL_CONTENT})..."
    if wait_for_content "$ADDR_C" "$NORMAL_CONTENT" 30 "客户端 C 全量内容"; then
        log_info "✅ [用例 1.3 停止灰度] PASS - C 回落到全量"
    else
        log_error "❌ [用例 1.3 停止灰度] FAIL"
        return 1
    fi

    echo ""
    echo -e "${YELLOW}提示: 用例 1 验证完可在 C 节点 ./client.sh stop 释放资源。${NC}"
}

# ======================== 用例 2: IP 维度灰度(watch 推送) ========================
run_case2() {
    log_step "用例 2: IP 维度灰度 (规则 CLIENT_IP EXACT <服务端视角的B连接IP>)"

    local ip_b
    ip_b=$(get_local_ip "$ADDR_B")
    log_info "客户端 B 自报本机 IP: ${ip_b} (仅作参考)"
    echo -e "${YELLOW}注意: CLIENT_IP 由服务端从 gRPC 连接对端解析(非客户端上报)。${NC}"
    echo -e "${YELLOW}若 B 经 NAT 出网，服务端看到的 IP 与上述自报 IP 不同，需从服务端日志/控制台获取。${NC}"

    echo ""
    echo -e "${BLUE}请在北极星控制台执行以下操作:${NC}"
    echo -e "  1. 编辑配置文件，修改内容为: ${GRAY_CONTENT}"
    echo -e "  2. 点击「灰度发布」，灰度规则选择标签 CLIENT_IP，匹配类型 EXACT"
    echo -e "     值填「服务端视角的 B 连接 IP」(同机/同局域网时即 ${ip_b}，跨 NAT 时需另取)"
    echo -e "  3. 确认发布灰度版本"
    echo ""
    read -r -p "完成后按 Enter 继续..."

    log_info "轮询 B，期望通过 watch 实时推送获取灰度内容(最长 60s)..."
    if wait_for_content "$ADDR_B" "$GRAY_CONTENT" 60 "客户端 B IP灰度推送"; then
        log_info "✅ [用例 2.1 IP灰度推送] PASS - B 通过 watch 实时推送获取灰度"
    else
        log_error "❌ [用例 2.1 IP灰度推送] FAIL"
        return 1
    fi

    log_info "验证 A 未命中(期望: ${NORMAL_CONTENT}, 限制2: 带自定义标签不被注入 CLIENT_IP)..."
    if wait_for_content "$ADDR_A" "$NORMAL_CONTENT" 5 "客户端 A IP灰度未命中"; then
        log_info "✅ [用例 2.2 IP灰度未命中] PASS - A(env=pre) 未命中 IP 灰度(符合限制2)"
    else
        log_warn "⚠️  [用例 2.2 IP灰度未命中] A 内容非全量，需人工确认"
    fi

    echo ""
    echo -e "${BLUE}请在北极星控制台停止上述 IP 维度灰度发布。${NC}"
    read -r -p "完成后按 Enter 继续..."

    log_info "验证 B 保持灰度内容(停止灰度不推送回落，已灰度客户端保持)..."
    sleep 5
    local b_content
    b_content=$(get_content "$ADDR_B")
    if [[ "$b_content" == "$GRAY_CONTENT" ]]; then
        log_info "✅ [用例 2.3 停止IP灰度] PASS - B 保持灰度内容(停止灰度不推送回落)"
    else
        log_warn "⚠️  [用例 2.3 停止IP灰度] B 内容=${b_content}，非预期的灰度内容"
    fi
    echo -e "${YELLOW}说明: 停止灰度只清理灰度规则，不向已灰度客户端推送回落通知。${NC}"
    echo -e "${YELLOW}      B 需重新拉取才回落全量(重启 B 节点 client.sh 或新启动客户端验证)。${NC}"
}

# ======================== 主流程 ========================
main() {
    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║        配置灰度云上验证编排脚本                  ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "节点地址:"
    echo "  客户端 A (env=pre,常驻):    ${ADDR_A}"
    echo "  客户端 B (无标签,常驻):     ${ADDR_B}"
    echo "  客户端 C (env=pre,临时):    ${ADDR_C}"
    echo "  运行用例:                   ${CASE}"
    echo ""

    local overall_pass=true
    if [[ "$CASE" == "1" || "$CASE" == "all" ]]; then
        run_case1 || overall_pass=false
    fi
    if [[ "$CASE" == "2" || "$CASE" == "all" ]]; then
        run_case2 || overall_pass=false
    fi

    echo ""
    if [[ "$overall_pass" == "true" ]]; then
        echo -e "${GREEN}验证结论: ✅ 配置灰度云上验证通过${NC}"
    else
        echo -e "${YELLOW}验证结论: ⚠️ 部分用例未通过，请对照日志排查${NC}"
    fi
}

main "$@"
