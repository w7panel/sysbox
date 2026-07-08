package main

import (
	"crypto/tls"
	"net/http"
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

func TestDynamicTLSConfig_setsMinimumTLSVersion(t *testing.T) {
	// Given: a dynamic TLS reloader.
	reloader := admission.NewCertificateReloader(tls.Certificate{Certificate: [][]byte{[]byte("initial")}})

	// When: TLS config is built.
	tlsConfig := dynamicTLSConfig(reloader)

	// Then: old TLS versions are disabled.
	require.Equal(t, uint16(tls.VersionTLS12), tlsConfig.MinVersion)
}

func TestNewAdmissionHTTPServer_setsExplicitTimeouts(t *testing.T) {
	// Given: an admission handler.
	handler := http.NewServeMux()

	// When: the HTTP server is built.
	server := newAdmissionHTTPServer(":9443", handler, nil)

	// Then: all request lifecycle timeouts are explicit.
	require.NotZero(t, server.ReadHeaderTimeout)
	require.NotZero(t, server.ReadTimeout)
	require.NotZero(t, server.WriteTimeout)
	require.NotZero(t, server.IdleTimeout)
	require.Equal(t, ":9443", server.Addr)
	require.Same(t, handler, server.Handler)
}

func TestValidateServeConfig_rejectsPlainHTTPByDefault(t *testing.T) {
	// Given: bootstrap is disabled and no TLS files are configured.
	config := serveConfig{}

	// When: the serve config is validated.
	err := validateServeConfig(config)

	// Then: plaintext HTTP is rejected by default.
	require.ErrorIs(t, err, errInsecureHTTPDisabled)
}

func TestValidateServeConfig_allowsPlainHTTP_whenExplicitlyEnabled(t *testing.T) {
	// Given: plaintext HTTP is explicitly enabled for development.
	config := serveConfig{allowInsecureHTTP: true}

	// When: the serve config is validated.
	err := validateServeConfig(config)

	// Then: validation allows the configuration.
	require.NoError(t, err)
}

func TestValidateServeConfig_rejectsPartialTLSFiles(t *testing.T) {
	// Given: only one TLS file is configured.
	config := serveConfig{tlsCert: "tls.crt"}

	// When: the serve config is validated.
	err := validateServeConfig(config)

	// Then: both TLS files are required together.
	require.ErrorIs(t, err, errIncompleteTLSFiles)
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
	runtime := bootstrapTLSWithClients(admission.LifecycleClients{
		Secrets:                       client.CoreV1().Secrets(config.Namespace),
		Leases:                        client.CoordinationV1().Leases(config.Namespace),
		LeaseClient:                   client.CoordinationV1(),
		MutatingWebhookConfigurations: client.AdmissionregistrationV1().MutatingWebhookConfigurations(),
	}, config)

	// Then
	require.NotNil(t, runtime.clients.Secrets)
	require.NotNil(t, runtime.clients.Leases)
	require.NotNil(t, runtime.clients.LeaseClient)
	require.NotNil(t, runtime.clients.MutatingWebhookConfigurations)
	require.NotNil(t, runtime.manager)
	require.NotNil(t, runtime.reloader)
	require.Empty(t, client.Actions())
}
