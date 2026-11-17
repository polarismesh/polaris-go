#!/usr/bin/env bash
# Run AI Registry E2E on a cloud host and always try to produce a log archive.
#
# This script is copied into the cloud package by build-cloud-package.sh.
set -euo pipefail
umask 077

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
BINARY="${SCRIPT_DIR}/polaris-go-ai-registry"
INVOCATION_DIR="$PWD"

SERVER="${POLARIS_SERVER:-}"
SUITE="${POLARIS_E2E_SUITE:-all}"
NAMESPACE="${POLARIS_E2E_NAMESPACE:-go-sdk-integration}"
TIMEOUT="2m"
RUN_TIMEOUT="35m"
LOG_ROOT="${SCRIPT_DIR}/.logs"
CONFIG_FILE=""
RUN_ID=""
DEBUG=false
CLEANUP=false
CLEANUP_SKILLS=false
EXTRA_ARGS=()

usage() {
    cat <<'EOF'
用法: run-cloud.sh --server <polaris-host> [选项] [-- 额外二进制参数]

在云端执行 AI Registry 真实 E2E。脚本把标准输出同时写入
.logs/<run-id>/run-cloud.log，运行结束后自动调用 pack-cloud-logs.sh。

必填:
  --server <host>          Polaris 服务端主机名/IP；端口按 8090/8091/8093/8094 推导

常用选项:
  --suite <all|agent|mcp|skill>  验证范围，默认 all
  --namespace <ns>               隔离命名空间，默认 go-sdk-integration
  --timeout <duration>           单次操作超时，默认 2m
  --run-timeout <duration>       全量运行超时，默认 35m
  --config-file <path>           可选 SDK 配置；相对路径按当前目录解析
  --run-id <id>                  指定 run ID；默认 UTC 时间戳+PID
  --log-root <dir>               日志根目录，默认 ./.logs
  --debug                        开启 SDK DEBUG
  --cleanup                      运行后停止 Provider，并清理本 run 的服务/实例
  --cleanup-skills               运行后删除本示例持有的三个 Skill fixture
  -h, --help                     显示帮助

默认保留 Provider、服务和 Skill，便于云端现场复核。
Token 只从环境变量读取，不写入 run.env 或日志：
  POLARIS_TOKEN / POLARIS_E2E_SERVICE_TOKEN / POLARIS_SKILL_TOKEN / POLARIS_CONFIG_TOKEN

示例:
  ./run-cloud.sh --server 1.14.219.4 --suite all
  POLARIS_TOKEN=*** ./run-cloud.sh --server 1.14.219.4 --cleanup
EOF
}

die() {
    echo "ERROR: $*" >&2
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --server)
            [[ $# -ge 2 ]] || die "--server requires a value"
            SERVER="$2"
            shift 2
            ;;
        --server=*)
            SERVER="${1#*=}"
            shift
            ;;
        --suite)
            [[ $# -ge 2 ]] || die "--suite requires a value"
            SUITE="$2"
            shift 2
            ;;
        --suite=*)
            SUITE="${1#*=}"
            shift
            ;;
        --namespace)
            [[ $# -ge 2 ]] || die "--namespace requires a value"
            NAMESPACE="$2"
            shift 2
            ;;
        --namespace=*)
            NAMESPACE="${1#*=}"
            shift
            ;;
        --timeout)
            [[ $# -ge 2 ]] || die "--timeout requires a value"
            TIMEOUT="$2"
            shift 2
            ;;
        --timeout=*)
            TIMEOUT="${1#*=}"
            shift
            ;;
        --run-timeout)
            [[ $# -ge 2 ]] || die "--run-timeout requires a value"
            RUN_TIMEOUT="$2"
            shift 2
            ;;
        --run-timeout=*)
            RUN_TIMEOUT="${1#*=}"
            shift
            ;;
        --config-file)
            [[ $# -ge 2 ]] || die "--config-file requires a value"
            CONFIG_FILE="$2"
            shift 2
            ;;
        --config-file=*)
            CONFIG_FILE="${1#*=}"
            shift
            ;;
        --run-id)
            [[ $# -ge 2 ]] || die "--run-id requires a value"
            RUN_ID="$2"
            shift 2
            ;;
        --run-id=*)
            RUN_ID="${1#*=}"
            shift
            ;;
        --log-root)
            [[ $# -ge 2 ]] || die "--log-root requires a value"
            LOG_ROOT="$2"
            shift 2
            ;;
        --log-root=*)
            LOG_ROOT="${1#*=}"
            shift
            ;;
        --debug)
            DEBUG=true
            shift
            ;;
        --cleanup)
            CLEANUP=true
            shift
            ;;
        --cleanup-skills)
            CLEANUP_SKILLS=true
            shift
            ;;
        --)
            shift
            EXTRA_ARGS=("$@")
            break
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            die "unknown argument: $1"
            ;;
    esac
done

[[ -n "${SERVER}" ]] || die "provide --server or POLARIS_SERVER"
[[ -x "${BINARY}" ]] || die "binary not found or not executable: ${BINARY}"
[[ -x "${SCRIPT_DIR}/pack-cloud-logs.sh" ]] || die "log packer not found: ${SCRIPT_DIR}/pack-cloud-logs.sh"
[[ -x "${SCRIPT_DIR}/ai-registry-summarize" ]] || die "summarizer not found: ${SCRIPT_DIR}/ai-registry-summarize"

