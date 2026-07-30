package admission

import (
	"fmt"
	"path/filepath"
	"strings"

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

func ensureRootfsSpecialMounts(spec *corev1.PodSpec, entries []RootfsRwLayerEntry) error {
	entryByContainer := make(map[string]RootfsRwLayerEntry, len(entries))
	for _, entry := range entries {
		entryByContainer[entry.Name] = entry
	}
	for i := range spec.Containers {
		container := &spec.Containers[i]
		if container.Name == SidecarContainerName {
			continue
		}
		entry, configured := entryByContainer[container.Name]
		target := ""
		if configured {
			target = filepath.Join(RootfsSpecialMountPath, entry.VolumeName)
		}
		found := false
		for _, mount := range container.VolumeMounts {
			cleaned := filepath.Clean(mount.MountPath)
			reserved := cleaned == RootfsSpecialMountPath || strings.HasPrefix(cleaned, RootfsSpecialMountPath+string(filepath.Separator))
			if !reserved {
				continue
			}
			if !configured || cleaned != target || mount.Name != entry.VolumeName || mount.SubPath != "" {
				return fmt.Errorf("container %s has conflicting mount at reserved path %s", container.Name, mount.MountPath)
			}
			if found {
				return fmt.Errorf("container %s has duplicate rootfs special mount", container.Name)
			}
			found = true
		}
		if configured && !found {
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{Name: entry.VolumeName, MountPath: target})
		}
	}
	return nil
}
