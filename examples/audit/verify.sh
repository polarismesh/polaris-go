#!/bin/bash
# =============================================================================
# callAuditLog 远程验证脚本（连接真实 Polaris 服务端，被调 provider + 主调 consumer 分离）
#
# 日志汇总（参考 examples/auth）：所有输出集中到脚本目录下的 .logs/：
#   .logs/verify-audit-<时间戳>.log  脚本自身输出（双写，去 ANSI 颜色）
#   .logs/provider.log               provider 进程 stdout/stderr（含 demo 日志与 SDK 日志）
#   .logs/consumer.log               consumer 进程 stdout/stderr
#   .logs/provider_run/polaris/log/  provider 侧 SDK 文件日志（base/network/cache/route/...）
#   .logs/consumer_run/polaris/log/  consumer 侧 SDK 文件日志
#   .logs/consumer_run/polaris/log/audit/polaris-audit.log  审计日志（本 demo 的核心产物）
# 编译产物集中到 .build/。清理残留进程与目录用同目录 cleanup.sh。
#
# 使用方法（命令行参数优先于环境变量；callee 指被调服务）：
#   bash verify.sh --polaris-server 127.0.0.1:8091 [--callee-namespace default] [--callee-service LogAuditCallee] [--token xxx]
#   POLARIS_SERVER=127.0.0.1:8091 [NAMESPACE=default] [SERVICE=LogAuditCallee] [TOKEN=xxx] bash verify.sh
# =============================================================================

set -o pipefail

# 默认值：环境变量优先，可被下方 -- 命令行参数覆盖
POLARIS_SERVER="${POLARIS_SERVER:-}"
NAMESPACE="${NAMESPACE:-default}"
SERVICE="${SERVICE:-LogAuditCallee}"
TOKEN="${TOKEN:-}"
# 持续请求参数：默认 60s 内每 5s 一次
DURATION="${DURATION:-60s}"
INTERVAL="${INTERVAL:-5s}"

# usage 打印用法说明。
usage() {
    cat <<'EOF'
用法（命令行参数优先于环境变量；callee 指被调服务）：
  bash verify.sh --polaris-server <host:port> [选项]
  POLARIS_SERVER=<host:port> [NAMESPACE=..] [SERVICE=..] [TOKEN=..] bash verify.sh

选项：
  --polaris-server <host:port>  远程 Polaris 服务端地址（必填；等价环境变量 POLARIS_SERVER）
  --callee-namespace <ns>       被调服务命名空间（默认 default；等价 NAMESPACE）
  --callee-service <svc>        被调服务名（默认 LogAuditCallee；等价 SERVICE）
  --token <token>               服务访问 Token，服务端开鉴权时必填（等价 TOKEN）
  --duration <dur>              consumer 持续请求总时长（默认 60s；等价 DURATION）
  --interval <dur>              consumer 请求间隔（默认 5s；等价 INTERVAL）
  -h, --help                    显示本帮助
EOF
}

# 解析命令行参数，覆盖环境变量默认值
while [[ $# -gt 0 ]]; do
    case "$1" in
        --polaris-server)   POLARIS_SERVER="$2"; shift 2 ;;
        --callee-namespace) NAMESPACE="$2";      shift 2 ;;
        --callee-service)   SERVICE="$2";        shift 2 ;;
        --token)            TOKEN="$2";          shift 2 ;;
        --duration)         DURATION="$2";       shift 2 ;;
        --interval)         INTERVAL="$2";       shift 2 ;;
        -h|--help)          usage; exit 0 ;;
        *) echo "❌ 未知参数: $1"; echo ""; usage; exit 1 ;;
    esac
done

echo "=== callAuditLog 远程验证脚本（provider + consumer）==="
echo ""

# 远程验证依赖真实服务端，POLARIS_SERVER 必填
if [ -z "${POLARIS_SERVER}" ]; then
    echo "❌ 缺少 Polaris 服务端地址（--polaris-server 或环境变量 POLARIS_SERVER）"
    echo ""
    usage
    exit 1
fi

# ======================== 目录与日志 ========================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_DIR="${SCRIPT_DIR}/.build"
LOG_DIR="${SCRIPT_DIR}/.logs"
# provider/consumer 各自的运行工作目录：SDK 文件日志与审计日志会落到其 polaris/log/ 下
PROVIDER_RUN="${LOG_DIR}/provider_run"
CONSUMER_RUN="${LOG_DIR}/consumer_run"
PROVIDER_LOG="${LOG_DIR}/provider.log"
CONSUMER_LOG="${LOG_DIR}/consumer.log"
TEST_LOG_FILE="${LOG_DIR}/verify-audit-$(date +%Y%m%d_%H%M%S).log"
# 审计日志实际落盘位置：consumer 在 CONSUMER_RUN 工作目录下运行，
# consumer/polaris.yaml 的 rotateOutputPath=./polaris/log/audit/polaris-audit.log（相对工作目录）
AUDIT_LOG="${CONSUMER_RUN}/polaris/log/audit/polaris-audit.log"

mkdir -p "$BUILD_DIR" "$LOG_DIR" "$PROVIDER_RUN" "$CONSUMER_RUN"
# 清理上次运行的 SDK/审计日志与 stdout 日志，保证本次验证独立
rm -rf "${PROVIDER_RUN}/polaris" "${CONSUMER_RUN}/polaris"
rm -f "$PROVIDER_LOG" "$CONSUMER_LOG"

# _log 把消息同时输出到 stdout 和脚本主日志（写文件时去除 ANSI 颜色码）
_log() {
    echo -e "$1"
    echo -e "$1" | sed 's/\x1b\[[0-9;]*m//g' >> "${TEST_LOG_FILE}" 2>/dev/null || true
}

