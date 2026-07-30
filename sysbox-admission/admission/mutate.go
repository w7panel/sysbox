package admission

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const AnnotationRootfsRwLayer = "sysbox/rootfs-rw-layer"

type Config struct {
	SandboxImage string
}

type Mutator struct {
	sandboxImage string
}

type RootfsRwLayerEntry struct {
	Name       string `json:"name"`
	VolumeName string `json:"volumeName"`
	Path       string `json:"path"`
}

func NewMutator(config Config) *Mutator {
	return &Mutator{sandboxImage: config.SandboxImage}
}

func (m *Mutator) Mutate(ctx context.Context, pod *corev1.Pod) (*corev1.Pod, error) {
	mutated := pod.DeepCopy()
	_, enabled, err := m.mutatePod(ctx, mutated)
	if err != nil || !enabled {
		return mutated, err
	}
	return mutated, nil
}

func (m *Mutator) mutatePod(ctx context.Context, pod *corev1.Pod) ([]RootfsRwLayerEntry, bool, error) {
	spec := &pod.Spec
	if spec.RuntimeClassName == nil || *spec.RuntimeClassName != "sysbox-runc" {
		return nil, false, nil
	}
	entries, enabled, err := parseRootfsAnnotation(pod.Annotations, spec)
	if err != nil || !enabled {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := ensureSidecar(spec, entries, m.sandboxImage); err != nil {
		return nil, false, err
	}
	if err := ensureRootfsSpecialMounts(spec, entries); err != nil {
		return nil, false, err
	}
	return entries, true, nil
}

func parseRootfsAnnotation(annotations map[string]string, spec *corev1.PodSpec) ([]RootfsRwLayerEntry, bool, error) {
	raw := annotations[AnnotationRootfsRwLayer]
	if raw == "" {
		return nil, false, nil
	}
	var entries []RootfsRwLayerEntry
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entries); err != nil {
		return nil, false, fmt.Errorf("invalid %s annotation: %w", AnnotationRootfsRwLayer, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, false, fmt.Errorf("invalid %s annotation: trailing JSON data", AnnotationRootfsRwLayer)
	}
	if len(entries) == 0 {
		return nil, false, fmt.Errorf("%s annotation must not be empty", AnnotationRootfsRwLayer)
	}
	seen := map[string]struct{}{}
	for _, entry := range entries {
		if entry.Name == "" {
			return nil, false, fmt.Errorf("rootfs rw-layer entry name is required")
		}
		if _, exists := seen[entry.Name]; exists {
			return nil, false, fmt.Errorf("container %s configured more than once", entry.Name)
		}
		seen[entry.Name] = struct{}{}
		if err := validateEntry(spec, entry); err != nil {
			return nil, false, err
		}
	}
	return entries, true, nil
}

func validateEntry(spec *corev1.PodSpec, entry RootfsRwLayerEntry) error {
	if entry.VolumeName == "" {
		return fmt.Errorf("container %s volumeName is required", entry.Name)
	}
	if _, found := findContainer(spec, entry.Name); !found {
		return fmt.Errorf("container %s not found in pod spec", entry.Name)
	}
	if _, claimName, found := findPVCVolume(spec, entry.VolumeName); !found {
		return fmt.Errorf("volume %s must be a persistentVolumeClaim volume", entry.VolumeName)
	} else if claimName == "" {
		return fmt.Errorf("volume %s pvc claim name is empty", entry.VolumeName)
	}
	if err := validateRelativePath(entry.Path); err != nil {
		return fmt.Errorf("container %s path invalid: %w", entry.Name, err)
	}
	return nil
}

func findContainer(spec *corev1.PodSpec, name string) (int, bool) {
	for index, container := range spec.Containers {
		if container.Name == name {
			return index, true
		}
	}
	return 0, false
}

func findPVCVolume(spec *corev1.PodSpec, volumeName string) (int, string, bool) {
	for index, volume := range spec.Volumes {
		if volume.Name == volumeName && volume.PersistentVolumeClaim != nil {
			return index, volume.PersistentVolumeClaim.ClaimName, true
		}
	}
	return 0, "", false
}

func validateRelativePath(path string) error {
	if path == "" {
		return nil
	}
	if filepath.IsAbs(path) {
		return fmt.Errorf("path must be relative")
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." {
		return nil
	}
	if cleaned == ".." || len(cleaned) >= 3 && cleaned[:3] == "../" {
		return fmt.Errorf("path must not escape volume root")
	}
	return nil
}
