#!/usr/bin/env bash
set -euo pipefail

# Fast, non-restarting smoke test for installing the Sysbox chart in an L1 K3s
# container. This script deliberately never rolls or restarts the L1 workload.

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
kubeconfig="${KUBECONFIG_218:-/root/.kube/218.config}"
outer_namespace="${NAMESPACE:-k3k-console-164315}"
l1_deployment="${DEPLOYMENT:-sysbox-inner-k3s-command-poc}"
l1_pod_override="${L1_POD:-}"
l1_container_override="${L1_CONTAINER:-}"
inner_namespace="${INNER_NAMESPACE:-sysbox-system}"
chart="${CHART:-$root_dir/charts/w7panel-sysbox}"
image_tag="${IMAGE_TAG:-v0.7.1-11-nested-proc-fallback}"
admission_enabled="${ADMISSION_ENABLED:-true}"
test_image="${TEST_IMAGE:-ccr.ccs.tencentyun.com/afan-public/nginx:latest}"
test_pod="${TEST_POD:-nested-chart-nginx}"

log() {
	printf '[nested-chart-smoke] %s\n' "$*"
}

die() {
	printf '[nested-chart-smoke] ERROR: %s\n' "$*" >&2
	exit 1
}

outer_kubectl() {
	kubectl --kubeconfig "$kubeconfig" "$@"
}

inner_kubectl() {
	outer_kubectl -n "$outer_namespace" exec "$l1_pod" -c "$l1_container" -- \
		/bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml "$@"
}

k3s_identity() {
	# The checks are intentionally expanded by the remote /bin/sh.
	# shellcheck disable=SC2016
	outer_kubectl -n "$outer_namespace" exec "$l1_pod" -c "$l1_container" -- /bin/sh -ec '
		for proc_dir in /proc/[0-9]*; do
			[ -r "$proc_dir/exe" ] && [ -r "$proc_dir/cmdline" ] || continue
			[ "${proc_dir##*/}" != "$$" ] || continue
			[ "$(basename "$(readlink "$proc_dir/exe" 2>/dev/null || true)")" = k3s ] || continue
			cmdline="$(tr "\000" " " <"$proc_dir/cmdline")"
			case "$cmdline" in
				*"k3s server"*)
					awk "{print \$1 \":\" \$22}" "$proc_dir/stat"
					exit 0
					;;
			esac
		done
		exit 1
	'
}

cleanup() {
	status=$?
	trap - EXIT INT TERM
	if [ "${KEEP_TEST_POD:-false}" != true ] && [ -n "${l1_pod:-}" ]; then
		inner_kubectl -n default delete pod "$test_pod" --ignore-not-found \
			--wait=true --timeout=60s >/dev/null 2>&1 || true
	fi
	exit "$status"
}
trap cleanup EXIT INT TERM

[ -r "$kubeconfig" ] || die "kubeconfig is not readable: $kubeconfig"
[ -r "$chart" ] || die "chart is not readable: $chart"
command -v kubectl >/dev/null || die 'kubectl is required'
command -v helm >/dev/null || die 'helm is required'

log "checking outer API through $kubeconfig"
outer_kubectl version --request-timeout=5s >/dev/null

	if [ -n "$l1_pod_override" ]; then
		l1_pod="$l1_pod_override"
	else
		l1_pod="$(outer_kubectl -n "$outer_namespace" get pods \
			-l "app=$l1_deployment" --field-selector=status.phase=Running \
			-o jsonpath='{.items[0].metadata.name}')"
	fi
[ -n "$l1_pod" ] || die "no Running L1 pod found for app=$l1_deployment"
log "L1 pod: $outer_namespace/$l1_pod"

if [ -n "$l1_container_override" ]; then
	l1_container="$l1_container_override"
else
	l1_container="$(outer_kubectl -n "$outer_namespace" get pod "$l1_pod" \
		-o jsonpath='{.spec.containers[0].name}')"
fi
[ -n "$l1_container" ] || die "no L1 container found in pod $l1_pod"

l1_runtime="$(outer_kubectl -n "$outer_namespace" get pod "$l1_pod" \
	-o jsonpath='{.spec.runtimeClassName}')"
l1_host_users="$(outer_kubectl -n "$outer_namespace" get pod "$l1_pod" \
	-o jsonpath='{.spec.hostUsers}')"
[ "$l1_runtime" = sysbox-runc ] || die "L1 runtimeClassName is $l1_runtime"
[ "$l1_host_users" = false ] || die "L1 hostUsers is $l1_host_users"

before_identity="$(k3s_identity)" || die 'cannot locate the L1 K3s process'
log "L1 K3s identity before chart apply: $before_identity"

inner_kubectl get namespace "$inner_namespace" >/dev/null 2>&1 ||
	inner_kubectl create namespace "$inner_namespace" >/dev/null

log "rendering and applying installMode=nested admission.enabled=$admission_enabled (this does not restart K3s)"
helm template w7panel-sysbox "$chart" \
	--namespace "$inner_namespace" \
	--set installMode=nested \
	--set admission.enabled="$admission_enabled" \
	--set installer.image.digest="" \
	--set-string installer.image.tag="$image_tag" |
	outer_kubectl -n "$outer_namespace" exec -i "$l1_pod" -c "$l1_container" -- \
		/bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml apply -f -

