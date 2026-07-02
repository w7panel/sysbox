package plugin

import (
	snapshotsapi "github.com/containerd/containerd/api/services/snapshots/v1"
	"github.com/containerd/containerd/v2/contrib/snapshotservice"
	"github.com/containerd/containerd/v2/core/snapshots"
)

func NewSnapshotService(snapshotter snapshots.Snapshotter) snapshotsapi.SnapshotsServer {
	return snapshotservice.FromSnapshotter(snapshotter)
}
