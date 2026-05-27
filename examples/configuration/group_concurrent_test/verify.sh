#!/bin/bash
# =============================================================================
# E2E 验证脚本：ConfigGroupFlow 并发获取配置分组
#
# 用法:
#   ./verify.sh -s 127.0.0.1
#   ./verify.sh -s 127.0.0.1 -n default -p e2e-grp -c 20 -t 10
#   ./verify.sh --help
#
# 流程:
#   1. 检查/创建测试用配置分组（如果已存在则跳过）
#   2. 等待 5 秒
#   3. 编译并运行 Go 测试程序（并发获取）
#   4. 输出测试结果
#
# 日志:
#   脚本输出同时写入终端和 .logs/verify.log
#   Demo 二进制输出保存在 .logs/demo.log
# =============================================================================

set -e

# ----- 默认值 -----
POLARIS_SERVER=""
NAMESPACE="default"
GROUP_PREFIX="e2e-concurrent-grp"
GROUP_COUNT=20
THRESHOLD=10

# ----- 参数解析 -----
usage() {
    echo "用法: $0 [选项]"
    echo ""
    echo "选项:"
    echo "  -s, --server <addr>     Polaris 服务端 IP/主机名 (必填)"
    echo "  -n, --namespace <ns>    命名空间 (默认: default)"
    echo "  -p, --prefix <prefix>   配置分组名称前缀 (默认: e2e-concurrent-grp)"
    echo "  -c, --count <num>       配置分组数量 (默认: 20)"
    echo "  -t, --threshold <sec>   并发获取最大允许秒数 (默认: 10)"
    echo "  -h, --help              显示帮助信息"
    echo ""
    echo "示例:"
    echo "  $0 -s 127.0.0.1"
    echo "  $0 -s 127.0.0.1 -c 50 -t 15"
    echo "  $0 --server 10.0.0.1 --namespace production --prefix my-grp"
    exit 0
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        -s|--server)
            POLARIS_SERVER="$2"
            shift 2
            ;;
        -n|--namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        -p|--prefix)
            GROUP_PREFIX="$2"
            shift 2
            ;;
        -c|--count)
            GROUP_COUNT="$2"
            shift 2
            ;;
        -t|--threshold)
            THRESHOLD="$2"
            shift 2
            ;;
        -h|--help)
            usage
            ;;
        *)
            echo "未知参数: $1"
            echo "使用 $0 --help 查看帮助"
            exit 1
            ;;
    esac
done

# ----- 校验必填参数 -----
if [ -z "${POLARIS_SERVER}" ]; then
    echo "ERROR: 必须指定 Polaris 服务端地址"
    echo ""
    echo "用法: $0 -s <server_ip>"
    echo "示例: $0 -s 127.0.0.1"
    exit 1
fi

OPENAPI="http://${POLARIS_SERVER}:8090"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG_DIR="${SCRIPT_DIR}/.logs"
mkdir -p "${LOG_DIR}"

VERIFY_LOG="${LOG_DIR}/verify.log"
DEMO_LOG="${LOG_DIR}/demo.log"

# ----- 日志函数：同时输出到终端和日志文件 -----
log_info() {
    local msg="[INFO] $(date '+%Y-%m-%d %H:%M:%S') $*"
    echo "$msg" | tee -a "$VERIFY_LOG"
}

log_warn() {
    local msg="[WARN] $(date '+%Y-%m-%d %H:%M:%S') $*"
    echo "$msg" | tee -a "$VERIFY_LOG"
}

log_error() {
    local msg="[ERROR] $(date '+%Y-%m-%d %H:%M:%S') $*"
    echo "$msg" | tee -a "$VERIFY_LOG"
}

# 清空旧日志
> "$VERIFY_LOG"
> "$DEMO_LOG"

# ----- 开始 -----
log_info "=== ConfigGroupFlow 并发获取 E2E 验证 ==="
log_info ""
log_info "  POLARIS_SERVER : ${POLARIS_SERVER}"
log_info "  OPENAPI        : ${OPENAPI}"
log_info "  NAMESPACE      : ${NAMESPACE}"
log_info "  GROUP_PREFIX   : ${GROUP_PREFIX}"
log_info "  GROUP_COUNT    : ${GROUP_COUNT}"
log_info "  THRESHOLD      : ${THRESHOLD}s"
log_info "  日志目录       : ${LOG_DIR}"
log_info ""