after_apply_identity="$(k3s_identity)" || die 'L1 K3s disappeared after chart apply'
[ "$after_apply_identity" = "$before_identity" ] ||
	die "L1 K3s restarted during chart apply: $before_identity -> $after_apply_identity"

if ! inner_kubectl -n "$inner_namespace" rollout status \
	daemonset/w7panel-sysbox-nested-agent --timeout=120s; then
	after_wait_identity="$(k3s_identity)" || die 'L1 K3s disappeared while waiting for nested agent'
	[ "$after_wait_identity" = "$before_identity" ] ||
		die "L1 K3s restarted while waiting for nested agent: $before_identity -> $after_wait_identity"
	inner_kubectl -n "$inner_namespace" get pods -o wide || true
	inner_kubectl -n "$inner_namespace" logs daemonset/w7panel-sysbox-nested-agent \
		--tail=100 || true
	die 'nested agent is not Ready; if this is the first handler migration, perform one controlled L1 Pod rollout from L0'
fi

if [ "$admission_enabled" = true ]; then
	log 'waiting for the inner admission webhook endpoint'
	if ! inner_kubectl -n "$inner_namespace" rollout status \
		deployment/w7panel-sysbox-admission --timeout=120s; then
		inner_kubectl -n "$inner_namespace" get deployment,pods,endpoints -o wide || true
		inner_kubectl -n "$inner_namespace" logs deployment/w7panel-sysbox-admission \
			--tail=100 || true
		die 'inner admission webhook is not Ready'
	fi
fi

after_ready_identity="$(k3s_identity)" || die 'L1 K3s disappeared after nested agent became Ready'
[ "$after_ready_identity" = "$before_identity" ] ||
	die "L1 K3s restarted by nested chart: $before_identity -> $after_ready_identity"
log "L1 K3s identity unchanged: $after_ready_identity"

handler="$(inner_kubectl get runtimeclass sysbox-runc -o jsonpath='{.handler}')"
[ "$handler" = sysbox-runc ] || die "unexpected RuntimeClass handler: $handler"
ready_nodes="$(inner_kubectl get nodes -l sysbox.w7panel.io/nested-runtime=ready \
	-o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')"
[ -n "$ready_nodes" ] || die 'no L1 node has nested-runtime=ready'

inner_kubectl -n default delete pod "$test_pod" --ignore-not-found --wait=true >/dev/null
log "creating L2 test Pod with $test_image"
inner_kubectl -n default run "$test_pod" --image="$test_image" --restart=Never \
	--overrides='{"metadata":{"annotations":{"sysbox/allow-proc-exec":"true"}},"spec":{"runtimeClassName":"sysbox-runc","enableServiceLinks":false}}'
inner_kubectl -n default wait --for=condition=Ready "pod/$test_pod" --timeout=180s

uid_map="$(inner_kubectl -n default exec "$test_pod" -- cat /proc/self/uid_map)"
printf '%s\n' "$uid_map" | awk '
	NF == 3 && $1 == 0 && $2 == 0 && $3 == 65536 { valid = 1 }
	END { exit !valid }
' || die "unexpected L2 uid_map: $uid_map"

l1_userns="$(outer_kubectl -n "$outer_namespace" exec "$l1_pod" -c "$l1_container" -- readlink /proc/self/ns/user)"
l2_userns="$(inner_kubectl -n default exec "$test_pod" -- readlink /proc/self/ns/user)"
[ "$l1_userns" != "$l2_userns" ] || die "L2 reused the L1 user namespace: $l2_userns"

l2_lo="$(inner_kubectl -n default exec "$test_pod" -- cat /sys/class/net/lo/operstate)"
[ "$l2_lo" = unknown ] || [ "$l2_lo" = up ] || die "L2 loopback is not up: $l2_lo"

pod_ip="$(inner_kubectl -n default get pod "$test_pod" -o jsonpath='{.status.podIP}')"
[ -n "$pod_ip" ] || die 'L2 Pod did not receive a CNI address'
image_id="$(inner_kubectl -n default get pod "$test_pod" \
	-o jsonpath='{.status.containerStatuses[0].imageID}')"
[ -n "$image_id" ] || die 'L2 Pod did not report an image ID'
outer_kubectl -n "$outer_namespace" exec "$l1_pod" -c "$l1_container" -- \
	/bin/wget -qO- --timeout=10 "http://$pod_ip" | grep -q 'Welcome to nginx' ||
	die "L1 cannot reach nginx at $pod_ip"
inner_kubectl -n default get pod "$test_pod" -o wide
log "nginx image ID: $image_id"

inner_kubectl -n default delete pod "$test_pod" --wait=true --timeout=60s >/dev/null
# The cleanup checks are intentionally expanded by the remote /bin/sh.
# shellcheck disable=SC2016
outer_kubectl -n "$outer_namespace" exec "$l1_pod" -c "$l1_container" -- /bin/sh -ec '
	[ -z "$(find /var/lib/cni/networks -type f -name "$1" -print -quit 2>/dev/null)" ]
	! iptables-save 2>/dev/null | grep -q -- "$1"
' nested-chart-smoke "$pod_ip" || die "CNI resources for $pod_ip were not reclaimed"

log "PASS: nested chart kept L1 K3s at $before_identity; L2 userns=$l2_userns uid_map=0:0:65536 podIP=$pod_ip; HTTP and CNI cleanup passed"
