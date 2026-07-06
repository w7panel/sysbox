package admission

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var certHealthNow = time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)

func TestEvaluateCertificateHealth_returnsHealthy_whenBundleValidAndOutsideRenewalWindow(t *testing.T) {
	// Given
	bundle := mustGenerateHealthBundle(t, certHealthNow)
	config := healthConfig(30 * 24 * time.Hour)

	// When
	action, err := EvaluateCertificateHealth(bundle.CACertPEM, bundle.CAKeyPEM, bundle.TLSCertPEM, bundle.TLSKeyPEM, config)

	// Then
	require.NoError(t, err)
	require.Equal(t, CertificateHealthy, action)
}

func TestEvaluateCertificateHealth_rotatesBundle_whenCAKeyMismatchesCACertificate(t *testing.T) {
	// Given
	bundle := mustGenerateHealthBundle(t, certHealthNow)
	otherBundle := mustGenerateHealthBundle(t, certHealthNow)

	// When
	action, err := EvaluateCertificateHealth(bundle.CACertPEM, otherBundle.CAKeyPEM, bundle.TLSCertPEM, bundle.TLSKeyPEM, healthConfig(24*time.Hour))

	// Then
	require.NoError(t, err)
	require.Equal(t, RotateCertificateBundle, action)
}

func TestEvaluateCertificateHealth_rotatesBundle_whenCAUnhealthy(t *testing.T) {
	tests := []struct {
		name   string
		update func(CertificateBundle) CertificateBundle
	}{
		{name: "ca missing", update: func(bundle CertificateBundle) CertificateBundle {
			bundle.CACertPEM = nil
			return bundle
		}},
		{name: "ca unparseable", update: func(bundle CertificateBundle) CertificateBundle {
			bundle.CACertPEM = []byte("not a certificate")
			return bundle
		}},
		{name: "ca expired", update: func(bundle CertificateBundle) CertificateBundle {
			return mustGenerateHealthBundle(t, certHealthNow.Add(-caValidity-time.Hour))
		}},
		{name: "ca not yet valid", update: func(bundle CertificateBundle) CertificateBundle {
			return mustGenerateHealthBundle(t, certHealthNow.Add(time.Hour))
		}},
		{name: "ca not ca", update: func(bundle CertificateBundle) CertificateBundle {
			bundle.CACertPEM = mustSelfSignedHealthCertPEM(t, x509.Certificate{
				SerialNumber:          big.NewInt(10),
				Subject:               pkix.Name{CommonName: "not-ca"},
				NotBefore:             certHealthNow.Add(-time.Hour),
				NotAfter:              certHealthNow.Add(caValidity),
				KeyUsage:              x509.KeyUsageCertSign,
				BasicConstraintsValid: true,
			})
			return bundle
		}},
		{name: "ca inside renewal window", update: func(bundle CertificateBundle) CertificateBundle {
			return mustGenerateHealthBundle(t, certHealthNow.Add(-caValidity+12*time.Hour))
		}},
		{name: "ca lacks signing usage", update: func(bundle CertificateBundle) CertificateBundle {
			bundle.CACertPEM = mustSelfSignedHealthCertPEM(t, x509.Certificate{
				SerialNumber:          big.NewInt(11),
				Subject:               pkix.Name{CommonName: "ca-without-signing"},
				NotBefore:             certHealthNow.Add(-time.Hour),
				NotAfter:              certHealthNow.Add(caValidity),
				BasicConstraintsValid: true,
				IsCA:                  true,
			})
			return bundle
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			bundle := tt.update(mustGenerateHealthBundle(t, certHealthNow))

			// When
			action, err := EvaluateCertificateHealth(bundle.CACertPEM, bundle.CAKeyPEM, bundle.TLSCertPEM, bundle.TLSKeyPEM, healthConfig(24*time.Hour))

			// Then
			require.NoError(t, err)
			require.Equal(t, RotateCertificateBundle, action)
		})
	}
}

