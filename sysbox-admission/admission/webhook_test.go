package admission

import (
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admissionregistration/v1"
)

func TestBuildMutatingWebhookConfiguration_setsExpectedMetadataAndClient(t *testing.T) {
	// Given: webhook configuration inputs for the release namespace and Service.
	config := WebhookConfig{
		Name:        WebhookName,
		ServiceName: "sysbox-admission",
		Namespace:   "sysbox-system",
		ServicePort: 443,
		CABundle:    []byte("ca-bundle"),
	}

	// When: the MutatingWebhookConfiguration is built.
	webhook, err := BuildMutatingWebhookConfiguration(config)

	// Then: metadata and clientConfig point apiserver at the Service backend.
	require.NoError(t, err)
	require.Equal(t, "sysbox-webhook-mutator", webhook.Name)
	require.Len(t, webhook.Webhooks, 1)
	client := webhook.Webhooks[0].ClientConfig
	require.NotNil(t, client.Service)
	require.Equal(t, "sysbox-admission", client.Service.Name)
	require.Equal(t, "sysbox-system", client.Service.Namespace)
	require.Equal(t, "/mutate", *client.Service.Path)
	require.Equal(t, int32(443), *client.Service.Port)
	require.Equal(t, []byte("ca-bundle"), client.CABundle)
}

func TestBuildMutatingWebhookConfiguration_setsExactRulesAndMatchCondition(t *testing.T) {
	// Given: webhook configuration inputs.
	config := WebhookConfig{
		Name:        WebhookName,
		ServiceName: "sysbox-admission",
		Namespace:   "sysbox-system",
		ServicePort: 443,
		CABundle:    []byte("ca-bundle"),
	}

	// When: the MutatingWebhookConfiguration is built.
	webhook, err := BuildMutatingWebhookConfiguration(config)

	// Then: the webhook rules and match condition match the rootfs rw-layer contract.
	require.NoError(t, err)
	entry := webhook.Webhooks[0]
	require.Equal(t, "pod.sysbox.nestybox.com", entry.Name)
	require.Equal(t, []string{"v1"}, entry.AdmissionReviewVersions)
	require.Equal(t, admissionv1.SideEffectClassNone, *entry.SideEffects)
	require.Equal(t, admissionv1.Fail, *entry.FailurePolicy)
	require.Equal(t, admissionv1.IfNeededReinvocationPolicy, *entry.ReinvocationPolicy)
	require.Equal(t, int32(10), *entry.TimeoutSeconds)
	require.Len(t, entry.Rules, 1)
	require.Equal(t, []admissionv1.OperationType{admissionv1.Create}, entry.Rules[0].Operations)
	require.Equal(t, []string{""}, entry.Rules[0].Rule.APIGroups)
	require.Equal(t, []string{"v1"}, entry.Rules[0].Rule.APIVersions)
	require.Equal(t, []string{"pods"}, entry.Rules[0].Rule.Resources)
	require.Len(t, entry.MatchConditions, 1)
	require.Equal(t, "uses-sysbox-runtime", entry.MatchConditions[0].Name)
	require.Equal(t, `has(object.spec.runtimeClassName) && (object.spec.runtimeClassName == "sysbox-runc" || object.spec.runtimeClassName == "runc-lite")`, entry.MatchConditions[0].Expression)
}
