package rootfs

import (
	"context"
	"fmt"

	containerdclient "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

type ContainerdSidecarSpecStore struct {
	socketPath string
}

func NewContainerdSidecarSpecStore(socketPath string) *ContainerdSidecarSpecStore {
	return &ContainerdSidecarSpecStore{socketPath: socketPath}
}

func (s *ContainerdSidecarSpecStore) LoadSidecarSpec(ctx context.Context, request RootfsRwLayerRequest) (*runtimespec.Spec, error) {
	if request.PodName == "" || request.Namespace == "" {
		return nil, ErrSidecarSpecUnavailable
	}
	client, err := containerdclient.New(s.socketPath)
	if err != nil {
		return nil, fmt.Errorf("connect containerd for sidecar spec lookup: %w", err)
	}
	defer client.Close()
	ctx = namespaces.WithNamespace(ctx, "k8s.io")
	containers, err := client.Containers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list containerd containers for sidecar spec lookup: %w", err)
	}
	for _, container := range containers {
		labels, err := container.Labels(ctx)
		if err != nil {
			return nil, fmt.Errorf("read container labels for sidecar spec lookup: %w", err)
		}
		if labels["io.kubernetes.pod.namespace"] != request.Namespace ||
			labels["io.kubernetes.pod.name"] != request.PodName ||
			labels["io.kubernetes.container.name"] != SidecarContainerName {
			continue
		}
		spec, err := container.Spec(ctx)
		if err != nil {
			return nil, fmt.Errorf("read sidecar oci spec: %w", err)
		}
		if spec == nil {
			return nil, ErrSidecarSpecMalformed
		}
		return spec, nil
	}
	return nil, ErrSidecarSpecUnavailable
}
