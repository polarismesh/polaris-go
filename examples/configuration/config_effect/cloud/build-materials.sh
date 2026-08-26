#!/bin/bash
# =============================================================================
# 配置生效查询云上验证 - 部署物料生成脚本
#
# 执行后生成 dist/client/ 部署物料目录并打包为 client.zip，上传到云节点运行:
#   dist/client/
#     x86-bin           config-effect-demo 预编译 Linux x86_64 二进制
#     polaris.yaml      配置模板(${POLARIS_SERVER}/${POLARIS_TOKEN} 占位)
#     client.sh         节点启动脚本(setup/start/stop/status/restart)
#     verify-cloud.sh   配置生效查询验证脚本(调服务端 maintain 接口)
#     pack-logs.sh      节点日志打包脚本(生成 client-logs-<时间戳>.zip)
#     clean.sh          节点清理脚本
#
# 使用方法:
#   ./build-materials.sh            # 生成 dist/client/ 物料 + client.zip
#   ./build-materials.sh --clean    # 清理 dist/、zip 包与临时二进制
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEMO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"   # examples/configuration/config_effect
DIST_DIR="${SCRIPT_DIR}/dist"
TEMPLATES_DIR="${SCRIPT_DIR}/templates"
TMP_BIN="${SCRIPT_DIR}/x86-bin"

GREEN='\033[0;32m'
CYAN='\033[0;36m'
YELLOW='\033[1;33m'
NC='\033[0m'

NODE_NAME="client"

# ======================== --clean 模式 ========================
if [[ "${1:-}" == "--clean" ]]; then
    echo "清理物料: ${DIST_DIR}(含 zip 包) + 临时二进制"
    rm -rf "$DIST_DIR" "$TMP_BIN"
    echo -e "${GREEN}已清理${NC}"
    exit 0
fi

# ======================== 1. 交叉编译 config-effect-demo ========================
echo -e "${CYAN}=== 1. 交叉编译 config-effect-demo (Linux x86_64) ===${NC}"
if ! command -v go &> /dev/null; then
    echo "❌ Go 未安装"; exit 1
fi
echo "  Go 版本: $(go version)"
echo "  源码目录: ${DEMO_DIR}"
(cd "$DEMO_DIR" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$TMP_BIN" .)
echo "  编译产物: ${TMP_BIN} ($(file "$TMP_BIN" | sed 's/.*: //'))"
echo ""

# ======================== 2. 生成节点物料 ========================
echo -e "${CYAN}=== 2. 生成节点物料 ===${NC}"
rm -rf "$DIST_DIR"
mkdir -p "${DIST_DIR}/${NODE_NAME}"

cp "$TMP_BIN" "${DIST_DIR}/${NODE_NAME}/x86-bin"
cp "${TEMPLATES_DIR}/polaris.yaml" "${DIST_DIR}/${NODE_NAME}/polaris.yaml"
cp "${TEMPLATES_DIR}/client.sh" "${DIST_DIR}/${NODE_NAME}/client.sh"
cp "${TEMPLATES_DIR}/verify-cloud.sh" "${DIST_DIR}/${NODE_NAME}/verify-cloud.sh"
cp "${TEMPLATES_DIR}/pack-logs.sh" "${DIST_DIR}/${NODE_NAME}/pack-logs.sh"
cp "${TEMPLATES_DIR}/clean.sh" "${DIST_DIR}/${NODE_NAME}/clean.sh"
chmod +x "${DIST_DIR}/${NODE_NAME}/x86-bin" \
    "${DIST_DIR}/${NODE_NAME}/client.sh" \
    "${DIST_DIR}/${NODE_NAME}/verify-cloud.sh" \
    "${DIST_DIR}/${NODE_NAME}/pack-logs.sh" \
    "${DIST_DIR}/${NODE_NAME}/clean.sh"
rm -f "$TMP_BIN"

echo -e "  ${GREEN}${NODE_NAME}/${NC} 常驻客户端(订阅配置 + WatchClientEvents 长连接 + HTTP 观察接口)"
echo ""

# ======================== 3. 生成 zip 包 ========================
echo -e "${CYAN}=== 3. 生成 zip 包 ===${NC}"
if command -v zip &> /dev/null; then
    zip_file="${DIST_DIR}/${NODE_NAME}.zip"
    rm -f "$zip_file"
    (cd "$DIST_DIR" && zip -rq "$zip_file" "$NODE_NAME")
    echo -e "  ${GREEN}${NODE_NAME}.zip${NC} -> ${zip_file} ($(du -h "$zip_file" | cut -f1))"
else
    echo -e "  ${YELLOW}zip 命令不可用，跳过 zip 生成${NC}"
    echo -e "  ${YELLOW}可用 tar 替代: tar czf ${NODE_NAME}.tar.gz -C ${DIST_DIR} ${NODE_NAME}${NC}"
fi
echo ""

# ======================== 4. 物料清单 ========================
echo -e "${CYAN}=== 4. 物料清单 ===${NC}"
echo -e "  ${GREEN}${DIST_DIR}/${NODE_NAME}/${NC}"
ls -1 "${DIST_DIR}/${NODE_NAME}" | sed 's/^/    /'
if command -v zip &> /dev/null; then
    echo -e "  ${GREEN}zip 包:${NC} ${DIST_DIR}/${NODE_NAME}.zip"
fi
echo ""

# ======================== 5. 部署说明 ========================
echo -e "${CYAN}=== 5. 部署说明 ===${NC}"
cat <<EOF
物料目录: ${DIST_DIR}/${NODE_NAME}
zip 包:   ${DIST_DIR}/${NODE_NAME}.zip

上传 zip 到云节点后:
  unzip ${NODE_NAME}.zip && cd ${NODE_NAME}

  # 1. 发布 3 份基线配置(已存在则跳过)，并把第 1 份经 console 接口覆盖为加密配置后发布
  POLARIS_TOKEN=xxx ./client.sh setup --polaris-server <服务端地址> --content effect-content-v

  # 2. 启动常驻客户端(自动订阅配置 + 建 WatchClientEvents 长连接)
  POLARIS_TOKEN=xxx ./client.sh start --polaris-server <服务端地址> --port 18091

  # 3. 查看状态(显示 clientID 与本地生效配置)
  ./client.sh status --port 18091

  # 4. 执行配置生效查询验证(调服务端 maintain 接口，校验 ACK)
  POLARIS_TOKEN=xxx ./verify-cloud.sh --polaris-server <服务端地址> \\
      --maintain-port 8090 --client-port 18091

  # 5. 打包节点日志传回本地(可选，需在 clean.sh 之前执行)
  ./pack-logs.sh

  # 6. 停止与清理
  ./client.sh stop
  ./clean.sh -f

鉴权: 通过环境变量 POLARIS_TOKEN 或 --polaris-token 传入。
DEBUG: client.sh start 加 --debug。

验证原理:
  - 客户端启动后通过 ReportClient 上报 clientID，建立 WatchClientEvents 长连接
  - verify-cloud.sh 读取客户端 /clientid 与 /config，获得 clientID 与本地 version/md5/content
  - 调服务端 maintain 接口向该 clientID PUSH 配置生效查询
  - 服务端经长连接下发 PUSH，客户端回 ACK，服务端透传给 verify-cloud.sh
  - 脚本解析 ACK，断言 applied=true 且 version/md5/content 与客户端本地一致
  - 加密文件(第 1 份)的 ACK 额外携带 encrypted/encrypt_algo/data_key，
    脚本用 data_key 解密密文 content 并断言与客户端生效明文一致
EOF
echo ""
echo -e "${GREEN}物料生成完成。${NC}"
