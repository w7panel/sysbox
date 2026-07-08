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

func assertAdmissionDenied(t *testing.T, raw []byte, expectedMessage string) {
	t.Helper()
	var review admissionv1.AdmissionReview
	require.NoError(t, json.Unmarshal(raw, &review))
	require.NotNil(t, review.Response)
	require.False(t, review.Response.Allowed)
	require.Contains(t, review.Response.Result.Message, expectedMessage)
}
