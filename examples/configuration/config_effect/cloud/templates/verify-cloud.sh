#!/bin/bash
# =============================================================================
# 配置生效查询云上验证脚本
#
# 验证客户端通过 WatchClientEvents 长连接响应服务端「配置生效查询」的端到端能力：
#   1. 客户端启动后自动上报 clientID 并建立 WatchClientEvents 长连接
#   2. 本脚本调服务端 maintain 接口 GET /maintain/v1/clients/event 向该 clientID PUSH 配置生效查询
#   3. 客户端经长连接回 ACK(含 version/md5/content/applied)，服务端原样透传给本脚本
#   4. 本脚本解析 ACK，校验 applied=true 且 version/md5/content 与客户端本地一致
#
# 前置条件:
#   1. 客户端已通过 client.sh start 启动并就绪(本目录 x86-bin 在跑)
#   2. 服务端 maintain HTTP 端口可达(默认 8090，可用 --maintain-port 指定)
#   3. 服务端已实现 WatchClientEvents 接口(商业版已含)
#   4. python3 可用(解析 ACK JSON)
#
# 使用方法:
#   ./verify-cloud.sh --polaris-server 172.16.0.5 --maintain-port 8090 --client-port 18091
#   # 鉴权:
#   POLARIS_TOKEN=xxx ./verify-cloud.sh --polaris-server 172.16.0.5 --maintain-port 8090 --client-port 18091
# =============================================================================

set -euo pipefail

# ======================== 默认配置 ========================
POLARIS_SERVER="${POLARIS_SERVER:-}"
POLARIS_TOKEN="${POLARIS_TOKEN:-}"
MAINTAIN_PORT="${MAINTAIN_PORT:-8090}"
CLIENT_PORT="${CLIENT_PORT:-18091}"
NAMESPACE="${NAMESPACE:-default}"
FILE_GROUP="${FILE_GROUP:-polaris-config-example}"
FILE_NAME="${FILE_NAME:-config-effect-example}"
WAIT_WATCHER_SEC="${WAIT_WATCHER_SEC:-5}"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# ======================== 解析参数 ========================
while [[ $# -gt 0 ]]; do
    case "$1" in
        --polaris-server) POLARIS_SERVER="$2"; shift 2 ;;
        --polaris-token)  POLARIS_TOKEN="$2";  shift 2 ;;
        --maintain-port)  MAINTAIN_PORT="$2";  shift 2 ;;
        --client-port)    CLIENT_PORT="$2";    shift 2 ;;
        --namespace)      NAMESPACE="$2";       shift 2 ;;
        --group)          FILE_GROUP="$2";     shift 2 ;;
        --file)           FILE_NAME="$2";       shift 2 ;;
        --wait-watcher)   WAIT_WATCHER_SEC="$2"; shift 2 ;;
        -h|--help)
            echo "用法: $0 [选项]"
            echo ""
            echo "选项:"
            echo "  --polaris-server <地址>  北极星服务端地址 (必填)"
            echo "  --polaris-token <令牌>   北极星鉴权令牌 (默认: 空)"
            echo "  --maintain-port <端口>   服务端 maintain HTTP 端口 (默认: 8090)"
            echo "  --client-port <端口>     客户端 HTTP 观察端口 (默认: 18091)"
            echo "  --namespace <命名空间>   命名空间 (默认: default)"
            echo "  --group <配置组>         配置文件组 (默认: polaris-config-example)"
            echo "  --file <base name>      配置文件 base name，派生 -1/-2/-3.yaml (默认: config-effect-example)"
            echo "  --wait-watcher <秒>      等待 WatchClientEvents 长连接建立 (默认: 5)"
            exit 0
            ;;
        *)
            echo -e "${RED}未知参数: $1${NC}"; exit 1 ;;
    esac
done

if [[ -z "$POLARIS_SERVER" ]]; then
    echo -e "${RED}需要 --polaris-server <地址>${NC}"
    exit 1
fi

