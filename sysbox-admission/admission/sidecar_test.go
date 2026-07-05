package admission_test

import (
	"context"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/nestybox/sysbox-admission/admission"
	"github.com/nestybox/sysbox-snapshotter/rootfs"
	"github.com/stretchr/testify/require"
)

func TestMutator_replacesExistingSidecarWithCanonicalContainer_whenPodSidecarWasTampered(t *testing.T) {
	// Given
	mutator := newTestMutator()
	pod := validRootfsPod()
	pod.Spec.Containers = []corev1.Container{
		{Name: "c1"},
		{
			Name:    rootfs.SidecarContainerName,
			Image:   "attacker.example/old:latest",
			Command: []string{"sh", "-c", "exfiltrate"},
			Env: []corev1.EnvVar{{
				Name:  "EVIL",
				Value: "true",
			}},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "host", MountPath: "/host"},
				{Name: "rootfs", MountPath: filepath.Join(rootfs.SidecarMountPath, "wrong")},
			},
		},
		{Name: "c2"},
	}

	// When
	mutated, err := mutator.Mutate(context.Background(), pod)

	// Then
	require.NoError(t, err)
	require.Equal(t, rootfs.SidecarContainerName, mutated.Spec.Containers[0].Name)
	require.Equal(t, "c1", mutated.Spec.Containers[1].Name)
	require.Equal(t, "c2", mutated.Spec.Containers[2].Name)
	sidecar := mutated.Spec.Containers[0]
	require.Equal(t, testSandboxImage, sidecar.Image)
	require.Empty(t, sidecar.Command)
	require.Len(t, sidecar.Env, 1)
	require.Equal(t, rootfs.SpecEnv, sidecar.Env[0].Name)
	require.Equal(t, []corev1.VolumeMount{{
		Name:      "rootfs",
		MountPath: filepath.Join(rootfs.SidecarMountPath, "rootfs"),
	}}, sidecar.VolumeMounts)
}

func TestMutator_injectsSidecarBeforeRootfsContainers_whenPodHasRootfsRwLayer(t *testing.T) {
	// Given
	mutator := newTestMutator()
	pod := validRootfsPod()
	pod.Spec.Containers = []corev1.Container{{Name: "c1"}, {Name: "c2"}}
	pod.Annotations[admission.AnnotationRootfsRwLayer] = `[
		{"name":"c1","volumeName":"rootfs","path":"containers/c1"},
		{"name":"c2","volumeName":"rootfs","path":"containers/c2"}
	]`

	// When
	mutated, err := mutator.Mutate(context.Background(), pod)

	// Then
	require.NoError(t, err)
	require.Equal(t, rootfs.SidecarContainerName, mutated.Spec.Containers[0].Name)
	require.Equal(t, "c1", mutated.Spec.Containers[1].Name)
	require.Equal(t, "c2", mutated.Spec.Containers[2].Name)
}

func TestMutator_replacesExistingSidecarWithCanonicalContainer_whenDeploymentTemplateSidecarWasTampered(t *testing.T) {
	// Given
	mutator := newTestMutator()
	deployment := validRootfsDeployment()
	deployment.Spec.Template.Spec.Containers = []corev1.Container{
		{Name: "my-container"},
		{
			Name:         rootfs.SidecarContainerName,
			Image:        "old-image",
			Command:      []string{"old"},
			Env:          []corev1.EnvVar{{Name: "OLD", Value: "1"}},
			VolumeMounts: []corev1.VolumeMount{{Name: "extra", MountPath: "/extra"}},
		},
	}

	// When
	mutated, err := mutator.MutateDeployment(context.Background(), deployment)

	// Then
	require.NoError(t, err)
	sidecar := mutated.Spec.Template.Spec.Containers[0]
	require.Equal(t, "my-container", mutated.Spec.Template.Spec.Containers[1].Name)
	require.Equal(t, rootfs.SidecarContainerName, sidecar.Name)
	require.Equal(t, testSandboxImage, sidecar.Image)
	require.Empty(t, sidecar.Command)
	require.Len(t, sidecar.Env, 1)
	require.Equal(t, rootfs.SpecEnv, sidecar.Env[0].Name)
	require.Equal(t, []corev1.VolumeMount{{
		Name:      "test",
		MountPath: filepath.Join(rootfs.SidecarMountPath, "test"),
	}}, sidecar.VolumeMounts)
}

func TestMutator_injectsDistinctSidecarMounts_whenEntriesUseDifferentPVCs(t *testing.T) {
	// Given
	mutator := newTestMutator()
	pod := validRootfsPod()
	pod.Annotations[admission.AnnotationRootfsRwLayer] = `[
		{"name":"c1","volumeName":"rootfs-a","path":"containers/c1"},
		{"name":"c2","volumeName":"rootfs-b","path":"containers/c2"}
	]`
	pod.Spec.Volumes = []corev1.Volume{
		{Name: "rootfs-a", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc-a"}}},
		{Name: "rootfs-b", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc-b"}}},
	}

	// When
	mutated, err := mutator.Mutate(context.Background(), pod)

	// Then
	require.NoError(t, err)
	sidecar := mutated.Spec.Containers[0]
	require.Equal(t, []corev1.VolumeMount{
		{Name: "rootfs-a", MountPath: filepath.Join(rootfs.SidecarMountPath, "rootfs-a")},
		{Name: "rootfs-b", MountPath: filepath.Join(rootfs.SidecarMountPath, "rootfs-b")},
	}, sidecar.VolumeMounts)
}

func TestMutator_replacesSidecarMountsWithRequiredMounts_whenSidecarAlreadyExists(t *testing.T) {
	// Given
	mutator := newTestMutator()
	pod := validRootfsPod()
	pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{
		Name: rootfs.SidecarContainerName,
		VolumeMounts: []corev1.VolumeMount{{
			Name:      "rootfs",
			MountPath: filepath.Join(rootfs.SidecarMountPath, "rootfs"),
		}},
	})
	pod.Annotations[admission.AnnotationRootfsRwLayer] = `[
		{"name":"c1","volumeName":"rootfs","path":"containers/c1"},
		{"name":"c2","volumeName":"cache","path":"containers/c2"}
	]`
	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name:         "cache",
		VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "cache-pvc"}},
	})

	// When
	mutated, err := mutator.Mutate(context.Background(), pod)

	// Then
	require.NoError(t, err)
	require.Len(t, mutated.Spec.Containers, 3)
	sidecar := mutated.Spec.Containers[0]
	require.Equal(t, []corev1.VolumeMount{
		{Name: "rootfs", MountPath: filepath.Join(rootfs.SidecarMountPath, "rootfs")},
		{Name: "cache", MountPath: filepath.Join(rootfs.SidecarMountPath, "cache")},
	}, sidecar.VolumeMounts)
}

func TestMutator_replacesConflictingSidecarMountPath_whenSidecarAlreadyExists(t *testing.T) {
	// Given
	mutator := newTestMutator()
	pod := validRootfsPod()
	pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{
		Name: rootfs.SidecarContainerName,
		VolumeMounts: []corev1.VolumeMount{{
			Name:      "other",
			MountPath: filepath.Join(rootfs.SidecarMountPath, "rootfs"),
		}},
	})

	// When
	mutated, err := mutator.Mutate(context.Background(), pod)

	// Then
	require.NoError(t, err)
	require.Equal(t, []corev1.VolumeMount{{
		Name:      "rootfs",
		MountPath: filepath.Join(rootfs.SidecarMountPath, "rootfs"),
	}}, mutated.Spec.Containers[0].VolumeMounts)
}
