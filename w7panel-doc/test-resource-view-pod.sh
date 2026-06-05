#!/usr/bin/env bash
#
# Validate sysbox-fs resource-view files inside a Kubernetes Sysbox test pod.
#
# Usage:
#   ./w7panel-doc/test-resource-view-pod.sh
#   POD_NAME=test-sysbox KUBECTL="kubectl" ./w7panel-doc/test-resource-view-pod.sh
#   INSTALL_TOOLS=1 ./w7panel-doc/test-resource-view-pod.sh
#
# The script expects the pod to already exist. Use w7panel-doc/test-pod.sh to
# create test-sysbox when needed.

set -euo pipefail

POD_NAME="${POD_NAME:-test-sysbox}"
KUBECTL="${KUBECTL:-k3s kubectl}"
INSTALL_TOOLS="${INSTALL_TOOLS:-0}"

TARGET_PATHS=(
	/proc/cpuinfo
	/proc/diskstats
	/proc/meminfo
	/proc/stat
	/proc/swaps
	/proc/uptime
	/proc/slabinfo
	/proc/pressure/io
	/proc/pressure/cpu
	/proc/pressure/memory
	/sys/devices/system/cpu/online
	/proc/loadavg
	/sys/devices/system/cpu/present
	/sys/devices/system/cpu/online
)

log() {
	printf '\n==> %s\n' "$*"
}

need_cmd() {
	command -v "$1" >/dev/null 2>&1 || {
		echo "missing required command on host: $1" >&2
		exit 1
	}
}

pod_exec() {
	${KUBECTL} exec "${POD_NAME}" -- "$@"
}

ensure_pod_ready() {
	log "Checking pod ${POD_NAME}"
	${KUBECTL} get pod "${POD_NAME}" -o wide
	${KUBECTL} wait --for=condition=Ready "pod/${POD_NAME}" --timeout=60s
}

install_tools() {
	if [[ "${INSTALL_TOOLS}" != "1" ]]; then
		return
	fi

	log "Installing optional tools in pod"
	pod_exec sh -c '
set -e
if command -v apt-get >/dev/null 2>&1; then
  apt-get update || true
  DEBIAN_FRONTEND=noninteractive apt-get install -y procps coreutils gawk grep sed stress || true
fi
'
}

run_validation() {
	log "Running resource-view validation"

	local script
	script="$(cat <<'POD_SCRIPT'
set -u

fail=0

ok() {
	printf 'PASS %s\n' "$1"
}

bad() {
	printf 'FAIL %s: %s\n' "$1" "$2"
	fail=1
}

check_eq() {
	name="$1"
	got="$2"
	want="$3"
	[ "$got" = "$want" ] && ok "$name" || bad "$name" "$got != $want"
}

check_readable() {
	p="$1"
	[ -r "$p" ] && ok "$p readable" || bad "$p readable" "not readable"
}

check_printable() {
	p="$1"
	badhex="$(dd if="$p" bs=4096 count=1 2>/dev/null | od -An -v -tx1 | awk '{
		for (i = 1; i <= NF; i++) {
			h = $i
			if (h != "09" && h != "0a" && (h < "20" || h > "7e")) {
				print h
				exit
			}
		}
	}')"
	[ -z "$badhex" ] && ok "$p printable-bytes" || bad "$p printable-bytes" "bad byte $badhex"
}

count_range() {
	awk -v s="$1" 'BEGIN {
		n = 0
		split(s, a, ",")
		for (i in a) {
			if (a[i] ~ /-/) {
				split(a[i], b, "-")
				n += b[2] - b[1] + 1
			} else if (a[i] != "") {
				n++
			}
		}
		print n
	}'
}

paths="/proc/cpuinfo /proc/diskstats /proc/meminfo /proc/stat /proc/swaps /proc/uptime /proc/slabinfo /proc/pressure/io /proc/pressure/cpu /proc/pressure/memory /sys/devices/system/cpu/online /proc/loadavg /sys/devices/system/cpu/present /sys/devices/system/cpu/online"

