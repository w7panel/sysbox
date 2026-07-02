package rootfs_test

import (
	"context"
	"errors"
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

func TestComposedPVCMountPathResolver_usesPrimaryResolver_whenSidecarDataIsAvailable(t *testing.T) {
	// Given
	resolver := rootfs.NewComposedPVCMountPathResolver(
		fakePVCResolver{path: "/from-sidecar"},
		fakePVCResolver{path: "/from-kubelet"},
	)

	// When
	resolved, err := resolver.ResolvePVCMountPath(context.Background(), rootfs.RootfsRwLayerRequest{}, rootfs.RootfsRwLayerSpec{})

	// Then
	require.NoError(t, err)
	require.Equal(t, "/from-sidecar", resolved)
}

func TestComposedPVCMountPathResolver_fallsBackToKubeletOnlyWhenSidecarDataIsUnavailable(t *testing.T) {
	// Given
	resolver := rootfs.NewComposedPVCMountPathResolver(
		fakePVCResolver{err: rootfs.ErrSidecarSpecUnavailable},
		fakePVCResolver{path: "/from-kubelet"},
	)

	// When
	resolved, err := resolver.ResolvePVCMountPath(context.Background(), rootfs.RootfsRwLayerRequest{}, rootfs.RootfsRwLayerSpec{})

	// Then
	require.NoError(t, err)
	require.Equal(t, "/from-kubelet", resolved)
}

func TestComposedPVCMountPathResolver_doesNotFallback_whenSidecarDataIsMalformed(t *testing.T) {
	// Given
	resolver := rootfs.NewComposedPVCMountPathResolver(
		fakePVCResolver{err: rootfs.ErrSidecarSpecMalformed},
		fakePVCResolver{path: "/from-kubelet"},
	)

	// When
	_, err := resolver.ResolvePVCMountPath(context.Background(), rootfs.RootfsRwLayerRequest{}, rootfs.RootfsRwLayerSpec{})

	// Then
	require.ErrorIs(t, err, rootfs.ErrSidecarSpecMalformed)
}

func TestComposedPVCMountPathResolver_doesNotFallback_whenSidecarMountIsMissing(t *testing.T) {
	// Given
	resolver := rootfs.NewComposedPVCMountPathResolver(
		fakePVCResolver{err: rootfs.ErrPVCMountNotFound},
		fakePVCResolver{path: "/from-kubelet"},
	)

	// When
	_, err := resolver.ResolvePVCMountPath(context.Background(), rootfs.RootfsRwLayerRequest{}, rootfs.RootfsRwLayerSpec{})

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

type fakePVCResolver struct {
	path string
	err  error
}

func (f fakePVCResolver) ResolvePVCMountPath(_ context.Context, _ rootfs.RootfsRwLayerRequest, _ rootfs.RootfsRwLayerSpec) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if f.path == "" {
		return "", errors.New("fake pvc resolver path missing")
	}
	return f.path, nil
}
