#!/bin/bash
# =============================================================================
# 元数据路由(Metadata Route)功能验证脚本
#
# 使用方法:
#   chmod +x verify_metadata_route.sh
#   ./verify_metadata_route.sh [--polaris-server <地址>] [--polaris-token <令牌>]
#                              [--service <服务名>] [--namespace <命名空间>]
#                              [--request-count <请求次数>]
#
# 前置条件:
#   1. 北极星服务端(Polaris Server)已启动
#   2. Go 环境已安装
#
#   说明：元数据路由(dstMetaRouter)的匹配由 SDK 侧完成（Consumer 通过
#   GetOneInstanceRequest.Metadata 过滤目标实例），不依赖服务端的路由规则，
#   因此本脚本无需在北极星创建路由规则。
#
# 验证原理:
#   元数据路由允许 Consumer 在发起服务调用时，根据请求携带的元数据标签，
#   将请求只路由到具备相同标签的 Provider 实例。
#
#   本脚本启动四个 Provider 实例：
#     - provider-dev:  携带标签 env=dev
#     - provider-test: 携带标签 env=test
#     - provider-pre:  携带标签 env=pre
#     - provider-prod: 携带标签 env=prod
#   然后分别以不同的 env 值启动 Consumer 并发送请求，验证路由是否按 metadata 精准匹配。
#
#   验证：Consumer 携带 env=<e> 发出的所有请求应该只路由到对应的 Provider 实例，
#   不会路由到其他 env 的 Provider。
#
#   另外增加 1 条 负测试用例：Consumer 携带 env=noexist（不存在的标签），
#   预期应无任何实例匹配，请求应返回失败，验证元数据路由的严格过滤特性。
#
# 验证流程:
#   1. 环境准备
#   2. 编译 Provider 和 Consumer
#   3. 启动 Provider-dev / Provider-test / Provider-pre / Provider-prod
#   4. 对每个 env 启动 Consumer + 发送请求验证
#   5. 负用例：env=noexist 预期无匹配
#   6. 汇总验证结论
# =============================================================================

set -euo pipefail

# ======================== 默认配置 ========================
POLARIS_SERVER="${POLARIS_SERVER:-127.0.0.1}"
POLARIS_TOKEN="${POLARIS_TOKEN:-}"
SERVICE_NAME="${SERVICE_NAME:-RouteMetadataEchoServer}"
CALLER_SERVICE="${CALLER_SERVICE:-RouteMetadataEchoCaller}"
NAMESPACE="${NAMESPACE:-default}"
# 两条链路的 Consumer 起始端口（每个 env 使用 base+offset）：
#   - simple-consumer (GetOneInstance + dstMetaRouter): SIMPLE_CONSUMER_PORT_BASE
#   - consumer        (ProcessRouters + 业务侧过滤)  : CONSUMER_PORT_BASE
SIMPLE_CONSUMER_PORT_BASE="${SIMPLE_CONSUMER_PORT_BASE:-18090}"
CONSUMER_PORT_BASE="${CONSUMER_PORT_BASE:-18190}"
PROVIDER_DEV_PORT="${PROVIDER_DEV_PORT:-28071}"    # 固定端口：同一 host:port 实例 ID 稳定，避免 zombie 实例累积
PROVIDER_TEST_PORT="${PROVIDER_TEST_PORT:-28072}"
PROVIDER_PRE_PORT="${PROVIDER_PRE_PORT:-28073}"
PROVIDER_PROD_PORT="${PROVIDER_PROD_PORT:-28074}"
REQUEST_COUNT="${REQUEST_COUNT:-10}"                # 每个 env 的请求次数
DEBUG_MODE="${DEBUG_MODE:-false}"

# 路由测试的 env 值列表（正用例）
ROUTE_ENVS="${ROUTE_ENVS:-dev,test,pre,prod}"
# 负用例的 env 值（Provider 中不存在的标签）
NEGATIVE_ENV="${NEGATIVE_ENV:-noexist}"
# 是否执行负用例
RUN_NEGATIVE_CASE="${RUN_NEGATIVE_CASE:-true}"

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
        --caller-service)
            CALLER_SERVICE="$2"; shift 2 ;;
        --namespace)
            NAMESPACE="$2"; shift 2 ;;
        --consumer-port-base)
            CONSUMER_PORT_BASE="$2"; shift 2 ;;
        --simple-consumer-port-base)
            SIMPLE_CONSUMER_PORT_BASE="$2"; shift 2 ;;
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
        --negative-env)
            NEGATIVE_ENV="$2"; shift 2 ;;
        --no-negative-case)
            RUN_NEGATIVE_CASE="false"; shift ;;
        --debug)
            DEBUG_MODE="true"; shift ;;
        --help|-h)
            echo "用法: $0 [选项]"
            echo ""
            echo "选项:"
            echo "  --polaris-server <地址>         北极星服务端地址 (默认: 127.0.0.1)"
            echo "  --polaris-token <令牌>          北极星鉴权令牌 (默认: 空)"
            echo "  --service <服务名>              Provider 服务名 (默认: RouteMetadataEchoServer)"
            echo "  --caller-service <服务名>       Consumer 自身服务名 (默认: RouteMetadataEchoCaller)"
            echo "  --namespace <命名空间>          命名空间 (默认: default)"
            echo "  --simple-consumer-port-base <端口> Simple-Consumer(GetOneInstance) 起始端口 (默认: 18090, 每个 env +1)"
            echo "  --consumer-port-base <端口>     Consumer(ProcessRouters) 起始端口 (默认: 18190, 每个 env +1)"
            echo "  --provider-dev-port <端口>      Provider-dev 端口 (默认: 自动分配)"
            echo "  --provider-test-port <端口>     Provider-test 端口 (默认: 自动分配)"
            echo "  --provider-pre-port <端口>      Provider-pre 端口 (默认: 自动分配)"
            echo "  --provider-prod-port <端口>     Provider-prod 端口 (默认: 自动分配)"
            echo "  --request-count <次数>          每个 env 的请求次数 (默认: 10)"
            echo "  --route-envs <列表>             正用例 env 列表，逗号分隔 (默认: dev,test,pre,prod)"
            echo "  --negative-env <值>             负用例的不存在 env 值 (默认: noexist)"
            echo "  --no-negative-case              跳过负用例验证"
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

