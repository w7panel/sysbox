package rootfs

import (
	"context"
	"fmt"

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
