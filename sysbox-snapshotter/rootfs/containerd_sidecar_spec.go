package rootfs

import (
	"context"
	"errors"
	"fmt"
	"time"

	containerdclient "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

type ContainerdSidecarSpecStore struct{ socketPath string }

func NewContainerdSidecarSpecStore(socketPath string) *ContainerdSidecarSpecStore {
	return &ContainerdSidecarSpecStore{socketPath: socketPath}
}

func (s *ContainerdSidecarSpecStore) LoadSidecarSpec(ctx context.Context, request RootfsRwLayerRequest) (*runtimespec.Spec, error) {
	if request.PodUID == "" {
		return nil, ErrSidecarSpecUnavailable
	}
	client, err := containerdclient.New(s.socketPath)
	if err != nil {
		return nil, fmt.Errorf("connect containerd for sidecar spec lookup: %w", err)
	}
	defer client.Close()
	ctx = namespaces.WithNamespace(ctx, "k8s.io")
	// containerd creates the app task immediately after the pause sidecar. The
	// sidecar container record can lag that request by a few scheduler ticks.
	// Wait only for this explicit transient condition; malformed/ambiguous
	// specs and containerd failures remain fail-fast.
	for attempt := 0; attempt < 20; attempt++ {
		spec, err := s.loadOnce(ctx, client, request)
		if !errors.Is(err, ErrSidecarSpecUnavailable) {
			return spec, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil, ErrSidecarSpecUnavailable
}

func (s *ContainerdSidecarSpecStore) loadOnce(ctx context.Context, client *containerdclient.Client, request RootfsRwLayerRequest) (*runtimespec.Spec, error) {
	containers, err := client.Containers(ctx, sidecarContainerFilters(request)...)
	if err != nil {
		return nil, fmt.Errorf("list containerd containers for sidecar spec lookup: %w", err)
	}
	specs := make([]*runtimespec.Spec, 0, len(containers))
	for _, container := range containers {
		spec, err := container.Spec(ctx)
		if err != nil {
			return nil, fmt.Errorf("read sidecar oci spec: %w", err)
		}
		if spec == nil {
			return nil, ErrSidecarSpecMalformed
		}
		specs = append(specs, spec)
	}
	return uniqueSidecarSpec(specs)
}

func uniqueSidecarSpec(specs []*runtimespec.Spec) (*runtimespec.Spec, error) {
	switch len(specs) {
	case 0:
		return nil, ErrSidecarSpecUnavailable
	case 1:
		return specs[0], nil
	default:
		return nil, ErrSidecarSpecAmbiguous
	}
}

func sidecarContainerFilters(request RootfsRwLayerRequest) []string {
	if request.PodUID == "" {
		return nil
	}
	return []string{fmt.Sprintf(`labels."io.kubernetes.pod.uid"==%s,labels."io.kubernetes.container.name"==%s`, request.PodUID, SidecarContainerName)}
}
