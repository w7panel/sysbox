package admission

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

const (
	caValidity   = 10 * 365 * 24 * time.Hour
	leafValidity = 365 * 24 * time.Hour
)

type CertificateConfig struct {
	ServiceName string
	Namespace   string
	Now         func() time.Time
}

type CertificateBundle struct {
	CACertPEM  []byte
	CAKeyPEM   []byte
	TLSCertPEM []byte
	TLSKeyPEM  []byte
}

type TLSCertificateConfig struct {
	ServiceName string
	Namespace   string
	CACertPEM   []byte
	CAKeyPEM    []byte
	Now         func() time.Time
}

func ServiceDNSNames(serviceName, namespace string) []string {
	return []string{
		serviceName,
		fmt.Sprintf("%s.%s", serviceName, namespace),
		fmt.Sprintf("%s.%s.svc", serviceName, namespace),
		fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, namespace),
	}
}

func GenerateCertificateBundle(config CertificateConfig) (CertificateBundle, error) {
	if config.ServiceName == "" {
		return CertificateBundle{}, fmt.Errorf("service name is required")
	}
	if config.Namespace == "" {
		return CertificateBundle{}, fmt.Errorf("namespace is required")
	}
	now := time.Now
	if config.Now != nil {
		now = config.Now
	}
	issuedAt := now()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return CertificateBundle{}, fmt.Errorf("generate ca key: %w", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "sysbox-admission-webhook-ca"},
		NotBefore:             issuedAt.Add(-time.Minute),
		NotAfter:              issuedAt.Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return CertificateBundle{}, fmt.Errorf("create ca certificate: %w", err)
	}
	tlsCertPEM, tlsKeyPEM, err := GenerateTLSCertificate(TLSCertificateConfig{
		ServiceName: config.ServiceName,
		Namespace:   config.Namespace,
		CACertPEM:   pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		CAKeyPEM:    pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(caKey)}),
		Now:         now,
	})
	if err != nil {
		return CertificateBundle{}, err
	}
	return CertificateBundle{
		CACertPEM:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		CAKeyPEM:   pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(caKey)}),
		TLSCertPEM: tlsCertPEM,
		TLSKeyPEM:  tlsKeyPEM,
	}, nil
}

func GenerateTLSCertificate(config TLSCertificateConfig) ([]byte, []byte, error) {
	if config.ServiceName == "" {
		return nil, nil, fmt.Errorf("service name is required")
	}
	if config.Namespace == "" {
		return nil, nil, fmt.Errorf("namespace is required")
	}
	caBlock, _ := pem.Decode(config.CACertPEM)
	if caBlock == nil {
		return nil, nil, fmt.Errorf("decode ca certificate")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse ca certificate: %w", err)
	}
	caKeyBlock, _ := pem.Decode(config.CAKeyPEM)
	if caKeyBlock == nil {
		return nil, nil, fmt.Errorf("decode ca key")
	}
	caKey, err := x509.ParsePKCS1PrivateKey(caKeyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse ca key: %w", err)
	}
	now := time.Now
	if config.Now != nil {
		now = config.Now
	}
	issuedAt := now()
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("generate leaf key: %w", err)
	}
	dnsNames := ServiceDNSNames(config.ServiceName, config.Namespace)
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: dnsNames[0]},
		DNSNames:     dnsNames,
		NotBefore:    issuedAt.Add(-time.Minute),
		NotAfter:     issuedAt.Add(leafValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create leaf certificate: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)}), nil
}
