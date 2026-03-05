#!/bin/bash

echo "=== 配置中心分段锁优化验证脚本 ==="
echo ""

# 编译测试程序
echo "1. 编译测试程序..."
cd $(dirname "$0")
go build -o config_concurrent_test main.go
if [ $? -ne 0 ]; then
    echo "❌ 编译失败"
    exit 1
fi
echo "✅ 编译成功"
echo ""

# 运行测试
echo "2. 运行并发配置获取测试..."
echo "   测试参数：50个并发线程，100个配置文件，持续30秒"
echo ""

./config_concurrent_test \
    -namespace=default \
    -service=config-service \
    -fileCount=100 \
    -threads=50 \
    -duration=30

echo ""
echo "=== 验证结果分析 ==="
echo ""
echo "✅ 如果测试结果显示："
echo "   - 所有操作都成功完成（错误数为0）"
echo "   - 操作成功率接近100%"
echo "   - 每秒操作数较高（表示并发性能良好）"
echo ""
echo "📊 关键指标说明："
echo "   - 成功操作数：应该等于总操作数"
echo "   - 错误操作数：应该为0"
echo "   - 成功率：应该为100%"
echo "   - 每秒操作数：数值越高表示并发性能越好"
echo ""
echo "🔍 优化验证要点："
echo "   1. 不同FileName的配置获取应该能够并发执行"
echo "   2. 不应该出现锁竞争导致的性能瓶颈"
echo "   3. 高并发场景下应该保持稳定运行"
echo ""
echo "💡 如果测试失败，请检查："
echo "   - 配置文件服务是否正常运行"
echo "   - 网络连接是否正常"
echo "   - 是否有其他并发限制因素"
echo ""