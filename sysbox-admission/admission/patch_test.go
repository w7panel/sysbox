package admission

import (
	"testing"

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
