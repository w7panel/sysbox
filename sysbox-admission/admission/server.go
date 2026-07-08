package admission

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const admissionReviewBodyLimit = 1 << 20

type Server struct {
	mutator *Mutator
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
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, admissionReviewBodyLimit))
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			err = fmt.Errorf("request body too large: limit is %d bytes", admissionReviewBodyLimit)
		}
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
	default:
		return nil, fmt.Errorf("unsupported resource %s", request.Resource.Resource)
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
