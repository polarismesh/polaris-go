#!/bin/bash
# =============================================================================
# callAuditLog 集成测试验证脚本（本地 mock 自包含版，无需外部 Polaris 服务端）
#
# 日志汇总（参考 examples/auth）：所有输出集中到脚本目录下的 .logs/：
#   .logs/verify-audit-local-<时间戳>.log  脚本自身输出（双写，去 ANSI 颜色）
#   .logs/audit_test.log                   demo 进程 stdout/stderr（含 mock 服务端 + SDK 日志）
#   .logs/run/polaris/log/                 SDK 文件日志（base/network/cache/route/...）
#   .logs/run/polaris/log/audit/polaris-audit.log  审计日志（本 demo 的核心产物）
# 编译产物集中到 .build/。
#
# 使用方法：
#   bash verify.sh
# =============================================================================

set -o pipefail

echo "=== callAuditLog 集成测试验证脚本（本地 mock 自包含）==="
echo ""

# ======================== 目录与日志 ========================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_DIR="${SCRIPT_DIR}/.build"
LOG_DIR="${SCRIPT_DIR}/.logs"
# demo 运行工作目录：SDK 文件日志与审计日志落到其 polaris/log/ 下
RUN_DIR="${LOG_DIR}/run"
DEMO_LOG="${LOG_DIR}/audit_test.log"
TEST_LOG_FILE="${LOG_DIR}/verify-audit-local-$(date +%Y%m%d_%H%M%S).log"
# 审计日志实际落盘位置：demo 在 RUN_DIR 工作目录下运行，
# polaris.yaml 的 rotateOutputPath=./polaris/log/audit/polaris-audit.log（相对工作目录）
AUDIT_LOG="${RUN_DIR}/polaris/log/audit/polaris-audit.log"

mkdir -p "$BUILD_DIR" "$LOG_DIR" "$RUN_DIR"
# 清理上次运行的 SDK/审计日志与 stdout 日志，保证本次验证独立
rm -rf "${RUN_DIR}/polaris"
rm -f "$DEMO_LOG"

# _log 把消息同时输出到 stdout 和脚本主日志（写文件时去除 ANSI 颜色码）
_log() {
    echo -e "$1"
    echo -e "$1" | sed 's/\x1b\[[0-9;]*m//g' >> "${TEST_LOG_FILE}" 2>/dev/null || true
}

_log "日志目录: ${LOG_DIR}"
_log ""

# ======================== 编译 ========================
_log "1. 编译集成测试程序到 ${BUILD_DIR}..."
if ! go build -o "${BUILD_DIR}/audit_test" main.go; then
    _log "❌ 编译失败"
    exit 1
fi
_log "✅ 编译成功"
_log ""

# ======================== 运行 ========================
# demo 用相对路径加载 polaris.yaml，故复制一份到运行工作目录
cp "${SCRIPT_DIR}/polaris.yaml" "${RUN_DIR}/polaris.yaml"

_log "2. 运行集成测试（自带 mock Polaris 服务端，无需外部 Polaris 部署）..."
_log ""
# 前台运行：stdout/stderr 用 tee 同时输出到终端与 audit_test.log；PIPESTATUS 取真实退出码
(cd "$RUN_DIR" && "${BUILD_DIR}/audit_test") 2>&1 | tee "${DEMO_LOG}"
ret=${PIPESTATUS[0]}
# 把 demo 输出并入脚本主日志（去色）
sed 's/\x1b\[[0-9;]*m//g' "${DEMO_LOG}" >> "${TEST_LOG_FILE}" 2>/dev/null || true
_log ""

_log "日志汇总目录：${LOG_DIR}"
_log "  demo stdout : ${DEMO_LOG}"
_log "  SDK 文件日志 : ${RUN_DIR}/polaris/log/"
_log "  审计日志     : ${AUDIT_LOG}"
_log "  脚本主日志   : ${TEST_LOG_FILE}"
_log ""

if [ "${ret}" -eq 0 ]; then
    _log "✅ 集成测试通过：审计日志已生成"
    exit 0
else
    _log "❌ 集成测试失败"
    exit 1
fi
