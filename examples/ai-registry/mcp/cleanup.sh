#!/usr/bin/env bash
set -euo pipefail

suite_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
source "${suite_dir}/../_scripts.sh"
run_ai_registry cleanup --suite mcp "$@"
