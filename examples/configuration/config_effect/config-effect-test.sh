#!/bin/bash
# =============================================================================
# 配置生效查询验证脚本
#
# 验证 polaris-go 客户端通过 WatchClientEvents 长连接响应服务端「配置生效查询」：
#   - 客户端订阅配置文件后，SDK 自动建立 WatchClientEvents 双向流并上报 clientID
#   - 服务端 maintain 接口 (GET /maintain/v1/clients/event) 向指定客户端 PUSH 配置生效查询
#   - 客户端回 ACK (含 version/md5/applied)，服务端原样透传给本脚本
#   - 脚本解析 ACK content，校验 version/md5 与客户端本地生效配置一致
#
# 使用方法:
#   chmod +x config-effect-test.sh
#   ./config-effect-test.sh [--polaris-server <地址>] [--polaris-token <令牌>]
#                           [--maintain-port <端口>] [--namespace <命名空间>]
#                           [--group <配置组>] [--file <文件名>]
#                           [--port <客户端HTTP端口>] [--debug]
#
# 前置条件:
#   1. 北极星服务端(Polaris Server 商业版)已启动，且已更新含 WatchClientEvents 逻辑的版本
#   2. Go 环境已安装
#   3. 服务端 maintain HTTP 端口可达 (默认 8090，可用 --maintain-port 指定)
#
# 验证原理:
#   - 客户端启动后通过 ReportClient 上报 clientID，并建立 WatchClientEvents 长连接
#   - 脚本读取客户端 /clientid 与 /config，获得 clientID 与本地生效配置 version/md5
#   - 脚本调服务端 maintain 接口向该 clientID PUSH 查询 {kind:config, config:{ns,group,file}}
#   - 服务端通过 stream 下发 PUSH，客户端回 ACK，服务端把 ACK.clientEvent.content 透传回脚本
#   - 脚本解析 ACK content，断言 applied=true 且 version/md5 与客户端 /config 一致
# =============================================================================

set -euo pipefail

# ======================== 默认配置 ========================
POLARIS_SERVER="${POLARIS_SERVER:-127.0.0.1}"
POLARIS_TOKEN="${POLARIS_TOKEN:-}"
MAINTAIN_PORT="${MAINTAIN_PORT:-8090}"
NAMESPACE="${NAMESPACE:-default}"
FILE_GROUP="${FILE_GROUP:-polaris-config-example}"
FILE_NAME="${FILE_NAME:-config-effect-example}"
CLIENT_PORT="${CLIENT_PORT:-18091}"
DEBUG_MODE="${DEBUG_MODE:-false}"

# 验证用的配置内容 base(具有辨识度，便于自动判定)，main.go 派生 effect-content-v1/v2/v3
CONFIG_CONTENT="${CONFIG_CONTENT:-effect-content-v}"

# ======================== 加密配置（第 1 个文件） ========================
# 第 ENCRYPT_FILE_INDEX 个派生文件作为「加密配置」验证端到端加解密 + 生效查询链路。
# SDK 的 CreateConfigFile/UpdateConfigFile 不带 Encrypted/Tags（见 transferToConfigFile），
# 无法创建加密配置，故改用服务端 console HTTP 接口 (POST /config/v1/configfiles) 创建。
ENCRYPT_FILE_INDEX="${ENCRYPT_FILE_INDEX:-1}"
# 加密算法名，与 SDK crypto/aes filter 注册的算法名一致（plugin/configfilter/crypto/aes）。
ENCRYPT_ALGO="${ENCRYPT_ALGO:-AES}"
# 服务端返回码：ExecuteSuccess / ExistedResource(已存在则转 PUT 更新)
CODE_EXECUTE_SUCCESS=200000
CODE_EXISTED_RESOURCE=400201

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# ======================== 解析命令行参数 ========================
while [[ $# -gt 0 ]]; do
    case "$1" in
        --polaris-server) POLARIS_SERVER="$2"; shift 2 ;;
        --polaris-token)  POLARIS_TOKEN="$2";  shift 2 ;;
        --maintain-port)  MAINTAIN_PORT="$2";  shift 2 ;;
        --namespace)      NAMESPACE="$2";      shift 2 ;;
        --group)          FILE_GROUP="$2";     shift 2 ;;
        --file)           FILE_NAME="$2";      shift 2 ;;
        --port)           CLIENT_PORT="$2";    shift 2 ;;
        --debug)          DEBUG_MODE="true";   shift ;;
        --help|-h)
            echo "用法: $0 [选项]"
            echo ""
            echo "选项:"
            echo "  --polaris-server <地址>  北极星服务端地址 (默认: 127.0.0.1)"
            echo "  --polaris-token <令牌>   北极星鉴权令牌 (默认: 空)"
            echo "  --maintain-port <端口>   服务端 maintain HTTP 端口 (默认: 8090)"
            echo "  --namespace <命名空间>   命名空间 (默认: default)"
            echo "  --group <配置组>         配置文件组 (默认: polaris-config-example)"
            echo "  --file <base name>      配置文件 base name，派生 -1/-2/-3.yaml (默认: config-effect-example)"
            echo "  --port <端口>            客户端 HTTP 观察端口 (默认: 18091)"
            echo "  --debug                  启用 debug 日志 (默认: 关闭)"
            exit 0
            ;;
        *)
            echo -e "${RED}未知参数: $1${NC}"; exit 1 ;;
    esac
