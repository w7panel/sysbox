package plugin_test

import (
	"testing"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/nestybox/sysbox-snapshotter/plugin"
	"github.com/stretchr/testify/require"
)

func TestNewSnapshotService_returnsService_whenSnapshotterProvided(t *testing.T) {
	var snapshotter snapshots.Snapshotter

	service := plugin.NewSnapshotService(snapshotter)

	require.NotNil(t, service)
}
