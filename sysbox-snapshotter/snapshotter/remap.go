//go:build linux

package overlay

import (
	"fmt"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/nestybox/sysbox-snapshotter/internal/userns"
)

type remapMode int

const (
	remapModeDisabled remapMode = iota
	remapModeIDMapped
)

func remapModeOf(sn snapshots.Snapshotter) remapMode {
	overlaySnapshotter, ok := sn.(*snapshotter)
	if !ok {
		return remapModeDisabled
	}
	return overlaySnapshotter.remapMode
}

func remapOptions(mode remapMode, labels map[string]string) ([]string, error) {
	if mode != remapModeIDMapped {
		return nil, nil
	}

	uidmap, hasUIDMap := labels[snapshots.LabelSnapshotUIDMapping]
	gidmap, hasGIDMap := labels[snapshots.LabelSnapshotGIDMapping]
	if hasUIDMap != hasGIDMap {
		return nil, fmt.Errorf("idmapped snapshot requires both %s and %s labels", snapshots.LabelSnapshotUIDMapping, snapshots.LabelSnapshotGIDMapping)
	}
	if !hasUIDMap {
		return nil, nil
	}

	return []string{fmt.Sprintf("uidmap=%s", uidmap), fmt.Sprintf("gidmap=%s", gidmap)}, nil
}

type snapshotOwner struct {
	uid int
	gid int
}

func fallbackChownOwner(mode remapMode, labels map[string]string) (snapshotOwner, bool, error) {
	if mode != remapModeDisabled {
		return snapshotOwner{}, false, nil
	}

	uidmap, hasUIDMap := labels[snapshots.LabelSnapshotUIDMapping]
	gidmap, hasGIDMap := labels[snapshots.LabelSnapshotGIDMapping]
	if hasUIDMap != hasGIDMap {
		return snapshotOwner{}, false, fmt.Errorf("fallback chown snapshot requires both %s and %s labels", snapshots.LabelSnapshotUIDMapping, snapshots.LabelSnapshotGIDMapping)
	}
	if !hasUIDMap {
		return snapshotOwner{}, false, nil
	}

	var idMap userns.IDMap
	if err := idMap.Unmarshal(uidmap, gidmap); err != nil {
		return snapshotOwner{}, false, fmt.Errorf("failed to unmarshal fallback chown snapshot labels: %w", err)
	}
	root, err := idMap.RootPair()
	if err != nil {
		return snapshotOwner{}, false, fmt.Errorf("failed to find fallback chown root pair: %w", err)
	}
	return snapshotOwner{uid: int(root.Uid), gid: int(root.Gid)}, true, nil
}

func idmappedSnapshotOwner(mode remapMode, labels map[string]string) (snapshotOwner, bool, error) {
	if mode != remapModeIDMapped {
		return snapshotOwner{}, false, nil
	}

	uidmap, hasUIDMap := labels[snapshots.LabelSnapshotUIDMapping]
	gidmap, hasGIDMap := labels[snapshots.LabelSnapshotGIDMapping]
	if hasUIDMap != hasGIDMap {
		return snapshotOwner{}, false, fmt.Errorf("idmapped snapshot requires both %s and %s labels", snapshots.LabelSnapshotUIDMapping, snapshots.LabelSnapshotGIDMapping)
	}
	if !hasUIDMap {
		return snapshotOwner{}, false, nil
	}

	var idMap userns.IDMap
	if err := idMap.Unmarshal(uidmap, gidmap); err != nil {
		return snapshotOwner{}, false, fmt.Errorf("failed to unmarshal snapshot ID mapped labels: %w", err)
	}
	root, err := idMap.RootPair()
	if err != nil {
		return snapshotOwner{}, false, fmt.Errorf("failed to find root pair: %w", err)
	}
	return snapshotOwner{uid: int(root.Uid), gid: int(root.Gid)}, true, nil
}