done

# ======================== 全局变量 ========================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_DIR="${SCRIPT_DIR}/.build"
LOG_DIR="${SCRIPT_DIR}/.logs"
PID_FILE="${SCRIPT_DIR}/.config-effect-pids"
RESULT_FILE="${LOG_DIR}/config_effect_result.csv"
PID_CLIENT=""

# ======================== 清理函数 ========================
cleanup() {
    log_info "清理客户端进程..."
    if [[ -n "$PID_CLIENT" ]] && kill -0 "$PID_CLIENT" 2>/dev/null; then
        kill "$PID_CLIENT" 2>/dev/null || true
        wait "$PID_CLIENT" 2>/dev/null || true
    fi
    if [[ -f "$PID_FILE" ]]; then
        while IFS= read -r pid; do
            [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true
        done < "$PID_FILE"
        rm -f "$PID_FILE"
    fi
}
trap cleanup EXIT

# ======================== 工具函数 ========================

log_info()  { echo -e "${GREEN}[INFO]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*" >&2; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*" >&2; }
log_error() { echo -e "${RED}[ERROR]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*" >&2; }
log_step() {
    echo "" >&2
    echo -e "${CYAN}========================================${NC}" >&2
    echo -e "${CYAN}  $*${NC}" >&2
    echo -e "${CYAN}========================================${NC}" >&2
}

# setup_test_log 把后续 stdout/stderr 同时写入日志文件（带时间戳），终端保留彩色输出。
# 日志文件经 sed 去除 ANSI 颜色码便于 grep/less。
TEST_LOG_FILE="${LOG_DIR}/config-effect-$(date +%Y%m%d_%H%M%S).log"
setup_test_log() {
	mkdir -p "${LOG_DIR}"
	{
		echo "===== 配置生效查询验证日志 $(date '+%Y-%m-%d %H:%M:%S') ====="
		echo "Command: $0 $*"
	} > "${TEST_LOG_FILE}"
	exec > >(tee >(sed -u 's/\x1b\[[0-9;]*m//g' >> "${TEST_LOG_FILE}")) 2>&1
}

# wait_for_http 轮询 HTTP 端口直到返回响应，同时检查进程存活。
# 入参: url max_wait desc pid
wait_for_http() {
    local url="$1" max_wait="${2:-30}" desc="${3:-服务}" pid="${4:-}"
    local waited=0
    while [[ $waited -lt $max_wait ]]; do
        if [[ -n "$pid" ]] && ! kill -0 "$pid" 2>/dev/null; then
            log_error "${desc} 进程 (PID: ${pid}) 已退出"
            return 1
        fi
        if curl -s --connect-timeout 2 "$url" > /dev/null 2>&1; then
            log_info "${desc} 已就绪 ($url)"
            return 0
        fi
        sleep 1
        waited=$((waited + 1))
    done
    log_error "${desc} 未就绪 ($url)，等待了 ${max_wait}s"
    return 1
}

# get_client_id 从客户端 /clientid 接口获取 clientID。
get_client_id() {
    curl -s --connect-timeout 3 "http://127.0.0.1:${CLIENT_PORT}/clientid" 2>/dev/null
}

# get_file_field 从客户端 /config 接口的 files 数组中，按文件名提取指定字段。
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
    pass
" "$file" "$field" 2>/dev/null
}

# ======================== 加密配置准备（console HTTP 接口） ========================

# console_api_url 拼接服务端 console 配置接口 URL。console 与 maintain 同属一个 HTTP server，复用 MAINTAIN_PORT。
# 入参: path (如 /configfiles 或 /configfiles/release)
console_api_url() {
    echo "http://${POLARIS_SERVER}:${MAINTAIN_PORT}/config/v1$1"
}

# console_post 向 console 配置接口发送一次写请求并回显响应体。
# 入参: method(POST|PUT) path body_json
console_post() {
    local method="$1" path="$2" body="$3"
    curl -s --connect-timeout 10 --max-time 20 -X "$method" \
        -H "X-Polaris-Token: ${POLARIS_TOKEN}" -H "Content-Type: application/json" \
        -d "$body" "$(console_api_url "$path")" 2>/dev/null || true
}

# resp_code 提取 console 响应 JSON 的 code 字段（数字）。
resp_code() {
    echo "$1" | python3 -c 'import sys,json
try:
    print(json.load(sys.stdin).get("code",""))
except Exception:
    pass' 2>/dev/null
}

# create_or_update_config_file 通过 console 接口创建或更新一个配置文件。
# 入参: file_name content encrypted(true|false)
# 先 POST 创建；若已存在(code=400201)则 PUT 更新。返回非 0 表示失败。
create_or_update_config_file() {
    local file="$1" fcontent="$2" encrypted="$3"
    local body
    body=$(python3 -c 'import json,sys
print(json.dumps({
    "namespace": sys.argv[1], "group": sys.argv[2], "name": sys.argv[3],
    "content": sys.argv[4], "format": "yaml",
    "encrypted": sys.argv[5] == "true", "encrypt_algo": sys.argv[6],
}))' "$NAMESPACE" "$FILE_GROUP" "$file" "$fcontent" "$encrypted" "$ENCRYPT_ALGO")

    local resp code
    resp=$(console_post POST "/configfiles" "$body")
    code=$(resp_code "$resp")
    if [[ "$code" == "$CODE_EXECUTE_SUCCESS" ]]; then
        log_info "console 创建配置文件成功: ${file} (encrypted=${encrypted}, algo=${ENCRYPT_ALGO})"
        return 0
    fi
    if [[ "$code" == "$CODE_EXISTED_RESOURCE" ]]; then
        log_info "配置文件已存在，转为更新: ${file} (encrypted=${encrypted})"
        resp=$(console_post PUT "/configfiles" "$body")
        code=$(resp_code "$resp")
        if [[ "$code" == "$CODE_EXECUTE_SUCCESS" ]]; then
            log_info "console 更新配置文件成功: ${file}"
            return 0
        fi
    fi
    log_error "console 创建/更新配置文件失败: ${file}, 响应: ${resp}"
    return 1
}

# publish_config_file 通过 console 接口发布一个配置文件（全量 release）。
# release name 带时间戳保证唯一，避免重复执行时因 release 名冲突而发布失败。
# 入参: file_name
publish_config_file() {
    local file="$1"
    local body
    body=$(python3 -c 'import json,sys,time
print(json.dumps({
    "namespace": sys.argv[1], "group": sys.argv[2], "file_name": sys.argv[3],
    "name": "%s-release-%d" % (sys.argv[3], int(time.time())),
}))' "$NAMESPACE" "$FILE_GROUP" "$file")

    local resp code
    resp=$(console_post POST "/configfiles/release" "$body")
    code=$(resp_code "$resp")
    if [[ "$code" == "$CODE_EXECUTE_SUCCESS" ]]; then
        log_info "console 发布配置文件成功: ${file}"
        return 0
    fi
    log_error "console 发布配置文件失败: ${file}, 响应: ${resp}"
    return 1
}

# setup_encrypted_file 将第 ENCRYPT_FILE_INDEX 个派生文件创建/更新为加密配置并发布。
# 放在 demo setup（全量明文基线）之后调用，确保最终态为加密——即便 demo setup 先把该文件建成明文，
# 此处也会覆盖为加密；重复执行具有自纠正性。
setup_encrypted_file() {
    local enc_file="${FILE_NAME}-${ENCRYPT_FILE_INDEX}.yaml"
    local enc_content="${CONFIG_CONTENT}${ENCRYPT_FILE_INDEX}"
    log_info "通过 console 接口准备加密配置文件: ${enc_file} (algo=${ENCRYPT_ALGO}, 明文内容=${enc_content})"
    create_or_update_config_file "$enc_file" "$enc_content" "true" || return 1
    publish_config_file "$enc_file" || return 1
    log_info "加密配置文件已就绪: ${enc_file}"
    return 0
}

# ======================== 生成临时 polaris.yaml ========================
generate_polaris_yaml() {
    local target_file="$1"
    cat > "$target_file" <<EOF
global:
  serverConnector:
    addresses:
      - ${POLARIS_SERVER}:8091
    token: ${POLARIS_TOKEN}
    connectTimeout: 5000ms
  api:
    timeout: 5s
    maxRetryTimes: 2
    retryInterval: 1s
  eventReporter:
    enable: true
    chain:
      - pushgateway
config:
  configConnector:
    addresses:
      - ${POLARIS_SERVER}:8093
    token: ${POLARIS_TOKEN}
EOF
    log_info "生成 polaris.yaml -> ${target_file}"
}

# ======================== 进程管理 ========================
start_client() {
    local workdir="${BUILD_DIR}/client-run"
    local log_file="${LOG_DIR}/client.log"
    mkdir -p "$workdir"
    cp "${BUILD_DIR}/polaris-client.yaml" "${workdir}/polaris.yaml"

    local debug_flag=""
    [[ "$DEBUG_MODE" == "true" ]] && debug_flag="-debug"

    (cd "$workdir" && exec "${BUILD_DIR}/config-effect-demo" \
        -action=run \
        -config="${workdir}/polaris.yaml" \
        -namespace="$NAMESPACE" \
        -group="$FILE_GROUP" \
        -file="$FILE_NAME" \
        -port=":${CLIENT_PORT}" \
        $debug_flag \
        > "$log_file" 2>&1) &
    PID_CLIENT=$!
    echo "$PID_CLIENT" >> "$PID_FILE"
    log_info "客户端已启动 (PID: ${PID_CLIENT}, 端口: ${CLIENT_PORT}, 日志: ${log_file})"
}

# ======================== 核心验证：调服务端 maintain 接口下发 PUSH ========================
# query_config_effect 向服务端 maintain 接口请求对指定 clientID 下发配置生效查询。
# 返回值：服务端响应 JSON (apiservice.Response)，其中 clientEvent.content 为客户端 ACK content。
# 入参: client_id
query_config_effect() {
    local client_id="$1" file="$2"
    # PUSH content：单点查询目标配置文件（kind=config + 三元组，snake_case）
    local push_content="{\"kind\":\"config\",\"config\":{\"namespace\":\"${NAMESPACE}\",\"group\":\"${FILE_GROUP}\",\"file_name\":\"${file}\"}}"
    local url="http://${POLARIS_SERVER}:${MAINTAIN_PORT}/maintain/v1/clients/event?client_id=${client_id}&content=$(python3 -c "import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1]))" "$push_content" 2>/dev/null || echo "$push_content")"
    log_info "调服务端 maintain: ${url}"
    local resp
    resp=$(curl -s --connect-timeout 10 --max-time 20 \
        -H "X-Polaris-Token: ${POLARIS_TOKEN}" \
        "$url" 2>/dev/null) || true
    echo "$resp"
}