if [[ -n "${CONFIG_FILE}" ]]; then
    if [[ "${CONFIG_FILE}" != /* ]]; then
        CONFIG_FILE="${INVOCATION_DIR}/${CONFIG_FILE}"
    fi
    [[ -f "${CONFIG_FILE}" ]] || die "config file not found: ${CONFIG_FILE}"
fi

if [[ -z "${RUN_ID}" ]]; then
    RUN_ID="$(date -u +%Y%m%d%H%M%S)-$$"
fi
if [[ ! "${RUN_ID}" =~ ^[A-Za-z0-9_.-]+$ ]]; then
    die "run-id may contain only letters, digits, dot, underscore and dash"
fi

if [[ "${LOG_ROOT}" != /* ]]; then
    LOG_ROOT="${INVOCATION_DIR}/${LOG_ROOT}"
fi
mkdir -p "${LOG_ROOT}"
LOG_ROOT="$(cd "${LOG_ROOT}" && pwd)"
RUN_DIR="${LOG_ROOT}/${RUN_ID}"
[[ ! -e "${RUN_DIR}" ]] || die "run directory already exists: ${RUN_DIR}"
mkdir -p "${RUN_DIR}"

RUNNER_LOG="${RUN_DIR}/run-cloud.log"
RUN_ENV="${RUN_DIR}/run.env"
if [[ -f "${SCRIPT_DIR}/package.env" ]]; then
    cp "${SCRIPT_DIR}/package.env" "${RUN_DIR}/package.env"
fi

started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
cat > "${RUN_ENV}" <<EOF
RUN_ID=${RUN_ID}
SERVER=${SERVER}
SUITE=${SUITE}
NAMESPACE=${NAMESPACE}
TIMEOUT=${TIMEOUT}
RUN_TIMEOUT=${RUN_TIMEOUT}
DEBUG=${DEBUG}
CLEANUP=${CLEANUP}
CLEANUP_SKILLS=${CLEANUP_SKILLS}
CONFIG_FILE=${CONFIG_FILE}
STARTED_AT_UTC=${started_at}
PACKAGE_DIR=${SCRIPT_DIR}
EOF

ARGS=(
    -mode=verify
    -server "${SERVER}"
    -suite "${SUITE}"
    -namespace "${NAMESPACE}"
    -timeout "${TIMEOUT}"
    -run-timeout "${RUN_TIMEOUT}"
    -run-id "${RUN_ID}"
    -run-dir "${RUN_DIR}"
)
if [[ -n "${CONFIG_FILE}" ]]; then
    ARGS+=(-config-file "${CONFIG_FILE}")
fi
if [[ "${DEBUG}" == "true" ]]; then
    ARGS+=(-debug)
fi
if [[ "${CLEANUP}" == "true" ]]; then
    ARGS+=(-services -processes)
fi
if [[ "${CLEANUP_SKILLS}" == "true" ]]; then
    ARGS+=(-skills)
fi
ARGS+=(${EXTRA_ARGS[@]+"${EXTRA_ARGS[@]}"})

{
    echo "============================================================"
    echo "AI Registry cloud verification"
    echo "run_id=${RUN_ID}"
    echo "server=${SERVER}"
    echo "suite=${SUITE}"
    echo "namespace=${NAMESPACE}"
    echo "started_at_utc=${started_at}"
    echo "binary=${BINARY}"
    echo "command=${BINARY} ${ARGS[*]}"
    echo "============================================================"
} > "${RUNNER_LOG}"

echo "运行目录: ${RUN_DIR}"
echo "日志: ${RUNNER_LOG}"
echo "Token 只从环境变量读取，不写入日志。"

set +e
"${BINARY}" "${ARGS[@]}" 2>&1 | tee -a "${RUNNER_LOG}"
binary_rc=${PIPESTATUS[0]}
set -e

finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
{
    echo "FINISHED_AT_UTC=${finished_at}"
    echo "VERIFY_EXIT_CODE=${binary_rc}"
} >> "${RUN_ENV}"
summarizer="${SCRIPT_DIR}/ai-registry-summarize"
summary_rc=0
if [[ -x "${summarizer}" ]]; then
    set +e
    "${summarizer}" \
        --strict \
        --output "${RUN_DIR}/analysis.md" \
        --json "${RUN_DIR}/analysis.json" \
        "${RUN_DIR}" 2>&1 | tee -a "${RUNNER_LOG}"
    summary_rc=${PIPESTATUS[0]}
    set -e
fi
echo "ANALYSIS_EXIT_CODE=${summary_rc}" >> "${RUN_ENV}"

pack_stamp="$(date -u +%Y%m%d_%H%M%S)"
pack_output="${LOG_ROOT}/ai-registry-logs-${RUN_ID}-${pack_stamp}.tar.gz"
set +e
"${SCRIPT_DIR}/pack-cloud-logs.sh" --run-dir "${RUN_DIR}" --output "${pack_output}" 2>&1 | tee -a "${RUNNER_LOG}"
pack_rc=${PIPESTATUS[0]}
set -e

if [[ "${binary_rc}" -ne 0 ]]; then
    exit "${binary_rc}"
fi
if [[ "${summary_rc}" -ne 0 ]]; then
    exit "${summary_rc}"
fi
exit "${pack_rc}"
