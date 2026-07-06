package admission

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var ErrTLSCertificateNotLoaded = errors.New("tls certificate not loaded")

type TLSCertificateRefreshConfig struct {
	Client       kubernetes.Interface
	Namespace    string
	Secret       string
	Interval     time.Duration
	Reloader     *CertificateReloader
	ErrorHandler func(error)
}

type CertificateReloader struct {
	certificate atomic.Pointer[tls.Certificate]
}

func NewCertificateReloader(initial tls.Certificate) *CertificateReloader {
	reloader := &CertificateReloader{}
	reloader.SetCertificate(initial)
	return reloader
}

func NewEmptyCertificateReloader() *CertificateReloader {
	return &CertificateReloader{}
}

func (r *CertificateReloader) SetCertificate(certificate tls.Certificate) {
	stored := cloneCertificate(certificate)
	r.certificate.Store(&stored)
}

func (r *CertificateReloader) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	stored := r.certificate.Load()
	if stored == nil {
		return nil, ErrTLSCertificateNotLoaded
	}
	certificate := cloneCertificate(*stored)
	return &certificate, nil
}

func LoadTLSCertificateFromSecret(ctx context.Context, client kubernetes.Interface, namespace, secretName string) (tls.Certificate, error) {
	secret, err := client.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("get tls secret: %w", err)
	}
	certificate, err := tls.X509KeyPair(secret.Data[corev1.TLSCertKey], secret.Data[corev1.TLSPrivateKeyKey])
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load tls secret key pair: %w", err)
	}
	return certificate, nil
}

func RefreshTLSCertificateFromSecret(ctx context.Context, config TLSCertificateRefreshConfig) error {
	certificate, err := LoadTLSCertificateFromSecret(ctx, config.Client, config.Namespace, config.Secret)
	if err != nil {
		return err
	}
	config.Reloader.SetCertificate(certificate)
	return nil
}

func WaitForTLSCertificateFromSecret(ctx context.Context, config TLSCertificateRefreshConfig) error {
	ticker := time.NewTicker(config.Interval)
	defer ticker.Stop()
	for {
		if err := RefreshTLSCertificateFromSecret(ctx, config); err == nil {
			return nil
		} else {
			reportTLSCertificateRefreshError(config, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func RunTLSCertificateRefreshLoop(ctx context.Context, config TLSCertificateRefreshConfig) error {
	ticker := time.NewTicker(config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := RefreshTLSCertificateFromSecret(ctx, config); err != nil {
				reportTLSCertificateRefreshError(config, err)
			}
		}
	}
}

func reportTLSCertificateRefreshError(config TLSCertificateRefreshConfig, err error) {
	if config.ErrorHandler != nil {
		config.ErrorHandler(err)
	}
}

func cloneCertificate(certificate tls.Certificate) tls.Certificate {
	return tls.Certificate{
		Certificate:                  cloneByteSlices(certificate.Certificate),
		PrivateKey:                   certificate.PrivateKey,
		SupportedSignatureAlgorithms: append([]tls.SignatureScheme(nil), certificate.SupportedSignatureAlgorithms...),
		OCSPStaple:                   cloneBytes(certificate.OCSPStaple),
		SignedCertificateTimestamps:  cloneByteSlices(certificate.SignedCertificateTimestamps),
		Leaf:                         cloneLeaf(certificate.Leaf),
	}
}

func cloneByteSlices(values [][]byte) [][]byte {
	clones := make([][]byte, len(values))
	for index, value := range values {
		clones[index] = cloneBytes(value)
	}
	return clones
}

func cloneBytes(value []byte) []byte {
	return append([]byte(nil), value...)
}

func cloneLeaf(leaf *x509.Certificate) *x509.Certificate {
	if leaf == nil {
		return nil
	}
	clone := *leaf
	return &clone
}
