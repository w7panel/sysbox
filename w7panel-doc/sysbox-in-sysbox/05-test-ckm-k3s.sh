#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
discover_l1
log "testing a runc-lite workload directly in CKM K3s with ${TEST_IMAGE}"
if ! l1_exec test -x /tmp/runc-lite3 && ! l1_exec test -x /var/lib/rancher/k3s/runc-lite && ! l1_exec test -x /usr/local/bin/runc-lite; then
  die 'runc-lite is not installed in the CKM server image/data volume; install it before running the workload test'
fi
l1_exec sh -c '(/tmp/runc-lite3 features 2>/dev/null || /var/lib/rancher/k3s/runc-lite features 2>/dev/null || /usr/local/bin/runc-lite features 2>/dev/null) | grep -q '"'"'"user_namespaces": true'"'"'' \
  || die 'runc-lite does not advertise linux.user_namespaces=true'
l1_kubectl -n "$CHART_NAMESPACE" delete deployment "$CKM_TEST_DEPLOYMENT" --ignore-not-found --wait=true
l1_kubectl_input apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ${CKM_TEST_ROOTFS_PVC}
  namespace: ${CHART_NAMESPACE}
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: ${ROOTFS_STORAGE_CLASS}
  resources:
    requests:
      storage: 2Gi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${CKM_TEST_DEPLOYMENT}
  namespace: ${CHART_NAMESPACE}
spec:
  replicas: 1
  selector:
    matchLabels: {app: ${CKM_TEST_DEPLOYMENT}}
  template:
    metadata:
      labels: {app: ${CKM_TEST_DEPLOYMENT}}
      annotations:
        sysbox/rootfs-rw-layer: '[{"name":"nginx","volumeName":"rootfs","path":"nginx","persistentSpecialMounts":true,"specialPath":["/srv/data"]}]'
    spec:
      runtimeClassName: runc-lite
      tolerations:
      - key: sysbox-runtime
        operator: Exists
        effect: NoSchedule
      containers:
      - name: nginx
        image: ${TEST_IMAGE}
        ports: [{containerPort: 80}]
        volumeMounts:
        - name: rootfs
          mountPath: /usr/share/nginx/html
      volumes:
      - name: rootfs
        persistentVolumeClaim: {claimName: ${CKM_TEST_ROOTFS_PVC}}
EOF
l1_kubectl -n "$CHART_NAMESPACE" rollout status "deployment/$CKM_TEST_DEPLOYMENT" --timeout=180s
pod="$(l1_kubectl -n "$CHART_NAMESPACE" get pod -l "app=$CKM_TEST_DEPLOYMENT" --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.name}')"
[ -n "$pod" ] || die 'runc-lite workload Pod was not created'
[ "$(l1_kubectl -n "$CHART_NAMESPACE" get pod "$pod" -o jsonpath='{.spec.runtimeClassName}')" = runc-lite ] || die 'workload runtime is not runc-lite'
l1_rootfs_exec "$pod" nginx 'test -f /usr/share/nginx/html/index.html; mkdir -p /srv/data; echo persisted > /usr/share/nginx/html/.runc-lite-marker; test -d /srv/data'
l1_kubectl -n "$CHART_NAMESPACE" delete pod "$pod" --wait=true
l1_kubectl -n "$CHART_NAMESPACE" rollout status "deployment/$CKM_TEST_DEPLOYMENT" --timeout=180s
pod="$(l1_kubectl -n "$CHART_NAMESPACE" get pod -l "app=$CKM_TEST_DEPLOYMENT" --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.name}')"
l1_rootfs_exec "$pod" nginx 'test -f /usr/share/nginx/html/index.html; grep -qx persisted /usr/share/nginx/html/.runc-lite-marker; test -d /srv/data'
log 'FUNCTIONAL PASS: rootfs persistence, empty-volume init and special bind mount verified'
