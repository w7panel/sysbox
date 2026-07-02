package rootfs_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
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

func TestLocalPreparer_chownsUpperAndWorkToContainerRoot_whenIDMappedFallbackActive(t *testing.T) {
	// Given
	volumeRoot := t.TempDir()
	preparer := rootfs.NewLocalPreparer()

	// When
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

	// Then
	require.NoError(t, err)
	requireOwnership(t, prepared.UpperDir, 100000, 100000)
	requireOwnership(t, prepared.WorkDir, 100000, 100000)
}

func TestLocalPreparer_reusesExistingManagedLayer_whenMetaExists(t *testing.T) {
	volumeRoot := t.TempDir()
	layerRoot := filepath.Join(volumeRoot, "containers/app")
	require.NoError(t, os.MkdirAll(filepath.Join(layerRoot, "upper"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(layerRoot, "work"), 0o711))
	writeLayerMeta(t, filepath.Join(layerRoot, "meta.json"), layerMetaFixture{
		Version:      1,
		State:        "available",
		ImageChainID: "sha256:chain-a",
		UIDMappings:  []rootfs.IDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}},
		GIDMappings:  []rootfs.IDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}},
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
		ImageChainID:  "sha256:chain-a",
		UIDMappings:   []rootfs.IDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}},
		GIDMappings:   []rootfs.IDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}},
	})

	require.NoError(t, err)
	require.Equal(t, filepath.Join(layerRoot, "upper"), prepared.UpperDir)
	require.Equal(t, filepath.Join(layerRoot, "work"), prepared.WorkDir)
	meta := readLayerMeta(t, filepath.Join(layerRoot, "meta.json"))
	require.Equal(t, "attached", meta.State)
}

func TestLocalPreparer_rejectsExistingLayer_whenImageChainDiffers(t *testing.T) {
	volumeRoot := t.TempDir()
	layerRoot := filepath.Join(volumeRoot, "containers/app")
	require.NoError(t, os.MkdirAll(filepath.Join(layerRoot, "upper"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(layerRoot, "work"), 0o711))
	writeLayerMeta(t, filepath.Join(layerRoot, "meta.json"), layerMetaFixture{
		Version:      1,
		State:        "available",
		ImageChainID: "sha256:chain-a",
	})
	preparer := rootfs.NewLocalPreparer()

	_, err := preparer.PrepareRootfsRwLayer(context.Background(), rootfs.PrepareRootfsRequest{
		SnapshotKey:  "snapshot-key",
		VolumeName:   "rootfs",
		Path:         "containers/app",
		PVCClaimName: "rootfs-rw-pvc",
		PVCMountPath: volumeRoot,
		ImageChainID: "sha256:chain-b",
	})

	require.ErrorIs(t, err, rootfs.ErrIncompatibleRootfsLayer)
}

func TestLocalPreparer_migratesExistingLayerOwnership_whenIDMappingDiffers(t *testing.T) {
	// Given
	volumeRoot := t.TempDir()
	layerRoot := filepath.Join(volumeRoot, "containers/app")
	require.NoError(t, os.MkdirAll(filepath.Join(layerRoot, "upper", "root"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(layerRoot, "upper", "root", "marker"), []byte("data"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(layerRoot, "work"), 0o711))
	writeLayerMeta(t, filepath.Join(layerRoot, "meta.json"), layerMetaFixture{
		Version:      1,
		State:        "attached",
		ImageChainID: "sha256:chain-a",
		UIDMappings:  []rootfs.IDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}},
		GIDMappings:  []rootfs.IDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}},
	})
	preparer := rootfs.NewLocalPreparer()

	// When
	prepared, err := preparer.PrepareRootfsRwLayer(context.Background(), rootfs.PrepareRootfsRequest{
		SnapshotKey:   "snapshot-key",
		PodUID:        "pod-456",
		ContainerName: "app",
		VolumeName:    "rootfs",
		Path:          "containers/app",
		PVCClaimName:  "rootfs-rw-pvc",
		PVCMountPath:  volumeRoot,
		ImageChainID:  "sha256:chain-a",
		UIDMappings:   []rootfs.IDMapping{{ContainerID: 0, HostID: 200000, Size: 65536}},
		GIDMappings:   []rootfs.IDMapping{{ContainerID: 0, HostID: 200001, Size: 65536}},
	})

	// Then
	require.NoError(t, err)
	require.Equal(t, filepath.Join(layerRoot, "upper"), prepared.UpperDir)
	requireOwnership(t, filepath.Join(layerRoot, "upper", "root", "marker"), 200000, 200001)
	requireOwnership(t, filepath.Join(layerRoot, "work"), 200000, 200001)
	meta := readLayerMeta(t, filepath.Join(layerRoot, "meta.json"))
	require.Equal(t, []rootfs.IDMapping{{ContainerID: 0, HostID: 200000, Size: 65536}}, meta.UIDMappings)
	require.Equal(t, []rootfs.IDMapping{{ContainerID: 0, HostID: 200001, Size: 65536}}, meta.GIDMappings)
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

type layerMetaFixture struct {
	Version      int                `json:"version"`
	State        string             `json:"state"`
	ImageChainID string             `json:"imageChainID"`
	UIDMappings  []rootfs.IDMapping `json:"uidMappings"`
	GIDMappings  []rootfs.IDMapping `json:"gidMappings"`
}

func requireOwnership(t *testing.T, path string, uid uint32, gid uint32) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	stat := info.Sys().(*syscall.Stat_t)
	require.Equal(t, uid, stat.Uid)
	require.Equal(t, gid, stat.Gid)
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
