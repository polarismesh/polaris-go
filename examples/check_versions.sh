#!/bin/bash

# 版本一致性检查脚本
# 检查项目中所有子模块的polaris-go版本和Go版本是否一致

set -e

echo "检查项目中所有模块的版本一致性"
echo "=========================================="

# 检查polaris-go版本
echo "📦 polaris-go版本检查:"
echo "------------------------------------------"

find . -name "go.mod" -type f | while read modfile; do
    dir=$(dirname "$modfile")
    polaris_line=$(grep "^[[:space:]]*require.*github.com/polarismesh/polaris-go" "$modfile" | head -1)
    polaris_version=$(echo "$polaris_line" | awk '{print $NF}')
    go_version=$(grep "^go " "$modfile" | awk '{print $2}')
    
    if [ -n "$polaris_line" ]; then
        echo "📍 $dir:"
        echo "   PolarIS-go: $polaris_version"
        echo "   Go: $go_version"
    fi
done | sort

echo ""
echo "=========================================="
echo "版本统计:"
echo "------------------------------------------"

# 统计polaris-go版本
echo "📊 PolarIS-go版本分布:"
find . -name "go.mod" -type f -exec grep -h "^[[:space:]]*require.*github.com/polarismesh/polaris-go" {} \; | \
    awk '{if(NF>=2) print $NF}' | sort | uniq -c | sort -nr

# 统计Go版本
echo ""
echo "📊 Go版本分布:"
find . -name "go.mod" -type f -exec grep -h "^go " {} \; | \
    awk '{print $2}' | sort | uniq -c | sort -nr

echo ""
echo "=========================================="

# 检查是否有不一致的版本
polaris_versions=$(find . -name "go.mod" -type f -exec grep -h "^[[:space:]]*require.*github.com/polarismesh/polaris-go" {} \; | awk '{if(NF>=2) print $NF}' | sort | uniq | wc -l)
go_versions=$(find . -name "go.mod" -type f -exec grep -h "^go " {} \; | awk '{print $2}' | sort | uniq | wc -l)

if [ "$polaris_versions" -eq 1 ] && [ "$go_versions" -eq 1 ]; then
    echo "✅ 所有模块版本一致!"
else
    echo "⚠️  发现版本不一致:"
    if [ "$polaris_versions" -gt 1 ]; then
        echo "   - PolarIS-go有 $polaris_versions 个不同版本"
    fi
    if [ "$go_versions" -gt 1 ]; then
        echo "   - Go有 $go_versions 个不同版本"
    fi
    echo ""
    echo "💡 建议运行 ./update_polaris_versions.sh 来统一版本"
fi