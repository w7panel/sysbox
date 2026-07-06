package admission

import (
	"encoding/json"
	"reflect"

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

func patchForAppWorkload(original *corev1.PodTemplateSpec, mutated *corev1.PodTemplateSpec) ([]byte, error) {
	patches := []jsonPatch{}
	if !reflect.DeepEqual(original.Spec.Containers, mutated.Spec.Containers) {
		patches = append(patches, jsonPatch{Op: "replace", Path: "/spec/template/spec/containers", Value: mutated.Spec.Containers})
	}
	if !reflect.DeepEqual(original.Annotations, mutated.Annotations) {
		path := "/spec/template/metadata/annotations"
		op := "add"
		if original.Annotations != nil {
			op = "replace"
		}
		patches = append(patches, jsonPatch{Op: op, Path: path, Value: mutated.Annotations})
	}
	return json.Marshal(patches)
}
