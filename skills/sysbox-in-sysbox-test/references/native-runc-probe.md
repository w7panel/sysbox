# Native runc rootfs probe (218, 2026-09-30)

This is a comparison result, not a replacement for the full lite regression
matrix. Preserve the named test resources unless the user requests cleanup.

## Tested topology and result

- L0: 218. A newly created L1 CKM, `k3k-console-164315/ckm-disk-ubuntu-0930`,
  uses a `disk-default` outer data PVC (80 Gi). This is distinct from the
  earlier `local-path` CKM `ckm-cristats-0929`.
- L2: Ubuntu 24.04 Deployment `default/runc-ubuntu-disk-probe-0930` uses a
  test RuntimeClass whose containerd handler points to official `/bin/runc`
  with the `sysbox` snapshotter. The image was
  `ccr.ccs.tencentyun.com/afan-public/ubuntu:24.04`.
- `/bin/bash` and interactive exec worked. A rootfs marker survived both L2
  Pod recreation and L1 CKM Pod recreation; ownership remained `0:0`.
- L1 and L2 reported the same UID map. `/dev/null` appeared as
  `65534:65534`, mode `0666`; both root and `nobody` could write to it. Do not
  infer from this that its owner is `0:0`.
- An initial `sysbox sidecar oci spec unavailable` was transient: retry
  reached Ready and the functional assertions then passed.

The result establishes that basic rootfs persistence, including the
`disk-default` CKM storage path, does not require the lite runtime binary.
The relevant data path still depends on admission and the `sysbox`
snapshotter; running ordinary runc with its default `overlayfs` snapshotter
is **not** equivalent.

## Boundary before replacing lite

The native-runc probe did **not** verify either of these lite behaviors:

1. Copy image directory contents into an empty PVC at first mount without a
   `sysbox/volume-init` annotation.
2. Consume the snapshotter handoff and persistently bind special directories
   selected by `persistentSpecialMounts` / `specialPath`.

`sysbox-runc-lite/lite.go` currently implements both. Treat lite as removable
only after these cases pass under native runc or their implementation is
moved elsewhere and the complete regression matrix passes. The observed
`/dev/null` and exec success also means lite's device-mount compatibility
code is not demonstrated to be necessary by this probe.

## Live inner-script regression after removing redundant config

On the same CKM, the inner K3s startup script was replaced in the running
Server and `k3s-server` was restarted. Both `config-v3.toml.tmpl` and the
generated `config.toml` then lacked `runtime_platforms` and
`container_annotations`. CRI info still reported default `runc/overlayfs`
and `sysbox-runc-lite/sysbox`; `PodAnnotations` remained configured while
`ContainerAnnotations` was null.

`05-test-ckm-k3s.sh` reported `FUNCTIONAL PASS`, covering lite rootfs
persistence, annotation-free empty-PVC initialization, special binds, CRI
mountpoint, and `/stats/summary`. Nested `kubectl exec` could access
`/dev/null` and files, and the native-runc Ubuntu rootfs marker still existed.
This is a **live-script-swap regression**, not validation that any rebuilt or
published bootstrap image contains the changed script. Rebuild, deploy, and
rerun the same checks before calling a release artifact verified.

## Minimal lite binary regression

A locally built static lite binary removed custom `/dev`, `/proc`, sysfs,
mount-error, bind, and pivot fallbacks while preserving standard runc safety
logic plus the two lite features. Its SHA256 was
`9268ef44e1d5a6823c1bb3a4f878974496bb5d2f3bedf0e8f72c52459678befa`.
The same binary replaced only the new CKM's lite copy and L0
`/usr/bin/sysbox-runc-lite`; both previous binaries were backed up.

The new L2 `ckm-k3s-nginx-minimal` passed `05-test-ckm-k3s.sh`, including
empty-PVC copy, persistent rootfs and special bind, CRI mountpoint, and
`/stats/summary`. L0 `sysbox-l0-rootfs-lite-minimal-0930` passed on
`local-path`, and `sysbox-l0-rootfs-lite-minimal-disk-0930` passed on the
Longhorn `disk-default` StorageClass. L0 and L2 TTY exec, `/dev/null`
character-device access, and ordinary `/proc` and `/sys` mounts also passed.
The Deployments and PVCs were retained. This is a local binary test, not a
built bootstrap/deploy image or tagged release result.
The source subsequently dropped an unrelated `/dev/null` precheck in
`fixStdioPermissions`; that final source variant has passed unit tests but has
not yet received the live L0/L2 regression.
