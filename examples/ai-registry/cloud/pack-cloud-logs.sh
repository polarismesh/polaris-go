#!/usr/bin/env bash
# Pack AI Registry run logs and lightweight diagnostics on a cloud host.
#
# The archive is a tar.gz with a top-level directory containing one or more run
# directories and a diagnostics directory. It never copies environment variables
# or SDK config files.
set -euo pipefail
umask 077

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
INVOCATION_DIR="$PWD"
LOG_ROOT="${SCRIPT_DIR}/.logs"
RUN_DIR=""
OUTPUT=""
PACK_ALL=false

usage() {
    cat <<'EOF'
用法: pack-cloud-logs.sh [选项]

把云端 .logs/<run-id>/ 打包为 tar.gz，便于 scp 回本地分析。

选项:
  --run-dir <dir>   指定单次运行目录
  --log-root <dir>  日志根目录，默认包内 .logs
  --all             打包日志根目录下的全部运行目录
  --output <path>   输出文件；默认 <包目录>/ai-registry-logs-<run|all>-<时间>.tar.gz
  -h, --help        显示帮助

默认优先选择包含 state.json 或 result.json 的最新运行目录。
EOF
}

die() {
    echo "ERROR: $*" >&2
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --run-dir)
            [[ $# -ge 2 ]] || die "--run-dir requires a value"
            RUN_DIR="$2"
            shift 2
            ;;
        --run-dir=*)
            RUN_DIR="${1#*=}"
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
        --output)
            [[ $# -ge 2 ]] || die "--output requires a value"
            OUTPUT="$2"
            shift 2
            ;;
        --output=*)
            OUTPUT="${1#*=}"
            shift
            ;;
        --all)
            PACK_ALL=true
            shift
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

if [[ "${PACK_ALL}" == "true" && -n "${RUN_DIR}" ]]; then
    die "--all cannot be combined with --run-dir"
fi

