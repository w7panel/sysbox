package rootfs_test

import (
	"context"
	"testing"

	"github.com/nestybox/sysbox-snapshotter/rootfs"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/require"
)

func TestPVCMountPathResolver_resolvesHostSourceFromSidecarOCIMount_whenRequestedVolumeExists(t *testing.T) {
	// Given
	store := fakeSidecarSpecStore{spec: &runtimespec.Spec{Mounts: []runtimespec.Mount{
		{Source: "/host/pvc-a", Destination: "/var/lib/sysbox/rootfs-rw-volume/rootfs-a"},
		{Source: "/host/pvc-b", Destination: "/var/lib/sysbox/rootfs-rw-volume/rootfs-b"},
	}}}
	resolver := rootfs.NewPVCMountPathResolver(store)

	// When
	resolved, err := resolver.ResolvePVCMountPath(context.Background(), rootfs.RootfsRwLayerRequest{
		Namespace: "default",
		PodName:   "rootfs-demo",
	}, rootfs.RootfsRwLayerSpec{VolumeName: "rootfs-b"})

	// Then
	require.NoError(t, err)
	require.Equal(t, "/host/pvc-b", resolved)
}

func TestPVCMountPathResolver_returnsUnavailable_whenSidecarSpecIsUnavailable(t *testing.T) {
	// Given
	store := fakeSidecarSpecStore{err: rootfs.ErrSidecarSpecUnavailable}
	resolver := rootfs.NewPVCMountPathResolver(store)

	// When
	_, err := resolver.ResolvePVCMountPath(context.Background(), rootfs.RootfsRwLayerRequest{}, rootfs.RootfsRwLayerSpec{VolumeName: "rootfs"})

	// Then
	require.ErrorIs(t, err, rootfs.ErrSidecarSpecUnavailable)
}

func TestPVCMountPathResolver_returnsMalformed_whenSidecarMountSourceIsEmpty(t *testing.T) {
	// Given
	store := fakeSidecarSpecStore{spec: &runtimespec.Spec{Mounts: []runtimespec.Mount{{
		Destination: "/var/lib/sysbox/rootfs-rw-volume/rootfs",
	}}}}
	resolver := rootfs.NewPVCMountPathResolver(store)

	// When
	_, err := resolver.ResolvePVCMountPath(context.Background(), rootfs.RootfsRwLayerRequest{}, rootfs.RootfsRwLayerSpec{VolumeName: "rootfs"})

	// Then
	require.ErrorIs(t, err, rootfs.ErrSidecarSpecMalformed)
}

func TestPVCMountPathResolver_returnsMountMissing_whenRequestedVolumeIsAbsent(t *testing.T) {
	// Given
	store := fakeSidecarSpecStore{spec: &runtimespec.Spec{Mounts: []runtimespec.Mount{{
		Source:      "/host/pvc-a",
		Destination: "/var/lib/sysbox/rootfs-rw-volume/rootfs-a",
	}}}}
	resolver := rootfs.NewPVCMountPathResolver(store)

	// When
	_, err := resolver.ResolvePVCMountPath(context.Background(), rootfs.RootfsRwLayerRequest{}, rootfs.RootfsRwLayerSpec{VolumeName: "rootfs-b"})

	// Then
	require.ErrorIs(t, err, rootfs.ErrPVCMountNotFound)
}

type fakeSidecarSpecStore struct {
	spec *runtimespec.Spec
	err  error
}

func (f fakeSidecarSpecStore) LoadSidecarSpec(_ context.Context, _ rootfs.RootfsRwLayerRequest) (*runtimespec.Spec, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.spec, nil
}
