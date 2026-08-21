#!/usr/bin/env bash
set -euo pipefail

# Enter the retained nginx Deployment in the CKM-owned K3s using a real TTY.
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
discover_l1

nginx_pod="$(l1_kubectl -n "$CHART_NAMESPACE" get pods \
	-l "app=${CKM_TEST_DEPLOYMENT:-ckm-k3s-nginx}" \
	-o jsonpath='{range .items[?(@.status.phase=="Running")]}{.metadata.name}{"\n"}{end}' | head -n1)"
[ -n "$nginx_pod" ] || die 'no Running ckm-k3s-nginx Pod found in the CKM K3s'

log "opening an interactive shell in ${CHART_NAMESPACE}/${nginx_pod}"
[ -c /dev/tty ] || die 'this helper requires a local interactive terminal (stdin must be a TTY)'
outer_kubectl -n "$OUTER_NAMESPACE" exec -it "$L1_POD" -c "$L1_CONTAINER" -- \
	kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml \
	exec -it -n "$CHART_NAMESPACE" "pod/$nginx_pod" -- /bin/sh < /dev/tty
