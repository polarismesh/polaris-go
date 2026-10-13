#!/usr/bin/env bash
# 清理本示例进程、构建产物、验证日志和 SDK 缓存；支持 macOS 自带 Bash 3.2。
set -euo pipefail

force=false
dry_run=false
stop_timeout=60

usage() {
    cat <<'EOF'
用法: ./cleanup.sh [-f|--force] [--dry-run] [--timeout 秒数]

  -f, --force    跳过交互确认
  --dry-run      仅展示待停止进程和待删除路径
  --timeout N    SIGTERM 后等待退出的秒数，默认 60，范围 1-3600
  -h, --help     显示帮助

清理范围: 本目录的 .build/、.logs/、polaris/ 和 config_group_listener_cancel。
只停止当前用户运行的本目录示例二进制，需要 lsof。
停止超时则保留产物并返回失败，不发送 SIGKILL。
远端测试数据由 E2E 进程退出时清理；本脚本不发起远端删除请求。
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        -f|--force) force=true; shift ;;
        --dry-run) dry_run=true; shift ;;
        --timeout)
            if [[ $# -lt 2 ]] || [[ ! "$2" =~ ^[1-9][0-9]{0,3}$ ]] || (( $2 > 3600 )); then
                echo "--timeout 需要 1-3600 之间的整数秒数" >&2
                exit 1
            fi
            stop_timeout="$2"
            shift 2
            ;;
        -h|--help) usage; exit 0 ;;
        *) echo "未知参数: $1" >&2; usage >&2; exit 1 ;;
    esac
done

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
if ! command -v lsof >/dev/null 2>&1; then
    echo "需要 lsof 才能确认示例进程是否已停止；未执行清理。" >&2
    exit 1
fi

# lsof 的 txt 路径支持从相对路径启动的程序；不扫描或输出进程命令行参数。
# 命令名前缀只缩小候选集，最终仍以本目录下的完整二进制路径匹配。
find_demo_pids() {
    local snapshot line pid="" executable seen=" " status=0
    snapshot=$(lsof -nP -a -u "$UID" -c config-group -c config_group -d txt -Fpn "$@" 2>&1) || status=$?
    # macOS 中一个 -c 条件未匹配即可返回 1，即使另一个条件已返回有效记录。
    # 状态 1 仍解析字段；错误诊断会在下面按非字段行处理，不把检查失败当成无进程。
    if (( status > 1 )); then
        printf '无法检查示例进程，未执行文件清理: %s\n' "$snapshot" >&2
        return 1
    fi
    while IFS= read -r line; do
        case "$line" in
            p*) pid="${line#p}" ;;
            n*)
                executable="${line#n}"
                executable="${executable% (deleted)}"
                case "$executable" in
                    "$script_dir/.build/config-group-listener-cancel"|\
                    "$script_dir/.build/config-group-listener-cancel-linux-amd64"|\
                    "$script_dir/config_group_listener_cancel")
                        if [[ "$pid" =~ ^[0-9]+$ ]] && [[ "$seen" != *" $pid "* ]]; then
                            printf '%s\n' "$pid"
                            seen="$seen$pid "
                        fi
                        ;;
                esac
                ;;
            f*|"") ;;
            *) printf '无法确认进程检查结果: %s\n' "$line" >&2; return 1 ;;
        esac
    done <<< "$snapshot"
}

target_pids=$(find_demo_pids)
targets=()
for name in .build .logs polaris config_group_listener_cancel; do
    path="$script_dir/$name"
    if [[ -e "$path" || -L "$path" ]]; then
        targets+=("$path")
    fi
done

echo "清理目录: $script_dir"
echo "待停止的示例进程:"
if [[ -n "$target_pids" ]]; then
    while IFS= read -r pid; do
        printf '  PID=%s\n' "$pid"
    done <<< "$target_pids"
else
    echo "  无"
fi
echo "待删除的本地产物:"
for ((i=0; i<${#targets[@]}; i++)); do
    printf '  %s\n' "${targets[$i]}"
done
if [[ ${#targets[@]} -eq 0 ]]; then
    echo "  无"
fi

if [[ "$dry_run" == true ]]; then
    echo "[dry-run] 仅预览，未停止进程或删除文件。"
    exit 0
fi
if [[ -z "$target_pids" && ${#targets[@]} -eq 0 ]]; then
    echo "无需清理。"
    exit 0
fi
if [[ "$force" != true ]]; then
    answer=""
    if ! read -r -p "确认停止以上进程并删除以上产物? [y/N] " answer; then
        echo "未获得确认，已取消。"
        exit 0
    fi
    case "$answer" in
        y|Y|yes|YES|Yes) ;;
        *) echo "已取消。"; exit 0 ;;
    esac
fi

if [[ -n "$target_pids" ]]; then
    while IFS= read -r pid; do
        # 发送信号前重新确认路径，避免使用已经失效的进程快照。
        current=$(find_demo_pids -p "$pid")
        if [[ -z "$current" ]]; then
            continue
        fi
        if ! kill -TERM "$pid" 2>/dev/null; then
            current=$(find_demo_pids -p "$pid")
            if [[ -n "$current" ]]; then
                echo "无法停止 PID=$pid，保留本地产物。" >&2
                exit 1
            fi
        else
            printf '已发送 SIGTERM: PID=%s，等待 E2E 退出和远端清理。\n' "$pid"
        fi
    done <<< "$target_pids"

    deadline=$((SECONDS + stop_timeout))
    while :; do
        remaining=$(find_demo_pids)
        if [[ -z "$remaining" ]]; then
            break
        fi
        if (( SECONDS >= deadline )); then
            printf '等待退出超时，保留本地产物。仍在运行的 PID:\n%s\n' "$remaining" >&2
            echo "请检查 .logs/ 中的清理结果，或增大 --timeout 后重试。" >&2
            exit 1
        fi
        sleep 1
    done
fi

# 确认期间可能有新一轮测试启动；仍有示例进程时不删除运行中的产物。
remaining=$(find_demo_pids)
if [[ -n "$remaining" ]]; then
    printf '发现运行中的示例进程，保留本地产物。PID:\n%s\n' "$remaining" >&2
    exit 1
fi
for ((i=0; i<${#targets[@]}; i++)); do
    rm -rf -- "${targets[$i]}"
    printf '已清理: %s\n' "${targets[$i]}"
done
echo "本地清理完成。"