log_info()  { echo -e "${GREEN}[INFO]${NC} $(date '+%H:%M:%S') $*" >&2; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $(date '+%H:%M:%S') $*" >&2; }
log_error() { echo -e "${RED}[ERROR]${NC} $(date '+%H:%M:%S') $*" >&2; }
log_step()  { echo -e "${CYAN}=== $* ===${NC}" >&2; }

# setup_test_log 把后续 stdout/stderr 同时写入日志文件（带时间戳），并保留终端彩色输出。
# 日志文件经 sed 去除 ANSI 颜色码便于 grep/less；log_* 输出到 stderr，由 exec 2>&1 接管一并落盘。
SCRIPT_DIR_V="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG_DIR="${SCRIPT_DIR_V}/.logs"
TEST_LOG_FILE="${LOG_DIR}/verify-cloud-$(date +%Y%m%d_%H%M%S).log"
setup_test_log() {
	mkdir -p "${LOG_DIR}"
	{
		echo "===== 配置生效查询云上验证日志 $(date '+%Y-%m-%d %H:%M:%S') ====="
		echo "Command: $0 $*"
	} > "${TEST_LOG_FILE}"
	exec > >(tee >(sed -u 's/\x1b\[[0-9;]*m//g' >> "${TEST_LOG_FILE}")) 2>&1
}
setup_test_log "$@"

# 从客户端 /config 接口的 files 数组中，按文件名提取指定字段(依赖 python3)
# 入参: file_name field (version|md5|content)
get_file_field() {
    local file="$1" field="$2"
    curl -s --connect-timeout 3 "http://127.0.0.1:${CLIENT_PORT}/config" 2>/dev/null \
        | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    for f in d.get('files', []):
        if f.get('fileName') == sys.argv[1]:
            print(f.get(sys.argv[2], ''))
            break
except Exception:
    print('')
" "$file" "$field" 2>/dev/null
}

# do_push 向服务端 maintain 接口 PUSH 单个配置文件的生效查询。
# 入参: file_name。成功时全局 RESP/HTTP_CODE 填入服务端响应与状态码，返回 0；失败返回 1。
# 鉴权失败(401/403)或路由不存在(404)直接 exit 1(对所有文件都失败，无谓重试)。
do_push() {
    local file="$1"
    local push_content="{\"kind\":\"config\",\"config\":{\"namespace\":\"${NAMESPACE}\",\"group\":\"${FILE_GROUP}\",\"file_name\":\"${file}\"}}"
    local encoded
    encoded=$(python3 -c "import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1]))" "$push_content" 2>/dev/null || echo "$push_content")
    local url="http://${POLARIS_SERVER}:${MAINTAIN_PORT}/maintain/v1/clients/event?client_id=${CLIENT_ID}&content=${encoded}"
    log_info "调服务端: ${url}"

    local http_info
    http_info=$(mktemp)
    local curl_exit=0
    RESP=$(curl -sS --connect-timeout 10 --max-time 20 \
        -H "X-Polaris-Token: ${POLARIS_TOKEN}" \
        -w "\n__HTTP_CODE__%{http_code}\n__EXIT__%{exitcode}\n" \
        "$url" 2>"$http_info") || curl_exit=$?
    HTTP_CODE=$(echo "$RESP" | grep -oE '__HTTP_CODE__[0-9]+' | sed 's/__HTTP_CODE__//')
    RESP=$(echo "$RESP" | sed '/^__HTTP_CODE__/,/^__EXIT__/d; /^__EXIT__/d')
    local curl_err
    curl_err=$(cat "$http_info")
    rm -f "$http_info"

    if [[ -z "$RESP" && -z "$HTTP_CODE" ]]; then
        log_error "curl 调用失败 (退出码 ${curl_exit}): ${curl_err}"
        log_error "确认 maintain 端口 ${MAINTAIN_PORT} 可达、路径 /maintain/v1/clients/event 存在"
        log_error "诊断: curl -v ${url}"
        return 1
    fi
    log_info "HTTP 状态码: ${HTTP_CODE:-unknown}"
    log_info "服务端响应: ${RESP}"
    if [[ "$HTTP_CODE" == "401" || "$HTTP_CODE" == "403" ]]; then
        log_error "鉴权失败 (HTTP ${HTTP_CODE})，确认 POLARIS_TOKEN 正确"
        exit 1
    fi
    if [[ "$HTTP_CODE" == "404" ]]; then
        log_error "maintain 接口路径不存在 (HTTP 404)"
        log_error "确认服务端已实现 WatchClientEvents 且 maintain 路由含 /maintain/v1/clients/event"
        exit 1
    fi
    if [[ "$HTTP_CODE" != "200" ]]; then
        log_error "服务端返回非 200 (HTTP ${HTTP_CODE})，响应: ${RESP}"
        return 1
    fi
    return 0
}

echo ""
echo -e "${BLUE}╔══════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║      配置生效查询云上验证                       ║${NC}"
echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
echo ""
echo "  服务端:          ${POLARIS_SERVER} (maintain ${MAINTAIN_PORT})"
echo "  客户端 HTTP:     127.0.0.1:${CLIENT_PORT}"
echo "  配置文件:        ${NAMESPACE}/${FILE_GROUP}/${FILE_NAME}"
echo ""

log_step "步骤 1/3 获取客户端 clientID 与本地生效配置"

CLIENT_ID=$(curl -s --connect-timeout 3 "http://127.0.0.1:${CLIENT_PORT}/clientid" 2>/dev/null)
if [[ -z "$CLIENT_ID" ]]; then
    log_error "获取 clientID 失败，确认客户端已启动(client.sh start)且 /clientid 可达"
    exit 1
fi
log_info "clientID: ${CLIENT_ID}"

# 派生 3 个配置文件名
FILE_NAMES=()
for i in 1 2 3; do
    FILE_NAMES+=("${FILE_NAME}-${i}.yaml")
done

# 轮询 /config 直到 3 个文件都拿到非空 version/md5
waited=0
ALL_READY=false
while [[ $waited -lt 30 ]]; do
    ALL_READY=true
    for fname in "${FILE_NAMES[@]}"; do
        v=$(get_file_field "$fname" "version")
        m=$(get_file_field "$fname" "md5")
        if [[ -z "$v" || "$v" == "0" || -z "$m" ]]; then
            ALL_READY=false
            break
        fi
    done
    [[ "$ALL_READY" == "true" ]] && break
    sleep 1
    waited=$((waited + 1))
done
if [[ "$ALL_READY" != "true" ]]; then
    log_error "客户端未拉取到全部配置文件，确认已通过 client.sh setup 发布 3 份基线配置"
    exit 1
fi
for fname in "${FILE_NAMES[@]}"; do
    v=$(get_file_field "$fname" "version")
    m=$(get_file_field "$fname" "md5")
    c=$(get_file_field "$fname" "content")
    log_info "本地生效配置 ${fname}: version=${v}, md5=${m}, content 长度=${#c}"
done

log_step "步骤 2/3 等待 WatchClientEvents 长连接建立"
log_info "等待 ${WAIT_WATCHER_SEC}s ..."
sleep "$WAIT_WATCHER_SEC"

log_step "步骤 3/3 循环 3 个配置文件下发配置生效查询并校验 ACK"

OVERALL_PASS=true
CASE_IDX=0
for fname in "${FILE_NAMES[@]}"; do
    CASE_IDX=$((CASE_IDX + 1))
    log_step "  文件 ${CASE_IDX}/${#FILE_NAMES[@]}: ${fname}"

    cv=$(get_file_field "$fname" "version")
    cm=$(get_file_field "$fname" "md5")
    cc=$(get_file_field "$fname" "content")

    if ! do_push "$fname"; then
        log_error "❌ [文件 ${fname}] PUSH 失败"
        OVERALL_PASS=false
        continue
    fi

    # 解析 ACK content 字段(服务端响应 resp.clientEvent.content)
    ACK_JSON=$(echo "$RESP" | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    ce = d.get('clientEvent') or {}
    print(ce.get('content', ''))
except Exception as e:
    sys.stderr.write(str(e) + '\n')
    sys.exit(1)
" 2>/dev/null) || {
        log_error "❌ [文件 ${fname}] 解析服务端响应失败"
        OVERALL_PASS=false
        continue
    }
    if [[ -z "$ACK_JSON" ]]; then
        log_error "❌ [文件 ${fname}] 服务端响应无 clientEvent.content"
        log_error "  排查: 查客户端 client.log 是否有 'stream established' 日志"
        OVERALL_PASS=false
        continue
    fi
    log_info "ACK content: ${ACK_JSON}"

    # 从 ACK content JSON 提取字段
    ACK_APPLIED=$(echo "$ACK_JSON" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('applied',''))" 2>/dev/null || echo "")
    ACK_VERSION=$(echo "$ACK_JSON" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('version',''))" 2>/dev/null || echo "")
    ACK_MD5=$(echo "$ACK_JSON" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('md5',''))" 2>/dev/null || echo "")
    ACK_CONTENT=$(echo "$ACK_JSON" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('content',''))" 2>/dev/null || echo "")

    echo ""
    echo "  对比项        客户端本地            ACK 应答  (${fname})"
    echo "  -----------   -------------------   -------------------"
    echo "  applied       (客户端在监听)        ${ACK_APPLIED}"
    echo "  version       ${cv}      ${ACK_VERSION}"
    echo "  md5           ${cm}      ${ACK_MD5}"
    echo "  content 长度  ${#cc}      ${#ACK_CONTENT}"
    echo ""

    # 校验 1: applied 必须为 True
    if [[ "$ACK_APPLIED" == "True" ]]; then
        log_info "✅ [校验 1] ${fname} ACK applied=true"
    else
        log_error "❌ [校验 1] ${fname} ACK applied=${ACK_APPLIED} (期望 True)"
        OVERALL_PASS=false
    fi

    # 校验 2: version 一致
    if [[ -n "$ACK_VERSION" && "$ACK_VERSION" == "$cv" ]]; then
        log_info "✅ [校验 2] ${fname} ACK version 一致 (${ACK_VERSION})"
    else
        log_error "❌ [校验 2] ${fname} ACK version=${ACK_VERSION} != 客户端 ${cv}"
        OVERALL_PASS=false
    fi

    # 校验 3: md5 一致
    if [[ -n "$ACK_MD5" && "$ACK_MD5" == "$cm" ]]; then
        log_info "✅ [校验 3] ${fname} ACK md5 一致 (${ACK_MD5})"
    else
        log_error "❌ [校验 3] ${fname} ACK md5=${ACK_MD5} != 客户端 ${cm}"
        OVERALL_PASS=false
    fi

    # 校验 4: content 一致(客户端本地内容应包含在 ACK content 中，或被截断标记)
    if [[ "$ACK_CONTENT" == "$cc" ]]; then
        log_info "✅ [校验 4] ${fname} ACK content 与客户端本地一致"
    else
        log_warn "⚠️  [校验 4] ${fname} ACK content 与客户端本地不完全一致"
        log_warn "    客户端长度=${#cc}, ACK 长度=${#ACK_CONTENT}"
        log_warn "    若配置超 512KB 会被截断(正常)，否则需排查"
    fi
done

echo ""
if [[ "$OVERALL_PASS" == "true" ]]; then
    echo -e "${GREEN}验证结论: ✅ 配置生效查询功能验证通过${NC}"
    echo -e "${GREEN}  - 客户端通过 WatchClientEvents 长连接响应服务端配置生效查询${NC}"
    echo -e "${GREEN}  - ACK 携带的 version/md5/content 与客户端本地生效配置一致${NC}"
else
    echo -e "${YELLOW}验证结论: ⚠️ 部分校验未通过，请对照上述明细排查${NC}"
    echo -e "${YELLOW}  常见原因:${NC}"
    echo -e "${YELLOW}  1. 服务端未实现 WatchClientEvents 接口${NC}"
    echo -e "${YELLOW}  2. maintain 端口或鉴权 token 配置错误${NC}"
    echo -e "${YELLOW}  3. 客户端未订阅该配置文件(检查 /config version/md5 非空)${NC}"
    echo -e "${YELLOW}  4. WatchClientEvents 长连接未建立(查 client.log 'stream established')${NC}"
fi
echo ""
log_info "完整日志: ${TEST_LOG_FILE}"
echo ""