# 测试总日志文件（同时输出到标准输出和日志文件，参考 lane-test.sh）
TEST_LOG_FILE="${LOG_DIR}/verify_metadata_route-$(date +%Y%m%d_%H%M%S).log"

# Provider PID / 端口
PROVIDER_DEV_PID=""
PROVIDER_TEST_PID=""
PROVIDER_PRE_PID=""
PROVIDER_PROD_PID=""
PROVIDER_DEV_ACTUAL_PORT=""
PROVIDER_TEST_ACTUAL_PORT=""
PROVIDER_PRE_ACTUAL_PORT=""
PROVIDER_PROD_ACTUAL_PORT=""

# 运行期的 consumer PID 列表（供 cleanup 使用）
CONSUMER_PIDS=()

# ======================== 清理函数 ========================
cleanup() {
    log_info "清理进程..."
    # Consumer
    for pid in "${CONSUMER_PIDS[@]:-}"; do
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
            log_info "已停止 Consumer 进程 (PID: $pid)"
        fi
    done
    # Providers
    for pid_var in PROVIDER_DEV_PID PROVIDER_TEST_PID PROVIDER_PRE_PID PROVIDER_PROD_PID; do
        local pid="${!pid_var}"
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
            log_info "已停止 ${pid_var} 进程 (PID: $pid)"
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
        echo "===== 元数据路由测试日志 $(date '+%Y-%m-%d %H:%M:%S') ====="
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
    # 把默认 500ms 放宽到 3s：外网北极星服务端首次 TCP 握手容易超过 500ms，
    connectTimeout: 3s
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
    # 把默认 500ms 放宽到 3s：首次服务发现同样容易超过默认值，
    connectTimeout: 3s
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
      # 启用元数据路由
      - dstMetaRouter
EOF
    log_info "生成 Consumer polaris.yaml -> $yaml_file"
}

# ProcessRouters 版 consumer 用的 polaris.yaml：
#   该 consumer 在业务侧手工做 metadata 过滤 + 用 DefaultServiceInstances 包装后交给 ProcessRouters，
#   所以这里不启用 dstMetaRouter，只保留规则路由作为剩余路由链示例。
generate_consumer_pr_polaris_yaml() {
    local target_dir="$1"
    local yaml_file="${target_dir}/polaris.yaml"

    cat > "$yaml_file" <<EOF
global:
  serverConnector:
    addresses:
      - ${POLARIS_SERVER}:8091
    # 同 generate_consumer_polaris_yaml：默认 500ms 偏小，首请求易超时。
    connectTimeout: 3s
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
      - ruleBasedRouter
    plugin:
      ruleBasedRouter:
        failoverType: all
EOF
    log_info "生成 Consumer(PR) polaris.yaml -> $yaml_file"
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

    _STARTED_PID="$pid"
    _STARTED_PORT="$actual_port"
}

# 启动一个 Consumer 实例
# 参数: $1=名称 $2=metadata $3=指定端口 $4=日志文件
# 副作用: 设置 _STARTED_PID，把 pid 追加到 CONSUMER_PIDS
start_consumer() {
    local name="$1"
    local meta="$2"
    local port="$3"
    local log_file="$4"

    local workdir="${BUILD_DIR}/${name}"
    mkdir -p "$workdir"
    generate_consumer_polaris_yaml "$workdir"

    # 若端口已被占用，尝试先释放
    if curl -s --connect-timeout 2 "http://127.0.0.1:${port}/echo" > /dev/null 2>&1; then
        log_warn "端口 ${port} 已被占用，尝试终止已有进程..."
        local existing_pid
        existing_pid=$(lsof -ti :"${port}" 2>/dev/null | head -1 || echo "")
        if [[ -n "$existing_pid" ]]; then
            kill "$existing_pid" 2>/dev/null || true
            sleep 2
        fi
    fi

    # simple-consumer 的 flag 命名: --namespace/--service 对应自身
    (cd "$workdir" && "${BUILD_DIR}/simple_consumer" \
        --calleeNamespace "$NAMESPACE" \
        --calleeService "$SERVICE_NAME" \
        --namespace "$NAMESPACE" \
        --service "$CALLER_SERVICE" \
        --port "$port" \
        --metadata "$meta" \
        > "$log_file" 2>&1) &
    local pid=$!
    log_info "${name} 已启动 (PID: $pid, metadata: ${meta}, port: ${port})"

    sleep 1
    check_process_alive "$pid" "$name" || {
        log_error "${name} 启动失败，请检查日志: $log_file"
        cat "$log_file" 2>/dev/null || true
        return 1
    }

    wait_for_http "http://127.0.0.1:${port}/echo" 30 "$name" "$pid" || return 1

    CONSUMER_PIDS+=("$pid")
    _STARTED_PID="$pid"
    return 0
}

