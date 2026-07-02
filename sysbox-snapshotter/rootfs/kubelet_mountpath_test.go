package rootfs_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nestybox/sysbox-snapshotter/rootfs"
	"github.com/stretchr/testify/require"
)

func TestKubeletPVCMountPathResolver_resolvesPVCRoot_whenLayerPathDoesNotExist(t *testing.T) {
	// Given
	root := t.TempDir()
	mountPath := filepath.Join(root, "pod-123", "volumes", "kubernetes.io~local-volume", "pvc-abc")
	require.NoError(t, os.MkdirAll(mountPath, 0o755))
	resolver := rootfs.NewKubeletPVCMountPathResolver(root)

	// When
	resolved, err := resolver.ResolvePVCMountPath(context.Background(), rootfs.RootfsRwLayerRequest{PodUID: "pod-123"}, rootfs.RootfsRwLayerSpec{PVCClaimName: "claim-a", Path: "containers/main"})

	// Then
	require.NoError(t, err)
	require.Equal(t, mountPath, resolved)
	require.NoDirExists(t, filepath.Join(mountPath, "containers/main"))
}

func TestKubeletPVCMountPathResolver_ignoresProjectedVolumes_whenResolvingPVCRoot(t *testing.T) {
	// Given
	root := t.TempDir()
	projectedPath := filepath.Join(root, "pod-123", "volumes", "kubernetes.io~projected", "kube-api-access")
	mountPath := filepath.Join(root, "pod-123", "volumes", "kubernetes.io~local-volume", "pvc-abc")
	require.NoError(t, os.MkdirAll(projectedPath, 0o755))
	require.NoError(t, os.MkdirAll(mountPath, 0o755))
	resolver := rootfs.NewKubeletPVCMountPathResolver(root)

	// When
	resolved, err := resolver.ResolvePVCMountPath(context.Background(), rootfs.RootfsRwLayerRequest{PodUID: "pod-123"}, rootfs.RootfsRwLayerSpec{PVCClaimName: "claim-a", Path: "containers/main"})

	// Then
	require.NoError(t, err)
	require.Equal(t, mountPath, resolved)
}

func TestKubeletPVCMountPathResolver_returnsExplicitPVCMountPath_whenProvided(t *testing.T) {
	// Given
	resolver := rootfs.NewKubeletPVCMountPathResolver(t.TempDir())

	// When
	resolved, err := resolver.ResolvePVCMountPath(context.Background(), rootfs.RootfsRwLayerRequest{}, rootfs.RootfsRwLayerSpec{PVCMountPath: "/pvc/root"})

	// Then
	require.NoError(t, err)
	require.Equal(t, "/pvc/root", resolved)
}

func TestKubeletPVCMountPathResolver_returnsError_whenMultipleAccessibleNonProjectedCandidatesExist(t *testing.T) {
	// Given
	root := t.TempDir()
	firstMountPath := filepath.Join(root, "pod-123", "volumes", "kubernetes.io~local-volume", "pvc-a")
	secondMountPath := filepath.Join(root, "pod-123", "volumes", "kubernetes.io~csi", "pvc-b")
	require.NoError(t, os.MkdirAll(firstMountPath, 0o755))
	require.NoError(t, os.MkdirAll(secondMountPath, 0o755))
	resolver := rootfs.NewKubeletPVCMountPathResolver(root)

	// When
	_, err := resolver.ResolvePVCMountPath(context.Background(), rootfs.RootfsRwLayerRequest{PodUID: "pod-123"}, rootfs.RootfsRwLayerSpec{PVCClaimName: "claim-a"})

	// Then
	require.ErrorContains(t, err, "multiple kubelet pvc mount path candidates")
}
