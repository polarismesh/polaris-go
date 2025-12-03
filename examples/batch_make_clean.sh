#!/bin/bash

# 批量执行 make clean 脚本
# 用于递归清理所有包含 Makefile 的子目录

set -e  # 遇到错误立即退出

echo "🚀 开始批量执行 make clean..."

# 计数器
success_count=0
fail_count=0
total_count=0

# 查找所有包含 Makefile 或 makefile 的目录
find . \( -name "Makefile" -o -name "makefile" \) -type f | while read -r makefile; do
    # 获取目录路径
    dir=$(dirname "$makefile")
    
    # 跳过根目录的 Makefile（如果存在）
    if [ "$dir" = "." ]; then
        continue
    fi
    
    total_count=$((total_count + 1))
    
    echo "📁 处理目录: $dir"
    
    # 进入目录并执行 make clean
    if cd "$dir"; then
        echo "   🧹 执行: make clean"
        
        if make clean 2>&1; then
            echo "   ✅ 成功: $dir"
            success_count=$((success_count + 1))
        else
            echo "   ❌ 失败: $dir"
            fail_count=$((fail_count + 1))
        fi
        
        # 返回上级目录
        cd - > /dev/null
    else
        echo "   ❌ 无法进入目录: $dir"
        fail_count=$((fail_count + 1))
    fi
    
    echo "---"
done

echo "📊 执行结果统计:"
echo "   总模块数: $total_count"
echo "   成功数: $success_count"
echo "   失败数: $fail_count"

if [ $fail_count -eq 0 ]; then
    echo "🎉 所有模块的 make clean 执行成功!"
else
    echo "⚠️  有 $fail_count 个模块执行失败，请检查相关目录"
fi

# 可选：显示使用说明
echo ""
echo "💡 使用说明:"
echo "   1. 给脚本执行权限: chmod +x batch_make_clean.sh"
echo "   2. 运行脚本: ./batch_make_clean.sh"
echo "   3. 脚本会自动跳过根目录的 Makefile 文件（如果存在）"
echo "   4. 每个模块执行完成后会显示状态"
