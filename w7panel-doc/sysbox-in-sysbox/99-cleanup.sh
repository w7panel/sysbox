#!/usr/bin/env bash
# shellcheck disable=SC2153
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
discover_l1
log "deleting retained CKM nginx Deployment/PVC from $CHART_NAMESPACE"
l1_kubectl -n "$CHART_NAMESPACE" delete deployment "$CKM_TEST_DEPLOYMENT" \
  --ignore-not-found --wait=true --timeout=120s || true
l1_kubectl -n "$CHART_NAMESPACE" delete pvc "$CKM_TEST_ROOTFS_PVC" \
  --ignore-not-found --wait=true --timeout=120s || true
log "deleting retained Docker persistence Deployment/PVC from $CHART_NAMESPACE"
l1_kubectl -n "$CHART_NAMESPACE" delete deployment "$CKM_DOCKER_TEST_DEPLOYMENT" \
  --ignore-not-found --wait=true --timeout=120s || true
l1_kubectl -n "$CHART_NAMESPACE" delete pvc "$CKM_DOCKER_TEST_ROOTFS_PVC" \
  --ignore-not-found --wait=true --timeout=120s || true
log "deleting chart resources from CKM K3s namespace $CHART_NAMESPACE"
l1_kubectl -n "$CHART_NAMESPACE" delete daemonset,deploy,service,serviceaccount,configmap,secret,role,rolebinding \
  -l app.kubernetes.io/instance=w7panel-sysbox --ignore-not-found >/dev/null 2>&1 || true
l1_kubectl -n "$CHART_NAMESPACE" delete daemonset w7panel-sysbox-installer --ignore-not-found >/dev/null 2>&1 || true
l1_kubectl delete clusterrole,clusterrolebinding -l app.kubernetes.io/instance=w7panel-sysbox --ignore-not-found >/dev/null 2>&1 || true
l1_kubectl delete runtimeclass sysbox-runc runc-lite --ignore-not-found >/dev/null 2>&1 || true
if [ "${DELETE_CKM:-false}" = true ]; then
  log "deleting CKM $CKM_NAMESPACE/$CKM_NAME"
  outer_kubectl -n "$CKM_NAMESPACE" delete ckm "$CKM_NAME" --wait=false --ignore-not-found
else
  log 'DELETE_CKM=false; CKM retained. Set DELETE_CKM=true only for disposable test resources.'
fi
