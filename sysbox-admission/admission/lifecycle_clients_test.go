package admission

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/fake"
)

func TestLifecycleClients_acceptsTypedResourceInterfaces_whenConstructedFromClientset(t *testing.T) {
	// Given: a test clientset exposing the same typed resources production wiring uses.
	client := fake.NewSimpleClientset()
	clients := LifecycleClients{
		Secrets:                       client.CoreV1().Secrets("sysbox-system"),
		Leases:                        client.CoordinationV1().Leases("sysbox-system"),
		LeaseClient:                   client.CoordinationV1(),
		MutatingWebhookConfigurations: client.AdmissionregistrationV1().MutatingWebhookConfigurations(),
	}

	// When: lifecycle helpers are built from typed resources instead of the aggregate clientset.
	manager := NewLifecycleManager(clients, LifecycleConfig{Namespace: "sysbox-system"})
	lock := NewLeaseLock(clients.LeaseClient, LifecycleConfig{Namespace: "sysbox-system", LeaseName: "sysbox-admission-lifecycle"}, "pod-a")
	refreshConfig := TLSCertificateRefreshConfig{Secrets: clients.Secrets, Secret: "sysbox-admission-tls"}

	// Then: all public lifecycle entry points accept the narrow interfaces needed for their work.
	require.NotNil(t, manager)
	require.NotNil(t, lock)
	require.NotNil(t, refreshConfig.Secrets)
	require.Equal(t, "sysbox-admission-tls", refreshConfig.Secret)
}
