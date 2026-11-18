#!/bin/bash

# 版本一致性检查脚本
# 检查所有示例模块的 polaris-go 版本和 Go 版本是否一致

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cd "$script_dir"

if ! command -v go >/dev/null 2>&1; then
    echo "go command not found"
    exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
    echo "jq command not found"
    exit 1
fi

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/polaris-go-example-versions.XXXXXX")
trap 'rm -rf "$tmp_dir"' EXIT
modules_file="$tmp_dir/modules"
missing_file="$tmp_dir/missing"
: > "$modules_file"
: > "$missing_file"

while IFS= read -r -d '' modfile; do
    dir=$(dirname "$modfile")
    metadata=$(go mod edit -json "$modfile")
    polaris_version=$(printf '%s\n' "$metadata" | jq -r \
        '.Require[]? | select(.Path == "github.com/polarismesh/polaris-go") | .Version' | head -n 1)
    go_version=$(printf '%s\n' "$metadata" | jq -r '.Go // empty')

    if [ -z "$polaris_version" ]; then
        printf '%s\n' "$dir" >> "$missing_file"
        continue
    fi

    printf '%s\t%s\t%s\n' "$dir" "$polaris_version" "$go_version" >> "$modules_file"
done < <(find . -name "go.mod" -type f -print0)

echo "检查示例模块版本一致性"
echo "=========================================="
echo "MODULE                                                       POLARIS-GO  GO"
awk -F '\t' '{printf "%-60s %-11s %s\n", $1, $2, $3}' "$modules_file" | sort

echo ""
echo "=========================================="
echo "版本统计:"
echo "------------------------------------------"

echo "polaris-go 版本分布:"
cut -f2 "$modules_file" | sort | uniq -c | sort -nr

echo ""
echo "Go 版本分布:"
cut -f3 "$modules_file" | sort | uniq -c | sort -nr

polaris_versions=$(cut -f2 "$modules_file" | sort -u | wc -l | tr -d ' ')
go_versions=$(cut -f3 "$modules_file" | sort -u | wc -l | tr -d ' ')
missing_count=$(wc -l < "$missing_file" | tr -d ' ')
module_count=$(wc -l < "$modules_file" | tr -d ' ')

echo ""
echo "=========================================="

if [ "$missing_count" -ne 0 ]; then
    echo "以下模块没有 github.com/polarismesh/polaris-go 依赖:"
    sed 's/^/  - /' "$missing_file"
fi

if [ "$polaris_versions" -eq 1 ] && [ "$go_versions" -eq 1 ] && [ "$missing_count" -eq 0 ]; then
    echo "所有 $module_count 个模块版本一致"
    exit 0
fi

echo "发现版本不一致:"
if [ "$polaris_versions" -gt 1 ]; then
    echo "  - polaris-go 有 $polaris_versions 个不同版本"
fi
if [ "$go_versions" -gt 1 ]; then
    echo "  - Go 有 $go_versions 个不同版本"
fi
if [ "$missing_count" -ne 0 ]; then
    echo "  - $missing_count 个模块缺少 polaris-go 依赖"
fi
echo ""
echo "建议运行 ./update_polaris_versions.sh 来统一版本"
exit 1
