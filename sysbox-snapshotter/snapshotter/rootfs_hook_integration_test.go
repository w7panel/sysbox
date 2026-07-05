//go:build linux

package overlay

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/nestybox/sysbox-snapshotter/rootfs"
	"github.com/nestybox/sysbox-snapshotter/rootfscontract"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/require"
)

func TestApplyRootfsHook_rewritesOverlayMounts_fromSidecarIntentAndPVCRoot(t *testing.T) {
	// Given
	pvcRoot := filepath.Join(t.TempDir(), "pv-rootfs")
	require.NoError(t, os.MkdirAll(pvcRoot, 0o755))
	require.NoDirExists(t, filepath.Join(pvcRoot, "containers/main"))

	mounts := []mount.Mount{{
		Type:    "overlay",
		Source:  "overlay",
		Options: []string{"lowerdir=/lower", "upperdir=/old", "workdir=/old-work", "index=off"},
	}}
	hooks := RootfsHooks{
		IdentityResolver: fakeIdentityResolver{request: rootfs.RootfsRwLayerRequest{
			SnapshotKey:   "snapshot-key",
			Namespace:     "default",
			PodName:       "rootfs-demo-abc12",
			PodUID:        "pod-uid-123",
			ContainerName: "main",
		}},
		MetadataResolver: fakeMetadataResolver{spec: rootfs.RootfsRwLayerSpec{
			Namespace:    "default",
			PodName:      "rootfs-demo-abc12",
			VolumeName:   "rootfs",
			Path:         "containers/main",
			PVCClaimName: "rootfs-pvc",
		}},
		PVCResolver: rootfs.NewPVCMountPathResolver(fakeSidecarSpecStore{source: pvcRoot}),
		Preparer:    rootfs.NewLocalPreparer(),
	}

	// When
	got, err := applyRootfsHook(context.Background(), hooks, "snapshot-key", nil, mounts)

	// Then
	require.NoError(t, err)
	wantUpper := filepath.Join(pvcRoot, "containers/main", "upper")
	wantWork := filepath.Join(pvcRoot, "containers/main", "work")
	require.Equal(t, []mount.Mount{{
		Type:    "overlay",
		Source:  "overlay",
		Options: []string{"lowerdir=/lower", "upperdir=" + wantUpper, "workdir=" + wantWork, "index=off"},
	}}, got)
	require.DirExists(t, wantUpper)
	require.DirExists(t, wantWork)
	require.FileExists(t, filepath.Join(pvcRoot, "containers/main", "meta.json"))
}

func TestSnapshotterPrepareAndMounts_rewriteSameRootfsOverlayMount(t *testing.T) {
	// Given
	ctx := context.Background()
	pvcRoot := filepath.Join(t.TempDir(), "pv-rootfs")
	require.NoError(t, os.MkdirAll(pvcRoot, 0o755))
	hooks := RootfsHooks{
		IdentityResolver: snapshotKeyIdentityResolver{request: rootfs.RootfsRwLayerRequest{
			Namespace:     "default",
			PodName:       "rootfs-demo-abc12",
			PodUID:        "pod-uid-123",
			ContainerName: "main",
		}},
		MetadataResolver: snapshotKeyMetadataResolver{
			key: "child",
			spec: rootfs.RootfsRwLayerSpec{
				Namespace:    "default",
				PodName:      "rootfs-demo-abc12",
				VolumeName:   "rootfs",
				Path:         "containers/main",
				PVCClaimName: "rootfs-pvc",
			},
		},
		PVCResolver: rootfs.NewPVCMountPathResolver(fakeSidecarSpecStore{source: pvcRoot}),
		Preparer:    rootfs.NewLocalPreparer(),
	}
	sn, cleanup, err := newSnapshotterWithOpts(WithRootfsHooks(hooks), WithRemapIDs)(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cleanup()) })
	_, err = sn.Prepare(ctx, "base-active", "")
	require.NoError(t, err)
	require.NoError(t, sn.Commit(ctx, "base", "base-active"))

	// When
	labels := containerd.WithUserNSRemapperLabels(
		[]runtimespec.LinuxIDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}},
		[]runtimespec.LinuxIDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}},
	)
	prepareMounts, err := sn.Prepare(ctx, "child", "base", labels)
	require.NoError(t, err)
	recoveredMounts, err := sn.Mounts(ctx, "child")
	require.NoError(t, err)

	// Then
	wantUpper := filepath.Join(pvcRoot, "containers/main", "upper")
	wantWork := filepath.Join(pvcRoot, "containers/main", "work")
	for _, mounts := range [][]mount.Mount{prepareMounts, recoveredMounts} {
		require.Equal(t, "upperdir="+wantUpper, findMountOption(t, mounts, "upperdir="))
		require.Equal(t, "workdir="+wantWork, findMountOption(t, mounts, "workdir="))
		require.NotEmpty(t, findMountOption(t, mounts, "lowerdir="))
		require.Equal(t, "uidmap=0:100000:65536", findMountOption(t, mounts, "uidmap="))
		require.Equal(t, "gidmap=0:100000:65536", findMountOption(t, mounts, "gidmap="))
	}
	require.Equal(t, prepareMounts, recoveredMounts)
	require.DirExists(t, wantUpper)
	require.DirExists(t, wantWork)
}

