package admission

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/stretchr/testify/require"
)

func TestServer_routesSupportedResourcesToMatchingMutator(t *testing.T) {
	tests := []struct {
		name      string
		resource  string
		object    runtime.Object
		patchPath string
	}{
		{name: "pod", resource: "pods", object: serverTestPod(), patchPath: "/spec/containers"},
		{name: "deployment", resource: "deployments", object: serverTestDeployment(), patchPath: "/spec/template/spec/containers"},
		{name: "statefulset", resource: "statefulsets", object: serverTestStatefulSet(), patchPath: "/spec/template/spec/containers"},
		{name: "daemonset", resource: "daemonsets", object: serverTestDaemonSet(), patchPath: "/spec/template/spec/containers"},
		{name: "job", resource: "jobs", object: serverTestJob(), patchPath: "/spec/template/spec/containers"},
		{name: "cronjob", resource: "cronjobs", object: serverTestCronJob(), patchPath: "/spec/jobTemplate/spec/template/spec/containers"},
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
			assertRootfsSidecarPatch(t, review.Response.Patch, tt.patchPath)
		})
	}
}

func assertRootfsSidecarPatch(t *testing.T, rawPatch []byte, path string) {
	t.Helper()
	var patches []struct {
		Path  string             `json:"path"`
		Value []corev1.Container `json:"value"`
	}
	require.NoError(t, json.Unmarshal(rawPatch, &patches))
	for _, patch := range patches {
		if patch.Path != path {
			continue
		}
		require.Len(t, patch.Value, 2)
		sidecar := patch.Value[0]
		require.Equal(t, SidecarContainerName, sidecar.Name)
		require.Equal(t, "registry.example/pause:9.9", sidecar.Image)
		require.Len(t, sidecar.Env, 1)
		require.Equal(t, SpecEnv, sidecar.Env[0].Name)
		require.JSONEq(t, `{"version":1,"entries":[{"containerName":"app","volumeName":"rootfs","path":"app","pvcClaimName":"rootfs-pvc"}]}`, sidecar.Env[0].Value)
		require.Equal(t, []corev1.VolumeMount{{Name: "rootfs", MountPath: "/var/lib/sysbox/rootfs-rw-volume/rootfs"}}, sidecar.VolumeMounts)
		require.Equal(t, "app", patch.Value[1].Name)
		return
	}
	require.Failf(t, "patch path missing", "path %s not found in %s", path, string(rawPatch))
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
	return &appsv1.Deployment{ObjectMeta: serverTestMetadata(), Spec: appsv1.DeploymentSpec{Template: serverTestTemplate()}}
}

func serverTestStatefulSet() *appsv1.StatefulSet {
	return &appsv1.StatefulSet{ObjectMeta: serverTestMetadata(), Spec: appsv1.StatefulSetSpec{Template: serverTestTemplate()}}
}

func serverTestDaemonSet() *appsv1.DaemonSet {
	return &appsv1.DaemonSet{ObjectMeta: serverTestMetadata(), Spec: appsv1.DaemonSetSpec{Template: serverTestTemplate()}}
}

func serverTestJob() *batchv1.Job {
	return &batchv1.Job{ObjectMeta: serverTestMetadata(), Spec: batchv1.JobSpec{Template: serverTestTemplate()}}
}

func serverTestCronJob() *batchv1.CronJob {
	return &batchv1.CronJob{ObjectMeta: serverTestMetadata(), Spec: batchv1.CronJobSpec{JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{Template: serverTestTemplate()}}}}
}

func serverTestMetadata() metav1.ObjectMeta {
	return metav1.ObjectMeta{Annotations: map[string]string{AnnotationRootfsRwLayer: `[{"name":"app","volumeName":"rootfs","path":"app"}]`}}
}

func serverTestTemplate() corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{Spec: serverTestPod().Spec}
}
