package admission

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type Server struct {
	mutator *Mutator
}

type appWorkload struct {
	metadata *metav1.ObjectMeta
	template *corev1.PodTemplateSpec
}

func NewServer(mutator *Mutator) *Server {
	return &Server{mutator: mutator}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.URL.Path != "/mutate" {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAdmissionError(w, "", err)
		return
	}
	var review admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &review); err != nil {
		writeAdmissionError(w, "", err)
		return
	}
	response := s.mutate(r, &review)
	writeAdmissionResponse(w, review.TypeMeta, response)
}

func (s *Server) mutate(r *http.Request, review *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	if review.Request == nil {
		return admissionError("", fmt.Errorf("missing admission request"))
	}
	patch, err := s.patch(r, review.Request)
	if err != nil {
		return admissionError(review.Request.UID, err)
	}
	patchType := admissionv1.PatchTypeJSONPatch
	return &admissionv1.AdmissionResponse{
		UID:       review.Request.UID,
		Allowed:   true,
		PatchType: &patchType,
		Patch:     patch,
	}
}

func (s *Server) patch(r *http.Request, request *admissionv1.AdmissionRequest) ([]byte, error) {
	switch request.Resource.Resource {
	case "pods":
		var pod corev1.Pod
		if err := json.Unmarshal(request.Object.Raw, &pod); err != nil {
			return nil, err
		}
		mutated, err := s.mutator.Mutate(r.Context(), &pod)
		if err != nil {
			return nil, err
		}
		return patchForPod(&pod, mutated)
	case "deployments", "statefulsets", "daemonsets":
		workload, err := appWorkloadFromRaw(request.Resource.Resource, request.Object.Raw)
		if err != nil {
			return nil, err
		}
		original := workload.template.DeepCopy()
		if err := s.mutator.mutateAppWorkload(r.Context(), workload.metadata, workload.template); err != nil {
			return nil, err
		}
		return patchForAppWorkload(original, workload.template)
	default:
		return nil, fmt.Errorf("unsupported resource %s", request.Resource.Resource)
	}
}

func appWorkloadFromRaw(resource string, raw []byte) (appWorkload, error) {
	switch resource {
	case "deployments":
		var deployment appsv1.Deployment
		if err := json.Unmarshal(raw, &deployment); err != nil {
			return appWorkload{}, err
		}
		return appWorkload{metadata: &deployment.ObjectMeta, template: &deployment.Spec.Template}, nil
	case "statefulsets":
		var statefulSet appsv1.StatefulSet
		if err := json.Unmarshal(raw, &statefulSet); err != nil {
			return appWorkload{}, err
		}
		return appWorkload{metadata: &statefulSet.ObjectMeta, template: &statefulSet.Spec.Template}, nil
	case "daemonsets":
		var daemonSet appsv1.DaemonSet
		if err := json.Unmarshal(raw, &daemonSet); err != nil {
			return appWorkload{}, err
		}
		return appWorkload{metadata: &daemonSet.ObjectMeta, template: &daemonSet.Spec.Template}, nil
	default:
		return appWorkload{}, fmt.Errorf("unsupported app resource %s", resource)
	}
}

func writeAdmissionError(w http.ResponseWriter, uid types.UID, err error) {
	writeAdmissionResponse(w, metav1.TypeMeta{APIVersion: "admission.k8s.io/v1", Kind: "AdmissionReview"}, admissionError(uid, err))
}

func admissionError(uid types.UID, err error) *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{
		UID:     uid,
		Allowed: false,
		Result:  &metav1.Status{Message: err.Error()},
	}
}

func writeAdmissionResponse(w http.ResponseWriter, typeMeta metav1.TypeMeta, response *admissionv1.AdmissionResponse) {
	w.Header().Set("Content-Type", "application/json")
	review := admissionv1.AdmissionReview{TypeMeta: typeMeta, Response: response}
	_ = json.NewEncoder(w).Encode(review)
}
