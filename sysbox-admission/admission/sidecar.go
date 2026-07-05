package admission

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
)

func ensureSidecar(spec *corev1.PodSpec, entries []RootfsRwLayerEntry, sandboxImage string) error {
	sidecar, err := canonicalSidecar(spec, entries, sandboxImage)
	if err != nil {
		return err
	}
	containers := make([]corev1.Container, 0, len(spec.Containers)+1)
	containers = append(containers, sidecar)
	for _, container := range spec.Containers {
		if container.Name != SidecarContainerName {
			containers = append(containers, container)
		}
	}
	spec.Containers = containers
	return nil
}

func canonicalSidecar(spec *corev1.PodSpec, entries []RootfsRwLayerEntry, sandboxImage string) (corev1.Container, error) {
	intent, err := sidecarIntent(spec, entries)
	if err != nil {
		return corev1.Container{}, err
	}
	return corev1.Container{
		Name:         SidecarContainerName,
		Image:        sandboxImage,
		Env:          []corev1.EnvVar{{Name: SpecEnv, Value: intent}},
		VolumeMounts: requiredSidecarMounts(entries),
	}, nil
}

func sidecarIntent(spec *corev1.PodSpec, entries []RootfsRwLayerEntry) (string, error) {
	intent := SidecarIntent{Version: 1, Entries: make([]SidecarIntentEntry, 0, len(entries))}
	for _, entry := range entries {
		_, claimName, found := findPVCVolume(spec, entry.VolumeName)
		if !found {
			return "", fmt.Errorf("volume %s must be a persistentVolumeClaim volume", entry.VolumeName)
		}
		intent.Entries = append(intent.Entries, SidecarIntentEntry{
			ContainerName: entry.Name,
			VolumeName:    entry.VolumeName,
			Path:          entry.Path,
			PVCClaimName:  claimName,
		})
	}
	data, err := json.Marshal(intent)
	if err != nil {
		return "", fmt.Errorf("encode sidecar rootfs rw-layer intent: %w", err)
	}
	return string(data), nil
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
