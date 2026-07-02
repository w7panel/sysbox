//go:build linux

package overlay

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/nestybox/sysbox-snapshotter/rootfs"
	"github.com/stretchr/testify/require"
)

func TestApplyRootfsHook_rewritesOverlayMounts_fromSidecarIntentAndKubeletPVCRoot(t *testing.T) {
	// Given
	kubeletPodsRoot := t.TempDir()
	pvcRoot := filepath.Join(kubeletPodsRoot, "pod-uid-123", "volumes", "kubernetes.io~local-volume", "pv-rootfs")
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
		PVCResolver: rootfs.NewKubeletPVCMountPathResolver(kubeletPodsRoot),
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
