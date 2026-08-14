#!/bin/sh
set -eu

cni_conf_dir="${CNI_CONF_DIR:-/etc/cni/net.d}"
cni_conf="$cni_conf_dir/10-sysbox-nested.conflist"
containerd_template="${CONTAINERD_TEMPLATE:-/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.tmpl}"
pause_image="${K3S_PAUSE_IMAGE:-docker.cnb.cool/i0358/zpk/nested-pause:20260810-1}"

write_cni_config() {
	mkdir -p "$cni_conf_dir"
	cat >"$cni_conf" <<'EOF'
{
  "cniVersion": "1.0.0",
  "name": "sysbox-nested",
  "plugins": [
    {
      "type": "bridge",
      "bridge": "cni3",
      "isGateway": true,
      "ipMasq": true,
      "hairpinMode": true,
      "ipam": {
        "type": "host-local",
        "ranges": [[{"subnet": "10.245.0.0/16"}]],
        "routes": [{"dst": "0.0.0.0/0"}]
      }
    },
    {
      "type": "portmap",
      "capabilities": {"portMappings": true}
    }
  ]
}
EOF
}

write_containerd_template() {
	# A nested chart agent may have installed the sysbox runtime after the first
	# K3s boot. Preserve that template across a controlled L1 container restart.
	if [ -s "$containerd_template" ] && grep -q 'runtimes.sysbox-runc' "$containerd_template"; then
		return 0
	fi
	mkdir -p "$(dirname "$containerd_template")"
	cat >"$containerd_template" <<EOF
version = 3
imports = ["/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.d/*.toml"]
root = "/var/lib/rancher/k3s/agent/containerd"
state = "/run/k3s/containerd"

[grpc]
  address = "/run/k3s/containerd/containerd.sock"

[plugins.'io.containerd.internal.v1.opt']
  path = "/var/lib/rancher/k3s/agent/containerd"

[plugins.'io.containerd.grpc.v1.cri']
  stream_server_address = "127.0.0.1"
  stream_server_port = "10010"

[plugins.'io.containerd.cri.v1.runtime']
  enable_selinux = false
  enable_unprivileged_ports = true
  enable_unprivileged_icmp = true
  device_ownership_from_security_context = false

[plugins.'io.containerd.cri.v1.runtime'.cni]
  bin_dirs = ["/var/lib/rancher/k3s/data/cni"]
  conf_dir = "/etc/cni/net.d"

[plugins.'io.containerd.cri.v1.images']
  snapshotter = "native"
  disable_snapshot_annotations = false
  use_local_image_pull = true

[plugins.'io.containerd.cri.v1.images'.pinned_images]
  sandbox = "$pause_image"

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc.options]
  SystemdCgroup = false

[plugins.'io.containerd.cri.v1.images'.registry]
  config_path = "/var/lib/rancher/k3s/agent/etc/containerd/certs.d"
EOF
}

prepare_server() {
	write_cni_config
	write_containerd_template
}

if [ "$#" -gt 0 ]; then
	case "$1" in
		server)
			prepare_server
			exec /bin/k3s "$@"
			;;
		agent)
			exec /bin/k3s "$@"
			;;
		-*)
			prepare_server
			exec /bin/k3s server "$@"
			;;
		*)
			exec "$@"
			;;
	esac
fi

prepare_server
exec /bin/k3s server \
	--snapshotter=native \
	--flannel-backend=none \
	--disable-network-policy \
	--disable=traefik \
	--disable=servicelb \
	--cluster-cidr=10.245.0.0/16 \
	--service-cidr=10.246.0.0/16 \
	--cluster-dns=10.246.0.10 \
	--write-kubeconfig-mode=0644 \
	--kubelet-arg='eviction-hard=nodefs.available<1Gi,imagefs.available<1Gi' \
	--kubelet-arg='eviction-minimum-reclaim=nodefs.available=1Gi,imagefs.available=1Gi'
