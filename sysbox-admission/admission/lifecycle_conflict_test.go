package admission

import (
	"context"
	"errors"
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

func TestLifecycleManager_Ensure_retriesCASecretUpdateConflict_whenFullBundleRotates(t *testing.T) {
	// Given: CA and TLS Secrets inside the CA renewal window, and the first CA Secret update conflicts.
	ctx := context.Background()
	expiringBundle := mustGenerateHealthBundle(t, certHealthNow.Add(-caValidity+12*time.Hour))
	client := fake.NewSimpleClientset(
		lifecycleCASecret(expiringBundle),
		lifecycleTLSSecret(expiringBundle.TLSCertPEM, expiringBundle.TLSKeyPEM),
	)
	webhook := mustLifecycleWebhook(t, expiringBundle.CACertPEM)
	_, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Create(ctx, webhook, metav1.CreateOptions{})
	require.NoError(t, err)
	caUpdateAttempts := 0
	client.PrependReactor("update", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		updatedSecret := action.(k8stesting.UpdateAction).GetObject().(*corev1.Secret)
		if updatedSecret.Name != "sysbox-admission-ca" {
			return false, nil, nil
		}
		caUpdateAttempts++
		if caUpdateAttempts == 1 {
			return true, nil, apierrors.NewConflict(corev1.Resource("secrets"), "sysbox-admission-ca", errors.New("stale resource version"))
		}
		return false, nil, nil
	})
	manager := newTestLifecycleManager(client, lifecycleRotationConfig())

	// When: lifecycle reconciliation rotates the full certificate bundle.
	result, err := manager.Ensure(ctx)

	// Then: the conflict is retried within the same Ensure call and the rotated bundle is applied.
	require.NoError(t, err)
	require.Equal(t, 2, caUpdateAttempts)
	caSecret, tlsSecret, caBundle := lifecycleCertificateState(t, ctx, client)
	require.NotEqual(t, expiringBundle.CACertPEM, caSecret.Data[CASecretCertKey])
	require.NotEqual(t, expiringBundle.CAKeyPEM, caSecret.Data[CASecretKeyKey])
	require.NotEqual(t, expiringBundle.TLSCertPEM, tlsSecret.Data[corev1.TLSCertKey])
	require.NotEqual(t, expiringBundle.TLSKeyPEM, tlsSecret.Data[corev1.TLSPrivateKeyKey])
	require.Equal(t, caSecret.Data[CASecretCertKey], caBundle)
	requireReturnedCertificateFromSecret(t, result, tlsSecret)
}

func TestLifecycleManager_Ensure_retriesTLSSecretUpdateConflict_whenLeafRotates(t *testing.T) {
	// Given: a healthy CA with a serving certificate inside the renewal window, and the first TLS Secret update conflicts.
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
	tlsUpdateAttempts := 0
	client.PrependReactor("update", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		updatedSecret := action.(k8stesting.UpdateAction).GetObject().(*corev1.Secret)
		if updatedSecret.Name != "sysbox-admission-tls" {
			return false, nil, nil
		}
		tlsUpdateAttempts++
		if tlsUpdateAttempts == 1 {
			return true, nil, apierrors.NewConflict(corev1.Resource("secrets"), "sysbox-admission-tls", errors.New("stale resource version"))
		}
		return false, nil, nil
	})
	manager := newTestLifecycleManager(client, lifecycleRotationConfig())

	// When: lifecycle reconciliation rotates only the leaf certificate.
	result, err := manager.Ensure(ctx)

	// Then: the conflict is retried within the same Ensure call and the TLS Secret is rotated.
	require.NoError(t, err)
	require.Equal(t, 2, tlsUpdateAttempts)
	caSecret, tlsSecret, caBundle := lifecycleCertificateState(t, ctx, client)
	require.Equal(t, bundle.CACertPEM, caSecret.Data[CASecretCertKey])
	require.Equal(t, bundle.CACertPEM, caBundle)
	require.NotEqual(t, expiringTLSCertPEM, tlsSecret.Data[corev1.TLSCertKey])
	require.NotEqual(t, expiringTLSKeyPEM, tlsSecret.Data[corev1.TLSPrivateKeyKey])
	requireReturnedCertificateFromSecret(t, result, tlsSecret)
}

