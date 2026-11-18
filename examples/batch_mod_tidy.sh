#!/bin/bash

# 批量执行 go mod tidy，整理所有示例模块的依赖关系

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cd "$script_dir"

echo "开始批量执行 go mod tidy..."

success_count=0
fail_count=0
total_count=0

while IFS= read -r -d '' modfile; do
    dir=$(dirname "$modfile")
    if [ "$dir" = "." ]; then
        continue
    fi

    total_count=$((total_count + 1))
    echo "处理目录: $dir"

    if (cd "$dir" && go mod tidy); then
        echo "  成功: $dir"
        success_count=$((success_count + 1))
    else
        echo "  失败: $dir"
        fail_count=$((fail_count + 1))
    fi

    echo "---"
done < <(find . -name "go.mod" -type f -print0)

echo "执行结果统计:"
echo "  总模块数: $total_count"
echo "  成功数: $success_count"
echo "  失败数: $fail_count"

if [ "$fail_count" -eq 0 ]; then
    echo "所有模块的 go mod tidy 执行成功"
    exit 0
fi

echo "有 $fail_count 个模块执行失败，请检查相关目录"
exit 1
