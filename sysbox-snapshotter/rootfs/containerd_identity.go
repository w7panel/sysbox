package rootfs

import (
	"context"
	"errors"
	"fmt"
	"path"

	containerdclient "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
)

var (
	ErrContainerIdentityUnavailable = errors.New("containerd CRI container identity unavailable")
	ErrContainerIdentityIncomplete  = errors.New("containerd CRI container identity is incomplete")
)

type ContainerdIdentityResolver struct {
	socketPath string
}

func NewContainerdIdentityResolver(socketPath string) *ContainerdIdentityResolver {
	return &ContainerdIdentityResolver{socketPath: socketPath}
}

func (r *ContainerdIdentityResolver) ResolveIdentity(ctx context.Context, snapshotKey string) (RootfsRwLayerRequest, error) {
	client, err := containerdclient.New(r.socketPath)
	if err != nil {
		return RootfsRwLayerRequest{}, fmt.Errorf("connect containerd for identity lookup: %w", err)
	}
	defer client.Close()
	ctx = namespaces.WithNamespace(ctx, "k8s.io")
	lookupKeys := []string{snapshotKey}
	baseKey := path.Base(snapshotKey)
	if baseKey != snapshotKey {
		lookupKeys = append(lookupKeys, baseKey)
	}
	var container containerdclient.Container
	for _, lookupKey := range lookupKeys {
		container, err = client.LoadContainer(ctx, lookupKey)
		if err == nil {
			snapshotKey = lookupKey
			break
		}
		if !errdefs.IsNotFound(err) {
			return RootfsRwLayerRequest{}, fmt.Errorf("load container %q for identity lookup: %w", lookupKey, err)
		}
	}
	if err != nil {
		return RootfsRwLayerRequest{}, fmt.Errorf("container %q not found for identity lookup: %w", snapshotKey, ErrContainerIdentityUnavailable)
	}
	labels, err := container.Labels(ctx)
	if err != nil {
		return RootfsRwLayerRequest{}, fmt.Errorf("read container labels for identity lookup: %w", err)
	}
	request, err := rootfsRwLayerRequestFromLabels(snapshotKey, labels)
	if err != nil {
		return RootfsRwLayerRequest{}, err
	}
	return request, nil
}

func rootfsRwLayerRequestFromLabels(snapshotKey string, labels map[string]string) (RootfsRwLayerRequest, error) {
	podUID := labels["io.kubernetes.pod.uid"]
	if podUID == "" {
		return RootfsRwLayerRequest{}, fmt.Errorf("missing io.kubernetes.pod.uid label: %w", ErrContainerIdentityIncomplete)
	}
	containerName := labels["io.kubernetes.container.name"]
	if containerName == "" {
		return RootfsRwLayerRequest{}, fmt.Errorf("missing io.kubernetes.container.name label: %w", ErrContainerIdentityUnavailable)
	}
	return RootfsRwLayerRequest{
		SnapshotKey:   snapshotKey,
		Namespace:     labels["io.kubernetes.pod.namespace"],
		PodName:       labels["io.kubernetes.pod.name"],
		PodUID:        podUID,
		ContainerName: containerName,
		UIDMappings:   ParseIDMap(labels[snapshots.LabelSnapshotUIDMapping]),
		GIDMappings:   ParseIDMap(labels[snapshots.LabelSnapshotGIDMapping]),
	}, nil
}
