package rootfs

const (
	// SidecarMountPath is the fixed in-container path where the rootfs sidecar mounts PVC roots.
	SidecarMountPath = "/var/lib/sysbox/rootfs-rw-volume"
	// SidecarContainerName is the canonical Kubernetes container name for the rootfs sidecar.
	SidecarContainerName = "sysbox-rootfs"
	// SpecEnv stores the sidecar intent JSON in the sidecar OCI process environment.
	SpecEnv = "ROOTFS_RW_LAYER_SPEC"
)

// Intent is the JSON contract written by sysbox-admission and read by sysbox-snapshotter.
type Intent struct {
	Version int           `json:"version"`
	Entries []IntentEntry `json:"entries"`
}

// IntentEntry maps one workload container to its PVC-backed rootfs rw-layer intent.
type IntentEntry struct {
	ContainerName string `json:"containerName"`
	VolumeName    string `json:"volumeName"`
	Path          string `json:"path"`
	PVCClaimName  string `json:"pvcClaimName"`
}
