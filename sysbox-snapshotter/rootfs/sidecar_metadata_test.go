package rootfs_test

import (
	"context"
	"testing"

	"github.com/nestybox/sysbox-snapshotter/rootfs"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/require"
)

func TestSidecarMetadataResolver_returnsSpecFromSidecarEnv_whenContainerEntryExists(t *testing.T) {
	// Given
	store := fakeSidecarSpecStore{spec: &runtimespec.Spec{Process: &runtimespec.Process{Env: []string{
		`ROOTFS_RW_LAYER_SPEC={"version":1,"entries":[{"containerName":"app-a","volumeName":"rootfs-a","path":"containers/app-a","pvcClaimName":"pvc-a"},{"containerName":"app-b","volumeName":"rootfs-b","path":"containers/app-b","pvcClaimName":"pvc-b"}]}`,
	}}}}
	resolver := rootfs.NewSidecarMetadataResolver(store)

	// When
	spec, err := resolver.ResolveRootfsRwLayer(context.Background(), rootfs.RootfsRwLayerRequest{ContainerName: "app-b"})

	// Then
	require.NoError(t, err)
	require.Equal(t, rootfs.RootfsRwLayerSpec{
		VolumeName:   "rootfs-b",
		Path:         "containers/app-b",
		PVCClaimName: "pvc-b",
	}, spec)
}

func TestSidecarMetadataResolver_returnsNotConfigured_whenContainerEntryIsMissing(t *testing.T) {
	// Given
	store := fakeSidecarSpecStore{spec: &runtimespec.Spec{Process: &runtimespec.Process{Env: []string{
		`ROOTFS_RW_LAYER_SPEC={"version":1,"entries":[{"containerName":"app-a","volumeName":"rootfs-a","path":"containers/app-a","pvcClaimName":"pvc-a"}]}`,
	}}}}
	resolver := rootfs.NewSidecarMetadataResolver(store)

	// When
	_, err := resolver.ResolveRootfsRwLayer(context.Background(), rootfs.RootfsRwLayerRequest{ContainerName: "app-b"})

	// Then
	require.ErrorIs(t, err, rootfs.ErrRootfsRwLayerNotConfigured)
}

func TestSidecarMetadataResolver_returnsNotConfigured_whenSidecarSpecIsUnavailable(t *testing.T) {
	// Given
	store := fakeSidecarSpecStore{err: rootfs.ErrSidecarSpecUnavailable}
	resolver := rootfs.NewSidecarMetadataResolver(store)

	// When
	_, err := resolver.ResolveRootfsRwLayer(context.Background(), rootfs.RootfsRwLayerRequest{ContainerName: "app"})

	// Then
	require.ErrorIs(t, err, rootfs.ErrRootfsRwLayerNotConfigured)
}

func TestSidecarMetadataResolver_rejectsPathTraversal_whenSidecarIntentEscapesVolume(t *testing.T) {
	// Given
	store := fakeSidecarSpecStore{spec: &runtimespec.Spec{Process: &runtimespec.Process{Env: []string{
		`ROOTFS_RW_LAYER_SPEC={"version":1,"entries":[{"containerName":"app","volumeName":"rootfs","path":"../escape","pvcClaimName":"pvc"}]}`,
	}}}}
	resolver := rootfs.NewSidecarMetadataResolver(store)

	// When
	_, err := resolver.ResolveRootfsRwLayer(context.Background(), rootfs.RootfsRwLayerRequest{ContainerName: "app"})

	// Then
	require.Error(t, err)
	require.NotErrorIs(t, err, rootfs.ErrRootfsRwLayerNotConfigured)
}
