#!/bin/sh
set -eu

pid="${1:-$$}"

usage() {
	echo "Usage: $0 [pid]" >&2
	echo "Checks swap-accounting support the same way lxcfs does at startup." >&2
}

walk_up_has_file() {
	base="$1"
	rel="$2"
	file="$3"

	rel="/${rel#/}"

	while :; do
		if [ -e "$base$rel/$file" ]; then
			return 0
		fi

		[ "$rel" = "/" ] && break
		rel="$(dirname "$rel")"
	done

	return 1
}

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
	usage
	exit 0
fi

if [ ! -r "/proc/$pid/cgroup" ]; then
	echo "Cannot read /proc/$pid/cgroup" >&2
	exit 2
fi

if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
	cg="$(awk -F: '$1 == "0" { print $3; exit }' "/proc/$pid/cgroup")"

	if [ -n "$cg" ] && walk_up_has_file /sys/fs/cgroup "$cg" memory.swap.current; then
		echo "Kernel supports swap accounting"
		exit 0
	fi

	echo "Kernel does not support swap accounting"
	exit 1
fi

cg="$(awk -F: '$2 ~ /(^|,)memory(,|$)/ { print $3; exit }' "/proc/$pid/cgroup")"

if [ -n "$cg" ] && walk_up_has_file /sys/fs/cgroup/memory "$cg" memory.memsw.usage_in_bytes; then
	echo "Kernel supports swap accounting"
	exit 0
fi

echo "Kernel does not support swap accounting"
exit 1