echo "--- mountinfo ---"
grep -E '(/proc/(cpuinfo|diskstats|meminfo|stat|swaps|uptime|slabinfo|loadavg|pressure/(io|cpu|memory))|/sys/devices/system/cpu/(online|present))' /proc/self/mountinfo || true

for p in $paths; do
	check_readable "$p"
	check_printable "$p"
done

online="$(tr -d "\n" < /sys/devices/system/cpu/online)"
present="$(tr -d "\n" < /sys/devices/system/cpu/present)"
online2="$(tr -d "\n" < /sys/devices/system/cpu/online)"
visible="$(count_range "$online")"
printf 'VALUE online=%s present=%s online2=%s visible_cpus=%s\n' "$online" "$present" "$online2" "$visible"

echo "$online" | grep -Eq '^[0-9]+(-[0-9]+)?(,[0-9]+(-[0-9]+)?)*$' && ok "cpu online format" || bad "cpu online format" "$online"
echo "$present" | grep -Eq '^[0-9]+(-[0-9]+)?(,[0-9]+(-[0-9]+)?)*$' && ok "cpu present format" || bad "cpu present format" "$present"
[ "$online" = "$online2" ] && ok "duplicate online consistent" || bad "duplicate online consistent" "$online != $online2"
[ "$online" = "$present" ] && ok "online present aligned" || bad "online present aligned" "$online != $present"

cpuinfo_count="$(grep -c '^processor' /proc/cpuinfo || true)"
cpuinfo_ids="$(awk -F: '/^processor/ {gsub(/[ \t]/, "", $2); print $2}' /proc/cpuinfo | paste -sd, -)"
seq_ok="$(awk -F: 'BEGIN{ok=1;n=0} /^processor/ {gsub(/[ \t]/, "", $2); if ($2 != n) ok=0; n++} END{exit ok?0:1}' /proc/cpuinfo; echo $?)"
printf 'VALUE cpuinfo_processors=%s cpuinfo_ids=%s nproc=%s\n' "$cpuinfo_count" "$cpuinfo_ids" "$(nproc 2>/dev/null || echo unknown)"
[ "$cpuinfo_count" -eq "$visible" ] && ok "cpuinfo count matches online" || bad "cpuinfo count matches online" "$cpuinfo_count != $visible"
[ "$seq_ok" -eq 0 ] && ok "cpuinfo processor ids sequential" || bad "cpuinfo processor ids sequential" "$cpuinfo_ids"

first_stat="$(head -1 /proc/stat)"
stat_cpu_count="$(grep -Ec '^cpu[0-9]+ ' /proc/stat || true)"
printf 'VALUE stat_first=%s\n' "$first_stat"
echo "$first_stat" | grep -Eq '^cpu[[:space:]]+[0-9]' && ok "proc stat aggregate line" || bad "proc stat aggregate line" "$first_stat"
[ "$stat_cpu_count" -eq "$visible" ] && ok "proc stat cpu count matches online" || bad "proc stat cpu count matches online" "$stat_cpu_count != $visible"
awk '/^btime [0-9]+$/ {found=1} END{exit found?0:1}' /proc/stat && ok "proc stat btime" || bad "proc stat btime" "missing/invalid"

grep -Eq '^[0-9]+\.[0-9]{2} [0-9]+\.[0-9]{2}$' /proc/uptime && ok "uptime format" || bad "uptime format" "$(cat /proc/uptime)"
grep -Eq '^[0-9]+\.[0-9]{2} [0-9]+\.[0-9]{2} [0-9]+\.[0-9]{2} [0-9]+/[0-9]+ [0-9]+$' /proc/loadavg && ok "loadavg format" || bad "loadavg format" "$(cat /proc/loadavg)"

