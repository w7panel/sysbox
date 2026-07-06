package admission_test

import (
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/stretchr/testify/require"
)

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

func assertPatchEquals(t *testing.T, raw []byte, expected string) {
	t.Helper()
	patches := patchFromAdmissionResponse(t, raw)
	encoded, err := json.Marshal(patches)
	require.NoError(t, err)
	require.JSONEq(t, expected, string(encoded))
}

func patchFromAdmissionResponse(t *testing.T, raw []byte) []struct {
	Path string `json:"path"`
} {
	t.Helper()
	var review admissionv1.AdmissionReview
	require.NoError(t, json.Unmarshal(raw, &review))
	require.NotNil(t, review.Response)
	var patches []struct {
		Path string `json:"path"`
	}
	require.NoError(t, json.Unmarshal(review.Response.Patch, &patches))
	return patches
}

func patchValuesFromAdmissionResponse(t *testing.T, raw []byte) []struct {
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
} {
	t.Helper()
	var review admissionv1.AdmissionReview
	require.NoError(t, json.Unmarshal(raw, &review))
	require.NotNil(t, review.Response)
	var patches []struct {
		Path  string          `json:"path"`
		Value json.RawMessage `json:"value"`
	}
	require.NoError(t, json.Unmarshal(review.Response.Patch, &patches))
	return patches
}
