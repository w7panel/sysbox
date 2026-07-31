#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOC_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
ROOT_DIR="$(cd "${DOC_DIR}/.." && pwd)"
TEST_IMAGE="${TEST_IMAGE:-docker.cnb.cool/i0358/docker-images-chrom/busybox:1.36}"
GO_BIN="${GOBIN:-/tmp/sysbox-build-bin}"
DAEMON_BACKUP=""
HAD_DAEMON_CONFIG=0
DOCKER_CONFIGURED=0
SYSTEMD_DAEMONS=0
MGR_WAS_ACTIVE=0
FS_WAS_ACTIVE=0
MGR_PID=""
FS_PID=""

log() {
	printf '\n==> %s\n' "$*"
}

need_cmd() {
	command -v "$1" >/dev/null 2>&1 || {
		echo "missing required command: $1" >&2
		exit 1
	}
}

build_sysbox() {
	install_build_deps
	log "Building Sysbox binaries"
	"${DOC_DIR}/build.sh" local
}

install_build_deps() {
	need_cmd go
	need_cmd sudo
	need_cmd apt-get
	log "Installing build dependencies"
	sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y \
		protobuf-compiler pkg-config libseccomp-dev rsync fuse3
	mkdir -p "${GO_BIN}"
	export GOPROXY="${GOPROXY:-https://goproxy.cn}"
	export PATH="${GO_BIN}:${PATH}"
	if ! command -v protoc-gen-go >/dev/null 2>&1; then
		env GOBIN="${GO_BIN}" go install github.com/golang/protobuf/protoc-gen-go@v1.3.5
	fi
}

install_binaries() {
	log "Installing local Sysbox binaries into /usr/bin"
	install_binary "$ROOT_DIR/sysbox-runc/build/amd64/sysbox-runc" /usr/bin/sysbox-runc
	install_binary "$ROOT_DIR/sysbox-fs/build/amd64/sysbox-fs" /usr/bin/sysbox-fs
	install_binary "$ROOT_DIR/sysbox-mgr/build/sysbox-mgr" /usr/bin/sysbox-mgr
}

install_binary() {
	local source="$1" target="$2" temporary="${2}.new.$$"
	sudo install -m 0755 "$source" "$temporary"
	sudo mv -f "$temporary" "$target"
}

restore_docker_runtime() {
	if [ "$DOCKER_CONFIGURED" -ne 1 ]; then
		return
	fi

	log "Restoring Docker configuration"
	if [ "$HAD_DAEMON_CONFIG" -eq 1 ]; then
		sudo install -m 0644 "$DAEMON_BACKUP" /etc/docker/daemon.json
	else
		sudo rm -f /etc/docker/daemon.json
	fi
	if pgrep dockerd >/dev/null 2>&1; then
		sudo kill -HUP "$(pgrep dockerd | head -1)" || true
	fi
	rm -f "$DAEMON_BACKUP"
}

restore_daemons() {
	if [ "${SYSTEMD_DAEMONS}" -eq 1 ]; then
		if [ "${MGR_WAS_ACTIVE}" -eq 1 ]; then
			sudo systemctl restart sysbox-mgr >/dev/null 2>&1 || true
		else
			sudo systemctl stop sysbox-mgr >/dev/null 2>&1 || true
		fi
		if [ "${FS_WAS_ACTIVE}" -eq 1 ]; then
			sudo systemctl restart sysbox-fs >/dev/null 2>&1 || true
		else
			sudo systemctl stop sysbox-fs >/dev/null 2>&1 || true
		fi
	else
		[ -z "${MGR_PID}" ] || sudo kill "${MGR_PID}" >/dev/null 2>&1 || true
		[ -z "${FS_PID}" ] || sudo kill "${FS_PID}" >/dev/null 2>&1 || true
		sudo rm -f /run/sysbox/sysmgr.sock /run/sysbox/sysfs.sock \
			/run/sysbox/sysfs-seccomp.sock
	fi
}

restore_environment() {
	restore_daemons
	restore_docker_runtime
}

