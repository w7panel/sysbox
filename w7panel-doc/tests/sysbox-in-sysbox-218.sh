#!/usr/bin/env bash
set -euo pipefail

# Validate the inner Sysbox RuntimeClass in the 218 command-mode PoC.
namespace="${NAMESPACE:-k3k-console-164315}"
deployment="${DEPLOYMENT:-sysbox-inner-k3s-command-poc}"
kubeconfig="${KUBECONFIG_218:-$HOME/.kube/218.config}"
k3s_image="rancher/k3s:v1.35.6-k3s1"
pod="nested-sysbox-hostusers"

outer_kubectl() {
  docker run --rm --network host -v "$kubeconfig:/kubeconfig:ro" \
    --entrypoint /bin/kubectl "$k3s_image" --kubeconfig /kubeconfig "$@"
}

inner_kubectl() {
  outer_kubectl -n "$namespace" exec "$outer_pod" -c k3s -- \
    /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml "$@"
}

outer_pod="$(outer_kubectl -n "$namespace" get pods -l "app=$deployment" \
  --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.name}')"
[ -n "$outer_pod" ] || { echo "no running outer pod for $deployment" >&2; exit 1; }

for _ in $(seq 1 36); do
  ready="$(inner_kubectl get nodes -o jsonpath='{.items[0].status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)"
  service_account="$(inner_kubectl -n default get serviceaccount default -o name 2>/dev/null || true)"
  [ "$ready" = True ] && [ "$service_account" = serviceaccount/default ] && break
  sleep 5
done
[ "$ready" = True ] || { echo "inner node did not become Ready" >&2; exit 1; }
[ "$service_account" = serviceaccount/default ] || { echo "inner default service account was not created" >&2; exit 1; }
inner_kubectl get nodes
outer_kubectl -n "$namespace" exec "$outer_pod" -c k3s -- \
  /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml delete runtimeclass sysbox-runc-inner --ignore-not-found
outer_kubectl -n "$namespace" exec "$outer_pod" -c k3s -- /bin/sh -c \
  'printf "%s" "{\"apiVersion\":\"node.k8s.io/v1\",\"kind\":\"RuntimeClass\",\"metadata\":{\"name\":\"sysbox-runc-inner\"},\"handler\":\"sysbox-runc-inner\"}" | /bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml apply -f -'
inner_kubectl -n default delete pod "$pod" --ignore-not-found
inner_kubectl -n default run "$pod" \
  --image=docker.cnb.cool/i0358/zpk/nested-pause:20260810-1 --restart=Never \
  --overrides='{"metadata":{"annotations":{"sysbox/allow-proc-exec":"true"}},"spec":{"runtimeClassName":"sysbox-runc-inner"}}'
for _ in $(seq 1 12); do
  phase="$(inner_kubectl -n default get pod "$pod" -o jsonpath='{.status.phase}')"
  [ "$phase" = Running ] && break
  sleep 5
done
inner_kubectl -n default get pod "$pod" -o wide
inner_kubectl -n default get events --sort-by=.lastTimestamp | tail -20
phase="$(inner_kubectl -n default get pod "$pod" -o jsonpath='{.status.phase}')"
[ "$phase" = Running ]
runtime_class="$(inner_kubectl -n default get pod "$pod" -o jsonpath='{.spec.runtimeClassName}')"
[ "$runtime_class" = sysbox-runc-inner ] || {
  echo "unexpected runtimeClassName: $runtime_class" >&2
  exit 1
}
uid_map="$(inner_kubectl -n default exec "$pod" -- cat /proc/self/uid_map)"
printf '%s\n' "$uid_map"
case "$uid_map" in
  *" 0 0 65536"*) ;;
  *) echo "unexpected user namespace mapping: $uid_map" >&2; exit 1 ;;
esac
inner_kubectl -n default exec "$pod" -- /bin/sh -c 'free -h; top -b -n 1 | head -12; ps -ef'
