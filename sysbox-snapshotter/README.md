# sysbox-snapshotter

`sysbox-snapshotter` is a containerd proxy snapshotter based on `fuse-overlayfs`. It maintains its own snapshot metadata under a containerd-compatible root directory and rewrites only the writable `upperdir` and `workdir` for containers that opt into Sysbox rootfs rw-layer persistence.

The snapshotter root follows the native containerd plugin layout convention under the configured containerd data root:

```text
<containerd-root>/io.containerd.snapshotter.v1.sysbox
```

The current code path keeps the PVC-backed `upperdir/workdir` implementation and leaves `lowerdir` under sysbox-snapshotter's own snapshot root. UID/GID remap labels from containerd are passed through to `fuse-overlayfs` as `uidmapping=` and `gidmapping=` mount options.

## Run

```bash
sysbox-snapshotter \
  --socket /run/sysbox-snapshotter.sock \
  --root /var/lib/containerd/io.containerd.snapshotter.v1.sysbox \
  --containerd-socket /run/containerd/containerd.sock
```

All runtime paths are explicit. `--socket` must match the containerd proxy plugin address, `--root` stores sysbox-snapshotter metadata and snapshots, and `--containerd-socket` is used to read Kubernetes container labels and sidecar OCI specs from containerd.

This project is intended to apply only to the `sysbox-runc` runtime path. The global CRI image snapshotter remains the host default (for example `overlayfs`). Configure containerd so only the `sysbox-runc` runtime uses this snapshotter, and so CRI forwards the rootfs rw-layer pod annotation:

```toml
[proxy_plugins."sysbox"]
  type = "snapshot"
  address = "/run/sysbox-snapshotter.sock"
  capabilities = ["remap-ids"]

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.sysbox-runc]
  runtime_type = "io.containerd.runc.v2"
  snapshotter = "sysbox"
  pod_annotations = ["sysbox/rootfs-rw-layer"]
```

## Remap Contract

`sysbox-snapshotter` consumes containerd's snapshot remap labels when containerd passes them to `Prepare`. For active fuse-overlayfs mounts, labels named `containerd.io/snapshot/uidmapping` and `containerd.io/snapshot/gidmapping` are converted to `uidmapping=` and `gidmapping=` options.

For `hostUsers: false` pods, containerd only delegates remap handling to the proxy snapshotter when the proxy plugin advertises `remap-ids`.

Containerd reads proxy snapshotter capabilities from its static config; the external snapshotter daemon cannot publish them dynamically through the snapshot gRPC service. Keep containerd config, sysbox-snapshotter state, and runtime validation in sync when changing this capability. Existing committed snapshots created under a different remap mode may need to be removed and unpacked again before reuse.

Verify the runtime contract after changing containerd config and restarting containerd/k3s:

```bash
ctr plugins ls -d id==sysbox
```

When remap delegation is enabled, the output must include `Capabilities: remap-ids`. For a fresh `runtimeClassName: sysbox-runc`, `hostUsers: false` pod, `ctr -n k8s.io snapshots --snapshotter sysbox mounts <container-id>` should show `uidmapping=` and `gidmapping=` options, and `ctr -n k8s.io snapshots --snapshotter sysbox ls` should not show a new `*-remap` committed parent for that pod. When `remap-ids` is omitted, the active mount should not contain `uidmapping=` / `gidmapping=`, and containerd may create a fallback `*-remap` parent.

## Rootfs Intent

Rootfs rw-layer intent comes from the Kubernetes Pod annotation forwarded by containerd into each container OCI spec. `sysbox-admission` validates the annotation and always replaces any existing `sysbox-rootfs` container with a canonical sidecar, but it does not write rootfs intent into sidecar environment variables.

```json
{
  "name": "c1",
  "volumeName": "rootfs",
  "path": "containers/c1"
}
```

At runtime, `sysbox-snapshotter` reads the current container OCI annotation `sysbox/rootfs-rw-layer` to find the matching container entry. It still uses containerd labels to find the current Pod's sidecar OCI spec, but only to read the sidecar OCI mounts and resolve `volumeName` to the exact node-side PVC source path.

`path` must be relative and must not contain `..`. The snapshotter never infers a host path from `volumeName` alone; it must match the corresponding sidecar mount at `/var/lib/sysbox/rootfs-rw-volume/<volumeName>`. If no annotation entry is available for a container, native overlay mounts are returned unchanged. Once an annotation entry exists for that container, malformed intent, unavailable sidecar metadata, or a missing requested mount fails closed for that rootfs rw-layer request.

`LocalPreparer` remains local to `sysbox-snapshotter`; rootfs preparation is not performed by sysbox-admission, a database, or a containerd fork.

When an annotation entry exists, the snapshotter prepares this layout under the resolved PVC mount path plus `path`:

```text
upper/
work/
```

Existing `upper/` and `work/` paths must be directories and must not be symlinks. Symlink path components are rejected before creating or reusing the layer.

The identity of a reusable rootfs rw-layer is the configured PVC-backed path, not
the container image. Recreating a Pod with the same `volumeName` and `path` may
reuse the existing `upper/` and `work/` over a different image. This is an
intentional persistence semantic: users are responsible for choosing stable paths
whose lifecycle matches the desired rootfs state. The current Kubernetes
annotation path does not use image chain identity to reject cross-image reuse.

## Snapshot Labels

The current Kubernetes path does not depend on custom rootfs rw-layer snapshot labels. Stock containerd CRI does not pass pod rootfs persistence intent, PVC names, or pod volume data to app-container snapshot `Prepare` calls.

If a future CRI/containerd integration explicitly sets labels on `Prepare`, keep any Sysbox-specific keys under a snapshot-label namespace such as:

```text
io.nestybox.sysbox.rootfs-rw-layer.pod-uid
io.nestybox.sysbox.rootfs-rw-layer.container-name
```

Those labels are not a replacement for resolving the node-side PVC mount path unless that future integration also provides a trusted path. If no rootfs rw-layer annotation entry exists, native overlay mounts are returned unchanged.

## Build And Test

```bash
make sysbox-snapshotter
make test
```

The module currently targets Go 1.24.3.

From the repository root:

```bash
make sysbox-snapshotter
make test-snapshotter-local
```
