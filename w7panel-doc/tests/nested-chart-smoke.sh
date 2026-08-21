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
inner_namespace="${INNER_NAMESPACE:-default}"
test_namespace="${TEST_NAMESPACE:-default}"
chart="${CHART:-$root_dir/charts/w7panel-sysbox}"
image_tag="${IMAGE_TAG:-v0.7.1-11-nested-proc-fallback}"
admission_enabled="${ADMISSION_ENABLED:-true}"
test_image="${TEST_IMAGE:-ccr.ccs.tencentyun.com/afan-public/nginx:latest}"
test_pod="${TEST_POD:-nested-chart-nginx}"
test_deployment="${TEST_DEPLOYMENT:-$test_pod}"
test_rootfs_pvc="${TEST_ROOTFS_PVC:-${test_deployment}-rootfs}"
rootfs_storage_class="${ROOTFS_STORAGE_CLASS:-local-path}"
keep_test_deployment="${KEEP_TEST_DEPLOYMENT:-true}"
test_pod_name=""
deployment_manifest=""

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
	[ -z "${deployment_manifest:-}" ] || rm -f "$deployment_manifest"
	if [ "$status" -ne 0 ] && [ "${KEEP_TEST_POD:-false}" != true ] && [ -n "${l1_pod:-}" ]; then
		inner_kubectl -n "$test_namespace" delete deployment "$test_deployment" --ignore-not-found \
			--wait=true --timeout=60s >/dev/null 2>&1 || true
		inner_kubectl -n "$test_namespace" delete pvc "${test_rootfs_pvc:-}" --ignore-not-found \
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
[ "$admission_enabled" = true ] || die 'rootfs-rw-layer persistence test requires admission.enabled=true'

after_ready_identity="$(k3s_identity)" || die 'L1 K3s disappeared after nested agent became Ready'
[ "$after_ready_identity" = "$before_identity" ] ||
	die "L1 K3s restarted by nested chart: $before_identity -> $after_ready_identity"
log "L1 K3s identity unchanged: $after_ready_identity"

handler="$(inner_kubectl get runtimeclass sysbox-runc -o jsonpath='{.handler}')"
[ "$handler" = sysbox-runc ] || die "unexpected RuntimeClass handler: $handler"
ready_nodes="$(inner_kubectl get nodes -l sysbox.w7panel.io/nested-runtime=ready \
	-o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')"
[ -n "$ready_nodes" ] || die 'no L1 node has nested-runtime=ready'

inner_kubectl -n "$test_namespace" delete deployment "$test_deployment" --ignore-not-found --wait=true >/dev/null
log "creating rootfs PVC and test Deployment in $test_namespace with $test_image"
deployment_manifest="$(mktemp)"
printf '%s\n' \
	'apiVersion: v1' \
	'kind: PersistentVolumeClaim' \
	'metadata:' \
	"  name: $test_rootfs_pvc" \
	"  namespace: $test_namespace" \
	'spec:' \
	'  accessModes:' \
	'  - ReadWriteOnce' \
	"  storageClassName: $rootfs_storage_class" \
	'  resources:' \
	'    requests:' \
	'      storage: 1Gi' \
	'---' \
	'apiVersion: apps/v1' \
	'kind: Deployment' \
	'metadata:' \
	"  name: $test_deployment" \
	"  namespace: $test_namespace" \
	'  labels:' \
	"    w7.cc/group-name: $test_deployment" \
	'spec:' \
	'  replicas: 1' \
	'  strategy:' \
	'    type: Recreate' \
	'  selector:' \
	'    matchLabels:' \
	"      app: $test_deployment" \
	"      w7.cc/group-name: $test_deployment" \
	'  template:' \
	'    metadata:' \
	'      labels:' \
	"        app: $test_deployment" \
	"        w7.cc/group-name: $test_deployment" \
	'      annotations:' \
	'        sysbox/allow-proc-exec: "true"' \
	'        sysbox/rootfs-rw-layer: '\''[{"name":"nginx","volumeName":"rootfs","path":"nginx"}]'\''' \
	'    spec:' \
	'      runtimeClassName: sysbox-runc' \
	'      enableServiceLinks: false' \
	'      containers:' \
	'      - name: nginx' \
	"        image: $test_image" \
	'        imagePullPolicy: IfNotPresent' \
	'        resources:' \
	'          limits:' \
	'            cpu: "1"' \
	'            memory: 2Gi' \
	'      volumes:' \
	'      - name: rootfs' \
	'        persistentVolumeClaim:' \
	"          claimName: $test_rootfs_pvc" > "$deployment_manifest"
