package admission

import (
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServiceDNSNames_returnsKubernetesServiceSANs(t *testing.T) {
	// Given: a webhook Service name and namespace.
	serviceName := "sysbox-admission"
	namespace := "sysbox-system"

	// When: the serving certificate SANs are derived.
	dnsNames := ServiceDNSNames(serviceName, namespace)

	// Then: all Kubernetes Service DNS forms used by apiserver verification are present.
	require.Equal(t, []string{
		"sysbox-admission",
		"sysbox-admission.sysbox-system",
		"sysbox-admission.sysbox-system.svc",
		"sysbox-admission.sysbox-system.svc.cluster.local",
	}, dnsNames)
}

func TestGenerateCertificateBundle_issuesLeafForServiceDNSNames(t *testing.T) {
	// Given: a deterministic certificate request for the webhook Service.
	now := time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)
	config := CertificateConfig{
		ServiceName: "sysbox-admission",
		Namespace:   "sysbox-system",
		Now:         func() time.Time { return now },
	}

	// When: the CA and leaf certificate are generated.
	bundle, err := GenerateCertificateBundle(config)

	// Then: the leaf verifies against the generated CA for every Service DNS name.
	require.NoError(t, err)
	require.NotEmpty(t, bundle.CACertPEM)
	require.NotEmpty(t, bundle.CAKeyPEM)
	require.NotEmpty(t, bundle.TLSCertPEM)
	require.NotEmpty(t, bundle.TLSKeyPEM)

	caBlock, _ := pem.Decode(bundle.CACertPEM)
	require.NotNil(t, caBlock)
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	require.NoError(t, err)

	leafBlock, _ := pem.Decode(bundle.TLSCertPEM)
	require.NotNil(t, leafBlock)
	leafCert, err := x509.ParseCertificate(leafBlock.Bytes)
	require.NoError(t, err)

	require.ElementsMatch(t, ServiceDNSNames(config.ServiceName, config.Namespace), leafCert.DNSNames)
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	_, err = leafCert.Verify(x509.VerifyOptions{
		DNSName: "sysbox-admission.sysbox-system.svc",
		Roots:   pool,
	})
	require.NoError(t, err)
}

func TestGenerateCertificateBundle_usesRandomPositiveSerialNumbers(t *testing.T) {
	// Given: a deterministic certificate request for the webhook Service.
	now := time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)
	config := CertificateConfig{
		ServiceName: "sysbox-admission",
		Namespace:   "sysbox-system",
		Now:         func() time.Time { return now },
	}

	// When: a bundle is generated.
	bundle, err := GenerateCertificateBundle(config)
	require.NoError(t, err)

	// Then: CA and leaf serial numbers are positive and not fixed constants.
	caCert := parseTestCertificate(t, bundle.CACertPEM)
	leafCert := parseTestCertificate(t, bundle.TLSCertPEM)
	require.Positive(t, caCert.SerialNumber.Sign())
	require.Positive(t, leafCert.SerialNumber.Sign())
	require.NotEqual(t, big.NewInt(1), caCert.SerialNumber)
	require.NotEqual(t, big.NewInt(2), leafCert.SerialNumber)
}

func parseTestCertificate(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	return cert
}
