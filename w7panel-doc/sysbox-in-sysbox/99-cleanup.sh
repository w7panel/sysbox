#!/usr/bin/env bash
# shellcheck disable=SC2153
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
discover_l1
if l1_kubectl -n "$L2_NAMESPACE" get pod "$L2_POD" >/dev/null 2>&1; then
  log 'deleting legacy L2 chart resources and L2 test Pod/PVC'
  l2_kubectl -n sysbox-system delete daemonset,deploy,service,serviceaccount,configmap,secret,role,rolebinding \
    -l app.kubernetes.io/instance=w7panel-sysbox --ignore-not-found >/dev/null 2>&1 || true
  l2_kubectl delete clusterrole,clusterrolebinding -l app.kubernetes.io/instance=w7panel-sysbox --ignore-not-found >/dev/null 2>&1 || true
  l2_kubectl delete runtimeclass sysbox-runc --ignore-not-found >/dev/null 2>&1 || true
  l1_kubectl -n "$L2_NAMESPACE" delete pod "$L2_POD" --ignore-not-found --wait=true --timeout=120s || true
  l1_kubectl -n "$L2_NAMESPACE" delete pvc "$L2_PVC" --ignore-not-found --wait=true --timeout=120s || true
fi
for chart_namespace in "$CHART_NAMESPACE" sysbox-system; do
  [ "$chart_namespace" = "$CHART_NAMESPACE" ] || [ "$chart_namespace" != default ] || continue
  log "deleting chart resources from CKM K3s namespace $chart_namespace"
  l1_kubectl -n "$chart_namespace" delete daemonset,deploy,service,serviceaccount,configmap,secret,role,rolebinding \
    -l app.kubernetes.io/instance=w7panel-sysbox --ignore-not-found >/dev/null 2>&1 || true
done
l1_kubectl delete clusterrole,clusterrolebinding -l app.kubernetes.io/instance=w7panel-sysbox --ignore-not-found >/dev/null 2>&1 || true
l1_kubectl delete runtimeclass sysbox-runc --ignore-not-found >/dev/null 2>&1 || true
if [ "${DELETE_CKM:-false}" = true ]; then
  log "deleting CKM $CKM_NAMESPACE/$CKM_NAME"
  outer_kubectl -n "$CKM_NAMESPACE" delete ckm "$CKM_NAME" --wait=false --ignore-not-found
else
  log 'DELETE_CKM=false; CKM retained. Set DELETE_CKM=true only for disposable test resources.'
fi
