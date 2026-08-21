#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
discover_l1
[ "$(outer_kubectl -n "$OUTER_NAMESPACE" get pod "$L1_POD" -o jsonpath='{.spec.runtimeClassName}')" = "$OUTER_RUNTIME_CLASS" ] || die "CKM Pod is not using runtimeClass $OUTER_RUNTIME_CLASS"
[ "$(outer_kubectl -n "$OUTER_NAMESPACE" get pod "$L1_POD" -o jsonpath='{.spec.hostUsers}')" = false ] || die 'CKM Pod hostUsers is not false'
l1_kubectl create namespace sysbox-system --dry-run=client -o yaml | l1_kubectl_input apply -f - >/dev/null
manifest="$(mktemp)"
trap 'rm -f "$manifest"' EXIT
render_chart "$manifest" sysbox-system
log 'installing installMode=nested chart in the K3s owned by the configured CKM'
l1_kubectl_input apply -f - < "$manifest"
l1_kubectl -n sysbox-system rollout status daemonset/w7panel-sysbox-nested-agent --timeout=180s
l1_kubectl -n sysbox-system rollout status deployment/w7panel-sysbox-admission --timeout=180s
l1_kubectl get runtimeclass sysbox-runc -o jsonpath='{.handler}{"\n"}' | grep -qx sysbox-runc
l1_kubectl get node -l sysbox.w7panel.io/nested-runtime=ready
log 'PASS: chart installed in the existing CKM K3s; no second K3s was created'
