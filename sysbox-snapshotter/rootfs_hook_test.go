//go:build linux

package sysboxsnapshotter

import (
	"context"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/w7panel/sysbox/sysbox-snapshotter/rootfs"
)

func TestApplyRootfsHookRewritesOnlyUpperAndWorkdir(t *testing.T) {
	hooks := RootfsHooks{
		IdentityResolver: fakeIdentityResolver{request: rootfs.RootfsRwLayerRequest{SnapshotKey: "snap", ContainerName: "app"}},
		MetadataResolver: fakeMetadataResolver{spec: rootfs.RootfsRwLayerSpec{VolumeName: "rootfs", Path: "containers/app"}},
		PVCResolver:      fakePVCResolver{path: "/pvc"},
		Preparer:         fakePreparer{prepared: rootfs.PreparedRootfs{UpperDir: "/pvc/containers/app/upper", WorkDir: "/pvc/containers/app/work"}},
	}
	mounts := []mount.Mount{{Type: "fuse3.fuse-overlayfs", Source: "overlay", Options: []string{
		"workdir=/snap/work",
		"upperdir=/snap/fs",
		"lowerdir=/lower-a:/lower-b",
		"uidmapping=0:100000:65536",
		"gidmapping=0:100000:65536",
		"dev",
	}}}

	rewritten, err := applyRootfsHook(context.Background(), hooks, "snap", mounts)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"workdir=/pvc/containers/app/work",
		"upperdir=/pvc/containers/app/upper",
		"lowerdir=/lower-a:/lower-b",
		"uidmapping=0:100000:65536",
		"gidmapping=0:100000:65536",
		"dev",
	}
	if got := rewritten[0].Options; !equalStrings(got, want) {
		t.Fatalf("options = %#v, want %#v", got, want)
	}
}

func TestApplyRootfsHookWritesPersistentSpecialHandoff(t *testing.T) {
	store := &fakeHandoffStore{}
	hooks := RootfsHooks{
		IdentityResolver: fakeIdentityResolver{request: rootfs.RootfsRwLayerRequest{
			SnapshotKey:             "container-id",
			PodUID:                  "pod-uid",
			ContainerName:           "app",
			PersistentSpecialMounts: true,
		}},
		MetadataResolver: fakeMetadataResolver{spec: rootfs.RootfsRwLayerSpec{VolumeName: "rootfs", Path: "containers/app"}},
		PVCResolver:      fakePVCResolver{path: "/pvc"},
		Preparer:         fakePreparer{prepared: rootfs.PreparedRootfs{UpperDir: "/pvc/upper", WorkDir: "/pvc/work"}},
		HandoffStore:     store,
	}
	mounts := []mount.Mount{{Type: "fuse3.fuse-overlayfs", Source: "overlay", Options: []string{"workdir=/snap/work", "upperdir=/snap/fs"}}}

	if _, err := applyRootfsHook(context.Background(), hooks, "container-id", mounts); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) != 1 {
		t.Fatalf("handoff writes = %d, want 1", len(store.writes))
	}
	want := rootfs.PersistentSpecialHandoff{
		Version:       rootfs.PersistentSpecialHandoffVersion,
		SnapshotKey:   "container-id",
		PodUID:        "pod-uid",
		ContainerName: "app",
		VolumeName:    "rootfs",
		PVCMountPath:  "/pvc",
	}
	if store.writes[0] != want {
		t.Fatalf("handoff = %#v, want %#v", store.writes[0], want)
	}
}

func TestApplyRootfsHookFailsWithoutPersistentSpecialHandoffStore(t *testing.T) {
	hooks := RootfsHooks{
		IdentityResolver: fakeIdentityResolver{request: rootfs.RootfsRwLayerRequest{SnapshotKey: "container-id", PodUID: "pod-uid", ContainerName: "app", PersistentSpecialMounts: true}},
		MetadataResolver: fakeMetadataResolver{spec: rootfs.RootfsRwLayerSpec{VolumeName: "rootfs", Path: "containers/app"}},
		PVCResolver:      fakePVCResolver{path: "/pvc"},
		Preparer:         fakePreparer{prepared: rootfs.PreparedRootfs{UpperDir: "/pvc/upper", WorkDir: "/pvc/work"}},
	}
	mounts := []mount.Mount{{Type: "fuse3.fuse-overlayfs", Source: "overlay", Options: []string{"workdir=/snap/work", "upperdir=/snap/fs"}}}

	if _, err := applyRootfsHook(context.Background(), hooks, "container-id", mounts); err == nil {
		t.Fatal("expected missing handoff store error")
	}
}

func TestApplyRootfsHookLeavesSidecarMountsUnchanged(t *testing.T) {
	hooks := RootfsHooks{
		IdentityResolver: fakeIdentityResolver{request: rootfs.RootfsRwLayerRequest{SnapshotKey: "snap", ContainerName: rootfs.SidecarContainerName}},
		MetadataResolver: fakeMetadataResolver{spec: rootfs.RootfsRwLayerSpec{Sidecar: true}},
		PVCResolver:      fakePVCResolver{path: "/pvc"},
		Preparer:         fakePreparer{prepared: rootfs.PreparedRootfs{UpperDir: "/pvc/upper", WorkDir: "/pvc/work"}},
	}
	mounts := []mount.Mount{{Type: "fuse3.fuse-overlayfs", Source: "overlay", Options: []string{"workdir=/snap/work", "upperdir=/snap/fs", "uidmapping=0:100000:65536"}}}

	rewritten, err := applyRootfsHook(context.Background(), hooks, "snap", mounts)
	if err != nil {
		t.Fatal(err)
	}
	if got := rewritten[0].Options; !equalStrings(got, mounts[0].Options) {
		t.Fatalf("options = %#v, want %#v", got, mounts[0].Options)
	}
}

