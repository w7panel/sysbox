#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_HOME="${BUILD_HOME:-/tmp/sysbox-build-home}"
GO_PATH="${GOPATH:-/tmp/go-path}"
GO_BUILD_CACHE="${GOCACHE:-/tmp/go-build-cache}"
GO_MOD_CACHE="${GOMODCACHE:-/tmp/go-mod-cache}"
GO_BIN="${GOBIN:-/tmp/sysbox-build-bin}"
GO_PROXY="${GOPROXY:-https://goproxy.cn,direct}"
PATH="$GO_BIN:/home/go/bin:/usr/local/bin:/usr/bin:/bin:$PATH"

export HOME="$BUILD_HOME"
export GOPATH="$GO_PATH"
export GOCACHE="$GO_BUILD_CACHE"
export GOMODCACHE="$GO_MOD_CACHE"
export GOPROXY="$GO_PROXY"
export PATH

log() {
	printf '\n==> %s\n' "$*"
}

need_cmd() {
	command -v "$1" >/dev/null 2>&1 || {
		echo "missing required command: $1" >&2
		exit 1
	}
}

install_deps() {
	if command -v apt-get >/dev/null 2>&1; then
		log "Installing build and runtime dependencies"
		sudo apt-get install -y protobuf-compiler pkg-config libseccomp-dev rsync fuse3
	else
		echo "apt-get not found; install protobuf-compiler, pkg-config, libseccomp-dev, rsync, and fuse3 manually." >&2
		exit 1
	fi
}

install_go_tools() {
	log "Installing protoc-gen-go v1.3.5"
	mkdir -p "$BUILD_HOME" "$GO_PATH" "$GO_BUILD_CACHE" "$GO_MOD_CACHE" "$GO_BIN"
	env GOBIN="$GO_BIN" go install github.com/golang/protobuf/protoc-gen-go@v1.3.5
}

build_sysbox() {
	log "Generating sysbox-ipc protobuf"
	make -C "$ROOT_DIR" sysbox-ipc

	log "Building sysbox-fs and sysbox-runc"
	make -C "$ROOT_DIR" sysbox-fs sysbox-runc

	log "Building sysbox-mgr"
	GOFLAGS=-mod=mod make -C "$ROOT_DIR" sysbox-mgr
}

install_binaries() {
	log "Installing local Sysbox binaries into /usr/bin"
	sudo install -m 0755 "$ROOT_DIR/sysbox-runc/build/amd64/sysbox-runc" /usr/bin/sysbox-runc
	sudo install -m 0755 "$ROOT_DIR/sysbox-fs/build/amd64/sysbox-fs" /usr/bin/sysbox-fs
	sudo install -m 0755 "$ROOT_DIR/sysbox-mgr/build/amd64/sysbox-mgr" /usr/bin/sysbox-mgr
}

configure_docker_runtime() {
	log "Configuring Docker runtime sysbox-runc"
	sudo cp /etc/docker/daemon.json /etc/docker/daemon.json.sysbox-test-bak 2>/dev/null || true
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

start_daemons() {
	log "Starting sysbox-mgr and sysbox-fs"
	sudo mkdir -p /run/sysbox /run/sysbox-runc /var/lib/sysbox /var/lib/sysboxfs /var/log/sysbox

	if sudo test -f /run/sysbox/sysmgr.pid; then
		sudo kill "$(sudo cat /run/sysbox/sysmgr.pid)" 2>/dev/null || true
	fi
	if sudo test -f /run/sysbox/sysfs.pid; then
		sudo kill "$(sudo cat /run/sysbox/sysfs.pid)" 2>/dev/null || true
	fi
	sudo rm -f /run/sysbox/sysmgr.sock /run/sysbox/sysfs.sock /run/sysbox/sysfs-seccomp.sock

	sudo sh -c 'nohup /usr/bin/sysbox-mgr --log /var/log/sysbox/sysbox-mgr.log --log-level debug >/var/log/sysbox/sysbox-mgr.stdout 2>&1 &'
	sleep 1
	sudo sh -c 'nohup /usr/bin/sysbox-fs --log /var/log/sysbox/sysbox-fs.log --log-level debug >/var/log/sysbox/sysbox-fs.stdout 2>&1 &'
	sleep 2

	sudo test -S /run/sysbox/sysmgr.sock
	sudo test -S /run/sysbox/sysfs.sock
}

run_docker_test() {
	log "Running Docker mount and resource-view test"
	docker pull busybox:latest
	docker run --rm --name sysbox-mount-test \
		--runtime=sysbox-runc \
		--memory=128m \
		--cpus=1 \
		busybox:latest sh -c '
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

main() {
	need_cmd go
	need_cmd make
	need_cmd docker
	need_cmd sudo
	need_cmd python3

	cd "$ROOT_DIR"
	install_deps
	install_go_tools
	build_sysbox
	install_binaries
	configure_docker_runtime
	ensure_fuse
	start_daemons
	run_docker_test
}

main "$@"
