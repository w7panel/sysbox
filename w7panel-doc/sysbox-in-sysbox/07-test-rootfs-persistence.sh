#!/usr/bin/env bash
set -euo pipefail
# DEPRECATED: tests the old L2 Pod/PVC rootfs flow and is outside the current
# CKM-owned-K3s workflow. It may create or recreate a second K3s.
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
discover_l1
l2_kubectl get pod "$L2_POD" >/dev/null || die 'L2 Pod is unavailable'
marker="/rootfs-persistence-manual"
value="sysbox-in-sysbox-$(date +%s)"
l2_kubectl exec "$L2_POD" -c k3s -- /bin/sh -ec \
  "printf '%s\\n' '$value' > '$marker'; stat -c 'before inode=%i owner=%u:%g size=%s' '$marker'; cat '$marker'"
if [ "${RECREATE_L2:-false}" != true ]; then
  log 'RECREATE_L2=false; marker write passed. Set RECREATE_L2=true for Pod recreation.'
  exit 0
fi
log "recreating L2 Pod $L2_POD (PVC is retained)"
l1_kubectl -n "$L2_NAMESPACE" delete pod "$L2_POD" --wait=true --timeout=120s
bash "$(dirname "$0")/04-create-l2-k3s.sh"
got="$(l2_kubectl exec "$L2_POD" -c k3s -- cat "$marker")"
[ "$got" = "$value" ] || die "rootfs marker changed after Pod recreation: expected=$value got=$got"
l2_kubectl exec "$L2_POD" -c k3s -- stat -c 'after inode=%i owner=%u:%g size=%s' "$marker"
log 'PASS: rootfs-rw-layer marker survived L2 Pod recreation'
