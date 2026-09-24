# Build and test script catalog

The authoritative implementation remains in the repository. The wrappers in
this Skill dispatch to it so a build or test always uses the current source.

## Build and release

| Script | Role | Important inputs | Output / proof |
| --- | --- | --- | --- |
| `w7panel-doc/release.sh` | `release` builds deb, static lite binary, deploy image and chart; `test` incrementally patches selected binaries into explicit base images | `BUILD_PROFILE`, `TEST_COMPONENTS`, `TEST_TARGETS`, base/output images, `RELEASE_TAG`, `IMAGE_REPO`, `IMAGE_TAG`, `PUSH_IMAGE`, `MIRROR_PROFILE`, `PACKAGE_CHART` | `dist/`, registry image, optional GitHub release |
| `skills/sysbox-in-sysbox-test/scripts/build-release.sh` | Skill entrypoint for the above script | Passes all environment and arguments through unchanged | Same as `release.sh` |

Use a China mirror profile when appropriate:

```bash
MIRROR_PROFILE=china PUSH_IMAGE=true \
  RELEASE_TAG=<tag> IMAGE_TAG=<tag> \
  skills/sysbox-in-sysbox-test/scripts/build-release.sh
```

The script clears and regenerates `dist/`; do not point it at an artifact
directory that must be retained. A pushed temporary image must be tested
before a tag release, but only the GitHub release chart/image/binary is the
final release input.

For an ordinary code iteration prefer cached test mode. Valid components are
`runc-lite`, `snapshotter`, `admission`, and `inner-script`; valid targets are
`deploy`, `bootstrap`, or both. `test` writes `dist/test-images.env`, including
the bootstrap image that must be applied to CKM explicitly.

## Test stages

| Stage | Authoritative script | What it validates |
| --- | --- | --- |
| `preflight` | `00-check-prereqs.sh` | kubeconfig, CRDs, templates and tools |
| `ckm` | `01-create-ckm.sh` | explicitly configured CKM discovery or creation |
| `l0-smoke` | `02-test-l0-runtimeclasses.sh` | both host RuntimeClasses and no automatic FUSE |
| `l0-rootfs` | `03-test-l0-rootfs.sh` | PVC initialization, rootfs persistence and special bind |
| `l1-install` | `04-install-ckm-chart.sh` | nested chart and persisted lite binary/config |
| `l2` | `05-test-ckm-k3s.sh` | nested lite nginx rootfs/PVC regression |
| `build-and-test` | `06-build-and-test.sh` | selected cached build plus 00/02/03/04/05 regression |
| `cleanup` | `99-cleanup.sh` | destructive L2 chart/PVC cleanup; retains CKM by default |

Invoke one stage through the Skill wrapper after exporting explicit values:

```bash
export KUBECONFIG_218=/path/to/218.config
export CKM_NAMESPACE=<namespace>
export CKM_NAME=<name>
skills/sysbox-in-sysbox-test/scripts/run-stage.sh l0-smoke
```

For `l0-rootfs`, set `L0_RUNTIME_CLASS`, `L0_TEST_SUFFIX`, and
`ROOTFS_STORAGE_CLASS`. `sysbox-runc` receives `hostUsers:false` by default;
`sysbox-runc-lite` does not. `cleanup` deletes L2 resources, so run it only
when that cleanup is intended.