func TestApplyRootfsHookLeavesConfiguredSidecarUnchangedWhenItsSpecIsUnavailable(t *testing.T) {
	hooks := RootfsHooks{
		IdentityResolver: fakeIdentityResolver{request: rootfs.RootfsRwLayerRequest{SnapshotKey: "snap", ContainerName: rootfs.SidecarContainerName, RootfsRwLayerAnnotation: `[{"name":"app","volumeName":"rootfs","path":"containers/app"}]`}},
		MetadataResolver: rootfs.NewSidecarMetadataResolver(fakeSidecarSpecStore{err: rootfs.ErrSidecarSpecUnavailable}),
		PVCResolver:      fakePVCResolver{path: "/pvc"},
		Preparer:         fakePreparer{prepared: rootfs.PreparedRootfs{UpperDir: "/pvc/upper", WorkDir: "/pvc/work"}},
	}
	mounts := []mount.Mount{{Type: "fuse3.fuse-overlayfs", Source: "overlay", Options: []string{"workdir=/snap/work", "upperdir=/snap/fs"}}}

	rewritten, err := applyRootfsHook(context.Background(), hooks, "snap", mounts)

	if err != nil {
		t.Fatal(err)
	}
	if got := rewritten[0].Options; !equalStrings(got, mounts[0].Options) {
		t.Fatalf("options = %#v, want %#v", got, mounts[0].Options)
	}
}

func TestMountsWithRootfsHookRewritesOnlyActiveFuseMount(t *testing.T) {
	o := &snapshotter{root: "/snapshotter", rootfsHooks: RootfsHooks{
		IdentityResolver: fakeIdentityResolver{request: rootfs.RootfsRwLayerRequest{SnapshotKey: "snap", ContainerName: "app"}},
		MetadataResolver: fakeMetadataResolver{spec: rootfs.RootfsRwLayerSpec{VolumeName: "rootfs", Path: "containers/app"}},
		PVCResolver:      fakePVCResolver{path: "/pvc"},
		Preparer:         fakePreparer{prepared: rootfs.PreparedRootfs{UpperDir: "/pvc/upper", WorkDir: "/pvc/work"}},
	}}
	s := storage.Snapshot{Kind: snapshots.KindActive, ID: "2", ParentIDs: []string{"1"}}
	info := snapshots.Info{Labels: map[string]string{
		"containerd.io/snapshot/uidmapping": "0:100000:65536",
		"containerd.io/snapshot/gidmapping": "0:100000:65536",
	}}

	mounts, err := o.mounts(context.Background(), "snap", s, info)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"workdir=/pvc/work",
		"upperdir=/pvc/upper",
		"lowerdir=/snapshotter/snapshots/1/fs",
		"nodev,uidmapping=0:100000:65536,gidmapping=0:100000:65536",
	}
	if got := mounts[0].Options; !equalStrings(got, want) {
		t.Fatalf("options = %#v, want %#v", got, want)
	}

	view := storage.Snapshot{Kind: snapshots.KindView, ID: "3", ParentIDs: []string{"2", "1"}}
	mounts, err = o.mounts(context.Background(), "snap", view, snapshots.Info{})
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range mounts[0].Options {
		if option == "upperdir=/pvc/upper" || option == "workdir=/pvc/work" {
			t.Fatalf("view snapshot was rewritten: %#v", mounts[0].Options)
		}
	}
}

type fakeIdentityResolver struct {
	request rootfs.RootfsRwLayerRequest
	err     error
}

func (f fakeIdentityResolver) ResolveIdentity(context.Context, string) (rootfs.RootfsRwLayerRequest, error) {
	return f.request, f.err
}

type fakeMetadataResolver struct {
	spec rootfs.RootfsRwLayerSpec
	err  error
}

type fakeSidecarSpecStore struct{ err error }

func (f fakeSidecarSpecStore) LoadSidecarSpec(context.Context, rootfs.RootfsRwLayerRequest) (*runtimespec.Spec, error) {
	return nil, f.err
}

func (f fakeMetadataResolver) ResolveRootfsRwLayer(context.Context, rootfs.RootfsRwLayerRequest) (rootfs.RootfsRwLayerSpec, error) {
	return f.spec, f.err
}

type fakePVCResolver struct {
	path string
	err  error
}

func (f fakePVCResolver) ResolvePVCMountPath(context.Context, rootfs.RootfsRwLayerRequest, rootfs.RootfsRwLayerSpec) (string, error) {
	return f.path, f.err
}

type fakePreparer struct {
	prepared rootfs.PreparedRootfs
	err      error
}

type fakeHandoffStore struct {
	writes  []rootfs.PersistentSpecialHandoff
	removes []string
	err     error
}

func (f *fakeHandoffStore) Write(_ context.Context, handoff rootfs.PersistentSpecialHandoff) error {
	f.writes = append(f.writes, handoff)
	return f.err
}

func (f *fakeHandoffStore) Remove(_ context.Context, snapshotKey string) error {
	f.removes = append(f.removes, snapshotKey)
	return f.err
}

func (f fakePreparer) PrepareRootfsRwLayer(context.Context, rootfs.PrepareRootfsRequest) (rootfs.PreparedRootfs, error) {
	return f.prepared, f.err
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
