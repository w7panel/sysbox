package rootfs

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

const SidecarMountPath = "/var/lib/sysbox/rootfs-rw-volume"

var (
	ErrSidecarSpecUnavailable = errors.New("sysbox sidecar oci spec unavailable")
	ErrSidecarSpecMalformed   = errors.New("sysbox sidecar oci spec malformed")
	ErrPVCMountNotFound       = errors.New("sysbox sidecar pvc mount not found")
)

type SidecarSpecStore interface {
	LoadSidecarSpec(ctx context.Context, request RootfsRwLayerRequest) (*runtimespec.Spec, error)
}

type PVCMountPathResolverFromSidecar struct {
	store SidecarSpecStore
}

func NewPVCMountPathResolver(store SidecarSpecStore) *PVCMountPathResolverFromSidecar {
	return &PVCMountPathResolverFromSidecar{store: store}
}

func (r *PVCMountPathResolverFromSidecar) ResolvePVCMountPath(
	ctx context.Context,
	request RootfsRwLayerRequest,
	spec RootfsRwLayerSpec,
) (string, error) {
	if spec.PVCMountPath != "" {
		return spec.PVCMountPath, nil
	}
	if spec.VolumeName == "" {
		return "", fmt.Errorf("volume name is required to resolve sidecar pvc mount path: %w", ErrSidecarSpecMalformed)
	}
	sidecarSpec, err := r.store.LoadSidecarSpec(ctx, request)
	if err != nil {
		return "", err
	}
	if sidecarSpec == nil {
		return "", ErrSidecarSpecUnavailable
	}
	target := filepath.ToSlash(filepath.Join(SidecarMountPath, spec.VolumeName))
	for _, mount := range sidecarSpec.Mounts {
		if cleanOCIDestination(mount.Destination) != target {
			continue
		}
		if mount.Source == "" {
			return "", ErrSidecarSpecMalformed
		}
		return mount.Source, nil
	}
	return "", ErrPVCMountNotFound
}

func cleanOCIDestination(destination string) string {
	return filepath.ToSlash(filepath.Clean(strings.TrimSpace(destination)))
}

type ComposedPVCMountPathResolver struct {
	primary  PVCMountPathResolver
	fallback PVCMountPathResolver
}

func NewComposedPVCMountPathResolver(primary PVCMountPathResolver, fallback PVCMountPathResolver) *ComposedPVCMountPathResolver {
	return &ComposedPVCMountPathResolver{primary: primary, fallback: fallback}
}

func (r *ComposedPVCMountPathResolver) ResolvePVCMountPath(
	ctx context.Context,
	request RootfsRwLayerRequest,
	spec RootfsRwLayerSpec,
) (string, error) {
	path, err := r.primary.ResolvePVCMountPath(ctx, request, spec)
	if err == nil {
		return path, nil
	}
	if errors.Is(err, ErrSidecarSpecUnavailable) {
		return r.fallback.ResolvePVCMountPath(ctx, request, spec)
	}
	return "", err
}
