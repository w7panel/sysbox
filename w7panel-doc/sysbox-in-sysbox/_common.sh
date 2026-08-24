#!/usr/bin/env bash
# Variables are supplied by config.sh, which ShellCheck cannot follow through
# the runtime-selected config path.
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
configured_ckm_exists() {
  [ -n "${CKM_NAMESPACE:-}" ] && [ -n "${CKM_NAME:-}" ] || return 1
  outer_kubectl -n "$CKM_NAMESPACE" get ckm "$CKM_NAME" >/dev/null 2>&1
}

select_existing_ckm() {
  local rows candidate ckm_inner ckm_runtime ckm_phase

  [ -n "${CKM_NAME:-}" ] || die 'CKM_NAME must be set in config.sh; automatic/random CKM selection is disabled'
  rows="$(outer_kubectl get ckm -A -o custom-columns='NAMESPACE:.metadata.namespace,NAME:.metadata.name,INNER:.spec.innerSysbox.enabled,RUNTIME:.spec.runtimeClass,PHASE:.status.clusterPhase' --no-headers)"
  candidate="$(awk -v namespace="${CKM_NAMESPACE:-}" -v name="$CKM_NAME" \
    '$2 == name && (namespace == "" || $1 == namespace) { print; exit }' <<<"$rows")"
  [ -n "$candidate" ] || die "configured CKM ${CKM_NAMESPACE:-<any>}/$CKM_NAME was not found"

  read -r CKM_NAMESPACE CKM_NAME ckm_inner ckm_runtime ckm_phase <<<"$candidate"
  [ "$ckm_inner" = true ] || die "CKM $CKM_NAMESPACE/$CKM_NAME has innerSysbox.enabled=$ckm_inner"
  [ "$ckm_runtime" = sysbox-runc ] || die "CKM $CKM_NAMESPACE/$CKM_NAME uses runtimeClass=$ckm_runtime"
  [ "$ckm_phase" = Ready ] || die "CKM $CKM_NAMESPACE/$CKM_NAME is not Ready: $ckm_phase"
  CKM_SELECTOR="cluster=${CKM_NAME},role=server"
  CKM_SELECTED=true
  export CKM_NAMESPACE CKM_NAME CKM_SELECTOR CKM_SELECTED
  log "selected existing CKM ${CKM_NAMESPACE}/${CKM_NAME} selector=${CKM_SELECTOR}"
}

discover_l1() {
  local pod_rows
  [ "${CKM_SELECTED:-false}" = true ] || select_existing_ckm
  if [ -z "${L1_POD:-}" ]; then
    pod_rows="$(outer_kubectl -n "$OUTER_NAMESPACE" get pods -l "$CKM_SELECTOR" \
      -o custom-columns='NAME:.metadata.name,PHASE:.status.phase,DELETING:.metadata.deletionTimestamp' --no-headers)"
    L1_POD="$(awk '$2 == "Running" && ($3 == "" || $3 == "<none>") { print $1; exit }' <<<"$pod_rows")"
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
l1_kubectl_input() {
  outer_kubectl -n "$OUTER_NAMESPACE" exec -i "$L1_POD" -c "$L1_CONTAINER" -- \
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
