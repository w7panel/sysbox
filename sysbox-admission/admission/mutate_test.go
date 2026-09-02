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

func TestMutator_allowsHostUsersTrueForRuncLite(t *testing.T) {
	pod := validRootfsPod()
	runtimeClass := admission.RuntimeClassRuncLite
	hostUsers := false
	pod.Spec.RuntimeClassName = &runtimeClass
	pod.Spec.HostUsers = &hostUsers
	_, err := newTestMutator().Mutate(context.Background(), pod)
	require.NoError(t, err)

	hostUsers = true
	_, err = newTestMutator().Mutate(context.Background(), pod)
	require.NoError(t, err)
}

func TestMutator_injectsFuseDeviceForPlainSysboxRuncPod(t *testing.T) {
	mutator := newTestMutator()
	runtimeClass := admission.RuntimeClassSysboxRunc
	pod := &corev1.Pod{Spec: corev1.PodSpec{
		RuntimeClassName: &runtimeClass,
		Containers:       []corev1.Container{{Name: "ckm"}},
	}}

	mutated, err := mutator.Mutate(context.Background(), pod)
	require.NoError(t, err)
	require.Len(t, mutated.Spec.Volumes, 1)
	require.Equal(t, "sysbox-fuse", mutated.Spec.Volumes[0].Name)
	require.Equal(t, "/dev/fuse", mutated.Spec.Volumes[0].HostPath.Path)
	require.Equal(t, corev1.HostPathCharDev, *mutated.Spec.Volumes[0].HostPath.Type)
	require.Equal(t, "/dev/fuse", mutated.Spec.Containers[0].VolumeMounts[0].MountPath)
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

func TestMutator_generatesVolumeInitAnnotationForWritablePVCMounts(t *testing.T) {
	mutator := newTestMutator()
	runtimeClass := "sysbox-runc"
	hostUsers := false
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{admission.AnnotationVolumeInit: `[{"name":"forged"}]`}},
		Spec: corev1.PodSpec{
			RuntimeClassName: &runtimeClass,
			HostUsers:        &hostUsers,
			InitContainers:   []corev1.Container{{Name: "init", VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/seed", SubPath: "initial"}}}},
			Containers: []corev1.Container{{Name: "app", VolumeMounts: []corev1.VolumeMount{
				{Name: "data", MountPath: "/data"},
				{Name: "readonly", MountPath: "/readonly", ReadOnly: true},
				{Name: "cache", MountPath: "/cache"},
			}}},
			Volumes: []corev1.Volume{
				{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data-pvc"}}},
				{Name: "readonly", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "readonly-pvc"}}},
				{Name: "cache", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
			},
		},
	}

	mutated, err := mutator.Mutate(context.Background(), pod)

	require.NoError(t, err)
	require.JSONEq(t, `[
		{"name":"app","volumeName":"data","mountPath":"/data"}
	]`, mutated.Annotations[admission.AnnotationVolumeInit])
	require.Len(t, mutated.Spec.Containers, 1)
}

func TestMutator_removesForgedVolumeInitAnnotationWithoutWritablePVC(t *testing.T) {
	mutator := newTestMutator()
	runtimeClass := "sysbox-runc"
	hostUsers := false
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{admission.AnnotationVolumeInit: `[{"name":"forged"}]`}},
		Spec:       corev1.PodSpec{RuntimeClassName: &runtimeClass, HostUsers: &hostUsers, Containers: []corev1.Container{{Name: "app"}}},
	}

	mutated, err := mutator.Mutate(context.Background(), pod)

	require.NoError(t, err)
	_, found := mutated.Annotations[admission.AnnotationVolumeInit]
	require.False(t, found)
}

func newTestMutator() *admission.Mutator {
	return admission.NewMutator(admission.Config{SandboxImage: testSandboxImage})
}

func validRootfsPod() *corev1.Pod {
	runtimeClass := "sysbox-runc"
	hostUsers := false
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
			HostUsers:        &hostUsers,
			Containers:       []corev1.Container{{Name: "c1"}, {Name: "c2"}},
			Volumes: []corev1.Volume{{
				Name:         "rootfs",
				VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "sysbox-rootfs-pvc"}},
			}},
		},
	}
}
