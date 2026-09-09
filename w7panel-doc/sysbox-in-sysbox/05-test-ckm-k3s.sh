#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
discover_l1
log "testing a sysbox-runc-lite workload directly in CKM K3s with ${TEST_IMAGE}"
if ! l1_exec test -x /var/lib/rancher/k3s/sysbox-runc-lite && ! l1_exec test -x /usr/local/bin/sysbox-runc-lite; then
  die 'sysbox-runc-lite is not installed in the CKM server image/data volume; install it before running the workload test'
fi
l1_exec sh -c '(/var/lib/rancher/k3s/sysbox-runc-lite features 2>/dev/null || /usr/local/bin/sysbox-runc-lite features 2>/dev/null) | grep -q '"'"'"user_namespaces": true'"'"'' \
  || die 'sysbox-runc-lite does not advertise linux.user_namespaces=true'
l1_kubectl -n "$CHART_NAMESPACE" delete deployment "$CKM_TEST_DEPLOYMENT" --ignore-not-found --wait=true
l1_kubectl -n "$CHART_NAMESPACE" delete pvc "$CKM_TEST_VOLUME_INIT_PVC" --ignore-not-found --wait=true
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
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ${CKM_TEST_VOLUME_INIT_PVC}
  namespace: ${CHART_NAMESPACE}
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: ${ROOTFS_STORAGE_CLASS}
  resources:
    requests:
      storage: 256Mi
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
      runtimeClassName: sysbox-runc-lite
      containers:
      - name: nginx
        image: ${TEST_IMAGE}
        ports: [{containerPort: 80}]
        volumeMounts:
        # A normal empty CSI PVC. No sysbox/volume-init annotation is present:
        # the runtime discovers the kubelet CSI mount from the OCI spec.
        - name: webroot
          mountPath: /usr/share/nginx/html
      volumes:
      - name: rootfs
        persistentVolumeClaim: {claimName: ${CKM_TEST_ROOTFS_PVC}}
      - name: webroot
        persistentVolumeClaim: {claimName: ${CKM_TEST_VOLUME_INIT_PVC}}
EOF
l1_kubectl -n "$CHART_NAMESPACE" rollout status "deployment/$CKM_TEST_DEPLOYMENT" --timeout=180s
pod="$(l1_kubectl -n "$CHART_NAMESPACE" get pod -l "app=$CKM_TEST_DEPLOYMENT" --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.name}')"
[ -n "$pod" ] || die 'sysbox-runc-lite workload Pod was not created'
[ "$(l1_kubectl -n "$CHART_NAMESPACE" get pod "$pod" -o jsonpath='{.spec.runtimeClassName}')" = sysbox-runc-lite ] || die 'workload runtime is not sysbox-runc-lite'
annotation="$(l1_kubectl -n "$CHART_NAMESPACE" get pod "$pod" -o jsonpath='{.metadata.annotations.sysbox\\/volume-init}')"
[ -z "$annotation" ] || die 'sysbox/volume-init annotation must not be present; runtime discovery must initialize the CSI PVC'
l1_rootfs_exec "$pod" nginx 'test -f /usr/share/nginx/html/index.html; test -f /usr/share/nginx/html/50x.html; mkdir -p /srv/data; echo volume-persisted > /usr/share/nginx/html/.sysbox-runc-lite-marker; echo rootfs-persisted > /srv/data/.sysbox-runc-lite-rootfs-marker'
l1_kubectl -n "$CHART_NAMESPACE" delete pod "$pod" --wait=true
l1_kubectl -n "$CHART_NAMESPACE" rollout status "deployment/$CKM_TEST_DEPLOYMENT" --timeout=180s
pod="$(l1_kubectl -n "$CHART_NAMESPACE" get pod -l "app=$CKM_TEST_DEPLOYMENT" --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.name}')"
l1_rootfs_exec "$pod" nginx 'test -f /usr/share/nginx/html/index.html; test -f /usr/share/nginx/html/50x.html; grep -qx volume-persisted /usr/share/nginx/html/.sysbox-runc-lite-marker; grep -qx rootfs-persisted /srv/data/.sysbox-runc-lite-rootfs-marker'
log 'FUNCTIONAL PASS: rootfs persistence, annotation-free CSI empty-volume init and special bind mount verified'