# 启动一个 Consumer 实例（ProcessRouters + 业务侧 metadata 过滤 版本）
# 参数: $1=名称 $2=metadata $3=指定端口 $4=日志文件
# 副作用: 设置 _STARTED_PID，把 pid 追加到 CONSUMER_PIDS
start_consumer_pr() {
    local name="$1"
    local meta="$2"
    local port="$3"
    local log_file="$4"

    local workdir="${BUILD_DIR}/${name}"
    mkdir -p "$workdir"
    generate_consumer_pr_polaris_yaml "$workdir"

    # 若端口已被占用，尝试先释放
    if curl -s --connect-timeout 2 "http://127.0.0.1:${port}/echo" > /dev/null 2>&1; then
        log_warn "端口 ${port} 已被占用，尝试终止已有进程..."
        local existing_pid
        existing_pid=$(lsof -ti :"${port}" 2>/dev/null | head -1 || echo "")
        if [[ -n "$existing_pid" ]]; then
            kill "$existing_pid" 2>/dev/null || true
            sleep 2
        fi
    fi

    # consumer (ProcessRouters 版) 的 flag 命名: --selfNamespace/--selfService
    (cd "$workdir" && "${BUILD_DIR}/consumer" \
        --calleeNamespace "$NAMESPACE" \
        --calleeService "$SERVICE_NAME" \
        --selfNamespace "$NAMESPACE" \
        --selfService "$CALLER_SERVICE" \
        --port "$port" \
        --metadata "$meta" \
        --debug=$DEBUG_MODE \
        > "$log_file" 2>&1) &
    local pid=$!
    log_info "${name} 已启动 (PID: $pid, metadata: ${meta}, port: ${port})"

    sleep 1
    check_process_alive "$pid" "$name" || {
        log_error "${name} 启动失败，请检查日志: $log_file"
        cat "$log_file" 2>/dev/null || true
        return 1
    }

    wait_for_http "http://127.0.0.1:${port}/echo" 30 "$name" "$pid" || return 1

    CONSUMER_PIDS+=("$pid")
    _STARTED_PID="$pid"
    return 0
}

# 停止指定 consumer
stop_consumer() {
    local pid="$1"
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
        kill "$pid" 2>/dev/null || true
        wait "$pid" 2>/dev/null || true
        log_info "已停止 Consumer 进程 (PID: $pid)"
    fi
    # 从 CONSUMER_PIDS 中移除（只移除首个匹配即可）
    local new_pids=()
    local removed=false
    for existing in "${CONSUMER_PIDS[@]:-}"; do
        if ! $removed && [[ "$existing" == "$pid" ]]; then
            removed=true
            continue
        fi
        new_pids+=("$existing")
    done
    CONSUMER_PIDS=("${new_pids[@]:-}")
}

