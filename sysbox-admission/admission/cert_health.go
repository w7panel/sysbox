package admission

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"
)

type CertificateHealthAction string

const (
	CertificateHealthy      CertificateHealthAction = "CertificateHealthy"
	RotateLeafCertificate   CertificateHealthAction = "RotateLeafCertificate"
	RotateCertificateBundle CertificateHealthAction = "RotateCertificateBundle"
)

type CertificateHealthConfig struct {
	ServiceName   string
	Namespace     string
	RenewalWindow time.Duration
	Now           func() time.Time
}

func EvaluateCertificateHealth(caCertPEM, caKeyPEM, tlsCertPEM, tlsKeyPEM []byte, config CertificateHealthConfig) (CertificateHealthAction, error) {
	now := time.Now
	if config.Now != nil {
		now = config.Now
	}
	checkedAt := now()
	caCert, err := parseCertificate(caCertPEM)
	if err != nil {
		return RotateCertificateBundle, nil
	}
	if !certTimeHealthy(caCert, checkedAt, config.RenewalWindow) || !caCert.BasicConstraintsValid || !caCert.IsCA || caCert.KeyUsage&x509.KeyUsageCertSign == 0 {
		return RotateCertificateBundle, nil
	}
	caKey, err := parseRSAKey(caKeyPEM)
	if err != nil {
		return RotateCertificateBundle, nil
	}
	caPublicKey, ok := caCert.PublicKey.(*rsa.PublicKey)
	if !ok || !caPublicKey.Equal(&caKey.PublicKey) {
		return RotateCertificateBundle, nil
	}
	leafCert, err := parseCertificate(tlsCertPEM)
	if err != nil {
		return RotateLeafCertificate, nil
	}
	if !certTimeHealthy(leafCert, checkedAt, config.RenewalWindow) {
		return RotateLeafCertificate, nil
	}
	leafKey, err := parseRSAKey(tlsKeyPEM)
	if err != nil {
		return RotateLeafCertificate, nil
	}
	leafPublicKey, ok := leafCert.PublicKey.(*rsa.PublicKey)
	if !ok || !leafPublicKey.Equal(&leafKey.PublicKey) {
		return RotateLeafCertificate, nil
	}
	if !leafVerifiesAgainstCA(leafCert, caCert, config.ServiceName, config.Namespace, checkedAt) {
		return RotateLeafCertificate, nil
	}
	return CertificateHealthy, nil
}

func parseCertificate(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("decode certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}
	return cert, nil
}

func parseRSAKey(keyPEM []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, fmt.Errorf("decode rsa private key")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse rsa private key: %w", err)
	}
	return key, nil
}

func certTimeHealthy(cert *x509.Certificate, now time.Time, renewalWindow time.Duration) bool {
	return !now.Before(cert.NotBefore) && now.Before(cert.NotAfter) && now.Add(renewalWindow).Before(cert.NotAfter)
}

func leafVerifiesAgainstCA(leafCert, caCert *x509.Certificate, serviceName, namespace string, now time.Time) bool {
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	for _, dnsName := range ServiceDNSNames(serviceName, namespace) {
		if _, err := leafCert.Verify(x509.VerifyOptions{
			DNSName:     dnsName,
			Roots:       pool,
			CurrentTime: now,
		}); err != nil {
			return false
		}
	}
	return true
}