func TestLifecycleManager_Ensure_retriesTLSSecretUpdateConflict_whenFullBundleRotates(t *testing.T) {
	// Given: CA and TLS Secrets inside the CA renewal window, and the first TLS Secret update conflicts.
	ctx := context.Background()
	expiringBundle := mustGenerateHealthBundle(t, certHealthNow.Add(-caValidity+12*time.Hour))
	client := fake.NewSimpleClientset(
		lifecycleCASecret(expiringBundle),
		lifecycleTLSSecret(expiringBundle.TLSCertPEM, expiringBundle.TLSKeyPEM),
	)
	webhook := mustLifecycleWebhook(t, expiringBundle.CACertPEM)
	_, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Create(ctx, webhook, metav1.CreateOptions{})
	require.NoError(t, err)
	tlsUpdateAttempts := 0
	client.PrependReactor("update", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		updatedSecret := action.(k8stesting.UpdateAction).GetObject().(*corev1.Secret)
		if updatedSecret.Name != "sysbox-admission-tls" {
			return false, nil, nil
		}
		tlsUpdateAttempts++
		if tlsUpdateAttempts == 1 {
			return true, nil, apierrors.NewConflict(corev1.Resource("secrets"), "sysbox-admission-tls", errors.New("stale resource version"))
		}
		return false, nil, nil
	})
	manager := newTestLifecycleManager(client, lifecycleRotationConfig())

	// When: lifecycle reconciliation rotates the full certificate bundle.
	result, err := manager.Ensure(ctx)

	// Then: the conflict is retried within the same Ensure call and both Secrets are rotated.
	require.NoError(t, err)
	require.Equal(t, 2, tlsUpdateAttempts)
	caSecret, tlsSecret, caBundle := lifecycleCertificateState(t, ctx, client)
	require.NotEqual(t, expiringBundle.CACertPEM, caSecret.Data[CASecretCertKey])
	require.NotEqual(t, expiringBundle.TLSCertPEM, tlsSecret.Data[corev1.TLSCertKey])
	require.Equal(t, caSecret.Data[CASecretCertKey], caBundle)
	requireReturnedCertificateFromSecret(t, result, tlsSecret)
}

func TestLifecycleManager_Ensure_retriesWebhookUpdateConflict_whenCABundleChanges(t *testing.T) {
	// Given: healthy Secrets and a stale webhook caBundle whose first update conflicts.
	ctx := context.Background()
	bundle := mustGenerateHealthBundle(t, certHealthNow)
	client := fake.NewSimpleClientset(
		lifecycleCASecret(bundle),
		lifecycleTLSSecret(bundle.TLSCertPEM, bundle.TLSKeyPEM),
	)
	webhook := mustLifecycleWebhook(t, []byte("stale"))
	_, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Create(ctx, webhook, metav1.CreateOptions{})
	require.NoError(t, err)
	webhookUpdateAttempts := 0
	client.PrependReactor("update", "mutatingwebhookconfigurations", func(action k8stesting.Action) (bool, runtime.Object, error) {
		webhookUpdateAttempts++
		if webhookUpdateAttempts == 1 {
			return true, nil, apierrors.NewConflict(action.GetResource().GroupResource(), WebhookName, errors.New("stale resource version"))
		}
		return false, nil, nil
	})
	manager := newTestLifecycleManager(client, lifecycleRotationConfig())

	// When: lifecycle reconciliation updates the webhook trust bundle.
	_, err = manager.Ensure(ctx)

	// Then: the conflict is retried within the same Ensure call and the webhook trusts the current CA.
	require.NoError(t, err)
	require.Equal(t, 2, webhookUpdateAttempts)
	updatedWebhook, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, WebhookName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, bundle.CACertPEM, updatedWebhook.Webhooks[0].ClientConfig.CABundle)
}

func TestLifecycleManager_Ensure_appliesIntendedCASecret_whenCreateReturnsAlreadyExistsDuringFullBundleRotation(t *testing.T) {
	// Given: stale CA and TLS Secrets inside the CA renewal window, and a competing writer creates stale CA data during create.
	ctx := context.Background()
	expiringBundle := mustGenerateHealthBundle(t, certHealthNow.Add(-caValidity+12*time.Hour))
	staleRacedBundle := mustGenerateHealthBundle(t, certHealthNow.Add(-caValidity+6*time.Hour))
	client := fake.NewSimpleClientset(
		lifecycleTLSSecret(expiringBundle.TLSCertPEM, expiringBundle.TLSKeyPEM),
	)
	webhook := mustLifecycleWebhook(t, expiringBundle.CACertPEM)
	_, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Create(ctx, webhook, metav1.CreateOptions{})
	require.NoError(t, err)
	var intendedCACertPEM []byte
	var intendedCAKeyPEM []byte
	client.PrependReactor("create", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		createdSecret := action.(k8stesting.CreateAction).GetObject().(*corev1.Secret)
		if createdSecret.Name != "sysbox-admission-ca" {
			return false, nil, nil
		}
		intendedCACertPEM = append([]byte(nil), createdSecret.Data[CASecretCertKey]...)
		intendedCAKeyPEM = append([]byte(nil), createdSecret.Data[CASecretKeyKey]...)
		require.NoError(t, client.Tracker().Add(lifecycleCASecret(staleRacedBundle)))
		return true, nil, apierrors.NewAlreadyExists(corev1.Resource("secrets"), "sysbox-admission-ca")
	})
	manager := newTestLifecycleManager(client, lifecycleRotationConfig())

	// When: lifecycle reconciliation rotates the full bundle and loses the CA Secret create race.
	result, err := manager.Ensure(ctx)

	// Then: the intended newly generated CA becomes active across CA Secret, TLS Secret, and webhook.
	require.NoError(t, err)
	require.NotEqual(t, staleRacedBundle.CACertPEM, intendedCACertPEM)
	caSecret, tlsSecret, caBundle := lifecycleCertificateState(t, ctx, client)
	require.Equal(t, intendedCACertPEM, caSecret.Data[CASecretCertKey])
	require.Equal(t, intendedCAKeyPEM, caSecret.Data[CASecretKeyKey])
	require.Equal(t, intendedCACertPEM, caBundle)
	action, err := EvaluateCertificateHealth(caSecret.Data[CASecretCertKey], caSecret.Data[CASecretKeyKey], tlsSecret.Data[corev1.TLSCertKey], tlsSecret.Data[corev1.TLSPrivateKeyKey], lifecycleRotationHealthConfig())
	require.NoError(t, err)
	require.Equal(t, CertificateHealthy, action)
	requireReturnedCertificateFromSecret(t, result, tlsSecret)
}

