#!/bin/bash

# 批量更新polaris-go版本脚本
# 将项目中所有子模块的polaris-go版本统一为指定版本

set -e

# 配置参数
POLARIS_GO_VERSION="${1:-v1.6.1}"
GO_VERSION="${2:-1.24}"

echo "开始统一polaris-go版本为: $POLARIS_GO_VERSION"
echo "统一Go版本为: $GO_VERSION"
echo "=========================================="

# 查找所有包含go.mod文件的目录
find . -name "go.mod" -type f | while read modfile; do
    dir=$(dirname "$modfile")
    echo "处理模块: $dir"
    
    cd "$dir"
    
    # 更新Go版本
    sed -i.bak "s/^go [0-9.]*/go $GO_VERSION/" go.mod
    rm -f go.mod.bak
    
    # 更新polaris-go版本（只处理require依赖，忽略replace）
    if grep -q "^[[:space:]]*require.*github.com/polarismesh/polaris-go" go.mod; then
        # 如果已经有polaris-go的require依赖，更新版本
        sed -i.bak "s|^[[:space:]]*require[[:space:]]*github.com/polarismesh/polaris-go[[:space:]]*v[0-9.]*|require github.com/polarismesh/polaris-go $POLARIS_GO_VERSION|" go.mod
        rm -f go.mod.bak
    else
        # 如果没有polaris-go的require依赖，添加依赖（这种情况应该很少）
        echo "警告: $dir 模块没有polaris-go的require依赖"
    fi
    
    # 清理并重新下载依赖
    echo "更新依赖..."
    go mod tidy
    go mod download
    
    echo "✅ $dir 更新完成"
    echo "------------------------------------------"
    cd - > /dev/null
done

echo "=========================================="
echo "所有模块版本统一完成!"
echo ""
echo "验证版本一致性:"
find . -name "go.mod" -type f -exec grep -H "^[[:space:]]*require.*github.com/polarismesh/polaris-go" {} \;