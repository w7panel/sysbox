package admission

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/stretchr/testify/require"
)

func TestServer_routesSupportedResourcesToMatchingMutator(t *testing.T) {
	tests := []struct {
		name     string
		resource string
		object   runtime.Object
	}{
		{name: "pod", resource: "pods", object: serverTestPod()},
		{name: "deployment", resource: "deployments", object: serverTestDeployment()},
		{name: "statefulset", resource: "statefulsets", object: serverTestStatefulSet()},
		{name: "daemonset", resource: "daemonsets", object: serverTestDaemonSet()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			server := NewServer(NewMutator(Config{SandboxImage: "registry.example/pause:9.9"}))
			req := httptest.NewRequest(http.MethodPost, "/mutate", bytes.NewReader(admissionReviewBody(t, tt.resource, tt.object)))
			rec := httptest.NewRecorder()

			// When
			server.ServeHTTP(rec, req)

			// Then
			require.Equal(t, http.StatusOK, rec.Code)
			var review admissionv1.AdmissionReview
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &review))
			require.NotNil(t, review.Response)
			require.True(t, review.Response.Allowed, review.Response.Result)
			require.NotEmpty(t, review.Response.Patch)
		})
	}
}

func admissionReviewBody(t *testing.T, resource string, object runtime.Object) []byte {
	t.Helper()
	raw, err := json.Marshal(object)
	require.NoError(t, err)
	review := admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			UID:      types.UID("test-uid"),
			Resource: metav1.GroupVersionResource{Group: "apps", Version: "v1", Resource: resource},
			Object:   runtime.RawExtension{Raw: raw},
		},
	}
	body, err := json.Marshal(review)
	require.NoError(t, err)
	return body
}

func serverTestPod() *corev1.Pod {
	runtimeClass := "sysbox-runc"
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{AnnotationRootfsRwLayer: `[{"name":"app","volumeName":"rootfs","path":"app"}]`}},
		Spec: corev1.PodSpec{
			RuntimeClassName: &runtimeClass,
			Containers:       []corev1.Container{{Name: "app"}},
			Volumes:          []corev1.Volume{{Name: "rootfs", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "rootfs-pvc"}}}},
		},
	}
}

func serverTestDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Template: serverTestTemplate()}}
}

func serverTestStatefulSet() *appsv1.StatefulSet {
	return &appsv1.StatefulSet{Spec: appsv1.StatefulSetSpec{Template: serverTestTemplate()}}
}

func serverTestDaemonSet() *appsv1.DaemonSet {
	return &appsv1.DaemonSet{Spec: appsv1.DaemonSetSpec{Template: serverTestTemplate()}}
}

func serverTestTemplate() corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{ObjectMeta: serverTestPod().ObjectMeta, Spec: serverTestPod().Spec}
}
