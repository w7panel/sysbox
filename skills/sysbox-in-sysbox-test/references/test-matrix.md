# Sysbox-in-Sysbox regression matrix

Run commands from `w7panel-doc/sysbox-in-sysbox`. Export the actual outer
kubeconfig before every flow:

```bash
export KUBECONFIG_218=/path/to/218.config
```

## Source or temporary-image verification

Use this after rebuilding/installing the host chart. The scripts source
`config.sh`; override its variables on the command line rather than editing it
for one test.

```bash
bash ./00-check-prereqs.sh
bash ./02-test-l0-runtimeclasses.sh

# Lite: no hostUsers field.
L0_RUNTIME_CLASS=sysbox-runc-lite L0_TEST_SUFFIX=lite-local \
  ROOTFS_STORAGE_CLASS=local-path bash ./03-test-l0-rootfs.sh

# Full handler: script writes hostUsers:false and checks nginx:80.
L0_RUNTIME_CLASS=sysbox-runc L0_TEST_SUFFIX=full-local \
  ROOTFS_STORAGE_CLASS=local-path bash ./03-test-l0-rootfs.sh

# Repeat one handler for a real CSI StorageClass when available.
L0_RUNTIME_CLASS=sysbox-runc L0_TEST_SUFFIX=full-csi \
  ROOTFS_STORAGE_CLASS=<csi-storage-class> bash ./03-test-l0-rootfs.sh
```

`02` creates a `hostUsers:false` `sysbox-runc` Pod without a FUSE volume or
mount in its Pod spec, then checks `/dev/fuse` is a character device and opens
it. It also checks that `sysbox-runc-lite` does not receive the device. Inspect
the Pod spec as well as the container: runtime-added mounts are absent from
`kubectl get pod -o yaml`. If the L0 check fails, keep the CKM template mount
until the host runtime is corrected; a successful `test -c` alone is weaker
than opening the device.

Each `03` run must report `FUNCTIONAL PASS` and prove all of:

1. Fresh PVC has nginx image files (`index.html`, `50x.html`).
2. A marker written into the mounted PVC remains after Pod replacement.
3. A marker under `/srv/data` remains after Pod replacement.
4. The Pod has no `sysbox/volume-init` annotation.

## Fast source-build verification

Use the flow driver after a focused runtime, snapshotter, admission, or inner
script change. Build only the deploy image, which supplies the Chart's inner
agent and L1 payload:

```bash
BUILD_PROFILE=test \
TEST_COMPONENTS=runc-lite,snapshotter,admission,inner-script \
TEST_TARGETS=deploy \
TEST_BASE_IMAGE=<known-good-deploy-image> \
TEST_IMAGE=<test-deploy-image> \
PUSH_IMAGE=true \
bash ./06-build-and-test.sh
```

Set `SYSBOX_IMAGE_REPO` and `SYSBOX_IMAGE_TAG` to the emitted deploy image
before installing the Chart. The required proof is the final `FUNCTIONAL PASS`
from both `03` and `05`. For Chart, packaging, or release changes, use
`BUILD_PROFILE=release`; this is a source-artifact result, not GitHub-release
verification.

## L1 CKM and L2 lite verification

Set `CKM_NAME` and `CKM_NAMESPACE` to an existing Ready CKM. Its Server must
use the outer `sysbox-runc` and have a usable `/dev/fuse` without a Pod volume;
no bootstrap initContainer or
startup prelude is required. For a release, pass the downloaded,
checksum-verified Chart.

```bash
export CKM_NAMESPACE=<namespace>
export CKM_NAME=<ckm-name>

NESTED_CHART_FILE=/path/to/w7panel-sysbox-<version>.tgz \
  bash ./04-install-ckm-chart.sh
bash ./05-test-ckm-k3s.sh
```

`04` must leave Helm release `w7panel-sysbox` deployed, the native-runc inner
agent and admission Ready, the loaded `sysbox-runc-lite` handler visible in
`crictl info`, the `images.runtime_platforms.sysbox-runc-lite` mapping set to
`sysbox`, and no full inner handler. It may reconnect after the agent
restarts inner K3s. A second install with unchanged image/configuration must
not restart K3s. `05` must reach `FUNCTIONAL PASS`; an API watch error during
restart is not success. In the test cluster, repeat the migration and checks
for every existing CKM sequentially, including previously pinned templates.
Also create one fresh CKM when validating a change to inner containerd: a
migrated instance may retain CRI filesystem state from the old bootstrap.

## GitHub release verification

1. Push an immutable tag; wait for its GitHub Actions run to succeed.
2. Download the tag's `w7panel-sysbox-*.tgz`, matching lite binary, and
   `SHA256SUMS` through the configured GitHub proxy if required.
3. Check both assets with `sha256sum -c SHA256SUMS --ignore-missing`.
4. Upgrade L0 using the downloaded chart and the image listed in its
   `image-metadata.txt`; wait for installer and admission Pods to become
   Ready.
5. Re-run the L0 matrix and then the L2 matrix using the downloaded lite
   binary. Only then describe the tag as verified.

## Releasing test resources

The test names begin with `sysbox-l0-rootfs-` or `sysbox-full-`. After evidence
is collected, release their Pod IPs without deleting their PVCs:

```bash
kubectl --kubeconfig "$KUBECONFIG_218" -n default scale deployment <test-name> --replicas=0
```

Use deletion only for named, confirmed test resources and only when storage
cleanup is authorized.
