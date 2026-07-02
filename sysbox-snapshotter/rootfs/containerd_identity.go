package rootfs

import (
	"context"
	"fmt"
	"path"

	containerdclient "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/namespaces"
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
	}
	if err != nil {
		return RootfsRwLayerRequest{}, err
	}
	labels, err := container.Labels(ctx)
	if err != nil {
		return RootfsRwLayerRequest{}, err
	}
	return RootfsRwLayerRequest{
		SnapshotKey:   snapshotKey,
		Namespace:     labels["io.kubernetes.pod.namespace"],
		PodName:       labels["io.kubernetes.pod.name"],
		PodUID:        labels["io.kubernetes.pod.uid"],
		ContainerName: labels["io.kubernetes.container.name"],
		UIDMappings:   ParseIDMap(labels[snapshots.LabelSnapshotUIDMapping]),
		GIDMappings:   ParseIDMap(labels[snapshots.LabelSnapshotGIDMapping]),
	}, nil
}
