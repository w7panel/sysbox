package main

import (
	"crypto/tls"
	"os"
	"testing"
	"time"

	"github.com/nestybox/sysbox-admission/admission"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/fake"
)

func TestLeaderIdentity_returnsOverride_whenOverrideConfigured(t *testing.T) {
	// Given
	override := "sysbox-admission-1"

	// When
	identity, err := leaderIdentity(override)

	// Then
	require.NoError(t, err)
	require.Equal(t, override, identity)
}

func TestLeaderIdentity_returnsHostname_whenOverrideEmpty(t *testing.T) {
	// Given
	hostname, err := os.Hostname()
	require.NoError(t, err)

	// When
	identity, err := leaderIdentity("")

	// Then
	require.NoError(t, err)
	require.Equal(t, hostname, identity)
}

func TestDynamicTLSConfig_getsCertificateFromReloader(t *testing.T) {
	// Given
	initial := tls.Certificate{Certificate: [][]byte{[]byte("initial")}}
	updated := tls.Certificate{Certificate: [][]byte{[]byte("updated")}}
	reloader := admission.NewCertificateReloader(initial)
	tlsConfig := dynamicTLSConfig(reloader)
	reloader.SetCertificate(updated)

	// When
	certificate, err := tlsConfig.GetCertificate(&tls.ClientHelloInfo{})

	// Then
	require.NoError(t, err)
	require.Equal(t, updated.Certificate, certificate.Certificate)
	require.Empty(t, tlsConfig.Certificates)
}

func TestBootstrapTLSWithClient_doesNotEnsureLifecycleBeforeLeaderPath(t *testing.T) {
	// Given
	client := fake.NewSimpleClientset()
	config := admission.LifecycleConfig{
		Namespace:     "sysbox-system",
		ServiceName:   "sysbox-admission",
		ServicePort:   443,
		CASecretName:  "sysbox-admission-ca",
		TLSSecretName: "sysbox-admission-tls",
		LeaseName:     "sysbox-admission-init",
		WebhookName:   admission.WebhookName,
		RenewalWindow: 90 * 24 * time.Hour,
	}

	// When
	runtime := bootstrapTLSWithClient(client, config)

	// Then
	require.Same(t, client, runtime.client)
	require.NotNil(t, runtime.manager)
	require.NotNil(t, runtime.reloader)
	require.Empty(t, client.Actions())
}
