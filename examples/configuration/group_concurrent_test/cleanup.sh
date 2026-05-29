#!/bin/bash
# =============================================================================
# 清理脚本：清理 group_concurrent_test 产生的日志、二进制及服务端测试数据
#
# 使用方法:
#   ./cleanup.sh -s 127.0.0.1            # 清理本地文件 + 服务端测试分组
#   ./cleanup.sh                          # 仅清理本地文件（不清理服务端）
#   ./cleanup.sh -f                       # 强制模式，不需要确认
#   ./cleanup.sh --dry-run                # 仅展示，不执行清理
#   ./cleanup.sh --help
# =============================================================================

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

POLARIS_SERVER=""
NAMESPACE="default"
GROUP_PREFIX="e2e-concurrent-grp"
GROUP_COUNT=20
FORCE=false
DRY_RUN=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        -s|--server)
            POLARIS_SERVER="$2"; shift 2 ;;
        -n|--namespace)
            NAMESPACE="$2"; shift 2 ;;
        -p|--prefix)
            GROUP_PREFIX="$2"; shift 2 ;;
        -c|--count)
            GROUP_COUNT="$2"; shift 2 ;;
        -f|--force)
            FORCE=true; shift ;;
        --dry-run)
            DRY_RUN=true; shift ;;
        -h|--help)
            echo "用法: $0 [选项]"
            echo ""
            echo "选项:"
            echo "  -s, --server <addr>     Polaris 服务端地址（提供则清理服务端测试分组）"
            echo "  -n, --namespace <ns>    命名空间 (默认: default)"
            echo "  -p, --prefix <prefix>   配置分组名称前缀 (默认: e2e-concurrent-grp)"
            echo "  -c, --count <num>       配置分组数量 (默认: 20)"
            echo "  -f, --force             直接清理，不需要确认"
            echo "  --dry-run               仅展示要清理的内容，不执行"
            echo "  -h, --help              显示帮助信息"
            echo ""
            echo "示例:"
            echo "  $0                              # 仅清理本地日志和二进制"
            echo "  $0 -s 127.0.0.1                 # 同时清理服务端测试分组"
            echo "  $0 -s 127.0.0.1 --dry-run       # 预览要清理的内容"
            exit 0
            ;;
        *)
            echo -e "${RED}未知参数: $1${NC}"; exit 1 ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo ""
echo -e "${CYAN}========================================${NC}"
echo -e "${CYAN}  group_concurrent_test 清理工具${NC}"
echo -e "${CYAN}========================================${NC}"
echo ""

# =============================================================================
# Part 1: 清理本地文件
# =============================================================================

