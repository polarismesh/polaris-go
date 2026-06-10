#!/bin/bash
# =============================================================================
# 清理 mixed 路由示例在 Polaris 服务端的历史残留规则 (lane group / rule route /
# nearby route). 默认 token 没有 delete 权限, 必须用 POLARIS_ADMIN_TOKEN
# (有命名空间级 admin/owner 权限) 才能成功删除。
#
# 用法:
#   POLARIS_ADMIN_TOKEN=xxx POLARIS_HOST=114.132.29.62 bash cleanup_rules.sh
#   POLARIS_ADMIN_TOKEN=xxx POLARIS_HOST=114.132.29.62 bash cleanup_rules.sh --dry-run
# =============================================================================

set -u
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ==================== 配置 ====================
POLARIS_HOST="${POLARIS_HOST:-127.0.0.1}"
POLARIS_HTTP_PORT="${POLARIS_HTTP_PORT:-8090}"
POLARIS_ADMIN_TOKEN="${POLARIS_ADMIN_TOKEN:-}"
NAMESPACE="${NAMESPACE:-default}"
LANE_GROUP_NAME="${LANE_GROUP_NAME:-mixed-lane-group}"
RULE_ROUTE_NAME="${RULE_ROUTE_NAME:-mixed-rule-route}"
NEARBY_ROUTE_NAME="${NEARBY_ROUTE_NAME:-mixed-nearby-route}"
DRY_RUN=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run) DRY_RUN=true; shift ;;
        -h|--help) sed -n '2,11p' "${BASH_SOURCE[0]}"; exit 0 ;;
        *) echo "未知参数: $1"; exit 1 ;;
    esac
done

POLARIS_HTTP_ADDR="http://${POLARIS_HOST}:${POLARIS_HTTP_PORT}"

if [[ -z "${POLARIS_ADMIN_TOKEN}" ]]; then
    echo "[ERROR] POLARIS_ADMIN_TOKEN 未设置, 无 delete 权限无法清理"
    echo "        用法: POLARIS_ADMIN_TOKEN=xxx POLARIS_HOST=${POLARIS_HOST} bash $0"
    exit 1
fi

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'
_log()      { echo -e "$@"; }
info()      { _log "${BLUE}[INFO]${NC} $*"; }
ok()        { _log "${GREEN}[OK]${NC}   $*"; }
warn()      { _log "${YELLOW}[WARN]${NC} $*"; }
fail()      { _log "${RED}[FAIL]${NC} $*"; }

delete_lane_groups() {
    info "查询泳道组 ${LANE_GROUP_NAME} (namespace=${NAMESPACE})..."
    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/v1/Discover" \
        --header "X-Polaris-Token:${POLARIS_ADMIN_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "{\"service\":{\"name\":\"${CONSUMER_SERVICE:-MixedRouteEchoClient}\",\"namespace\":\"${NAMESPACE}\"},\"type\":\"LANE\"}" \
        2>/dev/null)

    local ids
    ids=$(echo "$resp" | LANE_NAME="$LANE_GROUP_NAME" python3 -c "
import os, sys, json
target = os.environ['LANE_NAME']
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
for g in (data.get('lanes') or []):
    if g.get('name') == target:
        print(g.get('id', ''))
" 2>/dev/null)

    if [[ -z "$ids" ]]; then
        ok "未发现泳道组 ${LANE_GROUP_NAME}"
        return 0
    fi

    local count
    count=$(echo "$ids" | grep -c . || true)
    info "发现 ${count} 份泳道组, ids: $(echo "$ids" | tr '\n' ' ')"

    for id in $ids; do
        if [[ "$DRY_RUN" == "true" ]]; then
            warn "[dry-run] 跳过实际删除: ${id}"
            continue
        fi
        local result
        result=$(curl -s --connect-timeout 5 --max-time 10 \
            --request POST "${POLARIS_HTTP_ADDR}/naming/v1/lane/groups/delete" \
            --header "X-Polaris-Token:${POLARIS_ADMIN_TOKEN}" \
            --header 'Content-Type: application/json' \
            --data-raw "[{\"id\":\"${id}\",\"name\":\"${LANE_GROUP_NAME}\"}]" 2>/dev/null)
        local code
        code=$(echo "$result" | python3 -c "import sys,json; print(json.load(sys.stdin).get('code',0))" 2>/dev/null || echo "?")
        if [[ "$code" =~ ^20000[01]$ ]]; then
            ok "删除泳道组 ${id} 成功"
        else
            fail "删除泳道组 ${id} 失败 (code=${code})"
        fi
    done
}

delete_routings() {
    local name="$1"
    info "查询规则 ${name}..."
    local resp
    resp=$(curl -s --connect-timeout 5 --max-time 10 \
        --request GET "${POLARIS_HTTP_ADDR}/naming/v2/routings?name=${name}&offset=0&limit=100" \
        --header "X-Polaris-Token:${POLARIS_ADMIN_TOKEN}" 2>/dev/null)

    local ids
    ids=$(echo "$resp" | NAME="$name" python3 -c "
import os, sys, json
target = os.environ['NAME']
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
for it in (data.get('data') or []):
    if it.get('name') == target:
        print(it.get('id', ''))
" 2>/dev/null)

    if [[ -z "$ids" ]]; then
        ok "未发现规则 ${name}"
        return 0
    fi

    local count
    count=$(echo "$ids" | grep -c . || true)
    info "发现 ${count} 份规则 ${name}, ids: $(echo "$ids" | tr '\n' ' ')"

    if [[ "$DRY_RUN" == "true" ]]; then
        warn "[dry-run] 跳过实际删除"
        return 0
    fi

    # 批量删除
    local del_body
    del_body=$(echo "$ids" | NAME="$name" python3 -c "
import os, sys, json
ids = [l.strip() for l in sys.stdin.read().splitlines() if l.strip()]
print(json.dumps([{'id':i,'name':os.environ['NAME']} for i in ids]))
" 2>/dev/null)
    local result
    result=$(curl -s --connect-timeout 5 --max-time 10 \
        --request POST "${POLARIS_HTTP_ADDR}/naming/v2/routings/delete" \
        --header "X-Polaris-Token:${POLARIS_ADMIN_TOKEN}" \
        --header 'Content-Type: application/json' \
        --data-raw "${del_body}" 2>/dev/null)
    local code
    code=$(echo "$result" | python3 -c "import sys,json; print(json.load(sys.stdin).get('code',0))" 2>/dev/null || echo "?")
    if [[ "$code" =~ ^20000[01]$ ]]; then
        ok "批量删除规则 ${name} 成功 (${count} 份)"
    else
        fail "批量删除规则 ${name} 失败 (code=${code}, resp=${result:0:200})"
    fi
}

echo "===== 清理 mixed 路由示例规则 ($(date '+%Y-%m-%d %H:%M:%S')) ====="
echo "POLARIS: ${POLARIS_HTTP_ADDR}"
echo "NAMESPACE: ${NAMESPACE}"
echo "DRY_RUN: ${DRY_RUN}"
echo ""

delete_lane_groups
delete_routings "${RULE_ROUTE_NAME}"
delete_routings "${NEARBY_ROUTE_NAME}"

echo ""
ok "清理完成"
