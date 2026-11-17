#!/usr/bin/env bash
# Build a self-contained AI Registry cloud verification package.
#
# The package contains a Linux binary, cloud runner, log packer, package metadata,
# and the E2E case reference. It does not contain repository sources or credentials.
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
EXAMPLE_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
REPO_ROOT="$(cd -- "${EXAMPLE_DIR}/../.." && pwd)"
DIST_DIR="${EXAMPLE_DIR}/dist"
INVOCATION_DIR="$PWD"

NAME="ai-registry-cloud"
GOOS_VALUE="${GOOS:-linux}"
GOARCH_VALUE="${GOARCH:-amd64}"
OUTPUT=""
CLEAN=false

usage() {
    cat <<'EOF'
用法: build-cloud-package.sh [选项]

在本地交叉编译 AI Registry 云端验证包。生成物默认位于 examples/ai-registry/dist/。

选项:
  --goos <os>        目标系统，默认 linux
  --goarch <arch>    目标架构，默认 amd64；arm64 机器使用 --goarch arm64
  --name <name>      包名前缀，默认 ai-registry-cloud
  --output <path>    指定输出 tar.gz 路径
  --clean            清理 dist/ 后退出
  -h, --help         显示帮助
EOF
}

die() {
    echo "ERROR: $*" >&2
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --goos)
            [[ $# -ge 2 ]] || die "--goos requires a value"
            GOOS_VALUE="$2"
            shift 2
            ;;
        --goos=*)
            GOOS_VALUE="${1#*=}"
            shift
            ;;
        --goarch)
            [[ $# -ge 2 ]] || die "--goarch requires a value"
            GOARCH_VALUE="$2"
            shift 2
            ;;
        --goarch=*)
            GOARCH_VALUE="${1#*=}"
            shift
            ;;
        --name)
            [[ $# -ge 2 ]] || die "--name requires a value"
            NAME="$2"
            shift 2
            ;;
        --name=*)
            NAME="${1#*=}"
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
        --clean)
            CLEAN=true
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

if [[ "${CLEAN}" == "true" ]]; then
    rm -rf "${DIST_DIR}"
    echo "已清理 ${DIST_DIR}"
    exit 0
fi

command -v go >/dev/null 2>&1 || die "go is required for local packaging"
command -v tar >/dev/null 2>&1 || die "tar is required for local packaging"

mkdir -p "${DIST_DIR}"
timestamp="$(date -u +%Y%m%d_%H%M%S)"
stage_name="${NAME}-${timestamp}-${GOOS_VALUE}-${GOARCH_VALUE}-$$"
stage_dir="${DIST_DIR}/${stage_name}"
binary="${stage_dir}/polaris-go-ai-registry"
summarizer="${stage_dir}/ai-registry-summarize"

rm -rf "${stage_dir}"
mkdir -p "${stage_dir}"

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

echo "=== 1/4 构建 ${GOOS_VALUE}/${GOARCH_VALUE} 二进制 ==="
(
    cd "${REPO_ROOT}"
    GOOS="${GOOS_VALUE}" GOARCH="${GOARCH_VALUE}" CGO_ENABLED=0 \
        go build -trimpath -o "${binary}" ./examples/ai-registry
    GOOS="${GOOS_VALUE}" GOARCH="${GOARCH_VALUE}" CGO_ENABLED=0 \
        go build -trimpath -o "${summarizer}" ./examples/ai-registry/cloud/summarize
)
chmod 0755 "${binary}" "${summarizer}"

echo "=== 2/4 组织部署包 ==="
for file in run-cloud.sh pack-cloud-logs.sh import-logs.sh README.md; do
    [[ -f "${SCRIPT_DIR}/${file}" ]] || die "missing cloud asset: ${SCRIPT_DIR}/${file}"
    cp "${SCRIPT_DIR}/${file}" "${stage_dir}/${file}"
done
cp "${EXAMPLE_DIR}/test.md" "${stage_dir}/E2E-CASES.md"
chmod 0755 "${stage_dir}/run-cloud.sh" "${stage_dir}/pack-cloud-logs.sh" "${stage_dir}/import-logs.sh"

build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
git_revision="unknown"
git_branch="unknown"
if command -v git >/dev/null 2>&1 && git -C "${REPO_ROOT}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    git_revision="$(git -C "${REPO_ROOT}" describe --always --dirty --tags 2>/dev/null || echo unknown)"
    git_branch="$(git -C "${REPO_ROOT}" rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)"
fi
binary_sha="$(sha256_file "${binary}")"
summarizer_sha="$(sha256_file "${summarizer}")"

cat > "${stage_dir}/package.env" <<EOF
PACKAGE_NAME=${stage_name}
BUILD_TIME_UTC=${build_time}
GIT_BRANCH=${git_branch}
GIT_REVISION=${git_revision}
TARGET_OS=${GOOS_VALUE}
TARGET_ARCH=${GOARCH_VALUE}
BINARY=polaris-go-ai-registry
BINARY_SHA256=${binary_sha}
SUMMARIZER=ai-registry-summarize
SUMMARIZER_SHA256=${summarizer_sha}
EOF

(
    cd "${stage_dir}"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum README.md E2E-CASES.md package.env run-cloud.sh pack-cloud-logs.sh import-logs.sh \
            polaris-go-ai-registry ai-registry-summarize > SHA256SUMS
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 README.md E2E-CASES.md package.env run-cloud.sh pack-cloud-logs.sh import-logs.sh \
            polaris-go-ai-registry ai-registry-summarize > SHA256SUMS
    else
        echo "sha256sum/shasum unavailable; SHA256SUMS not generated" > SHA256SUMS
    fi
)

echo "=== 3/4 打包 tar.gz ==="
archive="${OUTPUT:-${DIST_DIR}/${stage_name}.tar.gz}"
case "${archive}" in
    *.tar.gz) ;;
    *) archive="${archive}.tar.gz" ;;
esac
if [[ "${archive}" != /* ]]; then
    archive="${INVOCATION_DIR}/${archive}"
fi
mkdir -p "$(dirname "${archive}")"
archive="$(cd "$(dirname "${archive}")" && pwd)/$(basename "${archive}")"
tar -czf "${archive}" -C "${DIST_DIR}" "${stage_name}"

archive_sha="$(sha256_file "${archive}")"
if [[ "${archive_sha}" != "unavailable" ]]; then
    printf '%s  %s\n' "${archive_sha}" "$(basename "${archive}")" > "${archive}.sha256"
fi

echo "=== 4/4 完成 ==="
echo "部署包: ${archive}"
echo "大小: $(du -h "${archive}" | cut -f1)"
echo "包内文件:"
tar -tzf "${archive}" | sed 's/^/  /'
if [[ -f "${archive}.sha256" ]]; then
    echo "校验文件: ${archive}.sha256"
fi

cat <<EOF

上传到云端 Linux 测试机:
  scp "${archive}" <user>@<cloud-host>:/tmp/

云端执行:
  cd /tmp
  tar xzf "$(basename "${archive}")"
  cd ${stage_name}
  ./run-cloud.sh --server <polaris-host>

运行结束会自动在 .logs/ 下生成日志包，再按提示 scp 回本地。
详细说明见包内 README.md。
EOF
