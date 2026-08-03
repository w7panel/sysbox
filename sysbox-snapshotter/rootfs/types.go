package rootfs

import (
	"context"
	"errors"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

var ErrRootfsRwLayerNotConfigured = errors.New("rootfs rw-layer not configured")

const (
	AnnotationRootfsRwLayer = "sysbox/rootfs-rw-layer"
	SidecarMountPath        = "/var/lib/sysbox/rootfs-rw-volume"
	SidecarContainerName    = "sysbox-rootfs"
)

type RootfsRwLayerRequest struct {
	SnapshotKey             string
	PodUID                  string
	ContainerName           string
	RootfsRwLayerAnnotation string
}

type IdentityResolver interface {
	ResolveIdentity(ctx context.Context, snapshotKey string) (RootfsRwLayerRequest, error)
}

type MetadataResolver interface {
	ResolveRootfsRwLayer(ctx context.Context, request RootfsRwLayerRequest) (RootfsRwLayerSpec, error)
}

type PVCMountPathResolver interface {
	ResolvePVCMountPath(ctx context.Context, request RootfsRwLayerRequest, spec RootfsRwLayerSpec) (string, error)
}

type RootfsPreparer interface {
	PrepareRootfsRwLayer(ctx context.Context, request PrepareRootfsRequest) (PreparedRootfs, error)
}

type RootfsRwLayerSpec struct {
	VolumeName              string
	Path                    string
	PersistentSpecialMounts bool
	SpecialPath             []string
	Sidecar                 bool
	sidecarSpec             *runtimespec.Spec
}

type PrepareRootfsRequest struct {
	SnapshotKey   string
	PodUID        string
	ContainerName string
	Path          string
	PVCMountPath  string
}

type PreparedRootfs struct {
	UpperDir string
	WorkDir  string
}

type PersistentSpecialHandoff struct {
	Version       int    `json:"version"`
	SnapshotKey   string `json:"snapshotKey"`
	PodUID        string `json:"podUID"`
	ContainerName string `json:"containerName"`
	VolumeName    string `json:"volumeName"`
	PVCMountPath  string `json:"pvcMountPath"`
}

type PersistentSpecialHandoffStore interface {
	Write(context.Context, PersistentSpecialHandoff) error
	Remove(context.Context, string) error
}

type Intent struct {
	Entries []IntentEntry `json:"entries"`
}

type IntentEntry struct {
	ContainerName           string   `json:"containerName"`
	VolumeName              string   `json:"volumeName"`
	Path                    string   `json:"path"`
	PersistentSpecialMounts bool     `json:"persistentSpecialMounts,omitempty"`
	SpecialPath             []string `json:"specialPath,omitempty"`
	PVCClaimName            string   `json:"pvcClaimName"`
}