# ----- Step 1: 检查/创建配置分组 -----
log_info "1. 检查服务端配置分组 ..."

existing_count=0
missing_groups=""

for i in $(seq -w 0 $(printf "%03d" $((GROUP_COUNT - 1)))); do
    group_name="${GROUP_PREFIX}-${i}"
    resp=$(curl -s "${OPENAPI}/config/v1/configfilegroups?namespace=${NAMESPACE}&group=${group_name}")
    total=$(echo "$resp" | grep -o '"total": *[0-9]*' | head -1 | grep -o '[0-9]*$')
    if [ "${total:-0}" -ge 1 ]; then
        existing_count=$((existing_count + 1))
    else
        missing_groups="${missing_groups} ${group_name}"
    fi
done

log_info "   已存在: ${existing_count}/${GROUP_COUNT} 个分组"

if [ ${existing_count} -ge ${GROUP_COUNT} ]; then
    log_info "   已有足够数量的配置分组，无需创建"
else
    missing_count=$((GROUP_COUNT - existing_count))
    log_info "   需要创建 ${missing_count} 个缺失的分组 ..."

    for group_name in ${missing_groups}; do
        # 创建分组
        curl -s "${OPENAPI}/config/v1/configfilegroups" \
            -X POST -H "Content-type:application/json" \
            -d "{\"name\":\"${group_name}\", \"namespace\":\"${NAMESPACE}\"}" > /dev/null

        # 创建配置文件
        curl -s "${OPENAPI}/config/v1/configfiles" \
            -X POST -H "Content-type:application/json" \
            -d "{\"namespace\":\"${NAMESPACE}\", \"group\":\"${group_name}\", \"name\":\"data.yaml\", \"content\":\"group: ${group_name}\\nkey: value\", \"format\":\"yaml\"}" > /dev/null

        # 发布配置文件
        curl -s "${OPENAPI}/config/v1/configfiles/release" \
            -X POST -H "Content-type:application/json" \
            -d "{\"namespace\":\"${NAMESPACE}\", \"group\":\"${group_name}\", \"fileName\":\"data.yaml\", \"name\":\"initial-release\"}" > /dev/null

        log_info "   创建: ${group_name}"
    done

    log_info "   所有缺失分组已创建完毕"
fi

# ----- Step 2: 等待 -----
log_info ""
log_info "2. 等待 5 秒，确保服务端数据就绪 ..."
sleep 5

# ----- Step 3: 编译 -----
log_info ""
log_info "3. 编译测试程序 ..."
cd "${SCRIPT_DIR}"
go build -o group_concurrent_test . 2>&1 | tee -a "$VERIFY_LOG"
if [ ${PIPESTATUS[0]} -ne 0 ]; then
    log_error "   编译失败"
    exit 1
fi
log_info "   编译成功"

# 构造分组名列表（逗号分隔）
group_list=""
for i in $(seq -w 0 $(printf "%03d" $((GROUP_COUNT - 1)))); do
    if [ -n "${group_list}" ]; then
        group_list="${group_list},"
    fi
    group_list="${group_list}${GROUP_PREFIX}-${i}"
done

# ----- Step 4: 运行并发获取测试 -----
log_info ""
log_info "4. 运行并发获取测试 ..."
log_info "   参数: ${GROUP_COUNT} 个分组, 阈值 ${THRESHOLD}s"
log_info "   Demo 日志: ${DEMO_LOG}"
log_info ""

# 运行二进制: stdout/stderr 同时输出到终端和 Demo 日志文件
./group_concurrent_test \
    -server "${POLARIS_SERVER}" \
    -namespace "${NAMESPACE}" \
    -groups "${group_list}" \
    -threshold "${THRESHOLD}" \
    2>&1 | tee -a "$DEMO_LOG" | tee -a "$VERIFY_LOG"

exit_code=${PIPESTATUS[0]}

# ----- 清理二进制 -----
rm -f group_concurrent_test

# ----- 结果 -----
log_info ""
if [ ${exit_code} -eq 0 ]; then
    log_info "=== 验证通过 ==="
else
    log_error "=== 验证失败 ==="
fi

log_info ""
log_info "日志文件:"
log_info "  脚本日志: ${VERIFY_LOG}"
log_info "  Demo 日志: ${DEMO_LOG}"

exit ${exit_code}
