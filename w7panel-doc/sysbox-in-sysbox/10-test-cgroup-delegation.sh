#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
need_cmd jq
discover_l1

deployment="$CKM_DOCKER_TEST_DEPLOYMENT"
namespace="$CHART_NAMESPACE"
pod="$(l1_kubectl -n "$namespace" get pod -l "app=$deployment" \
  -o jsonpath='{.items[0].metadata.name}')"
[ -n "$pod" ] || die "run 09-test-docker-rootfs.sh first; Deployment/$deployment has no Pod"
l1_kubectl -n "$namespace" wait --for=condition=Ready "pod/$pod" --timeout=120s

runtime_endpoint=unix:///run/k3s/containerd/containerd.sock
pod_id="$(l1_exec /bin/crictl --runtime-endpoint "$runtime_endpoint" pods --name "$pod" -q | head -n1)"
[ -n "$pod_id" ] || die "cannot find CRI sandbox for $pod"
container_id="$(l1_exec /bin/crictl --runtime-endpoint "$runtime_endpoint" \
  ps --pod "$pod_id" --name system -q | head -n1)"
[ -n "$container_id" ] || die "cannot find system container for $pod"
pid="$(l1_exec /bin/crictl --runtime-endpoint "$runtime_endpoint" inspect "$container_id" | jq -r '.info.pid')"
case "$pid" in
  ''|null|*[!0-9]*) die "invalid L1-visible container PID: $pid" ;;
esac

cgroup_path="$(l1_exec cat "/proc/$pid/cgroup" | awk -F: '$1 == 0 { print $3 }')"
case "$cgroup_path" in
  */sysbox.delegate/init.scope) ;;
  *) die "container is outside delegated sysbox cgroup: $cgroup_path" ;;
esac
container_boundary="${cgroup_path%/sysbox.delegate/init.scope}"
memory_max="$(l1_exec cat "/sys/fs/cgroup${container_boundary}/memory.max")"
cpu_max="$(l1_exec cat "/sys/fs/cgroup${container_boundary}/cpu.max")"
[ "$memory_max" = "$CKM_DOCKER_MEMORY_MAX" ] ||
  die "unexpected L1 memory boundary: $memory_max"
[ "$cpu_max" = "$CKM_DOCKER_CPU_MAX" ] ||
  die "unexpected L1 CPU boundary: $cpu_max"

l1_cgroupns="$(l1_exec readlink /proc/self/ns/cgroup)"
l2_cgroupns="$(l1_kubectl -n "$namespace" exec "$pod" -c system -- \
  readlink /proc/self/ns/cgroup)"
[ "$l1_cgroupns" != "$l2_cgroupns" ] || die "L1 and L2 share cgroup namespace $l1_cgroupns"

# TEST_IMAGE and the cgroup paths are evaluated by the remote shell.
# shellcheck disable=SC2016
l1_kubectl -n "$namespace" exec "$pod" -c system -- \
  env TEST_IMAGE="$TEST_IMAGE" /bin/sh -ec '
    test ! -e /sys/fs/cgroup/../memory.max
    grep -qw cpu /sys/fs/cgroup/cgroup.controllers
    grep -qw memory /sys/fs/cgroup/cgroup.controllers
    grep -qw cpu /sys/fs/cgroup/cgroup.subtree_control
    grep -qw memory /sys/fs/cgroup/cgroup.subtree_control
    systemctl is-active --quiet docker
    docker run --rm "$TEST_IMAGE" sh -ec "test -r /sys/fs/cgroup/memory.max; test -r /sys/fs/cgroup/cpu.max"
  '

memory_after="$(l1_exec cat "/sys/fs/cgroup${container_boundary}/memory.max")"
cpu_after="$(l1_exec cat "/sys/fs/cgroup${container_boundary}/cpu.max")"
[ "$memory_after" = "$memory_max" ] || die 'L2 changed its L1 memory boundary'
[ "$cpu_after" = "$cpu_max" ] || die 'L2 changed its L1 CPU boundary'

log "PASS: cgroup namespace $l2_cgroupns is delegated at $cgroup_path; L1 boundary remains memory.max=$memory_after cpu.max=$cpu_after"
