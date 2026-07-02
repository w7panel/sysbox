package admission

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	AnnotationRootfsRwLayer = "sysbox/rootfs-rw-layer"
	SidecarMountPath        = "/var/lib/sysbox/rootfs-rw-volume"
	RootfsPVCSidecarName    = "sysbox-rootfs"
	RootfsRwLayerSpecEnv    = "ROOTFS_RW_LAYER_SPEC"
)

type Config struct {
	SandboxImage string
}

type Mutator struct {
	sandboxImage string
}

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
	_, enabled, err := m.mutatePodTemplate(ctx, &mutated.ObjectMeta, &mutated.Spec)
	if err != nil || !enabled {
		return mutated, err
	}
	return mutated, nil
}

func (m *Mutator) MutateDeployment(ctx context.Context, deployment *appsv1.Deployment) (*appsv1.Deployment, error) {
	mutated := deployment.DeepCopy()
	copyRootfsAnnotation(&mutated.ObjectMeta, &mutated.Spec.Template.ObjectMeta)
	_, _, err := m.mutatePodTemplate(ctx, &mutated.Spec.Template.ObjectMeta, &mutated.Spec.Template.Spec)
	return mutated, err
}

func copyRootfsAnnotation(source *metav1.ObjectMeta, target *metav1.ObjectMeta) {
	if target.Annotations[AnnotationRootfsRwLayer] != "" {
		return
	}
	value := source.Annotations[AnnotationRootfsRwLayer]
	if value == "" {
		return
	}
	if target.Annotations == nil {
		target.Annotations = map[string]string{}
	}
	target.Annotations[AnnotationRootfsRwLayer] = value
}

func (m *Mutator) mutatePodTemplate(ctx context.Context, metadata *metav1.ObjectMeta, spec *corev1.PodSpec) ([]RootfsRwLayerEntry, bool, error) {
	if spec.RuntimeClassName == nil || *spec.RuntimeClassName != "sysbox-runc" {
		return nil, false, nil
	}
	entries, enabled, err := parseRootfsAnnotation(metadata.Annotations, spec)
	if err != nil || !enabled {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := ensureSidecar(spec, entries, m.sandboxImage); err != nil {
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
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, false, fmt.Errorf("invalid %s annotation: %w", AnnotationRootfsRwLayer, err)
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
