package admission

import (
	"encoding/json"
	"reflect"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

type jsonPatch struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

func patchForPod(original *corev1.Pod, mutated *corev1.Pod) ([]byte, error) {
	patches := []jsonPatch{}
	if !reflect.DeepEqual(original.Spec.Containers, mutated.Spec.Containers) {
		patches = append(patches, jsonPatch{Op: "replace", Path: "/spec/containers", Value: mutated.Spec.Containers})
	}
	if reflect.DeepEqual(original.Labels, mutated.Labels) {
		return json.Marshal(patches)
	}
	path := "/metadata/labels"
	op := "add"
	if original.Labels != nil {
		op = "replace"
	}
	patches = append(patches, jsonPatch{Op: op, Path: path, Value: mutated.Labels})
	return json.Marshal(patches)
}

func patchForDeployment(original *appsv1.Deployment, mutated *appsv1.Deployment) ([]byte, error) {
	patches := []jsonPatch{}
	if !reflect.DeepEqual(original.Spec.Template.Spec.Containers, mutated.Spec.Template.Spec.Containers) {
		patches = append(patches, jsonPatch{Op: "replace", Path: "/spec/template/spec/containers", Value: mutated.Spec.Template.Spec.Containers})
	}
	if !reflect.DeepEqual(original.Spec.Template.Annotations, mutated.Spec.Template.Annotations) {
		path := "/spec/template/metadata/annotations"
		op := "add"
		if original.Spec.Template.Annotations != nil {
			op = "replace"
		}
		patches = append(patches, jsonPatch{Op: op, Path: path, Value: mutated.Spec.Template.Annotations})
	}
	return json.Marshal(patches)
}
