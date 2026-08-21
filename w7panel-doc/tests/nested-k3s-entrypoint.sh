#!/bin/sh
set -eu

cni_conf_dir="${CNI_CONF_DIR:-/var/lib/rancher/k3s/agent/etc/cni/net.d}"
cni_bin_dir="${CNI_BIN_DIR:-/var/lib/rancher/k3s/data/cni}"
containerd_template="${CONTAINERD_TEMPLATE:-/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.tmpl}"
pause_image="${K3S_PAUSE_IMAGE:-docker.cnb.cool/i0358/zpk/nested-pause:20260810-1}"
service_cidr="${K3S_SERVICE_CIDR:-10.247.0.0/16}"
cluster_dns="${K3S_CLUSTER_DNS:-10.247.0.10}"

# A persistent rootfs may retain /usr/sbin/iptables links created by an older
# nested chart. Those links target /var/lib/sysbox-inner and are unavailable
# during the next K3s bootstrap, before the chart agent starts. Prefer K3s'
# image-owned helpers and select the real nft multi-call backend up front.
export PATH="/bin/aux:$PATH"
if [ -x /bin/aux/xtables-nft-multi ]; then
	for utility in iptables iptables-save iptables-restore ip6tables ip6tables-save ip6tables-restore; do
		ln -sf xtables-nft-multi "/bin/aux/$utility"
	done
fi

prepare_cni_plugins() {
	# The rancher/k3s image ships the CNI plugins as a multicall /bin/cni
	# binary, but does not populate the data/cni symlinks when used with this
	# custom entrypoint. containerd invokes each plugin by basename.
	[ -x /bin/cni ] || {
		echo 'missing /bin/cni multicall binary' >&2
		exit 1
	}
	mkdir -p "$cni_bin_dir"
	for plugin in bandwidth bridge cni firewall flannel host-local loopback portmap; do
		ln -sf /bin/cni "$cni_bin_dir/$plugin"
	done
}

write_cni_config() {
	# Older test images default to /etc/cni/net.d while K3s-generated
	# containerd config reads its agent directory. Keep both paths populated so
	# a controlled Pod restart cannot lose the network during migration.
	for target_dir in "$cni_conf_dir" /etc/cni/net.d /var/lib/rancher/k3s/agent/etc/cni/net.d; do
		[ -n "$target_dir" ] || continue
		mkdir -p "$target_dir"
		cat >"$target_dir/10-sysbox-nested.conflist" <<'EOF'
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
	done
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
  # The outer Sysbox user namespace owns /proc/sys; do not ask L2 runc to
  # write the host-backed unprivileged-port sysctl during L3 sandbox setup.
  enable_unprivileged_ports = false
  enable_unprivileged_icmp = false
  device_ownership_from_security_context = false

[plugins.'io.containerd.cri.v1.runtime'.cni]
  bin_dirs = ["$cni_bin_dir"]
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
	prepare_cni_plugins
	write_cni_config
	write_containerd_template
	# Publish the L2 K3s and Sysbox sockets through the persistent data mount.
	# The nested chart's hostPath volumes must not resolve to L1's outer /run.
	# Sysbox may not materialize these mountpoints until the first task starts;
	# create the source directories before establishing the persistent binds.
	mkdir -p /run/k3s /run/sysbox
	mkdir -p /var/lib/rancher/k3s/agent/k3s-run /var/lib/rancher/k3s/agent/sysbox-run
	# The nested chart bind-mounts the complete K3s data tree with
	# Bidirectional propagation; containerd rejects a non-shared parent even
	# when the individual socket subdirectories are shared.
	mount --make-rshared /var/lib/rancher/k3s
	# hostPath + Bidirectional propagation used by the L2 nested-agent requires
	# this FUSE mountpoint to be a shared mount before the agent Pod is created.
	# A Pod restart recreates the mount namespace, so a previous manual
	# `mount --make-rshared` is not sufficient.
	mkdir -p /var/lib/sysboxfs-inner
	mountpoint -q /var/lib/sysboxfs-inner || \
		mount --bind /var/lib/sysboxfs-inner /var/lib/sysboxfs-inner
	mount --make-rshared /var/lib/sysboxfs-inner
	mountpoint -q /var/lib/rancher/k3s/agent/k3s-run || \
		mount --bind /run/k3s /var/lib/rancher/k3s/agent/k3s-run
	mountpoint -q /run/sysbox || \
		mount --bind /var/lib/rancher/k3s/agent/sysbox-run /run/sysbox
	mount --make-rshared /var/lib/rancher/k3s/agent/k3s-run
	mount --make-rshared /run/sysbox
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
	--service-cidr="$service_cidr" \
	--cluster-dns="$cluster_dns" \
	--write-kubeconfig-mode=0644 \
	--kubelet-arg='eviction-hard=nodefs.available<1Gi,imagefs.available<1Gi' \
	--kubelet-arg='eviction-minimum-reclaim=nodefs.available=1Gi,imagefs.available=1Gi'
