#!/usr/bin/env bash
set -euo pipefail
# Backward-compatible name. The CKM Pod's own K3s is the chart target.
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec bash "$script_dir/04-install-ckm-chart.sh" "$@"