if [[ "${LOG_ROOT}" != /* ]]; then
    LOG_ROOT="${INVOCATION_DIR}/${LOG_ROOT}"
fi

declare -a LOG_DIRS=()
if [[ "${PACK_ALL}" == "true" || -z "${RUN_DIR}" ]]; then
    [[ -d "${LOG_ROOT}" ]] || die "log root not found: ${LOG_ROOT}"
    LOG_ROOT="$(cd "${LOG_ROOT}" && pwd)"
    while IFS= read -r dir; do
        LOG_DIRS+=("${dir}")
    done < <(find "${LOG_ROOT}" -mindepth 1 -maxdepth 1 -type d -print | sort)
    [[ ${#LOG_DIRS[@]} -gt 0 ]] || die "no run directories under ${LOG_ROOT}"
fi

declare -a SELECTED=()
archive_name=""
if [[ "${PACK_ALL}" == "true" ]]; then
    SELECTED=("${LOG_DIRS[@]}")
    archive_name="all"
else
    if [[ -z "${RUN_DIR}" ]]; then
        latest_with_result=""
        latest_any=""
        for dir in "${LOG_DIRS[@]}"; do
            latest_any="${dir}"
            if [[ -f "${dir}/state.json" || -f "${dir}/result.json" ]]; then
                latest_with_result="${dir}"
            fi
        done
        RUN_DIR="${latest_with_result:-${latest_any}}"
    fi
    if [[ "${RUN_DIR}" != /* ]]; then
        RUN_DIR="${INVOCATION_DIR}/${RUN_DIR}"
    fi
    [[ -d "${RUN_DIR}" ]] || die "run directory not found: ${RUN_DIR}"
    RUN_DIR="$(cd "${RUN_DIR}" && pwd)"
    SELECTED=("${RUN_DIR}")
    archive_name="$(basename "${RUN_DIR}")"
fi

timestamp="$(date -u +%Y%m%d_%H%M%S)"
TMP_BASE="$(mktemp -d "${TMPDIR:-/tmp}/ai-registry-logs.XXXXXX")"
trap 'rm -rf "${TMP_BASE}"' EXIT

stage_name="ai-registry-logs-${archive_name}-${timestamp}"
stage_dir="${TMP_BASE}/${stage_name}"
mkdir -p "${stage_dir}"

copy_tree_contents() {
    local src="$1"
    local dst="$2"
    mkdir -p "${dst}"
    if cp -a "${src}/." "${dst}/" 2>/dev/null; then
        return 0
    fi
    cp -R -p "${src}/." "${dst}/"
}

summarizer="${SCRIPT_DIR}/ai-registry-summarize"
repo_root="$(cd -- "${SCRIPT_DIR}/../../.." 2>/dev/null && pwd || true)"
for dir in "${SELECTED[@]}"; do
    if [[ -x "${summarizer}" ]]; then
        "${summarizer}" \
            --output "${dir}/analysis.md" \
            --json "${dir}/analysis.json" \
            "${dir}" >/dev/null || true
    elif [[ -n "${repo_root}" && -f "${repo_root}/go.mod" ]] && command -v go >/dev/null 2>&1; then
        (
            cd "${repo_root}"
            go run ./examples/ai-registry/cloud/summarize \
                --output "${dir}/analysis.md" \
                --json "${dir}/analysis.json" \
                "${dir}" >/dev/null
        ) || true
    fi
done

for dir in "${SELECTED[@]}"; do
    run_name="$(basename "${dir}")"
    copy_tree_contents "${dir}" "${stage_dir}/${run_name}"
done

mkdir -p "${stage_dir}/diagnostics"
{
    echo "archive_created_at_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo "packaged_on_host=$(hostname 2>/dev/null || echo unknown)"
    echo "log_root=${LOG_ROOT}"
    echo "runs=${#SELECTED[@]}"
    for dir in "${SELECTED[@]}"; do
        echo "run_dir=${dir}"
    done
} > "${stage_dir}/diagnostics/context.txt"

if [[ -f "${SCRIPT_DIR}/package.env" ]]; then
    cp "${SCRIPT_DIR}/package.env" "${stage_dir}/diagnostics/package.env"
fi
{
    echo "uname:"
    uname -a 2>&1 || true
    echo
    echo "date_utc: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo
    echo "disk:"
    df -h 2>&1 || true
} > "${stage_dir}/diagnostics/runtime.txt"
if command -v free >/dev/null 2>&1; then
    free -m > "${stage_dir}/diagnostics/memory.txt" 2>&1 || true
fi
ps -eo pid,ppid,etime,args 2>/dev/null \
    | grep -F "${SCRIPT_DIR}/polaris-go-ai-registry" \
    | grep -v grep > "${stage_dir}/diagnostics/processes.txt" || true

(
    cd "${stage_dir}"
    find . -type f -print | sort
) > "${stage_dir}/archive-contents.txt"

if [[ -z "${OUTPUT}" ]]; then
    OUTPUT="${SCRIPT_DIR}/ai-registry-logs-${archive_name}-${timestamp}.tar.gz"
fi
case "${OUTPUT}" in
    *.tar.gz) ;;
    *.zip) die "zip output is not supported; use .tar.gz" ;;
    *) OUTPUT="${OUTPUT}.tar.gz" ;;
esac
if [[ "${OUTPUT}" != /* ]]; then
    OUTPUT="${INVOCATION_DIR}/${OUTPUT}"
fi
mkdir -p "$(dirname "${OUTPUT}")"
OUTPUT="$(cd "$(dirname "${OUTPUT}")" && pwd)/$(basename "${OUTPUT}")"

tar -czf "${OUTPUT}" -C "${TMP_BASE}" "${stage_name}"
chmod 0600 "${OUTPUT}"

sha256_file() {
    local file="$1"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "${file}" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "${file}" | awk '{print $1}'
    else
        echo "unavailable"
    fi
}

archive_sha="$(sha256_file "${OUTPUT}")"
if [[ "${archive_sha}" != "unavailable" ]]; then
    printf '%s  %s\n' "${archive_sha}" "$(basename "${OUTPUT}")" > "${OUTPUT}.sha256"
    chmod 0600 "${OUTPUT}.sha256"
fi

echo "日志包: ${OUTPUT}"
echo "大小: $(du -h "${OUTPUT}" | cut -f1)"
if [[ -f "${OUTPUT}.sha256" ]]; then
    echo "校验文件: ${OUTPUT}.sha256"
fi
echo "传回本地: scp '<user>@<cloud-host>:${OUTPUT}*' ."
