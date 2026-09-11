---
name: sysbox-in-sysbox-test
description: Run and diagnose the Sysbox-in-Sysbox L0 host, L1 CKM, and L2 workload regression flow, including rootfs PVC persistence, empty-volume initialization, special bind mounts, and release-artifact verification.
---

# Sysbox-in-Sysbox Test

Use this skill for validating the W7Panel Sysbox runtime flow, not for generic
Kubernetes workload tests.

The topology is L0 host Kubernetes, L1 CKM Server/K3s, and L2 workload. The
supported workload is nginx; Docker, systemd, strong `/proc` isolation, and
host-view isolation are outside this flow.

## Before changing a cluster

1. Read `w7panel-doc/sysbox-in-sysbox/config.sh`, `_common.sh`, and the target
   script. Set `KUBECONFIG_218`, CKM namespace/name, image repository/tag, and
   storage class explicitly; do not select an arbitrary CKM.
2. Confirm the intended CKM is `Ready`, its L1 Server uses `sysbox-runc` and
   `hostUsers: false`, and that its L2 test target is disposable.
3. For a release verification, download the exact tag's chart and lite binary,
   verify `SHA256SUMS`, and install that chart/image before calling a result a
   release result. A workspace-built image is only a preliminary result.

Read [the test matrix](references/test-matrix.md) before running tests. It
contains the commands and the required evidence for source and release modes.
Read [the build and script catalog](references/build-and-script-catalog.md)
when building an image, packaging a chart, selecting a test stage, or using
the executable Skill wrappers.

## Required invariants

- L0 exposes both `sysbox-runc` and `sysbox-runc-lite`; normal L0 Pods must
  not receive an automatic `/dev/fuse` mount.
- Full L0 `sysbox-runc` rootfs/nginx validation uses `hostUsers: false`.
  L2 `sysbox-runc-lite` workloads deliberately omit it.
- Empty-volume initialization is runtime discovery, not the removed
  `sysbox/volume-init` annotation. Accept only Kubelet `kubernetes.io~csi`
  and K3s `kubernetes.io~local-volume` PVC paths; never widen this to
  `emptyDir`, `hostPath`, projected, or arbitrary bind mounts.
- Validate both `local-path` and at least one real CSI StorageClass when the
  cluster provides one. Verify initial image files, a PVC marker after Pod
  recreation, and a rootfs special-bind marker after recreation.
- Nested chart installation targets the selected L1 K3s only. It creates only
  `sysbox-runc-lite` there and writes its binary to the persistent L1 K3s data
  volume; it must not create a second K3s or replace `/usr/bin/runc`.

## Failure handling and cleanup

Do not treat a failed rollout as a passed test. Capture the Pod description,
container state/logs, PVC status, and relevant L1/L0 events; fix the cause and
re-run the failed case plus its persistence recreation step.

The snapshotter may initially report `sysbox sidecar oci spec unavailable`;
only continue after the retry reaches Ready and the functional assertions pass.
If a newly created `local-path` PVC is Pending, wait for provisioning before
calling it a runtime failure. Installing the nested lite handler restarts the
CKM Server; wait for its replacement to become Ready, then re-run the L2
case.

After successful tests, remove ephemeral Pods. To release Pod IPs while
keeping persistence evidence, scale test Deployments to zero. Delete test
PVCs only when the user authorizes storage cleanup. Record material failures
and their verified cause in `w7panel-doc/sysbox-in-sysbox/KNOWN-ISSUES.md`.