top -b -n1 >/tmp/top.out 2>/tmp/top.err && ok "top parses proc files" || bad "top parses proc files" "$(cat /tmp/top.err)"
grep -q 'load average:' /tmp/top.out && ok "top loadavg line" || bad "top loadavg line" "missing"
top_load="$(awk -F'load average: ' 'NR == 1 && NF > 1 {gsub(/,/, "", $2); print $2}' /tmp/top.out)"
proc_load="$(awk '{print $1, $2, $3}' /proc/loadavg)"
awk -v top="$top_load" -v proc="$proc_load" 'BEGIN {
	split(top, t, " ")
	split(proc, p, " ")
	for (i = 1; i <= 3; i++) {
		d = t[i] - p[i]
		if (d < 0) {
			d = -d
		}
		if (d > 0.05) {
			exit 1
		}
	}
}' && ok "top loadavg matches proc loadavg" || bad "top loadavg matches proc loadavg" "$top_load != $proc_load"
free -m >/tmp/free.out 2>/tmp/free.err && ok "free parses meminfo" || bad "free parses meminfo" "$(cat /tmp/free.err)"
awk '/^Mem:/ && $2 == 512 {found=1} END{exit found?0:1}' /tmp/free.out && ok "free sees 512Mi limit" || bad "free sees 512Mi limit" "$(sed -n '1,3p' /tmp/free.out)"

set -- $(free -b | awk 'NR==2 {print $2, $3, $4, $5, $6, $7}')
free_mem_total="$1"
free_mem_used="$2"
free_mem_free="$3"
free_mem_shared="$4"
free_mem_buff_cache="$5"
free_mem_available="$6"
set -- $(free -b | awk 'NR==3 {print $2, $3, $4}')
free_swap_total="$1"
free_swap_used="$2"
free_swap_free="$3"

mem_total_kb="$(awk '/^MemTotal:/ {print $2}' /proc/meminfo)"
mem_free_kb="$(awk '/^MemFree:/ {print $2}' /proc/meminfo)"
mem_available_kb="$(awk '/^MemAvailable:/ {print $2}' /proc/meminfo)"
buffers_kb="$(awk '/^Buffers:/ {print $2}' /proc/meminfo)"
cached_kb="$(awk '/^Cached:/ {print $2}' /proc/meminfo)"
sreclaimable_kb="$(awk '/^SReclaimable:/ {print $2}' /proc/meminfo)"
shmem_kb="$(awk '/^Shmem:/ {print $2}' /proc/meminfo)"
swap_total_kb="$(awk '/^SwapTotal:/ {print $2}' /proc/meminfo)"
swap_free_kb="$(awk '/^SwapFree:/ {print $2}' /proc/meminfo)"

expected_mem_total=$((mem_total_kb * 1024))
expected_mem_free=$((mem_free_kb * 1024))
expected_mem_shared=$((shmem_kb * 1024))
expected_mem_buff_cache=$(((buffers_kb + cached_kb + sreclaimable_kb) * 1024))
expected_mem_used=$((expected_mem_total - expected_mem_free - expected_mem_buff_cache))
expected_mem_available=$((mem_available_kb * 1024))
expected_swap_total=$((swap_total_kb * 1024))
expected_swap_free=$((swap_free_kb * 1024))
expected_swap_used=$((expected_swap_total - expected_swap_free))

check_eq "free mem total" "$free_mem_total" "$expected_mem_total"
check_eq "free mem used" "$free_mem_used" "$expected_mem_used"
check_eq "free mem free" "$free_mem_free" "$expected_mem_free"
check_eq "free mem shared" "$free_mem_shared" "$expected_mem_shared"
check_eq "free mem buff/cache" "$free_mem_buff_cache" "$expected_mem_buff_cache"
check_eq "free mem available" "$free_mem_available" "$expected_mem_available"
check_eq "free swap total" "$free_swap_total" "$expected_swap_total"
check_eq "free swap used" "$free_swap_used" "$expected_swap_used"
check_eq "free swap free" "$free_swap_free" "$expected_swap_free"

