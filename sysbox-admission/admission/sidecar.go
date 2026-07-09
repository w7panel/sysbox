package admission

import (
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
)

func ensureSidecar(spec *corev1.PodSpec, entries []RootfsRwLayerEntry, sandboxImage string) error {
	sidecar, err := canonicalSidecar(spec, entries, sandboxImage)
	if err != nil {
		return err
	}
	containers := make([]corev1.Container, 0, len(spec.Containers)+1)
	for _, container := range spec.Containers {
		if container.Name != SidecarContainerName {
			containers = append(containers, container)
		}
	}
	containers = append(containers, sidecar)
	spec.Containers = containers
	return nil
}

func canonicalSidecar(spec *corev1.PodSpec, entries []RootfsRwLayerEntry, sandboxImage string) (corev1.Container, error) {
	return corev1.Container{
		Name:         SidecarContainerName,
		Image:        sandboxImage,
		VolumeMounts: requiredSidecarMounts(entries),
	}, nil
}

func requiredSidecarMounts(entries []RootfsRwLayerEntry) []corev1.VolumeMount {
	seenVolumes := map[string]struct{}{}
	volumeMounts := []corev1.VolumeMount{}
	for _, entry := range entries {
		if _, ok := seenVolumes[entry.VolumeName]; ok {
			continue
		}
		seenVolumes[entry.VolumeName] = struct{}{}
		volumeMounts = append(volumeMounts, corev1.VolumeMount{Name: entry.VolumeName, MountPath: filepath.Join(SidecarMountPath, entry.VolumeName)})
	}
	return volumeMounts
}
