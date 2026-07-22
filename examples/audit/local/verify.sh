#!/bin/bash
# callAuditLog 集成测试验证脚本
cd "$(dirname "$0")"

echo "=== callAuditLog 集成测试验证脚本 ==="
echo ""

# 清理旧审计日志与构建产物，保证每次运行独立
rm -rf ./polaris/log/audit
rm -f audit_test

echo "1. 编译集成测试程序..."
go build -o audit_test main.go
if [ $? -ne 0 ]; then
    echo "❌ 编译失败"
    exit 1
fi
echo "✅ 编译成功"
echo ""

echo "2. 运行集成测试（自带 mock Polaris 服务端，无需外部 Polaris 部署）..."
echo ""
./audit_test
ret=$?
echo ""

rm -f audit_test

if [ $ret -eq 0 ]; then
    echo "✅ 集成测试通过：审计日志已生成"
    exit 0
else
    echo "❌ 集成测试失败"
    exit 1
fi
