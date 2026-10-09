#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
discover_l1
helm_binary="${HELM_BINARY:-$(command -v helm)}"
l1_copy_file "$helm_binary" /tmp
l1_exec chmod 0755 "/tmp/$(basename "$helm_binary")"
log 'uninstalling the nested Chart and restoring the pre-Chart containerd template'
l1_exec "/tmp/$(basename "$helm_binary")" --kubeconfig /etc/rancher/k3s/k3s.yaml \
  --namespace "$CHART_NAMESPACE" uninstall w7panel-sysbox --timeout 5m
l1_exec test -f /var/lib/rancher/k3s/sysbox-inner/restart-required ||
  die 'Chart uninstall did not record the required K3s restart'
log 'restarting the CKM Server after Helm uninstall completed'
outer_kubectl -n "$OUTER_NAMESPACE" delete pod "$L1_POD" --wait=true --timeout=180s
outer_kubectl -n "$OUTER_NAMESPACE" rollout status "deployment/k3k-${CKM_NAME}-server" --timeout=300s
L1_POD=""
discover_l1
l1_exec /bin/sh -ec '
  ! grep -Fq "runtimes.sysbox-runc-lite]" /var/lib/rancher/k3s/agent/etc/containerd/config.toml
  test ! -d /var/lib/rancher/k3s/sysbox-inner/releases
  rm -f /var/lib/rancher/k3s/sysbox-inner/restart-required
'
log 'PASS: nested Chart removed, runtime payload cleaned, and K3s restarted'
