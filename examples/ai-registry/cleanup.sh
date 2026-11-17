#!/usr/bin/env bash
set -euo pipefail

example_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
source "${example_dir}/_scripts.sh"
run_ai_registry cleanup "$@"
