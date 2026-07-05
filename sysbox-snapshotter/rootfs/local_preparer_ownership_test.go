package rootfs_test

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/nestybox/sysbox-snapshotter/rootfs"
	"github.com/stretchr/testify/require"
)

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

func TestLocalPreparer_skipsOwnershipMigration_whenExistingLayerMappingsMatch(t *testing.T) {
	// Given
	volumeRoot := t.TempDir()
	layerRoot := filepath.Join(volumeRoot, "containers/app")
	uidMappings := []rootfs.IDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}}
	gidMappings := []rootfs.IDMapping{{ContainerID: 0, HostID: 100000, Size: 65536}}
	require.NoError(t, os.MkdirAll(filepath.Join(layerRoot, "upper"), 0o755))
	require.NoError(t, os.Symlink("missing-target", filepath.Join(layerRoot, "upper", "dangling-link")))
	require.NoError(t, os.MkdirAll(filepath.Join(layerRoot, "work"), 0o711))
	writeLayerMeta(t, filepath.Join(layerRoot, "meta.json"), layerMetaFixture{
		Version:      1,
		State:        "attached",
		ImageChainID: "sha256:chain-a",
		UIDMappings:  uidMappings,
		GIDMappings:  gidMappings,
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
		UIDMappings:   uidMappings,
		GIDMappings:   gidMappings,
	})

	// Then
	require.NoError(t, err)
	require.Equal(t, filepath.Join(layerRoot, "upper"), prepared.UpperDir)
	require.Equal(t, filepath.Join(layerRoot, "work"), prepared.WorkDir)
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

func requireOwnership(t *testing.T, path string, uid uint32, gid uint32) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	stat := info.Sys().(*syscall.Stat_t)
	require.Equal(t, uid, stat.Uid)
	require.Equal(t, gid, stat.Gid)
}
