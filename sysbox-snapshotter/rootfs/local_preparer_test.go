package rootfs_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nestybox/sysbox-snapshotter/rootfs"
	"github.com/stretchr/testify/require"
)

func TestLocalPreparer_createsUpperWorkAndMeta_whenLayerIsNew(t *testing.T) {
	volumeRoot := t.TempDir()
	preparer := rootfs.NewLocalPreparer()

	prepared, err := preparer.PrepareRootfsRwLayer(context.Background(), rootfs.PrepareRootfsRequest{
		SnapshotKey:   "snapshot-key",
		PodUID:        "pod-123",
		ContainerName: "app",
		VolumeName:    "rootfs",
		Path:          "containers/app",
		PVCClaimName:  "rootfs-rw-pvc",
		PVCMountPath:  volumeRoot,
	})

	require.NoError(t, err)
	require.Equal(t, filepath.Join(volumeRoot, "containers/app/upper"), prepared.UpperDir)
	require.Equal(t, filepath.Join(volumeRoot, "containers/app/work"), prepared.WorkDir)
	require.DirExists(t, prepared.UpperDir)
	require.DirExists(t, prepared.WorkDir)
	meta := readLayerMeta(t, filepath.Join(volumeRoot, "containers/app/meta.json"))
	require.Equal(t, "attached", meta.State)
}

func TestLocalPreparer_reusesExistingManagedLayer_whenMetaExists(t *testing.T) {
	volumeRoot := t.TempDir()
	layerRoot := filepath.Join(volumeRoot, "containers/app")
	require.NoError(t, os.MkdirAll(filepath.Join(layerRoot, "upper"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(layerRoot, "work"), 0o711))
	writeLayerMeta(t, filepath.Join(layerRoot, "meta.json"), layerMetaFixture{
		Version:     1,
		State:       "available",
		UIDMappings: []rootfs.IDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}},
		GIDMappings: []rootfs.IDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}},
	})
	preparer := rootfs.NewLocalPreparer()

	prepared, err := preparer.PrepareRootfsRwLayer(context.Background(), rootfs.PrepareRootfsRequest{
		SnapshotKey:   "snapshot-key",
		PodUID:        "pod-123",
		ContainerName: "app",
		VolumeName:    "rootfs",
		Path:          "containers/app",
		PVCClaimName:  "rootfs-rw-pvc",
		PVCMountPath:  volumeRoot,
		UIDMappings:   []rootfs.IDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}},
		GIDMappings:   []rootfs.IDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}},
	})

	require.NoError(t, err)
	require.Equal(t, filepath.Join(layerRoot, "upper"), prepared.UpperDir)
	require.Equal(t, filepath.Join(layerRoot, "work"), prepared.WorkDir)
	meta := readLayerMeta(t, filepath.Join(layerRoot, "meta.json"))
	require.Equal(t, "attached", meta.State)
}

func TestLocalPreparer_reusesExistingLayer_whenImageChainDiffers(t *testing.T) {
	volumeRoot := t.TempDir()
	layerRoot := filepath.Join(volumeRoot, "containers/app")
	require.NoError(t, os.MkdirAll(filepath.Join(layerRoot, "upper"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(layerRoot, "work"), 0o711))
	writeLayerMeta(t, filepath.Join(layerRoot, "meta.json"), layerMetaFixture{
		Version: 1,
		State:   "available",
	})
	preparer := rootfs.NewLocalPreparer()

	prepared, err := preparer.PrepareRootfsRwLayer(context.Background(), rootfs.PrepareRootfsRequest{
		SnapshotKey:   "snapshot-key",
		PodUID:        "pod-123",
		ContainerName: "app",
		VolumeName:    "rootfs",
		Path:          "containers/app",
		PVCClaimName:  "rootfs-rw-pvc",
		PVCMountPath:  volumeRoot,
	})

	require.NoError(t, err)
	require.Equal(t, filepath.Join(layerRoot, "upper"), prepared.UpperDir)
	require.Equal(t, filepath.Join(layerRoot, "work"), prepared.WorkDir)
}

func TestLocalPreparer_rejectsNonEmptyUnmanagedLayer(t *testing.T) {
	volumeRoot := t.TempDir()
	layerRoot := filepath.Join(volumeRoot, "containers/app")
	require.NoError(t, os.MkdirAll(layerRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(layerRoot, "foreign-file"), []byte("data"), 0o600))
	preparer := rootfs.NewLocalPreparer()

	_, err := preparer.PrepareRootfsRwLayer(context.Background(), rootfs.PrepareRootfsRequest{
		SnapshotKey:   "snapshot-key",
		PodUID:        "pod-123",
		ContainerName: "app",
		VolumeName:    "rootfs",
		Path:          "containers/app",
		PVCClaimName:  "rootfs-rw-pvc",
		PVCMountPath:  volumeRoot,
	})

	require.ErrorIs(t, err, rootfs.ErrUnmanagedRootfsLayer)
}

func TestLocalPreparer_rejectsLayerPathTraversal(t *testing.T) {
	preparer := rootfs.NewLocalPreparer()

	_, err := preparer.PrepareRootfsRwLayer(context.Background(), rootfs.PrepareRootfsRequest{
		SnapshotKey:   "snapshot-key",
		PodUID:        "pod-123",
		ContainerName: "app",
		VolumeName:    "rootfs",
		Path:          "../escape",
		PVCClaimName:  "rootfs-rw-pvc",
		PVCMountPath:  t.TempDir(),
	})

	require.ErrorIs(t, err, rootfs.ErrUnsafeLayerPath)
}

func TestLocalPreparer_rejectsSymlinkLayerPath(t *testing.T) {
	volumeRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(volumeRoot, "containers"), 0o755))
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(volumeRoot, "containers/app")))
	preparer := rootfs.NewLocalPreparer()

	_, err := preparer.PrepareRootfsRwLayer(context.Background(), rootfs.PrepareRootfsRequest{
		SnapshotKey:   "snapshot-key",
		PodUID:        "pod-123",
		ContainerName: "app",
		VolumeName:    "rootfs",
		Path:          "containers/app",
		PVCClaimName:  "rootfs-rw-pvc",
		PVCMountPath:  volumeRoot,
	})

	require.ErrorIs(t, err, rootfs.ErrUnsafeLayerPath)
}

type layerMetaFixture struct {
	Version     int                `json:"version"`
	State       string             `json:"state"`
	UIDMappings []rootfs.IDMapping `json:"uidMappings"`
	GIDMappings []rootfs.IDMapping `json:"gidMappings"`
}

func writeLayerMeta(t *testing.T, path string, meta layerMetaFixture) {
	t.Helper()
	data, err := json.Marshal(meta)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

func readLayerMeta(t *testing.T, path string) layerMetaFixture {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var meta layerMetaFixture
	require.NoError(t, json.Unmarshal(data, &meta))
	return meta
}
