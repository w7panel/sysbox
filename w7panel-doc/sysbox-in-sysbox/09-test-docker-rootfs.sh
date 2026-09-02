#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
discover_l1

if ! l1_exec test -x /var/lib/rancher/k3s/runc-lite && ! l1_exec test -x /usr/local/bin/runc-lite; then
  die 'runc-lite is not installed in the CKM server image/data volume; install it before running the Docker workload test'
fi
l1_exec sh -c '(/var/lib/rancher/k3s/runc-lite features 2>/dev/null || /usr/local/bin/runc-lite features 2>/dev/null) | grep -q '"'"'"user_namespaces": true'"'"'' \
  || die 'runc-lite does not advertise linux.user_namespaces=true'

deployment="$CKM_DOCKER_TEST_DEPLOYMENT"
pvc="$CKM_DOCKER_TEST_ROOTFS_PVC"
namespace="$CHART_NAMESPACE"
manifest="$(mktemp)"
trap 'rm -f "$manifest"' EXIT

printf '%s\n' \
  'apiVersion: v1' \
  'kind: PersistentVolumeClaim' \
  'metadata:' \
  "  name: $pvc" \
  "  namespace: $namespace" \
  '  labels:' \
  "    w7.cc/group-name: $deployment" \
  'spec:' \
  '  accessModes:' \
  '  - ReadWriteOnce' \
  "  storageClassName: $ROOTFS_STORAGE_CLASS" \
  '  resources:' \
  '    requests:' \
  '      storage: 2Gi' \
  '---' \
  'apiVersion: apps/v1' \
  'kind: Deployment' \
  'metadata:' \
  "  name: $deployment" \
  "  namespace: $namespace" \
  '  labels:' \
  "    w7.cc/group-name: $deployment" \
  'spec:' \
  '  replicas: 1' \
  '  strategy:' \
  '    type: Recreate' \
  '  selector:' \
  '    matchLabels:' \
  "      app: $deployment" \
  '  template:' \
  '    metadata:' \
  '      labels:' \
  "        app: $deployment" \
  "        w7.cc/group-name: $deployment" \
  '      annotations:' \
  '        sysbox/rootfs-rw-layer: '\''[{"name":"system","volumeName":"rootfs","path":"system","persistentSpecialMounts":true}]'\''' \
  '    spec:' \
  '      runtimeClassName: runc-lite' \
  '      tolerations:' \
  '      - key: sysbox-runtime' \
  '        operator: Exists' \
  '        effect: NoSchedule' \
  '      enableServiceLinks: false' \
  '      terminationGracePeriodSeconds: 0' \
  '      containers:' \
  '      - name: system' \
  "        image: $CKM_DOCKER_TEST_IMAGE" \
  '        imagePullPolicy: IfNotPresent' \
  '        volumeMounts:' \
  '        - name: rootfs' \
  '          mountPath: /var/lib/docker' \
  '        command:' \
  '        - /sbin/init' \
  '        resources:' \
  '          limits:' \
  '            cpu: "1"' \
  '            memory: 2Gi' \
  '      volumes:' \
  '      - name: rootfs' \
  '        persistentVolumeClaim:' \
  "          claimName: $pvc" > "$manifest"

log "applying Docker persistence workload in $namespace"
l1_kubectl_input -n "$namespace" apply -f - < "$manifest"
l1_kubectl -n "$namespace" wait --for=jsonpath='{.status.phase}'=Bound "pvc/$pvc" --timeout=180s
l1_kubectl -n "$namespace" rollout status "deployment/$deployment" --timeout=300s

pod="$(l1_kubectl -n "$namespace" get pod -l "app=$deployment" \
  -o jsonpath='{.items[0].metadata.name}')"
[ -n "$pod" ] || die "no Pod found for Deployment/$deployment"
l1_kubectl -n "$namespace" wait --for=condition=Ready "pod/$pod" --timeout=120s

for _ in $(seq 1 60); do
  if l1_kubectl -n "$namespace" exec "$pod" -c system -- \
    /bin/sh -c 'docker info >/dev/null 2>&1'; then
    break
  fi
  sleep 2
done
l1_kubectl -n "$namespace" exec "$pod" -c system -- \
  /bin/sh -c 'docker info >/dev/null 2>&1' || die 'dockerd did not become ready'

uid_map="$(l1_kubectl -n "$namespace" exec "$pod" -c system -- cat /proc/self/uid_map)"
printf '%s\n' "$uid_map" | awk '
  NF == 3 && $1 == 0 && $2 == 0 && $3 == 65536 { valid = 1 }
  END { exit !valid }
' || die "unexpected uid_map: $uid_map"

driver="$(l1_kubectl -n "$namespace" exec "$pod" -c system -- \
  docker info --format '{{.Driver}}')"
