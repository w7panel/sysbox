package admission

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

const (
	CASecretCertKey = "ca.crt"
	CASecretKeyKey  = "ca.key"
)

type LifecycleConfig struct {
	Namespace     string
	ServiceName   string
	ServicePort   int32
	CASecretName  string
	TLSSecretName string
	LeaseName     string
	WebhookName   string
	RenewalWindow time.Duration
	Now           func() time.Time
}

type LifecycleResult struct {
	TLSCertificate tls.Certificate
}

type LifecycleManager struct {
	client kubernetes.Interface
	config LifecycleConfig
}

func NewLifecycleManager(client kubernetes.Interface, config LifecycleConfig) *LifecycleManager {
	return &LifecycleManager{client: client, config: config}
}

func (m *LifecycleManager) Ensure(ctx context.Context) (LifecycleResult, error) {
	if err := m.ensureLease(ctx); err != nil {
		return LifecycleResult{}, err
	}
	certificates, err := m.ensureCertificateResources(ctx)
	if err != nil {
		return LifecycleResult{}, err
	}
	if err := m.ensureWebhook(ctx, certificates.caCertPEM); err != nil {
		return LifecycleResult{}, err
	}
	tlsCertificate, err := tls.X509KeyPair(certificates.tlsCertPEM, certificates.tlsKeyPEM)
	if err != nil {
		return LifecycleResult{}, fmt.Errorf("load tls secret key pair: %w", err)
	}
	return LifecycleResult{TLSCertificate: tlsCertificate}, nil
}

func (m *LifecycleManager) ensureLease(ctx context.Context) error {
	leases := m.client.CoordinationV1().Leases(m.config.Namespace)
	_, err := leases.Get(ctx, m.config.LeaseName, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get lifecycle lease: %w", err)
	}
	_, err = leases.Create(ctx, &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: m.config.LeaseName, Namespace: m.config.Namespace},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create lifecycle lease: %w", err)
	}
	return nil
}

func (m *LifecycleManager) ensureWebhook(ctx context.Context, caBundle []byte) error {
	webhook, err := BuildMutatingWebhookConfiguration(WebhookConfig{
		Name:        m.config.WebhookName,
		ServiceName: m.config.ServiceName,
		Namespace:   m.config.Namespace,
		ServicePort: m.config.ServicePort,
		CABundle:    caBundle,
	})
	if err != nil {
		return fmt.Errorf("build mutating webhook configuration: %w", err)
	}
	webhooks := m.client.AdmissionregistrationV1().MutatingWebhookConfigurations()
	current, err := webhooks.Get(ctx, m.config.WebhookName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = webhooks.Create(ctx, webhook, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			current, err = webhooks.Get(ctx, m.config.WebhookName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("get mutating webhook configuration after create conflict: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("create mutating webhook configuration: %w", err)
		} else {
			return nil
		}
	}
	if err != nil {
		return fmt.Errorf("get mutating webhook configuration: %w", err)
	}
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		webhook.ResourceVersion = current.ResourceVersion
		_, err = webhooks.Update(ctx, webhook, metav1.UpdateOptions{})
		if apierrors.IsConflict(err) {
			conflictErr := err
			current, err = webhooks.Get(ctx, m.config.WebhookName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("get mutating webhook configuration after update conflict: %w", err)
			}
			return conflictErr
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("update mutating webhook configuration: %w", err)
	}
	return nil
}
