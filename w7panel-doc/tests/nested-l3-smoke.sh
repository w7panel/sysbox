#!/usr/bin/env bash
set -euo pipefail

# Validate a real L3 Sysbox pod through L0 -> L1 K3s -> L2 K3s. Do not use a
# plain kubectl command here: it targets L0 and is not evidence for L3.

kubeconfig="${KUBECONFIG_218:-/root/.kube/218.config}"
outer_namespace="${NAMESPACE:-k3k-console-164315}"
l1_deployment="${DEPLOYMENT:-sysbox-inner-k3s-rootfs-poc}"
l2_pod="${L2_POD:-nested-l2-k3s-final}"
test_pod="${TEST_POD:-nested-l3-chart-nginx}"
test_image="${TEST_IMAGE:-ccr.ccs.tencentyun.com/afan-public/nginx:latest}"

log() {
	printf '[nested-l3-smoke] %s\n' "$*"
}

die() {
	printf '[nested-l3-smoke] ERROR: %s\n' "$*" >&2
	exit 1
}

outer_kubectl() {
	kubectl --kubeconfig "$kubeconfig" "$@"
}

l1_kubectl() {
	outer_kubectl -n "$outer_namespace" exec "$l1_pod" -c k3s -- \
		/bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml "$@"
}

l2_kubectl() {
	l1_kubectl -n default exec "$l2_pod" -c k3s -- \
		/bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml "$@"
}

cleanup() {
	status=$?
	trap - EXIT INT TERM
	if [ "${KEEP_TEST_POD:-false}" != true ] && [ -n "${l1_pod:-}" ]; then
		l2_kubectl -n default delete pod "$test_pod" --ignore-not-found --wait=true --timeout=60s >/dev/null 2>&1 || true
	fi
	exit "$status"
}
trap cleanup EXIT INT TERM

[ -r "$kubeconfig" ] || die "kubeconfig is not readable: $kubeconfig"

l1_pod="$(outer_kubectl -n "$outer_namespace" get pods \
	-l "app=$l1_deployment" --field-selector=status.phase=Running \
	-o jsonpath='{.items[0].metadata.name}')"
[ -n "$l1_pod" ] || die "no Running L1 pod found for app=$l1_deployment"

l1_kubectl -n default get pod "$l2_pod" >/dev/null || die "L2 pod is not available: $l2_pod"
l2_kubectl get runtimeclass sysbox-runc -o jsonpath='{.handler}' | grep -qx sysbox-runc ||
	die 'L2 RuntimeClass handler is not sysbox-runc'

l2_kubectl -n default delete pod "$test_pod" --ignore-not-found --wait=true >/dev/null
log "creating L3 Sysbox Pod with $test_image"
l2_kubectl -n default run "$test_pod" --image="$test_image" --restart=Never \
	--overrides='{"metadata":{"annotations":{"sysbox/allow-proc-exec":"true"}},"spec":{"runtimeClassName":"sysbox-runc","enableServiceLinks":false}}'
l2_kubectl -n default wait --for=condition=Ready "pod/$test_pod" --timeout=180s

uid_map="$(l2_kubectl -n default exec "$test_pod" -- cat /proc/self/uid_map)"
gid_map="$(l2_kubectl -n default exec "$test_pod" -- cat /proc/self/gid_map)"
printf '%s\n' "$uid_map" | awk 'NF == 3 && $1 == 0 && $2 == 0 && $3 == 65536 { ok = 1 } END { exit !ok }' ||
	die "unexpected L3 uid_map: $uid_map"
printf '%s\n' "$gid_map" | awk 'NF == 3 && $1 == 0 && $2 == 0 && $3 == 65536 { ok = 1 } END { exit !ok }' ||
	die "unexpected L3 gid_map: $gid_map"

l2_userns="$(l1_kubectl -n default exec "$l2_pod" -c k3s -- readlink /proc/self/ns/user)"
l3_userns="$(l2_kubectl -n default exec "$test_pod" -- readlink /proc/self/ns/user)"
[ "$l2_userns" != "$l3_userns" ] || die "L3 reused the L2 user namespace: $l3_userns"

pod_ip="$(l2_kubectl -n default get pod "$test_pod" -o jsonpath='{.status.podIP}')"
[ -n "$pod_ip" ] || die 'L3 Pod did not receive a CNI address'
l1_kubectl -n default exec "$l2_pod" -c k3s -- /bin/wget -qO- --timeout=10 "http://$pod_ip" |
	grep -q 'Welcome to nginx' || die "L2 cannot reach nginx at $pod_ip"

l1_kubectl -n default exec "$l2_pod" -c k3s -- /bin/sh -ec '
	test -f "/var/lib/cni/networks/sysbox-nested/$1"
	ip link show cni3 >/dev/null
' nested-l3-smoke "$pod_ip" || die "L3 CNI state is missing for $pod_ip"

image_id="$(l2_kubectl -n default get pod "$test_pod" -o jsonpath='{.status.containerStatuses[0].imageID}')"
l2_kubectl -n default delete pod "$test_pod" --wait=true --timeout=60s >/dev/null
l1_kubectl -n default exec "$l2_pod" -c k3s -- /bin/sh -ec '
	[ -z "$(find /var/lib/cni/networks -type f -name "$1" -print -quit 2>/dev/null)" ]
	! iptables-save 2>/dev/null | grep -q -- "$1"
' nested-l3-smoke "$pod_ip" || die "L3 CNI resources were not reclaimed for $pod_ip"

log "PASS: L3 userns=$l3_userns uid_map=0:0:65536 podIP=$pod_ip imageID=$image_id; HTTP and CNI cleanup passed"
