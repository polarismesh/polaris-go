#!/bin/bash
# =============================================================================
# 配置生效查询云上验证 - 客户端节点日志打包脚本
#
# 收集节点目录下的日志并打包，便于传回本地分析:
#   client.log      客户端进程标准输出(client.sh start 的 LOGFILE)
#   nohup.out       nohup 兜底输出(正常不会产生，防御性收集)
#   .logs/          verify-cloud.sh 验证日志(含 ACK 内容与各用例断言明细)
#   polaris/        Polaris SDK 日志与本地配置缓存目录
#
# 注意: 请在 clean.sh 之前执行本脚本——clean.sh 会删除 client.log 与 polaris/。
#
# 使用方法:
#   ./pack-logs.sh                    # 生成 client-logs-<时间戳>.zip
#   ./pack-logs.sh /root/my-logs.zip  # 指定输出路径
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TS="$(date '+%Y%m%d_%H%M%S')"
OUT="${1:-${SCRIPT_DIR}/client-logs-${TS}.zip}"

cd "$SCRIPT_DIR"

FILES=()
for f in client.log nohup.out; do
    [[ -f "$f" ]] && FILES+=("$f")
done
[[ -d .logs ]] && FILES+=(".logs")
[[ -d polaris ]] && FILES+=("polaris")

if [[ ${#FILES[@]} -eq 0 ]]; then
    echo "未找到任何日志文件(尚未启动过客户端或执行 verify-cloud.sh?)"
    exit 1
fi

if command -v zip &> /dev/null; then
    rm -f "$OUT"
    zip -rq "$OUT" "${FILES[@]}"
else
    OUT="${OUT%.zip}.tar.gz"
    rm -f "$OUT"
    tar czf "$OUT" "${FILES[@]}"
fi

echo "日志包: ${OUT} ($(du -h "$OUT" | cut -f1))"
echo "传回本地: scp root@<本机地址>:${OUT} ."
