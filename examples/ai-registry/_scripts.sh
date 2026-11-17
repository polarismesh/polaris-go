#!/usr/bin/env bash
set -euo pipefail

example_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "${example_dir}/../.." && pwd)"
binary="${example_dir}/.build/polaris-go-ai-registry"
invocation_dir="$PWD"

resolve_state_path() {
    local value="$1"
    if [[ "$value" = /* ]]; then
        printf '%s' "$value"
        return
    fi
    if [[ "$value" == ".logs" || "$value" == ".logs/"* || "$value" == "./.logs/"* ]]; then
        printf '%s/%s' "$example_dir" "$value"
        return
    fi
    printf '%s/%s' "$invocation_dir" "$value"
}

resolve_config_path() {
    local value="$1"
    if [[ "$value" = /* ]]; then
        printf '%s' "$value"
    else
        printf '%s/%s' "$invocation_dir" "$value"
    fi
}

run_ai_registry() {
    local mode="$1"
    shift
    local args=("$@")
    local index arg value
    for ((index = 0; index < ${#args[@]}; index++)); do
        arg="${args[$index]}"
        case "$arg" in
            --state=*|-state=*|--config-file=*|-config-file=*)
                value="${arg#*=}"
                if [[ -n "$value" && "$value" != /* ]]; then
                    case "$arg" in
                        --state=*|-state=*)
                            args[$index]="${arg%%=*}=$(resolve_state_path "$value")"
                            ;;
                        *)
                            args[$index]="${arg%%=*}=$(resolve_config_path "$value")"
                            ;;
                    esac
                fi
                ;;
            --state|-state|--config-file|-config-file)
                if ((index + 1 < ${#args[@]})); then
                    value="${args[$((index + 1))]}"
                    if [[ -n "$value" && "$value" != /* ]]; then
                        case "$arg" in
                            --state|-state)
                                args[$((index + 1))]="$(resolve_state_path "$value")"
                                ;;
                            *)
                                args[$((index + 1))]="$(resolve_config_path "$value")"
                                ;;
                        esac
                    fi
                fi
                ;;
        esac
    done
    mkdir -p "${example_dir}/.build"
    (
        cd "${repo_root}"
        go build -o "${binary}" ./examples/ai-registry
    )
    cd "${example_dir}"
    exec "${binary}" "-mode=${mode}" "${args[@]}"
}