outer_kubectl -n "$outer_namespace" exec -i "$l1_pod" -c "$l1_container" -- \
	/bin/kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml apply -f - < "$deployment_manifest"
inner_kubectl -n "$test_namespace" wait --for=jsonpath='{.status.phase}'=Bound \
	"pvc/$test_rootfs_pvc" --timeout=180s
inner_kubectl -n "$test_namespace" wait --for=condition=Available "deployment/$test_deployment" --timeout=180s
test_pod_name="$(inner_kubectl -n "$test_namespace" get pods -l "app=$test_deployment" \
	-o jsonpath='{.items[0].metadata.name}')"
[ -n "$test_pod_name" ] || die "no Pod was created for Deployment $test_deployment"
inner_kubectl -n "$test_namespace" wait --for=condition=Ready "pod/$test_pod_name" --timeout=60s

containers="$(inner_kubectl -n "$test_namespace" get pod "$test_pod_name" \
	-o jsonpath='{range .spec.containers[*]}{.name}{"\n"}{end}')"
printf '%s\n' "$containers" | grep -qx sysbox-rootfs || die 'rootfs-rw-layer sidecar was not injected'
uid_map="$(inner_kubectl -n "$test_namespace" exec "$test_pod_name" -c nginx -- cat /proc/self/uid_map)"
printf '%s\n' "$uid_map" | awk '
	NF == 3 && $1 == 0 && $2 == 0 && $3 == 65536 { valid = 1 }
	END { exit !valid }
' || die "unexpected L2 uid_map: $uid_map"

l1_userns="$(outer_kubectl -n "$outer_namespace" exec "$l1_pod" -c "$l1_container" -- readlink /proc/self/ns/user)"
l2_userns="$(inner_kubectl -n "$test_namespace" exec "$test_pod_name" -c nginx -- readlink /proc/self/ns/user)"
[ "$l1_userns" != "$l2_userns" ] || die "L2 reused the L1 user namespace: $l2_userns"

l2_lo="$(inner_kubectl -n "$test_namespace" exec "$test_pod_name" -c nginx -- cat /sys/class/net/lo/operstate)"
[ "$l2_lo" = unknown ] || [ "$l2_lo" = up ] || die "L2 loopback is not up: $l2_lo"

pod_ip="$(inner_kubectl -n "$test_namespace" get pod "$test_pod_name" -o jsonpath='{.status.podIP}')"
[ -n "$pod_ip" ] || die 'L2 Pod did not receive a CNI address'
image_id="$(inner_kubectl -n "$test_namespace" get pod "$test_pod_name" \
	-o jsonpath='{.status.containerStatuses[?(@.name=="nginx")].imageID}')"
[ -n "$image_id" ] || die 'L2 Pod did not report an image ID'
outer_kubectl -n "$outer_namespace" exec "$l1_pod" -c "$l1_container" -- \
	/bin/wget -qO- --timeout=10 "http://$pod_ip" | grep -q 'Welcome to nginx' ||
	die "L1 cannot reach nginx at $pod_ip"

root_mount="$(inner_kubectl -n "$test_namespace" exec "$test_pod_name" -c nginx -- \
	sh -c 'grep " / / " /proc/self/mountinfo')"
printf '%s\n' "$root_mount" | grep -q ' - fuse\.fuse-overlayfs ' ||
	die "nginx root is not PVC-backed fuse-overlayfs: $root_mount"
rootfs_marker=/rootfs-persistence-ckm-nginx
rootfs_value="ckm-nginx-$(date +%s)"
# The remote shell expands the positional parameters.
# shellcheck disable=SC2016
rootfs_before="$(inner_kubectl -n "$test_namespace" exec "$test_pod_name" -c nginx -- \
	sh -ec 'printf "%s\n" "$1" > "$2"; sync; stat -c "%i:%u:%g:%s" "$2"' \
	sh "$rootfs_value" "$rootfs_marker")"
