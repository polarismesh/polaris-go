#!/bin/bash
# =============================================================================
# 配置生效查询验证客户端节点脚本
#
# 部署物料由 build-materials.sh 生成，节点目录含:
#   x86-bin      config-effect-demo 预编译 Linux x86_64 二进制
#   polaris.yaml 配置模板(${POLARIS_SERVER}/${POLARIS_TOKEN} 占位)
#   client.sh    本脚本(setup/start/stop/status/restart)
#   clean.sh     清理脚本
#   verify-cloud.sh 配置生效查询验证脚本
#
# 使用方法:
#   ./client.sh start   --polaris-server 172.16.0.5 --port 18091
#   ./client.sh setup  --polaris-server 172.16.0.5 --content effect-content-v1
#   ./client.sh status --polaris-server 172.16.0.5 --port 18091
#   ./client.sh stop
#   ./client.sh restart --polaris-server 172.16.0.5 --port 18091
#
# 鉴权: POLARIS_TOKEN=xxx ./client.sh start --polaris-server 172.16.0.5 --port 18091
# DEBUG: 加 --debug
# =============================================================================

set -euo pipefail

# ======================== 默认配置 ========================
ACTION="start"
POLARIS_SERVER="${POLARIS_SERVER:-}"
POLARIS_TOKEN="${POLARIS_TOKEN:-}"
NAMESPACE="default"
FILE_GROUP="polaris-config-example"
FILE_NAME="config-effect-example"
PORT="18091"
CONTENT="effect-content-v"
DEBUG_MODE=false

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

# ======================== 解析参数 ========================
while [[ $# -gt 0 ]]; do
    case "$1" in
        start|stop|status|setup|restart) ACTION="$1"; shift ;;
        --polaris-server) POLARIS_SERVER="$2"; shift 2 ;;
        --polaris-token)  POLARIS_TOKEN="$2";  shift 2 ;;
        --namespace)      NAMESPACE="$2";      shift 2 ;;
        --group)          FILE_GROUP="$2";     shift 2 ;;
        --file)           FILE_NAME="$2";      shift 2 ;;
        --port)           PORT="$2";           shift 2 ;;
        --content)        CONTENT="$2";        shift 2 ;;
        --debug)          DEBUG_MODE=true;     shift ;;
        -h|--help)
            echo "用法: $0 <start|stop|status|setup|restart> [选项]"
            echo ""
            echo "选项:"
            echo "  --polaris-server <地址>  北极星服务端地址 (必填)"
            echo "  --polaris-token <令牌>   北极星鉴权令牌 (默认: 空)"
            echo "  --namespace <命名空间>   命名空间 (默认: default)"
            echo "  --group <配置组>         配置文件组 (默认: polaris-config-example)"
            echo "  --file <base name>      配置文件 base name，派生 -1/-2/-3.yaml (默认: config-effect-example)"
            echo "  --port <端口>            run 模式 HTTP 监听端口 (默认: 18091)"
            echo "  --content <内容base>     setup 模式写入的内容 base，派生 1/2/3 后缀 (默认: effect-content-v)"
            echo "  --debug                  启用 SDK debug 日志"
            exit 0
            ;;
        *)
            echo -e "${RED}未知参数: $1${NC}"; exit 1 ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="${SCRIPT_DIR}/x86-bin"
PIDFILE="${SCRIPT_DIR}/client.pid"
LOGFILE="${SCRIPT_DIR}/client.log"

log_info()  { echo -e "${GREEN}[INFO]${NC} $(date '+%H:%M:%S') $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $(date '+%H:%M:%S') $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $(date '+%H:%M:%S') $*"; }

# ======================== 前置校验 ========================
if [[ ! -x "$BIN" ]]; then
    log_error "x86-bin 不存在或不可执行: ${BIN}"
    exit 1
fi
if [[ -z "$POLARIS_SERVER" ]]; then
    log_error "需要 --polaris-server <地址>"
    exit 1
fi

debug_flag=""
[[ "$DEBUG_MODE" == "true" ]] && debug_flag="-debug"

# ======================== 子命令实现 ========================

# do_setup 发布全量基线(已存在则跳过)。前台执行，完成即退出。
do_setup() {
    log_info "发布 3 份全量基线: ${NAMESPACE}/${FILE_GROUP}/${FILE_NAME}-(1/2/3).yaml, content base=${CONTENT}"
    POLARIS_SERVER="$POLARIS_SERVER" POLARIS_TOKEN="$POLARIS_TOKEN" \
        "$BIN" -action=setup \
        -config="${SCRIPT_DIR}/polaris.yaml" \
        -namespace="$NAMESPACE" -group="$FILE_GROUP" -file="$FILE_NAME" \
        -content="$CONTENT" $debug_flag
}