# extract_ack_field 从服务端响应中提取 clientEvent.content 内的指定字段。
# 服务端响应结构：{ code, info, clientEvent: { client_id, index, content } }
# content 是 JSON 字符串，内含 { kind, config, version, md5, applied }
# 入参: resp_json ack_field (applied|version|md5)
extract_ack_field() {
    local resp="$1" field="$2"
    # 先取 clientEvent.content 字符串值，再在其中提取目标字段
    local content
    content=$(echo "$resp" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    ce = data.get('clientEvent') or {}
    print(ce.get('content', ''))
except Exception as e:
    sys.stderr.write(str(e) + '\n')
    sys.exit(1)
" 2>/dev/null) || {
        log_error "解析服务端响应失败，原始响应: $resp"
        return 1
    }
    if [[ -z "$content" ]]; then
        log_error "服务端响应无 clientEvent.content，原始响应: $resp"
        return 1
    fi
    log_info "客户端 ACK content: $content"
    echo "$content" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    val = data.get('${field}', '')
    print(val)
except Exception as e:
    sys.stderr.write(str(e) + '\n')
    sys.exit(1)
" 2>/dev/null
}

# record_result 记录一条用例结果到 CSV。
# 入参: case_id name status detail
record_result() {
    echo "$(date '+%Y-%m-%d %H:%M:%S'),$1,$2,$3,$4" >> "$RESULT_FILE"
}

