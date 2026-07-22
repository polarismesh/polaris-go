#!/bin/bash
# callAuditLog 远程验证脚本（连接真实 Polaris 服务端）
cd "$(dirname "$0")"

echo "=== callAuditLog 远程验证脚本 ==="
echo ""

# 远程验证依赖真实服务端，POLARIS_SERVER 必填
if [ -z "${POLARIS_SERVER}" ]; then
    echo "❌ 缺少环境变量 POLARIS_SERVER（远程 Polaris 服务端地址 <host>:<port>）"
    echo ""
    echo "用法："
    echo "  POLARIS_SERVER=127.0.0.1:8091 [NAMESPACE=default] [SERVICE=DemoService] bash verify.sh"
    exit 1
fi

NAMESPACE="${NAMESPACE:-default}"
SERVICE="${SERVICE:-DemoService}"

# 清理旧审计日志与构建产物，保证每次运行独立
rm -rf ./polaris/log/audit
rm -f audit_remote

echo "1. 编译远程验证程序..."
go build -o audit_remote .
if [ $? -ne 0 ]; then
    echo "❌ 编译失败"
    exit 1
fi
echo "✅ 编译成功"
echo ""

echo "2. 连接远程服务端 ${POLARIS_SERVER}（namespace=${NAMESPACE} service=${SERVICE}）进行验证..."
echo ""
./audit_remote -server "${POLARIS_SERVER}" -namespace "${NAMESPACE}" -service "${SERVICE}"
ret=$?
echo ""

rm -f audit_remote

if [ $ret -eq 0 ]; then
    echo "✅ 远程验证通过：审计日志已生成"
    exit 0
else
    echo "❌ 远程验证失败"
    exit 1
fi
