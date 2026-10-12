#!/usr/bin/env bash
# 构建带 race 检测的 E2E 程序；参数原样传给 Go，失败保留日志并返回非零状态。
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$script_dir"
mkdir -p .build .logs
go_command="${GO:-go}"
"$go_command" build -race -o .build/config-group-listener-cancel .
log_file=".logs/verify-$(date +%Y%m%d-%H%M%S)-$$.log"
echo "E2E log: $script_dir/$log_file"
.build/config-group-listener-cancel "$@" 2>&1 | tee "$log_file"
