//go:build linux

package overlay

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/nestybox/sysbox-snapshotter/rootfs"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

var errRootfsHookTest = errors.New("rootfs hook test error")

type fakeIdentityResolver struct {
	request rootfs.RootfsRwLayerRequest
	err     error
}

func (f fakeIdentityResolver) ResolveIdentity(_ context.Context, _ string) (rootfs.RootfsRwLayerRequest, error) {
	return f.request, f.err
}

type fakeMetadataResolver struct {
	spec rootfs.RootfsRwLayerSpec
	err  error
}

func (f fakeMetadataResolver) ResolveRootfsRwLayer(_ context.Context, _ rootfs.RootfsRwLayerRequest) (rootfs.RootfsRwLayerSpec, error) {
	return f.spec, f.err
}

type fakePVCResolver struct {
	mountPath string
	err       error
}

func (f fakePVCResolver) ResolvePVCMountPath(_ context.Context, _ rootfs.RootfsRwLayerRequest, _ rootfs.RootfsRwLayerSpec) (string, error) {
	return f.mountPath, f.err
}

type fakeRootfsPreparer struct {
	prepared rootfs.PreparedRootfs
	request  rootfs.PrepareRootfsRequest
	err      error
}

func (f *fakeRootfsPreparer) PrepareRootfsRwLayer(_ context.Context, request rootfs.PrepareRootfsRequest) (rootfs.PreparedRootfs, error) {
	f.request = request
	return f.prepared, f.err
}

type countingSidecarSpecStore struct {
	spec  *runtimespec.Spec
	loads int
}

func (s *countingSidecarSpecStore) LoadSidecarSpec(_ context.Context, _ rootfs.RootfsRwLayerRequest) (*runtimespec.Spec, error) {
	s.loads++
	return s.spec, nil
}

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

func TestApplyRootfsHook_returns_original_mounts_when_identity_lookup_fails(t *testing.T) {
	// Given
	mounts := []mount.Mount{{Type: "overlay", Source: "overlay", Options: []string{"upperdir=/old", "workdir=/old-work"}}}
	hooks := RootfsHooks{
		IdentityResolver: fakeIdentityResolver{err: errRootfsHookTest},
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
			ImageChainID: "sha256:chain",
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
	if preparer.request.ImageChainID != "sha256:chain" {
		t.Fatalf("expected image chain sha256:chain, got %q", preparer.request.ImageChainID)
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
