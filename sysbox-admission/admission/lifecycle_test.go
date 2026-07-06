package admission

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestLifecycleManager_Ensure_createsLeaseAndSecretsAndWebhook_whenMissing(t *testing.T) {
	// Given: an empty cluster API and lifecycle config for the release namespace.
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	manager := NewLifecycleManager(client, LifecycleConfig{
		Namespace:     "sysbox-system",
		ServiceName:   "sysbox-admission",
		ServicePort:   443,
		CASecretName:  "sysbox-admission-ca",
		TLSSecretName: "sysbox-admission-tls",
		LeaseName:     "sysbox-admission-init",
		WebhookName:   WebhookName,
	})

	// When: the backend lifecycle is ensured.
	result, err := manager.Ensure(ctx)

	// Then: the generated TLS certificate is usable and all owned resources exist.
	require.NoError(t, err)
	require.NotEmpty(t, result.TLSCertificate.Certificate)
	_, err = client.CoordinationV1().Leases("sysbox-system").Get(ctx, "sysbox-admission-init", metav1.GetOptions{})
	require.NoError(t, err)
	caSecret, err := client.CoreV1().Secrets("sysbox-system").Get(ctx, "sysbox-admission-ca", metav1.GetOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, caSecret.Data[CASecretCertKey])
	require.NotEmpty(t, caSecret.Data[CASecretKeyKey])
	tlsSecret, err := client.CoreV1().Secrets("sysbox-system").Get(ctx, "sysbox-admission-tls", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, corev1.SecretTypeTLS, tlsSecret.Type)
	require.NotEmpty(t, tlsSecret.Data[corev1.TLSCertKey])
	require.NotEmpty(t, tlsSecret.Data[corev1.TLSPrivateKeyKey])
	webhook, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, WebhookName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, caSecret.Data[CASecretCertKey], webhook.Webhooks[0].ClientConfig.CABundle)
}

func TestLifecycleManager_Ensure_reusesExistingCASecretAndUpdatesWebhookCABundle(t *testing.T) {
	// Given: an existing CA Secret and stale webhook caBundle.
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	initialBundle, err := GenerateCertificateBundle(CertificateConfig{ServiceName: "sysbox-admission", Namespace: "sysbox-system"})
	require.NoError(t, err)
	_, err = client.CoreV1().Secrets("sysbox-system").Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "sysbox-admission-ca", Namespace: "sysbox-system"},
		Data: map[string][]byte{
			CASecretCertKey: initialBundle.CACertPEM,
			CASecretKeyKey:  initialBundle.CAKeyPEM,
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	staleWebhook, err := BuildMutatingWebhookConfiguration(WebhookConfig{
		Name:        WebhookName,
		ServiceName: "sysbox-admission",
		Namespace:   "sysbox-system",
		ServicePort: 443,
		CABundle:    []byte("stale"),
	})
	require.NoError(t, err)
	_, err = client.AdmissionregistrationV1().MutatingWebhookConfigurations().Create(ctx, staleWebhook, metav1.CreateOptions{})
	require.NoError(t, err)
	manager := NewLifecycleManager(client, LifecycleConfig{
		Namespace:     "sysbox-system",
		ServiceName:   "sysbox-admission",
		ServicePort:   443,
		CASecretName:  "sysbox-admission-ca",
		TLSSecretName: "sysbox-admission-tls",
		LeaseName:     "sysbox-admission-init",
		WebhookName:   WebhookName,
	})

	// When: lifecycle reconciliation runs.
	_, err = manager.Ensure(ctx)

	// Then: the webhook caBundle is replaced with the existing CA Secret data.
	require.NoError(t, err)
	webhook, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, WebhookName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, initialBundle.CACertPEM, webhook.Webhooks[0].ClientConfig.CABundle)
}

