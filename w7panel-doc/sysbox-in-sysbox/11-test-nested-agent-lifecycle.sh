#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
discover_l1

agent_selector='app.kubernetes.io/component=nested-agent,app.kubernetes.io/instance=w7panel-sysbox'

k3s_identity() {
  # shellcheck disable=SC2016
  l1_exec /bin/sh -ec '
    for proc_dir in /proc/[0-9]*; do
      [ -r "$proc_dir/exe" ] && [ -r "$proc_dir/cmdline" ] || continue
      [ "$(basename "$(readlink "$proc_dir/exe" 2>/dev/null || true)")" = k3s ] || continue
      case "$(tr "\000" " " 2>/dev/null < "$proc_dir/cmdline" || true)" in
        *"k3s server"*) awk "{print \$1 \":\" \$22}" "$proc_dir/stat"; exit 0 ;;
      esac
    done
    exit 1
  '
}

check_lifecycle_processes() {
  # Match the executable command line as well as the keepalive environment so
  # exec probes inheriting the Pod environment are not counted as launchers.
  # shellcheck disable=SC2016
  l1_exec /bin/sh -ec '
    launcher_count=0
    snapshotter_count=0
    for proc_dir in /proc/[0-9]*; do
      cmdline="$(tr "\000" " " 2>/dev/null < "$proc_dir/cmdline" || true)"
      case "$cmdline" in
        "/bin/sh /opt/sysbox/scripts/sysbox-inner-k3s.sh "*)
          if tr "\000" "\n" < "$proc_dir/environ" 2>/dev/null | grep -qx SYSBOX_INNER_KEEPALIVE=true; then
            launcher_count=$((launcher_count + 1))
          fi
          ;;
        "/var/lib/sysbox-inner/bin/sysbox-snapshotter "*)
          snapshotter_count=$((snapshotter_count + 1))
          ;;
      esac
    done
    [ "$launcher_count" -eq 1 ]
    [ "$snapshotter_count" -eq 1 ]
    awk '\''$4 == "00010000" && $6 == "01" && $8 == "/run/sysbox/sysbox-snapshotter.sock" { listening = 1 } END { exit !listening }'\'' /proc/net/unix
    printf "launcher=%s snapshotter=%s socket=listening\n" "$launcher_count" "$snapshotter_count"
  '
}

before_identity="$(k3s_identity)" || die 'cannot locate CKM K3s server process'
agent_pod="$(l1_kubectl -n "$CHART_NAMESPACE" get pod -l "$agent_selector" \
  -o jsonpath='{.items[0].metadata.name}')"
[ -n "$agent_pod" ] || die 'nested-agent Pod was not found'
old_uid="$(l1_kubectl -n "$CHART_NAMESPACE" get pod "$agent_pod" -o jsonpath='{.metadata.uid}')"
check_lifecycle_processes

log "recreating nested-agent Pod $agent_pod"
l1_kubectl -n "$CHART_NAMESPACE" delete pod "$agent_pod" --wait=true --timeout=120s >/dev/null
l1_kubectl -n "$CHART_NAMESPACE" rollout status \
  daemonset/w7panel-sysbox-nested-agent --timeout=300s
agent_pod="$(l1_kubectl -n "$CHART_NAMESPACE" get pod -l "$agent_selector" \
  -o jsonpath='{.items[0].metadata.name}')"
[ -n "$agent_pod" ] || die 'nested-agent Pod was not recreated'
l1_kubectl -n "$CHART_NAMESPACE" wait --for=condition=Ready "pod/$agent_pod" --timeout=120s
new_uid="$(l1_kubectl -n "$CHART_NAMESPACE" get pod "$agent_pod" -o jsonpath='{.metadata.uid}')"
[ "$new_uid" != "$old_uid" ] || die 'nested-agent Pod UID did not change'

for _ in $(seq 1 30); do
  if check_lifecycle_processes >/dev/null 2>&1; then
    break
  fi
  sleep 2
done
process_state="$(check_lifecycle_processes)" || die 'nested launcher or snapshotter lifecycle is unhealthy'
after_identity="$(k3s_identity)" || die 'CKM K3s disappeared during nested-agent recreation'
[ "$after_identity" = "$before_identity" ] ||
  die "CKM K3s restarted: $before_identity -> $after_identity"
l1_kubectl get node -l sysbox.w7panel.io/nested-runtime=ready >/dev/null

workload="$(l1_kubectl -n "$CHART_NAMESPACE" get pod -l "app=$CKM_TEST_DEPLOYMENT" \
  -o jsonpath='{.items[0].metadata.name}')"
[ -n "$workload" ] || die "run 05-test-ckm-k3s.sh first; no $CKM_TEST_DEPLOYMENT Pod exists"
workload_uid="$(l1_kubectl -n "$CHART_NAMESPACE" get pod "$workload" -o jsonpath='{.metadata.uid}')"
l1_kubectl -n "$CHART_NAMESPACE" delete pod "$workload" --wait=true --timeout=120s >/dev/null
l1_kubectl -n "$CHART_NAMESPACE" rollout status "deployment/$CKM_TEST_DEPLOYMENT" --timeout=300s
workload="$(l1_kubectl -n "$CHART_NAMESPACE" get pod -l "app=$CKM_TEST_DEPLOYMENT" \
  -o jsonpath='{.items[0].metadata.name}')"
l1_kubectl -n "$CHART_NAMESPACE" wait --for=condition=Ready "pod/$workload" --timeout=120s
new_workload_uid="$(l1_kubectl -n "$CHART_NAMESPACE" get pod "$workload" -o jsonpath='{.metadata.uid}')"
[ "$new_workload_uid" != "$workload_uid" ] || die 'Sysbox workload Pod was not recreated'
pod_ip="$(l1_kubectl -n "$CHART_NAMESPACE" get pod "$workload" -o jsonpath='{.status.podIP}')"
workload_reachable=false
for _ in $(seq 1 30); do
  if l1_exec /bin/wget -qO- --timeout=3 "http://$pod_ip" 2>/dev/null |
    grep -q 'Welcome to nginx'; then
    workload_reachable=true
    break
  fi
  sleep 2
done
[ "$workload_reachable" = true ] ||
  die "recreated Sysbox workload is unreachable at $pod_ip after 60 seconds"

log "PASS: nested-agent UID $old_uid -> $new_uid; CKM K3s stayed $before_identity; $process_state; new Sysbox workload is Ready"
