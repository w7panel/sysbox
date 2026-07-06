package admission

import (
	"context"
	"crypto/tls"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestLifecycleManager_Ensure_keepsCertificateResources_whenExistingBundleHealthy(t *testing.T) {
	// Given: healthy persisted CA and TLS Secrets already trusted by the webhook.
	ctx := context.Background()
	bundle := mustGenerateHealthBundle(t, certHealthNow)
	client := fake.NewSimpleClientset(
		lifecycleCASecret(bundle),
		lifecycleTLSSecret(bundle.TLSCertPEM, bundle.TLSKeyPEM),
	)
	webhook := mustLifecycleWebhook(t, bundle.CACertPEM)
	_, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Create(ctx, webhook, metav1.CreateOptions{})
	require.NoError(t, err)
	manager := newTestLifecycleManager(client, lifecycleRotationConfig())

	// When: lifecycle reconciliation runs outside the renewal window.
	result, err := manager.Ensure(ctx)

	// Then: the persisted Secrets and webhook trust bundle remain active.
	require.NoError(t, err)
	caSecret, tlsSecret, caBundle := lifecycleCertificateState(t, ctx, client)
	require.Equal(t, bundle.CACertPEM, caSecret.Data[CASecretCertKey])
	require.Equal(t, bundle.CAKeyPEM, caSecret.Data[CASecretKeyKey])
	require.Equal(t, bundle.TLSCertPEM, tlsSecret.Data[corev1.TLSCertKey])
	require.Equal(t, bundle.TLSKeyPEM, tlsSecret.Data[corev1.TLSPrivateKeyKey])
	require.Equal(t, bundle.CACertPEM, caBundle)
	requireReturnedCertificateFromSecret(t, result, tlsSecret)
}

func TestLifecycleManager_Ensure_rotatesOnlyTLSSecret_whenLeafInsideRenewalWindow(t *testing.T) {
	// Given: a healthy CA and webhook caBundle with a serving certificate inside the renewal window.
	ctx := context.Background()
	bundle := mustGenerateHealthBundle(t, certHealthNow)
	expiringTLSCertPEM, expiringTLSKeyPEM := mustGenerateHealthLeaf(t, bundle, certHealthNow.Add(-leafValidity+12*time.Hour), "sysbox-admission")
	client := fake.NewSimpleClientset(
		lifecycleCASecret(bundle),
		lifecycleTLSSecret(expiringTLSCertPEM, expiringTLSKeyPEM),
	)
	webhook := mustLifecycleWebhook(t, bundle.CACertPEM)
	_, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Create(ctx, webhook, metav1.CreateOptions{})
	require.NoError(t, err)
	manager := newTestLifecycleManager(client, lifecycleRotationConfig())

	// When: lifecycle reconciliation evaluates the persisted certificate health.
	result, err := manager.Ensure(ctx)

	// Then: only the TLS Secret is rotated and it is still signed by the original CA.
	require.NoError(t, err)
	caSecret, tlsSecret, caBundle := lifecycleCertificateState(t, ctx, client)
	require.Equal(t, bundle.CACertPEM, caSecret.Data[CASecretCertKey])
	require.Equal(t, bundle.CAKeyPEM, caSecret.Data[CASecretKeyKey])
	require.Equal(t, bundle.CACertPEM, caBundle)
	require.NotEqual(t, expiringTLSCertPEM, tlsSecret.Data[corev1.TLSCertKey])
	require.NotEqual(t, expiringTLSKeyPEM, tlsSecret.Data[corev1.TLSPrivateKeyKey])
	action, err := EvaluateCertificateHealth(caSecret.Data[CASecretCertKey], caSecret.Data[CASecretKeyKey], tlsSecret.Data[corev1.TLSCertKey], tlsSecret.Data[corev1.TLSPrivateKeyKey], lifecycleRotationHealthConfig())
	require.NoError(t, err)
	require.Equal(t, CertificateHealthy, action)
	requireReturnedCertificateFromSecret(t, result, tlsSecret)
}

func TestLifecycleManager_Ensure_rotatesCAAndTLSSecret_whenCAInsideRenewalWindow(t *testing.T) {
	// Given: CA and TLS Secrets inside the CA renewal window.
	ctx := context.Background()
	expiringBundle := mustGenerateHealthBundle(t, certHealthNow.Add(-caValidity+12*time.Hour))
	client := fake.NewSimpleClientset(
		lifecycleCASecret(expiringBundle),
		lifecycleTLSSecret(expiringBundle.TLSCertPEM, expiringBundle.TLSKeyPEM),
	)
	webhook := mustLifecycleWebhook(t, expiringBundle.CACertPEM)
	_, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Create(ctx, webhook, metav1.CreateOptions{})
	require.NoError(t, err)
	manager := newTestLifecycleManager(client, lifecycleRotationConfig())

	// When: lifecycle reconciliation evaluates the persisted certificate health.
	result, err := manager.Ensure(ctx)

	// Then: both Secrets rotate and the webhook trusts the new CA.
	require.NoError(t, err)
	caSecret, tlsSecret, caBundle := lifecycleCertificateState(t, ctx, client)
	require.NotEqual(t, expiringBundle.CACertPEM, caSecret.Data[CASecretCertKey])
	require.NotEqual(t, expiringBundle.CAKeyPEM, caSecret.Data[CASecretKeyKey])
	require.NotEqual(t, expiringBundle.TLSCertPEM, tlsSecret.Data[corev1.TLSCertKey])
	require.NotEqual(t, expiringBundle.TLSKeyPEM, tlsSecret.Data[corev1.TLSPrivateKeyKey])
	require.Equal(t, caSecret.Data[CASecretCertKey], caBundle)
	action, err := EvaluateCertificateHealth(caSecret.Data[CASecretCertKey], caSecret.Data[CASecretKeyKey], tlsSecret.Data[corev1.TLSCertKey], tlsSecret.Data[corev1.TLSPrivateKeyKey], lifecycleRotationHealthConfig())
	require.NoError(t, err)
	require.Equal(t, CertificateHealthy, action)
	requireReturnedCertificateFromSecret(t, result, tlsSecret)
}

func TestLifecycleManager_Ensure_rotatesCAAndTLSSecret_whenCAKeyMismatchesCACertificate(t *testing.T) {
	// Given: CA Secret key material does not match the persisted CA certificate.
	ctx := context.Background()
	bundle := mustGenerateHealthBundle(t, certHealthNow)
	otherBundle := mustGenerateHealthBundle(t, certHealthNow)
	mismatchedBundle := bundle
	mismatchedBundle.CAKeyPEM = otherBundle.CAKeyPEM
	client := fake.NewSimpleClientset(
		lifecycleCASecret(mismatchedBundle),
		lifecycleTLSSecret(bundle.TLSCertPEM, bundle.TLSKeyPEM),
	)
	webhook := mustLifecycleWebhook(t, bundle.CACertPEM)
	_, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Create(ctx, webhook, metav1.CreateOptions{})
	require.NoError(t, err)
	manager := newTestLifecycleManager(client, lifecycleRotationConfig())

	// When: lifecycle reconciliation evaluates the persisted certificate health.
	result, err := manager.Ensure(ctx)

	// Then: the CA Secret and TLS Secret rotate together to a matching bundle.
	require.NoError(t, err)
	caSecret, tlsSecret, caBundle := lifecycleCertificateState(t, ctx, client)
	require.NotEqual(t, bundle.CACertPEM, caSecret.Data[CASecretCertKey])
	require.NotEqual(t, otherBundle.CAKeyPEM, caSecret.Data[CASecretKeyKey])
	require.NotEqual(t, bundle.TLSCertPEM, tlsSecret.Data[corev1.TLSCertKey])
	require.NotEqual(t, bundle.TLSKeyPEM, tlsSecret.Data[corev1.TLSPrivateKeyKey])
	require.Equal(t, caSecret.Data[CASecretCertKey], caBundle)
	action, err := EvaluateCertificateHealth(caSecret.Data[CASecretCertKey], caSecret.Data[CASecretKeyKey], tlsSecret.Data[corev1.TLSCertKey], tlsSecret.Data[corev1.TLSPrivateKeyKey], lifecycleRotationHealthConfig())
	require.NoError(t, err)
	require.Equal(t, CertificateHealthy, action)
	requireReturnedCertificateFromSecret(t, result, tlsSecret)
}

func lifecycleRotationConfig() LifecycleConfig {
	return LifecycleConfig{
		Namespace:     "sysbox-system",
		ServiceName:   "sysbox-admission",
		ServicePort:   443,
		CASecretName:  "sysbox-admission-ca",
		TLSSecretName: "sysbox-admission-tls",
		LeaseName:     "sysbox-admission-init",
		WebhookName:   WebhookName,
		RenewalWindow: 24 * time.Hour,
		Now:           func() time.Time { return certHealthNow },
	}
}

func lifecycleRotationHealthConfig() CertificateHealthConfig {
	config := lifecycleRotationConfig()
	return CertificateHealthConfig{
		ServiceName:   config.ServiceName,
		Namespace:     config.Namespace,
		RenewalWindow: config.RenewalWindow,
		Now:           config.Now,
	}
}

func lifecycleCASecret(bundle CertificateBundle) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "sysbox-admission-ca", Namespace: "sysbox-system"},
		Data: map[string][]byte{
			CASecretCertKey: bundle.CACertPEM,
			CASecretKeyKey:  bundle.CAKeyPEM,
		},
	}
}