# do_start 启动常驻客户端(nohup 后台 + pidfile + /health 自检)。
# 启动后客户端自动建 WatchClientEvents 长连接，暴露 /health /config /clientid 接口。
do_start() {
    if [[ -f "$PIDFILE" ]]; then
        local old_pid
        old_pid=$(cat "$PIDFILE" 2>/dev/null || true)
        if [[ -n "$old_pid" ]] && kill -0 "$old_pid" 2>/dev/null; then
            log_warn "已在运行 (PID: ${old_pid})，如需重启请先 stop"
            exit 0
        fi
        rm -f "$PIDFILE"
    fi

    log_info "启动客户端: port=${PORT}, polaris=${POLARIS_SERVER}"
    POLARIS_SERVER="$POLARIS_SERVER" POLARIS_TOKEN="$POLARIS_TOKEN" \
        nohup "$BIN" -action=run \
        -config="${SCRIPT_DIR}/polaris.yaml" \
        -namespace="$NAMESPACE" -group="$FILE_GROUP" -file="$FILE_NAME" \
        -port=":${PORT}" $debug_flag >"$LOGFILE" 2>&1 &
    local pid=$!
    echo "$pid" > "$PIDFILE"
    log_info "客户端进程已启动 (PID: ${pid})，等待就绪..."

    # 自检: 轮询 /health，初始拉取完成后返回 200
    local waited=0
    while [[ $waited -lt 30 ]]; do
        if ! kill -0 "$pid" 2>/dev/null; then
            log_error "进程已退出，见日志: ${LOGFILE}"
            tail -20 "$LOGFILE" 2>/dev/null || true
            rm -f "$PIDFILE"
            exit 1
        fi
        local code
        code=$(curl -s -o /dev/null -w '%{http_code}' --connect-timeout 1 --max-time 2 \
            "http://127.0.0.1:${PORT}/health" 2>/dev/null || echo "000")
        if [[ "$code" == "200" ]]; then
            log_info "✅ 客户端就绪 (PID: ${pid}, port: ${PORT})"
            exit 0
        fi
        sleep 1
        waited=$((waited + 1))
    done
    log_error "客户端在 30s 内未就绪，见日志: ${LOGFILE}"
    tail -20 "$LOGFILE" 2>/dev/null || true
    exit 1
}

# do_stop 停止客户端(SIGTERM → 等待 → SIGKILL 兜底)。
do_stop() {
    if [[ ! -f "$PIDFILE" ]]; then
        log_info "未运行"
        exit 0
    fi
    local pid
    pid=$(cat "$PIDFILE" 2>/dev/null || true)
    if [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null; then
        log_info "进程未运行，清理 pidfile"
        rm -f "$PIDFILE"
        exit 0
    fi
    log_info "停止客户端 (PID: ${pid})..."
    kill "$pid" 2>/dev/null || true
    local waited=0
    while [[ $waited -lt 10 ]]; do
        kill -0 "$pid" 2>/dev/null || break
        sleep 1
        waited=$((waited + 1))
    done
    if kill -0 "$pid" 2>/dev/null; then
        log_warn "未在 10s 内退出，强制终止"
        kill -9 "$pid" 2>/dev/null || true
    fi
    rm -f "$PIDFILE"
    log_info "✅ 已停止"
}

# do_status 查询运行状态 + 当前生效配置 + clientID。
do_status() {
    if [[ -f "$PIDFILE" ]]; then
        local pid
        pid=$(cat "$PIDFILE" 2>/dev/null || true)
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            log_info "运行中 (PID: ${pid})"
            local code
            code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 \
                "http://127.0.0.1:${PORT}/health" 2>/dev/null || echo "000")
            echo -e "  health: ${code}"
            echo -n "  config: "
            curl -s --max-time 2 "http://127.0.0.1:${PORT}/config" 2>/dev/null || echo "(不可达)"
            echo
            echo -n "  clientID: "
            curl -s --max-time 2 "http://127.0.0.1:${PORT}/clientid" 2>/dev/null || echo "(不可达)"
            echo
            exit 0
        fi
    fi
    log_info "未运行"
    exit 1
}

# ======================== 主流程 ========================
case "$ACTION" in
    setup)   do_setup ;;
    start)   do_start ;;
    stop)    do_stop ;;
    status)  do_status ;;
    restart) do_stop; do_start ;;
esac