[ "$driver" = overlay2 ] || die "Docker storage driver is $driver, expected overlay2"
special_mount="$(l1_kubectl -n "$namespace" exec "$pod" -c system -- \
  findmnt -n -o FSTYPE,OPTIONS -T /var/lib/docker)"
printf '%s\n' "$special_mount" | grep -Eq '^ext4 .*idmapped' ||
  die "/var/lib/docker is not an ext4 idmapped special mount: $special_mount"

marker_value="sysbox-overlay2-$(date +%s)"
# TEST_IMAGE and MARKER_VALUE are expanded by the remote shell.
# shellcheck disable=SC2016
l1_kubectl -n "$namespace" exec "$pod" -c system -- \
  env TEST_IMAGE="$TEST_IMAGE" MARKER_VALUE="$marker_value" /bin/sh -ec '
    docker pull "$TEST_IMAGE"
    docker run --rm "$TEST_IMAGE" nginx -t
    rm -rf /tmp/sysbox-build-test
    mkdir -p /tmp/sysbox-build-test
    printf "%s\n" "FROM $TEST_IMAGE" \
      "RUN echo sysbox-build-ok > /sysbox-build-marker" \
      > /tmp/sysbox-build-test/Dockerfile
    docker build -t sysbox-nested-build:test /tmp/sysbox-build-test
    test "$(docker run --rm sysbox-nested-build:test cat /sysbox-build-marker)" = sysbox-build-ok
    printf "%s\n" "$MARKER_VALUE" > /root/sysbox-docker-persistence-marker
    sync
  '

marker_stat_before="$(l1_kubectl -n "$namespace" exec "$pod" -c system -- \
  stat -c '%i:%u:%g:%s' /root/sysbox-docker-persistence-marker)"
image_id_before="$(l1_kubectl -n "$namespace" exec "$pod" -c system -- \
  docker image inspect sysbox-nested-build:test --format '{{.Id}}')"
old_uid="$(l1_kubectl -n "$namespace" get pod "$pod" -o jsonpath='{.metadata.uid}')"

log "recreating Docker Pod $pod"
l1_kubectl -n "$namespace" delete pod "$pod" --wait=true --timeout=120s >/dev/null
l1_kubectl -n "$namespace" rollout status "deployment/$deployment" --timeout=300s
pod="$(l1_kubectl -n "$namespace" get pod -l "app=$deployment" \
  -o jsonpath='{.items[0].metadata.name}')"
[ -n "$pod" ] || die "Deployment/$deployment did not recreate its Pod"
l1_kubectl -n "$namespace" wait --for=condition=Ready "pod/$pod" --timeout=120s
new_uid="$(l1_kubectl -n "$namespace" get pod "$pod" -o jsonpath='{.metadata.uid}')"
[ "$new_uid" != "$old_uid" ] || die 'Docker Pod UID did not change'

for _ in $(seq 1 60); do
  if l1_kubectl -n "$namespace" exec "$pod" -c system -- \
    /bin/sh -c 'docker info >/dev/null 2>&1'; then
    break
  fi
  sleep 2
done
driver="$(l1_kubectl -n "$namespace" exec "$pod" -c system -- \
  docker info --format '{{.Driver}}')"
[ "$driver" = overlay2 ] || die "Docker storage driver after recreation is $driver"
marker_value_after="$(l1_kubectl -n "$namespace" exec "$pod" -c system -- \
  cat /root/sysbox-docker-persistence-marker)"
[ "$marker_value_after" = "$marker_value" ] ||
  die "rootfs marker changed: expected=$marker_value got=$marker_value_after"
marker_stat_after="$(l1_kubectl -n "$namespace" exec "$pod" -c system -- \
  stat -c '%i:%u:%g:%s' /root/sysbox-docker-persistence-marker)"
[ "$marker_stat_after" = "$marker_stat_before" ] ||
  die "rootfs marker metadata changed: before=$marker_stat_before after=$marker_stat_after"
image_id_after="$(l1_kubectl -n "$namespace" exec "$pod" -c system -- \
  docker image inspect sysbox-nested-build:test --format '{{.Id}}')"
[ "$image_id_after" = "$image_id_before" ] ||
  die "built image changed: before=$image_id_before after=$image_id_after"
# The command substitution is expanded by the remote shell.
# shellcheck disable=SC2016
l1_kubectl -n "$namespace" exec "$pod" -c system -- \
  /bin/sh -ec 'test "$(docker run --rm sysbox-nested-build:test cat /sysbox-build-marker)" = sysbox-build-ok'

log "PASS: Docker uses overlay2 on $special_mount; Pod UID $old_uid -> $new_uid; rootfs marker and image $image_id_after persisted"
