#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
[ -n "${CKM_NAME:-}" ] || die 'CKM_NAME must be set in config.sh'
if configured_ckm_exists; then
  [ "${CREATE_CKM:-false}" != true ] || die "CKM $CKM_NAMESPACE/$CKM_NAME already exists; choose a new CKM_NAME"
  select_existing_ckm
  discover_l1
  log "CREATE_CKM=false; reusing configured CKM ${CKM_NAMESPACE}/${CKM_NAME}, no Ckm object was created"
  outer_kubectl -n "$OUTER_NAMESPACE" get pod "$L1_POD" -o wide
  exit 0
fi

CKM_SELECTOR="cluster=${CKM_NAME},role=server"
export CKM_SELECTOR
outer_kubectl create namespace "$CKM_NAMESPACE" --dry-run=client -o yaml | outer_kubectl apply -f - >/dev/null
log "creating CKM $CKM_NAMESPACE/$CKM_NAME with innerSysbox.enabled=true"
outer_kubectl apply -f - <<EOF
apiVersion: ckm.w7.cc/v1alpha2
kind: Ckm
metadata:
  name: ${CKM_NAME}
  namespace: ${CKM_NAMESPACE}
  labels:
    ckm.w7.cc/controller-version: v1alpha2
spec:
  runtimeClass: ${OUTER_RUNTIME_CLASS}
  innerSysbox:
    enabled: true
  storageClassName: local-path
  workload:
    apiVersion: k3k.io/v1beta1
    kind: Cluster
    token: ${CKM_NAME}-token
  userResource:
    cpu: 4
    memory: 8
    storage: 40
    bandwidth: 20
  purchasedResource:
    cpu: 4
    memory: 8
    storage: 40
    bandwidth: 20
  expireTime: "2099-12-31 23:59:59"
EOF

log 'waiting for CKM and its L1 Deployment'
for _ in $(seq 1 60); do
  phase="$(outer_kubectl -n "$CKM_NAMESPACE" get ckm "$CKM_NAME" -o jsonpath='{.status.clusterPhase}' 2>/dev/null || true)"
  pod_rows="$(outer_kubectl -n "$OUTER_NAMESPACE" get pods -l "$CKM_SELECTOR" \
    -o custom-columns='NAME:.metadata.name,PHASE:.status.phase,DELETING:.metadata.deletionTimestamp' --no-headers 2>/dev/null || true)"
  l1="$(awk '$2 == "Running" && ($3 == "" || $3 == "<none>") { print $1; exit }' <<<"$pod_rows")"
  printf '  clusterPhase=%s l1=%s\n' "${phase:-<pending>}" "${l1:-<pending>}"
  if [ -n "$l1" ]; then
    L1_POD="$l1"
    export L1_POD
    discover_l1
    break
  fi
  sleep 5
done
[ -n "${L1_POD:-}" ] || die 'L1 Pod did not become Running; inspect Ckm status/events'
outer_kubectl -n "$OUTER_NAMESPACE" get pod "$L1_POD" -o wide
outer_kubectl -n "$CKM_NAMESPACE" get ckm "$CKM_NAME" -o yaml | sed -n '/^status:/,$p' || true
log 'PASS: CKM resource and L1 Pod created'