func TestSnapshotterRemoveAndCleanup_preservePVCRootfsLayer(t *testing.T) {
	// Given
	ctx := context.Background()
	snapshotterRoot := t.TempDir()
	pvcRoot := filepath.Join(t.TempDir(), "pv-rootfs")
	require.NoError(t, os.MkdirAll(pvcRoot, 0o755))
	hooks := RootfsHooks{
		IdentityResolver: snapshotKeyIdentityResolver{request: rootfs.RootfsRwLayerRequest{
			Namespace:     "default",
			PodName:       "rootfs-demo-abc12",
			PodUID:        "pod-uid-123",
			ContainerName: "main",
		}},
		MetadataResolver: snapshotKeyMetadataResolver{
			key: "child",
			spec: rootfs.RootfsRwLayerSpec{
				Namespace:    "default",
				PodName:      "rootfs-demo-abc12",
				VolumeName:   "rootfs",
				Path:         "containers/main",
				PVCClaimName: "rootfs-pvc",
			},
		},
		PVCResolver: rootfs.NewPVCMountPathResolver(fakeSidecarSpecStore{source: pvcRoot}),
		Preparer:    rootfs.NewLocalPreparer(),
	}
	sn, cleanup, err := newSnapshotterWithOpts(WithRootfsHooks(hooks))(ctx, snapshotterRoot)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cleanup()) })
	_, err = sn.Prepare(ctx, "base-active", "")
	require.NoError(t, err)
	require.NoError(t, sn.Commit(ctx, "base", "base-active"))
	_, err = sn.Prepare(ctx, "child", "base")
	require.NoError(t, err)
	wantLayer := filepath.Join(pvcRoot, "containers/main")
	wantUpper := filepath.Join(wantLayer, "upper")
	wantWork := filepath.Join(wantLayer, "work")
	marker := filepath.Join(wantUpper, "persistent-marker")
	require.NoError(t, os.WriteFile(marker, []byte("keep"), 0o600))

	// When
	require.NoError(t, sn.Remove(ctx, "child"))
	cleaner, ok := sn.(snapshots.Cleaner)
	require.True(t, ok)
	require.NoError(t, cleaner.Cleanup(ctx))

	// Then
	require.DirExists(t, wantUpper)
	require.DirExists(t, wantWork)
	require.FileExists(t, filepath.Join(wantLayer, "meta.json"))
	require.FileExists(t, marker)
}

func TestSnapshotterPrepareRootfsHookError_doesNotLeaveSnapshot(t *testing.T) {
	// Given
	ctx := context.Background()
	hooks := RootfsHooks{
		IdentityResolver: snapshotKeyIdentityResolver{request: rootfs.RootfsRwLayerRequest{
			Namespace:     "default",
			PodName:       "rootfs-demo-abc12",
			PodUID:        "pod-uid-123",
			ContainerName: "main",
		}},
		MetadataResolver: snapshotKeyMetadataResolver{
			key: "child",
			spec: rootfs.RootfsRwLayerSpec{
				Namespace:    "default",
				PodName:      "rootfs-demo-abc12",
				VolumeName:   "rootfs",
				Path:         "containers/main",
				PVCClaimName: "rootfs-pvc",
			},
		},
		PVCResolver: fakePVCResolver{mountPath: "/pvc"},
		Preparer:    &fakeRootfsPreparer{err: errRootfsHookTest},
	}
	sn, cleanup, err := newSnapshotterWithOpts(WithRootfsHooks(hooks))(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cleanup()) })
	_, err = sn.Prepare(ctx, "base-active", "")
	require.NoError(t, err)
	require.NoError(t, sn.Commit(ctx, "base", "base-active"))

	// When
	_, err = sn.Prepare(ctx, "child", "base")

	// Then
	require.ErrorIs(t, err, errRootfsHookTest)
	_, err = sn.Stat(ctx, "child")
	require.Error(t, err)
	_, err = sn.Mounts(ctx, "child")
	require.Error(t, err)
}

