#!/usr/bin/env bash
# Safely import an AI Registry cloud log archive for local analysis.
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../../.." && pwd)"
if [[ -f "${REPO_ROOT}/go.mod" ]]; then
    OUTPUT_ROOT="${REPO_ROOT}/examples/ai-registry/.logs/imported"
else
    REPO_ROOT="${SCRIPT_DIR}"
    OUTPUT_ROOT="${REPO_ROOT}/.logs/imported"
fi
ARCHIVE=""
OUTPUT_DIR=""
FORCE=false
STRICT=false
IMPORT_MARKER=".ai-registry-import"

usage() {
    cat <<'EOF'
用法: import-logs.sh ARCHIVE [选项]

选项:
  --output-dir <dir>  解压目录；默认 examples/ai-registry/.logs/imported/<archive-name>
  --force             覆盖此前由本脚本导入的目录
  --strict            存在失败 run 时返回非零
  -h, --help          显示帮助

若相邻存在 ARCHIVE.sha256，会先校验归档完整性。
归档中的绝对路径、..、符号链接、硬链接和设备文件会被拒绝。
EOF
}

die() {
    echo "ERROR: $*" >&2
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --output-dir)
            [[ $# -ge 2 ]] || die "--output-dir requires a value"
            OUTPUT_DIR="$2"
            shift 2
            ;;
        --output-dir=*)
            OUTPUT_DIR="${1#*=}"
            shift
            ;;
        --force)
            FORCE=true
            shift
            ;;
        --strict)
            STRICT=true
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        -*)
            die "unknown argument: $1"
            ;;
        *)
            [[ -z "${ARCHIVE}" ]] || die "only one archive may be imported"
            ARCHIVE="$1"
            shift
            ;;
    esac
done

[[ -n "${ARCHIVE}" ]] || die "ARCHIVE is required"
[[ -f "${ARCHIVE}" ]] || die "archive not found: ${ARCHIVE}"
ARCHIVE="$(cd "$(dirname "${ARCHIVE}")" && pwd)/$(basename "${ARCHIVE}")"
case "${ARCHIVE}" in
    *.tar.gz|*.tgz) ;;
    *) die "archive must end with .tar.gz or .tgz" ;;
esac

sidecar="${ARCHIVE}.sha256"
if [[ -f "${sidecar}" ]]; then
    if command -v sha256sum >/dev/null 2>&1; then
        (cd "$(dirname "${ARCHIVE}")" && sha256sum -c "$(basename "${sidecar}")")
    elif command -v shasum >/dev/null 2>&1; then
        (cd "$(dirname "${ARCHIVE}")" && shasum -a 256 -c "$(basename "${sidecar}")")
    else
        echo "WARN: sha256sum/shasum unavailable; checksum not verified" >&2
    fi
else
    echo "WARN: checksum sidecar not found: ${sidecar}" >&2
fi

entries="$(tar -tzf "${ARCHIVE}")"
[[ -n "${entries}" ]] || die "archive is empty"
while IFS= read -r entry; do
    [[ -n "${entry}" ]] || die "archive contains an empty path"
    case "${entry}" in
        /*) die "archive contains an absolute path: ${entry}" ;;
        *"\\"*) die "archive contains a backslash path: ${entry}" ;;
    esac
    case "/${entry}/" in
        *"/../"*) die "archive contains a parent traversal: ${entry}" ;;
    esac
done <<< "${entries}"

while IFS= read -r line; do
    [[ -n "${line}" ]] || continue
    case "${line:0:1}" in
        l|h|b|c|p|s)
            die "archive contains a link or special file: ${line}"
            ;;
    esac
done < <(tar -tvzf "${ARCHIVE}")

archive_name="$(basename "${ARCHIVE}")"
archive_name="${archive_name%.tar.gz}"
archive_name="${archive_name%.tgz}"
OUTPUT_DIR="${OUTPUT_DIR:-${OUTPUT_ROOT}/${archive_name}}"
if [[ -e "${OUTPUT_DIR}" ]]; then
    [[ "${FORCE}" == true ]] || die "output exists: ${OUTPUT_DIR} (use --force)"
    [[ -f "${OUTPUT_DIR}/${IMPORT_MARKER}" ]] ||
        die "--force only replaces a previous import directory: ${OUTPUT_DIR}"
    rm -rf -- "${OUTPUT_DIR}"
fi
mkdir -p "$(dirname "${OUTPUT_DIR}")"

tmp_dir="$(mktemp -d "${OUTPUT_DIR}.tmp.XXXXXX")"
cleanup() {
    [[ -z "${tmp_dir}" ]] || rm -rf -- "${tmp_dir}"
}
trap cleanup EXIT

tar -xzf "${ARCHIVE}" -C "${tmp_dir}"
if find "${tmp_dir}" -type l -print -quit | grep -q .; then
    die "archive extraction produced a symbolic link"
fi
printf 'archive=%s\n' "$(basename "${ARCHIVE}")" > "${tmp_dir}/${IMPORT_MARKER}"
mv "${tmp_dir}" "${OUTPUT_DIR}"
tmp_dir=""

summarize_run() {
    local run_dir="$1"
    local output
    local -a args
    output="$(mktemp "${TMPDIR:-/tmp}/ai-registry-summary.XXXXXX")"
    args=(--output "${run_dir}/analysis.md" --json "${run_dir}/analysis.json")
    [[ "${STRICT}" == false ]] || args+=(--strict)
    set +e
    if [[ -x "${SCRIPT_DIR}/ai-registry-summarize" ]]; then
        "${SCRIPT_DIR}/ai-registry-summarize" "${args[@]}" "${run_dir}" > "${output}"
    elif command -v go >/dev/null 2>&1; then
        (
            cd "${REPO_ROOT}"
            go run ./examples/ai-registry/cloud/summarize \
                "${args[@]}" "${run_dir}"
        ) > "${output}"
    else
        echo "WARN: Go/summarizer unavailable; imported without analysis" >&2
        rm -f "${output}"
        if [[ "${STRICT}" == true ]]; then
            set -e
            echo "ERROR: strict import requires the summarizer or Go toolchain" >&2
            return 1
        fi
        set -e
        return 0
    fi
    local rc=$?
    set -e
    cat "${output}"
    rm -f "${output}"
    return "${rc}"
}

status=0
found=false
while IFS= read -r result; do
    found=true
    run_dir="$(dirname "${result}")"
    summarize_run "${run_dir}" || status=$?
done < <(find "${OUTPUT_DIR}" -type f -name result.json -print | sort)

if [[ "${found}" == false ]]; then
    echo "WARN: no result.json found under ${OUTPUT_DIR}" >&2
    [[ "${STRICT}" == false ]] || status=1
fi

echo "Imported: ${OUTPUT_DIR}"
echo "Analysis files:"
find "${OUTPUT_DIR}" -type f \( -name analysis.md -o -name analysis.json \) -print | sort

if [[ "${STRICT}" == true ]]; then
    exit "${status}"
fi
exit 0