func TestLifecycleManager_Ensure_appliesIntendedCASecret_whenCreateReturnsAlreadyExists(t *testing.T) {
	// Given: another lifecycle process writes stale CA Secret data during the create race.
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	staleBundle, err := GenerateCertificateBundle(CertificateConfig{ServiceName: "sysbox-admission", Namespace: "sysbox-system"})
	require.NoError(t, err)
	var intendedCACertPEM []byte
	client.PrependReactor("create", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		createdSecret := action.(k8stesting.CreateAction).GetObject().(*corev1.Secret)
		if createdSecret.Name != "sysbox-admission-ca" {
			return false, nil, nil
		}
		intendedCACertPEM = append([]byte(nil), createdSecret.Data[CASecretCertKey]...)
		persistedSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "sysbox-admission-ca", Namespace: "sysbox-system"},
			Data: map[string][]byte{
				CASecretCertKey: staleBundle.CACertPEM,
				CASecretKeyKey:  staleBundle.CAKeyPEM,
			},
		}
		require.NoError(t, client.Tracker().Add(persistedSecret))
		return true, nil, apierrors.NewAlreadyExists(corev1.Resource("secrets"), "sysbox-admission-ca")
	})
	manager := NewLifecycleManager(client, LifecycleConfig{
		Namespace:     "sysbox-system",
		ServiceName:   "sysbox-admission",
		ServicePort:   443,
		CASecretName:  "sysbox-admission-ca",
		TLSSecretName: "sysbox-admission-tls",
		LeaseName:     "sysbox-admission-init",
		WebhookName:   WebhookName,
	})

	// When: lifecycle reconciliation loses the CA Secret create race.
	_, err = manager.Ensure(ctx)

	// Then: the webhook trusts the intended CA Secret data, not the stale raced bytes.
	require.NoError(t, err)
	webhook, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, WebhookName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, intendedCACertPEM, webhook.Webhooks[0].ClientConfig.CABundle)
}

func TestLifecycleManager_Ensure_signsTLSCertificateWithExistingCASecret(t *testing.T) {
	// Given: an existing CA Secret that must remain the webhook trust anchor.
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	initialBundle, err := GenerateCertificateBundle(CertificateConfig{ServiceName: "sysbox-admission", Namespace: "sysbox-system"})
	require.NoError(t, err)
	_, err = client.CoreV1().Secrets("sysbox-system").Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "sysbox-admission-ca", Namespace: "sysbox-system"},
		Data: map[string][]byte{
			CASecretCertKey: initialBundle.CACertPEM,
			CASecretKeyKey:  initialBundle.CAKeyPEM,
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	manager := NewLifecycleManager(client, LifecycleConfig{
		Namespace:     "sysbox-system",
		ServiceName:   "sysbox-admission",
		ServicePort:   443,
		CASecretName:  "sysbox-admission-ca",
		TLSSecretName: "sysbox-admission-tls",
		LeaseName:     "sysbox-admission-init",
		WebhookName:   WebhookName,
	})

	// When: lifecycle reconciliation creates the serving TLS Secret.
	_, err = manager.Ensure(ctx)

	// Then: the serving certificate verifies against the reused CA Secret.
	require.NoError(t, err)
	tlsSecret, err := client.CoreV1().Secrets("sysbox-system").Get(ctx, "sysbox-admission-tls", metav1.GetOptions{})
	require.NoError(t, err)
	caBlock, _ := pem.Decode(initialBundle.CACertPEM)
	require.NotNil(t, caBlock)
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	require.NoError(t, err)
	leafBlock, _ := pem.Decode(tlsSecret.Data[corev1.TLSCertKey])
	require.NotNil(t, leafBlock)
	leafCert, err := x509.ParseCertificate(leafBlock.Bytes)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	_, err = leafCert.Verify(x509.VerifyOptions{DNSName: "sysbox-admission.sysbox-system.svc", Roots: pool})
	require.NoError(t, err)
}

