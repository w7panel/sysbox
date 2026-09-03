#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
discover_l1
[ "$(outer_kubectl -n "$OUTER_NAMESPACE" get pod "$L1_POD" -o jsonpath='{.spec.runtimeClassName}')" = "$OUTER_RUNTIME_CLASS" ] || die "CKM Pod is not using runtimeClass $OUTER_RUNTIME_CLASS"
[ "$(outer_kubectl -n "$OUTER_NAMESPACE" get pod "$L1_POD" -o jsonpath='{.spec.hostUsers}')" = false ] || die 'CKM Pod hostUsers is not false'
l1_kubectl create namespace "$CHART_NAMESPACE" --dry-run=client -o yaml | l1_kubectl_input apply -f - >/dev/null
manifest="$(mktemp)"
trap 'rm -f "$manifest"' EXIT
render_chart "$manifest" "$CHART_NAMESPACE"
log 'installing installMode=nested chart in the K3s owned by the configured CKM'
l1_kubectl_input apply -f - < "$manifest"
l1_kubectl get runtimeclass runc-lite -o jsonpath='{.handler}{"\n"}' | grep -qx runc-lite
RUNC_LITE_BINARY="${RUNC_LITE_BINARY:-$REPO_DIR/runc-lite/build/amd64/runc-lite}"
[ -x "$RUNC_LITE_BINARY" ] || die "runc-lite binary missing: $RUNC_LITE_BINARY (build it with make runc-lite)"
log 'installing runc-lite into the persistent CKM K3s data volume'
base64 -w0 "$RUNC_LITE_BINARY" | outer_kubectl -n "$OUTER_NAMESPACE" exec -i "$L1_POD" -c "$L1_CONTAINER" -- sh -c \
  'base64 -d > /var/lib/rancher/k3s/runc-lite && chmod 0755 /var/lib/rancher/k3s/runc-lite'
runtime_config='[plugins.'"'"'io.containerd.cri.v1.runtime'"'"'.containerd.runtimes.runc-lite]
  runtime_type = "io.containerd.runc.v2"
  sandboxer = "podsandbox"
  snapshotter = "sysbox"
  pod_annotations = ["sysbox/*"]
[plugins.'"'"'io.containerd.cri.v1.runtime'"'"'.containerd.runtimes.runc-lite.options]
  BinaryName = "/var/lib/rancher/k3s/runc-lite"
'
runtime_config_b64="$(printf '%s' "$runtime_config" | base64 -w0)"
l1_exec sh -c "mkdir -p /var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.d; printf '%s' '$runtime_config_b64' | base64 -d > /var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.d/runc-lite.toml"
l1_kubectl -n "$CHART_NAMESPACE" rollout status deployment/w7panel-sysbox-admission --timeout=180s
log 'runc-lite is persisted; restart the CKM server during the normal rollout to load the handler'
log 'PASS: chart installed in the existing CKM K3s; no second K3s was created'
