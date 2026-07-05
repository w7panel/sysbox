package admission_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/nestybox/sysbox-admission/admission"
	"github.com/stretchr/testify/require"
)

const testSandboxImage = "registry.example/pause:9.9"

func TestMutator_injectsSidecarIntent_whenAnnotationIsValid(t *testing.T) {
	mutator := newTestMutator()
	pod := validRootfsPod()

	mutated, err := mutator.Mutate(context.Background(), pod)

	require.NoError(t, err)
	require.Len(t, mutated.Spec.Containers, 3)
	sidecar := mutated.Spec.Containers[0]
	require.Equal(t, admission.SidecarContainerName, sidecar.Name)
	require.Equal(t, testSandboxImage, sidecar.Image)
	require.Empty(t, sidecar.Command)
	require.Len(t, sidecar.VolumeMounts, 1)
	require.Equal(t, "rootfs", sidecar.VolumeMounts[0].Name)
	require.Equal(t, filepath.Join(admission.SidecarMountPath, "rootfs"), sidecar.VolumeMounts[0].MountPath)
	require.Empty(t, sidecar.VolumeMounts[0].SubPath)
	require.Len(t, sidecar.Env, 1)
	require.Equal(t, admission.SpecEnv, sidecar.Env[0].Name)
	assertSidecarIntent(t, sidecar.Env[0].Value, []map[string]any{
		{"containerName": "c1", "volumeName": "rootfs", "path": "containers/c1", "pvcClaimName": "sysbox-rootfs-pvc"},
		{"containerName": "c2", "volumeName": "rootfs", "path": "containers/c2", "pvcClaimName": "sysbox-rootfs-pvc"},
	})
	require.Equal(t, "c1", mutated.Spec.Containers[1].Name)
	require.Len(t, mutated.Spec.Containers[1].VolumeMounts, 0)
	require.Equal(t, "c2", mutated.Spec.Containers[2].Name)
	require.Len(t, mutated.Spec.Containers[2].VolumeMounts, 0)
}

func TestMutator_mutatesDeploymentPodTemplate_whenTemplateAnnotationIsValid(t *testing.T) {
	// Given
	mutator := newTestMutator()
	deployment := validRootfsDeployment()

	// When
	mutated, err := mutator.MutateDeployment(context.Background(), deployment)

	// Then
	require.NoError(t, err)
	require.Len(t, mutated.Spec.Template.Spec.Containers, 2)
	sidecar := mutated.Spec.Template.Spec.Containers[0]
	require.Equal(t, admission.SidecarContainerName, sidecar.Name)
	require.Equal(t, testSandboxImage, sidecar.Image)
	require.Empty(t, sidecar.Command)
	require.Len(t, sidecar.VolumeMounts, 1)
	require.Equal(t, "test", sidecar.VolumeMounts[0].Name)
	require.Equal(t, filepath.Join(admission.SidecarMountPath, "test"), sidecar.VolumeMounts[0].MountPath)
}

func TestMutator_injectsSidecarIntent_whenPodNameIsEmpty(t *testing.T) {
	// Given
	mutator := newTestMutator()
	pod := validRootfsPod()
	pod.Name = ""
	pod.GenerateName = "rootfs-demo-"

	// When
	mutated, err := mutator.Mutate(context.Background(), pod)

	// Then
	require.NoError(t, err)
	require.Equal(t, admission.SidecarContainerName, mutated.Spec.Containers[0].Name)
	require.Len(t, mutated.Spec.Containers[0].Env, 1)
}

func TestMutator_mutatesDeploymentPodTemplate_whenDeploymentAnnotationIsValid(t *testing.T) {
	// Given
	mutator := newTestMutator()
	deployment := validRootfsDeployment()
	deployment.Annotations = deployment.Spec.Template.Annotations
	deployment.Spec.Template.Annotations = nil

	// When
	mutated, err := mutator.MutateDeployment(context.Background(), deployment)

	// Then
	require.NoError(t, err)
	require.Equal(t, deployment.Annotations[admission.AnnotationRootfsRwLayer], mutated.Spec.Template.Annotations[admission.AnnotationRootfsRwLayer])
	require.Len(t, mutated.Spec.Template.Spec.Containers, 2)
	require.Equal(t, admission.SidecarContainerName, mutated.Spec.Template.Spec.Containers[0].Name)
}

func TestMutator_leavesDeploymentUnchanged_whenTemplateAnnotationMissing(t *testing.T) {
	// Given
	mutator := newTestMutator()
	deployment := validRootfsDeployment()
	deployment.Spec.Template.Annotations = nil

	// When
	mutated, err := mutator.MutateDeployment(context.Background(), deployment)

	// Then
	require.NoError(t, err)
	require.Len(t, mutated.Spec.Template.Spec.Containers, 1)
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

func validRootfsDeployment() *appsv1.Deployment {
	runtimeClass := "sysbox-runc"
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rootfs-deploy",
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						admission.AnnotationRootfsRwLayer: `[{"name":"my-container","volumeName":"test","path":"testa/con1"}]`,
					},
				},
				Spec: corev1.PodSpec{
					RuntimeClassName: &runtimeClass,
					Containers:       []corev1.Container{{Name: "my-container"}},
					Volumes: []corev1.Volume{{
						Name:         "test",
						VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "testsysbox"}},
					}},
				},
			},
		},
	}
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
					{"name":"c1","volumeName":"rootfs","path":"containers/c1"},
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

func assertSidecarIntent(t *testing.T, raw string, expected []map[string]any) {
	t.Helper()
	var decoded struct {
		Version int              `json:"version"`
		Entries []map[string]any `json:"entries"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &decoded))
	require.Equal(t, 1, decoded.Version)
	require.Equal(t, expected, decoded.Entries)
}
