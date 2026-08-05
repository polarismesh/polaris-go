#!/bin/bash
# =============================================================================
# 配置灰度云上验证 - 部署物料生成脚本
#
# 执行后生成 dist/ 下 3 个节点的部署物料目录，每个目录可独立打包上传到对应节点运行:
#   dist/client-a/  带 env=pre 标签，常驻，观察自定义标签灰度不推送(限制1)
#   dist/client-b/  无标签，常驻 + setup 基线，观察 IP 灰度 watch 推送
#   dist/client-c/  带 env=pre 标签，临时启动验证初始拉取命中灰度
#
# 每个节点目录含:
#   x86-bin       gray-demo 预编译 Linux x86_64 二进制
#   polaris.yaml  已固化该节点客户端标签(${POLARIS_SERVER}/${POLARIS_TOKEN} 占位)
#   client.sh     节点启动脚本(setup/start/stop/status/restart)
#   clean.sh      节点清理脚本
#
# 使用方法:
#   ./build-materials.sh            # 生成 dist/ 物料 + 每节点 zip 包
#   ./build-materials.sh --clean    # 清理 dist/、zip 包与临时 x86-bin
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEMO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"   # examples/configuration/gray
DIST_DIR="${SCRIPT_DIR}/dist"
TEMPLATES_DIR="${SCRIPT_DIR}/templates"
TMP_BIN="${SCRIPT_DIR}/x86-bin"

GREEN='\033[0;32m'
CYAN='\033[0;36m'
YELLOW='\033[1;33m'
NC='\033[0m'

# 节点物料定义: 名称 | polaris yaml 模板 | 默认端口 | 说明
declare -a NODES=(
    "client-a|polaris-client-a.yaml|18081|带 env=pre 标签，常驻，观察自定义标签灰度不推送(限制1)"
    "client-b|polaris-client-b.yaml|18082|无标签，常驻 + setup 基线，观察 IP 灰度 watch 推送"
    "client-c|polaris-client-c.yaml|18083|带 env=pre 标签，临时启动验证初始拉取命中灰度"
)

# ======================== --clean 模式 ========================
if [[ "${1:-}" == "--clean" ]]; then
    echo "清理物料: ${DIST_DIR}(含 zip 包) + 临时二进制"
    rm -rf "$DIST_DIR" "$TMP_BIN"
    echo -e "${GREEN}已清理${NC}"
    exit 0
fi

# ======================== 1. 交叉编译 gray-demo ========================
echo -e "${CYAN}=== 1. 交叉编译 gray-demo (Linux x86_64) ===${NC}"
if ! command -v go &> /dev/null; then
    echo "❌ Go 未安装"; exit 1
fi
log_go() { echo "  $*"; }
log_go "Go 版本: $(go version)"
log_go "源码目录: ${DEMO_DIR}"
(cd "$DEMO_DIR" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$TMP_BIN" .)
log_go "编译产物: ${TMP_BIN} ($(file "$TMP_BIN" | sed 's/.*: //'))"
echo ""

# ======================== 2. 生成节点物料 ========================
echo -e "${CYAN}=== 2. 生成节点物料 ===${NC}"
rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

for entry in "${NODES[@]}"; do
    IFS='|' read -r name yaml port desc <<< "$entry"
    out="${DIST_DIR}/${name}"
    mkdir -p "$out"

    cp "$TMP_BIN" "${out}/x86-bin"
    cp "${TEMPLATES_DIR}/${yaml}" "${out}/polaris.yaml"
    cp "${TEMPLATES_DIR}/client.sh" "${out}/client.sh"
    cp "${TEMPLATES_DIR}/clean.sh" "${out}/clean.sh"
    chmod +x "${out}/x86-bin" "${out}/client.sh" "${out}/clean.sh"

    echo -e "  ${GREEN}${name}/${NC} (默认端口 ${port}): ${desc}"
done
rm -f "$TMP_BIN"
echo ""

# ======================== 3. 生成 zip 包 ========================
echo -e "${CYAN}=== 3. 生成 zip 包(每节点一个，方便上传到对应节点) ===${NC}"
if command -v zip &> /dev/null; then
    for entry in "${NODES[@]}"; do
        IFS='|' read -r name _ _ _ <<< "$entry"
        zip_file="${DIST_DIR}/${name}.zip"
        rm -f "$zip_file"
        (cd "$DIST_DIR" && zip -rq "$zip_file" "$name")
        echo -e "  ${GREEN}${name}.zip${NC} -> ${zip_file} ($(du -h "$zip_file" | cut -f1))"
    done
else
    echo -e "  ${YELLOW}zip 命令不可用，跳过 zip 生成${NC}"
    echo -e "  ${YELLOW}可用 tar 替代: tar czf client-a.tar.gz -C ${DIST_DIR} client-a${NC}"
fi
echo ""

# ======================== 4. 物料清单 ========================
echo -e "${CYAN}=== 4. 物料清单 ===${NC}"
for name in client-a client-b client-c; do
    echo -e "  ${GREEN}${DIST_DIR}/${name}/${NC}"
    ls -1 "${DIST_DIR}/${name}" | sed 's/^/    /'
done
if command -v zip &> /dev/null; then
    echo -e "  ${GREEN}zip 包(dist 目录下):${NC}"
    ls -1 "${DIST_DIR}"/client-*.zip 2>/dev/null | sed 's/^/    /' || true
fi
echo ""

# ======================== 5. 部署说明 ========================
echo -e "${CYAN}=== 5. 部署说明 ===${NC}"
cat <<EOF
物料目录: ${DIST_DIR}
zip 包(每节点一个): ${DIST_DIR}/client-a.zip | client-b.zip | client-c.zip

上传 zip 到对应节点后:
  unzip client-a.zip && cd client-a
  # 启动常驻客户端
  ./client.sh start --polaris-server < polaris 服务端地址 > --port 18081
  # 查看状态 + 当前生效配置
  ./client.sh status --port 18081
  # 停止
  ./client.sh stop
  # 清理进程与产物
  ./clean.sh -f

鉴权: POLARIS_TOKEN=xxx ./client.sh start --polaris-server <地址> --port 18081
DEBUG: 加 --debug

详细云上验证流程见: ${SCRIPT_DIR}/cloud-gray-test.md
EOF
echo ""
echo -e "${GREEN}物料生成完成。${NC}"
