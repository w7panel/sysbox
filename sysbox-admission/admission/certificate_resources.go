package admission

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

type certificateResources struct {
	caCertPEM  []byte
	caKeyPEM   []byte
	tlsCertPEM []byte
	tlsKeyPEM  []byte
}

func (m *LifecycleManager) ensureCertificateResources(ctx context.Context) (certificateResources, error) {
	current, ok, err := m.readCertificateResources(ctx)
	if err != nil {
		return certificateResources{}, err
	}
	if !ok {
		return m.rotateCertificateBundle(ctx)
	}
	action, err := EvaluateCertificateHealth(current.caCertPEM, current.caKeyPEM, current.tlsCertPEM, current.tlsKeyPEM, CertificateHealthConfig{
		ServiceName:   m.config.ServiceName,
		Namespace:     m.config.Namespace,
		RenewalWindow: m.config.RenewalWindow,
		Now:           m.config.Now,
	})
	if err != nil {
		return certificateResources{}, fmt.Errorf("evaluate certificate health: %w", err)
	}
	switch action {
	case CertificateHealthy:
		return current, nil
	case RotateLeafCertificate:
		if _, err := parseRSAKey(current.caKeyPEM); err != nil {
			return m.rotateCertificateBundle(ctx)
		}
		return m.rotateLeafCertificate(ctx, current.caCertPEM, current.caKeyPEM)
	case RotateCertificateBundle:
		return m.rotateCertificateBundle(ctx)
	default:
		return certificateResources{}, fmt.Errorf("unknown certificate health action: %s", action)
	}
}

func (m *LifecycleManager) readCertificateResources(ctx context.Context) (certificateResources, bool, error) {
	secrets := m.clients.Secrets
	caSecret, err := secrets.Get(ctx, m.config.CASecretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return certificateResources{}, false, nil
	}
	if err != nil {
		return certificateResources{}, false, fmt.Errorf("get ca secret: %w", err)
	}
	tlsSecret, err := secrets.Get(ctx, m.config.TLSSecretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return certificateResources{
			caCertPEM: caSecret.Data[CASecretCertKey],
			caKeyPEM:  caSecret.Data[CASecretKeyKey],
		}, true, nil
	}
	if err != nil {
		return certificateResources{}, false, fmt.Errorf("get tls secret: %w", err)
	}
	return certificateResources{
		caCertPEM:  caSecret.Data[CASecretCertKey],
		caKeyPEM:   caSecret.Data[CASecretKeyKey],
		tlsCertPEM: tlsSecret.Data[corev1.TLSCertKey],
		tlsKeyPEM:  tlsSecret.Data[corev1.TLSPrivateKeyKey],
	}, true, nil
}

func (m *LifecycleManager) rotateCertificateBundle(ctx context.Context) (certificateResources, error) {
	bundle, err := GenerateCertificateBundle(CertificateConfig{
		ServiceName: m.config.ServiceName,
		Namespace:   m.config.Namespace,
		Now:         m.config.Now,
	})
	if err != nil {
		return certificateResources{}, fmt.Errorf("generate webhook certificate bundle: %w", err)
	}
	caSecret, err := m.applyCASecret(ctx, bundle.CACertPEM, bundle.CAKeyPEM)
	if err != nil {
		return certificateResources{}, err
	}
	tlsCertPEM, tlsKeyPEM, err := GenerateTLSCertificate(TLSCertificateConfig{
		ServiceName: m.config.ServiceName,
		Namespace:   m.config.Namespace,
		CACertPEM:   caSecret.Data[CASecretCertKey],
		CAKeyPEM:    caSecret.Data[CASecretKeyKey],
		Now:         m.config.Now,
	})
	if err != nil {
		return certificateResources{}, fmt.Errorf("generate tls secret: %w", err)
	}
	tlsSecret, err := m.applyTLSSecret(ctx, tlsCertPEM, tlsKeyPEM)
	if err != nil {
		return certificateResources{}, err
	}
	return certificateResources{
		caCertPEM:  caSecret.Data[CASecretCertKey],
		caKeyPEM:   caSecret.Data[CASecretKeyKey],
		tlsCertPEM: tlsSecret.Data[corev1.TLSCertKey],
		tlsKeyPEM:  tlsSecret.Data[corev1.TLSPrivateKeyKey],
	}, nil
}