func lifecycleTLSSecret(certPEM, keyPEM []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "sysbox-admission-tls", Namespace: "sysbox-system"},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       certPEM,
			corev1.TLSPrivateKeyKey: keyPEM,
		},
	}
}

func mustLifecycleWebhook(t *testing.T, caBundle []byte) *admissionv1.MutatingWebhookConfiguration {
	t.Helper()
	webhook, err := BuildMutatingWebhookConfiguration(WebhookConfig{
		Name:        WebhookName,
		ServiceName: "sysbox-admission",
		Namespace:   "sysbox-system",
		ServicePort: 443,
		CABundle:    caBundle,
	})
	require.NoError(t, err)
	return webhook
}

func lifecycleCertificateState(t *testing.T, ctx context.Context, client *fake.Clientset) (*corev1.Secret, *corev1.Secret, []byte) {
	t.Helper()
	caSecret, err := client.CoreV1().Secrets("sysbox-system").Get(ctx, "sysbox-admission-ca", metav1.GetOptions{})
	require.NoError(t, err)
	tlsSecret, err := client.CoreV1().Secrets("sysbox-system").Get(ctx, "sysbox-admission-tls", metav1.GetOptions{})
	require.NoError(t, err)
	webhook, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, WebhookName, metav1.GetOptions{})
	require.NoError(t, err)
	return caSecret, tlsSecret, webhook.Webhooks[0].ClientConfig.CABundle
}

func requireReturnedCertificateFromSecret(t *testing.T, result LifecycleResult, secret *corev1.Secret) {
	t.Helper()
	fromSecret, err := tls.X509KeyPair(secret.Data[corev1.TLSCertKey], secret.Data[corev1.TLSPrivateKeyKey])
	require.NoError(t, err)
	require.Equal(t, fromSecret.Certificate, result.TLSCertificate.Certificate)
}
