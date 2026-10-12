#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
for runtime_class in sysbox-runc sysbox-runc-lite; do
  pod="sysbox-${runtime_class}-smoke"
  host_users_json=''
  [ "$runtime_class" != sysbox-runc ] || host_users_json=',"hostUsers":false'
  log "testing L0 RuntimeClass ${runtime_class}"
  outer_kubectl get runtimeclass "$runtime_class" >/dev/null \
    || die "L0 RuntimeClass ${runtime_class} is not installed"
  # Some Sysbox cleanup paths can keep a completed smoke Pod in Terminating
  # while containerd finishes teardown. This test validates startup, so do
  # not make its result depend on that asynchronous cleanup.
  outer_kubectl -n default delete pod "$pod" --ignore-not-found --wait=false
  outer_kubectl -n default run "$pod" --image=busybox:1.36 --restart=Never \
    --overrides="{\"apiVersion\":\"v1\",\"spec\":{\"runtimeClassName\":\"${runtime_class}\"${host_users_json},\"containers\":[{\"name\":\"${pod}\",\"image\":\"busybox:1.36\",\"command\":[\"/bin/sh\",\"-c\",\"sleep 300\"]}]}}"
  outer_kubectl -n default wait --for=condition=Ready "pod/${pod}" --timeout=120s
  actual="$(outer_kubectl -n default get pod "$pod" -o jsonpath='{.spec.runtimeClassName}')"
  [ "$actual" = "$runtime_class" ] || die "${pod} selected ${actual}, expected ${runtime_class}"
  fuse_mounts="$(outer_kubectl -n default get pod "$pod" -o jsonpath='{range .spec.containers[*].volumeMounts[*]}{.mountPath}{"\n"}{end}')"
  fuse_volumes="$(outer_kubectl -n default get pod "$pod" -o jsonpath='{range .spec.volumes[*]}{.hostPath.path}{"\n"}{end}')"
  ! grep -qx '/dev/fuse' <<<"$fuse_mounts" || die "${pod} unexpectedly declares a /dev/fuse mount"
  ! grep -qx '/dev/fuse' <<<"$fuse_volumes" || die "${pod} unexpectedly declares a /dev/fuse volume"
  if [ "$runtime_class" = sysbox-runc ]; then
    outer_kubectl -n default exec "$pod" -- sh -c 'test -c /dev/fuse && exec 3<>/dev/fuse' \
      || die "${pod} cannot open runtime-provided /dev/fuse"
  else
    outer_kubectl -n default exec "$pod" -- sh -c 'test ! -e /dev/fuse' \
      || die "${pod} unexpectedly has /dev/fuse"
  fi
  outer_kubectl -n default delete pod "$pod" --wait=false
done
log 'FUNCTIONAL PASS: L0 sysbox-runc provides /dev/fuse; sysbox-runc-lite does not'
