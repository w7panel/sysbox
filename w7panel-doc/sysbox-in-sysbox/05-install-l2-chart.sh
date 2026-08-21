#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
discover_l1
l2_kubectl get nodes >/dev/null || die 'L2 API is unavailable; run 04-create-l2-k3s.sh'
manifest="$(mktemp)"
trap 'rm -f "$manifest"' EXIT
render_chart "$manifest" sysbox-system
log 'applying the same chart in L2 with installMode=nested'
l2_kubectl create namespace sysbox-system --dry-run=client -o yaml | l2_kubectl apply -f - >/dev/null
l2_kubectl apply -f - < "$manifest"
l2_kubectl -n sysbox-system rollout status daemonset/w7panel-sysbox-nested-agent --timeout=180s
l2_kubectl -n sysbox-system rollout status deployment/w7panel-sysbox-admission --timeout=180s
l2_kubectl get runtimeclass sysbox-runc -o jsonpath='{.handler}{"\n"}' | grep -qx sysbox-runc
l2_kubectl get node -l sysbox.w7panel.io/nested-runtime=ready
log 'PASS: L2 nested chart, admission and RuntimeClass are Ready'