func (m *LifecycleManager) rotateLeafCertificate(ctx context.Context, caCertPEM, caKeyPEM []byte) (certificateResources, error) {
	tlsCertPEM, tlsKeyPEM, err := GenerateTLSCertificate(TLSCertificateConfig{
		ServiceName: m.config.ServiceName,
		Namespace:   m.config.Namespace,
		CACertPEM:   caCertPEM,
		CAKeyPEM:    caKeyPEM,
		Now:         m.config.Now,
	})
	if err != nil {
		return certificateResources{}, fmt.Errorf("generate tls secret: %w", err)
	}
	tlsSecret, err := m.applyTLSSecret(ctx, tlsCertPEM, tlsKeyPEM)
	if err != nil {
		return certificateResources{}, err
	}
	return certificateResources{
		caCertPEM:  caCertPEM,
		caKeyPEM:   caKeyPEM,
		tlsCertPEM: tlsSecret.Data[corev1.TLSCertKey],
		tlsKeyPEM:  tlsSecret.Data[corev1.TLSPrivateKeyKey],
	}, nil
}

func (m *LifecycleManager) applyCASecret(ctx context.Context, caCertPEM, caKeyPEM []byte) (*corev1.Secret, error) {
	secrets := m.clients.Secrets
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: m.config.CASecretName, Namespace: m.config.Namespace},
		Data: map[string][]byte{
			CASecretCertKey: caCertPEM,
			CASecretKeyKey:  caKeyPEM,
		},
	}
	current, err := secrets.Get(ctx, m.config.CASecretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		var created *corev1.Secret
		created, err = secrets.Create(ctx, secret, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			current, err = secrets.Get(ctx, m.config.CASecretName, metav1.GetOptions{})
			if err != nil {
				return nil, fmt.Errorf("get ca secret after create conflict: %w", err)
			}
		} else if err != nil {
			return nil, fmt.Errorf("create ca secret: %w", err)
		} else {
			return created, nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("get ca secret: %w", err)
	}
	var updated *corev1.Secret
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		updatedSecret := current.DeepCopy()
		updatedSecret.Data = secret.Data
		updated, err = secrets.Update(ctx, updatedSecret, metav1.UpdateOptions{})
		if apierrors.IsConflict(err) {
			conflictErr := err
			current, err = secrets.Get(ctx, m.config.CASecretName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("get ca secret after update conflict: %w", err)
			}
			return conflictErr
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("update ca secret: %w", err)
	}
	return updated, nil
}

func (m *LifecycleManager) applyTLSSecret(ctx context.Context, tlsCertPEM, tlsKeyPEM []byte) (*corev1.Secret, error) {
	secrets := m.clients.Secrets
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: m.config.TLSSecretName, Namespace: m.config.Namespace},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       tlsCertPEM,
			corev1.TLSPrivateKeyKey: tlsKeyPEM,
		},
	}
	current, err := secrets.Get(ctx, m.config.TLSSecretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		var created *corev1.Secret
		created, err = secrets.Create(ctx, secret, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			current, err = secrets.Get(ctx, m.config.TLSSecretName, metav1.GetOptions{})
			if err != nil {
				return nil, fmt.Errorf("get tls secret after create conflict: %w", err)
			}
		} else if err != nil {
			return nil, fmt.Errorf("create tls secret: %w", err)
		} else {
			return created, nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("get tls secret: %w", err)
	}
	var updated *corev1.Secret
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		updatedSecret := current.DeepCopy()
		updatedSecret.Type = corev1.SecretTypeTLS
		updatedSecret.Data = secret.Data
		updated, err = secrets.Update(ctx, updatedSecret, metav1.UpdateOptions{})
		if apierrors.IsConflict(err) {
			conflictErr := err
			current, err = secrets.Get(ctx, m.config.TLSSecretName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("get tls secret after update conflict: %w", err)
			}
			return conflictErr
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("update tls secret: %w", err)
	}
	return updated, nil
}
