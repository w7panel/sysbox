package admission

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestLifecycleManager_Ensure_rotatesCAAndTLSSecret_whenTLSMissingAndCAInsideRenewalWindow(t *testing.T) {
	// Given: only an expiring CA Secret exists.
	ctx := context.Background()
	expiringBundle := mustGenerateHealthBundle(t, certHealthNow.Add(-caValidity+12*time.Hour))
	client := fake.NewSimpleClientset(lifecycleCASecret(expiringBundle))
	webhook := mustLifecycleWebhook(t, expiringBundle.CACertPEM)
	_, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Create(ctx, webhook, metav1.CreateOptions{})
	require.NoError(t, err)
	manager := NewLifecycleManager(client, lifecycleRotationConfig())

	// When: lifecycle reconciliation finds the TLS Secret missing.
	result, err := manager.Ensure(ctx)

	// Then: the unhealthy CA is rotated together with the new TLS Secret.
	require.NoError(t, err)
	caSecret, tlsSecret, caBundle := lifecycleCertificateState(t, ctx, client)
	require.NotEqual(t, expiringBundle.CACertPEM, caSecret.Data[CASecretCertKey])
	require.NotEqual(t, expiringBundle.CAKeyPEM, caSecret.Data[CASecretKeyKey])
	require.Equal(t, caSecret.Data[CASecretCertKey], caBundle)
	action, err := EvaluateCertificateHealth(caSecret.Data[CASecretCertKey], caSecret.Data[CASecretKeyKey], tlsSecret.Data[corev1.TLSCertKey], tlsSecret.Data[corev1.TLSPrivateKeyKey], lifecycleRotationHealthConfig())
	require.NoError(t, err)
	require.Equal(t, CertificateHealthy, action)
	requireReturnedCertificateFromSecret(t, result, tlsSecret)
}

func TestLifecycleManager_Ensure_rotatesCAAndTLSSecret_whenTLSMissingAndCAKeyMissing(t *testing.T) {
	// Given: only a CA Secret with unusable key material exists.
	ctx := context.Background()
	bundle := mustGenerateHealthBundle(t, certHealthNow)
	caSecret := lifecycleCASecret(bundle)
	delete(caSecret.Data, CASecretKeyKey)
	client := fake.NewSimpleClientset(caSecret)
	webhook := mustLifecycleWebhook(t, bundle.CACertPEM)
	_, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Create(ctx, webhook, metav1.CreateOptions{})
	require.NoError(t, err)
	manager := NewLifecycleManager(client, lifecycleRotationConfig())

	// When: lifecycle reconciliation finds the TLS Secret missing.
	result, err := manager.Ensure(ctx)

	// Then: the unusable CA Secret is replaced before generating TLS material.
	require.NoError(t, err)
	caSecret, tlsSecret, caBundle := lifecycleCertificateState(t, ctx, client)
	require.NotEqual(t, bundle.CACertPEM, caSecret.Data[CASecretCertKey])
	require.NotEmpty(t, caSecret.Data[CASecretKeyKey])
	require.Equal(t, caSecret.Data[CASecretCertKey], caBundle)
	action, err := EvaluateCertificateHealth(caSecret.Data[CASecretCertKey], caSecret.Data[CASecretKeyKey], tlsSecret.Data[corev1.TLSCertKey], tlsSecret.Data[corev1.TLSPrivateKeyKey], lifecycleRotationHealthConfig())
	require.NoError(t, err)
	require.Equal(t, CertificateHealthy, action)
	requireReturnedCertificateFromSecret(t, result, tlsSecret)
}

func TestLifecycleManager_Ensure_updatesWebhookCABundle_whenCreateLosesRace(t *testing.T) {
	// Given: another lifecycle process creates a stale webhook after Get reports NotFound.
	ctx := context.Background()
	bundle := mustGenerateHealthBundle(t, certHealthNow)
	client := fake.NewSimpleClientset(
		lifecycleCASecret(bundle),
		lifecycleTLSSecret(bundle.TLSCertPEM, bundle.TLSKeyPEM),
	)
	client.PrependReactor("create", "mutatingwebhookconfigurations", func(action k8stesting.Action) (bool, runtime.Object, error) {
		createdWebhook, err := BuildMutatingWebhookConfiguration(WebhookConfig{
			Name:        WebhookName,
			ServiceName: "sysbox-admission",
			Namespace:   "sysbox-system",
			ServicePort: 443,
			CABundle:    []byte("stale"),
		})
		require.NoError(t, err)
		require.NoError(t, client.Tracker().Add(createdWebhook))
		return true, nil, apierrors.NewAlreadyExists(action.GetResource().GroupResource(), WebhookName)
	})
	manager := NewLifecycleManager(client, lifecycleRotationConfig())

	// When: lifecycle reconciliation loses the webhook create race.
	_, err := manager.Ensure(ctx)

	// Then: the raced webhook is re-read and updated to the active CA bundle.
	require.NoError(t, err)
	webhook, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, WebhookName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, bundle.CACertPEM, webhook.Webhooks[0].ClientConfig.CABundle)
}