# ======================== 正/负用例执行逻辑 ========================
#
# run_positive_case 与 run_negative_case 把链路 A/B 发请求、统计结果、写 CSV 的
# 共同逻辑抽成函数，避免在 main() 里多处重复。
#
# 契约：
#   - 正用例 run_positive_case <chain> <target_env> <consumer_port> <result_file>
#     通过全局变量 ENV_TOTAL / ENV_SUCCESS / ENV_FAIL / ENV_CORRECT / ENV_WRONG
#     把本次 env 的统计回传给调用方累加到 grand_* 变量。
#   - 负用例 run_negative_case <chain> <neg_env> <consumer_port> <result_file>
#     通过 NEG_TOTAL / NEG_AS_EXPECTED / NEG_UNEXPECTED 回传。
#
# CSV 列定义：链路,用例类型,env值,序号,HTTP状态码,路由目标,是否正确,响应内容
run_positive_case() {
    local chain="$1"
    local target_env="$2"
    local consumer_port="$3"
    local result_file="$4"

    # 下面 5 个统计量不加 local / declare，以便跨函数回传给调用方 (main() 内的 a_/b_grand_*)。
    # 说明：macOS 自带 bash 3.2 不支持 declare -g，而在 bash 函数里的"裸赋值"本身就是
    # 全局写入，只要调用方使用不同的变量名，就不会污染 local 上下文。
    ENV_TOTAL=0
    ENV_SUCCESS=0
    ENV_FAIL=0
    ENV_CORRECT=0
    ENV_WRONG=0

    printf "  ${CYAN}%-6s %-12s %-30s %-8s %s${NC}\n" "序号" "状态" "路由目标" "正确?" "响应摘要"
    printf "  %-6s %-12s %-30s %-8s %s\n" "------" "------------" "------------------------------" "--------" "--------------------------------------------"

    for i in $(seq 1 "$REQUEST_COUNT"); do
        ENV_TOTAL=$((ENV_TOTAL + 1))
        local resp
        local http_code
        http_code=$(curl -s -o /tmp/_md_resp_$$.tmp -w '%{http_code}' --connect-timeout 5 \
            "http://127.0.0.1:${consumer_port}/echo" 2>/dev/null || echo "000")
        resp=$(cat /tmp/_md_resp_$$.tmp 2>/dev/null || echo "")
        rm -f /tmp/_md_resp_$$.tmp

        local route_target="unknown"
        local is_correct="?"
        local status_icon=""

        if [[ "$http_code" == "200" ]]; then
            ENV_SUCCESS=$((ENV_SUCCESS + 1))
            if echo "$resp" | grep -q "env=${target_env}"; then
                route_target="Provider-${target_env} (env=${target_env})"
                is_correct="✓"
                status_icon="${GREEN}✓${NC}"
                ENV_CORRECT=$((ENV_CORRECT + 1))
            else
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
                ENV_WRONG=$((ENV_WRONG + 1))
            fi
        else
            ENV_FAIL=$((ENV_FAIL + 1))
            route_target="请求失败 (HTTP ${http_code})"
            is_correct="✗"
            status_icon="${RED}✗${NC}"
        fi

        local resp_summary
        resp_summary=$(echo "$resp" | head -1 | cut -c1-80)
        printf "  %-6s ${status_icon} %-10s %-30s %-8s %s\n" \
            "$i" "HTTP $http_code" "$route_target" "$is_correct" "$resp_summary"
        # CSV 行：链路,用例类型,env值,序号,HTTP状态码,路由目标,是否正确,响应内容
        echo "${chain},positive,${target_env},${i},${http_code},${route_target},${is_correct},${resp}" >> "$result_file"
        sleep 0.1
    done

    echo ""
    if [[ "$ENV_CORRECT" -eq "$REQUEST_COUNT" ]]; then
        echo -e "  ${GREEN}[链路${chain}] env=${target_env}: 全部正确 (${ENV_CORRECT}/${REQUEST_COUNT})${NC}"
    elif [[ "$ENV_FAIL" -eq "$REQUEST_COUNT" ]]; then
        echo -e "  ${RED}[链路${chain}] env=${target_env}: 全部失败 (${ENV_FAIL}/${REQUEST_COUNT})${NC}"
    else
        echo -e "  ${YELLOW}[链路${chain}] env=${target_env}: 正确=${ENV_CORRECT}, 错误路由=${ENV_WRONG}, 失败=${ENV_FAIL} (共 ${REQUEST_COUNT})${NC}"
    fi
}

run_negative_case() {
    local chain="$1"
    local neg_env="$2"
    local consumer_port="$3"
    local result_file="$4"

    # 下面 3 个统计量不加 local / declare，以便跨函数回传给调用方 (a_/b_neg_*)。
    # 同 run_positive_case：bash 3.2 不支持 declare -g，裸赋值即是全局写入。
    NEG_TOTAL=0
    NEG_AS_EXPECTED=0
    NEG_UNEXPECTED=0

    printf "  ${CYAN}%-6s %-12s %-30s %-12s %s${NC}\n" "序号" "状态" "路由目标" "预期?" "响应摘要"
    printf "  %-6s %-12s %-30s %-12s %s\n" "------" "------------" "------------------------------" "------------" "--------------------------------------------"

    for i in $(seq 1 "$REQUEST_COUNT"); do
        NEG_TOTAL=$((NEG_TOTAL + 1))
        local resp
        local http_code
        http_code=$(curl -s -o /tmp/_md_neg_resp_$$.tmp -w '%{http_code}' --connect-timeout 5 \
            "http://127.0.0.1:${consumer_port}/echo" 2>/dev/null || echo "000")
        resp=$(cat /tmp/_md_neg_resp_$$.tmp 2>/dev/null || echo "")
        rm -f /tmp/_md_neg_resp_$$.tmp

        local route_target=""
        local verdict=""
        local status_icon=""

        # 负用例期望：无任何 provider 被命中（元数据匹配不到，应当返回错误或空实例）
        if [[ "$http_code" == "200" ]] && echo "$resp" | grep -qE "env=(dev|test|pre|prod)"; then
            local actual_env=""
            for check_env in dev test pre prod; do
                if echo "$resp" | grep -q "env=${check_env}"; then
                    actual_env="$check_env"
                    break
                fi
            done
            route_target="意外命中 Provider-${actual_env}"
            verdict="意外"
            status_icon="${RED}✗${NC}"
            NEG_UNEXPECTED=$((NEG_UNEXPECTED + 1))
        else
            route_target="无 provider（符合预期）"
            verdict="符合预期"
            status_icon="${GREEN}✓${NC}"
            NEG_AS_EXPECTED=$((NEG_AS_EXPECTED + 1))
        fi

        local resp_summary
        resp_summary=$(echo "$resp" | head -1 | cut -c1-80)
        printf "  %-6s ${status_icon} %-10s %-30s %-12s %s\n" \
            "$i" "HTTP $http_code" "$route_target" "$verdict" "$resp_summary"
        echo "${chain},negative,${neg_env},${i},${http_code},${route_target},${verdict},${resp}" >> "$result_file"
        sleep 0.1
    done

    echo ""
    if [[ "$NEG_UNEXPECTED" -eq 0 ]]; then
        echo -e "  ${GREEN}[链路${chain}] env=${neg_env}(负用例): 全部符合预期，无意外路由 (${NEG_AS_EXPECTED}/${NEG_TOTAL})${NC}"
    else
        echo -e "  ${RED}[链路${chain}] env=${neg_env}(负用例): 出现 ${NEG_UNEXPECTED} 次意外路由${NC}"
    fi
}

