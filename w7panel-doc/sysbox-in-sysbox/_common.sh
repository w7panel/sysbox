#!/usr/bin/env bash
# Variables such as L2_POD are supplied by config.sh, which ShellCheck cannot
# follow through the runtime-selected config path.
# shellcheck disable=SC2153
set -euo pipefail

FLOW_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${FLOW_DIR}/../.." && pwd)"
CONFIG_FILE="${SYSBOX_IN_SYSBOX_CONFIG:-${FLOW_DIR}/config.sh}"
if [ -r "$CONFIG_FILE" ]; then
  # shellcheck disable=SC1090
  source "$CONFIG_FILE"
fi

log() { printf '[sysbox-in-sysbox] %s\n' "$*"; }
die() { printf '[sysbox-in-sysbox] ERROR: %s\n' "$*" >&2; exit 1; }
need_cmd() { command -v "$1" >/dev/null 2>&1 || die "missing command: $1"; }
outer_kubectl() { kubectl --kubeconfig "$KUBECONFIG_218" "$@"; }

discover_l1() {
  if [ -z "${L1_POD:-}" ]; then
    L1_POD="$(outer_kubectl -n "$OUTER_NAMESPACE" get pods -l "$CKM_SELECTOR" \
      --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.name}')"
  fi
  [ -n "$L1_POD" ] || die "no running L1 Pod for selector $CKM_SELECTOR"
  if [ -z "${L1_CONTAINER:-}" ]; then
    L1_CONTAINER="$(outer_kubectl -n "$OUTER_NAMESPACE" get pod "$L1_POD" \
      -o jsonpath='{range .spec.containers[*]}{.name}{"\n"}{end}' | \
      awk '$1 != "sysbox-rootfs" { print; exit }')"
  fi
  [ -n "$L1_CONTAINER" ] || die "no regular container in L1 Pod $L1_POD"
  export L1_POD L1_CONTAINER
  log "L1=${OUTER_NAMESPACE}/${L1_POD} container=${L1_CONTAINER}"
}

l1_exec() { outer_kubectl -n "$OUTER_NAMESPACE" exec "$L1_POD" -c "$L1_CONTAINER" -- "$@"; }
l1_kubectl() { l1_exec /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml "$@"; }
l2_kubectl() {
  l1_kubectl -n "$L2_NAMESPACE" exec "$L2_POD" -c k3s -- \
    /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml "$@"
}

check_common() {
  need_cmd kubectl
  need_cmd helm
  [ -r "$KUBECONFIG_218" ] || die "kubeconfig is not readable: $KUBECONFIG_218"
  outer_kubectl version --request-timeout=5s >/dev/null
}

render_chart() {
  local output="$1" target_namespace="$2"
  helm template w7panel-sysbox "$REPO_DIR/charts/w7panel-sysbox" \
    --namespace "$target_namespace" --set installMode=nested \
    --set admission.enabled=true \
    --set installer.image.repository="$SYSBOX_IMAGE_REPO" \
    --set-string installer.image.tag="$SYSBOX_IMAGE_TAG" \
    --set installer.image.digest="" --set admission.image.digest="" > "$output"
}
