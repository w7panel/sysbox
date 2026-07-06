//go:build linux

package overlay

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/nestybox/sysbox-snapshotter/rootfs"
)

func TestApplyRootfsHook_returns_original_mounts_when_hooks_are_incomplete(t *testing.T) {
	// Given
	mounts := []mount.Mount{{Type: "overlay", Source: "overlay", Options: []string{"upperdir=/old", "workdir=/old-work"}}}

	// When
	got, err := applyRootfsHook(context.Background(), RootfsHooks{}, "snapshot-key", nil, mounts)

	// Then
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !reflect.DeepEqual(got, mounts) {
		t.Fatalf("expected original mounts %#v, got %#v", mounts, got)
	}
}

func TestApplyRootfsHook_returns_original_mounts_when_identity_is_unavailable(t *testing.T) {
	// Given
	mounts := []mount.Mount{{Type: "overlay", Source: "overlay", Options: []string{"upperdir=/old", "workdir=/old-work"}}}
	hooks := RootfsHooks{
		IdentityResolver: fakeIdentityResolver{err: rootfs.ErrContainerIdentityUnavailable},
		MetadataResolver: fakeMetadataResolver{},
		Preparer:         &fakeRootfsPreparer{},
	}

	// When
	got, err := applyRootfsHook(context.Background(), hooks, "snapshot-key", nil, mounts)

	// Then
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !reflect.DeepEqual(got, mounts) {
		t.Fatalf("expected original mounts %#v, got %#v", mounts, got)
	}
}

func TestApplyRootfsHook_returns_error_when_identity_lookup_fails(t *testing.T) {
	// Given
	hooks := RootfsHooks{
		IdentityResolver: fakeIdentityResolver{err: errRootfsHookTest},
		MetadataResolver: fakeMetadataResolver{},
		Preparer:         &fakeRootfsPreparer{},
	}

	// When
	_, err := applyRootfsHook(context.Background(), hooks, "snapshot-key", nil, nil)

	// Then
	if !errors.Is(err, errRootfsHookTest) {
		t.Fatalf("expected identity lookup error, got %v", err)
	}
}

func TestApplyRootfsHook_returns_error_when_identity_contract_is_incomplete(t *testing.T) {
	// Given
	hooks := RootfsHooks{
		IdentityResolver: fakeIdentityResolver{err: rootfs.ErrContainerIdentityIncomplete},
		MetadataResolver: fakeMetadataResolver{},
		Preparer:         &fakeRootfsPreparer{},
	}

	// When
	_, err := applyRootfsHook(context.Background(), hooks, "snapshot-key", nil, nil)

	// Then
	if !errors.Is(err, rootfs.ErrContainerIdentityIncomplete) {
		t.Fatalf("expected incomplete identity error, got %v", err)
	}
}

func TestApplyRootfsHook_returns_original_mounts_when_metadata_is_not_configured(t *testing.T) {
	// Given
	mounts := []mount.Mount{{Type: "overlay", Source: "overlay", Options: []string{"upperdir=/old", "workdir=/old-work"}}}
	hooks := RootfsHooks{
		IdentityResolver: fakeIdentityResolver{request: rootfs.RootfsRwLayerRequest{SnapshotKey: "snapshot-key"}},
		MetadataResolver: fakeMetadataResolver{err: rootfs.ErrRootfsRwLayerNotConfigured},
		Preparer:         &fakeRootfsPreparer{},
	}

	// When
	got, err := applyRootfsHook(context.Background(), hooks, "snapshot-key", nil, mounts)

	// Then
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !reflect.DeepEqual(got, mounts) {
		t.Fatalf("expected original mounts %#v, got %#v", mounts, got)
	}
}

func TestApplyRootfsHook_returns_error_when_configured_pvc_mount_becomes_unavailable(t *testing.T) {
	// Given
	mounts := []mount.Mount{{Type: "overlay", Source: "overlay", Options: []string{"upperdir=/old", "workdir=/old-work"}}}
	hooks := RootfsHooks{
		IdentityResolver: fakeIdentityResolver{request: rootfs.RootfsRwLayerRequest{SnapshotKey: "snapshot-key", ContainerName: "main"}},
		MetadataResolver: fakeMetadataResolver{spec: rootfs.RootfsRwLayerSpec{VolumeName: "rootfs", Path: "containers/main"}},
		PVCResolver:      fakePVCResolver{err: rootfs.ErrSidecarSpecUnavailable},
		Preparer:         &fakeRootfsPreparer{},
	}

	// When
	_, err := applyRootfsHook(context.Background(), hooks, "snapshot-key", nil, mounts)

	// Then
	if !errors.Is(err, rootfs.ErrSidecarSpecUnavailable) {
		t.Fatalf("expected sidecar spec unavailable error, got %v", err)
	}
}

