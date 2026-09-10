#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
for runtime_class in sysbox-runc sysbox-runc-lite; do
  pod="sysbox-${runtime_class}-smoke"
  log "testing L0 RuntimeClass ${runtime_class}"
  outer_kubectl get runtimeclass "$runtime_class" >/dev/null \
    || die "L0 RuntimeClass ${runtime_class} is not installed"
  # Some Sysbox cleanup paths can keep a completed smoke Pod in Terminating
  # while containerd finishes teardown. This test validates startup, so do
  # not make its result depend on that asynchronous cleanup.
  outer_kubectl -n default delete pod "$pod" --ignore-not-found --wait=false
  outer_kubectl -n default run "$pod" --image=busybox:1.36 --restart=Never \
    --overrides="{\"apiVersion\":\"v1\",\"spec\":{\"runtimeClassName\":\"${runtime_class}\",\"containers\":[{\"name\":\"${pod}\",\"image\":\"busybox:1.36\",\"command\":[\"/bin/sh\",\"-c\",\"sleep 300\"]}]}}"
  outer_kubectl -n default wait --for=condition=Ready "pod/${pod}" --timeout=120s
  actual="$(outer_kubectl -n default get pod "$pod" -o jsonpath='{.spec.runtimeClassName}')"
  [ "$actual" = "$runtime_class" ] || die "${pod} selected ${actual}, expected ${runtime_class}"
  fuse_mounts="$(outer_kubectl -n default get pod "$pod" -o jsonpath='{range .spec.containers[*].volumeMounts[*]}{.name}{"\n"}{end}')"
  ! grep -qx 'sysbox-fuse' <<<"$fuse_mounts" || die "${pod} unexpectedly received /dev/fuse"
  outer_kubectl -n default delete pod "$pod" --wait=false
done
log 'FUNCTIONAL PASS: L0 sysbox-runc and sysbox-runc-lite are runnable without automatic FUSE injection'
