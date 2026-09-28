#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
discover_l1
[ "$(outer_kubectl -n "$OUTER_NAMESPACE" get pod "$L1_POD" -o jsonpath='{.spec.runtimeClassName}')" = "$OUTER_RUNTIME_CLASS" ] || die "CKM Pod is not using runtimeClass $OUTER_RUNTIME_CLASS"
[ "$(outer_kubectl -n "$OUTER_NAMESPACE" get pod "$L1_POD" -o jsonpath='{.spec.hostUsers}')" = false ] || die 'CKM Pod hostUsers is not false'
helm_binary="${HELM_BINARY:-$(command -v helm)}"
[ -x "$helm_binary" ] || die "Helm binary is not executable: $helm_binary"
chart_file="${NESTED_CHART_FILE:-}"
package_dir=""
if [ -z "$chart_file" ]; then
  package_dir="$(mktemp -d)"
  helm package "$REPO_DIR/charts/w7panel-sysbox" --destination "$package_dir" >/dev/null
  chart_file="$(find "$package_dir" -maxdepth 1 -name 'w7panel-sysbox-*.tgz' -print -quit)"
fi
[ -r "$chart_file" ] || die "nested chart is not readable: $chart_file"
trap '[ -z "$package_dir" ] || rm -rf "$package_dir"' EXIT
l1_copy_file "$helm_binary" /tmp
l1_copy_file "$chart_file" /tmp
l1_exec chmod 0755 "/tmp/$(basename "$helm_binary")"
log 'installing a real Helm release in the K3s owned by the configured CKM'
l1_exec "/tmp/$(basename "$helm_binary")" --kubeconfig /etc/rancher/k3s/k3s.yaml \
  upgrade --install w7panel-sysbox "/tmp/$(basename "$chart_file")" \
  --namespace "$CHART_NAMESPACE" --create-namespace \
  --set installMode=nested --set runtimeClassName=sysbox-runc-lite \
  --set installer.enabled=false \
  --set admission.enabled=true \
  --set admission.image.repository="$SYSBOX_IMAGE_REPO" \
  --set-string admission.image.tag="$SYSBOX_IMAGE_TAG" \
  --set admission.image.digest="" \
  --set snapshotter.enabled=true --wait --timeout 5m
l1_exec "/tmp/$(basename "$helm_binary")" --kubeconfig /etc/rancher/k3s/k3s.yaml \
  --namespace "$CHART_NAMESPACE" status w7panel-sysbox >/dev/null
l1_kubectl get runtimeclass sysbox-runc-lite -o jsonpath='{.handler}{"\n"}' | grep -qx sysbox-runc-lite
l1_exec sh -ec 'test -x /opt/sysbox/bin/generic/sysbox-runc-lite; grep -q "runtimes.sysbox-runc-lite]" /var/lib/rancher/k3s/agent/etc/containerd/config.toml; ! grep -q "runtimes.sysbox-runc]" /var/lib/rancher/k3s/agent/etc/containerd/config.toml; test ! -e /opt/sysbox/bin/generic/sysbox-runc-nested'
l1_kubectl -n "$CHART_NAMESPACE" rollout status deployment/w7panel-sysbox-admission --timeout=180s
log 'PASS: Helm release installed in the existing CKM K3s; bootstrap provides only native runc and sysbox-runc-lite'