func TestEvaluateCertificateHealth_rotatesLeaf_whenLeafUnhealthyAndCAHealthy(t *testing.T) {
	tests := []struct {
		name   string
		update func(CertificateBundle) CertificateBundle
	}{
		{name: "leaf missing", update: func(bundle CertificateBundle) CertificateBundle {
			bundle.TLSCertPEM = nil
			return bundle
		}},
		{name: "leaf unparseable", update: func(bundle CertificateBundle) CertificateBundle {
			bundle.TLSCertPEM = []byte("not a certificate")
			return bundle
		}},
		{name: "leaf expired", update: func(bundle CertificateBundle) CertificateBundle {
			bundle.TLSCertPEM, bundle.TLSKeyPEM = mustGenerateHealthLeaf(t, bundle, certHealthNow.Add(-leafValidity-time.Hour), "sysbox-admission")
			return bundle
		}},
		{name: "leaf not yet valid", update: func(bundle CertificateBundle) CertificateBundle {
			bundle.TLSCertPEM, bundle.TLSKeyPEM = mustGenerateHealthLeaf(t, bundle, certHealthNow.Add(time.Hour), "sysbox-admission")
			return bundle
		}},
		{name: "leaf inside renewal window", update: func(bundle CertificateBundle) CertificateBundle {
			bundle.TLSCertPEM, bundle.TLSKeyPEM = mustGenerateHealthLeaf(t, bundle, certHealthNow.Add(-leafValidity+12*time.Hour), "sysbox-admission")
			return bundle
		}},
		{name: "leaf verifies against wrong ca", update: func(bundle CertificateBundle) CertificateBundle {
			wrongCA := mustGenerateHealthBundle(t, certHealthNow)
			bundle.TLSCertPEM, bundle.TLSKeyPEM = mustGenerateHealthLeaf(t, wrongCA, certHealthNow, "sysbox-admission")
			return bundle
		}},
		{name: "leaf missing service san", update: func(bundle CertificateBundle) CertificateBundle {
			bundle.TLSCertPEM, bundle.TLSKeyPEM = mustGenerateHealthLeaf(t, bundle, certHealthNow, "other-service")
			return bundle
		}},
		{name: "tls key mismatch", update: func(bundle CertificateBundle) CertificateBundle {
			other := mustGenerateHealthBundle(t, certHealthNow)
			bundle.TLSKeyPEM = other.TLSKeyPEM
			return bundle
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			bundle := tt.update(mustGenerateHealthBundle(t, certHealthNow))

			// When
			action, err := EvaluateCertificateHealth(bundle.CACertPEM, bundle.CAKeyPEM, bundle.TLSCertPEM, bundle.TLSKeyPEM, healthConfig(24*time.Hour))

			// Then
			require.NoError(t, err)
			require.Equal(t, RotateLeafCertificate, action)
		})
	}
}

func healthConfig(renewalWindow time.Duration) CertificateHealthConfig {
	return CertificateHealthConfig{
		ServiceName:   "sysbox-admission",
		Namespace:     "sysbox-system",
		RenewalWindow: renewalWindow,
		Now:           func() time.Time { return certHealthNow },
	}
}

func mustGenerateHealthBundle(t *testing.T, issuedAt time.Time) CertificateBundle {
	t.Helper()
	bundle, err := GenerateCertificateBundle(CertificateConfig{
		ServiceName: "sysbox-admission",
		Namespace:   "sysbox-system",
		Now:         func() time.Time { return issuedAt },
	})
	require.NoError(t, err)
	return bundle
}

func mustGenerateHealthLeaf(t *testing.T, bundle CertificateBundle, issuedAt time.Time, serviceName string) ([]byte, []byte) {
	t.Helper()
	certPEM, keyPEM, err := GenerateTLSCertificate(TLSCertificateConfig{
		ServiceName: serviceName,
		Namespace:   "sysbox-system",
		CACertPEM:   bundle.CACertPEM,
		CAKeyPEM:    bundle.CAKeyPEM,
		Now:         func() time.Time { return issuedAt },
	})
	require.NoError(t, err)
	return certPEM, keyPEM
}

func mustSelfSignedHealthCertPEM(t *testing.T, template x509.Certificate) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