func TestApplyRootfsHook_rewrites_overlay_mounts_when_layer_is_prepared(t *testing.T) {
	// Given
	mounts := []mount.Mount{{Type: "overlay", Source: "overlay", Options: []string{"lowerdir=/lower", "upperdir=/old", "workdir=/old-work"}}}
	preparer := &fakeRootfsPreparer{prepared: rootfs.PreparedRootfs{UpperDir: "/pvc/upper", WorkDir: "/pvc/work"}}
	hooks := RootfsHooks{
		IdentityResolver: fakeIdentityResolver{request: rootfs.RootfsRwLayerRequest{
			SnapshotKey:   "snapshot-key",
			Namespace:     "default",
			PodName:       "pod",
			PodUID:        "pod-uid",
			ContainerName: "main",
			UIDMappings:   []rootfs.IDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}},
			GIDMappings:   []rootfs.IDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}},
		}},
		MetadataResolver: fakeMetadataResolver{spec: rootfs.RootfsRwLayerSpec{
			VolumeName:   "rootfs",
			Path:         "containers/main",
			PVCClaimName: "rootfs-pvc",
		}},
		PVCResolver: fakePVCResolver{mountPath: "/pvc"},
		Preparer:    preparer,
	}

	// When
	got, err := applyRootfsHook(context.Background(), hooks, "snapshot-key", nil, mounts)

	// Then
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	want := []mount.Mount{{Type: "overlay", Source: "overlay", Options: []string{"lowerdir=/lower", "upperdir=/pvc/upper", "workdir=/pvc/work"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected rewritten mounts %#v, got %#v", want, got)
	}
	if preparer.request.PVCMountPath != "/pvc" {
		t.Fatalf("expected preparer pvc mount path /pvc, got %q", preparer.request.PVCMountPath)
	}
}

func TestApplyRootfsHook_uses_snapshot_mapping_labels_when_identity_has_no_mappings(t *testing.T) {
	// Given
	mounts := []mount.Mount{{Type: "overlay", Source: "overlay", Options: []string{"lowerdir=/lower", "upperdir=/old", "workdir=/old-work"}}}
	preparer := &fakeRootfsPreparer{prepared: rootfs.PreparedRootfs{UpperDir: "/pvc/upper", WorkDir: "/pvc/work"}}
	hooks := RootfsHooks{
		IdentityResolver: fakeIdentityResolver{request: rootfs.RootfsRwLayerRequest{
			SnapshotKey:   "snapshot-key",
			Namespace:     "default",
			PodName:       "pod",
			PodUID:        "pod-uid",
			ContainerName: "main",
		}},
		MetadataResolver: fakeMetadataResolver{spec: rootfs.RootfsRwLayerSpec{
			VolumeName:   "rootfs",
			Path:         "containers/main",
			PVCClaimName: "rootfs-pvc",
		}},
		PVCResolver: fakePVCResolver{mountPath: "/pvc"},
		Preparer:    preparer,
	}
	labels := map[string]string{
		snapshots.LabelSnapshotUIDMapping: "0:100000:65536",
		snapshots.LabelSnapshotGIDMapping: "0:100001:65536",
	}

	// When
	_, err := applyRootfsHook(context.Background(), hooks, "snapshot-key", labels, mounts)

	// Then
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	wantUID := []rootfs.IDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}}
	wantGID := []rootfs.IDMapping{{ContainerID: 0, HostID: 100001, Size: 65536}}
	if !reflect.DeepEqual(preparer.request.UIDMappings, wantUID) {
		t.Fatalf("expected UID mappings %#v, got %#v", wantUID, preparer.request.UIDMappings)
	}
	if !reflect.DeepEqual(preparer.request.GIDMappings, wantGID) {
		t.Fatalf("expected GID mappings %#v, got %#v", wantGID, preparer.request.GIDMappings)
	}
}

func TestApplyRootfsHook_returns_error_when_metadata_lookup_fails(t *testing.T) {
	// Given
	hooks := RootfsHooks{
		IdentityResolver: fakeIdentityResolver{request: rootfs.RootfsRwLayerRequest{SnapshotKey: "snapshot-key"}},
		MetadataResolver: fakeMetadataResolver{err: errRootfsHookTest},
		Preparer:         &fakeRootfsPreparer{},
	}

	// When
	_, err := applyRootfsHook(context.Background(), hooks, "snapshot-key", nil, nil)

	// Then
	if !errors.Is(err, errRootfsHookTest) {
		t.Fatalf("expected metadata error, got %v", err)
	}
}

func TestApplyRootfsHook_returns_error_when_prepare_fails(t *testing.T) {
	// Given
	hooks := RootfsHooks{
		IdentityResolver: fakeIdentityResolver{request: rootfs.RootfsRwLayerRequest{SnapshotKey: "snapshot-key"}},
		MetadataResolver: fakeMetadataResolver{spec: rootfs.RootfsRwLayerSpec{VolumeName: "rootfs"}},
		PVCResolver:      fakePVCResolver{mountPath: "/pvc"},
		Preparer:         &fakeRootfsPreparer{err: errRootfsHookTest},
	}

	// When
	_, err := applyRootfsHook(context.Background(), hooks, "snapshot-key", nil, nil)

	// Then
	if !errors.Is(err, errRootfsHookTest) {
		t.Fatalf("expected prepare error, got %v", err)
	}
}
