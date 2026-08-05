#!/bin/bash
# =============================================================================
# 配置灰度云上验证 - 开发机物料清理脚本
#
# 清理 build-materials.sh 生成的 dist/ 物料与临时 x86-bin。
#
# 使用方法:
#   ./clean-cloud.sh            # 默认: 展示后确认再清理
#   ./clean-cloud.sh -f         # 强制直接清理
#   ./clean-cloud.sh --dry-run  # 仅展示，不执行
# =============================================================================

set -euo pipefail

FORCE=false
DRY_RUN=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        -f|--force)  FORCE=true;   shift ;;
        --dry-run)   DRY_RUN=true; shift ;;
        -h|--help)
            echo "用法: $0 [-f|--force] [--dry-run]"
            exit 0
            ;;
        *) echo "未知参数: $1"; exit 1 ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIST_DIR="${SCRIPT_DIR}/dist"
TMP_BIN="${SCRIPT_DIR}/x86-bin"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo "待清理项:"
echo "  物料目录: ${DIST_DIR} ($([[ -d "$DIST_DIR" ]] && echo 存在 || echo 不存在))"
echo "  临时二进制: ${TMP_BIN} ($([[ -f "$TMP_BIN" ]] && echo 存在 || echo 不存在))"
local_zips=""
for zf in "${DIST_DIR}"/client-*.zip; do
    [[ -f "$zf" ]] && local_zips+=" $(basename "$zf")"
done
echo "  zip 包(dist 下):${local_zips:- 无}"
echo ""

if [[ "$DRY_RUN" == "true" ]]; then
    echo -e "${YELLOW}[dry-run] 仅展示，未清理${NC}"
    exit 0
fi

if [[ "$FORCE" != "true" ]]; then
    read -r -p "确认清理以上物料? [y/N] " ans
    [[ "$ans" =~ ^[Yy]$ ]] || { echo "已取消"; exit 0; }
fi

rm -rf "$DIST_DIR" "$TMP_BIN"
echo -e "${GREEN}已清理物料${NC}"
echo "清理完成。"
