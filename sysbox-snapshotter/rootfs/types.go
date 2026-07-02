package rootfs

import (
	"context"
	"fmt"
)

type RootfsRwLayerRequest struct {
	SnapshotKey   string
	Namespace     string
	PodName       string
	PodUID        string
	ContainerName string
	ImageChainID  string
	UIDMappings   []IDMapping
	GIDMappings   []IDMapping
}

type IdentityResolver interface {
	ResolveIdentity(ctx context.Context, snapshotKey string) (RootfsRwLayerRequest, error)
}

type PVCMountPathResolver interface {
	ResolvePVCMountPath(ctx context.Context, request RootfsRwLayerRequest, spec RootfsRwLayerSpec) (string, error)
}

type RootfsRwLayerSpec struct {
	Namespace    string
	PodName      string
	VolumeName   string
	Path         string
	PVCClaimName string
	PVCMountPath string
	ImageChainID string
	UIDMappings  []IDMapping
	GIDMappings  []IDMapping
}

type PrepareRootfsRequest struct {
	SnapshotKey   string
	Namespace     string
	PodName       string
	PodUID        string
	ContainerName string
	VolumeName    string
	Path          string
	PVCClaimName  string
	PVCMountPath  string
	ImageChainID  string
	UIDMappings   []IDMapping
	GIDMappings   []IDMapping
}

type PreparedRootfs struct {
	UpperDir string
	WorkDir  string
}

type IDMapping struct {
	ContainerID uint32 `json:"containerID"`
	HostID      uint32 `json:"hostID"`
	Size        uint32 `json:"size"`
}

func RootHostIdentity(mappings []IDMapping) (uint32, bool) {
	for _, mapping := range mappings {
		if mapping.ContainerID == 0 {
			return mapping.HostID, true
		}
	}
	return 0, false
}

func ParseIDMap(raw string) []IDMapping {
	if raw == "" {
		return nil
	}
	var containerID, hostID, size uint64
	if _, err := fmt.Sscanf(raw, "%d:%d:%d", &containerID, &hostID, &size); err != nil {
		return nil
	}
	return []IDMapping{{ContainerID: uint32(containerID), HostID: uint32(hostID), Size: uint32(size)}}
}
