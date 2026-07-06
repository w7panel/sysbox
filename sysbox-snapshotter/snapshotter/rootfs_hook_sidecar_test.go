//go:build linux

package overlay

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/nestybox/sysbox-snapshotter/rootfs"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

func TestApplyRootfsHook_loads_sidecar_spec_once_when_resolving_metadata_and_pvc_mount(t *testing.T) {
	// Given
	intent := rootfs.Intent{
		Version: 1,
		Entries: []rootfs.IntentEntry{{
			ContainerName: "main",
			VolumeName:    "rootfs",
			Path:          "containers/main",
			PVCClaimName:  "rootfs-pvc",
		}},
	}
	rawIntent, err := json.Marshal(intent)
	if err != nil {
		t.Fatalf("marshal sidecar intent: %v", err)
	}
	store := &countingSidecarSpecStore{spec: &runtimespec.Spec{
		Process: &runtimespec.Process{Env: []string{rootfs.SpecEnv + "=" + string(rawIntent)}},
		Mounts: []runtimespec.Mount{{
			Destination: rootfs.SidecarMountPath + "/rootfs",
			Source:      "/pvc/rootfs",
		}},
	}}
	preparer := &fakeRootfsPreparer{prepared: rootfs.PreparedRootfs{UpperDir: "/pvc/upper", WorkDir: "/pvc/work"}}
	hooks := RootfsHooks{
		IdentityResolver: fakeIdentityResolver{request: rootfs.RootfsRwLayerRequest{
			SnapshotKey:   "snapshot-key",
			Namespace:     "default",
			PodName:       "pod",
			PodUID:        "pod-uid",
			ContainerName: "main",
		}},
		MetadataResolver: rootfs.NewSidecarMetadataResolver(store),
		PVCResolver:      rootfs.NewPVCMountPathResolver(store),
		Preparer:         preparer,
	}
	mounts := []mount.Mount{{Type: "overlay", Source: "overlay", Options: []string{"upperdir=/old", "workdir=/old-work"}}}

	// When
	_, err = applyRootfsHook(context.Background(), hooks, "snapshot-key", nil, mounts)

	// Then
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if store.loads != 1 {
		t.Fatalf("expected one sidecar spec load, got %d", store.loads)
	}
	if preparer.request.PVCMountPath != "/pvc/rootfs" {
		t.Fatalf("expected pvc mount path /pvc/rootfs, got %q", preparer.request.PVCMountPath)
	}
}
