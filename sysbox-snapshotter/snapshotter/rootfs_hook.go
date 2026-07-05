package overlay

import (
	"context"
	"errors"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/nestybox/sysbox-snapshotter/rootfs"
)

type RootfsHooks struct {
	IdentityResolver rootfs.IdentityResolver
	MetadataResolver rootfs.MetadataResolver
	PVCResolver      rootfs.PVCMountPathResolver
	Preparer         rootfs.RootfsPreparer
}

func WithRootfsHooks(hooks RootfsHooks) Opt {
	return func(config *SnapshotterConfig) error {
		config.rootfsHooks = hooks
		return nil
	}
}

func applyRootfsHook(ctx context.Context, hooks RootfsHooks, snapshotKey string, snapshotLabels map[string]string, mounts []mount.Mount) ([]mount.Mount, error) {
	if hooks.IdentityResolver == nil || hooks.MetadataResolver == nil || hooks.Preparer == nil {
		return mounts, nil
	}
	request, err := hooks.IdentityResolver.ResolveIdentity(ctx, snapshotKey)
	if err != nil {
		return mounts, nil
	}
	request = withSnapshotMappings(request, snapshotLabels)
	spec, err := hooks.MetadataResolver.ResolveRootfsRwLayer(ctx, request)
	if errors.Is(err, rootfs.ErrRootfsRwLayerNotConfigured) {
		return mounts, nil
	}
	if err != nil {
		return nil, err
	}
	pvcMountPath := ""
	if hooks.PVCResolver != nil {
		resolved, resolveErr := hooks.PVCResolver.ResolvePVCMountPath(ctx, request, spec)
		if resolveErr != nil {
			return nil, resolveErr
		}
		pvcMountPath = resolved
	}
	prepared, err := hooks.Preparer.PrepareRootfsRwLayer(ctx, rootfs.PrepareRootfsRequest{
		SnapshotKey:   request.SnapshotKey,
		Namespace:     request.Namespace,
		PodName:       request.PodName,
		PodUID:        request.PodUID,
		ContainerName: request.ContainerName,
		VolumeName:    spec.VolumeName,
		Path:          spec.Path,
		PVCClaimName:  spec.PVCClaimName,
		PVCMountPath:  pvcMountPath,
		ImageChainID:  spec.ImageChainID,
		UIDMappings:   request.UIDMappings,
		GIDMappings:   request.GIDMappings,
	})
	if err != nil {
		return nil, err
	}
	return rewriteOverlayMounts(mounts, prepared.UpperDir, prepared.WorkDir), nil
}

func withSnapshotMappings(request rootfs.RootfsRwLayerRequest, labels map[string]string) rootfs.RootfsRwLayerRequest {
	if len(request.UIDMappings) == 0 {
		request.UIDMappings = rootfs.ParseIDMap(labels[snapshots.LabelSnapshotUIDMapping])
	}
	if len(request.GIDMappings) == 0 {
		request.GIDMappings = rootfs.ParseIDMap(labels[snapshots.LabelSnapshotGIDMapping])
	}
	return request
}

func rewriteOverlayMounts(mounts []mount.Mount, upperDir, workDir string) []mount.Mount {
	rewritten := make([]mount.Mount, len(mounts))
	for i, m := range mounts {
		next := m
		nextOptions := make([]string, len(m.Options))
		for j, option := range m.Options {
			switch {
			case m.Type == "overlay" && len(option) > len("upperdir=") && option[:len("upperdir=")] == "upperdir=":
				nextOptions[j] = "upperdir=" + upperDir
			case m.Type == "overlay" && len(option) > len("workdir=") && option[:len("workdir=")] == "workdir=":
				nextOptions[j] = "workdir=" + workDir
			default:
				nextOptions[j] = option
			}
		}
		next.Options = nextOptions
		rewritten[i] = next
	}
	return rewritten
}