old_pod_uid="$(inner_kubectl -n "$test_namespace" get pod "$test_pod_name" -o jsonpath='{.metadata.uid}')"
log "recreating nginx Pod $test_pod_name to verify rootfs-rw-layer"
inner_kubectl -n "$test_namespace" delete pod "$test_pod_name" --wait=true --timeout=120s >/dev/null
test_pod_name=""
for _ in $(seq 1 60); do
	test_pod_name="$(inner_kubectl -n "$test_namespace" get pods -l "app=$test_deployment" \
		-o jsonpath='{range .items[?(@.status.phase=="Running")]}{.metadata.name}{"\n"}{end}' | head -n1)"
	if [ -n "$test_pod_name" ] && inner_kubectl -n "$test_namespace" wait \
		--for=condition=Ready "pod/$test_pod_name" --timeout=5s >/dev/null 2>&1; then
		break
	fi
	sleep 2
done
[ -n "$test_pod_name" ] || die 'Deployment did not recreate the nginx Pod'
new_pod_uid="$(inner_kubectl -n "$test_namespace" get pod "$test_pod_name" -o jsonpath='{.metadata.uid}')"
[ "$new_pod_uid" != "$old_pod_uid" ] || die 'nginx Pod UID did not change during rootfs persistence test'
rootfs_after="$(inner_kubectl -n "$test_namespace" exec "$test_pod_name" -c nginx -- \
	stat -c '%i:%u:%g:%s' "$rootfs_marker")"
[ "$rootfs_after" = "$rootfs_before" ] ||
	die "rootfs marker metadata changed: before=$rootfs_before after=$rootfs_after"
persisted_value="$(inner_kubectl -n "$test_namespace" exec "$test_pod_name" -c nginx -- cat "$rootfs_marker")"
[ "$persisted_value" = "$rootfs_value" ] ||
	die "rootfs marker content changed: expected=$rootfs_value got=$persisted_value"
pod_ip="$(inner_kubectl -n "$test_namespace" get pod "$test_pod_name" -o jsonpath='{.status.podIP}')"
outer_kubectl -n "$outer_namespace" exec "$l1_pod" -c "$l1_container" -- \
	/bin/wget -qO- --timeout=10 "http://$pod_ip" | grep -q 'Welcome to nginx' ||
	die "L1 cannot reach recreated nginx at $pod_ip"
log "rootfs-rw-layer persisted across Pod UID $old_pod_uid -> $new_pod_uid: marker=$rootfs_marker stat=$rootfs_after"
inner_kubectl -n "$test_namespace" get deployment "$test_deployment" -o wide
inner_kubectl -n "$test_namespace" get pod "$test_pod_name" -o wide
log "nginx image ID: $image_id"

if [ "$keep_test_deployment" = true ]; then
	log "keeping Deployment/$test_deployment in $test_namespace for inspection"
else
	inner_kubectl -n "$test_namespace" delete deployment "$test_deployment" --wait=true --timeout=60s >/dev/null
	# The cleanup checks are intentionally expanded by the remote /bin/sh.
	# shellcheck disable=SC2016
	outer_kubectl -n "$outer_namespace" exec "$l1_pod" -c "$l1_container" -- /bin/sh -ec '
		[ -z "$(find /var/lib/cni/networks -type f -name "$1" -print -quit 2>/dev/null)" ]
		! iptables-save 2>/dev/null | grep -q -- "$1"
	' nested-chart-smoke "$pod_ip" || die "CNI resources for $pod_ip were not reclaimed"
	inner_kubectl -n "$test_namespace" delete pvc "$test_rootfs_pvc" --wait=true --timeout=60s >/dev/null
fi

log "FUNCTIONAL PASS: nested chart kept CKM K3s at $before_identity; Deployment=$test_deployment pod=$test_pod_name userns=$l2_userns uid_map=0:0:65536 podIP=$pod_ip rootfsPVC=$test_rootfs_pvc marker=$rootfs_marker; proc/view isolation is not supported"