func TestSnapshotterViewMounts_doNotPrepareRootfsRwLayer(t *testing.T) {
	// Given
	ctx := context.Background()
	preparer := &fakeRootfsPreparer{prepared: rootfs.PreparedRootfs{UpperDir: "/pvc/upper", WorkDir: "/pvc/work"}}
	hooks := RootfsHooks{
		IdentityResolver: snapshotKeyIdentityResolver{request: rootfs.RootfsRwLayerRequest{
			Namespace:     "default",
			PodName:       "rootfs-demo-abc12",
			PodUID:        "pod-uid-123",
			ContainerName: "main",
		}},
		MetadataResolver: snapshotKeyMetadataResolver{
			key: "view",
			spec: rootfs.RootfsRwLayerSpec{
				Namespace:    "default",
				PodName:      "rootfs-demo-abc12",
				VolumeName:   "rootfs",
				Path:         "containers/main",
				PVCClaimName: "rootfs-pvc",
			},
		},
		PVCResolver: fakePVCResolver{mountPath: "/pvc"},
		Preparer:    preparer,
	}
	sn, cleanup, err := newSnapshotterWithOpts(WithRootfsHooks(hooks))(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cleanup()) })
	_, err = sn.Prepare(ctx, "base-active", "")
	require.NoError(t, err)
	require.NoError(t, sn.Commit(ctx, "base", "base-active"))
	viewMounts, err := sn.View(ctx, "view", "base")
	require.NoError(t, err)

	// When
	recoveredMounts, err := sn.Mounts(ctx, "view")

	// Then
	require.NoError(t, err)
	require.Equal(t, viewMounts, recoveredMounts)
	require.Empty(t, preparer.request.SnapshotKey)
}

type snapshotKeyIdentityResolver struct {
	request rootfs.RootfsRwLayerRequest
}

func (r snapshotKeyIdentityResolver) ResolveIdentity(_ context.Context, snapshotKey string) (rootfs.RootfsRwLayerRequest, error) {
	request := r.request
	request.SnapshotKey = snapshotKey
	return request, nil
}

type snapshotKeyMetadataResolver struct {
	key  string
	spec rootfs.RootfsRwLayerSpec
}

func (r snapshotKeyMetadataResolver) ResolveRootfsRwLayer(_ context.Context, request rootfs.RootfsRwLayerRequest) (rootfs.RootfsRwLayerSpec, error) {
	if request.SnapshotKey != r.key {
		return rootfs.RootfsRwLayerSpec{}, rootfs.ErrRootfsRwLayerNotConfigured
	}
	return r.spec, nil
}

func findMountOption(t *testing.T, mounts []mount.Mount, prefix string) string {
	t.Helper()
	for _, m := range mounts {
		if m.Type != "overlay" {
			continue
		}
		for _, option := range m.Options {
			if len(option) >= len(prefix) && option[:len(prefix)] == prefix {
				return option
			}
		}
	}
	t.Fatalf("expected overlay mount option with prefix %q in %#v", prefix, mounts)
	return ""
}

type fakeSidecarSpecStore struct {
	source string
}

func (s fakeSidecarSpecStore) LoadSidecarSpec(context.Context, rootfs.RootfsRwLayerRequest) (*runtimespec.Spec, error) {
	return &runtimespec.Spec{Mounts: []runtimespec.Mount{{
		Source:      s.source,
		Destination: filepath.Join(rootfscontract.SidecarMountPath, "rootfs"),
	}}}, nil
}
