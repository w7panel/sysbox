package admission

const (
	SidecarMountPath     = "/var/lib/sysbox/rootfs-rw-volume"
	SidecarContainerName = "sysbox-rootfs"
	SpecEnv              = "ROOTFS_RW_LAYER_SPEC"
)

type SidecarIntent struct {
	Version int                  `json:"version"`
	Entries []SidecarIntentEntry `json:"entries"`
}

type SidecarIntentEntry struct {
	ContainerName string `json:"containerName"`
	VolumeName    string `json:"volumeName"`
	Path          string `json:"path"`
	PVCClaimName  string `json:"pvcClaimName"`
}
