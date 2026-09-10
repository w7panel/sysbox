#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
suffix="${L0_TEST_SUFFIX:-$(date +%s)}"
runtime_class="${L0_RUNTIME_CLASS:-sysbox-runc-lite}"
host_users="${L0_HOST_USERS:-}"
# Full sysbox-runc needs Kubernetes to create the Pod user namespace before
# its CNI namespace. This aligns CAP_NET_BIND_SERVICE with the network
# namespace so the stock nginx image can bind port 80. L2 runc-lite workloads
# deliberately do not set hostUsers.
if [[ -z "$host_users" && "$runtime_class" == "sysbox-runc" ]]; then
  host_users=false
fi
host_users_yaml=""
if [[ -n "$host_users" ]]; then
  host_users_yaml="      hostUsers: ${host_users}"
fi
hold_command=""
if [[ "${L0_HOLD_COMMAND:-false}" == "true" ]]; then
  hold_command='        command: ["sleep", "3600"]'
fi
name="sysbox-l0-rootfs-${suffix}"
rootfs="${name}-rootfs"
webroot="${name}-webroot"

outer_kubectl -n default apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: ${rootfs}}
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: ${ROOTFS_STORAGE_CLASS}
  resources: {requests: {storage: 2Gi}}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: ${webroot}}
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: ${ROOTFS_STORAGE_CLASS}
  resources: {requests: {storage: 256Mi}}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: ${name}}
spec:
  replicas: 1
  selector: {matchLabels: {app: ${name}}}
  template:
    metadata:
      labels: {app: ${name}}
      annotations:
        sysbox/rootfs-rw-layer: '[{"name":"nginx","volumeName":"rootfs","path":"nginx","persistentSpecialMounts":true,"specialPath":["/srv/data"]}]'
    spec:
      runtimeClassName: ${runtime_class}
${host_users_yaml}
      containers:
      - name: nginx
        image: ${TEST_IMAGE}
${hold_command}
        volumeMounts: [{name: webroot, mountPath: /usr/share/nginx/html}]
      volumes:
      - name: rootfs
        persistentVolumeClaim: {claimName: ${rootfs}}
      - name: webroot
        persistentVolumeClaim: {claimName: ${webroot}}
EOF
outer_kubectl -n default rollout status "deployment/${name}" --timeout=180s
pod="$(outer_kubectl -n default get pod -l "app=${name}" -o jsonpath='{.items[0].metadata.name}')"
[ -n "$pod" ] || die 'L0 workload Pod was not created'
annotation="$(outer_kubectl -n default get pod "$pod" -o jsonpath='{.metadata.annotations.sysbox\/volume-init}')"
[ -z "$annotation" ] || die 'sysbox/volume-init annotation must not be present'
outer_kubectl -n default exec "$pod" -c nginx -- sh -ec '
  test -f /usr/share/nginx/html/index.html
  test -f /usr/share/nginx/html/50x.html
  if [ "$(cat /proc/1/comm)" = nginx ]; then
    grep -Eq ":0050[[:space:]]" /proc/net/tcp /proc/net/tcp6
  fi
  mkdir -p /srv/data
  echo volume-persisted > /usr/share/nginx/html/.sysbox-l0-volume-marker
  echo rootfs-persisted > /srv/data/.sysbox-l0-rootfs-marker'
outer_kubectl -n default delete pod "$pod" --wait=false
outer_kubectl -n default rollout status "deployment/${name}" --timeout=180s
pod="$(outer_kubectl -n default get pod -l "app=${name}" -o jsonpath='{.items[0].metadata.name}')"
outer_kubectl -n default exec "$pod" -c nginx -- sh -ec '
  test -f /usr/share/nginx/html/index.html
  test -f /usr/share/nginx/html/50x.html
  if [ "$(cat /proc/1/comm)" = nginx ]; then
    grep -Eq ":0050[[:space:]]" /proc/net/tcp /proc/net/tcp6
  fi
  grep -qx volume-persisted /usr/share/nginx/html/.sysbox-l0-volume-marker
  grep -qx rootfs-persisted /srv/data/.sysbox-l0-rootfs-marker'
log "FUNCTIONAL PASS: L0 ${runtime_class} hostUsers=${host_users:-default} rootfs persistence, annotation-free ${ROOTFS_STORAGE_CLASS} PVC initialization and special bind mount verified (${name})"
