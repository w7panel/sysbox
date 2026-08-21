#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC1091
source "$(dirname "$0")/_common.sh"

check_common
discover_l1

deployment="${CKM_TEST_DEPLOYMENT:-ckm-k3s-nginx}"
pod="$(l1_kubectl -n "$CHART_NAMESPACE" get pods \
	-l "app=$deployment" \
	-o jsonpath='{range .items[?(@.status.phase=="Running")]}{.metadata.name}{"\n"}{end}' | head -n1)"
[ -n "$pod" ] || die "no Running Pod found for Deployment/$deployment"

proc_mount="$(l1_kubectl -n "$CHART_NAMESPACE" exec "$pod" -- \
	sh -c 'grep " /proc " /proc/self/mountinfo | head -n1')"
l1_pidns="$(l1_exec readlink /proc/self/ns/pid)"
l2_pidns="$(l1_kubectl -n "$CHART_NAMESPACE" exec "$pod" -- readlink /proc/self/ns/pid)"
# The awk program must be passed literally to the remote process.
# shellcheck disable=SC2016
mem_total_kb="$(l1_kubectl -n "$CHART_NAMESPACE" exec "$pod" -- \
	awk '$1 == "MemTotal:" { print $2 }' /proc/meminfo)"
cpu_count="$(l1_kubectl -n "$CHART_NAMESPACE" exec "$pod" -- \
	sh -c 'grep -c "^processor" /proc/cpuinfo')"

failed=false
case "$proc_mount" in
	*noexec*) ;;
	*)
		log "FAIL: /proc is executable: $proc_mount"
		failed=true
		;;
esac
if [ "$l1_pidns" = "$l2_pidns" ]; then
	log "FAIL: L1 and L2 share PID namespace $l1_pidns"
	failed=true
else
	log "PASS: PID namespaces differ: L1=$l1_pidns L2=$l2_pidns"
fi
if [ "$mem_total_kb" -gt 2097152 ]; then
	log "FAIL: /proc/meminfo exposes ${mem_total_kb}KiB, expected at most 2097152KiB"
	failed=true
else
	log "PASS: /proc/meminfo reflects the 2GiB limit: ${mem_total_kb}KiB"
fi
if [ "$cpu_count" -ne 1 ]; then
	log "FAIL: /proc/cpuinfo exposes $cpu_count CPUs, expected 1"
	failed=true
else
	log 'PASS: /proc/cpuinfo reflects the 1 CPU limit'
fi

[ "$failed" = false ] || die 'proc/view isolation is not supported; functional Sysbox-in-Sysbox checks are unaffected'
log 'PASS: mandatory /proc and Sysbox view isolation checks passed'
