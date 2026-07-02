# sysbox-snapshotter

`sysbox-snapshotter` is a containerd proxy snapshotter that maintains its own snapshot metadata under a containerd-compatible root directory and focuses on rewriting only the writable `upperdir` and `workdir` for containers that opt into Sysbox rootfs rw-layer persistence.

The snapshotter root should follow the native containerd plugin layout convention:

```text
/var/lib/rancher/k3s/agent/containerd/io.containerd.snapshotter.v1.sysbox
```

The current stable code path keeps the PVC-backed `upperdir/workdir` implementation and removes the previous experimental lower-source borrowing path. Lower alignment to overlay-native semantics remains an implementation task inside the sysbox snapshotter itself rather than by borrowing another snapshotter's runtime lower directories.

## Run

```bash
sysbox-snapshotter \
  --address /run/sysbox-snapshotter.sock \
  --root /var/lib/rancher/k3s/agent/containerd/io.containerd.snapshotter.v1.sysbox
```

Containerd proxy plugin config:

```toml
version = 3

[proxy_plugins]
  [proxy_plugins."sysbox"]
    type = "snapshot"
    address = "/run/sysbox-snapshotter.sock"
```

This project is intended to apply only to the `sysbox-runc` runtime path. The global CRI image snapshotter remains the host default (for example `overlayfs`).

## Idmapped Mount Contract

`sysbox-snapshotter` derives idmapped mount behavior from the configured containerd proxy plugin capabilities. If the `sysbox` proxy plugin does not advertise `remap-ids`, the daemon does not enable `overlay.WithRemapIDs` and never returns `uidmap=` / `gidmap=` mount options. In that state, if containerd receives user namespace remap labels, it may use its fallback `*-remap` parent snapshot path.

Idmapped overlay mounts are enabled by adding `remap-ids` to the proxy plugin capabilities:

```toml
[proxy_plugins."sysbox"]
  type = "snapshot"
  address = "/run/sysbox-snapshotter.sock"
  capabilities = ["remap-ids"]
```

When `remap-ids` is configured, the daemon checks kernel support before serving the proxy socket and fails closed if idmapped overlay mounts are unavailable. Containerd reads proxy snapshotter capabilities from its static config; the external snapshotter daemon cannot publish them dynamically through the snapshot gRPC service. Do not run a configuration where the daemon returns `uidmap=` / `gidmap=` mount options but containerd does not list `remap-ids` for the `sysbox` snapshotter, because containerd will create fallback `*-remap` snapshots and the rootfs will mix two remap strategies.

Verify the runtime contract after changing containerd config and restarting containerd/k3s:

```bash
ctr plugins ls -d id==sysbox
```

When idmapped mode is enabled, the output must include `Capabilities: remap-ids`. For a fresh `runtimeClassName: sysbox-runc`, `hostUsers: false` pod, `ctr -n k8s.io snapshots --snapshotter sysbox mounts <container-id>` should show `uidmap=` and `gidmap=` options, and `ctr -n k8s.io snapshots --snapshotter sysbox ls` should not show a new `*-remap` committed parent for that pod. When `remap-ids` is omitted, the active mount should not contain `uidmap=` / `gidmap=`, and containerd may create a fallback `*-remap` parent.

## Rootfs Intent

The current bridge stores rootfs rw-layer intent on the injected sidecar container, not in per-Pod files. `sysbox-admission` always replaces any existing `sysbox-rootfs` container with a canonical sidecar and writes the complete intent to the `ROOTFS_RW_LAYER_SPEC` environment variable:

```json
{
  "version": 1,
  "entries": [
    {
      "containerName": "c1",
      "volumeName": "rootfs",
      "path": "containers/c1",
      "pvcClaimName": "sysbox-rootfs-pvc"
    }
  ]
}
```

At runtime, `sysbox-snapshotter` uses containerd labels to find the current Pod's sidecar OCI spec. It reads `ROOTFS_RW_LAYER_SPEC` for container intent and reads the sidecar OCI mounts to resolve `volumeName` to the exact node-side PVC source path.

`path` must be relative and must not contain `..`. The snapshotter never infers a host path from `volumeName` alone; it must match the corresponding sidecar mount at `/var/lib/sysbox/rootfs-rw-volume/<volumeName>`. If the sidecar spec is malformed or the requested mount is missing, startup fails closed. Kubelet PVC directory scanning remains only as a fallback when sidecar spec data is unavailable.

`LocalPreparer` remains local to `sysbox-snapshotter`; this near-term bridge does not move rootfs preparation into sysbox-admission, a database, or a containerd fork.

When an intent entry exists, the snapshotter prepares this layout under the resolved PVC mount path plus `path`:

```text
upper/
work/
meta.json
```

If `meta.json` is missing and the target directory is non-empty, startup fails closed to avoid reusing foreign data.

## Snapshot Labels

The current Kubernetes path does not depend on custom rootfs rw-layer snapshot labels. Stock containerd CRI does not pass pod rootfs persistence intent, PVC names, or pod volume data to app-container snapshot `Prepare` calls.

If a future CRI/containerd integration explicitly sets labels on `Prepare`, keep any Sysbox-specific keys under a snapshot-label namespace such as:

```text
io.nestybox.sysbox.rootfs-rw-layer.pod-uid
io.nestybox.sysbox.rootfs-rw-layer.container-name
io.nestybox.sysbox.rootfs-rw-layer.image-chain-id
```

Those labels are not a replacement for resolving the node-side PVC mount path unless that future integration also provides a trusted path. If no sidecar intent exists, native overlay mounts are returned unchanged.

## Build And Test

```bash
make sysbox-snapshotter
make test
```

From the repository root:

```bash
make sysbox-snapshotter
make test-snapshotter-local
```
