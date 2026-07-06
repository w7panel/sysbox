package admission

import (
	"context"
	"crypto/tls"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestCertificateReloader_GetCertificate_returnsInitialCertificate(t *testing.T) {
	// Given
	initialCertificate := mustGenerateTLSCertificate(t)
	reloader := NewCertificateReloader(initialCertificate)

	// When
	got, err := reloader.GetCertificate(&tls.ClientHelloInfo{})

	// Then
	require.NoError(t, err)
	require.Equal(t, initialCertificate.Certificate, got.Certificate)
}

func TestCertificateReloader_GetCertificate_returnsUpdatedCertificate_afterSetCertificate(t *testing.T) {
	// Given
	initialCertificate := mustGenerateTLSCertificate(t)
	updatedCertificate := mustGenerateTLSCertificate(t)
	reloader := NewCertificateReloader(initialCertificate)

	// When
	reloader.SetCertificate(updatedCertificate)
	got, err := reloader.GetCertificate(&tls.ClientHelloInfo{})

	// Then
	require.NoError(t, err)
	require.Equal(t, updatedCertificate.Certificate, got.Certificate)
}

func TestCertificateReloader_GetCertificate_returnsClearError_whenEmpty(t *testing.T) {
	// Given
	reloader := NewEmptyCertificateReloader()

	// When
	certificate, err := reloader.GetCertificate(&tls.ClientHelloInfo{})

	// Then
	require.Nil(t, certificate)
	require.ErrorIs(t, err, ErrTLSCertificateNotLoaded)
}

func TestLoadTLSCertificateFromSecret_loadsCertificateUsingOnlyGet(t *testing.T) {
	// Given
	ctx := context.Background()
	bundle := mustGenerateTLSBundle(t)
	client := fake.NewSimpleClientset(lifecycleTLSSecret(bundle.TLSCertPEM, bundle.TLSKeyPEM))

	// When
	certificate, err := LoadTLSCertificateFromSecret(ctx, client, "sysbox-system", "sysbox-admission-tls")

	// Then
	require.NoError(t, err)
	wantCertificate, err := tls.X509KeyPair(bundle.TLSCertPEM, bundle.TLSKeyPEM)
	require.NoError(t, err)
	require.Equal(t, wantCertificate.Certificate, certificate.Certificate)
	requireOnlySecretGets(t, client.Actions())
}

func TestLoadTLSCertificateFromSecret_returnsNotFound_whenSecretMissing(t *testing.T) {
	// Given
	ctx := context.Background()
	client := fake.NewSimpleClientset()

	// When
	_, err := LoadTLSCertificateFromSecret(ctx, client, "sysbox-system", "sysbox-admission-tls")

	// Then
	require.True(t, apierrors.IsNotFound(err))
	requireOnlySecretGets(t, client.Actions())
}

func TestRefreshTLSCertificateFromSecret_updatesReloaderUsingOnlyGet(t *testing.T) {
	// Given
	ctx := context.Background()
	initial := mustGenerateTLSBundle(t)
	updated := mustGenerateTLSBundle(t)
	client := fake.NewSimpleClientset(lifecycleTLSSecret(initial.TLSCertPEM, initial.TLSKeyPEM))
	reloader := NewEmptyCertificateReloader()

	// When
	config := TLSCertificateRefreshConfig{
		Client:    client,
		Namespace: "sysbox-system",
		Secret:    "sysbox-admission-tls",
		Interval:  time.Minute,
		Reloader:  reloader,
	}
	err := RefreshTLSCertificateFromSecret(ctx, config)
	require.NoError(t, err)
	secret, err := client.CoreV1().Secrets("sysbox-system").Get(ctx, "sysbox-admission-tls", metav1.GetOptions{})
	require.NoError(t, err)
	secret.Data[corev1.TLSCertKey] = updated.TLSCertPEM
	secret.Data[corev1.TLSPrivateKeyKey] = updated.TLSKeyPEM
	_, err = client.CoreV1().Secrets("sysbox-system").Update(ctx, secret, metav1.UpdateOptions{})
	require.NoError(t, err)
	client.ClearActions()
	err = RefreshTLSCertificateFromSecret(ctx, config)

	// Then
	require.NoError(t, err)
	got, err := reloader.GetCertificate(&tls.ClientHelloInfo{})
	require.NoError(t, err)
	wantCertificate, err := tls.X509KeyPair(updated.TLSCertPEM, updated.TLSKeyPEM)
	require.NoError(t, err)
	require.Equal(t, wantCertificate.Certificate, got.Certificate)
	requireOnlySecretGets(t, client.Actions())
}

func TestWaitForTLSCertificateFromSecret_reportsLoadError_whenSecretMissing(t *testing.T) {
	// Given
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	client := fake.NewSimpleClientset()
	reportedErrors := make(chan error, 1)

	// When
	config := TLSCertificateRefreshConfig{
		Client:    client,
		Namespace: "sysbox-system",
		Secret:    "sysbox-admission-tls",
		Interval:  time.Millisecond,
		Reloader:  NewEmptyCertificateReloader(),
		ErrorHandler: func(err error) {
			select {
			case reportedErrors <- err:
			default:
			}
		},
	}
	err := WaitForTLSCertificateFromSecret(ctx, config)

	// Then
	require.ErrorIs(t, err, context.DeadlineExceeded)
	select {
	case reportedErr := <-reportedErrors:
		require.True(t, apierrors.IsNotFound(reportedErr))
	default:
		t.Fatal("expected TLS secret load error to be reported")
	}
	requireOnlySecretGets(t, client.Actions())
}

func TestRunTLSCertificateRefreshLoop_reportsLoadErrorAndKeepsRunning_whenSecretMissing(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := fake.NewSimpleClientset()
	reportedErrors := make(chan error, 1)

	// When
	config := TLSCertificateRefreshConfig{
		Client:    client,
		Namespace: "sysbox-system",
		Secret:    "sysbox-admission-tls",
		Interval:  time.Millisecond,
		Reloader:  NewEmptyCertificateReloader(),
		ErrorHandler: func(err error) {
			select {
			case reportedErrors <- err:
			default:
			}
			cancel()
		},
	}
	err := RunTLSCertificateRefreshLoop(ctx, config)

	// Then
	require.NoError(t, err)
	select {
	case reportedErr := <-reportedErrors:
		require.True(t, apierrors.IsNotFound(reportedErr))
	default:
		t.Fatal("expected TLS secret refresh error to be reported")
	}
	requireOnlySecretGets(t, client.Actions())
}

func mustGenerateTLSCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	bundle := mustGenerateTLSBundle(t)
	certificate, err := tls.X509KeyPair(bundle.TLSCertPEM, bundle.TLSKeyPEM)
	require.NoError(t, err)
	return certificate
}

func mustGenerateTLSBundle(t *testing.T) CertificateBundle {
	t.Helper()
	bundle, err := GenerateCertificateBundle(CertificateConfig{
		ServiceName: "sysbox-admission",
		Namespace:   "sysbox-system",
	})
	require.NoError(t, err)
	return bundle
}

func requireOnlySecretGets(t *testing.T, actions []k8stesting.Action) {
	t.Helper()
	require.NotEmpty(t, actions)
	for _, action := range actions {
		require.Equal(t, "get", action.GetVerb())
		require.Equal(t, "secrets", action.GetResource().Resource)
	}
}