cleanup_local() {
    local targets=()
    local descs=()

    # .logs 目录
    if [[ -d "${SCRIPT_DIR}/.logs" ]]; then
        local dir_size
        dir_size=$(du -sh "${SCRIPT_DIR}/.logs" 2>/dev/null | awk '{print $1}')
        targets+=("${SCRIPT_DIR}/.logs")
        descs+=(".logs/ (${dir_size})")
    fi

    # polaris sdk 缓存目录
    if [[ -d "${SCRIPT_DIR}/polaris" ]]; then
        local dir_size
        dir_size=$(du -sh "${SCRIPT_DIR}/polaris" 2>/dev/null | awk '{print $1}')
        targets+=("${SCRIPT_DIR}/polaris")
        descs+=("polaris/ (${dir_size})")
    fi

    # 编译产物
    if [[ -f "${SCRIPT_DIR}/group_concurrent_test" ]]; then
        targets+=("${SCRIPT_DIR}/group_concurrent_test")
        descs+=("group_concurrent_test (binary)")
    fi
    if [[ -f "${SCRIPT_DIR}/bin" ]]; then
        targets+=("${SCRIPT_DIR}/bin")
        descs+=("bin (binary)")
    fi

    if [[ ${#targets[@]} -eq 0 ]]; then
        echo -e "${GREEN}本地无需清理的文件。${NC}"
        return
    fi

    echo -e "${YELLOW}发现以下本地文件/目录:${NC}"
    for desc in "${descs[@]}"; do
        echo "  - ${desc}"
    done
    echo ""

    if [[ "$DRY_RUN" == true ]]; then
        echo -e "${YELLOW}[dry-run] 仅展示，未清理本地文件。${NC}"
        return
    fi

    if [[ "$FORCE" != true ]]; then
        read -r -p "是否清理以上本地文件? [y/N] " response
        case "$response" in
            [yY]|[yY][eE][sS]) ;;
            *)
                echo -e "${YELLOW}跳过本地文件清理。${NC}"
                return
                ;;
        esac
    fi

    for target in "${targets[@]}"; do
        rm -rf "$target"
    done
    echo -e "${GREEN}本地文件已清理完毕。${NC}"
}

# =============================================================================
# Part 2: 清理服务端测试数据
# =============================================================================

cleanup_server() {
    if [[ -z "${POLARIS_SERVER}" ]]; then
        echo ""
        echo -e "${YELLOW}未指定 -s/--server，跳过服务端数据清理。${NC}"
        echo -e "${YELLOW}如需清理服务端测试分组，请使用: $0 -s <server_ip>${NC}"
        return
    fi

    local openapi="http://${POLARIS_SERVER}:8090"

    echo ""
    echo -e "${CYAN}--- 服务端测试分组清理 ---${NC}"
    echo "  服务端: ${POLARIS_SERVER}"
    echo "  前缀:   ${GROUP_PREFIX}"
    echo "  数量:   ${GROUP_COUNT}"
    echo ""

    # 查询哪些存在
    local existing_groups=""
    local existing_count=0

    for i in $(seq -w 0 $(printf "%03d" $((GROUP_COUNT - 1)))); do
        local group_name="${GROUP_PREFIX}-${i}"
        local resp
        resp=$(curl -s "${openapi}/config/v1/configfilegroups?namespace=${NAMESPACE}&group=${group_name}" 2>/dev/null)
        local total
        total=$(echo "$resp" | grep -o '"total": *[0-9]*' | head -1 | grep -o '[0-9]*$')
        if [ "${total:-0}" -ge 1 ]; then
            existing_groups="${existing_groups} ${group_name}"
            existing_count=$((existing_count + 1))
        fi
    done

    if [[ ${existing_count} -eq 0 ]]; then
        echo -e "${GREEN}服务端未发现测试分组（前缀: ${GROUP_PREFIX}），无需清理。${NC}"
        return
    fi

    echo -e "${YELLOW}发现 ${existing_count} 个测试分组:${NC}"
    for name in ${existing_groups}; do
        echo "  - ${name}"
    done
    echo ""

    if [[ "$DRY_RUN" == true ]]; then
        echo -e "${YELLOW}[dry-run] 仅展示，未删除服务端分组。${NC}"
        return
    fi

    if [[ "$FORCE" != true ]]; then
        read -r -p "是否删除以上服务端测试分组? [y/N] " response
        case "$response" in
            [yY]|[yY][eE][sS]) ;;
            *)
                echo -e "${YELLOW}跳过服务端分组清理。${NC}"
                return
                ;;
        esac
    fi

    local deleted=0
    local failed=0
    for group_name in ${existing_groups}; do
        # 删除分组内的配置文件
        curl -s "${openapi}/config/v1/configfiles" \
            -X DELETE -H "Content-type:application/json" \
            -d "{\"namespace\":\"${NAMESPACE}\", \"group\":\"${group_name}\", \"name\":\"data.yaml\"}" > /dev/null 2>&1

        # 删除分组
        local del_resp
        del_resp=$(curl -s "${openapi}/config/v1/configfilegroups?namespace=${NAMESPACE}&name=${group_name}" \
            -X DELETE 2>/dev/null)
        local code
        code=$(echo "$del_resp" | grep -o '"code":[0-9]*' | head -1 | cut -d: -f2)
        if [[ "${code}" == "200000" ]] || [[ "${code}" == "200001" ]]; then
            echo -e "  ${GREEN}✓${NC} 已删除: ${group_name}"
            deleted=$((deleted + 1))
        else
            echo -e "  ${RED}✗${NC} 删除失败: ${group_name} (code: ${code})"
            failed=$((failed + 1))
        fi
    done

    echo ""
    echo -e "${GREEN}服务端清理完成: 删除 ${deleted} 个分组${NC}"
    if [[ ${failed} -gt 0 ]]; then
        echo -e "${RED}  ${failed} 个删除失败${NC}"
    fi
}

# =============================================================================
# 执行
# =============================================================================

cleanup_local
cleanup_server

echo ""
if [[ "$DRY_RUN" == true ]]; then
    echo -e "${YELLOW}[dry-run] 以上为预览结果，未执行任何清理操作。${NC}"
else
    echo -e "${GREEN}清理流程完成。${NC}"
fi
echo ""
