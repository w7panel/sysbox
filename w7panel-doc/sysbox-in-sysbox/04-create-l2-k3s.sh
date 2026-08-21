#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
discover_l1
l1_kubectl get runtimeclass sysbox-runc -o jsonpath='{.handler}{"\n"}' | grep -qx sysbox-runc || die 'L1 RuntimeClass/sysbox-runc is not ready'
log "creating L2 K3s Pod $L2_POD with rootfs-rw-layer PVC $L2_PVC"
l1_kubectl apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ${L2_PVC}
  namespace: ${L2_NAMESPACE}
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: local-path
  resources:
    requests:
      storage: 5Gi
---
apiVersion: v1
kind: Pod
metadata:
  name: ${L2_POD}
  namespace: ${L2_NAMESPACE}
  annotations:
    sysbox/allow-proc-exec: "true"
    sysbox/rootfs-rw-layer: '[{"name":"k3s","volumeName":"rootfs","path":"rootfs","persistentSpecialMounts":true}]'
spec:
  runtimeClassName: sysbox-runc
  enableServiceLinks: false
  terminationGracePeriodSeconds: 30
  containers:
    - name: k3s
      image: ${NESTED_K3S_IMAGE}
      imagePullPolicy: Always
      securityContext:
        seccompProfile:
          type: Unconfined
      command: [/bin/sh, -ec]
      args: ["exec /usr/local/bin/nested-k3s-entrypoint.sh"]
      volumeMounts:
        - name: fuse
          mountPath: /dev/fuse
  volumes:
    - name: fuse
      hostPath:
        path: /dev/fuse
        type: CharDevice
    - name: rootfs
      persistentVolumeClaim:
        claimName: ${L2_PVC}
EOF
for _ in $(seq 1 60); do
  phase="$(l1_kubectl -n "$L2_NAMESPACE" get pod "$L2_POD" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
  printf '  L2 phase=%s\n' "${phase:-<pending>}"
  [ "$phase" = Running ] && break
  sleep 5
done
[ "$(l1_kubectl -n "$L2_NAMESPACE" get pod "$L2_POD" -o jsonpath='{.status.phase}')" = Running ] || {
  l1_kubectl -n "$L2_NAMESPACE" describe pod "$L2_POD" || true
  die 'L2 K3s Pod did not become Running'
}
l1_kubectl -n "$L2_NAMESPACE" get pod "$L2_POD" -o wide
for _ in $(seq 1 60); do
  if l2_kubectl get nodes >/dev/null 2>&1; then
    log 'L2 Kubernetes API is accepting requests'
    break
  fi
  sleep 5
done
l2_kubectl get nodes
log 'PASS: L2 K3s Pod and persistent rootfs layer are ready'
