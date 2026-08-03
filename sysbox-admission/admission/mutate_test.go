package admission_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/require"
	"github.com/w7panel/sysbox/sysbox-admission/admission"
)

const testSandboxImage = "registry.example/pause:9.9"

func TestMutator_injectsSidecar_whenAnnotationIsValid(t *testing.T) {
	mutator := newTestMutator()
	pod := validRootfsPod()

	mutated, err := mutator.Mutate(context.Background(), pod)

	require.NoError(t, err)
	require.Len(t, mutated.Spec.Containers, 3)
	sidecar := mutated.Spec.Containers[2]
	require.Equal(t, admission.SidecarContainerName, sidecar.Name)
	require.Equal(t, testSandboxImage, sidecar.Image)
	require.Empty(t, sidecar.Command)
	require.Len(t, sidecar.VolumeMounts, 1)
	require.Equal(t, "rootfs", sidecar.VolumeMounts[0].Name)
	require.Equal(t, filepath.Join(admission.SidecarMountPath, "rootfs"), sidecar.VolumeMounts[0].MountPath)
	require.Empty(t, sidecar.VolumeMounts[0].SubPath)
	require.Empty(t, sidecar.Env)
	require.Equal(t, "c1", mutated.Spec.Containers[0].Name)
	require.Empty(t, mutated.Spec.Containers[0].VolumeMounts)
	require.Equal(t, "c2", mutated.Spec.Containers[1].Name)
	require.Empty(t, mutated.Spec.Containers[1].VolumeMounts)
}

func TestMutator_keepsSpecialMountConfigScopedToAnnotationEntry(t *testing.T) {
	mutator := newTestMutator()
	pod := validRootfsPod()

	mutated, err := mutator.Mutate(context.Background(), pod)

	require.NoError(t, err)
	require.Len(t, mutated.Spec.Containers, 3)
	require.Equal(t, admission.SidecarContainerName, mutated.Spec.Containers[2].Name)
	require.Empty(t, mutated.Spec.Containers[0].VolumeMounts)
	require.Empty(t, mutated.Spec.Containers[1].VolumeMounts)
}

func TestMutator_rejectsSpecialPathWithoutOptIn(t *testing.T) {
	mutator := newTestMutator()
	pod := validRootfsPod()
	pod.Annotations[admission.AnnotationRootfsRwLayer] = `[{"name":"c1","volumeName":"rootfs","path":"containers/c1","specialPath":["/srv/data"]}]`

	_, err := mutator.Mutate(context.Background(), pod)

	require.ErrorContains(t, err, "specialPath requires persistentSpecialMounts")
}

func TestMutator_rejectsOverlappingSpecialPath(t *testing.T) {
	mutator := newTestMutator()
	pod := validRootfsPod()
	pod.Annotations[admission.AnnotationRootfsRwLayer] = `[{"name":"c1","volumeName":"rootfs","path":"containers/c1","persistentSpecialMounts":true,"specialPath":["/var/lib/rancher/k3s/server"]}]`

	_, err := mutator.Mutate(context.Background(), pod)

	require.ErrorContains(t, err, "overlaps")
}

func TestMutator_injectsSidecar_whenPodNameIsEmpty(t *testing.T) {
	// Given
	mutator := newTestMutator()
	pod := validRootfsPod()
	pod.Name = ""
	pod.GenerateName = "rootfs-demo-"

	// When
	mutated, err := mutator.Mutate(context.Background(), pod)

	// Then
	require.NoError(t, err)
	require.Equal(t, admission.SidecarContainerName, mutated.Spec.Containers[2].Name)
	require.Empty(t, mutated.Spec.Containers[2].Env)
}

func TestServer_rejectsWorkloadResource_whenAnnotationIsValid(t *testing.T) {
	// Given
	server := admission.NewServer(newTestMutator())
	request := httptest.NewRequest(http.MethodPost, "/mutate", bytes.NewReader(admissionReviewBody(t, "deployments", validRootfsPod())))
	recorder := httptest.NewRecorder()

	// When
	server.ServeHTTP(recorder, request)

	// Then
	require.Equal(t, http.StatusOK, recorder.Code)
	assertAdmissionDenied(t, recorder.Body.Bytes(), "unsupported resource deployments")
}

func TestMutator_rejectsDuplicateContainerEntries(t *testing.T) {
	mutator := newTestMutator()
	pod := validRootfsPod()
	pod.Annotations[admission.AnnotationRootfsRwLayer] = `[
		{"name":"c1","volumeName":"rootfs","path":"containers/c1"},
		{"name":"c1","volumeName":"rootfs","path":"containers/c1b"}
	]`

	_, err := mutator.Mutate(context.Background(), pod)

	require.ErrorContains(t, err, "configured more than once")
}

func TestMutator_rejectsNonPVCVolume(t *testing.T) {
	mutator := newTestMutator()
	pod := validRootfsPod()
	pod.Spec.Volumes[0].PersistentVolumeClaim = nil
	pod.Spec.Volumes[0].EmptyDir = &corev1.EmptyDirVolumeSource{}

	_, err := mutator.Mutate(context.Background(), pod)

	require.ErrorContains(t, err, "must be a persistentVolumeClaim volume")
}

func TestMutator_rejectsInvalidPath(t *testing.T) {
	mutator := newTestMutator()
	pod := validRootfsPod()
	pod.Annotations[admission.AnnotationRootfsRwLayer] = `[
		{"name":"c1","volumeName":"rootfs","path":"../escape"}
	]`

	_, err := mutator.Mutate(context.Background(), pod)

	require.ErrorContains(t, err, "path")
}

func TestMutator_leavesPodUnchanged_whenRuntimeClassIsNotSysbox(t *testing.T) {
	mutator := newTestMutator()
	pod := validRootfsPod()
	otherRuntime := "runc"
	pod.Spec.RuntimeClassName = &otherRuntime

	mutated, err := mutator.Mutate(context.Background(), pod)

	require.NoError(t, err)
	require.Empty(t, mutated.Spec.Containers[0].VolumeMounts)
}

func newTestMutator() *admission.Mutator {
	return admission.NewMutator(admission.Config{SandboxImage: testSandboxImage})
}

func validRootfsPod() *corev1.Pod {
	runtimeClass := "sysbox-runc"
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rootfs-demo",
			Namespace: "default",
			UID:       "pod-uid-123",
			Annotations: map[string]string{
				admission.AnnotationRootfsRwLayer: `[
					{"name":"c1","volumeName":"rootfs","path":"containers/c1","persistentSpecialMounts":true,"specialPath":["/srv/data"]},
					{"name":"c2","volumeName":"rootfs","path":"containers/c2"}
				]`,
			},
		},
		Spec: corev1.PodSpec{
			RuntimeClassName: &runtimeClass,
			Containers:       []corev1.Container{{Name: "c1"}, {Name: "c2"}},
			Volumes: []corev1.Volume{{
				Name:         "rootfs",
				VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "sysbox-rootfs-pvc"}},
			}},
		},
	}
}
