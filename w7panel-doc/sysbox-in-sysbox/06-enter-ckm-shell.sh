#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

target="${1:-nginx}"
case "$target" in
  nginx)
    namespace="$CHART_NAMESPACE"
    deployment="$CKM_TEST_DEPLOYMENT"
    container=nginx
    shell=/bin/sh
    ;;
  docker)
    namespace="$CHART_NAMESPACE"
    deployment="$CKM_DOCKER_TEST_DEPLOYMENT"
    container=system
    shell=/bin/bash
    ;;
  *)
    die "usage: $0 [nginx|docker]"
    ;;
esac

check_common
discover_l1

pod="$(l1_kubectl -n "$namespace" get pods -l "app=$deployment" \
  -o jsonpath='{range .items[?(@.status.phase=="Running")]}{.metadata.name}{"\n"}{end}' | head -n1)"
[ -n "$pod" ] || die "no Running Pod found for Deployment/$deployment"
if [ ! -t 0 ] || [ ! -t 1 ]; then
  die 'this helper must run from an interactive terminal'
fi

log "opening $shell in ${namespace}/${pod} container=$container"
outer_kubectl -n "$OUTER_NAMESPACE" exec -it "$L1_POD" -c "$L1_CONTAINER" -- \
  /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml \
  -n "$namespace" exec -it "pod/$pod" -c "$container" -- "$shell"
