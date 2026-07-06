package admission_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/nestybox/sysbox-admission/admission"
	"github.com/stretchr/testify/require"
)

func TestMutator_replacesExistingSidecarWithCanonicalContainer_whenPodSidecarWasTampered(t *testing.T) {
	// Given
	mutator := newTestMutator()
	pod := validRootfsPod()
	pod.Spec.Containers = []corev1.Container{
		{Name: "c1"},
		{
			Name:    admission.SidecarContainerName,
			Image:   "attacker.example/old:latest",
			Command: []string{"sh", "-c", "exfiltrate"},
			Env: []corev1.EnvVar{{
				Name:  "EVIL",
				Value: "true",
			}},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "host", MountPath: "/host"},
				{Name: "rootfs", MountPath: filepath.Join(admission.SidecarMountPath, "wrong")},
			},
		},
		{Name: "c2"},
	}

	// When
	mutated, err := mutator.Mutate(context.Background(), pod)

	// Then
	require.NoError(t, err)
	require.Equal(t, admission.SidecarContainerName, mutated.Spec.Containers[0].Name)
	require.Equal(t, "c1", mutated.Spec.Containers[1].Name)
	require.Equal(t, "c2", mutated.Spec.Containers[2].Name)
	sidecar := mutated.Spec.Containers[0]
	require.Equal(t, testSandboxImage, sidecar.Image)
	require.Empty(t, sidecar.Command)
	require.Len(t, sidecar.Env, 1)
	require.Equal(t, admission.SpecEnv, sidecar.Env[0].Name)
	require.Equal(t, []corev1.VolumeMount{{
		Name:      "rootfs",
		MountPath: filepath.Join(admission.SidecarMountPath, "rootfs"),
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
	require.Equal(t, admission.SidecarContainerName, mutated.Spec.Containers[0].Name)
	require.Equal(t, "c1", mutated.Spec.Containers[1].Name)
	require.Equal(t, "c2", mutated.Spec.Containers[2].Name)
}

func TestMutator_replacesExistingSidecarWithCanonicalContainer_whenDeploymentTemplateSidecarWasTampered(t *testing.T) {
	// Given
	server := admission.NewServer(newTestMutator())
	deployment := validRootfsDeployment()
	deployment.Annotations = deployment.Spec.Template.Annotations
	deployment.Spec.Template.Annotations = nil
	deployment.Spec.Template.Spec.Containers = []corev1.Container{
		{Name: "my-container"},
		{
			Name:         admission.SidecarContainerName,
			Image:        "old-image",
			Command:      []string{"old"},
			Env:          []corev1.EnvVar{{Name: "OLD", Value: "1"}},
			VolumeMounts: []corev1.VolumeMount{{Name: "extra", MountPath: "/extra"}},
		},
	}
	request := httptest.NewRequest(http.MethodPost, "/mutate", bytes.NewReader(admissionReviewBody(t, "deployments", deployment)))
	recorder := httptest.NewRecorder()

	// When
	server.ServeHTTP(recorder, request)

	// Then
	require.Equal(t, http.StatusOK, recorder.Code)
	containers := appWorkloadContainersFromPatch(t, recorder.Body.Bytes())
	sidecar := containers[0]
	require.Equal(t, "my-container", containers[1].Name)
	require.Equal(t, admission.SidecarContainerName, sidecar.Name)
	require.Equal(t, testSandboxImage, sidecar.Image)
	require.Empty(t, sidecar.Command)
	require.Len(t, sidecar.Env, 1)
	require.Equal(t, admission.SpecEnv, sidecar.Env[0].Name)
	require.Equal(t, []corev1.VolumeMount{{
		Name:      "test",
		MountPath: filepath.Join(admission.SidecarMountPath, "test"),
	}}, sidecar.VolumeMounts)
}

func appWorkloadContainersFromPatch(t *testing.T, raw []byte) []corev1.Container {
	t.Helper()
	var review struct {
		Response struct {
			Patch string `json:"patch"`
		} `json:"response"`
	}
	require.NoError(t, json.Unmarshal(raw, &review))
	decoded, err := base64.StdEncoding.DecodeString(review.Response.Patch)
	require.NoError(t, err)
	var patches []struct {
		Path  string             `json:"path"`
		Value []corev1.Container `json:"value"`
	}
	require.NoError(t, json.Unmarshal(decoded, &patches))
	for _, patch := range patches {
		if patch.Path == "/spec/template/spec/containers" {
			return patch.Value
		}
	}
	require.Fail(t, "missing /spec/template/spec/containers patch")
	return nil
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
		{Name: "rootfs-a", MountPath: filepath.Join(admission.SidecarMountPath, "rootfs-a")},
		{Name: "rootfs-b", MountPath: filepath.Join(admission.SidecarMountPath, "rootfs-b")},
	}, sidecar.VolumeMounts)
}

func TestMutator_replacesSidecarMountsWithRequiredMounts_whenSidecarAlreadyExists(t *testing.T) {
	// Given
	mutator := newTestMutator()
	pod := validRootfsPod()
	pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{
		Name: admission.SidecarContainerName,
		VolumeMounts: []corev1.VolumeMount{{
			Name:      "rootfs",
			MountPath: filepath.Join(admission.SidecarMountPath, "rootfs"),
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
		{Name: "rootfs", MountPath: filepath.Join(admission.SidecarMountPath, "rootfs")},
		{Name: "cache", MountPath: filepath.Join(admission.SidecarMountPath, "cache")},
	}, sidecar.VolumeMounts)
}

func TestMutator_replacesConflictingSidecarMountPath_whenSidecarAlreadyExists(t *testing.T) {
	// Given
	mutator := newTestMutator()
	pod := validRootfsPod()
	pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{
		Name: admission.SidecarContainerName,
		VolumeMounts: []corev1.VolumeMount{{
			Name:      "other",
			MountPath: filepath.Join(admission.SidecarMountPath, "rootfs"),
		}},
	})

	// When
	mutated, err := mutator.Mutate(context.Background(), pod)

	// Then
	require.NoError(t, err)
	require.Equal(t, []corev1.VolumeMount{{
		Name:      "rootfs",
		MountPath: filepath.Join(admission.SidecarMountPath, "rootfs"),
	}}, mutated.Spec.Containers[0].VolumeMounts)
}
