#!/bin/bash

# 批量更新 polaris-go 版本脚本
# 将示例模块的 polaris-go 版本和 Go 版本统一为指定版本

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cd "$script_dir"

POLARIS_GO_VERSION="${1:-v1.7.2}"
GO_VERSION="${2:-1.24}"

if ! command -v go >/dev/null 2>&1; then
    echo "go command not found"
    exit 1
fi

echo "开始统一 polaris-go 版本为: $POLARIS_GO_VERSION"
echo "统一 Go 版本为: $GO_VERSION"
echo "=========================================="

while IFS= read -r -d '' modfile; do
    dir=$(dirname "$modfile")
    echo "处理模块: $dir"

    (
        cd "$dir"
        go mod edit -go="$GO_VERSION"
        go mod edit -require="github.com/polarismesh/polaris-go@$POLARIS_GO_VERSION"
        go mod tidy
        go mod download
    )

    echo "$dir 更新完成"
    echo "------------------------------------------"
done < <(find . -name "go.mod" -type f -print0)

echo "=========================================="
echo "所有模块版本统一完成"
echo ""
echo "验证版本一致性:"
"$script_dir/check_versions.sh"