# ======================== 主流程 ========================

main() {
    # 初始化测试总日志（stdout/stderr 同时输出到终端和日志文件）
    setup_test_log "$@"

    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║       元数据路由(Metadata Route)功能验证脚本     ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "配置信息:"
    echo "  北极星服务端:       ${POLARIS_SERVER}:8091"
    echo "  服务名:             ${SERVICE_NAME}"
    echo "  Caller 服务名:      ${CALLER_SERVICE}"
    echo "  命名空间:           ${NAMESPACE}"
    echo "  Simple-Consumer 起始端口: ${SIMPLE_CONSUMER_PORT_BASE}  (GetOneInstance + dstMetaRouter)"
    echo "  Consumer 起始端口:       ${CONSUMER_PORT_BASE}  (ProcessRouters + 业务侧过滤)"
    echo "  Provider env 列表:  ${ROUTE_ENVS}"
    echo "  负用例 env:         ${NEGATIVE_ENV} (执行: ${RUN_NEGATIVE_CASE})"
    echo "  每个 env 请求次数:  ${REQUEST_COUNT}"
    echo "  测试日志文件:       ${TEST_LOG_FILE}"
    echo ""

    # ==================== 步骤1: 环境准备 ====================
    log_step "1/7 环境准备"

    mkdir -p "$BUILD_DIR" "$LOG_DIR"

    # 检查 Go 环境
    if ! command -v go &> /dev/null; then
        log_error "Go 未安装，请先安装 Go"
        exit 1
    fi
    log_info "Go 版本: $(go version)"

    # 检查源码目录
    if [[ ! -f "${PROVIDER_DIR}/main.go" ]]; then
        log_error "找不到 Provider 源码: ${PROVIDER_DIR}/main.go"
        exit 1
    fi
    if [[ ! -f "${SIMPLE_CONSUMER_DIR}/main.go" ]]; then
        log_error "找不到 Simple-Consumer 源码: ${SIMPLE_CONSUMER_DIR}/main.go"
        exit 1
    fi
    if [[ ! -f "${CONSUMER_DIR}/main.go" ]]; then
        log_error "找不到 Consumer (ProcessRouters) 源码: ${CONSUMER_DIR}/main.go"
        exit 1
    fi
    log_info "源码检查通过"

    # ==================== 步骤2: 编译 ====================
    log_step "2/7 编译 Provider / Simple-Consumer / Consumer"

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

    # ==================== 步骤3: 启动 Provider 集群 ====================
    log_step "3/7 启动 Provider 集群"

    start_provider "provider_dev" "env=dev" "$PROVIDER_DEV_PORT" "${LOG_DIR}/provider_dev.log"
    PROVIDER_DEV_PID="$_STARTED_PID"
    PROVIDER_DEV_ACTUAL_PORT="$_STARTED_PORT"

    start_provider "provider_test" "env=test" "$PROVIDER_TEST_PORT" "${LOG_DIR}/provider_test.log"
    PROVIDER_TEST_PID="$_STARTED_PID"
    PROVIDER_TEST_ACTUAL_PORT="$_STARTED_PORT"

    start_provider "provider_pre" "env=pre" "$PROVIDER_PRE_PORT" "${LOG_DIR}/provider_pre.log"
    PROVIDER_PRE_PID="$_STARTED_PID"
    PROVIDER_PRE_ACTUAL_PORT="$_STARTED_PORT"

    start_provider "provider_prod" "env=prod" "$PROVIDER_PROD_PORT" "${LOG_DIR}/provider_prod.log"
    PROVIDER_PROD_PID="$_STARTED_PID"
    PROVIDER_PROD_ACTUAL_PORT="$_STARTED_PORT"

    # 等待服务发现缓存刷新
    log_info "等待 5s，确保服务发现缓存已刷新..."
    sleep 5

    # ==================== \u4e24\u6761\u94fe\u8def\u7684\u7edf\u8ba1\u53d8\u91cf\u4e0e\u7ed3\u679c\u6587\u4ef6 ====================
    # \u94fe\u8def A: simple-consumer (GetOneInstance + dstMetaRouter)
    local a_grand_total=0 a_grand_success=0 a_grand_fail=0 a_grand_correct=0 a_grand_wrong=0
    local a_neg_total=0 a_neg_as_expected=0 a_neg_unexpected=0
    # \u94fe\u8def B: consumer (ProcessRouters + \u4e1a\u52a1\u4fa7\u8fc7\u6ee4)
    local b_grand_total=0 b_grand_success=0 b_grand_fail=0 b_grand_correct=0 b_grand_wrong=0
    local b_neg_total=0 b_neg_as_expected=0 b_neg_unexpected=0

    local result_file="${LOG_DIR}/metadata_route_result.csv"
    echo "\u94fe\u8def,\u7528\u4f8b\u7c7b\u578b,env\u503c,\u5e8f\u53f7,HTTP\u72b6\u6001\u7801,\u8def\u7531\u76ee\u6807,\u662f\u5426\u6b63\u786e,\u54cd\u5e94\u5185\u5bb9" > "$result_file"

    # ==================== \u6b65\u9aa44\uff1a\u94fe\u8defA \u6b63\u7528\u4f8b ====================
    log_step "4/8 \u94fe\u8defA \u6b63\u7528\u4f8b\uff1asimple-consumer(GetOneInstance) \u2192 provider"

    IFS=',' read -ra ENV_ARRAY <<< "$ROUTE_ENVS"
    local env_index=0
    for target_env in "${ENV_ARRAY[@]}"; do
        env_index=$((env_index + 1))
        local consumer_port=$((SIMPLE_CONSUMER_PORT_BASE + env_index))
        local consumer_name="simple_consumer_${target_env}"
        local consumer_log="${LOG_DIR}/${consumer_name}.log"

        echo ""
        echo -e "${BLUE}\u2500\u2500 [\u94fe\u8defA] Simple-Consumer (env=${target_env}) \u2192 Provider (env=${target_env}) \u2500\u2500${NC}"
        echo ""

        start_consumer "$consumer_name" "env=${target_env}" "$consumer_port" "$consumer_log" || {
            log_error "Simple-Consumer (env=${target_env}) \u542f\u52a8\u5931\u8d25\uff0c\u8df3\u8fc7\u8be5\u7528\u4f8b"
            continue
        }
        local consumer_pid="$_STARTED_PID"

        run_positive_case "A" "$target_env" "$consumer_port" "$result_file"
        a_grand_total=$((a_grand_total + ENV_TOTAL))
        a_grand_success=$((a_grand_success + ENV_SUCCESS))
        a_grand_fail=$((a_grand_fail + ENV_FAIL))
        a_grand_correct=$((a_grand_correct + ENV_CORRECT))
        a_grand_wrong=$((a_grand_wrong + ENV_WRONG))

        stop_consumer "$consumer_pid"
        sleep 1
    done

    # ==================== \u6b65\u9aa45\uff1a\u94fe\u8defA \u8d1f\u7528\u4f8b ====================
    log_step "5/8 \u94fe\u8defA \u8d1f\u7528\u4f8b\uff1asimple-consumer env=${NEGATIVE_ENV}\uff08\u4e0d\u5b58\u5728\uff09"
    if [[ "$RUN_NEGATIVE_CASE" == "true" ]]; then
        local consumer_port=$((SIMPLE_CONSUMER_PORT_BASE + env_index + 1))
        local consumer_name="simple_consumer_${NEGATIVE_ENV}"
        local consumer_log="${LOG_DIR}/${consumer_name}.log"

        if start_consumer "$consumer_name" "env=${NEGATIVE_ENV}" "$consumer_port" "$consumer_log"; then
            local consumer_pid="$_STARTED_PID"
            run_negative_case "A" "$NEGATIVE_ENV" "$consumer_port" "$result_file"
            a_neg_total=$NEG_TOTAL
            a_neg_as_expected=$NEG_AS_EXPECTED
            a_neg_unexpected=$NEG_UNEXPECTED
            stop_consumer "$consumer_pid"
        else
            log_warn "[\u94fe\u8defA] \u8d1f\u7528\u4f8b Consumer \u542f\u52a8\u5931\u8d25\uff0c\u8df3\u8fc7"
        fi
    else
        log_info "\u5df2\u901a\u8fc7 --no-negative-case \u8df3\u8fc7\u94fe\u8defA \u8d1f\u7528\u4f8b"
    fi

    # ==================== \u6b65\u9aa46\uff1a\u94fe\u8defB \u6b63\u7528\u4f8b ====================
    log_step "6/8 \u94fe\u8defB \u6b63\u7528\u4f8b\uff1aconsumer(ProcessRouters) \u2192 provider"

    env_index=0
    for target_env in "${ENV_ARRAY[@]}"; do
        env_index=$((env_index + 1))
        local consumer_port=$((CONSUMER_PORT_BASE + env_index))
        local consumer_name="consumer_pr_${target_env}"
        local consumer_log="${LOG_DIR}/${consumer_name}.log"

        echo ""
        echo -e "${BLUE}\u2500\u2500 [\u94fe\u8defB] Consumer (env=${target_env}) \u2192 Provider (env=${target_env}) \u2500\u2500${NC}"
        echo ""

        start_consumer_pr "$consumer_name" "env=${target_env}" "$consumer_port" "$consumer_log" || {
            log_error "Consumer(PR) (env=${target_env}) \u542f\u52a8\u5931\u8d25\uff0c\u8df3\u8fc7\u8be5\u7528\u4f8b"
            continue
        }
        local consumer_pid="$_STARTED_PID"

        run_positive_case "B" "$target_env" "$consumer_port" "$result_file"
        b_grand_total=$((b_grand_total + ENV_TOTAL))
        b_grand_success=$((b_grand_success + ENV_SUCCESS))
        b_grand_fail=$((b_grand_fail + ENV_FAIL))
        b_grand_correct=$((b_grand_correct + ENV_CORRECT))
        b_grand_wrong=$((b_grand_wrong + ENV_WRONG))

        stop_consumer "$consumer_pid"
        sleep 1
    done

    # ==================== \u6b65\u9aa47\uff1a\u94fe\u8defB \u8d1f\u7528\u4f8b ====================
    log_step "7/8 \u94fe\u8defB \u8d1f\u7528\u4f8b\uff1aconsumer env=${NEGATIVE_ENV}\uff08\u4e0d\u5b58\u5728\uff09"
    if [[ "$RUN_NEGATIVE_CASE" == "true" ]]; then
        local consumer_port=$((CONSUMER_PORT_BASE + env_index + 1))
        local consumer_name="consumer_pr_${NEGATIVE_ENV}"
        local consumer_log="${LOG_DIR}/${consumer_name}.log"

        if start_consumer_pr "$consumer_name" "env=${NEGATIVE_ENV}" "$consumer_port" "$consumer_log"; then
            local consumer_pid="$_STARTED_PID"
            run_negative_case "B" "$NEGATIVE_ENV" "$consumer_port" "$result_file"
            b_neg_total=$NEG_TOTAL
            b_neg_as_expected=$NEG_AS_EXPECTED
            b_neg_unexpected=$NEG_UNEXPECTED
            stop_consumer "$consumer_pid"
        else
            log_warn "[\u94fe\u8defB] \u8d1f\u7528\u4f8b Consumer \u542f\u52a8\u5931\u8d25\uff0c\u8df3\u8fc7"
        fi
    else
        log_info "\u5df2\u901a\u8fc7 --no-negative-case \u8df3\u8fc7\u94fe\u8defB \u8d1f\u7528\u4f8b"
    fi

    # \u5408\u5e76\u7edf\u8ba1\uff0c\u4ee5\u4fbf\u540e\u7eed\u603b\u7ed3\u8bba\u4f7f\u7528
    local grand_total=$((a_grand_total + b_grand_total))
    local grand_success=$((a_grand_success + b_grand_success))
    local grand_fail=$((a_grand_fail + b_grand_fail))
    local grand_correct=$((a_grand_correct + b_grand_correct))
    local grand_wrong=$((a_grand_wrong + b_grand_wrong))
    local negative_total=$((a_neg_total + b_neg_total))
    local negative_as_expected=$((a_neg_as_expected + b_neg_as_expected))
    local negative_unexpected=$((a_neg_unexpected + b_neg_unexpected))

    # ==================== \u6b65\u9aa48: \u7ed3\u679c\u6c47\u603b\u4e0e\u9a8c\u8bc1\u7ed3\u8bba ====================
    log_step "8/8 \u7ed3\u679c\u6c47\u603b"

    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║          元数据路由验证结果汇总                  ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "  服务名:              ${SERVICE_NAME}"
    echo "  命名空间:            ${NAMESPACE}"
    echo "  测试 env 列表:       ${ROUTE_ENVS}"
    echo ""
    echo -e "  ${CYAN}── Provider 实例 ──${NC}"
    echo "  Provider-dev:        env=dev  (端口: ${PROVIDER_DEV_ACTUAL_PORT})"
    echo "  Provider-test:       env=test (端口: ${PROVIDER_TEST_ACTUAL_PORT})"
    echo "  Provider-pre:        env=pre  (端口: ${PROVIDER_PRE_ACTUAL_PORT})"
    echo "  Provider-prod:       env=prod (端口: ${PROVIDER_PROD_ACTUAL_PORT})"
    echo ""
    echo -e "  ${CYAN}── 正用例统计 ──${NC}"
    echo "  总请求数:            ${grand_total}"
    echo "  HTTP 成功:           ${grand_success}"
    echo "  HTTP 失败:           ${grand_fail}"
    echo "  路由正确:            ${grand_correct}"
    echo "  路由错误:            ${grand_wrong}"
    if [[ "$RUN_NEGATIVE_CASE" == "true" ]]; then
        echo ""
        echo -e "  ${CYAN}── 负用例统计 ──${NC}"
        echo "  总请求数:            ${negative_total}"
        echo "  符合预期:            ${negative_as_expected}"
        echo "  异常（意外路由）:    ${negative_unexpected}"
    fi
    echo ""
    echo "  详细结果 CSV:        ${result_file}"
    echo "  Provider-dev 日志:   ${LOG_DIR}/provider_dev.log"
    echo "  Provider-test 日志:  ${LOG_DIR}/provider_test.log"
    echo "  Provider-pre 日志:   ${LOG_DIR}/provider_pre.log"
    echo "  Provider-prod 日志:  ${LOG_DIR}/provider_prod.log"
    echo "  Consumer 日志:       ${LOG_DIR}/consumer_*.log"
    echo ""

    # ==================== 步骤7: 验证结论 ====================
    log_step "7/7 验证结论"

    local test_passed=true

    # 正用例 #1：请求是否全部成功
    if [[ "$grand_total" -gt 0 ]] && [[ "$grand_success" -eq "$grand_total" ]]; then
        log_info "✅ 正用例所有请求均成功 (${grand_success}/${grand_total})"
    elif [[ "$grand_success" -gt 0 ]]; then
        log_warn "⚠️  正用例部分请求失败 (成功: ${grand_success}/${grand_total})"
    else
        log_error "❌ 正用例所有请求均失败"
        test_passed=false
    fi

    # 正用例 #2：路由是否全部正确
    if [[ "$grand_correct" -eq "$grand_success" ]] && [[ "$grand_success" -gt 0 ]]; then
        log_info "✅ 正用例所有成功请求均路由到匹配的 Provider"
    else
        if [[ "$grand_wrong" -gt 0 ]]; then
            log_error "❌ 有 ${grand_wrong} 个请求路由到了不匹配 env 的 Provider"
            test_passed=false
        fi
    fi

    # 负用例：无意外路由
    if [[ "$RUN_NEGATIVE_CASE" == "true" ]] && [[ "$negative_total" -gt 0 ]]; then
        if [[ "$negative_unexpected" -eq 0 ]]; then
            log_info "✅ 负用例：env=${NEGATIVE_ENV} 未路由到任何 Provider，dstMetaRouter 过滤正确"
        else
            log_error "❌ 负用例：env=${NEGATIVE_ENV} 有 ${negative_unexpected} 个请求被路由到 Provider"
            test_passed=false
        fi
    fi

    echo ""
    if [[ "$test_passed" == "true" ]] && [[ "$grand_success" -eq "$grand_total" ]]; then
        echo -e "${GREEN}╔══════════════════════════════════════════════════╗${NC}"
        echo -e "${GREEN}║  验证结论: ✅ 元数据路由功能正常生效！            ║${NC}"
        echo -e "${GREEN}╚══════════════════════════════════════════════════╝${NC}"
        echo ""
        echo -e "${GREEN}  - Consumer env=dev  → 仅路由到 Provider-dev${NC}"
        echo -e "${GREEN}  - Consumer env=test → 仅路由到 Provider-test${NC}"
        echo -e "${GREEN}  - Consumer env=pre  → 仅路由到 Provider-pre${NC}"
        echo -e "${GREEN}  - Consumer env=prod → 仅路由到 Provider-prod${NC}"
        if [[ "$RUN_NEGATIVE_CASE" == "true" ]]; then
            echo -e "${GREEN}  - Consumer env=${NEGATIVE_ENV} → 无可用实例（严格过滤）${NC}"
        fi
    elif [[ "$test_passed" == "true" ]]; then
        echo -e "${YELLOW}验证结论: ⚠️  元数据路由基本正常，但部分请求失败${NC}"
        echo -e "${YELLOW}  请检查网络连接和服务状态${NC}"
    else
        echo -e "${RED}╔══════════════════════════════════════════════════╗${NC}"
        echo -e "${RED}║  验证结论: ❌ 元数据路由功能验证未通过            ║${NC}"
        echo -e "${RED}╚══════════════════════════════════════════════════╝${NC}"
        echo ""
        echo -e "${RED}  请检查以下配置:${NC}"
        echo -e "${RED}  1. Consumer 的 polaris.yaml 是否配置了 dstMetaRouter 路由链${NC}"
        echo -e "${RED}  2. Provider 实例是否正确注册了 env 元数据标签${NC}"
        echo -e "${RED}  3. Consumer 是否正确设置了 GetOneInstanceRequest.Metadata${NC}"
    fi

    echo ""
    echo -e "${BLUE}提示: 查看详细日志:${NC}"
    echo -e "${BLUE}  测试总日志:    cat ${TEST_LOG_FILE}${NC}"
    echo -e "${BLUE}  Provider-dev:  cat ${LOG_DIR}/provider_dev.log${NC}"
    echo -e "${BLUE}  Provider-test: cat ${LOG_DIR}/provider_test.log${NC}"
    echo -e "${BLUE}  Provider-pre:  cat ${LOG_DIR}/provider_pre.log${NC}"
    echo -e "${BLUE}  Provider-prod: cat ${LOG_DIR}/provider_prod.log${NC}"
    echo -e "${BLUE}  Consumer logs: ls ${LOG_DIR}/consumer_*.log${NC}"
    echo ""
}

main "$@"