_log "配置信息："
_log "  Polaris 服务端: ${POLARIS_SERVER}"
_log "  命名空间:       ${NAMESPACE}"
_log "  被调服务:       ${SERVICE}"
_log "  Token:          $([ -n "${TOKEN}" ] && echo '<已设置>' || echo '<未设置>')"
_log "  持续请求:       每 ${INTERVAL} 一次，持续 ${DURATION}"
_log "  日志目录:       ${LOG_DIR}"
_log ""

# ======================== 编译 ========================
_log "1. 编译 provider 与 consumer 到 ${BUILD_DIR}..."
if ! go build -o "${BUILD_DIR}/audit_provider" ./provider; then
    _log "❌ provider 编译失败"
    exit 1
fi
if ! go build -o "${BUILD_DIR}/audit_consumer" ./consumer; then
    _log "❌ consumer 编译失败"
    exit 1
fi
_log "✅ 编译成功"
_log ""

# ======================== 启动 provider ========================
_log "2. 后台启动 provider（注册被调实例 ${NAMESPACE}/${SERVICE} 到 ${POLARIS_SERVER}）..."
# 用 pushd/popd 切到运行工作目录后直接后台启动二进制，使 $! 为 audit_provider 本身的 PID，
# 便于退出时精确 kill（若用 (cd ... && bin) & 子 shell，$! 是子 shell 的 PID，
# kill 后 audit_provider 会变孤儿进程残留）。
pushd "$PROVIDER_RUN" > /dev/null || exit 1
"${BUILD_DIR}/audit_provider" \
    -server "${POLARIS_SERVER}" \
    -config "${SCRIPT_DIR}/provider/polaris.yaml" \
    -namespace "${NAMESPACE}" \
    -service "${SERVICE}" \
    -token "${TOKEN}" \
    > "${PROVIDER_LOG}" 2>&1 &
PROVIDER_PID=$!
popd > /dev/null || exit 1
_log "provider pid=${PROVIDER_PID}, log=${PROVIDER_LOG}"

# 收尾：无论成功失败都终止 provider（编译产物与日志保留在 .build/.logs，交由 cleanup.sh 清理）
cleanup() {
    if [ -n "${PROVIDER_PID}" ] && kill -0 "${PROVIDER_PID}" 2>/dev/null; then
        kill "${PROVIDER_PID}" 2>/dev/null
        sleep 0.5
        # SIGTERM 后仍存活则 SIGKILL 兜底
        kill -0 "${PROVIDER_PID}" 2>/dev/null && kill -9 "${PROVIDER_PID}" 2>/dev/null
        wait "${PROVIDER_PID}" 2>/dev/null
    fi
}
trap cleanup EXIT

# ======================== 等待 provider 就绪 ========================
_log "3. 等待 provider 注册完成（最多 30s）..."
READY=0
for _ in $(seq 1 60); do
    if grep -q "PROVIDER_READY" "${PROVIDER_LOG}" 2>/dev/null; then
        READY=1
        _log "✅ provider 已就绪"
        break
    fi
    if ! kill -0 "${PROVIDER_PID}" 2>/dev/null; then
        _log "❌ provider 进程已退出，日志如下："
        _log "$(cat "${PROVIDER_LOG}")"
        exit 1
    fi
    sleep 0.5
done
if [ "${READY}" -ne 1 ]; then
    _log "❌ 等待 provider 就绪超时，日志如下："
    _log "$(cat "${PROVIDER_LOG}")"
    exit 1
fi
_log ""

# ======================== 运行 consumer ========================
_log "4. 运行 consumer（发现被调 → 每 ${INTERVAL} 调用+上报一次，持续 ${DURATION} → 验证审计日志）..."
_log ""
# consumer 前台运行：stdout/stderr 用 tee 同时输出到终端与 consumer.log；PIPESTATUS 取真实退出码
(cd "$CONSUMER_RUN" && "${BUILD_DIR}/audit_consumer" \
    -server "${POLARIS_SERVER}" \
    -config "${SCRIPT_DIR}/consumer/polaris.yaml" \
    -namespace "${NAMESPACE}" \
    -service "${SERVICE}" \
    -duration "${DURATION}" \
    -interval "${INTERVAL}" \
    -audit-log "./polaris/log/audit/polaris-audit.log" 2>&1) | tee "${CONSUMER_LOG}"
ret=${PIPESTATUS[0]}
# 把 consumer 输出并入脚本主日志（去色）
sed 's/\x1b\[[0-9;]*m//g' "${CONSUMER_LOG}" >> "${TEST_LOG_FILE}" 2>/dev/null || true
_log ""

_log "--- provider 日志（完整见 ${PROVIDER_LOG}）---"
_log "$(cat "${PROVIDER_LOG}")"
_log "--------------------"
_log ""
_log "日志汇总目录：${LOG_DIR}"
_log "  provider stdout : ${PROVIDER_LOG}"
_log "  consumer stdout : ${CONSUMER_LOG}"
_log "  provider SDK 日志: ${PROVIDER_RUN}/polaris/log/"
_log "  consumer SDK 日志: ${CONSUMER_RUN}/polaris/log/"
_log "  审计日志         : ${AUDIT_LOG}"
_log "  脚本主日志       : ${TEST_LOG_FILE}"
_log ""

if [ "${ret}" -eq 0 ]; then
    _log "✅ 远程验证通过：审计日志已生成"
    exit 0
else
    _log "❌ 远程验证失败"
    exit 1
fi
