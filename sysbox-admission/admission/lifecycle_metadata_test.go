package admission

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestLifecycleManager_Ensure_preservesWebhookMetadata_whenUpdatingCABundle(t *testing.T) {
	// Given: an existing webhook with installer-owned metadata and a stale caBundle.
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	initialBundle, err := GenerateCertificateBundle(CertificateConfig{ServiceName: "sysbox-admission", Namespace: "sysbox-system"})
	require.NoError(t, err)
	_, err = client.CoreV1().Secrets("sysbox-system").Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "sysbox-admission-ca", Namespace: "sysbox-system"},
		Data: map[string][]byte{
			CASecretCertKey: initialBundle.CACertPEM,
			CASecretKeyKey:  initialBundle.CAKeyPEM,
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	staleWebhook, err := BuildMutatingWebhookConfiguration(WebhookConfig{
		Name:        WebhookName,
		ServiceName: "sysbox-admission",
		Namespace:   "sysbox-system",
		ServicePort: 443,
		CABundle:    []byte("stale"),
	})
	require.NoError(t, err)
	staleWebhook.Labels = map[string]string{"app.kubernetes.io/managed-by": "helm"}
	staleWebhook.Annotations = map[string]string{"example.com/keep": "true"}
	staleWebhook.Finalizers = []string{"example.com/finalizer"}
	_, err = client.AdmissionregistrationV1().MutatingWebhookConfigurations().Create(ctx, staleWebhook, metav1.CreateOptions{})
	require.NoError(t, err)
	manager := newTestLifecycleManager(client, LifecycleConfig{
		Namespace:     "sysbox-system",
		ServiceName:   "sysbox-admission",
		ServicePort:   443,
		CASecretName:  "sysbox-admission-ca",
		TLSSecretName: "sysbox-admission-tls",
		LeaseName:     "sysbox-admission-init",
		WebhookName:   WebhookName,
	})

	// When: lifecycle reconciliation updates only the webhook caBundle.
	_, err = manager.Ensure(ctx)

	// Then: installer-owned metadata remains intact.
	require.NoError(t, err)
	webhook, err := client.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, WebhookName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, initialBundle.CACertPEM, webhook.Webhooks[0].ClientConfig.CABundle)
	require.Equal(t, map[string]string{"app.kubernetes.io/managed-by": "helm"}, webhook.Labels)
	require.Equal(t, map[string]string{"example.com/keep": "true"}, webhook.Annotations)
	require.Equal(t, []string{"example.com/finalizer"}, webhook.Finalizers)
}
