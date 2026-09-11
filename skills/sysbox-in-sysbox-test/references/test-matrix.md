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

Each `03` run must report `FUNCTIONAL PASS` and prove all of:

1. Fresh PVC has nginx image files (`index.html`, `50x.html`).
2. A marker written into the mounted PVC remains after Pod replacement.
3. A marker under `/srv/data` remains after Pod replacement.
4. The Pod has no `sysbox/volume-init` annotation.

## L1 CKM and L2 lite verification

Set `CKM_NAME` and `CKM_NAMESPACE` to a known disposable, Ready CKM. Do not
let the scripts choose another cluster. For a local build, ensure the lite
binary exists first. For a release, pass the downloaded, checksum-verified
release binary.

```bash
export CKM_NAMESPACE=<namespace>
export CKM_NAME=<ckm-name>

SYSBOX_RUNC_LITE_BINARY=/path/to/sysbox-runc-lite-<tag>-amd64 \
  bash ./04-install-ckm-chart.sh

# The previous step changes L1 K3s runtime config. Wait for its Server
# replacement to be Ready before this step.
bash ./05-test-ckm-k3s.sh
```

`05` must reach `FUNCTIONAL PASS`; a watch error caused by the expected L1
restart is not success and must be rerun after the Server is stable.

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
