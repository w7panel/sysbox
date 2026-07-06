package admission

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/require"
)

func TestPatchForPod_returnsEmptyPatch_whenPodUnchanged(t *testing.T) {
	// Given
	original := testPatchPod()
	mutated := original.DeepCopy()

	// When
	patch, err := patchForPod(original, mutated)

	// Then
	require.NoError(t, err)
	require.JSONEq(t, `[]`, string(patch))
}

func TestPatchForDeployment_returnsEmptyPatch_whenDeploymentUnchanged(t *testing.T) {
	// Given
	original := testPatchDeployment().Spec.Template
	mutated := original.DeepCopy()

	// When
	patch, err := patchForAppWorkload(&original, mutated, "/spec/template")

	// Then
	require.NoError(t, err)
	require.JSONEq(t, `[]`, string(patch))
}

func TestPatchForPod_patchesChangedContainersAndLabels(t *testing.T) {
	// Given
	original := testPatchPod()
	mutated := original.DeepCopy()
	mutated.Spec.Containers = append([]corev1.Container{{Name: "sidecar", Image: "registry.example/sidecar:v1"}}, mutated.Spec.Containers...)
	mutated.Labels["sysbox"] = "enabled"

	// When
	patch, err := patchForPod(original, mutated)

	// Then
	require.NoError(t, err)
	require.JSONEq(t, `[
		{"op":"replace","path":"/spec/containers","value":[
			{"name":"sidecar","image":"registry.example/sidecar:v1","resources":{}},
			{"name":"app","image":"registry.example/app:v1","resources":{}}
		]},
		{"op":"replace","path":"/metadata/labels","value":{"app":"demo","sysbox":"enabled"}}
	]`, string(patch))
}

func TestPatchForPod_addsLabels_whenOriginalLabelsNil(t *testing.T) {
	// Given
	original := testPatchPod()
	original.Labels = nil
	mutated := original.DeepCopy()
	mutated.Labels = map[string]string{"sysbox": "enabled"}

	// When
	patch, err := patchForPod(original, mutated)

	// Then
	require.NoError(t, err)
	require.JSONEq(t, `[
		{"op":"add","path":"/metadata/labels","value":{"sysbox":"enabled"}}
	]`, string(patch))
}

func TestPatchForDeployment_patchesChangedContainersAndAnnotations(t *testing.T) {
	// Given
	original := testPatchDeployment().Spec.Template
	mutated := original.DeepCopy()
	mutated.Spec.Containers = append([]corev1.Container{{Name: "sidecar", Image: "registry.example/sidecar:v1"}}, mutated.Spec.Containers...)
	mutated.Annotations["sysbox.example/injected"] = "true"

	// When
	patch, err := patchForAppWorkload(&original, mutated, "/spec/template")

	// Then
	require.NoError(t, err)
	require.JSONEq(t, `[
		{"op":"replace","path":"/spec/template/spec/containers","value":[
			{"name":"sidecar","image":"registry.example/sidecar:v1","resources":{}},
			{"name":"app","image":"registry.example/app:v1","resources":{}}
		]},
		{"op":"replace","path":"/spec/template/metadata/annotations","value":{"sysbox.example/config":"enabled","sysbox.example/injected":"true"}}
	]`, string(patch))
}

func TestPatchForDeployment_addsAnnotations_whenOriginalAnnotationsNil(t *testing.T) {
	// Given
	original := testPatchDeployment().Spec.Template
	original.Annotations = nil
	mutated := original.DeepCopy()
	mutated.Annotations = map[string]string{"sysbox.example/injected": "true"}

	// When
	patch, err := patchForAppWorkload(&original, mutated, "/spec/template")

	// Then
	require.NoError(t, err)
	require.JSONEq(t, `[
		{"op":"add","path":"/spec/template/metadata/annotations","value":{"sysbox.example/injected":"true"}}
	]`, string(patch))
}

func TestPatchForAppWorkload_patchesChangedTemplate(t *testing.T) {
	// Given
	original := testPatchDeployment().Spec.Template
	mutated := original.DeepCopy()
	mutated.Spec.Containers = append([]corev1.Container{{Name: "sidecar", Image: "registry.example/sidecar:v1"}}, mutated.Spec.Containers...)
	mutated.Annotations["sysbox.example/injected"] = "true"

	// When
	patch, err := patchForAppWorkload(&original, mutated, "/spec/template")

	// Then
	require.NoError(t, err)
	require.JSONEq(t, `[
		{"op":"replace","path":"/spec/template/spec/containers","value":[
			{"name":"sidecar","image":"registry.example/sidecar:v1","resources":{}},
			{"name":"app","image":"registry.example/app:v1","resources":{}}
		]},
		{"op":"replace","path":"/spec/template/metadata/annotations","value":{"sysbox.example/config":"enabled","sysbox.example/injected":"true"}}
	]`, string(patch))
}

func testPatchPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pod",
			Labels: map[string]string{
				"app": "demo",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "registry.example/app:v1"}},
		},
	}
}

func testPatchDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "deployment"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"sysbox.example/config": "enabled",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: "registry.example/app:v1"}},
				},
			},
		},
	}
}