func TestLifecycleManager_Ensure_appliesIntendedTLSSecret_whenCreateReturnsAlreadyExistsDuringLeafRotation(t *testing.T) {
	// Given: a healthy CA, missing TLS Secret, and a competing writer creates stale TLS data during create.
	ctx := context.Background()
	bundle := mustGenerateHealthBundle(t, certHealthNow)
	staleTLSCertPEM, staleTLSKeyPEM := mustGenerateHealthLeaf(t, bundle, certHealthNow.Add(-leafValidity+6*time.Hour), "sysbox-admission")
	client := fake.NewSimpleClientset(lifecycleCASecret(bundle))
	webhook := mustLifecycleWebhook(t, bundle.CACertPEM)
	_, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Create(ctx, webhook, metav1.CreateOptions{})
	require.NoError(t, err)
	var intendedTLSCertPEM []byte
	var intendedTLSKeyPEM []byte
	client.PrependReactor("create", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		createdSecret := action.(k8stesting.CreateAction).GetObject().(*corev1.Secret)
		if createdSecret.Name != "sysbox-admission-tls" {
			return false, nil, nil
		}
		intendedTLSCertPEM = append([]byte(nil), createdSecret.Data[corev1.TLSCertKey]...)
		intendedTLSKeyPEM = append([]byte(nil), createdSecret.Data[corev1.TLSPrivateKeyKey]...)
		require.NoError(t, client.Tracker().Add(lifecycleTLSSecret(staleTLSCertPEM, staleTLSKeyPEM)))
		return true, nil, apierrors.NewAlreadyExists(corev1.Resource("secrets"), "sysbox-admission-tls")
	})
	manager := newTestLifecycleManager(client, lifecycleRotationConfig())

	// When: lifecycle reconciliation loses the TLS Secret create race.
	result, err := manager.Ensure(ctx)

	// Then: the intended serving certificate replaces the stale raced TLS Secret in the same Ensure call.
	require.NoError(t, err)
	_, tlsSecret, caBundle := lifecycleCertificateState(t, ctx, client)
	require.Equal(t, bundle.CACertPEM, caBundle)
	require.Equal(t, intendedTLSCertPEM, tlsSecret.Data[corev1.TLSCertKey])
	require.Equal(t, intendedTLSKeyPEM, tlsSecret.Data[corev1.TLSPrivateKeyKey])
	require.NotEqual(t, staleTLSCertPEM, tlsSecret.Data[corev1.TLSCertKey])
	requireReturnedCertificateFromSecret(t, result, tlsSecret)
}

func TestLifecycleManager_Ensure_retriesWebhookUpdateConflict_whenCreateReturnsAlreadyExists(t *testing.T) {
	// Given: Get reports the webhook missing, Create loses a race to stale caBundle, and the first repair update conflicts.
	ctx := context.Background()
	bundle := mustGenerateHealthBundle(t, certHealthNow)
	client := fake.NewSimpleClientset(
		lifecycleCASecret(bundle),
		lifecycleTLSSecret(bundle.TLSCertPEM, bundle.TLSKeyPEM),
	)
	client.PrependReactor("create", "mutatingwebhookconfigurations", func(action k8stesting.Action) (bool, runtime.Object, error) {
		createdWebhook := mustLifecycleWebhook(t, []byte("stale"))
		require.NoError(t, client.Tracker().Add(createdWebhook))
		return true, nil, apierrors.NewAlreadyExists(action.GetResource().GroupResource(), WebhookName)
	})
	webhookUpdateAttempts := 0
	client.PrependReactor("update", "mutatingwebhookconfigurations", func(action k8stesting.Action) (bool, runtime.Object, error) {
		webhookUpdateAttempts++
		if webhookUpdateAttempts == 1 {
			return true, nil, apierrors.NewConflict(action.GetResource().GroupResource(), WebhookName, errors.New("stale resource version"))
		}
		return false, nil, nil
	})
	manager := newTestLifecycleManager(client, lifecycleRotationConfig())

	// When: lifecycle reconciliation repairs the raced webhook after create AlreadyExists.
	_, err := manager.Ensure(ctx)

	// Then: the repair update is retried and the webhook trusts the current active CA bundle.
	require.NoError(t, err)
	require.Equal(t, 2, webhookUpdateAttempts)
	updatedWebhook, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, WebhookName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, bundle.CACertPEM, updatedWebhook.Webhooks[0].ClientConfig.CABundle)
}
