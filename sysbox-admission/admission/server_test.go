package admission

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/stretchr/testify/require"
)

func TestServer_routesPodResourceToMutator(t *testing.T) {
	// Given
	server := NewServer(NewMutator(Config{SandboxImage: "registry.example/pause:9.9"}))
	req := httptest.NewRequest(http.MethodPost, "/mutate", bytes.NewReader(admissionReviewBody(t, "pods", serverTestPod())))
	rec := httptest.NewRecorder()

	// When
	server.ServeHTTP(rec, req)

	// Then
	require.Equal(t, http.StatusOK, rec.Code)
	var review admissionv1.AdmissionReview
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &review))
	require.NotNil(t, review.Response)
	require.True(t, review.Response.Allowed, review.Response.Result)
	assertRootfsSidecarPatch(t, review.Response.Patch)
}

func TestServer_rejectsMutationRequest_whenResourceIsNotPod(t *testing.T) {
	// Given
	server := NewServer(NewMutator(Config{SandboxImage: "registry.example/pause:9.9"}))
	req := httptest.NewRequest(http.MethodPost, "/mutate", bytes.NewReader(admissionReviewBody(t, "deployments", serverTestPod())))
	rec := httptest.NewRecorder()

	// When
	server.ServeHTTP(rec, req)

	// Then
	require.Equal(t, http.StatusOK, rec.Code)
	var review admissionv1.AdmissionReview
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &review))
	require.NotNil(t, review.Response)
	require.False(t, review.Response.Allowed)
	require.Contains(t, review.Response.Result.Message, "unsupported resource deployments")
}

func TestServer_rejectsMutationRequest_whenBodyExceedsLimit(t *testing.T) {
	// Given: a mutate request larger than the admission body limit.
	server := NewServer(NewMutator(Config{SandboxImage: "registry.example/pause:9.9"}))
	body := bytes.Repeat([]byte("x"), admissionReviewBodyLimit+1)
	req := httptest.NewRequest(http.MethodPost, "/mutate", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	// When: the request is served.
	server.ServeHTTP(rec, req)

	// Then: the server returns an admission denial instead of reading an unbounded body.
	require.Equal(t, http.StatusOK, rec.Code)
	var review admissionv1.AdmissionReview
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &review))
	require.NotNil(t, review.Response)
	require.False(t, review.Response.Allowed)
	require.Contains(t, review.Response.Result.Message, "request body too large")
}

func assertRootfsSidecarPatch(t *testing.T, rawPatch []byte) {
	t.Helper()
	var patches []struct {
		Path  string             `json:"path"`
		Value []corev1.Container `json:"value"`
	}
	require.NoError(t, json.Unmarshal(rawPatch, &patches))
	for _, patch := range patches {
		if patch.Path != "/spec/containers" {
			continue
		}
		require.Len(t, patch.Value, 2)
		sidecar := patch.Value[1]
		require.Equal(t, SidecarContainerName, sidecar.Name)
		require.Equal(t, "registry.example/pause:9.9", sidecar.Image)
		require.Empty(t, sidecar.Env)
		require.Equal(t, []corev1.VolumeMount{{Name: "rootfs", MountPath: "/var/lib/sysbox/rootfs-rw-volume/rootfs"}}, sidecar.VolumeMounts)
		require.Equal(t, "app", patch.Value[0].Name)
		return
	}
	require.Failf(t, "patch path missing", "path /spec/containers not found in %s", string(rawPatch))
}

func admissionReviewBody(t *testing.T, resource string, object runtime.Object) []byte {
	t.Helper()
	raw, err := json.Marshal(object)
	require.NoError(t, err)
	review := admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			UID:      types.UID("test-uid"),
			Resource: admissionTestResource(resource),
			Object:   runtime.RawExtension{Raw: raw},
		},
	}
	body, err := json.Marshal(review)
	require.NoError(t, err)
	return body
}

func admissionTestResource(resource string) metav1.GroupVersionResource {
	if resource == "pods" {
		return metav1.GroupVersionResource{Version: "v1", Resource: resource}
	}
	return metav1.GroupVersionResource{Group: "apps", Version: "v1", Resource: resource}
}

func serverTestPod() *corev1.Pod {
	runtimeClass := "sysbox-runc"
	hostUsers := false
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{AnnotationRootfsRwLayer: `[{"name":"app","volumeName":"rootfs","path":"app"}]`}},
		Spec: corev1.PodSpec{
			RuntimeClassName: &runtimeClass,
			HostUsers:        &hostUsers,
			Containers:       []corev1.Container{{Name: "app"}},
			Volumes:          []corev1.Volume{{Name: "rootfs", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "rootfs-pvc"}}}},
		},
	}
}