for key in MemTotal MemFree MemAvailable Cached Active Inactive 'Active(anon)' 'Inactive(anon)' 'Active(file)' 'Inactive(file)' AnonPages Slab KernelStack PageTables SwapTotal SwapFree; do
	grep -q "^$key:" /proc/meminfo && ok "meminfo $key" || bad "meminfo $key" "missing"
done
mem_total="$(awk '/^MemTotal:/ {print $2}' /proc/meminfo)"
mem_free="$(awk '/^MemFree:/ {print $2}' /proc/meminfo)"
mem_avail="$(awk '/^MemAvailable:/ {print $2}' /proc/meminfo)"
[ "$mem_total" -eq 524288 ] && ok "meminfo total limit" || bad "meminfo total limit" "$mem_total"
[ "$mem_free" -le "$mem_total" ] && ok "meminfo free <= total" || bad "meminfo free <= total" "$mem_free > $mem_total"
[ "$mem_avail" -le "$mem_total" ] && ok "meminfo available <= total" || bad "meminfo available <= total" "$mem_avail > $mem_total"

head -1 /proc/swaps | grep -Eq '^Filename[[:space:]]+Type[[:space:]]+Size[[:space:]]+Used[[:space:]]+Priority$' && ok "swaps header" || bad "swaps header" "$(head -1 /proc/swaps)"
awk 'NR>1 {if (NF < 5) bad=1} END{exit bad?1:0}' /proc/swaps && ok "swaps rows format" || bad "swaps rows format" "invalid row"
swap_rows="$(awk 'NR > 1 {n++} END {print n + 0}' /proc/swaps)"
swap_row_total_kb="$(awk 'NR == 2 {print $3 + 0}' /proc/swaps)"
swap_row_used_kb="$(awk 'NR == 2 {print $4 + 0}' /proc/swaps)"
swap_max=""
swap_current=""
swap_source="missing"
if [ -r /sys/fs/cgroup/memory.swap.max ]; then
	swap_source="cgroup-v2"
	cg_path="$(awk -F: '$1 == "0" {print $3}' /proc/self/cgroup)"
	cg_path="${cg_path#/}"
	while :; do
		swap_max_file="/sys/fs/cgroup/${cg_path:+$cg_path/}memory.swap.max"
		if [ -r "$swap_max_file" ]; then
			value="$(cat "$swap_max_file")"
			if [ -n "$value" ] && [ "$value" != "max" ]; then
				swap_max="$value"
				break
			fi
		fi
		[ -z "$cg_path" ] && break
		case "$cg_path" in
			*/*) cg_path="${cg_path%/*}" ;;
			*) cg_path="" ;;
		esac
	done
	if [ -z "$swap_max" ]; then
		swap_max="max"
	fi
	swap_current="$(cat /sys/fs/cgroup/memory.swap.current 2>/dev/null || echo 0)"
elif [ -r /sys/fs/cgroup/memory/memory.memsw.limit_in_bytes ] && [ -r /sys/fs/cgroup/memory/memory.limit_in_bytes ]; then
	swap_source="cgroup-v1"
	mem_limit_bytes="$(cat /sys/fs/cgroup/memory/memory.limit_in_bytes)"
	memsw_limit_bytes="$(cat /sys/fs/cgroup/memory/memory.memsw.limit_in_bytes)"
	mem_usage_bytes="$(cat /sys/fs/cgroup/memory/memory.usage_in_bytes 2>/dev/null || echo 0)"
	memsw_usage_bytes="$(cat /sys/fs/cgroup/memory/memory.memsw.usage_in_bytes 2>/dev/null || echo 0)"
	if [ "$memsw_limit_bytes" -ge 4611686018427387903 ] 2>/dev/null; then
		swap_max=0
		swap_current=0
	elif [ "$memsw_limit_bytes" -gt "$mem_limit_bytes" ] 2>/dev/null; then
		swap_max="$memsw_limit_bytes"
		if [ "$memsw_usage_bytes" -gt "$mem_usage_bytes" ] 2>/dev/null; then
			swap_current=$((memsw_usage_bytes - mem_usage_bytes))
		else
			swap_current=0
		fi
	else
		swap_max=0
		swap_current=0
	fi
fi
printf 'VALUE swap_source=%s swap_max=%s swap_current=%s swap_rows=%s swap_total_kb=%s swap_free_kb=%s\n' "$swap_source" "${swap_max:-<missing>}" "${swap_current:-<missing>}" "$swap_rows" "$swap_total_kb" "$swap_free_kb"
case "$swap_max" in
	""|"max"|"0")
		[ "$swap_rows" -eq 0 ] && ok "swaps hides unlimited/default rows" || bad "swaps hides unlimited/default rows" "rows=$swap_rows"
		[ "$swap_total_kb" -eq 0 ] && ok "meminfo hides unlimited/default swap total" || bad "meminfo hides unlimited/default swap total" "SwapTotal=$swap_total_kb"
		[ "$swap_free_kb" -eq 0 ] && ok "meminfo hides unlimited/default swap free" || bad "meminfo hides unlimited/default swap free" "SwapFree=$swap_free_kb"
		;;
	*)
		expected_swap_kb=$((swap_max / 1024))
		expected_swap_used_kb=$((swap_current / 1024))
		if [ "$expected_swap_used_kb" -gt "$expected_swap_kb" ]; then
			expected_swap_used_kb="$expected_swap_kb"
		fi
		[ "$swap_rows" -ge 1 ] && ok "swaps shows explicit cgroup swap row" || bad "swaps shows explicit cgroup swap row" "rows=$swap_rows"
		check_eq "swaps row total matches cgroup" "$swap_row_total_kb" "$expected_swap_kb"
		check_eq "swaps row used matches cgroup" "$swap_row_used_kb" "$expected_swap_used_kb"
		check_eq "meminfo swap total matches cgroup" "$swap_total_kb" "$expected_swap_kb"
		check_eq "meminfo swap free matches cgroup" "$swap_free_kb" "$((expected_swap_kb - expected_swap_used_kb))"
		;;
esac

disk_content="$(cat /proc/diskstats)"
if [ -z "$disk_content" ]; then
	ok "diskstats empty conservative view"
else
	echo "$disk_content" | awk 'NF >= 14 {ok=1} NF > 0 && NF < 14 {bad=1} END{exit (ok && !bad)?0:1}' && ok "diskstats format" || bad "diskstats format" "$(printf '%s' "$disk_content" | head -3)"
fi

head -1 /proc/slabinfo | grep -Eq '^slabinfo - version: 2\.1' && ok "slabinfo header" || bad "slabinfo header" "$(head -1 /proc/slabinfo)"
head -2 /proc/slabinfo | tail -1 | grep -q '# name' && ok "slabinfo columns" || bad "slabinfo columns" "$(head -2 /proc/slabinfo | tail -1)"

for p in /proc/pressure/io /proc/pressure/cpu /proc/pressure/memory; do
	grep -q '^some avg10=' "$p" && ok "$p some line" || bad "$p some line" "$(cat "$p")"
	if [ "$p" = "/proc/pressure/cpu" ]; then
		ok "$p full line optional"
	else
		grep -q '^full avg10=' "$p" && ok "$p full line" || bad "$p full line" "$(cat "$p")"
	fi
done

echo "--- snapshots ---"
for p in $paths; do
	echo "== $p =="
	head -n 5 "$p" 2>&1
done

exit "$fail"
POD_SCRIPT
)"

	pod_exec bash -lc "${script}"
}

main() {
	need_cmd "${KUBECTL%% *}"
	ensure_pod_ready
	install_tools
	run_validation
}

main "$@"
