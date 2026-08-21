#!/usr/bin/env bash
set -euo pipefail
# Backward-compatible entrypoint. The CKM already owns the only K3s needed by
# this flow; do not create a second nested K3s here.
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
printf '[sysbox-in-sysbox] 04-create-l2-k3s.sh is deprecated; installing the chart in the configured CKM K3s\n'
exec bash "$script_dir/04-install-ckm-chart.sh" "$@"