func TestLifecycleManager_Ensure_returnsTLSCertificateFromSecret(t *testing.T) {
	// Given: an empty fake cluster.
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	manager := NewLifecycleManager(client, LifecycleConfig{
		Namespace:     "sysbox-system",
		ServiceName:   "sysbox-admission",
		ServicePort:   443,
		CASecretName:  "sysbox-admission-ca",
		TLSSecretName: "sysbox-admission-tls",
		LeaseName:     "sysbox-admission-init",
		WebhookName:   WebhookName,
	})

	// When: lifecycle reconciliation returns server TLS material.
	result, err := manager.Ensure(ctx)

	// Then: the returned certificate can be loaded by Go's TLS stack.
	require.NoError(t, err)
	tlsSecret, err := client.CoreV1().Secrets("sysbox-system").Get(ctx, "sysbox-admission-tls", metav1.GetOptions{})
	require.NoError(t, err)
	fromSecret, err := tls.X509KeyPair(tlsSecret.Data[corev1.TLSCertKey], tlsSecret.Data[corev1.TLSPrivateKeyKey])
	require.NoError(t, err)
	require.Equal(t, fromSecret.Certificate, result.TLSCertificate.Certificate)
}

func TestLifecycleManager_Ensure_returnsIntendedTLSCertificate_whenCreateReturnsAlreadyExists(t *testing.T) {
	// Given: an existing CA Secret and another lifecycle process writes stale TLS data during the create race.
	ctx := context.Background()
	initialBundle, err := GenerateCertificateBundle(CertificateConfig{ServiceName: "sysbox-admission", Namespace: "sysbox-system"})
	require.NoError(t, err)
	staleTLSCertPEM, staleTLSKeyPEM, err := GenerateTLSCertificate(TLSCertificateConfig{
		ServiceName: "sysbox-admission",
		Namespace:   "sysbox-system",
		CACertPEM:   initialBundle.CACertPEM,
		CAKeyPEM:    initialBundle.CAKeyPEM,
	})
	require.NoError(t, err)
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "sysbox-admission-ca", Namespace: "sysbox-system"},
		Data: map[string][]byte{
			CASecretCertKey: initialBundle.CACertPEM,
			CASecretKeyKey:  initialBundle.CAKeyPEM,
		},
	})
	var intendedTLSCertPEM []byte
	var intendedTLSKeyPEM []byte
	client.PrependReactor("create", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		createdSecret := action.(k8stesting.CreateAction).GetObject().(*corev1.Secret)
		if createdSecret.Name != "sysbox-admission-tls" {
			return false, nil, nil
		}
		intendedTLSCertPEM = append([]byte(nil), createdSecret.Data[corev1.TLSCertKey]...)
		intendedTLSKeyPEM = append([]byte(nil), createdSecret.Data[corev1.TLSPrivateKeyKey]...)
		persistedSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "sysbox-admission-tls", Namespace: "sysbox-system"},
			Type:       corev1.SecretTypeTLS,
			Data: map[string][]byte{
				corev1.TLSCertKey:       staleTLSCertPEM,
				corev1.TLSPrivateKeyKey: staleTLSKeyPEM,
			},
		}
		require.NoError(t, client.Tracker().Add(persistedSecret))
		return true, nil, apierrors.NewAlreadyExists(corev1.Resource("secrets"), "sysbox-admission-tls")
	})
	manager := NewLifecycleManager(client, LifecycleConfig{
		Namespace:     "sysbox-system",
		ServiceName:   "sysbox-admission",
		ServicePort:   443,
		CASecretName:  "sysbox-admission-ca",
		TLSSecretName: "sysbox-admission-tls",
		LeaseName:     "sysbox-admission-init",
		WebhookName:   WebhookName,
	})

	// When: lifecycle reconciliation loses the TLS Secret create race.
	result, err := manager.Ensure(ctx)

	// Then: the returned TLS certificate comes from the intended Secret data, not stale raced TLS bytes.
	require.NoError(t, err)
	fromSecret, err := tls.X509KeyPair(intendedTLSCertPEM, intendedTLSKeyPEM)
	require.NoError(t, err)
	require.Equal(t, fromSecret.Certificate, result.TLSCertificate.Certificate)
}

var _ *admissionv1.MutatingWebhookConfiguration