configure_docker_runtime() {
	log "Configuring Docker runtime sysbox-runc"
	DAEMON_BACKUP="$(mktemp)"
	if sudo test -e /etc/docker/daemon.json; then
		sudo cp /etc/docker/daemon.json "$DAEMON_BACKUP"
		HAD_DAEMON_CONFIG=1
	fi
	sudo python3 - <<'PY'
import json
from pathlib import Path

path = Path("/etc/docker/daemon.json")
data = {}
if path.exists() and path.read_text().strip():
    data = json.loads(path.read_text())
data.setdefault("runtimes", {})
data["runtimes"]["sysbox-runc"] = {"path": "/usr/bin/sysbox-runc"}
path.write_text(json.dumps(data, indent=2) + "\n")
PY
	DOCKER_CONFIGURED=1
	if pgrep dockerd >/dev/null 2>&1; then
		sudo kill -HUP "$(pgrep dockerd | head -1)"
	fi
}

ensure_fuse() {
	log "Ensuring fuse is available"
	sudo modprobe fuse || true
	if ! sudo test -e /dev/fuse; then
		sudo mknod -m 666 /dev/fuse c 10 229
	fi
}

prepare_daemons() {
	if sudo systemctl cat sysbox-mgr.service sysbox-fs.service >/dev/null 2>&1; then
		SYSTEMD_DAEMONS=1
		if sudo systemctl is-active --quiet sysbox-mgr; then
			MGR_WAS_ACTIVE=1
		fi
		if sudo systemctl is-active --quiet sysbox-fs; then
			FS_WAS_ACTIVE=1
		fi
		sudo systemctl stop sysbox-mgr sysbox-fs
	elif pgrep -x sysbox-mgr >/dev/null || pgrep -x sysbox-fs >/dev/null; then
		echo "refusing to replace unmanaged running Sysbox daemons" >&2
		return 1
	fi
	sudo rm -f /run/sysbox/sysmgr.sock /run/sysbox/sysfs.sock /run/sysbox/sysfs-seccomp.sock
}

start_daemons() {
	log "Starting sysbox-mgr and sysbox-fs"
	sudo mkdir -p /run/sysbox /run/sysbox-runc /var/lib/sysbox /var/lib/sysboxfs /var/log/sysbox

	if [ "${SYSTEMD_DAEMONS}" -eq 1 ]; then
		sudo systemctl restart sysbox-mgr sysbox-fs
	else
		MGR_PID="$(sudo sh -c 'nohup /usr/bin/sysbox-mgr --log /var/log/sysbox/sysbox-mgr.log --log-level debug >/var/log/sysbox/sysbox-mgr.stdout 2>&1 & echo $!')"
		sleep 1
		FS_PID="$(sudo sh -c 'nohup /usr/bin/sysbox-fs --log /var/log/sysbox/sysbox-fs.log --log-level debug >/var/log/sysbox/sysbox-fs.stdout 2>&1 & echo $!')"
		sleep 2
	fi

	sudo test -S /run/sysbox/sysmgr.sock
	sudo test -S /run/sysbox/sysfs.sock
}

run_docker_test() {
	log "Running Docker mount and resource-view test"
	docker pull "$TEST_IMAGE"
	docker run --rm --name sysbox-mount-test \
		--runtime=sysbox-runc \
		--memory=128m \
		--cpus=1 \
		"$TEST_IMAGE" sh -c '
set -e
echo MOUNTINFO
grep -E "(/proc/(cpuinfo|meminfo|stat|diskstats|slabinfo|swaps|uptime|pressure/(io|cpu|memory))|/sys/devices/system/cpu/online)" /proc/self/mountinfo || true
echo FILES
for f in \
  /proc/cpuinfo \
  /proc/meminfo \
  /proc/stat \
  /proc/diskstats \
  /proc/slabinfo \
  /proc/swaps \
  /proc/uptime \
  /proc/pressure/io \
  /proc/pressure/cpu \
  /proc/pressure/memory \
  /sys/devices/system/cpu/online
do
  printf "== %s ==\n" "$f"
  head -n 3 "$f" 2>&1 || true
done
'
}

test_local() {
	need_cmd docker
	need_cmd sudo
	need_cmd python3
	prepare_daemons
	install_binaries
	configure_docker_runtime
	ensure_fuse
	start_daemons
	run_docker_test
}

usage() {
	echo "usage: $0 [build|test|all]" >&2
}

main() {
	trap restore_environment EXIT
	cd "$ROOT_DIR"

	case "${1:-all}" in
		build) build_sysbox ;;
		test) test_local ;;
		all)
			build_sysbox
			test_local
			;;
		-h|--help|help) usage ;;
		*) usage; return 2 ;;
	esac
}

main "$@"