# ======================== 主流程 ========================
main() {
    setup_test_log "$@"
    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║          配置生效查询验证脚本                     ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "配置信息:"
    echo "  北极星服务端:       ${POLARIS_SERVER}"
    echo "  maintain 端口:      ${MAINTAIN_PORT}"
    echo "  命名空间/配置组:    ${NAMESPACE}/${FILE_GROUP}"
    echo "  配置文件名:         ${FILE_NAME}"
    echo "  客户端 HTTP 端口:   ${CLIENT_PORT}"
    echo "  Debug 日志:         ${DEBUG_MODE}"
    echo ""

    log_step "步骤 1/5 环境准备与编译"
    mkdir -p "$BUILD_DIR" "$LOG_DIR"
    echo "时间戳,用例编号,用例名称,结果,详情" > "$RESULT_FILE"
    : > "$PID_FILE"

    if ! command -v go &> /dev/null; then
        log_error "Go 未安装，请先安装 Go"
        exit 1
    fi
    if ! command -v python3 &> /dev/null; then
        log_error "python3 未安装，脚本依赖 python3 解析 JSON"
        exit 1
    fi
    log_info "Go 版本: $(go version)"

    if [[ ! -f "${SCRIPT_DIR}/main.go" ]]; then
        log_error "找不到源码: ${SCRIPT_DIR}/main.go"
        exit 1
    fi

    log_info "编译 config-effect-demo..."
    (cd "$SCRIPT_DIR" && go build -o "${BUILD_DIR}/config-effect-demo" .)
    if command -v xattr &> /dev/null; then
        xattr -c "${BUILD_DIR}/config-effect-demo" 2>/dev/null || true
    fi
    log_info "编译完成 -> ${BUILD_DIR}/config-effect-demo"

    log_step "步骤 2/5 生成配置与发布全量基线"
    generate_polaris_yaml "${BUILD_DIR}/polaris-client.yaml"

    log_info "通过 config-effect-demo setup 准备 3 份全量基线(base=${FILE_NAME}, content=${CONFIG_CONTENT}, 派生 -1/-2/-3.yaml, 已存在则跳过)..."
    (cd "${BUILD_DIR}" && "./config-effect-demo" \
        -action=setup \
        -config="${BUILD_DIR}/polaris-client.yaml" \
        -namespace="$NAMESPACE" -group="$FILE_GROUP" -file="$FILE_NAME" \
        -content="$CONFIG_CONTENT") || {
        log_error "全量基线准备失败，请确认配置组 ${FILE_GROUP} 存在且服务端可用"
        exit 1
    }

    # 将第 ${ENCRYPT_FILE_INDEX} 个文件改为加密配置（SDK 建不了加密配置，走 console HTTP 接口）
    setup_encrypted_file || {
        log_error "加密配置文件准备失败，请确认 console 接口 (${POLARIS_SERVER}:${MAINTAIN_PORT}/config/v1) 可达且 token 有写权限"
        exit 1
    }

    log_step "步骤 3/5 启动客户端"
    start_client
    sleep 1
    kill -0 "$PID_CLIENT" 2>/dev/null || { log_error "客户端启动失败"; cat "${LOG_DIR}/client.log" 2>/dev/null; exit 1; }
    wait_for_http "http://127.0.0.1:${CLIENT_PORT}/health" 30 "客户端" "$PID_CLIENT" || exit 1

    log_step "步骤 4/5 验证客户端已拉取全部基线配置"
    # 总体通过标记：在步骤 4（拉取/解密校验）与步骤 5（ACK 校验）间共享，任一环失败即置 false
    local overall_pass=true
    # 派生 3 个配置文件名
    local file_names=()
    local i
    for i in 1 2 3; do
        file_names+=("${FILE_NAME}-${i}.yaml")
    done

    # 轮询 /config 直到 3 个文件都拿到非空 version/md5
    local waited=0
    local all_ready=false
    while [[ $waited -lt 30 ]]; do
        all_ready=true
        for fname in "${file_names[@]}"; do
            local v m
            v=$(get_file_field "$fname" "version")
            m=$(get_file_field "$fname" "md5")
            if [[ -z "$v" || "$v" == "0" || -z "$m" ]]; then
                all_ready=false
                break
            fi
        done
        [[ "$all_ready" == "true" ]] && break
        sleep 1
        waited=$((waited + 1))
    done

    # 记录每个文件的拉取结果
    for fname in "${file_names[@]}"; do
        local v m c
        v=$(get_file_field "$fname" "version")
        m=$(get_file_field "$fname" "md5")
        c=$(get_file_field "$fname" "content")
        if [[ -z "$v" || "$v" == "0" || -z "$m" ]]; then
            log_error "客户端未拉取到配置文件 ${fname} (version=${v}, md5=${m})"
            record_result "0" "客户端拉取配置 ${fname}" "FAIL" "version=${v},md5=${m}"
        else
            log_info "客户端本地生效配置 ${fname}: version=${v}, md5=${m}, content=${c}"
            record_result "0" "客户端拉取配置 ${fname}" "PASS" "version=${v},md5=${m}"
        fi
    done
    if [[ "$all_ready" != "true" ]]; then
        log_error "存在配置文件未拉取成功，终止验证"
        exit 1
    fi

    # 加密配置端到端解密校验：第 ${ENCRYPT_FILE_INDEX} 个文件在客户端侧应被 crypto filter 解密回明文基线。
    # /config 快照的 content 字段是经 SDK 解密后的生效内容（model.ConfigFile.GetContent），
    # 与预期明文一致即证明「密文下发 → SDK 解密 → 明文生效」链路正确。
    local enc_file="${FILE_NAME}-${ENCRYPT_FILE_INDEX}.yaml"
    local enc_expect="${CONFIG_CONTENT}${ENCRYPT_FILE_INDEX}"
    local enc_actual
    enc_actual=$(get_file_field "$enc_file" "content")
    if [[ "$enc_actual" == "$enc_expect" ]]; then
        log_info "✅ [用例 3 加密配置解密一致] PASS - ${enc_file} 解密后内容=${enc_actual}"
        record_result "3" "加密配置解密一致 ${enc_file}" "PASS" "decrypted=${enc_actual}"
    else
        log_error "❌ [用例 3 加密配置解密一致] FAIL - ${enc_file} 解密后=${enc_actual} != 期望明文=${enc_expect}"
        record_result "3" "加密配置解密一致 ${enc_file}" "FAIL" "decrypted=${enc_actual},expect=${enc_expect}"
        overall_pass=false
    fi

    log_step "步骤 5/5 通过服务端 maintain 接口下发配置生效查询并校验 ACK"
    local client_id
    client_id=$(get_client_id)
    if [[ -z "$client_id" ]]; then
        log_error "获取 clientID 失败"
        record_result "1" "获取 clientID" "FAIL" "客户端 /clientid 为空"
        exit 1
    fi
    log_info "客户端 clientID: ${client_id}"
    record_result "1" "获取 clientID" "PASS" "clientID=${client_id}"

    # 等待 WatchClientEvents 长连接建立（SDK Start 后异步建立，留足时间）
    log_info "等待 WatchClientEvents 长连接建立 (5s)..."
    sleep 5

    # overall_pass 已在步骤 4 开头声明，此处直接沿用
    local case_idx=0
    for fname in "${file_names[@]}"; do
        case_idx=$((case_idx + 1))
        log_step "  文件 ${case_idx}/${#file_names[@]}: ${fname}"

        local client_version client_md5
        client_version=$(get_file_field "$fname" "version")
        client_md5=$(get_file_field "$fname" "md5")

        local resp
        resp=$(query_config_effect "$client_id" "$fname")
        log_info "服务端响应: ${resp}"

        local ack_applied ack_version ack_md5
        ack_applied=$(extract_ack_field "$resp" "applied") || {
            record_result "2.${case_idx}" "解析 ACK ${fname}" "FAIL" "解析失败"
            overall_pass=false
            continue
        }
        ack_version=$(extract_ack_field "$resp" "version") || true
        ack_md5=$(extract_ack_field "$resp" "md5") || true

        # 校验 1：applied 必须为 true（客户端确实在监听该配置文件）
        if [[ "$ack_applied" == "True" ]]; then
            log_info "✅ [用例 2.${case_idx}.1 ACK applied=true] PASS - ${fname}"
            record_result "2.${case_idx}.1" "ACK applied=true ${fname}" "PASS" "applied=True"
        else
            log_error "❌ [用例 2.${case_idx}.1 ACK applied=true] FAIL - ${fname} applied=${ack_applied} (期望 True)"
            record_result "2.${case_idx}.1" "ACK applied=true ${fname}" "FAIL" "applied=${ack_applied}"
            overall_pass=false
        fi

        # 校验 2：ACK version 与客户端本地 version 一致
        if [[ -n "$ack_version" && "$ack_version" == "$client_version" ]]; then
            log_info "✅ [用例 2.${case_idx}.2 ACK version 一致] PASS - ${fname} version=${ack_version}"
            record_result "2.${case_idx}.2" "ACK version 一致 ${fname}" "PASS" "ack=${ack_version},client=${client_version}"
        else
            log_error "❌ [用例 2.${case_idx}.2 ACK version 一致] FAIL - ${fname} ack=${ack_version} != client=${client_version}"
            record_result "2.${case_idx}.2" "ACK version 一致 ${fname}" "FAIL" "ack=${ack_version},client=${client_version}"
            overall_pass=false
        fi

        # 校验 3：ACK md5 与客户端本地 md5 一致
        if [[ -n "$ack_md5" && "$ack_md5" == "$client_md5" ]]; then
            log_info "✅ [用例 2.${case_idx}.3 ACK md5 一致] PASS - ${fname} md5=${ack_md5}"
            record_result "2.${case_idx}.3" "ACK md5 一致 ${fname}" "PASS" "ack=${ack_md5},client=${client_md5}"
        else
            log_error "❌ [用例 2.${case_idx}.3 ACK md5 一致] FAIL - ${fname} ack=${ack_md5} != client=${client_md5}"
            record_result "2.${case_idx}.3" "ACK md5 一致 ${fname}" "FAIL" "ack=${ack_md5},client=${client_md5}"
            overall_pass=false
        fi
    done

    # ==================== 结果汇总 ====================
    echo ""
    echo -e "${BLUE}╔══════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║          配置生效查询验证结果汇总                 ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "  配置文件 base:   ${NAMESPACE}/${FILE_GROUP}/${FILE_NAME} (派生 -1/-2/-3.yaml)"
    echo "  客户端 clientID: ${client_id}"
    echo ""
    echo "  用例明细:"
    awk -F',' 'NR>1 { printf "    [%s] %s: %s (%s)\n", $2, $3, $4, $5 }' "$RESULT_FILE"
    echo ""
    echo "  详细结果 CSV:    ${RESULT_FILE}"
    echo "  客户端日志:      ${LOG_DIR}/client.log"
    echo ""

    if [[ "$overall_pass" == "true" ]]; then
        echo -e "${GREEN}验证结论: ✅ 配置生效查询功能验证通过${NC}"
        echo -e "${GREEN}  - 客户端通过 WatchClientEvents 长连接响应服务端配置生效查询${NC}"
        echo -e "${GREEN}  - ACK 携带的 version/md5 与客户端本地生效配置一致${NC}"
        echo -e "${GREEN}  - applied=true 确认客户端正在监听该配置文件${NC}"
    else
        echo -e "${YELLOW}验证结论: ⚠️ 部分用例未通过，请对照上述明细与日志排查${NC}"
        echo -e "${YELLOW}  常见原因:${NC}"
        echo -e "${YELLOW}  1. 服务端未更新含 WatchClientEvents 逻辑的版本${NC}"
        echo -e "${YELLOW}  2. maintain 端口或鉴权 token 配置错误${NC}"
        echo -e "${YELLOW}  3. 客户端未订阅该配置文件 (检查 /config 的 version/md5 非空)${NC}"
        echo -e "${YELLOW}  4. WatchClientEvents 长连接未建立 (查 client.log 是否有 watcher 启动日志)${NC}"
    fi
    echo ""
    log_info "完整日志: ${TEST_LOG_FILE}"
    echo ""
}

main "$@"
