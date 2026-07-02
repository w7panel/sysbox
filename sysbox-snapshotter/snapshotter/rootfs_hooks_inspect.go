package overlay

import "github.com/containerd/containerd/v2/core/snapshots"

func HasRootfsHooks(sn snapshots.Snapshotter) bool {
	overlaySnapshotter, ok := sn.(*snapshotter)
	return ok && overlaySnapshotter.rootfsHooks.IdentityResolver != nil && overlaySnapshotter.rootfsHooks.MetadataResolver != nil && overlaySnapshotter.rootfsHooks.Preparer != nil
}
